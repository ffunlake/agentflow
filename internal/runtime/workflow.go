package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"sync"

	"agentflow/internal/ipc"
	"agentflow/internal/loopcore"
	"agentflow/internal/store"
)

// WorkflowRunner consumes only the dispatch produced by a newly accepted event.
// It has no worker loop, retry scheduler or native event collector. Adapters must
// return submission evidence before reporting events through the existing IPC.
type WorkflowRunner struct {
	sessions       *SessionCoordinator
	transport      WorkflowTransportAdapter
	yieldLeadActor bool
	mu             sync.Mutex
}

// WorkflowTransportAdapter addresses already launched surfaces. Dispatch claims
// the existing outbox; Collect may commit only bound structured workflow replies.
// Both operations are foreground and bounded, and never imply native READY.
type WorkflowTransportAdapter interface {
	Dispatch(context.Context, ipc.Actor, string) (store.MessageRecord, bool, error)
	Collect(context.Context, store.Message, loopcore.Cursor) (*store.WorkflowResult, error)
}

func NewWorkflowTransportRunner(db *store.Store, transport WorkflowTransportAdapter) (*WorkflowRunner, error) {
	if db == nil || transport == nil {
		return nil, errors.New("workflow transport requires store and adapter")
	}
	return &WorkflowRunner{sessions: &SessionCoordinator{db: db}, transport: transport}, nil
}

type WorkflowRunResult struct {
	Workflow        store.WorkflowResult
	Dispatch        *store.MessageRecord
	DispatchError   error            // the workflow transaction already committed
	Current         *loopcore.Cursor // latest cursor after native result collection
	ActionRequested bool             // this call crossed a native action claim, not historical SENT
	AwaitingActor   bool             // the dispatch is addressed to the lead; let that seat handle it
}

// WorkflowResultAdapter collects a previously completed native turn. It must not
// submit work; SessionCoordinator confirms SENT before this hook is called.
type WorkflowResultAdapter interface {
	WorkflowReply(context.Context, SeatSnapshot, store.Message, loopcore.Cursor) (ReplyMessage, loopcore.Event, error)
}

func NewWorkflowRunner(sessions *SessionCoordinator) (*WorkflowRunner, error) {
	if sessions == nil || sessions.db == nil {
		return nil, errors.New("workflow runner requires a session coordinator")
	}
	_, yieldLead := sessions.submit.(*CMUXOpenCode)
	return &WorkflowRunner{sessions: sessions, yieldLeadActor: yieldLead}, nil
}

func (r *WorkflowRunner) Advance(ctx context.Context, actor ipc.Actor, event loopcore.Event) (WorkflowRunResult, error) {
	return r.tick(ctx, actor, event, nil)
}

func (r *WorkflowRunner) Reply(ctx context.Context, reply ReplyMessage, event loopcore.Event) (WorkflowRunResult, error) {
	if r.transport != nil {
		return WorkflowRunResult{}, &ipc.Error{Code: ipc.InvalidRequest, Message: "cmux workflow replies require bound structured inbox evidence"}
	}
	return r.tick(ctx, reply.Actor, event, &reply)
}

