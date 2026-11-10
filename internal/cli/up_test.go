package cli

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"agentflow/internal/runtime"
)

func TestUpDryRun(t *testing.T) {
	file, err := filepath.Abs("../../examples/team.yaml")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", t.TempDir())
	for _, asJSON := range []bool{false, true} {
		args := []string{"up", "-f", file, "--dry-run", "--tool", "claude"}
		if asJSON {
			args = append(args, "--json")
		}
		var stdout, stderr bytes.Buffer
		if code := Run(args, &stdout, &stderr, "test"); code != 0 || stderr.Len() != 0 {
			t.Fatalf("exit=%d stderr=%s", code, &stderr)
		}
		if asJSON {
			var plan runtime.StartupPlan
			if err := json.Unmarshal(stdout.Bytes(), &plan); err != nil {
				t.Fatal(err)
			}
			if plan.Executed || plan.Status != "PLANNED" || plan.Seats[1].Tool != "claude" || plan.Seats[0].Tool != "opencode" {
				t.Fatalf("incorrect plan: %s", &stdout)
			}
		} else {
			for _, want := range []string{"S4.1 partial", "Status: PLANNED", "Executed: false", "builder: tool=opencode", "main: tool=claude", "session=persistent", "permissions=full", "prompt text ref:", "wrapper argv:", "horizontal ratio=0.4", "vertical ratio=0.5", "UNVERIFIED"} {
				if !strings.Contains(stdout.String(), want) {
					t.Errorf("text missing %q: %s", want, &stdout)
				}
			}
		}
		if code := Run(args, brokenWriter{}, &stderr, "test"); code != 1 || !strings.Contains(stderr.String(), "write output") {
			t.Fatalf("output failure: exit=%d stderr=%s", code, &stderr)
		}
	}
}

func TestUpFailuresAndHelp(t *testing.T) {
	file := "../../examples/team.yaml"
	for _, tc := range []struct {
		args []string
		code int
		want string
	}{
		{nil, 2, "requires -f"},
		{[]string{"-f"}, 2, "arguments"},
		{[]string{"-f", ""}, 2, "requires -f"},
		{[]string{"-f", file, "--dry-run", "--tool", "sh"}, 2, "--tool"},
		{[]string{"-f", file, "--dry-run", "--tool="}, 2, "--tool"},
		{[]string{"-f", file, "--dry-run", "--unknown"}, 2, "arguments"},
		{[]string{"-f", file, "--dry-run", "extra"}, 2, "positional"},
		{[]string{"-f", "missing.yaml", "--dry-run"}, 1, "read config"},
		{[]string{"-f", file, "--headless-codex", "--headless-opencode"}, 2, "mutually exclusive"},
		{[]string{"-f", file, "--dry-run", "--headless-codex", "--headless-opencode"}, 2, "mutually exclusive"},
		{[]string{"-f", file, "--headless-codex", "--headless-claude"}, 2, "mutually exclusive"},
		{[]string{"-f", file, "--headless-opencode", "--headless-claude"}, 2, "mutually exclusive"},
		{[]string{"-f", file, "--dry-run", "--headless-codex", "--headless-opencode", "--headless-claude"}, 2, "mutually exclusive"},
	} {
		var stdout, stderr bytes.Buffer
		code := Run(append([]string{"up"}, tc.args...), &stdout, &stderr, "test")
		if code != tc.code || stdout.Len() != 0 || !strings.Contains(stderr.String(), tc.want) || !strings.HasPrefix(stderr.String(), "agentflow: error:") {
			t.Fatalf("args=%q exit=%d stdout=%s stderr=%s", tc.args, code, &stdout, &stderr)
		}
	}
	for _, flag := range []string{"-h", "--help"} {
		var stdout, stderr bytes.Buffer
		if code := Run([]string{"up", flag}, &stdout, &stderr, "test"); code != 0 || stderr.Len() != 0 || !strings.Contains(stdout.String(), "Usage: agentflow up") {
			t.Fatalf("help exit=%d stdout=%s stderr=%s", code, &stdout, &stderr)
		}
		if code := Run([]string{"up", flag}, brokenWriter{}, &stderr, "test"); code != 1 {
			t.Fatalf("help output failure exit=%d", code)
		}
	}
}

func TestHeadlessCoordinatorFlagsMutuallyExclusive(t *testing.T) {
	for _, flags := range [][]string{{"--headless-codex", "--headless-opencode"}, {"--headless-codex", "--headless-claude"}, {"--headless-opencode", "--headless-claude"}, {"--headless-codex", "--headless-opencode", "--headless-claude"}} {
		var out, diagnostic bytes.Buffer
		code := Run(append([]string{"coordinator", "--run", "run"}, flags...), &out, &diagnostic, "test")
		if code != 2 || !strings.Contains(diagnostic.String(), "mutually exclusive") || out.Len() != 0 {
			t.Fatal(code, &out, &diagnostic)
		}
	}
}

func TestHeadlessClaudeDryRunAndHelp(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	var out, diagnostic bytes.Buffer
	code := Run([]string{"up", "-f", "../../examples/headless-claude.yaml", "--headless-claude", "--dry-run", "--json"}, &out, &diagnostic, "test")
	var plan runtime.StartupPlan
	if code != 0 || diagnostic.Len() != 0 || json.Unmarshal(out.Bytes(), &plan) != nil || plan.Executed || len(plan.Seats) != 2 || plan.Seats[0].Tool != "claude" {
		t.Fatal(code, &out, &diagnostic)
	}
	for _, cmd := range []string{"up", "coordinator"} {
		out.Reset()
		if code := Run([]string{cmd, "--help"}, &out, &diagnostic, "test"); code != 0 || !strings.Contains(out.String(), "--headless-claude") {
			t.Fatal(code, &out, &diagnostic)
		}
	}
}

func TestRealUpMissingRunIsStructured(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	for _, jsonOutput := range []bool{false, true} {
		args := []string{"up", "-f", "../../examples/team.yaml"}
		if jsonOutput {
			args = append(args, "--json")
		}
		var out, diagnostic bytes.Buffer
		if code := Run(args, &out, &diagnostic, "test"); code != 1 || !strings.Contains(diagnostic.String(), "UP_OPTIONS_REQUIRED") || !strings.Contains(out.String(), "PARTIAL") {
			t.Fatalf("%d %s %s", code, &out, &diagnostic)
		}
		if jsonOutput && !json.Valid(out.Bytes()) {
			t.Fatal(&out)
		}
	}
}
