package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"agentflow/internal/config"
)

func buildFakeCMUX(t *testing.T) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "fake cmux ' executable")
	cmd := exec.Command("go", "build", "-o", binary, "./testdata/cmux")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build fake: %v %s", err, out)
	}
	return binary
}

func workspacePlan(t *testing.T, binary, shape string) (*StartupPlan, CMUXWorkspaceOptions) {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	cwd := filepath.Join(dir, "cwd with ' quotes $(literal)")
	if err := os.Mkdir(cwd, 0700); err != nil {
		t.Fatal(err)
	}
	text := "prompt ' \" $(touch forbidden)\nfixture-secret"
	c := &config.Config{Version: 1, Name: "test", Terminal: "cmux", Lead: "zmain", ContextDir: "context", SourcePath: filepath.Join(dir, "team.yaml"),
		Defaults: config.Defaults{Cwd: &cwd}, Agents: map[string]config.Agent{}}
	for id, tool := range map[string]string{"zmain": "codex", "abuilder": "opencode", "reviewer": "claude"} {
		c.Agents[id] = config.Agent{Role: "role " + id, Prompt: &text, Tool: &tool}
	}
	ratio := 0.4
	c.Layout = &config.Layout{Split: "columns", Ratio: &ratio, Children: []config.Layout{{Agent: "zmain"}, {Split: "rows", Children: []config.Layout{{Agent: "abuilder"}, {Agent: "reviewer"}}}}}
	if shape != "nested" {
		delete(c.Agents, "reviewer")
		c.Layout = &config.Layout{Split: shape, Ratio: &ratio, Children: []config.Layout{{Agent: "zmain"}, {Agent: "abuilder"}}}
	}
	r, err := config.Resolve(c, config.ResolveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := Compile(r, Options{WrapperBinary: binary, SnapshotPath: filepath.Join(dir, "snapshot ' file.json")})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := NewSnapshot(plan, "cmux-test")
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteSnapshot(plan.SnapshotPath, snapshot); err != nil {
		t.Fatal(err)
	}
	return plan, CMUXWorkspaceOptions{CMUXBinary: binary, ToolBinaries: map[string]string{"codex": binary, "opencode": binary, "claude": binary}}
}

func fakeCMUXEnv(t *testing.T, mode string) string {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "calls.jsonl")
	t.Setenv("CMUX_FAKE_LOG", log)
	t.Setenv("CMUX_FAKE_STATE", filepath.Join(dir, "tree.json"))
	t.Setenv("CMUX_FAKE_MODE", mode)
	return log
}

func TestCMUXWorkspaceLayoutsAndReturnParsing(t *testing.T) {
	binary := buildFakeCMUX(t)
	for _, shape := range []string{"columns", "rows", "nested"} {
		for _, mode := range []string{"json", "refs", "uuid"} {
			t.Run(shape+"/"+mode, func(t *testing.T) {
				log := fakeCMUXEnv(t, mode)
				plan, o := workspacePlan(t, binary, shape)
				if mode == "json" {
					o.WindowID = "window:9"
				}
				command, err := BuildCMUXWorkspaceCommand(plan, o)
				if err != nil {
					t.Fatal(err)
				}
				var tree cmuxLayoutNode
				if err := json.Unmarshal([]byte(command.CMUX.Args[5]), &tree); err != nil {
					t.Fatal(err)
				}
				wantDirection := "horizontal"
				if shape == "rows" {
					wantDirection = "vertical"
				}
				if string(tree.Direction) != wantDirection || tree.Split != 0.4 || len(tree.Children) != 2 {
					t.Fatalf("layout %+v", tree)
				}
				if shape == "nested" && (tree.Children[1].Direction != "vertical" || tree.Children[1].Split != 0.5) {
					t.Fatalf("nested layout %+v", tree)
				}
				if strings.Contains(command.CMUX.Args[5], "fixture-secret") || strings.Contains(command.CMUX.Args[5], "Role:") {
					t.Fatal("prompt leaked into layout")
				}
				got, err := LaunchCMUXWorkspace(context.Background(), plan, o)
				if err != nil {
					t.Fatal(err)
				}
				if !got.Attempted || got.WorkspaceRef != "workspace:71" || got.WindowRef != "window:9" || len(got.Surfaces) != len(plan.Seats) {
					t.Fatalf("result %+v", got)
				}
				for i, id := range command.AgentIDs {
					if got.Surfaces[i].AgentID != id || got.Surfaces[i].Tool != plan.Config.Agents[id].Tool || got.Surfaces[i].SurfaceID == "" {
						t.Fatalf("incorrect binding %+v", got)
					}
				}
				body, err := os.ReadFile(log)
				if err != nil {
					t.Fatal(err)
				}
				if strings.Count(string(body), "new-workspace") != 1 || strings.Count(string(body), `"tree"`) != 1 {
					t.Fatal(string(body))
				}
				if mode != "json" && strings.Contains(string(body), `"--window"`) {
					t.Fatal("implicit window became explicit")
				}
			})
		}
	}
}

