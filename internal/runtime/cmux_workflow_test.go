package runtime

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"agentflow/internal/ipc"
	"agentflow/internal/loopcore"
	"agentflow/internal/store"
)

func TestCMUXWorkflowStrictReplyProtocol(t *testing.T) {
	valid, _ := json.Marshal(CMUXWorkflowReply{V: 1, Type: "workflow_reply", RequestID: "reply-1", Actor: ipc.Actor{RunID: "run", AgentID: "builder", Generation: 1}, MessageID: "dispatch-1", Body: "result", Status: "DONE", Workflow: CMUXWorkflowEvent{Revision: 1, Kind: "handoff", Handoff: &loopcore.Handoff{Summary: "work", Evidence: []string{"log"}, Next: "review"}}})
	if r, err := parseCMUXWorkflowReply(string(valid)); err != nil || r == nil {
		t.Fatal(r, err)
	}
	for _, pair := range [][2]string{
		{`"v":1`, `"v":2`},
		{`"request_id":"reply-1"`, `"request_id":null`},
		{`"request_id":"reply-1"`, `"Request_id":"reply-1"`},
		{`"generation":1`, `"generation":1,"generation":2`},
		{`"runId":"run"`, `"RunId":"run"`},
		{`"revision":1`, `"revision":null`},
		{`"kind":"handoff"`, `"Kind":"handoff"`},
		{`"evidence":["log"]`, `"evidence":null`},
		{`"next":"review"`, `"next":"review","Next":"other"`},
		{`"accepted"`, `"Accepted"`},
	} {
		bad := strings.Replace(string(valid), pair[0], pair[1], 1)
		if bad == string(valid) {
			continue
		}
		if r, err := parseCMUXWorkflowReply(bad); err == nil || r != nil {
			t.Fatal("accepted invalid protocol", bad, r, err)
		}
	}
	for _, ordinary := range []string{"DONE", `{"status":"DONE"}`, `{"type":"workflow_dispatch","workflow":{}}`} {
		if r, err := parseCMUXWorkflowReply(ordinary); r != nil || err != nil {
			t.Fatal(r, err)
		}
	}
}

type transportDrainAdapter struct {
	db         *store.Store
	results    drainAdapter
	dispatches int
	collects   int
	empty      int
}

func (a *transportDrainAdapter) Dispatch(ctx context.Context, actor ipc.Actor, id string) (store.MessageRecord, bool, error) {
	m, err := a.db.GetMessage(ctx, id)
	if err != nil {
		return store.MessageRecord{}, false, err
	}
	a.dispatches++
	return store.MessageRecord{Message: m, Delivery: store.DeliveryState{TargetGeneration: 1, Phase: "CMUX_QUEUED", Attempt: 1, Channel: "cmux-agent-message"}}, true, nil
}

func (a *transportDrainAdapter) Collect(ctx context.Context, m store.Message, c loopcore.Cursor) (*store.WorkflowResult, error) {
	a.collects++
	if a.empty > 0 {
		a.empty--
		return nil, nil
	}
	reply, event, err := a.results.WorkflowReply(ctx, SeatSnapshot{RunID: m.RunID, AgentID: m.Recipient, Generation: 1}, m, c)
	if err != nil {
		return nil, err
	}
	saved, err := a.db.ReplyWorkflow(ctx, store.MessageCommand{Actor: messageActor(reply.Actor), RequestID: reply.RequestID, Kind: "reply", ReplyTo: reply.MessageID, Body: reply.Body, ReportStatus: reply.Status}, event)
	return &saved, err
}

func TestCMUXWorkflowDrainCollectsOneReplyPerCall(t *testing.T) {
	db, _, actor := drainFixture(t, 1, &drainAdapter{})
	a := &transportDrainAdapter{db: db, empty: 1}
	runner, err := NewWorkflowTransportRunner(db, a)
	if err != nil {
		t.Fatal(err)
	}
	seed, err := db.AdvanceWorkflow(t.Context(), messageActor(actor), loopcore.Event{ID: "start", Actor: "main", Kind: "start"})
	if err != nil {
		t.Fatal(err)
	}
	out, err := runner.Drain(t.Context(), actor, seed.Cursor.Revision)
	if err != nil || out.DispatchError != nil || out.Current == nil || out.Current.Phase != loopcore.Executing || out.Current.Revision != 1 || a.dispatches != 1 || a.collects != 1 {
		t.Fatal(out, err, a.dispatches, a.collects)
	}
	out, err = runner.Drain(t.Context(), actor, out.Current.Revision)
	if err != nil || out.DispatchError != nil || out.Current == nil || out.Current.Phase != loopcore.Reviewing || out.Current.Revision != 2 || a.dispatches != 2 || a.collects != 2 {
		t.Fatal(out, err, a.dispatches, a.collects)
	}
	out, err = runner.Drain(t.Context(), actor, out.Current.Revision)
	if err != nil || out.DispatchError != nil || out.Current == nil || out.Current.Phase != loopcore.Completed || out.Current.Revision != 3 || a.dispatches != 3 || a.collects != 3 {
		t.Fatal(out, err, a.dispatches, a.collects)
	}
}
