package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"agentflow/internal/config"
	"agentflow/internal/loopcore"
)

const workflowAdvanced = "WORKFLOW_ADVANCED"

type WorkflowResult struct {
	Cursor     loopcore.Cursor `json:"cursor"`
	Message    *Message        `json:"message,omitempty"`
	Reply      *MessageRecord  `json:"reply,omitempty"`
	JournalSeq int64           `json:"journal_seq"`
}

type workflowReceipt struct {
	Actor  MessageActor    `json:"actor"`
	Event  loopcore.Event  `json:"event"`
	Reply  *MessageCommand `json:"reply,omitempty"`
	Result WorkflowResult  `json:"result"`
}

// WorkflowCursor reconstructs the materialized cursor from the last committed
// workflow journal record. No extra mutable table or migration is necessary.
func (s *Store) WorkflowCursor(ctx context.Context, runID string) (loopcore.Cursor, error) {
	return workflowCursor(ctx, s.db, runID)
}

func workflowCursor(ctx context.Context, q queryRower, runID string) (loopcore.Cursor, error) {
	var body string
	err := q.QueryRowContext(ctx, `SELECT details FROM journal WHERE run_id = ? AND event = ? ORDER BY seq DESC LIMIT 1`, runID, workflowAdvanced).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		return loopcore.Cursor{}, nil
	}
	if err != nil {
		return loopcore.Cursor{}, err
	}
	var receipt workflowReceipt
	if err := json.Unmarshal([]byte(body), &receipt); err != nil {
		return loopcore.Cursor{}, err
	}
	return receipt.Result.Cursor, nil
}

// AdvanceWorkflow is one explicit tick, never a native submission. It freezes
// identity, revision, stop intent, event receipt, cursor and outbox under the same
// BEGIN IMMEDIATE transaction. Exact event retries return the original result.
func (s *Store) AdvanceWorkflow(ctx context.Context, actor MessageActor, event loopcore.Event) (WorkflowResult, error) {
	return s.advanceWorkflow(ctx, actor, event, nil, false)
}

// AdvanceWorkflowWithSessions opts new dispatches into the existing session
// ledger in the same transaction as the cursor. It performs no native action.
func (s *Store) AdvanceWorkflowWithSessions(ctx context.Context, actor MessageActor, event loopcore.Event) (WorkflowResult, error) {
	return s.advanceWorkflow(ctx, actor, event, nil, true)
}

// ReplyWorkflow commits an ordinary reply and its explicit workflow event
// atomically. A plain reply never advances a workflow and no reply implies ACK.
func (s *Store) ReplyWorkflow(ctx context.Context, reply MessageCommand, event loopcore.Event) (WorkflowResult, error) {
	return s.replyWorkflow(ctx, reply, event, false)
}

// ReplyWorkflowWithSessions retains atomic reply/cursor/outbox persistence while
// selecting the next recipient's frozen session policy for an injected runner.
func (s *Store) ReplyWorkflowWithSessions(ctx context.Context, reply MessageCommand, event loopcore.Event) (WorkflowResult, error) {
	return s.replyWorkflow(ctx, reply, event, true)
}

func (s *Store) replyWorkflow(ctx context.Context, reply MessageCommand, event loopcore.Event, sessions bool) (WorkflowResult, error) {
	if reply.Kind != "reply" || reply.RequestID != event.ID || reply.Actor.AgentID != event.Actor || reply.ReplyTo != event.MessageID || event.Kind == "start" || event.Kind == "resume" {
		return WorkflowResult{}, loopcore.ErrTransition
	}
	// Advancing without a matching terminal report would leave the reporting
	// seat BUSY on superseded work and can block the next dispatch. Reject it
	// before the reply, cursor or dispatch transaction can write anything.
	status := ""
	switch event.Kind {
	case "handoff", "approve", "changes":
		status = "DONE"
	case "retry":
		status = "FAILED"
	case "blocked":
		status = "BLOCKED_CONFIRM"
	}
	if status == "" || reply.ReportStatus != status {
		return WorkflowResult{}, loopcore.ErrTransition
	}
	return s.advanceWorkflow(ctx, reply.Actor, event, &reply, sessions)
}

func (s *Store) advanceWorkflow(ctx context.Context, actor MessageActor, event loopcore.Event, reply *MessageCommand, sessions bool) (WorkflowResult, error) {
	return s.advanceWorkflowBound(ctx, actor, event, reply, sessions, nil)
}

