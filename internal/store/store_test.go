package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func openTestStore(t *testing.T, path string) *Store {
	t.Helper()
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s := openTestStore(t, filepath.Join(t.TempDir(), "state.db"))
	if err := s.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	return s
}

func seedStore(t *testing.T, s *Store) {
	t.Helper()
	if err := s.CreateRun(t.Context(), Run{RunID: "run-1", ConfigSnapshot: `{"name":"test"}`,
		ConfigDigest: "snapshot-digest", WorkspaceID: "workspace-1"}); err != nil {
		t.Fatal(err)
	}
	for _, agent := range []string{"main", "builder"} {
		if err := s.CreateSeat(t.Context(), Seat{RunID: "run-1", AgentID: agent, Generation: 1,
			Current: true, Tool: "opencode", NativeSessionID: "native-" + agent,
			WorkspaceID: "workspace-1", SurfaceID: "surface-" + agent, Status: "READY"}); err != nil {
			t.Fatal(err)
		}
	}
}

func testOutbox() Outbox {
	return Outbox{
		Message: Message{RunID: "run-1", ID: "message-1", Sender: "main", SenderGeneration: 1,
			Recipient: "builder", Kind: "assign", Body: "work ' ? ; DROP TABLE runs; --\n中文", RequestID: "request-1"},
		Delivery: Delivery{TargetGeneration: 1},
		Journal:  JournalEntry{EvidenceRef: "evidence/message-1.json", Details: "saved"},
	}
}

func assertCounts(t *testing.T, s *Store, messages, deliveries, journal int) {
	t.Helper()
	for _, check := range []struct {
		query string
		want  int
	}{
		{"SELECT count(*) FROM messages", messages},
		{"SELECT count(*) FROM deliveries", deliveries},
		{"SELECT count(*) FROM journal", journal},
	} {
		var got int
		if err := s.db.QueryRow(check.query).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != check.want {
			t.Fatalf("%s = %d, want %d", check.query, got, check.want)
		}
	}
}

func assertPragmas(t *testing.T, s *Store) {
	t.Helper()
	for _, check := range []struct{ query, want string }{
		{"PRAGMA foreign_keys", "1"},
		{"PRAGMA journal_mode", "wal"},
		{"PRAGMA synchronous", "2"},
	} {
		var got string
		if err := s.db.QueryRow(check.query).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != check.want {
			t.Fatalf("%s = %s, want %s", check.query, got, check.want)
		}
	}
}

