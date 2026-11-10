package runtime

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agentflow/internal/config"
	"agentflow/internal/loopcore"
	"agentflow/internal/store"
)

// Replay recorded real Codex captures through the current parser. This is an
// offline protocol regression check, not another real native integration run.
func TestHeadlessCodexRecordedNativeProtocol(t *testing.T) {
	source, err := config.Load(filepath.Join("..", "..", "examples", "headless-codex.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	frozen, err := config.Resolve(source, config.ResolveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	a, err := NewHeadlessCodex(frozen, HeadlessCodexOptions{RunID: "run", RunDir: dir, Binary: "/unused/codex", TurnTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	files, err := filepath.Glob(filepath.Join("..", "..", "docs", "evidence", "headless-codex-v1-native-complete", "headless-result-*.json"))
	if err != nil || len(files) != 2 {
		t.Fatal("recorded native results missing", files, err)
	}
	for _, path := range files {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var capture headlessCapture
		if err := json.Unmarshal(b, &capture); err != nil {
			t.Fatal(err)
		}
		m := store.Message{RunID: "run", ID: capture.MessageID, Recipient: capture.Seat.AgentID, Kind: "assign"}
		if err := os.WriteFile(a.resultPath(m), b, 0600); err != nil {
			t.Fatal(err)
		}
		c := loopcore.Cursor{Revision: 1, Step: "nonce", Attempt: 1, Phase: loopcore.Executing, DispatchID: m.ID}
		if m.Recipient == "main" {
			c.Revision, c.Phase = 2, loopcore.Reviewing
		}
		reply, event, err := a.WorkflowReply(t.Context(), capture.Seat, m, c)
		if err != nil || reply.Status != "DONE" || !strings.Contains(reply.Body, "AGENTFLOW_HEADLESS_V1_NONCE") {
			t.Fatal(reply, event, err)
		}
		if m.Recipient == "builder" {
			if event.Kind != "handoff" || event.Handoff == nil || event.Handoff.Summary != "AGENTFLOW_HEADLESS_V1_NONCE" {
				t.Fatal(event)
			}
		} else if event.Kind != "approve" || len(event.Accepted) != 1 || !event.Accepted[0] {
			t.Fatal(event)
		}
	}
}
