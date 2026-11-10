package store

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
)

func mainMessageActor() MessageActor    { return MessageActor{"run-1", "main", 1} }
func builderMessageActor() MessageActor { return MessageActor{"run-1", "builder", 1} }
func messageCommand() MessageCommand {
	return MessageCommand{Actor: mainMessageActor(), Recipient: "builder", RequestID: "request-1", Kind: "assign", Body: "first work\n中文"}
}

func requireMessageCode(t *testing.T, err error, code string) {
	t.Helper()
	var public *MessagingError
	if !errors.As(err, &public) || public.Code != code {
		t.Fatalf("error=%v want=%s", err, code)
	}
}

func TestMessageIdentityAndEmptyStores(t *testing.T) {
	s := newTestStore(t)
	_, err := s.StoreMessage(t.Context(), messageCommand())
	requireMessageCode(t, err, "RUN_NOT_FOUND")
	_, err = s.Inbox(t.Context(), InboxQuery{Actor: mainMessageActor()})
	requireMessageCode(t, err, "RUN_NOT_FOUND")
	_, err = s.Ack(t.Context(), mainMessageActor(), "ack-1", "missing")
	requireMessageCode(t, err, "RUN_NOT_FOUND")
	seedStore(t, s)
	for _, mode := range []string{"run", "unknown-sender", "old-sender", "future-sender", "external-sender", "unknown-recipient", "old-recipient", "invalid-generation"} {
		t.Run(mode, func(t *testing.T) {
			c := messageCommand()
			code := "ACTOR_MISMATCH"
			switch mode {
			case "run":
				c.Actor.RunID = "other"
			case "unknown-sender":
				c.Actor.AgentID = "missing"
			case "old-sender":
				c.Actor.Generation = 2
				code = "GENERATION_MISMATCH"
			case "future-sender":
				c.Actor.Generation = 99
				code = "GENERATION_MISMATCH"
			case "external-sender":
				c.Actor.Generation = 0
				code = "GENERATION_MISMATCH"
			case "unknown-recipient":
				c.Recipient = "missing"
			case "old-recipient":
				c.TargetGeneration = 2
				code = "GENERATION_MISMATCH"
			case "invalid-generation":
				c.TargetGeneration = -1
				code = "INVALID_REQUEST"
			}
			_, err := s.StoreMessage(t.Context(), c)
			requireMessageCode(t, err, code)
			assertCounts(t, s, 0, 0, 0)
		})
	}
	for _, actor := range []MessageActor{{"other", "main", 1}, {"run-1", "missing", 1}, {"run-1", "main", 2}} {
		if _, err := s.Inbox(t.Context(), InboxQuery{Actor: actor}); err == nil {
			t.Fatal("inbox accepted invalid actor")
		}
		if _, err := s.Ack(t.Context(), actor, "ack", "missing"); err == nil {
			t.Fatal("ack accepted invalid actor")
		}
	}
	items, err := s.Inbox(t.Context(), InboxQuery{Actor: builderMessageActor()})
	if err != nil || items == nil || len(items) != 0 {
		t.Fatalf("%+v %v", items, err)
	}
}

