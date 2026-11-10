package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const workflowJSON = `{
  "entry":"build", "completion":"done", "steps":[
    {"id":"build", "execute":{"agent":"main"}, "reviewer":"reviewer",
     "instruction":"Implement the scoped change", "acceptance":["Tests pass", "Evidence saved"],
     "handoff_required":true, "max_attempts":3,
     "transitions":{"approve":"next","changes":"self","retry":"self","blocked":"pause"}, "next":"verify"},
    {"id":"verify", "execute":{"agent":"reviewer"}, "reviewer":"main",
     "instruction":"Verify the evidence", "acceptance":["Independent review complete"],
     "handoff_required":true, "max_attempts":1,
     "transitions":{"approve":"next","changes":"self","retry":"self","blocked":"pause"}, "next":"done"}
  ]
}`

func workflowSource(w string) string {
	text := strings.Replace(minimal, `"agents": {"main": {"role": "lead role", "prompt": "lead prompt"}}`,
		`"agents":{"main":{"role":"lead role","prompt":"lead prompt"},"reviewer":{"role":"review","prompt":"Review independently"}}`, 1)
	text = strings.Replace(text, `"layout": {"agent": "main"}`, `"layout":{"split":"columns","children":[{"agent":"main"},{"agent":"reviewer"}]}`, 1)
	return strings.TrimSuffix(text, "}") + `,"workflow":` + w + "}"
}

func workflowYAML(t *testing.T, c *Config) []byte {
	t.Helper()
	// Use the source JSON contract to omit unset override pointers; yaml tags
	// intentionally retain legacy source-schema behavior without omitempty.
	body, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]any
	if err := json.Unmarshal(body, &object); err != nil {
		t.Fatal(err)
	}
	body, err = yaml.Marshal(object)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func TestWorkflowStrictSchema(t *testing.T) {
	for _, tc := range []struct{ name, old, replacement, path, message string }{
		{"null workflow", workflowJSON, `null`, "workflow", "null"},
		{"empty workflow", workflowJSON, `{}`, "workflow.entry", "non-empty"},
		{"unknown workflow", `"entry":"build"`, `"typo":1,"entry":"build"`, "workflow.typo", "unknown"},
		{"duplicate entry", `"entry":"build"`, `"entry":"build","entry":"build"`, "workflow.entry", "duplicate"},
		{"unknown step", `"id":"build"`, `"typo":1,"id":"build"`, "workflow.steps[0].typo", "unknown"},
		{"duplicate step key", `"id":"build"`, `"id":"build","id":"build"`, "workflow.steps[0].id", "duplicate"},
		{"unknown execute", `"agent":"main"`, `"agent":"main","typo":1`, "workflow.steps[0].execute.typo", "unknown"},
		{"unknown transition", `"approve":"next"`, `"approve":"next","typo":"pause"`, "workflow.steps[0].transitions.typo", "unknown"},
		{"duplicate transition", `"approve":"next"`, `"approve":"next","approve":"next"`, "workflow.steps[0].transitions.approve", "duplicate"},
		{"null step", `{"id":"build"`, `null, {"id":"build"`, "workflow.steps[0]", "null"},
		{"null bool", `"handoff_required":true`, `"handoff_required":null`, "workflow.steps[0].handoff_required", "null"},
		{"quoted bool", `"handoff_required":true`, `"handoff_required":"true"`, "workflow.steps[0].handoff_required", "boolean"},
		{"numeric bool", `"handoff_required":true`, `"handoff_required":1`, "workflow.steps[0].handoff_required", "boolean"},
		{"implicit string", `"instruction":"Implement the scoped change"`, `"instruction":true`, "workflow.steps[0].instruction", "string"},
		{"numeric criterion", `"Tests pass"`, `42`, "workflow.steps[0].acceptance[0]", "string"},
		{"string attempts", `"max_attempts":3`, `"max_attempts":"3"`, "workflow.steps[0].max_attempts", "integer"},
		{"float attempts", `"max_attempts":3`, `"max_attempts":1.5`, "workflow.steps[0].max_attempts", "integer"},
		{"null steps", `"steps":[`, `"steps":null,"unused":[`, "workflow.steps", "null"},
	} {
		for _, ext := range []string{"yaml", "json"} {
			t.Run(tc.name+"/"+ext, func(t *testing.T) {
				w := strings.Replace(workflowJSON, tc.old, tc.replacement, 1)
				_, err := Load(writeConfig(t, t.TempDir(), "team."+ext, workflowSource(w)))
				requireField(t, err, tc.path, tc.message)
			})
		}
	}
}

