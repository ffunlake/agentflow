package runtime

import (
	"bytes"
	"encoding/json"
	"math"
)

// These are separate exact schemas: init keys cannot disguise a retry or denial.
// See the SDK message reference and headless API-retry schema. All of these
// events are metadata; none can supply an answer or complete a native turn.
func claudeSystemFields(subtype string) (string, bool) {
	switch subtype {
	case "init":
		return "agents apiKeySource betas claude_code_version cwd tools mcp_servers model permissionMode slash_commands terminal_slash_commands output_style skills plugins plugin_errors mcp_server_errors fast_mode_state fast_mode_disabled_reason effort capabilities analytics_disabled product_feedback_disabled original_cwd additional_directories messaging_socket_path per_turn_effort_active ultrathink_available ultrathink_typed_raises view_mode", true
	case "permission_denied":
		return "tool_name tool_use_id agent_id decision_reason_type decision_reason message", true
	case "api_retry":
		return "attempt max_retries retry_delay_ms error_status no_response error", true
	case "compact_boundary":
		return "compact_metadata", true
	case "status":
		return "status permissionMode", true
	case "task_started":
		return "task_id run_id tool_use_id description task_type is_backgrounded spawn_depth parent_task_id ambient", true
	case "task_progress":
		return "task_id run_id tool_use_id description subagent_type usage last_tool_name summary", true
	default:
		return "", false
	}
}

func claudeStrings(e map[string]json.RawMessage, required, optional []string) bool {
	for _, key := range required {
		var value string
		if !claudeValue(e, key, &value) || !identityText(value) {
			return false
		}
	}
	for _, key := range optional {
		var value string
		if len(e[key]) != 0 && !claudeValue(e, key, &value) {
			return false
		}
	}
	return true
}

func claudeCount(e map[string]json.RawMessage, key string, minimum int) bool {
	var value int
	return claudeValue(e, key, &value) && value >= minimum
}

func claudeErrorCategory(value string) bool {
	switch value {
	case "authentication_failed", "oauth_org_not_allowed", "account_on_hold", "billing_error", "rate_limit", "overloaded", "invalid_request", "model_not_found", "server_error", "max_output_tokens", "cloud_credential_error", "unknown":
		return true
	}
	return false
}

func claudeRetry(e map[string]json.RawMessage, categoryKey string) bool {
	var category string
	if !claudeCount(e, "attempt", 1) || !claudeCount(e, "max_retries", 0) || !claudeCount(e, "retry_delay_ms", 0) ||
		!claudeValue(e, categoryKey, &category) || !claudeErrorCategory(category) {
		return false
	}
	// Persistent retries may exceed max_retries. No HTTP response has a null
	// status; absence or a non-HTTP number is not the documented schema.
	if !bytes.Equal(e["error_status"], []byte("null")) {
		var status int
		if !claudeValue(e, "error_status", &status) || status < 100 || status > 599 {
			return false
		}
	}
	if raw := e["no_response"]; len(raw) != 0 {
		n, err := claudeObject(raw, "waited_ms retry_wait_ms")
		if err != nil || !bytes.Equal(e["error_status"], []byte("null")) || !claudeCount(n, "waited_ms", 0) || !claudeCount(n, "retry_wait_ms", 0) {
			return false
		}
	}
	return true
}

