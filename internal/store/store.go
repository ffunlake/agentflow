// Package store persists run identity, messages, delivery outbox and audit events.
// It performs no IPC, process management or tool operations.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// ErrIdempotencyConflict is stable for errors.Is and IPC error-code mapping.
var ErrIdempotencyConflict = errors.New("IDEMPOTENCY_CONFLICT")

// Store owns its connection pool. SQL access remains private so journal writes
// are append-only and callers cannot bypass outbox transactions.
type Store struct {
	db *sql.DB
}

// Open opens an on-disk database in an existing parent directory. It configures
// every connection (including replacement connections), but does not migrate.
func Open(path string) (*Store, error) {
	if path == "" || path == ":memory:" || strings.HasPrefix(path, "file:") {
		return nil, fmt.Errorf("store requires an on-disk filesystem path")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve store path: %w", err)
	}
	u := url.URL{Scheme: "file", Path: abs}
	q := url.Values{"_pragma": {"busy_timeout(5000)", "foreign_keys(ON)", "journal_mode(WAL)", "synchronous(FULL)"}, "_txlock": {"immediate"}}
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, fmt.Errorf("open store: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	s := &Store{db: db}
	var foreignKeys, synchronous int
	var journalMode string
	for _, setting := range []struct {
		query string
		value any
	}{
		{"PRAGMA foreign_keys", &foreignKeys},
		{"PRAGMA journal_mode", &journalMode},
		{"PRAGMA synchronous", &synchronous},
	} {
		if err := db.QueryRow(setting.query).Scan(setting.value); err != nil {
			db.Close()
			return nil, fmt.Errorf("configure store: %w", err)
		}
	}
	if foreignKeys != 1 || journalMode != "wal" || synchronous != 2 {
		db.Close()
		return nil, fmt.Errorf("store PRAGMA mismatch: foreign_keys=%d journal_mode=%s synchronous=%d", foreignKeys, journalMode, synchronous)
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

type Run struct {
	RunID          string
	ConfigSnapshot string
	ConfigDigest   string
	WorkspaceID    string
	StartupPhase   string
	StopRequested  bool
	CreatedAt      time.Time
}

// CreateRun saves the resolved snapshot and its caller-supplied digest unchanged.
// The singleton constraint rejects a second run even with a different run ID.
func (s *Store) CreateRun(ctx context.Context, r Run) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO runs
		(run_id, config_snapshot, config_digest, workspace_id, startup_phase, stop_requested, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, r.RunID, r.ConfigSnapshot, r.ConfigDigest,
		r.WorkspaceID, defaultString(r.StartupPhase, "CREATED"), r.StopRequested, timestamp(r.CreatedAt))
	if err != nil {
		return fmt.Errorf("create run: %w", err)
	}
	return nil
}

type Seat struct {
	RunID            string
	AgentID          string
	Generation       int64
	Current          bool
	Tool             string
	NativeSessionID  string
	WorkspaceID      string
	SurfaceID        string
	Status           string
	CurrentMessageID string
	CreatedAt        time.Time
}

// CreateSeat preserves a binding as supplied. A partial unique index permits
// historical generations but at most one current generation per logical agent.
func (s *Store) CreateSeat(ctx context.Context, seat Seat) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO seats
		(run_id, agent_id, generation, is_current, tool, native_session_id, workspace_id,
		 surface_id, status, current_message_id, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		seat.RunID, seat.AgentID, seat.Generation, seat.Current, seat.Tool, seat.NativeSessionID,
		seat.WorkspaceID, seat.SurfaceID, defaultString(seat.Status, "STARTING"),
		nullString(seat.CurrentMessageID), timestamp(seat.CreatedAt))
	if err != nil {
		return fmt.Errorf("create seat: %w", err)
	}
	return nil
}

func (s *Store) GetRun(ctx context.Context) (Run, error) {
	return scanRun(s.db.QueryRowContext(ctx, selectRun))
}

const selectRun = `SELECT run_id, config_snapshot, config_digest,
	workspace_id, startup_phase, stop_requested, created_at FROM runs WHERE singleton = 1`

func scanRun(row rowScanner) (Run, error) {
	var r Run
	var created string
	err := row.Scan(
		&r.RunID, &r.ConfigSnapshot, &r.ConfigDigest, &r.WorkspaceID, &r.StartupPhase, &r.StopRequested, &created)
	if err != nil {
		return Run{}, err
	}
	r.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
	return r, err
}

func (s *Store) GetSeat(ctx context.Context, runID, agentID string, generation int64) (Seat, error) {
	var seat Seat
	var created string
	err := s.db.QueryRowContext(ctx, `SELECT run_id, agent_id, generation, is_current, tool,
		native_session_id, workspace_id, surface_id, status, COALESCE(current_message_id, ''), created_at
		FROM seats WHERE run_id = ? AND agent_id = ? AND generation = ?`, runID, agentID, generation).Scan(
		&seat.RunID, &seat.AgentID, &seat.Generation, &seat.Current, &seat.Tool, &seat.NativeSessionID,
		&seat.WorkspaceID, &seat.SurfaceID, &seat.Status, &seat.CurrentMessageID, &created)
	if err != nil {
		return Seat{}, err
	}
	seat.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
	return seat, err
}

func timestamp(t time.Time) string {
	if t.IsZero() {
		t = time.Now()
	}
	// Fixed-width fractional seconds keep retry timestamps sortable as TEXT.
	return t.UTC().Format("2006-01-02T15:04:05.000000000Z")
}

func defaultString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func nullString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func nullGeneration(value int64) any {
	if value == 0 {
		return nil
	}
	return value
}
