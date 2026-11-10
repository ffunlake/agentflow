package cli

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

func TestTeamOutputFailure(t *testing.T) {
	file, err := filepath.Abs("../../examples/team.yaml")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", t.TempDir())
	for _, args := range [][]string{
		{"validate", "-f", file}, {"plan", "-f", file}, {"plan", "-f", file, "--json"},
		{"doctor", "-f", file}, {"doctor", "-f", file, "--json"}, {"plan", "--help"},
		{"doctor", "-f", "missing.yaml", "--json"},
	} {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			var stderr bytes.Buffer
			if code := Run(args, brokenWriter{}, &stderr, "test"); code != 1 || !strings.Contains(stderr.String(), "write output: output unavailable") {
				t.Fatalf("expected output failure: exit=%d stderr=%s", code, stderr.String())
			}
		})
	}
}

func TestPromptSummary(t *testing.T) {
	if got := promptSummary("  first\n second\tthird "); got != "first second third" {
		t.Fatalf("summary = %q", got)
	}
	if got := promptSummary(strings.Repeat("长", 121)); got != strings.Repeat("长", 120)+"…" {
		t.Fatalf("summary truncation = %q", got)
	}
}
