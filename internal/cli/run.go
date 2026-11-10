// Package cli provides the command entry point and consistent exit codes.
package cli

import (
	"fmt"
	"io"
)

const help = `AgentFlow — team configuration and capability probes

Usage:
  agentflow [--help | --version]
  agentflow probe --tool <all|opencode|codex|claude|cmux> --evidence-dir <new-dir>
  agentflow validate -f <config> [--tool <opencode|codex|claude>]
  agentflow plan -f <config> [--tool <opencode|codex|claude>] [--json]
  agentflow doctor -f <config> [--tool <opencode|codex|claude>] [--json]
  agentflow up -f <config> --dry-run [--tool <opencode|codex|claude>] [--json]
  agentflow up -f <config> --run <id> [--task <text>] [--window <ref>] [--run-dir <dir>] [--json]
  agentflow coordinator --run <run-id> [--run-dir <dir>] [--json]
  agentflow up -f <codex-config> --headless-codex --run <id> [--run-dir <dir>] [--json]
  agentflow up -f <opencode-config> --headless-opencode --run <id> [--run-dir <dir>] [--json]
  agentflow up -f <claude-config> --headless-claude --run <id> [--run-dir <dir>] [--json]
  agentflow workflow start|resume|drain|status|decide --run <id> --agent <lead> --generation <n> [--revision <n>] --request-id <id> [--cmux-transport] [--json]
  agentflow workflow decide --message-id <dispatch-id> --kind <approve|changes> [--accepted true,...] [--reason <text>]
  agentflow status --run <run-id> [--run-dir <dir>] [--json]
  agentflow attach --run <run-id> [--run-dir <dir>] [--json]
  agentflow stop --run <run-id> --agent <id> --generation <n> --request-id <id> [--run-dir <dir>] [--json]
  agentflow assign|send <recipient> --run <id> --agent <id> --generation <n> --request-id <id> --body-file <file> [--json]
  agentflow inbox --run <id> --agent <id> --generation <n> [--pending] [--after <seq>] [--json]
  agentflow coordinator --run <id> --cmux-transport [--cmux-binary <path>] [--json]
  agentflow cmux-send <recipient> --run <id> --agent <id> --generation <n> --request-id <id> --body-file <file> [--json]
  agentflow cmux-inbox --run <id> --agent <id> --generation <n> [--json]
  agentflow ack <message-id> --run <id> --agent <id> --generation <n> --request-id <id> [--json]
  agentflow reply <message-id> --run <id> --agent <id> --generation <n> --request-id <id> --body-file <file> [--status DONE] [--json]
  agentflow reconcile|resume <message-id> --run <id> --agent <id> --generation <n> [--json]
  agentflow rebuild|bootstrap --run <id> --agent <id> --generation <n> [--run-dir <dir>] [--json]

Options:
  -h, --help     Show this help
      --version  Show the build version

Probe outputs JSON and runs only bounded version/help and read-only cmux checks.
Exit 0 proves baseline checks only; runtime communication remains UNVERIFIED.
Validate and plan work offline. Doctor only looks up required tool executables;
it does not launch tools or verify runtime communication. Use <command> --help.
Up --dry-run outputs a startup plan only. Default real up launches a new cmux
workspace/layout with interactive tools (LAUNCHED, runtime communication UNVERIFIED).
Surface registration is persisted; native READY and automatic TUI workflow remain UNVERIFIED.
Explicit cmux transport connects workflow start/resume/drain to agent message/inbox.
Use coordinator --cmux-transport for IPC, or workflow --cmux-transport for one foreground command.
Status reads persisted state. Attach queries coordinates (UNVERIFIED).
Stop records an intent only; process/workspace shutdown is not executed.
Message commands persist/query/confirm receipts only; delivery channel is UNVERIFIED.
Coordinator serves these operations over local IPC under one run lock (PARTIAL).
Explicit --headless-codex, --headless-opencode or --headless-claude enables persistent logical roles and structured workflow results.
Reconcile/resume persist recovery decisions only; no native recovery is executed.
Rebuild/bootstrap generates/reads file continuation packages only; native sessions remain UNVERIFIED.
`

// Run returns 0 on success, 1 on validation/diagnostic/output failure, and 2 on invalid usage.
// Successful output goes to stdout; diagnostics use a stable stderr prefix.
func Run(args []string, stdout, stderr io.Writer, version string) int {
	if len(args) > 0 && args[0] == "workflow" {
		return runWorkflow(args[1:], stdout, stderr)
	}
	if len(args) > 0 && args[0] == "coordinator" {
		return runCoordinator(args[1:], stdout, stderr)
	}
	if len(args) > 0 && (args[0] == "rebuild" || args[0] == "bootstrap") {
		return runBootstrap(args[0], args[1:], stdout, stderr)
	}
	if len(args) >= 2 && args[0] == "message" && args[1] == "ack" {
		return runMessages("ack", args[2:], stdout, stderr)
	}
	if len(args) > 0 {
		switch args[0] {
		case "send", "assign", "inbox", "ack", "reply", "reconcile", "resume", "cmux-send", "cmux-inbox":
			return runMessages(args[0], args[1:], stdout, stderr)
		}
	}
	if len(args) > 0 && args[0] == "_seat" {
		return runSeat(args[1:], stdout, stderr)
	}
	if len(args) > 0 && args[0] == "_tui-seat" {
		return runTUISeat(args[1:], stderr)
	}
	if len(args) > 0 && (args[0] == "status" || args[0] == "attach" || args[0] == "stop") {
		return runControl(args[0], args[1:], stdout, stderr)
	}
	if len(args) > 0 && args[0] == "up" {
		return runUp(args[1:], stdout, stderr)
	}
	if len(args) > 0 && args[0] == "probe" {
		return runProbe(args[1:], stdout, stderr)
	}
	if len(args) > 0 && (args[0] == "validate" || args[0] == "plan" || args[0] == "doctor") {
		return runTeamCommand(args[0], args[1:], stdout, stderr)
	}
	var output string
	switch {
	case len(args) == 0:
		output = help
	case len(args) == 1 && (args[0] == "--help" || args[0] == "-h"):
		output = help
	case len(args) == 1 && args[0] == "--version":
		output = "agentflow " + version + "\n"
	default:
		fmt.Fprintf(stderr, "agentflow: error: unsupported arguments %q\nTry 'agentflow --help'.\n", args)
		return 2
	}
	if _, err := io.WriteString(stdout, output); err != nil {
		fmt.Fprintf(stderr, "agentflow: error: write output: %v\n", err)
		return 1
	}
	return 0
}
