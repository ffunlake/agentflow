package runtime

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"agentflow/internal/config"
	"agentflow/internal/ipc"
)

const NativeTargetRequired = "SEAT_NATIVE_TARGET_REQUIRED"

// NativeOptions authorizes a foreground child of the current cmux surface only.
// It does not authorize typing a launch command into another terminal. Executable
// paths are absolute; cmux CLI shims are never used for native tools.
type NativeOptions struct {
	Target     CMUXTarget `json:"target"`
	ToolBinary string     `json:"tool_binary"`
	CMUXBinary string     `json:"cmux_binary"`
	// EvidencePath optionally records the challenge and bounded native stdout.
	// It must be a new private file beside the startup snapshot. Never stderr.
	EvidencePath string `json:"evidence_path,omitempty"`
}

type SeatExecution struct {
	Status   string        `json:"status"`
	Executed bool          `json:"executed"`
	Native   NativeBinding `json:"native,omitempty"`
	Claim    *Registration `json:"claim,omitempty"`
	Error    *SeatError    `json:"error,omitempty"`
}

func nativeFailure(result SeatExecution, code string) SeatExecution {
	result.Status, result.Claim = "FAILED", nil
	result.Error = &SeatError{code, "seat.native", "native startup was not verified; inspect the explicit target and tool availability"}
	return result
}

// NativeExecutable resolves the real CLI, skipping cmux routing/hook shims and
// relative PATH entries. No executable or user configuration is changed.
func NativeExecutable(name string) (string, error) {
	if name != "opencode" && name != "codex" && name != "claude" && name != "cmux" {
		return "", errors.New("unsupported native executable")
	}
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if !filepath.IsAbs(dir) || strings.Contains(filepath.ToSlash(dir), "/cmux-cli-shims/") || filepath.Base(dir) == "cmux-cli-shims" {
			continue
		}
		if path, err := exec.LookPath(filepath.Join(dir, name)); err == nil {
			return path, nil
		}
	}
	return "", exec.ErrNotFound
}

func validNativeOptions(o NativeOptions) bool {
	return cmuxRef(o.Target.WindowID, "window") && cmuxRef(o.Target.WorkspaceID, "workspace") &&
		cmuxRef(o.Target.SurfaceID, "surface") && o.Target.SplitDepth == 0 &&
		filepath.IsAbs(o.ToolBinary) && filepath.IsAbs(o.CMUXBinary) &&
		!strings.ContainsRune(o.ToolBinary+o.CMUXBinary, '\x00') &&
		!strings.Contains(filepath.ToSlash(o.ToolBinary), "/cmux-cli-shims/")
}

