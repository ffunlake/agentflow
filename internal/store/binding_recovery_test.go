package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func bindingStore(t *testing.T) (*Store, Seat, RetryPolicy, time.Time) {
	t.Helper()
	s := newTestStore(t)
	seedStore(t, s)
	seat, _ := s.GetSeat(t.Context(), "run-1", "builder", 1)
	return s, seat, RetryPolicy{MaxAttempts: 2, Backoff: []time.Duration{time.Second, 5 * time.Second}}, time.Date(2026, 11, 3, 18, 0, 0, 0, time.UTC)
}

func reserveBinding(t *testing.T, s *Store, seat Seat, request string, policy RetryPolicy, now time.Time) (BindingRecovery, bool) {
	t.Helper()
	r, claimed, err := s.ReserveBindingRecovery(t.Context(), MessageActor{RunID: seat.RunID, AgentID: "main", Generation: 1}, MessageActor{RunID: seat.RunID, AgentID: seat.AgentID, Generation: seat.Generation}, request, policy, now, false)
	if err != nil {
		t.Fatal(err)
	}
	return r, claimed
}

func freezeBindingLead(t *testing.T, s *Store) {
	t.Helper()
	if _, err := s.db.Exec(`UPDATE runs SET config_snapshot = '{"lead":"main"}'`); err != nil {
		t.Fatal(err)
	}
}

