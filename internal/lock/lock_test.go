package lock

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func acquire(t *testing.T, path string) *Lock {
	t.Helper()
	l, err := Acquire(path, Owner{RunID: "run", SocketPath: "coordinator.sock"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := l.Release(); err != nil {
			t.Error(err)
		}
	})
	return l
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func writeOwner(t *testing.T, path string, owner Owner) {
	t.Helper()
	data, err := json.Marshal(owner)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func requireConflict(t *testing.T, path string) *AlreadyRunningError {
	t.Helper()
	l, err := Acquire(path, Owner{RunID: "second"})
	if l != nil || !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("Acquire = %v, %v; want ALREADY_RUNNING", l, err)
	}
	var conflict *AlreadyRunningError
	if !errors.As(err, &conflict) || err.Error() != "ALREADY_RUNNING" {
		t.Fatalf("conflict = %v", err)
	}
	return conflict
}

func TestOwnerRoundTripAndSequentialCompetition(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "run.lock")
	input := Owner{
		PID: -1, Token: "caller-token", StartedAt: time.Unix(1, 0),
		RunID: "run-中文", SocketPath: filepath.Join(dir, "s"),
		Command:  []string{"agentflow", "up", "-f", "team with spaces.yaml"},
		Metadata: map[string]string{"version": "dev", "role": "coordinator"},
	}
	before := time.Now().UTC()
	l, err := Acquire(path, input)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Release()
	owner, err := Inspect(path)
	if err != nil {
		t.Fatal(err)
	}
	token, err := hex.DecodeString(owner.Token)
	if err != nil || len(token) != 32 || owner.Token == input.Token {
		t.Fatalf("token = %q, err = %v", owner.Token, err)
	}
	if owner.PID != os.Getpid() || owner.StartedAt.Location() != time.UTC || owner.StartedAt.Before(before) || owner.StartedAt.After(time.Now()) {
		t.Fatalf("generated owner identity = %+v", owner)
	}
	if owner.RunID != input.RunID || owner.SocketPath != input.SocketPath || !reflect.DeepEqual(owner.Command, input.Command) || !reflect.DeepEqual(owner.Metadata, input.Metadata) {
		t.Fatalf("owner metadata = %+v", owner)
	}
	if !bytes.Contains(readFile(t, path), []byte(`"startedAt":"`)) || !bytes.Contains(readFile(t, path), []byte(`Z"`)) {
		t.Fatal("startedAt is not an RFC3339 UTC timestamp")
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 || !info.Mode().IsRegular() {
		t.Fatalf("file info = %v, err = %v", info, err)
	}
	conflict := requireConflict(t, path)
	if conflict.InspectErr != nil || conflict.Owner == nil || !reflect.DeepEqual(*conflict.Owner, owner) {
		t.Fatalf("conflict = %+v", conflict)
	}
	if err := l.Release(); err != nil {
		t.Fatal(err)
	}
	next := acquire(t, path)
	nextOwner, err := Inspect(path)
	if err != nil || nextOwner.Token == owner.Token {
		t.Fatalf("next owner = %+v, err = %v", nextOwner, err)
	}
	if err := l.Release(); err != nil { // must not remove the new acquisition
		t.Fatal(err)
	}
	if got, err := Inspect(path); err != nil || got.Token != next.token {
		t.Fatalf("repeat release changed next owner: %+v, %v", got, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 || entries[0].Name() != "run.lock" {
		t.Fatalf("temporary owner files leaked: %v, %v", entries, err)
	}
}

func TestConcurrentAcquireAndRelease(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run.lock")
	const contenders = 32
	start := make(chan struct{})
	type result struct {
		lock *Lock
		err  error
	}
	results := make(chan result, contenders)
	for range contenders {
		go func() {
			<-start
			l, err := Acquire(path, Owner{RunID: "run"})
			results <- result{l, err}
		}()
	}
	close(start)
	var winner *Lock
	for range contenders {
		got := <-results
		if got.err == nil {
			if winner != nil || got.lock == nil {
				t.Fatal("multiple acquisitions succeeded")
			}
			winner = got.lock
			continue
		}
		var conflict *AlreadyRunningError
		if got.lock != nil || !errors.Is(got.err, ErrAlreadyRunning) || !errors.As(got.err, &conflict) || conflict.Owner == nil || conflict.InspectErr != nil {
			t.Errorf("contender result = %+v", got)
		}
	}
	if winner == nil {
		t.Fatal("no acquisition succeeded")
	}
	var releases sync.WaitGroup
	for range contenders {
		releases.Go(func() {
			if err := winner.Release(); err != nil {
				t.Error(err)
			}
		})
	}
	releases.Wait()
	if _, err := Inspect(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("released lock still exists: %v", err)
	}
	acquire(t, path)
}

func TestExistingPathsAreNeverRemoved(t *testing.T) {
	for _, kind := range []string{"malformed", "empty", "directory", "symlink", "stale"} {
		t.Run(kind, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "run.lock")
			switch kind {
			case "directory":
				if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Symlink("missing-target", path); err != nil {
					t.Fatal(err)
				}
			case "stale":
				writeOwner(t, path, Owner{PID: 2147483647, Token: "stale", StartedAt: time.Unix(1, 0).UTC(), RunID: "old"})
			default:
				data := []byte("{")
				if kind == "empty" {
					data = nil
				}
				if err := os.WriteFile(path, data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			before, err := os.Lstat(path)
			if err != nil {
				t.Fatal(err)
			}
			var contents []byte
			if before.Mode().IsRegular() {
				contents = readFile(t, path)
			}
			conflict := requireConflict(t, path)
			if kind == "stale" {
				if conflict.Owner == nil || conflict.Owner.Token != "stale" || conflict.InspectErr != nil {
					t.Fatalf("stale conflict = %+v", conflict)
				}
			} else if conflict.Owner != nil || conflict.InspectErr == nil {
				t.Fatalf("invalid owner conflict = %+v", conflict)
			}
			after, err := os.Lstat(path)
			if err != nil || !os.SameFile(before, after) {
				t.Fatalf("existing path replaced: %v", err)
			}
			if before.Mode().IsRegular() && !bytes.Equal(contents, readFile(t, path)) {
				t.Fatal("existing lock modified")
			}
		})
	}
}

func TestReleaseRequiresTokenAndFileIdentity(t *testing.T) {
	for _, change := range []string{"token", "inode", "malformed", "missing"} {
		t.Run(change, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "run.lock")
			l, err := Acquire(path, Owner{RunID: "run"})
			if err != nil {
				t.Fatal(err)
			}
			owner, err := Inspect(path)
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "token":
				owner.Token = "someone-else"
				writeOwner(t, path, owner)
			case "inode":
				// Keep the original inode allocated to avoid inode reuse.
				if err := os.Rename(path, path+".original"); err != nil {
					t.Fatal(err)
				}
				writeOwner(t, path, owner)
			case "malformed":
				if err := os.WriteFile(path, []byte("{"), 0600); err != nil {
					t.Fatal(err)
				}
			case "missing":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			}
			if change == "missing" {
				if err := l.Release(); err != nil {
					t.Fatal(err)
				}
				acquire(t, path)
				if err := l.Release(); err != nil {
					t.Fatal(err)
				}
				if _, err := Inspect(path); err != nil {
					t.Fatalf("repeated release removed next lock: %v", err)
				}
				return
			}
			before := readFile(t, path)
			err = l.Release()
			if err == nil || (change != "malformed" && !errors.Is(err, ErrNotOwner)) {
				t.Fatalf("release %s = %v", change, err)
			}
			if !bytes.Equal(before, readFile(t, path)) {
				t.Fatal("failed release modified replacement lock")
			}
		})
	}
	if err := new(Lock).Release(); !errors.Is(err, ErrNotOwner) {
		t.Fatalf("zero lock release = %v", err)
	}
}

