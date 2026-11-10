package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"sync"
	"testing"

	"agentflow/internal/config"
	"agentflow/internal/loopcore"
)

func workflowStore(t *testing.T) (*Store, string) {
	return workflowStoreWithSession(t, "")
}

func workflowStoreWithSession(t *testing.T, session string) (*Store, string) {
	t.Helper()
	c, err := config.Load(filepath.Join("..", "..", "examples", "workflow.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if session != "" {
		c.Defaults.Session = &session
	}
	r, err := config.Resolve(c, config.ResolveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "state.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateRun(t.Context(), Run{RunID: "workflow", ConfigSnapshot: string(body), ConfigDigest: r.Digest}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"main", "builder", "reviewer"} {
		if err := s.CreateSeat(t.Context(), Seat{RunID: "workflow", AgentID: id, Generation: 1, Current: true, Status: "READY"}); err != nil {
			t.Fatal(err)
		}
	}
	return s, path
}

func TestWorkflowSessionDispatchNeverRelocatesQueuedGeneration(t *testing.T) {
	s, path := workflowStoreWithSession(t, "persistent")
	e := workflowEvent(loopcore.Cursor{}, "start", "main")
	r, err := s.AdvanceWorkflowWithSessions(t.Context(), MessageActor{"workflow", "main", 1}, e)
	if err != nil {
		t.Fatal(err)
	}
	w, err := s.SessionWork(t.Context(), r.Message.ID)
	if err != nil || w.Record.Session != "persistent" {
		t.Fatal(w, err)
	}
	_, claimed, err := s.BeginSessionAction(t.Context(), r.Message.ID, w.Seat, "submit", "INPUT_DRAFT")
	if err != nil || claimed {
		t.Fatal(claimed, err)
	}
	if _, err := s.db.Exec(`UPDATE seats SET is_current = 0 WHERE agent_id = 'builder'`); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateSeat(t.Context(), Seat{RunID: "workflow", AgentID: "builder", Generation: 2, Current: true, Status: "READY"}); err != nil {
		t.Fatal(err)
	}
	other, err := OpenExisting(path, true)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	d, err := other.ReconcileSession(t.Context(), MessageActor{"workflow", "main", 1}, r.Message.ID)
	if err != nil || d.State != "REJECTED_OLD_GENERATION" || d.Delivery.TargetGeneration != 1 {
		t.Fatal("workflow queued dispatch relocated", d, err)
	}
	w, err = other.SessionWork(t.Context(), r.Message.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, claimed, err = other.BeginSessionAction(t.Context(), r.Message.ID, w.Seat, "submit", "")
	var public *MessagingError
	if claimed || !errors.As(err, &public) || public.Code != "GENERATION_MISMATCH" {
		t.Fatal("old workflow dispatch claimed", claimed, err)
	}
}

func TestWorkflowSessionActionFencesSupersededCursor(t *testing.T) {
	s, _ := workflowStoreWithSession(t, "persistent")
	r, err := s.AdvanceWorkflowWithSessions(t.Context(), MessageActor{"workflow", "main", 1}, workflowEvent(loopcore.Cursor{}, "start", "main"))
	if err != nil {
		t.Fatal(err)
	}
	w, err := s.SessionWork(t.Context(), r.Message.ID)
	if err != nil {
		t.Fatal(err)
	}
	e := workflowEvent(r.Cursor, "blocked", "builder")
	e.Reason = "need operator decision"
	_, err = s.ReplyWorkflowWithSessions(t.Context(), MessageCommand{Actor: MessageActor{"workflow", "builder", 1}, RequestID: e.ID,
		Kind: "reply", ReplyTo: e.MessageID, Body: "blocked", ReportStatus: "BLOCKED_CONFIRM"}, e)
	if err != nil {
		t.Fatal(err)
	}
	_, claimed, err := s.BeginSessionAction(t.Context(), r.Message.ID, w.Seat, "submit", "")
	var public *MessagingError
	if claimed || !errors.As(err, &public) || public.Code != "WORKFLOW_INVALID_TRANSITION" {
		t.Fatal("paused dispatch claimed", claimed, err)
	}
}

func workflowEvent(c loopcore.Cursor, kind, actor string) loopcore.Event {
	return loopcore.Event{ID: fmt.Sprintf("event-%d", c.Revision+1), Revision: c.Revision, Kind: kind, Actor: actor, MessageID: c.DispatchID}
}

func workflowTick(t *testing.T, s *Store, e loopcore.Event) WorkflowResult {
	t.Helper()
	r, err := s.AdvanceWorkflow(t.Context(), MessageActor{RunID: "workflow", AgentID: e.Actor, Generation: 1}, e)
	if err != nil {
		t.Fatalf("%s: %v", e.Kind, err)
	}
	return r
}

func TestWorkflowFakeThreeRolesReopenAndIdempotency(t *testing.T) {
	s, path := workflowStore(t)
	start := workflowEvent(loopcore.Cursor{}, "start", "main")
	r := workflowTick(t, s, start)
	if r.Message == nil || r.Message.Recipient != "builder" || r.Cursor.Phase != loopcore.Executing {
		t.Fatal(r)
	}
	retry := workflowTick(t, s, start)
	if !reflect.DeepEqual(r, retry) {
		t.Fatalf("retry changed result: %+v %+v", r, retry)
	}
	changed := start
	changed.Reason = "different"
	if _, err := s.AdvanceWorkflow(t.Context(), MessageActor{"workflow", "main", 1}, changed); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatal(err)
	}
	for _, pair := range []struct {
		executor, reviewer string
		criteria           int
	}{{"builder", "reviewer", 2}, {"reviewer", "main", 1}} {
		// Fake worker consumes the durable outbox and sends a formal handoff.
		d, err := s.GetDelivery(t.Context(), r.Message.ID, 1)
		if err != nil || d.Phase != "PENDING" || d.ChannelID != "UNVERIFIED" {
			t.Fatal(d, err)
		}
		e := workflowEvent(r.Cursor, "handoff", pair.executor)
		e.Handoff = &loopcore.Handoff{Summary: "work saved", Evidence: []string{"fake/tests.log"}, Next: "review saved work"}
		r = workflowTick(t, s, e)
		if r.Cursor.Phase != loopcore.Reviewing || r.Message.Recipient != pair.reviewer {
			t.Fatal(r)
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		s, err = OpenExisting(path, true)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		cursor, err := s.WorkflowCursor(t.Context(), "workflow")
		if err != nil || !reflect.DeepEqual(cursor, r.Cursor) {
			t.Fatal(cursor, err)
		}
		if replay := workflowTick(t, s, e); !reflect.DeepEqual(replay, r) {
			t.Fatal("reopen replay changed result")
		}
		e = workflowEvent(cursor, "approve", pair.reviewer)
		e.Accepted = make([]bool, pair.criteria)
		for i := range e.Accepted {
			e.Accepted[i] = true
		}
		r = workflowTick(t, s, e)
	}
	if r.Cursor.Phase != loopcore.Completed || r.Cursor.Step != "done" || r.Message != nil {
		t.Fatal(r)
	}
	var count int
	if err := s.db.QueryRow(`SELECT count(*) FROM messages`).Scan(&count); err != nil || count != 4 {
		t.Fatal(count, err)
	}
	entries, err := s.JournalAfter(t.Context(), "workflow", 0)
	if err != nil || len(entries) != 9 {
		t.Fatal(len(entries), err)
	}
}

func TestWorkflowAtomicRollbackAtEveryWrite(t *testing.T) {
	for _, point := range []struct{ name, trigger string }{
		{"message", `CREATE TRIGGER fail_workflow BEFORE INSERT ON messages BEGIN SELECT RAISE(ABORT, 'fault'); END`},
		{"delivery", `CREATE TRIGGER fail_workflow BEFORE INSERT ON deliveries BEGIN SELECT RAISE(ABORT, 'fault'); END`},
		{"dispatch journal", `CREATE TRIGGER fail_workflow BEFORE INSERT ON journal WHEN NEW.event = 'WORKFLOW_DISPATCH_STORED' BEGIN SELECT RAISE(ABORT, 'fault'); END`},
		{"cursor journal", `CREATE TRIGGER fail_workflow BEFORE INSERT ON journal WHEN NEW.event = 'WORKFLOW_ADVANCED' BEGIN SELECT RAISE(ABORT, 'fault'); END`},
	} {
		t.Run(point.name, func(t *testing.T) {
			s, _ := workflowStore(t)
			if _, err := s.db.Exec(point.trigger); err != nil {
				t.Fatal(err)
			}
			e := workflowEvent(loopcore.Cursor{}, "start", "main")
			if _, err := s.AdvanceWorkflowWithSessions(t.Context(), MessageActor{"workflow", "main", 1}, e); err == nil {
				t.Fatal("fault did not fail")
			}
			for _, table := range []string{"messages", "deliveries", "journal"} {
				var count int
				if err := s.db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&count); err != nil || count != 0 {
					t.Fatal(table, count, err)
				}
			}
			c, err := s.WorkflowCursor(t.Context(), "workflow")
			if err != nil || c.Revision != 0 {
				t.Fatal(c, err)
			}
			if _, err := s.db.Exec(`DROP TRIGGER fail_workflow`); err != nil {
				t.Fatal(err)
			}
			if _, err := s.AdvanceWorkflowWithSessions(t.Context(), MessageActor{"workflow", "main", 1}, e); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestWorkflowConcurrentTicksAndStopFence(t *testing.T) {
	s, path := workflowStore(t)
	other, err := OpenExisting(path, true)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	e := workflowEvent(loopcore.Cursor{}, "start", "main")
	var wg sync.WaitGroup
	results := make(chan WorkflowResult, 2)
	errs := make(chan error, 2)
	for _, db := range []*Store{s, other} {
		wg.Add(1)
		go func(db *Store) {
			defer wg.Done()
			r, err := db.AdvanceWorkflow(t.Context(), MessageActor{"workflow", "main", 1}, e)
			results <- r
			errs <- err
		}(db)
	}
	wg.Wait()
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	r, duplicate := <-results, <-results
	if !reflect.DeepEqual(r, duplicate) {
		t.Fatal("concurrent retry differs")
	}
	if _, err := s.RequestStop(t.Context(), "workflow"); err != nil {
		t.Fatal(err)
	}
	e = workflowEvent(r.Cursor, "handoff", "builder")
	e.Handoff = &loopcore.Handoff{Summary: "done", Evidence: []string{"log"}, Next: "review"}
	if _, err := s.AdvanceWorkflow(t.Context(), MessageActor{"workflow", "builder", 1}, e); err == nil {
		t.Fatal("stop ignored")
	}
	c, err := s.WorkflowCursor(t.Context(), "workflow")
	if err != nil || !reflect.DeepEqual(c, r.Cursor) {
		t.Fatal(c, err)
	}
}

func TestWorkflowRejectsStaleGenerationAndWrongRoles(t *testing.T) {
	s, _ := workflowStore(t)
	r := workflowTick(t, s, workflowEvent(loopcore.Cursor{}, "start", "main"))
	e := workflowEvent(r.Cursor, "handoff", "builder")
	e.Handoff = &loopcore.Handoff{Summary: "done", Evidence: []string{"log"}, Next: "review"}
	if _, err := s.AdvanceWorkflow(t.Context(), MessageActor{"workflow", "reviewer", 1}, e); err == nil {
		t.Fatal("actor spoof accepted")
	}
	if _, err := s.db.Exec(`UPDATE seats SET is_current = 0 WHERE agent_id = 'builder'`); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateSeat(t.Context(), Seat{RunID: "workflow", AgentID: "builder", Generation: 2, Current: true}); err != nil {
		t.Fatal(err)
	}
	for _, gen := range []int64{1, 2} {
		if _, err := s.AdvanceWorkflow(t.Context(), MessageActor{"workflow", "builder", gen}, e); err == nil {
			t.Fatalf("generation %d accepted old dispatch", gen)
		}
	}
}

func TestWorkflowDurableChangesPauseAndExhaustion(t *testing.T) {
	s, path := workflowStore(t)
	r := workflowTick(t, s, workflowEvent(loopcore.Cursor{}, "start", "main"))
	e := workflowEvent(r.Cursor, "handoff", "builder")
	e.Handoff = &loopcore.Handoff{Summary: "done", Evidence: []string{"log"}, Next: "review"}
	r = workflowTick(t, s, e)
	e = workflowEvent(r.Cursor, "changes", "reviewer")
	e.Reason = "missing coverage"
	r = workflowTick(t, s, e)
	if r.Cursor.Attempt != 2 || r.Message.Recipient != "builder" {
		t.Fatal(r)
	}
	e = workflowEvent(r.Cursor, "blocked", "builder")
	e.Reason = "needs decision"
	r = workflowTick(t, s, e)
	if r.Cursor.Phase != loopcore.Paused || r.Message != nil {
		t.Fatal(r)
	}
	other, err := OpenExisting(path, true)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	e = workflowEvent(r.Cursor, "resume", "main")
	e.Reason = "decision saved"
	r = workflowTick(t, other, e)
	if r.Cursor.Attempt != 3 || r.Cursor.Phase != loopcore.Executing {
		t.Fatal(r)
	}
	e = workflowEvent(r.Cursor, "retry", "builder")
	e.Reason = "still failing"
	r = workflowTick(t, other, e)
	if r.Cursor.Phase != loopcore.Exhausted || r.Message != nil {
		t.Fatal(r)
	}
	e = workflowEvent(r.Cursor, "resume", "main")
	e.Reason = "new event cannot increase budget"
	if _, err := other.AdvanceWorkflow(t.Context(), MessageActor{"workflow", "main", 1}, e); err == nil {
		t.Fatal("exhausted workflow restarted")
	}
}

func TestWorkflowStaleRevisionAndFrozenConfig(t *testing.T) {
	s, _ := workflowStore(t)
	r := workflowTick(t, s, workflowEvent(loopcore.Cursor{}, "start", "main"))
	e := workflowEvent(r.Cursor, "handoff", "builder")
	e.Handoff = &loopcore.Handoff{Summary: "done", Evidence: []string{"log"}, Next: "review"}
	e.Revision = 0
	if _, err := s.AdvanceWorkflow(t.Context(), MessageActor{"workflow", "builder", 1}, e); err == nil {
		t.Fatal("stale revision accepted")
	}
	e.Revision = r.Cursor.Revision
	if _, err := s.db.Exec(`UPDATE runs SET config_snapshot = json_set(config_snapshot, '$.workflow.steps[0].max_attempts', 9)`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AdvanceWorkflow(t.Context(), MessageActor{"workflow", "builder", 1}, e); err == nil {
		t.Fatal("tampered config accepted")
	}
	c, err := s.WorkflowCursor(t.Context(), "workflow")
	if err != nil || !reflect.DeepEqual(c, r.Cursor) {
		t.Fatal(c, err)
	}
}

func TestWorkflowDifferentEventsCompeteForOneRevision(t *testing.T) {
	s, path := workflowStore(t)
	other, err := OpenExisting(path, true)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i, db := range []*Store{s, other} {
		wg.Add(1)
		go func(i int, db *Store) {
			defer wg.Done()
			e := loopcore.Event{ID: fmt.Sprintf("competitor-%d", i), Kind: "start", Actor: "main"}
			_, err := db.AdvanceWorkflow(t.Context(), MessageActor{"workflow", "main", 1}, e)
			errs <- err
		}(i, db)
	}
	wg.Wait()
	a, b := <-errs, <-errs
	if !((a == nil && errors.Is(b, loopcore.ErrTransition)) || (b == nil && errors.Is(a, loopcore.ErrTransition))) {
		t.Fatal(a, b)
	}
	for table, want := range map[string]int{"messages": 1, "deliveries": 1, "journal": 2} {
		var got int
		if err := s.db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&got); err != nil || got != want {
			t.Fatal(table, got, want, err)
		}
	}
	c, err := s.WorkflowCursor(t.Context(), "workflow")
	if err != nil || c.Revision != 1 {
		t.Fatal(c, err)
	}
}

func TestWorkflowStopAllowsExactReplayWithoutWrites(t *testing.T) {
	s, _ := workflowStore(t)
	e := workflowEvent(loopcore.Cursor{}, "start", "main")
	r := workflowTick(t, s, e)
	if _, err := s.RequestStop(t.Context(), "workflow"); err != nil {
		t.Fatal(err)
	}
	before, err := s.JournalAfter(t.Context(), "workflow", 0)
	if err != nil {
		t.Fatal(err)
	}
	if replay := workflowTick(t, s, e); !reflect.DeepEqual(replay, r) {
		t.Fatal("stop changed exact receipt")
	}
	after, err := s.JournalAfter(t.Context(), "workflow", 0)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("replay wrote journal", err)
	}
}

func TestWorkflowInvalidHandoffHasNoWrites(t *testing.T) {
	for _, h := range []*loopcore.Handoff{nil, {}, {Summary: " ", Evidence: []string{"log"}, Next: "review"}, {Summary: "done", Next: "review"}, {Summary: "done", Evidence: []string{" "}, Next: "review"}, {Summary: "done", Evidence: []string{"log"}, Next: "\n"}} {
		s, _ := workflowStore(t)
		r := workflowTick(t, s, workflowEvent(loopcore.Cursor{}, "start", "main"))
		e := workflowEvent(r.Cursor, "handoff", "builder")
		e.Handoff = h
		if _, err := s.AdvanceWorkflow(t.Context(), MessageActor{"workflow", "builder", 1}, e); !errors.Is(err, loopcore.ErrTransition) {
			t.Fatal(h, err)
		}
		c, err := s.WorkflowCursor(t.Context(), "workflow")
		if err != nil || !reflect.DeepEqual(c, r.Cursor) {
			t.Fatal(c, err)
		}
		for table, want := range map[string]int{"messages": 1, "deliveries": 1, "journal": 2} {
			var got int
			if err := s.db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&got); err != nil || got != want {
				t.Fatal(table, got, err)
			}
		}
	}
}

func TestWorkflowReviewBlockedResumeAndNextTaskFence(t *testing.T) {
	s, _ := workflowStore(t)
	r := workflowTick(t, s, workflowEvent(loopcore.Cursor{}, "start", "main"))
	handoff := func(actor string) {
		e := workflowEvent(r.Cursor, "handoff", actor)
		e.Handoff = &loopcore.Handoff{Summary: "saved", Evidence: []string{"log"}, Next: "review"}
		r = workflowTick(t, s, e)
	}
	handoff("builder")
	e := workflowEvent(r.Cursor, "blocked", "reviewer")
	e.Reason = "need review environment"
	r = workflowTick(t, s, e)
	e = workflowEvent(r.Cursor, "resume", "main")
	e.Reason = "environment restored"
	r = workflowTick(t, s, e)
	if r.Cursor.Attempt != 2 || r.Cursor.Phase != loopcore.Executing || r.Cursor.Handoff != nil || r.Message.Recipient != "builder" {
		t.Fatal(r)
	}
	for _, p := range []struct {
		executor, reviewer string
		criteria           int
	}{{"builder", "reviewer", 2}, {"reviewer", "main", 1}} {
		handoff(p.executor)
		e = workflowEvent(r.Cursor, "approve", p.reviewer)
		for range p.criteria {
			e.Accepted = append(e.Accepted, true)
		}
		r = workflowTick(t, s, e)
	}
	completed := r.Cursor
	e = workflowEvent(completed, "start", "main")
	e.Task = "next task"
	r = workflowTick(t, s, e)
	if r.Cursor.Revision != completed.Revision+1 || r.Cursor.Phase != loopcore.Executing || r.Message == nil || r.Message.Recipient != "builder" {
		t.Fatal("completed workflow did not accept next task", r)
	}
	before, _ := s.JournalAfter(t.Context(), "workflow", 0)
	for _, kind := range []string{"start", "resume", "approve", "retry"} {
		e = workflowEvent(r.Cursor, kind, "main")
		e.Reason = "active task cannot be replaced"
		if _, err := s.AdvanceWorkflow(t.Context(), MessageActor{"workflow", "main", 1}, e); err == nil {
			t.Fatal(kind, err)
		}
	}
	after, err := s.JournalAfter(t.Context(), "workflow", 0)
	if err != nil || !reflect.DeepEqual(before, after) || r.Cursor.Phase != loopcore.Executing {
		t.Fatal(r, err)
	}
}

func TestWorkflowReplyRollbackIncludesOrdinaryReply(t *testing.T) {
	for _, point := range []string{"MESSAGE_STORED", "WORKFLOW_DISPATCH_STORED", "WORKFLOW_ADVANCED"} {
		t.Run(point, func(t *testing.T) {
			s, _ := workflowStore(t)
			r := workflowTick(t, s, workflowEvent(loopcore.Cursor{}, "start", "main"))
			before, _ := s.JournalAfter(t.Context(), "workflow", 0)
			if _, err := s.db.Exec(fmt.Sprintf(`CREATE TRIGGER fail_workflow BEFORE INSERT ON journal WHEN NEW.event = '%s' BEGIN SELECT RAISE(ABORT, 'fault'); END`, point)); err != nil {
				t.Fatal(err)
			}
			e := workflowEvent(r.Cursor, "handoff", "builder")
			e.Handoff = &loopcore.Handoff{Summary: "saved", Evidence: []string{"log"}, Next: "review"}
			cmd := MessageCommand{Actor: MessageActor{"workflow", "builder", 1}, RequestID: e.ID, Kind: "reply", ReplyTo: e.MessageID, Body: "saved result", ReportStatus: "DONE"}
			if _, err := s.ReplyWorkflow(t.Context(), cmd, e); err == nil {
				t.Fatal("fault ignored")
			}
			after, err := s.JournalAfter(t.Context(), "workflow", 0)
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatal("journal changed", err)
			}
			for _, table := range []string{"messages", "deliveries"} {
				var count int
				if err := s.db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&count); err != nil || count != 1 {
					t.Fatal(table, count, err)
				}
			}
		})
	}
}
