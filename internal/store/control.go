package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"time"
)

// OpenExisting never creates or migrates a database. Read-only access does not
// change journal mode or configuration; writes retain the normal durability settings.
func OpenExisting(path string, writable bool) (*Store, error) {
	if path == "" || path == ":memory:" {
		return nil, errors.New("store requires an existing on-disk path")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	u := url.URL{Scheme: "file", Path: abs}
	q := url.Values{"mode": {"ro"}, "_pragma": {"busy_timeout(5000)", "foreign_keys(ON)"}}
	if writable {
		q.Set("mode", "rw")
		q.Add("_pragma", "synchronous(FULL)")
		q.Set("_txlock", "immediate")
	}
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	var version int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		db.Close()
		return nil, err
	}
	if version != schemaVersion {
		db.Close()
		return nil, fmt.Errorf("unsupported schema version %d (expected %d)", version, schemaVersion)
	}
	return &Store{db: db}, nil
}

// ReadState reads the singleton and current generations from one SQLite view.
func (s *Store) ReadState(ctx context.Context) (Run, []Seat, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return Run{}, nil, err
	}
	defer tx.Rollback()
	r, err := scanRun(tx.QueryRowContext(ctx, selectRun))
	if err != nil {
		return Run{}, nil, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT run_id, agent_id, generation, is_current, tool,
		native_session_id, workspace_id, surface_id, status, COALESCE(current_message_id, ''), created_at
		FROM seats WHERE run_id = ? AND is_current = 1 ORDER BY agent_id`, r.RunID)
	if err != nil {
		return Run{}, nil, err
	}
	seats := []Seat{}
	for rows.Next() {
		var seat Seat
		var created string
		if err := rows.Scan(&seat.RunID, &seat.AgentID, &seat.Generation, &seat.Current, &seat.Tool,
			&seat.NativeSessionID, &seat.WorkspaceID, &seat.SurfaceID, &seat.Status, &seat.CurrentMessageID, &created); err != nil {
			rows.Close()
			return Run{}, nil, err
		}
		seat.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
		if err != nil {
			rows.Close()
			return Run{}, nil, err
		}
		seats = append(seats, seat)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return Run{}, nil, err
	}
	return r, seats, tx.Commit()
}

const StopRequested = "STOP_REQUESTED"

type StopIntent struct {
	RunID      string    `json:"run_id"`
	Status     string    `json:"status"`
	JournalSeq int64     `json:"journal_seq"`
	RecordedAt time.Time `json:"recorded_at"`
}

// RequestStop atomically records one run-wide intent and event. BEGIN IMMEDIATE
// also serializes retries across separate connections/processes. No lifecycle action occurs.
func (s *Store) RequestStop(ctx context.Context, runID string) (StopIntent, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return StopIntent{}, err
	}
	defer tx.Rollback()
	intent, err := requestStop(ctx, tx, runID)
	if err != nil {
		return StopIntent{}, err
	}
	return intent, tx.Commit()
}

// RequestStopAsActor validates current identity and durable request namespace in
// the same transaction as the stop intent. Stop remains an intent, not shutdown.
func (s *Store) RequestStopAsActor(ctx context.Context, a MessageActor, requestID string) (StopIntent, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return StopIntent{}, err
	}
	defer tx.Rollback()
	if err := validateMessageActor(ctx, tx, a); err != nil {
		return StopIntent{}, err
	}
	if strings.TrimSpace(requestID) == "" {
		return StopIntent{}, messagingError("INVALID_REQUEST", "request_id", "stop requires a request ID")
	}
	var existing string
	err = tx.QueryRowContext(ctx, `SELECT id FROM messages WHERE run_id = ? AND sender = ? AND request_id = ?`, a.RunID, a.AgentID, requestID).Scan(&existing)
	if err == nil {
		return StopIntent{}, ErrIdempotencyConflict
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return StopIntent{}, err
	}
	previous, err := findAckRequest(ctx, tx, a.RunID, a.AgentID, requestID)
	if err == nil && previous != stopReceipt {
		return StopIntent{}, ErrIdempotencyConflict
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return StopIntent{}, err
	}
	if previous == stopReceipt {
		var generation int64
		if err := tx.QueryRowContext(ctx, `SELECT generation FROM journal WHERE run_id = ? AND agent_id = ? AND event = 'STOP_REQUEST_ACCEPTED' AND json_extract(details, '$.request_id') = ? ORDER BY seq LIMIT 1`, a.RunID, a.AgentID, requestID).Scan(&generation); err != nil {
			return StopIntent{}, err
		}
		if generation != a.Generation {
			return StopIntent{}, messagingError("GENERATION_MISMATCH", "actor.generation", "stop request belongs to another generation")
		}
	}
	intent, err := requestStop(ctx, tx, a.RunID)
	if err != nil {
		return StopIntent{}, err
	}
	if previous == "" {
		details, _ := json.Marshal(map[string]string{"request_id": requestID})
		if _, err := appendJournal(ctx, tx, JournalEntry{RunID: a.RunID, AgentID: a.AgentID, Generation: a.Generation, Event: "STOP_REQUEST_ACCEPTED", Details: string(details)}); err != nil {
			return StopIntent{}, err
		}
	}
	return intent, tx.Commit()
}

func requestStop(ctx context.Context, tx *sql.Tx, runID string) (StopIntent, error) {
	var stopped bool
	if err := tx.QueryRowContext(ctx, `SELECT stop_requested FROM runs WHERE run_id = ?`, runID).Scan(&stopped); err != nil {
		return StopIntent{}, err
	}
	intent := StopIntent{RunID: runID, Status: StopRequested}
	if !stopped {
		if _, err := tx.ExecContext(ctx, `UPDATE runs SET stop_requested = 1 WHERE run_id = ?`, runID); err != nil {
			return StopIntent{}, err
		}
		if _, err := appendJournal(ctx, tx, JournalEntry{RunID: runID, Event: StopRequested, Details: "Explicit stop intent; process and workspace shutdown not executed"}); err != nil {
			return StopIntent{}, err
		}
	}
	var created string
	if err := tx.QueryRowContext(ctx, `SELECT seq, created_at FROM journal WHERE run_id = ? AND event = ? ORDER BY seq LIMIT 1`, runID, StopRequested).Scan(&intent.JournalSeq, &created); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return StopIntent{}, errors.New("stop intent flag has no journal event")
		}
		return StopIntent{}, fmt.Errorf("stop intent journal: %w", err)
	}
	var err error
	intent.RecordedAt, err = time.Parse(time.RFC3339Nano, created)
	if err != nil {
		return StopIntent{}, err
	}
	return intent, nil
}
