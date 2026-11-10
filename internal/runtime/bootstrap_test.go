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

func TestBootstrapReopenFreshPersistentAndMergedContinuation(t *testing.T) {
	svc, _, report, dbPath := reportServiceFixture(t)
	actor := ipc.Actor{RunID: report.RunID, AgentID: report.AgentID, Generation: 2}
	lead := ipc.Actor{RunID: report.RunID, AgentID: "main", Generation: 2}
	if _, err := svc.Publish(t.Context(), actor, report); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Merge(t.Context(), lead, "initial", "merged", []runcontext.ReportRef{{AgentID: report.AgentID, Generation: 2, ReportID: report.ReportID}}); err != nil {
		t.Fatal(err)
	}
	beforeRun, beforeSeats, err := svc.db.ReadState(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	beforeWork, _ := svc.db.SessionWork(t.Context(), report.MessageID)
	rebuild, _ := NewBootstrapService(svc.db, svc.run)
	first, err := rebuild.Rebuild(t.Context(), lead)
	if err != nil || first.Bootstrap.Revision != "merged" || first.Bootstrap.Protocol.Session != "persistent" || !strings.Contains(first.Bootstrap.Recommendation, "if unavailable") {
		t.Fatalf("%+v %v", first, err)
	}
	if err := svc.db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := store.OpenExisting(dbPath, true)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	run, err := runcontext.Open(filepath.Dir(svc.run.Path()), report.RunID)
	if err != nil {
		t.Fatal(err)
	}
	rebuild, _ = NewBootstrapService(db, run)
	second, err := rebuild.Bootstrap(t.Context(), lead)
	if err != nil || !reflect.DeepEqual(first, second) {
		t.Fatalf("unstable reopen: %+v %v", second, err)
	}
	fresh, err := rebuild.Bootstrap(t.Context(), actor)
	if err != nil || fresh.Bootstrap.Protocol.Session != "fresh" || !strings.Contains(fresh.Bootstrap.Recommendation, "fresh native session") || len(fresh.Bootstrap.Reports) != 1 {
		t.Fatalf("%+v %v", fresh, err)
	}
	other, err := rebuild.Bootstrap(t.Context(), ipc.Actor{RunID: report.RunID, AgentID: "reviewer", Generation: 2})
	if err != nil || len(other.Bootstrap.Reports) != 0 || other.Path == fresh.Path {
		t.Fatalf("agent isolation: %+v %v", other, err)
	}
	for _, b := range []*BootstrapResult{first, fresh, other} {
		body, _ := json.Marshal(b)
		if b.Executed || b.Bootstrap.Executed || b.RuntimeCommunication != "UNVERIFIED" || strings.Contains(string(body), "private evidence body") || strings.Contains(string(body), "private dispatch body") || strings.Contains(string(body), "判断工作可行性") {
			t.Fatal("unsafe recovery claim or copied private body")
		}
	}
	afterRun, afterSeats, _ := db.ReadState(t.Context())
	afterWork, _ := db.SessionWork(t.Context(), report.MessageID)
	if !reflect.DeepEqual(beforeRun, afterRun) || !reflect.DeepEqual(beforeSeats, afterSeats) || !reflect.DeepEqual(beforeWork, afterWork) {
		t.Fatal("bootstrap changed runtime/message state")
	}
}

func TestBootstrapMergedReportSurvivesAuthorFreshGeneration(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	recovery, _, _, opts := checkpointBindingFixture(t)
	db, run := recovery.sessions.db, recovery.bootstrap.run
	reports, _ := NewReportService(db, run)
	messages, _ := NewMessageServiceWithAdapters(db, &fakeResetAdapter{}, &fakeSubmitAdapter{})
	lead := assignment().Actor
	first, err := messages.Assign(t.Context(), assignment())
	if err != nil || first.Delivery.TargetGeneration != 2 || first.Delivery.Phase != "SENT" {
		t.Fatalf("first fresh assignment: %+v %v", first, err)
	}
	author := ipc.Actor{RunID: lead.RunID, AgentID: "builder", Generation: 2}
	evidence, err := reports.PublishEvidence(t.Context(), author, "result", "regression evidence", []byte("verified result"))
	if err != nil {
		t.Fatal(err)
	}
	report := runcontext.Report{Version: runcontext.Version, RunID: author.RunID, AgentID: author.AgentID, Generation: author.Generation,
		ReportID: "result-1", MessageID: first.Message.ID, RequestID: first.Message.RequestID, Status: "DONE", Summary: "First work completed",
		Evidence: []runcontext.Evidence{*evidence}, Next: "Continue with the next work"}
	if _, err := reports.Publish(t.Context(), author, report); err != nil {
		t.Fatal(err)
	}
	if _, err := messages.Reply(t.Context(), ReplyMessage{Actor: author, RequestID: "result-1", MessageID: first.Message.ID, Status: "DONE", Body: "done"}); err != nil {
		t.Fatal(err)
	}
	ref := runcontext.ReportRef{AgentID: author.AgentID, Generation: author.Generation, ReportID: report.ReportID}
	if _, err := reports.Merge(t.Context(), lead, "missing-base", "failed", []runcontext.ReportRef{ref}); err == nil {
		t.Fatal("merge with missing base succeeded")
	}
	entries, err := db.JournalAfter(t.Context(), lead.RunID, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Event == "REPORT_MERGED" {
			t.Fatal("failed merge granted historical authorization")
		}
	}
	if _, err := reports.Merge(t.Context(), lead, "initial", "merged", []runcontext.ReportRef{ref}); err != nil {
		t.Fatal(err)
	}
	bootstrap, _ := NewBootstrapService(db, run)
	before, err := bootstrap.Bootstrap(t.Context(), lead)
	if err != nil {
		t.Fatal(err)
	}
	next := assignment()
	next.RequestID = "work-2"
	second, err := messages.Assign(t.Context(), next)
	if err != nil || second.Delivery.TargetGeneration != 3 || second.Delivery.Phase != "SENT" {
		t.Fatalf("next fresh assignment: %+v %v", second, err)
	}
	after, err := bootstrap.Bootstrap(t.Context(), lead)
	if err != nil {
		t.Fatalf("lead cannot restore merged generation-2 report after author advanced to generation 3: %v", err)
	}
	if len(after.Bootstrap.Reports) != 1 || after.Bootstrap.Reports[0].ReportRef != ref || !reflect.DeepEqual(before, after) {
		t.Fatalf("merged continuation changed after author advanced: %+v", after)
	}
	// Historical merge authority must not allow a stale author to publish, or
	// allow a lead to newly read/review/merge reports from an old generation.
	if _, err := reports.Publish(t.Context(), author, report); err == nil {
		t.Fatal("stale author publication accepted")
	}
	if _, _, err := reports.Read(t.Context(), lead, ref); err == nil {
		t.Fatal("historical report read outside bootstrap accepted")
	}
	if _, err := reports.Review(t.Context(), lead, []runcontext.ReportRef{ref}); err == nil {
		t.Fatal("new review of stale author accepted")
	}
	if _, err := reports.Merge(t.Context(), lead, "merged", "unauthorized", []runcontext.ReportRef{ref}); err == nil {
		t.Fatal("new merge of stale author accepted")
	}
	// Reusing the report ID in the next generation cannot replace the report
	// selected by the lead's immutable merged revision.
	current := author
	current.Generation++
	newEvidence, err := reports.PublishEvidence(t.Context(), current, "result", "next work evidence", []byte("new generation result"))
	if err != nil {
		t.Fatal(err)
	}
	newReport := report
	newReport.Generation, newReport.MessageID, newReport.RequestID = current.Generation, second.Message.ID, second.Message.RequestID
	newReport.Summary, newReport.Evidence = "Next work completed", []runcontext.Evidence{*newEvidence}
	if _, err := reports.Publish(t.Context(), current, newReport); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(filepath.Dir(opts.SnapshotPath), "state.db")
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = store.OpenExisting(dbPath, true)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	run, err = runcontext.Open(filepath.Dir(run.Path()), run.ID())
	if err != nil {
		t.Fatal(err)
	}
	bootstrap, _ = NewBootstrapService(db, run)
	reopened, err := bootstrap.Rebuild(t.Context(), lead)
	if err != nil || !reflect.DeepEqual(before, reopened) {
		t.Fatalf("historical merge did not survive reopen: %+v %v", reopened, err)
	}
	for _, fault := range []string{"sha", "report-id", "revision", "run", "generation", "dispatch"} {
		t.Run("authorization-"+fault, func(t *testing.T) {
			binding := store.ReportBinding{Author: messageActor(author), MessageID: report.MessageID, RequestID: report.RequestID,
				ReportID: report.ReportID, ReportSHA256: before.Bootstrap.Reports[0].ReportSHA256, MergedRevision: "merged"}
			switch fault {
			case "sha":
				binding.ReportSHA256 = strings.Repeat("0", 64)
			case "report-id":
				binding.ReportID = "another-report"
			case "revision":
				binding.MergedRevision = "failed"
			case "run":
				binding.Author.RunID = "other-run"
			case "generation":
				binding.Author.Generation--
			case "dispatch":
				binding.MessageID = second.Message.ID
			}
			err := db.WithBootstrapContext(t.Context(), store.ReportContext{Actor: messageActor(lead), ConfigDigest: run.ConfigDigest(), Lead: run.Lead(), Review: true},
				func(_ store.Run, _ store.Seat, check func(store.ReportBinding) error) error { return check(binding) })
			if err == nil {
				t.Fatal("historical authority accepted mismatched " + fault)
			}
		})
	}
	t.Run("corrupt-historical-evidence", func(t *testing.T) {
		body, err := os.ReadFile(evidence.Path)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(evidence.Path, []byte("changed"), 0600); err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := os.WriteFile(evidence.Path, body, 0600); err != nil {
				t.Error(err)
			}
		}()
		if _, err := bootstrap.Bootstrap(t.Context(), lead); !errors.Is(err, runcontext.ErrCorrupt) {
			t.Fatalf("historical authorization bypassed evidence integrity: %v", err)
		}
	})
	// Copying an intact summary into a different revision does not grant that
	// revision the authorization of the original runtime merge.
	main, _ := run.Agent(lead.AgentID)
	copied, _, err := main.Read("merged")
	if err != nil {
		t.Fatal(err)
	}
	copied.Revision = "copied"
	if _, err := main.Publish(*copied); err != nil {
		t.Fatal(err)
	}
	if result, err := bootstrap.Bootstrap(t.Context(), lead); err == nil || result != nil {
		t.Fatalf("unauthorized copied revision accepted: %+v %v", result, err)
	}
}

