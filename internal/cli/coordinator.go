package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"agentflow/internal/coordinator"
	"agentflow/internal/ipc"
	"agentflow/internal/lock"
	"agentflow/internal/runtime"
)

func resolveRunDir(dir, runID string) (string, error) {
	// Legacy deterministic lookup only; never allocate a temporary directory to
	// reopen a run. Runs created by default up require its output --run-dir.
	if dir == "" {
		root := os.Getenv("XDG_STATE_HOME")
		if root == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return "", err
			}
			root = filepath.Join(home, ".local", "state")
		}
		dir = filepath.Join(root, "agentflow", "runs", runID)
	}
	return filepath.Abs(dir)
}

func runCoordinator(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("coordinator", flag.ContinueOnError)
	var usage bytes.Buffer
	flags.SetOutput(&usage)
	runID := flags.String("run", "", "existing run ID, required")
	runDir := flags.String("run-dir", "", "existing private run directory; use up output run_dir for temporary runs (default: legacy XDG lookup)")
	asJSON := flags.Bool("json", false, "output structured readiness and failures")
	headless := flags.Bool("headless-codex", false, "explicitly install Codex adapters for an existing headless run; does not drain")
	headlessOpenCode := flags.Bool("headless-opencode", false, "explicitly install OpenCode adapters for an existing headless run; does not drain")
	headlessClaude := flags.Bool("headless-claude", false, "explicitly install Claude Code adapters for an existing headless run; does not drain")
	cmuxTransport := flags.Bool("cmux-transport", false, "enable explicit cmux messages and foreground workflow start/resume/drain")
	cmuxBinary := flags.String("cmux-binary", "", "absolute cmux executable (transport only)")
	binary := flags.String("native-binary", "", "absolute native executable (headless only)")
	turnTimeout := flags.Duration("turn-timeout", 85*time.Second, "headless native turn deadline in (0,30m]")
	flags.Usage = func() {
		usage.WriteString("Usage: agentflow coordinator --run <id> [--run-dir <dir>] [--headless-codex|--headless-opencode|--headless-claude] [--json]\nForeground IPC coordinator; explicit headless adapters or default persistence-only mode. Capability PARTIAL.\n")
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return writeTeamOutput(stdout, stderr, usage.String())
		}
		fmt.Fprintf(stderr, "agentflow: error: coordinator arguments: %v\n", err)
		return 2
	}
	if flags.NArg() != 0 || strings.TrimSpace(*runID) == "" || *runID == "." || *runID == ".." || strings.ContainsAny(*runID, "/\\\x00") {
		fmt.Fprintln(stderr, "agentflow: error: coordinator requires --run without path separators and no positional arguments")
		return 2
	}
	if conflictingHeadless(*headless, *headlessOpenCode, *headlessClaude) || (*cmuxTransport && (*headless || *headlessOpenCode || *headlessClaude)) || (*cmuxBinary != "" && !*cmuxTransport) {
		fmt.Fprintln(stderr, "agentflow: error: headless tool flags are mutually exclusive")
		return 2
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	dir, err := resolveRunDir(*runDir, *runID)
	var c *coordinator.Coordinator
	if err == nil {
		if *headless || *headlessOpenCode || *headlessClaude {
			tool := "codex"
			open := coordinator.OpenHeadlessCodex
			if *headlessOpenCode {
				tool, open = "opencode", coordinator.OpenHeadlessOpenCode
			}
			if *headlessClaude {
				tool, open = "claude", coordinator.OpenHeadlessClaude
			}
			if *binary == "" {
				*binary, err = runtime.NativeExecutable(tool)
			}
			if err == nil {
				c, err = open(ctx, runtime.HeadlessOptions{RunID: *runID, RunDir: dir, Binary: *binary, TurnTimeout: *turnTimeout, Tool: tool})
			}
		} else if *cmuxTransport {
			if *cmuxBinary == "" {
				*cmuxBinary, err = runtime.NativeExecutable("cmux")
			}
			if err == nil {
				c, err = coordinator.OpenCMUXTransport(ctx, dir, *runID, *cmuxBinary)
			}
		} else {
			c, err = coordinator.Open(ctx, dir, *runID)
		}
	}
	if err != nil {
		code := "COORDINATOR_START_FAILED"
		if errors.Is(err, lock.ErrAlreadyRunning) {
			code = "ALREADY_RUNNING"
		}
		var public *ipc.Error
		if errors.As(err, &public) {
			code = public.Code
		}
		if *asJSON {
			writeTeamJSON(stdout, stderr, coordinator.Result{Status: "FAILED", CapabilityStatus: "PARTIAL", RuntimeCommunication: "UNVERIFIED", Diagnostics: []runtime.Diagnostic{{Code: code, Path: "coordinator", Message: err.Error()}}})
		}
		fmt.Fprintf(stderr, "agentflow: error: %s: %v\n", code, err)
		return 1
	}
	ready := struct {
		Status               string `json:"status"`
		RunID                string `json:"run_id"`
		CapabilityStatus     string `json:"capability_status"`
		RuntimeCommunication string `json:"runtime_communication"`
	}{"COORDINATOR_READY", *runID, "PARTIAL", "UNVERIFIED"}
	var code int
	if *asJSON {
		code = writeTeamJSON(stdout, stderr, ready)
	} else {
		code = writeTeamOutput(stdout, stderr, fmt.Sprintf("Status: COORDINATOR_READY\nRun: %s\nCapability: PARTIAL\nNative communication: UNVERIFIED\n", *runID))
	}
	if code != 0 {
		_ = c.Close()
		return code
	}
	if err := c.Serve(ctx); err != nil {
		fmt.Fprintf(stderr, "agentflow: error: coordinator: %v\n", err)
		return 1
	}
	return 0
}

