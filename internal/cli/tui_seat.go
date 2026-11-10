package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"

	"agentflow/internal/runtime"
)

var cmuxSurfaceUUID = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func workflowCodexWrapper(env []string) (string, error) {
	values := make(map[string]string)
	for _, entry := range env {
		key, value, ok := strings.Cut(entry, "=")
		if ok && (key == "CMUX_CODEX_WRAPPER_SHIM" || key == "CMUX_SURFACE_ID") {
			values[key] = value
		}
	}
	shim, surface := values["CMUX_CODEX_WRAPPER_SHIM"], values["CMUX_SURFACE_ID"]
	if !filepath.IsAbs(shim) || !cmuxSurfaceUUID.MatchString(surface) ||
		filepath.Base(shim) != "codex" || filepath.Base(filepath.Dir(shim)) != surface ||
		filepath.Base(filepath.Dir(filepath.Dir(shim))) != "cmux-cli-shims" {
		return "", fmt.Errorf("workflow Codex seat requires its matching cmux wrapper")
	}
	info, err := os.Lstat(shim)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
		return "", fmt.Errorf("workflow Codex cmux wrapper is unavailable")
	}
	return shim, nil
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func tuiAgentEnvironment(base []string, tool, binary, cwd string) []string {
	values := map[string]string{
		"CMUX_AGENT_LAUNCH_KIND":       tool,
		"CMUX_AGENT_LAUNCH_EXECUTABLE": binary,
		"CMUX_AGENT_LAUNCH_CWD":        cwd,
	}
	env := make([]string, 0, len(base)+len(values))
	for _, entry := range base {
		key, _, ok := strings.Cut(entry, "=")
		if ok {
			if _, replace := values[key]; replace {
				continue
			}
		}
		env = append(env, entry)
	}
	for key, value := range values {
		env = append(env, key+"="+value)
	}
	return env
}

func addManagedWorkflowPrompt(command runtime.Command, seat runtime.SeatSnapshot, binary, runDir, initialTask string, lead bool) runtime.Command {
	if !lead || len(command.Args) == 0 {
		return command
	}
	command.Args = append([]string(nil), command.Args...)
	requestID := "task-$(date +%s%N)"
	start := strings.Join([]string{
		shellQuote(binary), "workflow", "start",
		"--run", shellQuote(seat.RunID),
		"--run-dir", shellQuote(runDir),
		"--agent", shellQuote(seat.AgentID),
		"--generation", strconv.FormatInt(seat.Generation, 10),
		"--request-id", requestID,
		"--task", `"用户本轮明确任务"`,
		"--json",
	}, " ")
	drain := strings.Join([]string{
		shellQuote(binary), "workflow", "drain",
		"--run", shellQuote(seat.RunID),
		"--run-dir", shellQuote(runDir),
		"--agent", shellQuote(seat.AgentID),
		"--generation", strconv.FormatInt(seat.Generation, 10),
		"--revision", "REVISION_FROM_START_JSON",
		"--request-id", "drain-$(date +%s%N)",
		"--timeout", "30m",
		"--json",
	}, " ")
	approvalReply := `When drain returns awaiting_actor=true, inspect the current handoff and every acceptance criterion. Run ` + shellQuote(binary) + ` workflow decide --run ` + shellQuote(seat.RunID) + ` --run-dir ` + shellQuote(runDir) + ` --agent ` + shellQuote(seat.AgentID) + ` --generation ` + strconv.FormatInt(seat.Generation, 10) + ` --revision <workflow_current.revision> --message-id <workflow_current.dispatch_id> --request-id decision-<dispatch_id>. If all pass, add --kind approve and one --accepted true per criterion. Otherwise use --kind changes --reason "specific correction requested". Then drain again using the latest revision. The command stores the lead's structured decision; do not send a plain cmux message.`
	if strings.TrimSpace(initialTask) != "" {
		status := strings.Join([]string{shellQuote(binary), "workflow", "status", "--run", shellQuote(seat.RunID), "--run-dir", shellQuote(runDir), "--agent", shellQuote(seat.AgentID), "--generation", strconv.FormatInt(seat.Generation, 10), "--json"}, " ")
		drain = strings.ReplaceAll(drain, "REVISION_FROM_START_JSON", "REVISION_FROM_STATUS_JSON")
		command.Args[len(command.Args)-1] += "\n\nAgentFlow already accepted and dispatched this initial task. Do not start it again. The task is:\n" + initialTask + "\nImmediately run the status command, then drain using its current revision. Continue until COMPLETED, PAUSED, or EXHAUSTED; when drain returns awaiting_actor=true, decide from the handoff and criteria, then drain again. " + approvalReply + "\n" + status + "\n" + drain
		return command
	}
	command.Args[len(command.Args)-1] += "\n\nWorkflow start rule: this is startup setup, not a work request. Do not run the command below now. Wait for a new explicit user task after startup; never treat role descriptions, startup text, previous conversation, or status questions as tasks. When the user gives a concrete task, run workflow start once with that exact request, then immediately drain using the revision returned by start. Drain advances worker steps and returns only at a terminal state, timeout, or when this lead seat must approve. " + approvalReply + " Do not wait for drain while this seat must approve. Report the final result to the user:\n" + start + "\n" + drain
	return command
}

