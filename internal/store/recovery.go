package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

const (
	RecoveryRetryable  = "RECOVERABLE"
	RecoveryDependency = "DEPENDENCY_UNAVAILABLE"
	RecoveryPermanent  = "NON_RECOVERABLE"
	RecoveryManual     = "NEEDS_MANUAL_RECONCILE"
)

// RetryPolicy is frozen in the first recovery receipt. Attempts count dependency
// checks/action opportunities, independently of delivery.attempt (body submits).
type RetryPolicy struct {
	MaxAttempts int             `json:"max_attempts"`
	Backoff     []time.Duration `json:"backoff"`
}

func (p RetryPolicy) Validate() error {
	if p.MaxAttempts < 1 || len(p.Backoff) != p.MaxAttempts {
		return errors.New("recovery policy requires a finite positive maximum and one delay per attempt")
	}
	for _, d := range p.Backoff {
		if d <= 0 {
			return errors.New("recovery backoff must be positive")
		}
	}
	return nil
}

type RetryDecision struct {
	MessageID      string           `json:"message_id"`
	State          string           `json:"state"`
	Classification string           `json:"classification"`
	Reason         string           `json:"reason"`
	Attempts       int              `json:"attempts"`
	Policy         RetryPolicy      `json:"policy"`
	NextAttemptAt  *time.Time       `json:"next_attempt_at"`
	Reconcile      RecoveryDecision `json:"reconcile"`
	JournalSeq     int64            `json:"journal_seq"`
	RecordedAt     time.Time        `json:"recorded_at"`
}

// ClassifyRecovery uses an allowlist. Unknown errors never authorize native work.
func ClassifyRecovery(r RecoveryDecision) (classification, reason string) {
	if r.StopRequested {
		return RecoveryPermanent, "STOP_REQUESTED"
	}
	switch r.State {
	case "ACKED", "SENT":
		return RecoveryPermanent, r.State
	case "REJECTED_OLD_GENERATION":
		return RecoveryPermanent, r.State
	case "UNKNOWN_NEEDS_RECONCILE":
		return RecoveryManual, r.Reason
	case "SAFE_TO_CONTINUE":
		return RecoveryRetryable, r.Reason
	case "QUEUED":
		switch r.Reason {
		case "SEAT_BUSY", "SEAT_QUEUE", "INPUT_DRAFT":
			return RecoveryRetryable, r.Reason
		case "RESET_ADAPTER_UNIMPLEMENTED", "SUBMIT_ADAPTER_UNIMPLEMENTED", "DELIVERY_ADAPTER_UNIMPLEMENTED", "DEPENDENCY_UNAVAILABLE", "INPUT_UNVERIFIED", "BINDING_UNVERIFIED":
			return RecoveryDependency, r.Reason
		case "PERMISSION_DENIED", "CONFIGURATION_ERROR", "INVALID_CONFIG", "AUTH_REQUIRED", "STOP_REQUESTED", "USER_CLOSED", "SEAT_TERMINAL", "EXIT_CONFIRMED":
			return RecoveryPermanent, r.Reason
		}
	}
	return RecoveryManual, r.Reason
}

func latestRetry(ctx context.Context, tx *sql.Tx, id string) (RetryDecision, error) {
	var r RetryDecision
	var data, created string
	var seq int64
	err := tx.QueryRowContext(ctx, `SELECT seq, details, created_at FROM journal WHERE message_id = ? AND event = 'RECOVERY_DECIDED' ORDER BY seq DESC LIMIT 1`, id).Scan(&seq, &data, &created)
	if err != nil {
		return r, err
	}
	if err := json.Unmarshal([]byte(data), &r); err != nil {
		return r, fmt.Errorf("invalid recovery journal: %w", err)
	}
	r.JournalSeq = seq
	r.RecordedAt, err = time.Parse(time.RFC3339Nano, created)
	return r, err
}

func saveRetry(ctx context.Context, tx *sql.Tx, actor MessageActor, r RetryDecision, now time.Time) (RetryDecision, error) {
	r.JournalSeq, r.RecordedAt = 0, time.Time{}
	data, err := json.Marshal(r)
	if err != nil {
		return r, err
	}
	r.JournalSeq, err = appendJournal(ctx, tx, JournalEntry{RunID: actor.RunID, MessageID: r.MessageID, AgentID: actor.AgentID, Generation: actor.Generation, Event: "RECOVERY_DECIDED", Details: string(data), CreatedAt: now})
	r.RecordedAt = now.UTC()
	if err != nil {
		return r, err
	}
	var next any
	if r.NextAttemptAt != nil {
		next = timestamp(*r.NextAttemptAt)
	}
	_, err = tx.ExecContext(ctx, `UPDATE deliveries SET next_retry_at = ? WHERE message_id = ?`, next, r.MessageID)
	return r, err
}

func terminalRetry(r RetryDecision) bool {
	return r.State == "STOPPED" || r.State == "EXHAUSTED" || r.State == "MANUAL_RECONCILE" || r.State == "COMPLETE"
}