func TestMigrateAndPragmas(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state ?#中文.db")
	s := openTestStore(t, path)
	for _, stage := range []string{"first", "repeat", "replacement connection", "reopened"} {
		t.Run(stage, func(t *testing.T) {
			if stage == "replacement connection" {
				// Drop the pooled physical connection; PRAGMAs must survive reconnect.
				s.db.SetMaxIdleConns(0)
			} else if stage == "reopened" {
				if err := s.Close(); err != nil {
					t.Fatal(err)
				}
				s = openTestStore(t, path)
			}
			if err := s.Migrate(t.Context()); err != nil {
				t.Fatal(err)
			}
			assertPragmas(t, s)
			var version int
			if err := s.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 1 {
				t.Fatalf("user_version = %d, err = %v", version, err)
			}
			rows, err := s.db.Query(`SELECT name FROM sqlite_schema WHERE type = 'table'
				AND name NOT LIKE 'sqlite_%' ORDER BY name`)
			if err != nil {
				t.Fatal(err)
			}
			var tables []string
			for rows.Next() {
				var name string
				if err := rows.Scan(&name); err != nil {
					t.Fatal(err)
				}
				tables = append(tables, name)
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			rows.Close()
			if want := []string{"deliveries", "journal", "messages", "runs", "seats"}; !reflect.DeepEqual(tables, want) {
				t.Fatalf("tables = %v, want %v", tables, want)
			}
			for _, name := range []string{"seats_current", "messages_request", "messages_inbox", "deliveries_retry", "journal_run_seq", "journal_message_seq"} {
				var count int
				if err := s.db.QueryRow("SELECT count(*) FROM sqlite_schema WHERE type = 'index' AND name = ?", name).Scan(&count); err != nil || count != 1 {
					t.Fatalf("index %s: count=%d err=%v", name, count, err)
				}
			}
		})
	}
}

func TestMigrationRollback(t *testing.T) {
	for _, name := range []string{"messages", "deliveries", "journal_no_delete"} {
		t.Run(name, func(t *testing.T) {
			s := openTestStore(t, filepath.Join(t.TempDir(), "state.db"))
			// A preexisting object causes failure after earlier DDL has executed.
			// Names are test constants; production statements never interpolate input.
			var statement string
			switch name {
			case "messages":
				statement = "CREATE TABLE messages (marker TEXT)"
			case "deliveries":
				statement = "CREATE TABLE deliveries (marker TEXT)"
			default:
				statement = "CREATE TABLE journal_no_delete (marker TEXT)"
			}
			if _, err := s.db.Exec(statement); err != nil {
				t.Fatal(err)
			}
			// A table and trigger may share a name, so use an index collision at
			// the last index creation to test failure late in the migration.
			if name == "journal_no_delete" {
				if _, err := s.db.Exec("CREATE INDEX journal_message_seq ON journal_no_delete(marker)"); err != nil {
					t.Fatal(err)
				}
			}
			if err := s.Migrate(t.Context()); err == nil {
				t.Fatal("expected migration failure")
			}
			var count, version int
			if err := s.db.QueryRow("SELECT count(*) FROM sqlite_schema WHERE type = 'table' AND name = 'runs'").Scan(&count); err != nil || count != 0 {
				t.Fatalf("partial migration: runs count=%d err=%v", count, err)
			}
			if err := s.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 0 {
				t.Fatalf("migration version = %d, err = %v", version, err)
			}
		})
	}
}

func TestFutureSchemaRejected(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.db.Exec("PRAGMA user_version = 99"); err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(t.Context()); err == nil {
		t.Fatal("expected future schema rejection")
	}
}

func TestOutboxPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	s := openTestStore(t, path)
	if err := s.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	seedStore(t, s)
	out := testOutbox()
	out.Message.CreatedAt = time.Date(2026, 11, 2, 14, 15, 16, 123, time.FixedZone("UTC+8", 8*60*60))
	out.Delivery.ChannelID = "native-channel-1"
	out.Delivery.ResetPhase = "RESET_REQUESTED"
	out.Delivery.Attempt = 2
	out.Delivery.NextRetryAt = out.Message.CreatedAt.Add(time.Minute)
	out.Delivery.LastErrorCode, out.Delivery.LastError = "UNAVAILABLE", "retry later"
	saved, err := s.SaveOutbox(t.Context(), out)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Seq < 1 || saved.Status != "STORED" || saved.CreatedAt.Location() != time.UTC {
		t.Fatalf("invalid saved message: %+v", saved)
	}
	assertCounts(t, s, 1, 1, 1)
	if err := s.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s = openTestStore(t, path)
	assertPragmas(t, s)
	got, err := s.GetMessage(t.Context(), saved.ID)
	if err != nil || !reflect.DeepEqual(got, saved) {
		t.Fatalf("reopened message = %+v, want %+v, err=%v", got, saved, err)
	}
	d, err := s.GetDelivery(t.Context(), saved.ID, 1)
	if err != nil || d.RunID != "run-1" || d.Recipient != "builder" || d.Phase != "PENDING" ||
		d.ResetPhase != out.Delivery.ResetPhase || d.ChannelID != out.Delivery.ChannelID || d.Attempt != 2 ||
		!d.NextRetryAt.Equal(out.Delivery.NextRetryAt) || d.NextRetryAt.Location() != time.UTC ||
		d.LastErrorCode != "UNAVAILABLE" || d.LastError != "retry later" || !d.CreatedAt.Equal(saved.CreatedAt) || !d.UpdatedAt.Equal(saved.CreatedAt) {
		t.Fatalf("reopened delivery = %+v, err=%v", d, err)
	}
	entries, err := s.JournalAfter(t.Context(), "run-1", 0)
	if err != nil || len(entries) != 1 || entries[0].MessageID != saved.ID || entries[0].Event != "MESSAGE_STORED" ||
		entries[0].EvidenceRef != out.Journal.EvidenceRef || entries[0].Details != "saved" {
		t.Fatalf("reopened journal = %+v, err=%v", entries, err)
	}
	r, err := s.GetRun(t.Context())
	if err != nil || r.ConfigDigest != "snapshot-digest" || r.ConfigSnapshot != `{"name":"test"}` || r.WorkspaceID != "workspace-1" {
		t.Fatalf("reopened run = %+v, err=%v", r, err)
	}
	seat, err := s.GetSeat(t.Context(), "run-1", "builder", 1)
	if err != nil || seat.NativeSessionID != "native-builder" || seat.SurfaceID != "surface-builder" || !seat.Current {
		t.Fatalf("reopened seat = %+v, err=%v", seat, err)
	}
	// Simulate a lost success response and retry after every connection closed.
	retry, err := s.SaveOutbox(t.Context(), out)
	if err != nil || !reflect.DeepEqual(retry, saved) {
		t.Fatalf("reopened retry = %+v, err=%v", retry, err)
	}
	assertCounts(t, s, 1, 1, 1)
}

