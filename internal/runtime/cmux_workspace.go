package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"unicode/utf8"

	"agentflow/internal/layout"
)

// CMUXWorkspaceOptions creates only a new workspace. WindowID is optional;
// cmux chooses the caller's window when omitted. No existing pane is targeted.
type CMUXWorkspaceOptions struct {
	WindowID     string            `json:"window_id,omitempty"`
	CMUXBinary   string            `json:"cmux_binary"`
	ToolBinaries map[string]string `json:"tool_binaries"`
}

// These closed structs intentionally exclude raw config, prompts and tool flags.
type cmuxLayoutNode struct {
	Direction layout.Direction `json:"direction,omitempty"`
	Split     float64          `json:"split,omitempty"`
	Children  []cmuxLayoutNode `json:"children,omitempty"`
	Pane      *cmuxLayoutPane  `json:"pane,omitempty"`
}
type cmuxLayoutPane struct {
	Surfaces []cmuxLayoutSurface `json:"surfaces"`
}
type cmuxLayoutSurface struct {
	Type    string `json:"type"`
	Cwd     string `json:"cwd"`
	Command string `json:"command"`
}

type CMUXWorkspaceCommand struct {
	CMUX     Command  `json:"cmux"`
	AgentIDs []string `json:"agent_ids"` // visual leaf order, never sorted seat order
}

// ShellCommand quotes every argv word for POSIX shells, including empty strings
// and embedded single quotes. cmux sends layout commands as terminal input:
// shell quoting cannot protect C0/C1/DEL from terminal interpretation.
func ShellCommand(command Command) (string, error) {
	if !identityText(command.Binary) {
		return "", errors.New("invalid shell command binary")
	}
	words := append([]string{command.Binary}, command.Args...)
	for i, word := range words {
		if !cmuxCommandText(word) {
			return "", errors.New("invalid shell command argument")
		}
		words[i] = "'" + strings.ReplaceAll(word, "'", "'\"'\"'") + "'"
	}
	return "exec " + strings.Join(words, " "), nil
}

func cmuxCommandText(value string) bool {
	if !utf8.ValidString(value) {
		return false
	}
	for _, r := range value {
		if r < 0x20 || (r >= 0x7f && r <= 0x9f) {
			return false
		}
	}
	return true
}

// BuildCMUXWorkspaceCommand is pure. One new-workspace --layout operation owns
// all splits, ratios, terminal cwd values and initial commands. It validates the
// entire canonical plan before returning any executable command data.
func BuildCMUXWorkspaceCommand(plan *StartupPlan, o CMUXWorkspaceOptions) (CMUXWorkspaceCommand, error) {
	invalid := func() (CMUXWorkspaceCommand, error) {
		return CMUXWorkspaceCommand{}, errors.New("invalid cmux workspace plan or options")
	}
	if plan == nil || len(plan.Seats) == 0 || !filepath.IsAbs(plan.SnapshotPath) ||
		!filepath.IsAbs(plan.Seats[0].Wrapper.Binary) || !filepath.IsAbs(o.CMUXBinary) ||
		!cmuxCommandText(o.CMUXBinary) || (o.WindowID != "" && !cmuxRef(o.WindowID, "window")) {
		return invalid()
	}
	canonical, err := Compile(plan.Config, Options{WrapperBinary: plan.Seats[0].Wrapper.Binary, SnapshotPath: plan.SnapshotPath})
	if err != nil || !reflect.DeepEqual(canonical, plan) {
		return invalid()
	}
	if _, err := NewSnapshot(plan, "cmux-workspace-validation"); err != nil {
		return invalid()
	}
	for tool, binary := range o.ToolBinaries {
		if (tool != "codex" && tool != "opencode" && tool != "claude") || !cmuxCommandText(binary) {
			return invalid()
		}
	}
	var ids []string
	var build func(*layout.Node) (cmuxLayoutNode, error)
	build = func(node *layout.Node) (cmuxLayoutNode, error) {
		if node.Agent != nil {
			a, id := node.Agent.Config, node.Agent.ID
			if !cmuxCommandText(a.Role) || !cmuxCommandText(a.Cwd) {
				return cmuxLayoutNode{}, fmt.Errorf("agents.%s: invalid cmux role or cwd text", id)
			}
			binary := o.ToolBinaries[a.Tool]
			if _, err := TUICommand(a, binary); err != nil {
				return cmuxLayoutNode{}, fmt.Errorf("agents.%s: invalid TUI command configuration", id)
			}
			command, err := ShellCommand(Command{Binary: plan.Seats[0].Wrapper.Binary, Args: []string{
				"_tui-seat", "--snapshot", plan.SnapshotPath, "--agent", id, "--binary", binary,
			}})
			if err != nil {
				return cmuxLayoutNode{}, err
			}
			ids = append(ids, id)
			return cmuxLayoutNode{Pane: &cmuxLayoutPane{Surfaces: []cmuxLayoutSurface{{"terminal", a.Cwd, command}}}}, nil
		}
		result := cmuxLayoutNode{Direction: node.Direction, Split: node.Ratio}
		for i := range node.Children {
			child, err := build(&node.Children[i])
			if err != nil {
				return cmuxLayoutNode{}, err
			}
			result.Children = append(result.Children, child)
		}
		return result, nil
	}
	tree, err := build(plan.Layout)
	if err != nil {
		return CMUXWorkspaceCommand{}, err
	}
	body, err := json.Marshal(tree)
	if err != nil {
		return invalid()
	}
	args := []string{"--json", "--id-format", "both", "new-workspace", "--layout", string(body), "--focus", "true"}
	if o.WindowID != "" {
		args = append(args, "--window", o.WindowID)
	}
	return CMUXWorkspaceCommand{CMUX: Command{Binary: o.CMUXBinary, Args: args}, AgentIDs: ids}, nil
}

