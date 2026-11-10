package probe

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type Options struct {
	Tool           string
	EvidenceDir    string
	Timeout        time.Duration
	RequireRuntime bool
}

type Finding struct {
	Status   string   `json:"status"`
	Code     string   `json:"code"`
	Detail   string   `json:"detail"`
	Evidence []string `json:"evidence,omitempty"`
}

type Tool struct {
	Name         string             `json:"name"`
	Version      string             `json:"version"`
	Capabilities map[string]Finding `json:"capabilities"`
	SessionPlan  SessionPlan        `json:"isolated_session_plan"`
}

// SessionPlan is deliberately inert: runtime validation requires a separately
// owned visible terminal and an observed native identity, not a help exit code.
type SessionPlan struct {
	Status      string            `json:"status"`
	CWD         string            `json:"cwd"`
	LaunchArgv  []string          `json:"launch_argv"`
	Environment map[string]string `json:"environment_overrides"`
	Requires    []string          `json:"requires"`
}

type Report struct {
	Schema              int                `json:"schema_version"`
	Mode                string             `json:"mode"`
	Status              string             `json:"status"`
	BaselineStatus      string             `json:"baseline_status"`
	RuntimeAcceptance   string             `json:"runtime_acceptance"`
	GeneratedAt         string             `json:"generated_at"`
	Platform            string             `json:"platform"`
	CWD                 string             `json:"cwd"`
	EnvironmentIdentity map[string]string  `json:"environment_identity"`
	ProcessOverrides    map[string]string  `json:"process_environment_overrides"`
	CMUX                map[string]Finding `json:"cmux"`
	Tools               []Tool             `json:"tools"`
	Commands            []Command          `json:"commands"`
	Diagnostics         []Finding          `json:"diagnostics"`
	EvidenceDir         string             `json:"evidence_dir,omitempty"`
}

type engine struct {
	report  *Report
	exec    executor
	timeout time.Duration
	dir     string
}

func (e *engine) command(ctx context.Context, id, binary string, args ...string) Command {
	child, cancel := context.WithTimeout(ctx, e.timeout)
	defer cancel()
	c := e.exec.execute(child, id, binary, args)
	if e.dir != "" {
		c.Evidence = filepath.Join(e.dir, id+".json")
		if err := writeJSON(c.Evidence, c); err != nil {
			e.fail("EVIDENCE_WRITE_FAILED", err.Error())
		}
	}
	e.report.Commands = append(e.report.Commands, c)
	if c.Code != "OK" {
		e.fail(c.Code, id+": "+c.Error)
	}
	return c
}

func (e *engine) fail(code, detail string) {
	e.report.BaselineStatus, e.report.Status = "WAITING_DEPENDENCY", "WAITING_DEPENDENCY"
	e.report.Diagnostics = append(e.report.Diagnostics, Finding{Status: "WAITING_DEPENDENCY", Code: code, Detail: detail})
}

func observed(c Command, detail string) Finding {
	status := "PASS"
	if c.Code != "OK" {
		status = "WAITING_DEPENDENCY"
	}
	return Finding{Status: status, Code: c.Code, Detail: detail, Evidence: []string{c.ID}}
}

func (e *engine) advertised(c Command, terms ...string) Finding {
	if c.Code != "OK" {
		return observed(c, "help unavailable; runtime behavior unverified")
	}
	for _, term := range terms {
		if !strings.Contains(c.Stdout+"\n"+c.Stderr, term) {
			e.fail("CAPABILITY_NOT_ADVERTISED", c.ID+": missing "+term)
			return Finding{Status: "WAITING_DEPENDENCY", Code: "CAPABILITY_NOT_ADVERTISED", Detail: "missing help token: " + term, Evidence: []string{c.ID}}
		}
	}
	return Finding{Status: "ADVERTISED_ONLY", Code: "HELP_ONLY", Detail: "help advertises " + strings.Join(terms, ", ") + "; runtime behavior unverified", Evidence: []string{c.ID}}
}

func unverified(detail string) Finding {
	return Finding{Status: "UNVERIFIED", Code: "NO_RUNTIME_EVIDENCE", Detail: detail}
}

