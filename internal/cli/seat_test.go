package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentflow/internal/runtime"
)

func TestSeatStrictArgumentsAndHiddenHelp(t *testing.T) {
	for _, args := range [][]string{
		nil, {"--help"}, {"--snapshot", "/private/file"},
		{"--snapshot", "relative", "--agent", "main"},
		{"--snapshot", "", "--agent", "main"},
		{"--snapshot", "/bad\x00path", "--agent", "main"},
		{"--snapshot", "/private/file", "--agent", "--unknown"},
		{"--snapshot", "/private/file", "--agent", "Main"},
		{"--snapshot", "/private/file", "--agent", "main", "extra"},
		{"--snapshot", "/private/file", "--snapshot", "/other"},
		{"--agent", "main", "--agent", "builder"},
		{"--snapshot=/private/file", "--agent", "main", "fixture-secret"},
		{"-snapshot", "/private/file", "--agent", "main"},
		{"--unknown", "fixture-secret", "--agent", "main"},
		{"--", "fixture-secret", "--agent", "main"},
	} {
		var out, diagnostic bytes.Buffer
		if code := Run(append([]string{"_seat"}, args...), &out, &diagnostic, "test"); code != 2 || out.Len() != 0 || !strings.HasPrefix(diagnostic.String(), "agentflow: error:") || strings.Contains(diagnostic.String(), "fixture-secret") {
			t.Fatalf("unexpected usage result: %d %s %s", code, &out, &diagnostic)
		}
	}
	for _, args := range [][]string{nil, {"--help"}, {"-h"}} {
		var out, diagnostic bytes.Buffer
		if code := Run(args, &out, &diagnostic, "test"); code != 0 || strings.Contains(out.String(), "_seat") {
			t.Fatalf("hidden entry advertised: %d %s %s", code, &out, &diagnostic)
		}
	}
}

func TestManagedWorkflowPromptUsesExplicitRuntime(t *testing.T) {
	command := runtime.Command{Binary: "/tool", Args: []string{"--prompt", "base"}}
	seat := runtime.SeatSnapshot{RunID: "run-1", AgentID: "main", Generation: 1}
	got := addManagedWorkflowPrompt(command, seat, "/private/bin/agentflow", "/private/run dir", "", true)
	if !strings.Contains(got.Args[len(got.Args)-1], "'/private/bin/agentflow' workflow start") ||
		!strings.Contains(got.Args[len(got.Args)-1], "--run-dir '/private/run dir'") ||
		!strings.Contains(got.Args[len(got.Args)-1], "--request-id task-$(date +%s%N)") ||
		!strings.Contains(got.Args[len(got.Args)-1], "Wait for a new explicit user task") ||
		!strings.Contains(got.Args[len(got.Args)-1], "'/private/bin/agentflow' workflow drain --run 'run-1' --run-dir '/private/run dir' --agent 'main' --generation 1 --revision REVISION_FROM_START_JSON") ||
		!strings.Contains(got.Args[len(got.Args)-1], "awaiting_actor=true") ||
		!strings.Contains(got.Args[len(got.Args)-1], "--timeout 30m") ||
		!strings.Contains(got.Args[len(got.Args)-1], `--task "用户本轮明确任务"`) ||
		strings.Contains(got.Args[len(got.Args)-1], `--task "任务内容"`) {
		t.Fatalf("missing explicit workflow command: %q", got.Args[len(got.Args)-1])
	}
	initial := addManagedWorkflowPrompt(command, seat, "/private/bin/agentflow", "/private/run dir", "task text", true)
	if !strings.Contains(initial.Args[len(initial.Args)-1], "already accepted and dispatched") || !strings.Contains(initial.Args[len(initial.Args)-1], "task text") || strings.Contains(initial.Args[len(initial.Args)-1], "Workflow start rule") || !strings.Contains(initial.Args[len(initial.Args)-1], "REVISION_FROM_STATUS_JSON") {
		t.Fatalf("initial task must drain without starting twice: %q", initial.Args[len(initial.Args)-1])
	}
	worker := addWorkflowReplyProtocol(command, "codex")
	if strings.Contains(worker.Args[len(worker.Args)-1], "workflow start") || !strings.Contains(worker.Args[len(worker.Args)-1], "workflow_reply") || !strings.Contains(worker.Args[len(worker.Args)-1], "sender.surface_id") || !strings.Contains(worker.Args[len(worker.Args)-1], "--from \"$AGENTFLOW_AGENT_ID\"") {
		t.Fatalf("worker prompt lacks structured reply protocol: %q", worker.Args[len(worker.Args)-1])
	}
	opencode := addWorkflowReplyProtocol(command, "opencode")
	if !strings.Contains(opencode.Args[len(opencode.Args)-1], "collects this assistant result directly") || strings.Contains(opencode.Args[len(opencode.Args)-1], "--from \"$AGENTFLOW_AGENT_ID\"") {
		t.Fatalf("OpenCode prompt must return native structured result, not send a cmux message: %q", opencode.Args[len(opencode.Args)-1])
	}
	if !strings.Contains(initial.Args[len(initial.Args)-1], "workflow decide --run 'run-1'") || !strings.Contains(initial.Args[len(initial.Args)-1], "--message-id <workflow_current.dispatch_id>") {
		t.Fatalf("lead prompt lacks a runnable approval decision: %q", initial.Args[len(initial.Args)-1])
	}
}

