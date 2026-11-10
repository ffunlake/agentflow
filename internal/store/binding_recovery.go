package store

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// BindingRecovery is a durable receipt for a logical seat, independent of pane
// order. Journal-only records also cover launches that have not registered READY.
type BindingRecovery struct {
	AgentID       string      `json:"agent_id"`
	Generation    int64       `json:"generation"`
	RequestID     string      `json:"request_id"`
	Previous      Seat        `json:"previous"`
	Current       Seat        `json:"current"`
	ConfigDigest  string      `json:"config_digest"`
	State         string      `json:"state"`
	Reason        string      `json:"reason"`
	Attempts      int         `json:"attempts"`
	Policy        RetryPolicy `json:"policy"`
	NextAttemptAt *time.Time  `json:"next_attempt_at"`
	JournalSeq    int64       `json:"journal_seq"`
	RecordedAt    time.Time   `json:"recorded_at"`
}

// RecordStartupLaunch commits before Launch; a response lost before LAUNCH_BOUND
// is permanently ambiguous and must never be interpreted as an unstarted seat.
func (s *Store) RecordStartupLaunch(ctx context.Context, seat Seat, bound bool) error {
	event := "LAUNCH_REQUESTED"
	if bound {
		event = "LAUNCH_BOUND"
	}
	data, err := json.Marshal(seat)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var stopped bool
	if err := tx.QueryRowContext(ctx, `SELECT stop_requested FROM runs WHERE run_id = ?`, seat.RunID).Scan(&stopped); err != nil {
		return err
	}
	if stopped {
		return messagingError("STOP_REQUESTED", "startup", "run has a stop intent")
	}
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM journal WHERE run_id = ? AND event = ? AND json_extract(details, '$.AgentID') = ?)`, seat.RunID, event, seat.AgentID).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return ErrIdempotencyConflict
	}
	if _, err := appendJournal(ctx, tx, JournalEntry{RunID: seat.RunID, Event: event, Details: string(data)}); err != nil {
		return err
	}
	return tx.Commit()
}

func recoverySeat(ctx context.Context, tx queryRower, runID, agentID string) (Seat, error) {
	var seat Seat
	var created string
	err := tx.QueryRowContext(ctx, `SELECT run_id, agent_id, generation, is_current, tool, native_session_id, workspace_id, surface_id, status, COALESCE(current_message_id, ''), created_at FROM seats WHERE run_id = ? AND agent_id = ? AND is_current = 1`, runID, agentID).Scan(&seat.RunID, &seat.AgentID, &seat.Generation, &seat.Current, &seat.Tool, &seat.NativeSessionID, &seat.WorkspaceID, &seat.SurfaceID, &seat.Status, &seat.CurrentMessageID, &created)
	if err == nil {
		seat.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
		return seat, err
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return seat, err
	}
	var data string
	err = tx.QueryRowContext(ctx, `SELECT details FROM journal WHERE run_id = ? AND event IN ('LAUNCH_REQUESTED','LAUNCH_BOUND') AND json_extract(details, '$.AgentID') = ? ORDER BY seq DESC LIMIT 1`, runID, agentID).Scan(&data)
	if err != nil {
		return seat, err
	}
	err = json.Unmarshal([]byte(data), &seat)
	return seat, err
}

func (s *Store) RecoverySeat(ctx context.Context, runID, agentID string) (Seat, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return Seat{}, err
	}
	defer tx.Rollback()
	seat, err := recoverySeat(ctx, tx, runID, agentID)
	if err != nil {
		return seat, err
	}
	return seat, tx.Commit()
}

// RegisterStartupReady prevents a late in-memory startup registry from reviving
// a closed or recovering partial launch. It commits the first seat only.
func (s *Store) RegisterStartupReady(ctx context.Context, seat Seat) error {
	return s.registerStartupReady(ctx, seat, "")
}

const StartupReadyVerified = "STARTUP_READY_VERIFIED"

type StartupReadyProof struct {
	Seat  Seat   `json:"seat"`
	Nonce string `json:"nonce"`
}

// RegisterStartupReadyWithNonce atomically records the verified native challenge
// and the current seat. The trusted foreground adapter has already matched it.
func (s *Store) RegisterStartupReadyWithNonce(ctx context.Context, seat Seat, nonce string) error {
	decoded, err := hex.DecodeString(nonce)
	if err != nil || len(decoded) != 24 || nonce != strings.ToLower(nonce) {
		return messagingError("INVALID_REQUEST", "startup.nonce", "native READY requires a 24-byte lowercase hex nonce")
	}
	return s.registerStartupReady(ctx, seat, nonce)
}

func (s *Store) registerStartupReady(ctx context.Context, seat Seat, nonce string) error {
	if seat.CreatedAt.IsZero() {
		seat.CreatedAt = time.Now().UTC()
	}
	seat.Current = true
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if gate, err := bindingGate(ctx, tx, seat, true); err != nil {
		return err
	} else if gate != "" {
		return messagingError(gate, "startup.ready", "binding recovery or stop blocks registration")
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO seats (run_id,agent_id,generation,is_current,tool,native_session_id,workspace_id,surface_id,status,created_at) VALUES (?,?,?,1,?,?,?,?,?,?)`, seat.RunID, seat.AgentID, seat.Generation, seat.Tool, seat.NativeSessionID, seat.WorkspaceID, seat.SurfaceID, seat.Status, timestamp(seat.CreatedAt)); err != nil {
		return err
	}
	if nonce != "" {
		data, err := json.Marshal(StartupReadyProof{Seat: seat, Nonce: nonce})
		if err != nil {
			return err
		}
		if _, err := appendJournal(ctx, tx, JournalEntry{RunID: seat.RunID, AgentID: seat.AgentID, Generation: seat.Generation, Event: StartupReadyVerified, Details: string(data)}); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// bindingGate is shared by S5 and S7.1. A user close or ambiguous exit cannot be
// bypassed by invoking assignment recovery instead of binding recovery.
func bindingGate(ctx context.Context, tx queryRower, seat Seat, includeReservation bool) (string, error) {
	var stopped bool
	if err := tx.QueryRowContext(ctx, `SELECT stop_requested FROM runs WHERE run_id = ?`, seat.RunID).Scan(&stopped); err != nil {
		return "", err
	}
	if stopped {
		return "STOP_REQUESTED", nil
	}
	if seat.Status == "CLOSED" || seat.Status == "STOPPED" || seat.Status == "DONE" || seat.Status == "FAILED" {
		return "SEAT_TERMINAL", nil
	}
	var reason string
	err := tx.QueryRowContext(ctx, `SELECT json_extract(details, '$.reason') FROM journal WHERE run_id = ? AND event = 'BINDING_EXIT' AND json_extract(details, '$.agent_id') = ? AND json_extract(details, '$.generation') = ? ORDER BY seq DESC LIMIT 1`, seat.RunID, seat.AgentID, seat.Generation).Scan(&reason)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	if reason != "" {
		return reason, nil
	}
	if includeReservation {
		var state string
		err = tx.QueryRowContext(ctx, `SELECT json_extract(details, '$.state') FROM journal WHERE run_id = ? AND event = 'BINDING_RECOVERY_DECIDED' AND json_extract(details, '$.agent_id') = ? ORDER BY seq DESC LIMIT 1`, seat.RunID, seat.AgentID).Scan(&state)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return "", err
		}
		if state == "WAITING" || state == "ATTEMPT_RESERVED" || state == "CREATE_REQUESTED" || state == "MANUAL_RECONCILE" {
			return "BINDING_RECOVERY_UNVERIFIED", nil
		}
	}
	return "", nil
}

