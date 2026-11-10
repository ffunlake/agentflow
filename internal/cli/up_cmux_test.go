package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"agentflow/internal/config"
	"agentflow/internal/coordinator"
	"agentflow/internal/runtime"
	"agentflow/internal/store"
)

func TestDefaultRealUpCMUXExecutableEndToEnd(t *testing.T) {
	buildDir := t.TempDir()
	tool := filepath.Join(buildDir, "fake tool ' cmux 中文 😀")
	app := filepath.Join(buildDir, "agentflow ' app 中文 😀")
	for _, build := range []struct{ path, pkg string }{{tool, "../runtime/testdata/cmux"}, {app, "../../cmd/agentflow"}} {
		cmd := exec.Command("go", "build", "-o", build.path, build.pkg)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("build: %v %s", err, out)
		}
	}
	for _, mode := range []string{"refs", "json", "system-temp", "fail", "tree-fail", "invalid", "control-role", "control-cwd", "control-binary", "control-snapshot"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			dir, err := filepath.EvalSymlinks(dir)
			if err != nil {
				t.Fatal(err)
			}
			cwd := filepath.Join(dir, "cwd space ' $(literal) 中文 😀")
			if err := os.Mkdir(cwd, 0700); err != nil {
				t.Fatal(err)
			}
			file := filepath.Join(dir, "team.json")
			prompt := "fixture-secret; ' $(touch forbidden)\n\"literal\""
			c := config.Config{Version: 1, Name: "CMUX launcher", Terminal: "cmux", Lead: "main", ContextDir: "context", Defaults: config.Defaults{Cwd: &cwd}, Agents: map[string]config.Agent{}}
			cwds := map[string]string{"main": cwd, "builder": filepath.Join(dir, "builder cwd ' path"), "reviewer": filepath.Join(dir, "reviewer cwd \" path")}
			for id, name := range map[string]string{"main": "codex", "builder": "opencode", "reviewer": "claude"} {
				a := config.Agent{Role: "role " + id, Tool: &name, Prompt: &prompt, Model: "model ' \" $(literal)"}
				if id != "main" {
					if err := os.Mkdir(cwds[id], 0700); err != nil {
						t.Fatal(err)
					}
					agentCwd := cwds[id]
					a.Cwd = &agentCwd
				}
				c.Agents[id] = a
			}
			c.Layout = &config.Layout{Split: "columns", Children: []config.Layout{{Agent: "main"}, {Split: "rows", Children: []config.Layout{{Agent: "builder"}, {Agent: "reviewer"}}}}}
			if mode == "control-role" || mode == "control-cwd" {
				a := c.Agents["reviewer"]
				if mode == "control-role" {
					a.Role += "\runsafe"
				} else {
					badCwd := cwds["reviewer"] + "\tunsafe"
					if err := os.Mkdir(badCwd, 0700); err != nil {
						t.Fatal(err)
					}
					a.Cwd = &badCwd
				}
				c.Agents["reviewer"] = a
			}
			body, _ := json.Marshal(c)
			if err := os.WriteFile(file, body, 0600); err != nil {
				t.Fatal(err)
			}
			runDir := filepath.Join(dir, "new run ' path 中文 😀")
			if mode == "control-snapshot" {
				runDir += "\x1bunsafe"
			}
			log := filepath.Join(dir, "cmux.calls.jsonl")
			toolLog := filepath.Join(dir, "tool.calls.jsonl")
			env := append(os.Environ(), "CMUX_FAKE_LOG="+log, "CMUX_FAKE_STATE="+filepath.Join(dir, "tree.json"), "CMUX_FAKE_MODE="+mode, "CMUX_FAKE_TOOL_LOG="+toolLog, "CMUX_FAKE_EXEC=1")
			args := []string{"up", "-f", file, "--run", "cmux-demo", "--run-dir", runDir, "--cmux-binary", tool, "--codex-binary", tool, "--opencode-binary", tool, "--claude-binary", tool, "--json"}
			if mode == "system-temp" {
				// Use the host os.TempDir, including its native path separators.
				args = append(args[:5:5], args[7:]...)
			}
			if mode == "control-binary" {
				badTool := filepath.Join(buildDir, "bad tool\x7funsafe")
				body, err := os.ReadFile(tool)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(badTool, body, 0700); err != nil {
					t.Fatal(err)
				}
				for i, arg := range args {
					if arg == "--claude-binary" {
						args[i+1] = badTool
					}
				}
			}
			if mode == "json" {
				args = append(args, "--window", "window:9")
			}
			invoke := func() (coordinator.UpResult, error, string) {
				t.Helper()
				cmd := exec.Command(app, args...)
				cmd.Env = env
				var out, diagnostic bytes.Buffer
				cmd.Stdout, cmd.Stderr = &out, &diagnostic
				err := cmd.Run()
				var got coordinator.UpResult
				if json.Unmarshal(out.Bytes(), &got) != nil {
					t.Fatalf("invalid output %s %s", &out, &diagnostic)
				}
				if strings.Contains(out.String()+diagnostic.String(), prompt) || strings.Contains(diagnostic.String(), "fixture-secret") {
					t.Fatal("private prompt/native stderr leaked")
				}
				return got, err, diagnostic.String()
			}
			got, err, diagnostic := invoke()
			if mode == "system-temp" {
				runDir = got.RunDir
				parent, parentErr := filepath.EvalSymlinks(os.TempDir())
				if parentErr != nil || !filepath.IsAbs(runDir) || filepath.Dir(runDir) != parent || !strings.HasPrefix(filepath.Base(runDir), "agentflow-cmux-demo-") {
					t.Fatalf("temporary run_dir: %+v %v", got, parentErr)
				}
				t.Cleanup(func() { _ = os.RemoveAll(runDir) })
				// A repeat/reopen must use the actual output, never allocate again.
				args = append(args, "--run-dir", runDir)
			}
			if strings.HasPrefix(mode, "control-") {
				if err == nil || got.Executed || got.CMUX != nil || got.CoordinatorReady || got.ReadyProof != nil {
					t.Fatalf("accepted terminal control: %+v %v %s", got, err, diagnostic)
				}
				for _, path := range []string{log, toolLog, runDir, filepath.Join(dir, "tree.json")} {
					if _, err := os.Stat(path); !os.IsNotExist(err) {
						t.Fatalf("side effect before rejection: %s: %v", path, err)
					}
				}
				return
			}
			success := mode == "refs" || mode == "json" || mode == "system-temp"
			if success {
				if err != nil || got.RunDir != runDir || got.Status != "LAUNCHED" || !got.Executed || got.CoordinatorReady || got.Seat != nil || got.ReadyProof != nil || got.RuntimeCommunication != "UNVERIFIED" || got.Registration != "SURFACE_REGISTERED" || got.CMUX == nil || len(got.CMUX.Surfaces) != 3 {
					t.Fatalf("%+v %v %s", got, err, diagnostic)
				}
				body, err := os.ReadFile(toolLog)
				if err != nil {
					t.Fatal(err)
				}
				lines := strings.Split(strings.TrimSpace(string(body)), "\n")
				if len(lines) != 3 {
					t.Fatalf("tool launch count: %s", body)
				}
				for i, id := range []string{"main", "builder", "reviewer"} {
					var call struct {
						Cwd  string   `json:"cwd"`
						Args []string `json:"args"`
					}
					if json.Unmarshal([]byte(lines[i]), &call) != nil {
						t.Fatal(lines[i])
					}
					canonicalCwd, _ := filepath.EvalSymlinks(cwds[id])
					if call.Cwd != canonicalCwd {
						t.Fatalf("cwd changed: %s want %s", call.Cwd, canonicalCwd)
					}
					joined := strings.Join(call.Args, "\n")
					if !strings.Contains(joined, "--model\nmodel ' \" $(literal)") {
						t.Fatalf("model changed: %q", call.Args)
					}
					if !strings.Contains(joined, "Role: role "+id+"\nFrozen role instructions:\n"+prompt) {
						t.Fatalf("role/prompt changed: %q", call.Args)
					}
					if got.CMUX.Surfaces[i].AgentID != id {
						t.Fatal("surface mapping not visual order")
					}
				}
				snapshot, err := runtime.ReadSnapshot(got.SnapshotPath)
				if err != nil {
					t.Fatal(err)
				}
				for _, seat := range snapshot.Seats {
					if seat.Status != runtime.Starting || seat.Native != (runtime.NativeBinding{}) {
						t.Fatal("launch fabricated registration")
					}
				}
				db, err := store.OpenExisting(filepath.Join(runDir, "state.db"), false)
				if err != nil {
					t.Fatal(err)
				}
				bindings, err := db.CMUXRegistrations(t.Context(), "cmux-demo")
				if err != nil || len(bindings) != 3 {
					t.Fatal(bindings, err)
				}
				for _, b := range bindings {
					seat, err := db.GetSeat(t.Context(), b.RunID, b.AgentID, b.Generation)
					if err != nil || seat.NativeSessionID != "" || seat.Status != "LAUNCHED" || b.NativeReady != "UNVERIFIED" || b.LaunchID == "" {
						t.Fatal(b, seat, err)
					}
				}
				db.Close()
				statusCmd := exec.Command(app, "status", "--run", "cmux-demo", "--run-dir", runDir, "--json")
				statusBody, err := statusCmd.CombinedOutput()
				var status runtime.StatusResult
				if err != nil || json.Unmarshal(statusBody, &status) != nil || status.Run == nil || status.Run.Status != "LAUNCHED" || len(status.Surfaces) != 3 {
					t.Fatalf("status: %v %s", err, statusBody)
				}
			} else if err == nil || got.Status != "FAILED" {
				t.Fatalf("failure reported success: %+v %v", got, err)
			}
			if _, err := os.Stat(filepath.Join(runDir, "cmux.launch.json")); err != nil {
				t.Fatal("missing retained result", err)
			}
			before, _ := os.ReadFile(log)
			repeated, err, _ := invoke()
			if err == nil || len(repeated.Diagnostics) != 1 || repeated.Diagnostics[0].Code != "UP_ALREADY_ATTEMPTED" {
				t.Fatalf("repeated up: %+v %v", repeated, err)
			}
			after, _ := os.ReadFile(log)
			if !bytes.Equal(before, after) || strings.Count(string(after), "new-workspace") != 1 {
				t.Fatalf("replayed cmux: %s", after)
			}
		})
	}
}

func TestCMUXUpRejectsExistingTargetsAndHeadlessOverrides(t *testing.T) {
	for _, flags := range [][]string{{"--workspace", "workspace:2"}, {"--surface", "surface:2"}, {"--headless-codex", "--codex-binary", "/tmp/codex"}} {
		var out, diagnostic bytes.Buffer
		code := Run(append([]string{"up", "-f", "../../examples/team.yaml", "--run", "test", "--json"}, flags...), &out, &diagnostic, "test")
		if code != 1 || !strings.Contains(diagnostic.String(), "OPTIONS_INVALID") {
			t.Fatalf("%d %s %s", code, &out, &diagnostic)
		}
	}
}
