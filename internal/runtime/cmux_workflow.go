package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"agentflow/internal/ipc"
	"agentflow/internal/loopcore"
	"agentflow/internal/store"
)

// CMUXWorkflowReply is the text body of a native message addressed to the
// dispatch sender's inbox. Native sender_name must equal Actor.AgentID and native
// source/target UUIDs must match current launch registrations in the commit tx.
type CMUXWorkflowReply struct {
	V         int               `json:"v"`
	Type      string            `json:"type"`
	RequestID string            `json:"request_id"`
	Actor     ipc.Actor         `json:"actor"`
	MessageID string            `json:"message_id"`
	Body      string            `json:"body"`
	Status    string            `json:"status"`
	Workflow  CMUXWorkflowEvent `json:"workflow"`
}

type CMUXWorkflowEvent struct {
	Revision int64             `json:"revision"`
	Kind     string            `json:"kind"`
	Handoff  *loopcore.Handoff `json:"handoff,omitempty"`
	Accepted []bool            `json:"accepted,omitempty"`
	Reason   string            `json:"reason,omitempty"`
}

func exactWorkflowObject(raw []byte, required, optional []string) (map[string]json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if uniqueJSON(raw) != nil || json.Unmarshal(raw, &fields) != nil || fields == nil {
		return nil, errors.New("invalid workflow JSON object")
	}
	allowed := map[string]bool{}
	for _, k := range append(append([]string{}, required...), optional...) {
		allowed[k] = true
	}
	for k, value := range fields {
		if !allowed[k] || string(value) == "null" {
			return nil, errors.New("unknown or null workflow field")
		}
	}
	for _, k := range required {
		if fields[k] == nil {
			return nil, errors.New("missing workflow field")
		}
	}
	return fields, nil
}

func parseCMUXWorkflowReply(body string) (*CMUXWorkflowReply, error) {
	var probe map[string]json.RawMessage
	if json.Unmarshal([]byte(body), &probe) != nil || probe == nil {
		return nil, nil
	}
	var typ string
	_ = json.Unmarshal(probe["type"], &typ)
	if typ == "workflow_dispatch" {
		return nil, nil
	}
	if typ != "workflow_reply" && probe["workflow"] == nil {
		return nil, nil
	}
	fields, err := exactWorkflowObject([]byte(body), []string{"v", "type", "request_id", "actor", "message_id", "body", "status", "workflow"}, nil)
	if err != nil {
		return nil, err
	}
	if _, err := exactWorkflowObject(fields["actor"], []string{"runId", "agentId", "generation"}, nil); err != nil {
		return nil, err
	}
	w, err := exactWorkflowObject(fields["workflow"], []string{"revision", "kind"}, []string{"handoff", "accepted", "reason"})
	if err != nil {
		return nil, err
	}
	if h := w["handoff"]; h != nil {
		if _, err := exactWorkflowObject(h, []string{"summary", "evidence", "next"}, nil); err != nil {
			return nil, err
		}
	}
	var reply CMUXWorkflowReply
	if json.Unmarshal([]byte(body), &reply) != nil || reply.V != 1 || reply.Type != "workflow_reply" || strings.TrimSpace(reply.RequestID) == "" || reply.Actor.RunID == "" || reply.Actor.AgentID == "" || reply.Actor.Generation < 1 || reply.MessageID == "" || strings.TrimSpace(reply.Body) == "" {
		return nil, errors.New("invalid workflow reply identity")
	}
	return &reply, nil
}

func (t *CMUXTransport) Dispatch(ctx context.Context, actor ipc.Actor, id string) (store.MessageRecord, bool, error) {
	if err := ctx.Err(); err != nil {
		return store.MessageRecord{}, false, err
	}
	w, claimed, err := t.db.BeginCMUXDispatch(ctx, messageActor(actor), id)
	if err != nil || !claimed {
		return w.Record, claimed, err
	}
	if !validCMUXBody(w.WireBody) || !cmuxUUID.MatchString(w.Binding.SurfaceID) || !cmuxUUID.MatchString(w.Binding.WorkspaceID) {
		return w.Record, claimed, errors.New("invalid cmux workflow dispatch")
	}
	source, sourceErr := t.db.CMUXSurface(ctx, messageActor(actor))
	if sourceErr != nil {
		e := store.CMUXEvidence{ExitCode: -1, Stderr: sourceErr.Error()}
		saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		w, err = t.db.FinishCMUXSend(saveCtx, messageActor(actor), w, e)
		return w.Record, claimed, err
	}
	e := t.capture(ctx, []string{"agent", "message", w.Binding.SurfaceID, "--from", "agentflow", "--json", "--", w.WireBody}, source)
	if e.ExitCode == 0 && !e.Truncated {
		if parsed, err := parseCMUXMessage([]byte(e.Stdout), e); err == nil {
			e = parsed
		}
	}
	saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	w, err = t.db.FinishCMUXSend(saveCtx, messageActor(actor), w, e)
	if err == nil && w.Record.Delivery.Phase != "CMUX_QUEUED" {
		err = errors.New("cmux workflow send unverified; durable intent forbids replay")
	}
	return w.Record, claimed, err
}

func (t *CMUXTransport) Collect(ctx context.Context, m store.Message, c loopcore.Cursor) (*store.WorkflowResult, error) {
	inbox, err := t.Inbox(ctx, ipc.Actor{RunID: m.RunID, AgentID: m.Sender, Generation: m.SenderGeneration})
	if err != nil {
		return nil, err
	}
	for _, evidence := range inbox.Messages {
		reply, err := parseCMUXWorkflowReply(evidence.Body)
		if err != nil {
			return nil, err
		}
		if reply == nil {
			continue
		}
		event := loopcore.Event{ID: reply.RequestID, Actor: reply.Actor.AgentID, MessageID: reply.MessageID, Revision: reply.Workflow.Revision, Kind: reply.Workflow.Kind, Handoff: reply.Workflow.Handoff, Accepted: reply.Workflow.Accepted, Reason: reply.Workflow.Reason}
		saved, err := t.db.ReplyCMUXWorkflow(ctx, store.MessageCommand{Actor: messageActor(reply.Actor), RequestID: reply.RequestID, Kind: "reply", ReplyTo: reply.MessageID, Body: reply.Body, ReportStatus: reply.Status}, event,
			store.CMUXWorkflowReceipt{Inbox: inbox.Binding, Event: evidence, RequestID: reply.RequestID})
		if err != nil {
			return nil, err
		}
		if saved.Cursor.Revision > c.Revision {
			return &saved, nil
		}
	}
	return nil, nil
}
