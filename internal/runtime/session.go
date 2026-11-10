package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"

	"agentflow/internal/config"
	"agentflow/internal/ipc"
	"agentflow/internal/store"
)

// InputState is trusted adapter inspection of the exact visible native binding.
// Unknown inspection is never interpreted as an empty input buffer.
type InputState struct {
	Actor           ipc.Actor
	Native          NativeBinding
	Verified        bool
	Busy            bool
	Draft           string
	IncompleteInput bool
}

// ResetRequest deliberately carries no work body. Frozen role text and paths
// must be reinjected by the adapter before the independent agent READY.
type ResetRequest struct {
	MessageID  string
	RequestID  string
	Previous   SeatSnapshot
	Generation int64
	Role       config.ResolvedAgent
	ContextDir string
}

// ResetAdapter abstracts native reset/new/clear, reinjection and agent READY.
// Production adapters must protect the inspected input again at the native
// operation boundary; inspection alone cannot prevent a user's concurrent edit.
type ResetAdapter interface {
	Inspect(context.Context, SeatSnapshot) (InputState, error)
	Reset(context.Context, ResetRequest) (NativeBinding, error)
	WaitReady(context.Context, ResetRequest, NativeBinding) (Registration, error)
}

type Submission struct {
	Actor    ipc.Actor
	Native   NativeBinding
	Accepted bool
	Channel  string
}

// SubmitAdapter is independent of reset; only confirmed READY work reaches it.
// It must recheck/protect native input at the operation boundary, just as Reset.
type SubmitAdapter interface {
	Submit(context.Context, SeatSnapshot, store.Message) (Submission, error)
}

type SessionCoordinator struct {
	db     *store.Store
	reset  ResetAdapter
	submit SubmitAdapter
}

func NewSessionCoordinator(db *store.Store, reset ResetAdapter, submit SubmitAdapter) (*SessionCoordinator, error) {
	if db == nil {
		return nil, errors.New("session coordinator requires a store")
	}
	return &SessionCoordinator{db: db, reset: reset, submit: submit}, nil
}

func (s *SessionCoordinator) Assign(ctx context.Context, c SendMessage) (store.MessageRecord, error) {
	mode := c.Session
	if c.Mode != "" {
		if mode != "" && mode != c.Mode {
			return store.MessageRecord{}, &store.MessagingError{Code: "INVALID_REQUEST", Path: "session", Message: "mode and session must agree"}
		}
		mode = c.Mode
	}
	if mode == "" {
		mode = "config"
	}
	record, err := s.db.StoreMessage(ctx, store.MessageCommand{Actor: messageActor(c.Actor), RequestID: c.RequestID, Recipient: c.Recipient, TargetGeneration: c.TargetGeneration, Kind: "assign", Body: c.Body, Session: mode})
	if err != nil {
		return record, err
	}
	return s.Advance(ctx, c.Actor, record.Message.ID)
}

func sessionSeat(seat store.Seat) SeatSnapshot {
	return SeatSnapshot{RunID: seat.RunID, AgentID: seat.AgentID, Generation: seat.Generation, Tool: seat.Tool, Status: seat.Status,
		Native: NativeBinding{SessionID: seat.NativeSessionID, WorkspaceID: seat.WorkspaceID, SurfaceID: seat.SurfaceID}}
}

func seatActor(seat SeatSnapshot) ipc.Actor {
	return ipc.Actor{RunID: seat.RunID, AgentID: seat.AgentID, Generation: seat.Generation}
}

// Advance explicitly reconsiders queued work. It never replays a requested or
// unknown native action. Durable reconciliation always precedes native work.
func (s *SessionCoordinator) Advance(ctx context.Context, actor ipc.Actor, id string) (store.MessageRecord, error) {
	return s.advance(ctx, actor, id, nil)
}

