package config

import (
	"fmt"
	"strings"
)

// Workflow freezes a build-review chain, an approval chain, or a role pipeline.
type Workflow struct {
	Type        string         `yaml:"type,omitempty" json:"type,omitempty"`
	Flow        string         `yaml:"flow,omitempty" json:"flow,omitempty"`
	MaxAttempts *int           `yaml:"max_attempts,omitempty" json:"max_attempts,omitempty"`
	Entry       string         `yaml:"entry,omitempty" json:"entry,omitempty"`
	Completion  string         `yaml:"completion,omitempty" json:"completion,omitempty"`
	Steps       []WorkflowStep `yaml:"steps,omitempty" json:"steps,omitempty"`
}

const (
	WorkflowBuildReview     = "build_review"
	WorkflowApproval        = "approval"
	WorkflowPipeline        = "pipeline"
	DefaultPipelineAttempts = 3
)

type WorkflowStep struct {
	ID              string              `yaml:"id" json:"id"`
	Execute         WorkflowExecute     `yaml:"execute" json:"execute"`
	Reviewer        string              `yaml:"reviewer,omitempty" json:"reviewer,omitempty"`
	Approver        string              `yaml:"approver,omitempty" json:"approver,omitempty"`
	Instruction     string              `yaml:"instruction" json:"instruction"`
	Acceptance      []string            `yaml:"acceptance" json:"acceptance"`
	HandoffRequired bool                `yaml:"handoff_required" json:"handoff_required"`
	MaxAttempts     int                 `yaml:"max_attempts" json:"max_attempts"`
	Transitions     WorkflowTransitions `yaml:"transitions" json:"transitions"`
	Next            string              `yaml:"next" json:"next"`
}

type WorkflowExecute struct {
	Agent string `yaml:"agent" json:"agent"`
}

type WorkflowTransitions struct {
	Approve string `yaml:"approve" json:"approve"`
	Changes string `yaml:"changes" json:"changes"`
	Retry   string `yaml:"retry" json:"retry"`
	Blocked string `yaml:"blocked" json:"blocked"`
}

