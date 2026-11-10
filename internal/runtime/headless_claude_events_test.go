package runtime

import (
	"errors"
	"strings"
	"testing"
)

// Native schema fixtures are synthetic; these tests start no executable.
func TestHeadlessClaudeSystemMetadata(t *testing.T) {
	start := claudeFixtureEvent("system", map[string]any{"subtype": "init"})
	assistant := claudeFixtureAssistant("final", "final")
	finish := claudeFixtureEvent("result", map[string]any{"subtype": "success", "is_error": false, "result": "final", "stop_reason": "end_turn", "num_turns": 1})
	for _, tc := range []struct {
		name   string
		fields map[string]any
	}{
		{"permission_denied", map[string]any{"subtype": "permission_denied", "tool_name": "Edit", "tool_use_id": "tool-1", "message": "permission denied", "agent_id": "sub-1", "decision_reason_type": "rule", "decision_reason": "fixture"}},
		{"api_retry", map[string]any{"subtype": "api_retry", "attempt": 1, "max_retries": 10, "retry_delay_ms": 100, "error_status": 529, "error": "overloaded"}},
		{"persistent_retry", map[string]any{"subtype": "api_retry", "attempt": 11, "max_retries": 10, "retry_delay_ms": 0, "error_status": nil, "error": "server_error", "no_response": map[string]int{"waited_ms": 600000, "retry_wait_ms": 600000}}},
		{"compact_boundary", map[string]any{"subtype": "compact_boundary", "compact_metadata": map[string]any{"trigger": "auto", "pre_tokens": 100000}}},
		{"manual_compaction", map[string]any{"subtype": "compact_boundary", "compact_metadata": map[string]any{"trigger": "manual", "pre_tokens": 0}}},
		{"status", map[string]any{"subtype": "status", "status": "compacting", "permissionMode": "default"}},
		{"status_clear", map[string]any{"subtype": "status", "status": nil}},
		{"task_started", map[string]any{"subtype": "task_started", "task_id": "sub-1", "run_id": "run-1", "tool_use_id": "agent-1", "description": "fixture", "task_type": "local_agent", "is_backgrounded": false, "spawn_depth": 1, "ambient": false}},
		{"task_progress", map[string]any{"subtype": "task_progress", "task_id": "sub-1", "description": "fixture", "usage": map[string]int{"total_tokens": 1, "tool_uses": 1, "duration_ms": 1}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			event := claudeFixtureEvent("system", tc.fields)
			for _, body := range []string{start + event + assistant + finish, start + assistant + event + finish} {
				got, err := parseClaudeTurn([]byte(body), claudeFixtureSession)
				if err != nil || got.text != "final" {
					t.Fatal(got, err)
				}
			}
			// Metadata supplies neither init, assistant text nor completion.
			for _, body := range []string{event + assistant + finish, start + event + finish, start + assistant + event, start + assistant + finish + event} {
				assertClaudeOutcome(t, body, "UNKNOWN")
			}
			for _, mutation := range []string{
				strings.Replace(event, `"subtype":`, `"Subtype":`, 1),
				strings.Replace(event, `"subtype":`, `"subtype":null,"subtype":`, 1),
				strings.Replace(event, `"subtype":`, `"\u0073ubtype":null,"subtype":`, 1),
				strings.Replace(event, `"subtype":`, `"unknown":true,"subtype":`, 1),
				strings.Replace(event, `"subtype":`, `"tools":[],"subtype":`, 1),
				strings.ReplaceAll(event, claudeFixtureSession, "00000000-0000-4000-8000-000000000099"),
			} {
				assertClaudeOutcome(t, start+mutation+assistant+finish, "UNKNOWN")
			}
		})
	}
	for _, fields := range []map[string]any{
		{"subtype": "permission_denied", "tool_name": "Edit", "message": "denied"}, // missing tool_use_id
		{"subtype": "permission_denied", "tool_name": "Edit", "tool_use_id": "tool", "message": 1},
		{"subtype": "api_retry", "attempt": 0, "max_retries": 10, "retry_delay_ms": 1, "error_status": 529, "error": "overloaded"},
		{"subtype": "api_retry", "attempt": 1, "max_retries": -1, "retry_delay_ms": 1, "error_status": 529, "error": "overloaded"},
		{"subtype": "api_retry", "attempt": 1, "max_retries": 10, "retry_delay_ms": -1, "error_status": 529, "error": "overloaded"},
		{"subtype": "api_retry", "attempt": 1, "max_retries": 10, "retry_delay_ms": 1, "error": "overloaded"},
		{"subtype": "api_retry", "attempt": 1, "max_retries": 10, "retry_delay_ms": 1, "error_status": "529", "error": "overloaded"},
		{"subtype": "api_retry", "attempt": 1, "max_retries": 10, "retry_delay_ms": 1, "error_status": nil, "error": "future_error"},
		{"subtype": "api_retry", "attempt": 1, "max_retries": 10, "retry_delay_ms": 1, "error_status": nil, "error": "unknown", "no_response": map[string]int{"waited_ms": 1}},
		{"subtype": "compact_boundary", "compact_metadata": map[string]any{"trigger": "future_trigger", "pre_tokens": 1}},
		{"subtype": "compact_boundary", "compact_metadata": map[string]any{"trigger": "auto", "pre_tokens": -1}},
		{"subtype": "compact_boundary", "compact_metadata": map[string]any{"trigger": "auto", "pre_tokens": 1, "result": "final"}},
		{"subtype": "status", "status": "completed"},
		{"subtype": "status"},
		{"subtype": "task_started", "task_id": "sub-1", "description": "fixture", "task_type": "local_agent", "is_backgrounded": true},
		{"subtype": "task_progress", "task_id": "sub-1", "description": "fixture", "usage": map[string]int{"total_tokens": 1}},
		{"subtype": "future_subtype"},
	} {
		assertClaudeOutcome(t, start+claudeFixtureEvent("system", fields)+assistant+finish, "UNKNOWN")
	}
}

