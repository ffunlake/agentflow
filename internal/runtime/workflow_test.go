package runtime

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"agentflow/internal/config"
	"agentflow/internal/layout"
	"agentflow/internal/store"
)

func workflowConfig(t *testing.T) (*config.Config, *config.Resolved) {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("..", "..", "examples", "workflow.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "workflow.yaml")
	if err := os.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}
	c, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	r, err := config.Resolve(c, config.ResolveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return c, r
}

func refreshWorkflowDigest(t *testing.T, r *config.Resolved) {
	t.Helper()
	r.Digest = ""
	body, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(body)
	r.Digest = hex.EncodeToString(sum[:])
}

func TestPipelineNativePromptRequestsRoleHandoff(t *testing.T) {
	body, err := json.Marshal(map[string]any{
		"revision": 1, "recipient": "builder", "purpose": "execute", "step": "stage-1",
		"instruction": "Build this feature", "acceptance": []string{}, "handoff_required": true,
	})
	if err != nil {
		t.Fatal(err)
	}
	prompt := workflowNativePrompt(string(body))
	if !strings.Contains(prompt, "pipeline stage") || !strings.Contains(prompt, "only this role's part") ||
		!strings.Contains(prompt, "kind=handoff") || !strings.Contains(prompt, "at least one non-empty") ||
		strings.Contains(prompt, "review success requires") {
		t.Fatalf("pipeline worker received wrong result protocol: %s", prompt)
	}
}

func TestBuildReviewNativePromptRequiresHandoffEvidence(t *testing.T) {
	body, err := json.Marshal(map[string]any{
		"revision": 1, "recipient": "builder", "purpose": "execute", "step": "implement",
		"instruction": "Build this feature", "acceptance": []string{"feature works"}, "handoff_required": true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if prompt := workflowNativePrompt(string(body)); !strings.Contains(prompt, "at least one non-empty, truthful evidence reference") {
		t.Fatalf("build-review worker was not told the required handoff evidence: %s", prompt)
	}
}

func TestWorkflowFrozenPlanSnapshotAndDatabaseRecovery(t *testing.T) {
	c, resolved := workflowConfig(t)
	plan, err := Compile(resolved, Options{})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := NewSnapshot(plan, "workflow-freeze")
	if err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal(snapshot.Config)
	if !reflect.DeepEqual(plan.Config.Workflow, snapshot.Config.Workflow) || plan.Config.Workflow == snapshot.Config.Workflow {
		t.Fatal("startup and run workflow must match and be detached")
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.resolved.json")
	if err := WriteSnapshot(path, snapshot); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(dir, "state.db")
	db, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateRun(t.Context(), store.Run{RunID: snapshot.RunID, ConfigSnapshot: string(want), ConfigDigest: snapshot.Config.Digest, StartupPhase: Starting}); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	// Changes at every upstream layer must not affect the published run.
	c.Workflow.Steps[0].Acceptance[0] = "source changed"
	resolved.Workflow.Steps[0].Instruction = "resolved changed"
	plan.Config.Workflow.Steps[0].Acceptance[0] = "plan changed"
	if err := os.WriteFile(c.SourcePath, []byte("workflow: null\n"), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := ReadSnapshot(path)
	if err != nil {
		t.Fatal(err)
	}
	actual, _ := json.Marshal(got.Config)
	if string(actual) != string(want) {
		t.Fatal("upstream changes altered frozen run workflow")
	}
	db, err = store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	run, err := db.GetRun(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var recovered config.Resolved
	if err := json.Unmarshal([]byte(run.ConfigSnapshot), &recovered); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(recovered.Workflow, got.Config.Workflow) || recovered.Digest != got.Config.Digest {
		t.Fatal("database reopen did not preserve frozen workflow")
	}
	if _, err := Compile(&recovered, Options{}); err != nil {
		t.Fatal(err)
	}
}

func TestWorkflowSharedValidationAtCompileAndSnapshot(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*config.Workflow)
	}{
		{"empty object", func(w *config.Workflow) { *w = config.Workflow{} }},
		{"unknown agent", func(w *config.Workflow) { w.Steps[0].Execute.Agent = "missing" }},
		{"self review", func(w *config.Workflow) { w.Steps[0].Reviewer = w.Steps[0].Execute.Agent }},
		{"handoff", func(w *config.Workflow) { w.Steps[0].HandoffRequired = false }},
		{"attempts", func(w *config.Workflow) { w.Steps[0].MaxAttempts = 11 }},
		{"transition", func(w *config.Workflow) { w.Steps[0].Transitions.Changes = "next" }},
		{"cycle", func(w *config.Workflow) { w.Steps[0].Next = w.Entry }},
		{"unknown next", func(w *config.Workflow) { w.Steps[0].Next = "missing" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, r := workflowConfig(t)
			plan, err := Compile(r, Options{})
			if err != nil {
				t.Fatal(err)
			}
			snapshot, err := NewSnapshot(plan, "invalid-workflow")
			if err != nil {
				t.Fatal(err)
			}
			tc.mutate(r.Workflow)
			if _, err := layout.Compile(r); err == nil {
				t.Fatal("layout compile bypassed workflow validation")
			}
			if _, err := Compile(r, Options{}); err == nil {
				t.Fatal("startup compile bypassed workflow validation")
			}
			tc.mutate(snapshot.Config.Workflow)
			// A fresh digest must not let invalid semantics bypass validation.
			refreshWorkflowDigest(t, snapshot.Config)
			if err := snapshot.Validate(); err == nil {
				t.Fatal("snapshot accepted invalid workflow with correct digest")
			}
		})
	}
}

func TestWorkflowSnapshotRejectsTamperingAndEncodingAmbiguity(t *testing.T) {
	_, r := workflowConfig(t)
	plan, err := Compile(r, Options{})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := NewSnapshot(plan, "workflow-encoding")
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.resolved.json")
	for _, tc := range []struct{ name, old, replacement string }{
		{"capitalized workflow", `"workflow":`, `"Workflow":`},
		{"uppercase workflow", `"workflow":`, `"WORKFLOW":`},
		{"capitalized before canonical", `"workflow":`, `"Workflow":null,"workflow":`},
		{"capitalized after canonical", `"digest":`, `"Workflow":null,"digest":`},
		{"duplicate workflow", `"workflow":`, `"workflow":null,"workflow":`},
		{"duplicate digest", `"digest":`, `"digest":"discarded","digest":`},
		{"null workflow", `"workflow":`, `"workflow":null,"discarded_workflow":`},
		{"duplicate field", `"entry":`, `"entry":"discarded","entry":`},
		{"null criterion", `"acceptance":[`, `"acceptance":[null,`},
		{"unknown field", `"execute":{`, `"execute":{"unknown":true,`},
		{"wrong boolean", `"handoff_required":true`, `"handoff_required":"true"`},
		{"tampered workflow", `"max_attempts":3`, `"max_attempts":4`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			changed := strings.Replace(string(body), tc.old, tc.replacement, 1)
			if changed == string(body) {
				t.Fatal("fixture unchanged")
			}
			if err := os.WriteFile(path, []byte(changed), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := ReadSnapshot(path); err == nil {
				t.Fatal("accepted ambiguous/tampered workflow snapshot")
			}
		})
	}
}
