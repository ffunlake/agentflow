package runtime

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"agentflow/internal/config"
	"agentflow/internal/loopcore"
	"agentflow/internal/store"
)

// Headless owns bounded child processes, never terminal input. Coordinates
// prefixed headless: are logical bindings, not claims about cmux resources.
type Headless struct {
	runID, dir, binary, tool string
	config                   *config.Resolved
	timeout                  time.Duration
	ready                    map[string]Registration
}

type HeadlessOptions struct {
	RunID, RunDir, Binary string
	TurnTimeout           time.Duration
	Tool                  string // empty preserves the original Codex default
}

type HeadlessCodex = Headless
type HeadlessOpenCode = Headless
type HeadlessClaude = Headless
type HeadlessCodexOptions = HeadlessOptions
type HeadlessOpenCodeOptions = HeadlessOptions
type HeadlessClaudeOptions = HeadlessOptions

func NewHeadlessClaude(c *config.Resolved, o HeadlessClaudeOptions) (*HeadlessClaude, error) {
	if o.Tool != "" && o.Tool != "claude" {
		return nil, errors.New("headless Claude tool mismatch")
	}
	o.Tool = "claude"
	return NewHeadless(c, o)
}

func NewHeadlessCodex(c *config.Resolved, o HeadlessCodexOptions) (*HeadlessCodex, error) {
	if o.Tool != "" && o.Tool != "codex" {
		return nil, errors.New("headless Codex tool mismatch")
	}
	o.Tool = "codex"
	return NewHeadless(c, o)
}

func NewHeadlessOpenCode(c *config.Resolved, o HeadlessOpenCodeOptions) (*HeadlessOpenCode, error) {
	if o.Tool != "" && o.Tool != "opencode" {
		return nil, errors.New("headless OpenCode tool mismatch")
	}
	o.Tool = "opencode"
	return NewHeadless(c, o)
}

func NewHeadless(c *config.Resolved, o HeadlessOptions) (*Headless, error) {
	if o.Tool == "" {
		o.Tool = "codex"
	}
	if o.Tool != "codex" && o.Tool != "opencode" && o.Tool != "claude" {
		return nil, errors.New("unsupported headless tool")
	}
	plan, err := Compile(c, Options{})
	if err != nil {
		return nil, err
	}
	if !identityText(o.RunID) || !filepath.IsAbs(o.RunDir) || o.TurnTimeout <= 0 || o.TurnTimeout > 30*time.Minute {
		return nil, errors.New("headless requires run ID, absolute directory and turn timeout in (0,30m]")
	}
	for _, a := range plan.Config.Agents {
		if a.Tool != o.Tool || a.Session != "persistent" {
			return nil, errors.New("headless v1 requires only " + o.Tool + " agents with session=persistent")
		}
		if _, err := NativeCommand(a, o.Binary); err != nil {
			return nil, err
		}
	}
	if strings.Contains(filepath.ToSlash(o.Binary), "/cmux-cli-shims/") {
		return nil, errors.New("headless cannot use cmux shims")
	}
	return &Headless{runID: o.RunID, dir: o.RunDir, binary: o.Binary, tool: o.Tool, config: plan.Config,
		timeout: o.TurnTimeout, ready: map[string]Registration{}}, nil
}

func (a *Headless) binding(id string) NativeBinding {
	return NativeBinding{WorkspaceID: "headless:" + a.runID, SurfaceID: "headless:" + id}
}

