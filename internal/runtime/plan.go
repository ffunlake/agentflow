// Package runtime builds startup plans and coordinates seat registration through
// an injected launcher, including explicit foreground native adapters.
package runtime

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"agentflow/internal/config"
	"agentflow/internal/layout"
)

// Options describes artifacts a future launcher must materialize. Relative
// SnapshotPath values are relative to the future run directory, NOT agent cwd.
// Zero values select agentflow and config.resolved.json. No artifact is written.
type Options struct {
	WrapperBinary string
	SnapshotPath  string
}

// Command is data suitable for exec.CommandContext(ctx, Binary, Args...). Shell
// transport must use ShellCommand's per-word quoting. The _seat entry validates startup and
// requires explicit foreground authorization before executing a native adapter.
type Command struct {
	Binary string   `json:"binary"`
	Args   []string `json:"args"`
}

type RunSummary struct {
	Name         string `json:"name"`
	Terminal     string `json:"terminal"`
	Lead         string `json:"lead"`
	ContextDir   string `json:"context_dir"`
	ConfigDigest string `json:"config_digest"`
}

// PaneStep records one ancestor split. Child is 0 or 1; Ratio always describes
// the first child. The entire path is required to reconstruct nested geometry.
type PaneStep struct {
	Direction layout.Direction `json:"direction"`
	Ratio     float64          `json:"ratio"`
	Child     int              `json:"child"`
}

// PromptReference refers to the frozen Config, not a prompt file reread at
// launch. File is provenance only; inline and file prompts both use TextRef.
type PromptReference struct {
	File    string `json:"file,omitempty"`
	TextRef string `json:"text_ref"`
}

// Seat IDs are logical agent IDs, not cmux pane numbers or native session IDs.
// Seats are sorted by ID; Order is zero based and independent of pane position.
type Seat struct {
	ID          string          `json:"id"`
	Order       int             `json:"order"`
	Tool        string          `json:"tool"`
	Cwd         string          `json:"cwd"`
	Permissions string          `json:"permissions"`
	Session     string          `json:"session"`
	Role        string          `json:"role"`
	Prompt      PromptReference `json:"prompt"`
	PanePath    []PaneStep      `json:"pane_path"`
	Wrapper     Command         `json:"wrapper"`
}

type StartupPlan struct {
	Status       string           `json:"status"`
	Executed     bool             `json:"executed"`
	Run          RunSummary       `json:"run"`
	SnapshotPath string           `json:"snapshot_path"`
	Config       *config.Resolved `json:"config"`
	Layout       *layout.Node     `json:"layout"`
	Seats        []Seat           `json:"seats"`
}

var seatID = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,47}$`)

// WrapperCommand selects only the three supported tools. Tool arguments, model,
// role text, prompt body, and credentials never enter wrapper argv: _seat will
// obtain its configuration from the private, frozen snapshot. This function
// neither looks up nor starts any executable.
func WrapperCommand(id, tool string, options Options) (Command, error) {
	if !seatID.MatchString(id) {
		return Command{}, &config.FieldError{Path: "agents." + id, Message: "invalid seat ID"}
	}
	if tool != "opencode" && tool != "codex" && tool != "claude" {
		return Command{}, &config.FieldError{Path: "agents." + id + ".tool", Message: "must be opencode, codex or claude"}
	}
	options, err := normalizeOptions(options)
	if err != nil {
		return Command{}, err
	}
	return Command{Binary: options.WrapperBinary, Args: []string{
		"_seat", "--snapshot", options.SnapshotPath, "--agent", id,
	}}, nil
}

func normalizeOptions(options Options) (Options, error) {
	if options.WrapperBinary == "" {
		options.WrapperBinary = "agentflow"
	}
	if options.SnapshotPath == "" {
		options.SnapshotPath = "config.resolved.json"
	}
	for _, field := range []struct{ path, value string }{
		{"wrapper.binary", options.WrapperBinary}, {"snapshot_path", options.SnapshotPath},
	} {
		if strings.TrimSpace(field.value) == "" || strings.ContainsRune(field.value, '\x00') {
			return Options{}, &config.FieldError{Path: field.path, Message: "must be non-empty and contain no NUL bytes"}
		}
	}
	return options, nil
}

// Compile consumes a config.Resolve snapshot and composes layout.Compile into a
// detached, deterministic plan. It performs no I/O and allocates no run/session
// IDs. Equivalent YAML/JSON snapshots produce identical plans; source file paths
// are deliberately excluded from this runtime description.
func Compile(snapshot *config.Resolved, options Options) (*StartupPlan, error) {
	compiled, err := layout.Compile(snapshot)
	if err != nil {
		return nil, err
	}
	options, err = normalizeOptions(options)
	if err != nil {
		return nil, err
	}
	// Clone the snapshot through its serialized contract, including source layout
	// pointers and argument slices. SourcePath is diagnostic metadata, not state.
	body, err := json.Marshal(snapshot)
	if err != nil {
		return nil, fmt.Errorf("encode startup snapshot: %w", err)
	}
	var frozen config.Resolved
	if err := json.Unmarshal(body, &frozen); err != nil {
		return nil, fmt.Errorf("clone startup snapshot: %w", err)
	}
	plan := &StartupPlan{
		Status: "PLANNED", Executed: false,
		Run: RunSummary{Name: frozen.Name, Terminal: frozen.Terminal, Lead: frozen.Lead,
			ContextDir: frozen.ContextDir, ConfigDigest: frozen.Digest},
		SnapshotPath: options.SnapshotPath, Config: &frozen, Layout: compiled,
		Seats: make([]Seat, 0, len(frozen.Agents)),
	}
	paths := make(map[string][]PaneStep, len(frozen.Agents))
	var visit func(*layout.Node, []PaneStep)
	visit = func(node *layout.Node, path []PaneStep) {
		if node.Agent != nil {
			paths[node.Agent.ID] = append([]PaneStep{}, path...)
			return
		}
		for child := range node.Children {
			next := append(append([]PaneStep{}, path...), PaneStep{node.Direction, node.Ratio, child})
			visit(&node.Children[child], next)
		}
	}
	visit(compiled, nil)
	ids := make([]string, 0, len(frozen.Agents))
	for id := range frozen.Agents {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for order, id := range ids {
		a := frozen.Agents[id]
		wrapper, err := WrapperCommand(id, a.Tool, options)
		if err != nil {
			return nil, err
		}
		plan.Seats = append(plan.Seats, Seat{
			ID: id, Order: order, Tool: a.Tool, Cwd: a.Cwd, Permissions: a.Permissions,
			Session: a.Session, Role: a.Role,
			Prompt:   PromptReference{File: a.PromptFile, TextRef: "config.agents." + id + ".prompt"},
			PanePath: paths[id], Wrapper: wrapper,
		})
	}
	return plan, nil
}
