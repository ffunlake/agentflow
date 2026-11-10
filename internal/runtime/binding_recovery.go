package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"time"

	"agentflow/internal/config"
	"agentflow/internal/ipc"
	"agentflow/internal/store"
)

// SessionObservation is trusted read-only evidence about the original session
// or thread. AVAILABLE may change location, never native session identity. Both
// native observations and READY bind the frozen permission/configuration digest.
type SessionObservation struct {
	Actor        ipc.Actor
	Native       NativeBinding
	Tool         string
	Permissions  string
	ConfigDigest string
	State        string // AVAILABLE, UNAVAILABLE, DEPENDENCY_UNAVAILABLE, USER_CLOSED, TERMINAL
	Verified     bool
}

type BindingReady struct {
	Registration
	Permissions  string
	ConfigDigest string
}

type BindingRecoveryRequest struct {
	RequestID  string
	Previous   SeatSnapshot
	Generation int64
	Role       config.ResolvedAgent // Includes frozen model/account arguments.
	ContextDir string
	Checkpoint *BootstrapResult
}

// BindingRecoveryAdapter has no submit, ACK or reset operation. Create is called
// at most once, after a durable CREATE_REQUESTED receipt. It must honor context
// cancellation and protect the native operation boundary against concurrent close.
type BindingRecoveryAdapter interface {
	InspectOriginal(context.Context, SeatSnapshot) (SessionObservation, error)
	Create(context.Context, BindingRecoveryRequest) (NativeBinding, error)
	WaitBindingReady(context.Context, BindingRecoveryRequest, NativeBinding) (BindingReady, error)
}

type SessionRecovery struct {
	sessions  *SessionCoordinator
	adapter   BindingRecoveryAdapter
	policy    RecoveryPolicy
	now       func() time.Time
	bootstrap *BootstrapService
}

func NewSessionRecovery(sessions *SessionCoordinator, adapter BindingRecoveryAdapter, policy RecoveryPolicy, now func() time.Time, bootstrap *BootstrapService) (*SessionRecovery, error) {
	if sessions == nil || now == nil {
		return nil, errors.New("session recovery requires coordinator and clock")
	}
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	if bootstrap != nil && bootstrap.db != sessions.db {
		return nil, errors.New("bootstrap must use the recovery store")
	}
	policy.Backoff = append([]time.Duration(nil), policy.Backoff...)
	return &SessionRecovery{sessions, adapter, policy, now, bootstrap}, nil
}

