package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// RecoveryDecision describes durable evidence, never proof of native liveness.
// NextAction is advisory: a caller must still acquire BeginSessionAction.
type RecoveryDecision struct {
	MessageID     string        `json:"message_id"`
	State         string        `json:"state"`
	Reason        string        `json:"reason"`
	NextAction    string        `json:"next_action"`
	Delivery      DeliveryState `json:"delivery"`
	Generation    int64         `json:"generation"`
	Tool          string        `json:"tool"`
	NativeSession string        `json:"native_session_id"`
	WorkspaceID   string        `json:"workspace_id"`
	SurfaceID     string        `json:"surface_id"`
	SeatStatus    string        `json:"seat_status"`
	CurrentWork   string        `json:"current_message_id"`
	StopRequested bool          `json:"stop_requested"`
	EvidenceSeq   int64         `json:"evidence_seq"`
	JournalSeq    int64         `json:"journal_seq"`
	RecordedAt    time.Time     `json:"recorded_at"`
}

// ReconcileSession serializes inspection, safe queued relocation and a decision
// receipt in one transaction. Reopening the database cannot grant replay rights.
// No native operation or retry is performed, including for SAFE_TO_CONTINUE.
func (s *Store) ReconcileSession(ctx context.Context, actor MessageActor, id string) (RecoveryDecision, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return RecoveryDecision{}, err
	}
	defer tx.Rollback()
	r, err := reconcileSessionWithRelocation(ctx, tx, actor, id, true)
	if err != nil {
		return r, err
	}
	return r, tx.Commit()
}

// Recovery ticks never silently move an old-generation assignment. Explicit S5
// reconciliation retains its existing safe untouched-queue relocation semantics.
func (s *Store) ReconcileRecoverySession(ctx context.Context, actor MessageActor, id string) (RecoveryDecision, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return RecoveryDecision{}, err
	}
	defer tx.Rollback()
	r, err := reconcileSessionWithRelocation(ctx, tx, actor, id, false)
	if err != nil {
		return r, err
	}
	return r, tx.Commit()
}

