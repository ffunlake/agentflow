package runtime

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"time"

	"agentflow/internal/config"
	"agentflow/internal/lock"
	"agentflow/internal/store"
)

type Diagnostic struct {
	Code    string `json:"code"`
	Path    string `json:"path"`
	Message string `json:"message"`
}

type QueryOptions struct {
	RunID        string
	SnapshotPath string
	LockPath     string
}

type ArtifactStatus struct {
	Path   string `json:"path"`
	Status string `json:"status"`
}

// LockStatus omits the acquisition token. Reachability is never readiness.
type LockStatus struct {
	Path       string            `json:"path"`
	Status     string            `json:"status"`
	PID        *lock.PIDProbe    `json:"pid"`
	Socket     *lock.SocketProbe `json:"socket"`
	SocketPath string            `json:"socket_path"`
}

type RunStatus struct {
	RunID           string `json:"run_id"`
	Status          string `json:"status"`
	WorkspaceID     string `json:"workspace_id"`
	StartupPhase    string `json:"startup_phase"`
	StartupProgress string `json:"startup_progress"`
	StopRequested   bool   `json:"stop_requested"`
}

type StatusResult struct {
	Status               string                      `json:"status"`
	RuntimeCommunication string                      `json:"runtime_communication"`
	Run                  *RunStatus                  `json:"run"`
	Seats                []SeatSnapshot              `json:"seats"`
	Snapshot             ArtifactStatus              `json:"snapshot"`
	Lock                 LockStatus                  `json:"lock"`
	Diagnostics          []Diagnostic                `json:"diagnostics"`
	Surfaces             []store.SurfaceRegistration `json:"surfaces,omitempty"`
}

func EmptyStatus(options QueryOptions) StatusResult {
	return StatusResult{Status: "PARTIAL", RuntimeCommunication: "UNVERIFIED", Seats: []SeatSnapshot{},
		Snapshot: ArtifactStatus{Path: options.SnapshotPath, Status: "NOT_CHECKED"},
		Lock:     LockStatus{Path: options.LockPath, Status: "NOT_CHECKED"}, Diagnostics: []Diagnostic{}}
}

func (r *StatusResult) diagnose(code, path string, err error) {
	r.Diagnostics = append(r.Diagnostics, Diagnostic{Code: code, Path: path, Message: err.Error()})
}

