package runtime

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestPrivateSnapshotRoundTripAndNoOverwrite(t *testing.T) {
	plan, _, options := startupFixture(t)
	snapshot, err := NewSnapshot(plan, options.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteSnapshot(options.SnapshotPath, snapshot); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(options.SnapshotPath)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("snapshot mode: %v %v", info, err)
	}
	got, err := ReadSnapshot(options.SnapshotPath)
	if err != nil || !reflect.DeepEqual(snapshot, got) {
		t.Fatalf("round trip: %+v %v", got, err)
	}
	if err := WriteSnapshot(options.SnapshotPath, snapshot); err == nil {
		t.Fatal("overwrote immutable snapshot")
	}
	if err := os.Chmod(options.SnapshotPath, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadSnapshot(options.SnapshotPath); err == nil {
		t.Fatal("read public prompt snapshot")
	}
	link := filepath.Join(filepath.Dir(options.SnapshotPath), "link.json")
	if err := os.Symlink(options.SnapshotPath, link); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadSnapshot(link); err == nil {
		t.Fatal("accepted snapshot symlink")
	}
	if err := WriteSnapshot(link, snapshot); err == nil {
		t.Fatal("replaced snapshot symlink")
	}
	if err := os.Chmod(filepath.Dir(options.SnapshotPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := WriteSnapshot(filepath.Join(filepath.Dir(options.SnapshotPath), "public.json"), snapshot); err == nil {
		t.Fatal("wrote prompt data in a public directory")
	}
	if _, err := ReadSnapshot(options.SnapshotPath); err == nil {
		t.Fatal("read prompt data from a public directory")
	}
}

func TestSnapshotRejectsMalformedIdentityMetadataAndConfig(t *testing.T) {
	for _, stage := range []string{"version", "run", "seat-run", "agent", "generation", "tool", "cwd", "role", "permissions", "session", "status", "missing-seat", "digest", "config-change", "incomplete-native", "unbound-ready", "duplicate-native"} {
		t.Run(stage, func(t *testing.T) {
			plan, _, options := startupFixture(t)
			snapshot, err := NewSnapshot(plan, options.RunID)
			if err != nil {
				t.Fatal(err)
			}
			seat := &snapshot.Seats[0]
			switch stage {
			case "version":
				snapshot.Version = 2
			case "run":
				snapshot.RunID = ""
			case "seat-run":
				seat.RunID = "wrong"
			case "agent":
				seat.AgentID = "unknown"
			case "generation":
				seat.Generation = 0
			case "tool":
				seat.Tool = "sh"
			case "cwd":
				seat.Cwd = "relative"
			case "role":
				seat.Role = "wrong"
			case "permissions":
				seat.Permissions = "invalid"
			case "session":
				seat.Session = "invalid"
			case "status":
				seat.Status = "DONE"
			case "missing-seat":
				snapshot.Seats = snapshot.Seats[:1]
			case "digest":
				snapshot.Config.Digest = "wrong"
			case "config-change":
				a := snapshot.Config.Agents[seat.AgentID]
				a.Prompt = "modified prompt"
				snapshot.Config.Agents[seat.AgentID] = a
			case "incomplete-native":
				seat.Native = NativeBinding{SessionID: "session-only"}
			case "unbound-ready":
				seat.Status = Ready
			case "duplicate-native":
				seat.Native = fakeNative("one")
				snapshot.Seats[1].Native = seat.Native
			}
			if err := snapshot.Validate(); err == nil {
				t.Fatal("accepted invalid snapshot")
			}
			body, err := json.Marshal(snapshot)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(options.SnapshotPath, body, 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := ReadSnapshot(options.SnapshotPath); err == nil {
				t.Fatal("accepted invalid serialized snapshot")
			}
		})
	}
}

func TestSnapshotRejectsUnknownFieldsAndTrailingData(t *testing.T) {
	plan, _, options := startupFixture(t)
	snapshot, _ := NewSnapshot(plan, options.RunID)
	body, _ := json.Marshal(snapshot)
	for _, raw := range []string{`{"unknown":true,` + string(body[1:]), string(body) + `{}`, `null`, `{invalid}`} {
		if err := os.WriteFile(options.SnapshotPath, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadSnapshot(options.SnapshotPath); err == nil {
			t.Fatalf("accepted invalid snapshot encoding: %s", raw)
		}
	}
	if err := WriteSnapshot("relative.json", snapshot); err == nil {
		t.Fatal("accepted relative runtime path")
	}
}

func TestStartupSnapshotSecretsStayOutOfWrapperArguments(t *testing.T) {
	plan, db, options := startupFixture(t)
	// The resolved example already has prompt text; its serialized snapshot
	// must carry that text while launcher argv carries only artifact/agent refs.
	launcher := fakeLauncher{
		launch: func(_ context.Context, request LaunchRequest) (NativeBinding, error) {
			encoded, _ := json.Marshal(request.Wrapper)
			for _, agent := range plan.Config.Agents {
				if strings.Contains(string(encoded), agent.Prompt) || strings.Contains(string(encoded), agent.Role) {
					t.Fatal("prompt or role leaked into wrapper arguments")
				}
			}
			return fakeNative(request.Seat.AgentID), nil
		},
		wait: func(_ context.Context, seat SeatSnapshot) (Registration, error) { return readyClaim(seat), nil },
	}
	if _, err := Start(t.Context(), plan, options, db, launcher); err != nil {
		t.Fatal(err)
	}
}
