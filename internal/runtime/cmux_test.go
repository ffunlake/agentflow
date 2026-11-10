package runtime

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"agentflow/internal/config"
)

func cmuxPlan(t *testing.T, ext string) *StartupPlan {
	t.Helper()
	plan, err := Compile(example(t, ext), Options{WrapperBinary: "/trusted/path with spaces/agentflow", SnapshotPath: "/private/run $(literal); ' \"/snapshot.json"})
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func TestCMUXCommandsEquivalentAndDetached(t *testing.T) {
	var previous []CMUXLaunchCommand
	for _, ext := range []string{"yaml", "json", "yaml"} {
		plan := cmuxPlan(t, ext)
		before, _ := json.Marshal(plan)
		var commands []CMUXLaunchCommand
		for _, tc := range []struct {
			id     string
			target CMUXTarget
			args   []string
		}{
			{"main", CMUXTarget{WindowID: "window:1"}, []string{"--json", "--id-format", "uuids", "new-workspace", "--window", "window:1", "--cwd", plan.Seats[1].Cwd, "--focus", "false"}},
			{"builder", CMUXTarget{"window:1", "workspace:2", "surface:3", 0}, []string{"--json", "--id-format", "uuids", "new-split", "right", "--window", "window:1", "--workspace", "workspace:2", "--surface", "surface:3", "--focus", "false"}},
			{"reviewer", CMUXTarget{"window:1", "workspace:2", "surface:4", 1}, []string{"--json", "--id-format", "uuids", "new-split", "down", "--window", "window:1", "--workspace", "workspace:2", "--surface", "surface:4", "--focus", "false"}},
		} {
			got, err := BuildCMUXCommand(plan, tc.id, tc.target)
			if err != nil {
				t.Fatal(err)
			}
			a := plan.Config.Agents[tc.id]
			if got.CMUX.Binary != "cmux" || !reflect.DeepEqual(got.CMUX.Args, tc.args) || got.Tool != a.Tool || got.Cwd != a.Cwd || got.Wrapper.Args[2] != plan.SnapshotPath || got.Wrapper.Args[4] != tc.id {
				t.Fatalf("incorrect command: %+v", got)
			}
			encoded, _ := json.Marshal(got)
			for _, agent := range plan.Config.Agents {
				if strings.Contains(string(encoded), agent.Prompt) || strings.Contains(string(encoded), agent.Role) {
					t.Fatal("prompt or role leaked into launch description")
				}
			}
			if strings.Contains(string(encoded), "--command") {
				t.Fatal("builder introduced a shell command transport")
			}
			commands = append(commands, got)
		}
		after, _ := json.Marshal(plan)
		if string(before) != string(after) {
			t.Fatal("builder mutated plan")
		}
		if previous != nil && !reflect.DeepEqual(previous, commands) {
			t.Fatal("YAML/JSON commands or repeated calls differ")
		}
		previous = commands
		commands[0].Wrapper.Args[0] = "changed"
		commands[0].PanePath[0].Ratio = 0.8
		if plan.Seats[1].Wrapper.Args[0] != "_seat" || plan.Seats[1].PanePath[0].Ratio != 0.4 {
			t.Fatal("launch data aliases input")
		}
		// Restore only the detached test output used for equivalence next round.
		commands[0].Wrapper.Args[0] = "_seat"
		commands[0].PanePath[0].Ratio = 0.4
	}
}

func TestCMUXCommandRejectsInvalidPlanAndTargets(t *testing.T) {
	for _, stage := range []string{"nil", "status", "executed", "relative-snapshot", "digest", "tool", "cwd", "wrapper-secret", "pane-path", "seat-order", "unknown", "window", "half-target", "negative-depth", "depth", "wrong-child", "root-not-first", "split-not-first", "root-depth", "target-option"} {
		t.Run(stage, func(t *testing.T) {
			plan := cmuxPlan(t, "yaml")
			id := "main"
			target := CMUXTarget{WindowID: "window:1"}
			switch stage {
			case "nil":
				plan = nil
			case "status":
				plan.Status = Ready
			case "executed":
				plan.Executed = true
			case "relative-snapshot":
				plan.SnapshotPath = "snapshot.json"
			case "digest":
				plan.Config.Digest = "bad"
			case "tool":
				plan.Seats[1].Tool = "sh"
			case "cwd":
				plan.Seats[1].Cwd = "relative"
			case "wrapper-secret":
				plan.Seats[1].Wrapper.Args = append(plan.Seats[1].Wrapper.Args, "fixture-secret")
			case "pane-path":
				plan.Seats[1].PanePath[0].Ratio = 0.9
			case "seat-order":
				plan.Seats[0], plan.Seats[1] = plan.Seats[1], plan.Seats[0]
			case "unknown":
				id = "unknown"
			case "window":
				target.WindowID = ""
			case "half-target":
				target.WorkspaceID = "workspace:2"
			case "negative-depth", "depth", "wrong-child", "split-not-first":
				target = CMUXTarget{"window:1", "workspace:2", "surface:3", 0}
				if stage == "negative-depth" {
					target.SplitDepth = -1
				} else if stage == "depth" {
					target.SplitDepth = 100
				} else if stage == "split-not-first" {
					id = "reviewer"
				}
			case "root-not-first":
				id = "builder"
			case "root-depth":
				target.SplitDepth = 1
			case "target-option":
				target.WindowID = "--password=fixture-secret"
			}
			got, err := BuildCMUXCommand(plan, id, target)
			if err == nil || !reflect.DeepEqual(got, CMUXLaunchCommand{}) || strings.Contains(err.Error(), "fixture-secret") {
				t.Fatalf("invalid command emitted: %+v, %v", got, err)
			}
		})
	}
}

func TestCMUXSingleSeatLiteralPathsAndPrivateArguments(t *testing.T) {
	for _, tool := range []string{"opencode", "codex", "claude"} {
		prompt := "private prompt body"
		cwd := "/work/path $(literal); ' \" with spaces"
		c := &config.Config{Version: 1, Name: "solo", Terminal: "cmux", Lead: "solo", ContextDir: "context",
			SourcePath: filepath.Join(t.TempDir(), "team.yaml"),
			Agents: map[string]config.Agent{"solo": {Role: "Developer", Prompt: &prompt, Tool: &tool, Cwd: &cwd,
				Model: "private-model", Args: []string{"--credential=fixture-secret", "$(literal)"}}}, Layout: &config.Layout{Agent: "solo"}}
		r, err := config.Resolve(c, config.ResolveOptions{})
		if err != nil {
			t.Fatal(err)
		}
		plan, err := Compile(r, Options{SnapshotPath: "/private/run/snapshot.json"})
		if err != nil {
			t.Fatal(err)
		}
		got, err := BuildCMUXCommand(plan, "solo", CMUXTarget{WindowID: "D9F41024-5C40-4E98-8B34-BD39785B0C81"})
		if err != nil || got.CMUX.Args[7] != cwd || got.Cwd != cwd || got.Tool != tool || len(got.PanePath) != 0 {
			t.Fatalf("literal path/tool lost: %+v %v", got, err)
		}
		encoded, _ := json.Marshal(got)
		for _, secret := range []string{prompt, "fixture-secret", "private-model", "--credential"} {
			if strings.Contains(string(encoded), secret) {
				t.Fatalf("launch data leaked %s", secret)
			}
		}
	}
}
