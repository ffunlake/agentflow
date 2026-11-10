package store

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func cmuxRecipientActive() *bool {
	active := true
	return &active
}

func cmuxStoreFixture(t *testing.T) (*Store, MessageCommand) {
	t.Helper()
	s := newTestStore(t)
	if err := s.CreateRun(t.Context(), Run{RunID: "run-1", ConfigSnapshot: `{}`, ConfigDigest: "digest"}); err != nil {
		t.Fatal(err)
	}
	bindings := []SurfaceRegistration{
		{RunID: "run-1", AgentID: "main", Generation: 1, Tool: "codex", LaunchID: "launch", WorkspaceID: "ws", WorkspaceRef: "workspace:1", SurfaceID: "sf-main", SurfaceRef: "surface:1", Status: "LAUNCHED", NativeReady: "UNVERIFIED"},
		{RunID: "run-1", AgentID: "builder", Generation: 1, Tool: "codex", LaunchID: "launch", WorkspaceID: "ws", WorkspaceRef: "workspace:1", SurfaceID: "sf-builder", SurfaceRef: "surface:2", Status: "LAUNCHED", NativeReady: "UNVERIFIED"},
	}
	if err := s.RegisterCMUXSurfaces(t.Context(), bindings); err != nil {
		t.Fatal(err)
	}
	return s, MessageCommand{Actor: mainMessageActor(), Recipient: "builder", TargetGeneration: 1, RequestID: "cmux-send", Body: "work"}
}

func TestCMUXLateResultsKeepRawEvidenceAndRejectGenerationStop(t *testing.T) {
	for _, mode := range []string{"sender-generation", "target-generation", "stop", "binding"} {
		t.Run(mode, func(t *testing.T) {
			s, c := cmuxStoreFixture(t)
			w, claimed, err := s.BeginCMUXSend(t.Context(), c)
			if err != nil || !claimed {
				t.Fatal(w, claimed, err)
			}
			switch mode {
			case "sender-generation", "target-generation":
				id := "main"
				if mode == "target-generation" {
					id = "builder"
				}
				if _, err := s.db.Exec(`UPDATE seats SET is_current=0 WHERE agent_id=?`, id); err != nil {
					t.Fatal(err)
				}
				if err := s.CreateSeat(t.Context(), Seat{RunID: c.Actor.RunID, AgentID: id, Generation: 2, Current: true, Tool: "codex", Status: "UNVERIFIED"}); err != nil {
					t.Fatal(err)
				}
			case "stop":
				if _, err := s.RequestStop(t.Context(), c.Actor.RunID); err != nil {
					t.Fatal(err)
				}
			case "binding":
				if _, err := s.db.Exec(`UPDATE seats SET surface_id='other' WHERE agent_id='builder'`); err != nil {
					t.Fatal(err)
				}
			}
			e := CMUXEvidence{ID: "native-1", Source: "agentflow", TargetSurface: w.Binding.SurfaceID, TargetWorkspace: w.Binding.WorkspaceID, Body: c.Body, State: "queued", RecipientHasAgent: cmuxRecipientActive(), Stdout: "raw stdout", Stderr: "raw stderr", ExitCode: 0}
			finished, err := s.FinishCMUXSend(t.Context(), c.Actor, w, e)
			if err == nil || finished.Record.Delivery.Phase != "UNVERIFIED" {
				t.Fatal(finished, err)
			}
			events, err := s.JournalAfter(t.Context(), c.Actor.RunID, 0)
			if err != nil {
				t.Fatal(err)
			}
			found := 0
			for _, event := range events {
				if event.Event == "CMUX_SEND_RESULT" {
					var actual CMUXEvidence
					if json.Unmarshal([]byte(event.Details), &actual) != nil || actual.Stdout != e.Stdout || actual.Stderr != e.Stderr {
						t.Fatal(event)
					}
					found++
				}
			}
			if found != 1 {
				t.Fatal("lost late raw evidence")
			}
			if _, _, err := s.BeginCMUXSend(t.Context(), c); err == nil {
				t.Fatal("old generation/stop/binding retry passed")
			}
		})
	}
}

