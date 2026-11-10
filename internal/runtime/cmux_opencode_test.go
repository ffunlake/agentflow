package runtime

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"agentflow/internal/config"
	"agentflow/internal/loopcore"
	"agentflow/internal/store"
)

func TestCMUXOpenCodeUsesVisibleSurfaceAndNativeSession(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	cmux := filepath.Join(dir, "cmux")
	opencode := filepath.Join(dir, "opencode")
	for output, source := range map[string]string{cmux: "./testdata/cmux", opencode: "./testdata/opencode-tui"} {
		if body, err := exec.Command("go", "build", "-o", output, source).CombinedOutput(); err != nil {
			t.Fatalf("build fixture: %v: %s", err, body)
		}
	}
	cwd := t.TempDir()
	t.Setenv("CMUX_FAKE_CWD", cwd)
	t.Setenv("CMUX_FAKE_OPENCODE", opencode)
	t.Setenv("CMUX_FAKE_SESSION_ID", "ses_000000000002")
	t.Setenv("CMUX_FAKE_PROMPT", filepath.Join(dir, "prompt"))
	t.Setenv("CMUX_FAKE_PROMPT_PREFIX", "<system-reminder>native metadata</system-reminder>\n")
	t.Setenv("CMUX_FAKE_ENTER", filepath.Join(dir, "enter"))
	t.Setenv("CMUX_FAKE_SURFACE_REF", "surface:81")
	t.Setenv("CMUX_FAKE_WORKSPACE_REF", "workspace:71")
	binding := store.SurfaceRegistration{RunID: "run", AgentID: "builder", Generation: 1, Tool: "opencode", LaunchID: "launch",
		WorkspaceID: workflowWorkspaceID, WorkspaceRef: "workspace:71", SurfaceID: "30000000-0000-0000-0000-000000000002", SurfaceRef: "surface:81", Status: "LAUNCHED", NativeReady: "UNVERIFIED"}
	role := config.ResolvedAgent{Tool: "opencode", Cwd: cwd, Role: "developer", Prompt: "build", Permissions: "full", Session: "persistent"}
	db, err := store.Open(filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateRun(t.Context(), store.Run{RunID: "run", ConfigSnapshot: "{}", ConfigDigest: "digest"}); err != nil {
		t.Fatal(err)
	}
	if err := db.RegisterCMUXSurfaces(t.Context(), []store.SurfaceRegistration{binding}); err != nil {
		t.Fatal(err)
	}
	transport, err := NewCMUXTransport(db, cmux)
	if err != nil {
		t.Fatal(err)
	}
	a, err := NewCMUXOpenCode(t.Context(), transport, dir, &config.Resolved{Agents: map[string]config.ResolvedAgent{"builder": role}, Workflow: &config.Workflow{}})
	if err != nil {
		t.Fatal(err)
	}
	registered, err := db.GetSeat(t.Context(), "run", "builder", 1)
	if err != nil || registered.Status != "READY" || registered.NativeSessionID != "ses_000000000002" {
		t.Fatal(registered, err)
	}
	snapshot := SeatSnapshot{RunID: "run", AgentID: "builder", Generation: 1, Tool: "opencode", Status: Ready,
		Native: NativeBinding{SessionID: "ses_000000000002", WorkspaceID: binding.WorkspaceID, SurfaceID: binding.SurfaceID}}
	if state, err := a.Inspect(t.Context(), snapshot); err != nil || !state.Verified || state.Busy {
		t.Fatal(state, err)
	}
	body, _ := json.Marshal(loopcore.Intent{Revision: 1, Recipient: "builder", Purpose: "execute", Step: "implement", Attempt: 1,
		Instruction: "review user's docs", Acceptance: []string{"tests pass"}, HandoffRequired: true})
	m := store.Message{RunID: "run", ID: "message-one", Sender: "main", SenderGeneration: 1, Recipient: "builder", Kind: "assign", Body: string(body)}
	result, err := a.Submit(t.Context(), snapshot, m)
	if err != nil || !result.Accepted || result.Channel != "cmux-opencode-tui" {
		t.Fatal(result, err)
	}
	reply, event, err := a.WorkflowReply(t.Context(), snapshot, m, loopcore.Cursor{Revision: 1, Step: "implement", Attempt: 1, Phase: loopcore.Executing, DispatchID: m.ID})
	if err != nil || reply.Status != "DONE" || event.Kind != "handoff" || event.Handoff == nil || event.Handoff.Summary != "done" {
		t.Fatal(reply, event, err)
	}
}

func TestOpenCodeSQLLiteral(t *testing.T) {
	literal, err := openCodeSQLLiteral("review user's docs'; DROP TABLE message; --")
	if err != nil || literal != "'review user''s docs''; DROP TABLE message; --'" {
		t.Fatal(literal, err)
	}
	if _, err := openCodeSQLLiteral("bad\x00value"); err == nil {
		t.Fatal("NUL query value accepted")
	}
}

const workflowWorkspaceID = "10000000-0000-0000-0000-000000000071"
