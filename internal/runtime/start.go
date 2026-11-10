package runtime

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"

	"agentflow/internal/ipc"
	"agentflow/internal/store"
)

type LaunchRequest struct {
	Wrapper  Command
	Seat     SeatSnapshot
	PanePath []PaneStep
}

// Launcher is deliberately small. Launch starts the wrapper with Seat.Cwd and
// metadata, returning the expected native identity. WaitReady must wait for an
// actual agent READY; process start, wrapper registration or idle is insufficient.
// Implementations must honor ctx cancellation. ForegroundLauncher implements a
// single owned terminal; multi-pane topology execution remains unimplemented.
type Launcher interface {
	Launch(context.Context, LaunchRequest) (NativeBinding, error)
	WaitReady(context.Context, SeatSnapshot) (Registration, error)
}

type StartOptions struct {
	RunID        string
	SnapshotPath string // Absolute artifact path in an existing private run directory.
}

// Start consumes an unmodified StartupPlan in its stable seat order. It writes
// the private snapshot before invoking the launcher. After initialization, the
// registry is returned even on failure so callers can inspect partial progress.
// It does not retry or clean up launched seats: those require future reconciliation.
func Start(ctx context.Context, plan *StartupPlan, options StartOptions, db *store.Store, launcher Launcher) (*Registry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if launcher == nil || db == nil || plan == nil || len(plan.Seats) == 0 {
		return nil, errors.New("startup requires plan, store and launcher")
	}
	// Reject altered plan order, layout, wrappers or metadata before side effects.
	canonical, err := Compile(plan.Config, Options{WrapperBinary: plan.Seats[0].Wrapper.Binary, SnapshotPath: plan.SnapshotPath})
	if err != nil {
		return nil, err
	}
	if !reflect.DeepEqual(canonical, plan) {
		return nil, errors.New("startup plan differs from frozen configuration")
	}
	// Freeze the launch plan before giving caller-owned data to any adapter.
	plan = canonical
	snapshot, err := NewSnapshot(plan, options.RunID)
	if err != nil {
		return nil, err
	}
	if !filepath.IsAbs(options.SnapshotPath) {
		return nil, errors.New("startup snapshot path must be absolute")
	}
	// Refuse an existing run before publishing a new artifact. NewRegistry also
	// uses the database singleton constraint to reject competing initialization.
	if _, err := db.GetRun(ctx); err == nil {
		return nil, errors.New("startup store already contains a run")
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("inspect startup store: %w", err)
	}
	if err := WriteSnapshot(options.SnapshotPath, snapshot); err != nil {
		return nil, err
	}
	r, err := NewRegistry(ctx, db, snapshot)
	if err != nil {
		return nil, err
	}
	for i, planned := range plan.Seats {
		if err := ctx.Err(); err != nil {
			return r, err
		}
		seat := snapshot.Seats[i]
		wrapper, err := WrapperCommand(seat.AgentID, seat.Tool, Options{
			WrapperBinary: planned.Wrapper.Binary, SnapshotPath: options.SnapshotPath})
		if err != nil {
			return r, err
		}
		pending := store.Seat{RunID: seat.RunID, AgentID: seat.AgentID, Generation: seat.Generation, Tool: seat.Tool, Status: Starting}
		if err := db.RecordStartupLaunch(ctx, pending, false); err != nil {
			return r, err
		}
		native, err := launcher.Launch(ctx, LaunchRequest{Wrapper: wrapper, Seat: seat,
			PanePath: append([]PaneStep{}, planned.PanePath...)})
		if err != nil {
			return r, fmt.Errorf("launch %s: %w", seat.AgentID, err)
		}
		actor := ipc.Actor{RunID: seat.RunID, AgentID: seat.AgentID, Generation: seat.Generation}
		if err := r.bind(actor, native); err != nil {
			return r, fmt.Errorf("bind %s: %w", seat.AgentID, err)
		}
		pending.NativeSessionID, pending.WorkspaceID, pending.SurfaceID = native.SessionID, native.WorkspaceID, native.SurfaceID
		if err := db.RecordStartupLaunch(ctx, pending, true); err != nil {
			return r, err
		}
		seat.Native = native
		claim, err := launcher.WaitReady(ctx, seat)
		if err != nil {
			return r, fmt.Errorf("wait READY %s: %w", seat.AgentID, err)
		}
		// A READY from a different valid seat must not advance this startup step.
		if claim.Actor != actor {
			code := ipc.ActorMismatch
			if claim.Actor.RunID == actor.RunID && claim.Actor.AgentID == actor.AgentID {
				code = ipc.GenerationMismatch
			}
			return r, &ipc.Error{Code: code, Message: "READY does not match the pending launch"}
		}
		if err := r.Register(ctx, claim); err != nil {
			return r, fmt.Errorf("register %s: %w", seat.AgentID, err)
		}
	}
	return r, nil
}
