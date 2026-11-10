package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agentflow/internal/config"
	"agentflow/internal/coordinator"
	"agentflow/internal/ipc"
	"agentflow/internal/loopcore"
	"agentflow/internal/runtime"
	"agentflow/internal/store"
)

func TestCMUXWorkflowCLIForegroundStartDrainResume(t *testing.T) {
	dir, err := os.MkdirTemp("", "af-cwc-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	binary := filepath.Join(t.TempDir(), "fake cmux")
	if out, err := exec.Command("go", "build", "-o", binary, "../runtime/testdata/cmux").CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	log := filepath.Join(dir, "calls.jsonl")
	inbox := filepath.Join(dir, "inbox.json")
	if err := os.WriteFile(inbox, []byte(`{"messages":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CMUX_FAKE_LOG", log)
	t.Setenv("CMUX_FAKE_WORKFLOW", "1")
	t.Setenv("CMUX_FAKE_MODE", "json")
	t.Setenv("CMUX_FAKE_INBOX", inbox)
	source, err := config.Load("../../examples/workflow.yaml")
	if err != nil {
		t.Fatal(err)
	}
	frozen, err := config.Resolve(source, config.ResolveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(frozen)
	db, err := store.Open(filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateRun(t.Context(), store.Run{RunID: "cmux-cli", ConfigSnapshot: string(raw), ConfigDigest: frozen.Digest}); err != nil {
		t.Fatal(err)
	}
	const ws = "10000000-0000-0000-0000-000000000071"
	const mainSurface = "30000000-0000-0000-0000-000000000001"
	const builderSurface = "30000000-0000-0000-0000-000000000002"
	for id, surface := range map[string]string{"main": mainSurface, "builder": builderSurface, "reviewer": "30000000-0000-0000-0000-000000000003"} {
		if err := db.RegisterCMUXSurfaces(t.Context(), []store.SurfaceRegistration{{RunID: "cmux-cli", AgentID: id, Generation: 1, Tool: frozen.Agents[id].Tool, LaunchID: "launch", WorkspaceID: ws, WorkspaceRef: "workspace:71", SurfaceID: surface, SurfaceRef: "surface:" + id, Status: "LAUNCHED", NativeReady: "UNVERIFIED"}}); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()
	invoke := func(kind, rev, id string, extra ...string) (coordinator.Result, int, string) {
		t.Helper()
		args := []string{"workflow", kind, "--run", "cmux-cli", "--run-dir", dir, "--agent", "main", "--generation", "1", "--revision", rev, "--request-id", id, "--json"}
		args = append(args, extra...)
		var out, diagnostic bytes.Buffer
		code := Run(args, &out, &diagnostic, "test")
		var result coordinator.Result
		if out.Len() > 0 {
			if err := json.Unmarshal(out.Bytes(), &result); err != nil {
				t.Fatal(err, &out, &diagnostic)
			}
		}
		return result, code, diagnostic.String()
	}
	// Existing IPC is still mandatory unless the foreground option is explicit.
	if _, code, _ := invoke("start", "0", "start"); code != 1 {
		t.Fatal("implicit store fallback")
	}
	flags := []string{"--cmux-transport", "--cmux-binary", binary}
	r, code, diagnostic := invoke("start", "0", "start", flags...)
	if code != 0 || r.Dispatch == nil || r.Dispatch.Delivery.Phase != "CMUX_QUEUED" || !*r.Executed {
		t.Fatal(code, r, diagnostic)
	}
	for _, name := range []string{"coordinator.lock", "ipc.sock"} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Fatal("command leaked foreground owner", name, err)
		}
	}
	if replay, code, d := invoke("start", "0", "start", flags...); code != 0 || replay.Dispatch != nil || *replay.Executed {
		t.Fatal(code, replay, d)
	}
	c := r.Workflow.Cursor
	body, _ := json.Marshal(runtime.CMUXWorkflowReply{V: 1, Type: "workflow_reply", RequestID: "blocked", Actor: ipc.Actor{RunID: "cmux-cli", AgentID: "builder", Generation: 1}, MessageID: c.DispatchID, Body: "needs decision", Status: "BLOCKED_CONFIRM", Workflow: runtime.CMUXWorkflowEvent{Revision: 1, Kind: "blocked", Reason: "confirm next action"}})
	batch, _ := json.Marshal(map[string]any{"messages": []any{map[string]any{"id": "blocked-native", "sender_name": "builder", "sender_surface_id": builderSurface, "sender_workspace_id": ws, "recipient_surface_id": mainSurface, "recipient_workspace_id": ws, "body": string(body), "state": "queued"}}})
	commandDone := make(chan struct {
		result     coordinator.Result
		code       int
		diagnostic string
	}, 1)
	drainArgs := []string{"workflow", "drain", "--run", "cmux-cli", "--run-dir", dir, "--agent", "main", "--generation", "1", "--revision", "1", "--request-id", "drain", "--timeout", "10s", "--json"}
	drainArgs = append(drainArgs, flags...)
	go func() {
		var out, diagnostic bytes.Buffer
		code := Run(drainArgs, &out, &diagnostic, "test")
		var result coordinator.Result
		_ = json.Unmarshal(out.Bytes(), &result)
		commandDone <- struct {
			result     coordinator.Result
			code       int
			diagnostic string
		}{result, code, diagnostic.String()}
	}()
	time.Sleep(1200 * time.Millisecond)
	if err := os.WriteFile(inbox, batch, 0600); err != nil {
		t.Fatal(err)
	}
	var drained struct {
		result     coordinator.Result
		code       int
		diagnostic string
	}
	select {
	case drained = <-commandDone:
	case <-time.After(5 * time.Second):
		t.Fatal("single workflow drain command did not collect the later reply")
	}
	r, code, diagnostic = drained.result, drained.code, drained.diagnostic
	if code != 0 || r.WorkflowCurrent.Phase != loopcore.Paused || r.WorkflowCurrent.Revision != 2 {
		logBody, _ := os.ReadFile(log)
		t.Fatalf("code=%d result=%+v dispatch=%+v cmux=%s diagnostic=%s", code, r, r.Dispatch, logBody, diagnostic)
	}
	resumeFlags := append(append([]string{}, flags...), "--reason", "decision saved")
	r, code, diagnostic = invoke("resume", "2", "resume", resumeFlags...)
	if code != 0 || r.Dispatch.Delivery.Phase != "CMUX_QUEUED" || r.WorkflowCurrent.Revision != 3 || !*r.Executed {
		t.Fatal(code, r, diagnostic)
	}
	if status, code, d := invoke("status", "0", "status", flags...); code != 0 || status.WorkflowCurrent.Revision != 3 || *status.Executed {
		t.Fatal(code, status, d)
	}
	logBody, err := os.ReadFile(log)
	if err != nil || strings.Count(string(logBody), `"agent","message"`) != 2 || strings.Contains(string(logBody), "new-workspace") {
		t.Fatal(string(logBody), err)
	}
	if _, code, _ := invoke("status", "0", "bad-flags", "--cmux-binary", binary); code != 2 {
		t.Fatal("accepted cmux binary without foreground opt-in")
	}
}
