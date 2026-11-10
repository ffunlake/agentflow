package coordinator

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"agentflow/internal/config"
	"agentflow/internal/ipc"
	"agentflow/internal/loopcore"
	"agentflow/internal/store"
)

func workflowFixture(t *testing.T) string {
	return workflowFixtureWithSession(t, "")
}

func workflowFixtureWithSession(t *testing.T, session string) string {
	t.Helper()
	source, err := config.Load(filepath.Join("..", "..", "examples", "workflow.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if session != "" {
		source.Defaults.Session = &session
	}
	return workflowFixtureFromConfig(t, source)
}

func workflowFixtureFromConfig(t *testing.T, source *config.Config) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "af-loop-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	frozen, err := config.Resolve(source, config.ResolveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(frozen)
	db, err := store.Open(filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateRun(t.Context(), store.Run{RunID: "run", ConfigSnapshot: string(body), ConfigDigest: frozen.Digest}); err != nil {
		t.Fatal(err)
	}
	for id, agent := range frozen.Agents {
		if err := db.CreateSeat(t.Context(), store.Seat{RunID: "run", AgentID: id, Generation: 1, Current: true, Status: "READY", Tool: agent.Tool,
			NativeSessionID: "fake-" + id, WorkspaceID: "workspace", SurfaceID: "surface-" + id}); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func pipelineWorkflowFixture(t *testing.T, roles []string) string {
	t.Helper()
	source, err := config.Load(filepath.Join("..", "..", "examples", "workflow.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	tool, session := "opencode", "persistent"
	source.Defaults.Tool, source.Defaults.Session = &tool, &session
	source.Agents = map[string]config.Agent{"main": {Role: "Coordinator", Prompt: stringPointer("Start tasks and inspect final results.")}}
	ids := []string{"main"}
	for _, id := range roles {
		source.Agents[id] = config.Agent{Role: id, Prompt: stringPointer("Complete only this role's part, use previous handoff, and return evidence."), Tool: &tool, Session: &session}
		ids = append(ids, id)
	}
	var layoutTree func([]string) config.Layout
	layoutTree = func(seats []string) config.Layout {
		if len(seats) == 1 {
			return config.Layout{Agent: seats[0]}
		}
		return config.Layout{Split: "columns", Children: []config.Layout{layoutTree(seats[:1]), layoutTree(seats[1:])}}
	}
	root := layoutTree(ids)
	source.Layout = &root
	source.Workflow = &config.Workflow{Type: config.WorkflowPipeline, Flow: "main -> " + strings.Join(roles, " -> ") + " -> main"}
	return workflowFixtureFromConfig(t, source)
}

func stringPointer(value string) *string { return &value }

func workflowRequest(c loopcore.Cursor, actor, kind string) ipc.Request {
	p := Params{Workflow: &WorkflowParams{Revision: c.Revision, Kind: kind}}
	method := "workflow"
	if kind != "start" && kind != "resume" {
		method, p.MessageID, p.Body, p.Status = "reply", c.DispatchID, "saved agent result", "DONE"
	}
	if kind == "handoff" {
		p.Workflow.Handoff = &loopcore.Handoff{Summary: "work saved", Evidence: []string{"fake/test.log"}, Next: "review work"}
	}
	return request(actor, method, fmt.Sprintf("event-%d", c.Revision+1), p)
}

func workflowTickIPC(t *testing.T, dir string, req ipc.Request) Result {
	t.Helper()
	r := result(t, dir, req)
	if r.Status != "WORKFLOW_ADVANCED" || r.Workflow == nil || r.Executed == nil || *r.Executed || r.CapabilityStatus != "PARTIAL" || r.RuntimeCommunication != "UNVERIFIED" {
		t.Fatalf("workflow: %+v", r)
	}
	return r
}

func changeWorkflowRequest(t *testing.T, req ipc.Request, change func(*Params)) ipc.Request {
	t.Helper()
	var p Params
	if err := json.Unmarshal(req.Params, &p); err != nil {
		t.Fatal(err)
	}
	change(&p)
	req.Params, _ = json.Marshal(p)
	return req
}

func TestWorkflowIPCReplyReviewAndReopen(t *testing.T) {
	dir := workflowFixture(t)
	stop := serve(t, dir)
	start := workflowRequest(loopcore.Cursor{}, "main", "start")
	r := workflowTickIPC(t, dir, start)
	if r.Workflow.Message.Recipient != "builder" {
		t.Fatal(r)
	}
	for _, pair := range []struct {
		executor, reviewer string
		criteria           int
	}{{"builder", "reviewer", 2}, {"reviewer", "main", 1}} {
		c := r.Workflow.Cursor
		inbox := result(t, dir, request(pair.executor, "inbox", "inbox", Params{Pending: true}))
		found := false
		for _, m := range inbox.Messages {
			if m.Message.ID == c.DispatchID {
				found = true
				if m.Delivery.Phase != "PENDING" || m.Delivery.Channel != "UNVERIFIED" || m.Delivery.Attempt != 0 {
					t.Fatal(m)
				}
			}
		}
		if !found {
			t.Fatal("dispatch not visible in existing inbox")
		}
		ack := result(t, dir, request(pair.executor, "ack", fmt.Sprintf("ack-%d", c.Revision), Params{MessageID: c.DispatchID}))
		if ack.Status != "ACKED" {
			t.Fatal(ack)
		}
		// Ordinary DONE and ACK are receipts/claims, never workflow acceptance.
		plain := result(t, dir, request(pair.executor, "reply", fmt.Sprintf("plain-%d", c.Revision), Params{MessageID: c.DispatchID, Body: "DONE", Status: "DONE"}))
		if plain.Status != "STORED" || plain.Workflow != nil {
			t.Fatal(plain)
		}
		req := workflowRequest(c, pair.executor, "handoff")
		r = workflowTickIPC(t, dir, req)
		if r.Workflow.Cursor.Phase != loopcore.Reviewing || r.Workflow.Message.Recipient != pair.reviewer || r.Record == nil || r.Record.Message.ReplyTo != c.DispatchID {
			t.Fatal(r)
		}
		stop()
		stop = serve(t, dir)
		before := messageTablesSnapshot(t, dir)
		if replay := workflowTickIPC(t, dir, req); !reflect.DeepEqual(replay, r) {
			t.Fatal("reply/event changed after reopen")
		}
		if after := messageTablesSnapshot(t, dir); after != before {
			t.Fatal("exact reply replay wrote state")
		}
		approve := workflowRequest(r.Workflow.Cursor, pair.reviewer, "approve")
		approve = changeWorkflowRequest(t, approve, func(p *Params) {
			for range pair.criteria {
				p.Workflow.Accepted = append(p.Workflow.Accepted, true)
			}
		})
		for _, accepted := range [][]bool{nil, {false}, make([]bool, pair.criteria)} {
			badReq := changeWorkflowRequest(t, approve, func(p *Params) { p.Workflow.Accepted = accepted })
			before := messageTablesSnapshot(t, dir)
			bad := result(t, dir, badReq)
			if bad.Status != "FAILED" || before != messageTablesSnapshot(t, dir) {
				t.Fatal("incomplete/false criteria committed a review reply", bad)
			}
		}
		r = workflowTickIPC(t, dir, approve)
	}
	if r.Workflow.Cursor.Phase != loopcore.Completed || r.Workflow.Message != nil {
		t.Fatal(r)
	}
	next := result(t, dir, workflowRequest(r.Workflow.Cursor, "main", "start"))
	if next.Status != "WORKFLOW_ADVANCED" || next.Workflow.Cursor.Phase != loopcore.Executing || next.Workflow.Cursor.Revision != r.Workflow.Cursor.Revision+1 || next.Workflow.Message == nil {
		t.Fatal(next)
	}
	before := messageTablesSnapshot(t, dir)
	active := result(t, dir, workflowRequest(next.Workflow.Cursor, "main", "start"))
	if active.Status != "FAILED" || active.Diagnostics[0].Code != "WORKFLOW_INVALID_TRANSITION" || before != messageTablesSnapshot(t, dir) {
		t.Fatal(active)
	}
}

func TestWorkflowIPCInvalidEventsAndStrictNestedParams(t *testing.T) {
	dir := workflowFixture(t)
	serve(t, dir)
	r := workflowTickIPC(t, dir, workflowRequest(loopcore.Cursor{}, "main", "start"))
	valid := workflowRequest(r.Workflow.Cursor, "builder", "handoff")
	for _, raw := range []string{
		`{"workflow":{"revision":1,"kind":"handoff","actor":"main"}}`,
		`{"workflow":{"revision":1,"revision":1,"kind":"handoff"}}`,
		`{"workflow":{"revision":1,"Kind":"handoff"}}`,
		`{"workflow":{"kind":"handoff"}}`,
		`{"workflow":{"revision":null,"kind":"handoff"}}`,
		`{"workflow":{"revision":1,"kind":"handoff","handoff":{"summary":"done","evidence":["log"],"next":"review","next":"other"}}}`,
		`{"workflow":{"revision":1,"kind":"handoff","handoff":{"summary":"done","evidence":["log"],"next":"review","Next":"other"}}}`,
		`{"workflow":{"revision":1,"kind":"handoff","handoff":{"summary":"done","evidence":["log"],"next":null}}}`,
		`{"workflow":{"revision":1,"kind":"handoff","handoff":null}}`,
	} {
		req := valid
		req.Params = json.RawMessage(raw)
		before := messageTablesSnapshot(t, dir)
		_, err := Call(t.Context(), dir, req)
		var public *ipc.Error
		if !errors.As(err, &public) || public.Code != ipc.InvalidRequest || before != messageTablesSnapshot(t, dir) {
			t.Fatal(raw, err)
		}
	}
	for _, change := range []func(*Params){
		func(p *Params) { p.Workflow.Revision = 0 },
		func(p *Params) { p.Workflow.Handoff = nil },
		func(p *Params) { p.Workflow.Handoff.Evidence = []string{" "} },
		func(p *Params) { p.MessageID = "other" },
		func(p *Params) { p.Workflow.Kind = "approve"; p.Workflow.Accepted = []bool{true, true} },
	} {
		req := changeWorkflowRequest(t, valid, change)
		before := messageTablesSnapshot(t, dir)
		bad := result(t, dir, req)
		if bad.Status != "FAILED" || before != messageTablesSnapshot(t, dir) {
			t.Fatal("invalid event wrote a reply or transition", bad)
		}
	}
	for _, actor := range []ipc.Actor{{RunID: "run", AgentID: "builder", Generation: 2}, {RunID: "run", AgentID: "missing", Generation: 1}} {
		req := valid
		req.Actor = actor
		before := messageTablesSnapshot(t, dir)
		if _, err := Call(t.Context(), dir, req); err == nil || before != messageTablesSnapshot(t, dir) {
			t.Fatal(actor, err)
		}
	}
	wrongRole := valid
	wrongRole.Actor.AgentID = "reviewer"
	before := messageTablesSnapshot(t, dir)
	if bad := result(t, dir, wrongRole); bad.Status != "FAILED" || before != messageTablesSnapshot(t, dir) {
		t.Fatal(bad)
	}
}

func TestWorkflowIPCSharedNamespaceStopAndExactReplyReplay(t *testing.T) {
	dir := workflowFixture(t)
	stop := serve(t, dir)
	start := workflowRequest(loopcore.Cursor{}, "main", "start")
	r := workflowTickIPC(t, dir, start)
	// A control event reserves the ordinary caller/request namespace too.
	for _, req := range []ipc.Request{
		request("main", "send", start.RequestID, Params{Recipient: "builder", Body: "work"}),
		request("main", "stop", start.RequestID, Params{}),
		request("main", "ack", start.RequestID, Params{MessageID: r.Workflow.Cursor.DispatchID}),
	} {
		before := messageTablesSnapshot(t, dir)
		if bad := result(t, dir, req); bad.Status != "FAILED" || bad.Diagnostics[0].Code != ipc.IdempotencyConflict || before != messageTablesSnapshot(t, dir) {
			t.Fatal(bad)
		}
	}
	handoff := workflowRequest(r.Workflow.Cursor, "builder", "handoff")
	r = workflowTickIPC(t, dir, handoff)
	changed := changeWorkflowRequest(t, handoff, func(p *Params) { p.Body = "changed" })
	if bad := result(t, dir, changed); bad.Status != "FAILED" || bad.Diagnostics[0].Code != ipc.IdempotencyConflict {
		t.Fatal(bad)
	}
	call(t, dir, request("main", "stop", "stop", Params{}))
	stop()
	serve(t, dir)
	before := messageTablesSnapshot(t, dir)
	if replay := workflowTickIPC(t, dir, handoff); !reflect.DeepEqual(replay, r) {
		t.Fatal("stop changed exact reply receipt")
	}
	approve := changeWorkflowRequest(t, workflowRequest(r.Workflow.Cursor, "reviewer", "approve"), func(p *Params) { p.Workflow.Accepted = []bool{true, true} })
	if bad := result(t, dir, approve); bad.Status != "FAILED" || bad.Diagnostics[0].Code != "RUN_STOPPED" || before != messageTablesSnapshot(t, dir) {
		t.Fatal("stop allowed new reply/event", bad)
	}
}

func TestWorkflowIPCControlCannotReuseMessageOrACKRequest(t *testing.T) {
	for _, kind := range []string{"message", "ack", "dispatch-ack"} {
		t.Run(kind, func(t *testing.T) {
			dir := workflowFixture(t)
			serve(t, dir)
			seed := result(t, dir, request("builder", "send", "seed", Params{Recipient: "main", Body: "seed"}))
			start := workflowRequest(loopcore.Cursor{}, "main", "start")
			occupied := start.RequestID
			if kind == "dispatch-ack" {
				occupied = "workflow:1"
			}
			if kind == "message" {
				result(t, dir, request("main", "send", occupied, Params{Recipient: "builder", Body: "existing"}))
			} else {
				result(t, dir, request("main", "ack", occupied, Params{MessageID: seed.Record.Message.ID}))
			}
			before := messageTablesSnapshot(t, dir)
			bad := result(t, dir, start)
			if bad.Status != "FAILED" || bad.Diagnostics[0].Code != ipc.IdempotencyConflict || before != messageTablesSnapshot(t, dir) {
				t.Fatal(kind, bad)
			}
		})
	}
}

func TestWorkflowIPCReviewBlockedResumeAndFiniteRetry(t *testing.T) {
	dir := workflowFixture(t)
	serve(t, dir)
	r := workflowTickIPC(t, dir, workflowRequest(loopcore.Cursor{}, "main", "start"))
	r = workflowTickIPC(t, dir, workflowRequest(r.Workflow.Cursor, "builder", "handoff"))
	blocked := changeWorkflowRequest(t, workflowRequest(r.Workflow.Cursor, "reviewer", "blocked"), func(p *Params) { p.Workflow.Reason = "need decision"; p.Status = "BLOCKED_CONFIRM" })
	r = workflowTickIPC(t, dir, blocked)
	if r.Workflow.Cursor.Phase != loopcore.Paused || r.Workflow.Message != nil {
		t.Fatal(r)
	}
	resume := changeWorkflowRequest(t, workflowRequest(r.Workflow.Cursor, "main", "resume"), func(p *Params) { p.Workflow.Reason = "decision saved" })
	wrong := resume
	wrong.Actor.AgentID = "builder"
	before := messageTablesSnapshot(t, dir)
	if bad := result(t, dir, wrong); bad.Status != "FAILED" || before != messageTablesSnapshot(t, dir) {
		t.Fatal("non-lead resumed", bad)
	}
	r = workflowTickIPC(t, dir, resume)
	if r.Workflow.Cursor.Phase != loopcore.Executing || r.Workflow.Cursor.Attempt != 2 || r.Workflow.Message.Recipient != "builder" || r.Workflow.Cursor.Handoff != nil {
		t.Fatal(r)
	}
	for range 2 {
		retry := changeWorkflowRequest(t, workflowRequest(r.Workflow.Cursor, "builder", "retry"), func(p *Params) { p.Workflow.Reason = "failed verification"; p.Status = "FAILED" })
		r = workflowTickIPC(t, dir, retry)
	}
	if r.Workflow.Cursor.Phase != loopcore.Exhausted || r.Workflow.Message != nil {
		t.Fatal(r)
	}
}
