package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// MessagingError exposes stable service diagnostics without raw SQL or message bodies.
type MessagingError struct {
	Code    string
	Path    string
	Message string
}

func (e *MessagingError) Error() string { return e.Code + ": " + e.Path + ": " + e.Message }

func messagingError(code, path, message string) error {
	return &MessagingError{Code: code, Path: path, Message: message}
}

// MessageActor is always a registered current seat. External actor policy and
// authenticated transport binding belong to the future coordinator.
type MessageActor struct {
	RunID      string
	AgentID    string
	Generation int64
}

type MessageCommand struct {
	Actor            MessageActor
	RequestID        string
	Recipient        string
	TargetGeneration int64 // zero resolves the current recipient atomically
	Kind             string
	Body             string
	ReplyTo          string
	ReportStatus     string
	Session          string // assign only: fresh, persistent, or config; empty retains persistence-only API
}

type MessageRecord struct {
	Message      Message       `json:"message"`
	Delivery     DeliveryState `json:"delivery"`
	Session      string        `json:"session,omitempty"`
	Coordination string        `json:"coordination,omitempty"`
}

// DeliveryState deliberately separates explicit receipt from an unverified channel.
type DeliveryState struct {
	TargetGeneration int64  `json:"target_generation"`
	Phase            string `json:"phase"`
	ResetPhase       string `json:"reset_phase"`
	Channel          string `json:"channel"`
	Attempt          int    `json:"attempt"`
}

func deliveryState(d Delivery) DeliveryState {
	return DeliveryState{d.TargetGeneration, d.Phase, d.ResetPhase, d.ChannelID, d.Attempt}
}

func validateMessageActor(ctx context.Context, tx queryRower, a MessageActor) error {
	var runID string
	err := tx.QueryRowContext(ctx, `SELECT run_id FROM runs WHERE singleton = 1`).Scan(&runID)
	if errors.Is(err, sql.ErrNoRows) {
		return messagingError("RUN_NOT_FOUND", "actor.run_id", "run is not stored")
	}
	if err != nil {
		return err
	}
	if a.RunID != runID {
		return messagingError("ACTOR_MISMATCH", "actor.run_id", "run identity mismatch")
	}
	_, err = currentMessageSeat(ctx, tx, a.RunID, a.AgentID, a.Generation, "actor")
	return err
}

func currentMessageSeat(ctx context.Context, tx queryRower, runID, agentID string, generation int64, path string) (int64, error) {
	var current int64
	err := tx.QueryRowContext(ctx, `SELECT generation FROM seats WHERE run_id = ? AND agent_id = ? AND is_current = 1`, runID, agentID).Scan(&current)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, messagingError("ACTOR_MISMATCH", path+".agent_id", "unknown or unregistered agent")
	}
	if err != nil {
		return 0, err
	}
	if (path == "actor" && generation < 1) || (generation != 0 && generation != current) {
		return 0, messagingError("GENERATION_MISMATCH", path+".generation", "generation is not current")
	}
	return current, nil
}

// ValidateMessageActor is suitable for the IPC actor-validator hook. Each service
// operation repeats the check in its own transaction to close generation races.
func (s *Store) ValidateMessageActor(ctx context.Context, actor MessageActor) error {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := validateMessageActor(ctx, tx, actor); err != nil {
		return err
	}
	return tx.Commit()
}

// StoreMessage reuses SaveOutbox's transaction implementation, with run, current
// sender/recipient and reply authorization checked before committing any writes.
// Routing on a retry comes from the original record, per SaveOutbox's body contract.
func (s *Store) StoreMessage(ctx context.Context, c MessageCommand) (MessageRecord, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return MessageRecord{}, err
	}
	defer tx.Rollback()
	if err := validateMessageActor(ctx, tx, c.Actor); err != nil {
		if c.Kind == "reply" {
			return MessageRecord{}, rejectLateResult(ctx, tx, c.Actor, c.ReplyTo, "reply", err)
		}
		return MessageRecord{}, err
	}
	record, err := storeMessage(ctx, tx, c)
	if err != nil {
		return MessageRecord{}, err
	}
	return record, tx.Commit()
}