func bindingLedgerGate(ctx context.Context, tx *sql.Tx, seat Seat) (string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id, sender, sender_generation FROM messages WHERE run_id = ? AND recipient = ?`, seat.RunID, seat.AgentID)
	if err != nil {
		return "", err
	}
	type work struct {
		id    string
		actor MessageActor
	}
	var pending []work
	for rows.Next() {
		w := work{actor: MessageActor{RunID: seat.RunID}}
		if err := rows.Scan(&w.id, &w.actor.AgentID, &w.actor.Generation); err != nil {
			rows.Close()
			return "", err
		}
		pending = append(pending, w)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return "", err
	}
	for _, w := range pending {
		// Reuse S5.3's complete journal/delivery consistency checks without
		// relocating any work. Stale sender authority requires manual review.
		r, err := reconcileSessionWithRelocation(ctx, tx, w.actor, w.id, false)
		if err != nil {
			var identity *MessagingError
			if errors.As(err, &identity) {
				return "MESSAGE_ACTOR_NEEDS_RECONCILE", nil
			}
			return "", err
		}
		if r.State == "UNKNOWN_NEEDS_RECONCILE" {
			return "NATIVE_ACTION_OUTCOME_UNKNOWN", nil
		}
		retry, err := latestRetry(ctx, tx, w.id)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return "", err
		}
		switch retry.State {
		case "EXHAUSTED":
			return "MESSAGE_RECOVERY_EXHAUSTED", nil
		case "STOPPED":
			return "MESSAGE_RECOVERY_STOPPED", nil
		case "MANUAL_RECONCILE":
			return "MESSAGE_RECOVERY_NEEDS_RECONCILE", nil
		case "ATTEMPT_RESERVED":
			return "MESSAGE_RECOVERY_IN_FLIGHT", nil
		}
	}
	return "", nil
}

func latestBindingRecovery(ctx context.Context, tx *sql.Tx, runID, agentID, request string) (BindingRecovery, error) {
	var r BindingRecovery
	var data, created string
	err := tx.QueryRowContext(ctx, `SELECT seq, details, created_at FROM journal WHERE run_id = ? AND event = 'BINDING_RECOVERY_DECIDED' AND json_extract(details, '$.agent_id') = ? AND json_extract(details, '$.request_id') = ? ORDER BY seq DESC LIMIT 1`, runID, agentID, request).Scan(&r.JournalSeq, &data, &created)
	if err != nil {
		return r, err
	}
	seq := r.JournalSeq
	if err := json.Unmarshal([]byte(data), &r); err != nil {
		return r, err
	}
	r.JournalSeq = seq
	r.RecordedAt, err = time.Parse(time.RFC3339Nano, created)
	return r, err
}

func saveBindingRecovery(ctx context.Context, tx *sql.Tx, r BindingRecovery, now time.Time) (BindingRecovery, error) {
	r.JournalSeq, r.RecordedAt = 0, time.Time{}
	data, err := json.Marshal(r)
	if err != nil {
		return r, err
	}
	r.JournalSeq, err = appendJournal(ctx, tx, JournalEntry{RunID: r.Previous.RunID, Event: "BINDING_RECOVERY_DECIDED", Details: string(data), CreatedAt: now})
	r.RecordedAt = now.UTC()
	return r, err
}

// ReserveBindingRecovery uses the same finite policy as S7.1. The budget is per
// logical agent/generation, including different request IDs and process restarts.
func (s *Store) ReserveBindingRecovery(ctx context.Context, actor MessageActor, target MessageActor, request string, policy RetryPolicy, now time.Time, cancelled bool) (BindingRecovery, bool, error) {
	if policy.Validate() != nil || now.IsZero() || strings.TrimSpace(request) == "" || target.Generation < 1 {
		return BindingRecovery{}, false, messagingError("INVALID_REQUEST", "binding.recovery", "finite policy, clock, generation and request required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return BindingRecovery{}, false, err
	}
	defer tx.Rollback()
	run, err := scanRun(tx.QueryRowContext(ctx, selectRun))
	if err != nil {
		return BindingRecovery{}, false, err
	}
	var frozen struct {
		Lead string `json:"lead"`
	}
	if err := json.Unmarshal([]byte(run.ConfigSnapshot), &frozen); err != nil {
		return BindingRecovery{}, false, err
	}
	if target.RunID != actor.RunID || (actor.AgentID != frozen.Lead && actor.AgentID != target.AgentID) {
		return BindingRecovery{}, false, messagingError("ACTOR_MISMATCH", "binding.recovery", "only lead or seat owner may recover a binding")
	}
	if err := validateMessageActor(ctx, tx, actor); err != nil {
		// Trusted startup coordination may precede the lead's first READY.
		// Existing historical/current lead seats prohibit this exception.
		var leadExists bool
		if e := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM seats WHERE run_id = ? AND agent_id = ?)`, run.RunID, frozen.Lead).Scan(&leadExists); e != nil {
			return BindingRecovery{}, false, e
		}
		if actor.RunID != run.RunID || actor.AgentID != frozen.Lead || actor.Generation != 1 || leadExists {
			return BindingRecovery{}, false, err
		}
	}
	previous, err := latestBindingRecovery(ctx, tx, target.RunID, target.AgentID, request)
	fresh := errors.Is(err, sql.ErrNoRows)
	if err != nil && !fresh {
		return previous, false, err
	}
	if !fresh && previous.Generation != target.Generation {
		return previous, false, ErrIdempotencyConflict
	}
	seat, err := recoverySeat(ctx, tx, target.RunID, target.AgentID)
	if err != nil {
		return previous, false, err
	}
	r := previous
	if fresh {
		r = BindingRecovery{AgentID: target.AgentID, Generation: target.Generation, RequestID: request, Previous: seat, Current: seat, ConfigDigest: run.ConfigDigest, Policy: policy}
		// Freeze the first policy and retain spent opportunities across incidents.
		var data string
		var seq int64
		var created string
		err = tx.QueryRowContext(ctx, `SELECT seq, details, created_at FROM journal WHERE run_id = ? AND event = 'BINDING_RECOVERY_DECIDED' AND json_extract(details, '$.agent_id') = ? AND json_extract(details, '$.generation') = ? ORDER BY seq DESC LIMIT 1`, target.RunID, target.AgentID, target.Generation).Scan(&seq, &data, &created)
		if err == nil {
			var last BindingRecovery
			if err := json.Unmarshal([]byte(data), &last); err != nil {
				return r, false, err
			}
			last.JournalSeq = seq
			last.RecordedAt, err = time.Parse(time.RFC3339Nano, created)
			if err != nil {
				return r, false, err
			}
			r.Policy, r.Attempts = last.Policy, last.Attempts
			if last.State == "WAITING" || last.State == "ATTEMPT_RESERVED" || last.State == "CREATE_REQUESTED" || last.State == "MANUAL_RECONCILE" {
				return last, false, tx.Commit()
			}
			if last.NextAttemptAt != nil {
				r.NextAttemptAt = last.NextAttemptAt
			}
		} else if !errors.Is(err, sql.ErrNoRows) {
			return r, false, err
		}
	}
	reason, err := bindingGate(ctx, tx, seat, false)
	if err != nil {
		return r, false, err
	}
	state := "STOPPED"
	if reason == "EXIT_UNVERIFIED" {
		state = "MANUAL_RECONCILE"
	}
	if reason == "" && !fresh && (previous.State == "COMPLETE" || previous.State == "STOPPED" || previous.State == "EXHAUSTED" || previous.State == "MANUAL_RECONCILE" || previous.State == "ATTEMPT_RESERVED" || previous.State == "CREATE_REQUESTED") {
		return previous, false, tx.Commit()
	}
	if reason == "" && seat.Generation != target.Generation {
		reason = "REJECTED_OLD_GENERATION"
	}
	if reason == "" && cancelled {
		reason = "CONTEXT_CANCELLED"
	}
	if reason == "" {
		reason, err = bindingLedgerGate(ctx, tx, seat)
		if reason != "" {
			state = "MANUAL_RECONCILE"
		}
	}
	if err != nil {
		return r, false, err
	}
	if reason == "" && seat.NativeSessionID == "" {
		reason, state = "PARTIAL_LAUNCH_UNVERIFIED", "MANUAL_RECONCILE"
	}
	claimed := false
	if reason != "" {
		r.State, r.Reason, r.NextAttemptAt = state, reason, nil
	} else if r.Attempts >= r.Policy.MaxAttempts {
		r.State, r.Reason, r.NextAttemptAt = "EXHAUSTED", "RECOVERY_BUDGET_EXHAUSTED", nil
	} else if fresh {
		r.State, r.Reason = "WAITING", "CHECK_ORIGINAL_SESSION_FIRST"
		if r.NextAttemptAt == nil {
			next := now.UTC().Add(r.Policy.Backoff[r.Attempts])
			r.NextAttemptAt = &next
		}
	} else if r.NextAttemptAt != nil && now.Before(*r.NextAttemptAt) {
		return previous, false, tx.Commit()
	} else {
		r.State, r.Reason, r.NextAttemptAt = "ATTEMPT_RESERVED", "ORIGINAL_CHECK_IN_FLIGHT_OR_INTERRUPTED", nil
		r.Attempts++
		claimed = true
	}
	if !fresh && r.State == previous.State && r.Reason == previous.Reason {
		return previous, false, tx.Commit()
	}
	r, err = saveBindingRecovery(ctx, tx, r, now)
	if err != nil {
		return r, false, err
	}
	return r, claimed, tx.Commit()
}

