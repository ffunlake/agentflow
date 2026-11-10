package runtime

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
)

// Claude --resume also accepts names and transcript paths. Only UUID sessions
// discovered from the native init event are valid bindings for this adapter.
func claudeSessionID(id string) bool {
	if len(id) != 36 || id[8] != '-' || id[13] != '-' || id[18] != '-' || id[23] != '-' {
		return false
	}
	hexID := strings.ReplaceAll(id, "-", "")
	_, err := hex.DecodeString(hexID)
	return len(hexID) == 32 && err == nil
}

// Exact keys and duplicate detection apply recursively, including opaque native
// metadata. Metadata never supplies session identity, assistant text or success.
func claudeObject(body []byte, allowed string) (map[string]json.RawMessage, error) {
	fields := map[string]json.RawMessage{}
	err := decodeNativeObject(body, func(key string, d *json.Decoder) error {
		known := false
		for _, name := range strings.Fields(allowed) {
			known = known || key == name
		}
		if !known {
			return errors.New("unknown Claude protocol field")
		}
		var raw json.RawMessage
		if err := d.Decode(&raw); err != nil {
			return err
		}
		fields[key] = raw
		return nil
	})
	return fields, err
}

func claudeValue(fields map[string]json.RawMessage, key string, value any) bool {
	raw := fields[key]
	return len(raw) != 0 && !bytes.Equal(raw, []byte("null")) && json.Unmarshal(raw, value) == nil
}