func TestWorkflowSemanticValidation(t *testing.T) {
	for _, tc := range []struct {
		name, path, message string
		mutate              func(*Workflow)
	}{
		{"missing entry", "workflow.entry", "non-empty", func(w *Workflow) { w.Entry = "" }},
		{"unknown entry", "workflow.entry", "existing step", func(w *Workflow) { w.Entry = "missing" }},
		{"entry completion", "workflow.entry", "existing step", func(w *Workflow) { w.Entry = w.Completion }},
		{"completion NUL", "workflow.completion", "NUL", func(w *Workflow) { w.Completion = "done\x00" }},
		{"completion collision", "workflow.steps[0].id", "distinct", func(w *Workflow) { w.Completion = "build" }},
		{"empty steps", "workflow.steps", "at least one", func(w *Workflow) { w.Steps = nil }},
		{"duplicate ID", "workflow.steps[1].id", "duplicate", func(w *Workflow) { w.Steps[1].ID = "build" }},
		{"empty ID", "workflow.steps[0].id", "non-empty", func(w *Workflow) { w.Steps[0].ID = " " }},
		{"unknown executor", "workflow.steps[0].execute.agent", "unknown agent", func(w *Workflow) { w.Steps[0].Execute.Agent = "missing" }},
		{"unknown reviewer", "workflow.steps[0].reviewer", "unknown agent", func(w *Workflow) { w.Steps[0].Reviewer = "missing" }},
		{"self review", "workflow.steps[0].reviewer", "differ", func(w *Workflow) { w.Steps[0].Reviewer = "main" }},
		{"empty instruction", "workflow.steps[0].instruction", "non-empty", func(w *Workflow) { w.Steps[0].Instruction = "\n " }},
		{"NUL instruction", "workflow.steps[0].instruction", "NUL", func(w *Workflow) { w.Steps[0].Instruction = "a\x00b" }},
		{"empty acceptance", "workflow.steps[0].acceptance", "at least one", func(w *Workflow) { w.Steps[0].Acceptance = nil }},
		{"empty criterion", "workflow.steps[0].acceptance[0]", "non-empty", func(w *Workflow) { w.Steps[0].Acceptance[0] = " " }},
		{"NUL criterion", "workflow.steps[0].acceptance[0]", "NUL", func(w *Workflow) { w.Steps[0].Acceptance[0] = "a\x00b" }},
		{"handoff missing false", "workflow.steps[0].handoff_required", "true", func(w *Workflow) { w.Steps[0].HandoffRequired = false }},
		{"attempts zero", "workflow.steps[0].max_attempts", "1 and 10", func(w *Workflow) { w.Steps[0].MaxAttempts = 0 }},
		{"attempts negative", "workflow.steps[0].max_attempts", "1 and 10", func(w *Workflow) { w.Steps[0].MaxAttempts = -1 }},
		{"attempts eleven", "workflow.steps[0].max_attempts", "1 and 10", func(w *Workflow) { w.Steps[0].MaxAttempts = 11 }},
		{"approve target", "workflow.steps[0].transitions.approve", "next", func(w *Workflow) { w.Steps[0].Transitions.Approve = "verify" }},
		{"changes cross step", "workflow.steps[0].transitions.changes", "self", func(w *Workflow) { w.Steps[0].Transitions.Changes = "verify" }},
		{"retry cross step", "workflow.steps[0].transitions.retry", "self", func(w *Workflow) { w.Steps[0].Transitions.Retry = "build" }},
		{"blocked cross step", "workflow.steps[0].transitions.blocked", "pause", func(w *Workflow) { w.Steps[0].Transitions.Blocked = "next" }},
		{"missing transition", "workflow.steps[0].transitions.approve", "next", func(w *Workflow) { w.Steps[0].Transitions = WorkflowTransitions{} }},
		{"broken next", "workflow.steps[0].next", "non-empty", func(w *Workflow) { w.Steps[0].Next = "" }},
		{"unknown target", "workflow.steps[0].next", "unknown target", func(w *Workflow) { w.Steps[0].Next = "missing" }},
		{"multiple endpoints", "workflow.steps", "exactly one", func(w *Workflow) { w.Steps[0].Next = w.Completion }},
		{"no endpoint", "workflow.steps", "exactly one", func(w *Workflow) { w.Steps[1].Next = "build" }},
		{"self forward cycle", "workflow.steps", "cycle", func(w *Workflow) { w.Steps[0].Next = "build" }},
		{"forward cycle", "workflow.steps", "cycle", func(w *Workflow) {
			w.Steps = append(w.Steps, w.Steps[1])
			w.Steps[2].ID = "finish"
			w.Steps[1].Next = "build"
		}},
		{"isolated step", "workflow.steps", "disconnected", func(w *Workflow) {
			w.Steps = append(w.Steps, w.Steps[0])
			w.Steps[2].ID = "isolated"
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := loadText(t, workflowSource(workflowJSON))
			tc.mutate(c.Workflow)
			requireField(t, Validate(c), tc.path, tc.message)
			_, err := Resolve(c, ResolveOptions{})
			requireField(t, err, tc.path, tc.message)
			body, err := json.Marshal(c)
			if err != nil {
				t.Fatal(err)
			}
			_, err = Load(writeConfig(t, t.TempDir(), "team.json", string(body)))
			// nil slices serialize as null and must be rejected before semantic validation.
			if err == nil {
				t.Fatal("loader accepted invalid workflow")
			}
		})
	}
}

