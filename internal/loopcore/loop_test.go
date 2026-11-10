package loopcore

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"agentflow/internal/config"
)

func testWorkflow() *config.Workflow {
	return &config.Workflow{Entry: "build", Completion: "done", Steps: []config.WorkflowStep{{ID: "build", Execute: config.WorkflowExecute{Agent: "builder"}, Reviewer: "reviewer", Instruction: "build", Acceptance: []string{"tests pass"}, HandoffRequired: true, MaxAttempts: 2, Next: "done", Transitions: config.WorkflowTransitions{Approve: "next", Changes: "self", Retry: "self", Blocked: "pause"}}}}
}

func TestPureWorkflowTransitions(t *testing.T) {
	w := testWorkflow()
	c, intent, err := Advance(w, "main", Cursor{}, Event{ID: "start", Kind: "start", Actor: "main"})
	if err != nil || intent.Recipient != "builder" || c.Attempt != 1 {
		t.Fatalf("start: %+v %v", c, err)
	}
	c.DispatchID = "build-1"
	h := &Handoff{Summary: "implemented", Evidence: []string{"test.log"}, Next: "review"}
	e := Event{ID: "handoff", Revision: c.Revision, Kind: "handoff", Actor: "builder", MessageID: c.DispatchID, Handoff: h}
	before, _ := json.Marshal(c)
	r, intent, err := Advance(w, "main", c, e)
	if err != nil || r.Phase != Reviewing || intent.Recipient != "reviewer" {
		t.Fatalf("handoff: %+v %v", r, err)
	}
	h.Evidence[0] = "changed"
	intent.Handoff.Evidence[0] = "changed again"
	after, _ := json.Marshal(c)
	if string(before) != string(after) || r.Handoff.Evidence[0] != "test.log" {
		t.Fatal("reducer aliases its inputs or outputs")
	}
	r.DispatchID = "review-1"
	for _, invalid := range []Event{
		{ID: "wrong actor", Revision: r.Revision, Kind: "approve", Actor: "builder", MessageID: r.DispatchID, Accepted: []bool{true}},
		{ID: "no criteria", Revision: r.Revision, Kind: "approve", Actor: "reviewer", MessageID: r.DispatchID},
		{ID: "failed criterion", Revision: r.Revision, Kind: "approve", Actor: "reviewer", MessageID: r.DispatchID, Accepted: []bool{false}},
		{ID: "stale", Revision: 1, Kind: "approve", Actor: "reviewer", MessageID: r.DispatchID, Accepted: []bool{true}},
		{ID: "wrong message", Revision: r.Revision, Kind: "approve", Actor: "reviewer", MessageID: "other", Accepted: []bool{true}},
	} {
		if _, _, err := Advance(w, "main", r, invalid); !errors.Is(err, ErrTransition) {
			t.Fatalf("accepted invalid event: %+v %v", invalid, err)
		}
	}
	c, _, err = Advance(w, "main", r, Event{ID: "changes", Revision: r.Revision, Kind: "changes", Actor: "reviewer", MessageID: r.DispatchID, Reason: "fix"})
	if err != nil || c.Attempt != 2 || c.Phase != Executing || c.Handoff != nil {
		t.Fatalf("changes: %+v %v", c, err)
	}
	c.DispatchID = "build-2"
	c, intent, err = Advance(w, "main", c, Event{ID: "retry", Revision: c.Revision, Kind: "retry", Actor: "builder", MessageID: c.DispatchID, Reason: "failed"})
	if err != nil || c.Phase != Exhausted || intent != nil {
		t.Fatalf("budget: %+v %v", c, err)
	}
}

func TestPauseResumeConsumesBudgetAndCannotCompleteWithoutReview(t *testing.T) {
	w := testWorkflow()
	c := Cursor{Revision: 1, Step: "build", Attempt: 1, Phase: Executing, DispatchID: "m1"}
	if _, _, err := Advance(w, "main", c, Event{ID: "approve", Revision: 1, Kind: "approve", Actor: "builder", MessageID: "m1", Accepted: []bool{true}}); err == nil {
		t.Fatal("execution bypassed review")
	}
	c, intent, err := Advance(w, "main", c, Event{ID: "block", Revision: 1, Kind: "blocked", Actor: "builder", MessageID: "m1", Reason: "needs input"})
	if err != nil || c.Phase != Paused || intent != nil {
		t.Fatal(c, err)
	}
	c, intent, err = Advance(w, "main", c, Event{ID: "resume", Revision: c.Revision, Kind: "resume", Actor: "main", Reason: "input supplied"})
	if err != nil || c.Phase != Executing || c.Attempt != 2 || intent == nil {
		t.Fatal(c, err)
	}
}

