package context

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"time"
)

type descriptor struct {
	Version      int       `json:"version"`
	RunID        string    `json:"run_id"`
	Path         string    `json:"path"`
	Lead         string    `json:"lead"`
	Agents       []string  `json:"agents"`
	ConfigDigest string    `json:"config_digest"`
	CreatedAt    time.Time `json:"created_at"`
}

type agentDescriptor struct {
	Version      int    `json:"version"`
	RunID        string `json:"run_id"`
	AgentID      string `json:"agent_id"`
	Path         string `json:"path"`
	ConfigDigest string `json:"config_digest"`
}

// Run and Agent are identity-bound capabilities, not OS-user authentication.
// Their fields are private so paths cannot be redirected by callers.
type Run struct {
	base string
	desc descriptor
}

type Agent struct {
	run *Run
	id  string
}

func Create(base, runID, lead, configDigest string, agents []string) (*Run, error) {
	d := descriptor{Version: Version, RunID: runID, Path: filepath.Join(base, runID), Lead: lead,
		Agents: append([]string{}, agents...), ConfigDigest: configDigest, CreatedAt: time.Now().UTC()}
	sort.Strings(d.Agents)
	if !absolute(base) || !validDescriptor(d, base, runID) {
		return nil, fmt.Errorf("%w: run descriptor", ErrInvalid)
	}
	r, err := createBase(base)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	if err := r.Mkdir(runID, 0700); err != nil {
		if errors.Is(err, os.ErrExist) {
			return nil, fmt.Errorf("%w: run directory", ErrExists)
		}
		return nil, err
	}
	run, err := child(r, runID, true)
	if err != nil {
		return nil, err
	}
	defer run.Close()
	for _, id := range d.Agents {
		if err := run.Mkdir(id, 0700); err != nil {
			return nil, err
		}
		a, err := child(run, id, true)
		if err != nil {
			return nil, err
		}
		body, err := encode(agentDescriptor{Version: Version, RunID: runID, AgentID: id, Path: filepath.Join(d.Path, id), ConfigDigest: configDigest})
		if err == nil {
			err = publish(a, "agent.json", body)
		}
		a.Close()
		if err != nil {
			return nil, err
		}
	}
	body, err := encode(d)
	if err != nil {
		return nil, err
	}
	if err := publish(run, "run.json", body); err != nil {
		return nil, err
	}
	if err := syncDir(r); err != nil {
		return nil, err
	}
	return &Run{base: base, desc: d}, nil
}

func validDescriptor(d descriptor, base, runID string) bool {
	if d.Version != Version || !identifier.MatchString(runID) || d.RunID != runID || d.Path != filepath.Join(base, runID) ||
		!absolute(d.Path) || !agentID.MatchString(d.Lead) || !hexDigest.MatchString(d.ConfigDigest) || d.CreatedAt.IsZero() || len(d.Agents) == 0 || len(d.Agents) > 128 {
		return false
	}
	lead := false
	for i, id := range d.Agents {
		if !agentID.MatchString(id) || (i > 0 && d.Agents[i-1] >= id) {
			return false
		}
		lead = lead || id == d.Lead
	}
	return lead
}

func Open(base, runID string) (*Run, error) {
	if !absolute(base) || !identifier.MatchString(runID) {
		return nil, fmt.Errorf("%w: absolute base and safe run ID required", ErrInvalid)
	}
	r, err := openAbsolute(base)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	run, err := child(r, runID, true)
	if err != nil {
		return nil, err
	}
	defer run.Close()
	body, err := readFile(run, "run.json")
	if err != nil {
		return nil, err
	}
	var d descriptor
	if err := decode(body, &d); err != nil {
		return nil, err
	}
	if !validDescriptor(d, base, runID) {
		return nil, fmt.Errorf("%w: run descriptor", ErrIdentity)
	}
	return &Run{base: base, desc: d}, nil
}

func (r *Run) Path() string { return r.desc.Path }

func (r *Run) ID() string           { return r.desc.RunID }
func (r *Run) Lead() string         { return r.desc.Lead }
func (r *Run) ConfigDigest() string { return r.desc.ConfigDigest }

func (r *Run) AgentIDs() []string { return append([]string{}, r.desc.Agents...) }

func (r *Run) Agent(id string) (*Agent, error) {
	if !agentID.MatchString(id) {
		return nil, fmt.Errorf("%w: invalid agent ID", ErrIdentity)
	}
	for _, known := range r.desc.Agents {
		if id == known {
			return &Agent{run: r, id: id}, nil
		}
	}
	return nil, fmt.Errorf("%w: unknown agent", ErrIdentity)
}

func (a *Agent) Path() string { return filepath.Join(a.run.Path(), a.id) }

func (a *Agent) directory() (*os.Root, error) {
	base, err := openAbsolute(a.run.base)
	if err != nil {
		return nil, err
	}
	defer base.Close()
	run, err := child(base, a.run.desc.RunID, true)
	if err != nil {
		return nil, err
	}
	defer run.Close()
	// Validate on the same opened root used for the operation.
	body, err := readFile(run, "run.json")
	if err != nil {
		return nil, err
	}
	var d descriptor
	if err := decode(body, &d); err != nil {
		return nil, err
	}
	if !reflect.DeepEqual(d, a.run.desc) {
		return nil, fmt.Errorf("%w: run descriptor changed", ErrIdentity)
	}
	agent, err := child(run, a.id, true)
	if err != nil {
		return nil, err
	}
	body, err = readFile(agent, "agent.json")
	if err != nil {
		agent.Close()
		return nil, err
	}
	var identity agentDescriptor
	if err := decode(body, &identity); err != nil {
		agent.Close()
		return nil, err
	}
	expected := agentDescriptor{Version: Version, RunID: a.run.desc.RunID, AgentID: a.id, Path: a.Path(), ConfigDigest: a.run.desc.ConfigDigest}
	if identity != expected {
		agent.Close()
		return nil, fmt.Errorf("%w: agent directory descriptor", ErrIdentity)
	}
	return agent, nil
}
