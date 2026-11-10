package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

// SessionWork is a consistent view of a durable assignment and current binding.
type SessionWork struct {
	Record              MessageRecord
	Seat                Seat
	SubmissionConfirmed bool
}

// confirmedSessionSubmission requires native submission evidence independently
// of receipt state. ACKED alone cannot replace a missing MESSAGE_SENT receipt.
func confirmedSessionSubmission(ctx context.Context, tx queryRower, m Message, d DeliveryState) (bool, error) {
	if (d.Phase != "SENT" && d.Phase != "ACKED") || d.Attempt < 1 || d.Channel == "" || d.Channel == "UNVERIFIED" {
		return false, nil
	}
	var confirmed bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM journal WHERE run_id = ? AND message_id = ? AND agent_id = ? AND generation = ? AND event = 'MESSAGE_SENT')`, m.RunID, m.ID, m.Recipient, d.TargetGeneration).Scan(&confirmed)
	return confirmed, err
}

func earlierSessionAssignment(ctx context.Context, tx *sql.Tx, m Message) (bool, error) {
	var earlier bool
	// Also fence historical ACKED rows whose native action was never confirmed.
	// A receipt cannot make an unresolved native turn leave the queue.
	err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM messages m JOIN deliveries d ON d.message_id = m.id
		WHERE m.run_id = ? AND m.recipient = ? AND m.seq < ? AND m.kind = 'assign' AND
		(d.phase NOT IN ('SENT', 'ACKED') OR
		(EXISTS (SELECT 1 FROM journal j WHERE j.message_id = m.id AND j.event = 'SUBMIT_REQUESTED') AND
		NOT EXISTS (SELECT 1 FROM journal j WHERE j.message_id = m.id AND j.event = 'MESSAGE_SENT' AND j.agent_id = m.recipient AND j.generation = d.target_generation))))`, m.RunID, m.Recipient, m.Seq).Scan(&earlier)
	return earlier, err
}

