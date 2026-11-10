package coordinator

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"agentflow/internal/config"
	"agentflow/internal/ipc"
	"agentflow/internal/loopcore"
	"agentflow/internal/runtime"
	"agentflow/internal/store"
)

const workflowWorkspace = "10000000-0000-0000-0000-000000000071"

func workflowSurface(agent string) string {
	return map[string]string{"main": "30000000-0000-0000-0000-000000000001", "builder": "30000000-0000-0000-0000-000000000002", "reviewer": "30000000-0000-0000-0000-000000000003"}[agent]
}

func cmuxWorkflowFixture(t *testing.T, sourcePath ...string) (string, string, string) {
	t.Helper()
	dir, err := os.MkdirTemp("", "af-cwf-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	binary := filepath.Join(t.TempDir(), "fake cmux")
	if out, err := exec.Command("go", "build", "-o", binary, "../runtime/testdata/cmux").CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	t.Setenv("CMUX_FAKE_LOG", filepath.Join(dir, "calls.jsonl"))
	t.Setenv("CMUX_FAKE_MODE", "json")
	t.Setenv("CMUX_FAKE_WORKFLOW", "1")
	inbox := filepath.Join(dir, "inbox.json")
	t.Setenv("CMUX_FAKE_INBOX", inbox)
	writeWorkflowInbox(t, inbox)
	path := "../../examples/workflow.yaml"
	if len(sourcePath) != 0 {
		path = sourcePath[0]
	}
	source, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
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
	var bindings []store.SurfaceRegistration
	for i, id := range []string{"main", "builder", "reviewer"} {
		bindings = append(bindings, store.SurfaceRegistration{RunID: "run", AgentID: id, Generation: 1, Tool: frozen.Agents[id].Tool, LaunchID: "launch-one", WorkspaceID: workflowWorkspace, WorkspaceRef: "workspace:71", SurfaceID: workflowSurface(id), SurfaceRef: fmt.Sprintf("surface:%d", i+81), Status: "LAUNCHED", NativeReady: "UNVERIFIED"})
	}
	if err := db.RegisterCMUXSurfaces(t.Context(), bindings); err != nil {
		t.Fatal(err)
	}
	return dir, binary, inbox
}

func serveCMUXWorkflow(t *testing.T, dir, binary string) func() {
	t.Helper()
	c, err := OpenCMUXTransport(t.Context(), dir, "run", binary)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- c.Serve(ctx) }()
	var once sync.Once
	stop := func() {
		once.Do(func() {
			cancel()
			select {
			case err := <-done:
				if err != nil {
					t.Error(err)
				}
			case <-time.After(5 * time.Second):
				t.Error("cmux coordinator failed to drain")
			}
		})
	}
	t.Cleanup(stop)
	return stop
}

func writeWorkflowInbox(t *testing.T, path string, messages ...map[string]any) {
	t.Helper()
	if messages == nil {
		messages = []map[string]any{}
	}
	body, _ := json.Marshal(map[string]any{"messages": messages})
	if err := os.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}
}

func workflowNativeReply(c loopcore.Cursor, agent, kind string) map[string]any {
	status := map[string]string{"handoff": "DONE", "approve": "DONE", "changes": "DONE", "retry": "FAILED", "blocked": "BLOCKED_CONFIRM"}[kind]
	r := runtime.CMUXWorkflowReply{V: 1, Type: "workflow_reply", RequestID: fmt.Sprintf("reply-%d", c.Revision), Actor: ipc.Actor{RunID: "run", AgentID: agent, Generation: 1}, MessageID: c.DispatchID, Body: "saved result", Status: status, Workflow: runtime.CMUXWorkflowEvent{Revision: c.Revision, Kind: kind}}
	if kind == "handoff" {
		r.Workflow.Handoff = &loopcore.Handoff{Summary: "implemented", Evidence: []string{"test.log"}, Next: "review"}
	}
	if kind == "approve" {
		r.Workflow.Accepted = []bool{true, true}
		if c.Step == "verify" {
			r.Workflow.Accepted = []bool{true}
		}
	}
	if kind == "changes" || kind == "retry" || kind == "blocked" {
		r.Workflow.Reason = "needs explicit next action"
	}
	body, _ := json.Marshal(r)
	return map[string]any{"id": fmt.Sprintf("inbox-%d", c.Revision), "sender_name": agent, "sender_surface_id": workflowSurface(agent), "sender_workspace_id": workflowWorkspace, "recipient_surface_id": workflowSurface("main"), "recipient_workspace_id": workflowWorkspace, "body": string(body), "state": "queued"}
}

