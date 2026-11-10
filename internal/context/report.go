package context

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Evidence is an opaque, private artifact reference. Reports and merged context
// retain only its path, purpose and checksum, never the evidence body.
type Evidence struct {
	Path    string `json:"path"`
	Purpose string `json:"purpose"`
	SHA256  string `json:"sha256"`
}

// RequestID, when present, is the originating S5 dispatch request ID (not a new
// message or ACK request). Runtime validates it against MessageID or resolves it
// among dispatches from the frozen lead to this author.
type Report struct {
	Version    int        `json:"version"`
	RunID      string     `json:"run_id"`
	AgentID    string     `json:"agent_id"`
	Generation int64      `json:"generation"`
	ReportID   string     `json:"report_id"`
	MessageID  string     `json:"message_id,omitempty"`
	RequestID  string     `json:"request_id,omitempty"`
	Status     string     `json:"status"`
	Summary    string     `json:"summary"`
	Evidence   []Evidence `json:"evidence"`
	Next       string     `json:"next"`
}

type ReportRef struct {
	AgentID    string `json:"agent_id"`
	Generation int64  `json:"generation"`
	ReportID   string `json:"report_id"`
}

type ReportManifest struct {
	Version    int       `json:"version"`
	RunID      string    `json:"run_id"`
	AgentID    string    `json:"agent_id"`
	Generation int64     `json:"generation"`
	ReportID   string    `json:"report_id"`
	MessageID  string    `json:"message_id,omitempty"`
	RequestID  string    `json:"request_id,omitempty"`
	UpdatedAt  time.Time `json:"updated_at"`
	File       Entry     `json:"file"`
}

// ReviewedReport verifies integrity and identity only. Status remains an agent
// claim; this model deliberately has no PASS or acceptance field.
type ReviewedReport struct {
	ReportRef
	Status       string     `json:"status"`
	Summary      string     `json:"summary"`
	Evidence     []Evidence `json:"evidence"`
	Next         string     `json:"next"`
	ReportSHA256 string     `json:"report_sha256"`
	Verification string     `json:"verification"`
	Meaning      string     `json:"meaning"`
}

func generationDir(generation int64) string { return "g-" + strconv.FormatInt(generation, 10) }

func ensureChild(r *os.Root, name string) (*os.Root, error) {
	if err := r.Mkdir(name, 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return nil, err
	}
	if err := syncDir(r); err != nil {
		return nil, err
	}
	return child(r, name, true)
}

func (a *Agent) artifactDirectory(kind string, generation int64, create bool) (*os.Root, error) {
	if generation < 1 {
		return nil, fmt.Errorf("%w: report generation", ErrInvalid)
	}
	r, err := a.directory()
	if err != nil {
		return nil, err
	}
	defer r.Close()
	open := func(r *os.Root, name string) (*os.Root, error) { return child(r, name, true) }
	if create {
		open = ensureChild
	}
	artifacts, err := open(r, kind)
	if err != nil {
		return nil, err
	}
	defer artifacts.Close()
	return open(artifacts, generationDir(generation))
}

// PublishEvidence uses the same immutable publication and permission contract as
// context files. The runtime service must authorize the current generation first.
func (a *Agent) PublishEvidence(generation int64, name, purpose string, body []byte) (*Evidence, error) {
	if !identifier.MatchString(name) || strings.TrimSpace(purpose) == "" || !text(purpose, 512) || len(body) == 0 || len(body) > maxFileSize {
		return nil, fmt.Errorf("%w: evidence name, purpose or size", ErrInvalid)
	}
	r, err := a.artifactDirectory(".evidence", generation, true)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	if err := publish(r, name, body); err != nil {
		return nil, err
	}
	return &Evidence{Path: filepath.Join(a.Path(), ".evidence", generationDir(generation), name), Purpose: purpose, SHA256: digest(body)}, nil
}