// NativeCommand accepts only adapter-owned startup flags plus a small explicit
// extension (--variant for OpenCode, --profile for Codex; --bare or --safe-mode
// for Claude). Unknown/raw flags fail
// before execution: format, session, cwd and prompt transport cannot be replaced.
func NativeCommand(a config.ResolvedAgent, binary string) (Command, error) {
	if a.Tool != "opencode" && a.Tool != "codex" && a.Tool != "claude" {
		return Command{}, &SeatError{SeatToolAdapterUnimplemented, "seat.tool", "native tool adapter is not implemented"}
	}
	if !filepath.IsAbs(binary) || !filepath.IsAbs(a.Cwd) || strings.ContainsRune(binary+a.Cwd+a.Model, '\x00') ||
		(a.Permissions != "default" && a.Permissions != "full") || (a.Session != "fresh" && a.Session != "persistent") {
		return Command{}, errors.New("invalid native command metadata")
	}
	args := []string{"run", "--format", "json", "--dir", a.Cwd}
	extension := "--variant"
	if a.Tool == "codex" {
		args = []string{"exec", "--json", "--color", "never", "--skip-git-repo-check", "--cd", a.Cwd}
		extension = "--profile"
	}
	if a.Tool == "claude" {
		// Bare is the strict default: never read OAuth/keychain implicitly.
		// Only frozen resolved args may opt into non-bare CLI-managed OAuth.
		mode := "--bare"
		if len(a.Args) != 0 {
			if len(a.Args) != 1 || (a.Args[0] != "--bare" && a.Args[0] != "--safe-mode") {
				return Command{}, errors.New("unsupported native Claude authentication arguments")
			}
			mode = a.Args[0]
		}
		// Text is stdin; cwd belongs to captureNative. Settings/MCP/skills are
		// suppressed explicitly in both modes; no permission bypass by default.
		args = []string{"--print", mode, "--verbose", "--output-format", "stream-json", "--input-format", "text", "--permission-prompts", "none", "--prompt-suggestions", "false", "--strict-mcp-config", "--setting-sources", "", "--disable-slash-commands", "--no-chrome"}
		extension = ""
	}
	if a.Permissions == "full" {
		if a.Tool == "codex" {
			args = append(args, "--dangerously-bypass-approvals-and-sandbox")
		} else if a.Tool == "claude" {
			args = append(args, "--dangerously-skip-permissions")
		} else {
			args = append(args, "--auto")
		}
	}
	if a.Model != "" {
		args = append(args, "--model", a.Model)
	}
	if len(a.Args) != 0 && a.Tool != "claude" {
		if extension == "" || len(a.Args) != 2 || a.Args[0] != extension || a.Args[1] == "" || strings.HasPrefix(a.Args[1], "-") || strings.ContainsRune(a.Args[1], '\x00') {
			return Command{}, errors.New("unsupported native tool arguments")
		}
		args = append(args, a.Args...)
	}
	if a.Tool == "codex" {
		args = append(args, "-") // prompt is stdin, never shell source or argv
	}
	return Command{Binary: binary, Args: args}, nil
}

type nativeReady struct {
	Actor  ipc.Actor `json:"actor"`
	Tool   string    `json:"tool"`
	Status string    `json:"status"`
	Nonce  string    `json:"nonce"`
}

// decodeNativeObject checks keys before encoding/json can apply its
// case-insensitive struct matching or overwrite a duplicate field.
func decodeNativeObject(body []byte, field func(string, *json.Decoder) error) error {
	if err := uniqueJSON(body); err != nil {
		return err
	}
	d := json.NewDecoder(bytes.NewReader(body))
	if token, err := d.Token(); err != nil || token != json.Delim('{') {
		return errors.New("native JSON must be an object")
	}
	for d.More() {
		token, err := d.Token()
		key, ok := token.(string)
		if err != nil || !ok {
			return errors.New("invalid native JSON field")
		}
		if err := field(key, d); err != nil {
			return err
		}
	}
	if _, err := d.Token(); err != nil {
		return err
	}
	if d.Decode(new(any)) != io.EOF {
		return errors.New("trailing native JSON data")
	}
	return nil
}

func (r *nativeReady) UnmarshalJSON(body []byte) error {
	return decodeNativeObject(body, func(key string, d *json.Decoder) error {
		switch key {
		case "actor":
			var body json.RawMessage
			if err := d.Decode(&body); err != nil {
				return err
			}
			return decodeNativeObject(body, func(key string, d *json.Decoder) error {
				switch key {
				case "runId":
					return d.Decode(&r.Actor.RunID)
				case "agentId":
					return d.Decode(&r.Actor.AgentID)
				case "generation":
					return d.Decode(&r.Actor.Generation)
				default:
					return errors.New("unknown READY actor field")
				}
			})
		case "tool":
			return d.Decode(&r.Tool)
		case "status":
			return d.Decode(&r.Status)
		case "nonce":
			return d.Decode(&r.Nonce)
		default:
			return errors.New("unknown READY field")
		}
	})
}

