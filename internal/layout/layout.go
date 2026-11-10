// Package layout validates and compiles binary layout trees as pure data.
// It does not read files, construct commands, or start terminal processes.
package layout

import (
	"fmt"
	"math"
	"sort"

	"agentflow/internal/config"
)

// Direction is the cmux split direction, independent of any transport or command.
type Direction string

const (
	Horizontal Direction = "horizontal" // source columns
	Vertical   Direction = "vertical"   // source rows
)

// Node is either a split (Direction, Ratio, two Children) or an agent leaf
// (Agent only). Children retain source order. Ratio is the target fraction for
// the first child; terminal size constraints are applied by a future adapter.
type Node struct {
	Direction Direction `json:"direction,omitempty"`
	Ratio     float64   `json:"ratio,omitempty"`
	Children  []Node    `json:"children,omitempty"`
	Agent     *Agent    `json:"agent,omitempty"`
}

// Agent binds a leaf's stable role ID to a detached resolved configuration.
type Agent struct {
	ID     string               `json:"id"`
	Config config.ResolvedAgent `json:"config"`
}

// Validate checks tree shape, finite ratio bounds, and exactly-once references
// to agentIDs. Errors are *config.FieldError values rooted at layout. A nil
// ratio means 0.5; validation never writes this default back into the tree.
func Validate(tree *config.Layout, agentIDs []string) error {
	if tree == nil {
		return invalid("layout", "is required")
	}
	known := make(map[string]bool, len(agentIDs))
	for _, id := range agentIDs {
		known[id] = true
	}
	seen := make(map[string]string, len(known))
	var visit func(*config.Layout, string) error
	visit = func(node *config.Layout, path string) error {
		if node.Agent != "" {
			if node.Split != "" {
				return invalid(path+".split", "agent leaf cannot contain split")
			}
			if node.Ratio != nil {
				return invalid(path+".ratio", "agent leaf cannot contain ratio")
			}
			if node.Children != nil {
				return invalid(path+".children", "agent leaf cannot contain children")
			}
			if !known[node.Agent] {
				return invalid(path+".agent", "references unknown agent "+node.Agent)
			}
			if previous, ok := seen[node.Agent]; ok {
				return invalid(path+".agent", "duplicate agent reference; first at "+previous)
			}
			seen[node.Agent] = path + ".agent"
			return nil
		}
		if node.Split != "columns" && node.Split != "rows" {
			return invalid(path+".split", "must be columns or rows for a split; otherwise an agent leaf is required")
		}
		if node.Ratio != nil {
			ratio := *node.Ratio
			if math.IsNaN(ratio) || math.IsInf(ratio, 0) || ratio < 0.1 || ratio > 0.9 {
				return invalid(path+".ratio", "must be a finite number between 0.1 and 0.9 inclusive")
			}
		}
		if len(node.Children) != 2 {
			return invalid(path+".children", "split must have exactly two children")
		}
		for i := range node.Children {
			if err := visit(&node.Children[i], fmt.Sprintf("%s.children[%d]", path, i)); err != nil {
				return err
			}
		}
		return nil
	}
	if err := visit(tree, "layout"); err != nil {
		return err
	}
	ids := make([]string, 0, len(known))
	for id := range known {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if _, ok := seen[id]; !ok {
			return invalid("layout", "missing agent reference "+id)
		}
	}
	return nil
}

// Compile validates a snapshot's layout and returns a detached tree for plan
// output and future adapters. Call config.Resolve first: Compile consumes its
// resolved agent values as data and never resolves paths or reads prompt files.
// Columns become horizontal, rows become vertical, and absent ratios become 0.5.
func Compile(snapshot *config.Resolved) (*Node, error) {
	if snapshot == nil {
		return nil, invalid("layout", "resolved configuration is required")
	}
	ids := make([]string, 0, len(snapshot.Agents))
	for id := range snapshot.Agents {
		ids = append(ids, id)
	}
	if err := config.ValidateWorkflow(snapshot.Workflow, ids, snapshot.Lead); err != nil {
		return nil, err
	}
	if err := Validate(&snapshot.Layout, ids); err != nil {
		return nil, err
	}
	var compile func(config.Layout) Node
	compile = func(source config.Layout) Node {
		if source.Agent != "" {
			a := snapshot.Agents[source.Agent]
			if a.Args != nil {
				a.Args = append([]string{}, a.Args...)
			}
			return Node{Agent: &Agent{ID: source.Agent, Config: a}}
		}
		direction := Horizontal
		if source.Split == "rows" {
			direction = Vertical
		}
		ratio := 0.5
		if source.Ratio != nil {
			ratio = *source.Ratio
		}
		return Node{
			Direction: direction, Ratio: ratio,
			Children: []Node{compile(source.Children[0]), compile(source.Children[1])},
		}
	}
	result := compile(snapshot.Layout)
	return &result, nil
}

func invalid(path, message string) error {
	return &config.FieldError{Path: path, Message: message}
}
