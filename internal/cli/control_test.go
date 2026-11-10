package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentflow/internal/config"
	"agentflow/internal/lock"
	"agentflow/internal/runtime"
	"agentflow/internal/store"
)

func controlFixture(t *testing.T, seed bool) (string, *store.Store) {
	t.Helper()
	dir, err := os.MkdirTemp("", "af-cli-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	if seed {
		c, err := config.Load("../../examples/team.yaml")
		if err != nil {
			t.Fatal(err)
		}
		resolved, err := config.Resolve(c, config.ResolveOptions{})
		if err != nil {
			t.Fatal(err)
		}
		plan, err := runtime.Compile(resolved, runtime.Options{})
		if err != nil {
			t.Fatal(err)
		}
		snapshot, err := runtime.NewSnapshot(plan, "run-test")
		if err != nil {
			t.Fatal(err)
		}
		if err := runtime.WriteSnapshot(filepath.Join(dir, "config.resolved.json"), snapshot); err != nil {
			t.Fatal(err)
		}
		if _, err := runtime.NewRegistry(t.Context(), db, snapshot); err != nil {
			t.Fatal(err)
		}
	}
	return dir, db
}

func controlRun(t *testing.T, command, dir string, code int, jsonOutput bool) (string, string) {
	t.Helper()
	args := []string{command, "--run", "run-test", "--run-dir", dir}
	if command == "stop" {
		args = append(args, "--agent", "builder", "--generation", "2", "--request-id", "stop")
	}
	if jsonOutput {
		args = append(args, "--json")
	}
	var stdout, stderr bytes.Buffer
	if got := Run(args, &stdout, &stderr, "test"); got != code {
		t.Fatalf("exit=%d want=%d stdout=%s stderr=%s", got, code, &stdout, &stderr)
	}
	if jsonOutput && !json.Valid(stdout.Bytes()) {
		t.Fatalf("invalid JSON %s", &stdout)
	}
	return stdout.String(), stderr.String()
}

func TestControlCLIEmptyAndAbsentStores(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	for _, empty := range []bool{false, true} {
		dir := t.TempDir()
		if empty {
			dir, _ = controlFixture(t, false)
		}
		for _, command := range []string{"status", "attach", "stop"} {
			_, diagnostic := controlRun(t, command, dir, 1, true)
			want := "STORE_UNAVAILABLE: store.path"
			if empty {
				want = "RUN_NOT_FOUND: run.run_id"
			}
			if command == "stop" {
				want = "COORDINATOR_UNAVAILABLE"
			}
			if !strings.Contains(diagnostic, want) {
				t.Fatal(diagnostic)
			}
		}
		if !empty {
			entries, _ := os.ReadDir(dir)
			if len(entries) != 0 {
				t.Fatal("absent store query created files")
			}
		}
	}
}

func TestControlCLIStatusAttachStop(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	dir, db := controlFixture(t, true)
	first, _ := controlRun(t, "status", dir, 0, true)
	var r runtime.StatusResult
	if err := json.Unmarshal([]byte(first), &r); err != nil {
		t.Fatal(err)
	}
	if r.Run.StartupPhase != "STARTING" || r.Run.StartupProgress != "STARTING" || len(r.Seats) != 3 || r.Lock.Status != "MISSING" {
		t.Fatalf("%+v", r)
	}
	attach, diagnostic := controlRun(t, "attach", dir, 1, true)
	if !strings.Contains(attach, `"attached": false`) || !strings.Contains(diagnostic, "WORKSPACE_UNVERIFIED: run.workspace_id") {
		t.Fatal(attach, diagnostic)
	}
	if err := db.CreateSeat(t.Context(), store.Seat{RunID: "run-test", AgentID: "builder", Generation: 2, Current: true, Tool: "opencode", NativeSessionID: "native-2", WorkspaceID: "recorded-workspace", SurfaceID: "recorded-surface", Status: "READY"}); err != nil {
		t.Fatal(err)
	}
	l, err := lock.Acquire(filepath.Join(dir, "coordinator.lock"), lock.Owner{RunID: "run-test", SocketPath: filepath.Join(dir, "missing.sock")})
	if err != nil {
		t.Fatal(err)
	}
	defer l.Release()
	status, _ := controlRun(t, "status", dir, 0, true)
	if err := json.Unmarshal([]byte(status), &r); err != nil {
		t.Fatal(err)
	}
	if r.Seats[0].Generation != 2 || r.Seats[0].Native.SessionID != "native-2" || r.Run.StartupProgress != "PARTIAL" || r.Lock.Status != "PRESENT" || r.Lock.Socket.Status != lock.SocketMissing {
		t.Fatalf("%+v", r)
	}
	if strings.Contains(status, `"token"`) {
		t.Fatal("lock token exposed")
	}
	attach, diagnostic = controlRun(t, "attach", dir, 1, true)
	if !strings.Contains(attach, "recorded-workspace") || strings.Contains(diagnostic, "WORKSPACE_UNVERIFIED") || !strings.Contains(diagnostic, "ATTACH_UNVERIFIED") {
		t.Fatal(attach, diagnostic)
	}
	before, _ := os.ReadFile(filepath.Join(dir, "config.resolved.json"))
	if err := l.Release(); err != nil {
		t.Fatal(err)
	}
	startTestCoordinator(t, dir)
	stop1, _ := controlRun(t, "stop", dir, 0, true)
	stop2, _ := controlRun(t, "stop", dir, 0, true)
	if stop1 != stop2 || !strings.Contains(stop1, `"executed": false`) {
		t.Fatal(stop1, stop2)
	}
	controlRun(t, "stop", dir, 0, false)
	text, _ := controlRun(t, "status", dir, 0, false)
	if !strings.Contains(text, "Stop requested: true") || !strings.Contains(text, "generation=2") {
		t.Fatal(text)
	}
	events, err := db.JournalAfter(t.Context(), "run-test", 0)
	if err != nil || len(events) != 2 || events[0].Event != store.StopRequested || events[1].Event != "STOP_REQUEST_ACCEPTED" {
		t.Fatalf("%+v %v", events, err)
	}
	after, _ := os.ReadFile(filepath.Join(dir, "config.resolved.json"))
	if !bytes.Equal(before, after) {
		t.Fatal("control modified snapshot")
	}
	if _, err := lock.Inspect(filepath.Join(dir, "coordinator.lock")); err != nil {
		t.Fatal(err)
	}
}

