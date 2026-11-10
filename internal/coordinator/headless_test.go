package coordinator

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"agentflow/internal/config"
	"agentflow/internal/ipc"
	"agentflow/internal/loopcore"
	"agentflow/internal/runtime"
	"agentflow/internal/store"
)

func headlessPlan(t *testing.T, binary string) (*runtime.StartupPlan, UpOptions, string) {
	return headlessToolPlan(t, binary, "codex")
}

func headlessToolPlan(t *testing.T, binary, tool string) (*runtime.StartupPlan, UpOptions, string) {
	t.Helper()
	root, err := os.MkdirTemp("", "af-hc-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	dir, cwd := filepath.Join(root, "run"), filepath.Join(root, "cwd")
	for _, path := range []string{dir, cwd} {
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	source, err := config.Load(filepath.Join("..", "..", "examples", "headless-"+tool+".yaml"))
	if err != nil {
		t.Fatal(err)
	}
	source.ContextDir, source.Defaults.Cwd = filepath.Join(root, "context"), &cwd
	frozen, err := config.Resolve(source, config.ResolveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := runtime.Compile(frozen, runtime.Options{WrapperBinary: "/unused/agentflow", SnapshotPath: filepath.Join(dir, "config.resolved.json")})
	if err != nil {
		t.Fatal(err)
	}
	return plan, UpOptions{RunID: "run", RunDir: dir, Headless: &runtime.HeadlessOptions{RunID: "run", RunDir: dir, Binary: binary, TurnTimeout: 30 * time.Second, Tool: tool}}, cwd
}

func serveHeadless(t *testing.T, c *Coordinator) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- c.Serve(ctx) }()
	var once sync.Once
	stop := func() {
		once.Do(func() {
			cancel()
			if err := <-done; err != nil {
				t.Error(err)
			}
		})
	}
	t.Cleanup(stop)
	return stop
}

func headlessCalls(t *testing.T, cwd string) int {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(cwd, "calls.log"))
	if err != nil {
		t.Fatal(err)
	}
	return strings.Count(string(b), "\n")
}

func TestHeadlessCodexIntegratedFixtures(t *testing.T) {
	testHeadlessIntegratedFixtures(t, "codex")
}

func TestHeadlessOpenCodeIntegratedFixtures(t *testing.T) {
	testHeadlessIntegratedFixtures(t, "opencode")
}

func TestHeadlessClaudeIntegratedFixtures(t *testing.T) {
	testHeadlessIntegratedFixtures(t, "claude")
}