func sessionRecord(ctx context.Context, tx queryRower, m Message) (MessageRecord, error) {
	var generation int64
	if err := tx.QueryRowContext(ctx, `SELECT target_generation FROM deliveries WHERE message_id = ? ORDER BY id LIMIT 1`, m.ID).Scan(&generation); err != nil {
		return MessageRecord{}, err
	}
	d, err := getDelivery(ctx, tx, m.ID, generation)
	if err != nil {
		return MessageRecord{}, err
	}
	var details string
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT details FROM journal WHERE message_id = ? AND event IN ('MESSAGE_STORED', 'WORKFLOW_DISPATCH_STORED') ORDER BY seq LIMIT 1), '')`, m.ID).Scan(&details); err != nil {
		return MessageRecord{}, err
	}
	var selected struct {
		Session string `json:"session"`
	}
	_ = json.Unmarshal([]byte(details), &selected)
	return MessageRecord{Message: m, Delivery: deliveryState(d), Session: selected.Session, Coordination: d.LastErrorCode}, nil
}

func sessionWork(ctx context.Context, tx *sql.Tx, id string) (SessionWork, error) {
	m, err := scanMessage(tx.QueryRowContext(ctx, selectMessage+` WHERE id = ?`, id))
	if err != nil {
		return SessionWork{}, err
	}
	r, err := sessionRecord(ctx, tx, m)
	if err != nil {
		return SessionWork{}, err
	}
	var seat Seat
	var created string
	err = tx.QueryRowContext(ctx, `SELECT run_id, agent_id, generation, is_current, tool, native_session_id, workspace_id, surface_id, status, COALESCE(current_message_id, ''), created_at FROM seats WHERE run_id = ? AND agent_id = ? AND is_current = 1`, m.RunID, m.Recipient).Scan(&seat.RunID, &seat.AgentID, &seat.Generation, &seat.Current, &seat.Tool, &seat.NativeSessionID, &seat.WorkspaceID, &seat.SurfaceID, &seat.Status, &seat.CurrentMessageID, &created)
	if err != nil {
		return SessionWork{}, err
	}
	seat.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
	if err != nil {
		return SessionWork{}, err
	}
	confirmed, err := confirmedSessionSubmission(ctx, tx, m, r.Delivery)
	return SessionWork{Record: r, Seat: seat, SubmissionConfirmed: confirmed && seat.Generation == r.Delivery.TargetGeneration}, err
}

func (s *Store) SessionWork(ctx context.Context, id string) (SessionWork, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return SessionWork{}, err
	}
	defer tx.Rollback()
	w, err := sessionWork(ctx, tx, id)
	if err != nil {
		return SessionWork{}, err
	}
	return w, tx.Commit()
}

func sameSession(a, b Seat) bool {
	return a.RunID == b.RunID && a.AgentID == b.AgentID && a.Generation == b.Generation && a.Tool == b.Tool && a.NativeSessionID == b.NativeSessionID && a.WorkspaceID == b.WorkspaceID && a.SurfaceID == b.SurfaceID
}

// BeginSessionAction serializes the side-effect boundary across Store handles.
// Only the caller receiving claimed=true may invoke reset/submit. QUEUED work
// can be reconsidered explicitly; requested/unknown actions are never reclaimed.
func (s *Store) BeginSessionAction(ctx context.Context, id string, observed Seat, action, blocked string) (SessionWork, bool, error) {
	return s.beginSessionAction(ctx, id, observed, action, blocked, 0)
}

// BeginRecoverySessionAction additionally requires the still-current recovery
// reservation inside the same transaction as the native action claim.
func (s *Store) BeginRecoverySessionAction(ctx context.Context, id string, observed Seat, action, blocked string, reservation int64) (SessionWork, bool, error) {
	if reservation <= 0 {
		return SessionWork{}, false, messagingError("INVALID_REQUEST", "recovery.reservation", "recovery reservation required")
	}
	return s.beginSessionAction(ctx, id, observed, action, blocked, reservation)
}

func (s *Store) beginSessionAction(ctx context.Context, id string, observed Seat, action, blocked string, reservation int64) (SessionWork, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return SessionWork{}, false, err
	}
	defer tx.Rollback()
	w, err := sessionWork(ctx, tx, id)
	if err != nil {
		return w, false, err
	}
	r := w.Record
	workflow, err := workflowDispatch(ctx, tx, id)
	if err != nil {
		return w, false, err
	}
	if workflow {
		cursor, err := workflowCursor(ctx, tx, r.Message.RunID)
		if err != nil {
			return w, false, err
		}
		if cursor.DispatchID != id || (cursor.Phase != "EXECUTING" && cursor.Phase != "REVIEWING") {
			return w, false, messagingError("WORKFLOW_INVALID_TRANSITION", "workflow.dispatch", "dispatch is no longer current")
		}
		if r.Delivery.TargetGeneration != w.Seat.Generation {
			return w, false, messagingError("GENERATION_MISMATCH", "workflow.dispatch", "workflow dispatch cannot move to another generation")
		}
	}
	if reservation != 0 {
		decision, err := latestRetry(ctx, tx, id)
		if err != nil {
			return w, false, err
		}
		if decision.JournalSeq != reservation || decision.State != "ATTEMPT_RESERVED" {
			return w, false, tx.Commit()
		}
	}
	if r.Message.Kind != "assign" || (r.Session != "fresh" && r.Session != "persistent") || (action != "reset" && action != "submit") {
		return w, false, messagingError("INVALID_REQUEST", "session", "coordinated assignment and valid action required")
	}
	if err := validateMessageActor(ctx, tx, MessageActor{RunID: r.Message.RunID, AgentID: r.Message.Sender, Generation: r.Message.SenderGeneration}); err != nil {
		return w, false, err
	}
	if r.Delivery.Phase != "PENDING" && r.Delivery.Phase != "QUEUED" {
		return w, false, tx.Commit()
	}
	// A delivery row alone never erases a previously committed native intent.
	var resetStarted, submitStarted, readyConfirmed bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM journal WHERE message_id = ? AND event IN ('RESET_REQUESTED','RESET_READY')), EXISTS (SELECT 1 FROM journal WHERE message_id = ? AND event IN ('SUBMIT_REQUESTED','MESSAGE_SENT','MESSAGE_ACKED')), EXISTS (SELECT 1 FROM journal WHERE message_id = ? AND event = 'RESET_READY' AND generation = ?)`, id, id, id, r.Delivery.TargetGeneration).Scan(&resetStarted, &submitStarted, &readyConfirmed); err != nil {
		return w, false, err
	}
	if r.Delivery.Attempt != 0 || submitStarted || (resetStarted && (action == "reset" || r.Delivery.ResetPhase != "READY")) || (r.Delivery.ResetPhase == "READY" && !readyConfirmed) {
		return w, false, tx.Commit()
	}
	if action == "reset" && (r.Session != "fresh" || r.Delivery.ResetPhase != "NONE") {
		return w, false, tx.Commit()
	}
	if action == "submit" && r.Session == "fresh" && r.Delivery.ResetPhase != "READY" {
		return w, false, tx.Commit()
	}
	if !sameSession(w.Seat, observed) {
		return w, false, messagingError("GENERATION_MISMATCH", "seat", "binding changed since inspection")
	}
	if gate, err := bindingGate(ctx, tx, w.Seat, true); err != nil {
		return w, false, err
	} else if gate != "" {
		blocked = gate
	}
	if w.Seat.Status != "READY" || w.Seat.CurrentMessageID != "" {
		blocked = "SEAT_BUSY"
	}
	earlier, err := earlierSessionAssignment(ctx, tx, r.Message)
	if err != nil {
		return w, false, err
	}
	if earlier {
		blocked = "SEAT_QUEUE"
	}
	var stopped bool
	if err := tx.QueryRowContext(ctx, `SELECT stop_requested FROM runs WHERE run_id = ?`, r.Message.RunID).Scan(&stopped); err != nil {
		return w, false, err
	}
	if stopped {
		blocked = "STOP_REQUESTED"
	}
	// A completed reset belongs to its exact confirmed generation. Never move it.
	if r.Delivery.TargetGeneration != w.Seat.Generation && (r.Delivery.Phase != "QUEUED" || r.Delivery.ResetPhase != "NONE" || r.Delivery.Attempt != 0 || resetStarted || submitStarted) {
		return w, false, messagingError("GENERATION_MISMATCH", "delivery", "confirmed reset binding is no longer current")
	}
	phase, reset, event, attempt := "QUEUED", r.Delivery.ResetPhase, "SESSION_QUEUED", r.Delivery.Attempt
	target := w.Seat.Generation
	if stopped {
		target = r.Delivery.TargetGeneration
	}
	claimed := blocked == ""
	if claimed {
		if action == "reset" {
			phase, reset, event = "PENDING", "RESET_REQUESTED", "RESET_REQUESTED"
		} else {
			phase, event, attempt = "SUBMIT_REQUESTED", "SUBMIT_REQUESTED", attempt+1
		}
	}
	if phase == r.Delivery.Phase && reset == r.Delivery.ResetPhase && blocked == r.Coordination && target == r.Delivery.TargetGeneration && attempt == r.Delivery.Attempt {
		return w, false, tx.Commit()
	}
	if _, err := tx.ExecContext(ctx, `UPDATE deliveries SET target_generation = ?, phase = ?, reset_phase = ?, attempt = ?, last_error_code = ?, updated_at = ? WHERE message_id = ?`, target, phase, reset, attempt, blocked, timestamp(time.Time{}), id); err != nil {
		return w, false, err
	}
	// Reserve the binding in the same transaction as the submit intent. Native
	// execution can outlive this caller or return an unknown result; neither ACK
	// nor a second coordinator may admit another assignment during that interval.
	if claimed && action == "submit" {
		if _, err := tx.ExecContext(ctx, `UPDATE seats SET status = 'BUSY', current_message_id = ? WHERE run_id = ? AND agent_id = ? AND generation = ? AND is_current = 1`, id, w.Seat.RunID, w.Seat.AgentID, target); err != nil {
			return w, false, err
		}
	}
	if phase != r.Delivery.Phase || reset != r.Delivery.ResetPhase || blocked != r.Coordination || target != r.Delivery.TargetGeneration {
		if _, err := appendJournal(ctx, tx, JournalEntry{RunID: w.Seat.RunID, MessageID: id, AgentID: w.Seat.AgentID, Generation: w.Seat.Generation, Event: event, Details: blocked}); err != nil {
			return w, false, err
		}
	}
	w, err = sessionWork(ctx, tx, id)
	if err != nil {
		return w, false, err
	}
	return w, claimed, tx.Commit()
}

