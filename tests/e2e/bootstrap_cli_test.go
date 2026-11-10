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

	"agentflow/internal/config"
	"agentflow/internal/runtime"
	"agentflow/internal/store"
)

func TestBootstrapCLIContinuesFromFilesAcrossProcesses(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(t.TempDir(), "agentflow")
	build := exec.Command("go", "build", "-mod=readonly", "-o", binary, "./cmd/agentflow")
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("%s %v", out, err)
	}
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	promptPath := filepath.Join(dir, "source-prompt.md")
	if err := os.WriteFile(promptPath, []byte("PRIVATE original prompt body"), 0600); err != nil {
		t.Fatal(err)
	}
	persistent, fresh := "persistent", "fresh"
	c := &config.Config{Version: 1, Name: "bootstrap-team", Terminal: "cmux", Lead: "main", ContextDir: filepath.Join(dir, "contexts"), SourcePath: filepath.Join(dir, "team.json"),
		Agents: map[string]config.Agent{"main": {Role: "lead", Session: &persistent, PromptFile: &promptPath}, "worker": {Role: "builder", Session: &fresh, PromptFile: &promptPath}},
		Layout: &config.Layout{Split: "columns", Children: []config.Layout{{Agent: "main"}, {Agent: "worker"}}}}
	frozen, err := config.Resolve(c, config.ResolveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := runtime.Compile(frozen, runtime.Options{})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := runtime.NewSnapshot(plan, "bootstrap-run")
	if err != nil {
		t.Fatal(err)
	}
	snapshotPath := filepath.Join(dir, "config.resolved.json")
	if err := runtime.WriteSnapshot(snapshotPath, snapshot); err != nil {
		t.Fatal(err)
	}
	run, err := runtime.CreateContext(snapshotPath, "bootstrap-run")
	if err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(frozen)
	if err := db.CreateRun(t.Context(), store.Run{RunID: run.ID(), ConfigDigest: frozen.Digest, ConfigSnapshot: string(body)}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"main", "worker"} {
		for _, g := range []int64{1, 2} {
			// No live native binding exists; persisted current identity is enough
			// to prepare a file continuation package, never to mark READY.
			if err := db.CreateSeat(t.Context(), store.Seat{RunID: run.ID(), AgentID: id, Generation: g, Current: g == 2, Tool: frozen.Agents[id].Tool, Status: "STARTING"}); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(promptPath); err != nil {
		t.Fatal(err)
	}
	// Continuation relies on the database's frozen configuration and context
	// references. The old startup artifact can also be unavailable.
	if err := os.Remove(snapshotPath); err != nil {
		t.Fatal(err)
	}
	invoke := func(command, id, gen, runID string, want int) string {
		t.Helper()
		cmd := exec.Command(binary, command, "--run", runID, "--run-dir", dir, "--agent", id, "--generation", gen, "--json")
		for _, entry := range os.Environ() {
			if !strings.HasPrefix(entry, "PATH=") {
				cmd.Env = append(cmd.Env, entry)
			}
		}
		cmd.Env = append(cmd.Env, "PATH=")
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		err := cmd.Run()
		code := 0
		if err != nil {
			var exit *exec.ExitError
			if !errors.As(err, &exit) {
				t.Fatal(err)
			}
			code = exit.ExitCode()
		}
		if code != want || (want != 2 && !json.Valid(stdout.Bytes())) || (want == 0 && stderr.Len() != 0) {
			t.Fatalf("exit=%d out=%s err=%s", code, &stdout, &stderr)
		}
		if strings.Contains(stdout.String(), "PRIVATE") {
			t.Fatal("prompt body copied")
		}
		return stdout.String()
	}
	for _, id := range []string{"main", "worker"} {
		first := invoke("rebuild", id, "2", run.ID(), 0)
		second := invoke("bootstrap", id, "2", run.ID(), 0)
		if first != second {
			t.Fatal("unstable process/alias result")
		}
		var result runtime.BootstrapResult
		if err := json.Unmarshal([]byte(first), &result); err != nil {
			t.Fatal(err)
		}
		if result.Status != "BOOTSTRAP_READY" || result.Executed || result.RuntimeCommunication != "UNVERIFIED" || result.Bootstrap.Protocol.Session != frozen.Agents[id].Session {
			t.Fatal(first)
		}
		if _, err := os.Stat(result.Path); err != nil {
			t.Fatal(err)
		}
	}
	invoke("rebuild", "worker", "1", run.ID(), 1)
	invoke("rebuild", "worker", "3", run.ID(), 1)
	invoke("rebuild", "unknown", "2", run.ID(), 1)
	invoke("rebuild", "worker", "2", "other-run", 1)
	invoke("rebuild", "worker", "0", run.ID(), 2)
	a, _ := run.Agent("worker")
	if err := os.WriteFile(filepath.Join(a.Path(), "initial", "manifest.json"), []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	invoke("rebuild", "worker", "2", run.ID(), 1)
	invoke("rebuild", "main", "2", run.ID(), 0) // agent-local corruption stays isolated
}
