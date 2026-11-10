package store

import (
	"encoding/json"
	"reflect"
	"testing"

	"agentflow/internal/loopcore"
)

func cmuxWorkflowStore(t *testing.T) (*Store, WorkflowResult, MessageCommand, loopcore.Event, CMUXWorkflowReceipt) {
	t.Helper()
	s, _ := workflowStore(t)
	// Fixture conversion models launched seats, with no native READY evidence.
	if _, err := s.db.Exec(`DELETE FROM seats`); err != nil {
		t.Fatal(err)
	}
	var bindings []SurfaceRegistration
	for _, id := range []string{"main", "builder", "reviewer"} {
		bindings = append(bindings, SurfaceRegistration{RunID: "workflow", AgentID: id, Generation: 1, Tool: "codex", LaunchID: "launch", WorkspaceID: "workspace", WorkspaceRef: "workspace:1", SurfaceID: "surface-" + id, SurfaceRef: "surface:" + id, Status: "LAUNCHED", NativeReady: "UNVERIFIED"})
	}
	if err := s.RegisterCMUXSurfaces(t.Context(), bindings); err != nil {
		t.Fatal(err)
	}
	actor := MessageActor{"workflow", "main", 1}
	r, err := s.AdvanceWorkflow(t.Context(), actor, loopcore.Event{ID: "start", Kind: "start", Actor: "main"})
	if err != nil {
		t.Fatal(err)
	}
	w, claimed, err := s.BeginCMUXDispatch(t.Context(), actor, r.Message.ID)
	if err != nil || !claimed {
		t.Fatal(claimed, err)
	}
	e := CMUXEvidence{ID: "native-dispatch", Source: "agentflow", TargetSurface: w.Binding.SurfaceID, TargetWorkspace: w.Binding.WorkspaceID, Body: w.WireBody, State: "queued", RecipientHasAgent: cmuxRecipientActive()}
	if _, err := s.FinishCMUXSend(t.Context(), actor, w, e); err != nil {
		t.Fatal(err)
	}
	inbox, err := s.CMUXSurface(t.Context(), actor)
	if err != nil {
		t.Fatal(err)
	}
	event := loopcore.Event{ID: "reply", Kind: "handoff", Revision: 1, Actor: "builder", MessageID: r.Message.ID, Handoff: &loopcore.Handoff{Summary: "work", Evidence: []string{"test.log"}, Next: "review"}}
	reply := MessageCommand{Actor: MessageActor{"workflow", "builder", 1}, RequestID: "reply", Kind: "reply", ReplyTo: r.Message.ID, Body: "saved", ReportStatus: "DONE"}
	body, _ := json.Marshal(map[string]any{"v": 1, "type": "workflow_reply", "request_id": event.ID, "actor": map[string]any{"runId": "workflow", "agentId": "builder", "generation": 1}, "message_id": event.MessageID, "body": reply.Body, "status": reply.ReportStatus, "workflow": map[string]any{"revision": event.Revision, "kind": event.Kind, "handoff": event.Handoff}})
	native := CMUXWorkflowReceipt{Inbox: inbox, RequestID: "reply", Event: CMUXEvidence{ID: "native-reply", Source: "builder", SourceSurface: "surface-builder", SourceWorkspace: "workspace", TargetSurface: "surface-main", TargetWorkspace: "workspace", Body: string(body), State: "queued"}}
	if err := s.AcceptCMUXInbox(t.Context(), actor, inbox, []CMUXEvidence{native.Event}); err != nil {
		t.Fatal(err)
	}
	return s, r, reply, event, native
}

