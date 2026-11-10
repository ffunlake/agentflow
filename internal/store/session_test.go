package store

import (
	"errors"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
)

func coordinatedCommand() MessageCommand {
	c := messageCommand()
	c.Session = "fresh"
	return c
}

func TestResetConfirmationAtomicRollbackAndReopen(t *testing.T) {
	for _, stage := range []string{"seat", "delivery", "journal"} {
		t.Run(stage, func(t *testing.T) {
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
			w, err := s.SessionWork(t.Context(), r.Message.ID)
			if err != nil {
				t.Fatal(err)
			}
			w, claimed, err := s.BeginSessionAction(t.Context(), r.Message.ID, w.Seat, "reset", "")
			if err != nil || !claimed {
				t.Fatal(err, claimed)
			}
			next := w.Seat
			next.Generation++
			next.NativeSessionID = "new-session"
			statement := `CREATE TRIGGER inject_failure BEFORE INSERT ON seats BEGIN SELECT RAISE(ABORT, 'injected'); END`
			switch stage {
			case "delivery":
				statement = `CREATE TRIGGER inject_failure BEFORE UPDATE ON deliveries BEGIN SELECT RAISE(ABORT, 'injected'); END`
			case "journal":
				statement = `CREATE TRIGGER inject_failure BEFORE INSERT ON journal BEGIN SELECT RAISE(ABORT, 'injected'); END`
			}
			if _, err := s.db.Exec(statement); err != nil {
				t.Fatal(err)
			}
			if err := s.ConfirmSessionReset(t.Context(), r.Message.ID, w.Seat, next); err == nil {
				t.Fatal("injected failure accepted")
			}
			still, err := s.SessionWork(t.Context(), r.Message.ID)
			if err != nil || !reflect.DeepEqual(w, still) {
				t.Fatalf("partial commit: %+v %v", still, err)
			}
			if _, err := s.db.Exec(`DROP TRIGGER inject_failure`); err != nil {
				t.Fatal(err)
			}
			if err := s.ConfirmSessionReset(t.Context(), r.Message.ID, w.Seat, next); err != nil {
				t.Fatal(err)
			}
			if err := s.ConfirmSessionReset(t.Context(), r.Message.ID, w.Seat, next); err == nil {
				t.Fatal("late READY accepted")
			}
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			s = openTestStore(t, path)
			again, err := s.StoreMessage(t.Context(), coordinatedCommand())
			if err != nil || again.Message.ID != r.Message.ID || again.Delivery.TargetGeneration != 2 || again.Delivery.ResetPhase != "READY" || again.Delivery.Attempt != 0 {
				t.Fatalf("%+v %v", again, err)
			}
			old, err := s.GetSeat(t.Context(), "run-1", "builder", 1)
			if err != nil || old.Current {
				t.Fatalf("old binding: %+v %v", old, err)
			}
			_, err = s.Ack(t.Context(), builderMessageActor(), "old-ack", r.Message.ID)
			requireMessageCode(t, err, "GENERATION_MISMATCH")
			assertCounts(t, s, 1, 1, 5) // duplicate READY and old ACK are audited
		})
	}
}

func TestSessionActionClaimOnceAcrossStoresAndStop(t *testing.T) {
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
	w, err := s.SessionWork(t.Context(), r.Message.ID)
	if err != nil {
		t.Fatal(err)
	}
	other := openTestStore(t, path)
	var count atomic.Int32
	var wg sync.WaitGroup
	for i := range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			db := s
			if i%2 == 1 {
				db = other
			}
			_, claimed, err := db.BeginSessionAction(t.Context(), r.Message.ID, w.Seat, "reset", "")
			if err != nil {
				t.Error(err)
			}
			if claimed {
				count.Add(1)
			}
		}()
	}
	wg.Wait()
	if count.Load() != 1 {
		t.Fatalf("claims: %d", count.Load())
	}
	if _, err := s.RequestStop(t.Context(), "run-1"); err != nil {
		t.Fatal(err)
	}
	c := coordinatedCommand()
	c.RequestID = "second"
	r, err = s.StoreMessage(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	w, claimed, err := s.BeginSessionAction(t.Context(), r.Message.ID, w.Seat, "reset", "")
	if err != nil || claimed || w.Record.Coordination != "STOP_REQUESTED" || w.Record.Delivery.Phase != "QUEUED" {
		t.Fatalf("%+v %t %v", w, claimed, err)
	}
}

