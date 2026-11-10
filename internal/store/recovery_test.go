package store

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestRecoveryOldGenerationAndPermanentReasonsAreTerminal(t *testing.T) {
	for _, gate := range []string{"old-queued", "old-pending", "PERMISSION_DENIED", "CONFIGURATION_ERROR", "AUTH_REQUIRED", "UNRECOGNIZED_FAILURE"} {
		t.Run(gate, func(t *testing.T) {
			s := newTestStore(t)
			seedStore(t, s)
			r, err := s.StoreMessage(t.Context(), coordinatedCommand())
			if err != nil {
				t.Fatal(err)
			}
			w, _ := s.SessionWork(t.Context(), r.Message.ID)
			if gate != "old-pending" {
				if _, _, err := s.BeginSessionAction(t.Context(), r.Message.ID, w.Seat, "reset", gate); err != nil {
					t.Fatal(err)
				}
			}
			if gate == "old-queued" || gate == "old-pending" {
				replaceRecoverySeat(t, s)
			}
			policy := RetryPolicy{MaxAttempts: 2, Backoff: []time.Duration{time.Second, 2 * time.Second}}
			now := time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)
			d, claimed, err := s.ReserveRecovery(t.Context(), mainMessageActor(), r.Message.ID, policy, now, false)
			want := "STOPPED"
			if gate == "UNRECOGNIZED_FAILURE" {
				want = "MANUAL_RECONCILE"
			}
			if err != nil || claimed || d.State != want || d.Attempts != 0 || d.NextAttemptAt != nil {
				t.Fatal(d, claimed, err)
			}
			if gate == "old-queued" || gate == "old-pending" {
				if d.Reconcile.State != "REJECTED_OLD_GENERATION" || d.Reconcile.Delivery.TargetGeneration != 1 {
					t.Fatal("old work relocated", d)
				}
			}
			var count int
			if err := s.db.QueryRow(`SELECT count(*) FROM journal WHERE message_id = ? AND event = 'RECOVERY_DECIDED'`, r.Message.ID).Scan(&count); err != nil {
				t.Fatal(err)
			}
			again, claimed, err := s.ReserveRecovery(t.Context(), mainMessageActor(), r.Message.ID, policy, now.Add(time.Hour), false)
			if err != nil || claimed || count != 1 || !reflect.DeepEqual(d, again) {
				t.Fatal(d, again, claimed, count, err)
			}
		})
	}
}

func TestRecoveryReservationSurvivesReopenAndRetryTimestampIsAtomic(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	s := openTestStore(t, path)
	if err := s.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	seedStore(t, s)
	r, err := s.StoreMessage(t.Context(), coordinatedCommand())
	if err != nil {
		t.Fatal(err)
	}
	policy := RetryPolicy{MaxAttempts: 2, Backoff: []time.Duration{time.Second, 5 * time.Second}}
	now := time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)
	d, _, err := s.ReserveRecovery(t.Context(), mainMessageActor(), r.Message.ID, policy, now, false)
	if err != nil {
		t.Fatal(err)
	}
	var stored string
	if err := s.db.QueryRow(`SELECT next_retry_at FROM deliveries WHERE message_id = ?`, r.Message.ID).Scan(&stored); err != nil || stored != timestamp(*d.NextAttemptAt) {
		t.Fatal(stored, d, err)
	}
	now = *d.NextAttemptAt
	reserved, claimed, err := s.ReserveRecovery(t.Context(), mainMessageActor(), r.Message.ID, policy, now, false)
	if err != nil || !claimed || reserved.State != "ATTEMPT_RESERVED" {
		t.Fatal(reserved, claimed, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s = openTestStore(t, path)
	again, claimed, err := s.ReserveRecovery(t.Context(), mainMessageActor(), r.Message.ID, policy, now.Add(time.Hour), false)
	if err != nil || claimed || !reflect.DeepEqual(reserved, again) || again.Classification != RecoveryManual {
		t.Fatal("interrupted attempt reclaimed", again, claimed, err)
	}
	var next any
	if err := s.db.QueryRow(`SELECT next_retry_at FROM deliveries WHERE message_id = ?`, r.Message.ID).Scan(&next); err != nil || next != nil {
		t.Fatal(next, err)
	}
}

func TestRecoveryJournalFailureRollsBackRetrySchedule(t *testing.T) {
	s := newTestStore(t)
	seedStore(t, s)
	r, err := s.StoreMessage(t.Context(), coordinatedCommand())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`CREATE TRIGGER recovery_fault BEFORE INSERT ON journal WHEN NEW.event = 'RECOVERY_DECIDED' BEGIN SELECT RAISE(ABORT, 'recovery fault'); END`); err != nil {
		t.Fatal(err)
	}
	_, claimed, err := s.ReserveRecovery(t.Context(), mainMessageActor(), r.Message.ID, RetryPolicy{MaxAttempts: 1, Backoff: []time.Duration{time.Second}}, time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC), false)
	if err == nil || claimed {
		t.Fatal("journal failure did not roll back", err)
	}
	var count int
	if err := s.db.QueryRow(`SELECT count(*) FROM journal WHERE event IN ('RECOVERY_DECIDED', 'SESSION_RECONCILED')`).Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
	var next any
	if err := s.db.QueryRow(`SELECT next_retry_at FROM deliveries WHERE message_id = ?`, r.Message.ID).Scan(&next); err != nil || next != nil {
		t.Fatal(next, err)
	}
}

func TestRecoveryRejectsWrongActorWithoutReceipt(t *testing.T) {
	s := newTestStore(t)
	seedStore(t, s)
	r, err := s.StoreMessage(t.Context(), coordinatedCommand())
	if err != nil {
		t.Fatal(err)
	}
	actor := mainMessageActor()
	actor.Generation++
	_, claimed, err := s.ReserveRecovery(context.Background(), actor, r.Message.ID, RetryPolicy{MaxAttempts: 1, Backoff: []time.Duration{time.Second}}, time.Now(), false)
	if err == nil || claimed {
		t.Fatal("invalid actor accepted")
	}
}