func (e *engine) tool(ctx context.Context, name string) {
	t := Tool{Name: name, Capabilities: map[string]Finding{}}
	version := e.command(ctx, name+"-version", name, "--version")
	t.Version = strings.TrimSpace(version.Stdout)
	t.Capabilities["version"] = observed(version, "underlying CLI version; cmux per-surface shims bypassed")
	if version.Code == "OK" && t.Version == "" {
		e.fail("EMPTY_VERSION", name+": version returned no stdout")
		t.Capabilities["version"] = Finding{Status: "WAITING_DEPENDENCY", Code: "EMPTY_VERSION", Detail: "no version stdout", Evidence: []string{version.ID}}
	}
	help := e.command(ctx, name+"-help", name, "--help")
	var argv []string
	switch name {
	case "opencode":
		serve := e.command(ctx, "opencode-serve-help", name, "serve", "--help")
		attach := e.command(ctx, "opencode-attach-help", name, "attach", "--help")
		run := e.command(ctx, "opencode-run-help", name, "run", "--help")
		t.Capabilities["launch_candidate"] = e.advertised(serve, "--hostname", "--port")
		t.Capabilities["visible_session_candidate"] = e.advertised(attach, "--session", "--dir")
		t.Capabilities["receive_candidate"] = e.advertised(run, "--attach", "--session")
		t.Capabilities["permission_candidate"] = e.advertised(help, "--auto")
		t.Capabilities["permission_candidate"] = addDetail(t.Capabilities["permission_candidate"], "--auto does not override explicit denies; full permissions remain unverified")
		argv = []string{name, "--pure"}
	case "codex":
		queue := e.command(ctx, "codex-queue-help", name, "queue", "--help")
		server := e.command(ctx, "codex-app-server-help", name, "app-server", "--help")
		resume := e.command(ctx, "codex-resume-help", name, "resume", "--help")
		t.Capabilities["launch_candidate"] = e.advertised(server, "app-server")
		t.Capabilities["visible_session_candidate"] = e.advertised(help, "--remote")
		t.Capabilities["receive_candidate"] = e.advertised(queue, "--thread", "--message", "--resume")
		t.Capabilities["receive_candidate"] = addDetail(t.Capabilities["receive_candidate"], "queue --resume exit 0 never proves receiver wake or acknowledgment")
		t.Capabilities["permission_candidate"] = e.advertised(help, "--dangerously-bypass-approvals-and-sandbox")
		t.Capabilities["resume_candidate"] = e.advertised(resume, "resume")
		argv = []string{name, "--no-daemon"}
	case "claude":
		t.Capabilities["launch_candidate"] = e.advertised(help, "--session-id")
		t.Capabilities["receive_candidate"] = unverified("cmux message hook presence and active reception have not been inspected or installed")
		t.Capabilities["permission_candidate"] = e.advertised(help, "--permission-mode", "bypassPermissions", "--dangerously-skip-permissions")
		t.Capabilities["resume_candidate"] = e.advertised(help, "--resume")
		argv = []string{name, "--bare"}
	}
	t.Capabilities["help"] = observed(help, "CLI help only")
	for _, key := range []string{"native_visible_binding", "idle_receive", "reset", "reset_completion", "role_reinjection", "identity_refresh", "effective_full_permissions", "resume_original", "busy", "input_draft", "different_cwd", "intentional_exit", "crash_recovery"} {
		t.Capabilities[key] = unverified("requires independent visible session, native session/thread ID, receiver acknowledgment and before/after evidence")
	}
	if version.Path != "" {
		argv[0] = version.Path
	}
	t.SessionPlan = SessionPlan{Status: "WAITING_DEPENDENCY", CWD: filepath.Join(e.dir, "scratch", "sessions", name), LaunchArgv: argv, Environment: e.report.ProcessOverrides, Requires: []string{
		"inert plan only; create a separate disposable visible terminal, never reuse current worker/main",
		"create the listed CWD and apply environment_overrides only to the new process; these paths contain no login credentials",
		"use underlying binary recorded in commands; review tool hooks/configuration and login in the disposable environment",
		"record workspace/surface UUID and native session/thread before sending any probe message",
		"record nonce acknowledgment in that same visible session; record reset identity change, role, permissions and resume separately",
		"bare/pure/no-daemon launch is only a candidate; it does not validate production hooks or full permissions",
	}}
	e.report.Tools = append(e.report.Tools, t)
}

