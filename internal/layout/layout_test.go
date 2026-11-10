package layout

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"agentflow/internal/config"
)

func number(value float64) *float64 { return &value }

func leaf(id string) config.Layout { return config.Layout{Agent: id} }

func split(direction string, ratio *float64, children ...config.Layout) config.Layout {
	return config.Layout{Split: direction, Ratio: ratio, Children: children}
}

func snapshot(tree config.Layout, ids ...string) *config.Resolved {
	agents := make(map[string]config.ResolvedAgent, len(ids))
	for _, id := range ids {
		agents[id] = config.ResolvedAgent{
			Role: id + " role", Prompt: id + " prompt", PromptFile: "/not-read/" + id + ".md",
			Tool: "opencode", Cwd: "/not-checked/work", Permissions: "full", Session: "fresh",
			Model: "chosen", Args: []string{"--literal", "$(do-not-execute)", "../literal"},
		}
	}
	return &config.Resolved{Layout: tree, Agents: agents}
}

func TestCompileExamples(t *testing.T) {
	var outputs []*Node
	for _, ext := range []string{"yaml", "json"} {
		t.Run(ext, func(t *testing.T) {
			source, err := config.Load(filepath.Join("..", "..", "examples", "team."+ext))
			if err != nil {
				t.Fatal(err)
			}
			resolved, err := config.Resolve(source, config.ResolveOptions{})
			if err != nil {
				t.Fatal(err)
			}
			got, err := Compile(resolved)
			if err != nil {
				t.Fatal(err)
			}
			wantLeaf := func(id string) Node { return Node{Agent: &Agent{ID: id, Config: resolved.Agents[id]}} }
			want := &Node{Direction: Horizontal, Ratio: 0.4, Children: []Node{
				wantLeaf("main"), {Direction: Vertical, Ratio: 0.5, Children: []Node{wantLeaf("builder"), wantLeaf("reviewer")}},
			}}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("compiled example differs:\ngot %+v\nwant %+v", got, want)
			}
			outputs = append(outputs, got)
		})
	}
	if len(outputs) != 2 || !reflect.DeepEqual(outputs[0], outputs[1]) {
		t.Fatal("YAML and JSON compile differently")
	}
}

func TestCompileDirectionsAndRatios(t *testing.T) {
	for _, tc := range []struct {
		name, source string
		ratio        *float64
		direction    Direction
		wantRatio    float64
	}{
		{"columns default", "columns", nil, Horizontal, 0.5},
		{"rows default", "rows", nil, Vertical, 0.5},
		{"lower inclusive", "columns", number(0.1), Horizontal, 0.1},
		{"upper inclusive", "rows", number(0.9), Vertical, 0.9},
		{"explicit midpoint", "rows", number(0.5), Vertical, 0.5},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := snapshot(split(tc.source, tc.ratio, leaf("main"), leaf("builder")), "main", "builder")
			got, err := Compile(s)
			if err != nil {
				t.Fatal(err)
			}
			if got.Direction != tc.direction || got.Ratio != tc.wantRatio || got.Agent != nil || len(got.Children) != 2 {
				t.Fatalf("incorrect split: %+v", got)
			}
			for i, id := range []string{"main", "builder"} {
				child := got.Children[i]
				if child.Agent == nil || child.Agent.ID != id || !reflect.DeepEqual(child.Agent.Config, s.Agents[id]) || child.Direction != "" || child.Ratio != 0 || child.Children != nil {
					t.Fatalf("incorrect leaf %d: %+v", i, child)
				}
			}
		})
	}
	for _, args := range [][]string{nil, {}, {"literal"}} {
		t.Run(fmt.Sprintf("single leaf args=%v", args), func(t *testing.T) {
			s := snapshot(leaf("main"), "main")
			a := s.Agents["main"]
			a.Args = args
			s.Agents["main"] = a
			got, err := Compile(s)
			if err != nil || !reflect.DeepEqual(got, &Node{Agent: &Agent{ID: "main", Config: a}}) {
				t.Fatalf("single leaf differs: %+v, %v", got, err)
			}
		})
	}
}

func requireField(t *testing.T, err error, path, message string) {
	t.Helper()
	var field *config.FieldError
	if !errors.As(err, &field) || field.Path != path || !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), message) {
		t.Fatalf("want field %q containing %q, got %v", path, message, err)
	}
}