// NoteSessionUnknown leaves the durable intent intact even if the native action
// may already have happened. It does not authorize another attempt.
func (s *Store) NoteSessionUnknown(ctx context.Context, id, action string) error {
	if action != "reset" && action != "submit" {
		return messagingError("INVALID_REQUEST", "session.action", "valid native action required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	w, err := sessionWork(ctx, tx, id)
	if err != nil {
		return err
	}
	if (action == "reset" && w.Record.Delivery.ResetPhase != "RESET_REQUESTED") || (action == "submit" && w.Record.Delivery.Phase != "SUBMIT_REQUESTED") || w.Record.Coordination == "UNVERIFIED" {
		return tx.Commit()
	}
	if _, err := tx.ExecContext(ctx, `UPDATE deliveries SET last_error_code = 'UNVERIFIED', updated_at = ? WHERE message_id = ?`, timestamp(time.Time{}), id); err != nil {
		return err
	}
	if _, err := appendJournal(ctx, tx, JournalEntry{RunID: w.Seat.RunID, MessageID: id, AgentID: w.Seat.AgentID, Generation: w.Seat.Generation, Event: "SESSION_UNVERIFIED", Details: action + " evidence missing or rejected; no automatic replay"}); err != nil {
		return err
	}
	return tx.Commit()
}

// ConfirmSessionReset is called only after runtime verifies independent READY
// against adapter native evidence. All bindings and delivery routing commit together.
func (s *Store) ConfirmSessionReset(ctx context.Context, id string, previous, next Seat) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	w, err := sessionWork(ctx, tx, id)
	if err != nil {
		return err
	}
	if gate, err := bindingGate(ctx, tx, w.Seat, true); err != nil {
		return err
	} else if gate != "" {
		return messagingError(gate, "reset.ready", "binding recovery or stop blocks confirmation")
	}
	if w.Record.Session != "fresh" || w.Record.Delivery.ResetPhase != "RESET_REQUESTED" || !sameSession(w.Seat, previous) || w.Record.Delivery.TargetGeneration != previous.Generation ||
		next.RunID != previous.RunID || next.AgentID != previous.AgentID || next.Generation != previous.Generation+1 || next.Tool != previous.Tool || next.NativeSessionID == "" || next.NativeSessionID == previous.NativeSessionID || next.WorkspaceID != previous.WorkspaceID || next.SurfaceID != previous.SurfaceID {
		return rejectLateResult(ctx, tx, MessageActor{RunID: previous.RunID, AgentID: previous.AgentID, Generation: previous.Generation}, id, "reset.ready", messagingError("GENERATION_MISMATCH", "reset.ready", "reset intent or new native binding mismatch"))
	}
	var reused bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM seats WHERE run_id = ? AND tool = ? AND native_session_id = ?)`, next.RunID, next.Tool, next.NativeSessionID).Scan(&reused); err != nil {
		return err
	}
	if reused {
		return messagingError("ACTOR_MISMATCH", "reset.native", "native session was already bound")
	}
	if _, err := tx.ExecContext(ctx, `UPDATE seats SET is_current = 0 WHERE run_id = ? AND agent_id = ? AND generation = ?`, previous.RunID, previous.AgentID, previous.Generation); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO seats (run_id, agent_id, generation, is_current, tool, native_session_id, workspace_id, surface_id, status, created_at) VALUES (?, ?, ?, 1, ?, ?, ?, ?, 'READY', ?)`, next.RunID, next.AgentID, next.Generation, next.Tool, next.NativeSessionID, next.WorkspaceID, next.SurfaceID, timestamp(time.Time{})); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE deliveries SET target_generation = ?, reset_phase = 'READY', phase = 'PENDING', last_error_code = '', updated_at = ? WHERE message_id = ?`, next.Generation, timestamp(time.Time{}), id); err != nil {
		return err
	}
	if _, err := appendJournal(ctx, tx, JournalEntry{RunID: next.RunID, MessageID: id, AgentID: next.AgentID, Generation: next.Generation, Event: "RESET_READY", Details: "new native binding and agent READY confirmed"}); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ConfirmSessionSubmit(ctx context.Context, id string, binding Seat, channel string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	w, err := sessionWork(ctx, tx, id)
	if err != nil {
		return err
	}
	if gate, err := bindingGate(ctx, tx, w.Seat, true); err != nil {
		return err
	} else if gate != "" {
		return messagingError(gate, "submit", "binding recovery or stop blocks confirmation")
	}
	if !sameSession(w.Seat, binding) || w.Record.Delivery.TargetGeneration != binding.Generation || w.Record.Delivery.Phase != "SUBMIT_REQUESTED" || channel == "" || channel == "UNVERIFIED" {
		return rejectLateResult(ctx, tx, MessageActor{RunID: binding.RunID, AgentID: binding.AgentID, Generation: binding.Generation}, id, "submit", messagingError("GENERATION_MISMATCH", "submit", "submission does not match current durable intent"))
	}
	if w.Seat.CurrentMessageID != "" && w.Seat.CurrentMessageID != id {
		return messagingError("SEAT_BUSY", "submit", "another assignment owns the native binding")
	}
	if _, err := tx.ExecContext(ctx, `UPDATE deliveries SET phase = 'SENT', channel_id = ?, last_error_code = '', updated_at = ? WHERE message_id = ?`, channel, timestamp(time.Time{}), id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE seats SET status = 'BUSY', current_message_id = ? WHERE run_id = ? AND agent_id = ? AND generation = ?`, id, binding.RunID, binding.AgentID, binding.Generation); err != nil {
		return err
	}
	if _, err := appendJournal(ctx, tx, JournalEntry{RunID: binding.RunID, MessageID: id, AgentID: binding.AgentID, Generation: binding.Generation, Event: "MESSAGE_SENT", Details: "adapter confirmed submission; receipt requires explicit ACK"}); err != nil {
		return err
	}
	return tx.Commit()
}
