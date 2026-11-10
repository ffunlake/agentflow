// Package loopcore implements deterministic workflow decisions without I/O.
package loopcore

import (
	"errors"
	"strings"

	"agentflow/internal/config"
)

const (
	Executing = "EXECUTING"
	Reviewing = "REVIEWING"
	Paused    = "PAUSED"
	Exhausted = "EXHAUSTED"
	Completed = "COMPLETED"
)

var ErrTransition = errors.New("WORKFLOW_INVALID_TRANSITION")

// Cursor is the durable state after one accepted event. Revision also fences
// late results from earlier attempts, even when the logical agent is unchanged.
type Cursor struct {
	Revision   int64          `json:"revision"`
	Step       string         `json:"step"`
	Attempt    int            `json:"attempt"`
	Attempts   map[string]int `json:"attempts,omitempty"`
	Phase      string         `json:"phase"`
	DispatchID string         `json:"dispatch_id,omitempty"`
	Task       string         `json:"task,omitempty"`
	Handoff    *Handoff       `json:"handoff,omitempty"`
}

// Handoff is an explicit agent claim, never an automatic acceptance result.
type Handoff struct {
	Summary  string   `json:"summary"`
	Evidence []string `json:"evidence"`
	Next     string   `json:"next"`
}

type Event struct {
	ID        string   `json:"id"`
	Revision  int64    `json:"revision"`
	Kind      string   `json:"kind"` // start, handoff, approve, changes, retry, blocked, resume
	Actor     string   `json:"actor"`
	Task      string   `json:"task,omitempty"` // optional user task on the initial start event
	MessageID string   `json:"message_id,omitempty"`
	Handoff   *Handoff `json:"handoff,omitempty"`
	Accepted  []bool   `json:"accepted,omitempty"` // one explicit judgment per criterion
	Reason    string   `json:"reason,omitempty"`
}

// Intent describes an outbox item; persistence assigns its message identity.
type Intent struct {
	Revision        int64    `json:"revision"`
	Recipient       string   `json:"recipient"`
	Purpose         string   `json:"purpose"`
	Step            string   `json:"step"`
	Attempt         int      `json:"attempt"`
	Instruction     string   `json:"instruction"`
	Acceptance      []string `json:"acceptance"`
	HandoffRequired bool     `json:"handoff_required"`
	Handoff         *Handoff `json:"handoff,omitempty"`
	Reason          string   `json:"reason,omitempty"`
}