func (r *WorkflowRunner) tick(ctx context.Context, actor ipc.Actor, event loopcore.Event, reply *ReplyMessage) (WorkflowRunResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	db := r.sessions.db
	before, err := db.WorkflowCursor(ctx, actor.RunID)
	if err != nil {
		return WorkflowRunResult{}, err
	}
	var saved store.WorkflowResult
	if reply == nil {
		if r.transport != nil {
			saved, err = db.AdvanceWorkflow(ctx, messageActor(actor), event)
		} else {
			saved, err = db.AdvanceWorkflowWithSessions(ctx, messageActor(actor), event)
		}
	} else {
		saved, err = db.ReplyWorkflowWithSessions(ctx, store.MessageCommand{Actor: messageActor(actor),
			RequestID: reply.RequestID, Kind: "reply", ReplyTo: reply.MessageID, Body: reply.Body, ReportStatus: reply.Status}, event)
	}
	out := WorkflowRunResult{Workflow: saved}
	if err != nil {
		return out, err
	}
	// Exact event retries (including after restart/stop) return the durable
	// receipt only. They never retry a queued, failed or unknown submission.
	if saved.Message == nil || saved.Cursor.Revision <= before.Revision {
		return out, nil
	}
	m := saved.Message
	if r.transport != nil {
		record, claimed, err := r.transport.Dispatch(ctx, ipc.Actor{RunID: m.RunID, AgentID: m.Sender, Generation: m.SenderGeneration}, m.ID)
		out.DispatchError, out.ActionRequested = err, claimed
		if record.Message.ID != "" {
			out.Dispatch = &record
		}
		c := saved.Cursor
		out.Current = &c
		return out, nil
	}
	record, err := r.sessions.Advance(ctx, ipc.Actor{RunID: m.RunID, AgentID: m.Sender, Generation: m.SenderGeneration}, m.ID)
	out.DispatchError = err
	if record.Message.ID != "" {
		out.Dispatch = &record
		out.ActionRequested = record.Delivery.Attempt > 0 || record.Delivery.ResetPhase != "NONE"
	}
	if _, ok := r.sessions.submit.(WorkflowResultAdapter); ok && err == nil {
		r.drain(ctx, actor, &out)
	}
	return out, nil
}

// Drain explicitly resumes one current outbox/result after restart. CMUX
// callers poll through the CLI; opening a coordinator and exact event replays
// never implicitly drain old work.
func (r *WorkflowRunner) Drain(ctx context.Context, actor ipc.Actor, revision int64) (WorkflowRunResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out, err := r.Status(ctx, actor)
	if err != nil {
		return out, err
	}
	if out.Workflow.Cursor.Revision != revision {
		c := out.Workflow.Cursor
		if revision < c.Revision && (c.Phase == loopcore.Completed || c.Phase == loopcore.Paused || c.Phase == loopcore.Exhausted) {
			out.Current = &c
			return out, nil
		}
		return WorkflowRunResult{}, loopcore.ErrTransition
	}
	r.drain(ctx, actor, &out)
	return out, nil
}

// Status returns the current cursor without collecting or submitting work.
func (r *WorkflowRunner) Status(ctx context.Context, actor ipc.Actor) (WorkflowRunResult, error) {
	if err := r.sessions.db.ValidateMessageActor(ctx, messageActor(actor)); err != nil {
		return WorkflowRunResult{}, err
	}
	run, err := r.sessions.db.GetRun(ctx)
	if err != nil {
		return WorkflowRunResult{}, err
	}
	var frozen struct {
		Lead string `json:"lead"`
	}
	if err := json.Unmarshal([]byte(run.ConfigSnapshot), &frozen); err != nil || actor.AgentID != frozen.Lead {
		return WorkflowRunResult{}, errors.New("workflow drain requires current lead")
	}
	c, err := r.sessions.db.WorkflowCursor(ctx, actor.RunID)
	if err != nil {
		return WorkflowRunResult{}, err
	}
	return WorkflowRunResult{Workflow: store.WorkflowResult{Cursor: c}, Current: &c}, nil
}