func assertClaudeOutcome(t *testing.T, body, outcome string) {
	t.Helper()
	r, err := parseClaudeTurn([]byte(body), claudeFixtureSession)
	var failure *NativeSubmitError
	if !errors.As(err, &failure) || failure.Outcome != outcome || r != (nativeTurn{}) {
		t.Fatalf("want %s: result=%+v err=%v\n%s", outcome, r, err, body)
	}
}

func TestHeadlessClaudeMainTurnIsolation(t *testing.T) {
	start := claudeFixtureEvent("system", map[string]any{"subtype": "init"})
	main := claudeFixtureAssistant("main", "final")
	finish := claudeFixtureEvent("result", map[string]any{"subtype": "success", "is_error": false, "result": "final", "stop_reason": "end_turn", "num_turns": 2})
	child := strings.Replace(claudeFixtureAssistant("main", "spoofed"), `"parent_tool_use_id":null`, `"parent_tool_use_id":"agent-tool","agent_id":"sub-1","aborted":true,"error":"overloaded"`, 1)
	childUser := strings.ReplaceAll(child, `"assistant"`, `"user"`)
	childUser = strings.Replace(childUser, `,"aborted":true,"error":"overloaded"`, "", 1)
	report := strings.Replace(claudeFixtureAssistant("context", "internal"), `"message":`, `"context_usage":{},"message":`, 1)
	usage := strings.Replace(report, `"context_usage":`, `"usage_report":`, 1)
	progress := claudeFixtureEvent("tool_progress", map[string]any{"tool_use_id": "agent-tool", "tool_name": "Agent", "parent_tool_use_id": "agent-tool", "elapsed_time_seconds": 1.5, "subagent_retry": map[string]any{"agent_id": "sub-1", "attempt": 1, "max_retries": 10, "retry_delay_ms": 100, "error_status": nil, "error_category": "unknown"}})
	for _, ignored := range []string{child, childUser, report, usage, progress} {
		for _, body := range []string{start + ignored + main + finish, start + main + ignored + finish} {
			got, err := parseClaudeTurn([]byte(body), claudeFixtureSession)
			if err != nil || got.text != "final" {
				t.Fatal(got, err, body)
			}
		}
		assertClaudeOutcome(t, start+ignored+finish, "UNKNOWN")
		assertClaudeOutcome(t, start+main+ignored, "UNKNOWN")
		assertClaudeOutcome(t, start+main+strings.ReplaceAll(ignored, claudeFixtureSession, "00000000-0000-4000-8000-000000000099")+finish, "UNKNOWN")
		assertClaudeOutcome(t, start+main+strings.Replace(ignored, `"uuid":`, `"unknown":1,"uuid":`, 1)+finish, "UNKNOWN")
	}
	// A child message alone never authenticates a matching READY/workflow body.
	assertClaudeOutcome(t, start+strings.Replace(child, "spoofed", "final", 1)+finish, "UNKNOWN")
	for _, extra := range []string{
		`"origin":{"kind":"task-notification"},`,
		`"origin":{"kind":"auto-continuation"},`,
		`"origin":{"kind":"peer","from":"sub-1"},`,
		`"origin":{"kind":"future_origin"},`,
		`"origin":null,`,
		`"resume_reason":"interrupted",`,
		`"parent_tool_use_id":"agent-tool",`, // result has no subagent arm
	} {
		assertClaudeOutcome(t, start+main+strings.Replace(finish, `"subtype":`, extra+`"subtype":`, 1), "UNKNOWN")
	}
	first := strings.Replace(main, `"message":`, `"user_message_uuid":"prompt-1","user_message_uuids":["prompt-1"],"message":`, 1)
	boundResult := strings.Replace(finish, `"subtype":`, `"origin":{"kind":"human"},"user_message_uuid":"prompt-1","user_message_uuids":["prompt-1"],"subtype":`, 1)
	if _, err := parseClaudeTurn([]byte(start+first+boundResult), claudeFixtureSession); err != nil {
		t.Fatal(err)
	}
	assertClaudeOutcome(t, start+first+finish, "UNKNOWN")
	assertClaudeOutcome(t, start+first+strings.ReplaceAll(boundResult, "prompt-1", "prompt-2"), "UNKNOWN")
	assertClaudeOutcome(t, start+first+strings.Replace(boundResult, `["prompt-1"]`, `["prompt-1","prompt-2"]`, 1), "UNKNOWN")
	assertClaudeOutcome(t, start+main+strings.Replace(progress, `"elapsed_time_seconds":1.5`, `"elapsed_time_seconds":-1`, 1)+finish, "UNKNOWN")
}