// ParseNativeReady consumes native JSONL, not terminal text. Only a completed
// assistant message can acknowledge the challenge. User echoes, reasoning, tool
// output, idle, thread.started and step_finish alone cannot establish READY.
func ParseNativeReady(tool string, body []byte, seat SeatSnapshot, nonce string) (NativeBinding, *Registration, error) {
	native := NativeBinding{WorkspaceID: seat.Native.WorkspaceID, SurfaceID: seat.Native.SurfaceID}
	var text strings.Builder
	completed, failed := false, false
	setID := func(id string) bool {
		if !identityText(id) || (native.SessionID != "" && native.SessionID != id) {
			return false
		}
		native.SessionID = id
		return true
	}
	scanner := bufio.NewScanner(bytes.NewReader(body))
	scanner.Buffer(make([]byte, 4096), 1024*1024)
	for scanner.Scan() {
		var e struct {
			Type      string `json:"type"`
			ThreadID  string `json:"thread_id"`
			SessionID string `json:"sessionID"`
			Item      struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"item"`
			Part struct {
				Type      string `json:"type"`
				Text      string `json:"text"`
				SessionID string `json:"sessionID"`
				Reason    string `json:"reason"`
			} `json:"part"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &e); err != nil {
			return native, nil, errors.New("invalid native event stream")
		}
		if e.Type == "error" || e.Type == "turn.failed" {
			failed = true
		}
		switch tool {
		case "codex":
			if e.Type == "thread.started" && !setID(e.ThreadID) {
				return native, nil, errors.New("native thread identity changed")
			}
			if e.Type == "item.completed" && e.Item.Type == "agent_message" {
				text.WriteString(e.Item.Text)
			}
			if e.Type == "turn.completed" {
				completed = true
			}
		case "opencode":
			if e.SessionID != "" && !setID(e.SessionID) {
				return native, nil, errors.New("native session identity changed")
			}
			if e.Part.SessionID != "" && !setID(e.Part.SessionID) {
				return native, nil, errors.New("native part identity changed")
			}
			if e.Type == "text" && e.Part.Type == "text" {
				text.WriteString(e.Part.Text)
			}
			if e.Type == "step_finish" && e.Part.Reason == "stop" {
				completed = true
			}
		default:
			return native, nil, errors.New("unsupported native event stream")
		}
	}
	if scanner.Err() != nil || failed || !completed || native.validate() != nil || nonce == "" {
		return native, nil, errors.New("native turn did not complete with an identity")
	}
	return decodeNativeReady(text.String(), seat, tool, nonce, native)
}

func decodeNativeReady(text string, seat SeatSnapshot, tool, nonce string, native NativeBinding) (NativeBinding, *Registration, error) {
	var actual nativeReady
	d := json.NewDecoder(strings.NewReader(text))
	d.DisallowUnknownFields()
	if err := d.Decode(&actual); err != nil {
		return native, nil, errors.New("assistant READY is missing or malformed")
	}
	expected := nativeReady{Actor: ipc.Actor{RunID: seat.RunID, AgentID: seat.AgentID, Generation: seat.Generation}, Tool: tool, Status: Ready, Nonce: nonce}
	if d.Decode(new(any)) != io.EOF || actual != expected {
		return native, nil, &ipc.Error{Code: ipc.ActorMismatch, Message: "assistant READY challenge or identity mismatch"}
	}
	// Native coordinates come from this process's independently verified cmux
	// location and native stream. The model cannot supply or replace these IDs.
	return native, &Registration{Actor: actual.Actor, Tool: tool, Status: Ready, Native: native, Nonce: actual.Nonce}, nil
}

type nativeBuffer struct {
	bytes.Buffer
	truncated bool
}

func (b *nativeBuffer) Write(p []byte) (int, error) {
	n := len(p)
	remaining := 4*1024*1024 - b.Len()
	if len(p) > remaining {
		p, b.truncated = p[:remaining], true
	}
	_, _ = b.Buffer.Write(p)
	return n, nil
}

func captureNative(ctx context.Context, command Command, cwd, input string) ([]byte, bool, bool, error) {
	cmd := exec.CommandContext(ctx, command.Binary, command.Args...)
	cmd.Dir, cmd.Stdin, cmd.WaitDelay = cwd, strings.NewReader(input), 250*time.Millisecond
	var out, diagnostic nativeBuffer
	cmd.Stdout, cmd.Stderr = &out, &diagnostic
	err := cmd.Run()
	return out.Bytes(), cmd.ProcessState != nil, out.truncated || diagnostic.truncated, err
}

