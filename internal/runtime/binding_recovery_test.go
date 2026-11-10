package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"agentflow/internal/ipc"
	"agentflow/internal/store"
)

type fakeBindingAdapter struct {
	digest                 string
	inspect                func(context.Context, SeatSnapshot) (SessionObservation, error)
	create                 func(context.Context, BindingRecoveryRequest) (NativeBinding, error)
	wait                   func(context.Context, BindingRecoveryRequest, NativeBinding) (BindingReady, error)
	checks, creates, waits atomic.Int32
}

func (f *fakeBindingAdapter) observation(seat SeatSnapshot, state string) SessionObservation {
	return SessionObservation{Actor: seatActor(seat), Native: seat.Native, Tool: seat.Tool, Permissions: seat.Permissions, ConfigDigest: f.digest, State: state, Verified: true}
}
func (f *fakeBindingAdapter) InspectOriginal(ctx context.Context, seat SeatSnapshot) (SessionObservation, error) {
	f.checks.Add(1)
	if f.inspect != nil {
		return f.inspect(ctx, seat)
	}
	return f.observation(seat, "AVAILABLE"), nil
}
func (f *fakeBindingAdapter) Create(ctx context.Context, r BindingRecoveryRequest) (NativeBinding, error) {
	f.creates.Add(1)
	if f.create != nil {
		return f.create(ctx, r)
	}
	return NativeBinding{SessionID: "recovered-thread", WorkspaceID: "cmux-workspace-new", SurfaceID: "cmux-surface-new"}, nil
}
func (f *fakeBindingAdapter) WaitBindingReady(ctx context.Context, r BindingRecoveryRequest, native NativeBinding) (BindingReady, error) {
	f.waits.Add(1)
	if f.wait != nil {
		return f.wait(ctx, r, native)
	}
	seat := r.Previous
	seat.Generation, seat.Native = r.Generation, native
	return BindingReady{Registration: readyClaim(seat), Permissions: seat.Permissions, ConfigDigest: f.digest}, nil
}

func bindingFixture(t *testing.T) (*SessionRecovery, *fakeBindingAdapter, *time.Time, StartOptions) {
	t.Helper()
	db, _, plan, opts := sessionFixture(t)
	coordinator, _ := NewSessionCoordinator(db, &fakeResetAdapter{}, &fakeSubmitAdapter{})
	fake := &fakeBindingAdapter{digest: plan.Config.Digest}
	now := time.Date(2026, 11, 3, 15, 0, 0, 0, time.UTC)
	r, err := NewSessionRecovery(coordinator, fake, DefaultRecoveryPolicy(), func() time.Time { return now }, nil)
	if err != nil {
		t.Fatal(err)
	}
	return r, fake, &now, opts
}

func bindingTarget() ipc.Actor {
	return ipc.Actor{RunID: "run-test", AgentID: "builder", Generation: 1}
}

func bindingTick(t *testing.T, r *SessionRecovery, request string) store.BindingRecovery {
	t.Helper()
	d, err := r.Recover(t.Context(), assignment().Actor, bindingTarget(), request)
	if err != nil {
		t.Fatal(err)
	}
	if d.JournalSeq == 0 || d.RecordedAt.IsZero() {
		t.Fatal("missing receipt", d)
	}
	return d
}

func dueBinding(t *testing.T, r *SessionRecovery, now *time.Time, request string) store.BindingRecovery {
	t.Helper()
	d := bindingTick(t, r, request)
	if d.State != "WAITING" || d.NextAttemptAt == nil {
		t.Fatal(d)
	}
	*now = *d.NextAttemptAt
	return bindingTick(t, r, request)
}

