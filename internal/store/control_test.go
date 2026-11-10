package store

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
)

func TestStopIntentAtomicConcurrentAndReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	s := openTestStore(t, path)
	if err := s.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	seedStore(t, s)
	other, err := OpenExisting(path, true)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	// A journal failure must roll back the stop flag as well.
	if _, err := s.db.Exec(`CREATE TRIGGER reject_stop BEFORE INSERT ON journal WHEN NEW.event = 'STOP_REQUESTED' BEGIN SELECT RAISE(ABORT, 'fixture'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RequestStop(t.Context(), "run-1"); err == nil {
		t.Fatal("journal failure accepted")
	}
	run, _ := s.GetRun(t.Context())
	if run.StopRequested {
		t.Fatal("stop flag survived rollback")
	}
	if _, err := s.db.Exec(`DROP TRIGGER reject_stop`); err != nil {
		t.Fatal(err)
	}
	results := make(chan StopIntent, 12)
	errs := make(chan error, 12)
	var wg sync.WaitGroup
	for i := range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			db := s
			if i%2 == 0 {
				db = other
			}
			r, err := db.RequestStop(t.Context(), "run-1")
			results <- r
			errs <- err
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var first StopIntent
	for result := range results {
		if first.JournalSeq == 0 {
			first = result
		}
		if !reflect.DeepEqual(first, result) {
			t.Fatalf("unstable retry: %+v %+v", first, result)
		}
	}
	assertCounts(t, s, 0, 0, 1)
	if _, err := s.RequestStop(t.Context(), "wrong-run"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal(err)
	}
	s.Close()
	other.Close()
	reopened, err := OpenExisting(path, true)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	got, err := reopened.RequestStop(t.Context(), "run-1")
	if err != nil || !reflect.DeepEqual(first, got) {
		t.Fatalf("reopen: %+v %v", got, err)
	}
}

func TestOpenExistingDoesNotCreateMigrateOrWriteReadOnly(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "missing.db")
	for _, writable := range []bool{false, true} {
		if s, err := OpenExisting(missing, writable); err == nil {
			s.Close()
			t.Fatal("created absent database")
		}
	}
	if _, err := os.Stat(missing); !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "state.db")
	s := openTestStore(t, path)
	if existing, err := OpenExisting(path, false); err == nil {
		existing.Close()
		t.Fatal("accepted unmigrated schema")
	}
	if err := s.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	seedStore(t, s)
	ro, err := OpenExisting(path, false)
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close()
	if _, _, err := ro.ReadState(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := ro.RequestStop(t.Context(), "run-1"); err == nil {
		t.Fatal("read-only store allowed stop")
	}
	run, _ := s.GetRun(t.Context())
	if run.StopRequested {
		t.Fatal("read-only query changed stop flag")
	}
}

func TestStopActorReceiptRollbackAndGeneration(t *testing.T) {
	s := newTestStore(t)
	seedStore(t, s)
	actor := mainMessageActor()
	if _, err := s.RequestStopAsActor(t.Context(), actor, ""); err == nil {
		t.Fatal("accepted empty request")
	}
	// A failure saving the request receipt must roll back both intent and event.
	if _, err := s.db.Exec(`CREATE TRIGGER reject_stop_receipt BEFORE INSERT ON journal WHEN NEW.event = 'STOP_REQUEST_ACCEPTED' BEGIN SELECT RAISE(ABORT, 'fixture'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RequestStopAsActor(t.Context(), actor, "stop"); err == nil {
		t.Fatal("receipt failure accepted")
	}
	run, err := s.GetRun(t.Context())
	if err != nil || run.StopRequested {
		t.Fatal(run, err)
	}
	assertCounts(t, s, 0, 0, 0)
	if _, err := s.db.Exec(`DROP TRIGGER reject_stop_receipt`); err != nil {
		t.Fatal(err)
	}
	first, err := s.RequestStopAsActor(t.Context(), actor, "stop")
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.RequestStopAsActor(t.Context(), actor, "stop")
	if err != nil || !reflect.DeepEqual(first, again) {
		t.Fatal(first, again, err)
	}
	assertCounts(t, s, 0, 0, 2)
	if _, err := s.db.Exec(`UPDATE seats SET is_current=0 WHERE run_id='run-1' AND agent_id='main'`); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateSeat(t.Context(), Seat{RunID: "run-1", AgentID: "main", Generation: 2, Current: true, Status: "READY"}); err != nil {
		t.Fatal(err)
	}
	_, err = s.RequestStopAsActor(t.Context(), actor, "stop")
	requireMessageCode(t, err, "GENERATION_MISMATCH")
	actor.Generation = 2
	_, err = s.RequestStopAsActor(t.Context(), actor, "stop")
	requireMessageCode(t, err, "GENERATION_MISMATCH")
	assertCounts(t, s, 0, 0, 2)
	current, err := s.RequestStopAsActor(t.Context(), actor, "new-stop")
	if err != nil || !reflect.DeepEqual(first, current) {
		t.Fatal(first, current, err)
	}
	assertCounts(t, s, 0, 0, 3)
}
