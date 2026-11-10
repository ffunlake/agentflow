package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agentflow/internal/coordinator"
	"agentflow/internal/store"
)

func messageCLIFixture(t *testing.T) string {
	t.Helper()
	dir, db := controlFixture(t, true)
	for _, agent := range []string{"main", "builder", "reviewer"} {
		if err := db.CreateSeat(t.Context(), store.Seat{RunID: "run-test", AgentID: agent, Generation: 1, Current: true, Status: "READY"}); err != nil {
			t.Fatal(err)
		}
	}
	startTestCoordinator(t, dir)
	return dir
}

func startTestCoordinator(t *testing.T, dir string) {
	t.Helper()
	c, err := coordinator.Open(t.Context(), dir, "run-test")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.Serve(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Error("coordinator shutdown timed out")
		}
	})
}

func messageCLI(t *testing.T, dir, actor string, code int, args ...string) (string, string) {
	t.Helper()
	args = append(args, "--run", "run-test", "--run-dir", dir, "--agent", actor, "--generation", "1", "--json")
	var stdout, stderr bytes.Buffer
	if got := Run(args, &stdout, &stderr, "test"); got != code {
		t.Fatalf("exit=%d want=%d stdout=%s stderr=%s", got, code, &stdout, &stderr)
	}
	if code != 2 && !json.Valid(stdout.Bytes()) {
		t.Fatalf("invalid JSON %s", &stdout)
	}
	if code == 0 && stderr.Len() != 0 {
		t.Fatal(&stderr)
	}
	return stdout.String(), stderr.String()
}

