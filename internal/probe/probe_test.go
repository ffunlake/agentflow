package probe

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type helpExecutor struct{ calls []Command }

func (f *helpExecutor) execute(_ context.Context, id, binary string, args []string) Command {
	code := 0
	c := Command{ID: id, Binary: binary, Args: args, Code: "OK", ExitCode: &code, Stdout: "fixture 1.0\n"}
	if args[len(args)-1] == "--help" {
		c.Stdout = "--auto --hostname --port --session --dir --attach --remote --thread --message --resume --dangerously-bypass-approvals-and-sandbox app-server resume --session-id --permission-mode bypassPermissions --dangerously-skip-permissions new-workspace send-key read-screen"
	}
	if id == "cmux-identify" {
		c.Stdout = `{"caller":{},"focused":{}}`
	}
	if id == "cmux-tree" {
		c.Stdout = `{"windows":[{"workspaces":[{"id":"workspace-fixture"}]}]}`
	}
	if id == "cmux-capabilities" {
		c.Stdout = `{"methods":["system.tree"]}`
	}
	f.calls = append(f.calls, c)
	return c
}

func TestHelpCannotProveRuntime(t *testing.T) {
	r := Report{Status: "PARTIAL", BaselineStatus: "PASS", RuntimeAcceptance: "UNVERIFIED", EnvironmentIdentity: map[string]string{"CMUX_WORKSPACE_ID": "workspace-fixture"}, CMUX: map[string]Finding{}}
	f := &helpExecutor{}
	e := engine{report: &r, exec: f, timeout: time.Second}
	for _, name := range []string{"opencode", "codex", "claude"} {
		e.tool(t.Context(), name)
	}
	e.cmux(t.Context())
	if r.BaselineStatus != "PASS" || r.RuntimeAcceptance != "UNVERIFIED" || r.Status != "PARTIAL" {
		t.Fatalf("overstated acceptance: %+v", r)
	}
	for _, tool := range r.Tools {
		for _, key := range []string{"native_visible_binding", "idle_receive", "reset", "effective_full_permissions", "resume_original", "busy", "input_draft", "crash_recovery"} {
			if tool.Capabilities[key].Status != "UNVERIFIED" {
				t.Fatalf("%s %s: %+v", tool.Name, key, tool.Capabilities[key])
			}
		}
		if tool.SessionPlan.Status != "WAITING_DEPENDENCY" {
			t.Fatalf("launch plan counted as actual session")
		}
		if tool.Name == "claude" && (len(tool.SessionPlan.LaunchArgv) != 2 || tool.SessionPlan.LaunchArgv[1] != "--bare") {
			t.Fatal("Claude candidate must default to strict bare", tool.SessionPlan.LaunchArgv)
		}
	}
	for _, c := range f.calls {
		allowed := c.ID == "go-version" || strings.HasSuffix(c.ID, "-version") || strings.HasSuffix(c.ID, "-help") || c.ID == "cmux-ping" || c.ID == "cmux-capabilities" || c.ID == "cmux-identify" || c.ID == "cmux-tree"
		if !allowed {
			t.Fatalf("unexpected live operation: %+v", c)
		}
		if c.Binary != "cmux" && c.Args[len(c.Args)-1] != "--help" && c.Args[0] != "--version" {
			t.Fatalf("tool invoked beyond help/version: %+v", c)
		}
	}
}

func TestMissingCapabilityIsDependency(t *testing.T) {
	r := Report{BaselineStatus: "PASS", Status: "PARTIAL"}
	e := engine{report: &r}
	f := e.advertised(Command{ID: "codex-help", Code: "OK", Stdout: "old help"}, "--remote")
	if f.Status != "WAITING_DEPENDENCY" || r.BaselineStatus != "WAITING_DEPENDENCY" || r.Diagnostics[0].Code != "CAPABILITY_NOT_ADVERTISED" {
		t.Fatalf("missing feature silently accepted: %+v %+v", f, r)
	}
}

func TestHelpOnStderrIsSupported(t *testing.T) {
	r := Report{BaselineStatus: "PASS", Status: "PARTIAL"}
	e := engine{report: &r}
	f := e.advertised(Command{ID: "opencode-help", Code: "OK", Stderr: "--auto --session"}, "--auto", "--session")
	if f.Status != "ADVERTISED_ONLY" || r.BaselineStatus != "PASS" {
		t.Fatalf("valid stderr help rejected: %+v %+v", f, r)
	}
}

func TestEvidencePathAndNoOverwrite(t *testing.T) {
	root := t.TempDir()
	dir, err := newEvidenceDir(root, filepath.Join(root, "run"))
	if err != nil {
		t.Fatal(err)
	}
	if err := writeJSON(filepath.Join(dir, "matrix.json"), map[string]string{"status": "original"}); err != nil {
		t.Fatal(err)
	}
	if err := writeJSON(filepath.Join(dir, "matrix.json"), "replacement"); err == nil {
		t.Fatal("overwrote evidence")
	}
	if _, err := newEvidenceDir(root, dir); err == nil {
		t.Fatal("reused evidence directory")
	}
	outside := t.TempDir()
	if _, err := newEvidenceDir(root, filepath.Join(outside, "escape")); err == nil {
		t.Fatal("accepted outside path")
	}
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	if _, err := newEvidenceDir(root, filepath.Join(root, "link", "escape")); err == nil {
		t.Fatal("accepted symlink escape")
	}
	if _, err := os.Stat(filepath.Join(outside, "escape")); !os.IsNotExist(err) {
		t.Fatalf("wrote outside root: %v", err)
	}
}

func TestMissingToolsStructuredReport(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	t.Setenv("PATH", "")
	t.Setenv("CMUX_WORKSPACE_ID", "workspace-fixture")
	r, code := Run(t.Context(), Options{Tool: "all", EvidenceDir: "missing", Timeout: time.Second})
	if code != 1 || r.Status != "WAITING_DEPENDENCY" || len(r.Tools) != 3 {
		t.Fatalf("code=%d report=%+v", code, r)
	}
	for _, c := range r.Commands {
		if c.Code != "EXECUTABLE_MISSING" || c.ExitCode != nil {
			t.Fatalf("incorrect missing executable evidence: %+v", c)
		}
		data, err := os.ReadFile(c.Evidence)
		if err != nil || !json.Valid(data) {
			t.Fatalf("missing structured command evidence: %v", err)
		}
	}
	data, err := os.ReadFile(filepath.Join(r.EvidenceDir, "matrix.json"))
	if err != nil || !json.Valid(data) {
		t.Fatalf("matrix not saved: %v", err)
	}
}

func TestInvalidOptionsDoNotCreateArtifacts(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	for _, opts := range []Options{{Tool: "bogus", EvidenceDir: "run", Timeout: time.Second}, {Tool: "all", EvidenceDir: "run", Timeout: 0}, {Tool: "all", EvidenceDir: "run", Timeout: 2 * time.Minute}} {
		_, code := Run(t.Context(), opts)
		if code != 2 {
			t.Fatalf("invalid options code=%d", code)
		}
	}
	if _, err := os.Stat("run"); !os.IsNotExist(err) {
		t.Fatalf("invalid options created artifacts: %v", err)
	}
}