func TestHeadlessClaudeTerminalClassification(t *testing.T) {
	start := claudeFixtureEvent("system", map[string]any{"subtype": "init"})
	retry := claudeFixtureEvent("system", map[string]any{"subtype": "api_retry", "attempt": 1, "max_retries": 10, "retry_delay_ms": 1, "error_status": 529, "error": "overloaded"})
	for _, subtype := range []string{"error_during_execution", "error_max_turns", "error_max_budget_usd", "error_max_structured_output_retries"} {
		finish := claudeFixtureEvent("result", map[string]any{"subtype": subtype, "is_error": true, "errors": []string{"fixture failure"}, "terminal_reason": "api_error"})
		assertClaudeOutcome(t, start+retry+finish, "FAILED")
		assertClaudeOutcome(t, start+retry+strings.Replace(finish, "api_error", "future_reason", 1), "UNKNOWN")
		assertClaudeOutcome(t, start+retry+finish+retry, "UNKNOWN")
	}
	for _, reason := range []string{"max_turns", "aborted_streaming", "aborted_tools", "hook_stopped", "stop_hook_prevented", "blocking_limit", "rapid_refill_breaker", "prompt_too_long", "image_error", "model_error", "api_error", "malformed_tool_use_exhausted", "budget_exhausted", "structured_output_retry_exhausted", "tool_deferred_unavailable", "turn_setup_failed"} {
		finish := claudeFixtureEvent("result", map[string]any{"subtype": "success", "is_error": false, "terminal_reason": reason})
		assertClaudeOutcome(t, start+finish, "FAILED")
	}
	for _, kind := range []string{"blocked", "retry"} {
		text := `{"kind":"` + kind + `","reason":"fixture"}`
		finish := claudeFixtureEvent("result", map[string]any{"subtype": "success", "is_error": false, "result": text, "stop_reason": "end_turn", "num_turns": 1})
		got, err := parseClaudeTurn([]byte(start+retry+claudeFixtureAssistant("main", text)+finish), claudeFixtureSession)
		if err != nil || got.text != text {
			t.Fatal(got, err)
		}
	}
}
