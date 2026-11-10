package e2e_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Export all persisted rows in one read transaction, including column names,
// empty tables and SQLite migration metadata. JSON is deterministic for hashing.
func headlessDBSnapshot(t *testing.T, dir string) []byte {
	t.Helper()
	u := url.URL{Scheme: "file", Path: filepath.Join(dir, "state.db"), RawQuery: "mode=ro"}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	tx, err := db.BeginTx(t.Context(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(t.Context(), "SELECT name FROM sqlite_master WHERE type='table' ORDER BY name")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	tables := map[string][]map[string]any{}
	for _, name := range names {
		rows, err := tx.QueryContext(t.Context(), `SELECT * FROM "`+strings.ReplaceAll(name, `"`, `""`)+`" ORDER BY rowid`)
		if err != nil {
			t.Fatal(err)
		}
		columns, err := rows.Columns()
		if err != nil {
			t.Fatal(err)
		}
		tables[name] = []map[string]any{}
		for rows.Next() {
			values, pointers := make([]any, len(columns)), make([]any, len(columns))
			for i := range values {
				pointers[i] = &values[i]
			}
			if err := rows.Scan(pointers...); err != nil {
				t.Fatal(err)
			}
			row := map[string]any{}
			for i, column := range columns {
				row[column] = values[i]
			}
			tables[name] = append(tables[name], row)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	body, err := json.MarshalIndent(tables, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return append(body, '\n')
}

// Optional local artifact export. Normal tests leave artifacts in t.TempDir;
// the explicit evidence run refuses to overwrite an earlier archive.
func headlessEvidence(t *testing.T, tool, mode string) func(string, []byte) {
	t.Helper()
	base := os.Getenv("AGENTFLOW_CLAUDE_FIX_EVIDENCE")
	if tool != "claude" || base == "" {
		return func(string, []byte) {}
	}
	if !filepath.IsAbs(base) {
		t.Fatal("evidence directory must be absolute")
	}
	dir := filepath.Join(base, mode)
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	return func(name string, body []byte) {
		t.Helper()
		f, err := os.OpenFile(filepath.Join(dir, name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			t.Fatal(err)
		}
		_, writeErr := f.Write(body)
		closeErr := f.Close()
		if writeErr != nil || closeErr != nil {
			t.Fatal(writeErr, closeErr)
		}
	}
}

func headlessUnchanged(t *testing.T, dir, cwd, label string, before, calls []byte, archive func(string, []byte)) {
	t.Helper()
	after := headlessDBSnapshot(t, dir)
	afterCalls, err := os.ReadFile(filepath.Join(cwd, "calls.log"))
	if err != nil || !bytes.Equal(before, after) || !bytes.Equal(calls, afterCalls) {
		t.Fatal(label+": reopen/replay changed persisted rows or invoked native", err)
	}
	archive(label+".db.json", after)
	archive(label+".calls.jsonl", afterCalls)
}
