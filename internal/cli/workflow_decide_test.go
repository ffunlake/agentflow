package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentflow/internal/coordinator"
	"agentflow/internal/ipc"
	"agentflow/internal/lock"
	"agentflow/internal/runtime"
)

func TestWorkflowDecideUsesStructuredReplyRoute(t *testing.T) {
	dir, err := os.MkdirTemp("", "af-wd-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	server, err := ipc.Listen(filepath.Join(dir, "ipc.sock"), func(_ context.Context, req ipc.Request) (any, error) {
		var params coordinator.Params
		if req.Method != "reply" || req.RequestID != "decision-msg-1" || req.Actor != (ipc.Actor{RunID: "run", AgentID: "main", Generation: 1}) || json.Unmarshal(req.Params, &params) != nil ||
			params.MessageID != "msg-1" || params.Status != "DONE" || params.Workflow == nil || params.Workflow.Revision != 2 || params.Workflow.Kind != "approve" || len(params.Workflow.Accepted) != 2 || !params.Workflow.Accepted[0] || !params.Workflow.Accepted[1] {
			t.Fatalf("unexpected structured decision request: %+v params=%+v", req, params)
		}
		return coordinator.Result{Status: "WORKFLOW_ADVANCED", CapabilityStatus: "PARTIAL", RuntimeCommunication: "UNVERIFIED", Executed: new(bool), Diagnostics: []runtime.Diagnostic{}}, nil
	}, ipc.ServerOptions{ValidateActor: func(context.Context, ipc.Actor) error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	owner, err := lock.Acquire(filepath.Join(dir, "coordinator.lock"), lock.Owner{RunID: "run", SocketPath: filepath.Join(dir, "ipc.sock")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = owner.Release() })
	done := make(chan error, 1)
	go func() { done <- server.Serve(context.Background()) }()
	t.Cleanup(func() {
		_ = server.Close()
		if err := <-done; err != nil {
			t.Error(err)
		}
	})
	var out, diagnostic bytes.Buffer
	code := Run([]string{"workflow", "decide", "--run", "run", "--run-dir", dir, "--agent", "main", "--generation", "1", "--revision", "2", "--request-id", "decision-msg-1", "--message-id", "msg-1", "--kind", "approve", "--accepted", "true,true", "--json"}, &out, &diagnostic, "test")
	if code != 0 || diagnostic.Len() != 0 || !strings.Contains(out.String(), "WORKFLOW_ADVANCED") {
		t.Fatalf("decide failed: code=%d out=%s err=%s", code, &out, &diagnostic)
	}
}
