package coordinator

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"agentflow/internal/runtime"
	"agentflow/internal/store"
)

// UpCMUX materializes a private launch snapshot under the existing run lock,
// creates one workspace, records its coordinates and releases the lock. It
// persists surface registrations without claiming native READY. IPC transport
// is explicitly enabled later with OpenCMUXTransport.
func UpCMUX(ctx context.Context, plan *runtime.StartupPlan, o UpOptions) (UpResult, error) {
	r := UpResult{Status: "FAILED", CapabilityStatus: "PARTIAL", RuntimeCommunication: "UNVERIFIED",
		Registration: "NOT_REGISTERED", NativeProcess: "NOT_STARTED", RunID: o.RunID, Diagnostics: []runtime.Diagnostic{}}
	fail := func(code string, err error) (UpResult, error) {
		r.Status = "FAILED"
		r.Diagnostics = append(r.Diagnostics, runtime.Diagnostic{Code: code, Path: "up.cmux", Message: err.Error()})
		return r, err
	}
	if err := ctx.Err(); err != nil {
		return fail("UP_CANCELLED", err)
	}
	if o.CMUX == nil || o.Headless != nil || !upRunID.MatchString(o.RunID) || !filepath.IsAbs(o.RunDir) ||
		plan == nil || plan.SnapshotPath != filepath.Join(o.RunDir, "config.resolved.json") {
		return fail("UP_OPTIONS_INVALID", errors.New("cmux startup requires a new run ID and absolute private snapshot directory"))
	}
	command, err := runtime.BuildCMUXWorkspaceCommand(plan, *o.CMUX)
	if err != nil {
		return fail("UP_CMUX_PLAN_INVALID", err)
	}
	if err := runtime.PreflightCMUXWorkspace(plan, *o.CMUX); err != nil {
		return fail("UP_CMUX_PREFLIGHT_FAILED", err)
	}
	snapshot, err := runtime.NewSnapshot(plan, o.RunID)
	if err != nil {
		return fail("UP_SNAPSHOT_INVALID", err)
	}
	if err := os.MkdirAll(o.RunDir, 0700); err != nil {
		return fail("UP_DIRECTORY_INVALID", err)
	}
	canonical, err := filepath.EvalSymlinks(o.RunDir)
	info, statErr := os.Lstat(o.RunDir)
	if err != nil || statErr != nil || canonical != o.RunDir || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return fail("UP_DIRECTORY_INVALID", errors.New("run directory must be canonical, private (0700) and contain no symlinks"))
	}
	owner, _, err := lockRun(o.RunDir, o.RunID, []string{"agentflow", "up"})
	if err != nil {
		return fail("UP_LOCK_FAILED", errors.New("cannot acquire run lock"))
	}
	defer owner.Close()
	r.RunDir, r.SnapshotPath = o.RunDir, plan.SnapshotPath
	r.EvidencePath = filepath.Join(o.RunDir, "cmux.launch.json")
	// An attempt remains consumed even if cmux fails or a response is lost.
	for _, name := range []string{"up.requested.json", "config.resolved.json", "state.db", "native.jsonl", "cmux.launch.json"} {
		if _, err := os.Lstat(filepath.Join(o.RunDir, name)); !errors.Is(err, os.ErrNotExist) {
			return fail("UP_ALREADY_ATTEMPTED", errors.New("run artifacts already exist; startup is never replayed"))
		}
	}
	launchID := rand.Text()
	if err := writeLaunchArtifact(filepath.Join(o.RunDir, "up.requested.json"), struct {
		LaunchID string                       `json:"launch_id"`
		Command  runtime.CMUXWorkspaceCommand `json:"command"`
	}{launchID, command}); err != nil {
		return fail("UP_ATTEMPT_WRITE_FAILED", err)
	}
	if err := runtime.WriteSnapshot(plan.SnapshotPath, snapshot); err != nil {
		return fail("UP_SNAPSHOT_WRITE_FAILED", err)
	}
	owner.db, err = store.Open(filepath.Join(o.RunDir, "state.db"))
	if err != nil {
		return fail("UP_STORE_FAILED", err)
	}
	if err := owner.db.Migrate(ctx); err != nil {
		return fail("UP_STORE_FAILED", err)
	}
	if _, err := runtime.NewRegistry(ctx, owner.db, snapshot); err != nil {
		return fail("UP_STORE_FAILED", err)
	}
	initial := []store.SurfaceRegistration{}
	for _, seat := range snapshot.Seats {
		initial = append(initial, store.SurfaceRegistration{RunID: o.RunID, AgentID: seat.AgentID, Generation: seat.Generation, Tool: seat.Tool, LaunchID: launchID, Status: "UNVERIFIED", NativeReady: "UNVERIFIED"})
	}
	if err := owner.db.RegisterCMUXSurfaces(ctx, initial); err != nil {
		return fail("UP_REGISTRATION_FAILED", err)
	}
	launched, launchErr := runtime.LaunchCMUXWorkspace(ctx, plan, *o.CMUX)
	r.CMUX = &launched
	if launched.Attempted {
		r.NativeProcess = "LAUNCH_REQUESTED"
	}
	if launchErr != nil {
		r, _ = fail("UP_CMUX_LAUNCH_FAILED", launchErr)
	} else {
		r.Status, r.Executed = "LAUNCHED", true // workspace creation only
	}
	bindings := []store.SurfaceRegistration{}
	for _, seat := range snapshot.Seats {
		b := store.SurfaceRegistration{RunID: o.RunID, AgentID: seat.AgentID, Generation: seat.Generation, Tool: seat.Tool, LaunchID: launchID, Status: "UNVERIFIED", NativeReady: "UNVERIFIED"}
		for _, surface := range launched.Surfaces {
			if surface.AgentID == seat.AgentID && launchErr == nil {
				b.WorkspaceID, b.WorkspaceRef, b.SurfaceID, b.SurfaceRef, b.Status = launched.WorkspaceID, launched.WorkspaceRef, surface.SurfaceID, surface.SurfaceRef, "LAUNCHED"
			}
		}
		bindings = append(bindings, b)
	}
	saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	if err := owner.db.RegisterCMUXSurfaces(saveCtx, bindings); err != nil {
		return fail("UP_REGISTRATION_FAILED", err)
	}
	r.Registration = "SURFACE_REGISTERED"
	if err := writeLaunchArtifact(r.EvidencePath, r); err != nil {
		r.Status = "FAILED"
		return fail("UP_EVIDENCE_WRITE_FAILED", errors.New("cannot persist cmux launch result; inspect created workspace before another action"))
	}
	return r, launchErr
}

func writeLaunchArtifact(path string, value any) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	err = json.NewEncoder(f).Encode(value)
	if err == nil {
		err = f.Sync()
	}
	return errors.Join(err, f.Close())
}