func stopViaIPC(dir string, actor ipc.Actor, requestID string, asJSON bool, stdout, stderr io.Writer) int {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	response, err := coordinator.Call(ctx, dir, ipc.Request{V: ipc.Version, RequestID: requestID, Actor: actor, Method: "stop", Params: json.RawMessage(`{}`)})
	var result coordinator.StopResult
	var failure coordinator.Result
	if err == nil {
		err = json.Unmarshal(response.Result, &failure)
		if err == nil && failure.Status != "FAILED" {
			err = json.Unmarshal(response.Result, &result)
			if err == nil && (result.RunID != actor.RunID || result.Status != "STOP_REQUESTED" || result.JournalSeq < 1 || result.Executed) {
				err = errors.New("invalid coordinator stop result")
			}
		}
	}
	if err != nil {
		code, message := "COORDINATOR_IPC_FAILED", "coordinator transport failed; operation outcome may be unknown"
		var public *ipc.Error
		if errors.As(err, &public) {
			code, message = public.Code, public.Message
		}
		failure = coordinator.Result{Status: "FAILED", CapabilityStatus: "PARTIAL", RuntimeCommunication: "UNVERIFIED", Diagnostics: []runtime.Diagnostic{{Code: code, Path: "coordinator.socket", Message: message}}}
	}
	if failure.Status == "FAILED" {
		if asJSON {
			writeTeamJSON(stdout, stderr, failure)
		}
		for _, d := range failure.Diagnostics {
			fmt.Fprintf(stderr, "agentflow: error: %s: %s: %s\n", d.Code, d.Path, d.Message)
		}
		return 1
	}
	if asJSON {
		return writeTeamJSON(stdout, stderr, result)
	}
	return writeTeamOutput(stdout, stderr, fmt.Sprintf("Run: %s\nStatus: %s\nJournal sequence: %d\nExecuted: false (stop intent only; process/workspace shutdown UNIMPLEMENTED)\n", result.RunID, result.Status, result.JournalSeq))
}
