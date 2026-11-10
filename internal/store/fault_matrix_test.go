package store

import (
	"errors"
	"path/filepath"
	"reflect"
	"testing"
)

func TestFaultMatrixSQLiteRollback(t *testing.T) {
	for _, table := range []string{"messages", "deliveries", "journal"} {
		t.Run(table, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "state.db")
			s := openTestStore(t, path)
			if err := s.Migrate(t.Context()); err != nil {
				t.Fatal(err)
			}
			seedStore(t, s)
			original, err := s.SaveOutbox(t.Context(), testOutbox())
			if err != nil {
				t.Fatal(err)
			}
			// Inject at every stage of the message/delivery/journal transaction.
			if _, err := s.db.Exec(`CREATE TRIGGER s7_fail BEFORE INSERT ON ` + table + ` BEGIN SELECT RAISE(ABORT, 's7 matrix failure'); END`); err != nil {
				t.Fatal(err)
			}
			out := testOutbox()
			out.Message.ID, out.Message.RequestID = "second", "second-request"
			for range 2 {
				_, err := s.SaveOutbox(t.Context(), out)
				var code interface{ Code() int }
				if !errors.As(err, &code) || code.Code() != 1811 { // SQLITE_CONSTRAINT_TRIGGER
					t.Fatal("unstable injected SQLite failure", err)
				}
				assertCounts(t, s, 1, 1, 1)
			}
			if _, err := s.db.Exec(`DROP TRIGGER s7_fail`); err != nil {
				t.Fatal(err)
			}
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			s = openTestStore(t, path)
			got, err := s.GetMessage(t.Context(), original.ID)
			if err != nil || !reflect.DeepEqual(original, got) {
				t.Fatal("previously committed body lost", got, err)
			}
			first, err := s.SaveOutbox(t.Context(), out)
			if err != nil {
				t.Fatal(err)
			}
			again, err := s.SaveOutbox(t.Context(), out)
			if err != nil || !reflect.DeepEqual(first, again) {
				t.Fatal("retry after rollback was not idempotent", again, err)
			}
			assertCounts(t, s, 2, 2, 2)
		})
	}
}