func TestMessagingRetryReplyAndAck(t *testing.T) {
	s := newTestStore(t)
	seedStore(t, s)
	c := messageCommand()
	first, err := s.StoreMessage(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	if first.Message.Kind != "assign" || first.Message.Status != "STORED" || first.Delivery != (DeliveryState{1, "PENDING", "NONE", "UNVERIFIED", 0}) {
		t.Fatalf("%+v", first)
	}
	// Same body deliberately preserves the original route/kind, as SaveOutbox does.
	c.Recipient, c.Kind = "unknown", "send"
	retry, err := s.StoreMessage(t.Context(), c)
	if err != nil || !reflect.DeepEqual(first, retry) {
		t.Fatalf("%+v %v", retry, err)
	}
	c.Body = "changed"
	if _, err := s.StoreMessage(t.Context(), c); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatal(err)
	}
	assertCounts(t, s, 1, 1, 1)
	items, err := s.Inbox(t.Context(), InboxQuery{Actor: builderMessageActor(), Pending: true})
	if err != nil || !reflect.DeepEqual(items, []MessageRecord{first}) {
		t.Fatalf("%+v %v", items, err)
	}
	items, err = s.Inbox(t.Context(), InboxQuery{Actor: builderMessageActor(), After: first.Message.Seq})
	if err != nil || len(items) != 0 {
		t.Fatalf("%+v %v", items, err)
	}
	_, err = s.Ack(t.Context(), mainMessageActor(), "wrong-ack", first.Message.ID)
	requireMessageCode(t, err, "ACTOR_MISMATCH")
	_, err = s.Ack(t.Context(), builderMessageActor(), "missing-ack", "missing")
	requireMessageCode(t, err, "MESSAGE_NOT_FOUND")
	ack, err := s.Ack(t.Context(), builderMessageActor(), "ack-1", first.Message.ID)
	if err != nil || ack.Status != "ACKED" {
		t.Fatalf("%+v %v", ack, err)
	}
	for _, id := range []string{"ack-1", "ack-1", "ack-2", "ack-2"} {
		again, err := s.Ack(t.Context(), builderMessageActor(), id, first.Message.ID)
		if err != nil || !reflect.DeepEqual(ack, again) {
			t.Fatalf("%+v %v", again, err)
		}
	}
	assertCounts(t, s, 1, 1, 3) // saved, confirmed once, alias request receipt once
	items, err = s.Inbox(t.Context(), InboxQuery{Actor: builderMessageActor(), Pending: true})
	if err != nil || len(items) != 0 {
		t.Fatalf("%+v %v", items, err)
	}
	items, err = s.Inbox(t.Context(), InboxQuery{Actor: builderMessageActor()})
	if err != nil || len(items) != 1 || items[0].Message.Status != "ACKED" || items[0].Delivery.Attempt != 0 || items[0].Delivery.Channel != "UNVERIFIED" {
		t.Fatalf("%+v %v", items, err)
	}
	reply := MessageCommand{Actor: builderMessageActor(), RequestID: "reply-1", Kind: "reply", ReplyTo: first.Message.ID, Body: "result", ReportStatus: "DONE"}
	r, err := s.StoreMessage(t.Context(), reply)
	if err != nil {
		t.Fatal(err)
	}
	if r.Message.ReplyTo != first.Message.ID || r.Message.Recipient != "main" || r.Message.ReportStatus != "DONE" || r.Message.Kind != "reply" {
		t.Fatalf("%+v", r)
	}
	reply.ReportStatus = "CHANGED"
	again, err := s.StoreMessage(t.Context(), reply)
	if err != nil || !reflect.DeepEqual(r, again) {
		t.Fatalf("%+v %v", again, err)
	}
	reply.Body = "changed result"
	if _, err := s.StoreMessage(t.Context(), reply); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatal(err)
	}
	// ACK request IDs share the sender namespace with replies and sends.
	reply.RequestID = "ack-1"
	if _, err := s.StoreMessage(t.Context(), reply); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatal(err)
	}
	if _, err := s.Ack(t.Context(), builderMessageActor(), "reply-1", first.Message.ID); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatal(err)
	}
	second := messageCommand()
	second.RequestID = "request-2"
	m2, err := s.StoreMessage(t.Context(), second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Ack(t.Context(), builderMessageActor(), "ack-2", m2.Message.ID); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatal(err)
	}
	assertCounts(t, s, 3, 3, 5)
}

