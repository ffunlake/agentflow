package integration_test

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentflow/internal/config"
	runcontext "agentflow/internal/context"
	"agentflow/internal/runtime"
)

// This exercises Resolve -> frozen RunSnapshot -> context files -> reopen,
// using two runs and two agents with distinct cwd and prompt-file sources.
func TestContextTwoRunsTwoAgentsAndFrozenPrompt(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // No external tool is available to this API.
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	prompt := "Frozen private instructions: do not copy into context summaries"
	source := filepath.Join(dir, "role-source.md")
	if err := os.WriteFile(source, []byte(prompt), 0600); err != nil {
		t.Fatal(err)
	}
	c := &config.Config{Version: 1, Name: "context-team", Terminal: "cmux", Lead: "main",
		ContextDir: filepath.Join(dir, "contexts"), SourcePath: filepath.Join(dir, "team.json"),
		Agents: map[string]config.Agent{"main": {Role: "lead", PromptFile: &source}, "worker": {Role: "developer", PromptFile: &source}},
		Layout: &config.Layout{Split: "columns", Children: []config.Layout{{Agent: "main"}, {Agent: "worker"}}}}
	frozen, err := config.Resolve(c, config.ResolveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := runtime.Compile(frozen, runtime.Options{})
	if err != nil {
		t.Fatal(err)
	}
	paths := map[string]string{}
	for _, id := range []string{"run-a", "run-b"} {
		s, err := runtime.NewSnapshot(plan, id)
		if err != nil {
			t.Fatal(err)
		}
		runDir := filepath.Join(dir, id)
		if err := os.Mkdir(runDir, 0700); err != nil {
			t.Fatal(err)
		}
		paths[id] = filepath.Join(runDir, "config.resolved.json")
		if err := runtime.WriteSnapshot(paths[id], s); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Remove(source); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.CreateContext(paths["run-a"], "run-b"); !errors.Is(err, runcontext.ErrIdentity) {
		t.Fatalf("accepted cross-run frozen snapshot: %v", err)
	}
	for _, runID := range []string{"run-a", "run-b"} {
		r, err := runtime.CreateContext(paths[runID], runID)
		if err != nil {
			t.Fatal(err)
		}
		if r.Path() != filepath.Join(frozen.ContextDir, runID) {
			t.Fatalf("unexpected run path: %s", r.Path())
		}
		for _, agentID := range []string{"main", "worker"} {
			a, _ := r.Agent(agentID)
			s, m, err := a.Read("initial")
			if err != nil {
				t.Fatal(err)
			}
			sum := sha256.Sum256([]byte(prompt))
			if s.Protocol.PromptSHA256 != hex.EncodeToString(sum[:]) || s.Protocol.FrozenPrompt.Path != paths[runID] || len(m.Files) != 5 {
				t.Fatalf("lost frozen role reference: %+v", s)
			}
			s.Revision = "work-1"
			s.Progress.Text = runID + "/" + agentID
			s.Pending.References = []runcontext.Reference{{Path: filepath.Join(dir, "report-reference.md"), Purpose: "independent evidence; not merged"}}
			if _, err := a.Publish(*s); err != nil {
				t.Fatal(err)
			}
			other, _ := r.Agent(map[string]string{"main": "worker", "worker": "main"}[agentID])
			if _, err := other.Publish(*s); !errors.Is(err, runcontext.ErrIdentity) {
				t.Fatalf("cross-agent publish accepted: %v", err)
			}
			s.RunID = "another-run"
			if _, err := a.Publish(*s); !errors.Is(err, runcontext.ErrIdentity) {
				t.Fatalf("cross-run publish accepted: %v", err)
			}
		}
		if _, err := runtime.CreateContext(paths[runID], runID); !errors.Is(err, runcontext.ErrExists) {
			t.Fatalf("recreated existing context: %v", err)
		}
	}
	// Reopen all four identities independently; no in-memory state supplies truth.
	for _, runID := range []string{"run-a", "run-b"} {
		r, err := runcontext.Open(frozen.ContextDir, runID)
		if err != nil {
			t.Fatal(err)
		}
		for _, id := range []string{"main", "worker"} {
			a, _ := r.Agent(id)
			s, _, err := a.Read("work-1")
			if err != nil || s.Progress.Text != runID+"/"+id {
				t.Fatalf("context leaked: %+v %v", s, err)
			}
		}
	}
	// Context artifacts must contain references and digests, not the source prompt.
	if err := filepath.Walk(frozen.ContextDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		body, err := os.ReadFile(path)
		if err == nil && strings.Contains(string(body), prompt) {
			t.Fatalf("copied full prompt into context: %s", path)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	// Moving run-a's descriptor into run-b is rejected before any write.
	aBody, err := os.ReadFile(filepath.Join(frozen.ContextDir, "run-a", "run.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(frozen.ContextDir, "run-b", "run.json"), aBody, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := runcontext.Open(frozen.ContextDir, "run-b"); !errors.Is(err, runcontext.ErrIdentity) {
		t.Fatalf("cross-run descriptor accepted: %v", err)
	}
}