// storeMessage allows a structured workflow reply and its transition to share
// the existing message gates and a single transaction. Caller validates actor.
func storeMessage(ctx context.Context, tx transactionQuery, c MessageCommand) (MessageRecord, error) {
	if strings.TrimSpace(c.RequestID) == "" || (c.Kind != "assign" && c.Kind != "send" && c.Kind != "reply") || c.TargetGeneration < 0 ||
		(c.Session != "" && (c.Kind != "assign" || (c.Session != "fresh" && c.Session != "persistent" && c.Session != "config"))) ||
		(c.Kind == "reply" && c.ReplyTo == "") || (c.Kind != "reply" && (c.ReplyTo != "" || c.ReportStatus != "")) {
		return MessageRecord{}, messagingError("INVALID_REQUEST", "message", "request ID and valid message operation are required")
	}
	// Check the shared sender/request namespace before resolving routing. Still
	// validate the persisted route, so an old generation can never retry through.
	if _, err := findAckRequest(ctx, tx, c.Actor.RunID, c.Actor.AgentID, c.RequestID); err == nil {
		return MessageRecord{}, ErrIdempotencyConflict
	} else if !errors.Is(err, sql.ErrNoRows) {
		return MessageRecord{}, err
	}
	existing, err := scanMessage(tx.QueryRowContext(ctx, selectMessage+` WHERE run_id = ? AND sender = ? AND request_id = ?`, c.Actor.RunID, c.Actor.AgentID, c.RequestID))
	if err == nil {
		if existing.Body != c.Body {
			return MessageRecord{}, ErrIdempotencyConflict
		}
		if existing.SenderGeneration != c.Actor.Generation {
			return MessageRecord{}, messagingError("GENERATION_MISMATCH", "actor.generation", "request belongs to another sender generation")
		}
		// Coordinated assignments retain their original mode and may have moved
		// their delivery to a confirmed new generation. Retries never reselect it.
		record, err := sessionRecord(ctx, tx, existing)
		if err != nil {
			return MessageRecord{}, err
		}
		if record.Session != "" {
			return record, nil
		}
		c.Recipient, c.Kind, c.ReplyTo = existing.Recipient, existing.Kind, existing.ReplyTo
		c.Session = "" // legacy requests retain their original persistence-only contract
		var target int64
		if err := tx.QueryRowContext(ctx, `SELECT target_generation FROM deliveries WHERE message_id = ? ORDER BY id LIMIT 1`, existing.ID).Scan(&target); err != nil {
			return MessageRecord{}, err
		}
		c.TargetGeneration = target
	} else if !errors.Is(err, sql.ErrNoRows) {
		return MessageRecord{}, err
	}
	if c.Kind == "reply" {
		original, err := receivedMessage(ctx, tx, c.Actor, c.ReplyTo)
		if err != nil {
			return MessageRecord{}, err
		}
		if c.Recipient != "" && c.Recipient != original.Sender {
			return MessageRecord{}, messagingError("ACTOR_MISMATCH", "recipient.agent_id", "reply recipient must be original sender")
		}
		c.Recipient = original.Sender
		if c.TargetGeneration != 0 && c.TargetGeneration != original.SenderGeneration {
			return MessageRecord{}, messagingError("GENERATION_MISMATCH", "recipient.generation", "reply target differs from original sender generation")
		}
		c.TargetGeneration = original.SenderGeneration
	}
	target, err := currentMessageSeat(ctx, tx, c.Actor.RunID, c.Recipient, c.TargetGeneration, "recipient")
	if err != nil {
		return MessageRecord{}, err
	}
	details := "channel=UNVERIFIED; no submission or reset attempted"
	if c.Session != "" {
		if c.Session == "config" {
			var body string
			if err := tx.QueryRowContext(ctx, `SELECT config_snapshot FROM runs WHERE run_id = ?`, c.Actor.RunID).Scan(&body); err != nil {
				return MessageRecord{}, err
			}
			var frozen struct {
				Agents map[string]struct {
					Session string `json:"session"`
				} `json:"agents"`
			}
			if err := json.Unmarshal([]byte(body), &frozen); err != nil {
				return MessageRecord{}, messagingError("SESSION_CONFIG_UNAVAILABLE", "session", "frozen session configuration is invalid")
			}
			c.Session = frozen.Agents[c.Recipient].Session
			if c.Session != "fresh" && c.Session != "persistent" {
				return MessageRecord{}, messagingError("SESSION_CONFIG_UNAVAILABLE", "session", "recipient has no frozen session mode")
			}
		}
		data, _ := json.Marshal(map[string]string{"session": c.Session})
		details = string(data)
	}
	m, err := saveOutbox(ctx, tx, Outbox{
		Message: Message{RunID: c.Actor.RunID, Sender: c.Actor.AgentID, SenderGeneration: c.Actor.Generation,
			Recipient: c.Recipient, Kind: c.Kind, Body: c.Body, ReplyTo: c.ReplyTo, RequestID: c.RequestID, ReportStatus: c.ReportStatus},
		Delivery: Delivery{TargetGeneration: target, Phase: "PENDING", ResetPhase: "NONE", ChannelID: "UNVERIFIED"},
		Journal:  JournalEntry{AgentID: c.Actor.AgentID, Generation: c.Actor.Generation, Event: "MESSAGE_STORED", Details: details},
	})
	if err != nil {
		return MessageRecord{}, err
	}
	if m.Kind == "reply" && (m.ReportStatus == "DONE" || m.ReportStatus == "FAILED" || m.ReportStatus == "BLOCKED_CONFIRM") {
		if _, err := tx.ExecContext(ctx, `UPDATE seats SET status = 'READY', current_message_id = NULL WHERE run_id = ? AND agent_id = ? AND generation = ? AND current_message_id = ?`, c.Actor.RunID, c.Actor.AgentID, c.Actor.Generation, m.ReplyTo); err != nil {
			return MessageRecord{}, err
		}
	}
	record, err := sessionRecord(ctx, tx, m)
	if err != nil {
		return MessageRecord{}, err
	}
	return record, nil
}

