package runtime

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"agentflow/internal/store"
)

func TestRecoveryAfterRestartDoesNotReplayUnknownActions(t *testing.T) {
	for _, action := range []string{"reset", "submit"} {
		t.Run(action, func(t *testing.T) {
			db, _, _, opts := sessionFixture(t)
			reset, submit := &fakeResetAdapter{}, &fakeSubmitAdapter{}
			if action == "reset" {
				reset.reset = func(context.Context, ResetRequest) (NativeBinding, error) {
					return NativeBinding{}, errors.New("response lost")
				}
			} else {
				submit.submit = func(context.Context, SeatSnapshot, store.Message) (Submission, error) {
					return Submission{}, errors.New("response lost")
				}
			}
			svc, _ := NewMessageServiceWithAdapters(db, reset, submit)
			r, err := svc.Assign(t.Context(), assignment())
			if err != nil {
				t.Fatal(err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			db, err = store.OpenExisting(filepath.Join(filepath.Dir(opts.SnapshotPath), "state.db"), true)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			cleanReset, cleanSubmit := &fakeResetAdapter{}, &fakeSubmitAdapter{}
			svc, _ = NewMessageServiceWithAdapters(db, cleanReset, cleanSubmit)
			first, err := svc.Reconcile(t.Context(), assignment().Actor, r.Message.ID)
			if err != nil || first.State != "UNKNOWN_NEEDS_RECONCILE" || first.NextAction != "NONE" {
				t.Fatalf("%+v %v", first, err)
			}
			again, err := svc.Resume(t.Context(), assignment().Actor, r.Message.ID)
			if err != nil || !reflect.DeepEqual(first, again) {
				t.Fatalf("%+v %v", again, err)
			}
			for range 2 {
				if _, err := svc.Assign(t.Context(), assignment()); err != nil {
					t.Fatal(err)
				}
				if _, err := svc.sessions.Advance(t.Context(), assignment().Actor, r.Message.ID); err != nil {
					t.Fatal(err)
				}
			}
			if cleanReset.inspections.Load() != 0 || cleanReset.resets.Load() != 0 || cleanReset.waits.Load() != 0 || cleanSubmit.calls.Load() != 0 {
				t.Fatal("recovery called a native adapter")
			}
		})
	}
}

func TestResumeReadyDecisionThenAdvanceSubmitsOnce(t *testing.T) {
	db, _, _, opts := sessionFixture(t)
	reset := &fakeResetAdapter{}
	svc, _ := NewMessageServiceWithAdapters(db, reset, nil)
	r, err := svc.Assign(t.Context(), assignment())
	if err != nil || r.Delivery.ResetPhase != "READY" || reset.resets.Load() != 1 {
		t.Fatalf("%+v %v", r, err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = store.OpenExisting(filepath.Join(filepath.Dir(opts.SnapshotPath), "state.db"), true)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	reset, submit := &fakeResetAdapter{}, &fakeSubmitAdapter{}
	svc, _ = NewMessageServiceWithAdapters(db, reset, submit)
	d, err := svc.Resume(t.Context(), assignment().Actor, r.Message.ID)
	if err != nil || d.State != "QUEUED" || d.Delivery.ResetPhase != "READY" || d.Generation != 2 || submit.calls.Load() != 0 || reset.inspections.Load() != 0 {
		t.Fatalf("%+v %v", d, err)
	}
	for range 2 {
		r, err = svc.sessions.Advance(t.Context(), assignment().Actor, r.Message.ID)
		if err != nil || r.Delivery.Phase != "SENT" {
			t.Fatalf("%+v %v", r, err)
		}
	}
	if reset.resets.Load() != 0 || submit.calls.Load() != 1 {
		t.Fatal("confirmed reset or submission repeated")
	}
	d, err = svc.Reconcile(t.Context(), assignment().Actor, r.Message.ID)
	if err != nil || d.State != "SENT" {
		t.Fatalf("%+v %v", d, err)
	}
}

func TestRecoveryHonorsStopAndAuthorizesSender(t *testing.T) {
	db, _, _, _ := sessionFixture(t)
	svc, _ := NewMessageService(db)
	r, err := svc.Assign(t.Context(), assignment())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.RequestStop(t.Context(), "run-test"); err != nil {
		t.Fatal(err)
	}
	reset, submit := &fakeResetAdapter{}, &fakeSubmitAdapter{}
	svc, _ = NewMessageServiceWithAdapters(db, reset, submit)
	d, err := svc.Resume(t.Context(), assignment().Actor, r.Message.ID)
	if err != nil || !d.StopRequested || d.State != "QUEUED" || d.Reason != "STOP_REQUESTED" {
		t.Fatalf("%+v %v", d, err)
	}
	if _, err := svc.sessions.Advance(t.Context(), assignment().Actor, r.Message.ID); err != nil {
		t.Fatal(err)
	}
	if reset.inspections.Load() != 0 || reset.resets.Load() != 0 || submit.calls.Load() != 0 {
		t.Fatal("stopped run resumed")
	}
	bad := assignment().Actor
	bad.AgentID = "reviewer"
	_, err = svc.Reconcile(t.Context(), bad, r.Message.ID)
	requireSessionCode(t, err, "ACTOR_MISMATCH")
	bad = assignment().Actor
	bad.Generation++
	_, err = svc.Resume(t.Context(), bad, r.Message.ID)
	requireSessionCode(t, err, "GENERATION_MISMATCH")
}

func TestAdvanceReconcilesBeforeAnyNativeAction(t *testing.T) {
	db, _, _, _ := sessionFixture(t)
	reset, submit := &fakeResetAdapter{}, &fakeSubmitAdapter{}
	reset.reset = func(ctx context.Context, r ResetRequest) (NativeBinding, error) {
		events, err := db.JournalAfter(ctx, "run-test", 0)
		if err != nil {
			t.Fatal(err)
		}
		var reconcile, request int64
		for _, e := range events {
			if e.MessageID != r.MessageID {
				continue
			}
			if e.Event == "SESSION_RECONCILED" {
				reconcile = e.Seq
			}
			if e.Event == "RESET_REQUESTED" {
				request = e.Seq
			}
		}
		if reconcile == 0 || request <= reconcile {
			t.Fatal("native action preceded durable reconciliation", events)
		}
		n := r.Previous.Native
		n.SessionID = "reconciled-native"
		return n, nil
	}
	svc, _ := NewMessageServiceWithAdapters(db, reset, submit)
	if _, err := svc.Assign(t.Context(), assignment()); err != nil {
		t.Fatal(err)
	}
}
