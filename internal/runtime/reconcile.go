package runtime

import (
	"context"

	"agentflow/internal/ipc"
	"agentflow/internal/store"
)

// Reconcile persists a recovery decision without calling any native adapter.
func (s *SessionCoordinator) Reconcile(ctx context.Context, actor ipc.Actor, id string) (store.RecoveryDecision, error) {
	return s.db.ReconcileSession(ctx, messageActor(actor), id)
}

func (s *MessageService) Reconcile(ctx context.Context, actor ipc.Actor, id string) (store.RecoveryDecision, error) {
	return s.sessions.Reconcile(ctx, actor, id)
}

// Resume is deliberately a decision query. SAFE_TO_CONTINUE still requires input
// inspection and an exclusive action claim through Advance; it executes nothing.
func (s *MessageService) Resume(ctx context.Context, actor ipc.Actor, id string) (store.RecoveryDecision, error) {
	return s.Reconcile(ctx, actor, id)
}