// verifyForeground uses tree's real caller, not identify with an explicit
// --surface (which merely reports the requested target as caller). Every cmux
// command has explicit window/workspace/surface coordinates where supported.
func verifyForeground(ctx context.Context, o NativeOptions, cwd string) (NativeBinding, error) {
	body, _, truncated, err := captureNative(ctx, Command{Binary: o.CMUXBinary, Args: []string{
		"--json", "--id-format", "both", "tree", "--window", o.Target.WindowID, "--workspace", o.Target.WorkspaceID,
	}}, cwd, "")
	if err != nil || truncated {
		return NativeBinding{}, errors.New("cannot inspect foreground target")
	}
	// Explicit tags keep UUIDs and short refs distinct when comparing the caller.
	var response struct {
		Caller struct {
			WindowID     string `json:"window_id"`
			WindowRef    string `json:"window_ref"`
			WorkspaceID  string `json:"workspace_id"`
			WorkspaceRef string `json:"workspace_ref"`
			SurfaceID    string `json:"surface_id"`
			SurfaceRef   string `json:"surface_ref"`
		} `json:"caller"`
	}
	if json.Unmarshal(body, &response) != nil {
		return NativeBinding{}, errors.New("invalid cmux caller evidence")
	}
	c := response.Caller
	match := func(want, id, ref string) bool { return id != "" && (strings.EqualFold(want, id) || want == ref) }
	if !match(o.Target.WindowID, c.WindowID, c.WindowRef) || !match(o.Target.WorkspaceID, c.WorkspaceID, c.WorkspaceRef) || !match(o.Target.SurfaceID, c.SurfaceID, c.SurfaceRef) {
		return NativeBinding{}, errors.New("target is not the foreground caller")
	}
	return NativeBinding{WorkspaceID: c.WorkspaceID, SurfaceID: c.SurfaceID}, nil
}