// advance can stop after a confirmed reset so a recovery tick performs at most
// one native action. Independent READY remains part of the reset action.
func (s *SessionCoordinator) advance(ctx context.Context, actor ipc.Actor, id string, recovery *store.RetryDecision) (store.MessageRecord, error) {
	if err := s.db.ValidateMessageActor(ctx, messageActor(actor)); err != nil {
		return store.MessageRecord{}, err
	}
	w, err := s.db.SessionWork(ctx, id)
	if err != nil {
		return store.MessageRecord{}, err
	}
	if w.Record.Message.RunID != actor.RunID || w.Record.Message.Sender != actor.AgentID || w.Record.Message.SenderGeneration != actor.Generation {
		return store.MessageRecord{}, &store.MessagingError{Code: "ACTOR_MISMATCH", Path: "session.sender", Message: "only the original current sender can advance this assignment"}
	}
	var decision store.RecoveryDecision
	if recovery == nil {
		decision, err = s.Reconcile(ctx, actor, id)
	} else {
		decision, err = s.db.ReconcileRecoverySession(ctx, messageActor(actor), id)
	}
	if err != nil {
		return store.MessageRecord{}, err
	}
	if decision.State != "SAFE_TO_CONTINUE" && decision.State != "QUEUED" || decision.StopRequested {
		return w.Record, nil
	}
	if recovery != nil {
		class, _ := store.ClassifyRecovery(decision)
		if class == store.RecoveryPermanent || class == store.RecoveryManual {
			return w.Record, nil
		}
		previous := recovery.Reconcile
		if decision.Generation != previous.Generation || decision.NativeSession != previous.NativeSession || decision.Tool != previous.Tool || decision.WorkspaceID != previous.WorkspaceID || decision.SurfaceID != previous.SurfaceID {
			return w.Record, &store.MessagingError{Code: "GENERATION_MISMATCH", Path: "recovery.binding", Message: "binding changed after dependency check"}
		}
	}
	w, err = s.db.SessionWork(ctx, id)
	if err != nil {
		return store.MessageRecord{}, err
	}
	r := w.Record
	if r.Session == "" || (r.Delivery.Phase != "PENDING" && r.Delivery.Phase != "QUEUED") || r.Delivery.ResetPhase == "RESET_REQUESTED" {
		return r, nil
	}
	seat := sessionSeat(w.Seat)
	if r.Session == "fresh" && r.Delivery.ResetPhase == "NONE" {
		blocked := ""
		freshSupported := true
		if policy, ok := s.reset.(interface{ SupportsFresh() bool }); ok {
			freshSupported = policy.SupportsFresh()
		}
		var request ResetRequest
		if w.Seat.Status != Ready || w.Seat.CurrentMessageID != "" {
			blocked = "SEAT_BUSY"
		} else if s.reset == nil || !freshSupported {
			blocked = "RESET_ADAPTER_UNIMPLEMENTED"
		} else if seat.Native.validate() != nil {
			blocked = "BINDING_UNVERIFIED"
		} else {
			request, err = s.resetRequest(ctx, w, seat)
			if err != nil {
				return r, err
			}
			input, inspectErr := s.reset.Inspect(ctx, request.Previous)
			if permanentRecoveryError(inspectErr) {
				return r, inspectErr
			}
			switch {
			case inspectErr != nil || !input.Verified || input.Actor != seatActor(seat) || input.Native != seat.Native:
				blocked = "INPUT_UNVERIFIED"
			case input.Busy:
				blocked = "SEAT_BUSY"
			case input.Draft != "" || input.IncompleteInput:
				blocked = "INPUT_DRAFT"
			}
		}
		var claimed bool
		w, claimed, err = s.beginAction(ctx, id, w.Seat, "reset", blocked, recovery)
		if err != nil || !claimed {
			return w.Record, err
		}
		native, resetErr := s.reset.Reset(ctx, request)
		if resetErr != nil || native.validate() != nil || native.SessionID == seat.Native.SessionID || native.WorkspaceID != seat.Native.WorkspaceID || native.SurfaceID != seat.Native.SurfaceID {
			return s.unknown(ctx, id, "reset")
		}
		claim, readyErr := s.reset.WaitReady(ctx, request, native)
		expected := seatActor(seat)
		expected.Generation++
		if readyErr != nil || claim.Actor != expected || claim.Native != native || claim.Tool != seat.Tool || claim.Status != Ready {
			return s.unknown(ctx, id, "reset")
		}
		next := w.Seat
		next.Generation, next.NativeSessionID = expected.Generation, native.SessionID
		if err := s.db.ConfirmSessionReset(ctx, id, w.Seat, next); err != nil {
			return w.Record, err
		}
		if recovery != nil {
			w, err = s.db.SessionWork(ctx, id)
			return w.Record, err
		}
		w, err = s.db.SessionWork(ctx, id)
		if err != nil {
			return store.MessageRecord{}, err
		}
		seat = sessionSeat(w.Seat)
	}
	blocked := ""
	if s.submit == nil {
		blocked = "SUBMIT_ADAPTER_UNIMPLEMENTED"
	} else if seat.Native.validate() != nil {
		blocked = "BINDING_UNVERIFIED"
	} else if s.reset == nil {
		blocked = "INPUT_UNVERIFIED"
	} else if w.Seat.Status == Ready && w.Seat.CurrentMessageID == "" {
		input, inspectErr := s.reset.Inspect(ctx, seat)
		if permanentRecoveryError(inspectErr) {
			return r, inspectErr
		}
		switch {
		case inspectErr != nil || !input.Verified || input.Actor != seatActor(seat) || input.Native != seat.Native:
			blocked = "INPUT_UNVERIFIED"
		case input.Busy:
			blocked = "SEAT_BUSY"
		case input.Draft != "" || input.IncompleteInput:
			blocked = "INPUT_DRAFT"
		}
	}
	w, claimed, err := s.beginAction(ctx, id, w.Seat, "submit", blocked, recovery)
	if err != nil || !claimed {
		return w.Record, err
	}
	result, submitErr := s.submit.Submit(ctx, seat, w.Record.Message)
	if submitErr != nil || !result.Accepted || result.Actor != seatActor(seat) || result.Native != seat.Native || !identityText(result.Channel) || result.Channel == "UNVERIFIED" {
		return s.unknown(ctx, id, "submit")
	}
	if err := s.db.ConfirmSessionSubmit(ctx, id, w.Seat, result.Channel); err != nil {
		return w.Record, err
	}
	w, err = s.db.SessionWork(ctx, id)
	return w.Record, err
}

