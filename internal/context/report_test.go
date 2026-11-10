package context

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func reportFixture(t *testing.T) (*Run, *Agent, *Agent, Report) {
	t.Helper()
	r, lead, snapshot := fixture(t)
	if _, err := lead.Publish(snapshot); err != nil {
		t.Fatal(err)
	}
	worker, _ := r.Agent("worker")
	e, err := worker.PublishEvidence(2, "test-log", "test output", []byte("private full test output"))
	if err != nil {
		t.Fatal(err)
	}
	report := Report{Version: Version, RunID: r.ID(), AgentID: "worker", Generation: 2, ReportID: "result-1", RequestID: "dispatch-1",
		Status: "DONE", Summary: "Implemented and tested", Evidence: []Evidence{*e}, Next: "Await lead review"}
	return r, lead, worker, report
}

func TestReportRoundTripReviewMergeAndImmutability(t *testing.T) {
	r, lead, worker, report := reportFixture(t)
	m, err := worker.PublishReport(report)
	if err != nil {
		t.Fatal(err)
	}
	ref := ReportRef{AgentID: "worker", Generation: 2, ReportID: report.ReportID}
	reopened, err := Open(r.base, r.ID())
	if err != nil {
		t.Fatal(err)
	}
	worker, _ = reopened.Agent("worker")
	got, manifest, err := worker.ReadReport(2, report.ReportID)
	if err != nil || !reflect.DeepEqual(got, &report) || !reflect.DeepEqual(manifest, m) {
		t.Fatalf("report round trip: %+v %+v %v", got, manifest, err)
	}
	_, err = worker.PublishReport(report)
	requireError(t, err, ErrExists)
	report.ReportID = "different-id"
	_, err = worker.PublishReport(report)
	requireError(t, err, ErrExists)
	if _, _, err := worker.ReadReport(2, report.ReportID); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("duplicate request produced committed report: %v", err)
	}
	for _, status := range []string{"DONE", "FAILED", "BLOCKED_CONFIRM"} {
		report.ReportID, report.RequestID, report.Status = "result-"+status, "", status
		if _, err := worker.PublishReport(report); err != nil {
			t.Fatal(err)
		}
	}
	review, err := lead.Review([]ReportRef{ref})
	if err != nil || len(review) != 1 || review[0].Meaning != "agent_claim" || review[0].Verification != "INTEGRITY_VERIFIED" || review[0].ReportSHA256 != m.File.SHA256 {
		t.Fatalf("review: %+v %v", review, err)
	}
	if _, err := lead.Merge("v1", "merged", []ReportRef{ref}); err != nil {
		t.Fatal(err)
	}
	merged, _, err := lead.Read("merged")
	if err != nil || !strings.Contains(merged.Progress.Text, `"status":"DONE"`) || len(merged.Progress.References) != 2 || merged.Pending.Text != "worker: Await lead review\n" {
		t.Fatalf("merged summary: %+v %v", merged, err)
	}
	if strings.Contains(merged.Progress.Text, "private full test output") || strings.Contains(merged.Progress.Text, "PASS") {
		t.Fatal("merged sensitive evidence or invented acceptance")
	}
	old, _, err := lead.Read("v1")
	if err != nil || old.Progress.Text != "Implementation complete" || merged.Background.Text != old.Background.Text || merged.Decisions.Text != old.Decisions.Text {
		t.Fatalf("base revision changed: %+v %v", old, err)
	}
	got, _, err = worker.ReadReport(2, ref.ReportID)
	if err != nil || got.Status != "DONE" || got.RequestID != "dispatch-1" {
		t.Fatalf("source report changed: %+v %v", got, err)
	}
	_, err = lead.Merge("v1", "merged", []ReportRef{ref})
	requireError(t, err, ErrExists)
	_, err = worker.Review([]ReportRef{ref})
	requireError(t, err, ErrIdentity)
	if err := filepath.Walk(r.base, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		want := os.FileMode(0600)
		if info.IsDir() {
			want = 0700
		}
		if info.Mode().Perm() != want || strings.HasPrefix(info.Name(), ".tmp-") {
			t.Fatalf("unsafe artifact: %s %v", path, info.Mode())
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestReportInvalidAndUnsafeInputs(t *testing.T) {
	for _, fault := range []string{"run", "author", "version", "generation", "id", "message", "request", "status", "summary", "next", "missing-evidence", "many-evidence", "duplicate-evidence", "relative", "traversal", "cross-agent", "cross-generation", "cross-run", "missing-file", "checksum", "utf8"} {
		t.Run(fault, func(t *testing.T) {
			r, _, worker, report := reportFixture(t)
			switch fault {
			case "run":
				report.RunID = "other"
			case "author":
				report.AgentID = "main"
			case "version":
				report.Version++
			case "generation":
				report.Generation = 0
			case "id":
				report.ReportID = "../other"
			case "message":
				report.MessageID = "../message"
			case "request":
				report.RequestID = "../request"
			case "status":
				report.Status = "PASS"
			case "summary":
				report.Summary = strings.Repeat("x", 2049)
			case "next":
				report.Next = " "
			case "missing-evidence":
				report.Evidence = nil
			case "many-evidence":
				report.Evidence = make([]Evidence, 9)
			case "duplicate-evidence":
				report.Evidence = append(report.Evidence, report.Evidence[0])
			case "relative":
				report.Evidence[0].Path = "relative"
			case "traversal":
				report.Evidence[0].Path = worker.Path() + "/.evidence/g-2/../test-log"
			case "cross-agent":
				report.Evidence[0].Path = filepath.Join(r.Path(), "main", ".evidence", "g-2", "test-log")
			case "cross-generation":
				report.Evidence[0].Path = filepath.Join(worker.Path(), ".evidence", "g-1", "test-log")
			case "cross-run":
				report.Evidence[0].Path = filepath.Join(r.base, "other", "worker", ".evidence", "g-2", "test-log")
			case "missing-file":
				report.Evidence[0].Path = filepath.Join(worker.Path(), ".evidence", "g-2", "missing")
			case "checksum":
				report.Evidence[0].SHA256 = strings.Repeat("0", 64)
			case "utf8":
				report.Summary = string([]byte{255})
			}
			if _, err := worker.PublishReport(report); err == nil {
				t.Fatal("accepted invalid report")
			}
			if _, err := os.Stat(filepath.Join(worker.Path(), ".reports")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("invalid report reserved storage: %v", err)
			}
		})
	}
}

func TestReportCorruptionAndUnsafeFilesRejectMerge(t *testing.T) {
	for _, fault := range []string{"missing-manifest", "missing-report", "report-checksum", "unknown-report", "unknown-manifest", "manifest-path", "manifest-author", "manifest-generation", "report-generation", "request", "missing-evidence", "evidence-checksum", "evidence-public", "evidence-symlink", "evidence-dir-symlink", "report-public", "report-symlink", "generation-symlink", "incomplete"} {
		t.Run(fault, func(t *testing.T) {
			_, lead, worker, report := reportFixture(t)
			m, err := worker.PublishReport(report)
			if err != nil {
				t.Fatal(err)
			}
			dir := filepath.Join(worker.Path(), ".reports", "g-2", report.ReportID)
			manifest, doc := filepath.Join(dir, "manifest.json"), filepath.Join(dir, "report.json")
			remove := func(path string) {
				t.Helper()
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			}
			link := func(path string) {
				t.Helper()
				if err := os.Rename(path, path+"-real"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(path+"-real", path); err != nil {
					t.Fatal(err)
				}
			}
			switch fault {
			case "missing-manifest", "incomplete":
				remove(manifest)
			case "missing-report":
				remove(doc)
			case "report-checksum":
				overwrite(t, doc, []byte("{}"))
			case "unknown-report":
				body, _ := os.ReadFile(doc)
				body = append([]byte(`{"unknown":true,`), body[1:]...)
				overwrite(t, doc, body)
				m.File.SHA256 = digest(body)
			case "unknown-manifest":
				body, _ := os.ReadFile(manifest)
				overwrite(t, manifest, append([]byte(`{"unknown":true,`), body[1:]...))
			case "manifest-path":
				m.File.Path = "../../other"
			case "manifest-author":
				m.AgentID = "main"
			case "manifest-generation":
				m.Generation = 1
			case "report-generation":
				report.Generation = 1
				body, _ := encode(report)
				overwrite(t, doc, body)
				m.File.SHA256 = digest(body)
			case "request":
				overwrite(t, filepath.Join(filepath.Dir(dir), ".request-"+digest([]byte(report.RequestID))), []byte("another\n"))
			case "missing-evidence":
				remove(report.Evidence[0].Path)
			case "evidence-checksum":
				overwrite(t, report.Evidence[0].Path, []byte("changed"))
			case "evidence-public":
				if err := os.Chmod(report.Evidence[0].Path, 0644); err != nil {
					t.Fatal(err)
				}
			case "evidence-symlink":
				link(report.Evidence[0].Path)
			case "evidence-dir-symlink":
				link(filepath.Dir(report.Evidence[0].Path))
			case "report-public":
				if err := os.Chmod(doc, 0644); err != nil {
					t.Fatal(err)
				}
			case "report-symlink":
				link(doc)
			case "generation-symlink":
				link(filepath.Dir(dir))
			}
			if fault == "unknown-report" || fault == "report-generation" || strings.HasPrefix(fault, "manifest-") {
				body, _ := encode(m)
				overwrite(t, manifest, body)
			}
			ref := ReportRef{AgentID: "worker", Generation: 2, ReportID: "result-1"}
			if _, err := lead.Merge("v1", "merged", []ReportRef{ref}); err == nil {
				t.Fatal("merged corrupt report")
			}
			if _, err := os.Stat(filepath.Join(lead.Path(), "merged")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("failed merge published revision: %v", err)
			}
		})
	}
}

func TestConcurrentReportAndRequestReservation(t *testing.T) {
	for _, sameID := range []bool{true, false} {
		t.Run(map[bool]string{true: "report-id", false: "request-id"}[sameID], func(t *testing.T) {
			_, _, worker, report := reportFixture(t)
			var wg sync.WaitGroup
			errs := make(chan error, 8)
			for i := range 8 {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					r := report
					if !sameID {
						r.ReportID = generationDir(int64(i + 1))
					}
					_, err := worker.PublishReport(r)
					errs <- err
				}(i)
			}
			wg.Wait()
			close(errs)
			winners := 0
			for err := range errs {
				if err == nil {
					winners++
				} else {
					requireError(t, err, ErrExists)
				}
			}
			if winners != 1 {
				t.Fatalf("winners: %d", winners)
			}
		})
	}
}

func TestReportSameIdentifiersRemainIsolatedAcrossGenerations(t *testing.T) {
	_, _, worker, original := reportFixture(t)
	if _, err := worker.PublishReport(original); err != nil {
		t.Fatal(err)
	}
	current := original
	current.Generation = 3
	current.Summary = "New generation result"
	evidence, err := worker.PublishEvidence(3, "test-log", "new generation test", []byte("new evidence"))
	if err != nil {
		t.Fatal(err)
	}
	current.Evidence = []Evidence{*evidence}
	if _, err := worker.PublishReport(current); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []Report{original, current} {
		got, _, err := worker.ReadReport(expected.Generation, expected.ReportID)
		if err != nil || !reflect.DeepEqual(got, &expected) {
			t.Fatalf("generation was overwritten: %+v %v", got, err)
		}
	}
}

func TestMergeCompactLimitsAndSelection(t *testing.T) {
	_, lead, worker, report := reportFixture(t)
	report.RequestID = ""
	for i := range 4 {
		report.ReportID = generationDir(int64(i + 1))
		report.Summary = strings.Repeat("x", 2048)
		if _, err := worker.PublishReport(report); err != nil {
			t.Fatal(err)
		}
	}
	refs := []ReportRef{}
	for i := range 4 {
		refs = append(refs, ReportRef{AgentID: "worker", Generation: 2, ReportID: generationDir(int64(i + 1))})
	}
	if _, err := lead.Merge("v1", "too-large", refs); !errors.Is(err, ErrInvalid) {
		t.Fatalf("accepted oversized merged context: %v", err)
	}
	for _, selected := range [][]ReportRef{nil, {refs[0], refs[0]}, {{AgentID: "main", Generation: 2, ReportID: "r"}}} {
		if _, err := lead.Review(selected); err == nil {
			t.Fatal("accepted invalid review selection")
		}
	}
	_, err := worker.PublishEvidence(2, "test-log", "existing", []byte("overwrite"))
	requireError(t, err, ErrExists)
	for _, body := range [][]byte{nil, make([]byte, maxFileSize+1)} {
		if _, err := worker.PublishEvidence(2, "new", "test", body); !errors.Is(err, ErrInvalid) {
			t.Fatal(err)
		}
	}
	// The merged JSON summary stays machine readable and is not interpreted as an acceptance instruction.
	if _, err := lead.Merge("v1", "small", refs[:1]); err != nil {
		t.Fatal(err)
	}
	s, _, err := lead.Read("small")
	if err != nil {
		t.Fatal(err)
	}
	var reviewed []ReviewedReport
	if err := json.Unmarshal([]byte(s.Progress.Text), &reviewed); err != nil || len(reviewed) != 1 {
		t.Fatalf("summary: %v", err)
	}
}