func TestInvalidLayouts(t *testing.T) {
	for _, tc := range []struct {
		name    string
		tree    config.Layout
		ids     []string
		path    string
		message string
	}{
		{"empty node", config.Layout{}, nil, "layout.split", "agent leaf is required"},
		{"invalid direction", split("diagonal", nil, leaf("main"), leaf("builder")), []string{"main", "builder"}, "layout.split", "columns or rows"},
		{"leaf split", config.Layout{Agent: "main", Split: "rows"}, []string{"main"}, "layout.split", "leaf cannot contain split"},
		{"leaf ratio", config.Layout{Agent: "main", Ratio: number(0.5)}, []string{"main"}, "layout.ratio", "leaf cannot contain ratio"},
		{"leaf empty children", config.Layout{Agent: "main", Children: []config.Layout{}}, []string{"main"}, "layout.children", "leaf cannot contain children"},
		{"leaf children", config.Layout{Agent: "main", Children: []config.Layout{leaf("builder")}}, []string{"main", "builder"}, "layout.children", "leaf cannot contain children"},
		{"split agent", config.Layout{Agent: "main", Split: "columns", Children: []config.Layout{leaf("main"), leaf("builder")}}, []string{"main", "builder"}, "layout.split", "leaf cannot contain split"},
		{"missing children", split("rows", nil), nil, "layout.children", "exactly two"},
		{"one child", split("rows", nil, leaf("main")), []string{"main"}, "layout.children", "exactly two"},
		{"three children", split("rows", nil, leaf("main"), leaf("builder"), leaf("reviewer")), []string{"main", "builder", "reviewer"}, "layout.children", "exactly two"},
		{"unknown", leaf("other"), []string{"main"}, "layout.agent", "unknown agent other"},
		{"duplicate", split("rows", nil, leaf("main"), leaf("main")), []string{"main"}, "layout.children[1].agent", "first at layout.children[0].agent"},
		{"missing", leaf("main"), []string{"reviewer", "main", "builder"}, "layout", "missing agent reference builder"},
		{"nested unknown", split("columns", nil, leaf("main"), split("rows", nil, leaf("builder"), leaf("other"))), []string{"main", "builder", "reviewer"}, "layout.children[1].children[1].agent", "unknown agent"},
		{"nested ratio", split("columns", nil, leaf("main"), split("rows", number(0.99), leaf("builder"), leaf("reviewer"))), []string{"main", "builder", "reviewer"}, "layout.children[1].ratio", "0.1 and 0.9"},
		{"nested conflict", split("rows", nil, leaf("main"), config.Layout{Agent: "builder", Ratio: number(0.5)}), []string{"main", "builder"}, "layout.children[1].ratio", "leaf cannot contain ratio"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requireField(t, Validate(&tc.tree, tc.ids), tc.path, tc.message)
			got, err := Compile(snapshot(tc.tree, tc.ids...))
			requireField(t, err, tc.path, tc.message)
			if got != nil {
				t.Fatal("failed compile returned partial output")
			}
		})
	}
	for _, ratio := range []float64{0, -1, 0.099999, 0.900001, 1, math.NaN(), math.Inf(1), math.Inf(-1)} {
		t.Run(fmt.Sprintf("invalid ratio %v", ratio), func(t *testing.T) {
			s := snapshot(split("columns", number(ratio), leaf("main"), leaf("builder")), "main", "builder")
			requireField(t, Validate(&s.Layout, []string{"main", "builder"}), "layout.ratio", "finite number between")
			got, err := Compile(s)
			requireField(t, err, "layout.ratio", "finite number between")
			if got != nil {
				t.Fatal("failed compile returned partial output")
			}
		})
	}
	requireField(t, Validate(nil, []string{"main"}), "layout", "required")
	got, err := Compile(nil)
	requireField(t, err, "layout", "resolved configuration is required")
	if got != nil {
		t.Fatal("nil input returned output")
	}
}

func TestDeepLayout(t *testing.T) {
	const depth = 64
	tree := leaf("last")
	ids := []string{"last"}
	for i := depth - 1; i >= 0; i-- {
		id := fmt.Sprintf("agent%d", i)
		ids = append(ids, id)
		direction := "columns"
		if i%2 == 1 {
			direction = "rows"
		}
		tree = split(direction, nil, leaf(id), tree)
	}
	s := snapshot(tree, ids...)
	got, err := Compile(s)
	if err != nil {
		t.Fatal(err)
	}
	node := got
	for i := 0; i < depth; i++ {
		want := Horizontal
		if i%2 == 1 {
			want = Vertical
		}
		if node.Direction != want || node.Ratio != 0.5 || len(node.Children) != 2 || node.Children[0].Agent.ID != fmt.Sprintf("agent%d", i) {
			t.Fatalf("incorrect depth %d: %+v", i, node)
		}
		node = &node.Children[1]
	}
	if node.Agent == nil || node.Agent.ID != "last" {
		t.Fatalf("missing final leaf: %+v", node)
	}
	// Error paths retain every level, including the deepest leaf.
	deepest := &s.Layout
	for i := 0; i < depth; i++ {
		deepest = &deepest.Children[1]
	}
	deepest.Agent = "unknown"
	_, err = Compile(s)
	requireField(t, err, "layout"+strings.Repeat(".children[1]", depth)+".agent", "unknown agent")
}

func encode(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestPureAndDetached(t *testing.T) {
	for _, tc := range []struct {
		name string
		tree config.Layout
	}{
		{"default ratio", split("columns", nil, leaf("main"), leaf("builder"))},
		{"explicit ratio", split("rows", number(0.4), leaf("main"), leaf("builder"))},
		{"invalid input", split("rows", number(1), leaf("main"), leaf("builder"))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := snapshot(tc.tree, "main", "builder")
			ids := []string{"main", "builder"}
			before, idsBefore := encode(t, s), encode(t, ids)
			validationErr := Validate(&s.Layout, ids)
			got, err := Compile(s)
			if encode(t, s) != before || encode(t, ids) != idsBefore {
				t.Fatal("Validate or Compile modified its input")
			}
			if tc.name == "invalid input" {
				if validationErr == nil || err == nil || got != nil {
					t.Fatal("expected validation and compilation to fail")
				}
				return
			}
			if validationErr != nil || err != nil {
				t.Fatalf("valid tree rejected: %v / %v", validationErr, err)
			}
			outputBefore := encode(t, got)
			s.Agents["main"].Args[0] = "input changed"
			s.Layout.Children[0].Agent = "input changed"
			if s.Layout.Ratio != nil {
				*s.Layout.Ratio = 0.8
			}
			if encode(t, got) != outputBefore {
				t.Fatal("input edit changed compiled tree")
			}
			inputBefore := encode(t, s)
			got.Children[0].Agent.Config.Args[1] = "output changed"
			got.Children[1].Agent.ID = "output changed"
			got.Ratio = 0.7
			if encode(t, s) != inputBefore {
				t.Fatal("output edit changed input snapshot")
			}
		})
	}
}
