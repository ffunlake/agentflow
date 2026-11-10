package coordinator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"agentflow/internal/ipc"
	"agentflow/internal/loopcore"
	"agentflow/internal/runtime"
	"agentflow/internal/store"
)

// The fake emits work only when Submit is actually invoked; the test agents
// consume it after the submission has committed and reply via the Unix socket.
type workflowAdapter struct {
	work    chan store.Message
	calls   atomic.Int32
	inspect func(context.Context, runtime.SeatSnapshot) (runtime.InputState, error)
	submit  func(context.Context, runtime.SeatSnapshot, store.Message) (runtime.Submission, error)
}

func (f *workflowAdapter) Inspect(ctx context.Context, seat runtime.SeatSnapshot) (runtime.InputState, error) {
	if f.inspect != nil {
		return f.inspect(ctx, seat)
	}
	return runtime.InputState{Actor: ipc.Actor{RunID: seat.RunID, AgentID: seat.AgentID, Generation: seat.Generation}, Native: seat.Native, Verified: true}, nil
}

func (f *workflowAdapter) Reset(context.Context, runtime.ResetRequest) (runtime.NativeBinding, error) {
	return runtime.NativeBinding{}, errors.New("fake workflow expects persistent seats")
}

func (f *workflowAdapter) WaitReady(context.Context, runtime.ResetRequest, runtime.NativeBinding) (runtime.Registration, error) {
	return runtime.Registration{}, errors.New("fake workflow expects persistent seats")
}

func (f *workflowAdapter) Submit(ctx context.Context, seat runtime.SeatSnapshot, m store.Message) (runtime.Submission, error) {
	f.calls.Add(1)
	if f.submit != nil {
		return f.submit(ctx, seat, m)
	}
	if f.work != nil {
		f.work <- m
	}
	return runtime.Submission{Actor: ipc.Actor{RunID: seat.RunID, AgentID: seat.AgentID, Generation: seat.Generation}, Native: seat.Native, Accepted: true, Channel: "fake-workflow"}, nil
}

func serveWorkflowRunner(t *testing.T, dir string, adapter *workflowAdapter) func() {
	t.Helper()
	var reset runtime.ResetAdapter
	var submit runtime.SubmitAdapter
	if adapter != nil {
		reset, submit = adapter, adapter
	}
	c, err := OpenWithAdapters(t.Context(), dir, "run", reset, submit)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
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
				t.Error("runner shutdown did not drain")
			}
		})
	}
	t.Cleanup(stop)
	return stop
}

