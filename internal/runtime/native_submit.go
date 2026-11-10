package runtime

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"agentflow/internal/config"
	"agentflow/internal/store"
)

// NativeSubmitGuard must exclusively protect the exact native input from user
// edits and competing submissions until release. An inspection without ownership
// is insufficient. No TUI ownership/locking implementation is supplied here.
// cmux 0.65.2's surface.input_state is only a screen inspection, and
// terminal.input.exactly_once.v1 deduplicates identified input units; neither
// acquires exclusive ownership. A local mutex/file lock cannot fence human or
// other native writers. See docs/evidence/cmux-input-ownership-guard-2026-11-04.md.
type NativeSubmitGuard interface {
	Acquire(context.Context, SeatSnapshot) (input InputState, release func(), err error)
}

type NativeSubmitOptions struct {
	Seat       SeatSnapshot
	Agent      config.ResolvedAgent
	ToolBinary string
	Guard      NativeSubmitGuard
}

// NativeSubmitError deliberately excludes stdin, stderr and native event text.
// FAILED describes an explicit native failure, not proof that delivery never
// happened. SessionCoordinator conservatively leaves every error UNKNOWN.
type NativeSubmitError struct {
	Code    string
	Outcome string // FAILED or UNKNOWN; neither permits automatic replay
}

func (e *NativeSubmitError) Error() string { return e.Code + ": native submission " + e.Outcome }

func submitFailure(code, outcome string) error {
	return &NativeSubmitError{Code: code, Outcome: outcome}
}

// NativeSubmitAdapter runs a bounded foreground CLI resume against one explicitly
// bound generation/session. It never types into a terminal or launches a daemon.
// Durable action claims and retry policy remain SessionCoordinator's job.
type NativeSubmitAdapter struct {
	seat    SeatSnapshot
	command Command
	guard   NativeSubmitGuard
}

var _ SubmitAdapter = (*NativeSubmitAdapter)(nil)

func NewNativeSubmitAdapter(o NativeSubmitOptions) (*NativeSubmitAdapter, error) {
	s, a := o.Seat, o.Agent
	if a.Tool == "claude" {
		return nil, submitFailure("NATIVE_SUBMIT_TOOL_UNIMPLEMENTED", "FAILED")
	}
	if !identityText(s.RunID) || !identityText(s.AgentID) || s.Generation < 1 || s.Status != Ready ||
		s.Native.validate() != nil || strings.HasPrefix(s.Native.SessionID, "-") || o.Guard == nil ||
		s.Tool != a.Tool || s.Cwd != a.Cwd || s.Role != a.Role || s.Permissions != a.Permissions || s.Session != a.Session ||
		strings.Contains(filepath.ToSlash(o.ToolBinary), "/cmux-cli-shims/") {
		return nil, submitFailure("NATIVE_SUBMIT_OPTIONS_INVALID", "FAILED")
	}
	command, err := NativeCommand(a, o.ToolBinary)
	if err != nil {
		return nil, submitFailure("NATIVE_SUBMIT_COMMAND_INVALID", "FAILED")
	}
	command = nativeResumeCommand(command, a.Tool, s.Native.SessionID)
	return &NativeSubmitAdapter{seat: s, command: command, guard: o.Guard}, nil
}

func nativeResumeCommand(command Command, tool, session string) Command {
	if tool == "opencode" {
		command.Args = append(command.Args, "--session", session)
	} else if tool == "claude" {
		command.Args = append(command.Args, "--resume", session)
	} else {
		command.Args = append(command.Args[:len(command.Args)-1], "resume", session, "-")
	}
	return command
}