func (a *Agent) validateReport(r Report) error {
	if r.RunID != a.run.ID() || r.AgentID != a.id {
		return fmt.Errorf("%w: report author/run", ErrIdentity)
	}
	if r.Version != Version || r.Generation < 1 || !identifier.MatchString(r.ReportID) ||
		(r.MessageID != "" && !identifier.MatchString(r.MessageID)) || (r.RequestID != "" && !identifier.MatchString(r.RequestID)) ||
		(r.Status != "DONE" && r.Status != "FAILED" && r.Status != "BLOCKED_CONFIRM") ||
		strings.TrimSpace(r.Summary) == "" || !text(r.Summary, 2048) || strings.TrimSpace(r.Next) == "" || !text(r.Next, 2048) ||
		len(r.Evidence) == 0 || len(r.Evidence) > 8 {
		return fmt.Errorf("%w: compact report fields", ErrInvalid)
	}
	seen := map[string]bool{}
	for _, e := range r.Evidence {
		if !absolute(e.Path) || len(e.Path) > 4096 || filepath.Dir(e.Path) != filepath.Join(a.Path(), ".evidence", generationDir(r.Generation)) ||
			!identifier.MatchString(filepath.Base(e.Path)) || strings.TrimSpace(e.Purpose) == "" || !text(e.Purpose, 512) || !hexDigest.MatchString(e.SHA256) || seen[e.Path] {
			return fmt.Errorf("%w: evidence reference must belong to report author/generation", ErrUnsafe)
		}
		seen[e.Path] = true
	}
	return nil
}

func (a *Agent) verifyEvidence(r Report) error {
	evidence, err := a.artifactDirectory(".evidence", r.Generation, false)
	if err != nil {
		return fmt.Errorf("%w: evidence directory: %w", ErrCorrupt, err)
	}
	defer evidence.Close()
	for _, e := range r.Evidence {
		body, err := readFile(evidence, filepath.Base(e.Path))
		if err != nil {
			return err
		}
		if len(body) == 0 || digest(body) != e.SHA256 {
			return fmt.Errorf("%w: evidence SHA-256 mismatch", ErrCorrupt)
		}
	}
	return nil
}

// PublishReport reserves an immutable report ID and, if provided, one dispatch
// request ID per author/generation. Duplicate IDs return ErrExists. A failed
// publication leaves an explicit incomplete reservation, never overwrites it.
// This file layer binds identity; runtime additionally gates current S5 state.
func (a *Agent) PublishReport(report Report) (*ReportManifest, error) {
	if err := a.validateReport(report); err != nil {
		return nil, err
	}
	body, err := encode(report)
	if err != nil {
		return nil, err
	}
	if len(body) > maxFileSize {
		return nil, fmt.Errorf("%w: encoded report size", ErrInvalid)
	}
	if err := a.verifyEvidence(report); err != nil {
		return nil, err
	}
	r, err := a.artifactDirectory(".reports", report.Generation, true)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	if err := r.Mkdir(report.ReportID, 0700); err != nil {
		if errors.Is(err, os.ErrExist) {
			return nil, fmt.Errorf("%w: report ID", ErrExists)
		}
		return nil, err
	}
	rev, err := child(r, report.ReportID, true)
	if err != nil {
		return nil, err
	}
	defer rev.Close()
	if report.RequestID != "" {
		if err := publish(r, ".request-"+digest([]byte(report.RequestID)), []byte(report.ReportID+"\n")); err != nil {
			return nil, err
		}
	}
	m := &ReportManifest{Version: Version, RunID: report.RunID, AgentID: report.AgentID, Generation: report.Generation,
		ReportID: report.ReportID, MessageID: report.MessageID, RequestID: report.RequestID, UpdatedAt: time.Now().UTC()}
	m.File = Entry{Path: "report.json", Purpose: "independent agent report", SHA256: digest(body), UpdatedAt: m.UpdatedAt}
	if err := publish(rev, "report.json", body); err != nil {
		return nil, err
	}
	manifest, err := encode(m)
	if err != nil {
		return nil, err
	}
	if err := publish(rev, "manifest.json", manifest); err != nil {
		return nil, err
	}
	if err := syncDir(r); err != nil {
		return nil, err
	}
	return m, nil
}

