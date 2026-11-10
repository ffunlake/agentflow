package runtime

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"agentflow/internal/config"
	"agentflow/internal/ipc"
	"agentflow/internal/store"
)

func startupFixture(t *testing.T) (*StartupPlan, *store.Store, StartOptions) {
	t.Helper()
	source, err := config.Load("../../examples/team.yaml")
	if err != nil {
		t.Fatal(err)
	}
	fresh, codex := "fresh", "codex"
	for _, id := range []string{"builder", "reviewer"} {
		agent := source.Agents[id]
		agent.Session = &fresh
		if id == "builder" {
			agent.Tool = &codex
		}
		source.Agents[id] = agent
	}
	resolved, err := config.Resolve(source, config.ResolveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := Compile(resolved, Options{WrapperBinary: "/private/bin with spaces/agentflow"})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
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
	return plan, db, StartOptions{RunID: "run-test", SnapshotPath: filepath.Join(dir, "config.resolved.json")}
}

type fakeLauncher struct {
	launch func(context.Context, LaunchRequest) (NativeBinding, error)
	wait   func(context.Context, SeatSnapshot) (Registration, error)
}

func (f fakeLauncher) Launch(ctx context.Context, request LaunchRequest) (NativeBinding, error) {
	return f.launch(ctx, request)
}

func (f fakeLauncher) WaitReady(ctx context.Context, seat SeatSnapshot) (Registration, error) {
	return f.wait(ctx, seat)
}

func fakeNative(id string) NativeBinding {
	return NativeBinding{SessionID: "fake-session-" + id, WorkspaceID: "fake-workspace", SurfaceID: "fake-surface-" + id}
}

func readyClaim(seat SeatSnapshot) Registration {
	return Registration{Actor: ipc.Actor{RunID: seat.RunID, AgentID: seat.AgentID, Generation: seat.Generation},
		Tool: seat.Tool, Native: seat.Native, Status: Ready}
}

func requireAbsentSeat(t *testing.T, db *store.Store, id string) {
	t.Helper()
	if _, err := db.GetSeat(t.Context(), "run-test", id, 1); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("seat %s persisted before READY: %v", id, err)
	}
}