// v1 consumes complete messages and known foreground metadata, without partial
// messages, hooks or background work. The terminal result must agree with the
// final main assistant message; tool results, thinking, user echoes and result
// text alone cannot win.
func parseClaudeTurn(body []byte, expected string) (nativeTurn, error) {
	fail := func(code string) (nativeTurn, error) { return nativeTurn{}, submitFailure(code, "UNKNOWN") }
	var result nativeTurn
	var text strings.Builder
	messageID, assistantStop := "", ""
	turnID := ""
	initialized, completed, failed, toolUse := false, false, false, false
	scanner := bufio.NewScanner(bytes.NewReader(body))
	scanner.Buffer(make([]byte, 4096), 1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		// First inspect only the discriminator; the full exact-key decoder below
		// rejects aliases, unknown fields and duplicates before any event is used.
		var tag struct {
			Type    string `json:"type"`
			Subtype string `json:"subtype"`
		}
		if json.Unmarshal(line, &tag) != nil || completed {
			return fail("CLAUDE_STREAM_INVALID")
		}
		allowed := "type uuid session_id"
		switch tag.Type {
		case "system":
			fields, ok := claudeSystemFields(tag.Subtype)
			if !ok {
				return fail("CLAUDE_SYSTEM_UNKNOWN")
			}
			allowed += " subtype " + fields
		case "assistant":
			allowed += " message parent_tool_use_id error aborted agent_id timestamp context_usage usage_report user_message_uuid user_message_uuids resume_reason request_id thinking_display thinking_duration_ms"
		case "user":
			allowed += " message parent_tool_use_id agent_id isSynthetic isReplay client_composed tool_use_result origin"
		case "rate_limit_event":
			allowed += " rate_limit_info"
		case "tool_progress":
			allowed += " tool_use_id tool_name parent_tool_use_id elapsed_time_seconds task_id heartbeat subagent_type subagent_retry"
		case "result":
			allowed += " subtype is_error result stop_reason errors startup_failure_reason duration_ms duration_api_ms api_error_status num_turns total_cost_usd usage modelUsage permission_denials structured_output terminal_reason result_index queued_turn_count fast_mode_state fast_mode_disabled_reason origin user_message_uuid user_message_uuids resume_reason local_command ttft_ms ttft_stream_ms time_to_request_ms request_sent_wall_ms first_content_frame_ms first_stream_post_ms first_stream_post_ack_ms first_stream_post_queue_wait_ms first_stream_post_queued_behind first_stream_post_wall_ms first_text_post_ms first_text_post_queue_wait_ms first_text_post_queued_behind first_text_post_wall_ms deferred_tool_use subagent_stats safety_stops model_usage_since_last_result credential_expires_at"
		default:
			return fail("CLAUDE_EVENT_UNKNOWN")
		}
		e, err := claudeObject(line, allowed)
		var kind, session, uuid string
		if err != nil || !claudeValue(e, "type", &kind) || kind != tag.Type ||
			!claudeValue(e, "session_id", &session) || !claudeSessionID(session) ||
			!claudeValue(e, "uuid", &uuid) || !identityText(uuid) ||
			(expected != "" && session != expected) || (result.session != "" && session != result.session) {
			return fail("CLAUDE_IDENTITY_OR_EVENT_INVALID")
		}
		if kind == "system" {
			var subtype string
			if !claudeValue(e, "subtype", &subtype) || subtype != tag.Subtype {
				return fail("CLAUDE_INIT_INVALID")
			}
			if subtype == "init" {
				if initialized {
					return fail("CLAUDE_INIT_INVALID")
				}
				result.session, initialized = session, true
			} else if !initialized || !claudeSystemMetadata(e, subtype) {
				return fail("CLAUDE_SYSTEM_INVALID")
			}
			continue
		}
		if !initialized {
			return fail("CLAUDE_INIT_MISSING")
		}
		switch kind {
		case "tool_progress":
			if !claudeToolProgress(e) {
				return fail("CLAUDE_TOOL_PROGRESS_INVALID")
			}
		case "rate_limit_event":
			info, err := claudeObject(e["rate_limit_info"], "status resetsAt utilization errorCode canUserPurchaseCredits hasChargeableSavedPaymentMethod rateLimitType overageStatus overageDisabledReason isUsingOverage unifiedWindows")
			var status string
			if err != nil || !claudeValue(info, "status", &status) || (status != "allowed" && status != "allowed_warning" && status != "rejected") {
				return fail("CLAUDE_RATE_LIMIT_INVALID")
			}
			// A native retry can still succeed. This event never supplies final
			// text or completion; only the terminal result decides the outcome.
		case "assistant", "user":
			main, valid := claudeMainMessage(e)
			if !valid {
				return fail("CLAUDE_MESSAGE_ATTRIBUTION_INVALID")
			}
			m, err := claudeObject(e["message"], "id type role content model stop_reason stop_sequence usage container context_management stop_details input_transformations diagnostics")
			var role string
			if err != nil || !claudeValue(m, "role", &role) || role != kind {
				return fail("CLAUDE_MESSAGE_INVALID")
			}
			if kind == "user" || !main {
				// Tool results may contain arbitrary nested content. They never
				// establish an assistant candidate or a completion event.
				if len(m["content"]) == 0 {
					return fail("CLAUDE_USER_CONTENT_MISSING")
				}
				continue
			}
			// Command reports are internal assistant messages, not model answers.
			if len(e["context_usage"]) != 0 || len(e["usage_report"]) != 0 {
				continue
			}
			if !claudeTurnIdentity(e, &turnID) {
				return fail("CLAUDE_TURN_IDENTITY_INVALID")
			}
			var id, messageType string
			if !claudeValue(m, "id", &id) || !identityText(id) || !claudeValue(m, "type", &messageType) || messageType != "message" {
				return fail("CLAUDE_ASSISTANT_ID_INVALID")
			}
			if id != messageID {
				text.Reset()
				toolUse, assistantStop = false, ""
				messageID = id
			}
			if raw := m["stop_reason"]; len(raw) != 0 && !bytes.Equal(raw, []byte("null")) {
				if !claudeValue(m, "stop_reason", &assistantStop) {
					return fail("CLAUDE_ASSISTANT_STOP_INVALID")
				}
			}
			if raw := e["error"]; len(raw) != 0 {
				var messageError string
				if !claudeValue(e, "error", &messageError) || !claudeErrorCategory(messageError) {
					return fail("CLAUDE_ASSISTANT_ERROR_INVALID")
				}
				failed = true
			}
			if raw := e["aborted"]; len(raw) != 0 {
				var aborted bool
				if !claudeValue(e, "aborted", &aborted) || aborted {
					return fail("CLAUDE_ASSISTANT_ABORTED")
				}
			}
			var blocks []json.RawMessage
			if !claudeValue(m, "content", &blocks) || len(blocks) == 0 {
				return fail("CLAUDE_ASSISTANT_CONTENT_INVALID")
			}
			for _, raw := range blocks {
				b, err := claudeObject(raw, "type text citations thinking signature data id name input")
				var blockType string
				if err != nil || !claudeValue(b, "type", &blockType) {
					return fail("CLAUDE_CONTENT_BLOCK_INVALID")
				}
				switch blockType {
				case "text":
					b, err = claudeObject(raw, "type text citations")
					var part string
					if err != nil || !claudeValue(b, "text", &part) {
						return fail("CLAUDE_TEXT_INVALID")
					}
					text.WriteString(part)
				case "tool_use":
					b, err = claudeObject(raw, "type id name input")
					var id, name string
					var input map[string]json.RawMessage
					if err != nil || !claudeValue(b, "id", &id) || !identityText(id) || !claudeValue(b, "name", &name) || !identityText(name) || !claudeValue(b, "input", &input) {
						return fail("CLAUDE_TOOL_USE_INVALID")
					}
					toolUse = true
				case "thinking":
					b, err = claudeObject(raw, "type thinking signature")
					var thinking string
					if err != nil || !claudeValue(b, "thinking", &thinking) {
						return fail("CLAUDE_THINKING_INVALID")
					}
				case "redacted_thinking":
					b, err = claudeObject(raw, "type data")
					var data string
					if err != nil || !claudeValue(b, "data", &data) {
						return fail("CLAUDE_THINKING_INVALID")
					}
					// Deliberately excluded from final text.
				default:
					return fail("CLAUDE_CONTENT_BLOCK_UNKNOWN")
				}
			}
		case "result":
			// A result from an injected follow-up cannot finish this stdin turn.
			if !claudeMainResult(e) || !claudeTurnIdentity(e, &turnID) {
				return fail("CLAUDE_RESULT_TURN_INVALID")
			}
			var subtype string
			var isError bool
			if !claudeValue(e, "subtype", &subtype) || !claudeValue(e, "is_error", &isError) {
				return fail("CLAUDE_RESULT_INVALID")
			}
			reason := "completed"
			if len(e["terminal_reason"]) != 0 && (!claudeValue(e, "terminal_reason", &reason) || !claudeTerminalReason(reason)) {
				return fail("CLAUDE_TERMINAL_REASON_UNKNOWN")
			}
			if len(e["result_index"]) != 0 {
				var index int
				if !claudeValue(e, "result_index", &index) || index != 0 {
					return fail("CLAUDE_RESULT_INDEX_INVALID")
				}
			}
			if turnID != "" && len(e["user_message_uuid"]) == 0 {
				return fail("CLAUDE_RESULT_TURN_INVALID")
			}
			switch subtype {
			case "error_during_execution", "error_max_turns", "error_max_budget_usd", "error_max_structured_output_retries":
				if !isError {
					return fail("CLAUDE_FAILURE_CONTRADICTED")
				}
				failed = true
			case "success":
				if isError || reason != "completed" {
					failed = true
					break
				}
				var final, stop string
				var turns int
				if !claudeValue(e, "num_turns", &turns) || turns < 1 {
					return fail("CLAUDE_TURNS_MISSING")
				}
				if raw := e["api_error_status"]; len(raw) != 0 && !bytes.Equal(raw, []byte("null")) {
					return fail("CLAUDE_API_ERROR_ON_SUCCESS")
				}
				if failed || messageID == "" || toolUse || !identityText(text.String()) ||
					!claudeValue(e, "result", &final) || final != text.String() ||
					!claudeValue(e, "stop_reason", &stop) || stop != "end_turn" ||
					(assistantStop != "" && assistantStop != "end_turn") || len(e["errors"]) != 0 ||
					len(e["structured_output"]) != 0 || len(e["deferred_tool_use"]) != 0 ||
					len(e["local_command"]) != 0 || len(e["startup_failure_reason"]) != 0 {
					return fail("CLAUDE_FINAL_UNVERIFIED")
				}
				result.text = final
			default:
				return fail("CLAUDE_RESULT_UNKNOWN")
			}
			completed = true
		}
	}
	if scanner.Err() != nil || !initialized || !completed {
		return fail("CLAUDE_RESULT_MISSING")
	}
	if failed {
		return nativeTurn{}, submitFailure("CLAUDE_TURN_FAILED", "FAILED")
	}
	return result, nil
}