func changeNativeReply(t *testing.T, native map[string]any, f func(*runtime.CMUXWorkflowReply)) {
	t.Helper()
	var body runtime.CMUXWorkflowReply
	if err := json.Unmarshal([]byte(native["body"].(string)), &body); err != nil {
		t.Fatal(err)
	}
	f(&body)
	encoded, _ := json.Marshal(body)
	native["body"] = string(encoded)
}

func cmuxDrain(t *testing.T, dir string, c loopcore.Cursor) Result {
	t.Helper()
	return cmuxWorkflowResult(t, dir, request("main", "workflow", fmt.Sprintf("drain-%d", c.Revision), Params{Workflow: &WorkflowParams{Revision: c.Revision, Kind: "drain"}}))
}

func cmuxWorkflowResult(t *testing.T, dir string, req ipc.Request) Result {
	t.Helper()
	// A drain can cross multiple bounded process boundaries; the generic five
	// second persistence-only helper is shorter than one transport's deadline.
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	r, err := cmuxWorkflowResultWithin(ctx, dir, req)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func cmuxWorkflowResultWithin(ctx context.Context, dir string, req ipc.Request) (Result, error) {
	response, err := Call(ctx, dir, req)
	if err != nil {
		return Result{}, err
	}
	var r Result
	if err := json.Unmarshal(response.Result, &r); err != nil {
		return Result{}, err
	}
	return r, nil
}

func cmuxCursor(t *testing.T, dir string) loopcore.Cursor {
	t.Helper()
	db, err := store.OpenExisting(filepath.Join(dir, "state.db"), false)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	c, err := db.WorkflowCursor(t.Context(), "run")
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func cmuxCalls(t *testing.T, dir string, command string) int {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(dir, "calls.jsonl"))
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Count(string(body), `"agent","`+command+`"`)
}

func TestCMUXWorkflowUsesRegisteredOpenCodeTUIs(t *testing.T) {
	dir, binary, _ := cmuxWorkflowFixture(t, "../../examples/team.yaml")
	opencode := filepath.Join(t.TempDir(), "opencode")
	if out, err := exec.Command("go", "build", "-o", opencode, "../runtime/testdata/opencode-tui").CombinedOutput(); err != nil {
		t.Fatalf("build OpenCode fixture: %v: %s", err, out)
	}
	t.Setenv("CMUX_FAKE_CWD", "/Users/funlake/Projects/okra-wiki")
	t.Setenv("CMUX_FAKE_OPENCODE", opencode)
	t.Setenv("CMUX_FAKE_WORKSPACE_REF", "workspace:71")
	t.Setenv("CMUX_FAKE_SURFACE_REF_BY_ID", "1")
	t.Setenv("CMUX_FAKE_PROMPT", filepath.Join(dir, "native-prompt"))
	t.Setenv("CMUX_FAKE_ENTER", filepath.Join(dir, "native-enter"))
	serveCMUXWorkflow(t, dir, binary)
	r := cmuxWorkflowResult(t, dir, workflowRequest(loopcore.Cursor{}, "main", "start"))
	if len(r.Diagnostics) != 0 || r.WorkflowCurrent == nil || r.WorkflowCurrent.Phase != loopcore.Completed || r.WorkflowCurrent.Revision != 3 {
		db, _ := store.OpenExisting(filepath.Join(dir, "state.db"), false)
		if db != nil {
			_, seats, _ := db.ReadState(t.Context())
			t.Logf("seats: %+v", seats)
			if r.WorkflowCurrent != nil {
				w, e := db.SessionWork(t.Context(), r.WorkflowCurrent.DispatchID)
				t.Logf("work: %+v error=%v", w, e)
			}
			_ = db.Close()
		}
		t.Fatalf("registered OpenCode workflow did not complete: %+v current=%+v", r, r.WorkflowCurrent)
	}
	db, err := store.OpenExisting(filepath.Join(dir, "state.db"), false)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, id := range []string{"builder", "reviewer"} {
		seat, err := db.GetSeat(t.Context(), "run", id, 1)
		if err != nil || seat.Status != "READY" || seat.NativeSessionID == "" {
			t.Fatalf("%s was not bound to its native session: %+v, %v", id, seat, err)
		}
	}
	if cmuxCalls(t, dir, "message") != 0 {
		t.Fatal("workflow used the cmux agent inbox instead of the registered OpenCode TUI")
	}
}

func TestCMUXWorkflowThreeRolesTwoStepsIPC(t *testing.T) {
	dir, binary, inbox := cmuxWorkflowFixture(t)
	stop := serveCMUXWorkflow(t, dir, binary)
	start := workflowRequest(loopcore.Cursor{}, "main", "start")
	r := cmuxWorkflowResult(t, dir, start)
	if r.Status != "WORKFLOW_ADVANCED" || r.Dispatch == nil || r.Dispatch.Delivery.Phase != "CMUX_QUEUED" || !*r.Executed || len(r.Diagnostics) != 0 {
		t.Fatal(r)
	}
	var history []map[string]any
	var trace []string
	for i, agent := range []string{"builder", "reviewer", "reviewer", "main"} {
		c := cmuxCursor(t, dir)
		db, err := store.OpenExisting(filepath.Join(dir, "state.db"), true)
		if err != nil {
			t.Fatal(err)
		}
		m, err := db.GetMessage(t.Context(), c.DispatchID)
		_ = db.Close()
		if err != nil || m.Recipient != agent {
			t.Fatal(m, err, c)
		}
		var intent loopcore.Intent
		if err := json.Unmarshal([]byte(m.Body), &intent); err != nil {
			t.Fatal(err)
		}
		trace = append(trace, intent.Step+":"+intent.Purpose+":"+agent)
		// Reopening and replaying start must not resubmit an earlier phase.
		stop()
		before := messageTablesSnapshot(t, dir)
		stop = serveCMUXWorkflow(t, dir, binary)
		if replay := cmuxWorkflowResult(t, dir, start); replay.Dispatch != nil || *replay.Executed || before != messageTablesSnapshot(t, dir) {
			t.Fatal("reopen replayed work", replay)
		}
		kind := "handoff"
		if intent.Purpose == "review" {
			kind = "approve"
		}
		if agent == "main" {
			writeWorkflowInbox(t, inbox, history...)
			waiting := cmuxDrain(t, dir, c)
			if len(waiting.Diagnostics) != 0 || !waiting.AwaitingActor || waiting.WorkflowCurrent == nil || waiting.WorkflowCurrent.Revision != c.Revision || waiting.Dispatch == nil || waiting.Dispatch.Message.Recipient != "main" {
				t.Fatalf("lead approval did not yield to its seat: %+v", waiting)
			}
			history = append(history, workflowNativeReply(c, agent, kind))
			writeWorkflowInbox(t, inbox, history...)
			r = cmuxDrain(t, dir, c)
			if r.AwaitingActor {
				t.Fatal("lead approval remained pending after its structured reply", r)
			}
		} else {
			history = append(history, workflowNativeReply(c, agent, kind))
			writeWorkflowInbox(t, inbox, history...)
			r = cmuxDrain(t, dir, c)
		}
		if r.Status != "WORKFLOW_ADVANCED" || len(r.Diagnostics) != 0 || r.WorkflowCurrent == nil || r.WorkflowCurrent.Revision != c.Revision+1 {
			t.Fatal(r)
		}
		if cmuxCalls(t, dir, "message") != i+1 {
			t.Fatal("drain duplicated or skipped a phase dispatch", cmuxCalls(t, dir, "message"), i)
		}
	}
	if !reflect.DeepEqual(trace, []string{"implement:execute:builder", "implement:review:reviewer", "verify:execute:reviewer", "verify:review:main"}) || r.WorkflowCurrent.Phase != loopcore.Completed || cmuxCalls(t, dir, "message") != 4 {
		t.Fatal(trace, r)
	}
	before := messageTablesSnapshot(t, dir)
	if again := cmuxDrain(t, dir, *r.WorkflowCurrent); len(again.Diagnostics) != 0 || *again.Executed || before != messageTablesSnapshot(t, dir) {
		t.Fatal(again)
	}
	stop()
	db, err := store.OpenExisting(filepath.Join(dir, "state.db"), false)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, seats, err := db.ReadState(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, seat := range seats {
		if seat.Status != "LAUNCHED" || seat.NativeSessionID != "" {
			t.Fatal("transport fabricated READY", seat)
		}
	}
	journal, _ := db.JournalAfter(t.Context(), "run", 0)
	applied := 0
	for _, e := range journal {
		if e.Event == "CMUX_WORKFLOW_APPLIED" {
			applied++
		}
	}
	if applied != 4 {
		t.Fatal("missing atomic workflow evidence", applied)
	}
}

func TestCMUXWorkflowStructuredTerminalRepliesIPC(t *testing.T) {
	for _, kind := range []string{"handoff", "approve", "changes", "retry", "blocked"} {
		t.Run(kind, func(t *testing.T) {
			dir, binary, inbox := cmuxWorkflowFixture(t)
			serveCMUXWorkflow(t, dir, binary)
			r := cmuxWorkflowResult(t, dir, workflowRequest(loopcore.Cursor{}, "main", "start"))
			c := r.Workflow.Cursor
			agent := "builder"
			if kind == "approve" || kind == "changes" {
				writeWorkflowInbox(t, inbox, workflowNativeReply(c, agent, "handoff"))
				r = cmuxDrain(t, dir, c)
				c = *r.WorkflowCurrent
				agent = "reviewer"
			}
			for _, status := range []string{"", "WORKING", "DONE", "FAILED", "BLOCKED_CONFIRM"} {
				native := workflowNativeReply(c, agent, kind)
				var expected runtime.CMUXWorkflowReply
				_ = json.Unmarshal([]byte(native["body"].(string)), &expected)
				if status == expected.Status {
					continue
				}
				native["id"] = "bad-status-" + status
				changeNativeReply(t, native, func(r *runtime.CMUXWorkflowReply) { r.Status = status })
				writeWorkflowInbox(t, inbox, native)
				calls := cmuxCalls(t, dir, "message")
				bad := cmuxDrain(t, dir, c)
				callsAfter := cmuxCalls(t, dir, "message")
				if len(bad.Diagnostics) != 1 || !reflect.DeepEqual(c, cmuxCursor(t, dir)) || callsAfter < calls || callsAfter > calls+1 {
					t.Fatalf("status=%q result=%+v cursor=%+v calls=%d/%d", status, bad, cmuxCursor(t, dir), cmuxCalls(t, dir, "message"), calls)
				}
				calls = callsAfter
			}
			writeWorkflowInbox(t, inbox, workflowNativeReply(c, agent, kind))
			r = cmuxDrain(t, dir, c)
			if len(r.Diagnostics) != 0 || r.WorkflowCurrent.Revision != c.Revision+1 {
				t.Fatal(r)
			}
			if kind == "blocked" {
				if r.WorkflowCurrent.Phase != loopcore.Paused {
					t.Fatal(r)
				}
				resume := changeWorkflowRequest(t, workflowRequest(*r.WorkflowCurrent, "main", "resume"), func(p *Params) { p.Workflow.Reason = "decision saved" })
				r = cmuxWorkflowResult(t, dir, resume)
				if len(r.Diagnostics) != 0 || r.Dispatch == nil || r.Dispatch.Delivery.Phase != "CMUX_QUEUED" {
					t.Fatal(r)
				}
			}
		})
	}
}

func TestCMUXWorkflowInboxGatesIPC(t *testing.T) {
	dir, binary, inbox := cmuxWorkflowFixture(t)
	serveCMUXWorkflow(t, dir, binary)
	r := cmuxWorkflowResult(t, dir, workflowRequest(loopcore.Cursor{}, "main", "start"))
	c := r.Workflow.Cursor
	for _, mode := range []string{"plain", "missing-workflow", "missing-status", "duplicate-json", "wrong-kind", "old-revision", "wrong-dispatch", "missing-handoff", "old-generation", "future-generation", "wrong-run", "wrong-actor", "wrong-source-name", "wrong-source-surface", "wrong-source-workspace", "wrong-surface", "wrong-workspace", "unknown-source", "failed-native", "duplicate-batch"} {
		t.Run(mode, func(t *testing.T) {
			native := workflowNativeReply(c, "builder", "handoff")
			native["id"] = "bad-" + mode
			switch mode {
			case "plain":
				native["body"] = "DONE"
			case "missing-workflow":
				native["body"] = `{"status":"DONE","body":"ordinary result"}`
			case "missing-status":
				var body map[string]any
				_ = json.Unmarshal([]byte(native["body"].(string)), &body)
				delete(body, "status")
				raw, _ := json.Marshal(body)
				native["body"] = string(raw)
			case "duplicate-json":
				native["body"] = strings.Replace(native["body"].(string), `"kind":"handoff"`, `"kind":"handoff","kind":"approve"`, 1)
			case "wrong-kind":
				changeNativeReply(t, native, func(r *runtime.CMUXWorkflowReply) { r.Workflow.Kind = "start" })
			case "old-revision":
				changeNativeReply(t, native, func(r *runtime.CMUXWorkflowReply) { r.Workflow.Revision = 0 })
			case "wrong-dispatch":
				changeNativeReply(t, native, func(r *runtime.CMUXWorkflowReply) { r.MessageID = "other" })
			case "missing-handoff":
				changeNativeReply(t, native, func(r *runtime.CMUXWorkflowReply) { r.Workflow.Handoff = nil })
			case "old-generation":
				changeNativeReply(t, native, func(r *runtime.CMUXWorkflowReply) { r.Actor.Generation = 0 })
			case "future-generation":
				changeNativeReply(t, native, func(r *runtime.CMUXWorkflowReply) { r.Actor.Generation = 2 })
			case "wrong-run":
				changeNativeReply(t, native, func(r *runtime.CMUXWorkflowReply) { r.Actor.RunID = "other" })
			case "wrong-actor":
				changeNativeReply(t, native, func(r *runtime.CMUXWorkflowReply) { r.Actor.AgentID = "reviewer" })
			case "wrong-source-name":
				native["sender_name"] = "display alias"
			case "wrong-source-surface":
				native["sender_surface_id"] = workflowSurface("reviewer")
			case "wrong-source-workspace":
				native["sender_workspace_id"] = "10000000-0000-0000-0000-000000000999"
			case "wrong-surface":
				native["recipient_surface_id"] = workflowSurface("builder")
			case "wrong-workspace":
				native["recipient_workspace_id"] = "10000000-0000-0000-0000-000000000999"
			case "unknown-source":
				native["sender_surface_id"] = "30000000-0000-0000-0000-000000000999"
			case "failed-native":
				native["state"] = "failed"
			}
			writeWorkflowInbox(t, inbox, native)
			if mode == "duplicate-batch" {
				writeWorkflowInbox(t, inbox, native, native)
			}
			bad := cmuxDrain(t, dir, c)
			ordinary := mode == "plain" || mode == "missing-workflow"
			if (len(bad.Diagnostics) == 0) != ordinary || !reflect.DeepEqual(c, cmuxCursor(t, dir)) || cmuxCalls(t, dir, "message") != 1 {
				t.Fatal(mode, bad)
			}
		})
	}
	// Direct IPC cannot bypass the cmux source/surface evidence contract.
	if bad := cmuxWorkflowResult(t, dir, workflowRequest(c, "builder", "handoff")); bad.Status != "FAILED" {
		t.Fatal(bad)
	}
	writeWorkflowInbox(t, inbox, workflowNativeReply(c, "builder", "handoff"))
	if good := cmuxDrain(t, dir, c); len(good.Diagnostics) != 0 || good.WorkflowCurrent.Revision != 2 {
		t.Fatal(good)
	}
}

func TestCMUXWorkflowUnknownSendAndStopNeverReplay(t *testing.T) {
	for _, mode := range []string{"message-invalid", "message-fail", "message-wrong-surface", "message-truncated", "pending-intent", "stop"} {
		t.Run(mode, func(t *testing.T) {
			dir, binary, inbox := cmuxWorkflowFixture(t)
			if mode == "pending-intent" {
				db, _ := store.OpenExisting(filepath.Join(dir, "state.db"), true)
				saved, err := db.AdvanceWorkflow(t.Context(), store.MessageActor{RunID: "run", AgentID: "main", Generation: 1}, loopcore.Event{ID: "start", Actor: "main", Kind: "start"})
				if err != nil {
					t.Fatal(err)
				}
				if _, claimed, err := db.BeginCMUXDispatch(t.Context(), store.MessageActor{RunID: "run", AgentID: "main", Generation: 1}, saved.Message.ID); err != nil || !claimed {
					t.Fatal(claimed, err)
				}
				db.Close()
			} else if mode != "stop" {
				t.Setenv("CMUX_FAKE_MODE", mode)
			}
			stop := serveCMUXWorkflow(t, dir, binary)
			start := workflowRequest(loopcore.Cursor{}, "main", "start")
			if mode != "pending-intent" {
				cmuxWorkflowResult(t, dir, start)
			}
			c := cmuxCursor(t, dir)
			writeWorkflowInbox(t, inbox, workflowNativeReply(c, "builder", "handoff"))
			if mode == "stop" {
				cmuxWorkflowResult(t, dir, request("main", "stop", "stop", Params{}))
			}
			stop()
			t.Setenv("CMUX_FAKE_MODE", "json")
			beforeCalls := cmuxCalls(t, dir, "message")
			serveCMUXWorkflow(t, dir, binary)
			for range 2 {
				bad := cmuxDrain(t, dir, c)
				if len(bad.Diagnostics) != 1 || *bad.Executed {
					t.Fatal(bad)
				}
			}
			if cmuxCalls(t, dir, "message") != beforeCalls || !reflect.DeepEqual(c, cmuxCursor(t, dir)) {
				t.Fatal("unknown/stop replayed or advanced")
			}
		})
	}
}

func TestCMUXWorkflowDrainUntouchedOutbox(t *testing.T) {
	dir, binary, _ := cmuxWorkflowFixture(t)
	// Simulate restart after the atomic event/outbox commit, before send claim.
	db, _ := store.OpenExisting(filepath.Join(dir, "state.db"), true)
	saved, err := db.AdvanceWorkflow(t.Context(), store.MessageActor{RunID: "run", AgentID: "main", Generation: 1}, loopcore.Event{ID: "stored-start", Actor: "main", Kind: "start"})
	db.Close()
	if err != nil {
		t.Fatal(err)
	}
	serveCMUXWorkflow(t, dir, binary)
	if cmuxCalls(t, dir, "message") != 0 {
		t.Fatal("open implicitly dispatched")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	first, err := cmuxWorkflowResultWithin(ctx, dir, request("main", "workflow", "drain-first", Params{Workflow: &WorkflowParams{Revision: saved.Cursor.Revision, Kind: "drain"}}))
	cancel()
	if err != nil {
		t.Fatal("server-side drain must return without holding a long poll", err)
	}
	if len(first.Diagnostics) != 0 || first.WorkflowCurrent == nil || first.WorkflowCurrent.Revision != saved.Cursor.Revision || cmuxCalls(t, dir, "message") != 1 {
		t.Fatal("drain should return current cursor without duplicating the durable dispatch", first, cmuxCalls(t, dir, "message"))
	}
	ctx, cancel = context.WithTimeout(t.Context(), 3*time.Second)
	second, err := cmuxWorkflowResultWithin(ctx, dir, request("main", "workflow", "drain-second", Params{Workflow: &WorkflowParams{Revision: saved.Cursor.Revision, Kind: "drain"}}))
	cancel()
	if err != nil {
		t.Fatal("repeated server-side drain must return without holding a long poll", err)
	}
	if len(second.Diagnostics) != 0 || second.WorkflowCurrent == nil || second.WorkflowCurrent.Revision != saved.Cursor.Revision || cmuxCalls(t, dir, "message") != 1 {
		t.Fatal("repeat drain should preserve the single dispatch", second, cmuxCalls(t, dir, "message"))
	}
}

func TestCMUXWorkflowConcurrentStartClaimsOnce(t *testing.T) {
	dir, binary, _ := cmuxWorkflowFixture(t)
	serveCMUXWorkflow(t, dir, binary)
	req := workflowRequest(loopcore.Cursor{}, "main", "start")
	var wg sync.WaitGroup
	responses := make(chan Result, 8)
	for range 8 {
		wg.Add(1)
		go func() { defer wg.Done(); responses <- cmuxWorkflowResult(t, dir, req) }()
	}
	wg.Wait()
	close(responses)
	claims := 0
	for r := range responses {
		if r.Status != "WORKFLOW_ADVANCED" || len(r.Diagnostics) != 0 {
			t.Fatal(r)
		}
		if *r.Executed {
			claims++
		}
	}
	if claims != 1 || cmuxCalls(t, dir, "message") != 1 {
		t.Fatal(claims)
	}
}