// Recover is an explicit bounded tick. Existing sessions win over fresh session
// policy during failure recovery; assigning a new fresh task remains S5 behavior.
// An interrupted reservation is observable after restart and never auto-stolen.
func (s *SessionRecovery) Recover(ctx context.Context, actor, target ipc.Actor, requestID string) (store.BindingRecovery, error) {
	journalCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	r, claimed, err := s.sessions.db.ReserveBindingRecovery(journalCtx, messageActor(actor), messageActor(target), requestID, s.policy, s.now(), ctx.Err() != nil)
	cancel()
	if err != nil || !claimed {
		return r, err
	}
	finish := func(state, reason string, next *store.Seat) (store.BindingRecovery, error) {
		journalCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		return s.sessions.db.FinishBindingRecovery(journalCtx, r, state, reason, next, s.now())
	}
	if ctx.Err() != nil {
		return finish("STOPPED", "CONTEXT_CANCELLED", nil)
	}
	request, digest, err := s.request(ctx, r)
	if err != nil {
		return finish("STOPPED", "CONFIGURATION_ERROR", nil)
	}
	if s.adapter == nil {
		return finish("WAITING", "RECOVERY_ADAPTER_UNIMPLEMENTED", nil)
	}
	observation, inspectErr := s.adapter.InspectOriginal(ctx, request.Previous)
	if ctx.Err() != nil {
		return finish("STOPPED", "CONTEXT_CANCELLED", nil)
	}
	if inspectErr != nil {
		if permanentRecoveryError(inspectErr) {
			return finish("STOPPED", "PERMISSION_OR_CONFIGURATION_ERROR", nil)
		}
		var fault *RecoveryError
		if errors.As(inspectErr, &fault) && fault.Code == "DEPENDENCY_UNAVAILABLE" {
			return finish("WAITING", fault.Code, nil)
		}
		return finish("MANUAL_RECONCILE", "ORIGINAL_SESSION_UNVERIFIED", nil)
	}
	if !observation.Verified || observation.Actor != target || observation.Tool != request.Previous.Tool || observation.Permissions != request.Previous.Permissions || observation.ConfigDigest != digest {
		return finish("MANUAL_RECONCILE", "ORIGINAL_SESSION_UNVERIFIED", nil)
	}
	if observation.State != "AVAILABLE" && observation.Native != request.Previous.Native {
		return finish("MANUAL_RECONCILE", "ORIGINAL_BINDING_MISMATCH", nil)
	}
	switch observation.State {
	case "USER_CLOSED", "TERMINAL":
		reason := "USER_CLOSED"
		if observation.State == "TERMINAL" {
			reason = "EXIT_CONFIRMED"
		}
		if err := s.sessions.db.RecordBindingExit(ctx, r.Previous, reason, s.now()); err != nil {
			return r, err
		}
		return finish("STOPPED", observation.State, nil)
	case "DEPENDENCY_UNAVAILABLE":
		return finish("WAITING", "DEPENDENCY_UNAVAILABLE", nil)
	case "AVAILABLE":
		if observation.Native.validate() != nil || observation.Native.SessionID != request.Previous.Native.SessionID {
			return finish("MANUAL_RECONCILE", "ORIGINAL_BINDING_MISMATCH", nil)
		}
		if observation.Native == request.Previous.Native && r.Previous.Status != Starting {
			return finish("COMPLETE", "ORIGINAL_SESSION_AVAILABLE", nil)
		}
		// Location changes and partial startup both need independent READY.
		request.Generation = target.Generation
	case "UNAVAILABLE":
		// Only a verified absence may use the S6.3 file checkpoint. Missing or
		// corrupt context fails before committing any native creation intent.
		if s.bootstrap == nil {
			return finish("MANUAL_RECONCILE", "BOOTSTRAP_REQUIRED", nil)
		}
		request.Checkpoint, err = s.bootstrap.Bootstrap(ctx, target)
		if err != nil {
			return finish("MANUAL_RECONCILE", "BOOTSTRAP_UNVERIFIED", nil)
		}
		r, err = finish("CREATE_REQUESTED", "ORIGINAL_UNAVAILABLE_RECONCILED", nil)
		if err != nil || r.State != "CREATE_REQUESTED" {
			return r, err
		}
		observation.Native, err = s.adapter.Create(ctx, request)
		if ctx.Err() != nil || err != nil || observation.Native.validate() != nil || observation.Native.SessionID == request.Previous.Native.SessionID {
			return finish("MANUAL_RECONCILE", "CREATE_OUTCOME_UNVERIFIED", nil)
		}
	default:
		return finish("MANUAL_RECONCILE", "ORIGINAL_SESSION_UNVERIFIED", nil)
	}
	claim, err := s.adapter.WaitBindingReady(ctx, request, observation.Native)
	expected := target
	expected.Generation = request.Generation
	if ctx.Err() != nil || err != nil || claim.Actor != expected || claim.Status != Ready || claim.Native != observation.Native || claim.Tool != request.Role.Tool || claim.Permissions != request.Role.Permissions || claim.ConfigDigest != digest {
		return finish("MANUAL_RECONCILE", "READY_UNVERIFIED", nil)
	}
	next := r.Previous
	next.Generation, next.NativeSessionID, next.WorkspaceID, next.SurfaceID = expected.Generation, claim.Native.SessionID, claim.Native.WorkspaceID, claim.Native.SurfaceID
	return finish("COMPLETE", "NATIVE_AND_READY_CONFIRMED", &next)
}

