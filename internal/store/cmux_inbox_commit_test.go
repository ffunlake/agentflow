package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestCMUXInboxFailedCommitDiscardsConnectionAndRecovers(t *testing.T) {
	s, c := cmuxStoreFixture(t)
	if _, claimed, err := s.BeginCMUXSend(t.Context(), c); err != nil || !claimed {
		t.Fatal(claimed, err)
	}
	a := builderMessageActor()
	binding, err := s.CMUXSurface(t.Context(), a)
	if err != nil {
		t.Fatal(err)
	}
	old := CMUXEvidence{ID: "history-A", Source: "lead", SourceSurface: "sf-main", SourceWorkspace: "ws", TargetSurface: binding.SurfaceID, TargetWorkspace: binding.WorkspaceID, Body: "first", State: "queued"}
	if err := s.AcceptCMUXInbox(t.Context(), a, binding, []CMUXEvidence{old}); err != nil {
		t.Fatal(err)
	}
	before, err := s.JournalAfter(t.Context(), a.RunID, 0)
	if err != nil {
		t.Fatal(err)
	}
	var path string
	if err := s.db.QueryRow(`SELECT file FROM pragma_database_list WHERE name='main'`).Scan(&path); err != nil {
		t.Fatal(err)
	}
	// The insert succeeds, but its deferred foreign key fails at COMMIT. A TEMP
	// marker also distinguishes a replacement physical connection from pool reuse.
	for _, query := range []string{
		`CREATE TEMP TABLE cmux_connection_marker (id INTEGER)`,
		`CREATE TRIGGER fail_cmux_commit AFTER INSERT ON journal WHEN NEW.event='CMUX_INBOX_EVENT' AND json_extract(NEW.details,'$.id')='history-C' BEGIN INSERT INTO journal (run_id,message_id,event,created_at) VALUES (NEW.run_id,'missing-message','COMMIT_FAULT',NEW.created_at); END`,
		`PRAGMA defer_foreign_keys=ON`,
	} {
		if _, err := s.db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	first, second := old, old
	first.ID, second.ID = "history-B", "history-C"
	batch := []CMUXEvidence{first, old, second}
	err = s.AcceptCMUXInbox(t.Context(), a, binding, batch)
	if err == nil || !strings.Contains(err.Error(), "FOREIGN KEY constraint failed") {
		t.Fatalf("expected failure at COMMIT, got %v", err)
	}
	if stats := s.db.Stats(); stats.OpenConnections != 0 {
		t.Fatalf("failed COMMIT connection was returned to the pool: %+v", stats)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	conn, err := s.db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var markers int
	if err := conn.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_temp_master WHERE name='cmux_connection_marker'`).Scan(&markers); err != nil || markers != 0 {
		t.Fatal("failed connection reused", markers, err)
	}
	if _, err := conn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		t.Fatal("replacement connection cannot BEGIN", err)
	}
	if _, err := conn.ExecContext(ctx, `ROLLBACK`); err != nil {
		t.Fatal(err)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	assertPragmas(t, s)
	assertCounts(t, s, 1, 1, len(before))
	after, err := s.JournalAfter(ctx, a.RunID, 0)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("failed commit changed durable history", after, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = OpenExisting(path, true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	assertCounts(t, s, 1, 1, len(before))
	after, err = s.JournalAfter(ctx, a.RunID, 0)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("reopen changed pre-commit history", after, err)
	}
	if _, err := s.db.ExecContext(ctx, `DROP TRIGGER fail_cmux_commit`); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := s.AcceptCMUXInbox(ctx, a, binding, batch); err != nil {
			t.Fatal("reopened store cannot accept/replay recovered batch", err)
		}
	}
	assertCounts(t, s, 1, 1, len(before)+2)
	after, err = s.JournalAfter(ctx, a.RunID, 0)
	if err != nil || !reflect.DeepEqual(before, after[:len(before)]) {
		t.Fatal("recovery changed historical receipts", after, err)
	}
}

// These wrappers execute real SQLite statements except at the selected fault.
// They observe physical close and the explicit cleanup context, including when
// the caller is canceled at the failed write/commit boundary.
type cmuxInboxFaultConnector struct {
	base  driver.Driver
	dsn   string
	fault *cmuxInboxFault
}

type cmuxInboxFault struct {
	sync.Mutex
	commitErr, rollbackErr, writeErr error
	cancel                           context.CancelFunc
	opened, closed                   int
	commitConn, rollbackConn         int
	rollbackLive                     bool
	rollbackBound                    time.Duration
	rollbackWait                     bool
	commitAfter                      bool
}

func (c *cmuxInboxFaultConnector) Connect(ctx context.Context) (driver.Conn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	conn, err := c.base.Open(c.dsn)
	if err != nil {
		return nil, err
	}
	c.fault.Lock()
	c.fault.opened++
	id := c.fault.opened
	c.fault.Unlock()
	return &cmuxInboxFaultConn{Conn: conn, fault: c.fault, id: id}, nil
}

func (c *cmuxInboxFaultConnector) Driver() driver.Driver { return c.base }

type cmuxInboxFaultConn struct {
	driver.Conn
	fault *cmuxInboxFault
	id    int
}

func (c *cmuxInboxFaultConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	return c.Conn.(driver.ConnBeginTx).BeginTx(ctx, opts)
}

func (c *cmuxInboxFaultConn) Close() error {
	err := c.Conn.Close()
	c.fault.Lock()
	c.fault.closed++
	c.fault.Unlock()
	return err
}

func (c *cmuxInboxFaultConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	return c.Conn.(driver.QueryerContext).QueryContext(ctx, query, args)
}

func (c *cmuxInboxFaultConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	f := c.fault
	f.Lock()
	var injected error
	var cancel context.CancelFunc
	wait := false
	after := false
	switch {
	case query == "COMMIT":
		f.commitConn = c.id
		injected, cancel = f.commitErr, f.cancel
		after = f.commitAfter
	case query == "ROLLBACK":
		f.rollbackConn, f.rollbackLive = c.id, ctx.Err() == nil
		if deadline, ok := ctx.Deadline(); ok {
			f.rollbackBound = time.Until(deadline)
		}
		injected, wait = f.rollbackErr, f.rollbackWait
	case strings.HasPrefix(query, "INSERT INTO journal"):
		injected, cancel = f.writeErr, f.cancel
	}
	f.Unlock()
	if wait {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if injected != nil {
		if after {
			if _, err := c.Conn.(driver.ExecerContext).ExecContext(ctx, query, args); err != nil {
				return nil, err
			}
		}
		if cancel != nil {
			cancel()
		}
		return nil, injected
	}
	return c.Conn.(driver.ExecerContext).ExecContext(ctx, query, args)
}

func TestCMUXInboxCleanupFailuresDiscardPhysicalConnection(t *testing.T) {
	commitErr := errors.New("injected COMMIT failure")
	rollbackErr := errors.New("injected ROLLBACK failure")
	writeErr := errors.New("injected journal failure")
	for _, mode := range []string{"commit", "commit-and-rollback", "commit-and-rollback-timeout", "write-and-rollback"} {
		t.Run(mode, func(t *testing.T) {
			s, _ := cmuxStoreFixture(t)
			a := builderMessageActor()
			binding, err := s.CMUXSurface(t.Context(), a)
			if err != nil {
				t.Fatal(err)
			}
			before, err := s.JournalAfter(t.Context(), a.RunID, 0)
			if err != nil {
				t.Fatal(err)
			}
			var path string
			if err := s.db.QueryRow(`SELECT file FROM pragma_database_list WHERE name='main'`).Scan(&path); err != nil {
				t.Fatal(err)
			}
			baseDriver := s.db.Driver()
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			fault := &cmuxInboxFault{commitErr: commitErr, cancel: cancel}
			wantErr := commitErr
			switch mode {
			case "commit-and-rollback":
				fault.rollbackErr = rollbackErr
			case "commit-and-rollback-timeout":
				fault.rollbackWait = true
			case "write-and-rollback":
				fault.commitErr, fault.writeErr, fault.rollbackErr = nil, writeErr, rollbackErr
				wantErr = writeErr
			}
			u := url.URL{Scheme: "file", Path: path}
			u.RawQuery = url.Values{"_pragma": {"foreign_keys(ON)", "busy_timeout(5000)"}, "_txlock": {"immediate"}}.Encode()
			s.db = sql.OpenDB(&cmuxInboxFaultConnector{base: baseDriver, dsn: u.String(), fault: fault})
			s.db.SetMaxOpenConns(1)
			s.db.SetMaxIdleConns(1)
			event := CMUXEvidence{ID: "new-A", Source: "lead", SourceSurface: "sf-main", SourceWorkspace: "ws", TargetSurface: binding.SurfaceID, TargetWorkspace: binding.WorkspaceID, Body: "new"}
			err = s.AcceptCMUXInbox(ctx, a, binding, []CMUXEvidence{event})
			if !errors.Is(err, wantErr) || ctx.Err() == nil {
				t.Fatalf("expected injected failure and canceled caller, got %v / %v", err, ctx.Err())
			}
			if fault.rollbackErr != nil && !errors.Is(err, rollbackErr) {
				t.Fatal("rollback failure lost", err)
			}
			if fault.rollbackWait && !errors.Is(err, context.DeadlineExceeded) {
				t.Fatal("rollback deadline lost", err)
			}
			fault.Lock()
			if fault.opened != 1 || fault.closed != 1 || fault.rollbackConn != 1 ||
				!fault.rollbackLive || fault.rollbackBound <= 0 || fault.rollbackBound > 2*time.Second ||
				(mode != "write-and-rollback" && fault.commitConn != fault.rollbackConn) {
				t.Errorf("cleanup: opened=%d closed=%d commitConn=%d rollbackConn=%d live=%t bound=%s",
					fault.opened, fault.closed, fault.commitConn, fault.rollbackConn, fault.rollbackLive, fault.rollbackBound)
			}
			fault.Unlock()
			if stats := s.db.Stats(); stats.OpenConnections != 0 {
				t.Fatal("cleanup returned failed physical connection to pool", stats)
			}
			// The same store must acquire a fresh connection and start a transaction,
			// even when explicit ROLLBACK itself failed or timed out.
			if _, err := s.CMUXSurface(t.Context(), a); err != nil {
				t.Fatal("replacement connection cannot BEGIN", err)
			}
			assertCounts(t, s, 0, 0, len(before))
			after, err := s.JournalAfter(t.Context(), a.RunID, 0)
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatal("uncommitted history survived physical close", after, err)
			}
			fault.Lock()
			if fault.opened != 2 || fault.closed != 1 {
				t.Errorf("expected a new physical connection, opened=%d closed=%d", fault.opened, fault.closed)
			}
			fault.Unlock()
		})
	}
}
