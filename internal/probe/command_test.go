package probe

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fixture(t *testing.T, dir, name, body string, mode os.FileMode) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), mode); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestActualCommandFailuresAndTimeout(t *testing.T) {
	dir := t.TempDir()
	fixture(t, dir, "failed", "printf 'unavailable\\n' >&2; exit 23", 0700)
	fixture(t, dir, "denied", "exit 0", 0600)
	fixture(t, dir, "slow", "exec /bin/sleep 5", 0700)
	t.Setenv("PATH", dir)
	e := localExecutor{cwd: dir, env: os.Environ()}
	failed := e.execute(t.Context(), "failed", "failed", []string{"--help"})
	if failed.Code != "COMMAND_FAILED" || failed.ExitCode == nil || *failed.ExitCode != 23 || failed.Stderr != "unavailable\n" {
		t.Fatalf("lost real failure: %+v", failed)
	}
	denied := e.execute(t.Context(), "denied", "denied", []string{"--version"})
	if denied.Code != "PERMISSION_DENIED" || denied.ExitCode != nil {
		t.Fatalf("incorrect permission diagnosis: %+v", denied)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	start := time.Now()
	slow := e.execute(ctx, "slow", "slow", []string{"--help"})
	if slow.Code != "COMMAND_TIMEOUT_OR_CANCELLED" || time.Since(start) > time.Second {
		t.Fatalf("unbounded timeout: %+v", slow)
	}
	var timeoutExit any
	if slow.ExitCode != nil {
		timeoutExit = *slow.ExitCode
	}
	t.Logf("fixture commands: failed exit=%d stderr=%q; denied exit=null code=%s; timeout exit=%v code=%s", *failed.ExitCode, failed.Stderr, denied.Code, timeoutExit, slow.Code)
}

func TestBypassSurfaceShimAndPreserveArgv(t *testing.T) {
	root := t.TempDir()
	shim := filepath.Join(root, "cmux-cli-shims", "other-surface")
	if err := os.MkdirAll(shim, 0700); err != nil {
		t.Fatal(err)
	}
	fixture(t, shim, "codex", "exit 88", 0700)
	real := fixture(t, root, "codex", "printf '%s\\n' \"$@\"", 0700)
	t.Setenv("PATH", shim+string(os.PathListSeparator)+root)
	e := localExecutor{cwd: root, env: os.Environ()}
	c := e.execute(t.Context(), "argv", "codex", []string{"--help", "$(touch forbidden); literal"})
	if c.Code != "OK" || c.Path != real || len(c.SkippedShims) != 1 || c.Stdout != "--help\n$(touch forbidden); literal\n" {
		t.Fatalf("shim or argv handling incorrect: %+v", c)
	}
	if _, err := os.Stat(filepath.Join(root, "forbidden")); !os.IsNotExist(err) {
		t.Fatal("shell interpolation occurred")
	}
}

func TestBoundedOutput(t *testing.T) {
	var b boundedBuffer
	data := strings.Repeat("x", outputLimit+10)
	n, err := b.Write([]byte(data))
	if err != nil || n != len(data) || b.Len() != outputLimit || !b.truncated {
		t.Fatalf("buffer did not bound output")
	}
	if _, err := b.Write([]byte("more")); err != nil || b.Len() != outputLimit {
		t.Fatal("overflow after limit")
	}
}