func TestWorkflowRunnerFakeThreeRolesTwoStepsIPC(t *testing.T) {
	dir := workflowFixtureWithSession(t, "persistent")
	adapter := &workflowAdapter{work: make(chan store.Message, 4)}
	stop := serveWorkflowRunner(t, dir, adapter)
	start := workflowRequest(loopcore.Cursor{}, "main", "start")
	r := result(t, dir, start)
	var requests []ipc.Request
	requests = append(requests, start)
	var trace []string
	var dispatchIDs []string
	// No inbox scan or explicit session Advance/assign drives this flow. Each
	// fake role wakes only for the dispatch produced by the preceding event.
	for r.Workflow != nil && r.Workflow.Cursor.Phase != loopcore.Completed {
		if len(trace) == 4 {
			t.Fatal("workflow did not finish in four submissions")
		}
		if r.Status != "WORKFLOW_ADVANCED" || r.Dispatch == nil || r.Dispatch.Delivery.Phase != "SENT" || r.Dispatch.Delivery.Attempt != 1 ||
			r.Executed == nil || !*r.Executed || r.CapabilityStatus != "PARTIAL" || r.RuntimeCommunication != "UNVERIFIED" {
			t.Fatalf("dispatch not confirmed: %+v", r)
		}
		var m store.Message
		select {
		case m = <-adapter.work:
		case <-time.After(3 * time.Second):
			t.Fatal("fake role received no submission")
		}
		if m.ID != r.Workflow.Cursor.DispatchID || m.ID != r.Dispatch.Message.ID {
			t.Fatal("adapter received another dispatch")
		}
		var intent loopcore.Intent
		if err := json.Unmarshal([]byte(m.Body), &intent); err != nil {
			t.Fatal(err)
		}
		trace = append(trace, intent.Step+":"+intent.Purpose+":"+m.Recipient)
		dispatchIDs = append(dispatchIDs, m.ID)
		if intent.Revision != r.Workflow.Cursor.Revision || len(intent.Acceptance) == 0 {
			t.Fatal(intent)
		}
		kind := "handoff"
		if intent.Purpose == "review" {
			kind = "approve"
			if intent.Handoff == nil || len(intent.Handoff.Evidence) == 0 {
				t.Fatal("review did not receive formal handoff")
			}
		}
		req := workflowRequest(r.Workflow.Cursor, m.Recipient, kind)
		if kind == "approve" {
			req = changeWorkflowRequest(t, req, func(p *Params) {
				for range intent.Acceptance {
					p.Workflow.Accepted = append(p.Workflow.Accepted, true)
				}
			})
		}
		requests = append(requests, req)
		r = result(t, dir, req)
	}
	want := []string{"implement:execute:builder", "implement:review:reviewer", "verify:execute:reviewer", "verify:review:main"}
	if !reflect.DeepEqual(trace, want) || r.Status != "WORKFLOW_ADVANCED" || r.Workflow == nil || r.Workflow.Cursor.Phase != loopcore.Completed ||
		r.Workflow.Cursor.Revision != 5 || r.Workflow.Message != nil || r.Dispatch != nil || adapter.calls.Load() != 4 {
		t.Fatal(trace, r, adapter.calls.Load())
	}
	stop()
	serveWorkflowRunner(t, dir, adapter)
	before := messageTablesSnapshot(t, dir)
	for _, req := range requests {
		replay := result(t, dir, req)
		if replay.Status != "WORKFLOW_ADVANCED" || replay.Dispatch != nil || *replay.Executed {
			t.Fatal("event retry submitted native work", replay)
		}
	}
	if before != messageTablesSnapshot(t, dir) || adapter.calls.Load() != 4 {
		t.Fatal("restart replay changed ledger or resubmitted")
	}
	db, err := store.OpenExisting(filepath.Join(dir, "state.db"), false)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, id := range dispatchIDs {
		d, err := db.GetDelivery(t.Context(), id, 1)
		if err != nil || d.Phase != "SENT" || d.Attempt != 1 || d.ChannelID != "fake-workflow" {
			t.Fatal(d, err)
		}
	}
}

func TestWorkflowRunnerSixRolePipelineIPC(t *testing.T) {
	roles := []string{"role1", "role2", "role3", "role4", "role5", "role6"}
	dir := pipelineWorkflowFixture(t, roles)
	adapter := &workflowAdapter{work: make(chan store.Message, len(roles))}
	stop := serveWorkflowRunner(t, dir, adapter)
	start := workflowRequest(loopcore.Cursor{}, "main", "start")
	start = changeWorkflowRequest(t, start, func(p *Params) { p.Workflow.Task = "Complete each role's assigned portion in sequence" })
	requests := []ipc.Request{start}
	r := result(t, dir, start)
	var trace []string
	for i, role := range roles {
		if r.Dispatch == nil || r.Dispatch.Message.Recipient != role || r.Workflow == nil || r.Workflow.Cursor.Phase != loopcore.Executing {
			t.Fatalf("stage %d not dispatched to %s: %+v", i+1, role, r)
		}
		var message store.Message
		select {
		case message = <-adapter.work:
		case <-time.After(3 * time.Second):
			t.Fatalf("stage %d received no native submission", i+1)
		}
		var intent loopcore.Intent
		if err := json.Unmarshal([]byte(message.Body), &intent); err != nil {
			t.Fatal(err)
		}
		if intent.Purpose != "execute" || intent.Recipient != role || intent.Step != fmt.Sprintf("stage-%d", i+1) ||
			intent.Instruction != "Complete each role's assigned portion in sequence" {
			t.Fatalf("stage %d payload lost route or task: %+v", i+1, intent)
		}
		if i == 0 && intent.Handoff != nil || i > 0 && (intent.Handoff == nil || intent.Handoff.Summary != "work saved") {
			t.Fatalf("stage %d did not receive the preceding handoff: %+v", i+1, intent.Handoff)
		}
		trace = append(trace, message.Recipient)
		reply := workflowRequest(r.Workflow.Cursor, role, "handoff")
		requests = append(requests, reply)
		r = result(t, dir, reply)
		if i < len(roles)-1 && (r.Dispatch == nil || r.Dispatch.Message.Recipient != roles[i+1]) {
			t.Fatalf("handoff from %s did not dispatch to %s: %+v", role, roles[i+1], r)
		}
	}
	if !reflect.DeepEqual(trace, roles) || r.Workflow == nil || r.Workflow.Cursor.Phase != loopcore.Completed ||
		r.Workflow.Cursor.Step != "done" || r.Workflow.Cursor.Handoff == nil || r.Workflow.Cursor.Handoff.Summary != "work saved" ||
		r.Dispatch != nil || adapter.calls.Load() != int32(len(roles)) {
		t.Fatalf("six-role pipeline did not complete and return final handoff: trace=%v result=%+v submits=%d", trace, r, adapter.calls.Load())
	}
	stop()
	serveWorkflowRunner(t, dir, adapter)
	before := messageTablesSnapshot(t, dir)
	for _, req := range requests {
		replay := result(t, dir, req)
		if replay.Status != "WORKFLOW_ADVANCED" || replay.Dispatch != nil || replay.Executed == nil || *replay.Executed {
			t.Fatalf("exact pipeline replay executed a role: %+v", replay)
		}
	}
	if before != messageTablesSnapshot(t, dir) || adapter.calls.Load() != int32(len(roles)) {
		t.Fatal("pipeline replay changed durable messages or repeated role submissions")
	}
}

