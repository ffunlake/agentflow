// A native executable fixture for Codex, OpenCode and Claude JSONL. Never starts a model.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

func emit(v any) { _ = json.NewEncoder(os.Stdout).Encode(v) }

func openCodeEvent(kind, session, message, text, reason string) {
	partType := strings.ReplaceAll(kind, "_", "-")
	emit(map[string]any{"type": kind, "sessionID": session, "part": map[string]string{
		"type": partType, "sessionID": session, "messageID": message, "text": text, "reason": reason,
	}})
}

func claudeEvent(kind, session string, fields map[string]any) {
	fields["type"], fields["session_id"], fields["uuid"] = kind, session, "fixture-event"
	emit(fields)
}

func claudeTurn(session, text, mode string, exit bool) {
	claudeEvent("system", session, map[string]any{"subtype": "init", "claude_code_version": "2.1.318", "tools": []string{}, "capabilities": []string{}})
	claudeEvent("rate_limit_event", session, map[string]any{"rate_limit_info": map[string]any{"status": "allowed"}})
	if mode == "recovered-retry" || mode == "retry-failed" {
		claudeEvent("system", session, map[string]any{"subtype": "api_retry", "attempt": 1, "max_retries": 10, "retry_delay_ms": 100, "error_status": 529, "error": "overloaded"})
	}
	if mode == "permission-denied-blocked" || mode == "permission-denied-retry" {
		claudeEvent("system", session, map[string]any{"subtype": "permission_denied", "tool_name": "Edit", "tool_use_id": "tool-1", "message": "fixture permission denied"})
	}
	if mode == "compaction" {
		claudeEvent("system", session, map[string]any{"subtype": "status", "status": "compacting"})
		claudeEvent("system", session, map[string]any{"subtype": "compact_boundary", "compact_metadata": map[string]any{"trigger": "auto", "pre_tokens": 100000}})
		claudeEvent("system", session, map[string]any{"subtype": "status", "status": nil})
	}
	if mode == "foreground-subagent" {
		claudeEvent("system", session, map[string]any{"subtype": "task_started", "task_id": "sub-1", "description": "fixture child", "task_type": "local_agent", "is_backgrounded": false})
		claudeEvent("assistant", session, map[string]any{"parent_tool_use_id": "agent-tool", "agent_id": "sub-1", "error": "overloaded", "message": map[string]any{"role": "assistant", "content": []any{map[string]string{"type": "text", "text": "untrusted child result"}}}})
	}
	switch mode {
	case "failed", "retry-failed":
		claudeEvent("result", session, map[string]any{"subtype": "error_during_execution", "is_error": true, "errors": []string{"fixture failure"}})
		os.Exit(1)
	case "unknown":
		return
	case "malformed":
		fmt.Println("not JSONL")
		return
	}
	claudeEvent("assistant", session, map[string]any{"parent_tool_use_id": nil, "message": map[string]any{
		"id": "intermediate", "type": "message", "role": "assistant", "stop_reason": "tool_use", "content": []any{
			map[string]string{"type": "text", "text": "intermediate commentary"},
			map[string]any{"type": "tool_use", "id": "tool-1", "name": "Read", "input": map[string]string{"path": "fixture-only"}},
		},
	}})
	claudeEvent("user", session, map[string]any{"parent_tool_use_id": nil, "message": map[string]any{
		"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": "tool-1", "content": "untrusted tool text"}},
	}})
	claudeEvent("assistant", session, map[string]any{"parent_tool_use_id": nil, "message": map[string]any{
		"id": "final", "type": "message", "role": "assistant", "stop_reason": nil, "model": "fixture-only", "usage": map[string]int{},
		"content": []any{map[string]string{"type": "thinking", "thinking": "not final", "signature": "fixture"},
			map[string]string{"type": "text", "text": text[:len(text)/2]}, map[string]string{"type": "text", "text": text[len(text)/2:]}},
	}})
	if mode == "foreground-subagent" {
		// Late child/internal traffic must neither replace the main candidate nor
		// mark the main turn failed, even when it arrives after the final text.
		claudeEvent("system", session, map[string]any{"subtype": "task_progress", "task_id": "sub-1", "description": "fixture child", "usage": map[string]int{"total_tokens": 1, "tool_uses": 1, "duration_ms": 1}})
		claudeEvent("tool_progress", session, map[string]any{"tool_use_id": "agent-tool", "tool_name": "Agent", "parent_tool_use_id": nil, "elapsed_time_seconds": 1})
		claudeEvent("assistant", session, map[string]any{"parent_tool_use_id": "agent-tool", "agent_id": "sub-1", "aborted": true, "message": map[string]any{"role": "assistant", "content": []any{map[string]string{"type": "text", "text": "late child"}}}})
		claudeEvent("assistant", session, map[string]any{"parent_tool_use_id": nil, "context_usage": map[string]any{}, "message": map[string]any{"role": "assistant", "content": []any{map[string]string{"type": "text", "text": "internal context report"}}}})
	}
	if mode == "no-finish" {
		return
	}
	stop, finalSession := "end_turn", session
	if mode == "unknown-finish" {
		stop = "max_tokens"
	}
	if mode == "wrong-result-session" {
		finalSession = "00000000-0000-4000-8000-000000000099"
	}
	denials := []any{}
	if mode == "permission-denied-blocked" || mode == "permission-denied-retry" {
		denials = append(denials, map[string]any{"tool_name": "Edit", "tool_use_id": "tool-1", "tool_input": map[string]any{}})
	}
	claudeEvent("result", finalSession, map[string]any{"subtype": "success", "is_error": false, "num_turns": 2, "stop_reason": stop,
		"result": text, "terminal_reason": "completed", "result_index": 0, "duration_ms": 1, "total_cost_usd": 0, "permission_denials": denials})
	if mode == "after-finish" {
		claudeEvent("user", session, map[string]any{})
	}
	if exit {
		os.Exit(1)
	}
}