func TestBootstrapRejectsIdentityProtocolAndSourceFaults(t *testing.T) {
	for _, fault := range []string{"run", "agent", "old", "future", "context-path", "config-digest", "role", "cwd", "tool", "permissions", "session", "prompt", "foreign-reference", "old-report", "corrupt", "incomplete", "bad-dispatch"} {
		t.Run(fault, func(t *testing.T) {
			svc, _, report, _ := reportServiceFixture(t)
			actor := ipc.Actor{RunID: report.RunID, AgentID: "builder", Generation: 2}
			run := svc.run
			a, _ := run.Agent("builder")
			snapshot, _, err := a.Read("initial")
			if err != nil {
				t.Fatal(err)
			}
			snapshot.Revision = "new"
			publish := false
			switch fault {
			case "run":
				actor.RunID = "other"
			case "agent":
				actor.AgentID = "unknown"
			case "old":
				actor.Generation = 1
			case "future":
				actor.Generation = 3
			case "context-path", "config-digest":
				d := run.ConfigDigest()
				if fault == "config-digest" {
					d = strings.Repeat("0", 64)
				}
				run, err = runcontext.Create(filepath.Join(filepath.Dir(run.Path()), "wrong-base"), run.ID(), run.Lead(), d, run.AgentIDs())
				if err != nil {
					t.Fatal(err)
				}
			case "role":
				snapshot.Protocol.Role = "other"
				publish = true
			case "cwd":
				snapshot.Protocol.Cwd = "/other"
				publish = true
			case "tool":
				snapshot.Protocol.Tool = "claude"
				publish = true
			case "permissions":
				snapshot.Protocol.Permissions = "default"
				publish = true
			case "session":
				snapshot.Protocol.Session = "persistent"
				publish = true
			case "prompt":
				snapshot.Protocol.PromptSHA256 = strings.Repeat("0", 64)
				publish = true
			case "foreign-reference":
				snapshot.Pending.References = []runcontext.Reference{{Path: filepath.Join(run.Path(), "reviewer", "initial", "manifest.json"), Purpose: "foreign"}}
				publish = true
			case "old-report":
				report.Generation, report.MessageID, report.RequestID = 1, "", ""
				e, err := a.PublishEvidence(1, "old", "old", []byte("old"))
				if err != nil {
					t.Fatal(err)
				}
				report.Evidence = []runcontext.Evidence{*e}
				if _, err := a.PublishReport(report); err != nil {
					t.Fatal(err)
				}
				lead, _ := run.Agent("main")
				if _, err := lead.Merge("initial", "merged-old", []runcontext.ReportRef{{AgentID: "builder", Generation: 1, ReportID: report.ReportID}}); err != nil {
					t.Fatal(err)
				}
				actor.AgentID = "main"
			case "corrupt":
				if err := os.WriteFile(filepath.Join(a.Path(), "initial", "manifest.json"), []byte("{}"), 0600); err != nil {
					t.Fatal(err)
				}
			case "incomplete":
				if err := os.Mkdir(filepath.Join(a.Path(), "unfinished"), 0700); err != nil {
					t.Fatal(err)
				}
			case "bad-dispatch":
				report.MessageID = "missing"
				if _, err := a.PublishReport(report); err != nil {
					t.Fatal(err)
				}
			}
			if publish {
				if _, err := a.Publish(*snapshot); err != nil {
					t.Fatal(err)
				}
			}
			rebuild, _ := NewBootstrapService(svc.db, run)
			if result, err := rebuild.Bootstrap(t.Context(), actor); err == nil || result != nil {
				t.Fatalf("accepted %s: %+v", fault, result)
			}
			if _, err := os.Stat(filepath.Join(run.Path(), actor.AgentID, ".bootstrap")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("failed bootstrap published files: %v", err)
			}
		})
	}
	if _, err := NewBootstrapService(nil, nil); err == nil {
		t.Fatal("nil deps accepted")
	}
}
