package integration_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"agentflow/internal/sqlitecheck"
)

func TestSQLiteCommitCloseReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "selfcheck.db")
	var previous string
	for i := 0; i < 2; i++ {
		if err := sqlitecheck.Check(context.Background(), path); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(path)
		if err != nil || info.Size() == 0 {
			t.Fatalf("expected a nonempty database file: stat=%v, error=%v", info, err)
		}
		// An independent connection checks the artifact left after Check closes.
		db, err := sql.Open("sqlite", path)
		if err != nil {
			t.Fatal(err)
		}
		var marker string
		err = db.QueryRow("SELECT marker FROM s0_selfcheck WHERE id = 1").Scan(&marker)
		closeErr := db.Close()
		if err != nil || closeErr != nil {
			t.Fatalf("independent read: %v; close: %v", err, closeErr)
		}
		if marker == "" || marker == previous {
			t.Fatal("expected a fresh committed marker on each self-check")
		}
		previous = marker
	}
	t.Log("two fresh markers committed, connections closed, and disk data read through new handles")
}

func TestSQLiteCheckErrors(t *testing.T) {
	for _, path := range []string{"", ":memory:", filepath.Join(t.TempDir(), "missing", "selfcheck.db")} {
		if err := sqlitecheck.Check(context.Background(), path); err == nil {
			t.Fatalf("Check(%q) unexpectedly succeeded", path)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := sqlitecheck.Check(ctx, filepath.Join(t.TempDir(), "cancelled.db")); err == nil {
		t.Fatal("cancelled self-check unexpectedly succeeded")
	}
}