// Launch is invoked only after Start has durably recorded LAUNCH_REQUESTED.
func (a *Headless) Launch(ctx context.Context, req LaunchRequest) (NativeBinding, error) {
	s := req.Seat
	role, ok := a.config.Agents[s.AgentID]
	if !ok || s.RunID != a.runID || s.Generation != 1 || s.Tool != a.tool || s.Cwd != role.Cwd || s.Role != role.Role || s.Session != role.Session || s.Permissions != role.Permissions {
		return NativeBinding{}, errors.New("headless startup binding mismatch")
	}
	var nonce [24]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return NativeBinding{}, err
	}
	s.Native = a.binding(s.AgentID)
	challenge := nativeReady{Actor: seatActor(s), Tool: a.tool, Status: Ready, Nonce: hex.EncodeToString(nonce[:])}
	encoded, _ := json.Marshal(challenge)
	command, _ := NativeCommand(role, a.binary)
	input := "Role: " + role.Role + "\nFrozen instructions:\n" + role.Prompt + "\nStartup handshake only. Do not use tools, inspect files or change files. Reply with exactly this JSON:\n" + string(encoded) + "\n"
	result, err := a.execute(ctx, filepath.Join(a.dir, "headless-start-"+s.AgentID+".json"), s, "", command, input)
	if err != nil {
		return NativeBinding{}, err
	}
	if a.tool == "opencode" || a.tool == "claude" {
		turn, err := a.nativeTurn([]byte(result.Stdout), "")
		if err != nil {
			return NativeBinding{}, err
		}
		s.Native.SessionID = turn.session
		if err := uniqueJSON([]byte(turn.text)); err != nil {
			return NativeBinding{}, err
		}
		native, claim, err := decodeNativeReady(turn.text, s, a.tool, challenge.Nonce, s.Native)
		if err != nil {
			return NativeBinding{}, err
		}
		a.ready[s.AgentID] = *claim
		return native, nil
	}
	// Codex may emit completed commentary before its final answer. READY must
	// match the final answer, never concatenated commentary or an earlier echo.
	final, err := codexFinalText([]byte(result.Stdout))
	if err != nil {
		return NativeBinding{}, err
	}
	if err := uniqueJSON([]byte(final)); err != nil {
		return NativeBinding{}, err
	}
	var thread string
	for _, line := range strings.Split(strings.TrimSpace(result.Stdout), "\n") {
		var e struct {
			Type   string `json:"type"`
			Thread string `json:"thread_id"`
		}
		if json.Unmarshal([]byte(line), &e) != nil {
			return NativeBinding{}, errors.New("invalid startup JSONL")
		}
		if e.Type == "thread.started" {
			if thread != "" || !identityText(e.Thread) {
				return NativeBinding{}, errors.New("startup thread identity invalid")
			}
			thread = e.Thread
		}
	}
	s.Native.SessionID = thread
	if _, err := parseNativeSubmission(s, []byte(result.Stdout)); err != nil {
		return NativeBinding{}, err
	}
	native, claim, err := decodeNativeReady(final, s, "codex", challenge.Nonce, s.Native)
	if err != nil {
		return NativeBinding{}, err
	}
	a.ready[s.AgentID] = *claim
	return native, nil
}

func (a *Headless) WaitReady(ctx context.Context, s SeatSnapshot) (Registration, error) {
	r, ok := a.ready[s.AgentID]
	if ctx.Err() != nil || !ok || r.Actor != seatActor(s) || r.Native != s.Native {
		return Registration{}, errors.New("headless READY binding mismatch")
	}
	return r, nil
}

func (a *Headless) validSeat(s SeatSnapshot) bool {
	b := a.binding(s.AgentID)
	_, ok := a.config.Agents[s.AgentID]
	return ok && s.RunID == a.runID && s.Generation == 1 && s.Tool == a.tool &&
		(a.tool != "claude" || claudeSessionID(s.Native.SessionID)) &&
		s.Native.validate() == nil && !strings.HasPrefix(s.Native.SessionID, "-") &&
		s.Native.WorkspaceID == b.WorkspaceID && s.Native.SurfaceID == b.SurfaceID
}

func (a *Headless) Inspect(ctx context.Context, s SeatSnapshot) (InputState, error) {
	if ctx.Err() != nil || !a.validSeat(s) {
		return InputState{}, errors.New("headless binding invalid")
	}
	// There is no editable terminal buffer. Store action claims and the single
	// coordinator run lock fence this adapter's processes.
	return InputState{Actor: seatActor(s), Native: s.Native, Verified: true}, nil
}