func TestMessagingRejectsStaleGenerationsAndReplySpoofing(t *testing.T) {
	s := newTestStore(t)
	seedStore(t, s)
	c := messageCommand()
	m, err := s.StoreMessage(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	reply := MessageCommand{Actor: builderMessageActor(), RequestID: "reply", Kind: "reply", Body: "result", ReplyTo: m.Message.ID}
	for _, mode := range []string{"sender", "recipient", "target", "missing", "cross-run"} {
		bad := reply
		switch mode {
		case "sender":
			bad.Actor = mainMessageActor()
		case "recipient":
			bad.Recipient = "builder"
		case "target":
			bad.TargetGeneration = 2
		case "missing":
			bad.ReplyTo = "missing"
		case "cross-run":
			bad.Actor.RunID = "other"
		}
		if _, err := s.StoreMessage(t.Context(), bad); err == nil {
			t.Fatalf("accepted %s", mode)
		}
	}
	if _, err := s.db.Exec(`UPDATE seats SET is_current = 0 WHERE agent_id = 'builder'`); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateSeat(t.Context(), Seat{RunID: "run-1", AgentID: "builder", Generation: 2, Current: true}); err != nil {
		t.Fatal(err)
	}
	_, err = s.StoreMessage(t.Context(), c) // retry to stale persisted target
	requireMessageCode(t, err, "GENERATION_MISMATCH")
	_, err = s.StoreMessage(t.Context(), reply)
	requireMessageCode(t, err, "GENERATION_MISMATCH")
	_, err = s.Ack(t.Context(), builderMessageActor(), "ack", m.Message.ID)
	requireMessageCode(t, err, "GENERATION_MISMATCH")
	newActor := MessageActor{"run-1", "builder", 2}
	_, err = s.Ack(t.Context(), newActor, "ack", m.Message.ID)
	requireMessageCode(t, err, "GENERATION_MISMATCH")
	reply.Actor = newActor
	_, err = s.StoreMessage(t.Context(), reply)
	requireMessageCode(t, err, "GENERATION_MISMATCH")
	items, err := s.Inbox(t.Context(), InboxQuery{Actor: newActor})
	if err != nil || len(items) != 0 {
		t.Fatalf("%+v %v", items, err)
	}
	assertCounts(t, s, 1, 1, 3) // rejected historical ACK and reply are audited
}

func TestMessagingTransactionRollback(t *testing.T) {
	for _, stage := range []string{"message", "delivery", "journal", "ack-message", "ack-journal", "reply-journal"} {
		t.Run(stage, func(t *testing.T) {
			s := newTestStore(t)
			seedStore(t, s)
			var original MessageRecord
			var err error
			if stage == "ack-message" || stage == "ack-journal" || stage == "reply-journal" {
				original, err = s.StoreMessage(t.Context(), messageCommand())
				if err != nil {
					t.Fatal(err)
				}
			}
			statement := ""
			switch stage {
			case "message":
				statement = `CREATE TRIGGER inject_failure BEFORE INSERT ON messages BEGIN SELECT RAISE(ABORT, 'injected'); END`
			case "delivery":
				statement = `CREATE TRIGGER inject_failure BEFORE INSERT ON deliveries BEGIN SELECT RAISE(ABORT, 'injected'); END`
			case "journal", "ack-journal", "reply-journal":
				statement = `CREATE TRIGGER inject_failure BEFORE INSERT ON journal BEGIN SELECT RAISE(ABORT, 'injected'); END`
			case "ack-message":
				statement = `CREATE TRIGGER inject_failure BEFORE UPDATE ON messages BEGIN SELECT RAISE(ABORT, 'injected'); END`
			}
			if _, err := s.db.Exec(statement); err != nil {
				t.Fatal(err)
			}
			if stage == "ack-message" || stage == "ack-journal" {
				_, err = s.Ack(t.Context(), builderMessageActor(), "ack-1", original.Message.ID)
			} else if stage == "reply-journal" {
				_, err = s.StoreMessage(t.Context(), MessageCommand{Actor: builderMessageActor(), RequestID: "reply", Kind: "reply", ReplyTo: original.Message.ID, Body: "result"})
			} else {
				_, err = s.StoreMessage(t.Context(), messageCommand())
			}
			if err == nil {
				t.Fatal("injected failure accepted")
			}
			count := 0
			if original.Message.ID != "" {
				count = 1
				d, err := s.GetDelivery(t.Context(), original.Message.ID, 1)
				if err != nil || d.Phase != "PENDING" || d.Attempt != 0 {
					t.Fatalf("%+v %v", d, err)
				}
				m, err := s.GetMessage(t.Context(), original.Message.ID)
				if err != nil || m.Status != "STORED" {
					t.Fatalf("%+v %v", m, err)
				}
			}
			assertCounts(t, s, count, count, count)
			if _, err := s.db.Exec(`DROP TRIGGER inject_failure`); err != nil {
				t.Fatal(err)
			}
			if stage == "ack-message" || stage == "ack-journal" {
				if _, err := s.Ack(t.Context(), builderMessageActor(), "ack-1", original.Message.ID); err != nil {
					t.Fatal(err)
				}
			} else if stage == "reply-journal" {
				if _, err := s.StoreMessage(t.Context(), MessageCommand{Actor: builderMessageActor(), RequestID: "reply", Kind: "reply", ReplyTo: original.Message.ID, Body: "result"}); err != nil {
					t.Fatal(err)
				}
			} else if _, err := s.StoreMessage(t.Context(), messageCommand()); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestMessagingConcurrentRetriesAndReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	s := openTestStore(t, path)
	if err := s.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	seedStore(t, s)
	other := openTestStore(t, path)
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			db := s
			if i%2 == 0 {
				db = other
			}
			if _, err := db.StoreMessage(context.Background(), messageCommand()); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	assertCounts(t, s, 1, 1, 1)
	items, err := s.Inbox(t.Context(), InboxQuery{Actor: builderMessageActor()})
	if err != nil || len(items) != 1 {
		t.Fatalf("%+v %v", items, err)
	}
	m := items[0]
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			db := s
			if i%2 == 0 {
				db = other
			}
			if _, err := db.Ack(context.Background(), builderMessageActor(), "ack-1", m.Message.ID); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	assertCounts(t, s, 1, 1, 2)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := other.Close(); err != nil {
		t.Fatal(err)
	}
	reopened := openTestStore(t, path)
	again, err := reopened.StoreMessage(t.Context(), messageCommand())
	if err != nil || again.Message.ID != m.Message.ID || again.Message.Status != "ACKED" {
		t.Fatalf("%+v %v", again, err)
	}
	if _, err := reopened.Ack(t.Context(), builderMessageActor(), "ack-1", m.Message.ID); err != nil {
		t.Fatal(err)
	}
	assertCounts(t, reopened, 1, 1, 2)
}
