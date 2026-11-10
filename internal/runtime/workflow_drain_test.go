package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"agentflow/internal/ipc"
	"agentflow/internal/loopcore"
	"agentflow/internal/store"
)

type drainAdapter struct {
	fakeSubmitAdapter
	collected int
	lastKind  string
}

func (a *drainAdapter) WorkflowReply(_ context.Context, seat SeatSnapshot, m store.Message, c loopcore.Cursor) (ReplyMessage, loopcore.Event, error) {
	a.collected++
	id := fmt.Sprintf("result-%d", c.Revision)
	e := loopcore.Event{ID: id, Actor: seat.AgentID, Revision: c.Revision, MessageID: m.ID, Kind: "handoff",
		Handoff: &loopcore.Handoff{Summary: "done", Evidence: []string{"fixture.log"}, Next: "review"}}
	if c.Phase == loopcore.Reviewing {
		e.Kind, e.Handoff, e.Accepted = "approve", nil, []bool{true}
	}
	status := "DONE"
	if a.collected == 128 && a.lastKind != "" {
		e.Kind, e.Handoff, e.Accepted = a.lastKind, nil, nil
		e.Reason = "fixture boundary result"
		if e.Kind == "blocked" {
			status = "BLOCKED_CONFIRM"
		} else {
			status = "FAILED"
		}
	}
	return ReplyMessage{Actor: seatActor(seat), RequestID: id, MessageID: m.ID, Body: "fixture result", Status: status}, e, nil
}

func drainFixture(t *testing.T, steps int, a *drainAdapter, leadApprover ...string) (*store.Store, *WorkflowRunner, ipc.Actor) {
	t.Helper()
	_, frozen := workflowConfig(t)
	base := frozen.Workflow.Steps[0]
	base.Execute.Agent, base.Reviewer, base.Acceptance, base.MaxAttempts = "builder", "reviewer", []string{"verified"}, 1
	if len(leadApprover) > 0 {
		frozen.Workflow.Type = "approval"
		base.Reviewer, base.Approver = "", leadApprover[0]
	}
	frozen.Workflow.Steps = nil
	for i := range steps {
		s := base
		s.ID, s.Next = fmt.Sprintf("step-%d", i), fmt.Sprintf("step-%d", i+1)
		if i == steps-1 {
			s.Next = frozen.Workflow.Completion
		}
		frozen.Workflow.Steps = append(frozen.Workflow.Steps, s)
	}
	frozen.Workflow.Entry = "step-0"
	for id, role := range frozen.Agents {
		role.Session = "persistent"
		frozen.Agents[id] = role
	}
	refreshWorkflowDigest(t, frozen)
	body, err := json.Marshal(frozen)
	if err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateRun(t.Context(), store.Run{RunID: "drain", ConfigSnapshot: string(body), ConfigDigest: frozen.Digest}); err != nil {
		t.Fatal(err)
	}
	for id, role := range frozen.Agents {
		if err := db.CreateSeat(t.Context(), store.Seat{RunID: "drain", AgentID: id, Generation: 1, Current: true, Status: "READY", Tool: role.Tool,
			NativeSessionID: "native-" + id, WorkspaceID: "workspace", SurfaceID: "surface-" + id}); err != nil {
			t.Fatal(err)
		}
	}
	sessions, err := NewSessionCoordinator(db, &fakeResetAdapter{}, a)
	if err != nil {
		t.Fatal(err)
	}
	runner, err := NewWorkflowRunner(sessions)
	if err != nil {
		t.Fatal(err)
	}
	return db, runner, ipc.Actor{RunID: "drain", AgentID: "main", Generation: 1}
}

func TestWorkflowDrainYieldsLeadApprovalToCurrentSeat(t *testing.T) {
	a := &drainAdapter{}
	db, runner, actor := drainFixture(t, 1, a, "main")
	runner.yieldLeadActor = true
	seed, err := db.AdvanceWorkflowWithSessions(t.Context(), messageActor(actor), loopcore.Event{ID: "start", Actor: "main", Kind: "start"})
	if err != nil {
		t.Fatal(err)
	}
	out, err := runner.Drain(t.Context(), actor, seed.Cursor.Revision)
	if err != nil || out.DispatchError != nil || !out.AwaitingActor || out.Dispatch == nil || out.Dispatch.Message.Recipient != "main" || out.Current == nil || out.Current.Phase != loopcore.Reviewing || a.collected != 1 || a.calls.Load() != 1 {
		t.Fatalf("lead approval was not yielded without auto-approving: out=%+v collected=%d calls=%d err=%v", out, a.collected, a.calls.Load(), err)
	}
}