// ValidateWorkflow is shared by source validation, layout/startup compilation
// and runtime snapshot validation. A nil workflow preserves legacy configs.
func ValidateWorkflow(w *Workflow, agentIDs []string, lead string) error {
	if w == nil {
		return nil
	}
	typ := w.Kind()
	if typ != WorkflowBuildReview && typ != WorkflowApproval && typ != WorkflowPipeline {
		return invalid("workflow.type", "must be build_review, approval or pipeline")
	}
	known := make(map[string]bool, len(agentIDs))
	for _, id := range agentIDs {
		known[id] = true
	}
	if typ == WorkflowPipeline {
		if strings.TrimSpace(w.Entry) != "" || strings.TrimSpace(w.Completion) != "" || len(w.Steps) != 0 {
			return invalid("workflow", "pipeline uses flow and cannot also define entry, completion or steps")
		}
		flow := w.FlowAgents()
		if len(flow) < 3 {
			return invalid("workflow.flow", "must route lead -> at least one agent -> lead")
		}
		if lead == "" || flow[0] != lead || flow[len(flow)-1] != lead {
			return invalid("workflow.flow", "must start and end with the configured lead "+lead)
		}
		for i, id := range flow {
			if id == "" || !known[id] {
				return invalid("workflow.flow", "references unknown or empty agent "+id)
			}
			if i > 0 && i < len(flow)-1 && id == lead {
				return invalid("workflow.flow", "lead may appear only at the start and end")
			}
		}
		if w.MaxAttempts != nil && (*w.MaxAttempts < 1 || *w.MaxAttempts > 10) {
			return invalid("workflow.max_attempts", "must be between 1 and 10 when set")
		}
		return nil
	}
	if w.Flow != "" || w.MaxAttempts != nil {
		return invalid("workflow", "build_review and approval use entry, completion and steps; flow/max_attempts are pipeline-only")
	}
	text := func(path, value string) error {
		if strings.TrimSpace(value) == "" || strings.ContainsRune(value, '\x00') {
			return invalid(path, "must be non-empty and contain no NUL bytes")
		}
		return nil
	}
	for _, field := range []struct{ path, value string }{
		{"workflow.entry", w.Entry}, {"workflow.completion", w.Completion},
	} {
		if err := text(field.path, field.value); err != nil {
			return err
		}
	}
	if len(w.Steps) == 0 {
		return invalid("workflow.steps", "must contain at least one step")
	}
	steps := make(map[string]int, len(w.Steps))
	for i, step := range w.Steps {
		path := fmt.Sprintf("workflow.steps[%d]", i)
		roleName, roleID := "reviewer", step.Reviewer
		if typ == WorkflowApproval {
			roleName, roleID = "approver", step.Approver
		}
		for _, field := range []struct{ name, value string }{
			{"id", step.ID}, {"execute.agent", step.Execute.Agent},
			{roleName, roleID}, {"instruction", step.Instruction}, {"next", step.Next},
		} {
			if err := text(path+"."+field.name, field.value); err != nil {
				return err
			}
		}
		if _, exists := steps[step.ID]; exists {
			return invalid(path+".id", "duplicate step ID "+step.ID)
		}
		if step.ID == w.Completion {
			return invalid(path+".id", "completion must be distinct from step IDs")
		}
		steps[step.ID] = i
		if !known[step.Execute.Agent] {
			return invalid(path+".execute.agent", "references unknown agent "+step.Execute.Agent)
		}
		if typ == WorkflowApproval {
			if step.Reviewer != "" {
				return invalid(path+".reviewer", "is only valid for build_review")
			}
			if !known[step.Approver] {
				return invalid(path+".approver", "references unknown agent "+step.Approver)
			}
			if step.Execute.Agent == step.Approver {
				return invalid(path+".approver", "must differ from execute.agent")
			}
		} else {
			if step.Approver != "" {
				return invalid(path+".approver", "is only valid for approval")
			}
			if !known[step.Reviewer] {
				return invalid(path+".reviewer", "references unknown agent "+step.Reviewer)
			}
			if step.Execute.Agent == step.Reviewer {
				return invalid(path+".reviewer", "must differ from execute.agent")
			}
		}
		if len(step.Acceptance) == 0 {
			return invalid(path+".acceptance", "must contain at least one criterion")
		}
		for j, criterion := range step.Acceptance {
			if err := text(fmt.Sprintf("%s.acceptance[%d]", path, j), criterion); err != nil {
				return err
			}
		}
		if !step.HandoffRequired {
			return invalid(path+".handoff_required", "must be true")
		}
		if step.MaxAttempts < 1 || step.MaxAttempts > 10 {
			return invalid(path+".max_attempts", "must be between 1 and 10 inclusive")
		}
		changes := step.Transitions.Changes
		for _, transition := range []struct{ name, value, want string }{
			{"approve", step.Transitions.Approve, "next"},
			{"retry", step.Transitions.Retry, "self"}, {"blocked", step.Transitions.Blocked, "pause"},
		} {
			if transition.value != transition.want {
				return invalid(path+".transitions."+transition.name, "must be "+transition.want)
			}
		}
		if typ == WorkflowBuildReview && changes != "self" {
			return invalid(path+".transitions.changes", "must be self for build_review")
		}
	}
	if _, exists := steps[w.Entry]; !exists {
		return invalid("workflow.entry", "must reference an existing step")
	}
	terminals := 0
	for i, step := range w.Steps {
		if step.Next == w.Completion {
			terminals++
		} else if _, exists := steps[step.Next]; !exists {
			return invalid(fmt.Sprintf("workflow.steps[%d].next", i), "references unknown target "+step.Next)
		}
	}
	if terminals != 1 {
		return invalid("workflow.steps", "must have exactly one next edge to completion")
	}
	visited := make(map[string]bool, len(steps))
	for id := w.Entry; id != w.Completion; id = w.Steps[steps[id]].Next {
		if visited[id] {
			return invalid("workflow.steps", "next chain contains a forward cycle")
		}
		visited[id] = true
	}
	if len(visited) != len(steps) {
		return invalid("workflow.steps", "entry next chain must cover every step; disconnected or isolated steps")
	}
	if typ == WorkflowApproval {
		position := make(map[string]int, len(steps))
		for i, id := 0, w.Entry; id != w.Completion; i, id = i+1, w.Steps[steps[id]].Next {
			position[id] = i
		}
		for i, step := range w.Steps {
			target := step.Transitions.Changes
			if target == "self" {
				continue
			}
			targetPosition, exists := position[target]
			if !exists || targetPosition >= position[step.ID] {
				return invalid(fmt.Sprintf("workflow.steps[%d].transitions.changes", i), "must be self or an earlier step")
			}
		}
	}
	return nil
}