func TestInitialTaskOverridesConfiguredInstruction(t *testing.T) {
	w := testWorkflow()
	_, intent, err := Advance(w, "main", Cursor{}, Event{ID: "start", Kind: "start", Actor: "main", Task: "梳理当前项目用途"})
	if err != nil {
		t.Fatal(err)
	}
	if intent == nil || intent.Instruction != "梳理当前项目用途" {
		t.Fatalf("initial task was not dispatched: %+v", intent)
	}
}

func TestCompletedWorkflowStartsNextTaskWithoutResettingRevision(t *testing.T) {
	w := testWorkflow()
	current := Cursor{Revision: 8, Step: w.Completion, Attempt: 1, Phase: Completed}
	next, intent, err := Advance(w, "main", current, Event{ID: "next-task", Revision: 8, Kind: "start", Actor: "main", Task: "第二个任务"})
	if err != nil || next.Revision != 9 || next.Step != w.Entry || next.Attempt != 1 || next.Phase != Executing || intent == nil || intent.Instruction != "第二个任务" {
		t.Fatalf("next task: cursor=%+v intent=%+v err=%v", next, intent, err)
	}
	if _, _, err := Advance(w, "main", Cursor{Revision: 8, Step: w.Entry, Attempt: 1, Phase: Executing}, Event{ID: "replace-active", Revision: 8, Kind: "start", Actor: "main"}); !errors.Is(err, ErrTransition) {
		t.Fatalf("active workflow was replaced: %v", err)
	}
}