func TestBindingRecoveryOriginalPreferredAndReopen(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	r, f, now, opts := bindingFixture(t)
	before, _ := r.sessions.db.GetSeat(t.Context(), "run-test", "builder", 1)
	waiting := bindingTick(t, r, "incident")
	if again := bindingTick(t, r, "incident"); !reflect.DeepEqual(waiting, again) {
		t.Fatal("wait changed", again)
	}
	*now = *waiting.NextAttemptAt
	d := bindingTick(t, r, "incident")
	after, _ := r.sessions.db.GetSeat(t.Context(), "run-test", "builder", 1)
	if d.State != "COMPLETE" || d.Reason != "ORIGINAL_SESSION_AVAILABLE" || !reflect.DeepEqual(before, after) || f.checks.Load() != 1 || f.creates.Load() != 0 || f.waits.Load() != 0 {
		t.Fatal(d, after)
	}
	if err := r.sessions.db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := store.OpenExisting(filepath.Join(filepath.Dir(opts.SnapshotPath), "state.db"), true)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	sessions, _ := NewSessionCoordinator(db, nil, nil)
	r, _ = NewSessionRecovery(sessions, f, DefaultRecoveryPolicy(), func() time.Time { return *now }, nil)
	if again := bindingTick(t, r, "incident"); !reflect.DeepEqual(d, again) || f.checks.Load() != 1 {
		t.Fatal("reopen replayed", again)
	}
}

func TestBindingRecoveryIDChangeRequiresReadyAndRejectsOldLocation(t *testing.T) {
	r, f, now, _ := bindingFixture(t)
	f.inspect = func(_ context.Context, seat SeatSnapshot) (SessionObservation, error) {
		o := f.observation(seat, "AVAILABLE")
		o.Native.WorkspaceID, o.Native.SurfaceID = "new-cmux-id", "new-surface-id"
		return o, nil
	}
	before, _ := r.sessions.db.GetSeat(t.Context(), "run-test", "builder", 1)
	d := dueBinding(t, r, now, "move")
	current, _ := r.sessions.db.GetSeat(t.Context(), "run-test", "builder", 1)
	if d.State != "COMPLETE" || current.NativeSessionID != before.NativeSessionID || current.Generation != 1 || current.WorkspaceID != "new-cmux-id" || f.creates.Load() != 0 || f.waits.Load() != 1 {
		t.Fatal(d, current)
	}
	old := f.observation(sessionSeat(before), "USER_CLOSED")
	old.Permissions = "full"
	// Use the actual frozen permission, independent of test fixture defaults.
	req, _, _ := r.request(t.Context(), d)
	old.Permissions = req.Role.Permissions
	if err := r.NoteExit(t.Context(), old); err == nil {
		t.Fatal("old location accepted")
	}
	claim := BindingReady{Registration: readyClaim(req.Previous), Permissions: req.Role.Permissions, ConfigDigest: f.digest}
	if err := r.ValidateBinding(t.Context(), claim); err == nil {
		t.Fatal("old READY/ACK identity accepted")
	}
	claim.Native = sessionSeat(current).Native
	if err := r.ValidateBinding(t.Context(), claim); err != nil {
		t.Fatal(err)
	}
	if again := bindingTick(t, r, "move"); !reflect.DeepEqual(d, again) {
		t.Fatal(again)
	}
}

