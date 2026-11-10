package e2e_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLIProcess(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(t.TempDir(), "agentflow")
	build := exec.Command("go", "build", "-mod=readonly", "-trimpath", "-ldflags", "-X main.version=s0-test", "-o", binary, "./cmd/agentflow")
	build.Dir = root
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, output)
	}
	for _, tc := range []struct {
		name string
		args []string
		code int
		want string
	}{
		{"help", []string{"--help"}, 0, "Usage:"},
		{"version", []string{"--version"}, 0, "agentflow s0-test\n"},
		{"unknown flag", []string{"--unknown"}, 2, ""},
		{"unimplemented launcher", []string{"up", "-f", "examples/team.yaml"}, 1, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(binary, tc.args...)
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
			if code != tc.code {
				t.Fatalf("exit = %d, want %d; stderr = %s", code, tc.code, stderr.String())
			}
			if tc.code == 0 {
				if !strings.Contains(stdout.String(), tc.want) || stderr.Len() != 0 {
					t.Fatalf("stdout = %q; stderr = %q", stdout.String(), stderr.String())
				}
			} else if (stdout.Len() != 0 && !(tc.name == "unimplemented launcher" && strings.Contains(stdout.String(), "PARTIAL"))) || !strings.HasPrefix(stderr.String(), "agentflow: error:") {
				t.Fatalf("stdout = %q; stderr = %q", stdout.String(), stderr.String())
			}
		})
	}
	for _, scenario := range []struct {
		name, diagnostic string
		mode             os.FileMode
	}{
		{"missing", "EXECUTABLE_MISSING", 0},
		{"denied", "PERMISSION_DENIED", 0600},
		{"unavailable", "COMMAND_FAILED", 0700},
	} {
		t.Run("probe-"+scenario.name, func(t *testing.T) {
			dir := t.TempDir()
			if scenario.mode != 0 {
				if err := os.WriteFile(filepath.Join(dir, "opencode"), []byte("#!/bin/sh\nexit 23\n"), scenario.mode); err != nil {
					t.Fatal(err)
				}
			}
			cmd := exec.Command(binary, "probe", "--tool", "opencode", "--evidence-dir", "evidence")
			cmd.Dir = dir
			for _, entry := range os.Environ() {
				if !strings.HasPrefix(entry, "PATH=") {
					cmd.Env = append(cmd.Env, entry)
				}
			}
			cmd.Env = append(cmd.Env, "PATH="+dir)
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			err := cmd.Run()
			var exitErr *exec.ExitError
			if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
				t.Fatalf("expected exit 1: %v stderr=%s", err, stderr.String())
			}
			var report struct {
				Status      string `json:"status"`
				Diagnostics []struct {
					Code string `json:"code"`
				} `json:"diagnostics"`
			}
			if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
				t.Fatal(err)
			}
			found := false
			for _, d := range report.Diagnostics {
				if d.Code == scenario.diagnostic {
					found = true
				}
			}
			if report.Status != "WAITING_DEPENDENCY" || !found {
				t.Fatalf("incorrect structured diagnosis: %s", stdout.String())
			}
			t.Logf("actual CLI exit=1, diagnostic=%s; fixture-only, no native tool communication", scenario.diagnostic)
		})
	}
}
