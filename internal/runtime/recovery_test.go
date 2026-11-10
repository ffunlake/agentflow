package runtime

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"agentflow/internal/store"
)

type dependencyFunc func(context.Context, store.RecoveryDecision) (RecoveryReadiness, error)

func (f dependencyFunc) Check(ctx context.Context, d store.RecoveryDecision) (RecoveryReadiness, error) {
	return f(ctx, d)
}

func readyDependency(context.Context, store.RecoveryDecision) (RecoveryReadiness, error) {
	return RecoveryReadiness{CanContinue: true}, nil
}

func recoveryFixture(t *testing.T) (*store.Store, string, *fakeResetAdapter, *fakeSubmitAdapter, *SessionCoordinator, StartOptions) {
	t.Helper()
	db, _, _, opts := sessionFixture(t)
	queued, _ := NewSessionCoordinator(db, nil, nil)
	record, err := queued.Assign(t.Context(), assignment())
	if err != nil {
		t.Fatal(err)
	}
	reset, submit := &fakeResetAdapter{}, &fakeSubmitAdapter{}
	s, err := NewSessionCoordinator(db, reset, submit)
	if err != nil {
		t.Fatal(err)
	}
	return db, record.Message.ID, reset, submit, s, opts
}

func recoveryRunner(t *testing.T, s *SessionCoordinator, now *time.Time, dep RecoveryDependency) *RecoveryRunner {
	t.Helper()
	r, err := NewRecoveryRunner(s, DefaultRecoveryPolicy(), func() time.Time { return *now }, dep)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func recoverTick(t *testing.T, r *RecoveryRunner, id string) store.RetryDecision {
	t.Helper()
	d, err := r.Recover(t.Context(), assignment().Actor, id)
	if err != nil {
		t.Fatal(err)
	}
	if d.JournalSeq == 0 || d.RecordedAt.IsZero() {
		t.Fatalf("missing journal receipt: %+v", d)
	}
	return d
}

func TestRecoveryFiniteDependencyFailureAndFrozenPolicyAcrossReopen(t *testing.T) {
	db, id, reset, submit, s, opts := recoveryFixture(t)
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	checks := 0
	dep := dependencyFunc(func(context.Context, store.RecoveryDecision) (RecoveryReadiness, error) {
		checks++
		return RecoveryReadiness{Classification: store.RecoveryDependency, Reason: "DEPENDENCY_UNAVAILABLE"}, nil
	})
	r := recoveryRunner(t, s, &now, dep)
	first := recoverTick(t, r, id)
	if first.State != "WAITING" || first.Attempts != 0 || !first.NextAttemptAt.Equal(now.Add(time.Second)) {
		t.Fatal(first)
	}
	if again := recoverTick(t, r, id); !reflect.DeepEqual(first, again) {
		t.Fatal("wait was not idempotent", first, again)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.OpenExisting(filepath.Join(filepath.Dir(opts.SnapshotPath), "state.db"), true)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	s, _ = NewSessionCoordinator(reopened, reset, submit)
	// A new process cannot increase the original budget or shorten its delays.
	r, err = NewRecoveryRunner(s, RecoveryPolicy{MaxAttempts: 1, Backoff: []time.Duration{time.Nanosecond}}, func() time.Time { return now }, dep)
	if err != nil {
		t.Fatal(err)
	}
	if again := recoverTick(t, r, id); !reflect.DeepEqual(first, again) {
		t.Fatal("reopen changed receipt", first, again)
	}
	previous := first
	for attempt := 1; attempt <= 3; attempt++ {
		now = *previous.NextAttemptAt
		d := recoverTick(t, r, id)
		if d.Attempts != attempt || d.Policy.MaxAttempts != 3 || checks != attempt {
			t.Fatal(d, checks)
		}
		if attempt < 3 {
			if d.State != "WAITING" || !d.NextAttemptAt.Equal(now.Add(DefaultRecoveryPolicy().Backoff[attempt])) {
				t.Fatal(d)
			}
		} else if d.State != "EXHAUSTED" || d.NextAttemptAt != nil {
			t.Fatal(d)
		}
		previous = d
	}
	now = now.Add(24 * time.Hour)
	if d := recoverTick(t, r, id); !reflect.DeepEqual(previous, d) || checks != 3 {
		t.Fatal("exhaustion replayed", d, checks)
	}
	if reset.inspections.Load() != 0 || reset.resets.Load() != 0 || submit.calls.Load() != 0 {
		t.Fatal("dependency failures invoked native adapter")
	}
}

func TestRecoveryDependencyReturnsThenOneActionPerTick(t *testing.T) {
	db, id, reset, submit, s, _ := recoveryFixture(t)
	now := time.Now().UTC()
	available := false
	dep := dependencyFunc(func(context.Context, store.RecoveryDecision) (RecoveryReadiness, error) {
		return RecoveryReadiness{CanContinue: available, Classification: store.RecoveryRetryable, Reason: "DEPENDENCY_UNAVAILABLE"}, nil
	})
	r := recoveryRunner(t, s, &now, dep)
	d := recoverTick(t, r, id)
	now = *d.NextAttemptAt
	d = recoverTick(t, r, id)
	if d.Attempts != 1 || d.State != "WAITING" {
		t.Fatal(d)
	}
	available = true
	now = *d.NextAttemptAt
	d = recoverTick(t, r, id)
	if d.State != "WAITING" || d.Reconcile.Delivery.ResetPhase != "READY" || reset.resets.Load() != 1 || submit.calls.Load() != 0 {
		t.Fatal("reset tick also submitted", d)
	}
	now = *d.NextAttemptAt
	d = recoverTick(t, r, id)
	if d.State != "COMPLETE" || d.Reconcile.State != "SENT" || reset.resets.Load() != 1 || submit.calls.Load() != 1 {
		t.Fatal(d)
	}
	for i := 0; i < 3; i++ {
		recoverTick(t, r, id)
	}
	w, err := db.SessionWork(t.Context(), id)
	if err != nil || w.Record.Delivery.Phase != "SENT" || w.Record.Delivery.Attempt != 1 || reset.resets.Load() != 1 || submit.calls.Load() != 1 {
		t.Fatal(w, err)
	}
}

func TestRecoveryUnknownNativeResultsNeverReplay(t *testing.T) {
	for _, action := range []string{"reset", "submit", "cancel-during-reset"} {
		t.Run(action, func(t *testing.T) {
			db, id, reset, submit, s, opts := recoveryFixture(t)
			now := time.Now().UTC()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if action != "submit" {
				reset.reset = func(context.Context, ResetRequest) (NativeBinding, error) {
					if action == "cancel-during-reset" {
						cancel()
					}
					return NativeBinding{}, errors.New("response lost")
				}
			} else {
				submit.submit = func(context.Context, SeatSnapshot, store.Message) (Submission, error) {
					return Submission{}, errors.New("response lost")
				}
			}
			r := recoveryRunner(t, s, &now, dependencyFunc(readyDependency))
			d := recoverTick(t, r, id)
			now = *d.NextAttemptAt
			d, err := r.Recover(ctx, assignment().Actor, id)
			if action == "submit" {
				if err != nil {
					t.Fatal(err)
				}
				now = *d.NextAttemptAt
				d, err = r.Recover(ctx, assignment().Actor, id)
			}
			if action == "cancel-during-reset" && !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if d.State != "MANUAL_RECONCILE" || d.Reconcile.State != "UNKNOWN_NEEDS_RECONCILE" {
				t.Fatal(d, err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			db, err = store.OpenExisting(filepath.Join(filepath.Dir(opts.SnapshotPath), "state.db"), true)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			s, _ = NewSessionCoordinator(db, reset, submit)
			r = recoveryRunner(t, s, &now, dependencyFunc(readyDependency))
			if again := recoverTick(t, r, id); !reflect.DeepEqual(d, again) {
				t.Fatal("unknown changed after reopen", d, again)
			}
			if reset.resets.Load() != 1 || submit.calls.Load() > 1 {
				t.Fatal("native action replayed")
			}
		})
	}
}

func TestRecoveryCancellationStopAndPermissionGates(t *testing.T) {
	for _, gate := range []string{"cancel-before", "cancel-probe", "stop-before", "stop-probe", "permission", "config", "inspect-permission", "unverified"} {
		t.Run(gate, func(t *testing.T) {
			db, id, reset, submit, s, _ := recoveryFixture(t)
			now := time.Now().UTC()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			checks := 0
			dep := dependencyFunc(func(context.Context, store.RecoveryDecision) (RecoveryReadiness, error) {
				checks++
				switch gate {
				case "cancel-probe":
					cancel()
				case "stop-probe":
					if _, err := db.RequestStop(t.Context(), assignment().Actor.RunID); err != nil {
						t.Fatal(err)
					}
				case "permission":
					return RecoveryReadiness{}, &RecoveryError{Code: "PERMISSION_DENIED"}
				case "config":
					return RecoveryReadiness{}, &RecoveryError{Code: "CONFIGURATION_ERROR"}
				case "unverified":
					return RecoveryReadiness{}, nil
				}
				return RecoveryReadiness{CanContinue: true}, nil
			})
			if gate == "inspect-permission" {
				reset.inspect = func(context.Context, SeatSnapshot) (InputState, error) {
					return InputState{}, &RecoveryError{Code: "PERMISSION_DENIED"}
				}
			}
			r := recoveryRunner(t, s, &now, dep)
			if gate == "cancel-before" {
				cancel()
			}
			if gate == "stop-before" {
				if _, err := db.RequestStop(t.Context(), assignment().Actor.RunID); err != nil {
					t.Fatal(err)
				}
			}
			d, err := r.Recover(ctx, assignment().Actor, id)
			if gate != "cancel-before" && gate != "stop-before" {
				if err != nil || d.NextAttemptAt == nil {
					t.Fatal(d, err)
				}
				now = *d.NextAttemptAt
				d, err = r.Recover(ctx, assignment().Actor, id)
			}
			if gate == "cancel-before" || gate == "cancel-probe" {
				if !errors.Is(err, context.Canceled) || d.Reason != "CONTEXT_CANCELLED" {
					t.Fatal(d, err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			want := "STOPPED"
			if gate == "unverified" {
				want = "MANUAL_RECONCILE"
			}
			if d.State != want || d.NextAttemptAt != nil || reset.resets.Load() != 0 || submit.calls.Load() != 0 {
				t.Fatal(d)
			}
			if (gate == "cancel-before" || gate == "stop-before") && (checks != 0 || reset.inspections.Load() != 0) {
				t.Fatal("terminal gate invoked adapter")
			}
			if again := recoverTick(t, r, id); again.State != want {
				t.Fatal(again)
			}
		})
	}
}

func TestRecoveryConcurrentTicksDoNotDuplicateDependencyOrAction(t *testing.T) {
	_, id, reset, submit, s, opts := recoveryFixture(t)
	otherDB, err := store.OpenExisting(filepath.Join(filepath.Dir(opts.SnapshotPath), "state.db"), true)
	if err != nil {
		t.Fatal(err)
	}
	defer otherDB.Close()
	otherSessions, _ := NewSessionCoordinator(otherDB, reset, submit)
	now := time.Now().UTC()
	entered, release := make(chan struct{}), make(chan struct{})
	dep := dependencyFunc(func(context.Context, store.RecoveryDecision) (RecoveryReadiness, error) {
		close(entered)
		<-release
		return RecoveryReadiness{CanContinue: true}, nil
	})
	r := recoveryRunner(t, s, &now, dep)
	otherRunner := recoveryRunner(t, otherSessions, &now, dep)
	d := recoverTick(t, r, id)
	now = *d.NextAttemptAt
	var wg sync.WaitGroup
	wg.Add(1)
	var result store.RetryDecision
	var resultErr error
	go func() { defer wg.Done(); result, resultErr = r.Recover(t.Context(), assignment().Actor, id) }()
	<-entered
	other := recoverTick(t, otherRunner, id)
	if other.State != "ATTEMPT_RESERVED" || other.Attempts != 1 {
		t.Fatal(other)
	}
	close(release)
	wg.Wait()
	if resultErr != nil || result.State != "WAITING" || reset.resets.Load() != 1 || submit.calls.Load() != 0 {
		t.Fatal(result, resultErr)
	}
}

func TestRecoveryRevokedReservationCannotReachNativeAction(t *testing.T) {
	_, id, reset, submit, s, _ := recoveryFixture(t)
	now := time.Now().UTC()
	var r *RecoveryRunner
	dep := dependencyFunc(func(context.Context, store.RecoveryDecision) (RecoveryReadiness, error) {
		cancelled, cancel := context.WithCancel(t.Context())
		cancel()
		d, err := r.Recover(cancelled, assignment().Actor, id)
		if !errors.Is(err, context.Canceled) || d.State != "STOPPED" {
			t.Fatal(d, err)
		}
		return RecoveryReadiness{CanContinue: true}, nil
	})
	r = recoveryRunner(t, s, &now, dep)
	d := recoverTick(t, r, id)
	now = *d.NextAttemptAt
	d = recoverTick(t, r, id)
	if d.State != "STOPPED" || reset.resets.Load() != 0 || submit.calls.Load() != 0 {
		t.Fatal("revoked reservation executed", d)
	}
}

func TestRecoveryMissingDependencyProbeCannotReachAdapters(t *testing.T) {
	_, id, reset, submit, s, _ := recoveryFixture(t)
	now := time.Now().UTC()
	r := recoveryRunner(t, s, &now, nil)
	d := recoverTick(t, r, id)
	now = *d.NextAttemptAt
	d = recoverTick(t, r, id)
	if d.Reason != "RECOVERY_DEPENDENCY_UNIMPLEMENTED" || d.State != "WAITING" || reset.inspections.Load() != 0 || reset.resets.Load() != 0 || submit.calls.Load() != 0 {
		t.Fatal(d)
	}
}

func TestRecoveryMissingAdaptersAndInvalidPolicy(t *testing.T) {
	db, id, reset, submit, _, _ := recoveryFixture(t)
	s, _ := NewSessionCoordinator(db, nil, nil)
	now := time.Now().UTC()
	r := recoveryRunner(t, s, &now, dependencyFunc(readyDependency))
	d := recoverTick(t, r, id)
	now = *d.NextAttemptAt
	d = recoverTick(t, r, id)
	if d.State != "WAITING" || d.Reason != "RESET_ADAPTER_UNIMPLEMENTED" || d.Reconcile.Delivery.Channel != "UNVERIFIED" || d.Reconcile.Delivery.Attempt != 0 || reset.resets.Load() != 0 || submit.calls.Load() != 0 {
		t.Fatal(d)
	}
	for _, policy := range []RecoveryPolicy{{}, {MaxAttempts: 2, Backoff: []time.Duration{time.Second}}, {MaxAttempts: 1, Backoff: []time.Duration{0}}, {MaxAttempts: 1, Backoff: []time.Duration{-1}}} {
		if _, err := NewRecoveryRunner(s, policy, func() time.Time { return now }, nil); err == nil {
			t.Fatal("invalid policy accepted", policy)
		}
	}
}