type CMUXSurfaceLaunch struct {
	AgentID    string `json:"agent_id"`
	Tool       string `json:"tool"`
	SurfaceID  string `json:"surface_id"`
	SurfaceRef string `json:"surface_ref"`
}

// Launch result coordinates are cmux evidence only, never native session IDs or
// READY proof. Attempted includes errors/timeouts with potentially created panes.
type CMUXWorkspaceResult struct {
	Attempted    bool                `json:"attempted"`
	WorkspaceID  string              `json:"workspace_id,omitempty"`
	WorkspaceRef string              `json:"workspace_ref,omitempty"`
	WindowID     string              `json:"window_id,omitempty"`
	WindowRef    string              `json:"window_ref,omitempty"`
	Surfaces     []CMUXSurfaceLaunch `json:"surfaces,omitempty"`
}

// LaunchCMUXWorkspace does not retry or close panes after uncertain outcomes.
// The only mutation is new-workspace; tree reads only its returned workspace.
func LaunchCMUXWorkspace(ctx context.Context, plan *StartupPlan, o CMUXWorkspaceOptions) (CMUXWorkspaceResult, error) {
	var result CMUXWorkspaceResult
	command, err := BuildCMUXWorkspaceCommand(plan, o)
	if err != nil {
		return result, err
	}
	if err := PreflightCMUXWorkspace(plan, o); err != nil {
		return result, err
	}
	snapshot, err := ReadSnapshot(plan.SnapshotPath)
	if err != nil {
		return result, errors.New("cmux startup requires a private materialized snapshot")
	}
	expected, err := NewSnapshot(plan, snapshot.RunID)
	if err != nil || !reflect.DeepEqual(snapshot, expected) {
		return result, errors.New("cmux startup snapshot differs from launch plan")
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	result.Attempted = true
	body, _, truncated, err := captureNative(ctx, command.CMUX, plan.Seats[0].Cwd, "")
	if err != nil || truncated {
		return result, errors.New("cmux workspace creation failed or output was truncated; outcome unverified, do not replay")
	}
	// Some cmux versions print OK <workspace> even under --json. Both forms
	// require a verified workspace handle before the scoped read below.
	var created struct {
		WorkspaceID  string `json:"workspace_id"`
		WorkspaceRef string `json:"workspace_ref"`
	}
	if strings.HasPrefix(strings.TrimSpace(string(body)), "{") {
		if uniqueJSON(body) != nil || json.Unmarshal(body, &created) != nil {
			return result, errors.New("cmux returned malformed workspace JSON; outcome unverified, do not replay")
		}
	} else {
		words := strings.Fields(string(body))
		if len(words) == 2 && words[0] == "OK" && cmuxRef(words[1], "workspace") {
			if cmuxUUID.MatchString(words[1]) {
				created.WorkspaceID = words[1]
			} else {
				created.WorkspaceRef = words[1]
			}
		}
	}
	handle := created.WorkspaceID
	if handle == "" {
		handle = created.WorkspaceRef
	}
	if !cmuxRef(handle, "workspace") || (created.WorkspaceRef != "" && !cmuxRef(created.WorkspaceRef, "workspace")) {
		return result, errors.New("cmux returned no valid workspace identity; outcome unverified, do not replay")
	}
	result.WorkspaceID, result.WorkspaceRef = created.WorkspaceID, created.WorkspaceRef
	args := []string{"--json", "--id-format", "both", "tree", "--workspace", handle}
	if o.WindowID != "" {
		args = append(args, "--window", o.WindowID)
	}
	body, _, truncated, err = captureNative(ctx, Command{Binary: o.CMUXBinary, Args: args}, plan.Seats[0].Cwd, "")
	if err != nil || truncated {
		return result, errors.New("cmux workspace created but surface inspection failed; do not replay")
	}
	if err := parseCMUXWorkspaceTree(body, plan, command.AgentIDs, o.WindowID, &result); err != nil {
		return result, err
	}
	return result, nil
}

// PreflightCMUXWorkspace performs read-only filesystem checks before a run is
// consumed. All tools must be executable and all cwd directories must exist.
func PreflightCMUXWorkspace(plan *StartupPlan, o CMUXWorkspaceOptions) error {
	if _, err := BuildCMUXWorkspaceCommand(plan, o); err != nil {
		return err
	}
	paths := []string{o.CMUXBinary, plan.Seats[0].Wrapper.Binary}
	for _, seat := range plan.Seats {
		info, err := os.Stat(seat.Cwd)
		if err != nil || !info.IsDir() {
			return errors.New("cmux workspace requires existing agent cwd directories")
		}
		paths = append(paths, o.ToolBinaries[seat.Tool])
	}
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
			return errors.New("cmux startup requires executable cmux, wrapper and tool binaries")
		}
	}
	return nil
}

