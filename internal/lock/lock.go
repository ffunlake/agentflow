// Package lock provides a per-run filesystem lock and read-only diagnostics.
// It never removes stale locks or starts/reconnects a coordinator.
package lock

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

var (
	ErrAlreadyRunning = errors.New("ALREADY_RUNNING")
	ErrNotOwner       = errors.New("NOT_LOCK_OWNER")
)

const maxOwnerSize = 64 << 10

// Owner is the on-disk JSON record. Acquire always generates PID, Token and
// StartedAt; callers supply RunID, SocketPath and optional command/metadata.
// Command is descriptive argv, never executed by this package.
type Owner struct {
	PID        int               `json:"pid"`
	Token      string            `json:"token"`
	StartedAt  time.Time         `json:"startedAt"`
	RunID      string            `json:"runID"`
	SocketPath string            `json:"socketPath"`
	Command    []string          `json:"command,omitempty"`
	Metadata   map[string]string `json:"metadata,omitempty"`
}

// AlreadyRunningError preserves the stable sentinel even if an existing lock
// is unreadable or malformed. Owner is populated only after successful Inspect.
type AlreadyRunningError struct {
	Owner      *Owner
	InspectErr error
}

func (e *AlreadyRunningError) Error() string { return ErrAlreadyRunning.Error() }
func (e *AlreadyRunningError) Unwrap() error { return ErrAlreadyRunning }

// Lock is an acquisition capability. Do not copy it. Its token and file identity
// are private so changing an inspected Owner cannot authorize a different lock.
type Lock struct {
	mu       sync.Mutex
	path     string
	token    string
	file     os.FileInfo
	released bool
}

// Acquire publishes a complete owner record atomically using a same-directory
// hard link (which fails if path already exists). The parent directory must
// already exist and be private to the user. Filesystems must support hard links;
// unsupported operations fail without falling back to a nonexclusive write.
// Existing paths, including stale/malformed locks, are never removed or replaced.
func Acquire(path string, owner Owner) (*Lock, error) {
	if path == "" || owner.RunID == "" {
		return nil, errors.New("lock requires a path and run ID")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve lock path: %w", err)
	}
	var token [32]byte
	if _, err := rand.Read(token[:]); err != nil {
		return nil, fmt.Errorf("generate lock token: %w", err)
	}
	owner.PID = os.Getpid()
	owner.Token = hex.EncodeToString(token[:])
	owner.StartedAt = time.Now().UTC()
	data, err := json.Marshal(owner)
	if err != nil {
		return nil, fmt.Errorf("encode lock owner: %w", err)
	}
	data = append(data, '\n')
	if len(data) > maxOwnerSize {
		return nil, errors.New("lock owner exceeds 64 KiB")
	}
	f, err := os.CreateTemp(filepath.Dir(abs), ".agentflow-lock-*")
	if err != nil {
		return nil, fmt.Errorf("create lock owner: %w", err)
	}
	// Only this private temporary path is cleaned up, never the destination.
	defer os.Remove(f.Name())
	defer f.Close()
	if err := f.Chmod(0600); err != nil {
		return nil, fmt.Errorf("chmod lock owner: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		return nil, fmt.Errorf("write lock owner: %w", err)
	}
	if err := f.Sync(); err != nil {
		return nil, fmt.Errorf("sync lock owner: %w", err)
	}
	info, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat lock owner: %w", err)
	}
	if err := f.Close(); err != nil {
		return nil, fmt.Errorf("close lock owner: %w", err)
	}
	if err := os.Link(f.Name(), abs); err != nil {
		if errors.Is(err, os.ErrExist) {
			conflict := &AlreadyRunningError{}
			existing, inspectErr := Inspect(abs)
			if inspectErr != nil {
				conflict.InspectErr = inspectErr
			} else {
				conflict.Owner = &existing
			}
			return nil, conflict
		}
		return nil, fmt.Errorf("acquire lock: %w", err)
	}
	return &Lock{path: abs, token: owner.Token, file: info}, nil
}

// Inspect reads only the bounded owner record; it does not probe a socket or PID
// and makes no inference about business readiness. Nonregular paths are refused.
func Inspect(path string) (Owner, error) {
	owner, _, err := inspect(path)
	return owner, err
}

func inspect(path string) (Owner, os.FileInfo, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return Owner{}, nil, fmt.Errorf("inspect lock: %w", err)
	}
	if !info.Mode().IsRegular() {
		return Owner{}, nil, errors.New("lock is not a regular file")
	}
	f, err := os.Open(path)
	if err != nil {
		return Owner{}, nil, fmt.Errorf("open lock owner: %w", err)
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil {
		return Owner{}, nil, fmt.Errorf("stat lock owner: %w", err)
	}
	if !os.SameFile(info, opened) {
		return Owner{}, nil, errors.New("lock changed during inspection")
	}
	data, err := io.ReadAll(io.LimitReader(f, maxOwnerSize+1))
	if err != nil {
		return Owner{}, nil, fmt.Errorf("read lock owner: %w", err)
	}
	if len(data) > maxOwnerSize {
		return Owner{}, nil, errors.New("lock owner exceeds 64 KiB")
	}
	var owner Owner
	if err := json.Unmarshal(data, &owner); err != nil {
		return Owner{}, nil, fmt.Errorf("decode lock owner: %w", err)
	}
	if owner.PID <= 0 || owner.Token == "" || owner.StartedAt.IsZero() || owner.RunID == "" {
		return Owner{}, nil, errors.New("lock owner missing PID, token, startedAt or runID")
	}
	return owner, opened, nil
}

// Release removes only the acquired file with the original token. Repeated and
// concurrent calls on one handle are safe, including after another acquisition.
// Missing paths count as released. A mismatch or unreadable record leaves the
// path untouched. Callers must not independently unlink/replace active locks:
// token verification and unlink are not an atomic compare-and-delete primitive.
func (l *Lock) Release() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.released {
		return nil
	}
	if l.file == nil || l.token == "" {
		return ErrNotOwner
	}
	owner, info, err := inspect(l.path)
	if errors.Is(err, os.ErrNotExist) {
		l.released = true
		return nil
	}
	if err != nil {
		return fmt.Errorf("release lock: %w", err)
	}
	if owner.Token != l.token || !os.SameFile(l.file, info) {
		return ErrNotOwner
	}
	if err := os.Remove(l.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("release lock: %w", err)
	}
	l.released = true
	return nil
}
