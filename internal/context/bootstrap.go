package context

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
)

// Bootstrap is a compact file continuation package, never a native session or a
// transcript. References to prompt/evidence bodies remain references only.
type Bootstrap struct {
	Version              int              `json:"version"`
	RunID                string           `json:"run_id"`
	AgentID              string           `json:"agent_id"`
	Generation           int64            `json:"generation"`
	Revision             string           `json:"revision"`
	Protocol             Protocol         `json:"protocol"`
	Background           Section          `json:"background"`
	Decisions            Section          `json:"decisions"`
	Progress             Section          `json:"progress"`
	Pending              Section          `json:"pending"`
	Reports              []ReviewedReport `json:"reports"`
	Files                []Reference      `json:"files"`
	Seat                 BootstrapSeat    `json:"seat"`
	Recommendation       string           `json:"recommendation"`
	Next                 string           `json:"next"`
	Executed             bool             `json:"executed"`
	RuntimeCommunication string           `json:"runtime_communication"`
}

// BootstrapSeat is persisted evidence only; it does not verify a live binding.
type BootstrapSeat struct {
	Status           string `json:"status"`
	NativeSessionID  string `json:"native_session_id"`
	WorkspaceID      string `json:"workspace_id"`
	SurfaceID        string `json:"surface_id"`
	CurrentMessageID string `json:"current_message_id"`
	StopRequested    bool   `json:"stop_requested"`
}

// Latest validates every visible revision and selects by committed manifest time,
// with revision name as a deterministic tie-breaker. Incomplete/corrupt revisions
// fail closed; we never silently roll back to older progress. Dot artifact dirs
// belong to separate namespaces and are not revisions.
func (a *Agent) Latest() (*Snapshot, *Manifest, error) {
	r, err := a.directory()
	if err != nil {
		return nil, nil, err
	}
	defer r.Close()
	f, err := r.Open(".")
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	entries, err := f.ReadDir(-1)
	if err != nil {
		return nil, nil, err
	}
	var latest *Snapshot
	var manifest *Manifest
	for _, e := range entries {
		if e.Name() == "agent.json" || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if !identifier.MatchString(e.Name()) {
			return nil, nil, fmt.Errorf("%w: unexpected revision entry", ErrCorrupt)
		}
		s, m, err := a.Read(e.Name())
		if err != nil {
			return nil, nil, err
		}
		if manifest == nil || m.UpdatedAt.After(manifest.UpdatedAt) || (m.UpdatedAt.Equal(manifest.UpdatedAt) && m.Revision > manifest.Revision) {
			latest, manifest = s, m
		}
	}
	if latest == nil {
		return nil, nil, fmt.Errorf("%w: no committed revision", ErrCorrupt)
	}
	return latest, manifest, nil
}

func (a *Agent) latestReport(generation int64) (*Report, *ReportManifest, error) {
	r, err := a.artifactDirectory(".reports", generation, false)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	defer r.Close()
	f, err := r.Open(".")
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	entries, err := f.ReadDir(-1)
	if err != nil {
		return nil, nil, err
	}
	var report *Report
	var manifest *ReportManifest
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".request-") || strings.HasPrefix(e.Name(), ".tmp-") {
			continue
		}
		r, m, err := a.ReadReport(generation, e.Name())
		if err != nil {
			return nil, nil, err
		}
		if manifest == nil || m.UpdatedAt.After(manifest.UpdatedAt) || (m.UpdatedAt.Equal(manifest.UpdatedAt) && m.ReportID > manifest.ReportID) {
			report, manifest = r, m
		}
	}
	return report, manifest, nil
}

func (a *Agent) reportPath(ref ReportRef) string {
	return filepath.Join(a.run.Path(), ref.AgentID, ".reports", generationDir(ref.Generation), ref.ReportID, "manifest.json")
}

