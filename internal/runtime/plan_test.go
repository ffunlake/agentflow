package runtime

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"agentflow/internal/config"
	"agentflow/internal/layout"
)

func example(t *testing.T, ext string) *config.Resolved {
	t.Helper()
	c, err := config.Load("../../examples/team." + ext)
	if err != nil {
		t.Fatal(err)
	}
	r, err := config.Resolve(c, config.ResolveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestExamplesEquivalentAndStable(t *testing.T) {
	var previous *StartupPlan
	for _, ext := range []string{"yaml", "json", "yaml"} {
		snapshot := example(t, ext)
		plan, err := Compile(snapshot, Options{})
		if err != nil {
			t.Fatal(err)
		}
		if previous != nil && !reflect.DeepEqual(previous, plan) {
			t.Fatal("equivalent YAML/JSON snapshots or repeated calls produced different startup plans")
		}
		if plan.Status != "PLANNED" || plan.Executed || plan.Run.ConfigDigest != snapshot.Digest || plan.Run.Name != "dev-team" || plan.Run.Lead != "main" || plan.Run.Terminal != "cmux" || plan.Config.SourcePath != "" {
			t.Fatalf("incorrect run summary: %+v", plan)
		}
		for i, id := range []string{"builder", "main", "reviewer"} {
			seat := plan.Seats[i]
			a := snapshot.Agents[id]
			if seat.ID != id || seat.Order != i || seat.Tool != a.Tool || seat.Role != a.Role || seat.Permissions != a.Permissions || seat.Session != a.Session || seat.Cwd != a.Cwd || seat.Prompt.TextRef != "config.agents."+id+".prompt" {
				t.Fatalf("incorrect seat: %+v", seat)
			}
		}
		want := map[string][]PaneStep{
			"main":     {{layout.Horizontal, 0.4, 0}},
			"builder":  {{layout.Horizontal, 0.4, 1}, {layout.Vertical, 0.5, 0}},
			"reviewer": {{layout.Horizontal, 0.4, 1}, {layout.Vertical, 0.5, 1}},
		}
		for _, seat := range plan.Seats {
			if !reflect.DeepEqual(seat.PanePath, want[seat.ID]) {
				t.Fatalf("%s pane path: %+v", seat.ID, seat.PanePath)
			}
		}
		previous = plan
	}
}

func TestPlanDetachedAndPreservesArgumentsAsSnapshotData(t *testing.T) {
	snapshot := example(t, "yaml")
	a := snapshot.Agents["builder"]
	a.Model = "a model; $(touch forbidden)"
	a.Args = []string{"--literal", "$(touch forbidden); & | > ' \"", "--credential=fixture-secret"}
	snapshot.Agents["builder"] = a
	before, _ := json.Marshal(snapshot)
	plan, err := Compile(snapshot, Options{})
	if err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(snapshot)
	if string(before) != string(after) {
		t.Fatal("Compile modified input")
	}
	if !reflect.DeepEqual(plan.Config.Agents["builder"].Args, a.Args) || plan.Config.Agents["builder"].Model != a.Model {
		t.Fatal("raw tool arguments/model lost from frozen snapshot")
	}
	for _, seat := range plan.Seats {
		want := Command{Binary: "agentflow", Args: []string{"_seat", "--snapshot", "config.resolved.json", "--agent", seat.ID}}
		if !reflect.DeepEqual(seat.Wrapper, want) {
			t.Fatalf("wrapper contains more than snapshot/seat references: %+v", seat.Wrapper)
		}
		encoded, _ := json.Marshal(seat.Wrapper)
		if strings.Contains(string(encoded), "fixture-secret") || strings.Contains(string(encoded), a.Model) || strings.Contains(string(encoded), a.Prompt) {
			t.Fatal("tool configuration leaked into argv")
		}
	}
	// All three representations must be detached from the input, including the
	// source layout ratio, nested slices and leaf agent configuration.
	snapshot.Agents["builder"].Args[0] = "changed input"
	*snapshot.Layout.Ratio = 0.8
	plan.Config.Agents["builder"].Args[1] = "changed output"
	if plan.Layout.Children[1].Children[0].Agent.Config.Args[0] != "--literal" || plan.Config.Agents["builder"].Args[0] != "--literal" || *plan.Config.Layout.Ratio != 0.4 || plan.Layout.Ratio != 0.4 || snapshot.Agents["builder"].Args[1] != a.Args[1] {
		t.Fatal("startup plan aliases snapshot or layout data")
	}
}

func TestWrapperCommandLiteralArguments(t *testing.T) {
	options := Options{WrapperBinary: "/private/path with spaces/agentflow", SnapshotPath: "/private/run $(touch forbidden); ' \"/snapshot.json"}
	for _, tool := range []string{"opencode", "codex", "claude"} {
		command, err := WrapperCommand("developer", tool, options)
		if err != nil {
			t.Fatal(err)
		}
		if command.Binary != options.WrapperBinary || !reflect.DeepEqual(command.Args, []string{"_seat", "--snapshot", options.SnapshotPath, "--agent", "developer"}) {
			t.Fatalf("paths were split, interpolated or shell escaped: %+v", command)
		}
	}
	for _, tc := range []struct {
		id, tool string
		options  Options
	}{
		{"main", "sh", Options{}}, {"main", "codex; touch forbidden", Options{}},
		{"main", "", Options{}}, {"main;touch", "codex", Options{}},
		{"--agent", "codex", Options{}}, {"main", "codex", Options{WrapperBinary: "\x00"}},
		{"main", "codex", Options{SnapshotPath: "bad\x00path"}},
	} {
		if _, err := WrapperCommand(tc.id, tc.tool, tc.options); err == nil {
			t.Fatalf("accepted invalid wrapper input: %+v", tc)
		}
	}
}

func TestCompileErrors(t *testing.T) {
	if _, err := Compile(nil, Options{}); err == nil {
		t.Fatal("accepted nil snapshot")
	}
	for _, mutate := range []func(*config.Resolved){
		func(r *config.Resolved) { r.Layout.Children = nil },
		func(r *config.Resolved) { a := r.Agents["main"]; a.Tool = "sh"; r.Agents["main"] = a },
	} {
		r := example(t, "yaml")
		mutate(r)
		if _, err := Compile(r, Options{}); err == nil {
			t.Fatal("accepted malformed snapshot")
		}
	}
}

func TestSingleSeatPromptSnapshotNeedsNoIO(t *testing.T) {
	dir := t.TempDir()
	promptPath := filepath.Join(dir, "role.md")
	if err := os.WriteFile(promptPath, []byte("Frozen role text"), 0600); err != nil {
		t.Fatal(err)
	}
	promptFile := "role.md"
	c := &config.Config{
		Version: 1, Name: "solo", Terminal: "cmux", Lead: "solo", ContextDir: "context",
		SourcePath: filepath.Join(dir, "team.yaml"),
		Agents:     map[string]config.Agent{"solo": {Role: "Developer", PromptFile: &promptFile}},
		Layout:     &config.Layout{Agent: "solo"},
	}
	r, err := config.Resolve(c, config.ResolveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(promptPath); err != nil {
		t.Fatal(err)
	}
	plan, err := Compile(r, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Seats) != 1 || plan.Seats[0].PanePath == nil || len(plan.Seats[0].PanePath) != 0 || plan.Seats[0].Prompt.File != promptPath || plan.Config.Agents["solo"].Prompt != "Frozen role text" {
		t.Fatalf("single seat or prompt reference incorrect: %+v", plan)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("Compile wrote runtime state: %v, %v", entries, err)
	}
}