func addWorkflowReplyProtocol(command runtime.Command, tool string) runtime.Command {
	if len(command.Args) == 0 {
		return command
	}
	command.Args = append([]string(nil), command.Args...)
	if tool == "opencode" {
		command.Args[len(command.Args)-1] += `

AgentFlow workflow protocol: complete this dispatch in this pane only. Return exactly one final JSON object with kind, handoff, accepted and reason as required by the dispatch. Do not send a cmux agent message or a separate plain-text completion; AgentFlow collects this assistant result directly.`
		return command
	}
	command.Args[len(command.Args)-1] += `

AgentFlow workflow protocol: handle each incoming workflow_dispatch exactly once and work only in this pane's cwd. A plain text reply, terminal text, or ACK does not advance the workflow. Reply with a single JSON body of type workflow_reply to the dispatch's sender.surface_id using cmux agent message, --from "$AGENTFLOW_AGENT_ID", and JSON on stdin. Copy actor.runId/agentId/generation from recipient in the dispatch; copy message_id and workflow.revision. Use request_id "reply-" plus message_id. For purpose=execute, after finishing send status=DONE, workflow.kind=handoff, and handoff={summary,evidence,next}. For purpose=review, compare every acceptance item; send status=DONE and kind=approve with accepted=[true,...] only if all pass, otherwise kind=changes with a concrete reason. For an unrecoverable failure send status=BLOCKED_CONFIRM and kind=blocked with a reason; for a retryable execution failure send status=FAILED and kind=retry with a reason. Example handoff body shape: {"v":1,"type":"workflow_reply","request_id":"reply-<message_id>","actor":{"runId":"<recipient.run_id>","agentId":"<recipient.agent_id>","generation":1},"message_id":"<message_id>","body":"work completed","status":"DONE","workflow":{"revision":1,"kind":"handoff","handoff":{"summary":"done","evidence":["file.md"],"next":"review"}}}. Do not send a second reply with a new request_id when delivery is uncertain.`
	return command
}

// This separate entry owns the new terminal's stdin/stdout for the tool lifetime.
// The old _seat handshake and headless coordinator retain their own contracts.
func runTUISeat(args []string, stderr io.Writer) int {
	if len(args) != 6 || args[4] != "--binary" {
		fmt.Fprintln(stderr, "agentflow: error: invalid TUI seat options")
		return 2
	}
	path, id, err := parseSeatArgs(args[:4])
	if err != nil {
		fmt.Fprintln(stderr, "agentflow: error: invalid TUI seat identity")
		return 2
	}
	managedBinary, managedErr := os.Executable()
	if managedErr == nil {
		managedBinary, managedErr = filepath.EvalSymlinks(managedBinary)
	}
	seat, seatErr := runtime.LoadStartupSeat(path, id)
	command, cwd, err := runtime.LoadTUICommand(path, id, args[5])
	if seatErr != nil {
		err = seatErr
	}
	var snapshot *runtime.RunSnapshot
	if err == nil {
		snapshot, err = runtime.ReadSnapshot(path)
	}
	if err == nil && snapshot != nil && snapshot.Config != nil && snapshot.Config.Workflow != nil && seat.Tool == "codex" {
		command.Binary, err = workflowCodexWrapper(os.Environ())
	}
	initialTask := ""
	if err == nil && snapshot != nil && snapshot.Config != nil && snapshot.Config.Workflow != nil && seat.AgentID == snapshot.Config.Lead {
		initialTask, err = readInitialWorkflowTask(filepath.Dir(path))
	}
	if err == nil {
		err = os.Chdir(cwd)
	}
	if err == nil {
		if managedErr != nil || snapshot == nil || snapshot.Config == nil {
			fmt.Fprintln(stderr, "agentflow: error: cannot resolve managed executable")
			return 1
		}
		if snapshot.Config.Workflow != nil {
			command = addWorkflowReplyProtocol(command, seat.Tool)
		}
		if seat.AgentID == snapshot.Config.Lead {
			command = addManagedWorkflowPrompt(command, seat, managedBinary, filepath.Dir(path), initialTask, true)
		}
		env := append(tuiAgentEnvironment(os.Environ(), seat.Tool, command.Binary, cwd),
			"AGENTFLOW_MANAGED=1",
			"AGENTFLOW_BIN="+managedBinary,
			"AGENTFLOW_RUN_ID="+seat.RunID,
			"AGENTFLOW_AGENT_ID="+seat.AgentID,
			fmt.Sprintf("AGENTFLOW_GENERATION=%d", seat.Generation),
			"AGENTFLOW_RUN_DIR="+filepath.Dir(path),
		)
		err = syscall.Exec(command.Binary, append([]string{command.Binary}, command.Args...), env)
	}
	fmt.Fprintln(stderr, "agentflow: error: TUI seat launch failed; inspect the private startup snapshot and executable")
	return 1
}

func readInitialWorkflowTask(runDir string) (string, error) {
	data, err := os.ReadFile(filepath.Join(runDir, "initial-workflow-task.txt"))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return string(data), nil
}