// Kind keeps existing workflow files on the original build-review behavior.
func (w *Workflow) Kind() string {
	if w == nil || w.Type == "" {
		return WorkflowBuildReview
	}
	return w.Type
}

// FlowAgents parses the deliberately small route syntax: "lead -> worker -> lead".
func (w *Workflow) FlowAgents() []string {
	if w == nil {
		return nil
	}
	parts := strings.Split(w.Flow, "->")
	ids := make([]string, len(parts))
	for i, part := range parts {
		ids[i] = strings.TrimSpace(part)
	}
	return ids
}

func (w *Workflow) PipelineAttempts() int {
	if w == nil || w.MaxAttempts == nil {
		return DefaultPipelineAttempts
	}
	return *w.MaxAttempts
}

// RuntimeSteps turns each interior flow role into one serial execution stage.
func (w *Workflow) RuntimeSteps() []WorkflowStep {
	if w == nil || w.Kind() != WorkflowPipeline {
		if w == nil {
			return nil
		}
		return w.Steps
	}
	flow := w.FlowAgents()
	if len(flow) < 3 {
		return nil
	}
	steps := make([]WorkflowStep, 0, len(flow)-2)
	for i, agent := range flow[1 : len(flow)-1] {
		id := fmt.Sprintf("stage-%d", i+1)
		next := fmt.Sprintf("stage-%d", i+2)
		if i == len(flow)-3 {
			next = "done"
		}
		steps = append(steps, WorkflowStep{ID: id, Execute: WorkflowExecute{Agent: agent}, HandoffRequired: true, MaxAttempts: w.PipelineAttempts(), Next: next})
	}
	return steps
}

// ParticipantIDs returns the seats that receive work in this workflow.
func (w *Workflow) ParticipantIDs() []string {
	if w == nil {
		return nil
	}
	seen := map[string]bool{}
	var ids []string
	add := func(id string) {
		if id != "" && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	for _, step := range w.RuntimeSteps() {
		add(step.Execute.Agent)
		if w.Kind() == WorkflowApproval {
			add(step.Approver)
		} else {
			add(step.Reviewer)
		}
	}
	return ids
}

// cloneWorkflow follows a previously validated chain and detaches all slices.
// Declaration order is not semantic and therefore does not affect the digest.
func cloneWorkflow(w *Workflow) *Workflow {
	if w == nil {
		return nil
	}
	var maxAttempts *int
	if w.Kind() == WorkflowPipeline {
		attempts := w.PipelineAttempts()
		maxAttempts = &attempts
	} else if w.MaxAttempts != nil {
		attempts := *w.MaxAttempts
		maxAttempts = &attempts
	}
	result := &Workflow{Type: w.Type, Flow: strings.Join(w.FlowAgents(), " -> "), MaxAttempts: maxAttempts,
		Entry: w.Entry, Completion: w.Completion}
	if w.Kind() == WorkflowPipeline {
		return result
	}
	result.Steps = make([]WorkflowStep, 0, len(w.Steps))
	steps := make(map[string]WorkflowStep, len(w.Steps))
	for _, step := range w.Steps {
		steps[step.ID] = step
	}
	for id := w.Entry; id != w.Completion; {
		step := steps[id]
		step.Acceptance = append([]string{}, step.Acceptance...)
		result.Steps = append(result.Steps, step)
		id = step.Next
	}
	return result
}