func TestWorkflowRunnerReplyRequiresMatchingTerminalStatusIPC(t *testing.T) {
	for _, tc := range []struct {
		kind, status, actor, phase string
	}{
		{"handoff", "DONE", "builder", loopcore.Reviewing},
		{"approve", "DONE", "reviewer", loopcore.Executing},
		{"changes", "DONE", "reviewer", loopcore.Executing},
		{"retry", "FAILED", "builder", loopcore.Executing},
		{"blocked", "BLOCKED_CONFIRM", "reviewer", loopcore.Paused},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			dir := workflowFixtureWithSession(t, "persistent")
			adapter := &workflowAdapter{}
			serveWorkflowRunner(t, dir, adapter)
			r := result(t, dir, workflowRequest(loopcore.Cursor{}, "main", "start"))
			if tc.actor == "reviewer" {
				r = result(t, dir, workflowRequest(r.Workflow.Cursor, "builder", "handoff"))
			}
			cursor := r.Workflow.Cursor
			valid := changeWorkflowRequest(t, workflowRequest(cursor, tc.actor, tc.kind), func(p *Params) {
				p.Status = tc.status
				if tc.kind == "approve" {
					p.Workflow.Accepted = []bool{true, true}
				}
				if tc.kind == "changes" || tc.kind == "retry" || tc.kind == "blocked" {
					p.Workflow.Reason = "verification requires another action"
				}
			})
			db, err := store.OpenExisting(filepath.Join(dir, "state.db"), false)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			_, seats, err := db.ReadState(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			before, calls := messageTablesSnapshot(t, dir), adapter.calls.Load()
			for _, status := range []string{"", "WORKING", "DONE", "BLOCKED_CONFIRM", "FAILED"} {
				if status == tc.status {
					continue
				}
				// Empty Status is omitted from JSON. Every rejected variant uses
				// the same ID as the eventual valid reply, fencing partial receipts.
				bad := changeWorkflowRequest(t, valid, func(p *Params) { p.Status = status })
				got := result(t, dir, bad)
				if got.Status != "FAILED" || len(got.Diagnostics) != 1 || got.Diagnostics[0].Code != "WORKFLOW_INVALID_TRANSITION" ||
					got.Workflow != nil || got.Record != nil || got.Dispatch != nil || got.Executed == nil || *got.Executed {
					t.Fatalf("status %q accepted or dispatched: %+v", status, got)
				}
				if before != messageTablesSnapshot(t, dir) || adapter.calls.Load() != calls {
					t.Fatalf("status %q changed reply/cursor/dispatch state", status)
				}
				_, afterSeats, err := db.ReadState(t.Context())
				if err != nil || !reflect.DeepEqual(seats, afterSeats) {
					t.Fatalf("status %q changed seats: %+v, %v", status, afterSeats, err)
				}
			}
			r = result(t, dir, valid)
			if r.Status != "WORKFLOW_ADVANCED" || r.Workflow == nil || r.Workflow.Cursor.Revision != cursor.Revision+1 ||
				r.Workflow.Cursor.Phase != tc.phase || r.Record == nil || r.Record.Message.ReportStatus != tc.status {
				t.Fatalf("matching terminal reply did not commit: %+v", r)
			}
			seat, err := db.GetSeat(t.Context(), "run", tc.actor, 1)
			if err != nil || seat.CurrentMessageID == cursor.DispatchID {
				t.Fatalf("terminal reply left old work busy: %+v, %v", seat, err)
			}
			if tc.kind == "blocked" {
				if r.Dispatch != nil || adapter.calls.Load() != calls || seat.Status != "READY" {
					t.Fatal("blocked reply dispatched or retained busy seat", r, seat)
				}
				resume := changeWorkflowRequest(t, workflowRequest(r.Workflow.Cursor, "main", "resume"), func(p *Params) {
					p.Workflow.Reason = "decision saved"
				})
				r = result(t, dir, resume)
			}
			if r.Dispatch == nil || r.Dispatch.Delivery.Phase != "SENT" || r.Executed == nil || !*r.Executed || adapter.calls.Load() != calls+1 {
				t.Fatalf("next dispatch stalled after terminal reply: %+v", r)
			}
			before = messageTablesSnapshot(t, dir)
			replay := result(t, dir, valid)
			if replay.Status != "WORKFLOW_ADVANCED" || replay.Dispatch != nil || *replay.Executed ||
				before != messageTablesSnapshot(t, dir) || adapter.calls.Load() != calls+1 {
				t.Fatal("terminal reply replay changed state or dispatched", replay)
			}
		})
	}
}

