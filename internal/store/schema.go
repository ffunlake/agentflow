package store

import (
	"context"
	"fmt"
)

const schemaVersion = 1

// Migrate installs the schema atomically. user_version avoids a sixth table.
// A database from a newer binary is never silently downgraded.
func (s *Store) Migrate(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin migration: %w", err)
	}
	defer tx.Rollback()
	var version int
	if err := tx.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	if version > schemaVersion {
		return fmt.Errorf("unsupported schema version %d (maximum %d)", version, schemaVersion)
	}
	if version == 0 {
		for _, statement := range schemaV1 {
			if _, err := tx.ExecContext(ctx, statement); err != nil {
				return fmt.Errorf("migrate schema v1: %w", err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration: %w", err)
	}
	return nil
}

var schemaV1 = []string{
	`CREATE TABLE runs (
		singleton INTEGER NOT NULL DEFAULT 1 PRIMARY KEY CHECK (singleton = 1),
		run_id TEXT NOT NULL UNIQUE CHECK (length(run_id) > 0),
		config_snapshot TEXT NOT NULL CHECK (length(config_snapshot) > 0),
		config_digest TEXT NOT NULL CHECK (length(config_digest) > 0),
		workspace_id TEXT NOT NULL DEFAULT '',
		startup_phase TEXT NOT NULL DEFAULT 'CREATED',
		stop_requested INTEGER NOT NULL DEFAULT 0 CHECK (stop_requested IN (0, 1)),
		created_at TEXT NOT NULL
	)`,
	`CREATE TABLE seats (
		run_id TEXT NOT NULL REFERENCES runs(run_id),
		agent_id TEXT NOT NULL CHECK (length(agent_id) > 0),
		generation INTEGER NOT NULL CHECK (generation >= 1),
		is_current INTEGER NOT NULL CHECK (is_current IN (0, 1)),
		tool TEXT NOT NULL DEFAULT '',
		native_session_id TEXT NOT NULL DEFAULT '',
		workspace_id TEXT NOT NULL DEFAULT '',
		surface_id TEXT NOT NULL DEFAULT '',
		status TEXT NOT NULL DEFAULT 'STARTING',
		current_message_id TEXT,
		created_at TEXT NOT NULL,
		PRIMARY KEY (run_id, agent_id, generation),
		FOREIGN KEY (run_id, current_message_id, agent_id) REFERENCES messages(run_id, id, recipient)
	)`,
	`CREATE UNIQUE INDEX seats_current ON seats(run_id, agent_id) WHERE is_current = 1`,
	`CREATE TABLE messages (
		seq INTEGER PRIMARY KEY AUTOINCREMENT,
		run_id TEXT NOT NULL REFERENCES runs(run_id),
		id TEXT NOT NULL UNIQUE CHECK (length(id) > 0),
		sender TEXT NOT NULL CHECK (length(sender) > 0),
		sender_generation INTEGER CHECK (sender_generation >= 1),
		recipient TEXT NOT NULL CHECK (length(recipient) > 0),
		kind TEXT NOT NULL CHECK (length(kind) > 0),
		body TEXT NOT NULL,
		reply_to TEXT,
		request_id TEXT CHECK (length(request_id) > 0),
		status TEXT NOT NULL DEFAULT 'STORED',
		report_status TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL,
		UNIQUE (run_id, id),
		UNIQUE (run_id, id, recipient),
		FOREIGN KEY (run_id, sender, sender_generation) REFERENCES seats(run_id, agent_id, generation),
		FOREIGN KEY (run_id, reply_to) REFERENCES messages(run_id, id)
	)`,
	`CREATE UNIQUE INDEX messages_request ON messages(run_id, sender, request_id) WHERE request_id IS NOT NULL`,
	`CREATE INDEX messages_inbox ON messages(run_id, recipient, seq)`,
	`CREATE TABLE deliveries (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		run_id TEXT NOT NULL REFERENCES runs(run_id),
		message_id TEXT NOT NULL,
		recipient TEXT NOT NULL,
		target_generation INTEGER NOT NULL CHECK (target_generation >= 1),
		phase TEXT NOT NULL DEFAULT 'PENDING',
		reset_phase TEXT NOT NULL DEFAULT 'NONE',
		channel_id TEXT NOT NULL DEFAULT '',
		attempt INTEGER NOT NULL DEFAULT 0 CHECK (attempt >= 0),
		next_retry_at TEXT,
		last_error_code TEXT NOT NULL DEFAULT '',
		last_error TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL,
		UNIQUE (message_id, target_generation),
		FOREIGN KEY (run_id, message_id, recipient) REFERENCES messages(run_id, id, recipient),
		FOREIGN KEY (run_id, recipient, target_generation) REFERENCES seats(run_id, agent_id, generation)
	)`,
	`CREATE INDEX deliveries_retry ON deliveries(run_id, phase, next_retry_at, id)`,
	`CREATE TABLE journal (
		seq INTEGER PRIMARY KEY AUTOINCREMENT,
		run_id TEXT NOT NULL REFERENCES runs(run_id),
		message_id TEXT,
		agent_id TEXT,
		generation INTEGER,
		event TEXT NOT NULL CHECK (length(event) > 0),
		details TEXT NOT NULL DEFAULT '',
		evidence_ref TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL,
		CHECK ((agent_id IS NULL AND generation IS NULL) OR
		       (agent_id IS NOT NULL AND generation IS NOT NULL AND generation >= 1)),
		FOREIGN KEY (run_id, message_id) REFERENCES messages(run_id, id),
		FOREIGN KEY (run_id, agent_id, generation) REFERENCES seats(run_id, agent_id, generation)
	)`,
	`CREATE INDEX journal_run_seq ON journal(run_id, seq)`,
	`CREATE INDEX journal_message_seq ON journal(message_id, seq)`,
	`CREATE TRIGGER journal_no_update BEFORE UPDATE ON journal BEGIN
		SELECT RAISE(ABORT, 'journal is append-only'); END`,
	`CREATE TRIGGER journal_no_delete BEFORE DELETE ON journal BEGIN
		SELECT RAISE(ABORT, 'journal is append-only'); END`,
	`PRAGMA user_version = 1`,
}