func TestCMUXUnknownLaunchCannotInventREADYOrSurface(t *testing.T) {
	s := newTestStore(t)
	if err := s.CreateRun(t.Context(), Run{RunID: "run-1", ConfigSnapshot: `{}`, ConfigDigest: "digest"}); err != nil {
		t.Fatal(err)
	}
	b := SurfaceRegistration{RunID: "run-1", AgentID: "main", Generation: 1, Tool: "codex", LaunchID: "attempt", Status: "UNVERIFIED", NativeReady: "UNVERIFIED"}
	if err := s.RegisterCMUXSurfaces(t.Context(), []SurfaceRegistration{b}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CMUXSurface(t.Context(), mainMessageActor()); err == nil {
		t.Fatal("unbound launch accepted")
	}
	b.Status = "READY"
	if err := s.RegisterCMUXSurfaces(t.Context(), []SurfaceRegistration{b}); err == nil {
		t.Fatal("invented READY")
	}
	b.Status = "LAUNCHED"
	b.NativeReady = "READY"
	if err := s.RegisterCMUXSurfaces(t.Context(), []SurfaceRegistration{b}); err == nil {
		t.Fatal("invented native readiness")
	}
	seat, err := s.GetSeat(t.Context(), "run-1", "main", 1)
	if err != nil || seat.Status != "UNVERIFIED" || seat.NativeSessionID != "" {
		t.Fatal(seat, err)
	}
}

func TestBindCMUXNativeSessionPromotesOnlyExactOpenCodeSurface(t *testing.T) {
	s := newTestStore(t)
	if err := s.CreateRun(t.Context(), Run{RunID: "run-1", ConfigSnapshot: `{}`, ConfigDigest: "digest"}); err != nil {
		t.Fatal(err)
	}
	b := SurfaceRegistration{RunID: "run-1", AgentID: "builder", Generation: 1, Tool: "opencode", LaunchID: "launch", WorkspaceID: "ws", WorkspaceRef: "workspace:1", SurfaceID: "sf-builder", SurfaceRef: "surface:2", Status: "LAUNCHED", NativeReady: "UNVERIFIED"}
	if err := s.RegisterCMUXSurfaces(t.Context(), []SurfaceRegistration{b}); err != nil {
		t.Fatal(err)
	}
	actor := MessageActor{"run-1", "builder", 1}
	binding, err := s.CMUXSurface(t.Context(), actor)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.BindCMUXNativeSession(t.Context(), actor, binding, "ses_builder"); err != nil {
		t.Fatal(err)
	}
	seat, err := s.GetSeat(t.Context(), actor.RunID, actor.AgentID, actor.Generation)
	if err != nil || seat.Status != "READY" || seat.NativeSessionID != "ses_builder" {
		t.Fatal(seat, err)
	}
	if err := s.BindCMUXNativeSession(t.Context(), actor, binding, "ses_builder"); err != nil {
		t.Fatal("exact binding was not idempotent", err)
	}
	if err := s.BindCMUXNativeSession(t.Context(), actor, binding, "ses_other"); err == nil {
		t.Fatal("native session identity changed")
	}
	wrong := binding
	wrong.SurfaceID = "30000000-0000-0000-0000-000000000099"
	if err := s.BindCMUXNativeSession(t.Context(), actor, wrong, "ses_builder"); err == nil {
		t.Fatal("wrong surface accepted")
	}
}

func TestCMUXReceiptCannotCoverUnknownSend(t *testing.T) {
	s, c := cmuxStoreFixture(t)
	w, claimed, err := s.BeginCMUXSend(t.Context(), c)
	if err != nil || !claimed {
		t.Fatal(w, claimed, err)
	}
	if _, err := s.Ack(t.Context(), builderMessageActor(), "ack-too-soon", w.Record.Message.ID); err == nil {
		t.Fatal("ACK covered pending native intent")
	}
	if _, err := s.StoreMessage(t.Context(), MessageCommand{Actor: builderMessageActor(), Kind: "reply", RequestID: "reply-too-soon", ReplyTo: w.Record.Message.ID, Body: "DONE", ReportStatus: "DONE"}); err == nil {
		t.Fatal("reply covered pending native intent")
	}
	e := CMUXEvidence{ID: "native-1", Source: "agentflow", TargetSurface: w.Binding.SurfaceID, TargetWorkspace: w.Binding.WorkspaceID, Body: c.Body, State: "queued", RecipientHasAgent: cmuxRecipientActive(), ExitCode: 0}
	w, err = s.FinishCMUXSend(t.Context(), c.Actor, w, e)
	if err != nil || w.Record.Delivery.Phase != "CMUX_QUEUED" {
		t.Fatal(w, err)
	}
	if _, err := s.Ack(t.Context(), builderMessageActor(), "ack", w.Record.Message.ID); err != nil {
		t.Fatal(err)
	}
	seat, err := s.GetSeat(t.Context(), c.Actor.RunID, "builder", 1)
	if err != nil || seat.Status != "LAUNCHED" {
		t.Fatal("receipt invented READY", seat, err)
	}
}

func TestCMUXInboxHistoryConflictsRejectWholeBatch(t *testing.T) {
	for _, mode := range []string{"body", "source", "source-surface", "source-workspace", "target-surface", "target-workspace", "recipient", "generation", "duplicate-new", "duplicate-old", "unknown-new-source"} {
		for _, order := range []string{"old-first", "new-first"} {
			t.Run(mode+"/"+order, func(t *testing.T) {
				s, _ := cmuxStoreFixture(t)
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
				// Conflicts must still reject after reopening the durable history.
				var path string
				if err := s.db.QueryRow(`SELECT file FROM pragma_database_list WHERE name='main'`).Scan(&path); err != nil {
					t.Fatal(err)
				}
				if err := s.Close(); err != nil {
					t.Fatal(err)
				}
				s, err = OpenExisting(path, true)
				if err != nil {
					t.Fatal(err)
				}
				defer s.Close()
				fresh := old
				fresh.ID, fresh.Body = "history-B", "new"
				code := "CMUX_EVENT_DUPLICATE"
				switch mode {
				case "body":
					old.Body = "changed"
				case "source":
					old.Source = "other"
				case "source-surface":
					old.SourceSurface = binding.SurfaceID
				case "source-workspace":
					old.SourceWorkspace = "other"
				case "target-surface":
					old.TargetSurface, code = "sf-main", "CMUX_EVENT_REJECTED"
				case "target-workspace":
					old.TargetWorkspace, code = "other", "CMUX_EVENT_REJECTED"
				case "recipient":
					a = mainMessageActor()
					binding, err = s.CMUXSurface(t.Context(), a)
					if err != nil {
						t.Fatal(err)
					}
					old.TargetSurface, fresh.TargetSurface = binding.SurfaceID, binding.SurfaceID
				case "generation":
					// Even identical native coordinates cannot replay an old event
					// into a different receiving generation.
					if _, err := s.db.Exec(`UPDATE seats SET is_current=0 WHERE agent_id='builder'`); err != nil {
						t.Fatal(err)
					}
					a.Generation, binding.Generation = 2, 2
					if err := s.CreateSeat(t.Context(), Seat{RunID: a.RunID, AgentID: a.AgentID, Generation: 2, Current: true, Tool: binding.Tool, WorkspaceID: binding.WorkspaceID, SurfaceID: binding.SurfaceID, Status: "LAUNCHED"}); err != nil {
						t.Fatal(err)
					}
					data, _ := json.Marshal(binding)
					tx, err := s.db.BeginTx(t.Context(), nil)
					if err != nil {
						t.Fatal(err)
					}
					defer tx.Rollback()
					if _, err := appendJournal(t.Context(), tx, JournalEntry{RunID: a.RunID, AgentID: a.AgentID, Generation: a.Generation, Event: "CMUX_SURFACE_REGISTERED", Details: string(data)}); err != nil {
						t.Fatal(err)
					}
					if err := tx.Commit(); err != nil {
						t.Fatal(err)
					}
					before, err = s.JournalAfter(t.Context(), a.RunID, 0)
					if err != nil {
						t.Fatal(err)
					}
				case "duplicate-new", "duplicate-old":
					code = "CMUX_EVENT_REJECTED"
				case "unknown-new-source":
					fresh.SourceSurface, code = "unknown", "CMUX_SOURCE_UNKNOWN"
				}
				batch := []CMUXEvidence{old, fresh}
				if order == "new-first" {
					batch = []CMUXEvidence{fresh, old}
				}
				if mode == "duplicate-new" {
					batch = append(batch, fresh)
				} else if mode == "duplicate-old" {
					batch = append(batch, old)
				}
				err = s.AcceptCMUXInbox(t.Context(), a, binding, batch)
				var public *MessagingError
				if !errors.As(err, &public) || public.Code != code {
					t.Fatalf("expected %s, got %v", code, err)
				}
				after, err := s.JournalAfter(t.Context(), a.RunID, 0)
				if err != nil || !reflect.DeepEqual(before, after) {
					t.Fatal("rejected batch changed durable history", after, err)
				}
			})
		}
	}
}

func TestCMUXInboxMixedBatchWriteFailureRollsBack(t *testing.T) {
	s, _ := cmuxStoreFixture(t)
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
	first, second := old, old
	first.ID, second.ID = "history-B", "history-C"
	if _, err := s.db.Exec(`CREATE TRIGGER fail_cmux_inbox BEFORE INSERT ON journal WHEN NEW.event='CMUX_INBOX_EVENT' AND json_extract(NEW.details,'$.id')='history-C' BEGIN SELECT RAISE(ABORT, 'injected inbox write failure'); END`); err != nil {
		t.Fatal(err)
	}
	batch := []CMUXEvidence{first, old, second}
	err = s.AcceptCMUXInbox(t.Context(), a, binding, batch)
	if err == nil || !strings.Contains(err.Error(), "injected inbox write failure") {
		t.Fatalf("expected failure at second new event, got %v", err)
	}
	after, err := s.JournalAfter(t.Context(), a.RunID, 0)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("partial inbox batch persisted", after, err)
	}
	if _, err := s.db.Exec(`DROP TRIGGER fail_cmux_inbox`); err != nil {
		t.Fatal(err)
	}
	if err := s.AcceptCMUXInbox(t.Context(), a, binding, batch); err != nil {
		t.Fatal(err)
	}
	if err := s.AcceptCMUXInbox(t.Context(), a, binding, batch); err != nil {
		t.Fatal("retry after successful commit must be idempotent", err)
	}
	after, err = s.JournalAfter(t.Context(), a.RunID, 0)
	if err != nil || len(after) != len(before)+2 || !reflect.DeepEqual(before, after[:len(before)]) {
		t.Fatal("retry lost or duplicated events", after, err)
	}
	for i, id := range []string{first.ID, second.ID} {
		var e CMUXEvidence
		if json.Unmarshal([]byte(after[len(before)+i].Details), &e) != nil || e.ID != id {
			t.Fatal("new event order changed", after)
		}
	}
}
