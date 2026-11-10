package coordinator

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"

	"agentflow/internal/config"
	"agentflow/internal/ipc"
	"agentflow/internal/runtime"
	"agentflow/internal/store"
)

// OpenCMUXTransport installs explicit foreground message and workflow transport.
// Opening/serving never drains, creates surfaces, resets or inspects terminal UI.
func OpenCMUXTransport(ctx context.Context, dir, runID, binary string) (*Coordinator, error) {
	c, dir, err := lockRun(dir, runID, []string{"agentflow", "coordinator", "--cmux-transport"})
	if err != nil {
		return nil, err
	}
	fail := func(err error) (*Coordinator, error) { _ = c.Close(); return nil, err }
	c.db, err = store.OpenExisting(filepath.Join(dir, "state.db"), true)
	if err != nil {
		return fail(err)
	}
	run, err := c.db.GetRun(ctx)
	if err != nil {
		return fail(err)
	}
	if run.RunID != runID {
		return fail(&ipc.Error{Code: ipc.ActorMismatch, Message: "transport run differs from store"})
	}
	var resolved config.Resolved
	resolveErr := json.Unmarshal([]byte(run.ConfigSnapshot), &resolved)
	if resolved.Workflow != nil && (resolveErr != nil || resolved.Digest != run.ConfigDigest) {
		return fail(errors.New("cmux transport snapshot differs from stored run"))
	}
	bindings, err := c.db.CMUXRegistrations(ctx, runID)
	if err != nil {
		return fail(err)
	}
	if len(bindings) == 0 {
		return fail(&ipc.Error{Code: "SURFACE_UNBOUND", Message: "run has no cmux launcher registrations"})
	}
	t, err := runtime.NewCMUXTransport(c.db, binary)
	if err != nil {
		return fail(err)
	}
	if workflowUsesOnlyOpenCodeWorkers(&resolved) {
		adapter, err := runtime.NewCMUXOpenCode(ctx, t, dir, &resolved)
		if err != nil {
			return fail(err)
		}
		svc, err := runtime.NewMessageServiceWithAdapters(c.db, adapter, adapter)
		if err != nil {
			return fail(err)
		}
		sessions, err := runtime.NewSessionCoordinator(c.db, adapter, adapter)
		if err != nil {
			return fail(err)
		}
		runner, err := runtime.NewWorkflowRunner(sessions)
		if err != nil {
			return fail(err)
		}
		c.server, err = ipc.Listen(filepath.Join(dir, "ipc.sock"), cmuxHandler(handler(c.db, svc, runner), t), ipc.ServerOptions{ValidateActor: svc.ValidateActor})
		if err != nil {
			return fail(err)
		}
		return c, nil
	}
	svc, err := runtime.NewMessageService(c.db)
	if err != nil {
		return fail(err)
	}
	runner, err := runtime.NewWorkflowTransportRunner(c.db, t)
	if err != nil {
		return fail(err)
	}
	c.server, err = ipc.Listen(filepath.Join(dir, "ipc.sock"), cmuxHandler(handler(c.db, svc, runner), t), ipc.ServerOptions{ValidateActor: svc.ValidateActor})
	if err != nil {
		return fail(err)
	}
	return c, nil
}

func workflowUsesOnlyOpenCodeWorkers(resolved *config.Resolved) bool {
	if resolved == nil || resolved.Workflow == nil {
		return false
	}
	lead := resolved.Lead
	steps := resolved.Workflow.RuntimeSteps()
	if len(steps) == 0 {
		return false
	}
	for _, step := range steps {
		if step.Execute.Agent == lead || !openCodeSeat(resolved, step.Execute.Agent) {
			return false
		}
		approver := step.Reviewer
		if resolved.Workflow.Kind() == config.WorkflowApproval {
			approver = step.Approver
		}
		if approver != lead && !openCodeSeat(resolved, approver) {
			return false
		}
	}
	return true
}

func openCodeSeat(resolved *config.Resolved, id string) bool {
	agent := resolved.Agents[id]
	return id != "" && agent.Tool == "opencode" && agent.Session == "persistent"
}

func cmuxHandler(base ipc.Handler, t *runtime.CMUXTransport) ipc.Handler {
	return func(ctx context.Context, req ipc.Request) (any, error) {
		if req.Method != "cmux-send" && req.Method != "cmux-inbox" {
			return base(ctx, req)
		}
		method := "send"
		if req.Method == "cmux-inbox" {
			method = "stop"
		}
		p, err := decodeParams(method, req.Params)
		if err != nil {
			return nil, err
		}
		r := Result{Status: "QUERIED", CapabilityStatus: "PARTIAL", RuntimeCommunication: "UNVERIFIED", Diagnostics: []runtime.Diagnostic{}}
		if req.Method == "cmux-send" {
			w, e := t.Send(ctx, runtime.SendMessage{Actor: req.Actor, RequestID: req.RequestID, Recipient: p.Recipient, TargetGeneration: p.TargetGeneration, Body: p.Body})
			r.CMUXSend, err = &w, e
			r.Status = w.Record.Delivery.Phase
			if r.Status == "CMUX_SEND_REQUESTED" {
				r.Status = "UNVERIFIED"
			}
		} else {
			inbox, e := t.Inbox(ctx, req.Actor)
			r.CMUXInbox, err = &inbox, e
		}
		if err != nil {
			r.Status = "FAILED"
			d := runtime.Diagnostic{Code: "CMUX_TRANSPORT_FAILED", Path: "cmux.transport", Message: "transport failed; inspect retained raw evidence"}
			var public *store.MessagingError
			var wire *ipc.Error
			if errors.As(err, &public) {
				d.Code, d.Path, d.Message = public.Code, public.Path, public.Message
			} else if errors.Is(err, store.ErrIdempotencyConflict) {
				d.Code = ipc.IdempotencyConflict
			} else if errors.As(err, &wire) {
				d.Code, d.Message = wire.Code, wire.Message
			}
			r.Diagnostics = []runtime.Diagnostic{d}
		} else if r.Status == "UNVERIFIED" {
			code, message := "CMUX_SEND_UNVERIFIED", "send outcome unverified; durable intent forbids replay"
			if w := r.CMUXSend; w != nil && w.Evidence != nil && w.Evidence.RecipientHasAgent != nil && !*w.Evidence.RecipientHasAgent {
				code, message = "CMUX_RECIPIENT_INACTIVE", "cmux queued the message but found no active recipient agent; the durable intent will not be resent"
			}
			r.Diagnostics = []runtime.Diagnostic{{Code: code, Path: "cmux.transport", Message: message}}
		}
		return r, nil
	}
}