func TestOutboxRollback(t *testing.T) {
	for _, stage := range []string{"message", "delivery", "journal"} {
		t.Run(stage, func(t *testing.T) {
			s := newTestStore(t)
			seedStore(t, s)
			out := testOutbox()
			switch stage {
			case "message":
				out.Message.Kind = ""
			case "delivery":
				out.Delivery.TargetGeneration = 99
			case "journal":
				out.Journal.AgentID, out.Journal.Generation = "missing-seat", 1
			}
			if _, err := s.SaveOutbox(t.Context(), out); err == nil {
				t.Fatal("expected insert failure")
			}
			assertCounts(t, s, 0, 0, 0)
			// The failed key and generated sequence must not prevent a retry.
			if _, err := s.SaveOutbox(t.Context(), testOutbox()); err != nil {
				t.Fatal(err)
			}
			assertCounts(t, s, 1, 1, 1)
		})
	}
}

func TestRequestIdempotency(t *testing.T) {
	for _, tc := range []struct {
		name     string
		mutate   func(*Outbox)
		conflict bool
		count    int
	}{
		{"identical", func(*Outbox) {}, false, 1},
		{"different body", func(o *Outbox) { o.Message.Body += " changed" }, true, 1},
		{"same body uses original envelope", func(o *Outbox) {
			o.Message.ID, o.Message.Recipient, o.Message.Kind = "another-id", "main", "reply"
			o.Delivery.TargetGeneration = 99
		}, false, 1},
		{"different sender", func(o *Outbox) { o.Message.ID, o.Message.Sender = "another-id", "builder" }, false, 2},
		{"different request", func(o *Outbox) { o.Message.ID, o.Message.RequestID = "another-id", "request-2" }, false, 2},
		{"no request", func(o *Outbox) { o.Message.ID, o.Message.RequestID = "", "" }, false, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestStore(t)
			seedStore(t, s)
			first, err := s.SaveOutbox(t.Context(), testOutbox())
			if err != nil {
				t.Fatal(err)
			}
			out := testOutbox()
			tc.mutate(&out)
			got, err := s.SaveOutbox(t.Context(), out)
			if tc.conflict {
				if !errors.Is(err, ErrIdempotencyConflict) || err.Error() != "IDEMPOTENCY_CONFLICT" {
					t.Fatalf("conflict = %v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			} else if tc.count == 1 && !reflect.DeepEqual(first, got) {
				t.Fatalf("retry changed saved message: %+v vs %+v", first, got)
			}
			assertCounts(t, s, tc.count, tc.count, tc.count)
		})
	}
}

func TestConcurrentRequestAcrossHandles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	s1 := openTestStore(t, path)
	if err := s1.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	seedStore(t, s1)
	s2 := openTestStore(t, path)
	start := make(chan struct{})
	results := make(chan Message, 8)
	errors := make(chan error, 8)
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			s := s1
			if i%2 != 0 {
				s = s2
			}
			out := testOutbox()
			out.Message.ID = ""
			m, err := s.SaveOutbox(t.Context(), out)
			results <- m
			errors <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	var first Message
	for m := range results {
		if first.ID == "" {
			first = m
		}
		if !reflect.DeepEqual(m, first) {
			t.Fatalf("concurrent retry produced distinct message: %+v, %+v", first, m)
		}
	}
	assertCounts(t, s1, 1, 1, 1)
}

