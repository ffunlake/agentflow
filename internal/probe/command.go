// Package probe performs bounded, read-only CLI capability checks. It never
// sends prompts, installs hooks, resets sessions, or edits tool configuration.
package probe

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const outputLimit = 1024 * 1024

// Command is both the invocation and its evidence. A nil ExitCode means that
// no child exit status was obtained (e.g. executable missing or start denied).
type Command struct {
	ID           string   `json:"id"`
	Binary       string   `json:"binary"`
	Path         string   `json:"path,omitempty"`
	SkippedShims []string `json:"skipped_cmux_shims,omitempty"`
	Args         []string `json:"args"`
	CWD          string   `json:"cwd"`
	StartedAt    string   `json:"started_at"`
	DurationMS   int64    `json:"duration_ms"`
	ExitCode     *int     `json:"exit_code"`
	Code         string   `json:"code"`
	Error        string   `json:"error,omitempty"`
	Stdout       string   `json:"stdout"`
	Stderr       string   `json:"stderr"`
	Truncated    bool     `json:"truncated"`
	Evidence     string   `json:"evidence,omitempty"`
}

type executor interface {
	execute(context.Context, string, string, []string) Command
}

type localExecutor struct {
	cwd string
	env []string
}

// Look past cmux's per-surface shims. They can install hooks or route to a
// different surface; this probe needs the underlying binary, not a live seat.
func executable(name string) (string, []string, error) {
	var skipped []string
	var permissionErr error
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if strings.Contains(filepath.ToSlash(dir), "/cmux-cli-shims/") || filepath.Base(dir) == "cmux-cli-shims" {
			skipped = append(skipped, filepath.Join(dir, name))
			continue
		}
		path, err := exec.LookPath(filepath.Join(dir, name))
		if err == nil {
			path, err = filepath.Abs(path)
			return path, skipped, err
		}
		if errors.Is(err, os.ErrPermission) {
			permissionErr = err
		}
	}
	if permissionErr != nil {
		return "", skipped, permissionErr
	}
	return "", skipped, exec.ErrNotFound
}

type boundedBuffer struct {
	bytes.Buffer
	truncated bool
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	remaining := outputLimit - b.Len()
	if len(p) > remaining {
		p = p[:remaining]
		b.truncated = true
	}
	_, _ = b.Buffer.Write(p)
	return n, nil
}

func (e localExecutor) execute(ctx context.Context, id, binary string, args []string) Command {
	start := time.Now()
	c := Command{ID: id, Binary: binary, Args: args, CWD: e.cwd, StartedAt: start.UTC().Format(time.RFC3339Nano)}
	path, skipped, err := executable(binary)
	c.Path, c.SkippedShims = path, skipped
	if err != nil {
		c.Code = "EXECUTABLE_MISSING"
		if errors.Is(err, os.ErrPermission) {
			c.Code = "PERMISSION_DENIED"
		}
		c.Error = err.Error()
		c.DurationMS = time.Since(start).Milliseconds()
		return c
	}
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Dir, cmd.Env = e.cwd, e.env
	// No terminal/stdin inheritance. Bound pipe waits even if a broken binary
	// leaves a descendant holding stdout; no shell interpolation is involved.
	cmd.WaitDelay = 250 * time.Millisecond
	var stdout, stderr boundedBuffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err = cmd.Run()
	c.Stdout, c.Stderr = stdout.String(), stderr.String()
	c.Truncated = stdout.truncated || stderr.truncated
	c.DurationMS = time.Since(start).Milliseconds()
	c.Code = "OK"
	if cmd.ProcessState != nil {
		code := cmd.ProcessState.ExitCode()
		c.ExitCode = &code
	}
	if err != nil {
		c.Code, c.Error = "COMMAND_FAILED", err.Error()
		if errors.Is(err, os.ErrPermission) {
			c.Code = "PERMISSION_DENIED"
		}
		if ctx.Err() != nil {
			c.Code, c.Error = "COMMAND_TIMEOUT_OR_CANCELLED", ctx.Err().Error()
		}
	} else if c.Truncated {
		c.Code, c.Error = "OUTPUT_TRUNCATED", "output exceeded 1 MiB per stream; capability check is incomplete"
	}
	return c
}
