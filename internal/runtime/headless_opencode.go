package runtime

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"strings"
)

type openCodeTurn = nativeTurn

type openCodeEnvelope struct {
	Type, Session string
	Part          openCodePart
	Error         json.RawMessage
}

type openCodePart struct{ Type, Session, Message, Text, Reason string }

func (e *openCodeEnvelope) UnmarshalJSON(body []byte) error {
	return decodeNativeObject(body, func(key string, d *json.Decoder) error {
		switch key {
		case "type":
			return d.Decode(&e.Type)
		case "sessionID":
			return d.Decode(&e.Session)
		case "part":
			return d.Decode(&e.Part)
		case "error":
			return d.Decode(&e.Error)
		case "timestamp":
			var timestamp int64
			return d.Decode(&timestamp)
		default:
			return errors.New("unknown OpenCode envelope field")
		}
	})
}

func (p *openCodePart) UnmarshalJSON(body []byte) error {
	return decodeNativeObject(body, func(key string, d *json.Decoder) error {
		switch key {
		case "type":
			return d.Decode(&p.Type)
		case "sessionID":
			return d.Decode(&p.Session)
		case "messageID":
			return d.Decode(&p.Message)
		case "text":
			return d.Decode(&p.Text)
		case "reason":
			return d.Decode(&p.Reason)
		case "id", "time", "snapshot", "cost", "tokens", "callID", "tool", "state", "metadata":
			// Native metadata is opaque. It cannot provide identity, final text,
			// completion or READY, and its containing key is still exact.
			var metadata json.RawMessage
			return d.Decode(&metadata)
		default:
			return errors.New("unknown OpenCode part field")
		}
	})
}

// OpenCode run --format json emits assistant parts per step. Only the text of
// the last step finished with reason=stop is a final answer. Tool-call steps,
// reasoning and user echoes cannot establish READY or a workflow result.
func parseOpenCodeTurn(body []byte, expected string) (openCodeTurn, error) {
	var result openCodeTurn
	var text strings.Builder
	message := ""
	completed, active := false, false
	fail := func(code string) (openCodeTurn, error) { return openCodeTurn{}, submitFailure(code, "UNKNOWN") }
	scanner := bufio.NewScanner(bytes.NewReader(body))
	scanner.Buffer(make([]byte, 4096), 1024*1024)
	for scanner.Scan() {
		var e openCodeEnvelope
		if json.Unmarshal(scanner.Bytes(), &e) != nil || e.Type == "" || (e.Type != "error" && len(e.Error) != 0) {
			return fail("OPENCODE_STREAM_INVALID")
		}
		if completed {
			return fail("OPENCODE_EVENT_AFTER_COMPLETION")
		}
		for _, id := range []string{e.Session, e.Part.Session} {
			if id == "" {
				continue
			}
			if !identityText(id) || strings.HasPrefix(id, "-") || (expected != "" && id != expected) || (result.session != "" && id != result.session) {
				return fail("OPENCODE_SESSION_MISMATCH")
			}
			result.session = id
		}
		if e.Type == "error" {
			return openCodeTurn{}, submitFailure("OPENCODE_TURN_FAILED", "FAILED")
		}
		switch e.Type {
		case "step_start", "text", "step_finish", "tool_use", "reasoning":
			if result.session == "" || (e.Session == "" && e.Part.Session == "") || !identityText(e.Part.Message) {
				return fail("OPENCODE_PART_IDENTITY_MISSING")
			}
			if message != "" && message != e.Part.Message {
				return fail("OPENCODE_MESSAGE_MISMATCH")
			}
			message = e.Part.Message
		}
		switch e.Type {
		case "step_start":
			if active || e.Part.Type != "step-start" {
				return fail("OPENCODE_STEP_INVALID")
			}
			active = true
			text.Reset()
		case "text":
			if !active || e.Part.Type != "text" {
				return fail("OPENCODE_TEXT_INVALID")
			}
			text.WriteString(e.Part.Text)
		case "step_finish":
			if !active || e.Part.Type != "step-finish" {
				return fail("OPENCODE_STEP_INVALID")
			}
			active = false
			switch e.Part.Reason {
			case "stop":
				if !identityText(text.String()) {
					return fail("OPENCODE_FINAL_MISSING")
				}
				result.text, completed = text.String(), true
			case "tool-calls":
				text.Reset()
				message = ""
			default:
				return fail("OPENCODE_FINISH_UNKNOWN")
			}
		case "tool_use", "reasoning":
			// These parts cannot supply final answer text.
		default:
			return fail("OPENCODE_EVENT_UNKNOWN")
		}
	}
	if scanner.Err() != nil || !completed || result.session == "" {
		return fail("OPENCODE_RESULT_UNKNOWN")
	}
	return result, nil
}
