package integration_test

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"agentflow/internal/config"
	runcontext "agentflow/internal/context"
	"agentflow/internal/ipc"
	"agentflow/internal/runtime"
	"agentflow/internal/store"
)

// TestReportSubprocess is an internal test-binary helper, not a product CLI. Each
// invocation starts with fresh Store/Run handles and no parent in-memory state.
func TestReportSubprocess(t *testing.T) {
	mode := os.Getenv("AGENTFLOW_TEST_REPORT_MODE")
	if mode == "" {
		t.Skip("subprocess helper")
	}
	runID := os.Getenv("AGENTFLOW_TEST_REPORT_RUN")
	run, err := runcontext.Open(os.Getenv("AGENTFLOW_TEST_REPORT_BASE"), runID)
	if err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(os.Getenv("AGENTFLOW_TEST_REPORT_DB"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	svc, err := runtime.NewReportService(db, run)
	if err != nil {
		t.Fatal(err)
	}
	if mode == "bootstrap" {
		id := os.Getenv("AGENTFLOW_TEST_REPORT_AUTHOR")
		bootstrap, err := runtime.NewBootstrapService(db, run)
		if err != nil {
			t.Fatal(err)
		}
		result, err := bootstrap.Rebuild(t.Context(), ipc.Actor{RunID: runID, AgentID: id, Generation: 1})
		if err != nil {
			t.Fatal(err)
		}
		b := result.Bootstrap
		if result.Executed || result.RuntimeCommunication != "UNVERIFIED" || b.AgentID != id || b.RunID != runID {
			t.Fatalf("%+v", result)
		}
		want, policy := 1, "fresh"
		if id == "main" {
			want, policy = 2, "persistent"
			if b.Revision != "merged" {
				t.Fatal("lost merged progress")
			}
		}
		if len(b.Reports) != want || b.Protocol.Session != policy {
			t.Fatalf("%+v", b)
		}
		body, err := os.ReadFile(result.Path)
		if err != nil || strings.Contains(string(body), "PRIVATE") {
			t.Fatalf("private body copied: %v", err)
		}
		files, err := os.ReadDir(filepath.Dir(result.Path))
		if err != nil || len(files) != 1 {
			t.Fatalf("duplicate bootstrap: %v %v", files, err)
		}
		return
	}
	if mode == "publish" {
		id := os.Getenv("AGENTFLOW_TEST_REPORT_AUTHOR")
		actor := ipc.Actor{RunID: runID, AgentID: id, Generation: 1}
		e, err := svc.PublishEvidence(t.Context(), actor, "check-log", "local verification", []byte("PRIVATE full evidence for "+runID+"/"+id))
		if err != nil {
			t.Fatal(err)
		}
		status := "DONE"
		if id == "reviewer" {
			status = "BLOCKED_CONFIRM"
		}
		r := runcontext.Report{Version: runcontext.Version, RunID: runID, AgentID: id, Generation: 1, ReportID: "result-1", RequestID: "work-" + id,
			Status: status, Summary: runID + "/" + id + " compact result", Evidence: []runcontext.Evidence{*e}, Next: "Await lead decision"}
		if _, err := svc.Publish(t.Context(), actor, r); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.Publish(t.Context(), actor, r); !errors.Is(err, runcontext.ErrExists) {
			t.Fatalf("retry: %v", err)
		}
		return
	}
	if mode != "merge" {
		t.Fatal("unknown test mode")
	}
	refs := []runcontext.ReportRef{{AgentID: "worker", Generation: 1, ReportID: "result-1"}, {AgentID: "reviewer", Generation: 1, ReportID: "result-1"}}
	lead := ipc.Actor{RunID: runID, AgentID: "main", Generation: 1}
	review, err := svc.Review(t.Context(), lead, refs)
	if err != nil || len(review) != 2 {
		t.Fatalf("review: %+v %v", review, err)
	}
	if _, err := svc.Merge(t.Context(), lead, "initial", "merged", refs); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Merge(t.Context(), lead, "initial", "merged", refs); !errors.Is(err, runcontext.ErrExists) {
		t.Fatalf("merge retry: %v", err)
	}
}

func TestReportsAcrossProcessesAndIndependentRunDatabases(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(dir, "contexts")
	prompt := "PRIVATE frozen role instructions"
	persistent := "persistent"
	c := &config.Config{Version: 1, Name: "reports-team", Terminal: "cmux", Lead: "main", ContextDir: base, SourcePath: filepath.Join(dir, "team.json"),
		Agents: map[string]config.Agent{"main": {Role: "lead", Session: &persistent, Prompt: &prompt}, "worker": {Role: "builder", Prompt: &prompt}, "reviewer": {Role: "reviewer", Prompt: &prompt}},
		Layout: &config.Layout{Split: "columns", Children: []config.Layout{{Agent: "main"}, {Split: "rows", Children: []config.Layout{{Agent: "worker"}, {Agent: "reviewer"}}}}}}
	frozen, err := config.Resolve(c, config.ResolveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := runtime.Compile(frozen, runtime.Options{})
	if err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, runID := range []string{"report-a", "report-b"} {
		runDir := filepath.Join(dir, runID)
		if err := os.Mkdir(runDir, 0700); err != nil {
			t.Fatal(err)
		}
		snapshot, err := runtime.NewSnapshot(plan, runID)
		if err != nil {
			t.Fatal(err)
		}
		snapshotPath := filepath.Join(runDir, "config.resolved.json")
		if err := runtime.WriteSnapshot(snapshotPath, snapshot); err != nil {
			t.Fatal(err)
		}
		if _, err := runtime.CreateContext(snapshotPath, runID); err != nil {
			t.Fatal(err)
		}
		dbPath := filepath.Join(runDir, "state.db")
		db, err := store.Open(dbPath)
		if err != nil {
			t.Fatal(err)
		}
		if err := db.Migrate(t.Context()); err != nil {
			t.Fatal(err)
		}
		body, _ := json.Marshal(frozen)
		if err := db.CreateRun(t.Context(), store.Run{RunID: runID, ConfigDigest: frozen.Digest, ConfigSnapshot: string(body)}); err != nil {
			t.Fatal(err)
		}
		for _, id := range []string{"main", "worker", "reviewer"} {
			if err := db.CreateSeat(t.Context(), store.Seat{RunID: runID, AgentID: id, Generation: 1, Current: true, Tool: frozen.Agents[id].Tool, Status: "READY"}); err != nil {
				t.Fatal(err)
			}
		}
		messages, _ := runtime.NewMessageService(db)
		for _, id := range []string{"worker", "reviewer"} {
			if _, err := messages.Send(t.Context(), runtime.SendMessage{Actor: ipc.Actor{RunID: runID, AgentID: "main", Generation: 1}, RequestID: "work-" + id, Recipient: id, Body: "PRIVATE full dispatch"}); err != nil {
				t.Fatal(err)
			}
		}
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
		invoke := func(mode, author string) {
			t.Helper()
			cmd := exec.CommandContext(t.Context(), executable, "-test.run=^TestReportSubprocess$")
			cmd.Env = append(os.Environ(), "PATH=", "AGENTFLOW_TEST_REPORT_MODE="+mode, "AGENTFLOW_TEST_REPORT_RUN="+runID, "AGENTFLOW_TEST_REPORT_BASE="+base, "AGENTFLOW_TEST_REPORT_DB="+dbPath, "AGENTFLOW_TEST_REPORT_AUTHOR="+author)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("%s/%s: %s %v", mode, author, out, err)
			}
		}
		invoke("publish", "worker")
		invoke("publish", "reviewer")
		invoke("merge", "")
		for i := 0; i < 2; i++ {
			invoke("bootstrap", "main")
			invoke("bootstrap", "worker")
		}
		run, err := runcontext.Open(base, runID)
		if err != nil {
			t.Fatal(err)
		}
		main, _ := run.Agent("main")
		merged, _, err := main.Read("merged")
		if err != nil {
			t.Fatal(err)
		}
		var summaries []runcontext.ReviewedReport
		if err := json.Unmarshal([]byte(merged.Progress.Text), &summaries); err != nil {
			t.Fatal(err)
		}
		if len(summaries) != 2 || summaries[0].Status != "DONE" || summaries[1].Status != "BLOCKED_CONFIRM" || !strings.Contains(summaries[0].Summary, runID) {
			t.Fatalf("lost independent reports: %+v", summaries)
		}
		for _, summary := range summaries {
			if summary.Verification != "INTEGRITY_VERIFIED" || summary.Meaning != "agent_claim" {
				t.Fatalf("invented acceptance: %+v", summary)
			}
			a, _ := run.Agent(summary.AgentID)
			r, m, err := a.ReadReport(1, "result-1")
			if err != nil || r.Status != summary.Status || m.File.SHA256 != summary.ReportSHA256 {
				t.Fatalf("source report changed: %+v %v", r, err)
			}
		}
		if err := filepath.Walk(filepath.Join(main.Path(), "merged"), func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return err
			}
			body, err := os.ReadFile(path)
			if err == nil && strings.Contains(string(body), "PRIVATE") {
				t.Fatalf("sensitive body merged: %s", path)
			}
			return err
		}); err != nil {
			t.Fatal(err)
		}
		// Opening run-a files against the run-b database is explicitly rejected.
		if runID == "report-b" {
			db, err := store.Open(dbPath)
			if err != nil {
				t.Fatal(err)
			}
			other, err := runcontext.Open(base, "report-a")
			if err != nil {
				t.Fatal(err)
			}
			svc, _ := runtime.NewReportService(db, other)
			_, err = svc.Review(t.Context(), ipc.Actor{RunID: "report-a", AgentID: "main", Generation: 1}, []runcontext.ReportRef{{AgentID: "worker", Generation: 1, ReportID: "result-1"}})
			db.Close()
			if err == nil {
				t.Fatal("cross-database run impersonation accepted")
			}
		}
	}
}
