package coordinator

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"agentflow/internal/config"
	"agentflow/internal/ipc"
)

func TestOpenCodeWorkflowAllowsLeadToApproveWorkerSteps(t *testing.T) {
	loaded, err := config.Load(filepath.Join("..", "..", "examples", "approval.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := config.Resolve(loaded, config.ResolveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for id, agent := range resolved.Agents {
		if id != resolved.Lead {
			agent.Tool, agent.Session = "opencode", "persistent"
			resolved.Agents[id] = agent
		}
	}
	if !workflowUsesOnlyOpenCodeWorkers(resolved) {
		t.Fatal("OpenCode workers with the Codex lead as approver should use the OpenCode TUI adapter")
	}
	step := resolved.Workflow.Steps[0]
	step.Execute.Agent = resolved.Lead
	resolved.Workflow.Steps[0] = step
	if workflowUsesOnlyOpenCodeWorkers(resolved) {
		t.Fatal("lead execution must not be routed through the OpenCode worker adapter")
	}
}

func TestCMUXTransportRequiresLauncherRegistrationAndCleansLock(t *testing.T) {
	dir := fixture(t)
	if c, err := OpenCMUXTransport(t.Context(), dir, "run", "/unused/cmux"); err == nil || c != nil {
		t.Fatal("accepted non-cmux run", c, err)
	}
	for _, name := range []string{"coordinator.lock", "ipc.sock"} {
		if _, err := os.Lstat(filepath.Join(dir, name)); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("leaked startup resource", name, err)
		}
	}
}

func TestCMUXTransportParamsRejectCallerNativeTarget(t *testing.T) {
	for _, raw := range []string{`{"surface":"surface:1"}`, `{"workspace":"workspace:1"}`, `{"recipient":"builder","body":"x","body":"y"}`, `{"actor":{}}`, `{"recipient_generation":null}`} {
		req := request("main", "cmux-send", "request", Params{})
		req.Params = []byte(raw)
		_, err := cmuxHandler(nil, nil)(t.Context(), req)
		var public *ipc.Error
		if !errors.As(err, &public) || public.Code != ipc.InvalidRequest {
			t.Fatal(raw, err)
		}
	}
	req := request("main", "cmux-inbox", "request", Params{Recipient: "builder"})
	if _, err := cmuxHandler(nil, nil)(t.Context(), req); err == nil {
		t.Fatal("inbox accepted caller-selected target")
	}
}
