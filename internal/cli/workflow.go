package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"agentflow/internal/coordinator"
	"agentflow/internal/ipc"
	"agentflow/internal/runtime"
)

func runWorkflow(args []string, stdout, stderr io.Writer) (exit int) {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "agentflow: error: workflow requires start, resume, drain, status or decide")
		return 2
	}
	kind := args[0]
	f := flag.NewFlagSet("workflow", flag.ContinueOnError)
	f.SetOutput(stderr)
	run := f.String("run", "", "run ID")
	dir := f.String("run-dir", "", "existing private run directory; use up output run_dir for temporary runs (default: legacy XDG lookup)")
	agent := f.String("agent", "", "current lead ID")
	generation := f.Int64("generation", 0, "current lead generation")
	revision := f.Int64("revision", -1, "expected workflow revision (start=0)")
	id := f.String("request-id", "", "stable event request ID")
	reason := f.String("reason", "", "resume decision")
	messageID := f.String("message-id", "", "dispatch ID for decide")
	eventKind := f.String("kind", "", "approval decision: approve or changes")
	acceptedText := f.String("accepted", "", "comma-separated true/false acceptance judgments")
	task := f.String("task", "", "initial user task for start")
	deadline := f.Duration("timeout", 30*time.Minute, "overall workflow wait deadline")
	asJSON := f.Bool("json", false, "output JSON")
	cmux := f.Bool("cmux-transport", false, "own a foreground cmux coordinator for this command; otherwise use existing IPC")
	cmuxBinary := f.String("cmux-binary", "", "absolute cmux executable (requires --cmux-transport)")
	if err := f.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if kind == "status" && *revision == -1 {
		*revision = 0
	}
	decide := kind == "decide"
	if (kind != "start" && kind != "resume" && kind != "drain" && kind != "status" && !decide) || !entryRunID(*run) || *agent == "" || *generation < 1 || (*revision < 0 && !(kind == "start" && *revision == -1)) || *id == "" || f.NArg() != 0 || *deadline <= 0 || (*cmuxBinary != "" && !*cmux) ||
		(decide && (*messageID == "" || (*eventKind != "approve" && *eventKind != "changes") || *cmux)) {
		fmt.Fprintln(stderr, "agentflow: error: workflow requires valid kind, run, agent, generation, revision, request-id and timeout")
		return 2
	}
	resolvedDir, err := resolveRunDir(*dir, *run)
	if err != nil {
		fmt.Fprintln(stderr, "agentflow: error:", err)
		return 1
	}
	if kind != "start" && strings.TrimSpace(*task) != "" {
		fmt.Fprintln(stderr, "agentflow: error: --task is only valid for workflow start")
		return 2
	}
	ctx, cancel := context.WithTimeout(context.Background(), *deadline)
	defer cancel()
	if *cmux {
		if *cmuxBinary == "" {
			*cmuxBinary, err = runtime.NativeExecutable("cmux")
		}
		var owner *coordinator.Coordinator
		if err == nil {
			owner, err = coordinator.OpenCMUXTransport(ctx, resolvedDir, *run, *cmuxBinary)
		}
		if err != nil {
			fmt.Fprintln(stderr, "agentflow: error: foreground cmux workflow:", err)
			return 1
		}
		serveCtx, stop := context.WithCancel(ctx)
		done := make(chan error, 1)
		go func() { done <- owner.Serve(serveCtx) }()
		defer func() {
			stop()
			if err := <-done; err != nil {
				fmt.Fprintln(stderr, "agentflow: error: cmux coordinator shutdown:", err)
				exit = 1
			}
		}()
	}
	if kind == "start" && *revision == -1 {
		statusParams, _ := json.Marshal(coordinator.Params{Workflow: &coordinator.WorkflowParams{Revision: 0, Kind: "status"}})
		status, statusErr := coordinator.Call(ctx, resolvedDir, ipc.Request{V: ipc.Version, RequestID: *id + ":status",
			Actor: ipc.Actor{RunID: *run, AgentID: *agent, Generation: *generation}, Method: "workflow", Params: statusParams})
		if statusErr != nil {
			fmt.Fprintln(stderr, "agentflow: error: cannot read current workflow revision:", statusErr)
			return 1
		}
		var current coordinator.Result
		if json.Unmarshal(status.Result, &current) != nil || current.WorkflowCurrent == nil {
			fmt.Fprintln(stderr, "agentflow: error: invalid workflow status response")
			return 1
		}
		*revision = current.WorkflowCurrent.Revision
	}
	actor := ipc.Actor{RunID: *run, AgentID: *agent, Generation: *generation}
	method := "workflow"
	params := coordinator.Params{Workflow: &coordinator.WorkflowParams{Revision: *revision, Kind: kind, Task: *task, Reason: *reason}}
	if decide {
		if *eventKind == "changes" && strings.TrimSpace(*reason) == "" {
			fmt.Fprintln(stderr, "agentflow: error: workflow decide changes requires --reason")
			return 2
		}
		var accepted []bool
		if strings.TrimSpace(*acceptedText) != "" {
			for _, value := range strings.Split(*acceptedText, ",") {
				judgment, parseErr := strconv.ParseBool(strings.TrimSpace(value))
				if parseErr != nil {
					fmt.Fprintln(stderr, "agentflow: error: --accepted must be comma-separated booleans")
					return 2
				}
				accepted = append(accepted, judgment)
			}
		}
		allAccepted := len(accepted) > 0
		for _, judgment := range accepted {
			allAccepted = allAccepted && judgment
		}
		if *eventKind == "approve" && !allAccepted {
			fmt.Fprintln(stderr, "agentflow: error: approve requires all acceptance judgments to be true")
			return 2
		}
		method = "reply"
		body := "Lead approved the current workflow step."
		if *eventKind == "changes" {
			body = *reason
		}
		params = coordinator.Params{MessageID: *messageID, Body: body, Status: "DONE", Workflow: &coordinator.WorkflowParams{Revision: *revision, Kind: *eventKind, Accepted: accepted, Reason: *reason}}
	}
	p, _ := json.Marshal(params)
	response, err := coordinator.Call(ctx, resolvedDir, ipc.Request{V: ipc.Version, RequestID: *id,
		Actor: actor, Method: method, Params: p})
	if err != nil {
		fmt.Fprintln(stderr, "agentflow: error: workflow IPC outcome may be unknown:", err)
		return 1
	}
	var result coordinator.Result
	if err := json.Unmarshal(response.Result, &result); err != nil {
		fmt.Fprintln(stderr, "agentflow: error:", err)
		return 1
	}
	if kind == "drain" {
		poll := 1
		expectedRevision := *revision
		for result.Status != "FAILED" && len(result.Diagnostics) == 0 && result.WorkflowCurrent != nil {
			if result.AwaitingActor {
				break
			}
			phase := result.WorkflowCurrent.Phase
			if phase == "COMPLETED" || phase == "PAUSED" || phase == "EXHAUSTED" {
				break
			}
			if result.WorkflowCurrent.Revision == expectedRevision {
				timer := time.NewTimer(time.Second)
				select {
				case <-ctx.Done():
					timer.Stop()
					result.Diagnostics = append(result.Diagnostics, runtime.Diagnostic{Code: "WORKFLOW_WAIT_TIMEOUT", Path: "workflow", Message: "workflow did not reach a terminal phase before --timeout; inspect status and continue with the latest revision"})
					goto output
				case <-timer.C:
				}
			} else {
				expectedRevision = result.WorkflowCurrent.Revision
			}
			params, _ := json.Marshal(coordinator.Params{Workflow: &coordinator.WorkflowParams{Revision: result.WorkflowCurrent.Revision, Kind: "drain"}})
			response, err := coordinator.Call(ctx, resolvedDir, ipc.Request{V: ipc.Version, RequestID: fmt.Sprintf("%s-poll-%d", *id, poll),
				Actor: ipc.Actor{RunID: *run, AgentID: *agent, Generation: *generation}, Method: "workflow", Params: params})
			poll++
			if err != nil {
				result.Diagnostics = append(result.Diagnostics, runtime.Diagnostic{Code: "WORKFLOW_OUTCOME_UNKNOWN", Path: "workflow", Message: "workflow drain response was not received; inspect status before continuing"})
				goto output
			}
			var next coordinator.Result
			if err := json.Unmarshal(response.Result, &next); err != nil {
				fmt.Fprintln(stderr, "agentflow: error: invalid workflow drain response:", err)
				return 1
			}
			result = next
		}
		if result.Status != "FAILED" && result.WorkflowCurrent == nil && len(result.Diagnostics) == 0 {
			result.Diagnostics = append(result.Diagnostics, runtime.Diagnostic{Code: "WORKFLOW_INVALID_RESPONSE", Path: "workflow", Message: "drain response did not include the current workflow cursor"})
		}
	}

output:
	if *asJSON {
		if code := writeTeamJSON(stdout, stderr, result); code != 0 {
			return code
		}
	} else {
		fmt.Fprintln(stdout, "Status:", result.Status)
		if result.WorkflowCurrent != nil {
			fmt.Fprintf(stdout, "Workflow: %s revision=%d dispatch=%s\n", result.WorkflowCurrent.Phase, result.WorkflowCurrent.Revision, result.WorkflowCurrent.DispatchID)
			if result.AwaitingActor {
				fmt.Fprintln(stdout, "Workflow is waiting for this lead seat to approve or return the dispatched step.")
			}
		} else if result.Workflow != nil {
			fmt.Fprintf(stdout, "Receipt: %s revision=%d\n", result.Workflow.Cursor.Phase, result.Workflow.Cursor.Revision)
		}
	}
	for _, d := range result.Diagnostics {
		fmt.Fprintf(stderr, "agentflow: error: %s: %s\n", d.Code, d.Message)
	}
	if result.Status == "FAILED" || len(result.Diagnostics) != 0 {
		return 1
	}
	return 0
}