// PrepareBootstrap reads only managed compact context/report artifacts. Runtime
// must authorize current identity and the returned report bindings before publish.
func (a *Agent) PrepareBootstrap(generation int64) (*Bootstrap, []Report, error) {
	if generation < 1 {
		return nil, nil, fmt.Errorf("%w: bootstrap generation", ErrInvalid)
	}
	s, _, err := a.Latest()
	if err != nil {
		return nil, nil, err
	}
	b := &Bootstrap{Version: Version, RunID: s.RunID, AgentID: s.AgentID, Generation: generation, Revision: s.Revision,
		Protocol: s.Protocol, Background: s.Background, Decisions: s.Decisions, Progress: s.Progress, Pending: s.Pending,
		Reports: []ReviewedReport{}, RuntimeCommunication: "UNVERIFIED"}
	b.Files = []Reference{{Path: filepath.Join(a.run.Path(), "run.json"), Purpose: "run identity"},
		{Path: filepath.Join(a.Path(), "agent.json"), Purpose: "agent identity"},
		{Path: filepath.Join(a.Path(), s.Revision, "manifest.json"), Purpose: "verified latest context"}, s.Protocol.FrozenPrompt}
	if err := a.bootstrapReference(s.Protocol.FrozenPrompt, s, nil); err != nil {
		return nil, nil, err
	}
	allowed := map[string]bool{}
	var bindings []Report
	// Only a lead's exact, integrity-verified Merge output may reference other
	// agents. Arbitrary cross-agent context/report references are rejected.
	mergedReferences := false
	for _, ref := range s.Progress.References {
		rel, err := filepath.Rel(a.run.Path(), ref.Path)
		parts := strings.Split(rel, string(filepath.Separator))
		mergedReferences = mergedReferences || (err == nil && len(parts) == 5 && parts[0] != a.id && parts[1] == ".reports")
	}
	if a.id == a.run.Lead() && mergedReferences {
		var merged []ReviewedReport
		if err := decode([]byte(s.Progress.Text), &merged); err != nil {
			return nil, nil, err
		}
		refs := make([]ReportRef, len(merged))
		for i, m := range merged {
			refs[i] = m.ReportRef
		}
		verified, err := a.Review(refs)
		if err != nil {
			return nil, nil, err
		}
		if !reflect.DeepEqual(verified, merged) {
			return nil, nil, fmt.Errorf("%w: merged report summary", ErrCorrupt)
		}
		expectedReferences := []Reference{}
		for _, m := range merged {
			allowed[a.reportPath(m.ReportRef)] = true
			expectedReferences = append(expectedReferences, Reference{Path: a.reportPath(m.ReportRef), Purpose: "verified independent report; agent claim"})
			for _, e := range m.Evidence {
				allowed[e.Path] = true
				expectedReferences = append(expectedReferences, Reference{Path: e.Path, Purpose: e.Purpose})
			}
			author, _ := a.run.Agent(m.AgentID)
			r, _, err := author.ReadReport(m.Generation, m.ReportID)
			if err != nil {
				return nil, nil, err
			}
			bindings = append(bindings, *r)
		}
		if !reflect.DeepEqual(s.Progress.References, expectedReferences) {
			return nil, nil, fmt.Errorf("%w: merged report references", ErrCorrupt)
		}
		b.Reports = merged
	}
	r, m, err := a.latestReport(generation)
	if err != nil {
		return nil, nil, err
	}
	if r != nil {
		ref := ReportRef{AgentID: a.id, Generation: generation, ReportID: r.ReportID}
		b.Reports = append(b.Reports, ReviewedReport{ReportRef: ref, Status: r.Status, Summary: r.Summary, Evidence: r.Evidence,
			Next: r.Next, ReportSHA256: m.File.SHA256, Verification: "INTEGRITY_VERIFIED", Meaning: "agent_claim"})
		b.Files = append(b.Files, Reference{Path: a.reportPath(ref), Purpose: "latest own report; agent claim"})
		allowed[a.reportPath(ref)] = true
		for _, e := range r.Evidence {
			allowed[e.Path] = true
		}
		bindings = append(bindings, *r)
	}
	for _, section := range []Section{s.Background, s.Decisions, s.Progress, s.Pending} {
		for _, ref := range section.References {
			if err := a.bootstrapReference(ref, s, allowed); err != nil {
				return nil, nil, err
			}
			b.Files = append(b.Files, ref)
		}
	}
	for _, r := range b.Reports {
		for _, e := range r.Evidence {
			b.Files = append(b.Files, Reference{Path: e.Path, Purpose: e.Purpose})
		}
	}
	// Stable deduplication prevents repeated evidence references in the package.
	sort.Slice(b.Files, func(i, j int) bool {
		if b.Files[i].Path != b.Files[j].Path {
			return b.Files[i].Path < b.Files[j].Path
		}
		if b.Files[i].Fragment != b.Files[j].Fragment {
			return b.Files[i].Fragment < b.Files[j].Fragment
		}
		return b.Files[i].Purpose < b.Files[j].Purpose
	})
	files := b.Files[:0]
	for _, ref := range b.Files {
		if len(files) == 0 || files[len(files)-1] != ref {
			files = append(files, ref)
		}
	}
	b.Files = files
	return b, bindings, nil
}

