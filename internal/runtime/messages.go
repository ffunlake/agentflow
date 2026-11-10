package runtime

import (
	"context"
	"errors"

	"agentflow/internal/ipc"
	"agentflow/internal/store"
)

// MessageService binds message persistence to optional session adapters. The
// local coordinator checks claimed seats against current store state; native
// transport identity authentication remains an adapter responsibility.
type MessageService struct {
	db       *store.Store
	sessions *SessionCoordinator
}

func NewMessageService(db *store.Store) (*MessageService, error) {
	if db == nil {
		return nil, errors.New("message service requires a store")
	}
	sessions, _ := NewSessionCoordinator(db, nil, nil)
	return &MessageService{db: db, sessions: sessions}, nil
}

func NewMessageServiceWithAdapters(db *store.Store, reset ResetAdapter, submit SubmitAdapter) (*MessageService, error) {
	s, err := NewMessageService(db)
	if err != nil {
		return nil, err
	}
	s.sessions, err = NewSessionCoordinator(db, reset, submit)
	return s, err
}

type SendMessage struct {
	Actor            ipc.Actor
	RequestID        string
	Recipient        string
	TargetGeneration int64
	Body             string
	Session          string
	Mode             string // alias for Session on assign
}

type ReplyMessage struct {
	Actor     ipc.Actor
	RequestID string
	MessageID string
	Body      string
	Status    string
}

func messageActor(a ipc.Actor) store.MessageActor {
	return store.MessageActor{RunID: a.RunID, AgentID: a.AgentID, Generation: a.Generation}
}

// ValidateActor can be used as ipc.ServerOptions.ValidateActor. Store operations
// also validate inside their transaction, including every idempotent retry.
func (s *MessageService) ValidateActor(ctx context.Context, a ipc.Actor) error {
	err := s.db.ValidateMessageActor(ctx, messageActor(a))
	var public *store.MessagingError
	if errors.As(err, &public) {
		return &ipc.Error{Code: public.Code, Message: public.Message}
	}
	return err
}

func (s *MessageService) Assign(ctx context.Context, c SendMessage) (store.MessageRecord, error) {
	return s.sessions.Assign(ctx, c)
}

func (s *MessageService) Send(ctx context.Context, c SendMessage) (store.MessageRecord, error) {
	return s.save(ctx, "send", c)
}

func (s *MessageService) save(ctx context.Context, kind string, c SendMessage) (store.MessageRecord, error) {
	return s.db.StoreMessage(ctx, store.MessageCommand{Actor: messageActor(c.Actor), RequestID: c.RequestID,
		Recipient: c.Recipient, TargetGeneration: c.TargetGeneration, Kind: kind, Body: c.Body})
}

func (s *MessageService) Reply(ctx context.Context, c ReplyMessage) (store.MessageRecord, error) {
	return s.db.StoreMessage(ctx, store.MessageCommand{Actor: messageActor(c.Actor), RequestID: c.RequestID,
		Kind: "reply", Body: c.Body, ReplyTo: c.MessageID, ReportStatus: c.Status})
}

func (s *MessageService) Inbox(ctx context.Context, a ipc.Actor, after int64, pending bool) ([]store.MessageRecord, error) {
	return s.db.Inbox(ctx, store.InboxQuery{Actor: messageActor(a), After: after, Pending: pending})
}

func (s *MessageService) Ack(ctx context.Context, a ipc.Actor, requestID, messageID string) (store.AckResult, error) {
	return s.db.Ack(ctx, messageActor(a), requestID, messageID)
}