func (s *SessionRecovery) request(ctx context.Context, r store.BindingRecovery) (BindingRecoveryRequest, string, error) {
	run, err := s.sessions.db.GetRun(ctx)
	if err != nil {
		return BindingRecoveryRequest{}, "", err
	}
	var frozen config.Resolved
	if err := json.Unmarshal([]byte(run.ConfigSnapshot), &frozen); err != nil {
		return BindingRecoveryRequest{}, "", err
	}
	plan, err := Compile(&frozen, Options{})
	if err != nil {
		return BindingRecoveryRequest{}, "", err
	}
	if _, err := NewSnapshot(plan, run.RunID); err != nil {
		return BindingRecoveryRequest{}, "", err
	}
	role, ok := frozen.Agents[r.AgentID]
	if !ok || role.Tool != r.Previous.Tool || frozen.Digest != run.ConfigDigest || r.ConfigDigest != run.ConfigDigest {
		return BindingRecoveryRequest{}, "", errors.New("frozen recovery binding mismatch")
	}
	seat := sessionSeat(r.Previous)
	seat.Cwd, seat.Role, seat.Permissions, seat.Session = role.Cwd, role.Role, role.Permissions, role.Session
	return BindingRecoveryRequest{RequestID: r.RequestID, Previous: seat, Generation: seat.Generation + 1, Role: role, ContextDir: filepath.Join(frozen.ContextDir, run.RunID)}, frozen.Digest, nil
}

// NoteExit accepts trusted adapter evidence only for the exact current binding.
// Transport integrations must authenticate this evidence before calling the API.
func (s *SessionRecovery) NoteExit(ctx context.Context, event SessionObservation) error {
	seat, err := s.sessions.db.RecoverySeat(ctx, event.Actor.RunID, event.Actor.AgentID)
	if err != nil {
		return err
	}
	r := store.BindingRecovery{AgentID: seat.AgentID, Previous: seat}
	run, err := s.sessions.db.GetRun(ctx)
	if err != nil {
		return err
	}
	r.ConfigDigest = run.ConfigDigest
	request, digest, err := s.request(ctx, r)
	if err != nil {
		return err
	}
	if !event.Verified || event.Tool != seat.Tool || event.Permissions != request.Role.Permissions || event.ConfigDigest != digest {
		return errors.New("unverified native event")
	}
	binding := seat
	binding.Generation, binding.NativeSessionID, binding.WorkspaceID, binding.SurfaceID = event.Actor.Generation, event.Native.SessionID, event.Native.WorkspaceID, event.Native.SurfaceID
	reason := "EXIT_UNVERIFIED"
	if event.State == "USER_CLOSED" {
		reason = "USER_CLOSED"
	}
	if event.State == "TERMINAL" {
		reason = "EXIT_CONFIRMED"
	}
	return s.sessions.db.RecordBindingExit(ctx, binding, reason, s.now())
}

// ValidateBinding is the transport admission check for READY/ACK/result events
// after a location change. Actor alone cannot distinguish two locations in the
// same generation; integrations must authenticate and validate the whole claim.
func (s *SessionRecovery) ValidateBinding(ctx context.Context, claim BindingReady) error {
	seat, err := s.sessions.db.RecoverySeat(ctx, claim.Actor.RunID, claim.Actor.AgentID)
	if err != nil {
		return err
	}
	run, err := s.sessions.db.GetRun(ctx)
	if err != nil {
		return err
	}
	r := store.BindingRecovery{AgentID: seat.AgentID, Previous: seat, ConfigDigest: run.ConfigDigest}
	request, digest, err := s.request(ctx, r)
	if err != nil {
		return err
	}
	if claim.Status != Ready || claim.Actor != seatActor(request.Previous) || claim.Native != request.Previous.Native || claim.Tool != seat.Tool || claim.Permissions != request.Role.Permissions || claim.ConfigDigest != digest || seat.Status == Starting {
		return &store.MessagingError{Code: "ACTOR_MISMATCH", Path: "binding.event", Message: "event does not match the current frozen native binding"}
	}
	return nil
}