func setRetryOutcome(r *RetryDecision, class, reason string, now time.Time) {
	r.Classification, r.Reason, r.NextAttemptAt = class, reason, nil
	switch class {
	case RecoveryPermanent:
		r.State = "STOPPED"
		if reason == "SENT" || reason == "ACKED" {
			r.State = "COMPLETE"
		}
	case RecoveryManual:
		r.State = "MANUAL_RECONCILE"
	default:
		if r.Attempts >= r.Policy.MaxAttempts {
			r.State = "EXHAUSTED"
		} else {
			r.State = "WAITING"
			next := now.UTC().Add(r.Policy.Backoff[r.Attempts])
			r.NextAttemptAt = &next
		}
	}
}

// ReserveRecovery reconciles and serializes one finite recovery opportunity. A
// repeated WAITING/terminal query returns the original receipt. ATTEMPT_RESERVED
// survives crashes and cannot be reclaimed: explicit reconciliation is required.
func (s *Store) ReserveRecovery(ctx context.Context, actor MessageActor, id string, policy RetryPolicy, now time.Time, cancelled bool) (RetryDecision, bool, error) {
	if err := policy.Validate(); err != nil {
		return RetryDecision{}, false, err
	}
	if now.IsZero() {
		return RetryDecision{}, false, errors.New("recovery clock must return a nonzero time")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return RetryDecision{}, false, err
	}
	defer tx.Rollback()
	evidence, err := reconcileSessionWithRelocation(ctx, tx, actor, id, false)
	if err != nil {
		return RetryDecision{}, false, err
	}
	previous, err := latestRetry(ctx, tx, id)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return RetryDecision{}, false, err
	}
	fresh := errors.Is(err, sql.ErrNoRows)
	r := previous
	if fresh {
		r = RetryDecision{MessageID: id, Policy: policy}
	}
	r.Reconcile = evidence
	class, reason := ClassifyRecovery(evidence)
	claimed := false
	switch {
	case cancelled && !evidence.StopRequested:
		class, reason = RecoveryPermanent, "CONTEXT_CANCELLED"
		setRetryOutcome(&r, class, reason, now)
	case class == RecoveryPermanent || class == RecoveryManual:
		setRetryOutcome(&r, class, reason, now)
	case !fresh && terminalRetry(previous):
		return previous, false, tx.Commit()
	case !fresh && previous.State == "ATTEMPT_RESERVED":
		// Another live caller may own it. Return its receipt without executing.
		return previous, false, tx.Commit()
	case fresh:
		setRetryOutcome(&r, class, reason, now)
	case r.NextAttemptAt != nil && now.Before(*r.NextAttemptAt):
		if r.Reconcile.JournalSeq == previous.Reconcile.JournalSeq {
			return previous, false, tx.Commit()
		}
		// Evidence changes update the reason without shortening the existing wait.
		r.Classification, r.Reason = class, reason
	default:
		r.State, r.Classification, r.Reason = "ATTEMPT_RESERVED", RecoveryManual, "RECOVERY_ATTEMPT_IN_FLIGHT_OR_INTERRUPTED"
		r.Attempts++
		r.NextAttemptAt, claimed = nil, true
	}
	// Deduplicate unchanged terminal decisions as well as waiting decisions.
	if !fresh && r.State == previous.State && r.Reason == previous.Reason && r.Reconcile.JournalSeq == previous.Reconcile.JournalSeq {
		return previous, false, tx.Commit()
	}
	r, err = saveRetry(ctx, tx, actor, r, now)
	if err != nil {
		return r, false, err
	}
	return r, claimed, tx.Commit()
}

// FinishRecovery accepts only the holder of the latest durable reservation.
// Current native evidence and stop intent override a caller's dependency result.
func (s *Store) FinishRecovery(ctx context.Context, actor MessageActor, reserved RetryDecision, class, reason string, now time.Time) (RetryDecision, error) {
	switch class {
	case RecoveryRetryable, RecoveryDependency, RecoveryPermanent, RecoveryManual:
	default:
		return RetryDecision{}, errors.New("invalid recovery classification")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return RetryDecision{}, err
	}
	defer tx.Rollback()
	evidence, err := reconcileSessionWithRelocation(ctx, tx, actor, reserved.MessageID, false)
	if err != nil {
		return RetryDecision{}, err
	}
	r, err := latestRetry(ctx, tx, reserved.MessageID)
	if err != nil {
		return r, err
	}
	if r.JournalSeq != reserved.JournalSeq || r.State != "ATTEMPT_RESERVED" {
		return r, tx.Commit()
	}
	r.Reconcile = evidence
	if c, why := ClassifyRecovery(evidence); c == RecoveryPermanent || c == RecoveryManual {
		class, reason = c, why
	}
	setRetryOutcome(&r, class, reason, now)
	r, err = saveRetry(ctx, tx, actor, r, now)
	if err != nil {
		return r, err
	}
	return r, tx.Commit()
}
