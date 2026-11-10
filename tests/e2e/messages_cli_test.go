package e2e_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"agentflow/internal/store"
)

func TestMessagesCLIProcessOffline(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(t.TempDir(), "agentflow")
	build := exec.Command("go", "build", "-mod=readonly", "-o", binary, "./cmd/agentflow")
	build.Dir = root
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, output)
	}
	dir := shortRunDir(t)
	db, err := store.Open(filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateRun(t.Context(), store.Run{RunID: "offline-run", ConfigSnapshot: `{"agents":{"builder":{"session":"persistent"},"main":{"session":"fresh"}}}`, ConfigDigest: "digest"}); err != nil {
		t.Fatal(err)
	}
	for _, agent := range []string{"main", "builder"} {
		if err := db.CreateSeat(t.Context(), store.Seat{RunID: "offline-run", AgentID: agent, Generation: 1, Current: true, Status: "READY"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	stopCoordinator := startCoordinatorProcess(t, binary, dir, "offline-run")
	duplicate := exec.Command(binary, "coordinator", "--run", "offline-run", "--run-dir", dir, "--json")
	if output, err := duplicate.CombinedOutput(); err == nil || !strings.Contains(string(output), "ALREADY_RUNNING") {
		t.Fatalf("second coordinator: %v %s", err, output)
	}
	tools := t.TempDir()
	marker := filepath.Join(tools, "launched")
	for _, tool := range []string{"opencode", "codex", "claude", "cmux"} {
		if err := os.WriteFile(filepath.Join(tools, tool), []byte("#!/bin/sh\nprintf launched > \"$AGENTFLOW_TEST_MARKER\"\nexit 99\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	body := filepath.Join(dir, "body.md")
	if err := os.WriteFile(body, []byte("work\n中文"), 0600); err != nil {
		t.Fatal(err)
	}
	run := func(actor string, want int, args ...string) string {
		t.Helper()
		args = append(args, "--run", "offline-run", "--run-dir", dir, "--agent", actor, "--generation", "1", "--json")
		cmd := exec.Command(binary, args...)
		for _, entry := range os.Environ() {
			if !strings.HasPrefix(entry, "PATH=") && !strings.HasPrefix(entry, "AGENTFLOW_TEST_MARKER=") {
				cmd.Env = append(cmd.Env, entry)
			}
		}
		cmd.Env = append(cmd.Env, "PATH="+tools, "AGENTFLOW_TEST_MARKER="+marker)
		var out, diagnostic bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &diagnostic
		err := cmd.Run()
		code := 0
		if err != nil {
			var exit *exec.ExitError
			if !errors.As(err, &exit) {
				t.Fatal(err)
			}
			code = exit.ExitCode()
		}
		if code != want || !json.Valid(out.Bytes()) || (want == 0 && diagnostic.Len() != 0) {
			t.Fatalf("%v exit=%d %s %s", args, code, &out, &diagnostic)
		}
		return out.String()
	}
	run("builder", 0, "inbox", "--pending")
	m1 := run("main", 0, "assign", "builder", "--request-id", "assign", "--body-file", body)
	m2 := run("main", 0, "assign", "builder", "--request-id", "assign", "--body-file", body)
	if m1 != m2 {
		t.Fatal("message response changed across processes")
	}
	stopCoordinator()
	startCoordinatorProcess(t, binary, dir, "offline-run")
	if again := run("main", 0, "assign", "builder", "--request-id", "assign", "--body-file", body); again != m1 {
		t.Fatal("assignment changed after coordinator/store reopen", again, m1)
	}
	var saved struct {
		Record store.MessageRecord `json:"record"`
	}
	if err := json.Unmarshal([]byte(m1), &saved); err != nil {
		t.Fatal(err)
	}
	id := saved.Record.Message.ID
	if id == "" || saved.Record.Delivery.Channel != "UNVERIFIED" || saved.Record.Delivery.Attempt != 0 || saved.Record.Message.Status != "STORED" {
		t.Fatal(m1)
	}
	run("main", 0, "send", "builder", "--request-id", "send", "--body-file", body)
	run("builder", 0, "inbox", "--pending")
	a1 := run("builder", 0, "message", "ack", id, "--request-id", "ack")
	a2 := run("builder", 0, "ack", id, "--request-id", "ack")
	if a1 != a2 {
		t.Fatal("ack response changed across processes")
	}
	r1 := run("builder", 0, "reply", id, "--request-id", "reply", "--body-file", body, "--status", "DONE")
	r2 := run("builder", 0, "reply", id, "--request-id", "reply", "--body-file", body, "--status", "DONE")
	if r1 != r2 {
		t.Fatal("reply response changed across processes")
	}
	if err := os.WriteFile(body, []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if out := run("main", 1, "assign", "builder", "--request-id", "assign", "--body-file", body); !strings.Contains(out, "IDEMPOTENCY_CONFLICT") {
		t.Fatal(out)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("message CLI executed native tool")
	}
	db, err = store.OpenExisting(filepath.Join(dir, "state.db"), false)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	events, err := db.JournalAfter(t.Context(), "offline-run", 0)
	if err != nil || len(events) != 7 {
		t.Fatalf("%+v %v", events, err)
	}
	decision1 := run("main", 0, "reconcile", id)
	decision2 := run("main", 0, "resume", id)
	if decision1 != decision2 || !strings.Contains(decision1, `"state": "ACKED"`) || !strings.Contains(decision1, `"executed": false`) {
		t.Fatal("recovery decision changed across processes", decision1, decision2)
	}
	fresh1 := run("builder", 0, "assign", "main", "--request-id", "fresh-config", "--body-file", body)
	fresh2 := run("builder", 0, "assign", "main", "--request-id", "fresh-config", "--body-file", body, "--mode", "persistent")
	if fresh1 != fresh2 || !strings.Contains(fresh1, `"session": "fresh"`) || !strings.Contains(fresh1, "RESET_ADAPTER_UNIMPLEMENTED") || !strings.Contains(fresh1, `"reset_phase": "NONE"`) {
		t.Fatal(fresh1, fresh2)
	}
	var fresh struct {
		Record store.MessageRecord `json:"record"`
	}
	if err := json.Unmarshal([]byte(fresh1), &fresh); err != nil {
		t.Fatal(err)
	}
	queued1 := run("builder", 0, "resume", fresh.Record.Message.ID)
	queued2 := run("builder", 0, "reconcile", fresh.Record.Message.ID)
	if queued1 != queued2 || !strings.Contains(queued1, `"state": "QUEUED"`) {
		t.Fatal(queued1, queued2)
	}
	writer, err := store.OpenExisting(filepath.Join(dir, "state.db"), true)
	if err != nil {
		t.Fatal(err)
	}
	w, err := writer.SessionWork(t.Context(), fresh.Record.Message.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, claimed, err := writer.BeginSessionAction(t.Context(), fresh.Record.Message.ID, w.Seat, "reset", ""); err != nil || !claimed {
		t.Fatal(err, claimed)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	unknown1 := run("builder", 0, "resume", fresh.Record.Message.ID)
	unknown2 := run("builder", 0, "reconcile", fresh.Record.Message.ID)
	if unknown1 != unknown2 || !strings.Contains(unknown1, "UNKNOWN_NEEDS_RECONCILE") || !strings.Contains(unknown1, `"reset_phase": "RESET_REQUESTED"`) {
		t.Fatal(unknown1, unknown2)
	}
	submitSaved := run("main", 0, "assign", "builder", "--session", "persistent", "--request-id", "submit-interrupted", "--body-file", body)
	var interrupted struct {
		Record store.MessageRecord `json:"record"`
	}
	if err := json.Unmarshal([]byte(submitSaved), &interrupted); err != nil {
		t.Fatal(err)
	}
	writer, err = store.OpenExisting(filepath.Join(dir, "state.db"), true)
	if err != nil {
		t.Fatal(err)
	}
	w, err = writer.SessionWork(t.Context(), interrupted.Record.Message.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, claimed, err := writer.BeginSessionAction(t.Context(), interrupted.Record.Message.ID, w.Seat, "submit", ""); err != nil || !claimed {
		t.Fatal(err, claimed)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	submit1 := run("main", 0, "resume", interrupted.Record.Message.ID)
	submit2 := run("main", 0, "reconcile", interrupted.Record.Message.ID)
	if submit1 != submit2 || !strings.Contains(submit1, "UNKNOWN_NEEDS_RECONCILE") || !strings.Contains(submit1, `"phase": "SUBMIT_REQUESTED"`) || !strings.Contains(submit1, `"attempt": 1`) {
		t.Fatal(submit1, submit2)
	}
	if out := run("main", 1, "ack", fresh.Record.Message.ID, "--request-id", "premature-ack"); !strings.Contains(out, "SESSION_NOT_READY") {
		t.Fatal(out)
	}
	if out := run("main", 0, "send", "builder", "--request-id", "after-fresh", "--body-file", body); !strings.Contains(out, `"reset_phase": "NONE"`) || strings.Contains(out, "RESET_ADAPTER") {
		t.Fatal(out)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("session coordination executed native tool")
	}
	items, err := db.Inbox(t.Context(), store.InboxQuery{Actor: store.MessageActor{RunID: "offline-run", AgentID: "main", Generation: 1}})
	if err != nil || len(items) != 2 || items[0].Message.ReplyTo != id || items[1].Message.ID != fresh.Record.Message.ID {
		t.Fatalf("%+v %v", items, err)
	}
}