func TestWorkflowRunnerUnknownSubmitAndQueuedDoNotReplay(t *testing.T) {
	for _, mode := range []string{"unknown", "rejected", "wrong-binding", "draft", "configuration-error", "missing-adapter"} {
		t.Run(mode, func(t *testing.T) {
			dir := workflowFixtureWithSession(t, "persistent")
			adapter := &workflowAdapter{}
			switch mode {
			case "unknown":
				adapter.submit = func(context.Context, runtime.SeatSnapshot, store.Message) (runtime.Submission, error) {
					return runtime.Submission{}, errors.New("submission response lost")
				}
			case "rejected", "wrong-binding":
				adapter.submit = func(_ context.Context, seat runtime.SeatSnapshot, _ store.Message) (runtime.Submission, error) {
					return runtime.Submission{Accepted: mode == "wrong-binding", Actor: ipc.Actor{RunID: seat.RunID, AgentID: seat.AgentID, Generation: 99}, Native: seat.Native, Channel: "fake"}, nil
				}
			case "draft":
				adapter.inspect = func(_ context.Context, seat runtime.SeatSnapshot) (runtime.InputState, error) {
					return runtime.InputState{Actor: ipc.Actor{RunID: seat.RunID, AgentID: seat.AgentID, Generation: seat.Generation}, Native: seat.Native, Verified: true, Draft: "unsent user text"}, nil
				}
			case "configuration-error":
				adapter.inspect = func(context.Context, runtime.SeatSnapshot) (runtime.InputState, error) {
					return runtime.InputState{}, &runtime.RecoveryError{Code: "CONFIGURATION_ERROR"}
				}
			}
			if mode == "missing-adapter" {
				// A nil adapter is an explicit programmatic injection too.
				stop := serveWorkflowRunner(t, dir, nil)
				start := workflowRequest(loopcore.Cursor{}, "main", "start")
				r := result(t, dir, start)
				if r.Dispatch == nil || r.Dispatch.Delivery.Phase != "QUEUED" || *r.Executed {
					t.Fatal(r)
				}
				stop()
				serveWorkflowRunner(t, dir, adapter)
				before := messageTablesSnapshot(t, dir)
				result(t, dir, start)
				if adapter.calls.Load() != 0 || before != messageTablesSnapshot(t, dir) {
					t.Fatal("reopening with an adapter replayed queued work")
				}
				return
			}
			stop := serveWorkflowRunner(t, dir, adapter)
			start := workflowRequest(loopcore.Cursor{}, "main", "start")
			r := result(t, dir, start)
			phase, calls := "SUBMIT_REQUESTED", int32(1)
			if mode == "draft" {
				phase, calls = "QUEUED", 0
			}
			if mode == "configuration-error" {
				phase, calls = "PENDING", 0
				if r.Workflow == nil || len(r.Diagnostics) != 1 || r.Diagnostics[0].Code != "WORKFLOW_DISPATCH_FAILED" {
					t.Fatal("committed cursor was lost on dispatch failure", r)
				}
			}
			if r.Status != "WORKFLOW_ADVANCED" || r.Dispatch == nil || r.Dispatch.Delivery.Phase != phase || adapter.calls.Load() != calls {
				t.Fatal(r, adapter.calls.Load())
			}
			stop()
			clean := &workflowAdapter{}
			serveWorkflowRunner(t, dir, clean)
			before := messageTablesSnapshot(t, dir)
			for range 2 {
				replay := result(t, dir, start)
				if replay.Status != "WORKFLOW_ADVANCED" || replay.Dispatch != nil || *replay.Executed {
					t.Fatal(replay)
				}
			}
			if clean.calls.Load() != 0 || before != messageTablesSnapshot(t, dir) {
				t.Fatal("unknown/queued work was replayed")
			}
		})
	}
}

