package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// SurfaceRegistration is launch evidence, independent of native session READY.
// The immutable receipt lives in the existing append-only journal; seats retain
// the current generation and UUIDs used by all actor and delivery gates.
type SurfaceRegistration struct {
	RunID        string `json:"run_id"`
	AgentID      string `json:"agent_id"`
	Generation   int64  `json:"generation"`
	Tool         string `json:"tool"`
	LaunchID     string `json:"launch_id"`
	WorkspaceID  string `json:"workspace_id"`
	WorkspaceRef string `json:"workspace_ref"`
	SurfaceID    string `json:"surface_id"`
	SurfaceRef   string `json:"surface_ref"`
	Status       string `json:"status"`
	NativeReady  string `json:"native_ready"`
}

// RegisterCMUXSurfaces atomically saves every logical agent, including uncertain
// launches. Only an UNVERIFIED receipt from the same launch can be resolved;
// established bindings and native READY are never overwritten.
func (s *Store) RegisterCMUXSurfaces(ctx context.Context, bindings []SurfaceRegistration) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	seen := map[string]bool{}
	for _, b := range bindings {
		if b.LaunchID == "" || b.Generation != 1 || b.AgentID == "" || b.NativeReady != "UNVERIFIED" ||
			(b.Status != "LAUNCHED" && b.Status != "UNVERIFIED") ||
			(b.Status == "LAUNCHED" && (b.SurfaceID == "" || b.SurfaceRef == "" || b.WorkspaceID == "" || b.WorkspaceRef == "")) ||
			seen[b.AgentID] || (b.SurfaceID != "" && seen[strings.ToLower(b.SurfaceID)]) || (b.SurfaceRef != "" && seen[b.SurfaceRef]) {
			return messagingError("INVALID_REQUEST", "cmux.registration", "unique launch identities and surface bindings required")
		}
		seen[b.AgentID] = true
		if b.SurfaceID != "" {
			seen[strings.ToLower(b.SurfaceID)], seen[b.SurfaceRef] = true, true
		}
		if b.SurfaceID != "" {
			var collision bool
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM seats WHERE run_id=? AND is_current=1 AND agent_id!=? AND lower(surface_id)=lower(?))`, b.RunID, b.AgentID, b.SurfaceID).Scan(&collision); err != nil {
				return err
			}
			if collision {
				return messagingError("SURFACE_ALREADY_BOUND", "cmux.registration", "surface belongs to another logical agent")
			}
		}
		var previous string
		err := tx.QueryRowContext(ctx, `SELECT details FROM journal WHERE run_id=? AND agent_id=? AND generation=? AND event='CMUX_SURFACE_REGISTERED' ORDER BY seq DESC LIMIT 1`, b.RunID, b.AgentID, b.Generation).Scan(&previous)
		if err == nil {
			var old SurfaceRegistration
			if json.Unmarshal([]byte(previous), &old) != nil || old.Status != "UNVERIFIED" || old.LaunchID != b.LaunchID || old.Tool != b.Tool {
				return ErrIdempotencyConflict
			}
			if _, err := tx.ExecContext(ctx, `UPDATE seats SET workspace_id=?,surface_id=?,status=? WHERE run_id=? AND agent_id=? AND generation=? AND is_current=1 AND status='UNVERIFIED'`, b.WorkspaceID, b.SurfaceID, b.Status, b.RunID, b.AgentID, b.Generation); err != nil {
				return err
			}
		} else if errors.Is(err, sql.ErrNoRows) {
			if _, err := tx.ExecContext(ctx, `INSERT INTO seats (run_id,agent_id,generation,is_current,tool,workspace_id,surface_id,status,created_at) VALUES (?,?,?,1,?,?,?,?,?)`, b.RunID, b.AgentID, b.Generation, b.Tool, b.WorkspaceID, b.SurfaceID, b.Status, timestamp(time.Time{})); err != nil {
				return err
			}
		} else {
			return err
		}
		body, _ := json.Marshal(b)
		if _, err := appendJournal(ctx, tx, JournalEntry{RunID: b.RunID, AgentID: b.AgentID, Generation: b.Generation, Event: "CMUX_SURFACE_REGISTERED", Details: string(body)}); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE runs SET startup_phase=?,workspace_id=? WHERE run_id=?`, b.Status, b.WorkspaceID, b.RunID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func surfaceBinding(ctx context.Context, tx queryRower, a MessageActor) (SurfaceRegistration, error) {
	if err := validateMessageActor(ctx, tx, a); err != nil {
		return SurfaceRegistration{}, err
	}
	var data string
	err := tx.QueryRowContext(ctx, `SELECT details FROM journal WHERE run_id=? AND agent_id=? AND generation=? AND event='CMUX_SURFACE_REGISTERED' ORDER BY seq DESC LIMIT 1`, a.RunID, a.AgentID, a.Generation).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return SurfaceRegistration{}, messagingError("SURFACE_UNBOUND", "cmux.surface", "no surface registration for current generation")
	}
	if err != nil {
		return SurfaceRegistration{}, err
	}
	var b SurfaceRegistration
	if json.Unmarshal([]byte(data), &b) != nil {
		return b, errors.New("invalid stored surface registration")
	}
	seat, err := recoverySeat(ctx, tx, a.RunID, a.AgentID)
	if err != nil {
		return b, err
	}
	if b.RunID != a.RunID || b.AgentID != a.AgentID || b.Generation != a.Generation || b.Status != "LAUNCHED" || b.LaunchID == "" || b.SurfaceID == "" || b.WorkspaceID == "" || b.Tool != seat.Tool || b.SurfaceID != seat.SurfaceID || b.WorkspaceID != seat.WorkspaceID {
		return b, messagingError("SURFACE_UNBOUND", "cmux.surface", "current seat differs from launch registration")
	}
	if gate, err := bindingGate(ctx, tx, seat, true); err != nil {
		return b, err
	} else if gate != "" {
		return b, messagingError(gate, "cmux.surface", "binding or stop gate blocks transport")
	}
	return b, nil
}