func TestBindingStoreHistoryCASAndRollback(t *testing.T) {
	s, seat, policy, now := bindingStore(t)
	freezeBindingLead(t, s)
	r, _ := reserveBinding(t, s, seat, "move", policy, now)
	r, claimed := reserveBinding(t, s, seat, "move", policy, *r.NextAttemptAt)
	if !claimed {
		t.Fatal(r)
	}
	next := seat
	next.WorkspaceID, next.SurfaceID = "workspace-new", "surface-new"
	if _, err := s.db.Exec(`CREATE TRIGGER fail_binding_journal BEFORE INSERT ON journal WHEN NEW.event = 'BINDING_CHANGED' BEGIN SELECT RAISE(ABORT,'injected binding failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.FinishBindingRecovery(t.Context(), r, "COMPLETE", "READY", &next, now); err == nil {
		t.Fatal("expected journal rollback")
	}
	unchanged, _ := s.GetSeat(t.Context(), seat.RunID, seat.AgentID, seat.Generation)
	if !reflect.DeepEqual(seat, unchanged) {
		t.Fatal("binding committed without history", unchanged)
	}
	if _, err := s.db.Exec(`DROP TRIGGER fail_binding_journal`); err != nil {
		t.Fatal(err)
	}
	done, err := s.FinishBindingRecovery(t.Context(), r, "COMPLETE", "READY", &next, now)
	if err != nil || done.State != "COMPLETE" {
		t.Fatal(done, err)
	}
	var data string
	if err := s.db.QueryRow(`SELECT details FROM journal WHERE event = 'BINDING_CHANGED'`).Scan(&data); err != nil {
		t.Fatal(err)
	}
	var history struct{ Previous, Current Seat }
	if err := json.Unmarshal([]byte(data), &history); err != nil || !sameSession(history.Previous, seat) || !sameSession(history.Current, next) {
		t.Fatal("binding history lost", data, err)
	}
	again, err := s.FinishBindingRecovery(t.Context(), r, "COMPLETE", "READY", &next, now)
	if err != nil || !reflect.DeepEqual(done, again) {
		t.Fatal("CAS duplicate changed receipt", again, err)
	}
	if err := s.RecordBindingExit(t.Context(), seat, "USER_CLOSED", now); err == nil {
		t.Fatal("stale location accepted")
	}
	if err := s.RecordBindingExit(t.Context(), seat, "USER_CLOSED", now); err == nil {
		t.Fatal("stale duplicate accepted")
	}
	var rejected int
	_ = s.db.QueryRow(`SELECT count(*) FROM journal WHERE event = 'BINDING_EVENT_REJECTED'`).Scan(&rejected)
	if rejected != 1 {
		t.Fatal("stale event audit not idempotent", rejected)
	}
}

func TestBindingStoreCreationIntentReopenAndNoStealing(t *testing.T) {
	s, seat, policy, now := bindingStore(t)
	freezeBindingLead(t, s)
	r, _ := reserveBinding(t, s, seat, "replace", policy, now)
	r, claimed := reserveBinding(t, s, seat, "replace", policy, *r.NextAttemptAt)
	if !claimed {
		t.Fatal(r)
	}
	r, err := s.FinishBindingRecovery(t.Context(), r, "CREATE_REQUESTED", "ABSENCE_RECONCILED", nil, now)
	if err != nil {
		t.Fatal(err)
	}
	// Read the database path without assuming a temporary-directory spelling.
	var seq int
	var name, path string
	if err := s.db.QueryRow(`PRAGMA database_list`).Scan(&seq, &name, &path); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = OpenExisting(filepath.Clean(path), true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, request := range []string{"replace", "new-request-cannot-steal"} {
		again, claimed := reserveBinding(t, s, seat, request, policy, now.Add(time.Hour))
		if claimed || !reflect.DeepEqual(again, r) {
			t.Fatal("creation intent reclaimed", again)
		}
	}
}

func TestBindingStoreBudgetAndStopCloseRace(t *testing.T) {
	for _, gate := range []string{"budget", "stop", "close", "unknown", "terminal"} {
		t.Run(gate, func(t *testing.T) {
			s, seat, policy, now := bindingStore(t)
			freezeBindingLead(t, s)
			r, _ := reserveBinding(t, s, seat, "one", policy, now)
			r, claimed := reserveBinding(t, s, seat, "one", policy, *r.NextAttemptAt)
			if !claimed {
				t.Fatal(r)
			}
			if gate == "budget" {
				r, err := s.FinishBindingRecovery(t.Context(), r, "WAITING", "DEPENDENCY_UNAVAILABLE", nil, now)
				if err != nil {
					t.Fatal(err)
				}
				// A different request cannot shorten the original deadline.
				other, c := reserveBinding(t, s, seat, "two", RetryPolicy{1, []time.Duration{time.Nanosecond}}, now)
				if c || !reflect.DeepEqual(r, other) {
					t.Fatal("policy changed", other)
				}
				r, claimed = reserveBinding(t, s, seat, "one", policy, *r.NextAttemptAt)
				if !claimed || r.Attempts != 2 {
					t.Fatal(r)
				}
				r, err = s.FinishBindingRecovery(t.Context(), r, "WAITING", "DEPENDENCY_UNAVAILABLE", nil, now)
				if err != nil || r.State != "EXHAUSTED" {
					t.Fatal(r, err)
				}
				other, c = reserveBinding(t, s, seat, "three", policy, now.Add(time.Hour))
				if c || other.State != "EXHAUSTED" || other.Attempts != 2 || other.Policy.MaxAttempts != 2 {
					t.Fatal(other)
				}
				return
			}
			if gate == "stop" {
				if _, err := s.RequestStop(t.Context(), seat.RunID); err != nil {
					t.Fatal(err)
				}
			} else {
				reason := "USER_CLOSED"
				if gate == "unknown" {
					reason = ""
				}
				if gate == "terminal" {
					reason = "EXIT_CONFIRMED"
				}
				if err := s.RecordBindingExit(t.Context(), seat, reason, now); err != nil {
					t.Fatal(err)
				}
			}
			next := seat
			next.SurfaceID = "new-surface"
			d, err := s.FinishBindingRecovery(t.Context(), r, "COMPLETE", "READY", &next, now)
			if err != nil || (d.State != "STOPPED" && d.State != "MANUAL_RECONCILE") {
				t.Fatal(d, err)
			}
			current, _ := s.GetSeat(t.Context(), seat.RunID, seat.AgentID, seat.Generation)
			if !sameSession(seat, current) {
				t.Fatal("gate failed binding CAS", current)
			}
		})
	}
}

func TestBindingStoreNativeCollisionAndNoGenerationWithoutIntent(t *testing.T) {
	s, seat, policy, now := bindingStore(t)
	freezeBindingLead(t, s)
	r, _ := reserveBinding(t, s, seat, "collision", policy, now)
	r, _ = reserveBinding(t, s, seat, "collision", policy, *r.NextAttemptAt)
	next := seat
	next.Generation++
	next.NativeSessionID = "new-thread"
	if _, err := s.FinishBindingRecovery(t.Context(), r, "COMPLETE", "READY", &next, now); err == nil {
		t.Fatal("generation advanced without creation intent")
	}
	next = seat
	next.SurfaceID = "surface-main"
	if _, err := s.FinishBindingRecovery(t.Context(), r, "COMPLETE", "READY", &next, now); err == nil {
		t.Fatal("other agent surface accepted")
	}
	if _, err := s.GetSeat(t.Context(), seat.RunID, seat.AgentID, 2); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("unexpected next generation", err)
	}
}
