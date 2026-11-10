package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"agentflow/internal/config"
	"agentflow/internal/runtime"
)

func TestSeatCLIProcessOffline(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(t.TempDir(), "agentflow")
	build := exec.Command("go", "build", "-mod=readonly", "-o", binary, "./cmd/agentflow")
	build.Dir = root
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, output)
	}
	dir, tools := t.TempDir(), t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(tools, "launched")
	for _, tool := range []string{"opencode", "codex", "claude", "cmux"} {
		if err := os.WriteFile(filepath.Join(tools, tool), []byte("#!/bin/sh\nprintf launched > \"$AGENTFLOW_TEST_MARKER\"\nexit 99\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	run := func(args ...string) teamCLIResult {
		t.Helper()
		ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, binary, args...)
		cmd.Dir = dir
		for _, entry := range os.Environ() {
			if !strings.HasPrefix(entry, "PATH=") && !strings.HasPrefix(entry, "AGENTFLOW_TEST_MARKER=") && !strings.HasPrefix(entry, "AGENTFLOW_NATIVE_OPTIONS=") {
				cmd.Env = append(cmd.Env, entry)
			}
		}
		cmd.Env = append(cmd.Env, "PATH="+tools, "AGENTFLOW_TEST_MARKER="+marker)
		var out, diagnostic bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &diagnostic
		err := cmd.Run()
		var exit *exec.ExitError
		code := 0
		if errors.As(err, &exit) {
			code = exit.ExitCode()
		} else if err != nil {
			t.Fatal(err)
		}
		if ctx.Err() != nil {
			t.Fatalf("CLI timed out: %v", ctx.Err())
		}
		if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("entry launched a tool/cmux: %v", err)
		}
		return teamCLIResult{code, out.String(), diagnostic.String()}
	}
	assertFailure := func(result teamCLIResult, exitCode int, code string) {
		t.Helper()
		if result.code != exitCode || !strings.HasPrefix(result.stderr, "agentflow: error:") {
			t.Fatalf("wrong failure: %+v", result)
		}
		for _, secret := range []string{"fixture-secret", "private prompt", "private model"} {
			if strings.Contains(result.stdout+result.stderr, secret) {
				t.Fatalf("entry leaked %s", secret)
			}
		}
		if exitCode == 2 {
			if result.stdout != "" {
				t.Fatalf("usage error wrote stdout: %+v", result)
			}
			return
		}
		var report struct {
			Status   string
			Executed bool
			Error    runtime.SeatError
		}
		if err := json.Unmarshal([]byte(result.stdout), &report); err != nil {
			t.Fatal(err)
		}
		if report.Status != "FAILED" || report.Executed || report.Error.Code != code || report.Error.Path == "" || !strings.Contains(result.stderr, code) || strings.Contains(result.stdout, `"READY"`) || strings.Contains(result.stdout, `"ACK"`) || strings.Contains(result.stdout, `"native"`) {
			t.Fatalf("wrong structured failure: %+v", result)
		}
	}

	// Both CLI dry-run formats must produce the same materialized launch data,
	// snapshot bytes and _seat failure for all three supported tools.
	var previousCommands []runtime.CMUXLaunchCommand
	var previousSnapshot []byte
	path := filepath.Join(dir, "snapshot $(literal); ' \".json")
	var initial *runtime.RunSnapshot
	for _, ext := range []string{"yaml", "json"} {
		result := run("up", "-f", filepath.Join(root, "examples/team."+ext), "--dry-run", "--json")
		if result.code != 0 || result.stderr != "" {
			t.Fatalf("dry-run failed: %+v", result)
		}
		var plan runtime.StartupPlan
		if err := json.Unmarshal([]byte(result.stdout), &plan); err != nil {
			t.Fatal(err)
		}
		materialized, err := runtime.Compile(plan.Config, runtime.Options{WrapperBinary: binary, SnapshotPath: path})
		if err != nil {
			t.Fatal(err)
		}
		var commands []runtime.CMUXLaunchCommand
		for i, id := range []string{"main", "builder", "reviewer"} {
			target := runtime.CMUXTarget{WindowID: "window:1"}
			if i > 0 {
				target.WorkspaceID, target.SurfaceID, target.SplitDepth = "workspace:2", "surface:3", i-1
			}
			command, err := runtime.BuildCMUXCommand(materialized, id, target)
			if err != nil {
				t.Fatal(err)
			}
			commands = append(commands, command)
		}
		snapshot, err := runtime.NewSnapshot(materialized, "offline-seat-run")
		if err != nil {
			t.Fatal(err)
		}
		body, _ := json.Marshal(snapshot)
		if previousSnapshot != nil && (!bytes.Equal(body, previousSnapshot) || !reflect.DeepEqual(commands, previousCommands)) {
			t.Fatal("YAML/JSON snapshots or cmux/wrapper argv differ")
		}
		previousSnapshot, previousCommands, initial = body, commands, snapshot
		if err := os.WriteFile(path, body, 0600); err != nil {
			t.Fatal(err)
		}
		for _, command := range commands {
			// Execute only AgentFlow's wrapper entry. CMUX argv remains data.
			first := run(command.Wrapper.Args...)
			want := runtime.NativeTargetRequired
			if command.Tool == "claude" {
				want = runtime.SeatToolAdapterUnimplemented
			}
			assertFailure(first, 1, want)
			if again := run(command.Wrapper.Args...); again != first {
				t.Fatal("_seat error changed across invocations")
			}
		}
	}
	// Freeze private tool options and a file prompt, then remove the source.
	// Neither the command builder nor _seat may reread it or expose its text.
	promptFile := "private-role.md"
	if err := os.WriteFile(filepath.Join(dir, promptFile), []byte("private prompt fixture-secret"), 0600); err != nil {
		t.Fatal(err)
	}
	c := &config.Config{Version: 1, Name: "private", Terminal: "cmux", Lead: "solo", ContextDir: "context",
		SourcePath: filepath.Join(dir, "nonexistent-source.yaml"),
		Agents: map[string]config.Agent{"solo": {Role: "Developer", PromptFile: &promptFile,
			Model: "private model", Args: []string{"--credential=fixture-secret", "$(literal); ' \""}}}, Layout: &config.Layout{Agent: "solo"}}
	resolved, err := config.Resolve(c, config.ResolveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, promptFile)); err != nil {
		t.Fatal(err)
	}
	privatePath := filepath.Join(dir, "private-snapshot.json")
	privatePlan, err := runtime.Compile(resolved, runtime.Options{WrapperBinary: binary, SnapshotPath: privatePath})
	if err != nil {
		t.Fatal(err)
	}
	privateSnapshot, err := runtime.NewSnapshot(privatePlan, "private-seat-run")
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.WriteSnapshot(privatePath, privateSnapshot); err != nil {
		t.Fatal(err)
	}
	privateCommand, err := runtime.BuildCMUXCommand(privatePlan, "solo", runtime.CMUXTarget{WindowID: "window:1"})
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(privateCommand)
	for _, secret := range []string{"fixture-secret", "private prompt", "private model", "--credential"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("launch argv leaked %s", secret)
		}
	}
	assertFailure(run(privateCommand.Wrapper.Args...), 1, runtime.NativeTargetRequired)
	for _, args := range [][]string{
		{}, {"--snapshot", path}, {"--snapshot", "relative.json", "--agent", "main"},
		{"--snapshot", path, "--agent", "main", "--agent", "builder"},
		{"--snapshot", path, "--agent", "main", "extra"},
		{"--unknown", "fixture-secret", "--agent", "main"},
		{"--snapshot", path, "--snapshot", path},
		{"--snapshot", path, "--agent", "--help"},
	} {
		assertFailure(run(append([]string{"_seat"}, args...)...), 2, "")
	}
	assertFailure(run("_seat", "--snapshot", path, "--agent", "unknown"), 1, runtime.SeatAgentUnknown)
	assertFailure(run("_seat", "--snapshot", filepath.Join(dir, "absent.json"), "--agent", "main"), 1, runtime.SeatSnapshotInvalid)

	for _, stage := range []string{"tool", "cwd", "digest", "state", "run", "generation-zero", "generation-future", "ready", "bound", "unknown-field", "trailing", "decode-secret", "public-file", "symlink", "public-dir"} {
		t.Run(stage, func(t *testing.T) {
			body, _ := json.Marshal(initial)
			var snapshot runtime.RunSnapshot
			if err := json.Unmarshal(body, &snapshot); err != nil {
				t.Fatal(err)
			}
			code := runtime.SeatSnapshotInvalid
			switch stage {
			case "tool":
				snapshot.Seats[0].Tool = "claude" // Supported tool, wrong metadata.
			case "cwd":
				snapshot.Seats[0].Cwd = "relative"
			case "digest":
				a := snapshot.Config.Agents["builder"]
				a.Prompt = "private prompt fixture-secret"
				snapshot.Config.Agents["builder"] = a
			case "state":
				snapshot.Seats[0].Status = "STOPPED"
			case "run":
				snapshot.Seats[0].RunID = "other"
			case "generation-zero":
				snapshot.Seats[0].Generation = 0
			case "generation-future":
				snapshot.Seats[0].Generation, code = 2, runtime.SeatStartupInvalid
			case "ready", "bound":
				snapshot.Seats[0].Native = runtime.NativeBinding{SessionID: "session", WorkspaceID: "workspace", SurfaceID: "surface"}
				if stage == "ready" {
					snapshot.Seats[0].Status = runtime.Ready
				}
				code = runtime.SeatStartupInvalid
			}
			body, _ = json.Marshal(snapshot)
			switch stage {
			case "unknown-field":
				body = append([]byte(`{"fixture-secret":true,`), body[1:]...)
			case "trailing":
				body = append(body, []byte(`{}`)...)
			case "decode-secret":
				body = []byte(`{"version":"fixture-secret"}`)
			}
			file := filepath.Join(dir, stage+".json")
			if err := os.WriteFile(file, body, 0600); err != nil {
				t.Fatal(err)
			}
			if stage == "public-file" {
				if err := os.Chmod(file, 0644); err != nil {
					t.Fatal(err)
				}
			} else if stage == "symlink" {
				link := filepath.Join(dir, "link.json")
				if err := os.Symlink(file, link); err != nil {
					t.Fatal(err)
				}
				file = link
			} else if stage == "public-dir" {
				if err := os.Chmod(dir, 0755); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = os.Chmod(dir, 0700) })
			}
			before, err := os.ReadDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			assertFailure(run("_seat", "--snapshot", file, "--agent", "builder"), 1, code)
			after, err := os.ReadDir(dir)
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatalf("entry created/removed state: %v", err)
			}
			got, err := os.ReadFile(file)
			if err != nil || !bytes.Equal(got, body) {
				t.Fatalf("entry rewrote snapshot: %v", err)
			}
		})
	}
}
