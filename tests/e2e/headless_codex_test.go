package e2e_test

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"agentflow/internal/config"
	"agentflow/internal/coordinator"
)

// Real AgentFlow CLI processes and IPC; native tools are executable fixtures.
func TestHeadlessCodexCLIWorkflowAndRestart(t *testing.T) {
	testHeadlessCLIWorkflowAndRestart(t, "codex", "success")
}

func TestHeadlessCodexSystemTempCLIWorkflowAndRestart(t *testing.T) {
	testHeadlessCLIWorkflowAndRestart(t, "codex", "system-temp")
}

func TestHeadlessOpenCodeCLIWorkflowAndRestart(t *testing.T) {
	for _, mode := range []string{"success", "blocked-resume"} {
		t.Run(mode, func(t *testing.T) { testHeadlessCLIWorkflowAndRestart(t, "opencode", mode) })
	}
}

func TestHeadlessClaudeCLIWorkflowAndRestart(t *testing.T) {
	for _, mode := range []string{"success", "blocked-resume", "bare", "oauth", "oauth-blocked-resume"} {
		t.Run(mode, func(t *testing.T) { testHeadlessCLIWorkflowAndRestart(t, "claude", mode) })
	}
}

func testHeadlessCLIWorkflowAndRestart(t *testing.T, tool, mode string) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	binDir := t.TempDir()
	binary, native := filepath.Join(binDir, "agentflow"), filepath.Join(binDir, tool+" fixture")
	for _, build := range []struct{ output, pkg string }{{binary, "./cmd/agentflow"}, {native, "./internal/runtime/testdata/headless-codex"}} {
		cmd := exec.Command("go", "build", "-o", build.output, build.pkg)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s %v", out, err)
		}
	}
	dir := shortRunDir(t)
	cwd := shortRunDir(t)
	source, err := config.Load(filepath.Join(root, "examples", "headless-"+tool+".yaml"))
	if err != nil {
		t.Fatal(err)
	}
	source.Defaults.Cwd, source.ContextDir = &cwd, filepath.Join(dir, "context")
	blocked := mode == "blocked-resume" || mode == "oauth-blocked-resume"
	if tool == "claude" && (mode == "bare" || strings.HasPrefix(mode, "oauth")) {
		flag := "--bare"
		if strings.HasPrefix(mode, "oauth") {
			flag = "--safe-mode"
		}
		for id, agent := range source.Agents {
			agent.Args = []string{flag}
			source.Agents[id] = agent
		}
	}
	encoded, _ := json.Marshal(source)
	file := filepath.Join(t.TempDir(), "team.json")
	if err := os.WriteFile(file, encoded, 0600); err != nil {
		t.Fatal(err)
	}
	args := []string{"up", "-f", file, "--headless-" + tool, "--native-binary", native, "--run", "run", "--json"}
	var startup coordinator.UpResult
	if mode == "system-temp" {
		// Register before the process cleanup so the owner always stops before
		// test-owned files are deleted, even if a later assertion fails.
		t.Cleanup(func() {
			if startup.RunDir != "" {
				_ = os.RemoveAll(startup.RunDir)
			}
		})
	} else {
		args = append(args, "--run-dir", dir)
	}
	stop := startCoordinatorProcessWithResult(t, binary, dir, "run", &startup, args...)
	if mode == "system-temp" {
		parent, err := filepath.EvalSymlinks(os.TempDir())
		if err != nil || !filepath.IsAbs(startup.RunDir) || filepath.Dir(startup.RunDir) != parent || !strings.HasPrefix(filepath.Base(startup.RunDir), "agentflow-run-") {
			t.Fatal(startup, err)
		}
		dir = startup.RunDir
	} else if startup.RunDir != dir {
		t.Fatalf("explicit directory changed: %q want %q", startup.RunDir, dir)
	}
	archive := headlessEvidence(t, tool, mode)
	if tool == "claude" {
		archive("source-at-start.json", encoded)
		frozen, err := os.ReadFile(filepath.Join(dir, "config.resolved.json"))
		if err != nil {
			t.Fatal(err)
		}
		archive("config.resolved.json", frozen)
		// Change the source to the opposite auth choice after startup. Resume and
		// reopen must continue to use the detached startup snapshot and DB digest.
		for id, agent := range source.Agents {
			agent.Args = []string{"--safe-mode"}
			if strings.HasPrefix(mode, "oauth") {
				agent.Args = nil
			}
			source.Agents[id] = agent
		}
		changed, _ := json.Marshal(source)
		if err := os.WriteFile(file, changed, 0600); err != nil {
			t.Fatal(err)
		}
		archive("source-after-start.json", changed)
	}
	workflow := func(kind, revision, id string, wantSuccess bool) coordinator.Result {
		t.Helper()
		args := []string{"workflow", kind, "--run", "run", "--run-dir", dir, "--agent", "main", "--generation", "1", "--revision", revision, "--request-id", id, "--json"}
		if kind == "resume" {
			args = append(args, "--reason", "fixture decision resolved")
		}
		cmd := exec.Command(binary, args...)
		out, err := cmd.Output()
		if (err == nil) != wantSuccess {
			t.Fatalf("workflow err=%v output=%s", err, out)
		}
		var r coordinator.Result
		if err := json.Unmarshal(out, &r); err != nil {
			t.Fatalf("%s %v", out, err)
		}
		return r
	}
	revision, count := int64(3), 4
	if blocked {
		nativeMode := "blocked"
		if tool == "claude" {
			nativeMode = "permission-denied-blocked"
		}
		if err := os.WriteFile(filepath.Join(cwd, "mode"), []byte(nativeMode), 0600); err != nil {
			t.Fatal(err)
		}
	}
	r := workflow("start", "0", "start-1", true)
	if blocked {
		if r.WorkflowCurrent == nil || r.WorkflowCurrent.Phase != "PAUSED" || r.WorkflowCurrent.Revision != 2 {
			t.Fatal(r)
		}
		if tool == "claude" {
			before := headlessDBSnapshot(t, dir)
			calls, err := os.ReadFile(filepath.Join(cwd, "calls.log"))
			if err != nil {
				t.Fatal(err)
			}
			archive("paused-before-reopen.db.json", before)
			archive("paused-before-reopen.calls.jsonl", calls)
			stop()
			stop = startCoordinatorProcess(t, binary, dir, "run", "coordinator", "--headless-"+tool, "--native-binary", native, "--run", "run", "--run-dir", dir, "--json")
			headlessUnchanged(t, dir, cwd, "paused-after-reopen", before, calls, archive)
			if replay := workflow("start", "0", "start-1", true); replay.Dispatch != nil || *replay.Executed {
				t.Fatal("paused replay", replay)
			}
			headlessUnchanged(t, dir, cwd, "paused-after-replay", before, calls, archive)
		}
		if err := os.WriteFile(filepath.Join(cwd, "mode"), []byte("success"), 0600); err != nil {
			t.Fatal(err)
		}
		r = workflow("resume", "2", "resume-1", true)
		revision, count = 5, 5
		if again := workflow("resume", "2", "resume-1", true); again.Dispatch != nil || *again.Executed {
			t.Fatal("resume replay", again)
		}
	}
	if r.WorkflowCurrent == nil || r.WorkflowCurrent.Phase != "COMPLETED" || r.WorkflowCurrent.Revision != revision {
		t.Fatal(r)
	}
	calls, err := os.ReadFile(filepath.Join(cwd, "calls.log"))
	if err != nil || strings.Count(string(calls), "\n") != count {
		t.Fatal(string(calls), err)
	}
	if tool == "claude" {
		modeFlag, forbiddenMode := " --bare ", " --safe-mode "
		if strings.HasPrefix(mode, "oauth") {
			modeFlag, forbiddenMode = forbiddenMode, modeFlag
		}
		for i, line := range strings.Split(strings.TrimSpace(string(calls)), "\n") {
			var args []string
			if json.Unmarshal([]byte(line), &args) != nil {
				t.Fatal("invalid fixture argv")
			}
			joined := " " + strings.Join(args, " ") + " "
			for _, flag := range []string{" --print ", modeFlag, " --verbose ", " --output-format stream-json ", " --permission-prompts none ", " --strict-mcp-config ", " --setting-sources  ", " --disable-slash-commands ", " --no-chrome "} {
				if !strings.Contains(joined, flag) {
					t.Fatal("missing managed Claude flags", args)
				}
			}
			if strings.Contains(joined, forbiddenMode) || strings.Contains(joined, "--dangerously-skip-permissions") || (i < 2) == strings.Contains(joined, " --resume ") {
				t.Fatal("unexpected permissions or session argv", args)
			}
			if i >= 2 {
				id := "00000000-0000-4000-8000-000000000001"
				if i == count-1 {
					id = "00000000-0000-4000-8000-000000000002"
				}
				if !strings.HasSuffix(joined, " --resume "+id+" ") {
					t.Fatal("wrong Claude resume session", args)
				}
			}
		}
	}
	var completed []byte
	if tool == "claude" {
		completed = headlessDBSnapshot(t, dir)
		archive("completed-before-reopen.db.json", completed)
		archive("completed-before-reopen.calls.jsonl", calls)
	}
	stop()
	startCoordinatorProcess(t, binary, dir, "run", "coordinator", "--headless-"+tool, "--native-binary", native, "--run", "run", "--run-dir", dir, "--json")
	if tool == "claude" {
		headlessUnchanged(t, dir, cwd, "completed-after-reopen", completed, calls, archive)
	}
	replay := workflow("start", "0", "start-1", true)
	if replay.Dispatch != nil || *replay.Executed {
		t.Fatal(replay)
	}
	status := workflow("status", "0", "status-1", true)
	if status.Status != "WORKFLOW_STATUS" || status.WorkflowCurrent.Revision != revision || *status.Executed {
		t.Fatal(status)
	}
	drain := workflow("drain", strconv.FormatInt(revision, 10), "drain-1", true)
	if drain.WorkflowCurrent.Phase != "COMPLETED" || *drain.Executed {
		t.Fatal(drain)
	}
	beforeStale := headlessDBSnapshot(t, dir)
	stale := workflow("drain", "1", "stale-drain", true)
	if stale.WorkflowCurrent == nil || stale.WorkflowCurrent.Phase != "COMPLETED" || stale.WorkflowCurrent.Revision != revision || stale.Executed == nil || *stale.Executed || stale.Dispatch != nil || len(stale.Diagnostics) != 0 {
		t.Fatal(stale)
	}
	if afterStale := headlessDBSnapshot(t, dir); !bytes.Equal(afterStale, beforeStale) {
		t.Fatal("stale terminal drain changed durable state")
	}
	after, err := os.ReadFile(filepath.Join(cwd, "calls.log"))
	if err != nil || string(after) != string(calls) {
		t.Fatal("restart/replay called native", err)
	}
	if tool == "claude" {
		headlessUnchanged(t, dir, cwd, "completed-after-replay", completed, calls, archive)
	}
}
