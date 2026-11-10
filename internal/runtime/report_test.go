package runtime

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	runcontext "agentflow/internal/context"
	"agentflow/internal/ipc"
	"agentflow/internal/store"
)

func reportServiceFixture(t *testing.T) (*ReportService, *MessageService, runcontext.Report, string) {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	frozen := example(t, "json")
	for _, id := range []string{"builder", "reviewer"} {
		agent := frozen.Agents[id]
		agent.Session = "fresh"
		frozen.Agents[id] = agent
	}
	frozen.ContextDir = filepath.Join(dir, "contexts")
	frozen.Digest = ""
	body, _ := json.Marshal(frozen)
	frozen.Digest = contextTestDigest(body)
	plan, err := Compile(frozen, Options{})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := NewSnapshot(plan, "report-run")
	if err != nil {
		t.Fatal(err)
	}
	snapshotPath := filepath.Join(dir, "config.resolved.json")
	if err := WriteSnapshot(snapshotPath, snapshot); err != nil {
		t.Fatal(err)
	}
	run, err := CreateContext(snapshotPath, "report-run")
	if err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(dir, "state.db")
	db, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	body, _ = json.Marshal(frozen)
	if err := db.CreateRun(t.Context(), store.Run{RunID: run.ID(), ConfigDigest: frozen.Digest, ConfigSnapshot: string(body)}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"main", "builder", "reviewer"} {
		for _, gen := range []int64{1, 2} {
			if err := db.CreateSeat(t.Context(), store.Seat{RunID: run.ID(), AgentID: id, Generation: gen, Current: gen == 2, Tool: frozen.Agents[id].Tool, Status: "READY"}); err != nil {
				t.Fatal(err)
			}
		}
	}
	svc, err := NewReportService(db, run)
	if err != nil {
		t.Fatal(err)
	}
	msg, _ := NewMessageService(db)
	main := ipc.Actor{RunID: run.ID(), AgentID: "main", Generation: 2}
	assigned, err := msg.Send(t.Context(), SendMessage{Actor: main, RequestID: "work-1", Recipient: "builder", Body: "private dispatch body"})
	if err != nil {
		t.Fatal(err)
	}
	e, err := svc.PublishEvidence(t.Context(), ipc.Actor{RunID: run.ID(), AgentID: "builder", Generation: 2}, "log", "test result", []byte("private evidence body"))
	if err != nil {
		t.Fatal(err)
	}
	r := runcontext.Report{Version: runcontext.Version, RunID: run.ID(), AgentID: "builder", Generation: 2, ReportID: "result-1", MessageID: assigned.Message.ID, RequestID: "work-1",
		Status: "DONE", Summary: "Implementation completed", Evidence: []runcontext.Evidence{*e}, Next: "Await review"}
	return svc, msg, r, dbPath
}