func TestStartFakeLauncherStableOrderAndDurableRegistration(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	plan, db, options := startupFixture(t)
	before, _ := json.Marshal(plan)
	var events []string
	index := 0
	launcher := fakeLauncher{
		launch: func(ctx context.Context, request LaunchRequest) (NativeBinding, error) {
			seat := request.Seat
			expected := plan.Seats[index]
			if seat.AgentID != expected.ID || seat.RunID != options.RunID || seat.Generation != 1 ||
				seat.Cwd != expected.Cwd || seat.Tool != expected.Tool || seat.Role != expected.Role ||
				seat.Permissions != expected.Permissions || seat.Session != expected.Session || seat.Status != Starting ||
				!reflect.DeepEqual(request.PanePath, expected.PanePath) {
				t.Fatalf("launch lost order or metadata: %+v", request)
			}
			want := Command{Binary: expected.Wrapper.Binary, Args: []string{"_seat", "--snapshot", options.SnapshotPath, "--agent", seat.AgentID}}
			if !reflect.DeepEqual(request.Wrapper, want) {
				t.Fatalf("incorrect wrapper: %+v", request.Wrapper)
			}
			artifact, err := ReadSnapshot(options.SnapshotPath)
			if err != nil || artifact.Seats[index].AgentID != seat.AgentID || artifact.Config.Agents[seat.AgentID].Prompt == "" {
				t.Fatalf("snapshot unavailable before launch: %+v %v", artifact, err)
			}
			requireAbsentSeat(t, db, seat.AgentID)
			if index > 0 {
				previous, err := db.GetSeat(ctx, options.RunID, plan.Seats[index-1].ID, 1)
				if err != nil || previous.Status != Ready {
					t.Fatalf("next launch ran before previous registration: %+v %v", previous, err)
				}
			}
			events = append(events, "launch:"+seat.AgentID)
			return fakeNative(seat.AgentID), nil
		},
		wait: func(ctx context.Context, seat SeatSnapshot) (Registration, error) {
			requireAbsentSeat(t, db, seat.AgentID)
			if seat.Native != fakeNative(seat.AgentID) || seat.Status != Starting {
				t.Fatalf("wait missing trusted binding: %+v", seat)
			}
			events = append(events, "wait:"+seat.AgentID)
			index++
			return readyClaim(seat), nil
		},
	}
	r, err := Start(t.Context(), plan, options, db, launcher)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"launch:builder", "wait:builder", "launch:main", "wait:main", "launch:reviewer", "wait:reviewer"}; !reflect.DeepEqual(events, want) {
		t.Fatalf("events=%v", events)
	}
	live := r.Snapshot()
	if err := live.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, seat := range live.Seats {
		stored, err := db.GetSeat(t.Context(), options.RunID, seat.AgentID, 1)
		if err != nil || stored.Status != Ready || !stored.Current || stored.Tool != seat.Tool ||
			stored.NativeSessionID != seat.Native.SessionID || stored.WorkspaceID != seat.Native.WorkspaceID || stored.SurfaceID != seat.Native.SurfaceID {
			t.Fatalf("registration not persisted: %+v %v", stored, err)
		}
	}
	// Returned snapshots and launch requests cannot mutate the caller's plan.
	live.Seats[0].Status = "changed"
	live.Config.Agents["builder"] = live.Config.Agents["main"]
	after, _ := json.Marshal(plan)
	if string(before) != string(after) || r.Snapshot().Seats[0].Status != Ready {
		t.Fatal("startup state aliases external data")
	}
	run, err := db.GetRun(t.Context())
	if err != nil || run.ConfigDigest != plan.Config.Digest || run.StartupPhase != Starting || !strings.Contains(run.ConfigSnapshot, `"prompt"`) {
		t.Fatalf("frozen configuration not recorded: %+v %v", run, err)
	}
	// A fresh handle proves the accepted READYs survive close/reopen.
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.Open(filepath.Join(filepath.Dir(options.SnapshotPath), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	for _, seat := range plan.Seats {
		if got, err := reopened.GetSeat(t.Context(), options.RunID, seat.ID, 1); err != nil || got.Status != Ready {
			t.Fatalf("reopened seat=%+v err=%v", got, err)
		}
	}
}

func TestStartStopsOnLaunchWaitAndIdentityFailures(t *testing.T) {
	for _, stage := range []string{"launch", "binding", "wait", "wrong-run", "wrong-agent", "stale", "future", "wrong-tool", "wrong-session", "wrong-workspace", "wrong-surface", "wrong-status", "cancel"} {
		t.Run(stage, func(t *testing.T) {
			plan, db, options := startupFixture(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			calls := 0
			failure := errors.New("fixture failure")
			launcher := fakeLauncher{
				launch: func(context.Context, LaunchRequest) (NativeBinding, error) {
					calls++
					if stage == "launch" {
						return NativeBinding{}, failure
					}
					if stage == "binding" {
						return NativeBinding{SessionID: "incomplete"}, nil
					}
					return fakeNative("builder"), nil
				},
				wait: func(ctx context.Context, seat SeatSnapshot) (Registration, error) {
					requireAbsentSeat(t, db, seat.AgentID)
					claim := readyClaim(seat)
					switch stage {
					case "wait":
						return Registration{}, failure
					case "wrong-run":
						claim.Actor.RunID = "another-run"
					case "wrong-agent":
						claim.Actor.AgentID = "main"
					case "stale":
						claim.Actor.Generation = 0
					case "future":
						claim.Actor.Generation = 2
					case "wrong-tool":
						claim.Tool = "claude"
					case "wrong-session":
						claim.Native.SessionID = "another-session"
					case "wrong-workspace":
						claim.Native.WorkspaceID = "another-workspace"
					case "wrong-surface":
						claim.Native.SurfaceID = "another-surface"
					case "wrong-status":
						claim.Status = Starting
					case "cancel":
						cancel() // Even an adapter returning READY after cancellation cannot commit.
					}
					return claim, nil
				},
			}
			r, err := Start(ctx, plan, options, db, launcher)
			if err == nil || r == nil || calls != 1 || r.Snapshot().Seats[0].Status != Starting {
				t.Fatalf("failure advanced startup: calls=%d registry=%v err=%v", calls, r, err)
			}
			for _, seat := range plan.Seats {
				requireAbsentSeat(t, db, seat.ID)
			}
			if (stage == "launch" || stage == "wait") && !errors.Is(err, failure) {
				t.Fatalf("lost adapter error: %v", err)
			}
			if stage == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatalf("lost cancellation: %v", err)
			}
		})
	}
}

func TestRegistryRejectsWrongStaleDuplicateAndUnboundClaims(t *testing.T) {
	plan, db, _ := startupFixture(t)
	snapshot, err := NewSnapshot(plan, "run-test")
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Seats[0].Generation = 2
	r, err := NewRegistry(t.Context(), db, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	seat := snapshot.Seats[0]
	seat.Native = fakeNative(seat.AgentID)
	valid := readyClaim(seat)
	if err := r.Register(t.Context(), valid); err == nil {
		t.Fatal("accepted READY before native binding")
	}
	if err := r.bind(valid.Actor, seat.Native); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		edit func(*Registration)
		code string
	}{
		{"wrong-run", func(c *Registration) { c.Actor.RunID = "wrong" }, ipc.ActorMismatch},
		{"unknown-agent", func(c *Registration) { c.Actor.AgentID = "unknown" }, ipc.ActorMismatch},
		{"old-generation", func(c *Registration) { c.Actor.Generation = 1 }, ipc.GenerationMismatch},
		{"future-generation", func(c *Registration) { c.Actor.Generation = 3 }, ipc.GenerationMismatch},
		{"wrong-native", func(c *Registration) { c.Native.SessionID = "wrong" }, ipc.ActorMismatch},
	} {
		t.Run(tc.name, func(t *testing.T) {
			claim := valid
			tc.edit(&claim)
			var public *ipc.Error
			if err := r.Register(t.Context(), claim); !errors.As(err, &public) || public.Code != tc.code {
				t.Fatalf("expected %s, got %v", tc.code, err)
			}
			if _, err := db.GetSeat(t.Context(), seat.RunID, seat.AgentID, 2); !errors.Is(err, sql.ErrNoRows) {
				t.Fatalf("invalid claim wrote seat: %v", err)
			}
		})
	}
	// A native session or visible surface cannot be assigned to another seat.
	other := snapshot.Seats[1]
	actor := ipc.Actor{RunID: other.RunID, AgentID: other.AgentID, Generation: other.Generation}
	if err := r.bind(actor, seat.Native); err == nil {
		t.Fatal("accepted duplicate native binding")
	}
	// Concurrent duplicate READYs produce one commit and explicit duplicate errors.
	var wg sync.WaitGroup
	results := make(chan error, 8)
	for range cap(results) {
		wg.Add(1)
		go func() { defer wg.Done(); results <- r.Register(t.Context(), valid) }()
	}
	wg.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
			continue
		}
		var public *ipc.Error
		if !errors.As(err, &public) || public.Code != AlreadyRegistered {
			t.Fatalf("duplicate produced unexpected error: %v", err)
		}
	}
	if success != 1 || r.Snapshot().Seats[0].Status != Ready {
		t.Fatalf("accepted READY count=%d", success)
	}
}

