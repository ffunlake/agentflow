// Package sqlitecheck verifies on-disk SQLite persistence for S0.
// It uses a disposable diagnostic database, not the future business schema.
package sqlitecheck

import (
	"context"
	"crypto/rand"
	"database/sql"
	"fmt"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// Check commits a fresh marker, closes all database connections, opens a new
// database handle, and verifies the committed marker. The caller owns the file
// and its parent directory; Check does not create a runtime or business tables.
func Check(ctx context.Context, path string) error {
	if path == "" || path == ":memory:" {
		return fmt.Errorf("SQLite self-check requires an on-disk database path")
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("resolve database path: %w", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	db.SetMaxOpenConns(1)
	defer db.Close()
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS s0_selfcheck (
		id INTEGER PRIMARY KEY CHECK (id = 1), marker TEXT NOT NULL
	)`); err != nil {
		return fmt.Errorf("create self-check table: %w", err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin self-check transaction: %w", err)
	}
	defer tx.Rollback()
	marker := rand.Text()
	if _, err := tx.ExecContext(ctx, `INSERT INTO s0_selfcheck (id, marker) VALUES (1, ?)
		ON CONFLICT(id) DO UPDATE SET marker = excluded.marker`, marker); err != nil {
		return fmt.Errorf("write self-check marker: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit self-check transaction: %w", err)
	}
	if err := db.Close(); err != nil {
		return fmt.Errorf("close committed database: %w", err)
	}

	reopened, err := sql.Open("sqlite", path)
	if err != nil {
		return fmt.Errorf("reopen database: %w", err)
	}
	defer reopened.Close()
	var got string
	if err := reopened.QueryRowContext(ctx, "SELECT marker FROM s0_selfcheck WHERE id = 1").Scan(&got); err != nil {
		return fmt.Errorf("read marker after reopening: %w", err)
	}
	if got != marker {
		return fmt.Errorf("self-check marker mismatch after reopening")
	}
	if err := reopened.Close(); err != nil {
		return fmt.Errorf("close reopened database: %w", err)
	}
	return nil
}