func TestAcquireAndInspectErrors(t *testing.T) {
	dir := t.TempDir()
	for _, path := range []string{"", filepath.Join(dir, "missing", "run.lock")} {
		if l, err := Acquire(path, Owner{RunID: "run"}); l != nil || err == nil {
			t.Fatalf("Acquire(%q) = %v, %v", path, l, err)
		}
	}
	path := filepath.Join(dir, "run.lock")
	if l, err := Acquire(path, Owner{}); l != nil || err == nil {
		t.Fatalf("empty run ID = %v, %v", l, err)
	}
	if l, err := Acquire(path, Owner{RunID: "run", Metadata: map[string]string{"large": strings.Repeat("x", maxOwnerSize)}}); l != nil || err == nil {
		t.Fatalf("large owner = %v, %v", l, err)
	}
	if _, err := Inspect(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed acquisition created lock: %v", err)
	}
	for _, data := range []string{"{}", "null", `{"pid":1}`, strings.Repeat("x", maxOwnerSize+1)} {
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := Inspect(path); err == nil {
			t.Fatalf("Inspect(%q) succeeded", data[:min(len(data), 32)])
		}
	}
}

func TestAcquireProcessHelper(t *testing.T) {
	path := os.Getenv("AGENTFLOW_LOCK_TEST_PATH")
	if path == "" {
		t.Skip("subprocess helper")
	}
	_, err := Acquire(path, Owner{RunID: "process-run"})
	if errors.Is(err, ErrAlreadyRunning) {
		os.Exit(23)
	}
	if err != nil {
		t.Fatal(err)
	}
	// Deliberately exit without Release to verify locks survive owner exit.
}

func TestAcquireAcrossProcessesAndExitedOwner(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run.lock")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	commands := make([]*exec.Cmd, 8)
	for i := range commands {
		commands[i] = exec.Command(executable, "-test.run=^TestAcquireProcessHelper$")
		commands[i].Env = append(os.Environ(), "AGENTFLOW_LOCK_TEST_PATH="+path)
		if err := commands[i].Start(); err != nil {
			t.Fatal(err)
		}
	}
	winners := 0
	for _, cmd := range commands {
		err := cmd.Wait()
		if err == nil {
			winners++
		} else {
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 23 {
				t.Fatalf("contender process failed: %v", err)
			}
		}
	}
	if winners != 1 {
		t.Fatalf("%d process winners, want 1", winners)
	}
	owner, err := Inspect(path)
	if err != nil {
		t.Fatal(err)
	}
	if probe := ProbePID(owner.PID); probe.Status != PIDDead {
		t.Fatalf("exited owner probe = %+v", probe)
	}
	conflict := requireConflict(t, path)
	if conflict.Owner == nil || conflict.Owner.Token != owner.Token {
		t.Fatalf("exited owner lock was replaced: %+v", conflict)
	}
}