type cmuxTreeIdentity struct {
	ID  string `json:"id"`
	Ref string `json:"ref"`
}

func parseCMUXWorkspaceTree(body []byte, plan *StartupPlan, ids []string, window string, r *CMUXWorkspaceResult) error {
	invalid := errors.New("cmux workspace surface evidence is missing, ambiguous or differs from layout; do not replay")
	var tree struct {
		Windows []struct {
			cmuxTreeIdentity
			Workspaces []struct {
				cmuxTreeIdentity
				Layout *structuralLayout `json:"layout"`
				Panes  []struct {
					cmuxTreeIdentity
					Surfaces []struct {
						cmuxTreeIdentity
						Type string `json:"type"`
					} `json:"surfaces"`
				} `json:"panes"`
			} `json:"workspaces"`
		} `json:"windows"`
	}
	if uniqueJSON(body) != nil || json.Unmarshal(body, &tree) != nil {
		return invalid
	}
	matched := 0
	for _, w := range tree.Windows {
		for _, ws := range w.Workspaces {
			if !(ws.ID != "" && strings.EqualFold(ws.ID, r.WorkspaceID) || ws.Ref != "" && ws.Ref == r.WorkspaceRef) {
				continue
			}
			matched++
			if !cmuxUUID.MatchString(w.ID) || !cmuxRef(w.Ref, "window") || !cmuxUUID.MatchString(ws.ID) || !cmuxRef(ws.Ref, "workspace") ||
				(window != "" && !strings.EqualFold(window, w.ID) && window != w.Ref) ||
				(r.WorkspaceID != "" && !strings.EqualFold(r.WorkspaceID, ws.ID)) ||
				(r.WorkspaceRef != "" && r.WorkspaceRef != ws.Ref) {
				return invalid
			}
			var panes []string
			if !matchCMUXLayout(ws.Layout, plan.Layout, &panes) || len(panes) != len(ids) || len(ws.Panes) != len(ids) {
				return invalid
			}
			var surfaces []CMUXSurfaceLaunch
			seen := map[string]bool{}
			for i, paneID := range panes {
				found := 0
				for _, pane := range ws.Panes {
					if !strings.EqualFold(pane.ID, paneID) && pane.Ref != paneID {
						continue
					}
					found++
					if len(pane.Surfaces) != 1 {
						return invalid
					}
					s := pane.Surfaces[0]
					key := strings.ToLower(s.ID)
					if s.Type != "terminal" || !cmuxUUID.MatchString(s.ID) || !cmuxRef(s.Ref, "surface") || seen[key] || seen[s.Ref] {
						return invalid
					}
					seen[key], seen[s.Ref] = true, true
					surfaces = append(surfaces, CMUXSurfaceLaunch{ids[i], plan.Config.Agents[ids[i]].Tool, s.ID, s.Ref})
				}
				if found != 1 {
					return invalid
				}
			}
			r.WindowID, r.WindowRef, r.WorkspaceID, r.WorkspaceRef = w.ID, w.Ref, ws.ID, ws.Ref
			r.Surfaces = surfaces
		}
	}
	if matched != 1 {
		return invalid
	}
	return nil
}

type structuralLayout struct {
	Direction layout.Direction   `json:"direction"`
	Children  []structuralLayout `json:"children"`
	Pane      *cmuxTreeIdentity  `json:"pane"`
}

func matchCMUXLayout(actual *structuralLayout, expected *layout.Node, panes *[]string) bool {
	if actual == nil || expected == nil {
		return false
	}
	if expected.Agent != nil {
		if actual.Pane == nil || len(actual.Children) != 0 || actual.Direction != "" {
			return false
		}
		id := actual.Pane.ID
		if id == "" {
			id = actual.Pane.Ref
		}
		if !cmuxRef(id, "pane") {
			return false
		}
		*panes = append(*panes, id)
		return true
	}
	return actual.Pane == nil && actual.Direction == expected.Direction && len(actual.Children) == 2 &&
		matchCMUXLayout(&actual.Children[0], &expected.Children[0], panes) && matchCMUXLayout(&actual.Children[1], &expected.Children[1], panes)
}