func writeMessageBody(t *testing.T, dir, body string) string {
	t.Helper()
	path := filepath.Join(dir, "message-body.md")
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestMessageCLICommandsAndStableJSON(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // Commands must work without any native executable.
	dir := messageCLIFixture(t)
	body := writeMessageBody(t, dir, "work\n中文 ' ; $(touch never)\n")
	empty1, _ := messageCLI(t, dir, "builder", 0, "inbox", "--pending")
	empty2, _ := messageCLI(t, dir, "builder", 0, "inbox", "--pending")
	if empty1 != empty2 || !strings.Contains(empty1, `"messages": []`) || !strings.Contains(empty1, `"status": "QUERIED"`) {
		t.Fatal(empty1, empty2)
	}
	first, _ := messageCLI(t, dir, "main", 0, "assign", "builder", "--body-file", body, "--request-id", "assign-1", "--session", "persistent")
	second, _ := messageCLI(t, dir, "main", 0, "assign", "builder", "--body-file", body, "--request-id", "assign-1", "--session", "persistent")
	if first != second {
		t.Fatal("retry JSON changed", first, second)
	}
	var saved messageOutput
	if err := json.Unmarshal([]byte(first), &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Record == nil || saved.Status != "STORED" || saved.Record.Delivery.Channel != "UNVERIFIED" || saved.Record.Delivery.Attempt != 0 {
		t.Fatalf("%+v", saved)
	}
	id := saved.Record.Message.ID
	writeMessageBody(t, dir, "different")
	failed, diagnostic := messageCLI(t, dir, "main", 1, "assign", "builder", "--body-file", body, "--request-id", "assign-1")
	if !strings.Contains(failed, "IDEMPOTENCY_CONFLICT") || !strings.Contains(diagnostic, "IDEMPOTENCY_CONFLICT: request_id") {
		t.Fatal(failed, diagnostic)
	}
	sent, _ := messageCLI(t, dir, "main", 0, "send", "builder", "--body-file", body, "--request-id", "send-1")
	if !strings.Contains(sent, `"kind": "send"`) {
		t.Fatal(sent)
	}
	inbox1, _ := messageCLI(t, dir, "builder", 0, "inbox", "--pending")
	inbox2, _ := messageCLI(t, dir, "builder", 0, "inbox", "--pending")
	if inbox1 != inbox2 {
		t.Fatal("inbox JSON changed")
	}
	var inbox struct {
		Messages []store.MessageRecord `json:"messages"`
	}
	if err := json.Unmarshal([]byte(inbox1), &inbox); err != nil || len(inbox.Messages) != 2 {
		t.Fatal(inbox1, err)
	}
	ack1, _ := messageCLI(t, dir, "builder", 0, "ack", id, "--request-id", "ack-1")
	ack2, _ := messageCLI(t, dir, "builder", 0, "message", "ack", id, "--request-id", "ack-1")
	ack3, _ := messageCLI(t, dir, "builder", 0, "ack", id, "--request-id", "ack-2")
	if ack1 != ack2 || ack1 != ack3 || !strings.Contains(ack1, `"status": "ACKED"`) {
		t.Fatal(ack1, ack2, ack3)
	}
	reply1, _ := messageCLI(t, dir, "builder", 0, "reply", id, "--body-file", body, "--request-id", "reply-1", "--status", "DONE")
	reply2, _ := messageCLI(t, dir, "builder", 0, "reply", id, "--body-file", body, "--request-id", "reply-1", "--status", "DONE")
	if reply1 != reply2 || !strings.Contains(reply1, `"reply_to": "`+id+`"`) || !strings.Contains(reply1, `"recipient": "main"`) || !strings.Contains(reply1, `"report_status": "DONE"`) {
		t.Fatal(reply1, reply2)
	}
	for _, args := range [][]string{
		{"send", "builder", "--body-file", body, "--request-id", "send-text"},
		{"inbox", "--pending"},
		{"ack", id, "--request-id", "ack-text"},
		{"reply", id, "--body-file", body, "--request-id", "reply-text", "--status", "DONE"},
	} {
		args = append(args, "--run", "run-test", "--run-dir", dir, "--agent", "builder", "--generation", "1")
		var out, diagnostic bytes.Buffer
		if code := Run(args, &out, &diagnostic, "test"); code != 0 || diagnostic.Len() != 0 || !strings.Contains(out.String(), "Runtime communication: UNVERIFIED") {
			t.Fatalf("exit=%d %s %s", code, &out, &diagnostic)
		}
	}
}

func TestMessageCLIFailuresAndUsage(t *testing.T) {
	dir := messageCLIFixture(t)
	body := writeMessageBody(t, dir, "private body that must not appear in diagnostics")
	for _, c := range []struct {
		actor, want string
		args        []string
	}{
		{"missing", "ACTOR_MISMATCH", []string{"inbox"}},
		{"main", "ACTOR_MISMATCH", []string{"send", "missing", "--body-file", body, "--request-id", "send"}},
		{"main", "GENERATION_MISMATCH", []string{"assign", "builder", "--recipient-generation", "2", "--body-file", body, "--request-id", "assign"}},
		{"main", "MESSAGE_NOT_FOUND", []string{"ack", "missing", "--request-id", "ack"}},
		{"main", "MESSAGE_NOT_FOUND", []string{"reply", "missing", "--body-file", body, "--request-id", "reply"}},
		{"main", "BODY_UNAVAILABLE", []string{"send", "builder", "--body-file", filepath.Join(dir, "missing.md"), "--request-id", "body"}},
	} {
		out, diagnostic := messageCLI(t, dir, c.actor, 1, c.args...)
		if !strings.Contains(out, c.want) || !strings.Contains(diagnostic, "agentflow: error: "+c.want) || strings.Contains(out+diagnostic, "private body") {
			t.Fatal(out, diagnostic)
		}
	}
	var out, diagnostic bytes.Buffer
	args := []string{"inbox", "--run", "run-test", "--run-dir", dir, "--agent", "main", "--generation", "2", "--json"}
	if code := Run(args, &out, &diagnostic, "test"); code != 1 || !strings.Contains(diagnostic.String(), "GENERATION_MISMATCH") {
		t.Fatalf("%d %s %s", code, &out, &diagnostic)
	}
	for _, command := range []string{"send", "assign", "inbox", "ack", "reply"} {
		for _, args := range [][]string{{command}, {command, "--unknown"}, {command, "--generation", "bad"}} {
			var out, diagnostic bytes.Buffer
			if code := Run(args, &out, &diagnostic, "test"); code != 2 || out.Len() != 0 || !strings.HasPrefix(diagnostic.String(), "agentflow: error:") {
				t.Fatalf("%v %d %s %s", args, code, &out, &diagnostic)
			}
		}
		var out, diagnostic bytes.Buffer
		if code := Run([]string{command, "--help"}, &out, &diagnostic, "test"); code != 0 || diagnostic.Len() != 0 || !strings.Contains(out.String(), "UNVERIFIED") {
			t.Fatalf("%d %s %s", code, &out, &diagnostic)
		}
	}
	writeMessageBody(t, dir, string([]byte{0xff}))
	_, diagnosticText := messageCLI(t, dir, "main", 1, "send", "builder", "--body-file", body, "--request-id", "invalid-body")
	if !strings.Contains(diagnosticText, "BODY_INVALID") {
		t.Fatal(diagnosticText)
	}
}

func TestMessageCLIEmptyMissingAndWrongRun(t *testing.T) {
	for _, mode := range []string{"missing", "empty", "wrong-run"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			want := "COORDINATOR_UNAVAILABLE"
			if mode == "empty" {
				dir, _ = controlFixture(t, false)
				want = "COORDINATOR_UNAVAILABLE"
			}
			if mode == "wrong-run" {
				dir = messageCLIFixture(t)
				want = "ACTOR_MISMATCH"
			}
			for _, command := range []string{"send", "assign", "inbox", "ack", "reply"} {
				var out, diagnostic bytes.Buffer
				args := []string{command}
				if command != "inbox" {
					args = append(args, "target", "--request-id", "request")
				}
				if command == "send" || command == "assign" || command == "reply" {
					args = append(args, "--body-file", writeMessageBody(t, t.TempDir(), "work"))
				}
				// Body is read after opening the store; an empty store needs a valid file.
				if mode != "missing" && command != "inbox" && command != "ack" {
					args[len(args)-1] = writeMessageBody(t, t.TempDir(), "work")
				}
				runID := "run-test"
				if mode == "wrong-run" {
					runID = "other"
				}
				args = append(args, "--run", runID, "--run-dir", dir, "--agent", "main", "--generation", "1", "--json")
				if code := Run(args, &out, &diagnostic, "test"); code != 1 || !json.Valid(out.Bytes()) || !strings.Contains(diagnostic.String(), want) {
					t.Fatalf("%d %s %s", code, &out, &diagnostic)
				}
			}
			if mode == "missing" {
				if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 {
					t.Fatalf("missing store created files: %v %v", entries, err)
				}
			}
		})
	}
}