func claudeSystemMetadata(e map[string]json.RawMessage, subtype string) bool {
	switch subtype {
	case "permission_denied":
		return claudeStrings(e, []string{"tool_name", "tool_use_id", "message"}, []string{"agent_id", "decision_reason_type", "decision_reason"})
	case "api_retry":
		return claudeRetry(e, "error")
	case "compact_boundary":
		m, err := claudeObject(e["compact_metadata"], "trigger pre_tokens")
		var trigger string
		return err == nil && claudeValue(m, "trigger", &trigger) && (trigger == "auto" || trigger == "manual") && claudeCount(m, "pre_tokens", 0)
	case "status":
		var status, mode string
		if !bytes.Equal(e["status"], []byte("null")) && (!claudeValue(e, "status", &status) || status != "compacting") {
			return false
		}
		if len(e["permissionMode"]) != 0 {
			if !claudeValue(e, "permissionMode", &mode) {
				return false
			}
			switch mode {
			case "default", "acceptEdits", "bypassPermissions", "plan", "dontAsk", "auto":
			default:
				return false
			}
		}
		return true
	case "task_started", "task_progress":
		if !claudeStrings(e, []string{"task_id", "description"}, []string{"run_id", "tool_use_id", "parent_task_id", "subagent_type", "last_tool_name", "summary"}) {
			return false
		}
		if subtype == "task_progress" {
			u, err := claudeObject(e["usage"], "total_tokens tool_uses duration_ms")
			return err == nil && claudeCount(u, "total_tokens", 0) && claudeCount(u, "tool_uses", 0) && claudeCount(u, "duration_ms", 0)
		}
		var taskType string
		if !claudeValue(e, "task_type", &taskType) || taskType != "local_agent" {
			return false // Background/remote task protocols are outside bare v1.
		}
		for _, key := range []string{"is_backgrounded", "ambient"} {
			var value bool
			if len(e[key]) != 0 && (!claudeValue(e, key, &value) || value) {
				return false
			}
		}
		return len(e["spawn_depth"]) == 0 || claudeCount(e, "spawn_depth", 1)
	}
	return false
}

func claudeMainMessage(e map[string]json.RawMessage) (main, valid bool) {
	main = bytes.Equal(e["parent_tool_use_id"], []byte("null"))
	var parent, agent string
	if !main && (!claudeValue(e, "parent_tool_use_id", &parent) || !identityText(parent)) {
		return false, false
	}
	if len(e["agent_id"]) != 0 {
		if !claudeValue(e, "agent_id", &agent) || !identityText(agent) {
			return false, false
		}
		main = false
	}
	return main, true
}

func claudeToolProgress(e map[string]json.RawMessage) bool {
	_, valid := claudeMainMessage(e)
	var seconds float64
	if !valid || !claudeStrings(e, []string{"tool_use_id", "tool_name"}, []string{"task_id", "subagent_type"}) ||
		!claudeValue(e, "elapsed_time_seconds", &seconds) || seconds < 0 || math.IsInf(seconds, 0) {
		return false
	}
	var heartbeat bool
	if len(e["heartbeat"]) != 0 && !claudeValue(e, "heartbeat", &heartbeat) {
		return false
	}
	if raw := e["subagent_retry"]; len(raw) != 0 {
		r, err := claudeObject(raw, "agent_id attempt max_retries retry_delay_ms error_status error_category")
		return err == nil && claudeStrings(r, []string{"agent_id"}, nil) && claudeRetry(r, "error_category")
	}
	return true
}

// Text stdin supplies no prompt UUID. If native echoes one, bind it across the
// first main assistant and result. Multi-prompt and interrupted turns are outside
// this one-prompt adapter; metadata cannot silently select another turn.
func claudeTurnIdentity(e map[string]json.RawMessage, bound *string) bool {
	if len(e["resume_reason"]) != 0 {
		return false
	}
	var id string
	if len(e["user_message_uuid"]) != 0 {
		if !claudeValue(e, "user_message_uuid", &id) || !identityText(id) || (*bound != "" && *bound != id) {
			return false
		}
		*bound = id
	}
	if len(e["user_message_uuids"]) != 0 {
		var ids []string
		if !claudeValue(e, "user_message_uuids", &ids) || len(ids) != 1 || ids[0] != id || id == "" {
			return false
		}
	}
	return true
}

func claudeMainResult(e map[string]json.RawMessage) bool {
	if len(e["origin"]) == 0 {
		return true
	}
	o, err := claudeObject(e["origin"], "kind")
	var kind string
	return err == nil && claudeValue(o, "kind", &kind) && kind == "human"
}

func claudeTerminalReason(reason string) bool {
	switch reason {
	case "completed", "max_turns", "aborted_streaming", "aborted_tools", "hook_stopped", "stop_hook_prevented", "blocking_limit", "rapid_refill_breaker", "prompt_too_long", "image_error", "model_error", "api_error", "malformed_tool_use_exhausted", "budget_exhausted", "structured_output_retry_exhausted", "tool_deferred_unavailable", "turn_setup_failed":
		return true
	}
	return false // Deferred/background continuation needs a separate contract.
}