func (r *WorkflowRunner) drain(ctx context.Context, actor ipc.Actor, out *WorkflowRunResult) {
	if r.transport != nil {
		r.drainTransport(ctx, actor, out)
		return
	}
	collector, ok := r.sessions.submit.(WorkflowResultAdapter)
	if !ok {
		out.DispatchError = errors.New("workflow result collector unavailable")
		return
	}
	for range 128 { // bounded foreground work; another explicit drain may continue
		c, err := r.sessions.db.WorkflowCursor(ctx, actor.RunID)
		if err != nil {
			out.DispatchError = err
			return
		}
		out.Current = &c
		if c.Phase == loopcore.Paused || c.Phase == loopcore.Exhausted || c.Phase == loopcore.Completed || c.DispatchID == "" {
			return
		}
		w, err := r.sessions.db.SessionWork(ctx, c.DispatchID)
		if err != nil {
			out.DispatchError = err
			return
		}
		m := w.Record.Message
		if r.yieldLeadActor && m.Recipient == actor.AgentID {
			out.Dispatch = &w.Record
			out.AwaitingActor = true
			return
		}
		if w.Record.Delivery.Attempt == 0 {
			record, err := r.sessions.Advance(ctx, ipc.Actor{RunID: m.RunID, AgentID: m.Sender, Generation: m.SenderGeneration}, m.ID)
			out.Dispatch = &record
			if record.Message.ID != "" && (record.Delivery.Attempt > 0 || record.Delivery.ResetPhase != "NONE") {
				out.ActionRequested = true
			}
			if err != nil {
				out.DispatchError = err
				return
			}
			w, err = r.sessions.db.SessionWork(ctx, m.ID)
			if err != nil {
				out.DispatchError = err
				return
			}
		}
		out.Dispatch = &w.Record
		if !w.SubmissionConfirmed {
			out.DispatchError = errors.New("workflow native submission queued or unknown; inspect delivery before continuing")
			return
		}
		reply, event, err := collector.WorkflowReply(ctx, sessionSeat(w.Seat), m, c)
		if err == nil {
			_, err = r.sessions.db.ReplyWorkflowWithSessions(ctx, store.MessageCommand{Actor: messageActor(reply.Actor), RequestID: reply.RequestID,
				Kind: "reply", ReplyTo: reply.MessageID, Body: reply.Body, ReportStatus: reply.Status}, event)
		}
		if err != nil {
			out.DispatchError = err
			return
		}
		// The 128th result may itself finish or pause the workflow. Publish the
		// committed cursor before checking the foreground bound, just as for all
		// earlier results; only unfinished work needs another explicit drain.
		c, err = r.sessions.db.WorkflowCursor(ctx, actor.RunID)
		if err != nil {
			out.DispatchError = err
			return
		}
		out.Current = &c
		if c.Phase == loopcore.Paused || c.Phase == loopcore.Exhausted || c.Phase == loopcore.Completed || c.DispatchID == "" {
			return
		}
	}
	out.DispatchError = errors.New("foreground workflow action limit reached; explicitly drain to continue")
}

func (r *WorkflowRunner) drainTransport(ctx context.Context, actor ipc.Actor, out *WorkflowRunResult) {
	c, err := r.sessions.db.WorkflowCursor(ctx, actor.RunID)
	if err != nil {
		out.DispatchError = err
		return
	}
	out.Current = &c
	if c.Phase == loopcore.Paused || c.Phase == loopcore.Exhausted || c.Phase == loopcore.Completed || c.DispatchID == "" {
		return
	}
	m, err := r.sessions.db.GetMessage(ctx, c.DispatchID)
	if err != nil {
		out.DispatchError = err
		return
	}
	record, claimed, err := r.transport.Dispatch(ctx, ipc.Actor{RunID: m.RunID, AgentID: m.Sender, Generation: m.SenderGeneration}, m.ID)
	if record.Message.ID != "" {
		out.Dispatch = &record
	}
	out.ActionRequested = claimed
	if err != nil {
		out.DispatchError = err
		return
	}
	if record.Delivery.Phase != "CMUX_QUEUED" && record.Delivery.Phase != "ACKED" {
		out.DispatchError = errors.New("cmux workflow send outcome unknown; durable intent forbids replay")
		return
	}
	saved, err := r.transport.Collect(ctx, m, c)
	if err != nil {
		out.DispatchError = err
		return
	}
	if saved != nil {
		out.Workflow = *saved
		current := saved.Cursor
		out.Current = &current
		return
	}
	if c.Phase == loopcore.Reviewing && m.Recipient == actor.AgentID {
		out.AwaitingActor = true
	}
}