func TestWorkflowDrainActionLimitReturnsLatestCursor(t *testing.T) {
	for _, tc := range []struct {
		name, kind string
		steps      int
		phase      string
	}{
		{"completed", "", 64, loopcore.Completed},
		{"paused", "blocked", 64, loopcore.Paused},
		{"exhausted", "retry", 64, loopcore.Exhausted},
		{"continue", "", 65, loopcore.Executing},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := &drainAdapter{lastKind: tc.kind}
			db, runner, actor := drainFixture(t, tc.steps, a)
			seed, err := db.AdvanceWorkflowWithSessions(t.Context(), messageActor(actor), loopcore.Event{ID: "start", Actor: "main", Kind: "start"})
			if err != nil {
				t.Fatal(err)
			}
			out, err := runner.Drain(t.Context(), actor, seed.Cursor.Revision)
			latest, readErr := db.WorkflowCursor(t.Context(), actor.RunID)
			if err != nil || readErr != nil || out.Current == nil || !reflect.DeepEqual(*out.Current, latest) || latest.Revision != 129 || latest.Phase != tc.phase || a.collected != 128 || a.calls.Load() != 128 {
				t.Fatalf("out=%+v latest=%+v collected=%d calls=%d err=%v/%v", out, latest, a.collected, a.calls.Load(), err, readErr)
			}
			if tc.name == "continue" {
				if out.DispatchError == nil || !strings.Contains(out.DispatchError.Error(), "action limit") {
					t.Fatal(out.DispatchError)
				}
			} else if out.DispatchError != nil {
				t.Fatal(out.DispatchError)
			}
			calls, collected := a.calls.Load(), a.collected
			stale, staleErr := runner.Drain(t.Context(), actor, seed.Cursor.Revision)
			if tc.name == "continue" {
				if staleErr != loopcore.ErrTransition || a.calls.Load() != calls || a.collected != collected {
					t.Fatalf("stale drain advanced active workflow: %+v err=%v calls=%d/%d collected=%d/%d", stale, staleErr, a.calls.Load(), calls, a.collected, collected)
				}
			} else if staleErr != nil || stale.Dispatch != nil || stale.ActionRequested || stale.DispatchError != nil || stale.Current == nil || !reflect.DeepEqual(*stale.Current, latest) || a.calls.Load() != calls || a.collected != collected {
				t.Fatalf("stale terminal drain was not a read-only current-state result: %+v calls=%d/%d collected=%d/%d err=%v", stale, a.calls.Load(), calls, a.collected, collected, staleErr)
			}
			a.lastKind = ""
			again, err := runner.Drain(t.Context(), actor, latest.Revision)
			if err != nil || again.DispatchError != nil {
				t.Fatal(again, err)
			}
			if tc.name == "continue" {
				if again.Current.Phase != loopcore.Completed || a.collected != 130 {
					t.Fatal(again)
				}
			} else if !reflect.DeepEqual(*again.Current, latest) || a.collected != 128 {
				t.Fatal("terminal drain performed work", again)
			}
		})
	}
}

func TestWorkflowDrainMissingSentCannotBeReplacedByAck(t *testing.T) {
	a := &drainAdapter{}
	db, runner, actor := drainFixture(t, 1, a)
	seed, err := db.AdvanceWorkflowWithSessions(t.Context(), messageActor(actor), loopcore.Event{ID: "start", Actor: "main", Kind: "start"})
	if err != nil {
		t.Fatal(err)
	}
	w, err := db.SessionWork(t.Context(), seed.Message.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, claimed, err := db.BeginSessionAction(t.Context(), seed.Message.ID, w.Seat, "submit", ""); err != nil || !claimed {
		t.Fatal(claimed, err)
	}
	if err := db.NoteSessionUnknown(t.Context(), seed.Message.ID, "submit"); err != nil {
		t.Fatal(err)
	}
	_, ackErr := db.Ack(t.Context(), store.MessageActor{RunID: actor.RunID, AgentID: "builder", Generation: 1}, "ack", seed.Message.ID)
	if ackErr == nil {
		t.Error("ACK accepted without durable SENT")
	}
	out, err := runner.Drain(t.Context(), actor, seed.Cursor.Revision)
	if err != nil || out.DispatchError == nil || out.Current == nil || !reflect.DeepEqual(*out.Current, seed.Cursor) || a.collected != 0 || a.calls.Load() != 0 {
		t.Fatalf("ACK bypassed native gate: %+v collected=%d calls=%d err=%v", out, a.collected, a.calls.Load(), err)
	}
}
