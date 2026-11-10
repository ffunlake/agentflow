package runtime

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"agentflow/internal/store"
)

func TestSessionRunningAckCannotReleaseSecondAssignment(t *testing.T) {
	for _, unknown := range []bool{false, true} {
		t.Run(map[bool]string{false: "sent", true: "unknown"}[unknown], func(t *testing.T) {
			db, _, _, opts := sessionFixture(t)
			otherDB, err := store.Open(filepath.Join(filepath.Dir(opts.SnapshotPath), "state.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = otherDB.Close() })
			submit := &fakeSubmitAdapter{}
			sessions, _ := NewSessionCoordinator(db, &fakeResetAdapter{}, submit)
			other, _ := NewSessionCoordinator(otherDB, &fakeResetAdapter{}, submit)
			firstCommand := assignment()
			firstCommand.Session = "persistent"
			var second store.MessageRecord
			submit.submit = func(ctx context.Context, seat SeatSnapshot, m store.Message) (Submission, error) {
				_, err := db.Ack(ctx, messageActor(seatActor(seat)), "running-ack", m.ID)
				requireSessionCode(t, err, "SESSION_NOT_SENT")
				// A separate coordinator must see the durable reservation while
				// the first native invocation is still executing.
				next := firstCommand
				next.RequestID = "second"
				second, err = other.Assign(ctx, next)
				if err != nil || second.Delivery.Phase != "QUEUED" || second.Delivery.Attempt != 0 || submit.calls.Load() != 1 {
					t.Fatalf("concurrent submit: %+v calls=%d err=%v", second, submit.calls.Load(), err)
				}
				if unknown {
					return Submission{}, errors.New("native outcome unknown")
				}
				return Submission{Actor: seatActor(seat), Native: seat.Native, Accepted: true, Channel: "fake"}, nil
			}
			first, err := sessions.Assign(t.Context(), firstCommand)
			if err != nil {
				t.Fatal(err)
			}
			seat, err := db.GetSeat(t.Context(), firstCommand.Actor.RunID, "builder", 1)
			if err != nil || seat.Status != "BUSY" || seat.CurrentMessageID != first.Message.ID {
				t.Fatal("native reservation lost", seat, err)
			}
			_, err = db.Ack(t.Context(), messageActor(seatActor(sessionSeat(seat))), "after-submit-ack", first.Message.ID)
			if unknown {
				requireSessionCode(t, err, "SESSION_NOT_SENT")
			} else if err != nil {
				t.Fatal(err)
			}
			second, err = other.Advance(t.Context(), firstCommand.Actor, second.Message.ID)
			if err != nil || second.Delivery.Attempt != 0 || submit.calls.Load() != 1 {
				t.Fatal("ACK released unfinished assignment", second, err)
			}
			reply := store.MessageCommand{Actor: messageActor(seatActor(sessionSeat(seat))), RequestID: "done", Kind: "reply", ReplyTo: first.Message.ID, Body: "done", ReportStatus: "DONE"}
			_, err = db.StoreMessage(t.Context(), reply)
			if unknown {
				requireSessionCode(t, err, "SESSION_NOT_SENT")
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			submit.submit = nil
			second, err = other.Advance(t.Context(), firstCommand.Actor, second.Message.ID)
			if err != nil || second.Delivery.Phase != "SENT" || submit.calls.Load() != 2 {
				t.Fatal("terminal reply did not release seat", second, err)
			}
		})
	}
}