func (s *Store) CMUXSurface(ctx context.Context, a MessageActor) (SurfaceRegistration, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return SurfaceRegistration{}, err
	}
	defer tx.Rollback()
	b, err := surfaceBinding(ctx, tx, a)
	if err != nil {
		return b, err
	}
	return b, tx.Commit()
}

// BindCMUXNativeSession promotes a launcher-owned OpenCode surface only after
// the runtime has independently matched cmux resume metadata and session data.
func (s *Store) BindCMUXNativeSession(ctx context.Context, a MessageActor, observed SurfaceRegistration, sessionID string) (resultErr error) {
	if strings.TrimSpace(sessionID) == "" || len(sessionID) > 512 || strings.ContainsAny(sessionID, "\x00\r\n\t ") {
		return messagingError("INVALID_REQUEST", "cmux.native_session", "valid native session identity required")
	}
	tx, err := s.beginImmediate(ctx)
	if err != nil {
		return err
	}
	defer tx.finish(&resultErr)
	b, err := surfaceBinding(ctx, tx, a)
	if err != nil {
		return err
	}
	if b != observed || b.Tool != "opencode" {
		return messagingError("GENERATION_MISMATCH", "cmux.native_session", "surface registration changed or tool is unsupported")
	}
	seat, err := recoverySeat(ctx, tx, a.RunID, a.AgentID)
	if err != nil {
		return err
	}
	if seat.Generation != a.Generation || seat.WorkspaceID != b.WorkspaceID || seat.SurfaceID != b.SurfaceID {
		return messagingError("GENERATION_MISMATCH", "cmux.native_session", "current seat differs from launch registration")
	}
	if seat.Status == "READY" && seat.NativeSessionID == sessionID {
		return tx.Commit(ctx)
	}
	if seat.Status != "LAUNCHED" || seat.NativeSessionID != "" {
		return messagingError("BINDING_UNVERIFIED", "cmux.native_session", "seat is not an unbound launched surface")
	}
	var reused bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM seats WHERE run_id=? AND agent_id!=? AND native_session_id=?)`, a.RunID, a.AgentID, sessionID).Scan(&reused); err != nil {
		return err
	}
	if reused {
		return messagingError("ACTOR_MISMATCH", "cmux.native_session", "native session is already bound to another seat")
	}
	if _, err := tx.ExecContext(ctx, `UPDATE seats SET native_session_id=?,status='READY' WHERE run_id=? AND agent_id=? AND generation=? AND is_current=1`, sessionID, a.RunID, a.AgentID, a.Generation); err != nil {
		return err
	}
	details, _ := json.Marshal(struct {
		Binding SurfaceRegistration `json:"binding"`
		Session string              `json:"session_id"`
	}{b, sessionID})
	if _, err := appendJournal(ctx, tx, JournalEntry{RunID: a.RunID, AgentID: a.AgentID, Generation: a.Generation, Event: "CMUX_NATIVE_SESSION_BOUND", Details: string(details)}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// CMUXEvidence retains bounded raw process output and parsed native addressing.
type CMUXEvidence struct {
	ID                string          `json:"id"`
	Source            string          `json:"source"`
	SourceSurface     string          `json:"source_surface"`
	SourceWorkspace   string          `json:"source_workspace"`
	TargetSurface     string          `json:"target_surface"`
	TargetWorkspace   string          `json:"target_workspace"`
	Body              string          `json:"body"`
	State             string          `json:"state"`
	RecipientHasAgent *bool           `json:"recipient_has_agent,omitempty"`
	Stdout            string          `json:"stdout"`
	Stderr            string          `json:"stderr"`
	ExitCode          int             `json:"exit_code"`
	Truncated         bool            `json:"truncated"`
	Raw               json.RawMessage `json:"raw,omitempty"`
}

type CMUXTransportWork struct {
	Record   MessageRecord       `json:"record"`
	Binding  SurfaceRegistration `json:"binding"`
	Evidence *CMUXEvidence       `json:"evidence,omitempty"`
	WireBody string              `json:"wire_body,omitempty"`
}

// BeginCMUXSend uses the shared sender/request namespace and commits an intent
// before native execution. A committed intent is never reclaimed after a crash.
func (s *Store) BeginCMUXSend(ctx context.Context, c MessageCommand) (CMUXTransportWork, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return CMUXTransportWork{}, false, err
	}
	defer tx.Rollback()
	var w CMUXTransportWork
	if _, err := surfaceBinding(ctx, tx, c.Actor); err != nil {
		return w, false, err
	}
	target, err := currentMessageSeat(ctx, tx, c.Actor.RunID, c.Recipient, c.TargetGeneration, "recipient")
	if err != nil {
		return w, false, err
	}
	w.Binding, err = surfaceBinding(ctx, tx, MessageActor{c.Actor.RunID, c.Recipient, target})
	if err != nil {
		return w, false, err
	}
	existing, err := scanMessage(tx.QueryRowContext(ctx, selectMessage+` WHERE run_id=? AND sender=? AND request_id=?`, c.Actor.RunID, c.Actor.AgentID, c.RequestID))
	if err == nil {
		if existing.Body != c.Body || existing.Recipient != c.Recipient || existing.Kind != "send" || existing.SenderGeneration != c.Actor.Generation {
			return w, false, ErrIdempotencyConflict
		}
		var intent string
		if err := tx.QueryRowContext(ctx, `SELECT details FROM journal WHERE message_id=? AND event='CMUX_SEND_REQUESTED'`, existing.ID).Scan(&intent); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return w, false, ErrIdempotencyConflict
			}
			return w, false, err
		}
		var previous SurfaceRegistration
		if json.Unmarshal([]byte(intent), &previous) != nil || previous != w.Binding {
			return w, false, messagingError("GENERATION_MISMATCH", "cmux.binding", "send belongs to another launch")
		}
		w.Record, err = sessionRecord(ctx, tx, existing)
		if err != nil {
			return w, false, err
		}
		var data string
		err = tx.QueryRowContext(ctx, `SELECT details FROM journal WHERE message_id=? AND event='CMUX_SEND_RESULT'`, existing.ID).Scan(&data)
		if err == nil {
			w.Evidence = &CMUXEvidence{}
			if err := json.Unmarshal([]byte(data), w.Evidence); err != nil {
				return w, false, err
			}
		} else if !errors.Is(err, sql.ErrNoRows) {
			return w, false, err
		}
		if w.Record.Delivery.TargetGeneration != target {
			return w, false, messagingError("GENERATION_MISMATCH", "recipient.generation", "send targets another generation")
		}
		return w, false, tx.Commit()
	} else if !errors.Is(err, sql.ErrNoRows) {
		return w, false, err
	}
	c.Kind, c.Session = "send", ""
	w.Record, err = storeMessage(ctx, tx, c)
	if err != nil {
		return w, false, err
	}
	id := w.Record.Message.ID
	if _, err := tx.ExecContext(ctx, `UPDATE deliveries SET phase='CMUX_SEND_REQUESTED',attempt=1,channel_id='cmux-agent-message',updated_at=? WHERE message_id=?`, timestamp(time.Time{}), id); err != nil {
		return w, false, err
	}
	body, _ := json.Marshal(w.Binding)
	if _, err := appendJournal(ctx, tx, JournalEntry{RunID: c.Actor.RunID, MessageID: id, AgentID: c.Actor.AgentID, Generation: c.Actor.Generation, Event: "CMUX_SEND_REQUESTED", Details: string(body)}); err != nil {
		return w, false, err
	}
	w.Record.Delivery.Phase, w.Record.Delivery.Attempt, w.Record.Delivery.Channel = "CMUX_SEND_REQUESTED", 1, "cmux-agent-message"
	return w, true, tx.Commit()
}

func (s *Store) FinishCMUXSend(ctx context.Context, a MessageActor, w CMUXTransportWork, e CMUXEvidence) (_ CMUXTransportWork, resultErr error) {
	tx, err := s.beginImmediate(ctx)
	if err != nil {
		return w, err
	}
	defer tx.finish(&resultErr)
	m, err := scanMessage(tx.QueryRowContext(ctx, selectMessage+` WHERE run_id=? AND id=?`, a.RunID, w.Record.Message.ID))
	if err != nil {
		return w, err
	}
	var intent string
	if err := tx.QueryRowContext(ctx, `SELECT details FROM journal WHERE message_id=? AND event='CMUX_SEND_REQUESTED' AND agent_id=? AND generation=?`, m.ID, a.AgentID, a.Generation).Scan(&intent); err != nil {
		return w, err
	}
	var b SurfaceRegistration
	if json.Unmarshal([]byte(intent), &b) != nil || b != w.Binding || m.Sender != a.AgentID || m.SenderGeneration != a.Generation || m.Body != w.Record.Message.Body || m.Recipient != b.AgentID {
		return w, messagingError("ACTOR_MISMATCH", "cmux.send", "result differs from durable send intent")
	}
	_, gateErr := surfaceBinding(ctx, tx, a)
	current, targetErr := surfaceBinding(ctx, tx, MessageActor{a.RunID, b.AgentID, b.Generation})
	if gateErr == nil {
		gateErr = targetErr
	}
	if gateErr == nil && current != b {
		gateErr = messagingError("GENERATION_MISMATCH", "cmux.binding", "launch changed during send")
	}
	var phase string
	if err := tx.QueryRowContext(ctx, `SELECT phase FROM deliveries WHERE message_id=? AND target_generation=?`, w.Record.Message.ID, b.Generation).Scan(&phase); err != nil {
		return w, err
	}
	if phase != "CMUX_SEND_REQUESTED" {
		return w, ErrIdempotencyConflict
	}
	phase = "UNVERIFIED"
	var duplicate bool
	if e.ID != "" {
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM journal WHERE run_id=? AND event='CMUX_SEND_RESULT' AND json_extract(details,'$.id')=?)`, a.RunID, e.ID).Scan(&duplicate); err != nil {
			return w, err
		}
	}
	if duplicate && gateErr == nil {
		gateErr = messagingError("CMUX_EVENT_DUPLICATE", "cmux.send", "native message ID already used")
	}
	wireBody := w.WireBody
	if wireBody == "" {
		wireBody = w.Record.Message.Body
	}
	if gateErr == nil && e.ExitCode == 0 && !e.Truncated && e.ID != "" && e.RecipientHasAgent != nil && *e.RecipientHasAgent && (e.State == "queued" || e.State == "delivered" || e.State == "read") && strings.EqualFold(e.TargetSurface, b.SurfaceID) && strings.EqualFold(e.TargetWorkspace, b.WorkspaceID) && e.Source == "agentflow" && e.Body == wireBody {
		phase = "CMUX_QUEUED"
	}
	if _, err := tx.ExecContext(ctx, `UPDATE deliveries SET phase=?,updated_at=? WHERE message_id=?`, phase, timestamp(time.Time{}), w.Record.Message.ID); err != nil {
		return w, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE messages SET status=? WHERE id=?`, phase, w.Record.Message.ID); err != nil {
		return w, err
	}
	data, _ := json.Marshal(e)
	if _, err := appendJournal(ctx, tx, JournalEntry{RunID: a.RunID, MessageID: w.Record.Message.ID, AgentID: a.AgentID, Generation: a.Generation, Event: "CMUX_SEND_RESULT", Details: string(data)}); err != nil {
		return w, err
	}
	w.Record.Message.Status, w.Record.Delivery.Phase, w.Evidence = phase, phase, &e
	if err := tx.Commit(ctx); err != nil {
		return w, err
	}
	return w, gateErr
}

// AcceptCMUXInbox is an explicit event entry, never a workflow result or READY.
// All events are checked before any write. Matching historical IDs are skipped;
// conflicting history, duplicate IDs within a batch, unknown sources and wrong
// target surfaces reject the entire batch.
func (s *Store) AcceptCMUXInbox(ctx context.Context, a MessageActor, observed SurfaceRegistration, events []CMUXEvidence) (resultErr error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	// Own the SQL transaction on a pinned connection. sql.Tx marks itself done
	// even when SQLite rejects COMMIT, so a deferred Tx.Rollback cannot clean up
	// that still-open transaction. BEGIN IMMEDIATE matches the store's _txlock.
	if _, err := conn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		_ = conn.Raw(func(any) error { return driver.ErrBadConn })
		return err
	}
	committed, commitFailed := false, false
	defer func() {
		if committed {
			return
		}
		// Cleanup must survive caller cancellation, but has its own finite bound.
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_, rollbackErr := conn.ExecContext(cleanupCtx, `ROLLBACK`)
		if commitFailed || rollbackErr != nil {
			// Conn.Close alone returns the physical connection to the pool. Force
			// disposal even if the best-effort rollback after COMMIT succeeded.
			_ = conn.Raw(func(any) error { return driver.ErrBadConn })
		}
		if rollbackErr != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("cmux inbox rollback: %w", rollbackErr))
		}
	}()
	b, err := surfaceBinding(ctx, conn, a)
	if err != nil {
		return err
	}
	if b != observed {
		return messagingError("GENERATION_MISMATCH", "cmux.inbox", "launch changed during inbox read")
	}
	seen := map[string]bool{}
	pending := []CMUXEvidence{}
	for _, e := range events {
		if e.ID == "" || seen[e.ID] || !strings.EqualFold(e.TargetSurface, b.SurfaceID) || !strings.EqualFold(e.TargetWorkspace, b.WorkspaceID) || e.Body == "" || e.ExitCode != 0 || e.Truncated {
			return messagingError("CMUX_EVENT_REJECTED", "cmux.inbox", "missing, duplicate or wrong-surface message evidence")
		}
		seen[e.ID] = true
		var data, agent string
		var generation int64
		err := conn.QueryRowContext(ctx, `SELECT details,agent_id,generation FROM journal WHERE run_id=? AND event='CMUX_INBOX_EVENT' AND json_extract(details,'$.id')=? LIMIT 1`, a.RunID, e.ID).Scan(&data, &agent, &generation)
		if err == nil {
			var old CMUXEvidence
			if json.Unmarshal([]byte(data), &old) != nil || agent != a.AgentID || generation != a.Generation || old.Source != e.Source || old.Body != e.Body ||
				!strings.EqualFold(old.SourceSurface, e.SourceSurface) || !strings.EqualFold(old.SourceWorkspace, e.SourceWorkspace) ||
				!strings.EqualFold(old.TargetSurface, e.TargetSurface) || !strings.EqualFold(old.TargetWorkspace, e.TargetWorkspace) {
				return messagingError("CMUX_EVENT_DUPLICATE", "cmux.inbox", "native message ID conflicts with accepted identity or content")
			}
			// State and raw metadata may change in the native history. Preserve the
			// original receipt without appending another event or creating work.
			continue
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		// An agentflow echo must match a previously confirmed outbound receipt.
		var known bool
		if e.Source == "agentflow" {
			err = conn.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM journal j JOIN deliveries d ON d.message_id=j.message_id WHERE j.run_id=? AND j.event='CMUX_SEND_RESULT' AND d.phase IN ('CMUX_QUEUED','ACKED') AND json_extract(j.details,'$.id')=? AND json_extract(j.details,'$.body')=? AND lower(json_extract(j.details,'$.target_surface'))=lower(?) AND lower(json_extract(j.details,'$.target_workspace'))=lower(?) AND json_extract(j.details,'$.source_surface')=? AND json_extract(j.details,'$.source_workspace')=?)`, a.RunID, e.ID, e.Body, b.SurfaceID, b.WorkspaceID, e.SourceSurface, e.SourceWorkspace).Scan(&known)
		} else {
			var sourceAgent string
			var generation int64
			err = conn.QueryRowContext(ctx, `SELECT agent_id,generation FROM seats WHERE run_id=? AND is_current=1 AND lower(surface_id)=lower(?) AND lower(workspace_id)=lower(?)`, a.RunID, e.SourceSurface, e.SourceWorkspace).Scan(&sourceAgent, &generation)
			if err == nil {
				_, err = surfaceBinding(ctx, conn, MessageActor{a.RunID, sourceAgent, generation})
				known = err == nil
			}
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if !known {
			return messagingError("CMUX_SOURCE_UNKNOWN", "cmux.inbox", "message source is not a registered surface or known outbound receipt")
		}
		pending = append(pending, e)
	}
	for _, e := range pending {
		data, _ := json.Marshal(e)
		if _, err := appendJournal(ctx, conn, JournalEntry{RunID: a.RunID, AgentID: a.AgentID, Generation: a.Generation, Event: "CMUX_INBOX_EVENT", Details: string(data)}); err != nil {
			return err
		}
	}
	_, err = conn.ExecContext(ctx, `COMMIT`)
	committed, commitFailed = err == nil, err != nil
	return err
}

func (s *Store) CMUXRegistrations(ctx context.Context, runID string) ([]SurfaceRegistration, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT j.details FROM journal j JOIN seats s ON s.run_id=j.run_id AND s.agent_id=j.agent_id AND s.generation=j.generation AND s.is_current=1 WHERE j.run_id=? AND j.event='CMUX_SURFACE_REGISTERED' AND j.seq=(SELECT MAX(j2.seq) FROM journal j2 WHERE j2.run_id=j.run_id AND j2.agent_id=j.agent_id AND j2.generation=j.generation AND j2.event='CMUX_SURFACE_REGISTERED') ORDER BY j.agent_id`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []SurfaceRegistration{}
	for rows.Next() {
		var data string
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		var b SurfaceRegistration
		if err := json.Unmarshal([]byte(data), &b); err != nil {
			return nil, err
		}
		result = append(result, b)
	}
	return result, rows.Err()
}