func TestShellCommandLiteralRoundTrip(t *testing.T) {
	binary := buildFakeCMUX(t)
	args := []string{"", "space here", "'single' \"double\"", "$(touch forbidden); & | > `id`", "中文 café 😀\u00a0\u2028\u2029", "literal \\n \\t \\x1b", "--model=fixture-secret"}
	command, err := ShellCommand(Command{Binary: binary, Args: append([]string{"record"}, args...)})
	if err != nil {
		t.Fatal(err)
	}
	body, err := exec.Command("/bin/sh", "-c", command).Output()
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	if json.Unmarshal(body, &got) != nil || !reflect.DeepEqual(got, args) {
		t.Fatalf("changed arguments %q", body)
	}
	if _, err := ShellCommand(Command{Binary: binary, Args: []string{"bad\x00text"}}); err == nil {
		t.Fatal("accepted NUL")
	}
}

func TestShellCommandRejectsTerminalControls(t *testing.T) {
	for _, value := range append(terminalControlTexts(), string([]byte{0xff})) {
		for _, field := range []string{"binary", "argument"} {
			t.Run(fmt.Sprintf("%s/%x", field, []byte(value)), func(t *testing.T) {
				command := Command{Binary: "/bin/tool", Args: []string{"ordinary"}}
				if field == "binary" {
					command.Binary += value + "suffix"
				} else {
					command.Args = append(command.Args, "prefix"+value+"suffix")
				}
				if got, err := ShellCommand(command); err == nil || got != "" {
					t.Fatalf("accepted terminal control: %q %v", got, err)
				}
			})
		}
	}
}

func TestFakeCMUXTerminalTransport(t *testing.T) {
	binary := buildFakeCMUX(t)
	// Pinned sendInput transport cases, independent of shell quoting. Even
	// single-quoted text is parsed as terminal input before the shell sees it.
	for _, tc := range []struct {
		name, input, command, problem string
	}{
		{"LF-submit", "exec 'ordinary 中文 😀'\n", "exec 'ordinary 中文 😀'", ""},
		{"CR-submit", "exec 'ordinary'\r", "exec 'ordinary'", ""},
		{"CRLF-submit", "exec 'ordinary'\r\n", "exec 'ordinary'", ""},
		{"quoted-CR", "exec 'a\rb'\n", "", "text after terminal Return"},
		{"quoted-LF", "exec 'a\nb'\n", "", "text after terminal Return"},
		{"quoted-tab", "exec 'a\tb'\n", "", "terminal key event: tab"},
		{"quoted-BS", "exec 'a\bb'\n", "", "terminal key event: backspace"},
		{"quoted-DEL", "exec 'a\x7fb'\n", "", "terminal key event: backspace"},
		{"quoted-escape", "exec 'a\x1bb'\n", "", "terminal escape key or control sequence"},
		{"quoted-CSI", "exec 'a\x1b[31mb'\n", "", "terminal escape key or control sequence"},
		{"quoted-C1-OSC", "exec 'a\u009d0;title\u009cb'\n", "", "terminal control input"},
		{"extra-submit", "exec 'a'\n\n", "", "expected one terminal Return"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body, err := exec.Command(binary, "terminal-command", tc.input).Output()
			if err != nil {
				t.Fatal(err)
			}
			var got struct{ Command, Error string }
			if err := json.Unmarshal(body, &got); err != nil {
				t.Fatal(err)
			}
			if got.Command != tc.command || got.Error != tc.problem {
				t.Fatalf("transport changed: %s", body)
			}
		})
	}
}

func terminalControlTexts() []string {
	var values []string
	for r := rune(0); r <= 0x9f; r++ {
		if r < 0x20 || r >= 0x7f {
			values = append(values, string(r))
		}
	}
	return append(values, "\r\n", "\x1b[A", "\x1b]0;title\a", "\u009b31m", "\u009d0;title\u009c")
}