// ReadStatus combines persisted identity, frozen configuration and bounded lock
// diagnostics. It never invokes a tool, changes configuration or repairs artifacts.
// STARTING describes missing registration evidence, not a known live process.
func ReadStatus(ctx context.Context, db *store.Store, options QueryOptions) StatusResult {
	r := EmptyStatus(options)
	if db == nil {
		r.diagnose("STORE_UNAVAILABLE", "store.path", errors.New("status requires an existing store"))
		return r
	}
	run, stored, err := db.ReadState(ctx)
	if err != nil {
		code := "STORE_READ_FAILED"
		if errors.Is(err, sql.ErrNoRows) {
			code = "RUN_NOT_FOUND"
			r.Status = "NOT_FOUND"
		}
		r.diagnose(code, "run.run_id", err)
		return r
	}
	if run.RunID != options.RunID {
		r.diagnose("RUN_MISMATCH", "run.run_id", errors.New("requested run differs from store identity"))
		return r
	}
	r.Run = &RunStatus{RunID: run.RunID, WorkspaceID: run.WorkspaceID, StartupPhase: run.StartupPhase,
		StartupProgress: Starting, StopRequested: run.StopRequested}
	r.Surfaces, err = db.CMUXRegistrations(ctx, run.RunID)
	if err != nil {
		r.diagnose("CMUX_REGISTRATION_READ_FAILED", "surfaces", err)
	}
	// Reconstruct planned seats from the frozen database config even when the
	// external artifact is missing. Never reread the original team/prompt files.
	var frozen config.Resolved
	var baseline *RunSnapshot
	err = json.Unmarshal([]byte(run.ConfigSnapshot), &frozen)
	if err == nil {
		var plan *StartupPlan
		plan, err = Compile(&frozen, Options{})
		if err == nil {
			baseline, err = NewSnapshot(plan, run.RunID)
		}
		if err == nil && frozen.Digest != run.ConfigDigest {
			err = errors.New("stored config digest differs from run digest")
		}
	}
	if err != nil {
		baseline = nil
		r.diagnose("CONFIG_SNAPSHOT_INVALID", "run.config_snapshot", err)
	}
	artifact, err := ReadSnapshot(options.SnapshotPath)
	switch {
	case errors.Is(err, os.ErrNotExist):
		r.Snapshot.Status = "MISSING"
		r.diagnose("SNAPSHOT_MISSING", "snapshot.path", err)
	case err != nil:
		r.Snapshot.Status = "INVALID"
		r.diagnose("SNAPSHOT_INVALID", "snapshot.path", err)
	case artifact.RunID != run.RunID || artifact.Config.Digest != run.ConfigDigest:
		r.Snapshot.Status = "MISMATCH"
		r.diagnose("SNAPSHOT_MISMATCH", "snapshot.path", errors.New("snapshot run/config identity differs from store"))
	default:
		r.Snapshot.Status = "VALID"
	}
	byID := map[string]SeatSnapshot{}
	if baseline != nil {
		for _, seat := range baseline.Seats {
			byID[seat.AgentID] = seat
		}
	}
	workspaces := map[string]bool{}
	if run.WorkspaceID != "" {
		workspaces[run.WorkspaceID] = true
	}
	for _, seat := range stored {
		view, planned := byID[seat.AgentID]
		if !planned && baseline != nil {
			r.diagnose("SEAT_MISMATCH", "seats."+seat.AgentID, errors.New("current seat is absent from frozen configuration"))
		}
		if planned && view.Tool != seat.Tool {
			r.diagnose("SEAT_MISMATCH", "seats."+seat.AgentID+".tool", errors.New("current tool differs from frozen configuration"))
		}
		view.RunID, view.AgentID, view.Generation = seat.RunID, seat.AgentID, seat.Generation
		view.Tool, view.Status = seat.Tool, seat.Status
		view.Native = NativeBinding{SessionID: seat.NativeSessionID, WorkspaceID: seat.WorkspaceID, SurfaceID: seat.SurfaceID}
		if seat.Status == Ready || seat.Status == Busy {
			if err := view.Native.validate(); err != nil {
				r.diagnose("SEAT_BINDING_INVALID", "seats."+seat.AgentID+".native", err)
			}
		}
		byID[seat.AgentID] = view
		if seat.WorkspaceID != "" {
			workspaces[seat.WorkspaceID] = true
		}
	}
	ready := 0
	validReady := 0
	for _, seat := range byID {
		r.Seats = append(r.Seats, seat)
		if seat.Status == Ready || seat.Status == Busy {
			ready++
			if seat.Native.validate() == nil && baseline != nil && baseline.Config.Agents[seat.AgentID].Tool == seat.Tool {
				validReady++
			}
		}
	}
	sort.Slice(r.Seats, func(i, j int) bool { return r.Seats[i].AgentID < r.Seats[j].AgentID })
	if baseline != nil && validReady == len(baseline.Seats) && len(r.Seats) == len(baseline.Seats) {
		r.Run.StartupProgress = "REGISTERED"
	} else if ready > 0 {
		r.Run.StartupProgress = "PARTIAL"
	}
	if ready == 0 && len(r.Surfaces) > 0 {
		r.Run.StartupProgress = "LAUNCHED"
		for _, surface := range r.Surfaces {
			if surface.Status != "LAUNCHED" {
				r.Run.StartupProgress = "UNVERIFIED"
			}
		}
	}
	r.Run.Status = r.Run.StartupProgress
	if run.StopRequested {
		r.Run.Status = store.StopRequested
	}
	if len(workspaces) == 1 {
		for id := range workspaces {
			r.Run.WorkspaceID = id
		}
	} else if len(workspaces) > 1 {
		r.Run.WorkspaceID = ""
		r.diagnose("WORKSPACE_CONFLICT", "run.workspace_id", errors.New("persisted bindings reference multiple workspaces"))
	}
	owner, err := lock.Inspect(options.LockPath)
	switch {
	case errors.Is(err, os.ErrNotExist):
		r.Lock.Status = "MISSING"
	case err != nil:
		r.Lock.Status = "INVALID"
		r.diagnose("LOCK_INVALID", "lock.path", err)
	case owner.RunID != run.RunID:
		r.Lock.Status = "MISMATCH"
		r.diagnose("LOCK_RUN_MISMATCH", "lock.run_id", errors.New("lock belongs to another run"))
	default:
		r.Lock.Status = "PRESENT"
		r.Lock.SocketPath = owner.SocketPath
		pid := lock.ProbePID(owner.PID)
		r.Lock.PID = &pid
		if owner.SocketPath != "" {
			probe := lock.ProbeSocket(ctx, owner.SocketPath, 250*time.Millisecond)
			r.Lock.Socket = &probe
		}
	}
	return r
}

type AttachResult struct {
	Status               string       `json:"status"`
	RunID                string       `json:"run_id"`
	WorkspaceID          string       `json:"workspace_id"`
	SocketPath           string       `json:"socket_path"`
	Attached             bool         `json:"attached"`
	RuntimeCommunication string       `json:"runtime_communication"`
	Diagnostics          []Diagnostic `json:"diagnostics"`
}

// QueryAttach returns only recorded coordinates; even a recorded workspace and
// connectable socket cannot prove a real cmux workspace exists or was attached.
func QueryAttach(status StatusResult) AttachResult {
	r := AttachResult{Status: "PARTIAL", SocketPath: status.Lock.SocketPath,
		RuntimeCommunication: "UNVERIFIED", Diagnostics: append([]Diagnostic{}, status.Diagnostics...)}
	if status.Run != nil {
		r.RunID, r.WorkspaceID = status.Run.RunID, status.Run.WorkspaceID
	}
	if r.WorkspaceID == "" {
		r.Diagnostics = append(r.Diagnostics, Diagnostic{Code: "WORKSPACE_UNVERIFIED", Path: "run.workspace_id", Message: "no unambiguous persisted workspace identity"})
	}
	r.Diagnostics = append(r.Diagnostics, Diagnostic{Code: "ATTACH_UNVERIFIED", Path: "attach.attached", Message: "real cmux attach is not implemented or verified"})
	return r
}

type StopResult struct {
	store.StopIntent
	Executed bool `json:"executed"`
}

func RequestStop(ctx context.Context, db *store.Store, runID string) (StopResult, error) {
	if db == nil || !identityText(runID) {
		return StopResult{}, fmt.Errorf("stop requires store and run identity")
	}
	intent, err := db.RequestStop(ctx, runID)
	return StopResult{StopIntent: intent}, err
}
