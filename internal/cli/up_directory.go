package cli

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

func entryRunID(id string) bool {
	if len(id) == 0 || len(id) > 96 {
		return false
	}
	for i, c := range id {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || i > 0 && (c == '_' || c == '-')) {
			return false
		}
	}
	return true
}

// createUpRunDir gives a run ID one stable system-temp directory. Repeating
// the same up command therefore reopens the same attempt and is rejected by
// the durable artifacts instead of creating another workspace.
func createUpRunDir(dir, runID string) (string, bool, error) {
	if dir != "" {
		path, err := filepath.Abs(dir)
		return path, false, err
	}
	if !entryRunID(runID) {
		return "", false, errors.New("temporary run directory requires a safe run ID")
	}
	// Keep the name short enough for Unix socket paths while making it stable
	// across repeated invocations and distinct for different run IDs.
	prefix := runID
	if len(prefix) > 12 {
		prefix = prefix[:12]
	}
	digest := sha256.Sum256([]byte(runID))
	path := filepath.Join(os.TempDir(), fmt.Sprintf("agentflow-%s-%x", prefix, digest[:6]))
	created := false
	if err := os.Mkdir(path, 0700); err == nil {
		created = true
	} else if !errors.Is(err, os.ErrExist) {
		return "", false, err
	}
	var err error
	dir, err = filepath.Abs(path)
	if err == nil {
		dir, err = filepath.EvalSymlinks(dir)
	}
	if err != nil {
		if created {
			return "", false, errors.Join(err, os.Remove(path))
		}
		return "", false, err
	}
	return dir, created, nil
}
