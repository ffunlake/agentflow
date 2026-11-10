package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

type Message struct {
	Seq              int64     `json:"seq"`
	RunID            string    `json:"run_id"`
	ID               string    `json:"id"`
	Sender           string    `json:"sender"`
	SenderGeneration int64     `json:"sender_generation"` // zero means an external actor without a seat binding
	Recipient        string    `json:"recipient"`
	Kind             string    `json:"kind"`
	Body             string    `json:"body"`
	ReplyTo          string    `json:"reply_to"`
	RequestID        string    `json:"request_id"` // empty disables request deduplication
	Status           string    `json:"status"`
	ReportStatus     string    `json:"report_status"`
	CreatedAt        time.Time `json:"created_at"`
}

type Delivery struct {
	ID               int64
	RunID            string
	MessageID        string
	Recipient        string
	TargetGeneration int64
	Phase            string
	ResetPhase       string
	ChannelID        string
	Attempt          int
	NextRetryAt      time.Time // zero means no scheduled retry
	LastErrorCode    string
	LastError        string
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

type JournalEntry struct {
	Seq         int64
	RunID       string
	MessageID   string
	AgentID     string
	Generation  int64
	Event       string
	Details     string
	EvidenceRef string
	CreatedAt   time.Time
}

// Outbox includes one initial delivery and one event. Their run/message/recipient
// links and timestamps are set from Message by SaveOutbox, not caller overrides.
type Outbox struct {
	Message  Message
	Delivery Delivery
	Journal  JournalEntry
}

// SaveOutbox commits message, delivery and journal together before returning.
// A sender/request ID retry with identical body returns the original message,
// including its original routing and report fields. A changed body conflicts.
// BEGIN IMMEDIATE serializes deduplication even across independent Store handles.
func (s *Store) SaveOutbox(ctx context.Context, out Outbox) (Message, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Message{}, fmt.Errorf("begin outbox: %w", err)
	}
	defer tx.Rollback()
	m, err := saveOutbox(ctx, tx, out)
	if err != nil {
		return Message{}, err
	}
	if err := tx.Commit(); err != nil {
		return Message{}, fmt.Errorf("commit outbox: %w", err)
	}
	return m, nil
}

