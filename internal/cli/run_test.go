package cli

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestRun(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		code int
		want string
	}{
		{"no arguments", nil, 0, "Usage:"},
		{"help", []string{"--help"}, 0, "Usage:"},
		{"short help", []string{"-h"}, 0, "Usage:"},
		{"version", []string{"--version"}, 0, "agentflow test-version\n"},
		{"unknown flag", []string{"--unknown"}, 2, ""},
		{"future command", []string{"resume", "--run", "example"}, 2, ""},
		{"extra argument", []string{"--help", "extra"}, 2, ""},
		{"conflicting flags", []string{"--help", "--version"}, 2, ""},
		{"probe unknown flag", []string{"probe", "--unknown"}, 2, ""},
		{"probe positional argument", []string{"probe", "extra"}, 2, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := Run(tc.args, &stdout, &stderr, "test-version"); code != tc.code {
				t.Fatalf("exit code = %d, want %d", code, tc.code)
			}
			if tc.code == 0 {
				if !strings.Contains(stdout.String(), tc.want) || stderr.Len() != 0 {
					t.Fatalf("stdout = %q, stderr = %q", stdout.String(), stderr.String())
				}
			} else if stdout.Len() != 0 || !strings.HasPrefix(stderr.String(), "agentflow: error:") {
				t.Fatalf("stdout = %q, stderr = %q", stdout.String(), stderr.String())
			}
		})
	}
}

type brokenWriter struct{}

func (brokenWriter) Write([]byte) (int, error) { return 0, errors.New("output unavailable") }

func TestRunOutputFailure(t *testing.T) {
	var stderr bytes.Buffer
	if code := Run([]string{"--version"}, brokenWriter{}, &stderr, "test"); code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "agentflow: error: write output: output unavailable") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}
