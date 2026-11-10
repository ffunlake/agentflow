package e2e_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"agentflow/internal/coordinator"
)

// A real foreground process, independent of the CLI subprocesses under test.
// Readiness comes from its stdout, never a sleep or native terminal probe.
func startCoordinatorProcess(t *testing.T, binary, dir, runID string, explicitArgs ...string) func() {
	t.Helper()
	return startCoordinatorProcessWithResult(t, binary, dir, runID, nil, explicitArgs...)
}

func startCoordinatorProcessWithResult(t *testing.T, binary, dir, runID string, result *coordinator.UpResult, explicitArgs ...string) func() {
	t.Helper()
	args := []string{"coordinator", "--run", runID, "--run-dir", dir, "--json"}
	expected := "COORDINATOR_READY"
	if len(explicitArgs) > 0 {
		args = explicitArgs
		if args[0] == "up" {
			expected = "READY"
		}
	}
	cmd := exec.Command(binary, args...)
	cmd.Stderr = os.Stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	wait := make(chan error, 1)
	ready := make(chan error, 1)
	go func() {
		var output coordinator.UpResult
		err := json.NewDecoder(stdout).Decode(&output)
		if err == nil && output.Status != expected {
			err = os.ErrInvalid
		}
		if err == nil && result != nil {
			*result = output
		}
		ready <- err
	}()
	stopped := false
	stop := func() {
		t.Helper()
		if stopped {
			return
		}
		stopped = true
		_ = cmd.Process.Signal(os.Interrupt)
		select {
		case err := <-wait:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(10 * time.Second):
			_ = cmd.Process.Kill()
			<-wait
			t.Error("coordinator process shutdown timed out")
		}
	}
	// Wait only after the pipe reader consumes readiness: Cmd.Wait closes pipes.
	t.Cleanup(stop)
	select {
	case err := <-ready:
		go func() { wait <- cmd.Wait() }()
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		go func() { wait <- cmd.Wait() }()
		t.Fatal("coordinator readiness timed out")
	}
	return stop
}

func shortRunDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "af-e2e-")
	if err != nil {
		t.Fatal(err)
	}
	dir, err = filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}
