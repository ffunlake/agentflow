package coordinator

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"agentflow/internal/ipc"
	"agentflow/internal/lock"
	"agentflow/internal/runtime"
	"agentflow/internal/store"
)

type UpOptions struct {
	RunID    string
	RunDir   string
	Native   runtime.NativeOptions
	Headless *runtime.HeadlessOptions
	CMUX     *runtime.CMUXWorkspaceOptions
}

type UpResult struct {
	Status               string                       `json:"status"`
	CapabilityStatus     string                       `json:"capability_status"`
	RuntimeCommunication string                       `json:"runtime_communication"`
	Executed             bool                         `json:"executed"`
	RunID                string                       `json:"run_id"`
	RunDir               string                       `json:"run_dir,omitempty"`
	SnapshotPath         string                       `json:"snapshot_path,omitempty"`
	ContextPath          string                       `json:"context_path,omitempty"`
	EvidencePath         string                       `json:"evidence_path,omitempty"`
	SocketPath           string                       `json:"socket_path,omitempty"`
	CoordinatorReady     bool                         `json:"coordinator_ready"`
	WorkflowStarted      bool                         `json:"workflow_started,omitempty"`
	NativeProcess        string                       `json:"native_process"`
	Seat                 *runtime.SeatSnapshot        `json:"seat,omitempty"`
	ReadyProof           *store.StartupReadyProof     `json:"ready_proof,omitempty"`
	Seats                []runtime.SeatSnapshot       `json:"seats,omitempty"`
	ReadyProofs          []store.StartupReadyProof    `json:"ready_proofs,omitempty"`
	Diagnostics          []runtime.Diagnostic         `json:"diagnostics"`
	CMUX                 *runtime.CMUXWorkspaceResult `json:"cmux,omitempty"`
	Registration         string                       `json:"registration,omitempty"`
}

