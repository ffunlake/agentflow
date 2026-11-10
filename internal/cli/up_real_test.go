package cli

import (
	"os"
	"path/filepath"
	"testing"

	"agentflow/internal/config"
	"agentflow/internal/runtime"
)

func TestAutoStartCMUXWorkflowOnlyWhenConfigured(t *testing.T) {
	if autoStartCMUXWorkflow(nil) || autoStartCMUXWorkflow(&runtime.StartupPlan{}) {
		t.Fatal("missing workflow must not start a coordinator")
	}
	if !autoStartCMUXWorkflow(&runtime.StartupPlan{Config: &config.Resolved{Workflow: &config.Workflow{}}}) {
		t.Fatal("workflow config must start a coordinator")
	}
}

func TestPersistInitialWorkflowTaskIsPrivateAndNeverOverwritten(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "initial-workflow-task.txt")
	if err := persistInitialWorkflowTask(dir, "approval smoke task"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("task file is not private: %v %v", info, err)
	}
	got, err := readInitialWorkflowTask(dir)
	if err != nil || got != "approval smoke task" {
		t.Fatalf("readInitialWorkflowTask() = %q, %v", got, err)
	}
	if err := persistInitialWorkflowTask(dir, "overwrite"); err == nil {
		t.Fatal("existing task marker was overwritten")
	}
}