func TestResetRejectsBindingReuseAndMissingIntent(t *testing.T) {
	s := newTestStore(t)
	seedStore(t, s)
	r, err := s.StoreMessage(t.Context(), coordinatedCommand())
	if err != nil {
		t.Fatal(err)
	}
	w, err := s.SessionWork(t.Context(), r.Message.ID)
	if err != nil {
		t.Fatal(err)
	}
	next := w.Seat
	next.Generation++
	next.NativeSessionID = "new-native"
	if err := s.ConfirmSessionReset(t.Context(), r.Message.ID, w.Seat, next); err == nil {
		t.Fatal("READY without intent accepted")
	}
	if _, claimed, err := s.BeginSessionAction(t.Context(), r.Message.ID, w.Seat, "reset", ""); err != nil || !claimed {
		t.Fatal(err)
	}
	for _, mode := range []string{"same", "other-seat", "wrong-surface", "wrong-generation"} {
		bad := next
		switch mode {
		case "same":
			bad.NativeSessionID = w.Seat.NativeSessionID
		case "other-seat":
			bad.NativeSessionID = "native-main"
		case "wrong-surface":
			bad.SurfaceID = "other"
		case "wrong-generation":
			bad.Generation++
		}
		if err := s.ConfirmSessionReset(t.Context(), r.Message.ID, w.Seat, bad); err == nil {
			t.Fatal("accepted", mode)
		}
	}
	_, err = s.Ack(t.Context(), builderMessageActor(), "premature", r.Message.ID)
	requireMessageCode(t, err, "SESSION_NOT_READY")
	c := coordinatedCommand()
	c.Body = "changed"
	if _, err := s.StoreMessage(t.Context(), c); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatal(err)
	}
}

func TestSubmitConfirmationRollbackAndReplyRelease(t *testing.T) {
	s := newTestStore(t)
	seedStore(t, s)
	c := coordinatedCommand()
	c.Session = "persistent"
	r, err := s.StoreMessage(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	w, err := s.SessionWork(t.Context(), r.Message.ID)
	if err != nil {
		t.Fatal(err)
	}
	w, claimed, err := s.BeginSessionAction(t.Context(), r.Message.ID, w.Seat, "submit", "")
	if err != nil || !claimed {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`CREATE TRIGGER inject_failure BEFORE INSERT ON journal BEGIN SELECT RAISE(ABORT, 'injected'); END`); err != nil {
		t.Fatal(err)
	}
	if err := s.ConfirmSessionSubmit(t.Context(), r.Message.ID, w.Seat, "fake"); err == nil {
		t.Fatal("partial submission accepted")
	}
	still, err := s.SessionWork(t.Context(), r.Message.ID)
	if err != nil || !reflect.DeepEqual(w, still) {
		t.Fatalf("%+v %v", still, err)
	}
	if _, err := s.db.Exec(`DROP TRIGGER inject_failure`); err != nil {
		t.Fatal(err)
	}
	if err := s.ConfirmSessionSubmit(t.Context(), r.Message.ID, w.Seat, "fake"); err != nil {
		t.Fatal(err)
	}
	reply := MessageCommand{Actor: builderMessageActor(), RequestID: "progress", Kind: "reply", ReplyTo: r.Message.ID, Body: "working", ReportStatus: "IN_PROGRESS"}
	if _, err := s.StoreMessage(t.Context(), reply); err != nil {
		t.Fatal(err)
	}
	reply.ReportStatus = "DONE" // retry must retain the originally nonterminal report
	if _, err := s.StoreMessage(t.Context(), reply); err != nil {
		t.Fatal(err)
	}
	seat, _ := s.GetSeat(t.Context(), "run-1", "builder", 1)
	if seat.CurrentMessageID != r.Message.ID || seat.Status != "BUSY" {
		t.Fatal("retry released active assignment")
	}
	reply.RequestID = "done"
	if _, err := s.StoreMessage(t.Context(), reply); err != nil {
		t.Fatal(err)
	}
	seat, _ = s.GetSeat(t.Context(), "run-1", "builder", 1)
	if seat.CurrentMessageID != "" || seat.Status != "READY" {
		t.Fatal("terminal result did not release active assignment")
	}
}

func TestSessionActionRevalidatesSenderInsideTransaction(t *testing.T) {
	s := newTestStore(t)
	seedStore(t, s)
	r, err := s.StoreMessage(t.Context(), coordinatedCommand())
	if err != nil {
		t.Fatal(err)
	}
	w, err := s.SessionWork(t.Context(), r.Message.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE seats SET is_current = 0 WHERE agent_id = 'main'`); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateSeat(t.Context(), Seat{RunID: "run-1", AgentID: "main", Generation: 2, Current: true, Status: "READY"}); err != nil {
		t.Fatal(err)
	}
	_, claimed, err := s.BeginSessionAction(t.Context(), r.Message.ID, w.Seat, "reset", "")
	requireMessageCode(t, err, "GENERATION_MISMATCH")
	if claimed {
		t.Fatal("stale sender claimed reset")
	}
	d, err := s.GetDelivery(t.Context(), r.Message.ID, 1)
	if err != nil || d.ResetPhase != "NONE" {
		t.Fatalf("%+v %v", d, err)
	}
}