func receivedMessage(ctx context.Context, tx queryRower, a MessageActor, id string) (Message, error) {
	m, err := scanMessage(tx.QueryRowContext(ctx, selectMessage+` WHERE run_id = ? AND id = ?`, a.RunID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Message{}, messagingError("MESSAGE_NOT_FOUND", "message.id", "message is not stored in this run")
	}
	if err != nil {
		return Message{}, err
	}
	if m.Recipient != a.AgentID {
		return Message{}, messagingError("ACTOR_MISMATCH", "message.recipient", "caller is not the message recipient")
	}
	if _, err := getDelivery(ctx, tx, id, a.Generation); errors.Is(err, sql.ErrNoRows) {
		return Message{}, messagingError("GENERATION_MISMATCH", "message.target_generation", "message targets another generation")
	} else if err != nil {
		return Message{}, err
	}
	record, err := sessionRecord(ctx, tx, m)
	if err != nil {
		return Message{}, err
	}
	if record.Delivery.Channel == "cmux-agent-message" {
		var confirmed bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM journal WHERE run_id=? AND message_id=? AND event='CMUX_SEND_RESULT' AND json_extract(details,'$.id')!='' AND json_extract(details,'$.exit_code')=0 AND json_extract(details,'$.truncated')=0 AND json_extract(details,'$.source')='agentflow' AND
			(json_extract(details,'$.body')=? OR (EXISTS(SELECT 1 FROM journal w WHERE w.message_id=? AND w.event='WORKFLOW_DISPATCH_STORED') AND
			CASE WHEN json_valid(json_extract(details,'$.body')) THEN json_extract(json_extract(details,'$.body'),'$.type')='workflow_dispatch' AND json_extract(json_extract(details,'$.body'),'$.message_id')=? AND json_extract(json_extract(details,'$.body'),'$.workflow')=json(?) ELSE 0 END)))`, m.RunID, m.ID, m.Body, m.ID, m.ID, m.Body).Scan(&confirmed); err != nil {
			return Message{}, err
		}
		if !confirmed || (record.Delivery.Phase != "CMUX_QUEUED" && record.Delivery.Phase != "ACKED") {
			return Message{}, messagingError("CMUX_NOT_SENT", "delivery.phase", "cmux message has no confirmed durable native queue receipt")
		}
	}
	if record.Session == "fresh" && record.Delivery.ResetPhase != "READY" {
		return Message{}, messagingError("SESSION_NOT_READY", "delivery.reset_phase", "fresh assignment has no confirmed READY")
	}
	if record.Session != "" {
		// Persistence-only messaging can record receipts without invoking a
		// native adapter. Once a native submit intent exists, however, neither
		// ACK nor a result may turn an unconfirmed action into completed work.
		var started bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM journal WHERE run_id = ? AND message_id = ? AND event IN ('SUBMIT_REQUESTED', 'MESSAGE_SENT'))`, m.RunID, m.ID).Scan(&started); err != nil {
			return Message{}, err
		}
		confirmed, err := confirmedSessionSubmission(ctx, tx, m, record.Delivery)
		if err != nil {
			return Message{}, err
		}
		if (started || record.Delivery.Attempt != 0 || record.Delivery.Phase == "SUBMIT_REQUESTED") && !confirmed {
			return Message{}, messagingError("SESSION_NOT_SENT", "delivery.phase", "coordinated assignment has no matching durable SENT confirmation")
		}
	}
	return m, nil
}