func (a *NativeSubmitAdapter) Submit(ctx context.Context, seat SeatSnapshot, m store.Message) (Submission, error) {
	if a == nil || a.guard == nil || seatActor(seat) != seatActor(a.seat) || seat.Native != a.seat.Native || seat.Tool != a.seat.Tool || seat.Status != Ready ||
		m.RunID != seat.RunID || m.Recipient != seat.AgentID || m.Kind != "assign" || !identityText(m.ID) || !identityText(m.RequestID) ||
		// Coordinator passes live identity only. If metadata is supplied it must
		// agree with the constructor's detached frozen configuration.
		(seat.Cwd != "" && seat.Cwd != a.seat.Cwd) || (seat.Role != "" && seat.Role != a.seat.Role) ||
		(seat.Permissions != "" && seat.Permissions != a.seat.Permissions) || (seat.Session != "" && seat.Session != a.seat.Session) {
		return Submission{}, submitFailure("NATIVE_SUBMIT_BINDING_MISMATCH", "FAILED")
	}
	if ctx.Err() != nil {
		return Submission{}, submitFailure("NATIVE_SUBMIT_CANCELLED", "UNKNOWN")
	}
	info, err := os.Stat(a.seat.Cwd)
	if err != nil || !info.IsDir() {
		return Submission{}, submitFailure("NATIVE_SUBMIT_CWD_INVALID", "FAILED")
	}
	input, release, err := a.guard.Acquire(ctx, a.seat)
	if release != nil {
		defer release()
	}
	if err != nil || release == nil || !input.Verified || input.Actor != seatActor(a.seat) || input.Native != a.seat.Native || input.Busy || input.Draft != "" || input.IncompleteInput {
		return Submission{}, submitFailure("NATIVE_SUBMIT_INPUT_UNVERIFIED", "UNKNOWN")
	}
	// The frozen body is passed byte-for-byte: no wrapper prompt, shell, argv,
	// environment variable, prompt file or additional acknowledgement challenge.
	body, _, truncated, runErr := captureNative(ctx, a.command, a.seat.Cwd, m.Body)
	if ctx.Err() != nil {
		return Submission{}, submitFailure("NATIVE_SUBMIT_CANCELLED", "UNKNOWN")
	}
	if truncated {
		return Submission{}, submitFailure("NATIVE_SUBMIT_OUTPUT_TRUNCATED", "UNKNOWN")
	}
	result, parseErr := parseNativeSubmission(a.seat, body)
	if runErr != nil {
		var failure *NativeSubmitError
		if errors.As(parseErr, &failure) && failure.Outcome == "FAILED" {
			return Submission{}, parseErr
		}
		return Submission{}, submitFailure("NATIVE_SUBMIT_EXEC_FAILED", "UNKNOWN")
	}
	return result, parseErr
}

// Only native protocol events can confirm the turn. Assistant prose, echoed user
// text and a zero process exit cannot replace identity and completion evidence.
func parseNativeSubmission(seat SeatSnapshot, body []byte) (Submission, error) {
	if seat.Tool == "claude" {
		// Claude is exposed through the headless adapter only.
		return Submission{}, submitFailure("NATIVE_SUBMIT_TOOL_UNIMPLEMENTED", "FAILED")
	}
	identified, assistant, completed := false, false, false
	setID := func(id string) bool {
		if id != seat.Native.SessionID {
			return false
		}
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
		if json.Unmarshal(scanner.Bytes(), &e) != nil || e.Type == "" {
			return Submission{}, submitFailure("NATIVE_SUBMIT_STREAM_INVALID", "UNKNOWN")
		}
		if (e.ThreadID != "" && !setID(e.ThreadID)) || (e.SessionID != "" && !setID(e.SessionID)) || (e.Part.SessionID != "" && !setID(e.Part.SessionID)) {
			return Submission{}, submitFailure("NATIVE_SUBMIT_IDENTITY_MISMATCH", "UNKNOWN")
		}
		if e.Type == "error" || e.Type == "turn.failed" {
			return Submission{}, submitFailure("NATIVE_SUBMIT_TURN_FAILED", "FAILED")
		}
		switch seat.Tool {
		case "codex":
			if e.Type == "thread.started" && e.ThreadID != "" {
				if identified {
					return Submission{}, submitFailure("NATIVE_SUBMIT_STREAM_INVALID", "UNKNOWN")
				}
				identified = true
			}
			if e.Type == "item.completed" && e.Item.Type == "agent_message" && identityText(e.Item.Text) && identified && !completed {
				assistant = true
			}
			if e.Type == "turn.completed" && identified && assistant {
				completed = true
			}
		case "opencode":
			if e.SessionID != "" || e.Part.SessionID != "" {
				identified = true
			}
			if e.Type == "text" && e.Part.Type == "text" && identityText(e.Part.Text) && identified && !completed {
				assistant = true
			}
			if e.Type == "step_finish" && e.Part.Reason == "stop" && identified && assistant {
				completed = true
			}
		default:
			return Submission{}, submitFailure("NATIVE_SUBMIT_TOOL_UNIMPLEMENTED", "FAILED")
		}
	}
	if scanner.Err() != nil || !identified || !assistant || !completed {
		return Submission{}, submitFailure("NATIVE_SUBMIT_RESULT_UNKNOWN", "UNKNOWN")
	}
	return Submission{Actor: seatActor(seat), Native: seat.Native, Accepted: true, Channel: "native-cli-" + seat.Tool + "-resume"}, nil
}