func TestCMUXWorkspaceRejectsTerminalControlsBeforeExecution(t *testing.T) {
	binary := buildFakeCMUX(t)
	for _, field := range []string{"role", "cwd", "binary", "snapshot", "wrapper", "cmux"} {
		for _, value := range terminalControlTexts() {
			t.Run(fmt.Sprintf("%s/%x", field, []byte(value)), func(t *testing.T) {
				log := fakeCMUXEnv(t, "json")
				plan, o := workspacePlan(t, binary, "nested")
				snapshotPath, wrapper := plan.SnapshotPath, binary
				switch field {
				case "role", "cwd":
					a := plan.Config.Agents["reviewer"] // Last leaf must also fail before any creation.
					if field == "role" {
						a.Role += value + "suffix"
					} else {
						a.Cwd += value + "suffix"
					}
					plan.Config.Agents["reviewer"] = a
					plan.Config.Digest = ""
					body, _ := json.Marshal(plan.Config)
					sum := sha256.Sum256(body)
					plan.Config.Digest = hex.EncodeToString(sum[:])
				case "binary":
					o.ToolBinaries["claude"] += value + "suffix"
				case "snapshot":
					snapshotPath += value + "suffix"
				case "wrapper":
					wrapper += value + "suffix"
				case "cmux":
					o.CMUXBinary += value + "suffix"
				}
				// Recompile when representable, so canonical equality cannot mask
				// missing transport validation. Compile already rejects NUL paths
				// and the config decoder rejects some DEL/C1 role/cwd values.
				if recompiled, err := Compile(plan.Config, Options{WrapperBinary: wrapper, SnapshotPath: snapshotPath}); err == nil {
					plan = recompiled
				} else {
					// Keep the raw invalid input for the builder to reject too.
					plan.SnapshotPath = snapshotPath
					for i := range plan.Seats {
						plan.Seats[i].Wrapper.Binary = wrapper
					}
				}
				if command, err := BuildCMUXWorkspaceCommand(plan, o); err == nil || !reflect.DeepEqual(command, CMUXWorkspaceCommand{}) {
					t.Fatalf("builder accepted terminal control: %+v %v", command, err)
				}
				if err := PreflightCMUXWorkspace(plan, o); err == nil {
					t.Fatal("preflight accepted terminal control")
				}
				got, err := LaunchCMUXWorkspace(context.Background(), plan, o)
				if err == nil || !reflect.DeepEqual(got, CMUXWorkspaceResult{}) {
					t.Fatalf("launcher accepted terminal control: %+v %v", got, err)
				}
				for _, path := range []string{log, os.Getenv("CMUX_FAKE_STATE")} {
					if _, err := os.Stat(path); !os.IsNotExist(err) {
						t.Fatalf("cmux invoked before validation: %s: %v", path, err)
					}
				}
			})
		}
	}
}

func TestCMUXWorkspaceEquivalentYAMLJSON(t *testing.T) {
	binary := buildFakeCMUX(t)
	options := CMUXWorkspaceOptions{CMUXBinary: binary, ToolBinaries: map[string]string{"codex": binary, "opencode": binary, "claude": binary}}
	var previous CMUXWorkspaceCommand
	for _, ext := range []string{"yaml", "json"} {
		plan, err := Compile(example(t, ext), Options{WrapperBinary: binary, SnapshotPath: "/private/run/snapshot.json"})
		if err != nil {
			t.Fatal(err)
		}
		got, err := BuildCMUXWorkspaceCommand(plan, options)
		if err != nil {
			t.Fatal(err)
		}
		if ext == "json" && !reflect.DeepEqual(got, previous) {
			t.Fatal("YAML/JSON produced different cmux launch commands")
		}
		previous = got
	}
}

func TestCMUXWorkspaceCancellation(t *testing.T) {
	binary := buildFakeCMUX(t)
	for _, before := range []bool{true, false} {
		t.Run(map[bool]string{true: "before", false: "during"}[before], func(t *testing.T) {
			log := fakeCMUXEnv(t, "delay")
			plan, options := workspacePlan(t, binary, "nested")
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if before {
				cancel()
			}
			type outcome struct {
				result CMUXWorkspaceResult
				err    error
			}
			done := make(chan outcome, 1)
			go func() { got, err := LaunchCMUXWorkspace(ctx, plan, options); done <- outcome{got, err} }()
			if !before {
				// Cancel only after the fake executable has observed the request,
				// so process startup scheduling cannot make this test flaky.
				deadline := time.Now().Add(5 * time.Second)
				for {
					if body, _ := os.ReadFile(log); strings.Contains(string(body), "new-workspace") {
						break
					}
					if time.Now().After(deadline) {
						t.Fatal("fake cmux did not observe startup")
					}
					time.Sleep(10 * time.Millisecond)
				}
				cancel()
			}
			out := <-done
			got, err := out.result, out.err
			if err == nil || got.Attempted == before {
				t.Fatalf("%+v %v", got, err)
			}
			body, _ := os.ReadFile(log)
			wantCalls := 1
			if before {
				wantCalls = 0
			}
			if strings.Count(string(body), "new-workspace") != wantCalls {
				t.Fatal("unexpected launch/retry", string(body))
			}
		})
	}
}