type InboxQuery struct {
	Actor   MessageActor
	After   int64
	Pending bool
}

// Inbox reads one consistent view and filters by exact recipient generation.
func (s *Store) Inbox(ctx context.Context, q InboxQuery) ([]MessageRecord, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err := validateMessageActor(ctx, tx, q.Actor); err != nil {
		return nil, err
	}
	if q.After < 0 {
		return nil, messagingError("INVALID_REQUEST", "inbox.after", "sequence must be nonnegative")
	}
	rows, err := tx.QueryContext(ctx, selectMessage+` WHERE run_id = ? AND recipient = ? AND seq > ?
		AND EXISTS (SELECT 1 FROM deliveries d WHERE d.message_id = messages.id AND d.target_generation = ? AND (? = 0 OR d.phase != 'ACKED')) ORDER BY seq`,
		q.Actor.RunID, q.Actor.AgentID, q.After, q.Actor.Generation, q.Pending)
	if err != nil {
		return nil, err
	}
	messages := []Message{}
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		messages = append(messages, m)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	result := make([]MessageRecord, 0, len(messages))
	for _, m := range messages {
		record, err := sessionRecord(ctx, tx, m)
		if err != nil {
			return nil, err
		}
		result = append(result, record)
	}
	return result, tx.Commit()
}

type AckResult struct {
	RunID      string    `json:"run_id"`
	MessageID  string    `json:"message_id"`
	AgentID    string    `json:"agent_id"`
	Generation int64     `json:"generation"`
	Status     string    `json:"status"`
	JournalSeq int64     `json:"journal_seq"`
	RecordedAt time.Time `json:"recorded_at"`
}

const stopReceipt = "\x00stop"
const workflowReceiptID = "\x00workflow"