func reconcileSessionWithRelocation(ctx context.Context, tx *sql.Tx, actor MessageActor, id string, relocate bool) (RecoveryDecision, error) {
	workflow, err := workflowDispatch(ctx, tx, id)
	if err != nil {
		return RecoveryDecision{}, err
	}
	if workflow {
		relocate = false
	}
	if err := validateMessageActor(ctx, tx, actor); err != nil {
		return RecoveryDecision{}, err
	}
	w, err := sessionWork(ctx, tx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return RecoveryDecision{}, messagingError("MESSAGE_NOT_FOUND", "message.id", "message or current binding is not stored")
	}
	if err != nil {
		return RecoveryDecision{}, err
	}
	m := w.Record.Message
	if m.RunID != actor.RunID || m.Sender != actor.AgentID || m.SenderGeneration != actor.Generation {
		return RecoveryDecision{}, messagingError("ACTOR_MISMATCH", "session.sender", "only the original current sender can reconcile this message")
	}
	r := RecoveryDecision{MessageID: id, NextAction: "NONE", Delivery: w.Record.Delivery,
		Generation: w.Seat.Generation, Tool: w.Seat.Tool, NativeSession: w.Seat.NativeSessionID,
		WorkspaceID: w.Seat.WorkspaceID, SurfaceID: w.Seat.SurfaceID, SeatStatus: w.Seat.Status, CurrentWork: w.Seat.CurrentMessageID}
	if err := tx.QueryRowContext(ctx, `SELECT stop_requested FROM runs WHERE run_id = ?`, actor.RunID).Scan(&r.StopRequested); err != nil {
		return r, err
	}
	var resetRequested, resetReady, submitRequested, sent, acked, nativeStarted bool
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq), 0),
		COALESCE(MAX(event = 'RESET_REQUESTED'), 0), COALESCE(MAX(event = 'RESET_READY' AND generation = ?), 0),
		COALESCE(MAX(event = 'SUBMIT_REQUESTED'), 0), COALESCE(MAX(event = 'MESSAGE_SENT' AND generation = ?), 0),
		COALESCE(MAX(event = 'MESSAGE_ACKED' AND generation = ?), 0),
		COALESCE(MAX(event IN ('RESET_REQUESTED','RESET_READY','SUBMIT_REQUESTED','MESSAGE_SENT','MESSAGE_ACKED')), 0)
		FROM journal WHERE message_id = ? AND event IN ('MESSAGE_STORED','SESSION_QUEUED','RESET_REQUESTED','RESET_READY','SUBMIT_REQUESTED','MESSAGE_SENT','MESSAGE_ACKED','SESSION_UNVERIFIED','SESSION_RELOCATED')`,
		r.Delivery.TargetGeneration, r.Delivery.TargetGeneration, r.Delivery.TargetGeneration, id).Scan(&r.EvidenceSeq, &resetRequested, &resetReady, &submitRequested, &sent, &acked, &nativeStarted); err != nil {
		return r, err
	}
	untouched := r.Delivery.ResetPhase == "NONE" && r.Delivery.Attempt == 0 && !nativeStarted
	if relocate && m.Kind == "assign" && w.Record.Session != "" && r.Delivery.TargetGeneration != r.Generation && r.Delivery.Phase == "QUEUED" && untouched && !r.StopRequested {
		if _, err := tx.ExecContext(ctx, `UPDATE deliveries SET target_generation = ?, updated_at = ? WHERE message_id = ?`, r.Generation, timestamp(time.Time{}), id); err != nil {
			return r, err
		}
		entry, err := appendJournal(ctx, tx, JournalEntry{RunID: actor.RunID, MessageID: id, AgentID: m.Recipient, Generation: r.Generation, Event: "SESSION_RELOCATED", Details: "queued work had no native action; selected current binding"})
		if err != nil {
			return r, err
		}
		r.EvidenceSeq, r.Delivery.TargetGeneration = entry, r.Generation
	}
	earlier, err := earlierSessionAssignment(ctx, tx, m)
	if err != nil {
		return r, err
	}
	d := r.Delivery
	bindingBlocked, err := bindingGate(ctx, tx, w.Seat, true)
	if err != nil {
		return r, err
	}
	switch {
	case d.Phase == "ACKED" && acked && (w.Record.Session == "" || (!submitRequested && d.Attempt == 0) || w.SubmissionConfirmed):
		r.State, r.Reason = "ACKED", "RECEIPT_CONFIRMED"
	case d.Phase == "SENT" && sent:
		r.State, r.Reason = "SENT", "SUBMISSION_CONFIRMED"
	case d.TargetGeneration != r.Generation:
		r.State, r.Reason = "REJECTED_OLD_GENERATION", "DELIVERY_BINDING_CHANGED"
	case d.ResetPhase == "RESET_REQUESTED" || d.Phase == "SUBMIT_REQUESTED" ||
		(submitRequested && !sent) || (resetRequested && !resetReady):
		r.State, r.Reason = "UNKNOWN_NEEDS_RECONCILE", "NATIVE_ACTION_OUTCOME_UNKNOWN"
	case (d.Phase != "PENDING" && d.Phase != "QUEUED") || d.Attempt != 0 ||
		(d.ResetPhase != "NONE" && d.ResetPhase != "READY") || (d.ResetPhase == "READY" && !resetReady) ||
		(d.ResetPhase == "NONE" && nativeStarted) || sent || acked:
		r.State, r.Reason = "UNKNOWN_NEEDS_RECONCILE", "DURABLE_EVIDENCE_INCONSISTENT"
	case r.StopRequested:
		r.State, r.Reason = "QUEUED", "STOP_REQUESTED"
	case bindingBlocked != "":
		r.State, r.Reason = "QUEUED", bindingBlocked
	case m.Kind != "assign" || w.Record.Session == "":
		r.State, r.Reason = "QUEUED", "DELIVERY_ADAPTER_UNIMPLEMENTED"
	case r.SeatStatus != "READY" || r.CurrentWork != "":
		r.State, r.Reason = "QUEUED", "SEAT_BUSY"
	case earlier:
		r.State, r.Reason = "QUEUED", "SEAT_QUEUE"
	case d.Phase == "QUEUED" && w.Record.Coordination != "":
		r.State, r.Reason = "QUEUED", w.Record.Coordination
	default:
		r.State, r.Reason, r.NextAction = "SAFE_TO_CONTINUE", "RECHECK_INPUT_AND_CLAIM", "SUBMIT"
		if w.Record.Session == "fresh" && d.ResetPhase == "NONE" {
			r.NextAction = "RESET"
		}
	}
	// Compare only the latest decision, so A -> B -> A creates a new receipt.
	data, err := json.Marshal(r)
	if err != nil {
		return r, err
	}
	var previous, created string
	err = tx.QueryRowContext(ctx, `SELECT seq, details, created_at FROM journal WHERE message_id = ? AND event = 'SESSION_RECONCILED' ORDER BY seq DESC LIMIT 1`, id).Scan(&r.JournalSeq, &previous, &created)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return r, err
	}
	if previous != string(data) {
		r.RecordedAt = time.Now().UTC()
		entry, err := appendJournal(ctx, tx, JournalEntry{RunID: actor.RunID, MessageID: id, AgentID: m.Recipient, Generation: r.Generation, Event: "SESSION_RECONCILED", Details: string(data), CreatedAt: r.RecordedAt})
		if err != nil {
			return r, err
		}
		r.JournalSeq = entry
	} else {
		r.RecordedAt, err = time.Parse(time.RFC3339Nano, created)
		if err != nil {
			return r, err
		}
	}
	return r, nil
}

// rejectLateResult records only known historical recipients. Repeated identical
// rejections are deduplicated; untrusted bodies and request IDs are never logged.
func rejectLateResult(ctx context.Context, tx *sql.Tx, a MessageActor, id, action string, original error) error {
	var eligible bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM messages m JOIN seats s ON s.run_id = m.run_id AND s.agent_id = m.recipient WHERE m.run_id = ? AND m.id = ? AND m.recipient = ? AND s.generation = ? AND s.is_current = 0)`, a.RunID, id, a.AgentID, a.Generation).Scan(&eligible); err != nil {
		return err
	}
	if !eligible {
		return original
	}
	details := action + ": GENERATION_MISMATCH"
	var recorded bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM journal WHERE message_id = ? AND agent_id = ? AND generation = ? AND event = 'SESSION_RESULT_REJECTED' AND details = ?)`, id, a.AgentID, a.Generation, details).Scan(&recorded); err != nil {
		return err
	}
	if !recorded {
		if _, err := appendJournal(ctx, tx, JournalEntry{RunID: a.RunID, MessageID: id, AgentID: a.AgentID, Generation: a.Generation, Event: "SESSION_RESULT_REJECTED", Details: details}); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return original
}