// Advance consumes exactly one event and returns detached state and at most one
// intent. Caller authenticates generations and persists the result atomically.
func Advance(w *config.Workflow, lead string, current Cursor, event Event) (Cursor, *Intent, error) {
	if w == nil || strings.TrimSpace(lead) == "" || strings.TrimSpace(event.ID) == "" || event.Revision != current.Revision {
		return Cursor{}, nil, ErrTransition
	}
	ids := append([]string{lead}, w.ParticipantIDs()...)
	if err := config.ValidateWorkflow(w, ids, lead); err != nil {
		return Cursor{}, nil, err
	}
	steps := w.RuntimeSteps()
	next := current
	next.Handoff = cloneHandoff(current.Handoff)
	next.Attempts = cloneAttempts(current.Attempts)
	next.Revision++
	var step config.WorkflowStep
	for _, s := range steps {
		if s.ID == current.Step {
			step = s
			break
		}
	}
	dispatch := func(cursor Cursor, purpose string, startTask string) (Cursor, *Intent, error) {
		var target config.WorkflowStep
		for _, s := range steps {
			if s.ID == cursor.Step {
				target = s
				break
			}
		}
		if target.ID == "" {
			return Cursor{}, nil, ErrTransition
		}
		recipient := target.Execute.Agent
		if purpose == "review" {
			if w.Kind() == config.WorkflowApproval {
				recipient = target.Approver
			} else {
				recipient = target.Reviewer
			}
		}
		cursor.DispatchID = ""
		instruction := target.Instruction
		if w.Kind() == config.WorkflowPipeline {
			instruction = cursor.Task
		} else if strings.TrimSpace(startTask) != "" {
			instruction = startTask
		}
		return cursor, &Intent{Revision: cursor.Revision, Recipient: recipient, Purpose: purpose, Step: cursor.Step, Attempt: cursor.Attempt,
			Instruction: instruction, Acceptance: append([]string{}, target.Acceptance...), HandoffRequired: target.HandoffRequired,
			Handoff: cloneHandoff(cursor.Handoff), Reason: event.Reason}, nil
	}
	if w.Kind() == config.WorkflowPipeline {
		return advancePipeline(w, lead, current, event, steps, dispatch)
	}
	if current.Revision == 0 {
		if !zeroCursor(current) || event.Kind != "start" || event.Actor != lead || event.MessageID != "" {
			return Cursor{}, nil, ErrTransition
		}
		next.Step, next.Attempt, next.Phase = w.Entry, 1, Executing
		if w.Kind() == config.WorkflowApproval {
			next.Attempts = map[string]int{w.Entry: 1}
		}
		return dispatch(next, "execute", event.Task)
	}
	// A completed workflow is a reusable team, not a one-shot run. Preserve the
	// monotonic revision fence while starting the next user task from entry.
	if current.Phase == Completed {
		if event.Kind != "start" || event.Actor != lead || event.MessageID != "" {
			return Cursor{}, nil, ErrTransition
		}
		next.Step, next.Attempt, next.Phase, next.DispatchID, next.Handoff = w.Entry, 1, Executing, "", nil
		next.Attempts = nil
		if w.Kind() == config.WorkflowApproval {
			next.Attempts = map[string]int{w.Entry: 1}
		}
		return dispatch(next, "execute", event.Task)
	}
	if step.ID == "" || current.Attempt < 1 || current.Attempt > step.MaxAttempts ||
		(current.Phase != Executing && current.Phase != Reviewing && current.Phase != Paused) {
		return Cursor{}, nil, ErrTransition
	}
	if w.Kind() == config.WorkflowApproval && current.Attempts[current.Step] != current.Attempt {
		return Cursor{}, nil, ErrTransition
	}
	if event.Kind == "resume" {
		if current.Phase != Paused || event.Actor != lead || strings.TrimSpace(event.Reason) == "" {
			return Cursor{}, nil, ErrTransition
		}
	} else {
		expected := step.Execute.Agent
		if current.Phase == Reviewing {
			if w.Kind() == config.WorkflowApproval {
				expected = step.Approver
			} else {
				expected = step.Reviewer
			}
		}
		if current.Phase == Paused || event.Actor != expected || current.DispatchID == "" || event.MessageID != current.DispatchID {
			return Cursor{}, nil, ErrTransition
		}
	}
	switch event.Kind {
	case "handoff":
		if current.Phase != Executing || !validHandoff(event.Handoff) {
			return Cursor{}, nil, ErrTransition
		}
		next.Handoff, next.Phase = cloneHandoff(event.Handoff), Reviewing
		return dispatch(next, "review", "")
	case "approve":
		if current.Phase != Reviewing || !validHandoff(current.Handoff) || len(event.Accepted) != len(step.Acceptance) {
			return Cursor{}, nil, ErrTransition
		}
		for _, accepted := range event.Accepted {
			if !accepted {
				return Cursor{}, nil, ErrTransition
			}
		}
		next.Step, next.Handoff, next.DispatchID = step.Next, nil, ""
		if step.Next == w.Completion {
			next.Phase = Completed
			return next, nil, nil
		}
		next.Phase = Executing
		if w.Kind() == config.WorkflowApproval {
			var ok bool
			next, ok = enterApprovalStep(next, step.Next, steps)
			if !ok {
				return next, nil, nil
			}
		} else {
			next.Attempt = 1
		}
		return dispatch(next, "execute", "")
	case "blocked":
		if strings.TrimSpace(event.Reason) == "" {
			return Cursor{}, nil, ErrTransition
		}
		next.Phase = Paused
		return next, nil, nil
	case "changes":
		if strings.TrimSpace(event.Reason) == "" || current.Phase != Reviewing {
			return Cursor{}, nil, ErrTransition
		}
		if w.Kind() == config.WorkflowApproval {
			target := step.Transitions.Changes
			if target == "self" {
				target = step.ID
			}
			next.Step, next.Handoff, next.DispatchID, next.Phase = target, cloneHandoff(current.Handoff), "", Executing
			var ok bool
			next, ok = enterApprovalStep(next, target, steps)
			if !ok {
				return next, nil, nil
			}
			return dispatch(next, "execute", "")
		}
		return repeatStep(w, next, steps, dispatch)
	case "retry", "resume":
		if strings.TrimSpace(event.Reason) == "" {
			return Cursor{}, nil, ErrTransition
		}
		return repeatStep(w, next, steps, dispatch)
	default:
		return Cursor{}, nil, ErrTransition
	}
}

