package runtime

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadStartupSeatBoundary(t *testing.T) {
	for _, stage := range []string{"valid", "relative", "unknown", "bad-agent", "missing", "public-file", "public-dir", "symlink", "directory", "unknown-field", "trailing", "decode-secret", "digest", "tool", "run", "cwd", "state", "generation-zero", "generation-future", "ready", "bound-starting", "other-generation"} {
		t.Run(stage, func(t *testing.T) {
			plan := cmuxPlan(t, "yaml")
			snapshot, err := NewSnapshot(plan, "seat-run")
			if err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			if err := os.Chmod(dir, 0700); err != nil {
				t.Fatal(err)
			}
			path, id, code := filepath.Join(dir, "snapshot.json"), "builder", SeatSnapshotInvalid
			switch stage {
			case "valid":
				code = ""
			case "unknown":
				id, code = "unknown", SeatAgentUnknown
			case "bad-agent":
				id, code = "--agent", SeatAgentUnknown
			case "digest":
				snapshot.Config.Digest = "wrong"
			case "tool":
				snapshot.Seats[0].Tool = "sh"
			case "run":
				snapshot.Seats[0].RunID = "wrong"
			case "cwd":
				snapshot.Seats[0].Cwd = "relative"
			case "state":
				snapshot.Seats[0].Status = "STOPPED"
			case "generation-zero":
				snapshot.Seats[0].Generation = 0
			case "generation-future":
				snapshot.Seats[0].Generation, code = 2, SeatStartupInvalid
			case "other-generation":
				snapshot.Seats[1].Generation, code = 2, SeatStartupInvalid
			case "ready":
				snapshot.Seats[0].Status, snapshot.Seats[0].Native, code = Ready, fakeNative("builder"), SeatStartupInvalid
			case "bound-starting":
				snapshot.Seats[0].Native, code = fakeNative("builder"), SeatStartupInvalid
			}
			body, err := json.Marshal(snapshot)
			if err != nil {
				t.Fatal(err)
			}
			switch stage {
			case "unknown-field":
				body = append([]byte(`{"fixture-secret":true,`), body[1:]...)
			case "trailing":
				body = append(body, []byte(`{}`)...)
			case "decode-secret":
				body = []byte(`{"version":"fixture-secret"}`)
			}
			if err := os.WriteFile(path, body, 0600); err != nil {
				t.Fatal(err)
			}
			switch stage {
			case "relative":
				path = "snapshot.json"
			case "missing":
				path = filepath.Join(dir, "missing.json")
			case "public-file":
				err = os.Chmod(path, 0644)
			case "public-dir":
				err = os.Chmod(dir, 0755)
			case "directory":
				path = dir
			case "symlink":
				link := filepath.Join(dir, "link.json")
				err = os.Symlink(path, link)
				path = link
			}
			if err != nil {
				t.Fatal(err)
			}
			seat, err := LoadStartupSeat(path, id)
			if code == "" {
				if err != nil || seat.AgentID != id || seat.Tool != plan.Config.Agents[id].Tool || seat.Generation != 1 || seat.Status != Starting || seat.Native != (NativeBinding{}) {
					t.Fatalf("valid startup rejected: %+v %v", seat, err)
				}
				return
			}
			var diagnostic *SeatError
			if !errors.As(err, &diagnostic) || diagnostic.Code != code || seat != (SeatSnapshot{}) || strings.Contains(err.Error(), "fixture-secret") {
				t.Fatalf("wrong rejection: %+v %v", seat, err)
			}
		})
	}
}
