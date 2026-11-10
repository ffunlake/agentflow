package cli

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func setSystemTemp(t *testing.T, dir string) {
	t.Helper()
	// Go selects TMPDIR on Unix and TMP/TEMP on Windows. Set all three so
	// these tests exercise the host's real os.TempDir implementation.
	for _, key := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(key, dir)
	}
}

// Kept independent of runtime/coordinator so these path semantics can also be
// compiled or tested on platforms not yet supported by those packages:
// go test up_directory.go up_directory_path_test.go
func TestUpRunDirectorySystemTemp(t *testing.T) {
	for _, name := range []string{"temp space 中文", `C:\Users\fixture\AppData\Local\Temp`, "symlink-root"} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			parent := filepath.Join(root, name)
			if runtime.GOOS == "windows" && strings.HasPrefix(name, `C:\`) {
				// t.TempDir is drive-qualified, with real backslash separators.
				parent = filepath.Join(root, "AppData", "Local", "Temp")
			}
			if name == "symlink-root" {
				if err := os.Symlink(root, parent); err != nil {
					t.Skipf("symlinks unavailable: %v", err)
				}
			} else if err := os.MkdirAll(parent, 0700); err != nil {
				t.Fatal(err)
			}
			setSystemTemp(t, parent)
			t.Setenv("XDG_STATE_HOME", filepath.Join(root, "unused-state"))
			wantParent, err := filepath.EvalSymlinks(os.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			previous := ""
			for i := range 2 {
				dir, created, err := createUpRunDir("", "safe-Run_1234"+strings.Repeat("x", 80))
				if err != nil {
					t.Fatal(err)
				}
				if created {
					t.Cleanup(func() { _ = os.Remove(dir) })
				}
				if !filepath.IsAbs(dir) || filepath.Dir(dir) != wantParent || !strings.HasPrefix(filepath.Base(dir), "agentflow-safe-Run_123-") || (i == 0 && !created) || (i == 1 && created) || (i == 1 && dir != previous) {
					t.Fatalf("unexpected allocation %q, parent %q, previous %q", dir, wantParent, previous)
				}
				info, err := os.Stat(dir)
				if err != nil || !info.IsDir() {
					t.Fatal(info, err)
				}
				if runtime.GOOS != "windows" && info.Mode().Perm() != 0700 {
					t.Fatalf("run directory permissions: %v", info.Mode())
				}
				previous = dir
			}
		})
	}
}

func TestUpRunDirectoryExplicitUnchanged(t *testing.T) {
	setSystemTemp(t, filepath.Join(t.TempDir(), "missing-temp"))
	for _, dir := range []string{filepath.Join(t.TempDir(), "not-created"), filepath.Join("relative", "run")} {
		got, created, err := createUpRunDir(dir, "safe")
		want, _ := filepath.Abs(dir)
		if err != nil || created || got != want {
			t.Fatal(got, want, err)
		}
		if _, err := os.Stat(got); !os.IsNotExist(err) {
			t.Fatal("explicit directory was created during resolution", err)
		}
	}
}