// ExecuteNativeSeat is an explicit one-shot foreground handshake, with no daemon,
// retries, login or terminal injection. Persistent policy creates its first session
// here; subsequent resume/reset is outside S4. Native stderr is never published.
func ExecuteNativeSeat(ctx context.Context, path, id string, o NativeOptions) SeatExecution {
	result := SeatExecution{Status: "FAILED"}
	seat, err := LoadStartupSeat(path, id)
	if err != nil {
		var diagnostic *SeatError
		if errors.As(err, &diagnostic) {
			result.Error = diagnostic
			return result
		}
		return nativeFailure(result, SeatSnapshotInvalid)
	}
	if seat.Tool == "claude" {
		return nativeFailure(result, SeatToolAdapterUnimplemented)
	}
	if !validNativeOptions(o) {
		return nativeFailure(result, NativeTargetRequired)
	}
	info, err := os.Stat(seat.Cwd)
	if err != nil || !info.IsDir() {
		return nativeFailure(result, "SEAT_CWD_INVALID")
	}
	snapshot, err := ReadSnapshot(path)
	if err != nil {
		return nativeFailure(result, SeatSnapshotInvalid)
	}
	command, err := NativeCommand(snapshot.Config.Agents[id], o.ToolBinary)
	if err != nil {
		return nativeFailure(result, "SEAT_TOOL_ARGUMENTS_INVALID")
	}
	location, err := verifyForeground(ctx, o, seat.Cwd)
	if err != nil {
		return nativeFailure(result, "SEAT_NATIVE_TARGET_MISMATCH")
	}
	seat.Native = location
	var random [24]byte
	if _, err := rand.Read(random[:]); err != nil {
		return nativeFailure(result, "SEAT_NONCE_FAILED")
	}
	nonce := hex.EncodeToString(random[:])
	ready := nativeReady{Actor: ipc.Actor{RunID: seat.RunID, AgentID: seat.AgentID, Generation: seat.Generation}, Tool: seat.Tool, Status: Ready, Nonce: nonce}
	challenge, _ := json.Marshal(ready)
	var evidence *os.File
	if o.EvidencePath != "" {
		if !filepath.IsAbs(o.EvidencePath) || filepath.Dir(o.EvidencePath) != filepath.Dir(path) || privateParent(o.EvidencePath) != nil {
			return nativeFailure(result, "SEAT_NATIVE_EVIDENCE_INVALID")
		}
		evidence, err = os.OpenFile(o.EvidencePath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return nativeFailure(result, "SEAT_NATIVE_EVIDENCE_INVALID")
		}
		defer evidence.Close()
		if json.NewEncoder(evidence).Encode(struct {
			Type      string          `json:"type"`
			Challenge json.RawMessage `json:"challenge"`
			Command   Command         `json:"command"`
			Location  NativeBinding   `json:"location"`
		}{"challenge", challenge, command, location}) != nil || evidence.Sync() != nil {
			return nativeFailure(result, "SEAT_NATIVE_EVIDENCE_FAILED")
		}
	}
	a := snapshot.Config.Agents[id]
	input := "Role: " + seat.Role + "\nFrozen role instructions:\n" + a.Prompt + "\nStartup handshake only. Do not use tools or change files. Acknowledge the role by replying with exactly this JSON and no other text:\n" + string(challenge) + "\n"
	body, executed, truncated, runErr := captureNative(ctx, command, seat.Cwd, input)
	result.Executed = executed
	if evidence != nil {
		if json.NewEncoder(evidence).Encode(struct {
			Type      string `json:"type"`
			Stdout    string `json:"stdout"`
			Executed  bool   `json:"executed"`
			Truncated bool   `json:"truncated"`
			Succeeded bool   `json:"process_succeeded"`
		}{"finished", string(body), executed, truncated, runErr == nil}) != nil || evidence.Sync() != nil {
			return nativeFailure(result, "SEAT_NATIVE_EVIDENCE_FAILED")
		}
	}
	native, claim, parseErr := ParseNativeReady(seat.Tool, body, seat, nonce)
	if native.SessionID != "" {
		result.Native = native
	}
	if ctx.Err() != nil {
		return nativeFailure(result, "SEAT_NATIVE_CANCELLED")
	}
	if runErr != nil {
		return nativeFailure(result, "SEAT_NATIVE_EXEC_FAILED")
	}
	if truncated {
		return nativeFailure(result, "SEAT_NATIVE_OUTPUT_TRUNCATED")
	}
	if parseErr != nil {
		return nativeFailure(result, "SEAT_NATIVE_READY_INVALID")
	}
	result.Status, result.Claim = Ready, claim
	return result
}

// ForegroundLauncher implements Launcher for a single owned terminal. A coordinator
// must run in that surface; this cannot bind invisible children to other panes.
// New team topology/tool launch uses LaunchCMUXWorkspace and a separate TUI entry.
type ForegroundLauncher struct {
	Options NativeOptions
	seat    *SeatSnapshot
	result  SeatExecution
}

var _ Launcher = (*ForegroundLauncher)(nil)

// ExecutionStarted reports execution confirmed by a decoded wrapper result.
// A false value after cancellation leaves the native outcome unknown.
func (l *ForegroundLauncher) ExecutionStarted() bool { return l.result.Executed }