func addDetail(f Finding, s string) Finding { f.Detail += "; " + s; return f }

func (e *engine) cmux(ctx context.Context) {
	v := e.command(ctx, "cmux-version", "cmux", "version")
	h := e.command(ctx, "cmux-help", "cmux", "--help")
	p := e.command(ctx, "cmux-ping", "cmux", "ping")
	c := e.command(ctx, "cmux-capabilities", "cmux", "--json", "capabilities")
	i := e.command(ctx, "cmux-identify", "cmux", "--json", "--id-format", "uuids", "identify")
	e.report.CMUX["version"] = observed(v, strings.TrimSpace(v.Stdout))
	e.report.CMUX["socket"] = observed(p, "read-only ping; no workspace creation")
	e.report.CMUX["capabilities"] = observed(c, "server capability advertisement only")
	e.report.CMUX["control_candidates"] = e.advertised(h, "new-workspace", "send-key", "read-screen")
	e.report.CMUX["identify"] = observed(i, "caller/focused observations are not a worker or native-session binding")
	for key, command := range map[string]Command{"identify": i, "capabilities": c} {
		if command.Code == "OK" {
			var object map[string]json.RawMessage
			if err := json.Unmarshal([]byte(command.Stdout), &object); err != nil || len(object) == 0 {
				e.fail("INVALID_CMUX_JSON", command.ID+": expected nonempty JSON object")
				e.report.CMUX[key] = Finding{Status: "WAITING_DEPENDENCY", Code: "INVALID_CMUX_JSON", Detail: "expected nonempty JSON object", Evidence: []string{command.ID}}
			}
		}
	}
	workspace := e.report.EnvironmentIdentity["CMUX_WORKSPACE_ID"]
	if workspace == "" {
		e.fail("CMUX_WORKSPACE_MISSING", "no CMUX_WORKSPACE_ID; refusing to guess an active workspace")
		e.report.CMUX["visible_topology"] = Finding{Status: "WAITING_DEPENDENCY", Code: "CMUX_WORKSPACE_MISSING", Detail: "tree not queried without explicit environment workspace"}
		return
	}
	tree := e.command(ctx, "cmux-tree", "cmux", "--json", "--id-format", "uuids", "tree", "--workspace", workspace)
	finding := observed(tree, "read-only topology snapshot; titles are not proof of provider or native-session identity")
	if tree.Code == "OK" {
		var topology struct {
			Windows []struct {
				Workspaces []struct {
					ID string `json:"id"`
				} `json:"workspaces"`
			} `json:"windows"`
		}
		if err := json.Unmarshal([]byte(tree.Stdout), &topology); err != nil {
			e.fail("INVALID_CMUX_JSON", "cmux-tree: "+err.Error())
			finding = Finding{Status: "WAITING_DEPENDENCY", Code: "INVALID_CMUX_JSON", Detail: err.Error(), Evidence: []string{tree.ID}}
		} else {
			found := false
			for _, window := range topology.Windows {
				for _, ws := range window.Workspaces {
					if strings.EqualFold(ws.ID, workspace) {
						found = true
					}
				}
			}
			if !found {
				e.fail("CMUX_WORKSPACE_NOT_VISIBLE", workspace+" not present in topology")
				finding = Finding{Status: "WAITING_DEPENDENCY", Code: "CMUX_WORKSPACE_NOT_VISIBLE", Detail: workspace, Evidence: []string{tree.ID}}
			}
		}
	}
	e.report.CMUX["visible_topology"] = finding
	if e.report.EnvironmentIdentity["CMUX_SURFACE_ID"] == "" && e.report.EnvironmentIdentity["CMUX_PANEL_ID"] == "" {
		e.report.CMUX["environment_surface"] = unverified("surface/panel environment identity absent; never substitute focused/caller surface")
	} else {
		e.report.CMUX["environment_surface"] = unverified("environment ID recorded; relationship to assigned worker requires independent confirmation")
	}
}