func TestApprovalWorkflowLetsSeatsWorkApproveAndReturnEarlier(t *testing.T) {
	w := &config.Workflow{Type: config.WorkflowApproval, Entry: "prepare", Completion: "done", Steps: []config.WorkflowStep{
		{ID: "prepare", Execute: config.WorkflowExecute{Agent: "builder"}, Approver: "reviewer", Instruction: "Prepare the request", Acceptance: []string{"request is clear"},
			HandoffRequired: true, MaxAttempts: 3, Transitions: config.WorkflowTransitions{Approve: "next", Changes: "self", Retry: "self", Blocked: "pause"}, Next: "deliver"},
		{ID: "deliver", Execute: config.WorkflowExecute{Agent: "reviewer"}, Approver: "main", Instruction: "Deliver the approved request", Acceptance: []string{"delivery is correct"},
			HandoffRequired: true, MaxAttempts: 2, Transitions: config.WorkflowTransitions{Approve: "next", Changes: "prepare", Retry: "self", Blocked: "pause"}, Next: "done"},
	}}
	advance := func(c Cursor, kind, actor, reason, dispatchID string, handoff *Handoff, accepted []bool) (Cursor, *Intent, error) {
		return Advance(w, "main", c, Event{ID: kind + actor + reason, Revision: c.Revision, Kind: kind, Actor: actor,
			MessageID: dispatchID, Reason: reason, Handoff: handoff, Accepted: accepted})
	}
	newHandoff := func(summary string) *Handoff {
		return &Handoff{Summary: summary, Evidence: []string{summary + ".md"}, Next: "continue"}
	}
	c, intent, err := Advance(w, "main", Cursor{}, Event{ID: "start", Kind: "start", Actor: "main", Task: "Process request"})
	if err != nil || intent == nil || intent.Recipient != "builder" || c.Attempts["prepare"] != 1 {
		t.Fatalf("start: cursor=%+v intent=%+v err=%v", c, intent, err)
	}
	c.DispatchID = "prepare-1"
	c, intent, err = advance(c, "handoff", "builder", "", c.DispatchID, newHandoff("draft-1"), nil)
	if err != nil || intent == nil || intent.Recipient != "reviewer" || c.Phase != Reviewing {
		t.Fatalf("first approval dispatch: cursor=%+v intent=%+v err=%v", c, intent, err)
	}
	c.DispatchID = "approve-prepare-1"
	c, intent, err = advance(c, "changes", "reviewer", "clarify the request", c.DispatchID, nil, nil)
	if err != nil || intent == nil || intent.Recipient != "builder" || intent.Reason != "clarify the request" || intent.Handoff == nil || intent.Handoff.Summary != "draft-1" || c.Attempts["prepare"] != 2 {
		t.Fatalf("return to previous executor: cursor=%+v intent=%+v err=%v", c, intent, err)
	}
	c.DispatchID = "prepare-2"
	c, intent, err = advance(c, "handoff", "builder", "", c.DispatchID, newHandoff("draft-2"), nil)
	if err != nil || intent == nil || intent.Recipient != "reviewer" {
		t.Fatalf("resubmit: cursor=%+v intent=%+v err=%v", c, intent, err)
	}
	c.DispatchID = "approve-prepare-2"
	c, intent, err = advance(c, "approve", "reviewer", "", c.DispatchID, nil, []bool{true})
	if err != nil || intent == nil || intent.Recipient != "reviewer" || intent.Purpose != "execute" || c.Step != "deliver" || c.Attempts["deliver"] != 1 {
		t.Fatalf("approve into next role's work: cursor=%+v intent=%+v err=%v", c, intent, err)
	}
	c.DispatchID = "deliver-1"
	c, intent, err = advance(c, "handoff", "reviewer", "", c.DispatchID, newHandoff("delivery-1"), nil)
	if err != nil || intent == nil || intent.Recipient != "main" {
		t.Fatalf("main approval dispatch: cursor=%+v intent=%+v err=%v", c, intent, err)
	}
	c.DispatchID = "approve-deliver-1"
	c, intent, err = advance(c, "changes", "main", "correct the source request", c.DispatchID, nil, nil)
	if err != nil || intent == nil || intent.Recipient != "builder" || c.Step != "prepare" || c.Attempts["prepare"] != 3 || intent.Handoff == nil || intent.Handoff.Summary != "delivery-1" {
		t.Fatalf("return to earlier workflow step: cursor=%+v intent=%+v err=%v", c, intent, err)
	}
	c.DispatchID = "prepare-3"
	c, intent, err = advance(c, "handoff", "builder", "", c.DispatchID, newHandoff("draft-3"), nil)
	if err != nil || intent == nil || intent.Recipient != "reviewer" {
		t.Fatalf("second resubmit: cursor=%+v intent=%+v err=%v", c, intent, err)
	}
	c.DispatchID = "approve-prepare-3"
	c, intent, err = advance(c, "approve", "reviewer", "", c.DispatchID, nil, []bool{true})
	if err != nil || intent == nil || intent.Recipient != "reviewer" || c.Attempts["deliver"] != 2 {
		t.Fatalf("second work stage visit: cursor=%+v intent=%+v err=%v", c, intent, err)
	}
	c.DispatchID = "deliver-2"
	c, intent, err = advance(c, "handoff", "reviewer", "", c.DispatchID, newHandoff("delivery-2"), nil)
	if err != nil || intent == nil || intent.Recipient != "main" {
		t.Fatalf("final approval dispatch: cursor=%+v intent=%+v err=%v", c, intent, err)
	}
	c.DispatchID = "approve-deliver-2"
	c, intent, err = advance(c, "approve", "main", "", c.DispatchID, nil, []bool{true})
	if err != nil || intent != nil || c.Phase != Completed || c.Step != "done" {
		t.Fatalf("complete: cursor=%+v intent=%+v err=%v", c, intent, err)
	}
}