func checkpointBindingFixture(t *testing.T) (*SessionRecovery, *fakeBindingAdapter, *time.Time, StartOptions) {
	t.Helper()
	// Context rejects symlink ancestors such as macOS /var. Freeze a private
	// canonical context path before startup instead of changing live config.
	plan, db, opts := startupFixture(t)
	dir, err := filepath.EvalSymlinks(filepath.Dir(opts.SnapshotPath))
	if err != nil {
		t.Fatal(err)
	}
	opts.SnapshotPath = filepath.Join(dir, "config.resolved.json")
	plan.Config.ContextDir = filepath.Join(dir, "contexts")
	plan.Config.Digest = ""
	body, _ := json.Marshal(plan.Config)
	plan.Config.Digest = contextTestDigest(body)
	plan, err = Compile(plan.Config, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Start(t.Context(), plan, opts, db, fakeLauncher{
		launch: func(_ context.Context, req LaunchRequest) (NativeBinding, error) {
			return fakeNative(req.Seat.AgentID), nil
		},
		wait: func(_ context.Context, seat SeatSnapshot) (Registration, error) { return readyClaim(seat), nil },
	}); err != nil {
		t.Fatal(err)
	}
	sessions, _ := NewSessionCoordinator(db, nil, nil)
	f := &fakeBindingAdapter{digest: plan.Config.Digest}
	now := time.Now().UTC()
	r, _ := NewSessionRecovery(sessions, f, DefaultRecoveryPolicy(), func() time.Time { return now }, nil)
	contextRun, err := CreateContext(opts.SnapshotPath, "run-test")
	if err != nil {
		t.Fatal(err)
	}
	r.bootstrap, _ = NewBootstrapService(r.sessions.db, contextRun)
	return r, f, &now, opts
}

func TestBindingRecoveryReplacementUsesCheckpointAndNewReady(t *testing.T) {
	r, f, now, _ := checkpointBindingFixture(t)
	f.inspect = func(_ context.Context, seat SeatSnapshot) (SessionObservation, error) {
		return f.observation(seat, "UNAVAILABLE"), nil
	}
	f.create = func(ctx context.Context, req BindingRecoveryRequest) (NativeBinding, error) {
		if req.Checkpoint == nil || req.Checkpoint.Bootstrap.Generation != 1 || req.Checkpoint.Bootstrap.AgentID != "builder" || req.Generation != 2 || req.Role.Tool != req.Previous.Tool || req.Checkpoint.Bootstrap.Protocol.Permissions != req.Role.Permissions {
			t.Fatal("checkpoint/frozen metadata missing", req)
		}
		seat, _ := r.sessions.db.GetSeat(ctx, "run-test", "builder", 1)
		if !seat.Current {
			t.Fatal("generation changed before READY")
		}
		return NativeBinding{SessionID: "next-native-thread", WorkspaceID: "next-cmux", SurfaceID: "next-surface"}, nil
	}
	d := dueBinding(t, r, now, "missing")
	previous, _ := r.sessions.db.GetSeat(t.Context(), "run-test", "builder", 1)
	current, err := r.sessions.db.GetSeat(t.Context(), "run-test", "builder", 2)
	if err != nil || d.State != "COMPLETE" || previous.Current || !current.Current || f.creates.Load() != 1 || f.waits.Load() != 1 {
		t.Fatal(d, current, err)
	}
	if again := bindingTick(t, r, "missing"); !reflect.DeepEqual(d, again) || f.creates.Load() != 1 {
		t.Fatal("replacement replay", again)
	}
	if err := r.NoteExit(t.Context(), f.observation(sessionSeat(previous), "USER_CLOSED")); err == nil {
		t.Fatal("old generation exit accepted")
	}
	// New location is also accepted by S6.3 when the run-level workspace was
	// unspecified. File progress survives the new native generation.
	if _, err := r.bootstrap.Bootstrap(t.Context(), ipc.Actor{RunID: "run-test", AgentID: "builder", Generation: 2}); err != nil {
		t.Fatal(err)
	}
}

func TestBindingRecoveryReadyAndInspectionMismatchFailClosed(t *testing.T) {
	for _, fault := range []string{"run", "agent", "generation", "tool", "permission", "digest", "session", "ready-run", "ready-generation", "ready-native", "ready-tool", "ready-permission", "ready-digest", "ready-status", "ready-error"} {
		t.Run(fault, func(t *testing.T) {
			r, f, now, _ := bindingFixture(t)
			f.inspect = func(_ context.Context, seat SeatSnapshot) (SessionObservation, error) {
				o := f.observation(seat, "AVAILABLE")
				o.Native.SurfaceID = "move"
				switch fault {
				case "run":
					o.Actor.RunID = "wrong"
				case "agent":
					o.Actor.AgentID = "reviewer"
				case "generation":
					o.Actor.Generation++
				case "tool":
					o.Tool = "claude"
				case "permission":
					o.Permissions = "wrong"
				case "digest":
					o.ConfigDigest = "other-model-account-config"
				case "session":
					o.Native.SessionID = "unrelated-thread"
				}
				return o, nil
			}
			f.wait = func(_ context.Context, req BindingRecoveryRequest, native NativeBinding) (BindingReady, error) {
				seat := req.Previous
				seat.Generation, seat.Native = req.Generation, native
				c := BindingReady{Registration: readyClaim(seat), Permissions: seat.Permissions, ConfigDigest: f.digest}
				switch fault {
				case "ready-run":
					c.Actor.RunID = "wrong"
				case "ready-generation":
					c.Actor.Generation++
				case "ready-native":
					c.Native.SurfaceID = "old"
				case "ready-tool":
					c.Tool = "claude"
				case "ready-permission":
					c.Permissions = "wrong"
				case "ready-digest":
					c.ConfigDigest = "wrong"
				case "ready-status":
					c.Status = Busy
				case "ready-error":
					return c, errors.New("lost READY")
				}
				return c, nil
			}
			d := dueBinding(t, r, now, "mismatch")
			seat, _ := r.sessions.db.GetSeat(t.Context(), "run-test", "builder", 1)
			if d.State != "MANUAL_RECONCILE" || seat.SurfaceID == "move" || f.creates.Load() != 0 {
				t.Fatal(d, seat)
			}
			bindingTick(t, r, "mismatch")
			if f.checks.Load() != 1 {
				t.Fatal("mismatch replayed")
			}
		})
	}
}

func TestBindingRecoveryStopUnknownExitAndS71Composition(t *testing.T) {
	for _, gate := range []string{"stop", "close", "unknown", "terminal", "stop-inspect", "cancel-inspect", "unknown-submit"} {
		t.Run(gate, func(t *testing.T) {
			r, f, now, _ := bindingFixture(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			seat, _ := r.sessions.db.GetSeat(t.Context(), "run-test", "builder", 1)
			req := store.BindingRecovery{AgentID: "builder", Previous: seat, ConfigDigest: f.digest}
			request, _, _ := r.request(t.Context(), req)
			switch gate {
			case "stop":
				_, _ = r.sessions.db.RequestStop(t.Context(), "run-test")
			case "close", "unknown", "terminal":
				o := f.observation(request.Previous, "unknown")
				if gate == "close" {
					o.State = "USER_CLOSED"
				}
				if gate == "terminal" {
					o.State = "TERMINAL"
				}
				if err := r.NoteExit(t.Context(), o); err != nil {
					t.Fatal(err)
				}
			case "unknown-submit":
				c := assignment()
				c.Session = "persistent"
				r.sessions.submit = &fakeSubmitAdapter{submit: func(context.Context, SeatSnapshot, store.Message) (Submission, error) {
					return Submission{}, errors.New("response lost")
				}}
				if _, err := r.sessions.Assign(t.Context(), c); err != nil {
					t.Fatal(err)
				}
			}
			f.inspect = func(_ context.Context, seat SeatSnapshot) (SessionObservation, error) {
				if gate == "stop-inspect" {
					_, _ = r.sessions.db.RequestStop(t.Context(), "run-test")
				}
				if gate == "cancel-inspect" {
					cancel()
				}
				return f.observation(seat, "AVAILABLE"), nil
			}
			d := bindingTick(t, r, gate)
			if d.NextAttemptAt != nil {
				*now = *d.NextAttemptAt
				var err error
				d, err = r.Recover(ctx, assignment().Actor, bindingTarget(), gate)
				if err != nil {
					t.Fatal(err)
				}
			}
			if d.State != "STOPPED" && d.State != "MANUAL_RECONCILE" {
				t.Fatal(d)
			}
			bindingTick(t, r, gate)
			if f.creates.Load() != 0 || f.waits.Load() != 0 {
				t.Fatal("gate invoked native operation")
			}
			if gate == "close" || gate == "unknown" || gate == "terminal" {
				// S7.1 cannot bypass the sticky exit via a new assignment.
				reset, submit := &fakeResetAdapter{}, &fakeSubmitAdapter{}
				r.sessions.reset, r.sessions.submit = reset, submit
				work, err := r.sessions.Assign(t.Context(), assignment())
				if err != nil {
					t.Fatal(err)
				}
				runner := recoveryRunner(t, r.sessions, now, dependencyFunc(readyDependency))
				decision := recoverTick(t, runner, work.Message.ID)
				if decision.State != "STOPPED" && decision.State != "MANUAL_RECONCILE" {
					t.Fatal(decision)
				}
				if reset.resets.Load() != 0 || submit.calls.Load() != 0 {
					t.Fatal("S7.1 resurrected stopped seat")
				}
			}
		})
	}
}

func TestBindingRecoveryPartialStartupAndLostLaunch(t *testing.T) {
	for _, lost := range []bool{false, true} {
		t.Run(map[bool]string{false: "bound-before-ready", true: "launch-response-lost"}[lost], func(t *testing.T) {
			plan, db, opts := startupFixture(t)
			registry, err := Start(t.Context(), plan, opts, db, fakeLauncher{
				launch: func(_ context.Context, req LaunchRequest) (NativeBinding, error) {
					if lost {
						return NativeBinding{}, errors.New("launch response lost")
					}
					return fakeNative(req.Seat.AgentID), nil
				}, wait: func(context.Context, SeatSnapshot) (Registration, error) {
					return Registration{}, errors.New("READY interrupted")
				},
			})
			if err == nil {
				t.Fatal("expected partial start")
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			db, err = store.OpenExisting(filepath.Join(filepath.Dir(opts.SnapshotPath), "state.db"), true)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			sessions, _ := NewSessionCoordinator(db, nil, nil)
			f := &fakeBindingAdapter{digest: plan.Config.Digest}
			now := time.Now().UTC()
			r, _ := NewSessionRecovery(sessions, f, DefaultRecoveryPolicy(), func() time.Time { return now }, nil)
			d := bindingTick(t, r, "partial")
			if lost {
				if d.State != "MANUAL_RECONCILE" || d.Reason != "PARTIAL_LAUNCH_UNVERIFIED" || f.checks.Load() != 0 {
					t.Fatal(d)
				}
			} else {
				now = *d.NextAttemptAt
				d = bindingTick(t, r, "partial")
				seat, err := db.GetSeat(t.Context(), "run-test", "builder", 1)
				if err != nil || d.State != "COMPLETE" || seat.Status != Ready || f.waits.Load() != 1 {
					t.Fatal(d, seat, err)
				}
			}
			if registry.Snapshot().Seats[0].Status != Starting || f.creates.Load() != 0 {
				t.Fatal("startup memory used as authority")
			}
		})
	}
}

func TestBindingRecoveryClosedPartialLaunchRejectsLateStartupReady(t *testing.T) {
	plan, db, opts := startupFixture(t)
	registry, err := Start(t.Context(), plan, opts, db, fakeLauncher{
		launch: func(_ context.Context, req LaunchRequest) (NativeBinding, error) {
			return fakeNative(req.Seat.AgentID), nil
		},
		wait: func(context.Context, SeatSnapshot) (Registration, error) {
			return Registration{}, errors.New("interrupted READY")
		},
	})
	if err == nil {
		t.Fatal("expected partial startup")
	}
	sessions, _ := NewSessionCoordinator(db, nil, nil)
	f := &fakeBindingAdapter{digest: plan.Config.Digest}
	now := time.Now().UTC()
	r, _ := NewSessionRecovery(sessions, f, DefaultRecoveryPolicy(), func() time.Time { return now }, nil)
	seat := registry.Snapshot().Seats[0]
	if err := r.NoteExit(t.Context(), f.observation(seat, "USER_CLOSED")); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(t.Context(), readyClaim(seat)); err == nil {
		t.Fatal("late startup READY resurrected user-closed seat")
	}
	d := bindingTick(t, r, "closed-partial")
	if d.State != "STOPPED" || f.checks.Load() != 0 {
		t.Fatal(d)
	}
	requireAbsentSeat(t, db, "builder")
}

func TestBindingRecoveryConcurrentHandlesAndInterruptedClaim(t *testing.T) {
	r, f, now, opts := bindingFixture(t)
	d := bindingTick(t, r, "race")
	*now = *d.NextAttemptAt
	other, err := store.OpenExisting(filepath.Join(filepath.Dir(opts.SnapshotPath), "state.db"), true)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	sessions, _ := NewSessionCoordinator(other, nil, nil)
	r2, _ := NewSessionRecovery(sessions, f, DefaultRecoveryPolicy(), func() time.Time { return *now }, nil)
	entered, release := make(chan struct{}), make(chan struct{})
	f.inspect = func(_ context.Context, seat SeatSnapshot) (SessionObservation, error) {
		close(entered)
		<-release
		return f.observation(seat, "AVAILABLE"), nil
	}
	var wg sync.WaitGroup
	wg.Add(1)
	var result store.BindingRecovery
	var firstErr error
	go func() {
		defer wg.Done()
		result, firstErr = r.Recover(t.Context(), assignment().Actor, bindingTarget(), "race")
	}()
	<-entered
	for _, request := range []string{"race", "competing-request"} {
		d, err := r2.Recover(t.Context(), assignment().Actor, bindingTarget(), request)
		if err != nil || d.State != "ATTEMPT_RESERVED" {
			t.Fatal(d, err)
		}
	}
	close(release)
	wg.Wait()
	if firstErr != nil || result.State != "COMPLETE" || f.checks.Load() != 1 {
		t.Fatal(result, firstErr)
	}
	// Emulate a process dying immediately after claiming its next incident.
	d = bindingTick(t, r, "interrupted")
	*now = *d.NextAttemptAt
	d, claimed, err := r.sessions.db.ReserveBindingRecovery(t.Context(), messageActor(assignment().Actor), messageActor(bindingTarget()), "interrupted", DefaultRecoveryPolicy(), *now, false)
	if err != nil || !claimed {
		t.Fatal(d, err)
	}
	if again := bindingTick(t, r2, "interrupted"); again.State != "ATTEMPT_RESERVED" || f.checks.Load() != 1 {
		t.Fatal("stole interrupted claim", again)
	}
}

func TestBindingRecoveryCreationUnknownAndStopDuringReadyNeverReplay(t *testing.T) {
	for _, fault := range []string{"create-error", "ready-error", "ready-old-generation", "stop-ready", "close-ready"} {
		t.Run(fault, func(t *testing.T) {
			r, f, now, opts := checkpointBindingFixture(t)
			f.inspect = func(_ context.Context, seat SeatSnapshot) (SessionObservation, error) {
				return f.observation(seat, "UNAVAILABLE"), nil
			}
			if fault == "create-error" {
				f.create = func(context.Context, BindingRecoveryRequest) (NativeBinding, error) {
					return NativeBinding{}, errors.New("created but response lost")
				}
			}
			f.wait = func(_ context.Context, req BindingRecoveryRequest, native NativeBinding) (BindingReady, error) {
				if fault == "stop-ready" {
					_, _ = r.sessions.db.RequestStop(t.Context(), "run-test")
				}
				if fault == "close-ready" {
					if err := r.NoteExit(t.Context(), f.observation(req.Previous, "USER_CLOSED")); err != nil {
						t.Fatal(err)
					}
				}
				seat := req.Previous
				seat.Generation, seat.Native = req.Generation, native
				c := BindingReady{Registration: readyClaim(seat), Permissions: seat.Permissions, ConfigDigest: f.digest}
				if fault == "ready-old-generation" {
					c.Actor.Generation--
				}
				if fault == "ready-error" {
					return c, errors.New("READY history expired")
				}
				return c, nil
			}
			d := dueBinding(t, r, now, "fault")
			if d.State != "MANUAL_RECONCILE" && d.State != "STOPPED" {
				t.Fatal(d)
			}
			if _, err := r.sessions.db.GetSeat(t.Context(), "run-test", "builder", 2); err == nil {
				t.Fatal("unconfirmed generation committed")
			}
			if err := r.sessions.db.Close(); err != nil {
				t.Fatal(err)
			}
			db, err := store.OpenExisting(filepath.Join(filepath.Dir(opts.SnapshotPath), "state.db"), true)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			sessions, _ := NewSessionCoordinator(db, nil, nil)
			r, _ = NewSessionRecovery(sessions, f, DefaultRecoveryPolicy(), func() time.Time { return *now }, nil)
			bindingTick(t, r, "fault")
			bindingTick(t, r, "different-request")
			if f.creates.Load() != 1 || f.checks.Load() != 1 {
				t.Fatal("unknown action replayed", f.creates.Load(), f.checks.Load())
			}
		})
	}
}

func TestBindingRecoveryHonorsMessageRecoveryBudget(t *testing.T) {
	r, f, now, _ := bindingFixture(t)
	queued, _ := NewSessionCoordinator(r.sessions.db, nil, nil)
	work, err := queued.Assign(t.Context(), assignment())
	if err != nil {
		t.Fatal(err)
	}
	runner := recoveryRunner(t, r.sessions, now, dependencyFunc(func(context.Context, store.RecoveryDecision) (RecoveryReadiness, error) {
		return RecoveryReadiness{Classification: store.RecoveryDependency, Reason: "DEPENDENCY_UNAVAILABLE"}, nil
	}))
	d := recoverTick(t, runner, work.Message.ID)
	for i := 0; i < 3; i++ {
		*now = *d.NextAttemptAt
		d = recoverTick(t, runner, work.Message.ID)
	}
	if d.State != "EXHAUSTED" {
		t.Fatal(d)
	}
	binding := bindingTick(t, r, "budget-cannot-bypass")
	if binding.State != "MANUAL_RECONCILE" || binding.Reason != "MESSAGE_RECOVERY_EXHAUSTED" || f.checks.Load() != 0 || f.creates.Load() != 0 {
		t.Fatal(binding)
	}
}