func TestCMUXWorkflowReplyTransactionRollsBackEvidenceCursorAndOutbox(t *testing.T) {
	s, start, reply, event, native := cmuxWorkflowStore(t)
	before, err := s.JournalAfter(t.Context(), "workflow", 0)
	if err != nil {
		t.Fatal(err)
	}
	var count int
	if err := s.db.QueryRow(`SELECT count(*) FROM messages`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`CREATE TRIGGER fail_workflow_receipt BEFORE INSERT ON journal WHEN NEW.event='CMUX_WORKFLOW_APPLIED' BEGIN SELECT RAISE(ABORT, 'injected workflow receipt failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReplyCMUXWorkflow(t.Context(), reply, event, native); err == nil {
		t.Fatal("expected atomic receipt failure")
	}
	after, _ := s.JournalAfter(t.Context(), "workflow", 0)
	cursor, _ := s.WorkflowCursor(t.Context(), "workflow")
	var afterCount int
	_ = s.db.QueryRow(`SELECT count(*) FROM messages`).Scan(&afterCount)
	if !reflect.DeepEqual(before, after) || !reflect.DeepEqual(start.Cursor, cursor) || count != afterCount {
		t.Fatal("partial reply/cursor/outbox/native receipt")
	}
	if _, err := s.db.Exec(`DROP TRIGGER fail_workflow_receipt`); err != nil {
		t.Fatal(err)
	}
	result, err := s.ReplyCMUXWorkflow(t.Context(), reply, event, native)
	if err != nil || result.Cursor.Revision != 2 || result.Message.Recipient != "reviewer" {
		t.Fatal(result, err)
	}
	before, _ = s.JournalAfter(t.Context(), "workflow", 0)
	if again, err := s.ReplyCMUXWorkflow(t.Context(), reply, event, native); err != nil || !reflect.DeepEqual(result, again) {
		t.Fatal(again, err)
	}
	after, _ = s.JournalAfter(t.Context(), "workflow", 0)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("duplicate inbox wrote work")
	}
}

func TestCMUXWorkflowCommitRechecksGenerationBindingAndStop(t *testing.T) {
	for _, mode := range []string{"source-generation", "target-generation", "source-binding", "target-binding", "stop", "changed-body", "native-id-request-conflict"} {
		t.Run(mode, func(t *testing.T) {
			s, start, reply, event, native := cmuxWorkflowStore(t)
			switch mode {
			case "source-generation", "target-generation":
				id := "builder"
				if mode == "target-generation" {
					id = "main"
				}
				if _, err := s.db.Exec(`UPDATE seats SET is_current=0 WHERE agent_id=?`, id); err != nil {
					t.Fatal(err)
				}
				if err := s.CreateSeat(t.Context(), Seat{RunID: "workflow", AgentID: id, Generation: 2, Current: true, Tool: "codex", Status: "UNVERIFIED"}); err != nil {
					t.Fatal(err)
				}
			case "source-binding", "target-binding":
				id := "builder"
				if mode == "target-binding" {
					id = "main"
				}
				if _, err := s.db.Exec(`UPDATE seats SET surface_id='wrong' WHERE agent_id=?`, id); err != nil {
					t.Fatal(err)
				}
			case "stop":
				if _, err := s.RequestStop(t.Context(), "workflow"); err != nil {
					t.Fatal(err)
				}
			case "changed-body":
				native.Event.Body = "different"
			case "native-id-request-conflict":
				data, _ := json.Marshal(CMUXWorkflowReceipt{Event: native.Event, RequestID: "another"})
				tx, _ := s.db.BeginTx(t.Context(), nil)
				_, err := appendJournal(t.Context(), tx, JournalEntry{RunID: "workflow", AgentID: "builder", Generation: 1, Event: "CMUX_WORKFLOW_APPLIED", Details: string(data)})
				if err != nil {
					t.Fatal(err)
				}
				if err := tx.Commit(); err != nil {
					t.Fatal(err)
				}
			}
			before, _ := s.JournalAfter(t.Context(), "workflow", 0)
			if _, err := s.ReplyCMUXWorkflow(t.Context(), reply, event, native); err == nil {
				t.Fatal("late workflow result bypassed gate")
			}
			after, _ := s.JournalAfter(t.Context(), "workflow", 0)
			cursor, _ := s.WorkflowCursor(t.Context(), "workflow")
			if !reflect.DeepEqual(before, after) || !reflect.DeepEqual(start.Cursor, cursor) {
				t.Fatal("gate wrote partial workflow result")
			}
		})
	}
}