func TestPipelineMovesThroughSixRolesAndReturnsFinalHandoff(t *testing.T) {
	roles := []string{"role1", "role2", "role3", "role4", "role5", "role6"}
	flow := "main -> " + strings.Join(roles, " -> ") + " -> main"
	w := &config.Workflow{Type: config.WorkflowPipeline, Flow: flow}
	ids := append([]string{"main"}, roles...)
	if err := config.ValidateWorkflow(w, ids, "main"); err != nil {
		t.Fatal(err)
	}
	c, intent, err := Advance(w, "main", Cursor{}, Event{ID: "start", Kind: "start", Actor: "main", Task: "Complete the six role-specific parts"})
	if err != nil || intent == nil || intent.Recipient != roles[0] || intent.Instruction != "Complete the six role-specific parts" || c.Task != intent.Instruction {
		t.Fatalf("pipeline start: cursor=%+v intent=%+v err=%v", c, intent, err)
	}
	var trace []string
	for i, role := range roles {
		c.DispatchID = "dispatch-" + role
		if intent.Recipient != role || intent.Purpose != "execute" || intent.Instruction != "Complete the six role-specific parts" {
			t.Fatalf("stage %d dispatched incorrectly: %+v", i+1, intent)
		}
		trace = append(trace, intent.Step+":"+role)
		handoff := &Handoff{Summary: "finished " + role, Evidence: []string{role + ".md"}, Next: "continue"}
		c, intent, err = Advance(w, "main", c, Event{ID: "handoff-" + role, Revision: c.Revision, Kind: "handoff", Actor: role,
			MessageID: c.DispatchID, Handoff: handoff})
		if err != nil {
			t.Fatalf("handoff from %s: %v", role, err)
		}
		if i < len(roles)-1 {
			if intent == nil || intent.Recipient != roles[i+1] || intent.Handoff == nil || intent.Handoff.Summary != handoff.Summary || c.Task != "Complete the six role-specific parts" {
				t.Fatalf("stage %d did not hand off to next role: cursor=%+v intent=%+v", i+1, c, intent)
			}
		} else if intent != nil || c.Phase != Completed || c.Step != "done" || c.Handoff == nil || c.Handoff.Summary != handoff.Summary {
			t.Fatalf("final result did not return to lead: cursor=%+v intent=%+v", c, intent)
		}
	}
	if len(trace) != 6 || c.Revision != 7 {
		t.Fatalf("unexpected six-role route: %v cursor=%+v", trace, c)
	}
}

func TestPipelineRetryPauseResumeAndRejectReviewEvents(t *testing.T) {
	maxAttempts := 2
	w := &config.Workflow{Type: config.WorkflowPipeline, Flow: "main -> builder -> reviewer -> main", MaxAttempts: &maxAttempts}
	c, intent, err := Advance(w, "main", Cursor{}, Event{ID: "start", Kind: "start", Actor: "main", Task: "task"})
	if err != nil || intent.Recipient != "builder" {
		t.Fatal(c, intent, err)
	}
	c.DispatchID = "builder-1"
	if _, _, err := Advance(w, "main", c, Event{ID: "approve", Revision: c.Revision, Kind: "approve", Actor: "builder", MessageID: c.DispatchID, Accepted: []bool{true}}); !errors.Is(err, ErrTransition) {
		t.Fatalf("pipeline accepted review-only event: %v", err)
	}
	c, intent, err = Advance(w, "main", c, Event{ID: "retry", Revision: c.Revision, Kind: "retry", Actor: "builder", MessageID: c.DispatchID, Reason: "redo"})
	if err != nil || c.Attempt != 2 || intent == nil || intent.Recipient != "builder" {
		t.Fatalf("pipeline retry: cursor=%+v intent=%+v err=%v", c, intent, err)
	}
	c.DispatchID = "builder-2"
	c, intent, err = Advance(w, "main", c, Event{ID: "blocked", Revision: c.Revision, Kind: "blocked", Actor: "builder", MessageID: c.DispatchID, Reason: "need user input"})
	if err != nil || c.Phase != Paused || intent != nil {
		t.Fatalf("pipeline pause: cursor=%+v intent=%+v err=%v", c, intent, err)
	}
	c, intent, err = Advance(w, "main", c, Event{ID: "resume", Revision: c.Revision, Kind: "resume", Actor: "main", Reason: "input supplied"})
	if err != nil || c.Phase != Exhausted || intent != nil {
		t.Fatalf("pipeline budget was bypassed by resume: cursor=%+v intent=%+v err=%v", c, intent, err)
	}
}
