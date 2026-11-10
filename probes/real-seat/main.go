// real-seat is an explicitly invoked, one-shot installed-provider S4 probe.
// It is never called by go test or make check. No login, reset or retry path exists.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"agentflow/internal/config"
	"agentflow/internal/ipc"
	"agentflow/internal/lock"
	"agentflow/internal/runtime"
	"agentflow/internal/store"
)

type countedLauncher struct {
	*runtime.ForegroundLauncher
	Calls int
}

func (l *countedLauncher) Launch(ctx context.Context, request runtime.LaunchRequest) (runtime.NativeBinding, error) {
	l.Calls++
	return l.ForegroundLauncher.Launch(ctx, request)
}

func main() {
	if len(os.Args) == 4 && os.Args[1] == "lock-check" {
		l, err := lock.Acquire(os.Args[2], lock.Owner{RunID: os.Args[3]})
		if errors.Is(err, lock.ErrAlreadyRunning) {
			fmt.Println("ALREADY_RUNNING")
			return
		}
		if l != nil {
			_ = l.Release()
		}
		fmt.Fprintln(os.Stderr, "lock contention was not rejected", err)
		os.Exit(1)
	}
	tool := flag.String("tool", "", "opencode or codex; one process maximum")
	dir := flag.String("evidence-dir", "", "existing private absolute evidence directory")
	cwd := flag.String("cwd", "", "explicit absolute project cwd")
	wrapper := flag.String("wrapper", "", "absolute built agentflow executable")
	window := flag.String("window", "", "explicit window ref")
	workspace := flag.String("workspace", "", "explicit workspace ref")
	surface := flag.String("surface", "", "explicit owned temporary surface ref")
	flag.Parse()
	if (*tool != "opencode" && *tool != "codex") || !filepath.IsAbs(*dir) || !filepath.IsAbs(*cwd) || !filepath.IsAbs(*wrapper) || flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "invalid explicit probe options")
		os.Exit(2)
	}
	if info, err := os.Lstat(*dir); err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		fmt.Fprintln(os.Stderr, "evidence directory must already exist and be private")
		os.Exit(2)
	}
	runDir := filepath.Join(*dir, *tool)
	// This durable intent blocks all subsequent invocations, including failures.
	if err := os.Mkdir(runDir, 0700); err != nil {
		fmt.Fprintln(os.Stderr, "probe already attempted or directory unavailable")
		os.Exit(1)
	}
	summary := map[string]any{"tool": *tool, "status": "UNVERIFIED", "at": time.Now().UTC(), "cwd": *cwd, "run_dir": runDir}
	err := run(summary, runDir, *tool, *cwd, *wrapper, runtime.CMUXTarget{WindowID: *window, WorkspaceID: *workspace, SurfaceID: *surface})
	if err != nil {
		summary["error"] = err.Error()
	}
	f, writeErr := os.OpenFile(filepath.Join(runDir, "result.json"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if writeErr == nil {
		writeErr = json.NewEncoder(f).Encode(summary)
		_ = f.Close()
	}
	_ = json.NewEncoder(os.Stdout).Encode(summary)
	if err != nil || writeErr != nil {
		os.Exit(1)
	}
}

func run(out map[string]any, dir, tool, cwd, wrapper string, target runtime.CMUXTarget) error {
	actualCwd, err := os.Getwd()
	out["terminal_cwd"] = actualCwd
	if err != nil || actualCwd != cwd {
		return errors.New("temporary terminal cwd does not match the authorized project")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 85*time.Second)
	defer cancel()
	runID := "s4-real-6c5bcfcb-" + tool
	out["run_id"] = runID
	nativeBinary, err := runtime.NativeExecutable(tool)
	if err != nil {
		return err
	}
	cmuxBinary, err := runtime.NativeExecutable("cmux")
	if err != nil {
		return err
	}
	options := runtime.NativeOptions{Target: target, ToolBinary: nativeBinary, CMUXBinary: cmuxBinary, EvidencePath: filepath.Join(dir, "native.jsonl")}
	out["native_options"] = options
	lockPath := filepath.Join(dir, "coordinator.lock")
	l, err := lock.Acquire(lockPath, lock.Owner{RunID: runID, Command: []string{"real-seat", "--tool", tool}})
	if err != nil {
		return err
	}
	defer l.Release()
	owner, err := lock.Inspect(lockPath)
	if err != nil {
		return err
	}
	out["lock_owner"] = owner
	self, err := os.Executable()
	if err != nil {
		return err
	}
	contention := exec.CommandContext(ctx, self, "lock-check", lockPath, runID)
	b, err := contention.CombinedOutput()
	out["lock_contention"] = string(b)
	if err != nil || string(b) != "ALREADY_RUNNING\n" {
		return errors.New("cross-process lock contention check failed")
	}
	prompt, permissions, session := "You are the single S4 validation seat. Only acknowledge the exact startup challenge; do not use tools, inspect files, authenticate, retry, or change anything.", "default", "fresh"
	c := &config.Config{Version: 1, Name: "s4-real", Terminal: "cmux", Lead: "solo", ContextDir: filepath.Join(dir, "context"), SourcePath: filepath.Join(dir, "team.json"),
		Agents: map[string]config.Agent{"solo": {Role: "Senior developer validation", Prompt: &prompt, Tool: &tool, Cwd: &cwd, Permissions: &permissions, Session: &session}}, Layout: &config.Layout{Agent: "solo"}}
	resolved, err := config.Resolve(c, config.ResolveOptions{})
	if err != nil {
		return err
	}
	snapshotPath := filepath.Join(dir, "snapshot.json")
	plan, err := runtime.Compile(resolved, runtime.Options{WrapperBinary: wrapper, SnapshotPath: snapshotPath})
	if err != nil {
		return err
	}
	dbPath := filepath.Join(dir, "state.db")
	db, err := store.Open(dbPath)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	if err := db.Migrate(ctx); err != nil {
		return err
	}
	launcher := &countedLauncher{ForegroundLauncher: &runtime.ForegroundLauncher{Options: options}}
	startOptions := runtime.StartOptions{RunID: runID, SnapshotPath: snapshotPath}
	registry, startErr := runtime.Start(ctx, plan, startOptions, db, launcher)
	out["launch_calls"] = launcher.Calls
	if registry != nil {
		out["registry"] = registry.Snapshot()
	}
	// Re-enter Start, never _seat: it must reject before reaching the adapter.
	checkCtx, checkCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer checkCancel()
	_, duplicateErr := runtime.Start(checkCtx, plan, startOptions, db, launcher)
	out["duplicate_start_rejected"] = duplicateErr != nil && launcher.Calls == 1
	if duplicateErr != nil {
		out["duplicate_start_error"] = duplicateErr.Error()
	}
	if err := db.Close(); err != nil {
		return err
	}
	db, err = store.Open(dbPath)
	if err != nil {
		return err
	}
	runRecord, runErr := db.GetRun(checkCtx)
	seat, seatErr := db.GetSeat(checkCtx, runID, "solo", 1)
	journal, journalErr := db.JournalAfter(checkCtx, runID, 0)
	out["stored_run"], out["stored_seat"], out["journal"] = runRecord, seat, journal
	_, reopenErr := runtime.Start(checkCtx, plan, startOptions, db, launcher)
	out["reopened_duplicate_rejected"] = reopenErr != nil && launcher.Calls == 1
	if startErr != nil {
		out["status"] = "PARTIAL"
		return startErr
	}
	if runErr != nil || seatErr != nil || journalErr != nil || seat.Status != runtime.Ready || duplicateErr == nil || reopenErr == nil || launcher.Calls != 1 {
		return errors.New("persisted READY or duplicate protection check failed")
	}
	// Independently parse the saved real stdout against its pre-execution nonce.
	f, err := os.Open(options.EvidencePath)
	if err != nil {
		return err
	}
	defer f.Close()
	var challenge struct {
		Challenge struct {
			Actor ipc.Actor `json:"actor"`
			Nonce string    `json:"nonce"`
		} `json:"challenge"`
		Location runtime.NativeBinding `json:"location"`
	}
	var finished struct {
		Stdout    string `json:"stdout"`
		Executed  bool   `json:"executed"`
		Succeeded bool   `json:"process_succeeded"`
		Truncated bool   `json:"truncated"`
	}
	d := json.NewDecoder(f)
	if d.Decode(&challenge) != nil || d.Decode(&finished) != nil || d.Decode(new(any)) != io.EOF || !finished.Executed || !finished.Succeeded || finished.Truncated {
		return errors.New("incomplete native evidence")
	}
	snapshot, err := runtime.ReadSnapshot(snapshotPath)
	if err != nil {
		return err
	}
	expected := snapshot.Seats[0]
	expected.Native = challenge.Location
	native, claim, err := runtime.ParseNativeReady(tool, []byte(finished.Stdout), expected, challenge.Challenge.Nonce)
	if err != nil || claim == nil || claim.Actor != challenge.Challenge.Actor || native.SessionID != seat.NativeSessionID || native.WorkspaceID != seat.WorkspaceID || native.SurfaceID != seat.SurfaceID {
		return errors.New("saved JSONL challenge differs from persisted registration")
	}
	out["verified_claim"], out["nonce_exact_match"] = claim, true
	if err := l.Release(); err != nil {
		return err
	}
	out["lock_released"] = true
	out["status"] = "PASS"
	return nil
}