func TestControlCLIArtifactBoundaries(t *testing.T) {
	for _, mode := range []string{"missing-snapshot", "invalid-snapshot", "wrong-snapshot-run", "invalid-lock", "wrong-lock-run"} {
		t.Run(mode, func(t *testing.T) {
			dir, _ := controlFixture(t, true)
			path := filepath.Join(dir, "config.resolved.json")
			code := ""
			switch mode {
			case "missing-snapshot":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				code = "SNAPSHOT_MISSING: snapshot.path"
			case "invalid-snapshot":
				if err := os.WriteFile(path, []byte("broken"), 0600); err != nil {
					t.Fatal(err)
				}
				code = "SNAPSHOT_INVALID: snapshot.path"
			case "wrong-snapshot-run":
				body, _ := os.ReadFile(path)
				if err := os.WriteFile(path, bytes.ReplaceAll(body, []byte("run-test"), []byte("other-run")), 0600); err != nil {
					t.Fatal(err)
				}
				code = "SNAPSHOT_MISMATCH: snapshot.path"
			case "invalid-lock":
				if err := os.WriteFile(filepath.Join(dir, "coordinator.lock"), []byte("broken"), 0600); err != nil {
					t.Fatal(err)
				}
				code = "LOCK_INVALID: lock.path"
			case "wrong-lock-run":
				l, err := lock.Acquire(filepath.Join(dir, "coordinator.lock"), lock.Owner{RunID: "other-run", SocketPath: "untrusted.sock"})
				if err != nil {
					t.Fatal(err)
				}
				defer l.Release()
				code = "LOCK_RUN_MISMATCH: lock.run_id"
			}
			out, diagnostic := controlRun(t, "status", dir, 1, true)
			if !strings.Contains(diagnostic, code) || !strings.Contains(out, `"agent_id": "builder"`) {
				t.Fatal(out, diagnostic)
			}
			if mode == "invalid-lock" || mode == "wrong-lock-run" {
				controlRun(t, "stop", dir, 1, true) // A bad lock cannot authorize an IPC route.
			} else {
				db, err := store.OpenExisting(filepath.Join(dir, "state.db"), true)
				if err != nil {
					t.Fatal(err)
				}
				if err := db.CreateSeat(t.Context(), store.Seat{RunID: "run-test", AgentID: "builder", Generation: 2, Current: true, Status: "READY"}); err != nil {
					t.Fatal(err)
				}
				db.Close()
				startTestCoordinator(t, dir)
				controlRun(t, "stop", dir, 0, true) // Snapshot health does not block intent.
			}
		})
	}
}

func TestControlCLIUsageAndDefaultDirectory(t *testing.T) {
	for _, command := range []string{"status", "attach", "stop"} {
		for _, args := range [][]string{{}, {"--run", "../oops"}, {"--run", "r", "extra"}, {"--unknown"}, {"--run"}, {"--run", ".."}} {
			var out, diagnostic bytes.Buffer
			if code := Run(append([]string{command}, args...), &out, &diagnostic, "test"); code != 2 || out.Len() != 0 {
				t.Fatalf("%s %v: %d %s %s", command, args, code, &out, &diagnostic)
			}
		}
		var out, diagnostic bytes.Buffer
		if code := Run([]string{command, "--help"}, &out, &diagnostic, "test"); code != 0 || !strings.Contains(out.String(), "Usage:") {
			t.Fatal(code, &out, &diagnostic)
		}
	}
	root := t.TempDir()
	t.Setenv("XDG_STATE_HOME", root)
	var out, diagnostic bytes.Buffer
	if code := Run([]string{"status", "--run", "absent", "--json"}, &out, &diagnostic, "test"); code != 1 {
		t.Fatal(code)
	}
	if _, err := os.Stat(filepath.Join(root, "agentflow")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("query created state directories")
	}
}

func TestControlCLIRunIdentityMismatch(t *testing.T) {
	dir, db := controlFixture(t, true)
	for _, command := range []string{"status", "attach", "stop"} {
		var out, diagnostic bytes.Buffer
		code := Run([]string{command, "--run", "another-run", "--run-dir", dir, "--json"}, &out, &diagnostic, "test")
		want := "RUN_MISMATCH: run.run_id"
		if command == "stop" {
			out.Reset()
			diagnostic.Reset()
			code = Run([]string{command, "--run", "another-run", "--run-dir", dir, "--agent", "builder", "--generation", "1", "--request-id", "stop", "--json"}, &out, &diagnostic, "test")
			want = "COORDINATOR_UNAVAILABLE"
		}
		if code != 1 || !strings.Contains(diagnostic.String(), want) || !json.Valid(out.Bytes()) {
			t.Fatal(code, &out, &diagnostic)
		}
	}
	run, err := db.GetRun(t.Context())
	if err != nil || run.StopRequested {
		t.Fatal(run, err)
	}
}
