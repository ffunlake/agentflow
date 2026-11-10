package cli

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"agentflow/internal/config"
	"agentflow/internal/coordinator"
	"agentflow/internal/ipc"
	"agentflow/internal/runtime"
)

type realUpOptions struct {
	runID, runDir, window, workspace, surface, nativeBinary, cmuxBinary, task string
	timeout                                                                   time.Duration
	headless                                                                  bool
	headlessOpenCode                                                          bool
	headlessClaude                                                            bool
	turnTimeout                                                               time.Duration
	toolBinaries                                                              map[string]string
}

func (o realUpOptions) isHeadless() bool { return o.headless || o.headlessOpenCode || o.headlessClaude }

func conflictingHeadless(tools ...bool) bool {
	count := 0
	for _, enabled := range tools {
		if enabled {
			count++
		}
	}
	return count > 1
}

func (o realUpOptions) headlessTool() string {
	if o.headlessClaude {
		return "claude"
	}
	if o.headlessOpenCode {
		return "opencode"
	}
	return "codex"
}

func autoStartCMUXWorkflow(plan *runtime.StartupPlan) bool {
	return plan != nil && plan.Config != nil && plan.Config.Workflow != nil
}

func runRealUp(file, tool string, o realUpOptions, asJSON bool, stdout, stderr io.Writer) int {
	result := coordinator.UpResult{Status: "FAILED", CapabilityStatus: "PARTIAL", RuntimeCommunication: "UNVERIFIED", RunID: o.runID, NativeProcess: "NOT_STARTED", Diagnostics: []runtime.Diagnostic{}}
	failure := func(code, path string, err error) int {
		result.Diagnostics = append(result.Diagnostics, runtime.Diagnostic{Code: code, Path: path, Message: err.Error()})
		return writeUpResult(result, asJSON, stdout, stderr)
	}
	c, err := config.Load(file)
	if err != nil {
		return failure("CONFIG_INVALID", "config", err)
	}
	resolved, err := config.Resolve(c, config.ResolveOptions{Tool: tool})
	if err != nil {
		return failure("CONFIG_INVALID", "config", err)
	}
	// Unsupported plans fail before directory creation, executable lookup or lock.
	plan, err := runtime.Compile(resolved, runtime.Options{})
	if err != nil {
		return failure("LAYOUT_INVALID", "layout", err)
	}
	if o.isHeadless() && (o.window != "" || o.workspace != "" || o.surface != "" || o.cmuxBinary != "") {
		return failure("HEADLESS_OPTIONS_INVALID", "up.headless", errors.New("headless up does not accept cmux target flags"))
	}
	maxStartup := 90 * time.Second
	if o.isHeadless() {
		maxStartup = 30 * time.Minute
	}
	if !entryRunID(o.runID) || o.timeout <= 0 || o.timeout > maxStartup {
		return failure("UP_OPTIONS_REQUIRED", "up.options", errors.New("real up requires a safe --run and valid startup deadline; cmux allows <=90s; headless allows <=30m"))
	}
	if !o.isHeadless() && (o.workspace != "" || o.surface != "") {
		return failure("UP_OPTIONS_INVALID", "up.options", errors.New("cmux up creates a new workspace; only --window is an optional target"))
	}
	if o.isHeadless() && len(o.toolBinaries) != 0 {
		return failure("HEADLESS_OPTIONS_INVALID", "up.headless", errors.New("headless uses --native-binary; per-tool TUI binary overrides are unsupported"))
	}
	dir, createdRunDir, err := createUpRunDir(o.runDir, o.runID)
	if err != nil {
		return failure("UP_DIRECTORY_INVALID", "up.run_dir", err)
	}
	result.RunDir = dir
	handedOff := false
	defer func() {
		if createdRunDir && !handedOff {
			// Remove only our unused, empty allocation. Never recursively remove
			// artifacts or a directory handed to a runtime, even after failure.
			if err := os.Remove(dir); err != nil && !errors.Is(err, os.ErrNotExist) {
				fmt.Fprintf(stderr, "agentflow: error: UP_DIRECTORY_CLEANUP_FAILED: up.run_dir: %v\n", err)
			}
		}
	}()
	wrapper, err := os.Executable()
	if err == nil {
		wrapper, err = filepath.EvalSymlinks(wrapper)
	}
	if err != nil {
		return failure("UP_WRAPPER_UNAVAILABLE", "up.wrapper", err)
	}
	if o.isHeadless() && o.nativeBinary == "" {
		o.nativeBinary, err = runtime.NativeExecutable(o.headlessTool())
		if err != nil {
			return failure("UP_NATIVE_UNAVAILABLE", "up.native", err)
		}
	}
	if !o.isHeadless() && o.cmuxBinary == "" {
		o.cmuxBinary, err = runtime.NativeExecutable("cmux")
		if err != nil {
			return failure("UP_CMUX_UNAVAILABLE", "up.cmux", err)
		}
	}
	plan, err = runtime.Compile(resolved, runtime.Options{WrapperBinary: wrapper, SnapshotPath: filepath.Join(dir, "config.resolved.json")})
	if err != nil {
		return failure("LAYOUT_INVALID", "layout", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	startup, cancel := context.WithTimeout(ctx, o.timeout)
	defer cancel()
	options := coordinator.UpOptions{RunID: o.runID, RunDir: dir, Native: runtime.NativeOptions{Target: runtime.CMUXTarget{WindowID: o.window, WorkspaceID: o.workspace, SurfaceID: o.surface}, ToolBinary: o.nativeBinary, CMUXBinary: o.cmuxBinary, EvidencePath: filepath.Join(dir, "native.jsonl")}}
	if o.isHeadless() {
		options.Headless = &runtime.HeadlessOptions{RunID: o.runID, RunDir: dir, Binary: o.nativeBinary, TurnTimeout: o.turnTimeout, Tool: o.headlessTool()}
	} else {
		binaries := make(map[string]string)
		for _, seat := range plan.Seats {
			binaries[seat.Tool] = o.toolBinaries[seat.Tool]
		}
		if o.nativeBinary != "" {
			if len(binaries) != 1 {
				return failure("UP_OPTIONS_INVALID", "up.native", errors.New("mixed-tool cmux layouts require per-tool --codex-binary/--opencode-binary/--claude-binary overrides"))
			}
			for name := range binaries {
				if binaries[name] != "" {
					return failure("UP_OPTIONS_INVALID", "up.native", errors.New("native and per-tool binary overrides conflict"))
				}
				binaries[name] = o.nativeBinary
			}
		}
		for name, binary := range binaries {
			if binary == "" {
				binary, err = runtime.NativeExecutable(name)
				if err != nil {
					cancel()
					return failure("UP_NATIVE_UNAVAILABLE", "up.native", errors.New("required native tool executable is unavailable"))
				}
			}
			binaries[name] = binary
		}
		options.CMUX = &runtime.CMUXWorkspaceOptions{WindowID: o.window, CMUXBinary: o.cmuxBinary, ToolBinaries: binaries}
	}
	if options.CMUX != nil && autoStartCMUXWorkflow(plan) && strings.TrimSpace(o.task) != "" {
		if err := persistInitialWorkflowTask(dir, o.task); err != nil {
			return failure("UP_TASK_WRITE_FAILED", "up.workflow", err)
		}
	}
	handedOff = true
	owner, started, startErr := coordinator.Up(startup, plan, options)
	// Early runtime/preflight failures may precede artifact setup, but the
	// allocated directory still belongs to this attempt and must be discoverable.
	if started.RunDir == "" {
		started.RunDir = dir
	}
	cancel()
	if startErr != nil {
		return writeUpResult(started, asJSON, stdout, stderr)
	}
	if options.CMUX != nil {
		// A workflow-bearing cmux run owns the transport from startup. Without a
		// task it stays idle until the managed main seat starts one from natural
		// language; with --task the first dispatch is sent immediately.
		if autoStartCMUXWorkflow(plan) {
			transport, err := coordinator.OpenCMUXTransport(ctx, dir, o.runID, o.cmuxBinary)
			if err != nil {
				started.Status = "FAILED"
				started.Diagnostics = append(started.Diagnostics, runtime.Diagnostic{Code: "COORDINATOR_START_FAILED", Path: "coordinator", Message: err.Error()})
				return writeUpResult(started, asJSON, stdout, stderr)
			}
			started.CoordinatorReady = true
			started.SocketPath = filepath.Join(dir, "ipc.sock")
			serveDone := make(chan error, 1)
			go func() { serveDone <- transport.Serve(ctx) }()
			if strings.TrimSpace(o.task) != "" {
				startCtx, startCancel := context.WithTimeout(ctx, 30*time.Minute)
				requestID := "up-workflow-" + rand.Text()
				params, _ := json.Marshal(coordinator.Params{Workflow: &coordinator.WorkflowParams{Kind: "start", Revision: 0, Task: o.task}})
				response, callErr := coordinator.Call(startCtx, dir, ipc.Request{
					V: ipc.Version, RequestID: requestID,
					Actor:  ipc.Actor{RunID: o.runID, AgentID: plan.Run.Lead, Generation: 1},
					Method: "workflow", Params: params,
				})
				startCancel()
				if callErr == nil {
					var workflow coordinator.Result
					callErr = json.Unmarshal(response.Result, &workflow)
					if callErr == nil && workflow.Status != "FAILED" {
						started.WorkflowStarted = true
					}
				}
				if callErr != nil || !started.WorkflowStarted {
					message := "workflow start was not accepted; coordinator remains available for explicit retry"
					if callErr != nil {
						message = callErr.Error()
					}
					started.Diagnostics = append(started.Diagnostics, runtime.Diagnostic{Code: "WORKFLOW_START_UNVERIFIED", Path: "workflow", Message: message})
				}
			}
			if code := writeUpResult(started, asJSON, stdout, stderr); code != 0 {
				_ = transport.Close()
				return code
			}
			if err := <-serveDone; err != nil {
				fmt.Fprintf(stderr, "agentflow: error: coordinator: %v\n", err)
				return 1
			}
			return 0
		}
		return writeUpResult(started, asJSON, stdout, stderr)
	}
	if code := writeUpResult(started, asJSON, stdout, stderr); code != 0 {
		_ = owner.Close()
		return code
	}
	if err := owner.Serve(ctx); err != nil {
		fmt.Fprintf(stderr, "agentflow: error: coordinator: %v\n", err)
		return 1
	}
	return 0
}

func persistInitialWorkflowTask(runDir, task string) error {
	f, err := os.OpenFile(filepath.Join(runDir, "initial-workflow-task.txt"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, writeErr := f.WriteString(task)
	if writeErr == nil {
		writeErr = f.Sync()
	}
	return errors.Join(writeErr, f.Close())
}

func writeUpResult(r coordinator.UpResult, asJSON bool, stdout, stderr io.Writer) int {
	var code int
	if asJSON {
		code = writeTeamJSON(stdout, stderr, r)
	} else {
		var text strings.Builder
		fmt.Fprintf(&text, "Status: %s\nCapability: %s\nRuntime communication: %s\nRun: %s\nRun directory: %s\nSnapshot: %s\nContext: %s\nEvidence: %s\nCoordinator ready: %t\nWorkflow started: %t\nNative process: %s\n", r.Status, r.CapabilityStatus, r.RuntimeCommunication, r.RunID, r.RunDir, r.SnapshotPath, r.ContextPath, r.EvidencePath, r.CoordinatorReady, r.WorkflowStarted, r.NativeProcess)
		if r.Seat != nil {
			fmt.Fprintf(&text, "Seat: %s generation=%d tool=%s native_session=%s\n", r.Seat.AgentID, r.Seat.Generation, r.Seat.Tool, r.Seat.Native.SessionID)
		}
		if r.CMUX != nil {
			fmt.Fprintf(&text, "Window: %s (%s)\nWorkspace: %s (%s)\nRegistration: %s\n", r.CMUX.WindowRef, r.CMUX.WindowID, r.CMUX.WorkspaceRef, r.CMUX.WorkspaceID, r.Registration)
			for _, surface := range r.CMUX.Surfaces {
				fmt.Fprintf(&text, "Agent: %s tool=%s surface=%s (%s)\n", surface.AgentID, surface.Tool, surface.SurfaceRef, surface.SurfaceID)
			}
		}
		code = writeTeamOutput(stdout, stderr, text.String())
	}
	if code != 0 {
		return code
	}
	for _, d := range r.Diagnostics {
		fmt.Fprintf(stderr, "agentflow: error: %s: %s: %s\n", d.Code, d.Path, d.Message)
	}
	if r.Status != "READY" && r.Status != "LAUNCHED" {
		return 1
	}
	return 0
}
