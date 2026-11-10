package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"agentflow/internal/coordinator"
)

func TestRealUpTemporaryDirectoryFailureBoundaries(t *testing.T) {
	parent := t.TempDir()
	setSystemTemp(t, parent)
	t.Setenv("PATH", parent) // No real tools may be discovered or launched.
	invoke := func(extra ...string) coordinator.UpResult {
		t.Helper()
		args := append([]string{"up", "-f", "../../examples/headless-codex.yaml", "--run", "temp-failure", "--headless-codex", "--json"}, extra...)
		var out, diagnostic bytes.Buffer
		if code := Run(args, &out, &diagnostic, "test"); code != 1 {
			t.Fatalf("exit %d: %s %s", code, &out, &diagnostic)
		}
		var r coordinator.UpResult
		if err := json.Unmarshal(out.Bytes(), &r); err != nil {
			t.Fatal(err, &out, &diagnostic)
		}
		return r
	}
	// Executable lookup fails before runtime ownership; its empty allocation
	// must be removed. Explicit directories must never be removed in this path.
	r := invoke()
	if r.RunDir == "" || r.Diagnostics[0].Code != "UP_NATIVE_UNAVAILABLE" {
		t.Fatal(r)
	}
	if _, err := os.Stat(r.RunDir); !os.IsNotExist(err) {
		t.Fatal("unused allocation retained", err)
	}
	explicit := t.TempDir()
	invoke("--run-dir", explicit)
	if _, err := os.Stat(explicit); err != nil {
		t.Fatal("explicit directory removed", err)
	}
	// Handing off to runtime consumes ownership even on early validation
	// failure; keep its directory available for inspection, never RemoveAll.
	r = invoke("--native-binary", "relative-invalid-executable")
	if r.RunDir == "" || r.Diagnostics[0].Code != "HEADLESS_OPTIONS_INVALID" {
		t.Fatal(r)
	}
	if _, err := os.Stat(r.RunDir); err != nil {
		t.Fatal("runtime allocation removed", err)
	}
	// Missing temp parent is reported structurally with no fallback to HOME,
	// XDG or a hard-coded temporary root.
	setSystemTemp(t, filepath.Join(parent, "missing"))
	r = invoke()
	if r.RunDir != "" || r.Diagnostics[0].Code != "UP_DIRECTORY_INVALID" {
		t.Fatal(r)
	}
}

func TestExistingRunCommandsNeverAllocateTemporaryDirectory(t *testing.T) {
	parent := t.TempDir()
	setSystemTemp(t, parent)
	t.Setenv("XDG_STATE_HOME", filepath.Join(parent, "legacy-state"))
	for _, args := range [][]string{
		{"coordinator", "--run", "absent", "--json"},
		{"status", "--run", "absent", "--json"},
		{"workflow", "status", "--run", "absent", "--agent", "main", "--generation", "1", "--request-id", "query", "--json"},
	} {
		var out, diagnostic bytes.Buffer
		if code := Run(args, &out, &diagnostic, "test"); code != 1 {
			t.Fatalf("%q: exit %d: %s %s", args, code, &out, &diagnostic)
		}
		entries, err := os.ReadDir(parent)
		if err != nil || len(entries) != 0 {
			t.Fatal("existing-run command allocated state", entries, err)
		}
	}
	for _, id := range []string{"", "../escape", `..\escape`, "safe/escape", "safe*escape"} {
		if _, _, err := createUpRunDir("", id); err == nil {
			t.Fatalf("accepted unsafe run ID %q", id)
		}
	}
}
