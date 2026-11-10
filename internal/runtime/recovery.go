package runtime

import (
	"context"
	"errors"
	"time"

	"agentflow/internal/ipc"
	"agentflow/internal/store"
)

type RecoveryPolicy = store.RetryPolicy

func DefaultRecoveryPolicy() RecoveryPolicy {
	return RecoveryPolicy{MaxAttempts: 3, Backoff: []time.Duration{time.Second, 5 * time.Second, 30 * time.Second}}
}

// RecoveryReadiness must explicitly permit continuation for this exact binding.
// Check is a dependency probe, not a reset, submit, ACK or native-session resume.
type RecoveryReadiness struct {
	CanContinue    bool
	Classification string
	Reason         string
}

type RecoveryDependency interface {
	Check(context.Context, store.RecoveryDecision) (RecoveryReadiness, error)
}

// RecoveryError describes errors known to have occurred before native effects.
// After a reset/submit intent, unknown evidence always wins over this category.
type RecoveryError struct {
	Code string
}

func (e *RecoveryError) Error() string { return e.Code }

func permanentRecoveryError(err error) bool {
	var fault *RecoveryError
	return errors.As(err, &fault) && (fault.Code == "PERMISSION_DENIED" || fault.Code == "CONFIGURATION_ERROR" || fault.Code == "AUTH_REQUIRED")
}

type RecoveryRunner struct {
	sessions   *SessionCoordinator
	policy     RecoveryPolicy
	now        func() time.Time
	dependency RecoveryDependency
}

func NewRecoveryRunner(sessions *SessionCoordinator, policy RecoveryPolicy, now func() time.Time, dependency RecoveryDependency) (*RecoveryRunner, error) {
	if sessions == nil || now == nil {
		return nil, errors.New("recovery runner requires a session coordinator and clock")
	}
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	policy.Backoff = append([]time.Duration(nil), policy.Backoff...)
	return &RecoveryRunner{sessions: sessions, policy: policy, now: now, dependency: dependency}, nil
}

// Recover is an explicit tick, never a background loop or a sleep. The first call
// schedules a wait; a due call reserves one bounded attempt. Repeated calls while
// waiting, after exhaustion or during an unresolved reservation execute nothing.
// Reservations left by a crash require manual review; they are never auto-stolen.
func (r *RecoveryRunner) Recover(ctx context.Context, actor ipc.Actor, id string) (store.RetryDecision, error) {
	// Use a bounded detached context only for durable cancellation/unknown receipts.
	// Adapter calls always receive the original cancellable context.
	journalCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	decision, claimed, err := r.sessions.db.ReserveRecovery(journalCtx, messageActor(actor), id, r.policy, r.now(), ctx.Err() != nil)
	cancel()
	if err != nil {
		return decision, err
	}
	if !claimed {
		return decision, ctx.Err()
	}
	finish := func(class, reason string, cause error) (store.RetryDecision, error) {
		// Durable native intent, current generation and stop are rechecked here.
		journalCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		result, err := r.sessions.db.FinishRecovery(journalCtx, messageActor(actor), decision, class, reason, r.now())
		if err != nil {
			return result, err
		}
		return result, cause
	}
	if ctx.Err() != nil {
		return finish(store.RecoveryPermanent, "CONTEXT_CANCELLED", ctx.Err())
	}
	if r.dependency == nil {
		return finish(store.RecoveryDependency, "RECOVERY_DEPENDENCY_UNIMPLEMENTED", nil)
	}
	readiness, checkErr := r.dependency.Check(ctx, decision.Reconcile)
	if ctx.Err() != nil {
		return finish(store.RecoveryPermanent, "CONTEXT_CANCELLED", ctx.Err())
	}
	if checkErr != nil {
		if permanentRecoveryError(checkErr) {
			var fault *RecoveryError
			errors.As(checkErr, &fault)
			return finish(store.RecoveryPermanent, fault.Code, nil)
		}
		// Only typed errors opt in to retry. An arbitrary error is untrusted.
		var fault *RecoveryError
		if errors.As(checkErr, &fault) && fault.Code == "DEPENDENCY_UNAVAILABLE" {
			return finish(store.RecoveryDependency, fault.Code, nil)
		}
		return finish(store.RecoveryManual, "DEPENDENCY_CHECK_UNVERIFIED", nil)
	}
	if !readiness.CanContinue || (readiness.Classification != "" && readiness.Classification != store.RecoveryRetryable) {
		class := readiness.Classification
		reason := readiness.Reason
		if class != store.RecoveryRetryable && class != store.RecoveryDependency && class != store.RecoveryPermanent && class != store.RecoveryManual {
			class = store.RecoveryManual
		}
		if reason == "" {
			reason = "DEPENDENCY_CHECK_UNVERIFIED"
		}
		return finish(class, reason, nil)
	}
	// The dependency probe cannot grant permission to bypass reconciliation or the
	// exclusive S5 action claim; Advance rechecks both and protects user input.
	_, advanceErr := r.sessions.advance(ctx, actor, id, &decision)
	if ctx.Err() != nil {
		return finish(store.RecoveryPermanent, "CONTEXT_CANCELLED", ctx.Err())
	}
	if advanceErr != nil {
		if permanentRecoveryError(advanceErr) {
			var fault *RecoveryError
			errors.As(advanceErr, &fault)
			return finish(store.RecoveryPermanent, fault.Code, nil)
		}
		return finish(store.RecoveryManual, "ADVANCE_UNVERIFIED", advanceErr)
	}
	readCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	current, err := r.sessions.db.ReconcileRecoverySession(readCtx, messageActor(actor), id)
	if err != nil {
		return finish(store.RecoveryManual, "RECONCILE_FAILED", err)
	}
	class, reason := store.ClassifyRecovery(current)
	return finish(class, reason, nil)
}
