package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"agentflow/internal/ipc"
	"agentflow/internal/store"
)

func TestCMUXSourceEnvPinsSenderSurface(t *testing.T) {
	t.Setenv("CMUX_WORKSPACE_ID", "old-workspace")
	t.Setenv("CMUX_SURFACE_ID", "old-surface")
	env := cmuxSourceEnv(store.SurfaceRegistration{WorkspaceID: "target-workspace", SurfaceID: "target-surface"})
	values := map[string]string{}
	for _, entry := range env {
		if key, value, ok := strings.Cut(entry, "="); ok {
			values[key] = value
		}
	}
	for key, want := range map[string]string{
		"CMUX_WORKSPACE_ID": "target-workspace",
		"CMUX_TAB_ID":       "target-workspace",
		"CMUX_SURFACE_ID":   "target-surface",
		"CMUX_PANEL_ID":     "target-surface",
	} {
		if values[key] != want {
			t.Fatalf("%s=%q, want %q", key, values[key], want)
		}
	}
}

func transportFixture(t *testing.T, binary string) (*CMUXTransport, *store.Store, SendMessage, string) {
	t.Helper()
	log := fakeCMUXEnv(t, "json")
	plan, o := workspacePlan(t, binary, "nested")
	launched, err := LaunchCMUXWorkspace(t.Context(), plan, o)
	if err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(filepath.Join(filepath.Dir(log), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	snapshot, _ := NewSnapshot(plan, "cmux-test")
	if _, err := NewRegistry(t.Context(), db, snapshot); err != nil {
		t.Fatal(err)
	}
	bindings := []store.SurfaceRegistration{}
	for _, s := range launched.Surfaces {
		bindings = append(bindings, store.SurfaceRegistration{RunID: "cmux-test", AgentID: s.AgentID, Generation: 1, Tool: s.Tool, LaunchID: "launch-one", WorkspaceID: launched.WorkspaceID, WorkspaceRef: launched.WorkspaceRef, SurfaceID: s.SurfaceID, SurfaceRef: s.SurfaceRef, Status: "LAUNCHED", NativeReady: "UNVERIFIED"})
	}
	if err := db.RegisterCMUXSurfaces(t.Context(), bindings); err != nil {
		t.Fatal(err)
	}
	transport, err := NewCMUXTransport(db, binary)
	if err != nil {
		t.Fatal(err)
	}
	return transport, db, SendMessage{Actor: ipc.Actor{RunID: "cmux-test", AgentID: "zmain", Generation: 1}, RequestID: "request-one", Recipient: "abuilder", TargetGeneration: 1, Body: "--help --from intruder ' \" $(touch forbidden); `id`\n中文\t😀"}, log
}

func countNativeCalls(t *testing.T, log string) int {
	t.Helper()
	body, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Count(string(body), `"agent"`)
}

func TestCMUXTransportExactTargetArgvAndDurableRetry(t *testing.T) {
	binary := buildFakeCMUX(t)
	tr, db, c, log := transportFixture(t, binary)
	w, err := tr.Send(t.Context(), c)
	if err != nil || w.Record.Delivery.Phase != "CMUX_QUEUED" || w.Evidence == nil || w.Evidence.ExitCode != 0 || w.Evidence.ID != "native-message-1" || w.Evidence.Stderr == "" {
		t.Fatal(w, err)
	}
	if w.Binding.SurfaceID != "30000000-0000-0000-0000-000000000002" {
		t.Fatal(w.Binding)
	}
	body, _ := os.ReadFile(log)
	lines := strings.Split(strings.TrimSpace(string(body)), "\n")
	var args []string
	if json.Unmarshal([]byte(lines[len(lines)-1]), &args) != nil || !reflect.DeepEqual(args, []string{"agent", "message", w.Binding.SurfaceID, "--from", "agentflow", "--json", "--", c.Body}) {
		t.Fatalf("argv changed: %q", args)
	}
	// A new service over a reopened store must return the same receipt, without cmux.
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = store.OpenExisting(filepath.Join(filepath.Dir(log), "state.db"), true)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	tr2, _ := NewCMUXTransport(db, binary)
	tr = tr2
	again, err := tr2.Send(t.Context(), c)
	againJSON, _ := json.Marshal(again)
	originalJSON, _ := json.Marshal(w)
	if err != nil || string(againJSON) != string(originalJSON) || countNativeCalls(t, log) != 1 {
		t.Fatal(again, w, err)
	}
	c.Body += "changed"
	if _, err := tr.Send(t.Context(), c); !errors.Is(err, store.ErrIdempotencyConflict) {
		t.Fatal(err)
	}
	c.Body = w.Record.Message.Body
	c.Recipient = "reviewer"
	if _, err := tr.Send(t.Context(), c); !errors.Is(err, store.ErrIdempotencyConflict) {
		t.Fatal(err)
	}
	seat, err := db.GetSeat(t.Context(), "cmux-test", "abuilder", 1)
	if err != nil || seat.Status != "LAUNCHED" || seat.NativeSessionID != "" {
		t.Fatal(seat, err)
	}
	if countNativeCalls(t, log) != 1 {
		t.Fatal("duplicate side effect")
	}
}

func TestCMUXTransportDoesNotTreatUnownedQueueAsAgentDelivery(t *testing.T) {
	for _, mode := range []string{"message-no-agent", "message-no-agent-field"} {
		t.Run(mode, func(t *testing.T) {
			binary := buildFakeCMUX(t)
			tr, _, c, log := transportFixture(t, binary)
			t.Setenv("CMUX_FAKE_MODE", mode)

			w, err := tr.Send(t.Context(), c)
			if err != nil || w.Record.Delivery.Phase != "UNVERIFIED" || w.Evidence == nil ||
				(mode == "message-no-agent" && (w.Evidence.RecipientHasAgent == nil || *w.Evidence.RecipientHasAgent)) ||
				(mode == "message-no-agent-field" && w.Evidence.RecipientHasAgent != nil) {
				t.Fatalf("inactive recipient was accepted: %+v %v", w, err)
			}
			if got := countNativeCalls(t, log); got != 1 {
				t.Fatalf("native submit count=%d, want 1", got)
			}
			again, err := tr.Send(t.Context(), c)
			if err != nil || again.Record.Delivery.Phase != "UNVERIFIED" || countNativeCalls(t, log) != 1 {
				t.Fatalf("replay changed inactive queue: %+v %v", again, err)
			}
		})
	}
}

func TestCMUXTransportFailuresNeverReplay(t *testing.T) {
	binary := buildFakeCMUX(t)
	for _, mode := range []string{"message-fail", "message-invalid", "message-wrong-surface", "message-truncated", "message-delay"} {
		t.Run(mode, func(t *testing.T) {
			tr, db, c, log := transportFixture(t, binary)
			t.Setenv("CMUX_FAKE_MODE", mode)
			ctx := t.Context()
			cancel := func() {}
			if mode == "message-delay" {
				// Allow race-instrumented process startup to reach the fixture log;
				// the fixture then blocks for five seconds without sending a receipt.
				ctx, cancel = context.WithTimeout(ctx, 2*time.Second)
			}
			defer cancel()
			w, err := tr.Send(ctx, c)
			if err != nil || w.Record.Delivery.Phase != "UNVERIFIED" || w.Evidence == nil {
				t.Fatal(w, err)
			}
			if mode == "message-fail" && (w.Evidence.ExitCode != 7 || !strings.Contains(w.Evidence.Stderr, "refused") || !strings.Contains(w.Evidence.Stdout, "partial")) {
				t.Fatal(w.Evidence)
			}
			if mode == "message-truncated" && !w.Evidence.Truncated {
				t.Fatal("missing truncation")
			}
			t.Setenv("CMUX_FAKE_MODE", "json")
			again, err := tr.Send(t.Context(), c)
			if err != nil || again.Record.Delivery.Phase != "UNVERIFIED" || countNativeCalls(t, log) != 1 {
				t.Fatal(again, err)
			}
			events, err := db.JournalAfter(t.Context(), c.Actor.RunID, 0)
			if err != nil {
				t.Fatal(err)
			}
			found := 0
			for _, e := range events {
				if e.Event == "CMUX_SEND_RESULT" {
					found++
				}
			}
			if found != 1 {
				t.Fatal("missing durable evidence", events)
			}
		})
	}
}

func TestCMUXTransportGatesBeforeExecution(t *testing.T) {
	binary := buildFakeCMUX(t)
	for _, mode := range []string{"run", "actor", "old-generation", "future-generation", "old-target", "unbound", "stop", "cancel", "body", "shared-request"} {
		t.Run(mode, func(t *testing.T) {
			tr, db, c, log := transportFixture(t, binary)
			ctx := t.Context()
			switch mode {
			case "run":
				c.Actor.RunID = "wrong"
			case "actor":
				c.Actor.AgentID = "unknown"
			case "old-generation":
				c.Actor.Generation = 0
			case "future-generation":
				c.Actor.Generation = 2
			case "old-target":
				c.TargetGeneration = 2
			case "unbound":
				if err := db.CreateSeat(ctx, store.Seat{RunID: c.Actor.RunID, AgentID: "unbound", Generation: 1, Current: true, Tool: "codex", Status: "READY"}); err != nil {
					t.Fatal(err)
				}
				c.Recipient = "unbound"
			case "stop":
				if _, err := db.RequestStop(ctx, c.Actor.RunID); err != nil {
					t.Fatal(err)
				}
			case "cancel":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "body":
				c.Body = "-"
			case "shared-request":
				if _, err := db.StoreMessage(ctx, store.MessageCommand{Actor: messageActor(c.Actor), RequestID: c.RequestID, Recipient: c.Recipient, Kind: "send", Body: c.Body}); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := tr.Send(ctx, c); err == nil {
				t.Fatal("gate accepted", mode)
			}
			if countNativeCalls(t, log) != 0 {
				t.Fatal("native side effect before gate")
			}
		})
	}
}

func TestCMUXConcurrentSendAndPendingIntent(t *testing.T) {
	tr, db, c, log := transportFixture(t, buildFakeCMUX(t))
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := tr.Send(t.Context(), c); errs <- err }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if countNativeCalls(t, log) != 1 {
		t.Fatal("concurrent replay")
	}
	c.RequestID = "pending"
	w, claimed, err := db.BeginCMUXSend(t.Context(), store.MessageCommand{Actor: messageActor(c.Actor), RequestID: c.RequestID, Recipient: c.Recipient, Body: c.Body})
	if err != nil || !claimed {
		t.Fatal(w, claimed, err)
	}
	again, err := tr.Send(t.Context(), c)
	if err != nil || again.Record.Delivery.Phase != "CMUX_SEND_REQUESTED" || countNativeCalls(t, log) != 1 {
		t.Fatal(again, err)
	}
}

func TestCMUXInboxEventsExactSurfaceAndRejects(t *testing.T) {
	binary := buildFakeCMUX(t)
	for _, mode := range []string{"valid", "duplicate-batch", "wrong-surface", "unknown-source", "unknown-agentflow", "malformed", "duplicate-json", "empty"} {
		t.Run(mode, func(t *testing.T) {
			tr, db, c, log := transportFixture(t, binary)
			actor := ipc.Actor{RunID: c.Actor.RunID, AgentID: c.Recipient, Generation: 1}
			b, err := db.CMUXSurface(t.Context(), messageActor(actor))
			if err != nil {
				t.Fatal(err)
			}
			m := cmuxMessage{ID: "incoming-one", SenderName: "lead", SenderSurface: "30000000-0000-0000-0000-000000000001", SenderWorkspace: b.WorkspaceID, RecipientSurface: b.SurfaceID, RecipientWorkspace: b.WorkspaceID, Body: "result text\n中文", State: "queued"}
			switch mode {
			case "wrong-surface":
				m.RecipientSurface = m.SenderSurface
			case "unknown-source":
				m.SenderSurface = "30000000-0000-0000-0000-000000000999"
			case "unknown-agentflow":
				m.SenderName = "agentflow"
			}
			list := []cmuxMessage{m}
			if mode == "duplicate-batch" {
				list = append(list, m)
			}
			if mode == "empty" {
				list = []cmuxMessage{}
			}
			body, _ := json.Marshal(map[string]any{"messages": list})
			if mode == "malformed" {
				body = []byte(`{}`)
			}
			if mode == "duplicate-json" {
				body = []byte(`{"messages":[],"messages":[]}`)
			}
			file := filepath.Join(t.TempDir(), "inbox.json")
			if err := os.WriteFile(file, body, 0600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("CMUX_FAKE_INBOX", file)
			r, err := tr.Inbox(t.Context(), actor)
			valid := mode == "valid" || mode == "empty"
			if (err == nil) != valid {
				t.Fatal(r, err)
			}
			if valid && len(r.Messages) != len(list) {
				t.Fatal(r)
			}
			if mode == "valid" {
				if len(r.Messages[0].Raw) == 0 || r.Messages[0].ID != m.ID || r.Evidence.Stdout == "" {
					t.Fatal(r)
				}
				if _, err := tr.Inbox(t.Context(), actor); err != nil {
					t.Fatal("identical history must be idempotent", err)
				}
				seat, _ := db.GetSeat(t.Context(), actor.RunID, actor.AgentID, 1)
				if seat.Status != "LAUNCHED" || seat.NativeSessionID != "" {
					t.Fatal("inbox fabricated READY", seat)
				}
			}
			body, _ = os.ReadFile(log)
			if strings.Contains(string(body), "mark-read") || !strings.Contains(string(body), `"--surface","`+b.SurfaceID+`"`) {
				t.Fatal(string(body))
			}
		})
	}
}

func TestCMUXInboxMixedHistoryAfterReopen(t *testing.T) {
	binary := buildFakeCMUX(t)
	for _, reopen := range []bool{false, true} {
		for _, order := range []string{"old-first", "new-first"} {
			t.Run(fmt.Sprintf("reopen=%t/%s", reopen, order), func(t *testing.T) {
				tr, db, c, log := transportFixture(t, binary)
				actor := ipc.Actor{RunID: c.Actor.RunID, AgentID: c.Recipient, Generation: 1}
				binding, err := db.CMUXSurface(t.Context(), messageActor(actor))
				if err != nil {
					t.Fatal(err)
				}
				old := cmuxMessage{ID: "history-A", SenderName: "lead", SenderSurface: "30000000-0000-0000-0000-000000000001", SenderWorkspace: binding.WorkspaceID, RecipientSurface: binding.SurfaceID, RecipientWorkspace: binding.WorkspaceID, Body: "first message", State: "queued"}
				file := filepath.Join(t.TempDir(), "inbox.json")
				t.Setenv("CMUX_FAKE_INBOX", file)
				writeInbox := func(messages ...cmuxMessage) {
					t.Helper()
					body, err := json.Marshal(map[string]any{"messages": messages})
					if err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(file, body, 0600); err != nil {
						t.Fatal(err)
					}
				}
				writeInbox(old)
				if _, err := tr.Inbox(t.Context(), actor); err != nil {
					t.Fatal(err)
				}
				before, err := db.JournalAfter(t.Context(), actor.RunID, 0)
				if err != nil {
					t.Fatal(err)
				}
				if reopen {
					if err := db.Close(); err != nil {
						t.Fatal(err)
					}
					db, err = store.OpenExisting(filepath.Join(filepath.Dir(log), "state.db"), true)
					if err != nil {
						t.Fatal(err)
					}
					defer db.Close()
					tr, err = NewCMUXTransport(db, binary)
					if err != nil {
						t.Fatal(err)
					}
				}
				fresh := old
				fresh.ID, fresh.Body = "history-B", "new message"
				// Native delivery metadata can change without creating new work.
				old.State = "read"
				batch := []cmuxMessage{old, fresh}
				if order == "new-first" {
					batch = []cmuxMessage{fresh, old}
				}
				writeInbox(batch...)
				for range 2 {
					r, err := tr.Inbox(t.Context(), actor)
					if err != nil || len(r.Messages) != 2 || r.Messages[0].ID != batch[0].ID || r.Evidence.Stdout == "" {
						t.Fatal(r, err)
					}
				}
				after, err := db.JournalAfter(t.Context(), actor.RunID, 0)
				if err != nil || len(after) != len(before)+1 || !reflect.DeepEqual(before, after[:len(before)]) {
					t.Fatal("history changed or new event missing", after, err)
				}
				var accepted store.CMUXEvidence
				last := after[len(after)-1]
				if last.Event != "CMUX_INBOX_EVENT" || json.Unmarshal([]byte(last.Details), &accepted) != nil || accepted.ID != fresh.ID || accepted.Body != fresh.Body || len(accepted.Raw) == 0 {
					t.Fatal(last)
				}
			})
		}
	}
}