func (s *SessionCoordinator) beginAction(ctx context.Context, id string, seat store.Seat, action, blocked string, recovery *store.RetryDecision) (store.SessionWork, bool, error) {
	if recovery != nil {
		return s.db.BeginRecoverySessionAction(ctx, id, seat, action, blocked, recovery.JournalSeq)
	}
	return s.db.BeginSessionAction(ctx, id, seat, action, blocked)
}

func (s *SessionCoordinator) unknown(ctx context.Context, id, action string) (store.MessageRecord, error) {
	// A cancelled context still leaves RESET_REQUESTED/SUBMIT_REQUESTED committed.
	// Do not replace it with a misleading retryable phase if this write fails.
	if err := s.db.NoteSessionUnknown(ctx, id, action); err != nil {
		return store.MessageRecord{}, err
	}
	w, err := s.db.SessionWork(ctx, id)
	return w.Record, err
}

func (s *SessionCoordinator) resetRequest(ctx context.Context, w store.SessionWork, seat SeatSnapshot) (ResetRequest, error) {
	run, err := s.db.GetRun(ctx)
	if err != nil {
		return ResetRequest{}, err
	}
	var frozen config.Resolved
	if err := json.Unmarshal([]byte(run.ConfigSnapshot), &frozen); err != nil {
		return ResetRequest{}, &RecoveryError{Code: "CONFIGURATION_ERROR"}
	}
	plan, err := Compile(&frozen, Options{})
	if err != nil {
		return ResetRequest{}, &RecoveryError{Code: "CONFIGURATION_ERROR"}
	}
	if _, err := NewSnapshot(plan, run.RunID); err != nil {
		return ResetRequest{}, &RecoveryError{Code: "CONFIGURATION_ERROR"}
	}
	if frozen.Digest != run.ConfigDigest {
		return ResetRequest{}, &RecoveryError{Code: "CONFIGURATION_ERROR"}
	}
	role, ok := frozen.Agents[seat.AgentID]
	if !ok || role.Tool != seat.Tool {
		return ResetRequest{}, &RecoveryError{Code: "CONFIGURATION_ERROR"}
	}
	seat.Cwd, seat.Role, seat.Permissions, seat.Session = role.Cwd, role.Role, role.Permissions, w.Record.Session
	return ResetRequest{MessageID: w.Record.Message.ID, RequestID: w.Record.Message.RequestID, Previous: seat, Generation: seat.Generation + 1, Role: role, ContextDir: filepath.Join(frozen.ContextDir, run.RunID)}, nil
}
