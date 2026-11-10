package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agentflow/internal/coordinator"
	"agentflow/internal/store"
)

func TestCMUXTransportCLIIPCAndReopen(t *testing.T) {
	dir, err := os.MkdirTemp("", "af-cm-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	dir, err = filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(t.TempDir(), "fake cmux")
	if out, err := exec.Command("go", "build", "-o", binary, "../runtime/testdata/cmux").CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	log := filepath.Join(dir, "calls.jsonl")
	t.Setenv("CMUX_FAKE_LOG", log)
	t.Setenv("CMUX_FAKE_MODE", "json")
	const ws = "10000000-0000-0000-0000-000000000071"
	db, err := store.Open(filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateRun(t.Context(), store.Run{RunID: "transport", ConfigSnapshot: `{}`, ConfigDigest: "digest"}); err != nil {
		t.Fatal(err)
	}
	if err := db.RegisterCMUXSurfaces(t.Context(), []store.SurfaceRegistration{
		{RunID: "transport", AgentID: "main", Generation: 1, Tool: "codex", LaunchID: "launch", WorkspaceID: ws, WorkspaceRef: "workspace:71", SurfaceID: "30000000-0000-0000-0000-000000000001", SurfaceRef: "surface:81", Status: "LAUNCHED", NativeReady: "UNVERIFIED"},
		{RunID: "transport", AgentID: "builder", Generation: 1, Tool: "opencode", LaunchID: "launch", WorkspaceID: ws, WorkspaceRef: "workspace:71", SurfaceID: "30000000-0000-0000-0000-000000000002", SurfaceRef: "surface:82", Status: "LAUNCHED", NativeReady: "UNVERIFIED"},
	}); err != nil {
		t.Fatal(err)
	}
	db.Close()
	start := func() func() {
		c, err := coordinator.OpenCMUXTransport(t.Context(), dir, "transport", binary)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() { done <- c.Serve(ctx) }()
		return func() {
			cancel()
			select {
			case err := <-done:
				if err != nil {
					t.Error(err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("coordinator did not drain")
			}
		}
	}
	stop := start()
	defer func() {
		if stop != nil {
			stop()
		}
	}()
	bodyFile := filepath.Join(dir, "body.md")
	if err := os.WriteFile(bodyFile, []byte("--help ' $(literal)\n中文"), 0600); err != nil {
		t.Fatal(err)
	}
	invoke := func(command, agent string, rest ...string) (coordinator.Result, int) {
		t.Helper()
		args := []string{command}
		if command == "cmux-send" {
			args = append(args, "builder")
		}
		args = append(args, "--run", "transport", "--run-dir", dir, "--agent", agent, "--generation", "1", "--json")
		args = append(args, rest...)
		var out, diagnostic bytes.Buffer
		code := Run(args, &out, &diagnostic, "test")
		var r coordinator.Result
		if err := json.Unmarshal(out.Bytes(), &r); err != nil {
			t.Fatalf("%v %d %s %s", err, code, &out, &diagnostic)
		}
		return r, code
	}
	r, code := invoke("cmux-send", "main", "--request-id", "send-1", "--body-file", bodyFile)
	if code != 0 || r.Status != "CMUX_QUEUED" || r.CMUXSend == nil || r.RuntimeCommunication != "UNVERIFIED" {
		t.Fatal(r, code)
	}
	original, _ := json.Marshal(r.CMUXSend)
	stop()
	stop = start()
	r, code = invoke("cmux-send", "main", "--request-id", "send-1", "--body-file", bodyFile)
	again, _ := json.Marshal(r.CMUXSend)
	if code != 0 || string(original) != string(again) {
		t.Fatal(r, code)
	}
	e := r.CMUXSend.Evidence
	echo := map[string]any{"id": e.ID, "sender_name": e.Source, "sender_surface_id": nil, "sender_workspace_id": nil, "recipient_surface_id": e.TargetSurface, "recipient_workspace_id": e.TargetWorkspace, "body": e.Body, "state": "queued"}
	file := filepath.Join(dir, "inbox.json")
	writeInbox := func(messages ...any) {
		t.Helper()
		body, err := json.Marshal(map[string]any{"messages": messages})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, body, 0600); err != nil {
			t.Fatal(err)
		}
	}
	writeInbox(echo)
	t.Setenv("CMUX_FAKE_INBOX", file)
	r, code = invoke("cmux-inbox", "builder")
	if code != 0 || r.CMUXInbox == nil || len(r.CMUXInbox.Messages) != 1 {
		t.Fatal(r, code)
	}
	r, code = invoke("cmux-inbox", "builder")
	if code != 0 || r.CMUXInbox == nil || len(r.CMUXInbox.Messages) != 1 {
		t.Fatal(r, code)
	}
	echo["state"] = "read"
	for _, order := range []string{"old-first", "new-first"} {
		stop()
		stop = start()
		fresh := map[string]any{"id": order, "sender_name": "lead", "sender_surface_id": "30000000-0000-0000-0000-000000000001", "sender_workspace_id": ws, "recipient_surface_id": e.TargetSurface, "recipient_workspace_id": e.TargetWorkspace, "body": "new message", "state": "queued"}
		batch := []any{echo, fresh}
		if order == "new-first" {
			batch = []any{fresh, echo}
		}
		writeInbox(batch...)
		for range 2 {
			r, code = invoke("cmux-inbox", "builder")
			if code != 0 || r.CMUXInbox == nil || len(r.CMUXInbox.Messages) != 2 {
				t.Fatal(order, r, code)
			}
		}
		fresh["body"] = "conflicting history"
		extra := map[string]any{"id": "must-rollback-" + order, "sender_name": "lead", "sender_surface_id": "30000000-0000-0000-0000-000000000001", "sender_workspace_id": ws, "recipient_surface_id": e.TargetSurface, "recipient_workspace_id": e.TargetWorkspace, "body": "must not persist", "state": "queued"}
		writeInbox(extra, echo, fresh)
		r, code = invoke("cmux-inbox", "builder")
		if code != 1 || r.Status != "FAILED" || len(r.Diagnostics) == 0 || r.Diagnostics[0].Code != "CMUX_EVENT_DUPLICATE" {
			t.Fatal(order, r, code)
		}
	}
	stop()
	stop = nil
	db, err = store.OpenExisting(filepath.Join(dir, "state.db"), false)
	if err != nil {
		t.Fatal(err)
	}
	journal, err := db.JournalAfter(t.Context(), "transport", 0)
	db.Close()
	if err != nil {
		t.Fatal(err)
	}
	accepted := map[string]int{}
	for _, event := range journal {
		if event.Event == "CMUX_INBOX_EVENT" {
			var evidence store.CMUXEvidence
			if err := json.Unmarshal([]byte(event.Details), &evidence); err != nil {
				t.Fatal(err)
			}
			accepted[evidence.ID]++
			if evidence.ID == e.ID && evidence.State != "queued" {
				t.Fatal("native state update overwrote original receipt", evidence)
			}
		}
	}
	if len(accepted) != 3 || accepted[e.ID] != 1 || accepted["old-first"] != 1 || accepted["new-first"] != 1 {
		t.Fatal("missing, duplicate or partially committed inbox events", accepted)
	}
	stop = start()
	r, code = invoke("cmux-send", "unknown", "--request-id", "unknown", "--body-file", bodyFile)
	if code != 1 {
		t.Fatal(r, code)
	}
	_, code = invoke("stop", "main", "--request-id", "stop-1")
	if code != 0 {
		t.Fatal(code)
	}
	r, code = invoke("cmux-send", "main", "--request-id", "stop-send", "--body-file", bodyFile)
	if code != 1 || r.Diagnostics[0].Code != "STOP_REQUESTED" {
		t.Fatal(r, code)
	}
	logBody, err := os.ReadFile(log)
	if err != nil || strings.Count(string(logBody), `"message"`) != 1 || strings.Count(string(logBody), `"inbox"`) != 8 || strings.Contains(string(logBody), "paste") {
		t.Fatal(string(logBody), err)
	}
}
