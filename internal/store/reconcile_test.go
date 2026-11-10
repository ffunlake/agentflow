package store

import (
	"path/filepath"
	"reflect"
	"sync"
	"testing"
)

func TestReconcileDurableDecisionMatrix(t *testing.T) {
	for _, tc := range []struct{ name, state, action string }{
		{"untouched", "SAFE_TO_CONTINUE", "RESET"},
		{"queued", "QUEUED", "NONE"},
		{"reset-interrupted", "UNKNOWN_NEEDS_RECONCILE", "NONE"},
		{"reset-unknown", "UNKNOWN_NEEDS_RECONCILE", "NONE"},
		{"ready", "SAFE_TO_CONTINUE", "SUBMIT"},
		{"submit-interrupted", "UNKNOWN_NEEDS_RECONCILE", "NONE"},
		{"submit-unknown", "UNKNOWN_NEEDS_RECONCILE", "NONE"},
		{"sent", "SENT", "NONE"},
		{"acked", "ACKED", "NONE"},
		{"stop", "QUEUED", "NONE"},
		{"journal-intent", "UNKNOWN_NEEDS_RECONCILE", "NONE"},
		{"ready-without-journal", "UNKNOWN_NEEDS_RECONCILE", "NONE"},
		{"sent-without-journal", "UNKNOWN_NEEDS_RECONCILE", "NONE"},
		{"acked-without-journal", "UNKNOWN_NEEDS_RECONCILE", "NONE"},
	} {
		t.Run(tc.name, func(t *testing.T) {
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
			switch tc.name {
			case "queued":
				_, _, err = s.BeginSessionAction(t.Context(), r.Message.ID, w.Seat, "reset", "INPUT_DRAFT")
			case "stop":
				_, err = s.RequestStop(t.Context(), "run-1")
			case "ready-without-journal":
				_, err = s.db.Exec(`UPDATE deliveries SET reset_phase = 'READY'`)
			case "sent-without-journal":
				_, err = s.db.Exec(`UPDATE deliveries SET phase = 'SENT'`)
			case "acked-without-journal":
				_, err = s.db.Exec(`UPDATE deliveries SET phase = 'ACKED'`)
			case "untouched":
			default:
				_, claimed, claimErr := s.BeginSessionAction(t.Context(), r.Message.ID, w.Seat, "reset", "")
				if claimErr != nil || !claimed {
					t.Fatal(claimErr, claimed)
				}
				switch tc.name {
				case "reset-interrupted":
				case "reset-unknown":
					err = s.NoteSessionUnknown(t.Context(), r.Message.ID, "reset")
				case "journal-intent":
					_, err = s.db.Exec(`UPDATE deliveries SET reset_phase = 'NONE', phase = 'QUEUED'`)
				default:
					next := w.Seat
					next.Generation++
					next.NativeSessionID = "recovered-native"
					if err := s.ConfirmSessionReset(t.Context(), r.Message.ID, w.Seat, next); err != nil {
						t.Fatal(err)
					}
					if tc.name != "ready" {
						w, err = s.SessionWork(t.Context(), r.Message.ID)
						if err != nil {
							t.Fatal(err)
						}
						_, claimed, err = s.BeginSessionAction(t.Context(), r.Message.ID, w.Seat, "submit", "")
						if err != nil || !claimed {
							t.Fatal(err, claimed)
						}
						if tc.name == "submit-unknown" {
							err = s.NoteSessionUnknown(t.Context(), r.Message.ID, "submit")
						}
						if tc.name == "sent" || tc.name == "acked" {
							err = s.ConfirmSessionSubmit(t.Context(), r.Message.ID, w.Seat, "fake")
							if err != nil {
								t.Fatal(err)
							}
							if tc.name == "acked" {
								_, err = s.Ack(t.Context(), MessageActor{"run-1", "builder", 2}, "receipt", r.Message.ID)
							}
						}
					}
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			first, err := s.ReconcileSession(t.Context(), mainMessageActor(), r.Message.ID)
			if err != nil || first.State != tc.state || first.NextAction != tc.action || first.JournalSeq == 0 || first.RecordedAt.IsZero() || first.NativeSession == "" || first.StopRequested != (tc.name == "stop") {
				t.Fatalf("%+v %v", first, err)
			}
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			s = openTestStore(t, path)
			again, err := s.ReconcileSession(t.Context(), mainMessageActor(), r.Message.ID)
			if err != nil || !reflect.DeepEqual(first, again) {
				t.Fatalf("reopen changed decision: %+v %+v %v", first, again, err)
			}
			if tc.name == "journal-intent" {
				w, _ := s.SessionWork(t.Context(), r.Message.ID)
				if _, claimed, err := s.BeginSessionAction(t.Context(), r.Message.ID, w.Seat, "reset", ""); err != nil || claimed {
					t.Fatal("journal intent replayed", err)
				}
			}
		})
	}
}

func replaceRecoverySeat(t *testing.T, s *Store) {
	t.Helper()
	if _, err := s.db.Exec(`UPDATE seats SET is_current = 0 WHERE agent_id = 'builder'`); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateSeat(t.Context(), Seat{RunID: "run-1", AgentID: "builder", Generation: 2, Current: true, Status: "READY", Tool: "opencode", NativeSessionID: "new", WorkspaceID: "workspace-1", SurfaceID: "surface-builder"}); err != nil {
		t.Fatal(err)
	}
}

func TestReconcileRelocatesOnlyUntouchedQueuedWork(t *testing.T) {
	for _, mode := range []string{"queued", "pending", "reset", "submit", "ready", "journal-only"} {
		t.Run(mode, func(t *testing.T) {
			s := newTestStore(t)
			seedStore(t, s)
			c := coordinatedCommand()
			if mode == "submit" {
				c.Session = "persistent"
			}
			r, err := s.StoreMessage(t.Context(), c)
			if err != nil {
				t.Fatal(err)
			}
			w, _ := s.SessionWork(t.Context(), r.Message.ID)
			switch mode {
			case "queued":
				_, _, err = s.BeginSessionAction(t.Context(), r.Message.ID, w.Seat, "reset", "INPUT_DRAFT")
			case "reset", "journal-only":
				_, _, err = s.BeginSessionAction(t.Context(), r.Message.ID, w.Seat, "reset", "")
				if mode == "journal-only" {
					_, err = s.db.Exec(`UPDATE deliveries SET reset_phase = 'NONE', phase = 'QUEUED'`)
				}
			case "submit":
				_, _, err = s.BeginSessionAction(t.Context(), r.Message.ID, w.Seat, "submit", "")
			case "ready":
				_, err = s.db.Exec(`UPDATE deliveries SET reset_phase = 'READY', phase = 'QUEUED'`)
			}
			if err != nil {
				t.Fatal(err)
			}
			replaceRecoverySeat(t, s)
			decision, err := s.ReconcileSession(t.Context(), mainMessageActor(), r.Message.ID)
			if err != nil {
				t.Fatal(err)
			}
			want := int64(1)
			if mode == "queued" {
				want = 2
			} else if decision.State != "REJECTED_OLD_GENERATION" {
				t.Fatal(decision)
			}
			if decision.Delivery.TargetGeneration != want {
				t.Fatal(decision)
			}
			if mode != "queued" {
				current, _ := s.GetSeat(t.Context(), "run-1", "builder", 2)
				_, claimed, err := s.BeginSessionAction(t.Context(), r.Message.ID, current, "reset", "")
				if claimed {
					t.Fatal("stale action claimed", err)
				}
			}
		})
	}
}

func TestReconcileRollbackAndConcurrentReceipt(t *testing.T) {
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
	w, _ := s.SessionWork(t.Context(), r.Message.ID)
	if _, _, err := s.BeginSessionAction(t.Context(), r.Message.ID, w.Seat, "reset", "INPUT_DRAFT"); err != nil {
		t.Fatal(err)
	}
	replaceRecoverySeat(t, s)
	if _, err := s.db.Exec(`CREATE TRIGGER inject_failure BEFORE INSERT ON journal WHEN NEW.event = 'SESSION_RECONCILED' BEGIN SELECT RAISE(ABORT, 'injected'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReconcileSession(t.Context(), mainMessageActor(), r.Message.ID); err == nil {
		t.Fatal("failed journal committed")
	}
	still, _ := s.SessionWork(t.Context(), r.Message.ID)
	if still.Record.Delivery.TargetGeneration != 1 {
		t.Fatal("relocation partially committed")
	}
	if _, err := s.db.Exec(`DROP TRIGGER inject_failure`); err != nil {
		t.Fatal(err)
	}
	other := openTestStore(t, path)
	var wg sync.WaitGroup
	decisions := make(chan RecoveryDecision, 12)
	for i := range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			db := s
			if i%2 != 0 {
				db = other
			}
			d, err := db.ReconcileSession(t.Context(), mainMessageActor(), r.Message.ID)
			if err != nil {
				t.Error(err)
				return
			}
			decisions <- d
		}()
	}
	wg.Wait()
	close(decisions)
	var first RecoveryDecision
	for d := range decisions {
		if first.JournalSeq == 0 {
			first = d
		}
		if !reflect.DeepEqual(first, d) {
			t.Fatalf("concurrent receipt changed: %+v %+v", first, d)
		}
	}
	assertCounts(t, s, 1, 1, 4)
}

func TestLateResultsAreRejectedAndAuditedOnce(t *testing.T) {
	s := newTestStore(t)
	seedStore(t, s)
	r, err := s.StoreMessage(t.Context(), coordinatedCommand())
	if err != nil {
		t.Fatal(err)
	}
	w, _ := s.SessionWork(t.Context(), r.Message.ID)
	replaceRecoverySeat(t, s)
	for range 2 {
		_, err := s.Ack(t.Context(), builderMessageActor(), "late", r.Message.ID)
		requireMessageCode(t, err, "GENERATION_MISMATCH")
		_, err = s.StoreMessage(t.Context(), MessageCommand{Actor: builderMessageActor(), Kind: "reply", RequestID: "late-result", ReplyTo: r.Message.ID, Body: "private report", ReportStatus: "DONE"})
		requireMessageCode(t, err, "GENERATION_MISMATCH")
		err = s.ConfirmSessionSubmit(t.Context(), r.Message.ID, w.Seat, "late-channel")
		requireMessageCode(t, err, "GENERATION_MISMATCH")
	}
	assertCounts(t, s, 1, 1, 4)
	still, _ := s.SessionWork(t.Context(), r.Message.ID)
	if still.Record.Delivery.Phase != "PENDING" || still.Seat.CurrentMessageID != "" {
		t.Fatal(still)
	}
}

func TestStopPreventsRelocationAtActionBoundary(t *testing.T) {
	s := newTestStore(t)
	seedStore(t, s)
	r, err := s.StoreMessage(t.Context(), coordinatedCommand())
	if err != nil {
		t.Fatal(err)
	}
	w, _ := s.SessionWork(t.Context(), r.Message.ID)
	if _, _, err := s.BeginSessionAction(t.Context(), r.Message.ID, w.Seat, "reset", "INPUT_DRAFT"); err != nil {
		t.Fatal(err)
	}
	replaceRecoverySeat(t, s)
	if _, err := s.RequestStop(t.Context(), "run-1"); err != nil {
		t.Fatal(err)
	}
	current, _ := s.GetSeat(t.Context(), "run-1", "builder", 2)
	w, claimed, err := s.BeginSessionAction(t.Context(), r.Message.ID, current, "reset", "")
	if err != nil || claimed || w.Record.Delivery.TargetGeneration != 1 || w.Record.Coordination != "STOP_REQUESTED" {
		t.Fatalf("%+v %v %v", w, claimed, err)
	}
	d, err := s.ReconcileSession(t.Context(), mainMessageActor(), r.Message.ID)
	if err != nil || !d.StopRequested || d.Delivery.TargetGeneration != 1 || d.NextAction != "NONE" {
		t.Fatalf("%+v %v", d, err)
	}
}
