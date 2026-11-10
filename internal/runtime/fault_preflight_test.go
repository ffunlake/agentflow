package runtime

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"agentflow/internal/config"
	"agentflow/internal/layout"
	"agentflow/internal/lock"
)

func TestFaultMatrixPreflight(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	for _, tc := range []struct{ fault, code string }{
		{"config-invalid", "$"},
		{"layout-invalid", "layout.agent"},
		{"snapshot-corrupt", SeatSnapshotInvalid},
		{"snapshot-permission", SeatSnapshotInvalid},
		{"lock-contention", "ALREADY_RUNNING"},
		{"lock-corrupt", "ALREADY_RUNNING"},
	} {
		t.Run(tc.fault, func(t *testing.T) {
			plan, db, opts := startupFixture(t)
			snapshot, err := NewSnapshot(plan, opts.RunID)
			if err != nil {
				t.Fatal(err)
			}
			if err := WriteSnapshot(opts.SnapshotPath, snapshot); err != nil {
				t.Fatal(err)
			}
			path := opts.SnapshotPath
			var owner *lock.Lock
			switch tc.fault {
			case "config-invalid":
				path = filepath.Join(filepath.Dir(path), "invalid.json")
				err = os.WriteFile(path, []byte(`{"version":1,`), 0600)
			case "layout-invalid":
				plan.Config.Layout = config.Layout{Agent: "unknown"}
			case "snapshot-corrupt":
				err = os.WriteFile(path, []byte(`{}`), 0600)
			case "snapshot-permission":
				err = os.Chmod(path, 0644)
			case "lock-contention", "lock-corrupt":
				path = filepath.Join(filepath.Dir(path), "coordinator.lock")
				if tc.fault == "lock-corrupt" {
					err = os.WriteFile(path, []byte("broken-owner"), 0600)
				} else {
					owner, err = lock.Acquire(path, lock.Owner{RunID: opts.RunID})
					if owner != nil {
						defer owner.Release()
					}
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			for range 2 {
				switch tc.fault {
				case "config-invalid", "layout-invalid":
					if tc.fault == "config-invalid" {
						_, err = config.Load(path)
					} else {
						_, err = layout.Compile(plan.Config)
					}
					var field *config.FieldError
					if !errors.As(err, &field) || field.Path != tc.code {
						t.Fatal("unstable field diagnosis", err)
					}
				case "snapshot-corrupt", "snapshot-permission":
					_, err = LoadStartupSeat(path, "builder")
					var public *SeatError
					if !errors.As(err, &public) || public.Code != tc.code {
						t.Fatal("unstable snapshot diagnosis", err)
					}
				default:
					contender, err := lock.Acquire(path, lock.Owner{RunID: "competing-run"})
					if contender != nil || !errors.Is(err, lock.ErrAlreadyRunning) || err.Error() != tc.code {
						t.Fatal("lock was stolen", contender, err)
					}
				}
			}
			after, err := os.ReadFile(path)
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatal("preflight failure overwrote source/owner", err)
			}
			journal, err := db.JournalAfter(t.Context(), opts.RunID, 0)
			if err != nil || len(journal) != 0 {
				t.Fatal("preflight created runtime actions", journal, err)
			}
		})
	}
}
