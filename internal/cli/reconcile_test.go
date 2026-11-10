package cli

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"agentflow/internal/store"
)

func TestReconcileCLIQueriesAndPersistsOnly(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	dir := messageCLIFixture(t)
	body := writeMessageBody(t, dir, "work")
	out, _ := messageCLI(t, dir, "main", 0, "assign", "builder", "--session", "persistent", "--request-id", "work", "--body-file", body)
	var saved messageOutput
	if err := json.Unmarshal([]byte(out), &saved); err != nil {
		t.Fatal(err)
	}
	id := saved.Record.Message.ID
	first, _ := messageCLI(t, dir, "main", 0, "reconcile", id)
	again, _ := messageCLI(t, dir, "main", 0, "resume", id)
	if first != again || !strings.Contains(first, `"executed": false`) || !strings.Contains(first, `"status": "RECONCILED"`) || !strings.Contains(first, `"state": "QUEUED"`) {
		t.Fatal(first, again)
	}
	db, err := store.OpenExisting(filepath.Join(dir, "state.db"), true)
	if err != nil {
		t.Fatal(err)
	}
	w, err := db.SessionWork(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if _, claimed, err := db.BeginSessionAction(t.Context(), id, w.Seat, "submit", ""); err != nil || !claimed {
		t.Fatal(err, claimed)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	unknown, _ := messageCLI(t, dir, "main", 0, "resume", id)
	if !strings.Contains(unknown, "UNKNOWN_NEEDS_RECONCILE") || !strings.Contains(unknown, `"attempt": 1`) {
		t.Fatal(unknown)
	}
	for _, command := range []string{"reconcile", "resume"} {
		out, _ := messageCLI(t, dir, "builder", 1, command, id)
		if !strings.Contains(out, "ACTOR_MISMATCH") {
			t.Fatal(out)
		}
		out, _ = messageCLI(t, dir, "main", 1, command, "missing")
		if !strings.Contains(out, "MESSAGE_NOT_FOUND") {
			t.Fatal(out)
		}
		for _, args := range [][]string{{command}, {command, "--unknown"}, {command, id, "--request-id", "unused"}} {
			var stdout, stderr bytes.Buffer
			if code := Run(args, &stdout, &stderr, "test"); code != 2 || stdout.Len() != 0 {
				t.Fatalf("%d %s %s", code, &stdout, &stderr)
			}
		}
		var stdout, stderr bytes.Buffer
		if code := Run([]string{command, id, "--run", "run-test", "--run-dir", dir, "--agent", "main", "--generation", "1"}, &stdout, &stderr, "test"); code != 0 || !strings.Contains(stdout.String(), "decision persisted only") {
			t.Fatalf("%d %s %s", code, &stdout, &stderr)
		}
	}
}