func (*Headless) Reset(context.Context, ResetRequest) (NativeBinding, error) {
	return NativeBinding{}, errors.New("headless fresh reset is unimplemented")
}

// headlessReset supplies inspection only; persistent policy never calls Reset.
type headlessReset struct{ *Headless }

func (headlessReset) SupportsFresh() bool { return false }

func (a headlessReset) WaitReady(context.Context, ResetRequest, NativeBinding) (Registration, error) {
	return Registration{}, errors.New("headless fresh reset is unimplemented")
}

func (a *Headless) ResetAdapter() ResetAdapter { return headlessReset{a} }

type headlessCapture struct {
	Seat      SeatSnapshot `json:"seat"`
	MessageID string       `json:"message_id"`
	Stdout    string       `json:"stdout"`
	Succeeded bool         `json:"succeeded"`
	Truncated bool         `json:"truncated"`
}

func (a *Headless) resultPath(m store.Message) string {
	sum := sha256.Sum256([]byte(m.ID))
	return filepath.Join(a.dir, fmt.Sprintf("headless-result-%x.json", sum))
}

func (a *Headless) execute(ctx context.Context, path string, s SeatSnapshot, id string, command Command, input string) (headlessCapture, error) {
	// Exclusive artifact reservation precedes the process. Interrupted writes
	// remain unknown and cannot authorize another invocation.
	if err := privateParent(path); err != nil {
		return headlessCapture{}, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return headlessCapture{}, submitFailure("HEADLESS_EVIDENCE_EXISTS_OR_INVALID", "UNKNOWN")
	}
	defer f.Close()
	turn, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()
	body, _, truncated, runErr := captureNative(turn, command, a.config.Agents[s.AgentID].Cwd, input)
	r := headlessCapture{Seat: s, MessageID: id, Stdout: string(body), Succeeded: runErr == nil && turn.Err() == nil, Truncated: truncated}
	if err := json.NewEncoder(f).Encode(r); err != nil {
		return r, err
	}
	if err := f.Sync(); err != nil {
		return r, err
	}
	if !r.Succeeded || truncated {
		// Preserve explicit native failures when present, otherwise UNKNOWN.
		_, parseErr := a.submission(s, body)
		var failure *NativeSubmitError
		if errors.As(parseErr, &failure) && failure.Outcome == "FAILED" && !truncated {
			return r, parseErr
		}
		return r, submitFailure("HEADLESS_EXEC_INCOMPLETE", "UNKNOWN")
	}
	return r, nil
}

func (a *Headless) Submit(ctx context.Context, s SeatSnapshot, m store.Message) (Submission, error) {
	if !a.validSeat(s) || s.Status != Ready || m.RunID != a.runID || m.Recipient != s.AgentID || m.Kind != "assign" {
		return Submission{}, submitFailure("HEADLESS_BINDING_MISMATCH", "FAILED")
	}
	command, err := NativeCommand(a.config.Agents[s.AgentID], a.binary)
	if err != nil {
		return Submission{}, err
	}
	command = nativeResumeCommand(command, a.tool, s.Native.SessionID)
	input := m.Body
	var intent loopcore.Intent
	if json.Unmarshal([]byte(m.Body), &intent) == nil && intent.Revision > 0 && (intent.Purpose == "execute" || intent.Purpose == "review") {
		input = workflowNativePrompt(input)
	}
	r, err := a.execute(ctx, a.resultPath(m), s, m.ID, command, input)
	if err != nil {
		return Submission{}, err
	}
	result, err := a.submission(s, []byte(r.Stdout))
	if err == nil {
		_, err = a.finalText(s, []byte(r.Stdout))
	}
	result.Channel = "headless-" + a.tool + "-resume"
	return result, err
}