// saveOutbox also serves validated service writes inside their identity transaction.
func saveOutbox(ctx context.Context, tx transactionQuery, out Outbox) (Message, error) {
	m := out.Message
	var err error
	if m.RequestID != "" {
		if _, err := findAckRequest(ctx, tx, m.RunID, m.Sender, m.RequestID); err == nil {
			return Message{}, ErrIdempotencyConflict
		} else if !errors.Is(err, sql.ErrNoRows) {
			return Message{}, err
		}
		existing, err := scanMessage(tx.QueryRowContext(ctx, selectMessage+` WHERE run_id = ? AND sender = ? AND request_id = ?`, m.RunID, m.Sender, m.RequestID))
		if err == nil {
			if existing.Body != m.Body {
				return Message{}, ErrIdempotencyConflict
			}
			return existing, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return Message{}, fmt.Errorf("find outbox retry: %w", err)
		}
	}
	if m.ID == "" {
		m.ID = rand.Text()
	}
	m.Status = defaultString(m.Status, "STORED")
	created := timestamp(m.CreatedAt)
	m.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
	if err != nil {
		return Message{}, fmt.Errorf("invalid message creation time: %w", err)
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO messages
		(run_id, id, sender, sender_generation, recipient, kind, body, reply_to, request_id, status, report_status, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, m.RunID, m.ID, m.Sender,
		nullGeneration(m.SenderGeneration), m.Recipient, m.Kind, m.Body, nullString(m.ReplyTo),
		nullString(m.RequestID), m.Status, m.ReportStatus, created)
	if err != nil {
		return Message{}, fmt.Errorf("insert outbox message: %w", err)
	}
	m.Seq, err = result.LastInsertId()
	if err != nil {
		return Message{}, fmt.Errorf("read message sequence: %w", err)
	}
	d := out.Delivery
	var nextRetry any
	if !d.NextRetryAt.IsZero() {
		nextRetry = timestamp(d.NextRetryAt)
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO deliveries
		(run_id, message_id, recipient, target_generation, phase, reset_phase, channel_id,
		 attempt, next_retry_at, last_error_code, last_error, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, m.RunID, m.ID, m.Recipient,
		d.TargetGeneration, defaultString(d.Phase, "PENDING"), defaultString(d.ResetPhase, "NONE"),
		d.ChannelID, d.Attempt, nextRetry, d.LastErrorCode, d.LastError, created, created)
	if err != nil {
		return Message{}, fmt.Errorf("insert outbox delivery: %w", err)
	}
	j := out.Journal
	j.RunID, j.MessageID, j.CreatedAt = m.RunID, m.ID, m.CreatedAt
	j.Event = defaultString(j.Event, "MESSAGE_STORED")
	if _, err := appendJournal(ctx, tx, j); err != nil {
		return Message{}, fmt.Errorf("insert outbox journal: %w", err)
	}
	return m, nil
}

const selectMessage = `SELECT seq, run_id, id, sender, COALESCE(sender_generation, 0), recipient,
	kind, body, COALESCE(reply_to, ''), COALESCE(request_id, ''), status, report_status, created_at FROM messages`

type rowScanner interface{ Scan(...any) error }

func scanMessage(row rowScanner) (Message, error) {
	var m Message
	var created string
	if err := row.Scan(&m.Seq, &m.RunID, &m.ID, &m.Sender, &m.SenderGeneration, &m.Recipient,
		&m.Kind, &m.Body, &m.ReplyTo, &m.RequestID, &m.Status, &m.ReportStatus, &created); err != nil {
		return Message{}, err
	}
	var err error
	m.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
	return m, err
}

func (s *Store) GetMessage(ctx context.Context, id string) (Message, error) {
	return scanMessage(s.db.QueryRowContext(ctx, selectMessage+` WHERE id = ?`, id))
}

type journalWriter interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func appendJournal(ctx context.Context, writer journalWriter, j JournalEntry) (int64, error) {
	result, err := writer.ExecContext(ctx, `INSERT INTO journal
		(run_id, message_id, agent_id, generation, event, details, evidence_ref, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, j.RunID, nullString(j.MessageID), nullString(j.AgentID),
		nullGeneration(j.Generation), j.Event, j.Details, j.EvidenceRef, timestamp(j.CreatedAt))
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

// AppendJournal appends an event and returns its persisted order. There is no
// journal update/delete API; database triggers also reject those operations.
func (s *Store) AppendJournal(ctx context.Context, j JournalEntry) (int64, error) {
	seq, err := appendJournal(ctx, s.db, j)
	if err != nil {
		return 0, fmt.Errorf("append journal: %w", err)
	}
	return seq, nil
}

// JournalAfter reads events in append order, independent of their timestamps.
func (s *Store) JournalAfter(ctx context.Context, runID string, after int64) ([]JournalEntry, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT seq, run_id, COALESCE(message_id, ''),
		COALESCE(agent_id, ''), COALESCE(generation, 0), event, details, evidence_ref, created_at
		FROM journal WHERE run_id = ? AND seq > ? ORDER BY seq`, runID, after)
	if err != nil {
		return nil, fmt.Errorf("read journal: %w", err)
	}
	defer rows.Close()
	entries := make([]JournalEntry, 0)
	for rows.Next() {
		var j JournalEntry
		var created string
		if err := rows.Scan(&j.Seq, &j.RunID, &j.MessageID, &j.AgentID, &j.Generation,
			&j.Event, &j.Details, &j.EvidenceRef, &created); err != nil {
			return nil, err
		}
		j.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
		if err != nil {
			return nil, err
		}
		entries = append(entries, j)
	}
	return entries, rows.Err()
}

// GetDelivery exposes the saved outbox record without submitting to a channel.
func (s *Store) GetDelivery(ctx context.Context, messageID string, generation int64) (Delivery, error) {
	return getDelivery(ctx, s.db, messageID, generation)
}

type queryRower interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func getDelivery(ctx context.Context, q queryRower, messageID string, generation int64) (Delivery, error) {
	var d Delivery
	var next sql.NullString
	var created, updated string
	err := q.QueryRowContext(ctx, `SELECT id, run_id, message_id, recipient, target_generation,
		phase, reset_phase, channel_id, attempt, next_retry_at, last_error_code, last_error,
		created_at, updated_at FROM deliveries WHERE message_id = ? AND target_generation = ?`,
		messageID, generation).Scan(&d.ID, &d.RunID, &d.MessageID, &d.Recipient, &d.TargetGeneration,
		&d.Phase, &d.ResetPhase, &d.ChannelID, &d.Attempt, &next, &d.LastErrorCode, &d.LastError, &created, &updated)
	if err != nil {
		return Delivery{}, err
	}
	if d.CreatedAt, err = time.Parse(time.RFC3339Nano, created); err != nil {
		return Delivery{}, err
	}
	if d.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated); err != nil {
		return Delivery{}, err
	}
	if next.Valid {
		d.NextRetryAt, err = time.Parse(time.RFC3339Nano, next.String)
	}
	return d, err
}
