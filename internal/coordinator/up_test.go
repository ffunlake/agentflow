package coordinator

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentflow/internal/config"
	"agentflow/internal/ipc"
	"agentflow/internal/lock"
	"agentflow/internal/runtime"
	"agentflow/internal/store"
)

type upLauncher struct {
	calls  int
	launch func(context.Context, runtime.LaunchRequest) (runtime.NativeBinding, error)
	ready  func(context.Context, runtime.SeatSnapshot) (runtime.Registration, error)
}

func (l *upLauncher) Launch(ctx context.Context, r runtime.LaunchRequest) (runtime.NativeBinding, error) {
	l.calls++
	return l.launch(ctx, r)
}
func (l *upLauncher) WaitReady(ctx context.Context, s runtime.SeatSnapshot) (runtime.Registration, error) {
	return l.ready(ctx, s)
}

func upFixture(t *testing.T) (*runtime.StartupPlan, UpOptions, *upLauncher) {
	t.Helper()
	dir, err := os.MkdirTemp("", "af-up-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	dir, err = filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	tool, prompt, cwd, permissions := "opencode", "Acknowledge startup only.", dir, "default"
	c := &config.Config{Version: 1, Name: "up-test", Terminal: "cmux", Lead: "solo", SourcePath: filepath.Join(dir, "team.json"), ContextDir: filepath.Join(dir, "context"), Agents: map[string]config.Agent{"solo": {Role: "Developer", Tool: &tool, Prompt: &prompt, Cwd: &cwd, Permissions: &permissions}}, Layout: &config.Layout{Agent: "solo"}}
	resolved, err := config.Resolve(c, config.ResolveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := runtime.Compile(resolved, runtime.Options{WrapperBinary: "/unused/agentflow", SnapshotPath: filepath.Join(dir, "config.resolved.json")})
	if err != nil {
		t.Fatal(err)
	}
	o := UpOptions{RunID: "up-run", RunDir: dir, Native: runtime.NativeOptions{EvidencePath: filepath.Join(dir, "native.jsonl")}}
	l := &upLauncher{}
	l.launch = func(ctx context.Context, r runtime.LaunchRequest) (runtime.NativeBinding, error) {
		if _, err := lock.Inspect(filepath.Join(dir, "coordinator.lock")); err != nil {
			t.Fatal("launch without lock", err)
		}
		if _, err := os.Stat(filepath.Join(dir, "context", o.RunID, "solo", "initial", "manifest.json")); err != nil {
			t.Fatal("launch without context", err)
		}
		return runtime.NativeBinding{SessionID: "native", WorkspaceID: "workspace", SurfaceID: "surface"}, nil
	}
	l.ready = func(ctx context.Context, s runtime.SeatSnapshot) (runtime.Registration, error) {
		return runtime.Registration{Actor: ipc.Actor{RunID: s.RunID, AgentID: s.AgentID, Generation: s.Generation}, Tool: s.Tool, Status: runtime.Ready, Native: s.Native, Nonce: strings.Repeat("a", 48)}, nil
	}
	return plan, o, l
}

func TestUpOwnsLockBeforeInitializationAndNeverReplays(t *testing.T) {
	plan, o, l := upFixture(t)
	held, err := lock.Acquire(filepath.Join(o.RunDir, "coordinator.lock"), lock.Owner{RunID: o.RunID})
	if err != nil {
		t.Fatal(err)
	}
	_, r, err := up(t.Context(), plan, o, l)
	if err == nil || r.Diagnostics[0].Code != "ALREADY_RUNNING" || l.calls != 0 {
		t.Fatal(r, err, l.calls)
	}
	for _, name := range []string{"up.requested.json", "state.db", "config.resolved.json", "context"} {
		if _, err := os.Stat(filepath.Join(o.RunDir, name)); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("initialized without lock", name, err)
		}
	}
	if err := held.Release(); err != nil {
		t.Fatal(err)
	}
	c, r, err := up(t.Context(), plan, o, l)
	if err != nil {
		t.Fatal(r, err)
	}
	t.Cleanup(func() { _ = c.Close() })
	if !r.CoordinatorReady || r.Status != "READY" || r.ReadyProof == nil || r.ReadyProof.Nonce != strings.Repeat("a", 48) || l.calls != 1 {
		t.Fatalf("%+v calls=%d", r, l.calls)
	}
	if _, _, err := up(t.Context(), plan, o, l); !errors.Is(err, lock.ErrAlreadyRunning) {
		t.Fatal("active up not exclusive", err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(r.SnapshotPath)
	if err != nil {
		t.Fatal(err)
	}
	_, again, err := up(t.Context(), plan, o, l)
	if err == nil || again.Diagnostics[0].Code != "UP_ALREADY_ATTEMPTED" || l.calls != 1 {
		t.Fatal(again, err, l.calls)
	}
	after, err := os.ReadFile(r.SnapshotPath)
	if err != nil || string(before) != string(after) {
		t.Fatal("snapshot overwritten", err)
	}
	db, err := store.OpenExisting(filepath.Join(o.RunDir, "state.db"), false)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	events, err := db.JournalAfter(t.Context(), o.RunID, 0)
	if err != nil {
		t.Fatal(err)
	}
	proofs := 0
	for _, e := range events {
		if e.Event == store.StartupReadyVerified {
			var proof store.StartupReadyProof
			if json.Unmarshal([]byte(e.Details), &proof) != nil || proof.Nonce != r.ReadyProof.Nonce || proof.Seat.NativeSessionID != r.Seat.Native.SessionID {
				t.Fatal(e)
			}
			proofs++
		}
	}
	if proofs != 1 {
		t.Fatal("missing/duplicate proof", events)
	}
}

func TestUpFailuresAndCancellationPreserveAttempt(t *testing.T) {
	for _, mode := range []string{"launch", "ready", "nonce", "generation", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			plan, o, l := upFixture(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			switch mode {
			case "launch":
				l.launch = func(context.Context, runtime.LaunchRequest) (runtime.NativeBinding, error) {
					return runtime.NativeBinding{}, errors.New("failure")
				}
			case "ready":
				l.ready = func(context.Context, runtime.SeatSnapshot) (runtime.Registration, error) {
					return runtime.Registration{}, errors.New("no READY")
				}
			case "nonce":
				original := l.ready
				l.ready = func(ctx context.Context, s runtime.SeatSnapshot) (runtime.Registration, error) {
					r, e := original(ctx, s)
					r.Nonce = ""
					return r, e
				}
			case "generation":
				original := l.ready
				l.ready = func(ctx context.Context, s runtime.SeatSnapshot) (runtime.Registration, error) {
					r, e := original(ctx, s)
					r.Actor.Generation++
					return r, e
				}
			case "cancel":
				l.launch = func(context.Context, runtime.LaunchRequest) (runtime.NativeBinding, error) {
					cancel()
					return runtime.NativeBinding{}, ctx.Err()
				}
			}
			c, r, err := up(ctx, plan, o, l)
			if err == nil || c != nil || r.Status == "READY" || r.CoordinatorReady {
				t.Fatal(r, err)
			}
			if _, err := lock.Inspect(filepath.Join(o.RunDir, "coordinator.lock")); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("leaked lock", err)
			}
			_, again, err := up(t.Context(), plan, o, l)
			if err == nil || again.Diagnostics[0].Code != "UP_ALREADY_ATTEMPTED" || l.calls != 1 {
				t.Fatal(again, err, l.calls)
			}
			db, err := store.OpenExisting(filepath.Join(o.RunDir, "state.db"), false)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if _, err := db.GetSeat(t.Context(), o.RunID, "solo", 1); err == nil {
				t.Fatal("failed up registered seat")
			}
			events, err := db.JournalAfter(t.Context(), o.RunID, 0)
			if err != nil {
				t.Fatal(err)
			}
			for _, e := range events {
				if e.Event == store.StartupReadyVerified {
					t.Fatal("failed up persisted proof", e)
				}
			}
		})
	}
}

func TestUpIPCFailureRetainsVerifiedREADYWithoutReplay(t *testing.T) {
	plan, o, l := upFixture(t)
	original := l.ready
	l.ready = func(ctx context.Context, s runtime.SeatSnapshot) (runtime.Registration, error) {
		if err := os.WriteFile(filepath.Join(o.RunDir, "ipc.sock"), []byte("occupied"), 0600); err != nil {
			t.Fatal(err)
		}
		return original(ctx, s)
	}
	c, r, err := up(t.Context(), plan, o, l)
	if err == nil || c != nil || r.CoordinatorReady || r.ReadyProof == nil || r.Seat.Status != "READY" || r.RuntimeCommunication != "STARTUP_READY_VERIFIED" || r.NativeProcess != "EXITED_AFTER_HANDSHAKE" || r.Diagnostics[0].Code != "UP_IPC_FAILED" {
		t.Fatal(r, err)
	}
	if body, err := os.ReadFile(filepath.Join(o.RunDir, "ipc.sock")); err != nil || string(body) != "occupied" {
		t.Fatal(string(body), err)
	}
	_, again, err := up(t.Context(), plan, o, l)
	if err == nil || again.Diagnostics[0].Code != "UP_ALREADY_ATTEMPTED" || l.calls != 1 {
		t.Fatal(again, err, l.calls)
	}
}
