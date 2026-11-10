package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"sync"

	"agentflow/internal/ipc"
	"agentflow/internal/store"
)

const AlreadyRegistered = "ALREADY_REGISTERED"

type Registration struct {
	Actor  ipc.Actor     `json:"actor"`
	Tool   string        `json:"tool"`
	Native NativeBinding `json:"native"`
	Status string        `json:"status"`
	Nonce  string        `json:"nonce,omitempty"`
}

// Registry owns one startup attempt. Native bindings are trusted launcher output;
// registrations are agent READY claims that must match those bindings exactly.
// S7.2 recovery uses durable store state, never this startup memory snapshot.
type Registry struct {
	mu       sync.Mutex
	snapshot RunSnapshot
	db       *store.Store
}

func NewRegistry(ctx context.Context, db *store.Store, snapshot *RunSnapshot) (*Registry, error) {
	if db == nil {
		return nil, errors.New("registration requires a store")
	}
	if err := snapshot.Validate(); err != nil {
		return nil, err
	}
	for _, seat := range snapshot.Seats {
		if seat.Status != Starting || seat.Native != (NativeBinding{}) {
			return nil, errors.New("new registry requires unbound STARTING seats; resume is not implemented")
		}
	}
	// Freeze caller-owned slices/maps before saving or accepting any claims.
	body, err := json.Marshal(snapshot)
	if err != nil {
		return nil, err
	}
	r := &Registry{db: db}
	if err := json.Unmarshal(body, &r.snapshot); err != nil {
		return nil, err
	}
	configBody, err := json.Marshal(r.snapshot.Config)
	if err != nil {
		return nil, err
	}
	if err := db.CreateRun(ctx, store.Run{RunID: snapshot.RunID, ConfigSnapshot: string(configBody),
		ConfigDigest: snapshot.Config.Digest, StartupPhase: Starting}); err != nil {
		return nil, err
	}
	return r, nil
}

// Snapshot returns a detached view, including native bindings and accepted READYs.
func (r *Registry) Snapshot() RunSnapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	body, _ := json.Marshal(&r.snapshot) // Validated configuration is JSON serializable.
	var detached RunSnapshot
	_ = json.Unmarshal(body, &detached)
	return detached
}

func (r *Registry) find(actor ipc.Actor) (*SeatSnapshot, error) {
	if actor.RunID != r.snapshot.RunID {
		return nil, &ipc.Error{Code: ipc.ActorMismatch, Message: "registration run ID mismatch"}
	}
	for i := range r.snapshot.Seats {
		seat := &r.snapshot.Seats[i]
		if seat.AgentID != actor.AgentID {
			continue
		}
		if actor.Generation != seat.Generation {
			return nil, &ipc.Error{Code: ipc.GenerationMismatch, Message: "registration generation mismatch"}
		}
		return seat, nil
	}
	return nil, &ipc.Error{Code: ipc.ActorMismatch, Message: "unknown registration agent"}
}

func (r *Registry) bind(actor ipc.Actor, native NativeBinding) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	seat, err := r.find(actor)
	if err != nil {
		return err
	}
	if err := native.validate(); err != nil {
		return err
	}
	if seat.Native != (NativeBinding{}) || seat.Status != Starting {
		return &ipc.Error{Code: AlreadyRegistered, Message: "seat already has a launch binding"}
	}
	for _, other := range r.snapshot.Seats {
		if bindingConflicts(other, seat.Tool, native) {
			return &ipc.Error{Code: ipc.ActorMismatch, Message: "native binding is already assigned to another seat"}
		}
	}
	seat.Native = native
	return nil
}

// Register commits only after a matching agent READY. Duplicate, unknown,
// stale/future, unbound and mismatched claims leave both memory and store intact.
func (r *Registry) Register(ctx context.Context, claim Registration) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	seat, err := r.find(claim.Actor)
	if err != nil {
		return err
	}
	if claim.Status != Ready || claim.Tool != seat.Tool || seat.Native == (NativeBinding{}) || claim.Native != seat.Native {
		return &ipc.Error{Code: ipc.ActorMismatch, Message: "READY status, tool or native identity mismatch"}
	}
	if seat.Status != Starting {
		return &ipc.Error{Code: AlreadyRegistered, Message: "seat READY already registered"}
	}
	persisted := store.Seat{RunID: seat.RunID, AgentID: seat.AgentID,
		Generation: seat.Generation, Current: true, Tool: seat.Tool, NativeSessionID: seat.Native.SessionID,
		WorkspaceID: seat.Native.WorkspaceID, SurfaceID: seat.Native.SurfaceID, Status: Ready}
	if claim.Nonce != "" {
		err = r.db.RegisterStartupReadyWithNonce(ctx, persisted, claim.Nonce)
	} else {
		err = r.db.RegisterStartupReady(ctx, persisted)
	}
	if err != nil {
		return err
	}
	seat.Status = Ready
	return nil
}
