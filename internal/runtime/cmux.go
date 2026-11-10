package runtime

import (
	"errors"
	"path/filepath"
	"reflect"
	"regexp"

	"agentflow/internal/layout"
)

// CMUXTarget is supplied by a future launcher from verified cmux responses. A
// window is always explicit. Empty workspace/surface creates a workspace. For a
// split, SurfaceID must belong to the left/upper sibling at SplitDepth; the
// builder cannot verify live ownership. New default startup uses the independent
// CMUXWorkspaceOptions API with an optional window. Only the first leaf of a new subtree is
// created by each command; callers traverse leaves in visual order, not seat ID.
type CMUXTarget struct {
	WindowID    string `json:"window_id"`
	WorkspaceID string `json:"workspace_id,omitempty"`
	SurfaceID   string `json:"surface_id,omitempty"`
	SplitDepth  int    `json:"split_depth"`
}

// CMUXLaunchCommand separates terminal topology from direct wrapper execution.
// cmux --command accepts shell text, not argv. This builder never emits that
// option or serializes argv into shell source. Wrapper transport and exact split
// ratios are outside this legacy builder; use BuildCMUXWorkspaceCommand for the
// complete layout and safely quoted TUI entry transport.
type CMUXLaunchCommand struct {
	AgentID  string     `json:"agent_id"`
	Tool     string     `json:"tool"`
	Cwd      string     `json:"cwd"`
	PanePath []PaneStep `json:"pane_path"`
	Target   CMUXTarget `json:"target"`
	CMUX     Command    `json:"cmux"`
	Wrapper  Command    `json:"wrapper"`
}

var cmuxUUID = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func cmuxRef(value, kind string) bool {
	return cmuxUUID.MatchString(value) || regexp.MustCompile(`^`+kind+`:[1-9][0-9]*$`).MatchString(value)
}

// BuildCMUXCommand is a pure builder over an unchanged StartupPlan whose snapshot
// reference has been materialized as an absolute path. No files are read and no
// tool, cmux or shell is run. It returns no partial command on invalid input.
func BuildCMUXCommand(plan *StartupPlan, id string, target CMUXTarget) (CMUXLaunchCommand, error) {
	invalid := func() (CMUXLaunchCommand, error) {
		return CMUXLaunchCommand{}, errors.New("invalid cmux launch plan, seat or target")
	}
	if plan == nil || len(plan.Seats) == 0 || !filepath.IsAbs(plan.SnapshotPath) || !cmuxRef(target.WindowID, "window") {
		return invalid()
	}
	canonical, err := Compile(plan.Config, Options{WrapperBinary: plan.Seats[0].Wrapper.Binary, SnapshotPath: plan.SnapshotPath})
	if err != nil || !reflect.DeepEqual(canonical, plan) {
		return invalid()
	}
	if _, err := NewSnapshot(canonical, "cmux-command-validation"); err != nil {
		return invalid()
	}
	var selected *Seat
	for i := range canonical.Seats {
		if canonical.Seats[i].ID == id {
			selected = &canonical.Seats[i]
			break
		}
	}
	if selected == nil {
		return invalid()
	}
	seat := *selected
	args := []string{"--json", "--id-format", "uuids"}
	start := 0
	if target.WorkspaceID == "" && target.SurfaceID == "" {
		if target.SplitDepth != 0 {
			return invalid()
		}
		args = append(args, "new-workspace", "--window", target.WindowID, "--cwd", seat.Cwd, "--focus", "false")
	} else {
		if !cmuxRef(target.WorkspaceID, "workspace") || !cmuxRef(target.SurfaceID, "surface") || target.SplitDepth < 0 || target.SplitDepth >= len(seat.PanePath) {
			return invalid()
		}
		step := seat.PanePath[target.SplitDepth]
		if step.Child != 1 {
			return invalid()
		}
		direction := "right"
		if step.Direction == layout.Vertical {
			direction = "down"
		}
		args = append(args, "new-split", direction, "--window", target.WindowID,
			"--workspace", target.WorkspaceID, "--surface", target.SurfaceID, "--focus", "false")
		start = target.SplitDepth + 1
	}
	// A new workspace/subtree starts with its first visual leaf. Further right
	// or lower branches need separate commands and verified returned surfaces.
	for _, step := range seat.PanePath[start:] {
		if step.Child != 0 {
			return invalid()
		}
	}
	return CMUXLaunchCommand{AgentID: seat.ID, Tool: seat.Tool, Cwd: seat.Cwd,
		PanePath: seat.PanePath, Target: target,
		CMUX: Command{Binary: "cmux", Args: args}, Wrapper: seat.Wrapper}, nil
}
