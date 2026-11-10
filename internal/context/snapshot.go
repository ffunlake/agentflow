package context

import (
	"errors"
	"fmt"
	"os"
	"time"
)

// Publish reserves a revision exactly once. manifest.json is published last;
// readers reject incomplete revisions. A failed or ambiguous publication must
// be inspected/read explicitly, never retried by overwriting that revision.
func (a *Agent) Publish(s Snapshot) (*Manifest, error) {
	if s.RunID != a.run.desc.RunID || s.AgentID != a.id || s.Protocol.ConfigDigest != a.run.desc.ConfigDigest {
		return nil, fmt.Errorf("%w: snapshot does not match bound run/agent/config", ErrIdentity)
	}
	if err := s.validate(); err != nil {
		return nil, err
	}
	sections := []*Section{nil, &s.Background, &s.Decisions, &s.Progress, &s.Pending}
	m := &Manifest{Version: Version, RunID: s.RunID, AgentID: s.AgentID, Revision: s.Revision, UpdatedAt: time.Now().UTC()}
	bodies := make([][]byte, len(kinds))
	for i, kind := range kinds {
		d := document{Version: Version, RunID: s.RunID, AgentID: s.AgentID, Revision: s.Revision,
			Kind: kind, UpdatedAt: m.UpdatedAt, Content: sections[i]}
		if kind == "role" {
			d.Protocol = &s.Protocol
		}
		body, err := encode(d)
		if err != nil {
			return nil, err
		}
		if len(body) > maxFileSize {
			return nil, fmt.Errorf("%w: encoded document exceeds size limit", ErrInvalid)
		}
		bodies[i] = body
		name := kind + ".json"
		m.Files = append(m.Files, Entry{Path: name, Purpose: kind, SHA256: digest(body), UpdatedAt: m.UpdatedAt})
	}
	r, err := a.directory()
	if err != nil {
		return nil, err
	}
	defer r.Close()
	if err := r.Mkdir(s.Revision, 0700); err != nil {
		if errors.Is(err, os.ErrExist) {
			return nil, fmt.Errorf("%w: revision", ErrExists)
		}
		return nil, err
	}
	rev, err := child(r, s.Revision, true)
	if err != nil {
		return nil, err
	}
	defer rev.Close()
	for i, kind := range kinds {
		if err := publish(rev, kind+".json", bodies[i]); err != nil {
			return nil, err
		}
	}
	body, err := encode(m)
	if err != nil {
		return nil, err
	}
	if err := publish(rev, "manifest.json", body); err != nil {
		return nil, err
	}
	if err := syncDir(r); err != nil {
		return nil, err
	}
	return m, nil
}

// Read validates the entire revision, including all referenced managed files.
// Manifest paths are a fixed allowlist and are never used as arbitrary paths.
func (a *Agent) Read(revision string) (*Snapshot, *Manifest, error) {
	if !identifier.MatchString(revision) {
		return nil, nil, fmt.Errorf("%w: revision", ErrInvalid)
	}
	r, err := a.directory()
	if err != nil {
		return nil, nil, err
	}
	defer r.Close()
	rev, err := child(r, revision, true)
	if err != nil {
		return nil, nil, err
	}
	defer rev.Close()
	body, err := readFile(rev, "manifest.json")
	if err != nil {
		return nil, nil, err
	}
	var m Manifest
	if err := decode(body, &m); err != nil {
		return nil, nil, err
	}
	if m.Version != Version || m.RunID != a.run.desc.RunID || m.AgentID != a.id || m.Revision != revision || m.UpdatedAt.IsZero() || len(m.Files) != len(kinds) {
		return nil, nil, fmt.Errorf("%w: manifest version, identity or file count", ErrCorrupt)
	}
	s := &Snapshot{Version: Version, RunID: m.RunID, AgentID: m.AgentID, Revision: revision}
	sections := []*Section{nil, &s.Background, &s.Decisions, &s.Progress, &s.Pending}
	for i, kind := range kinds {
		e := m.Files[i]
		if e.Path != kind+".json" || e.Purpose != kind || !hexDigest.MatchString(e.SHA256) || !e.UpdatedAt.Equal(m.UpdatedAt) {
			return nil, nil, fmt.Errorf("%w: manifest file path, purpose, digest or timestamp", ErrCorrupt)
		}
		body, err := readFile(rev, kind+".json")
		if err != nil {
			return nil, nil, err
		}
		if digest(body) != e.SHA256 {
			return nil, nil, fmt.Errorf("%w: %s SHA-256 mismatch", ErrCorrupt, kind)
		}
		var d document
		if err := decode(body, &d); err != nil {
			return nil, nil, err
		}
		if d.Version != Version || d.RunID != s.RunID || d.AgentID != a.id || d.Revision != revision || d.Kind != kind || !d.UpdatedAt.Equal(m.UpdatedAt) {
			return nil, nil, fmt.Errorf("%w: document version, identity, kind or timestamp", ErrCorrupt)
		}
		if kind == "role" {
			if d.Protocol == nil || d.Content != nil {
				return nil, nil, fmt.Errorf("%w: role document payload", ErrCorrupt)
			}
			s.Protocol = *d.Protocol
		} else {
			if d.Content == nil || d.Protocol != nil {
				return nil, nil, fmt.Errorf("%w: section document payload", ErrCorrupt)
			}
			*sections[i] = *d.Content
		}
	}
	if err := s.validate(); err != nil {
		return nil, nil, fmt.Errorf("%w: %v", ErrCorrupt, err)
	}
	if s.Protocol.ConfigDigest != a.run.desc.ConfigDigest {
		return nil, nil, fmt.Errorf("%w: frozen config digest", ErrIdentity)
	}
	return s, &m, nil
}