func TestMessageCLIOutputFailures(t *testing.T) {
	dir := messageCLIFixture(t)
	for _, asJSON := range []bool{false, true} {
		args := []string{"inbox", "--run", "run-test", "--run-dir", dir, "--agent", "main", "--generation", "1"}
		if asJSON {
			args = append(args, "--json")
		}
		var diagnostic bytes.Buffer
		if code := Run(args, messageFailWriter{}, &diagnostic, "test"); code != 1 || !strings.Contains(diagnostic.String(), "write") {
			t.Fatalf("%d %s", code, &diagnostic)
		}
	}
}

type messageFailWriter struct{}

func (messageFailWriter) Write([]byte) (int, error) { return 0, errors.New("injected output failure") }

func TestAssignSessionModesAndUnimplementedAdapters(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	for _, c := range []struct {
		name, target, mode, coordination string
		flags                            []string
	}{
		{"config-default", "builder", "persistent", "SUBMIT_ADAPTER_UNIMPLEMENTED", nil},
		{"config-persistent", "main", "persistent", "SUBMIT_ADAPTER_UNIMPLEMENTED", nil},
		{"session-override", "builder", "persistent", "SUBMIT_ADAPTER_UNIMPLEMENTED", []string{"--session", "persistent"}},
		{"mode-override", "main", "fresh", "RESET_ADAPTER_UNIMPLEMENTED", []string{"--mode", "fresh"}},
		{"matching-aliases", "builder", "persistent", "SUBMIT_ADAPTER_UNIMPLEMENTED", []string{"--session", "persistent", "--mode", "persistent"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := messageCLIFixture(t)
			body := writeMessageBody(t, dir, "work")
			args := append([]string{"assign", c.target, "--body-file", body, "--request-id", "mode-test"}, c.flags...)
			out, _ := messageCLI(t, dir, "reviewer", 0, args...)
			var result messageOutput
			if err := json.Unmarshal([]byte(out), &result); err != nil {
				t.Fatal(err)
			}
			if result.Record.Session != c.mode || result.Record.Coordination != c.coordination || result.Record.Delivery.Phase != "QUEUED" || result.Record.Delivery.ResetPhase != "NONE" || result.Record.Delivery.Attempt != 0 || result.RuntimeCommunication != "UNVERIFIED" {
				t.Fatal(out)
			}
			// A changed mode on a retry cannot turn an existing assignment into
			// a second reset or a different session policy.
			again, _ := messageCLI(t, dir, "reviewer", 0, "assign", c.target, "--body-file", body, "--request-id", "mode-test", "--session", "persistent")
			if again != out {
				t.Fatal("mode retry changed persisted decision", out, again)
			}
			if c.mode == "fresh" {
				failed, _ := messageCLI(t, dir, c.target, 1, "ack", result.Record.Message.ID, "--request-id", "premature-ack")
				if !strings.Contains(failed, "SESSION_NOT_READY") {
					t.Fatal(failed)
				}
				failed, _ = messageCLI(t, dir, c.target, 1, "reply", result.Record.Message.ID, "--request-id", "premature-result", "--body-file", body, "--status", "DONE")
				if !strings.Contains(failed, "SESSION_NOT_READY") {
					t.Fatal(failed)
				}
			}
		})
	}
	for _, flags := range [][]string{{"--session", "unknown"}, {"--mode", "unknown"}, {"--session", "fresh", "--mode", "persistent"}} {
		dir := messageCLIFixture(t)
		body := writeMessageBody(t, dir, "work")
		args := append([]string{"assign", "builder", "--body-file", body, "--request-id", "invalid-mode"}, flags...)
		messageCLI(t, dir, "main", 2, args...)
	}
	dir := messageCLIFixture(t)
	body := writeMessageBody(t, dir, "supplement")
	messageCLI(t, dir, "main", 2, "send", "builder", "--body-file", body, "--request-id", "invalid-send", "--session", "fresh")
}
