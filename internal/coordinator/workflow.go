package coordinator

import (
	"context"
	"encoding/json"
	"errors"

	"agentflow/internal/ipc"
	"agentflow/internal/loopcore"
	"agentflow/internal/runtime"
	"agentflow/internal/store"
)

// WorkflowParams excludes identity and request/message IDs: the envelope and
// existing reply route supply those. Revision is always an explicit fence.
type WorkflowParams struct {
	Revision int64             `json:"revision"`
	Kind     string            `json:"kind"`
	Task     string            `json:"task,omitempty"`
	Handoff  *loopcore.Handoff `json:"handoff,omitempty"`
	Accepted []bool            `json:"accepted,omitempty"`
	Reason   string            `json:"reason,omitempty"`
}

func validateWorkflowParams(raw json.RawMessage, p *WorkflowParams) error {
	if p == nil {
		return nil
	}
	var outer map[string]json.RawMessage
	_ = json.Unmarshal(raw, &outer)
	value := outer["workflow"]
	if err := validateParamsObject(value, map[string]bool{"revision": true, "kind": true, "task": true, "handoff": true, "accepted": true, "reason": true}); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(value, &fields)
	if fields["revision"] == nil || fields["kind"] == nil {
		return &ipc.Error{Code: ipc.InvalidRequest, Message: "workflow requires revision and kind"}
	}
	if p.Handoff != nil {
		return validateParamsObject(fields["handoff"], map[string]bool{"summary": true, "evidence": true, "next": true})
	}
	return nil
}

func workflowEvent(req ipc.Request, p Params) loopcore.Event {
	w := p.Workflow
	return loopcore.Event{ID: req.RequestID, Actor: req.Actor.AgentID, MessageID: p.MessageID,
		Revision: w.Revision, Kind: w.Kind, Task: w.Task, Handoff: w.Handoff, Accepted: w.Accepted, Reason: w.Reason}
}

func workflowControl(ctx context.Context, db *store.Store, runner *runtime.WorkflowRunner, req ipc.Request, p Params) (any, error) {
	if p.Workflow != nil && p.Workflow.Kind == "status" && runner != nil {
		r, err := runner.Status(ctx, req.Actor)
		out := workflowRunResult(r, err)
		if err == nil {
			out.Status = "WORKFLOW_STATUS"
		}
		return out, nil
	}
	if p.Workflow != nil && p.Workflow.Kind == "drain" && runner != nil {
		r, err := runner.Drain(ctx, req.Actor, p.Workflow.Revision)
		return workflowRunResult(r, err), nil
	}
	if p.Workflow == nil || (p.Workflow.Kind != "start" && p.Workflow.Kind != "resume") {
		return nil, &ipc.Error{Code: ipc.InvalidRequest, Message: "workflow control requires start or resume; worker events use reply"}
	}
	if runner != nil {
		r, err := runner.Advance(ctx, req.Actor, workflowEvent(req, p))
		return workflowRunResult(r, err), nil
	}
	r, err := db.AdvanceWorkflow(ctx, store.MessageActor{RunID: req.Actor.RunID, AgentID: req.Actor.AgentID, Generation: req.Actor.Generation}, workflowEvent(req, p))
	return workflowResult(r, err), nil
}

func workflowReply(ctx context.Context, db *store.Store, runner *runtime.WorkflowRunner, req ipc.Request, p Params) (any, error) {
	event := workflowEvent(req, p)
	if runner != nil {
		r, err := runner.Reply(ctx, runtime.ReplyMessage{Actor: req.Actor, RequestID: req.RequestID,
			MessageID: p.MessageID, Body: p.Body, Status: p.Status}, event)
		return workflowRunResult(r, err), nil
	}
	r, err := db.ReplyWorkflow(ctx, store.MessageCommand{Actor: store.MessageActor{RunID: req.Actor.RunID, AgentID: req.Actor.AgentID, Generation: req.Actor.Generation},
		RequestID: req.RequestID, Kind: "reply", ReplyTo: p.MessageID, Body: p.Body, ReportStatus: p.Status}, event)
	return workflowResult(r, err), nil
}

func workflowRunResult(r runtime.WorkflowRunResult, err error) Result {
	out := workflowResult(r.Workflow, err)
	out.Dispatch = r.Dispatch
	out.WorkflowCurrent = r.Current
	out.AwaitingActor = r.AwaitingActor
	if r.ActionRequested {
		*out.Executed = true
	}
	if r.DispatchError != nil {
		d := runtime.Diagnostic{Code: "WORKFLOW_DISPATCH_FAILED", Path: "workflow.dispatch", Message: "workflow receipt retained; foreground dispatch or result collection failed; inspect evidence before continuing"}
		var public *store.MessagingError
		if errors.As(r.DispatchError, &public) {
			d.Code, d.Path, d.Message = public.Code, public.Path, public.Message
		} else if errors.Is(r.DispatchError, loopcore.ErrTransition) {
			d.Code, d.Message = "WORKFLOW_INVALID_TRANSITION", "inbox event does not match current workflow revision, role, dispatch or terminal status"
		} else if errors.Is(r.DispatchError, store.ErrIdempotencyConflict) {
			d.Code = ipc.IdempotencyConflict
		}
		out.Diagnostics = append(out.Diagnostics, d)
	}
	return out
}

func workflowResult(r store.WorkflowResult, err error) Result {
	out := Result{Status: "WORKFLOW_ADVANCED", CapabilityStatus: "PARTIAL", RuntimeCommunication: "UNVERIFIED", Workflow: &r, Record: r.Reply, Executed: new(bool), Diagnostics: []runtime.Diagnostic{}}
	if err == nil {
		return out
	}
	out.Status, out.Workflow, out.Record = "FAILED", nil, nil
	d := runtime.Diagnostic{Code: "WORKFLOW_FAILED", Path: "workflow", Message: "workflow operation failed"}
	var public *store.MessagingError
	switch {
	case errors.As(err, &public):
		d = runtime.Diagnostic{Code: public.Code, Path: public.Path, Message: public.Message}
	case errors.Is(err, store.ErrIdempotencyConflict):
		d = runtime.Diagnostic{Code: ipc.IdempotencyConflict, Path: "request_id", Message: "request ID conflicts with a stored operation"}
	case errors.Is(err, loopcore.ErrTransition):
		d.Code, d.Message = "WORKFLOW_INVALID_TRANSITION", "event does not match the current workflow revision, role, dispatch or transition requirements"
	}
	out.Diagnostics = []runtime.Diagnostic{d}
	return out
}