// ACK and stop receipts live in the existing append-only journal; immediate transactions
// serialize their shared sender/request namespace with outbox message writes.
func findAckRequest(ctx context.Context, tx queryRower, runID, sender, requestID string) (string, error) {
	var id string
	err := tx.QueryRowContext(ctx, `SELECT CASE WHEN event = 'STOP_REQUEST_ACCEPTED' THEN ? WHEN event = 'WORKFLOW_ADVANCED' THEN ? ELSE message_id END FROM journal WHERE run_id = ? AND agent_id = ?
		AND ((event IN ('MESSAGE_ACKED', 'MESSAGE_ACK_RECORDED', 'STOP_REQUEST_ACCEPTED') AND json_extract(details, '$.request_id') = ?)
		OR (event = 'WORKFLOW_ADVANCED' AND json_extract(details, '$.event.id') = ?)) ORDER BY seq LIMIT 1`, stopReceipt, workflowReceiptID, runID, sender, requestID, requestID).Scan(&id)
	return id, err
}

// Ack confirms receipt only, never completion. Distinct request IDs for an
// already ACKED message receive a journal receipt but no second state transition.
func (s *Store) Ack(ctx context.Context, a MessageActor, requestID, messageID string) (AckResult, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AckResult{}, err
	}
	defer tx.Rollback()
	if err := validateMessageActor(ctx, tx, a); err != nil {
		return AckResult{}, rejectLateResult(ctx, tx, a, messageID, "ack", err)
	}
	if strings.TrimSpace(requestID) == "" || messageID == "" {
		return AckResult{}, messagingError("INVALID_REQUEST", "ack", "request ID and message ID are required")
	}
	var existing string
	err = tx.QueryRowContext(ctx, `SELECT id FROM messages WHERE run_id = ? AND sender = ? AND request_id = ?`, a.RunID, a.AgentID, requestID).Scan(&existing)
	if err == nil {
		return AckResult{}, ErrIdempotencyConflict
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return AckResult{}, err
	}
	previous, err := findAckRequest(ctx, tx, a.RunID, a.AgentID, requestID)
	if err == nil && previous != messageID {
		return AckResult{}, ErrIdempotencyConflict
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return AckResult{}, err
	}
	if _, err := receivedMessage(ctx, tx, a, messageID); err != nil {
		return AckResult{}, err
	}
	d, err := getDelivery(ctx, tx, messageID, a.Generation)
	if err != nil {
		return AckResult{}, err
	}
	if previous == "" {
		event := "MESSAGE_ACK_RECORDED"
		if d.Phase != "ACKED" {
			event = "MESSAGE_ACKED"
			if _, err := tx.ExecContext(ctx, `UPDATE deliveries SET phase = 'ACKED', updated_at = ? WHERE id = ?`, timestamp(time.Time{}), d.ID); err != nil {
				return AckResult{}, err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE messages SET status = 'ACKED' WHERE id = ? AND run_id = ?`, messageID, a.RunID); err != nil {
				return AckResult{}, err
			}
		}
		details, _ := json.Marshal(struct {
			RequestID string `json:"request_id"`
		}{requestID})
		if _, err := appendJournal(ctx, tx, JournalEntry{RunID: a.RunID, MessageID: messageID, AgentID: a.AgentID, Generation: a.Generation, Event: event, Details: string(details)}); err != nil {
			return AckResult{}, err
		}
	}
	r := AckResult{RunID: a.RunID, MessageID: messageID, AgentID: a.AgentID, Generation: a.Generation, Status: "ACKED"}
	var created string
	if err := tx.QueryRowContext(ctx, `SELECT seq, created_at FROM journal WHERE run_id = ? AND message_id = ? AND agent_id = ? AND generation = ? AND event = 'MESSAGE_ACKED' ORDER BY seq LIMIT 1`, a.RunID, messageID, a.AgentID, a.Generation).Scan(&r.JournalSeq, &created); err != nil {
		return AckResult{}, err
	}
	r.RecordedAt, err = time.Parse(time.RFC3339Nano, created)
	if err != nil {
		return AckResult{}, err
	}
	return r, tx.Commit()
}