// WorkflowReply reads only this message's immutable native capture after SENT.
// It is safe to call after restart; no native process is launched here.
func (a *Headless) WorkflowReply(ctx context.Context, s SeatSnapshot, m store.Message, c loopcore.Cursor) (ReplyMessage, loopcore.Event, error) {
	if ctx.Err() != nil || !a.validSeat(s) || c.DispatchID != m.ID {
		return ReplyMessage{}, loopcore.Event{}, errors.New("headless workflow binding mismatch")
	}
	path := a.resultPath(m)
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return ReplyMessage{}, loopcore.Event{}, errors.New("headless result requires a private regular file")
	}
	f, err := os.Open(path)
	if err != nil {
		return ReplyMessage{}, loopcore.Event{}, err
	}
	defer f.Close()
	var r headlessCapture
	d := json.NewDecoder(io.LimitReader(f, 32*1024*1024))
	d.DisallowUnknownFields()
	if d.Decode(&r) != nil || d.Decode(new(any)) != io.EOF || !r.Succeeded || r.Truncated || r.MessageID != m.ID || seatActor(r.Seat) != seatActor(s) || r.Seat.Native != s.Native {
		return ReplyMessage{}, loopcore.Event{}, errors.New("headless workflow capture invalid")
	}
	if r.Seat.Tool != a.tool {
		return ReplyMessage{}, loopcore.Event{}, errors.New("headless capture tool mismatch")
	}
	if _, err := a.submission(s, []byte(r.Stdout)); err != nil {
		return ReplyMessage{}, loopcore.Event{}, err
	}
	text, err := a.finalText(s, []byte(r.Stdout))
	if err != nil {
		return ReplyMessage{}, loopcore.Event{}, err
	}
	return workflowNativeReply("headless:", text, s, m, c)
}

func workflowNativePrompt(body string) string {
	var intent loopcore.Intent
	_ = json.Unmarshal([]byte(body), &intent)
	protocol := "AgentFlow result protocol: return exactly one final JSON object, no markdown. Fields: kind (handoff|approve|changes|retry|blocked), handoff ({summary,evidence:[references],next}), accepted ([boolean per acceptance criterion]), reason. Execute success requires handoff with at least one non-empty, truthful evidence reference; if no file was created, cite the concrete result from this stage. Review success requires approve and explicit true for every criterion. Use changes/retry with reason for failure, blocked with reason for a user decision. Do not claim tests or evidence you did not verify."
	if intent.Purpose == "execute" && len(intent.Acceptance) == 0 {
		protocol = "AgentFlow pipeline stage: perform only this role's part of the original task, using the preceding handoff as context. When finished, hand off to the next configured role. Return exactly one final JSON object, no markdown, with kind=handoff and handoff={summary,evidence:[references],next}. evidence must contain at least one non-empty, truthful reference; if no file was created, cite the concrete result from this stage. Use kind=retry with a reason to retry this stage, or kind=blocked with a reason to pause for the lead. Do not approve or review the whole task, and do not claim tests or evidence you did not verify."
	}
	return body + "\n" + protocol + "\n"
}

func workflowNativeReply(prefix, text string, s SeatSnapshot, m store.Message, c loopcore.Cursor) (ReplyMessage, loopcore.Event, error) {
	var result struct {
		Kind     string            `json:"kind"`
		Handoff  *loopcore.Handoff `json:"handoff,omitempty"`
		Accepted []bool            `json:"accepted,omitempty"`
		Reason   string            `json:"reason,omitempty"`
	}
	d := json.NewDecoder(strings.NewReader(text))
	d.DisallowUnknownFields()
	if uniqueJSON([]byte(text)) != nil || exactResultFields([]byte(text)) != nil || d.Decode(&result) != nil || d.Decode(new(any)) != io.EOF {
		return ReplyMessage{}, loopcore.Event{}, errors.New("native workflow result is not structured JSON")
	}
	status := "DONE"
	switch result.Kind {
	case "handoff", "approve", "changes":
	case "retry":
		status = "FAILED"
	case "blocked":
		status = "BLOCKED_CONFIRM"
	default:
		return ReplyMessage{}, loopcore.Event{}, errors.New("native workflow result kind invalid")
	}
	id := prefix + m.ID
	e := loopcore.Event{ID: id, Revision: c.Revision, Kind: result.Kind, Actor: s.AgentID, MessageID: m.ID,
		Handoff: result.Handoff, Accepted: result.Accepted, Reason: result.Reason}
	return ReplyMessage{Actor: seatActor(s), RequestID: id, MessageID: m.ID, Body: text, Status: status}, e, nil
}