func (l *ForegroundLauncher) Launch(ctx context.Context, request LaunchRequest) (NativeBinding, error) {
	if l.seat != nil {
		return NativeBinding{}, errors.New("foreground launcher already used; no replay")
	}
	if len(request.Wrapper.Args) != 5 || request.Wrapper.Args[0] != "_seat" || request.Wrapper.Args[1] != "--snapshot" || request.Wrapper.Args[3] != "--agent" || !filepath.IsAbs(request.Wrapper.Binary) {
		return NativeBinding{}, errors.New("invalid foreground wrapper command")
	}
	path := request.Wrapper.Args[2]
	seat, err := LoadStartupSeat(path, request.Wrapper.Args[4])
	if err != nil || !reflect.DeepEqual(seat, request.Seat) || !validNativeOptions(l.Options) {
		return NativeBinding{}, errors.New("foreground launch differs from startup snapshot")
	}
	artifact, err := ReadSnapshot(path)
	if err != nil || len(artifact.Seats) != 1 || len(request.PanePath) != 0 {
		return NativeBinding{}, errors.New("foreground launcher requires a single-seat plan; team topology transport is unimplemented")
	}
	plan, err := Compile(artifact.Config, Options{WrapperBinary: request.Wrapper.Binary, SnapshotPath: path})
	if err != nil {
		return NativeBinding{}, err
	}
	// Reuse S4.4 validation and keep its cmux topology argv separate. Creating a
	// new workspace here would claim a terminal that does not own this process.
	launchPlan, err := BuildCMUXCommand(plan, seat.AgentID, CMUXTarget{WindowID: l.Options.Target.WindowID})
	if err != nil || !reflect.DeepEqual(launchPlan.Wrapper, request.Wrapper) || launchPlan.Cwd != seat.Cwd {
		return NativeBinding{}, errors.New("foreground wrapper differs from canonical cmux launch data")
	}
	// Consume the launch before external calls; unknown outcomes must not replay.
	l.seat = &seat
	options, _ := json.Marshal(l.Options)
	cmd := exec.CommandContext(ctx, request.Wrapper.Binary, request.Wrapper.Args...)
	cmd.Dir, cmd.WaitDelay = seat.Cwd, 2*time.Second
	// Let _seat cancel and reap its native child before killing the wrapper.
	cmd.Cancel = func() error { return cmd.Process.Signal(os.Interrupt) }
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "AGENTFLOW_NATIVE_OPTIONS=") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	cmd.Env = append(cmd.Env, "AGENTFLOW_NATIVE_OPTIONS="+string(options))
	var out, diagnostic nativeBuffer
	cmd.Stdout, cmd.Stderr = &out, &diagnostic
	runErr := cmd.Run()
	if ctx.Err() != nil {
		return NativeBinding{}, ctx.Err()
	}
	d := json.NewDecoder(bytes.NewReader(out.Bytes()))
	d.DisallowUnknownFields()
	if out.truncated || diagnostic.truncated || d.Decode(&l.result) != nil || d.Decode(new(any)) != io.EOF {
		return NativeBinding{}, errors.New("invalid foreground wrapper result")
	}
	if runErr != nil && l.result.Status == Ready {
		return NativeBinding{}, errors.New("wrapper failed after READY")
	}
	if err := l.result.Native.validate(); err != nil {
		if l.result.Error != nil {
			return NativeBinding{}, l.result.Error
		}
		return NativeBinding{}, errors.New("wrapper did not report native identity")
	}
	return l.result.Native, nil
}

func (l *ForegroundLauncher) WaitReady(ctx context.Context, seat SeatSnapshot) (Registration, error) {
	if err := ctx.Err(); err != nil {
		return Registration{}, err
	}
	if l.seat == nil {
		return Registration{}, errors.New("foreground launch has not run")
	}
	expected := *l.seat
	expected.Native = l.result.Native
	if !reflect.DeepEqual(expected, seat) {
		return Registration{}, errors.New("foreground READY seat mismatch")
	}
	if l.result.Error != nil {
		return Registration{}, l.result.Error
	}
	claim := l.result.Claim
	actor := ipc.Actor{RunID: seat.RunID, AgentID: seat.AgentID, Generation: seat.Generation}
	if !l.result.Executed || l.result.Status != Ready || claim == nil || claim.Actor != actor || claim.Tool != seat.Tool || claim.Status != Ready || claim.Native != seat.Native {
		return Registration{}, errors.New("foreground READY identity mismatch")
	}
	return *claim, nil
}