func TestForeignKeys(t *testing.T) {
	for _, tc := range []struct {
		name string
		sql  string
		args []any
	}{
		{"seat run", `INSERT INTO seats(run_id, agent_id, generation, is_current, created_at) VALUES (?, 'orphan', 1, 1, 'now')`, []any{"absent"}},
		{"message run", `INSERT INTO messages(run_id, id, sender, recipient, kind, body, created_at) VALUES (?, 'orphan', 'human', 'builder', 'send', '', 'now')`, []any{"absent"}},
		{"message reply", `INSERT INTO messages(run_id, id, sender, recipient, kind, body, reply_to, created_at) VALUES ('run-1', 'orphan', 'human', 'builder', 'reply', '', ?, 'now')`, []any{"absent"}},
		{"message sender generation", `INSERT INTO messages(run_id, id, sender, sender_generation, recipient, kind, body, created_at) VALUES ('run-1', 'orphan', 'main', ?, 'builder', 'send', '', 'now')`, []any{99}},
		{"delivery message", `INSERT INTO deliveries(run_id, message_id, recipient, target_generation, created_at, updated_at) VALUES ('run-1', ?, 'builder', 1, 'now', 'now')`, []any{"absent"}},
		{"delivery generation", `INSERT INTO deliveries(run_id, message_id, recipient, target_generation, created_at, updated_at) VALUES ('run-1', 'message-1', 'builder', ?, 'now', 'now')`, []any{99}},
		{"delivery recipient mismatch", `UPDATE deliveries SET recipient = ? WHERE message_id = 'message-1'`, []any{"main"}},
		{"journal run", `INSERT INTO journal(run_id, event, created_at) VALUES (?, 'TEST', 'now')`, []any{"absent"}},
		{"journal message", `INSERT INTO journal(run_id, message_id, event, created_at) VALUES ('run-1', ?, 'TEST', 'now')`, []any{"absent"}},
		{"journal seat", `INSERT INTO journal(run_id, agent_id, generation, event, created_at) VALUES ('run-1', 'builder', ?, 'TEST', 'now')`, []any{99}},
		{"seat current message", `INSERT INTO seats(run_id, agent_id, generation, is_current, current_message_id, created_at) VALUES ('run-1', 'builder', 2, 0, ?, 'now')`, []any{"absent"}},
		{"referenced seat delete", `DELETE FROM seats WHERE agent_id = ?`, []any{"builder"}},
		{"referenced run delete", `DELETE FROM runs WHERE run_id = ?`, []any{"run-1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestStore(t)
			seedStore(t, s)
			if _, err := s.SaveOutbox(t.Context(), testOutbox()); err != nil {
				t.Fatal(err)
			}
			if _, err := s.db.Exec(tc.sql, tc.args...); err == nil || !strings.Contains(err.Error(), "FOREIGN KEY constraint failed") {
				t.Fatalf("expected foreign key rejection, got %v", err)
			}
		})
	}
}

func TestUniqueConstraints(t *testing.T) {
	for _, tc := range []struct {
		name string
		run  func(*Store, context.Context) error
	}{
		{"one run", func(s *Store, ctx context.Context) error {
			return s.CreateRun(ctx, Run{RunID: "run-2", ConfigSnapshot: "{}", ConfigDigest: "digest"})
		}},
		{"one current generation", func(s *Store, ctx context.Context) error {
			return s.CreateSeat(ctx, Seat{RunID: "run-1", AgentID: "builder", Generation: 2, Current: true})
		}},
		{"unique seat generation", func(s *Store, ctx context.Context) error {
			return s.CreateSeat(ctx, Seat{RunID: "run-1", AgentID: "builder", Generation: 1})
		}},
		{"unique message ID", func(s *Store, ctx context.Context) error {
			out := testOutbox()
			out.Message.RequestID = "different-request"
			_, err := s.SaveOutbox(ctx, out)
			return err
		}},
		{"unique delivery", func(s *Store, ctx context.Context) error {
			_, err := s.db.ExecContext(ctx, `INSERT INTO deliveries(run_id, message_id, recipient, target_generation, created_at, updated_at)
				VALUES ('run-1', 'message-1', 'builder', 1, 'now', 'now')`)
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestStore(t)
			seedStore(t, s)
			if _, err := s.SaveOutbox(t.Context(), testOutbox()); err != nil {
				t.Fatal(err)
			}
			if err := tc.run(s, t.Context()); err == nil {
				t.Fatal("expected uniqueness rejection")
			}
			assertCounts(t, s, 1, 1, 1)
		})
	}
}

