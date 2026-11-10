package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"agentflow/internal/loopcore"
)

type workflowCommitCase struct {
	s      *Store
	event  string
	call   func(context.Context) (any, error)
	replay func(context.Context, any)
}

func workflowCommitFixture(t *testing.T, kind string) workflowCommitCase {
	t.Helper()
	if strings.HasSuffix(kind, "-replay") {
		c := workflowCommitFixture(t, strings.TrimSuffix(kind, "-replay"))
		if _, err := c.call(t.Context()); err != nil {
			t.Fatal(err)
		}
		return c
	}
	var c workflowCommitCase
	if kind == "start" {
		s, _ := workflowStore(t)
		c.s, c.event = s, workflowAdvanced
		c.call = func(ctx context.Context) (any, error) {
			return s.AdvanceWorkflow(ctx, MessageActor{"workflow", "main", 1}, workflowEvent(loopcore.Cursor{}, "start", "main"))
		}
	} else {
		s, start, reply, event, native := cmuxWorkflowStore(t)
		c.s = s
		switch kind {
		case "event", "reply", "cmux-reply":
			c.event = workflowAdvanced
			if _, err := s.db.Exec(`UPDATE seats SET status='BUSY',current_message_id=? WHERE agent_id='builder'`, start.Message.ID); err != nil {
				t.Fatal(err)
			}
			c.call = func(ctx context.Context) (any, error) {
				switch kind {
				case "event":
					return s.AdvanceWorkflow(ctx, reply.Actor, event)
				case "reply":
					return s.ReplyWorkflow(ctx, reply, event)
				default:
					return s.ReplyCMUXWorkflow(ctx, reply, event, native)
				}
			}
		case "claim", "result", "unknown-result":
			r, err := s.ReplyCMUXWorkflow(t.Context(), reply, event, native)
			if err != nil {
				t.Fatal(err)
			}
			a := MessageActor{"workflow", "main", 1}
			if kind == "claim" {
				c.event = "CMUX_SEND_REQUESTED"
				c.call = func(ctx context.Context) (any, error) {
					w, claimed, err := s.BeginCMUXDispatch(ctx, a, r.Message.ID)
					return struct {
						Work    CMUXTransportWork
						Claimed bool
					}{w, claimed}, err
				}
				c.replay = func(ctx context.Context, _ any) {
					w, claimed, err := s.BeginCMUXDispatch(ctx, a, r.Message.ID)
					if err != nil || claimed || w.Record.Delivery.Attempt != 1 {
						t.Fatal("durable claim replayed", w, claimed, err)
					}
				}
			} else {
				c.event = "CMUX_SEND_RESULT"
				w, claimed, err := s.BeginCMUXDispatch(t.Context(), a, r.Message.ID)
				if err != nil || !claimed {
					t.Fatal(claimed, err)
				}
				e := CMUXEvidence{ID: "native-review", Source: "agentflow", TargetSurface: w.Binding.SurfaceID, TargetWorkspace: w.Binding.WorkspaceID, Body: w.WireBody, State: "queued"}
				if kind == "unknown-result" {
					e.ID, e.ExitCode = "", -1
				}
				c.call = func(ctx context.Context) (any, error) { return s.FinishCMUXSend(ctx, a, w, e) }
				c.replay = func(ctx context.Context, _ any) {
					if _, err := s.FinishCMUXSend(ctx, a, w, e); !errors.Is(err, ErrIdempotencyConflict) {
						t.Fatal("result recorded twice", err)
					}
					if _, claimed, err := s.BeginCMUXDispatch(ctx, a, r.Message.ID); err != nil || claimed {
						t.Fatal("completed/unknown send reclaimed", claimed, err)
					}
				}
			}
		default:
			t.Fatal("unknown case", kind)
		}
	}
	if c.replay == nil {
		c.replay = func(ctx context.Context, first any) {
			again, err := c.call(ctx)
			if err != nil || !reflect.DeepEqual(first, again) {
				t.Fatal("request retry changed receipt", first, again, err)
			}
		}
	}
	return c
}