func (a *Agent) ReadReport(generation int64, reportID string) (*Report, *ReportManifest, error) {
	if !identifier.MatchString(reportID) {
		return nil, nil, fmt.Errorf("%w: report ID", ErrInvalid)
	}
	r, err := a.artifactDirectory(".reports", generation, false)
	if err != nil {
		return nil, nil, err
	}
	defer r.Close()
	rev, err := child(r, reportID, true)
	if err != nil {
		return nil, nil, err
	}
	defer rev.Close()
	body, err := readFile(rev, "manifest.json")
	if err != nil {
		return nil, nil, err
	}
	var m ReportManifest
	if err := decode(body, &m); err != nil {
		return nil, nil, err
	}
	if m.Version != Version || m.RunID != a.run.ID() || m.AgentID != a.id || m.Generation != generation || m.ReportID != reportID ||
		m.UpdatedAt.IsZero() || m.File.Path != "report.json" || m.File.Purpose != "independent agent report" ||
		!hexDigest.MatchString(m.File.SHA256) || !m.File.UpdatedAt.Equal(m.UpdatedAt) {
		return nil, nil, fmt.Errorf("%w: report manifest", ErrCorrupt)
	}
	body, err = readFile(rev, "report.json")
	if err != nil {
		return nil, nil, err
	}
	if digest(body) != m.File.SHA256 {
		return nil, nil, fmt.Errorf("%w: report SHA-256 mismatch", ErrCorrupt)
	}
	var report Report
	if err := decode(body, &report); err != nil {
		return nil, nil, err
	}
	if err := a.validateReport(report); err != nil {
		return nil, nil, err
	}
	if report.Generation != generation || report.ReportID != reportID || report.MessageID != m.MessageID || report.RequestID != m.RequestID {
		return nil, nil, fmt.Errorf("%w: report/manifest identity", ErrCorrupt)
	}
	if report.RequestID != "" {
		reservation, err := readFile(r, ".request-"+digest([]byte(report.RequestID)))
		if err != nil {
			return nil, nil, err
		}
		if string(reservation) != reportID+"\n" {
			return nil, nil, fmt.Errorf("%w: report request reservation", ErrCorrupt)
		}
	}
	if err := a.verifyEvidence(report); err != nil {
		return nil, nil, err
	}
	return &report, &m, nil
}

// Review reads executor artifacts only. Callers cannot supply a report body or
// acceptance result to merge. Runtime must also validate all current authors.
func (a *Agent) Review(refs []ReportRef) ([]ReviewedReport, error) {
	if a.id != a.run.Lead() {
		return nil, fmt.Errorf("%w: report review requires lead", ErrIdentity)
	}
	if len(refs) == 0 || len(refs) > 4 {
		return nil, fmt.Errorf("%w: review requires 1..4 reports", ErrInvalid)
	}
	seen := map[ReportRef]bool{}
	result := make([]ReviewedReport, 0, len(refs))
	for _, ref := range refs {
		if ref.AgentID == a.id || seen[ref] {
			return nil, fmt.Errorf("%w: executor or duplicate report", ErrIdentity)
		}
		seen[ref] = true
		author, err := a.run.Agent(ref.AgentID)
		if err != nil {
			return nil, err
		}
		r, m, err := author.ReadReport(ref.Generation, ref.ReportID)
		if err != nil {
			return nil, err
		}
		result = append(result, ReviewedReport{ReportRef: ref, Status: r.Status, Summary: r.Summary, Evidence: r.Evidence,
			Next: r.Next, ReportSHA256: m.File.SHA256, Verification: "INTEGRITY_VERIFIED", Meaning: "agent_claim"})
	}
	return result, nil
}

// Merge publishes a new lead revision. It replaces progress/pending with the
// explicitly selected batch; previous revisions and source reports stay intact.
func (a *Agent) Merge(baseRevision, revision string, refs []ReportRef) (*Manifest, error) {
	reports, err := a.Review(refs)
	if err != nil {
		return nil, err
	}
	s, _, err := a.Read(baseRevision)
	if err != nil {
		return nil, err
	}
	body, err := encode(reports)
	if err != nil {
		return nil, err
	}
	s.Revision = revision
	s.Progress = Section{Text: string(body)}
	s.Pending = Section{}
	for _, report := range reports {
		s.Progress.References = append(s.Progress.References, Reference{Path: filepath.Join(a.run.Path(), report.AgentID, ".reports", generationDir(report.Generation), report.ReportID, "manifest.json"), Purpose: "verified independent report; agent claim"})
		for _, e := range report.Evidence {
			s.Progress.References = append(s.Progress.References, Reference{Path: e.Path, Purpose: e.Purpose})
		}
		s.Pending.Text += report.AgentID + ": " + report.Next + "\n"
	}
	return a.Publish(*s)
}