func TestHistoricalSeatsAndReply(t *testing.T) {
	s := newTestStore(t)
	seedStore(t, s)
	if err := s.CreateSeat(t.Context(), Seat{RunID: "run-1", AgentID: "builder", Generation: 2, Current: false}); err != nil {
		t.Fatal(err)
	}
	first, err := s.SaveOutbox(t.Context(), testOutbox())
	if err != nil {
		t.Fatal(err)
	}
	out := Outbox{Message: Message{RunID: "run-1", Sender: "builder", SenderGeneration: 1,
		Recipient: "main", Kind: "reply", Body: "done", ReplyTo: first.ID, RequestID: "reply-1", ReportStatus: "DONE"},
		Delivery: Delivery{TargetGeneration: 1}}
	reply, err := s.SaveOutbox(t.Context(), out)
	if err != nil || reply.Seq <= first.Seq || reply.ReplyTo != first.ID || reply.ReportStatus != "DONE" {
		t.Fatalf("reply = %+v, err=%v", reply, err)
	}
	if got, err := s.SaveOutbox(t.Context(), out); err != nil || got.ID != reply.ID {
		t.Fatalf("reply retry = %+v, err=%v", got, err)
	}
	assertCounts(t, s, 2, 2, 2)
}

func TestJournalAppendOrder(t *testing.T) {
	s := newTestStore(t)
	seedStore(t, s)
	var seqs []int64
	for i, event := range []string{"STARTUP", "BINDING", "RECOVERING"} {
		// Deliberately reverse timestamps: sequence, not wall clock, defines order.
		seq, err := s.AppendJournal(t.Context(), JournalEntry{RunID: "run-1", Event: event,
			AgentID: "builder", Generation: 1, EvidenceRef: "evidence/" + event,
			CreatedAt: time.Date(2026, 11, 3-i, 0, 0, 0, 0, time.FixedZone("offset", 3600))})
		if err != nil {
			t.Fatal(err)
		}
		seqs = append(seqs, seq)
	}
	entries, err := s.JournalAfter(t.Context(), "run-1", seqs[0])
	if err != nil || len(entries) != 2 || entries[0].Seq != seqs[1] || entries[1].Seq != seqs[2] || entries[0].Event != "BINDING" {
		t.Fatalf("journal order = %+v, err=%v", entries, err)
	}
	for _, entry := range entries {
		if entry.CreatedAt.Location() != time.UTC || entry.EvidenceRef != "evidence/"+entry.Event {
			t.Fatalf("journal metadata = %+v", entry)
		}
	}
	for _, statement := range []string{"UPDATE journal SET details = 'changed'", "DELETE FROM journal"} {
		if _, err := s.db.Exec(statement); err == nil || !strings.Contains(err.Error(), "append-only") {
			t.Fatalf("journal mutation = %v", err)
		}
	}
	if seq, err := s.AppendJournal(t.Context(), JournalEntry{RunID: "run-1"}); err == nil || seq != 0 {
		t.Fatalf("empty event must fail, seq=%d err=%v", seq, err)
	}
	assertCounts(t, s, 0, 0, 3)
}

func TestErrorsAndCancellation(t *testing.T) {
	for _, path := range []string{"", ":memory:", "file:test?mode=memory"} {
		if s, err := Open(path); err == nil {
			s.Close()
			t.Fatalf("invalid path accepted: %q", path)
		}
	}
	if s, err := Open(filepath.Join(t.TempDir(), "missing", "state.db")); err == nil {
		s.Close()
		t.Fatal("missing parent accepted")
	}
	s := newTestStore(t)
	seedStore(t, s)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := s.SaveOutbox(ctx, testOutbox()); err == nil {
		t.Fatal("canceled outbox accepted")
	}
	assertCounts(t, s, 0, 0, 0)
	if _, err := s.GetMessage(t.Context(), "missing"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing message = %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveOutbox(t.Context(), testOutbox()); err == nil {
		t.Fatal("closed store accepted write")
	}
}