// Run writes a new evidence directory within the current checkout. It refuses
// overwrites and symlink escapes. Exit 0 means baseline checks only, never S1.
func Run(ctx context.Context, opts Options) (Report, int) {
	cwd, err := os.Getwd()
	r := Report{Schema: 1, Mode: "read-only-capability-check", Status: "PARTIAL", BaselineStatus: "PASS", RuntimeAcceptance: "UNVERIFIED", GeneratedAt: time.Now().UTC().Format(time.RFC3339Nano), Platform: runtime.GOOS + "/" + runtime.GOARCH, CWD: cwd, EnvironmentIdentity: map[string]string{}, CMUX: map[string]Finding{}, Tools: []Tool{}, Commands: []Command{}, Diagnostics: []Finding{}}
	e := engine{report: &r, timeout: opts.Timeout}
	if err != nil {
		e.fail("CWD_UNAVAILABLE", err.Error())
		return r, 1
	}
	for _, key := range []string{"CMUX_WORKSPACE_ID", "CMUX_SURFACE_ID", "CMUX_PANEL_ID"} {
		r.EnvironmentIdentity[key] = os.Getenv(key)
	}
	if opts.Timeout <= 0 || opts.Timeout > time.Minute {
		e.fail("INVALID_TIMEOUT", "timeout must be > 0 and <= 1m")
		return r, 2
	}
	names := []string{"opencode", "codex", "claude"}
	if opts.Tool != "all" && opts.Tool != "cmux" {
		if opts.Tool != "opencode" && opts.Tool != "codex" && opts.Tool != "claude" {
			e.fail("INVALID_TOOL", opts.Tool)
			return r, 2
		}
		names = []string{opts.Tool}
	}
	dir, err := newEvidenceDir(cwd, opts.EvidenceDir)
	if err != nil {
		e.fail("EVIDENCE_DIRECTORY_UNAVAILABLE", err.Error())
		return r, 1
	}
	e.dir, r.EvidenceDir = dir, dir
	// Configuration/state paths below are child-process-only and disposable.
	// No credentials/config are copied; these checks cannot establish login or
	// the effective permissions of any existing user session.
	env := os.Environ()
	r.ProcessOverrides = map[string]string{}
	for key, value := range map[string]string{"XDG_CONFIG_HOME": "config", "XDG_DATA_HOME": "data", "XDG_STATE_HOME": "state", "XDG_CACHE_HOME": "cache", "CODEX_HOME": "codex", "CLAUDE_CONFIG_DIR": "claude", "TMPDIR": "tmp"} {
		path := filepath.Join(dir, "scratch", value)
		if err := os.MkdirAll(path, 0700); err != nil {
			e.fail("SCRATCH_UNAVAILABLE", err.Error())
			return r, 1
		}
		env = setEnv(env, key, path)
		r.ProcessOverrides[key] = path
	}
	e.exec = localExecutor{cwd: cwd, env: env}
	e.command(ctx, "go-version", "go", "version")
	if opts.Tool != "cmux" {
		for _, name := range names {
			e.tool(ctx, name)
		}
	}
	e.cmux(ctx)
	if opts.RequireRuntime {
		r.Status = "WAITING_DEPENDENCY"
		r.Diagnostics = append(r.Diagnostics, Finding{Status: "WAITING_DEPENDENCY", Code: "RUNTIME_UNVERIFIED", Detail: "baseline checks cannot satisfy same-visible-session reception/reset/permission/recovery acceptance"})
	}
	if err := writeJSON(filepath.Join(dir, "matrix.json"), r); err != nil {
		e.fail("EVIDENCE_WRITE_FAILED", err.Error())
	}
	if r.Status == "WAITING_DEPENDENCY" {
		return r, 1
	}
	return r, 0
}

func setEnv(env []string, key, value string) []string {
	result := make([]string, 0, len(env)+1)
	for _, item := range env {
		if !strings.HasPrefix(item, key+"=") {
			result = append(result, item)
		}
	}
	return append(result, key+"="+value)
}

func newEvidenceDir(cwd, path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("--evidence-dir is required (new directory beneath current project)")
	}
	root, err := filepath.EvalSymlinks(cwd)
	if err != nil {
		return "", err
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(abs))
	if err != nil {
		return "", err
	}
	abs = filepath.Join(parent, filepath.Base(abs))
	rel, err := filepath.Rel(root, abs)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("evidence directory must be inside current project")
	}
	if err := os.Mkdir(abs, 0700); err != nil {
		return "", err
	}
	return abs, nil
}

func writeJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, writeErr := f.Write(append(data, '\n'))
	closeErr := f.Close()
	if writeErr != nil {
		return writeErr
	}
	return closeErr
}