// FinishBindingRecovery is also the CAS boundary before creating a replacement.
// It rechecks stop, exit, current binding, unknown delivery and competing native
// intents. CREATE_REQUESTED survives a crash and is never reclaimed.
func (s *Store) FinishBindingRecovery(ctx context.Context, reserved BindingRecovery, state, reason string, next *Seat, now time.Time) (BindingRecovery, error) {
	if state != "COMPLETE" && state != "CREATE_REQUESTED" && state != "MANUAL_RECONCILE" && state != "STOPPED" && state != "WAITING" {
		return reserved, messagingError("INVALID_REQUEST", "binding.state", "invalid recovery outcome")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return reserved, err
	}
	defer tx.Rollback()
	r, err := latestBindingRecovery(ctx, tx, reserved.Previous.RunID, reserved.AgentID, reserved.RequestID)
	if err != nil {
		return r, err
	}
	if r.JournalSeq != reserved.JournalSeq || (r.State != "ATTEMPT_RESERVED" && r.State != "CREATE_REQUESTED") {
		return r, tx.Commit()
	}
	seat, err := recoverySeat(ctx, tx, r.Previous.RunID, r.AgentID)
	if err != nil {
		return r, err
	}
	gate, err := bindingGate(ctx, tx, seat, false)
	if err != nil {
		return r, err
	}
	if gate != "" {
		state, reason, next = "STOPPED", gate, nil
		if gate == "EXIT_UNVERIFIED" {
			state = "MANUAL_RECONCILE"
		}
	}
	if !sameSession(seat, r.Previous) {
		state, reason, next = "MANUAL_RECONCILE", "BINDING_CHANGED_DURING_RECOVERY", nil
	}
	if gate, err := bindingLedgerGate(ctx, tx, seat); err != nil {
		return r, err
	} else if gate != "" {
		state, reason, next = "MANUAL_RECONCILE", gate, nil
	}
	if (state == "CREATE_REQUESTED" || (next != nil && next.Generation != seat.Generation)) && seat.CurrentMessageID != "" {
		state, reason, next = "MANUAL_RECONCILE", "CURRENT_WORK_NEEDS_RECONCILE", nil
	}
	if next != nil && state == "COMPLETE" {
		if next.RunID != seat.RunID || next.AgentID != seat.AgentID || next.Tool != seat.Tool || next.NativeSessionID == "" || next.WorkspaceID == "" || next.SurfaceID == "" ||
			(next.Generation != seat.Generation && next.Generation != seat.Generation+1) ||
			(next.Generation == seat.Generation && next.NativeSessionID != seat.NativeSessionID) ||
			(next.Generation != seat.Generation && (r.State != "CREATE_REQUESTED" || next.NativeSessionID == seat.NativeSessionID)) {
			return r, messagingError("ACTOR_MISMATCH", "binding.ready", "binding transition does not match durable intent")
		}
		var conflict bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM seats WHERE run_id = ? AND NOT (agent_id = ? AND generation = ?) AND ((tool = ? AND native_session_id = ?) OR (is_current = 1 AND workspace_id = ? AND surface_id = ?)))`, seat.RunID, seat.AgentID, seat.Generation, next.Tool, next.NativeSessionID, next.WorkspaceID, next.SurfaceID).Scan(&conflict); err != nil {
			return r, err
		}
		if conflict {
			return r, messagingError("ACTOR_MISMATCH", "binding.native", "native identity is already bound")
		}
		if next.Generation != seat.Generation {
			if _, err := tx.ExecContext(ctx, `UPDATE seats SET is_current = 0 WHERE run_id = ? AND agent_id = ? AND is_current = 1`, seat.RunID, seat.AgentID); err != nil {
				return r, err
			}
		}
		status := seat.Status
		if status == "STARTING" || next.Generation != seat.Generation {
			status = "READY"
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO seats (run_id,agent_id,generation,is_current,tool,native_session_id,workspace_id,surface_id,status,current_message_id,created_at) VALUES (?,?,?,1,?,?,?,?,?,?,?) ON CONFLICT(run_id,agent_id,generation) DO UPDATE SET native_session_id=excluded.native_session_id,workspace_id=excluded.workspace_id,surface_id=excluded.surface_id`, next.RunID, next.AgentID, next.Generation, next.Tool, next.NativeSessionID, next.WorkspaceID, next.SurfaceID, status, nullString(seat.CurrentMessageID), timestamp(now))
		if err != nil {
			return r, err
		}
		r.Current, err = recoverySeat(ctx, tx, next.RunID, next.AgentID)
		if err != nil {
			return r, err
		}
		// Both old and new locations remain in append-only history, even when
		// the native session and generation stay unchanged.
		data, _ := json.Marshal(struct{ Previous, Current Seat }{seat, r.Current})
		if _, err := appendJournal(ctx, tx, JournalEntry{RunID: seat.RunID, AgentID: next.AgentID, Generation: next.Generation, Event: "BINDING_CHANGED", Details: string(data), CreatedAt: now}); err != nil {
			return r, err
		}
	}
	r.State, r.Reason, r.NextAttemptAt = state, reason, nil
	if state == "WAITING" {
		if r.Attempts >= r.Policy.MaxAttempts {
			r.State = "EXHAUSTED"
		} else {
			next := now.UTC().Add(r.Policy.Backoff[r.Attempts])
			r.NextAttemptAt = &next
		}
	}
	r, err = saveBindingRecovery(ctx, tx, r, now)
	if err != nil {
		return r, err
	}
	return r, tx.Commit()
}

