package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentflow/internal/lock"
	"agentflow/internal/store"
)

func TestMessageAndStopCLIRequireIPCWithoutStoreFallback(t *testing.T) {
	dir, db := controlFixture(t, true)
	if err := db.CreateSeat(t.Context(), store.Seat{RunID: "run-test", AgentID: "main", Generation: 1, Current: true, Status: "READY"}); err != nil {
		t.Fatal(err)
	}
	body := writeMessageBody(t, dir, "work")
	for _, state := range []string{"no-lock", "dead-socket", "wrong-run"} {
		var l *lock.Lock
		if state != "no-lock" {
			runID := "run-test"
			if state == "wrong-run" {
				runID = "another-run"
			}
			var err error
			l, err = lock.Acquire(filepath.Join(dir, "coordinator.lock"), lock.Owner{RunID: runID, SocketPath: filepath.Join(dir, "absent.sock")})
			if err != nil {
				t.Fatal(err)
			}
		}
		for _, command := range []string{"assign", "send", "inbox", "ack", "reply", "stop", "reconcile", "resume"} {
			args := []string{command}
			if command != "inbox" && command != "stop" {
				args = append(args, "target")
			}
			args = append(args, "--run", "run-test", "--run-dir", dir, "--agent", "main", "--generation", "1", "--json")
			if command != "inbox" && command != "reconcile" && command != "resume" {
				args = append(args, "--request-id", "request")
			}
			if command == "assign" || command == "send" || command == "reply" {
				args = append(args, "--body-file", body)
			}
			var out, diagnostic bytes.Buffer
			code := Run(args, &out, &diagnostic, "test")
			want := "COORDINATOR_UNAVAILABLE"
			if state == "dead-socket" {
				want = "COORDINATOR_IPC_FAILED"
			}
			if state == "wrong-run" {
				want = "ACTOR_MISMATCH"
			}
			if code != 1 || !strings.Contains(out.String(), want) || !strings.Contains(out.String(), `"capability_status": "PARTIAL"`) {
				t.Fatalf("%s %s: %d %s %s", state, command, code, &out, &diagnostic)
			}
		}
		if l != nil {
			if err := l.Release(); err != nil {
				t.Fatal(err)
			}
		}
	}
	run, err := db.GetRun(t.Context())
	if err != nil || run.StopRequested {
		t.Fatal(run, err)
	}
	journal, err := db.JournalAfter(t.Context(), "run-test", 0)
	if err != nil || len(journal) != 0 {
		t.Fatal(journal, err)
	}
	items, err := db.Inbox(t.Context(), store.InboxQuery{Actor: store.MessageActor{RunID: "run-test", AgentID: "main", Generation: 1}})
	if err != nil || len(items) != 0 {
		t.Fatal(items, err)
	}
}

func TestCoordinatorCLIUsageAndStructuredFailure(t *testing.T) {
	for _, args := range [][]string{{"coordinator"}, {"coordinator", "--run", "../bad"}, {"coordinator", "--run", "run", "extra"}, {"coordinator", "--unknown"}, {"coordinator", "--run"}} {
		var out, diagnostic bytes.Buffer
		if code := Run(args, &out, &diagnostic, "test"); code != 2 || out.Len() != 0 {
			t.Fatal(code, &out, &diagnostic)
		}
	}
	var out, diagnostic bytes.Buffer
	if code := Run([]string{"coordinator", "--help"}, &out, &diagnostic, "test"); code != 0 || !strings.Contains(out.String(), "PARTIAL") {
		t.Fatal(code, &out, &diagnostic)
	}
	dir := t.TempDir()
	out.Reset()
	diagnostic.Reset()
	if code := Run([]string{"coordinator", "--run", "run", "--run-dir", dir, "--json"}, &out, &diagnostic, "test"); code != 1 || !strings.Contains(out.String(), "COORDINATOR_START_FAILED") {
		t.Fatal(code, &out, &diagnostic)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatal("failed startup created artifacts", entries, err)
	}
}