func TestCMUXWorkspaceFailureAndNoRetry(t *testing.T) {
	binary := buildFakeCMUX(t)
	for _, mode := range []string{"fail", "invalid", "duplicate-json", "trailing-json", "tree-fail", "bad-tree", "duplicate-tree", "duplicate-surface", "browser", "wrong-workspace", "wrong-layout"} {
		t.Run(mode, func(t *testing.T) {
			log := fakeCMUXEnv(t, mode)
			plan, o := workspacePlan(t, binary, "nested")
			got, err := LaunchCMUXWorkspace(context.Background(), plan, o)
			if err == nil || !got.Attempted || strings.Contains(err.Error(), "fixture-secret") {
				t.Fatalf("%+v %v", got, err)
			}
			body, _ := os.ReadFile(log)
			if strings.Count(string(body), "new-workspace") != 1 {
				t.Fatalf("retried %s", body)
			}
		})
	}
}

func TestCMUXWorkspaceRejectsInvalidBeforeExecution(t *testing.T) {
	binary := buildFakeCMUX(t)
	for _, field := range []string{"cwd", "role", "prompt", "model", "args", "binary", "snapshot", "window", "missing-cwd", "missing-binary", "mutated-plan"} {
		t.Run(field, func(t *testing.T) {
			log := fakeCMUXEnv(t, "json")
			plan, o := workspacePlan(t, binary, "nested")
			switch field {
			case "binary":
				o.ToolBinaries["codex"] = binary + "\x00"
			case "snapshot":
				plan.SnapshotPath += "\x00"
			case "window":
				o.WindowID = "window:9; exit"
			case "missing-cwd":
				if err := os.Remove(plan.Seats[0].Cwd); err != nil {
					t.Fatal(err)
				}
			case "missing-binary":
				o.ToolBinaries["codex"] = "/missing/tool"
			case "mutated-plan":
				plan.Seats[0].Tool = "claude"
			default:
				c := plan.Config
				a := c.Agents["zmain"]
				switch field {
				case "cwd":
					a.Cwd += "\x00"
				case "role":
					a.Role += "\x00"
				case "prompt":
					a.Prompt += "\x00"
				case "model":
					a.Model += "\x00"
				case "args":
					a.Args = []string{"--profile", "bad\x00"}
				}
				c.Agents["zmain"] = a
				// Preserve the digest contract so these reach text validation.
				c.Digest = ""
				body, _ := json.Marshal(c)
				sum := sha256.Sum256(body)
				c.Digest = hex.EncodeToString(sum[:])
				plan, _ = Compile(c, Options{WrapperBinary: binary, SnapshotPath: plan.SnapshotPath})
			}
			got, err := LaunchCMUXWorkspace(context.Background(), plan, o)
			if err == nil || got.Attempted {
				t.Fatalf("accepted %s: %+v %v", field, got, err)
			}
			if _, err := os.Stat(log); !os.IsNotExist(err) {
				t.Fatal("cmux invoked before validation")
			}
		})
	}
}

func TestTUICommandThreeToolsFrozenRoleAndFlags(t *testing.T) {
	for _, tool := range []string{"codex", "opencode", "claude"} {
		a := config.ResolvedAgent{Tool: tool, Cwd: "/tmp/space ' cwd", Role: "role ' quoted", Prompt: "prompt $(literal)\nsecret", Model: "model ' ; $(literal)", Permissions: "full", Session: "persistent"}
		got, err := TUICommand(a, "/tmp/tool ' binary")
		if err != nil {
			t.Fatal(err)
		}
		if got.Binary != "/tmp/tool ' binary" || !strings.Contains(strings.Join(got.Args, "\n"), a.Prompt) {
			t.Fatalf("lost frozen text %+v", got)
		}
		for _, arg := range got.Args {
			if arg == "exec" || arg == "run" || arg == "--print" || arg == "--json" {
				t.Fatalf("one-shot adapter used: %+v", got)
			}
		}
		foundModel := false
		for i, arg := range got.Args {
			if arg == "--model" && i+1 < len(got.Args) {
				foundModel = got.Args[i+1] == a.Model
			}
		}
		if !foundModel {
			t.Fatal("model did not remain literal argv")
		}
	}
}