func TestRegisterStoreFailureDoesNotMarkReady(t *testing.T) {
	plan, db, _ := startupFixture(t)
	snapshot, _ := NewSnapshot(plan, "run-test")
	r, err := NewRegistry(t.Context(), db, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	seat := snapshot.Seats[0]
	seat.Native = fakeNative(seat.AgentID)
	claim := readyClaim(seat)
	if err := r.bind(claim.Actor, claim.Native); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(t.Context(), claim); err == nil || r.Snapshot().Seats[0].Status != Starting {
		t.Fatalf("store failure reported READY: %v", err)
	}
}

func TestStartPreservesPartialRegistrationAndRejectsRelaunch(t *testing.T) {
	plan, db, options := startupFixture(t)
	waits := 0
	failure := errors.New("second agent READY unavailable")
	launcher := fakeLauncher{
		launch: func(_ context.Context, request LaunchRequest) (NativeBinding, error) {
			return fakeNative(request.Seat.AgentID), nil
		},
		wait: func(_ context.Context, seat SeatSnapshot) (Registration, error) {
			waits++
			if waits == 2 {
				return Registration{}, failure
			}
			return readyClaim(seat), nil
		},
	}
	r, err := Start(t.Context(), plan, options, db, launcher)
	if !errors.Is(err, failure) || r == nil || waits != 2 {
		t.Fatalf("partial startup: registry=%v waits=%d err=%v", r, waits, err)
	}
	live := r.Snapshot()
	if live.Seats[0].Status != Ready || live.Seats[1].Status != Starting || live.Seats[2].Native != (NativeBinding{}) {
		t.Fatalf("partial progress lost or fabricated: %+v", live.Seats)
	}
	if got, err := db.GetSeat(t.Context(), options.RunID, "builder", 1); err != nil || got.Status != Ready {
		t.Fatalf("earlier READY lost: %+v %v", got, err)
	}
	requireAbsentSeat(t, db, "main")
	requireAbsentSeat(t, db, "reviewer")
	if _, err := Start(t.Context(), plan, options, db, launcher); err == nil || waits != 2 {
		t.Fatal("partial startup silently relaunched seats")
	}
}

func TestStartRejectsInvalidPlanAndRepeatedAttemptBeforeLaunch(t *testing.T) {
	for _, stage := range []string{"order", "cwd", "wrapper", "nil-launcher", "canceled", "existing-artifact", "existing-run"} {
		t.Run(stage, func(t *testing.T) {
			plan, db, options := startupFixture(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var launcher Launcher = fakeLauncher{launch: func(context.Context, LaunchRequest) (NativeBinding, error) {
				t.Fatal("invalid startup invoked launcher")
				return NativeBinding{}, nil
			}}
			switch stage {
			case "order":
				plan.Seats[0], plan.Seats[1] = plan.Seats[1], plan.Seats[0]
			case "cwd":
				plan.Seats[0].Cwd = "/wrong"
			case "wrapper":
				plan.Seats[0].Wrapper.Args = append(plan.Seats[0].Wrapper.Args, "prompt text")
			case "nil-launcher":
				launcher = nil
			case "canceled":
				cancel()
			case "existing-artifact":
				if err := os.WriteFile(options.SnapshotPath, []byte("existing"), 0600); err != nil {
					t.Fatal(err)
				}
			case "existing-run":
				snapshot, _ := NewSnapshot(plan, options.RunID)
				if _, err := NewRegistry(ctx, db, snapshot); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := Start(ctx, plan, options, db, launcher); err == nil {
				t.Fatal("accepted invalid startup")
			}
		})
	}
}