func TestWorkflowYAMLOnlyStrictness(t *testing.T) {
	c := loadText(t, workflowSource(workflowJSON))
	body := workflowYAML(t, c)
	text := string(body)
	if _, err := Load(writeConfig(t, t.TempDir(), "valid.yaml", text)); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, old, replacement string }{
		{"yes", "handoff_required: true", "handoff_required: yes"},
		{"on", "handoff_required: true", "handoff_required: on"},
		{"quoted", "handoff_required: true", "handoff_required: 'true'"},
		{"alias", "entry: build", "entry: &entry build\n    completion_alias: *entry"},
		{"scalar alias", "entry: build", "entry: &entry build"},
		{"merge", "entry: build", "<<: {entry: build}\n    entry: build"},
		{"implicit instruction", "instruction: Implement the scoped change", "instruction: 2026-11-03"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			changed := strings.Replace(text, tc.old, tc.replacement, 1)
			if tc.name == "scalar alias" {
				changed = strings.Replace(changed, "instruction: Implement the scoped change", "instruction: *entry", 1)
			}
			if changed == text {
				t.Fatal("mutation did not change fixture")
			}
			if _, err := Load(writeConfig(t, t.TempDir(), "team.yaml", changed)); err == nil {
				t.Fatal("accepted YAML coercion/alias/merge")
			}
		})
	}
}

