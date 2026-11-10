package config

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestResolvedWorkflowStrictJSONRecovery(t *testing.T) {
	c := loadText(t, workflowSource(workflowJSON))
	r, err := Resolve(c, ResolveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	workflowField := `"workflow":` + string(mustWorkflowJSON(t, r.Workflow))
	for _, tc := range []struct{ name, old, replacement, path, message string }{
		{"capitalized workflow", `"workflow":`, `"Workflow":`, "Workflow", "unknown"},
		{"uppercase workflow", `"workflow":`, `"WORKFLOW":`, "WORKFLOW", "unknown"},
		{"mixed workflow", `"workflow":`, `"wOrKfLoW":`, "wOrKfLoW", "unknown"},
		{"capitalized null", workflowField, `"Workflow":null`, "Workflow", "unknown"},
		{"capitalized unknown nested field", workflowField, `"Workflow":{"unknown":true}`, "Workflow", "unknown"},
		{"capitalized duplicate nested key", `"workflow":`, `"Workflow":{"entry":"discarded","entry":"build"},"workflow":`, "Workflow", "unknown"},
		{"capitalized then canonical", `"workflow":`, `"Workflow":null,"workflow":`, "Workflow", "unknown"},
		{"canonical then capitalized", `"digest":`, `"Workflow":null,"digest":`, "Workflow", "unknown"},
		{"duplicate workflow", `"workflow":`, `"workflow":{},"workflow":`, "workflow", "duplicate"},
		{"duplicate identical workflow", `"workflow":`, `"workflow":` + string(mustWorkflowJSON(t, r.Workflow)) + `,"workflow":`, "workflow", "duplicate"},
		{"escaped duplicate workflow", `"workflow":`, `"work\u0066low":{},"workflow":`, "workflow", "duplicate"},
		{"duplicate digest", `"digest":`, `"digest":"discarded","digest":`, "digest", "duplicate"},
		{"capitalized digest", `"digest":`, `"Digest":`, "Digest", "unknown"},
		{"null workflow", `"workflow":`, `"workflow":null,"discarded":`, "workflow", "null"},
		{"nested case", `"entry":`, `"Entry":`, "workflow.Entry", "unknown"},
		{"step case", `"execute":`, `"Execute":`, "workflow.steps[0].Execute", "unknown"},
		{"execute case", `"execute":{"agent":"main"`, `"execute":{"Agent":"main"`, "workflow.steps[0].execute.Agent", "unknown"},
		{"transition case", `"approve":`, `"Approve":`, "workflow.steps[0].transitions.Approve", "unknown"},
		{"duplicate entry", `"entry":`, `"entry":"discarded","entry":`, "workflow.entry", "duplicate"},
		{"duplicate execute", `"execute":`, `"execute":{},"execute":`, "workflow.steps[0].execute", "duplicate"},
		{"duplicate agent", `"execute":{"agent":"main"`, `"execute":{"agent":"discarded","agent":"main"`, "workflow.steps[0].execute.agent", "duplicate"},
		{"duplicate transition", `"approve":`, `"approve":"discarded","approve":`, "workflow.steps[0].transitions.approve", "duplicate"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			changed := strings.Replace(string(body), tc.old, tc.replacement, 1)
			if changed == string(body) {
				t.Fatal("fixture unchanged")
			}
			// A failed restore must leave an existing receiver intact.
			got := *r
			err := json.Unmarshal([]byte(changed), &got)
			requireField(t, err, tc.path, tc.message)
			if !reflect.DeepEqual(got, *r) {
				t.Fatal("failed restore mutated receiver")
			}
		})
	}
}

func TestResolvedWorkflowJSONRecoveryCompatibility(t *testing.T) {
	for _, source := range []string{minimal, workflowSource(workflowJSON)} {
		c := loadText(t, source)
		r, err := Resolve(c, ResolveOptions{})
		if err != nil {
			t.Fatal(err)
		}
		body, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		for _, text := range []string{string(body), strings.Replace(string(body), `"workflow":`, `"work\u0066low":`, 1)} {
			var got Resolved
			if err := json.Unmarshal([]byte(text), &got); err != nil {
				t.Fatal(err)
			}
			got.SourcePath = r.SourcePath // Diagnostic metadata is not serialized.
			if !reflect.DeepEqual(got, *r) {
				t.Fatal("restore changed resolved configuration or digest")
			}
		}
	}
}

func mustWorkflowJSON(t *testing.T, w *Workflow) []byte {
	t.Helper()
	body, err := json.Marshal(w)
	if err != nil {
		t.Fatal(err)
	}
	return body
}
