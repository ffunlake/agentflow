package cli

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"agentflow/internal/ipc"
	"agentflow/internal/runtime"
	"agentflow/internal/store"
)

func runControl(command string, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	var usage bytes.Buffer
	flags.SetOutput(&usage)
	runID := flags.String("run", "", "existing run ID, required")
	runDir := flags.String("run-dir", "", "existing run directory; use up output run_dir for temporary runs (default: legacy XDG lookup)")
	asJSON := flags.Bool("json", false, "output stable JSON")
	var agent, requestID string
	var generation int64
	if command == "stop" {
		flags.StringVar(&agent, "agent", "", "registered caller agent ID, required")
		flags.Int64Var(&generation, "generation", 0, "current caller generation, required")
		flags.StringVar(&requestID, "request-id", "", "stable sender request ID, required")
	}
	flags.Usage = func() {
		fmt.Fprintf(&usage, "Usage: agentflow %s --run <run-id> [--run-dir <dir>] [--json]\n\n", command)
		flags.PrintDefaults()
		usage.WriteString("\nAttach queries recorded coordinates only (UNVERIFIED). Stop records an intent only.\n")
	}
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return writeTeamOutput(stdout, stderr, usage.String())
		}
		fmt.Fprintf(stderr, "agentflow: error: %s arguments: %v\n", command, err)
		return 2
	}
	if flags.NArg() != 0 || strings.TrimSpace(*runID) == "" || *runID == "." || *runID == ".." || strings.ContainsAny(*runID, "/\\\x00") {
		fmt.Fprintf(stderr, "agentflow: error: %s requires --run <run-id> without path separators and accepts no positional arguments\n", command)
		return 2
	}
	if command == "stop" && (strings.TrimSpace(agent) == "" || generation < 1 || strings.TrimSpace(requestID) == "") {
		fmt.Fprintln(stderr, "agentflow: error: stop requires --agent, current --generation and --request-id")
		return 2
	}
	dir := *runDir
	if dir == "" {
		root := os.Getenv("XDG_STATE_HOME")
		if root == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				fmt.Fprintf(stderr, "agentflow: error: resolve state directory: %v\n", err)
				return 1
			}
			root = filepath.Join(home, ".local", "state")
		}
		dir = filepath.Join(root, "agentflow", "runs", *runID)
	}
	dir, err := filepath.Abs(dir)
	if err != nil {
		fmt.Fprintf(stderr, "agentflow: error: resolve run directory: %v\n", err)
		return 1
	}
	if command == "stop" {
		return stopViaIPC(dir, ipc.Actor{RunID: *runID, AgentID: agent, Generation: generation}, requestID, *asJSON, stdout, stderr)
	}
	options := runtime.QueryOptions{RunID: *runID, SnapshotPath: filepath.Join(dir, "config.resolved.json"), LockPath: filepath.Join(dir, "coordinator.lock")}
	report := runtime.EmptyStatus(options)
	path := filepath.Join(dir, "state.db")
	db, err := store.OpenExisting(path, false)
	if err != nil {
		report.Diagnostics = append(report.Diagnostics, runtime.Diagnostic{Code: "STORE_UNAVAILABLE", Path: "store.path", Message: err.Error()})
	} else {
		defer db.Close()
		report = runtime.ReadStatus(context.Background(), db, options)
	}
	var output any = report
	diagnostics := report.Diagnostics
	text := formatStatus(report)
	exit := 0
	if command == "attach" {
		result := runtime.QueryAttach(report)
		output, diagnostics = result, result.Diagnostics
		text = fmt.Sprintf("Status: %s\nRun: %s\nWorkspace: %s\nSocket: %s\nAttached: false\nRuntime communication: UNVERIFIED\n", result.Status, result.RunID, result.WorkspaceID, result.SocketPath)
		exit = 1
	}
	if *asJSON {
		if code := writeTeamJSON(stdout, stderr, output); code != 0 {
			return code
		}
	} else if code := writeTeamOutput(stdout, stderr, text); code != 0 {
		return code
	}
	for _, diagnostic := range diagnostics {
		fmt.Fprintf(stderr, "agentflow: error: %s: %s: %s\n", diagnostic.Code, diagnostic.Path, diagnostic.Message)
		exit = 1
	}
	return exit
}

func formatStatus(r runtime.StatusResult) string {
	var out strings.Builder
	fmt.Fprintf(&out, "Status: %s\nRuntime communication: %s\n", r.Status, r.RuntimeCommunication)
	if r.Run != nil {
		fmt.Fprintf(&out, "Run: %s\nRun status (persisted evidence): %s\nWorkspace: %s\nStartup phase (persisted): %s\nStartup progress: %s\nStop requested: %t\n", r.Run.RunID, r.Run.Status, r.Run.WorkspaceID, r.Run.StartupPhase, r.Run.StartupProgress, r.Run.StopRequested)
	}
	fmt.Fprintf(&out, "Snapshot: %s (%s)\nLock: %s (%s)\nSocket: %s\n", r.Snapshot.Status, r.Snapshot.Path, r.Lock.Status, r.Lock.Path, r.Lock.SocketPath)
	if r.Lock.PID != nil {
		fmt.Fprintf(&out, "Owner PID: %d (%s)\n", r.Lock.PID.PID, r.Lock.PID.Status)
	}
	if r.Lock.Socket != nil {
		fmt.Fprintf(&out, "Socket reachability: %s\n", r.Lock.Socket.Status)
	}
	for _, seat := range r.Seats {
		fmt.Fprintf(&out, "Seat %s: %s tool=%s generation=%d native_session=%s workspace=%s surface=%s\n", seat.AgentID, seat.Status, seat.Tool, seat.Generation, seat.Native.SessionID, seat.Native.WorkspaceID, seat.Native.SurfaceID)
	}
	for _, b := range r.Surfaces {
		fmt.Fprintf(&out, "Surface %s: %s generation=%d launch=%s workspace=%s (%s) surface=%s (%s) native_ready=%s\n", b.AgentID, b.Status, b.Generation, b.LaunchID, b.WorkspaceRef, b.WorkspaceID, b.SurfaceRef, b.SurfaceID, b.NativeReady)
	}
	return out.String()
}
