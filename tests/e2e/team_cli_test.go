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
	"sort"
	"strings"
	"testing"
	"time"
)

type teamCLIResult struct {
	code           int
	stdout, stderr string
}

func TestTeamCLIProcess(t *testing.T) {
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
	workdir := t.TempDir()
	tools, emptyPath := t.TempDir(), t.TempDir()
	marker := filepath.Join(workdir, "unexpected-tool-launch")
	for _, tool := range []string{"opencode", "codex", "claude", "cmux"} {
		// A lookup must succeed without executing even a version/help probe.
		body := "#!/bin/sh\nprintf 'unexpected launch\\n' >> \"$AGENTFLOW_TEST_MARKER\"\nexit 97\n"
		if err := os.WriteFile(filepath.Join(tools, tool), []byte(body), 0700); err != nil {
			t.Fatal(err)
		}
	}
	run := func(t *testing.T, path string, args ...string) teamCLIResult {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, binary, args...)
		cmd.Dir = workdir // Paths must be relative to the config, not the caller.
		for _, entry := range os.Environ() {
			if !strings.HasPrefix(entry, "PATH=") && !strings.HasPrefix(entry, "AGENTFLOW_TEST_MARKER=") {
				cmd.Env = append(cmd.Env, entry)
			}
		}
		cmd.Env = append(cmd.Env, "PATH="+path, "AGENTFLOW_TEST_MARKER="+marker)
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		err := cmd.Run()
		code := 0
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			code = exitErr.ExitCode()
		} else if err != nil {
			t.Fatal(err)
		}
		if ctx.Err() != nil {
			t.Fatalf("CLI timed out: %v", ctx.Err())
		}
		if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("CLI executed a tool or cmux: marker stat = %v", err)
		}
		return teamCLIResult{code, stdout.String(), stderr.String()}
	}
	assertSuccess := func(t *testing.T, result teamCLIResult) {
		t.Helper()
		if result.code != 0 || result.stderr != "" || result.stdout == "" {
			t.Fatalf("expected success: %+v", result)
		}
	}
	yamlFile, jsonFile := filepath.Join(root, "examples/team.yaml"), filepath.Join(root, "examples/team.json")
	t.Run("up-dry-run-offline-and-equivalent", func(t *testing.T) {
		var previous string
		for _, file := range []string{yamlFile, jsonFile, yamlFile} {
			result := run(t, tools, "up", "-f", file, "--dry-run", "--json")
			assertSuccess(t, result)
			var plan struct {
				Status   string
				Executed bool
				Seats    []struct{ ID, Tool string }
			}
			if err := json.Unmarshal([]byte(result.stdout), &plan); err != nil {
				t.Fatal(err)
			}
			if plan.Status != "PLANNED" || plan.Executed || len(plan.Seats) != 3 || plan.Seats[0].ID != "builder" || plan.Seats[1].ID != "main" {
				t.Fatalf("incorrect startup plan: %s", result.stdout)
			}
			if previous != "" && previous != result.stdout {
				t.Fatal("YAML/JSON or repeated startup plans differ")
			}
			previous = result.stdout
		}
		assertSuccess(t, run(t, emptyPath, "up", "-f", yamlFile, "--dry-run"))
		for _, tool := range []string{"opencode", "codex", "claude"} {
			result := run(t, tools, "up", "-f", yamlFile, "--dry-run", "--tool", tool, "--json")
			assertSuccess(t, result)
			var plan struct{ Seats []struct{ ID, Tool string } }
			if err := json.Unmarshal([]byte(result.stdout), &plan); err != nil {
				t.Fatal(err)
			}
			if plan.Seats[0].Tool != "opencode" || plan.Seats[1].Tool != tool || plan.Seats[2].Tool != "opencode" {
				t.Fatalf("wrong tool precedence: %s", result.stdout)
			}
		}
		entries, err := os.ReadDir(workdir)
		if err != nil || len(entries) != 0 {
			t.Fatalf("dry-run created state in caller directory: %v, %v", entries, err)
		}
	})
	t.Run("up-errors-without-launch", func(t *testing.T) {
		for _, tc := range []struct {
			args []string
			code int
			want string
		}{
			{nil, 2, "requires -f"},
			{[]string{"-f", yamlFile, "--dry-run", "--tool", "bash"}, 2, "--tool"},
			{[]string{"-f", yamlFile, "--dry-run", "--unknown"}, 2, "arguments"},
			{[]string{"-f", "absent.yaml", "--dry-run"}, 1, "read config"},
			{[]string{"-f", yamlFile}, 1, "UP_OPTIONS_REQUIRED"},
			{[]string{"-f", yamlFile, "--json"}, 1, "UP_OPTIONS_REQUIRED"},
		} {
			result := run(t, tools, append([]string{"up"}, tc.args...)...)
			structuredUp := tc.want == "UP_OPTIONS_REQUIRED" && strings.Contains(result.stdout, "PARTIAL") && strings.Contains(result.stderr, "UP_OPTIONS_REQUIRED")
			if result.code != tc.code || (result.stdout != "" && !structuredUp) || !strings.Contains(result.stderr, tc.want) {
				t.Fatalf("unexpected up result: %+v", result)
			}
		}
	})
	for _, file := range []string{yamlFile, jsonFile} {
		t.Run("validate-"+filepath.Ext(file), func(t *testing.T) {
			result := run(t, emptyPath, "validate", "-f", file)
			assertSuccess(t, result)
			if !strings.Contains(result.stdout, "Configuration valid: "+file) {
				t.Fatalf("unclear validation: %s", result.stdout)
			}
		})
	}
	t.Run("plan-text", func(t *testing.T) {
		result := run(t, emptyPath, "plan", "-f", yamlFile)
		assertSuccess(t, result)
		for _, want := range []string{
			"Configuration: " + yamlFile, "Lead: main", "Context directory: " + filepath.Join(root, ".agentflow/context"),
			"main: tool=codex", "builder: tool=opencode", "reviewer: tool=opencode", "permissions=full", "session=persistent",
			"cwd: /Users/funlake/Projects/okra-wiki", "role=", "prompt:", "判断工作可行性", "split: horizontal ratio=0.4", "split: vertical ratio=0.5",
		} {
			if !strings.Contains(result.stdout, want) {
				t.Errorf("plan missing %q: %s", want, result.stdout)
			}
		}
	})
	t.Run("plans-equivalent-and-stable", func(t *testing.T) {
		type planJSON struct {
			SourcePath string          `json:"source_path"`
			Config     json.RawMessage `json:"config"`
			Layout     json.RawMessage `json:"layout"`
		}
		var plans [2]planJSON
		for i, file := range []string{yamlFile, jsonFile} {
			result := run(t, emptyPath, "plan", "-f", file, "--json")
			assertSuccess(t, result)
			if err := json.Unmarshal([]byte(result.stdout), &plans[i]); err != nil {
				t.Fatal(err)
			}
			if plans[i].SourcePath != file || string(plans[i].Config) == "null" || len(plans[i].Layout) == 0 {
				t.Fatalf("incomplete plan: %s", result.stdout)
			}
			if again := run(t, emptyPath, "plan", "-f", file, "--json"); again != result {
				t.Fatal("JSON plan changes between identical invocations")
			}
		}
		if !bytes.Equal(plans[0].Config, plans[1].Config) || !bytes.Equal(plans[0].Layout, plans[1].Layout) {
			t.Fatal("example YAML and JSON plans differ")
		}
	})
	t.Run("doctor-text", func(t *testing.T) {
		result := run(t, tools, "doctor", "-f", yamlFile)
		assertSuccess(t, result)
		for _, want := range []string{"Doctor: OK", "Configuration: " + yamlFile + " (VALID)", "Layout: VALID", "codex: FOUND", "Runtime communication: UNVERIFIED"} {
			if !strings.Contains(result.stdout, want) {
				t.Errorf("doctor missing %q: %s", want, result.stdout)
			}
		}
	})
	t.Run("doctor-json", func(t *testing.T) {
		result := run(t, tools, "doctor", "-f", jsonFile, "--json")
		assertSuccess(t, result)
		var report struct {
			Status, Configuration, Layout string
			Runtime                       string `json:"runtime_communication"`
			Tools                         []struct{ Tool, Status, Executable string }
			Diagnostics                   []json.RawMessage
		}
		if err := json.Unmarshal([]byte(result.stdout), &report); err != nil {
			t.Fatal(err)
		}
		if report.Status != "OK" || report.Configuration != "VALID" || report.Layout != "VALID" || report.Runtime != "UNVERIFIED" || len(report.Diagnostics) != 0 || len(report.Tools) != 2 {
			t.Fatalf("incorrect report: %s", result.stdout)
		}
		for i, want := range []string{"codex", "opencode"} {
			got := report.Tools[i]
			if got.Tool != want || got.Status != "FOUND" || got.Executable != filepath.Join(tools, want) {
				t.Fatalf("incorrect executable lookup: %+v", got)
			}
		}
		if again := run(t, tools, "doctor", "-f", jsonFile, "--json"); again != result {
			t.Fatal("JSON doctor report changes between identical invocations")
		}
	})
	for _, tool := range []string{"opencode", "codex", "claude"} {
		t.Run("override-"+tool, func(t *testing.T) {
			result := run(t, emptyPath, "plan", "-f", yamlFile, "--tool", tool, "--json")
			assertSuccess(t, result)
			var plan struct {
				Config struct {
					Agents map[string]struct{ Tool string }
				}
			}
			if err := json.Unmarshal([]byte(result.stdout), &plan); err != nil {
				t.Fatal(err)
			}
			for id, want := range map[string]string{"main": tool, "builder": "opencode", "reviewer": "opencode"} {
				if got := plan.Config.Agents[id].Tool; got != want {
					t.Errorf("%s tool = %q, want %q", id, got, want)
				}
			}
			assertSuccess(t, run(t, emptyPath, "validate", "-f", yamlFile, "--tool", tool))
			result = run(t, emptyPath, "doctor", "-f", yamlFile, "--tool", tool, "--json")
			var report struct{ Tools []struct{ Tool string } }
			if err := json.Unmarshal([]byte(result.stdout), &report); err != nil {
				t.Fatal(err)
			}
			var names []string
			for _, lookup := range report.Tools {
				names = append(names, lookup.Tool)
			}
			want := []string{"opencode"}
			if tool != "opencode" {
				want = append(want, tool)
			}
			sort.Strings(want)
			if result.code != 1 || !reflect.DeepEqual(names, want) {
				t.Fatalf("doctor did not use resolved tools: %+v", result)
			}
		})
	}

	// These cases exercise loading, resolution and layout compilation separately.
	valid := "version: 1\nname: test\nterminal: cmux\nlead: main\ncontext_dir: ./context\nagents:\n  main:\n    role: Developer\n    prompt: Build something\nlayout:\n  agent: main\n"
	fixtures := map[string]struct{ body, field string }{
		"invalid-schema": {strings.Replace(valid, "version: 1", "version: 7", 1), "version"},
		"unknown-field":  {valid + "surprise: value\n", "surprise"},
		"duplicate-key":  {valid + "name: duplicate\n", "name"},
		"invalid-layout": {strings.Replace(valid, "  agent: main", "  agent: main\n  ratio: 0.4", 1), "layout.ratio"},
		"missing-prompt": {strings.Replace(valid, "prompt: Build something", "prompt_file: absent.md", 1), "agents.main.prompt_file"},
	}
	for name, fixture := range fixtures {
		file := filepath.Join(workdir, name+".yaml")
		if err := os.WriteFile(file, []byte(fixture.body), 0600); err != nil {
			t.Fatal(err)
		}
		for _, command := range []string{"validate", "plan", "doctor", "up"} {
			t.Run(command+"-"+name, func(t *testing.T) {
				args := []string{command, "-f", file}
				if command == "up" {
					args = append(args, "--dry-run")
				}
				result := run(t, emptyPath, args...)
				if result.code != 1 || !strings.Contains(result.stderr, fixture.field) || !strings.Contains(result.stderr, file) || !strings.HasPrefix(result.stderr, "agentflow: error:") {
					t.Fatalf("wrong validation failure: %+v", result)
				}
				if command != "doctor" && result.stdout != "" {
					t.Fatalf("unexpected success output: %s", result.stdout)
				}
			})
		}
	}
	for _, command := range []string{"validate", "plan", "doctor"} {
		t.Run(command+"-missing-file", func(t *testing.T) {
			file := filepath.Join(workdir, "absent.yaml")
			result := run(t, tools, command, "-f", file)
			if result.code != 1 || !strings.Contains(result.stderr, file) || !strings.Contains(result.stderr, "read config") {
				t.Fatalf("wrong missing-file error: %+v", result)
			}
		})
		for _, args := range [][]string{
			{}, {"-f"}, {"-f", ""}, {"-f", yamlFile, "--unknown"}, {"-f", yamlFile, "extra"},
			{"-f", yamlFile, "--tool", "cmux"}, {"-f", yamlFile, "--tool="}, {"-f", yamlFile, "--tool"},
		} {
			t.Run(command+"-usage-"+strings.Join(args, "_"), func(t *testing.T) {
				result := run(t, tools, append([]string{command}, args...)...)
				if result.code != 2 || result.stdout != "" || !strings.HasPrefix(result.stderr, "agentflow: error:") {
					t.Fatalf("wrong usage failure: %+v", result)
				}
			})
		}
		for _, help := range []string{"-h", "--help"} {
			t.Run(command+help, func(t *testing.T) {
				result := run(t, emptyPath, command, help)
				assertSuccess(t, result)
				if !strings.Contains(result.stdout, "Usage: agentflow "+command) {
					t.Fatalf("missing command help: %s", result.stdout)
				}
			})
		}
	}
	for _, command := range []string{"plan", "doctor"} {
		for _, fixture := range []struct{ file, field, layout string }{
			{"absent.yaml", "$", "NOT_CHECKED"},
			{"invalid-schema.yaml", "version", "NOT_CHECKED"},
			{"missing-prompt.yaml", "agents.main.prompt_file", "NOT_CHECKED"},
			{"invalid-layout.yaml", "layout.ratio", "INVALID"},
		} {
			t.Run(command+"-json-"+fixture.file, func(t *testing.T) {
				file := filepath.Join(workdir, fixture.file)
				result := run(t, tools, command, "-f", file, "--json")
				var report struct {
					SourcePath  string `json:"source_path"`
					Status      string
					Layout      string
					Diagnostics []struct{ Path, Code string }
				}
				if err := json.Unmarshal([]byte(result.stdout), &report); err != nil {
					t.Fatal(err)
				}
				if result.code != 1 || result.stderr == "" || report.SourcePath != file || report.Status != "FAILED" || report.Layout != fixture.layout || len(report.Diagnostics) != 1 || report.Diagnostics[0].Path != fixture.field {
					t.Fatalf("wrong structured config failure: %+v", result)
				}
			})
		}
	}
	t.Run("doctor-missing-tools", func(t *testing.T) {
		result := run(t, emptyPath, "doctor", "-f", yamlFile, "--json")
		var report struct {
			Status, Configuration, Layout string
			Runtime                       string `json:"runtime_communication"`
			Tools                         []struct{ Tool, Status, Executable string }
			Diagnostics                   []struct{ Code, Path string }
		}
		if err := json.Unmarshal([]byte(result.stdout), &report); err != nil {
			t.Fatal(err)
		}
		if result.code != 1 || report.Status != "FAILED" || report.Configuration != "VALID" || report.Layout != "VALID" || report.Runtime != "UNVERIFIED" || len(report.Diagnostics) != 2 || len(report.Tools) != 2 {
			t.Fatalf("wrong missing-tool report: %+v", result)
		}
		for i, lookup := range report.Tools {
			if lookup.Status != "MISSING" || lookup.Executable != "" || report.Diagnostics[i].Code != "EXECUTABLE_MISSING" || report.Diagnostics[i].Path != "tools."+lookup.Tool+".executable" || !strings.Contains(result.stderr, lookup.Tool) {
				t.Fatalf("missing tool is not structured: %+v", result)
			}
		}
		text := run(t, emptyPath, "doctor", "-f", yamlFile)
		if text.code != 1 || !strings.Contains(text.stdout, "opencode: MISSING") || text.stderr == "" {
			t.Fatalf("wrong text diagnosis: %+v", text)
		}
	})
	t.Run("prompt-file-paths-and-no-config-writes", func(t *testing.T) {
		dir := filepath.Join(workdir, "config with spaces")
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
		file := filepath.Join(dir, "team.yaml")
		body := strings.Replace(valid, "prompt: Build something", "prompt_file: role.md", 1)
		if err := os.WriteFile(file, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "role.md"), []byte("Role prompt from file\n"), 0600); err != nil {
			t.Fatal(err)
		}
		result := run(t, emptyPath, "plan", "-f", file, "--json")
		assertSuccess(t, result)
		var plan struct {
			Config struct {
				ContextDir string `json:"context_dir"`
				Agents     map[string]struct {
					Cwd, Prompt string
					PromptFile  string `json:"prompt_file"`
				}
			}
		}
		if err := json.Unmarshal([]byte(result.stdout), &plan); err != nil {
			t.Fatal(err)
		}
		agent := plan.Config.Agents["main"]
		if agent.Cwd != dir || agent.PromptFile != filepath.Join(dir, "role.md") || agent.Prompt != "Role prompt from file\n" || plan.Config.ContextDir != filepath.Join(dir, "context") {
			t.Fatalf("paths/prompt not resolved: %s", result.stdout)
		}
		assertSuccess(t, run(t, emptyPath, "validate", "-f", file))
		assertSuccess(t, run(t, tools, "doctor", "-f", file))
		after, err := os.ReadFile(file)
		if err != nil || string(after) != body {
			t.Fatalf("source config changed: %v", err)
		}
		entries, err := os.ReadDir(dir)
		if err != nil || len(entries) != 2 {
			t.Fatalf("unexpected output files beside config: entries=%v err=%v", entries, err)
		}
	})
}