func (s *Store) advanceWorkflowBound(ctx context.Context, actor MessageActor, event loopcore.Event, reply *MessageCommand, sessions bool, native *CMUXWorkflowReceipt) (_ WorkflowResult, resultErr error) {
	tx, err := s.beginImmediate(ctx)
	if err != nil {
		return WorkflowResult{}, err
	}
	defer tx.finish(&resultErr)
	if err := validateMessageActor(ctx, tx, actor); err != nil {
		return WorkflowResult{}, err
	}
	if native != nil {
		if err := validateCMUXWorkflow(ctx, tx, actor, event, reply, *native); err != nil {
			return WorkflowResult{}, err
		}
	}
	if event.Actor != actor.AgentID {
		return WorkflowResult{}, loopcore.ErrTransition
	}
	var body string
	var seq int64
	err = tx.QueryRowContext(ctx, `SELECT seq, details FROM journal WHERE run_id = ? AND event = ? AND json_extract(details, '$.event.id') = ? ORDER BY seq LIMIT 1`, actor.RunID, workflowAdvanced, event.ID).Scan(&seq, &body)
	if err == nil {
		var saved workflowReceipt
		if err := json.Unmarshal([]byte(body), &saved); err != nil {
			return WorkflowResult{}, err
		}
		want, _ := json.Marshal(event)
		got, _ := json.Marshal(saved.Event)
		wantReply, _ := json.Marshal(reply)
		gotReply, _ := json.Marshal(saved.Reply)
		if string(want) != string(got) || saved.Actor != actor || string(wantReply) != string(gotReply) {
			return WorkflowResult{}, ErrIdempotencyConflict
		}
		saved.Result.JournalSeq = seq
		return saved.Result, tx.Commit(ctx)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return WorkflowResult{}, err
	}
	// Workflow events share the existing caller/request namespace with messages,
	// ACKs and stop. Check before writing the optional reply with that same ID.
	var occupied string
	err = tx.QueryRowContext(ctx, `SELECT id FROM messages WHERE run_id = ? AND sender = ? AND request_id = ?`, actor.RunID, actor.AgentID, event.ID).Scan(&occupied)
	if err == nil {
		return WorkflowResult{}, ErrIdempotencyConflict
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return WorkflowResult{}, err
	}
	if _, err := findAckRequest(ctx, tx, actor.RunID, actor.AgentID, event.ID); err == nil {
		return WorkflowResult{}, ErrIdempotencyConflict
	} else if !errors.Is(err, sql.ErrNoRows) {
		return WorkflowResult{}, err
	}
	run, err := scanRun(tx.QueryRowContext(ctx, selectRun))
	if err != nil {
		return WorkflowResult{}, err
	}
	if run.StopRequested {
		return WorkflowResult{}, messagingError("RUN_STOPPED", "workflow", "stop intent prevents workflow advancement")
	}
	var frozen config.Resolved
	if err := json.Unmarshal([]byte(run.ConfigSnapshot), &frozen); err != nil {
		return WorkflowResult{}, err
	}
	digest := frozen.Digest
	frozen.Digest = ""
	encoded, err := json.Marshal(frozen)
	if err != nil {
		return WorkflowResult{}, err
	}
	sum := sha256.Sum256(encoded)
	if digest == "" || digest != run.ConfigDigest || digest != hex.EncodeToString(sum[:]) {
		return WorkflowResult{}, errors.New("WORKFLOW_CONFIG_MISMATCH")
	}
	ids := make([]string, 0, len(frozen.Agents))
	for id := range frozen.Agents {
		ids = append(ids, id)
	}
	if _, ok := frozen.Agents[frozen.Lead]; !ok {
		return WorkflowResult{}, errors.New("WORKFLOW_CONFIG_MISMATCH")
	}
	if err := config.ValidateWorkflow(frozen.Workflow, ids, frozen.Lead); err != nil {
		return WorkflowResult{}, err
	}
	current, err := workflowCursor(ctx, tx, actor.RunID)
	if err != nil {
		return WorkflowResult{}, err
	}
	// A result from a prior native generation cannot satisfy a current dispatch.
	if event.Kind != "start" && event.Kind != "resume" {
		if event.MessageID != current.DispatchID {
			return WorkflowResult{}, loopcore.ErrTransition
		}
		if _, err := receivedMessage(ctx, tx, actor, event.MessageID); err != nil {
			return WorkflowResult{}, err
		}
	}
	next, intent, err := loopcore.Advance(frozen.Workflow, frozen.Lead, current, event)
	if err != nil {
		return WorkflowResult{}, err
	}
	result := WorkflowResult{Cursor: next}
	if reply != nil {
		record, err := storeMessage(ctx, tx, *reply)
		if err != nil {
			return WorkflowResult{}, err
		}
		result.Reply = &record
	}
	if intent != nil {
		leadGeneration, err := currentMessageSeat(ctx, tx, actor.RunID, frozen.Lead, 0, "recipient")
		if err != nil {
			return WorkflowResult{}, err
		}
		target, err := currentMessageSeat(ctx, tx, actor.RunID, intent.Recipient, 0, "recipient")
		if err != nil {
			return WorkflowResult{}, err
		}
		payload, err := json.Marshal(intent)
		if err != nil {
			return WorkflowResult{}, err
		}
		requestID := fmt.Sprintf("workflow:%d", next.Revision)
		if actor.AgentID == frozen.Lead && event.ID == requestID {
			return WorkflowResult{}, ErrIdempotencyConflict
		}
		if _, err := findAckRequest(ctx, tx, actor.RunID, frozen.Lead, requestID); err == nil {
			return WorkflowResult{}, ErrIdempotencyConflict
		} else if !errors.Is(err, sql.ErrNoRows) {
			return WorkflowResult{}, err
		}
		var occupied string
		err = tx.QueryRowContext(ctx, `SELECT id FROM messages WHERE run_id = ? AND sender = ? AND request_id = ?`, actor.RunID, frozen.Lead, requestID).Scan(&occupied)
		if err == nil {
			return WorkflowResult{}, ErrIdempotencyConflict
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return WorkflowResult{}, err
		}
		details := ""
		if sessions {
			mode := frozen.Agents[intent.Recipient].Session
			if mode != "fresh" && mode != "persistent" {
				return WorkflowResult{}, messagingError("SESSION_CONFIG_UNAVAILABLE", "workflow.session", "recipient has no frozen session mode")
			}
			data, _ := json.Marshal(map[string]string{"session": mode})
			details = string(data)
		}
		m, err := saveOutbox(ctx, tx, Outbox{
			Message: Message{RunID: actor.RunID, Sender: frozen.Lead, SenderGeneration: leadGeneration, Recipient: intent.Recipient,
				Kind: "assign", Body: string(payload), RequestID: requestID},
			Delivery: Delivery{TargetGeneration: target, Phase: "PENDING", ChannelID: "UNVERIFIED"},
			Journal:  JournalEntry{AgentID: actor.AgentID, Generation: actor.Generation, Event: "WORKFLOW_DISPATCH_STORED", Details: details},
		})
		if err != nil {
			return WorkflowResult{}, err
		}
		result.Cursor.DispatchID, result.Message = m.ID, &m
	}
	receipt, err := json.Marshal(workflowReceipt{Actor: actor, Event: event, Reply: reply, Result: result})
	if err != nil {
		return WorkflowResult{}, err
	}
	result.JournalSeq, err = appendJournal(ctx, tx, JournalEntry{RunID: actor.RunID, AgentID: actor.AgentID, Generation: actor.Generation,
		Event: workflowAdvanced, Details: string(receipt)})
	if err != nil {
		return WorkflowResult{}, err
	}
	if native != nil {
		data, _ := json.Marshal(native)
		if _, err := appendJournal(ctx, tx, JournalEntry{RunID: actor.RunID, AgentID: actor.AgentID, Generation: actor.Generation,
			MessageID: event.MessageID, Event: "CMUX_WORKFLOW_APPLIED", Details: string(data)}); err != nil {
			return WorkflowResult{}, err
		}
	}
	return result, tx.Commit(ctx)
}

// workflowDispatch identifies outbox entries whose revision and generation must
// remain fenced even when ordinary untouched assignments could be relocated.
func workflowDispatch(ctx context.Context, tx queryRower, id string) (bool, error) {
	var found bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM journal WHERE message_id = ? AND event = 'WORKFLOW_DISPATCH_STORED')`, id).Scan(&found)
	return found, err
}