// RecordBindingExit requires the exact current native identity. An absent reason
// is unknown, never proof of a crash. User closure is a durable, sticky stop.
func (s *Store) RecordBindingExit(ctx context.Context, binding Seat, reason string, now time.Time) error {
	if reason != "USER_CLOSED" && reason != "EXIT_CONFIRMED" {
		reason = "EXIT_UNVERIFIED"
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	seat, err := recoverySeat(ctx, tx, binding.RunID, binding.AgentID)
	if err != nil {
		return err
	}
	if !sameSession(seat, binding) {
		data, _ := json.Marshal(binding)
		var exists bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM journal WHERE run_id = ? AND event = 'BINDING_EVENT_REJECTED' AND details = ?)`, binding.RunID, string(data)).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			if _, err := appendJournal(ctx, tx, JournalEntry{RunID: binding.RunID, Event: "BINDING_EVENT_REJECTED", Details: string(data), CreatedAt: now}); err != nil {
				return err
			}
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		return messagingError("ACTOR_MISMATCH", "binding.event", "event does not match current native binding")
	}
	var previous string
	err = tx.QueryRowContext(ctx, `SELECT json_extract(details, '$.reason') FROM journal WHERE run_id = ? AND event = 'BINDING_EXIT' AND json_extract(details, '$.agent_id') = ? AND json_extract(details, '$.generation') = ? ORDER BY seq DESC LIMIT 1`, seat.RunID, seat.AgentID, seat.Generation).Scan(&previous)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if previous == "USER_CLOSED" || previous == "EXIT_CONFIRMED" || previous == reason {
		return tx.Commit()
	}
	data, _ := json.Marshal(struct {
		AgentID    string `json:"agent_id"`
		Generation int64  `json:"generation"`
		Reason     string `json:"reason"`
		Binding    Seat   `json:"binding"`
	}{seat.AgentID, seat.Generation, reason, seat})
	if _, err := appendJournal(ctx, tx, JournalEntry{RunID: seat.RunID, Event: "BINDING_EXIT", Details: string(data), CreatedAt: now}); err != nil {
		return err
	}
	if reason == "USER_CLOSED" {
		if _, err := tx.ExecContext(ctx, `UPDATE seats SET status = 'CLOSED' WHERE run_id = ? AND agent_id = ? AND is_current = 1`, seat.RunID, seat.AgentID); err != nil {
			return err
		}
	}
	return tx.Commit()
}
