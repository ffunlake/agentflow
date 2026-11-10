package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"agentflow/internal/loopcore"
)

// CMUXWorkflowReceipt binds an accepted inbox observation to one atomic workflow
// reply/cursor/outbox transaction. It is evidence in the existing journal.
type CMUXWorkflowReceipt struct {
	Inbox     SurfaceRegistration `json:"inbox"`
	Event     CMUXEvidence        `json:"event"`
	RequestID string              `json:"request_id"`
}

// BeginCMUXDispatch claims the existing workflow outbox, never creates another
// message or changes its request ID. A claimed/unknown send is never reclaimed.
func (s *Store) BeginCMUXDispatch(ctx context.Context, a MessageActor, id string) (_ CMUXTransportWork, _ bool, resultErr error) {
	tx, err := s.beginImmediate(ctx)
	if err != nil {
		return CMUXTransportWork{}, false, err
	}
	defer tx.finish(&resultErr)
	w := CMUXTransportWork{}
	source, err := surfaceBinding(ctx, tx, a)
	if err != nil {
		return w, false, err
	}
	m, err := scanMessage(tx.QueryRowContext(ctx, selectMessage+` WHERE run_id=? AND id=?`, a.RunID, id))
	if err != nil {
		return w, false, err
	}
	if m.Sender != a.AgentID || m.SenderGeneration != a.Generation || m.Kind != "assign" {
		return w, false, messagingError("ACTOR_MISMATCH", "workflow.dispatch", "dispatch sender differs from current actor")
	}
	current, err := workflowCursor(ctx, tx, a.RunID)
	if err != nil {
		return w, false, err
	}
	workflow, err := workflowDispatch(ctx, tx, id)
	if err != nil {
		return w, false, err
	}
	if !workflow || current.DispatchID != id || (current.Phase != loopcore.Executing && current.Phase != loopcore.Reviewing) {
		return w, false, loopcore.ErrTransition
	}
	w.Record, err = sessionRecord(ctx, tx, m)
	if err != nil {
		return w, false, err
	}
	if w.Record.Session != "" {
		return w, false, messagingError("INVALID_REQUEST", "workflow.transport", "cmux workflow cannot consume a native session dispatch")
	}
	w.Binding, err = surfaceBinding(ctx, tx, MessageActor{a.RunID, m.Recipient, w.Record.Delivery.TargetGeneration})
	if err != nil {
		return w, false, err
	}
	wire, err := json.Marshal(struct {
		V         int                 `json:"v"`
		Type      string              `json:"type"`
		RequestID string              `json:"request_id"`
		MessageID string              `json:"message_id"`
		Sender    SurfaceRegistration `json:"sender"`
		Recipient SurfaceRegistration `json:"recipient"`
		Workflow  json.RawMessage     `json:"workflow"`
	}{1, "workflow_dispatch", m.RequestID, m.ID, source, w.Binding, json.RawMessage(m.Body)})
	if err != nil {
		return w, false, err
	}
	w.WireBody = string(wire)
	if len(wire) > 32*1024 {
		return w, false, messagingError("INVALID_REQUEST", "workflow.dispatch", "cmux workflow envelope exceeds 32 KiB")
	}
	if w.Record.Delivery.Attempt != 0 {
		return w, false, tx.Commit(ctx)
	}
	if w.Record.Delivery.Phase != "PENDING" || w.Record.Delivery.Channel != "UNVERIFIED" {
		return w, false, loopcore.ErrTransition
	}
	if _, err := tx.ExecContext(ctx, `UPDATE deliveries SET phase='CMUX_SEND_REQUESTED',attempt=1,channel_id='cmux-agent-message',updated_at=? WHERE message_id=?`, timestamp(time.Time{}), id); err != nil {
		return w, false, err
	}
	data, _ := json.Marshal(w.Binding)
	if _, err := appendJournal(ctx, tx, JournalEntry{RunID: a.RunID, MessageID: id, AgentID: a.AgentID, Generation: a.Generation, Event: "CMUX_SEND_REQUESTED", Details: string(data)}); err != nil {
		return w, false, err
	}
	w.Record.Delivery.Phase, w.Record.Delivery.Attempt, w.Record.Delivery.Channel = "CMUX_SEND_REQUESTED", 1, "cmux-agent-message"
	return w, true, tx.Commit(ctx)
}

func (s *Store) ReplyCMUXWorkflow(ctx context.Context, reply MessageCommand, event loopcore.Event, native CMUXWorkflowReceipt) (WorkflowResult, error) {
	status := map[string]string{"handoff": "DONE", "approve": "DONE", "changes": "DONE", "retry": "FAILED", "blocked": "BLOCKED_CONFIRM"}[event.Kind]
	if status == "" || reply.ReportStatus != status || reply.Kind != "reply" || reply.RequestID != event.ID || reply.Actor.AgentID != event.Actor || reply.ReplyTo != event.MessageID || native.RequestID != event.ID {
		return WorkflowResult{}, loopcore.ErrTransition
	}
	return s.advanceWorkflowBound(ctx, reply.Actor, event, &reply, false, &native)
}