var upRunID = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,95}$`)

// Up returns the coordinator with the original lock/store still owned. The
// caller must Serve or Close it. It never retries or reopens an earlier attempt.
func Up(ctx context.Context, plan *runtime.StartupPlan, o UpOptions) (*Coordinator, UpResult, error) {
	if o.CMUX != nil {
		if o.Headless != nil {
			return nil, UpResult{Status: "FAILED"}, errors.New("cmux and headless startup are mutually exclusive")
		}
		r, err := UpCMUX(ctx, plan, o)
		return nil, r, err
	}
	if o.Headless != nil {
		if plan == nil || o.Headless.RunID != o.RunID || o.Headless.RunDir != o.RunDir {
			return nil, UpResult{Status: "FAILED"}, errors.New("headless options differ from run")
		}
		adapter, err := runtime.NewHeadless(plan.Config, *o.Headless)
		if err != nil {
			return nil, UpResult{Status: "FAILED", Diagnostics: []runtime.Diagnostic{{Code: "HEADLESS_OPTIONS_INVALID", Path: "up.headless", Message: err.Error()}}}, err
		}
		return up(ctx, plan, o, adapter)
	}
	return up(ctx, plan, o, &runtime.ForegroundLauncher{Options: o.Native})
}

func up(ctx context.Context, plan *runtime.StartupPlan, o UpOptions, native runtime.Launcher) (*Coordinator, UpResult, error) {
	r := UpResult{Status: "FAILED", CapabilityStatus: "PARTIAL", RuntimeCommunication: "UNVERIFIED", RunID: o.RunID, NativeProcess: "NOT_STARTED", Diagnostics: []runtime.Diagnostic{}}
	fail := func(code, path string, err error) (*Coordinator, UpResult, error) {
		var seatError *runtime.SeatError
		if errors.As(err, &seatError) {
			code, path = seatError.Code, seatError.Path
		}
		if errors.Is(err, lock.ErrAlreadyRunning) {
			code = "ALREADY_RUNNING"
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			code = "UP_CANCELLED"
		}
		r.Diagnostics = append(r.Diagnostics, runtime.Diagnostic{Code: code, Path: path, Message: "single-seat startup was not completed; inspect retained run evidence before another action"})
		return nil, r, err
	}
	if err := ctx.Err(); err != nil {
		return fail("UP_CANCELLED", "up", err)
	}
	if plan == nil || len(plan.Seats) == 0 || (o.Headless == nil && (len(plan.Seats) != 1 || plan.Seats[0].Tool != "opencode" || len(plan.Seats[0].PanePath) != 0)) {
		r.Status = "PARTIAL"
		return fail("UP_LAYOUT_UNIMPLEMENTED", "up.seats", errors.New("real up supports one OpenCode foreground seat only"))
	}
	if !upRunID.MatchString(o.RunID) || !filepath.IsAbs(o.RunDir) || !filepath.IsAbs(plan.Seats[0].Wrapper.Binary) || native == nil {
		return fail("UP_OPTIONS_INVALID", "up.options", errors.New("explicit valid run ID, absolute run directory and wrapper are required"))
	}
	r.RunDir = filepath.Clean(o.RunDir)
	r.SnapshotPath = filepath.Join(r.RunDir, "config.resolved.json")
	r.EvidencePath = filepath.Join(r.RunDir, "native.jsonl")
	r.SocketPath = filepath.Join(r.RunDir, "ipc.sock")
	if o.Headless != nil {
		r.EvidencePath = r.RunDir // per-seat startup and per-message capture files
	}
	if plan.SnapshotPath != r.SnapshotPath || (o.Headless == nil && o.Native.EvidencePath != r.EvidencePath) {
		return fail("UP_OPTIONS_INVALID", "up.snapshot", errors.New("snapshot and evidence must use the selected private run directory"))
	}
	// Directory scaffolding is the only creation allowed before lock acquisition.
	if err := os.MkdirAll(r.RunDir, 0700); err != nil {
		return fail("UP_DIRECTORY_INVALID", "up.run_dir", err)
	}
	canonical, err := filepath.EvalSymlinks(r.RunDir)
	if err != nil || canonical != r.RunDir {
		return fail("UP_DIRECTORY_INVALID", "up.run_dir", errors.New("run directory must be canonical without symlinks"))
	}
	c, _, err := lockRun(r.RunDir, o.RunID, []string{"agentflow", "up"})
	if err != nil {
		return fail("UP_LOCK_FAILED", "up.lock", err)
	}
	keep := false
	defer func() {
		if !keep {
			_ = c.Close()
		}
	}()
	for _, name := range []string{"up.requested.json", "state.db", "state.db-wal", "state.db-shm", "config.resolved.json", "native.jsonl", "ipc.sock"} {
		if _, err := os.Lstat(filepath.Join(r.RunDir, name)); err == nil {
			return fail("UP_ALREADY_ATTEMPTED", "up.run_dir", errors.New("run artifacts already exist; startup is never replayed"))
		} else if !errors.Is(err, os.ErrNotExist) {
			return fail("UP_ARTIFACT_INVALID", "up.run_dir", err)
		}
	}
	marker, err := os.OpenFile(filepath.Join(r.RunDir, "up.requested.json"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return fail("UP_ALREADY_ATTEMPTED", "up.run_dir", err)
	}
	err = json.NewEncoder(marker).Encode(struct {
		RunID  string             `json:"run_id"`
		At     time.Time          `json:"at"`
		Target runtime.CMUXTarget `json:"target"`
	}{o.RunID, time.Now().UTC(), o.Native.Target})
	if err == nil {
		err = marker.Sync()
	}
	closeErr := marker.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return fail("UP_ATTEMPT_WRITE_FAILED", "up.attempt", err)
	}
	if o.Headless != nil {
		tool := o.Headless.Tool
		if tool == "" {
			tool = "codex"
		}
		f, err := os.OpenFile(filepath.Join(r.RunDir, "headless."+tool+".json"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return fail("UP_MODE_WRITE_FAILED", "up.headless", err)
		}
		err = json.NewEncoder(f).Encode("headless-" + tool + "-v1")
		if err == nil {
			err = f.Sync()
		}
		err = errors.Join(err, f.Close())
		if err != nil {
			return fail("UP_MODE_WRITE_FAILED", "up.headless", err)
		}
	}
	c.db, err = store.Open(filepath.Join(r.RunDir, "state.db"))
	if err != nil {
		return fail("UP_STORE_FAILED", "up.store", err)
	}
	if err := c.db.Migrate(ctx); err != nil {
		return fail("UP_STORE_FAILED", "up.store", err)
	}
	l := &contextLauncher{native: native, snapshot: r.SnapshotPath, runID: o.RunID, result: &r}
	registry, err := runtime.Start(ctx, plan, runtime.StartOptions{RunID: o.RunID, SnapshotPath: r.SnapshotPath}, c.db, l)
	if registry != nil {
		snapshot := registry.Snapshot()
		r.Seat = &snapshot.Seats[0]
		if o.Headless != nil {
			r.Seats = snapshot.Seats
		}
	}
	if err != nil {
		return fail("UP_START_FAILED", "up.start", err)
	}
	r.NativeProcess = "EXITED_AFTER_HANDSHAKE"
	r.RuntimeCommunication = "STARTUP_READY_VERIFIED"
	events, err := c.db.JournalAfter(ctx, o.RunID, 0)
	if err != nil {
		return fail("UP_PROOF_UNAVAILABLE", "up.ready", err)
	}
	for _, event := range events {
		if event.Event == store.StartupReadyVerified {
			var proof store.StartupReadyProof
			if json.Unmarshal([]byte(event.Details), &proof) == nil {
				if r.Seat != nil && proof.Seat.AgentID == r.Seat.AgentID {
					r.ReadyProof = &proof
				}
				if o.Headless != nil {
					r.ReadyProofs = append(r.ReadyProofs, proof)
				}
			}
		}
	}
	if r.ReadyProof == nil || r.ReadyProof.Nonce == "" {
		return fail("UP_PROOF_UNAVAILABLE", "up.ready", errors.New("READY has no persisted native nonce proof"))
	}
	if o.Headless != nil && len(r.ReadyProofs) != len(r.Seats) {
		return fail("UP_PROOF_UNAVAILABLE", "up.ready", errors.New("headless seats lack READY proofs"))
	}
	if adapter, ok := native.(*runtime.Headless); ok {
		err = c.listenWithAdapters(r.RunDir, adapter.ResetAdapter(), adapter, true)
	} else {
		err = c.listen(r.RunDir)
	}
	if err != nil {
		return fail("UP_IPC_FAILED", "up.socket", err)
	}
	r.Status, r.CoordinatorReady = "READY", true
	keep = true
	return c, r, nil
}

type contextLauncher struct {
	native          runtime.Launcher
	snapshot, runID string
	result          *UpResult
}

func (l *contextLauncher) Launch(ctx context.Context, req runtime.LaunchRequest) (runtime.NativeBinding, error) {
	if err := ctx.Err(); err != nil {
		return runtime.NativeBinding{}, err
	}
	if l.result.ContextPath == "" {
		contextRun, err := runtime.CreateContext(l.snapshot, l.runID)
		if err != nil {
			return runtime.NativeBinding{}, err
		}
		l.result.ContextPath = contextRun.Path()
	}
	if err := ctx.Err(); err != nil {
		return runtime.NativeBinding{}, err
	}
	l.result.NativeProcess = "OUTCOME_UNVERIFIED"
	// The adapter reports execution after it has a process result. A failed or
	// canceled wrapper may have launched native work; do not infer replay safety.
	native, err := l.native.Launch(ctx, req)
	if adapter, ok := l.native.(*runtime.ForegroundLauncher); ok {
		l.result.Executed = adapter.ExecutionStarted()
	}
	if _, ok := l.native.(*runtime.Headless); ok && err == nil {
		l.result.Executed = true
	}
	return native, err
}

func (l *contextLauncher) WaitReady(ctx context.Context, seat runtime.SeatSnapshot) (runtime.Registration, error) {
	claim, err := l.native.WaitReady(ctx, seat)
	if err == nil && claim.Nonce == "" {
		return runtime.Registration{}, &ipc.Error{Code: ipc.ActorMismatch, Message: "real up requires a native READY nonce"}
	}
	return claim, err
}
