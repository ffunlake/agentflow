package runtime

import (
	"errors"
	"path/filepath"
	"testing"

	"agentflow/internal/ipc"
	"agentflow/internal/store"
)

func TestMessageService(t *testing.T) {
	if _, err := NewMessageService(nil); err == nil {
		t.Fatal("nil store accepted")
	}
	db, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateRun(t.Context(), store.Run{RunID: "run", ConfigSnapshot: "{}", ConfigDigest: "digest"}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"main", "builder"} {
		if err := db.CreateSeat(t.Context(), store.Seat{RunID: "run", AgentID: id, Generation: 2, Current: true}); err != nil {
			t.Fatal(err)
		}
	}
	s, err := NewMessageService(db)
	if err != nil {
		t.Fatal(err)
	}
	main := ipc.Actor{RunID: "run", AgentID: "main", Generation: 2}
	builder := ipc.Actor{RunID: "run", AgentID: "builder", Generation: 2}
	if err := s.ValidateActor(t.Context(), main); err != nil {
		t.Fatal(err)
	}
	for _, a := range []ipc.Actor{{RunID: "other", AgentID: "main", Generation: 2}, {RunID: "run", AgentID: "missing", Generation: 2}, {RunID: "run", AgentID: "main", Generation: 1}} {
		err := s.ValidateActor(t.Context(), a)
		var public *ipc.Error
		if !errors.As(err, &public) || (public.Code != ipc.ActorMismatch && public.Code != ipc.GenerationMismatch) {
			t.Fatal(err)
		}
	}
	c := SendMessage{Actor: main, RequestID: "assign", Recipient: "builder", Body: "new work", Session: "persistent"}
	m, err := s.Assign(t.Context(), c)
	if err != nil || m.Message.Kind != "assign" {
		t.Fatalf("%+v %v", m, err)
	}
	c.RequestID, c.Body = "send", "supplement"
	n, err := s.Send(t.Context(), c)
	if err != nil || n.Message.Kind != "send" || n.Delivery.TargetGeneration != 2 {
		t.Fatalf("%+v %v", n, err)
	}
	items, err := s.Inbox(t.Context(), builder, 0, true)
	if err != nil || len(items) != 2 {
		t.Fatalf("%+v %v", items, err)
	}
	if _, err := s.Ack(t.Context(), builder, "ack", m.Message.ID); err != nil {
		t.Fatal(err)
	}
	r, err := s.Reply(t.Context(), ReplyMessage{Actor: builder, RequestID: "reply", MessageID: m.Message.ID, Body: "done", Status: "DONE"})
	if err != nil || r.Message.Recipient != "main" || r.Message.ReplyTo != m.Message.ID {
		t.Fatalf("%+v %v", r, err)
	}
	seat, err := db.GetSeat(t.Context(), "run", "builder", 2)
	if err != nil || seat.Generation != 2 || seat.CurrentMessageID != "" {
		t.Fatalf("%+v %v", seat, err)
	}
}
