package store

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestReportGateSerializesGenerationChangesAcrossHandles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	s := openTestStore(t, path)
	if err := s.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateRun(t.Context(), Run{RunID: "run-1", ConfigSnapshot: `{"lead":"main"}`, ConfigDigest: "digest"}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"main", "builder"} {
		if err := s.CreateSeat(t.Context(), Seat{RunID: "run-1", AgentID: id, Generation: 1, Current: true}); err != nil {
			t.Fatal(err)
		}
	}
	other := openTestStore(t, path)
	entered, release := make(chan struct{}), make(chan struct{})
	finished := make(chan error, 1)
	q := ReportContext{Actor: builderMessageActor(), ConfigDigest: "digest", Lead: "main"}
	go func() {
		finished <- s.WithReportContext(t.Context(), q, func(check func(ReportBinding) error) error {
			if err := check(ReportBinding{Author: builderMessageActor()}); err != nil {
				return err
			}
			close(entered)
			<-release
			return check(ReportBinding{Author: builderMessageActor()})
		})
	}()
	select {
	case <-entered:
	case err := <-finished:
		t.Fatalf("gate failed before callback: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("gate did not enter callback")
	}
	changed := make(chan error, 1)
	go func() {
		_, err := other.db.ExecContext(t.Context(), `UPDATE seats SET is_current = 0 WHERE agent_id = 'builder'`)
		changed <- err
	}()
	select {
	case err := <-changed:
		close(release)
		t.Fatalf("generation authority changed during file operation: %v", err)
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
	err := s.WithReportContext(t.Context(), q, func(func(ReportBinding) error) error { called = true; return nil })
	requireMessageCode(t, err, "ACTOR_MISMATCH")
	if called {
		t.Fatal("invalidated caller reached file operation")
	}
}

func TestReportGateCallbackFailureReleasesTransaction(t *testing.T) {
	s := newTestStore(t)
	if err := s.CreateRun(t.Context(), Run{RunID: "run-1", ConfigSnapshot: `{"lead":"main"}`, ConfigDigest: "digest"}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateSeat(t.Context(), Seat{RunID: "run-1", AgentID: "main", Generation: 1, Current: true}); err != nil {
		t.Fatal(err)
	}
	q := ReportContext{Actor: mainMessageActor(), ConfigDigest: "digest", Lead: "main", Review: true}
	want := errors.New("file operation failed")
	if err := s.WithReportContext(t.Context(), q, func(func(ReportBinding) error) error { return want }); !errors.Is(err, want) {
		t.Fatal(err)
	}
	if err := s.WithReportContext(t.Context(), q, func(func(ReportBinding) error) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if err := s.WithReportContext(t.Context(), q, nil); err == nil {
		t.Fatal("nil operation accepted")
	}
}