func TestSeatValidatedFailureAndOutputFailure(t *testing.T) {
	t.Setenv("AGENTFLOW_NATIVE_OPTIONS", "")
	t.Setenv("PATH", t.TempDir())
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	var plan runtime.StartupPlan
	var out, diagnostic bytes.Buffer
	if code := Run([]string{"up", "-f", "../../examples/team.yaml", "--dry-run", "--json"}, &out, &diagnostic, "test"); code != 0 {
		t.Fatal(diagnostic.String())
	}
	if err := json.Unmarshal(out.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	snapshot, err := runtime.NewSnapshot(&plan, "seat-test")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "snapshot.json")
	if err := runtime.WriteSnapshot(path, snapshot); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"main", "builder", "reviewer", "unknown"} {
		out.Reset()
		diagnostic.Reset()
		args := []string{"_seat", "--agent", id, "--snapshot", path}
		if code := Run(args, &out, &diagnostic, "test"); code != 1 {
			t.Fatalf("exit = %d", code)
		}
		var result struct {
			Status   string
			Executed bool
			Error    runtime.SeatError
		}
		if err := json.Unmarshal(out.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		want := runtime.SeatToolAdapterUnimplemented
		if id != "unknown" && plan.Config.Agents[id].Tool != "claude" {
			want = runtime.NativeTargetRequired
		}
		if id == "unknown" {
			want = runtime.SeatAgentUnknown
		}
		if result.Status != "FAILED" || result.Executed || result.Error.Code != want || result.Error.Path == "" || !strings.Contains(diagnostic.String(), want) || strings.Contains(out.String(), `"native"`) || strings.Contains(out.String(), `"READY"`) || strings.Contains(out.String(), `"ACK"`) {
			t.Fatalf("wrong structured failure: %s %s", &out, &diagnostic)
		}
		for _, a := range plan.Config.Agents {
			if strings.Contains(out.String()+diagnostic.String(), a.Prompt) {
				t.Fatal("entry leaked prompt")
			}
		}
		if code := Run(args, brokenWriter{}, &diagnostic, "test"); code != 1 || !strings.Contains(diagnostic.String(), "write output") {
			t.Fatalf("wrong output failure: %d %s", code, &diagnostic)
		}
	}
	out.Reset()
	diagnostic.Reset()
	if err := os.WriteFile(path, []byte(`{"fixture-secret":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	if code := Run([]string{"_seat", "--snapshot", path, "--agent", "main"}, &out, &diagnostic, "test"); code != 1 || !strings.Contains(out.String(), runtime.SeatSnapshotInvalid) || strings.Contains(out.String()+diagnostic.String(), "fixture-secret") {
		t.Fatalf("wrong sanitized snapshot error: %d %s %s", code, &out, &diagnostic)
	}
}