func (a *Agent) bootstrapReference(ref Reference, s *Snapshot, allowed map[string]bool) error {
	rel, err := filepath.Rel(a.run.base, ref.Path)
	if err != nil {
		return err
	}
	external := rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator))
	if ref.Path == s.Protocol.FrozenPrompt.Path {
		if !external && rel != filepath.Join(a.run.ID(), "config.resolved.json") {
			return fmt.Errorf("%w: frozen prompt points into agent context", ErrIdentity)
		}
		if ref.Fragment == "" || ref.Fragment == s.Protocol.FrozenPrompt.Fragment {
			return nil
		}
		return fmt.Errorf("%w: foreign frozen prompt fragment", ErrIdentity)
	}
	if external {
		return nil
	} // opaque external evidence
	if allowed[ref.Path] {
		return nil
	}
	parts := strings.Split(rel, string(filepath.Separator))
	if len(parts) != 4 || parts[0] != a.run.ID() || parts[1] != a.id || !identifier.MatchString(parts[2]) {
		return fmt.Errorf("%w: cross-agent or uncommitted context reference", ErrIdentity)
	}
	if parts[3] != "manifest.json" {
		found := false
		for _, kind := range kinds {
			found = found || parts[3] == kind+".json"
		}
		if !found {
			return fmt.Errorf("%w: unmanaged context reference", ErrIdentity)
		}
	}
	_, _, err = a.Read(parts[2])
	return err
}

// PublishBootstrap is content addressed and no-replacement. Existing bytes are
// verified on every read; corrupt cache files are never silently regenerated.
func (a *Agent) PublishBootstrap(b *Bootstrap) (string, error) {
	if b == nil || b.RunID != a.run.ID() || b.AgentID != a.id || b.Generation < 1 || b.Executed || b.RuntimeCommunication != "UNVERIFIED" {
		return "", fmt.Errorf("%w: bootstrap identity or execution claim", ErrIdentity)
	}
	body, err := encode(b)
	if err != nil {
		return "", err
	}
	if len(body) > maxFileSize {
		return "", fmt.Errorf("%w: bootstrap exceeds size limit", ErrInvalid)
	}
	r, err := a.artifactDirectory(".bootstrap", b.Generation, true)
	if err != nil {
		return "", err
	}
	defer r.Close()
	name := digest(body) + ".json"
	if err := publish(r, name, body); err != nil {
		if !errors.Is(err, ErrExists) {
			return "", err
		}
		existing, err := readFile(r, name)
		if err != nil {
			return "", err
		}
		if !bytes.Equal(existing, body) {
			return "", fmt.Errorf("%w: bootstrap cache digest mismatch", ErrCorrupt)
		}
	}
	return filepath.Join(a.Path(), ".bootstrap", generationDir(b.Generation), name), nil
}