func testHeadlessIntegratedFixtures(t *testing.T, tool string) {
	open := OpenHeadlessCodex
	if tool == "opencode" {
		open = OpenHeadlessOpenCode
	}
	if tool == "claude" {
		open = OpenHeadlessClaude
	}
	binary := filepath.Join(t.TempDir(), tool+" fixture")
	cmd := exec.Command("go", "build", "-o", binary, "../runtime/testdata/headless-codex")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s: %v", out, err)
	}
	modes := []string{"success", "failed", "unknown", "wrong-thread", "malformed", "exit", "wait", "invalid-result", "duplicate-result", "case-result", "false-review", "blocked", "retry", "changes"}
	if tool == "opencode" {
		modes = append(modes, "no-finish", "wrong-part-session", "unknown-finish", "after-finish")
	}
	if tool == "claude" {
		modes = append(modes, "no-finish", "wrong-result-session", "unknown-finish", "after-finish", "permission-denied-blocked", "permission-denied-retry", "recovered-retry", "retry-failed", "compaction", "foreground-subagent")
	}
	for _, mode := range modes {
		t.Run(mode, func(t *testing.T) {
			plan, o, cwd := headlessToolPlan(t, binary, tool)
			if mode == "wait" {
				o.Headless.TurnTimeout = 500 * time.Millisecond
			}
			c, up, err := Up(t.Context(), plan, o)
			if err != nil {
				t.Fatalf("up=%+v: %v", up, err)
			}
			if !up.CoordinatorReady || !up.Executed || up.ReadyProof == nil || headlessCalls(t, cwd) != 2 {
				t.Fatal(up)
			}
			if len(up.Seats) != 2 || len(up.ReadyProofs) != 2 || up.Seat.AgentID != up.ReadyProof.Seat.AgentID || up.Seat.Native.SessionID != up.ReadyProof.Seat.NativeSessionID {
				t.Fatal("headless READY metadata mismatch", up)
			}
			if err := os.WriteFile(filepath.Join(cwd, "mode"), []byte(mode), 0600); err != nil {
				t.Fatal(err)
			}
			stop := serveHeadless(t, c)
			start := workflowRequest(loopcore.Cursor{}, "main", "start")
			r := result(t, o.RunDir, start)
			if r.Status != "WORKFLOW_ADVANCED" || r.WorkflowCurrent == nil || r.Dispatch == nil {
				t.Fatalf("%+v", r)
			}
			switch mode {
			case "success", "recovered-retry", "compaction", "foreground-subagent":
				if r.WorkflowCurrent.Phase != loopcore.Completed || r.WorkflowCurrent.Revision != 3 || headlessCalls(t, cwd) != 4 || len(r.Diagnostics) != 0 {
					t.Fatal(r, headlessCalls(t, cwd))
				}
			case "blocked", "permission-denied-blocked":
				if r.WorkflowCurrent.Phase != loopcore.Paused || r.WorkflowCurrent.Revision != 2 || len(r.Diagnostics) != 0 {
					t.Fatal(r)
				}
			case "retry", "permission-denied-retry":
				if r.WorkflowCurrent.Phase != loopcore.Exhausted || r.WorkflowCurrent.Revision != 3 || headlessCalls(t, cwd) != 4 {
					t.Fatal(r)
				}
			case "changes":
				if r.WorkflowCurrent.Phase != loopcore.Exhausted || r.WorkflowCurrent.Revision != 5 || headlessCalls(t, cwd) != 6 {
					t.Fatal(r)
				}
			case "invalid-result", "duplicate-result", "case-result", "false-review":
				if r.Dispatch.Delivery.Phase != "SENT" || len(r.Diagnostics) != 1 {
					t.Fatal(r)
				}
			default:
				if r.Dispatch.Delivery.Phase != "SUBMIT_REQUESTED" || r.Dispatch.Delivery.Attempt != 1 || len(r.Diagnostics) != 1 {
					t.Fatalf("%+v dispatch=%+v", r, r.Dispatch)
				}
			}
			calls := headlessCalls(t, cwd)
			stop()
			c, err = open(t.Context(), *o.Headless)
			if err != nil {
				t.Fatal(err)
			}
			serveHeadless(t, c)
			before := messageTablesSnapshot(t, o.RunDir)
			replay := result(t, o.RunDir, start)
			if !reflect.DeepEqual(replay.Workflow.Cursor, r.Workflow.Cursor) || replay.Dispatch != nil || *replay.Executed || headlessCalls(t, cwd) != calls || before != messageTablesSnapshot(t, o.RunDir) {
				t.Fatal("start replay changed state", replay)
			}
			drain := request("main", "workflow", "drain", Params{Workflow: &WorkflowParams{Kind: "drain", Revision: r.WorkflowCurrent.Revision}})
			again := result(t, o.RunDir, drain)
			if again.WorkflowCurrent == nil || again.WorkflowCurrent.Revision != r.WorkflowCurrent.Revision || headlessCalls(t, cwd) != calls {
				t.Fatal("restart/drain replayed native", again)
			}
			if mode == "blocked" || mode == "permission-denied-blocked" {
				if err := os.WriteFile(filepath.Join(cwd, "mode"), []byte("success"), 0600); err != nil {
					t.Fatal(err)
				}
				resume := changeWorkflowRequest(t, workflowRequest(*r.WorkflowCurrent, "main", "resume"), func(p *Params) { p.Workflow.Reason = "fixture decision resolved" })
				resumed := result(t, o.RunDir, resume)
				if resumed.WorkflowCurrent.Phase != loopcore.Completed || resumed.WorkflowCurrent.Revision != 5 || headlessCalls(t, cwd) != calls+2 {
					t.Fatal(resumed)
				}
				before := messageTablesSnapshot(t, o.RunDir)
				resumed = result(t, o.RunDir, resume)
				if resumed.Dispatch != nil || *resumed.Executed || headlessCalls(t, cwd) != calls+2 || before != messageTablesSnapshot(t, o.RunDir) {
					t.Fatal("resume replay mutated state", resumed)
				}
			}
		})
	}
	t.Run("fresh-override-queues-without-reset", func(t *testing.T) {
		plan, o, cwd := headlessToolPlan(t, binary, tool)
		c, _, err := Up(t.Context(), plan, o)
		if err != nil {
			t.Fatal(err)
		}
		serveHeadless(t, c)
		r := result(t, o.RunDir, request("main", "assign", "fresh", Params{Recipient: "builder", Body: "fixture", Session: "fresh"}))
		if r.Record == nil || r.Record.Delivery.ResetPhase != "NONE" || r.Record.Delivery.Attempt != 0 || r.Record.Coordination != "RESET_ADAPTER_UNIMPLEMENTED" || headlessCalls(t, cwd) != 2 {
			t.Fatal(r)
		}
	})
	t.Run("stop-and-generation-fences", func(t *testing.T) {
		plan, o, cwd := headlessToolPlan(t, binary, tool)
		c, _, err := Up(t.Context(), plan, o)
		if err != nil {
			t.Fatal(err)
		}
		w, err := c.db.AdvanceWorkflowWithSessions(t.Context(), store.MessageActor{RunID: "run", AgentID: "main", Generation: 1}, loopcore.Event{ID: "seed", Actor: "main", Kind: "start"})
		if err != nil {
			t.Fatal(err)
		}
		stop := serveHeadless(t, c)
		for _, kind := range []string{"start", "resume", "drain", "status"} {
			req := request("main", "workflow", "wrong-generation-"+kind, Params{Workflow: &WorkflowParams{Kind: kind, Revision: w.Cursor.Revision}})
			req.Actor.Generation = 2
			before := messageTablesSnapshot(t, o.RunDir)
			_, err := Call(t.Context(), o.RunDir, req)
			var public *ipc.Error
			if !errors.As(err, &public) || public.Code != ipc.GenerationMismatch || before != messageTablesSnapshot(t, o.RunDir) {
				t.Fatal(kind, err)
			}
		}
		call(t, o.RunDir, request("main", "stop", "stop", Params{}))
		stop()
		c, err = open(t.Context(), *o.Headless)
		if err != nil {
			t.Fatal(err)
		}
		serveHeadless(t, c)
		before := messageTablesSnapshot(t, o.RunDir)
		status := result(t, o.RunDir, request("main", "workflow", "status", Params{Workflow: &WorkflowParams{Kind: "status"}}))
		if status.WorkflowCurrent == nil || status.WorkflowCurrent.Revision != 1 || *status.Executed || before != messageTablesSnapshot(t, o.RunDir) {
			t.Fatal(status)
		}
		drain := result(t, o.RunDir, request("main", "workflow", "drain-stopped", Params{Workflow: &WorkflowParams{Kind: "drain", Revision: 1}}))
		if drain.WorkflowCurrent == nil || drain.WorkflowCurrent.Revision != 1 || len(drain.Diagnostics) != 1 || headlessCalls(t, cwd) != 2 {
			t.Fatal(drain)
		}
		start := result(t, o.RunDir, workflowRequest(loopcore.Cursor{}, "main", "start"))
		if start.Status != "FAILED" || start.Diagnostics[0].Code != "RUN_STOPPED" || headlessCalls(t, cwd) != 2 {
			t.Fatal(start)
		}
	})
	t.Run("concurrent-start", func(t *testing.T) {
		plan, o, cwd := headlessToolPlan(t, binary, tool)
		c, _, err := Up(t.Context(), plan, o)
		if err != nil {
			t.Fatal(err)
		}
		serveHeadless(t, c)
		var wg sync.WaitGroup
		responses := make(chan Result, 4)
		for range 4 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				responses <- result(t, o.RunDir, workflowRequest(loopcore.Cursor{}, "main", "start"))
			}()
		}
		wg.Wait()
		close(responses)
		executed := 0
		for r := range responses {
			if r.Status != "WORKFLOW_ADVANCED" {
				t.Fatal(r)
			}
			if *r.Executed {
				executed++
			}
		}
		if executed != 1 || headlessCalls(t, cwd) != 4 {
			t.Fatal(executed, headlessCalls(t, cwd))
		}
	})
	if tool == "opencode" || tool == "claude" {
		startupModes := []string{"wrong-nonce", "wrong-generation", "unknown", "failed", "malformed", "no-finish", "unknown-finish", "after-finish"}
		if tool == "opencode" {
			startupModes = append(startupModes, "wrong-part-session")
		} else {
			startupModes = append(startupModes, "wrong-result-session")
		}
		for _, mode := range startupModes {
			t.Run("startup/"+mode, func(t *testing.T) {
				plan, o, cwd := headlessToolPlan(t, binary, tool)
				if err := os.WriteFile(filepath.Join(cwd, "startup-mode"), []byte(mode), 0600); err != nil {
					t.Fatal(err)
				}
				c, up, err := Up(t.Context(), plan, o)
				if err == nil || c != nil || up.CoordinatorReady || up.ReadyProof != nil || headlessCalls(t, cwd) != 1 {
					t.Fatal(up, err)
				}
				if c, _, err := Up(t.Context(), plan, o); err == nil || c != nil || headlessCalls(t, cwd) != 1 {
					t.Fatal("startup replayed", err)
				}
			})
		}
		t.Run("reopen-mode-and-default", func(t *testing.T) {
			plan, o, cwd := headlessToolPlan(t, binary, tool)
			c, _, err := Up(t.Context(), plan, o)
			if err != nil {
				t.Fatal(err)
			}
			if err := c.Close(); err != nil {
				t.Fatal(err)
			}
			codexOptions := *o.Headless
			codexOptions.Tool = ""
			if c, err := OpenHeadlessCodex(t.Context(), codexOptions); err == nil || c != nil {
				t.Fatal("cross-tool reopen accepted")
			}
			c, err = Open(t.Context(), o.RunDir, o.RunID)
			if err != nil {
				t.Fatal(err)
			}
			stop := serveHeadless(t, c)
			r := result(t, o.RunDir, workflowRequest(loopcore.Cursor{}, "main", "start"))
			if r.Status != "WORKFLOW_ADVANCED" || r.Dispatch != nil || *r.Executed || headlessCalls(t, cwd) != 2 {
				t.Fatal("default coordinator executed", r)
			}
			stop()
			if err := os.WriteFile(filepath.Join(o.RunDir, "headless."+tool+".json"), []byte(`"headless-codex-v1"`), 0600); err != nil {
				t.Fatal(err)
			}
			if c, err := open(t.Context(), *o.Headless); err == nil || c != nil || headlessCalls(t, cwd) != 2 {
				t.Fatal("invalid marker accepted", err)
			}
		})
	}
	for _, cut := range []string{"queued", "sent-before-event", "submit-requested"} {
		t.Run("restart/"+cut, func(t *testing.T) {
			plan, o, cwd := headlessToolPlan(t, binary, tool)
			c, _, err := Up(t.Context(), plan, o)
			if err != nil {
				t.Fatal(err)
			}
			actor := store.MessageActor{RunID: "run", AgentID: "main", Generation: 1}
			w, err := c.db.AdvanceWorkflowWithSessions(t.Context(), actor, loopcore.Event{ID: "seed", Actor: "main", Kind: "start"})
			if err != nil {
				t.Fatal(err)
			}
			if cut == "sent-before-event" {
				a, err := runtime.NewHeadless(plan.Config, *o.Headless)
				if err != nil {
					t.Fatal(err)
				}
				s, _ := runtime.NewSessionCoordinator(c.db, a.ResetAdapter(), a)
				r, err := s.Advance(t.Context(), ipc.Actor{RunID: "run", AgentID: "main", Generation: 1}, w.Message.ID)
				if err != nil || r.Delivery.Phase != "SENT" {
					t.Fatal(r, err)
				}
			} else if cut == "submit-requested" {
				work, err := c.db.SessionWork(t.Context(), w.Message.ID)
				if err != nil {
					t.Fatal(err)
				}
				_, claimed, err := c.db.BeginSessionAction(t.Context(), w.Message.ID, work.Seat, "submit", "")
				if err != nil || !claimed {
					t.Fatal(claimed, err)
				}
			}
			if err := c.Close(); err != nil {
				t.Fatal(err)
			}
			c, err = open(t.Context(), *o.Headless)
			if err != nil {
				t.Fatal(err)
			}
			serveHeadless(t, c)
			if cut == "submit-requested" {
				ack := result(t, o.RunDir, request("builder", "ack", "premature-ack", Params{MessageID: w.Message.ID}))
				if ack.Status != "FAILED" || len(ack.Diagnostics) != 1 || ack.Diagnostics[0].Code != "SESSION_NOT_SENT" {
					t.Fatal("ACK replaced missing SENT after restart", ack)
				}
				second := result(t, o.RunDir, request("main", "assign", "second", Params{Recipient: "builder", Body: "must queue", Session: "persistent"}))
				if second.Record == nil || second.Record.Delivery.Phase != "QUEUED" || second.Record.Delivery.Attempt != 0 || headlessCalls(t, cwd) != 2 {
					t.Fatal("unresolved assignment allowed second native turn", second)
				}
			}
			r := result(t, o.RunDir, request("main", "workflow", "drain", Params{Workflow: &WorkflowParams{Kind: "drain", Revision: 1}}))
			if cut == "submit-requested" {
				if r.WorkflowCurrent.Revision != 1 || len(r.Diagnostics) != 1 || headlessCalls(t, cwd) != 2 {
					t.Fatal(r)
				}
			} else if r.WorkflowCurrent.Phase != loopcore.Completed || headlessCalls(t, cwd) != 4 || len(r.Diagnostics) != 0 {
				t.Fatal(r, headlessCalls(t, cwd))
			}
		})
	}
}