func TestReportServicePersistsAndMergesWithoutChangingS5(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	svc, _, report, dbPath := reportServiceFixture(t)
	author := ipc.Actor{RunID: report.RunID, AgentID: report.AgentID, Generation: report.Generation}
	lead := ipc.Actor{RunID: report.RunID, AgentID: "main", Generation: 2}
	ref := runcontext.ReportRef{AgentID: report.AgentID, Generation: report.Generation, ReportID: report.ReportID}
	before, err := svc.db.SessionWork(t.Context(), report.MessageID)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := svc.Publish(t.Context(), author, report)
	if err != nil {
		t.Fatal(err)
	}
	_, err = svc.Publish(t.Context(), author, report)
	if !errors.Is(err, runcontext.ErrExists) {
		t.Fatal(err)
	}
	if err := svc.db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	run, err := runcontext.Open(filepath.Dir(svc.run.Path()), report.RunID)
	if err != nil {
		t.Fatal(err)
	}
	svc, _ = NewReportService(db, run)
	for _, actor := range []ipc.Actor{author, lead} {
		got, m, err := svc.Read(t.Context(), actor, ref)
		if err != nil || !reflect.DeepEqual(got, &report) || !reflect.DeepEqual(m, manifest) {
			t.Fatalf("reopened: %+v %v", got, err)
		}
	}
	review, err := svc.Review(t.Context(), lead, []runcontext.ReportRef{ref})
	if err != nil || review[0].Meaning != "agent_claim" {
		t.Fatalf("review: %+v %v", review, err)
	}
	if _, err := svc.Merge(t.Context(), lead, "initial", "merged", []runcontext.ReportRef{ref}); err != nil {
		t.Fatal(err)
	}
	main, _ := run.Agent("main")
	s, _, err := main.Read("merged")
	if err != nil || strings.Contains(s.Progress.Text, "private evidence body") || strings.Contains(s.Progress.Text, "private dispatch body") || strings.Contains(s.Progress.Text, "PASS") {
		t.Fatalf("unsafe merge: %+v %v", s, err)
	}
	after, err := db.SessionWork(t.Context(), report.MessageID)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("report changed S5 state: %+v %+v %v", before, after, err)
	}
	_, err = svc.Merge(t.Context(), lead, "initial", "merged", []runcontext.ReportRef{ref})
	if !errors.Is(err, runcontext.ErrExists) {
		t.Fatal(err)
	}
}

func TestReportServiceRejectsUnauthorizedAndMismatchedDispatch(t *testing.T) {
	for _, fault := range []string{"actor-run", "actor-agent", "old-actor", "future-actor", "report-author", "report-generation", "report-run", "message-missing", "wrong-request", "request-only-missing", "wrong-recipient", "reply-id", "fresh-not-ready", "frozen-lead", "frozen-digest", "no-evidence"} {
		t.Run(fault, func(t *testing.T) {
			svc, msg, r, _ := reportServiceFixture(t)
			actor := ipc.Actor{RunID: r.RunID, AgentID: r.AgentID, Generation: r.Generation}
			lead := ipc.Actor{RunID: r.RunID, AgentID: "main", Generation: 2}
			switch fault {
			case "actor-run":
				actor.RunID = "other"
			case "actor-agent":
				actor.AgentID = "unknown"
			case "old-actor":
				actor.Generation = 1
				r.Generation = 1
			case "future-actor":
				actor.Generation = 3
				r.Generation = 3
			case "report-author":
				r.AgentID = "reviewer"
			case "report-generation":
				r.Generation = 1
			case "report-run":
				r.RunID = "other"
			case "message-missing":
				r.MessageID = "missing"
			case "wrong-request":
				r.RequestID = "other"
			case "request-only-missing":
				r.MessageID = ""
				r.RequestID = "missing"
			case "wrong-recipient":
				m, err := msg.Send(t.Context(), SendMessage{Actor: lead, RequestID: "other", Recipient: "reviewer", Body: "other"})
				if err != nil {
					t.Fatal(err)
				}
				r.MessageID = m.Message.ID
				r.RequestID = "other"
			case "reply-id":
				m, err := msg.Reply(t.Context(), ReplyMessage{Actor: actor, RequestID: "reply", MessageID: r.MessageID, Status: "DONE", Body: "done"})
				if err != nil {
					t.Fatal(err)
				}
				r.MessageID = m.Message.ID
				r.RequestID = "reply"
			case "fresh-not-ready":
				m, err := msg.Assign(t.Context(), SendMessage{Actor: lead, RequestID: "fresh", Recipient: actor.AgentID, Body: "work", Session: "fresh"})
				if err != nil {
					t.Fatal(err)
				}
				r.MessageID = m.Message.ID
				r.RequestID = "fresh"
			case "frozen-lead", "frozen-digest":
				digest, leadID := svc.run.ConfigDigest(), "main"
				if fault == "frozen-lead" {
					leadID = "reviewer"
				} else {
					digest = strings.Repeat("0", 64)
				}
				run, err := runcontext.Create(filepath.Join(filepath.Dir(svc.run.Path()), "other-contexts"), r.RunID, leadID, digest, []string{"main", "builder", "reviewer"})
				if err != nil {
					t.Fatal(err)
				}
				svc, _ = NewReportService(svc.db, run)
			case "no-evidence":
				r.Evidence = nil
			}
			if _, err := svc.Publish(t.Context(), actor, r); err == nil {
				t.Fatal("accepted invalid report")
			}
			a, _ := svc.run.Agent("builder")
			if _, err := os.Stat(filepath.Join(a.Path(), ".reports")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("rejection reserved report: %v", err)
			}
		})
	}
}

