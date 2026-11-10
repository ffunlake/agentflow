package store

import (
	"path/filepath"
	"testing"
	"time"
)

func TestBootstrapGateExposesPersistedUnavailableSeatAndFencesGeneration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	s := openTestStore(t, path)
	if err := s.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateRun(t.Context(), Run{RunID: "run-1", ConfigSnapshot: `{"lead":"main"}`, ConfigDigest: "digest", StopRequested: true}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateSeat(t.Context(), Seat{RunID: "run-1", AgentID: "builder", Generation: 1, Current: true, Tool: "codex", Status: "STARTING"}); err != nil {
		t.Fatal(err)
	}
	other := openTestStore(t, path)
	entered, release := make(chan struct{}), make(chan struct{})
	finished := make(chan error, 1)
	q := ReportContext{Actor: builderMessageActor(), ConfigDigest: "digest", Lead: "main"}
	go func() {
		finished <- s.WithBootstrapContext(t.Context(), q, func(run Run, seat Seat, _ func(ReportBinding) error) error {
			if !run.StopRequested || seat.Status != "STARTING" || seat.Tool != "codex" || seat.NativeSessionID != "" {
				t.Error("lost unavailable session state")
			}
			close(entered)
			<-release
			return nil
		})
	}()
	select {
	case <-entered:
	case err := <-finished:
		t.Fatalf("gate failed: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("gate did not enter")
	}
	changed := make(chan error, 1)
	go func() {
		_, err := other.db.ExecContext(t.Context(), `UPDATE seats SET is_current = 0 WHERE agent_id = 'builder'`)
		changed <- err
	}()
	select {
	case err := <-changed:
		close(release)
		t.Fatalf("generation changed during bootstrap: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	if err := <-changed; err != nil {
		t.Fatal(err)
	}
	called := false
	err := s.WithBootstrapContext(t.Context(), q, func(Run, Seat, func(ReportBinding) error) error { called = true; return nil })
	if err == nil || called {
		t.Fatal("invalidated actor reached bootstrap files")
	}
	if err := s.WithBootstrapContext(t.Context(), q, nil); err == nil {
		t.Fatal("nil operation accepted")
	}
}