func (a *Headless) submission(s SeatSnapshot, body []byte) (Submission, error) {
	if a.tool == "opencode" || a.tool == "claude" {
		if _, err := a.nativeTurn(body, s.Native.SessionID); err != nil {
			return Submission{}, err
		}
		return Submission{Actor: seatActor(s), Native: s.Native, Accepted: true, Channel: "headless-" + a.tool + "-resume"}, nil
	}
	return parseNativeSubmission(s, body)
}

func (a *Headless) finalText(s SeatSnapshot, body []byte) (string, error) {
	if a.tool == "opencode" || a.tool == "claude" {
		r, err := a.nativeTurn(body, s.Native.SessionID)
		return r.text, err
	}
	return codexFinalText(body)
}

// Both part-based protocols supply a validated session and final assistant text.
// Process ownership, durable captures and workflow decoding stay shared.
type nativeTurn struct{ session, text string }

func (a *Headless) nativeTurn(body []byte, expected string) (nativeTurn, error) {
	if a.tool == "claude" {
		return parseClaudeTurn(body, expected)
	}
	return parseOpenCodeTurn(body, expected)
}

func exactResultFields(body []byte) error {
	var fields map[string]json.RawMessage
	if json.Unmarshal(body, &fields) != nil || fields == nil {
		return errors.New("result must be an object")
	}
	for key, raw := range fields {
		switch key {
		case "kind", "accepted", "reason":
		case "handoff":
			var handoff map[string]json.RawMessage
			if json.Unmarshal(raw, &handoff) != nil || handoff == nil {
				return errors.New("handoff must be an object")
			}
			for field := range handoff {
				if field != "summary" && field != "evidence" && field != "next" {
					return errors.New("unknown handoff field")
				}
			}
		default:
			return errors.New("unknown result field")
		}
	}
	return nil
}

// encoding/json otherwise silently accepts duplicate object fields. A native
// result with ambiguous judgments cannot advance the workflow.
func uniqueJSON(body []byte) error {
	d := json.NewDecoder(bytes.NewReader(body))
	var value func() error
	value = func() error {
		token, err := d.Token()
		if err != nil {
			return err
		}
		switch token {
		case json.Delim('{'):
			seen := map[string]bool{}
			for d.More() {
				key, err := d.Token()
				name, ok := key.(string)
				if err != nil || !ok || seen[name] {
					return errors.New("duplicate or invalid result field")
				}
				seen[name] = true
				if err := value(); err != nil {
					return err
				}
			}
			_, err = d.Token()
			return err
		case json.Delim('['):
			for d.More() {
				if err := value(); err != nil {
					return err
				}
			}
			_, err = d.Token()
			return err
		}
		return nil
	}
	if err := value(); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return errors.New("trailing result data")
	}
	return nil
}

func codexFinalText(body []byte) (string, error) {
	scanner := bufio.NewScanner(bytes.NewReader(body))
	scanner.Buffer(make([]byte, 4096), 1024*1024)
	text := ""
	completed := false
	for scanner.Scan() {
		var e struct {
			Type string `json:"type"`
			Item struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"item"`
		}
		if json.Unmarshal(scanner.Bytes(), &e) != nil {
			return "", errors.New("invalid Codex JSONL")
		}
		if e.Type == "item.completed" && e.Item.Type == "agent_message" {
			if completed {
				return "", errors.New("assistant message after completion")
			}
			// Codex can emit commentary messages before the final result.
			text = e.Item.Text
		}
		if e.Type == "turn.completed" {
			if completed {
				return "", errors.New("multiple Codex turns")
			}
			completed = true
		}
	}
	if scanner.Err() != nil || !completed || !identityText(text) {
		return "", errors.New("Codex final result missing")
	}
	return text, nil
}