func TestReportOptionalBindingsAndReadAuthorization(t *testing.T) {
	for _, binding := range []string{"message", "request", "neither"} {
		t.Run(binding, func(t *testing.T) {
			svc, _, r, _ := reportServiceFixture(t)
			if binding != "message" {
				r.MessageID = ""
			}
			if binding != "request" {
				r.RequestID = ""
			}
			author := ipc.Actor{RunID: r.RunID, AgentID: r.AgentID, Generation: r.Generation}
			if _, err := svc.Publish(t.Context(), author, r); err != nil {
				t.Fatal(err)
			}
			ref := runcontext.ReportRef{AgentID: r.AgentID, Generation: 2, ReportID: r.ReportID}
			other := ipc.Actor{RunID: r.RunID, AgentID: "reviewer", Generation: 2}
			if _, _, err := svc.Read(t.Context(), other, ref); err == nil {
				t.Fatal("cross-author read accepted")
			}
			if _, err := svc.Review(t.Context(), author, []runcontext.ReportRef{ref}); err == nil {
				t.Fatal("non-lead review accepted")
			}
			if _, err := svc.Merge(t.Context(), author, "initial", "merged", []runcontext.ReportRef{ref}); err == nil {
				t.Fatal("non-lead merge accepted")
			}
			lead := ipc.Actor{RunID: r.RunID, AgentID: "main", Generation: 2}
			if _, err := svc.Review(t.Context(), lead, []runcontext.ReportRef{ref}); err != nil {
				t.Fatal(err)
			}
		})
	}
	if _, err := NewReportService(nil, nil); err == nil {
		t.Fatal("nil dependencies accepted")
	}
}

func TestReportServiceRejectsPersistedOldGenerationAndInvalidBinding(t *testing.T) {
	for _, fault := range []string{"old", "bad-message", "bad-request", "corrupt-evidence"} {
		t.Run(fault, func(t *testing.T) {
			svc, _, r, _ := reportServiceFixture(t)
			a, _ := svc.run.Agent(r.AgentID)
			switch fault {
			case "old":
				r.Generation = 1
				e, err := a.PublishEvidence(1, "log", "old test", []byte("old"))
				if err != nil {
					t.Fatal(err)
				}
				r.Evidence = []runcontext.Evidence{*e}
				r.MessageID = ""
				r.RequestID = ""
			case "bad-message":
				r.MessageID = "not-stored"
			case "bad-request":
				r.RequestID = "not-stored"
			}
			// The file layer cannot establish S5 authority; runtime must reject even
			// an otherwise valid file published directly or by another process.
			if _, err := a.PublishReport(r); err != nil {
				t.Fatal(err)
			}
			if fault == "corrupt-evidence" {
				if err := os.WriteFile(r.Evidence[0].Path, []byte("changed"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			lead := ipc.Actor{RunID: r.RunID, AgentID: "main", Generation: 2}
			ref := runcontext.ReportRef{AgentID: r.AgentID, Generation: r.Generation, ReportID: r.ReportID}
			if _, _, err := svc.Read(t.Context(), lead, ref); err == nil {
				t.Fatal("invalid persisted report read accepted")
			}
			if _, err := svc.Merge(t.Context(), lead, "initial", "merged", []runcontext.ReportRef{ref}); err == nil {
				t.Fatal("invalid persisted report merged")
			}
			main, _ := svc.run.Agent("main")
			if _, err := os.Stat(filepath.Join(main.Path(), "merged")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("failed merge wrote revision: %v", err)
			}
		})
	}
}