func advancePipeline(w *config.Workflow, lead string, current Cursor, event Event, steps []config.WorkflowStep,
	dispatch func(Cursor, string, string) (Cursor, *Intent, error)) (Cursor, *Intent, error) {
	next := current
	next.Handoff = cloneHandoff(current.Handoff)
	next.Attempts = cloneAttempts(current.Attempts)
	next.Revision++
	var step config.WorkflowStep
	for _, candidate := range steps {
		if candidate.ID == current.Step {
			step = candidate
			break
		}
	}
	if current.Revision == 0 || current.Phase == Completed {
		if (current.Revision == 0 && !zeroCursor(current)) || (current.Revision != 0 && current.Phase != Completed) || event.Kind != "start" || event.Actor != lead ||
			event.MessageID != "" || strings.TrimSpace(event.Task) == "" {
			return Cursor{}, nil, ErrTransition
		}
		steps = w.RuntimeSteps()
		if len(steps) == 0 {
			return Cursor{}, nil, ErrTransition
		}
		next.Step, next.Attempt, next.Phase, next.Task, next.DispatchID, next.Handoff = steps[0].ID, 1, Executing, event.Task, "", nil
		return dispatch(next, "execute", event.Task)
	}
	if step.ID == "" || current.Attempt < 1 || current.Attempt > step.MaxAttempts {
		return Cursor{}, nil, ErrTransition
	}
	if event.Kind == "resume" {
		if current.Phase != Paused || event.Actor != lead || strings.TrimSpace(event.Reason) == "" {
			return Cursor{}, nil, ErrTransition
		}
	} else if current.Phase != Executing || event.Actor != step.Execute.Agent || current.DispatchID == "" || event.MessageID != current.DispatchID {
		return Cursor{}, nil, ErrTransition
	}
	switch event.Kind {
	case "handoff":
		if !validHandoff(event.Handoff) {
			return Cursor{}, nil, ErrTransition
		}
		next.Handoff = cloneHandoff(event.Handoff)
		if step.Next == "done" {
			next.Step, next.Phase, next.DispatchID = "done", Completed, ""
			return next, nil, nil
		}
		next.Step, next.Attempt, next.Phase = step.Next, 1, Executing
		return dispatch(next, "execute", "")
	case "blocked":
		if strings.TrimSpace(event.Reason) == "" {
			return Cursor{}, nil, ErrTransition
		}
		next.Phase = Paused
		return next, nil, nil
	case "retry", "resume":
		if strings.TrimSpace(event.Reason) == "" {
			return Cursor{}, nil, ErrTransition
		}
		if current.Attempt == step.MaxAttempts {
			next.Phase = Exhausted
			return next, nil, nil
		}
		next.Attempt++
		next.Phase = Executing
		return dispatch(next, "execute", "")
	default:
		return Cursor{}, nil, ErrTransition
	}
}

func validHandoff(h *Handoff) bool {
	if h == nil || strings.TrimSpace(h.Summary) == "" || strings.TrimSpace(h.Next) == "" || len(h.Evidence) == 0 {
		return false
	}
	for _, ref := range h.Evidence {
		if strings.TrimSpace(ref) == "" {
			return false
		}
	}
	return true
}

func repeatStep(w *config.Workflow, next Cursor, steps []config.WorkflowStep,
	dispatch func(Cursor, string, string) (Cursor, *Intent, error)) (Cursor, *Intent, error) {
	if w.Kind() == config.WorkflowApproval {
		next.DispatchID, next.Phase = "", Executing
		var ok bool
		next, ok = enterApprovalStep(next, next.Step, steps)
		if !ok {
			return next, nil, nil
		}
		return dispatch(next, "execute", "")
	}
	var step config.WorkflowStep
	for _, candidate := range steps {
		if candidate.ID == next.Step {
			step = candidate
			break
		}
	}
	if step.ID == "" {
		return Cursor{}, nil, ErrTransition
	}
	next.Handoff, next.DispatchID = nil, ""
	if next.Attempt == step.MaxAttempts {
		next.Phase = Exhausted
		return next, nil, nil
	}
	next.Attempt++
	next.Phase = Executing
	return dispatch(next, "execute", "")
}

func enterApprovalStep(cursor Cursor, id string, steps []config.WorkflowStep) (Cursor, bool) {
	var target config.WorkflowStep
	for _, candidate := range steps {
		if candidate.ID == id {
			target = candidate
			break
		}
	}
	if target.ID == "" {
		return cursor, false
	}
	if cursor.Attempts == nil {
		cursor.Attempts = make(map[string]int)
	}
	cursor.Attempts[id]++
	cursor.Attempt = cursor.Attempts[id]
	if cursor.Attempt > target.MaxAttempts {
		cursor.Phase, cursor.DispatchID = Exhausted, ""
		return cursor, false
	}
	return cursor, true
}

func zeroCursor(cursor Cursor) bool {
	return cursor.Revision == 0 && cursor.Step == "" && cursor.Attempt == 0 && len(cursor.Attempts) == 0 &&
		cursor.Phase == "" && cursor.DispatchID == "" && cursor.Task == "" && cursor.Handoff == nil
}

func cloneAttempts(attempts map[string]int) map[string]int {
	if len(attempts) == 0 {
		return nil
	}
	copy := make(map[string]int, len(attempts))
	for step, count := range attempts {
		copy[step] = count
	}
	return copy
}

func cloneHandoff(h *Handoff) *Handoff {
	if h == nil {
		return nil
	}
	return &Handoff{Summary: h.Summary, Evidence: append([]string{}, h.Evidence...), Next: h.Next}
}
