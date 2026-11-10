package store

import "testing"

func TestSessionHistoricalAckCannotReplaceMatchingSent(t *testing.T) {
	for _, evidence := range []string{"missing", "wrong-generation", "wrong-agent"} {
		t.Run(evidence, func(t *testing.T) {
			s := newTestStore(t)
			seedStore(t, s)
			c := coordinatedCommand()
			c.Session = "persistent"
			first, err := s.StoreMessage(t.Context(), c)
			if err != nil {
				t.Fatal(err)
			}
			w, err := s.SessionWork(t.Context(), first.Message.ID)
			if err != nil {
				t.Fatal(err)
			}
			if _, claimed, err := s.BeginSessionAction(t.Context(), first.Message.ID, w.Seat, "submit", ""); err != nil || !claimed {
				t.Fatal(claimed, err)
			}
			// Simulate a pre-fix ACK that erased the unresolved delivery phase,
			// with no active seat reservation and even a plausible channel.
			if _, err := s.db.Exec(`UPDATE deliveries SET phase = 'ACKED', channel_id = 'fake' WHERE message_id = ?`, first.Message.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := s.db.Exec(`UPDATE seats SET status = 'READY', current_message_id = NULL WHERE agent_id = 'builder'`); err != nil {
				t.Fatal(err)
			}
			ack := JournalEntry{RunID: c.Actor.RunID, MessageID: first.Message.ID, AgentID: "builder", Generation: 1, Event: "MESSAGE_ACKED", Details: `{"request_id":"old-ack"}`}
			if _, err := s.AppendJournal(t.Context(), ack); err != nil {
				t.Fatal(err)
			}
			if evidence != "missing" {
				sent := ack
				sent.Event = "MESSAGE_SENT"
				if evidence == "wrong-generation" {
					if err := s.CreateSeat(t.Context(), Seat{RunID: c.Actor.RunID, AgentID: "builder", Generation: 2, Status: "READY"}); err != nil {
						t.Fatal(err)
					}
					sent.Generation = 2
				} else {
					sent.AgentID = "main"
				}
				if _, err := s.AppendJournal(t.Context(), sent); err != nil {
					t.Fatal(err)
				}
			}
			_, err = s.Ack(t.Context(), builderMessageActor(), "new-ack", first.Message.ID)
			requireMessageCode(t, err, "SESSION_NOT_SENT")
			_, err = s.StoreMessage(t.Context(), MessageCommand{Actor: builderMessageActor(), RequestID: "done", Kind: "reply", ReplyTo: first.Message.ID, ReportStatus: "DONE"})
			requireMessageCode(t, err, "SESSION_NOT_SENT")
			d, err := s.ReconcileSession(t.Context(), c.Actor, first.Message.ID)
			if err != nil || d.State != "UNKNOWN_NEEDS_RECONCILE" || d.NextAction != "NONE" {
				t.Fatal(d, err)
			}
			w, err = s.SessionWork(t.Context(), first.Message.ID)
			if err != nil || w.SubmissionConfirmed {
				t.Fatal("ACK was treated as SENT", w, err)
			}
			c.RequestID = "second"
			second, err := s.StoreMessage(t.Context(), c)
			if err != nil {
				t.Fatal(err)
			}
			w, claimed, err := s.BeginSessionAction(t.Context(), second.Message.ID, w.Seat, "submit", "")
			if err != nil || claimed || w.Record.Delivery.Attempt != 0 || w.Record.Coordination != "SEAT_QUEUE" {
				t.Fatal("historical ACK admitted another native turn", w, claimed, err)
			}
		})
	}
}
