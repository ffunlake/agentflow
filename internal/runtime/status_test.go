package runtime

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"agentflow/internal/store"
)

func TestStatusRegisteredBindingsSurviveReopenAndMissingArtifact(t *testing.T) {
	plan, db, options := startupFixture(t)
	_, err := Start(t.Context(), plan, options, db, fakeLauncher{
		launch: func(_ context.Context, request LaunchRequest) (NativeBinding, error) {
			return fakeNative(request.Seat.AgentID), nil
		},
		wait: func(_ context.Context, seat SeatSnapshot) (Registration, error) { return readyClaim(seat), nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	reopened, err := store.OpenExisting(filepath.Join(filepath.Dir(options.SnapshotPath), "state.db"), false)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	query := QueryOptions{RunID: options.RunID, SnapshotPath: options.SnapshotPath, LockPath: filepath.Join(filepath.Dir(options.SnapshotPath), "coordinator.lock")}
	r := ReadStatus(t.Context(), reopened, query)
	if len(r.Diagnostics) != 0 || r.Run.StartupProgress != "REGISTERED" || r.Run.StartupPhase != Starting || r.Run.WorkspaceID != "fake-workspace" {
		t.Fatalf("%+v", r)
	}
	for _, seat := range r.Seats {
		if seat.Native != fakeNative(seat.AgentID) || seat.Status != Ready {
			t.Fatalf("%+v", seat)
		}
	}
	if err := os.Remove(options.SnapshotPath); err != nil {
		t.Fatal(err)
	}
	r = ReadStatus(t.Context(), reopened, query)
	if len(r.Seats) != 3 || r.Snapshot.Status != "MISSING" || r.Seats[0].Status != Ready {
		t.Fatalf("%+v", r)
	}
	attach := QueryAttach(r)
	if attach.Attached || attach.WorkspaceID != "fake-workspace" || attach.RuntimeCommunication != "UNVERIFIED" {
		t.Fatalf("%+v", attach)
	}
}

func TestStatusDoesNotChooseBetweenConflictingWorkspaces(t *testing.T) {
	plan, db, options := startupFixture(t)
	snapshot, err := NewSnapshot(plan, options.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteSnapshot(options.SnapshotPath, snapshot); err != nil {
		t.Fatal(err)
	}
	if _, err := NewRegistry(t.Context(), db, snapshot); err != nil {
		t.Fatal(err)
	}
	for i, agent := range []string{"builder", "main"} {
		if err := db.CreateSeat(t.Context(), store.Seat{RunID: options.RunID, AgentID: agent, Generation: 3, Current: true, Tool: plan.Seats[i].Tool, NativeSessionID: agent, SurfaceID: agent, WorkspaceID: agent, Status: Ready}); err != nil {
			t.Fatal(err)
		}
	}
	r := ReadStatus(t.Context(), db, QueryOptions{RunID: options.RunID, SnapshotPath: options.SnapshotPath, LockPath: filepath.Join(filepath.Dir(options.SnapshotPath), "coordinator.lock")})
	if r.Run.WorkspaceID != "" {
		t.Fatalf("chose ambiguous workspace %+v", r)
	}
	found := false
	for _, d := range r.Diagnostics {
		if d.Code == "WORKSPACE_CONFLICT" && d.Path == "run.workspace_id" {
			found = true
		}
	}
	if !found || QueryAttach(r).Attached {
		t.Fatalf("%+v", r)
	}
}