func TestWorkflowRunnerFailedEventsPlainDoneAndStopDoNotDispatch(t *testing.T) {
	dir := workflowFixtureWithSession(t, "persistent")
	adapter := &workflowAdapter{}
	stop := serveWorkflowRunner(t, dir, adapter)
	start := workflowRequest(loopcore.Cursor{}, "main", "start")
	r := result(t, dir, start)
	c := r.Workflow.Cursor
	plain := result(t, dir, request("builder", "reply", "plain", Params{MessageID: c.DispatchID, Body: "DONE", Status: "DONE"}))
	if plain.Workflow != nil || adapter.calls.Load() != 1 {
		t.Fatal("plain DONE advanced workflow")
	}
	valid := workflowRequest(c, "builder", "handoff")
	for i, bad := range []ipc.Request{
		workflowRequest(c, "reviewer", "handoff"),
		changeWorkflowRequest(t, valid, func(p *Params) { p.Workflow.Revision = 0 }),
		changeWorkflowRequest(t, valid, func(p *Params) { p.Workflow.Handoff = nil }),
	} {
		bad.RequestID = fmt.Sprintf("invalid-%d", i)
		before := messageTablesSnapshot(t, dir)
		if got := result(t, dir, bad); got.Status != "FAILED" || adapter.calls.Load() != 1 || before != messageTablesSnapshot(t, dir) {
			t.Fatal(got)
		}
	}
	result(t, dir, request("main", "stop", "stop", Params{}))
	stop()
	serveWorkflowRunner(t, dir, adapter)
	before := messageTablesSnapshot(t, dir)
	if replay := result(t, dir, start); replay.Status != "WORKFLOW_ADVANCED" || replay.Dispatch != nil {
		t.Fatal(replay)
	}
	if got := result(t, dir, valid); got.Status != "FAILED" || got.Diagnostics[0].Code != "RUN_STOPPED" {
		t.Fatal(got)
	}
	if adapter.calls.Load() != 1 || before != messageTablesSnapshot(t, dir) {
		t.Fatal("stop or failed event dispatched work")
	}
}

func TestWorkflowRunnerConcurrentStartClaimsOneSubmission(t *testing.T) {
	dir := workflowFixtureWithSession(t, "persistent")
	adapter := &workflowAdapter{}
	serveWorkflowRunner(t, dir, adapter)
	req := workflowRequest(loopcore.Cursor{}, "main", "start")
	var wg sync.WaitGroup
	responses := make(chan Result, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			responses <- result(t, dir, req)
		}()
	}
	wg.Wait()
	close(responses)
	dispatches := 0
	for r := range responses {
		if r.Status != "WORKFLOW_ADVANCED" {
			t.Fatal(r)
		}
		if r.Dispatch != nil {
			dispatches++
		}
	}
	if dispatches != 1 || adapter.calls.Load() != 1 {
		t.Fatal("concurrent start duplicated submission", dispatches, adapter.calls.Load())
	}
}

func TestWorkflowRunnerStopBetweenEventCommitAndSubmission(t *testing.T) {
	dir := workflowFixtureWithSession(t, "persistent")
	adapter := &workflowAdapter{}
	adapter.inspect = func(ctx context.Context, seat runtime.SeatSnapshot) (runtime.InputState, error) {
		db, err := store.OpenExisting(filepath.Join(dir, "state.db"), true)
		if err != nil {
			return runtime.InputState{}, err
		}
		defer db.Close()
		if _, err := db.RequestStop(ctx, "run"); err != nil {
			return runtime.InputState{}, err
		}
		return runtime.InputState{Actor: ipc.Actor{RunID: seat.RunID, AgentID: seat.AgentID, Generation: seat.Generation}, Native: seat.Native, Verified: true}, nil
	}
	serveWorkflowRunner(t, dir, adapter)
	r := result(t, dir, workflowRequest(loopcore.Cursor{}, "main", "start"))
	if r.Status != "WORKFLOW_ADVANCED" || r.Workflow == nil || r.Dispatch == nil || r.Dispatch.Coordination != "STOP_REQUESTED" ||
		r.Dispatch.Delivery.Phase != "QUEUED" || adapter.calls.Load() != 0 || *r.Executed {
		t.Fatal("stop did not fence adapter after event commit", r)
	}
}