func validateCMUXWorkflow(ctx context.Context, tx queryRower, a MessageActor, event loopcore.Event, reply *MessageCommand, native CMUXWorkflowReceipt) error {
	if reply == nil {
		return loopcore.ErrTransition
	}
	b, err := surfaceBinding(ctx, tx, MessageActor{native.Inbox.RunID, native.Inbox.AgentID, native.Inbox.Generation})
	if err != nil {
		return err
	}
	source, err := surfaceBinding(ctx, tx, a)
	if err != nil {
		return err
	}
	e := native.Event
	// Keep the adapter's parsed event tied to the exact native text even at the
	// transactional API boundary. The adapter additionally enforces exact keys,
	// duplicates, nulls and the complete strict wire schema.
	var payload struct {
		V         int    `json:"v"`
		Type      string `json:"type"`
		RequestID string `json:"request_id"`
		Actor     struct {
			RunID      string `json:"runId"`
			AgentID    string `json:"agentId"`
			Generation int64  `json:"generation"`
		} `json:"actor"`
		MessageID string         `json:"message_id"`
		Body      string         `json:"body"`
		Status    string         `json:"status"`
		Workflow  loopcore.Event `json:"workflow"`
	}
	if json.Unmarshal([]byte(e.Body), &payload) != nil || payload.V != 1 || payload.Type != "workflow_reply" || payload.RequestID != event.ID || payload.MessageID != event.MessageID || payload.Body != reply.Body || payload.Status != reply.ReportStatus || (MessageActor{payload.Actor.RunID, payload.Actor.AgentID, payload.Actor.Generation}) != a {
		return messagingError("CMUX_EVENT_REJECTED", "workflow.inbox", "workflow reply differs from native message body")
	}
	payload.Workflow.ID, payload.Workflow.Actor, payload.Workflow.MessageID = event.ID, event.Actor, event.MessageID
	want, _ := json.Marshal(event)
	got, _ := json.Marshal(payload.Workflow)
	if string(want) != string(got) {
		return messagingError("CMUX_EVENT_REJECTED", "workflow.inbox", "workflow event differs from native message body")
	}
	if b != native.Inbox || e.ID == "" || e.Source != a.AgentID || !strings.EqualFold(e.SourceSurface, source.SurfaceID) || !strings.EqualFold(e.SourceWorkspace, source.WorkspaceID) || !strings.EqualFold(e.TargetSurface, b.SurfaceID) || !strings.EqualFold(e.TargetWorkspace, b.WorkspaceID) || (e.State != "queued" && e.State != "delivered" && e.State != "read") || e.ExitCode != 0 || e.Truncated {
		return messagingError("CMUX_EVENT_REJECTED", "workflow.inbox", "workflow source or target differs from current binding")
	}
	var accepted string
	if err := tx.QueryRowContext(ctx, `SELECT details FROM journal WHERE run_id=? AND agent_id=? AND generation=? AND event='CMUX_INBOX_EVENT' AND json_extract(details,'$.id')=?`, a.RunID, b.AgentID, b.Generation, e.ID).Scan(&accepted); err != nil {
		return err
	}
	var old CMUXEvidence
	if json.Unmarshal([]byte(accepted), &old) != nil || old.Source != e.Source || old.Body != e.Body || !strings.EqualFold(old.SourceSurface, e.SourceSurface) || !strings.EqualFold(old.SourceWorkspace, e.SourceWorkspace) || !strings.EqualFold(old.TargetSurface, e.TargetSurface) || !strings.EqualFold(old.TargetWorkspace, e.TargetWorkspace) {
		return messagingError("CMUX_EVENT_DUPLICATE", "workflow.inbox", "workflow differs from accepted native evidence")
	}
	var applied string
	err = tx.QueryRowContext(ctx, `SELECT json_extract(details,'$.request_id') FROM journal WHERE run_id=? AND event='CMUX_WORKFLOW_APPLIED' AND json_extract(details,'$.event.id')=?`, a.RunID, e.ID).Scan(&applied)
	if err == nil && applied != event.ID {
		return ErrIdempotencyConflict
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	m, err := receivedMessage(ctx, tx, a, event.MessageID)
	if err != nil {
		return err
	}
	if m.Sender != b.AgentID || m.SenderGeneration != b.Generation || m.RunID != b.RunID {
		return messagingError("ACTOR_MISMATCH", "workflow.inbox", "workflow result must target the original current sender")
	}
	r, err := sessionRecord(ctx, tx, m)
	if err != nil {
		return err
	}
	if r.Delivery.Channel != "cmux-agent-message" {
		return messagingError("CMUX_NOT_SENT", "workflow.dispatch", "workflow result requires cmux dispatch evidence")
	}
	return nil
}