func main() {
	body, _ := io.ReadAll(os.Stdin)
	f, err := os.OpenFile("calls.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		panic(err)
	}
	_ = json.NewEncoder(f).Encode(os.Args[1:])
	_ = f.Close()
	thread := ""
	openCode := len(os.Args) > 1 && os.Args[1] == "run"
	claude := len(os.Args) > 1 && os.Args[1] == "--print"
	for i, arg := range os.Args {
		if (arg == "resume" || arg == "--session" || arg == "--resume") && i+1 < len(os.Args) {
			thread = os.Args[i+1]
		}
	}
	text := ""
	exit := false
	mode := ""
	if thread == "" {
		start := strings.LastIndex(string(body), "\n{")
		var ready struct {
			Actor struct {
				AgentID string `json:"agentId"`
			} `json:"actor"`
		}
		text = strings.TrimSpace(string(body[start+1:]))
		if json.Unmarshal([]byte(text), &ready) != nil {
			panic("bad startup")
		}
		thread = "fixture-thread-" + ready.Actor.AgentID
		if claude {
			thread = "00000000-0000-4000-8000-000000000001"
			if ready.Actor.AgentID == "main" {
				thread = "00000000-0000-4000-8000-000000000002"
			}
		}
		startupMode, _ := os.ReadFile("startup-mode")
		mode = strings.TrimSpace(string(startupMode))
		switch mode {
		case "wrong-nonce":
			var challenge map[string]any
			_ = json.Unmarshal([]byte(text), &challenge)
			challenge["nonce"] = "wrong"
			b, _ := json.Marshal(challenge)
			text = string(b)
		case "wrong-generation":
			var challenge map[string]any
			_ = json.Unmarshal([]byte(text), &challenge)
			challenge["actor"].(map[string]any)["generation"] = 2
			b, _ := json.Marshal(challenge)
			text = string(b)
		}
	} else {
		var intent struct {
			Purpose    string   `json:"purpose"`
			Acceptance []string `json:"acceptance"`
		}
		_ = json.NewDecoder(strings.NewReader(string(body))).Decode(&intent)
		modeBytes, _ := os.ReadFile("mode")
		mode = strings.TrimSpace(string(modeBytes))
		switch mode {
		case "wait":
			time.Sleep(time.Minute)
			return
		case "exit":
			exit = true
		case "failed":
			if claude {
				claudeTurn(thread, "", mode, false)
				return
			}
			if openCode {
				emit(map[string]string{"type": "error", "sessionID": thread})
				os.Exit(1)
			}
			emit(map[string]any{"type": "thread.started", "thread_id": thread})
			emit(map[string]any{"type": "turn.failed", "error": map[string]string{"message": "fixture failure"}})
			os.Exit(1)
		case "unknown":
			if claude {
				claudeTurn(thread, "", mode, false)
				return
			}
			if openCode {
				openCodeEvent("step_start", thread, "msg", "", "")
				return
			}
			emit(map[string]any{"type": "thread.started", "thread_id": thread})
			return
		case "wrong-thread":
			thread = "wrong-thread"
		case "malformed":
			fmt.Println("not JSONL")
			return
		case "blocked", "permission-denied-blocked":
			text = `{"kind":"blocked","reason":"fixture needs decision"}`
		case "retry", "permission-denied-retry":
			text = `{"kind":"retry","reason":"fixture verification failed"}`
		case "changes":
			if intent.Purpose == "review" {
				text = `{"kind":"changes","reason":"fixture needs changes"}`
			}
		case "invalid-result":
			text = "DONE"
		case "duplicate-result":
			text = `{"kind":"handoff","kind":"approve","accepted":[true]}`
		case "case-result":
			text = `{"Kind":"approve","accepted":[true]}`
		case "false-review":
			if intent.Purpose == "review" {
				text = `{"kind":"approve","accepted":[false]}`
			}
		}
		if text == "" {
			if intent.Purpose == "review" {
				accepted := make([]bool, len(intent.Acceptance))
				for i := range accepted {
					accepted[i] = true
				}
				v, _ := json.Marshal(map[string]any{"kind": "approve", "accepted": accepted})
				text = string(v)
			} else {
				text = `{"kind":"handoff","handoff":{"summary":"fixture work","evidence":["fixture-only"],"next":"review"}}`
			}
		}
	}
	if claude {
		claudeTurn(thread, text, mode, exit)
		return
	}
	if openCode {
		switch mode {
		case "failed":
			emit(map[string]string{"type": "error", "sessionID": thread})
			os.Exit(1)
		case "malformed":
			fmt.Println("not JSONL")
			return
		case "unknown":
			openCodeEvent("step_start", thread, "msg", "", "")
			return
		}
		// Intermediate tool-call step must never be parsed as the final answer.
		openCodeEvent("step_start", thread, "intermediate", "", "")
		openCodeEvent("text", thread, "intermediate", "fixture intermediate commentary", "")
		openCodeEvent("step_finish", thread, "intermediate", "", "tool-calls")
		openCodeEvent("step_start", thread, "final", "", "")
		// A final answer can contain multiple text parts.
		mid := len(text) / 2
		openCodeEvent("text", thread, "final", text[:mid], "")
		openCodeEvent("text", thread, "final", text[mid:], "")
		switch mode {
		case "no-finish":
			return
		case "wrong-part-session":
			emit(map[string]any{"type": "step_finish", "sessionID": thread, "part": map[string]string{"type": "step-finish", "sessionID": "wrong", "messageID": "final", "reason": "stop"}})
			return
		case "unknown-finish":
			openCodeEvent("step_finish", thread, "final", "", "length")
			return
		}
		openCodeEvent("step_finish", thread, "final", "", "stop")
		if mode == "after-finish" {
			openCodeEvent("text", thread, "final", "extra", "")
		}
		if exit {
			os.Exit(1)
		}
		return
	}
	emit(map[string]any{"type": "thread.started", "thread_id": thread})
	emit(map[string]any{"type": "turn.started"})
	emit(map[string]any{"type": "item.completed", "item": map[string]string{"type": "agent_message", "text": "fixture commentary before final response"}})
	emit(map[string]any{"type": "item.completed", "item": map[string]string{"type": "agent_message", "text": text}})
	emit(map[string]any{"type": "turn.completed", "usage": map[string]int{"input_tokens": 1, "output_tokens": 1}})
	if exit {
		os.Exit(1)
	}
}