func TestWorkflowNormalizationDigestAndIsolation(t *testing.T) {
	dir := t.TempDir()
	c, err := Load(writeConfig(t, dir, "team.json", workflowSource(workflowJSON)))
	if err != nil {
		t.Fatal(err)
	}
	c.Workflow.Steps[0].MaxAttempts = 10
	r, err := Resolve(c, ResolveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(r)
	c.Workflow.Steps[0], c.Workflow.Steps[1] = c.Workflow.Steps[1], c.Workflow.Steps[0]
	yamlBody := workflowYAML(t, c)
	yamlConfig, err := Load(writeConfig(t, dir, "team.yaml", string(yamlBody)))
	if err != nil {
		t.Fatal(err)
	}
	other, err := Resolve(yamlConfig, ResolveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	r.SourcePath, other.SourcePath = "", ""
	if !reflect.DeepEqual(r, other) || other.Workflow.Steps[0].ID != "build" {
		t.Fatal("YAML/JSON or declaration order changed normalized workflow/digest")
	}
	c.Workflow.Steps[1].Acceptance[0] = "changed criterion"
	c.Workflow.Steps[1].Instruction = "changed instruction"
	c.Workflow.Entry = "changed"
	if err := os.WriteFile(filepath.Join(dir, "team.json"), []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(r)
	if string(before) != string(after) {
		t.Fatal("source object/file edits mutated resolved workflow")
	}
	yamlConfig.Workflow.Steps[0].Instruction += " changed"
	changed, err := Resolve(yamlConfig, ResolveOptions{})
	if err != nil || changed.Digest == r.Digest {
		t.Fatalf("workflow edit did not change digest: %v", err)
	}
	legacy := loadText(t, minimal)
	old, err := Resolve(legacy, ResolveOptions{})
	if err != nil || old.Workflow != nil {
		t.Fatalf("legacy config broken: %v", err)
	}
	encoded, _ := json.Marshal(old)
	if strings.Contains(string(encoded), `"workflow"`) {
		t.Fatal("absent workflow changed legacy serialized contract")
	}
}

func TestWorkflowExamplesAndSingleStep(t *testing.T) {
	var first *Resolved
	for _, ext := range []string{"yaml", "json"} {
		c, err := Load(filepath.Join("..", "..", "examples", "workflow."+ext))
		if err != nil {
			t.Fatal(err)
		}
		r, err := Resolve(c, ResolveOptions{})
		if err != nil {
			t.Fatal(err)
		}
		r.SourcePath = ""
		if first == nil {
			first = r
		} else if !reflect.DeepEqual(first, r) {
			t.Fatal("workflow examples do not produce equal frozen snapshots")
		}
	}
	c := loadText(t, workflowSource(workflowJSON))
	c.Workflow.Steps = c.Workflow.Steps[:1]
	c.Workflow.Steps[0].Next = c.Workflow.Completion
	c.Workflow.Steps[0].MaxAttempts = 1
	if _, err := Resolve(c, ResolveOptions{}); err != nil {
		t.Fatalf("valid single-step workflow rejected: %v", err)
	}
}

func TestApprovalWorkflowExamplesAndReturnTargets(t *testing.T) {
	var first *Resolved
	for _, ext := range []string{"yaml", "json"} {
		c, err := Load(filepath.Join("..", "..", "examples", "approval."+ext))
		if err != nil {
			t.Fatal(err)
		}
		r, err := Resolve(c, ResolveOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if r.Workflow.Kind() != WorkflowApproval || r.Workflow.Steps[0].Approver != "reviewer" ||
			r.Workflow.Steps[1].Execute.Agent != "reviewer" || r.Workflow.Steps[1].Approver != "main" ||
			r.Workflow.Steps[1].Transitions.Changes != "prepare" {
			t.Fatalf("approval example lost configured work/approval route: %+v", r.Workflow)
		}
		r.SourcePath = ""
		if first == nil {
			first = r
		} else if !reflect.DeepEqual(first, r) {
			t.Fatal("approval YAML and JSON snapshots differ")
		}
	}

	c, err := Load(filepath.Join("..", "..", "examples", "approval.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	c.Workflow.Steps[1].Transitions.Changes = "prepare"
	c.Workflow.Steps[0].Transitions.Changes = "deliver"
	_, err = Resolve(c, ResolveOptions{})
	requireField(t, err, "workflow.steps[0].transitions.changes", "earlier step")
	c.Workflow.Steps[0].Transitions.Changes = "self"
	c.Workflow.Steps[0].Approver = c.Workflow.Steps[0].Execute.Agent
	_, err = Resolve(c, ResolveOptions{})
	requireField(t, err, "workflow.steps[0].approver", "differ")
}

func TestPipelineFlowValidatesSixRolesAndFreezesCanonicalRoute(t *testing.T) {
	c := loadText(t, minimal)
	ids := []string{"main", "role1", "role2", "role3", "role4", "role5", "role6"}
	for _, id := range ids[1:] {
		prompt := "Do only this role's part and hand off evidence."
		c.Agents[id] = Agent{Role: id, Prompt: &prompt}
	}
	var tree func([]string) Layout
	tree = func(agents []string) Layout {
		if len(agents) == 1 {
			return Layout{Agent: agents[0]}
		}
		middle := len(agents) / 2
		return Layout{Split: "columns", Children: []Layout{tree(agents[:middle]), tree(agents[middle:])}}
	}
	c.Layout = ptrLayout(tree(ids))
	c.Workflow = &Workflow{Type: WorkflowPipeline, Flow: " main -> role1 -> role2 -> role3 -> role4 -> role5 -> role6 -> main "}
	if err := Validate(c); err != nil {
		t.Fatal(err)
	}
	resolved, err := Resolve(c, ResolveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Workflow.Flow != "main -> role1 -> role2 -> role3 -> role4 -> role5 -> role6 -> main" ||
		!reflect.DeepEqual(resolved.Workflow.ParticipantIDs(), ids[1:]) || len(resolved.Workflow.RuntimeSteps()) != 6 {
		t.Fatalf("pipeline route did not freeze as six ordered stages: %+v", resolved.Workflow)
	}
	defaultAttempts := DefaultPipelineAttempts
	c.Workflow.MaxAttempts = &defaultAttempts
	explicitDefault, err := Resolve(c, ResolveOptions{})
	if err != nil || explicitDefault.Digest != resolved.Digest {
		t.Fatalf("implicit and explicit pipeline retry defaults differ: %v", err)
	}
	c.Workflow.MaxAttempts = nil
	jsonSource, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	sourceDir := t.TempDir()
	jsonLoaded, err := Load(writeConfig(t, sourceDir, "team.json", string(jsonSource)))
	if err != nil {
		t.Fatal(err)
	}
	yamlLoaded, err := Load(writeConfig(t, sourceDir, "team.yaml", string(workflowYAML(t, c))))
	if err != nil {
		t.Fatal(err)
	}
	jsonResolved, err := Resolve(jsonLoaded, ResolveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	yamlResolved, err := Resolve(yamlLoaded, ResolveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	jsonResolved.SourcePath, yamlResolved.SourcePath = "", ""
	if !reflect.DeepEqual(jsonResolved, yamlResolved) {
		t.Fatal("six-role pipeline YAML and JSON did not freeze equivalently")
	}
	encoded, err := json.Marshal(resolved)
	if err != nil {
		t.Fatal(err)
	}
	var reopened Resolved
	if err := json.Unmarshal(encoded, &reopened); err != nil || reopened.Workflow.Flow != resolved.Workflow.Flow || reopened.Digest != resolved.Digest {
		t.Fatalf("pipeline snapshot did not round-trip: %v %+v", err, reopened.Workflow)
	}
}

func TestPipelineFlowRejectsWrongLeadUnknownRoleAndMixedSchema(t *testing.T) {
	for _, tc := range []struct {
		name, flow, path, message string
		addReviewStep             bool
	}{
		{"wrong start", "role1 -> role2 -> main", "workflow.flow", "configured lead", false},
		{"wrong end", "main -> role1 -> role2", "workflow.flow", "configured lead", false},
		{"unknown role", "main -> missing -> main", "workflow.flow", "unknown", false},
		{"empty hop", "main -> -> main", "workflow.flow", "unknown or empty", false},
		{"no work stage", "main -> main", "workflow.flow", "lead -> at least one", false},
		{"mixed schema", "main -> reviewer -> main", "workflow", "cannot also define", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := loadText(t, workflowSource(workflowJSON))
			c.Workflow = &Workflow{Type: WorkflowPipeline, Flow: tc.flow}
			if tc.addReviewStep {
				c.Workflow.Steps = []WorkflowStep{{ID: "unused"}}
			}
			if tc.name == "mixed schema" {
				c.Workflow.Entry = "unused"
			}
			requireField(t, Validate(c), tc.path, tc.message)
		})
	}
}

func TestPipelineRetryLimitRejectsExplicitZero(t *testing.T) {
	c := loadText(t, workflowSource(`{"type":"pipeline","flow":"main -> reviewer -> main"}`))
	zero := 0
	c.Workflow.MaxAttempts = &zero
	requireField(t, Validate(c), "workflow.max_attempts", "1 and 10")
}

func TestPipelineFlowCanContainOneOrManyRoles(t *testing.T) {
	short := loadText(t, workflowSource(workflowJSON))
	short.Workflow = &Workflow{Type: WorkflowPipeline, Flow: "main -> reviewer -> main"}
	if err := Validate(short); err != nil || len(short.Workflow.RuntimeSteps()) != 1 {
		t.Fatalf("one-role pipeline should be valid: steps=%d err=%v", len(short.Workflow.RuntimeSteps()), err)
	}
	many := loadText(t, minimal)
	ids := []string{"main", "a", "b", "c", "d", "e", "f"}
	for _, id := range ids[1:] {
		prompt := "role prompt"
		many.Agents[id] = Agent{Role: id, Prompt: &prompt}
	}
	var tree func([]string) Layout
	tree = func(agents []string) Layout {
		if len(agents) == 1 {
			return Layout{Agent: agents[0]}
		}
		return Layout{Split: "rows", Children: []Layout{tree(agents[:1]), tree(agents[1:])}}
	}
	root := tree(ids)
	many.Layout = &root
	many.Workflow = &Workflow{Type: WorkflowPipeline, Flow: "main -> a -> b -> c -> d -> e -> f -> main"}
	if err := Validate(many); err != nil || len(many.Workflow.RuntimeSteps()) != 6 {
		t.Fatalf("six-role pipeline should be valid: steps=%d err=%v", len(many.Workflow.RuntimeSteps()), err)
	}
}

func ptrLayout(l Layout) *Layout { return &l }

func intPointer(value int) *int { return &value }
