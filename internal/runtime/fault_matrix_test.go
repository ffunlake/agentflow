package runtime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	runcontext "agentflow/internal/context"
	"agentflow/internal/ipc"
	"agentflow/internal/store"
)

// This matrix exercises local contracts only. All native actions use the existing
// fake adapters; an empty PATH also prevents accidental native tool discovery.
func TestFaultMatrixSession(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	for _, tc := range []struct {
		fault, phase, reset, reason, decision string
		generation                            int64
		resets, submits                       int32
	}{
		{"busy", "QUEUED", "NONE", "SEAT_BUSY", "QUEUED", 1, 0, 0},
		{"draft", "QUEUED", "NONE", "INPUT_DRAFT", "QUEUED", 1, 0, 0},
		{"input-unknown", "QUEUED", "NONE", "INPUT_UNVERIFIED", "QUEUED", 1, 0, 0},
		{"reset-unknown", "PENDING", "RESET_REQUESTED", "UNVERIFIED", "UNKNOWN_NEEDS_RECONCILE", 1, 1, 0},
		{"ready-late", "PENDING", "RESET_REQUESTED", "UNVERIFIED", "UNKNOWN_NEEDS_RECONCILE", 1, 1, 0},
		{"submit-unknown", "SUBMIT_REQUESTED", "READY", "UNVERIFIED", "UNKNOWN_NEEDS_RECONCILE", 2, 1, 1},
		{"stop", "PENDING", "NONE", "", "QUEUED", 1, 0, 0},
		{"duplicate-request", "SENT", "READY", "", "SENT", 2, 1, 1},
		{"late-ack-result", "SENT", "READY", "", "SENT", 2, 1, 1},
	} {
		t.Run(tc.fault, func(t *testing.T) {
			db, _, _, opts := sessionFixture(t)
			reset, submit := &fakeResetAdapter{}, &fakeSubmitAdapter{}
			const draft = " \n用户尚未提交的草稿"
			reset.inspect = func(_ context.Context, seat SeatSnapshot) (InputState, error) {
				x := InputState{Actor: seatActor(seat), Native: seat.Native, Verified: true}
				switch tc.fault {
				case "busy":
					x.Busy = true
				case "draft":
					x.Draft = draft
				case "input-unknown":
					x.Verified = false
				}
				return x, nil
			}
			if tc.fault == "reset-unknown" {
				reset.reset = func(context.Context, ResetRequest) (NativeBinding, error) {
					return NativeBinding{}, errors.New("response lost after reset")
				}
			}
			if tc.fault == "ready-late" {
				reset.wait = func(_ context.Context, r ResetRequest, n NativeBinding) (Registration, error) {
					seat := r.Previous
					seat.Native = n // delayed READY still names the old generation
					return readyClaim(seat), nil
				}
			}
			if tc.fault == "submit-unknown" {
				submit.submit = func(context.Context, SeatSnapshot, store.Message) (Submission, error) {
					return Submission{}, errors.New("response lost after submission")
				}
			}
			if tc.fault == "stop" {
				first, err := db.RequestStop(t.Context(), "run-test")
				if err != nil {
					t.Fatal(err)
				}
				again, err := db.RequestStop(t.Context(), "run-test")
				if err != nil || !reflect.DeepEqual(first, again) {
					t.Fatal("stop receipt changed", again, err)
				}
			}
			svc, _ := NewMessageServiceWithAdapters(db, reset, submit)
			first, err := svc.Assign(t.Context(), assignment())
			if err != nil {
				t.Fatal(err)
			}
			if first.Delivery.Phase != tc.phase || first.Delivery.ResetPhase != tc.reset || first.Coordination != tc.reason || first.Delivery.TargetGeneration != tc.generation {
				t.Fatalf("unexpected durable outcome: %+v", first)
			}
			decision, err := svc.sessions.Reconcile(t.Context(), assignment().Actor, first.Message.ID)
			if err != nil || decision.State != tc.decision || decision.JournalSeq == 0 {
				t.Fatal(decision, err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			db, err = store.OpenExisting(filepath.Join(filepath.Dir(opts.SnapshotPath), "state.db"), true)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			svc, _ = NewMessageServiceWithAdapters(db, reset, submit)
			for range 2 {
				again, err := svc.Assign(t.Context(), assignment())
				if err != nil || !reflect.DeepEqual(first, again) {
					t.Fatal("retry changed saved work", again, err)
				}
				d, err := svc.sessions.Reconcile(t.Context(), assignment().Actor, first.Message.ID)
				if err != nil || !reflect.DeepEqual(decision, d) {
					t.Fatal("reopen/retry changed decision receipt", d, err)
				}
			}
			if tc.fault == "duplicate-request" {
				conflict := assignment()
				conflict.Body = "different body"
				if _, err := svc.Assign(t.Context(), conflict); !errors.Is(err, store.ErrIdempotencyConflict) {
					t.Fatal("changed body accepted", err)
				}
				actor := ipc.Actor{RunID: "run-test", AgentID: "builder", Generation: 2}
				ack, err := svc.Ack(t.Context(), actor, "receipt", first.Message.ID)
				if err != nil {
					t.Fatal(err)
				}
				for range 2 {
					again, err := svc.Ack(t.Context(), actor, "receipt", first.Message.ID)
					if err != nil || !reflect.DeepEqual(ack, again) {
						t.Fatal("duplicate ACK receipt", again, err)
					}
				}
			}
			if tc.fault == "late-ack-result" {
				for range 2 {
					_, err := svc.Ack(t.Context(), bindingTarget(), "late-ack", first.Message.ID)
					requireSessionCode(t, err, "GENERATION_MISMATCH")
					_, err = svc.Reply(t.Context(), ReplyMessage{Actor: bindingTarget(), RequestID: "late-result", MessageID: first.Message.ID, Status: "DONE", Body: "must not persist"})
					requireSessionCode(t, err, "GENERATION_MISMATCH")
				}
				inbox, err := svc.Inbox(t.Context(), assignment().Actor, 0, false)
				if err != nil || len(inbox) != 0 {
					t.Fatal("late result body persisted", inbox, err)
				}
			}
			message, err := db.GetMessage(t.Context(), first.Message.ID)
			if err != nil || message.Body != assignment().Body || message.RequestID != assignment().RequestID {
				t.Fatal("saved body lost", message, err)
			}
			seat, err := db.GetSeat(t.Context(), "run-test", "builder", tc.generation)
			if err != nil || !seat.Current || reset.resets.Load() != tc.resets || submit.calls.Load() != tc.submits {
				t.Fatal("binding advanced or native action replayed", seat, err)
			}
			if tc.fault == "draft" {
				x, err := reset.Inspect(t.Context(), sessionSeat(seat))
				if err != nil || x.Draft != draft {
					t.Fatal("draft changed", x, err)
				}
			}
			counts := faultJournalCounts(t, db, "run-test", first.Message.ID)
			if counts["MESSAGE_STORED"] != 1 || counts["RESET_REQUESTED"] != int(tc.resets) || counts["SUBMIT_REQUESTED"] != int(tc.submits) {
				t.Fatal("duplicate/missing durable action intent", counts)
			}
			wantAck, wantRejected := 0, 0
			if tc.fault == "duplicate-request" {
				wantAck = 1
			}
			if tc.fault == "late-ack-result" {
				wantRejected = 2 // ACK and result are each audited once
			}
			if counts["MESSAGE_ACKED"] != wantAck || counts["SESSION_RESULT_REJECTED"] != wantRejected {
				t.Fatal("ACK/rejection journal mismatch", counts)
			}
		})
	}
}

func faultJournalCounts(t *testing.T, db *store.Store, runID, messageID string) map[string]int {
	t.Helper()
	journal, err := db.JournalAfter(t.Context(), runID, 0)
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	var previous int64
	for _, event := range journal {
		if event.Seq <= previous {
			t.Fatal("journal is not append ordered", journal)
		}
		previous = event.Seq
		if event.MessageID == messageID {
			counts[event.Event]++
		}
	}
	return counts
}

func TestFaultMatrixRecoveryBudgetAndStop(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	for _, fault := range []string{"budget-exhausted", "stop-after-wait"} {
		t.Run(fault, func(t *testing.T) {
			db, id, reset, submit, sessions, opts := recoveryFixture(t)
			now := time.Date(2026, 11, 3, 18, 0, 0, 0, time.UTC)
			checks := 0
			dep := dependencyFunc(func(context.Context, store.RecoveryDecision) (RecoveryReadiness, error) {
				checks++
				return RecoveryReadiness{Classification: store.RecoveryDependency, Reason: "DEPENDENCY_UNAVAILABLE"}, nil
			})
			r := recoveryRunner(t, sessions, &now, dep)
			d := recoverTick(t, r, id)
			if fault == "stop-after-wait" {
				if _, err := db.RequestStop(t.Context(), "run-test"); err != nil {
					t.Fatal(err)
				}
			}
			for range 3 {
				if d.NextAttemptAt == nil {
					break
				}
				now = *d.NextAttemptAt
				d = recoverTick(t, r, id)
			}
			want, attempts := "EXHAUSTED", 3
			if fault == "stop-after-wait" {
				want, attempts = "STOPPED", 0
			}
			if d.State != want || d.Attempts != attempts || checks != attempts {
				t.Fatal(d, checks)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			db, err := store.OpenExisting(filepath.Join(filepath.Dir(opts.SnapshotPath), "state.db"), true)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			sessions, _ = NewSessionCoordinator(db, reset, submit)
			r = recoveryRunner(t, sessions, &now, dep)
			if again := recoverTick(t, r, id); !reflect.DeepEqual(d, again) || checks != attempts {
				t.Fatal("terminal recovery replayed after reopen", again)
			}
			w, err := db.SessionWork(t.Context(), id)
			if err != nil || w.Record.Message.Body != assignment().Body || w.Seat.Generation != 1 || reset.resets.Load() != 0 || submit.calls.Load() != 0 {
				t.Fatal("recovery lost work or performed native action", w, err)
			}
			counts := faultJournalCounts(t, db, "run-test", id)
			if counts["MESSAGE_STORED"] != 1 || counts["RESET_REQUESTED"] != 0 || counts["SUBMIT_REQUESTED"] != 0 || counts["MESSAGE_ACKED"] != 0 {
				t.Fatal(counts)
			}
		})
	}
}

func TestFaultMatrixContextReportBootstrap(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	for _, tc := range []struct {
		fault string
		want  error
	}{
		{"context-corrupt", runcontext.ErrCorrupt},
		{"context-permission", runcontext.ErrUnsafe},
		{"report-corrupt", runcontext.ErrCorrupt},
		{"bootstrap-corrupt", runcontext.ErrCorrupt},
		{"cross-run", runcontext.ErrIdentity},
		{"cross-agent", runcontext.ErrIdentity},
	} {
		t.Run(tc.fault, func(t *testing.T) {
			svc, _, report, _ := reportServiceFixture(t)
			actor := ipc.Actor{RunID: report.RunID, AgentID: report.AgentID, Generation: 2}
			if _, err := svc.Publish(t.Context(), actor, report); err != nil {
				t.Fatal(err)
			}
			bootstrap, _ := NewBootstrapService(svc.db, svc.run)
			initial, err := bootstrap.Bootstrap(t.Context(), actor)
			if err != nil {
				t.Fatal(err)
			}
			a, _ := svc.run.Agent(actor.AgentID)
			path, mode := "", os.FileMode(0600)
			switch tc.fault {
			case "context-corrupt":
				path = filepath.Join(a.Path(), "initial", "manifest.json")
			case "context-permission":
				path, mode = filepath.Join(a.Path(), "initial", "progress.json"), 0644
			case "report-corrupt":
				path = report.Evidence[0].Path
			case "bootstrap-corrupt":
				path = initial.Path
			case "cross-run":
				actor.RunID = "other-run"
			case "cross-agent":
				snapshot, _, err := a.Read("initial")
				if err != nil {
					t.Fatal(err)
				}
				snapshot.Revision = "foreign"
				snapshot.Pending.References = []runcontext.Reference{{Path: filepath.Join(svc.run.Path(), "reviewer", "initial", "manifest.json"), Purpose: "foreign"}}
				if _, err := a.Publish(*snapshot); err != nil {
					t.Fatal(err)
				}
			}
			if path != "" {
				if tc.fault == "context-permission" {
					err = os.Chmod(path, mode)
				} else {
					err = os.WriteFile(path, []byte("{}"), mode)
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			beforeRun, beforeSeats, err := svc.db.ReadState(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			beforeWork, err := svc.db.SessionWork(t.Context(), report.MessageID)
			if err != nil {
				t.Fatal(err)
			}
			beforeJournal, err := svc.db.JournalAfter(t.Context(), report.RunID, 0)
			if err != nil {
				t.Fatal(err)
			}
			for range 2 {
				result, err := bootstrap.Bootstrap(t.Context(), actor)
				if result != nil || !errors.Is(err, tc.want) {
					t.Fatalf("fault %s: result=%+v err=%v, want %v", tc.fault, result, err, tc.want)
				}
			}
			afterRun, afterSeats, err := svc.db.ReadState(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			afterWork, err := svc.db.SessionWork(t.Context(), report.MessageID)
			if err != nil {
				t.Fatal(err)
			}
			afterJournal, err := svc.db.JournalAfter(t.Context(), report.RunID, 0)
			if err != nil || !reflect.DeepEqual(beforeRun, afterRun) || !reflect.DeepEqual(beforeSeats, afterSeats) || !reflect.DeepEqual(beforeWork, afterWork) || !reflect.DeepEqual(beforeJournal, afterJournal) {
				t.Fatal("file recovery failure changed runtime work/identity/journal", err)
			}
			// A damaged artifact must not prevent another agent from reading its
			// own intact context, nor authorize a report or message ACK.
			other, err := bootstrap.Bootstrap(t.Context(), ipc.Actor{RunID: report.RunID, AgentID: "reviewer", Generation: 2})
			if err != nil || other.Executed || other.RuntimeCommunication != "UNVERIFIED" {
				t.Fatal("fault escaped agent boundary", other, err)
			}
		})
	}
}
