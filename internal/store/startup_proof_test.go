package store

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestNativeReadyProofAndSeatCommitAtomically(t *testing.T) {
	s := newTestStore(t)
	if err := s.CreateRun(t.Context(), Run{RunID: "proof", ConfigSnapshot: "{}", ConfigDigest: "digest"}); err != nil {
		t.Fatal(err)
	}
	seat := Seat{RunID: "proof", AgentID: "solo", Generation: 1, Tool: "opencode", NativeSessionID: "session", WorkspaceID: "workspace", SurfaceID: "surface", Status: "READY", Current: true}
	for _, nonce := range []string{"", "wrong", strings.Repeat("A", 48)} {
		if err := s.RegisterStartupReadyWithNonce(t.Context(), seat, nonce); err == nil {
			t.Fatal("invalid nonce accepted")
		}
	}
	if _, err := s.db.Exec(`CREATE TRIGGER reject_ready_proof BEFORE INSERT ON journal WHEN NEW.event='STARTUP_READY_VERIFIED' BEGIN SELECT RAISE(ABORT,'fixture'); END`); err != nil {
		t.Fatal(err)
	}
	nonce := strings.Repeat("a", 48)
	if err := s.RegisterStartupReadyWithNonce(t.Context(), seat, nonce); err == nil {
		t.Fatal("proof failure accepted")
	}
	if _, err := s.GetSeat(t.Context(), "proof", "solo", 1); err == nil {
		t.Fatal("seat survived failed proof transaction")
	}
	if _, err := s.db.Exec(`DROP TRIGGER reject_ready_proof`); err != nil {
		t.Fatal(err)
	}
	if err := s.RegisterStartupReadyWithNonce(t.Context(), seat, nonce); err != nil {
		t.Fatal(err)
	}
	stored, err := s.GetSeat(t.Context(), "proof", "solo", 1)
	if err != nil {
		t.Fatal(err)
	}
	events, err := s.JournalAfter(t.Context(), "proof", 0)
	if err != nil || len(events) != 1 {
		t.Fatal(events, err)
	}
	var proof StartupReadyProof
	if json.Unmarshal([]byte(events[0].Details), &proof) != nil || proof.Nonce != nonce || proof.Seat != stored {
		t.Fatal(proof, stored)
	}
}