// Opt-in only. This exercises the same Up/IPC/runner path using the installed
// Codex, isolated empty cwd, and a no-tools nonce workflow. Never a TUI operation.
func TestHeadlessCodexRealNonceIntegration(t *testing.T) {
	binary := os.Getenv("AGENTFLOW_REAL_CODEX")
	if binary == "" {
		t.Skip("set AGENTFLOW_REAL_CODEX to an absolute real executable for isolated native integration")
	}
	plan, o, cwd := headlessPlan(t, binary)
	if evidence := os.Getenv("AGENTFLOW_REAL_EVIDENCE_DIR"); evidence != "" {
		t.Cleanup(func() {
			files, _ := filepath.Glob(filepath.Join(o.RunDir, "headless-*.json"))
			for _, path := range files {
				b, err := os.ReadFile(path)
				if err != nil {
					t.Error(err)
					continue
				}
				f, err := os.OpenFile(filepath.Join(evidence, filepath.Base(path)), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
				if err != nil {
					t.Error(err)
					continue
				}
				_, writeErr := f.Write(b)
				if err := f.Close(); err != nil || writeErr != nil {
					t.Error(err, writeErr)
				}
			}
		})
	}
	o.Headless.TurnTimeout = 3 * time.Minute
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Minute)
	defer cancel()
	c, up, err := Up(ctx, plan, o)
	if err != nil {
		t.Fatalf("%+v %v", up, err)
	}
	stop := serveHeadless(t, c)
	start := workflowRequest(loopcore.Cursor{}, "main", "start")
	response, err := Call(ctx, o.RunDir, start)
	if err != nil {
		t.Fatal(err)
	}
	var r Result
	if err := json.Unmarshal(response.Result, &r); err != nil {
		t.Fatal(err)
	}
	if r.WorkflowCurrent == nil || r.WorkflowCurrent.Phase != loopcore.Completed || len(r.Diagnostics) != 0 {
		t.Fatalf("%+v", r)
	}
	stop()
	c, err = OpenHeadlessCodex(ctx, *o.Headless)
	if err != nil {
		t.Fatal(err)
	}
	stop = serveHeadless(t, c)
	before := messageTablesSnapshot(t, o.RunDir)
	replay := result(t, o.RunDir, start)
	if replay.Dispatch != nil || *replay.Executed || before != messageTablesSnapshot(t, o.RunDir) {
		t.Fatal("real replay mutated state", replay)
	}
	stop()
	entries, err := os.ReadDir(cwd)
	if err != nil || len(entries) != 0 {
		t.Fatal("native cwd changed", entries, err)
	}
	files, _ := filepath.Glob(filepath.Join(o.RunDir, "headless-*.json"))
	nonceObserved := false
	for _, path := range files {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var capture struct {
			Stdout string `json:"stdout"`
		}
		if json.Unmarshal(b, &capture) != nil {
			t.Fatal("invalid capture")
		}
		if strings.Contains(capture.Stdout, "AGENTFLOW_HEADLESS_V1_NONCE") {
			nonceObserved = true
		}
		for _, line := range strings.Split(strings.TrimSpace(capture.Stdout), "\n") {
			var e struct {
				Type string `json:"type"`
				Item struct {
					Type string `json:"type"`
				} `json:"item"`
			}
			if json.Unmarshal([]byte(line), &e) != nil {
				t.Fatal("invalid JSONL")
			}
			if strings.HasPrefix(e.Type, "item.") && e.Item.Type != "agent_message" && e.Item.Type != "reasoning" && e.Item.Type != "error" {
				t.Fatalf("unexpected native tool item: %s", e.Item.Type)
			}
		}
	}
	if len(files) != 4 || !nonceObserved {
		t.Fatal("native nonce/capture evidence missing", len(files), nonceObserved)
	}
	if evidence := os.Getenv("AGENTFLOW_REAL_EVIDENCE_DIR"); evidence != "" {
		b, _ := json.MarshalIndent(struct {
			Up            UpResult `json:"up"`
			Workflow      Result   `json:"workflow"`
			RestartReplay Result   `json:"restart_replay"`
			CwdEmpty      bool     `json:"cwd_empty"`
		}{up, r, replay, true}, "", "  ")
		f, err := os.OpenFile(filepath.Join(evidence, "integration.json"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			t.Fatal(err)
		}
		_, writeErr := f.Write(b)
		if err := f.Close(); err != nil || writeErr != nil {
			t.Fatal(err, writeErr)
		}
	}
	t.Logf("REAL_INTEGRATION_COMPLETED cwd=%s native_captures=%d revision=%d nonce=AGENTFLOW_HEADLESS_V1_NONCE restart_replay=unchanged", cwd, len(files), r.WorkflowCurrent.Revision)
}
