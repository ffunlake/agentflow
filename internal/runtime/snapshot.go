package runtime

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"agentflow/internal/config"
)

const (
	Starting = "STARTING"
	Ready    = "READY"
	Busy     = "BUSY"
)

// NativeBinding is supplied by Launch, then independently echoed by the agent's
// READY. Returning a binding from Launch alone is never registration evidence.
type NativeBinding struct {
	SessionID   string `json:"native_session_id"`
	WorkspaceID string `json:"workspace_id"`
	SurfaceID   string `json:"surface_id"`
}

type SeatSnapshot struct {
	RunID       string        `json:"run_id"`
	AgentID     string        `json:"agent_id"`
	Generation  int64         `json:"generation"`
	Tool        string        `json:"tool"`
	Cwd         string        `json:"cwd"`
	Role        string        `json:"role"`
	Permissions string        `json:"permissions"`
	Session     string        `json:"session"`
	Native      NativeBinding `json:"native"`
	Status      string        `json:"status"`
}

// RunSnapshot is the private, versioned startup artifact. Config includes prompt
// text and tool args. The initial artifact is immutable; live registrations are
// exposed by Registry.Snapshot and persisted in store, not rewritten here.
type RunSnapshot struct {
	Version int              `json:"version"`
	RunID   string           `json:"run_id"`
	Config  *config.Resolved `json:"config"`
	Seats   []SeatSnapshot   `json:"seats"`
}

func NewSnapshot(plan *StartupPlan, runID string) (*RunSnapshot, error) {
	if plan == nil || plan.Config == nil || !identityText(runID) || len(plan.Seats) == 0 || plan.Status != "PLANNED" || plan.Executed {
		return nil, errors.New("snapshot requires an unexecuted startup plan and run ID")
	}
	canonical, err := Compile(plan.Config, Options{})
	if err != nil {
		return nil, err
	}
	s := &RunSnapshot{Version: 1, RunID: runID, Config: canonical.Config}
	for _, seat := range canonical.Seats {
		s.Seats = append(s.Seats, SeatSnapshot{RunID: runID, AgentID: seat.ID, Generation: 1,
			Tool: seat.Tool, Cwd: seat.Cwd, Role: seat.Role, Permissions: seat.Permissions,
			Session: seat.Session, Status: Starting})
	}
	if err := s.Validate(); err != nil {
		return nil, err
	}
	return s, nil
}

// Validate checks frozen configuration integrity and seat metadata without
// rereading prompt files or resolving paths relative to the current directory.
func (s *RunSnapshot) Validate() error {
	if s == nil || s.Version != 1 || !identityText(s.RunID) || s.Config == nil {
		return errors.New("invalid runtime snapshot version, run ID or config")
	}
	c := *s.Config
	digest := c.Digest
	c.Digest = ""
	body, err := json.Marshal(&c)
	if err != nil {
		return fmt.Errorf("encode frozen config: %w", err)
	}
	sum := sha256.Sum256(body)
	if digest != hex.EncodeToString(sum[:]) {
		return errors.New("runtime snapshot config digest mismatch")
	}
	plan, err := Compile(s.Config, Options{})
	if err != nil {
		return err
	}
	if c.Version != 1 || c.Terminal != "cmux" || !identityText(c.Name) || !filepath.IsAbs(c.ContextDir) || len(plan.Seats) == 0 || len(s.Seats) != len(plan.Seats) {
		return errors.New("invalid runtime snapshot config or seat count")
	}
	if _, exists := c.Agents[c.Lead]; !exists {
		return errors.New("runtime snapshot lead is unknown")
	}
	for i, expected := range plan.Seats {
		seat := s.Seats[i]
		if seat.RunID != s.RunID || seat.AgentID != expected.ID || seat.Generation < 1 ||
			seat.Tool != expected.Tool || seat.Cwd != expected.Cwd || seat.Role != expected.Role ||
			seat.Permissions != expected.Permissions || seat.Session != expected.Session ||
			!filepath.IsAbs(seat.Cwd) || strings.ContainsRune(seat.Cwd, '\x00') || !identityText(seat.Role) ||
			(seat.Permissions != "full" && seat.Permissions != "default") ||
			(seat.Session != "fresh" && seat.Session != "persistent") {
			return fmt.Errorf("invalid snapshot seat %d metadata or generation", i)
		}
		if seat.Status != Starting && seat.Status != Ready && seat.Status != Busy {
			return fmt.Errorf("invalid snapshot seat %s status", seat.AgentID)
		}
		if seat.Native != (NativeBinding{}) || seat.Status == Ready || seat.Status == Busy {
			if err := seat.Native.validate(); err != nil {
				return err
			}
			for _, previous := range s.Seats[:i] {
				if bindingConflicts(previous, seat.Tool, seat.Native) {
					return errors.New("snapshot contains duplicate native bindings")
				}
			}
		}
	}
	return nil
}

func identityText(value string) bool {
	return strings.TrimSpace(value) != "" && !strings.ContainsRune(value, '\x00')
}

func (b NativeBinding) validate() error {
	if !identityText(b.SessionID) || !identityText(b.WorkspaceID) || !identityText(b.SurfaceID) {
		return errors.New("native binding requires session, workspace and surface IDs")
	}
	return nil
}

func bindingConflicts(seat SeatSnapshot, tool string, b NativeBinding) bool {
	return seat.Native != (NativeBinding{}) &&
		((seat.Native.WorkspaceID == b.WorkspaceID && seat.Native.SurfaceID == b.SurfaceID) ||
			(seat.Tool == tool && seat.Native.SessionID == b.SessionID))
}

// WriteSnapshot exclusively publishes a complete 0600 artifact in an existing
// private run directory, using the same hard-link convention as internal/lock.
// Existing artifacts (including symlinks) are never overwritten.
func WriteSnapshot(path string, snapshot *RunSnapshot) error {
	if err := snapshot.Validate(); err != nil {
		return err
	}
	if err := privateParent(path); err != nil {
		return err
	}
	body, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".agentflow-snapshot-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if err := f.Chmod(0600); err != nil {
		return err
	}
	if _, err := f.Write(append(body, '\n')); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Link(f.Name(), path); err != nil {
		return fmt.Errorf("publish runtime snapshot: %w", err)
	}
	return nil
}

func privateParent(path string) error {
	if !filepath.IsAbs(path) {
		return errors.New("runtime snapshot path must be absolute")
	}
	info, err := os.Lstat(filepath.Dir(path))
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return errors.New("runtime snapshot requires a private run directory (0700)")
	}
	return nil
}

func ReadSnapshot(path string) (*RunSnapshot, error) {
	if err := privateParent(path); err != nil {
		return nil, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("runtime snapshot must be a private regular file (0600)")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !os.SameFile(info, opened) {
		return nil, errors.New("runtime snapshot changed during open")
	}
	d := json.NewDecoder(f)
	d.DisallowUnknownFields()
	var snapshot RunSnapshot
	if err := d.Decode(&snapshot); err != nil {
		return nil, fmt.Errorf("decode runtime snapshot: %w", err)
	}
	if err := d.Decode(new(any)); !errors.Is(err, io.EOF) {
		return nil, errors.New("runtime snapshot contains trailing data")
	}
	if err := snapshot.Validate(); err != nil {
		return nil, err
	}
	return &snapshot, nil
}