// Include every column of each durable table, including cursor journal, reply,
// delivery attempt/phase and the reporting seat's BUSY state.
func workflowCommitSnapshot(t *testing.T, s *Store) string {
	t.Helper()
	var snapshot []any
	for _, table := range []string{"runs", "seats", "messages", "deliveries", "journal"} {
		rows, err := s.db.Query(`SELECT * FROM ` + table + ` ORDER BY rowid`)
		if err != nil {
			t.Fatal(err)
		}
		columns, err := rows.Columns()
		if err != nil {
			t.Fatal(err)
		}
		var records [][]any
		for rows.Next() {
			values, refs := make([]any, len(columns)), make([]any, len(columns))
			for i := range values {
				refs[i] = &values[i]
			}
			if err := rows.Scan(refs...); err != nil {
				t.Fatal(err)
			}
			records = append(records, values)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
		snapshot = append(snapshot, records)
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func workflowCommitPath(t *testing.T, s *Store) string {
	t.Helper()
	var path string
	if err := s.db.QueryRow(`SELECT file FROM pragma_database_list WHERE name='main'`).Scan(&path); err != nil {
		t.Fatal(err)
	}
	return path
}

func assertWorkflowNextBegin(t *testing.T, s *Store) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	conn, err := s.db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		t.Fatal("same Store next BEGIN failed", err)
	}
	if _, err := conn.ExecContext(ctx, `ROLLBACK`); err != nil {
		t.Fatal(err)
	}
}

func TestWorkflowFailedCommitDiscardsConnectionAndRecovers(t *testing.T) {
	for _, kind := range []string{"start", "event", "reply", "cmux-reply", "claim", "result", "unknown-result"} {
		t.Run(kind, func(t *testing.T) {
			c := workflowCommitFixture(t, kind)
			s := c.s
			path := workflowCommitPath(t, s)
			before := workflowCommitSnapshot(t, s)
			cursor, err := s.WorkflowCursor(t.Context(), "workflow")
			if err != nil {
				t.Fatal(err)
			}
			// Inserts succeed; only the actual SQLite COMMIT rejects the deferred FK.
			for _, q := range []string{
				`CREATE TRIGGER fail_workflow_commit AFTER INSERT ON journal WHEN NEW.event='` + c.event + `' BEGIN INSERT INTO journal (run_id,message_id,event,created_at) VALUES (NEW.run_id,'missing-message','COMMIT_FAULT',NEW.created_at); END`,
				`PRAGMA defer_foreign_keys=ON`,
			} {
				if _, err := s.db.Exec(q); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := c.call(t.Context()); err == nil || !strings.Contains(err.Error(), "FOREIGN KEY constraint failed") {
				t.Fatal("expected deferred FK COMMIT failure", err)
			}
			if stats := s.db.Stats(); stats.OpenConnections != 0 {
				t.Fatalf("failed COMMIT connection returned to pool: %+v", stats)
			}
			assertWorkflowNextBegin(t, s)
			if got := workflowCommitSnapshot(t, s); got != before {
				t.Fatal("rejected COMMIT left partial durable state", got, before)
			}
			if got, err := s.WorkflowCursor(t.Context(), "workflow"); err != nil || !reflect.DeepEqual(cursor, got) {
				t.Fatal("read uncommitted cursor/revision", got, err)
			}
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := OpenExisting(path, true)
			if err != nil {
				t.Fatal(err)
			}
			s.db = reopened.db // retain the Store captured by the original request
			if got := workflowCommitSnapshot(t, s); got != before {
				t.Fatal("reopen exposed rejected transaction")
			}
			if _, err := s.db.Exec(`DROP TRIGGER fail_workflow_commit`); err != nil {
				t.Fatal(err)
			}
			first, err := c.call(t.Context())
			if err != nil {
				t.Fatal("original request cannot recover", err)
			}
			wantRevision := cursor.Revision
			if c.event == workflowAdvanced {
				wantRevision++
			}
			recovered, err := s.WorkflowCursor(t.Context(), "workflow")
			if err != nil || recovered.Revision != wantRevision {
				t.Fatal("original request did not advance exactly once", recovered, err)
			}
			committed := workflowCommitSnapshot(t, s)
			c.replay(t.Context(), first)
			if workflowCommitSnapshot(t, s) != committed {
				t.Fatal("replay wrote duplicate state")
			}
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err = OpenExisting(path, true)
			if err != nil {
				t.Fatal(err)
			}
			s.db = reopened.db
			if got, err := s.WorkflowCursor(t.Context(), "workflow"); err != nil || !reflect.DeepEqual(recovered, got) || workflowCommitSnapshot(t, s) != committed {
				t.Fatal("reopen lost recovered durable revision/state", got, err)
			}
		})
	}
}

func installWorkflowCommitFault(t *testing.T, s *Store, f *cmuxInboxFault) {
	t.Helper()
	path, base := workflowCommitPath(t, s), s.db.Driver()
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	u := url.URL{Scheme: "file", Path: path}
	u.RawQuery = url.Values{"_pragma": {"foreign_keys(ON)", "busy_timeout(5000)"}, "_txlock": {"immediate"}}.Encode()
	s.db = sql.OpenDB(&cmuxInboxFaultConnector{base: base, dsn: u.String(), fault: f})
	s.db.SetMaxOpenConns(1)
	s.db.SetMaxIdleConns(1)
}

func TestWorkflowCommitCleanupFaults(t *testing.T) {
	for _, kind := range []string{"start", "event", "reply", "cmux-reply", "claim", "result", "unknown-result", "start-replay", "cmux-reply-replay", "claim-replay"} {
		for _, mode := range []string{"commit", "cancel", "rollback-failure", "rollback-timeout", "write-and-rollback"} {
			// Historical retries only read; the write failure would never fire.
			if strings.HasSuffix(kind, "-replay") && mode == "write-and-rollback" {
				continue
			}
			t.Run(kind+"/"+mode, func(t *testing.T) {
				c := workflowCommitFixture(t, kind)
				s := c.s
				before := workflowCommitSnapshot(t, s)
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				commitErr, rollbackErr, writeErr := errors.New("commit fault"), errors.New("rollback fault"), errors.New("write fault")
				f := &cmuxInboxFault{commitErr: commitErr}
				want := commitErr
				switch mode {
				case "cancel":
					f.commitErr, f.cancel, want = context.Canceled, cancel, context.Canceled
				case "rollback-failure":
					f.rollbackErr, f.cancel = rollbackErr, cancel
				case "rollback-timeout":
					f.rollbackWait, f.cancel = true, cancel
				case "write-and-rollback":
					f.commitErr, f.writeErr, f.rollbackErr, f.cancel = nil, writeErr, rollbackErr, cancel
					want = writeErr
				}
				installWorkflowCommitFault(t, s, f)
				if _, err := c.call(ctx); !errors.Is(err, want) ||
					(f.rollbackErr != nil && !errors.Is(err, rollbackErr)) ||
					(f.rollbackWait && !errors.Is(err, context.DeadlineExceeded)) {
					t.Fatal("original/cleanup error lost", err)
				}
				if mode != "commit" && ctx.Err() == nil {
					t.Fatal("fault did not cancel caller")
				}
				f.Lock()
				if f.opened != 1 || f.closed != 1 || f.rollbackConn != 1 || !f.rollbackLive || f.rollbackBound <= 0 || f.rollbackBound > 2*time.Second ||
					(mode != "write-and-rollback" && f.commitConn != f.rollbackConn) {
					t.Errorf("physical cleanup: %+v", f)
				}
				f.commitErr, f.writeErr, f.rollbackErr, f.cancel, f.rollbackWait = nil, nil, nil, nil, false
				f.Unlock()
				if stats := s.db.Stats(); stats.OpenConnections != 0 {
					t.Fatal("uncertain connection pooled", stats)
				}
				assertWorkflowNextBegin(t, s)
				if workflowCommitSnapshot(t, s) != before {
					t.Fatal("pre-COMMIT fault leaked uncommitted state")
				}
				first, err := c.call(t.Context())
				if err != nil {
					t.Fatal("same Store original request cannot recover", err)
				}
				after := workflowCommitSnapshot(t, s)
				c.replay(t.Context(), first)
				if workflowCommitSnapshot(t, s) != after {
					t.Fatal("recovered request duplicated writes")
				}
			})
		}
	}
}

// An error reported AFTER SQLite applied COMMIT is deliberately different from
// a rejected/deferred-FK commit. Durable receipts remain authoritative, including
// the claim that prevents another native send and the unknown-result fence.
func TestWorkflowAppliedCommitErrorPreservesDurableReceipts(t *testing.T) {
	for _, kind := range []string{"start", "event", "reply", "cmux-reply", "claim", "result", "unknown-result"} {
		t.Run(kind, func(t *testing.T) {
			c := workflowCommitFixture(t, kind)
			s, commitErr := c.s, errors.New("reported after applied COMMIT")
			path := workflowCommitPath(t, s)
			before, err := s.WorkflowCursor(t.Context(), "workflow")
			if err != nil {
				t.Fatal(err)
			}
			f := &cmuxInboxFault{commitErr: commitErr, commitAfter: true}
			installWorkflowCommitFault(t, s, f)
			first, err := c.call(t.Context())
			if !errors.Is(err, commitErr) {
				t.Fatal("commit error lost", err)
			}
			if stats := s.db.Stats(); stats.OpenConnections != 0 {
				t.Fatal("applied commit error connection pooled", stats)
			}
			f.Lock()
			if f.closed != 1 || f.commitConn != f.rollbackConn || !f.rollbackLive {
				t.Errorf("applied commit cleanup: %+v", f)
			}
			f.commitErr, f.commitAfter = nil, false
			f.Unlock()
			assertWorkflowNextBegin(t, s)
			cursor, err := s.WorkflowCursor(t.Context(), "workflow")
			if err != nil {
				t.Fatal(err)
			}
			wantRevision := before.Revision
			if c.event == workflowAdvanced {
				wantRevision++
			}
			if cursor.Revision != wantRevision {
				t.Fatal("applied revision was incorrectly assumed absent", cursor, before)
			}
			committed := workflowCommitSnapshot(t, s)
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := OpenExisting(path, true)
			if err != nil {
				t.Fatal(err)
			}
			s.db = reopened.db
			if got, err := s.WorkflowCursor(t.Context(), "workflow"); err != nil || !reflect.DeepEqual(cursor, got) {
				t.Fatal("durable revision changed on reopen", got, err)
			}
			c.replay(t.Context(), first)
			if workflowCommitSnapshot(t, s) != committed {
				t.Fatal("applied request replay wrote duplicate state")
			}
		})
	}
}
