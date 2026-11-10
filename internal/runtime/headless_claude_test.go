package runtime

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"agentflow/internal/config"
)

const claudeFixtureSession = "00000000-0000-4000-8000-000000000001"

func claudeFixtureEvent(kind string, fields map[string]any) string {
	fields["type"], fields["uuid"], fields["session_id"] = kind, "event-1", claudeFixtureSession
	b, _ := json.Marshal(fields)
	return string(b) + "\n"
}

func claudeFixtureAssistant(id, text string) string {
	return claudeFixtureEvent("assistant", map[string]any{"parent_tool_use_id": nil, "message": map[string]any{
		"id": id, "type": "message", "role": "assistant", "stop_reason": nil,
		"content": []any{map[string]string{"type": "text", "text": text}},
	}})
}

func TestHeadlessClaudeProtocol(t *testing.T) {
	start := claudeFixtureEvent("system", map[string]any{"subtype": "init", "claude_code_version": "2.1.318", "capabilities": []string{"unknown-metadata-capability"}})
	assistant := claudeFixtureAssistant("msg-final", "final")
	finish := claudeFixtureEvent("result", map[string]any{"subtype": "success", "is_error": false, "result": "final", "stop_reason": "end_turn", "num_turns": 1})
	valid := start + assistant + finish
	rateLimit := claudeFixtureEvent("rate_limit_event", map[string]any{"rate_limit_info": map[string]any{"status": "allowed_warning", "utilization": 0.9}})
	errorResult := claudeFixtureEvent("result", map[string]any{"subtype": "error_during_execution", "is_error": true, "errors": []string{"fixture failure"}})
	for _, tc := range []struct{ name, body, expected, outcome string }{
		{"success", valid, claudeFixtureSession, ""},
		{"discover", valid, "", ""},
		{"rate-limit-metadata", start + rateLimit + assistant + finish, "", ""},
		{"rate-limit-does-not-complete", start + rateLimit, "", "UNKNOWN"},
		{"unknown-rate-limit-status", start + strings.Replace(rateLimit, "allowed_warning", "new_status", 1) + assistant + finish, "", "UNKNOWN"},
		{"rate-limit-session-switch", start + strings.ReplaceAll(rateLimit, claudeFixtureSession, "00000000-0000-4000-8000-000000000099") + assistant + finish, "", "UNKNOWN"},
		{"unknown-key-with-spaces", start + strings.Replace(assistant, `"message":`, `"uuid session_id":null,"message":`, 1) + finish, "", "UNKNOWN"},
		{"final-only-candidate", start + claudeFixtureAssistant("intermediate", "commentary") + assistant + finish, "", ""},
		{"multipart-message", start + claudeFixtureAssistant("msg-final", "fi") + claudeFixtureAssistant("msg-final", "nal") + finish, "", ""},
		{"wrong-session", valid, "00000000-0000-4000-8000-000000000099", "UNKNOWN"},
		{"session-path", strings.ReplaceAll(valid, claudeFixtureSession, "/tmp/transcript.jsonl"), "", "UNKNOWN"},
		{"option-session", strings.ReplaceAll(valid, claudeFixtureSession, "--continue"), "", "UNKNOWN"},
		{"session-name", strings.ReplaceAll(valid, claudeFixtureSession, "name"), "", "UNKNOWN"},
		{"extra-session-hyphens", strings.ReplaceAll(valid, claudeFixtureSession, "0000--00-0000-4000-8000-000000000001"), "", "UNKNOWN"},
		{"session-switch", start + strings.ReplaceAll(assistant+finish, claudeFixtureSession, "00000000-0000-4000-8000-000000000099"), "", "UNKNOWN"},
		{"missing-init", assistant + finish, "", "UNKNOWN"},
		{"duplicate-init", start + valid, "", "UNKNOWN"},
		{"missing-assistant", start + finish, "", "UNKNOWN"},
		{"missing-result", start + assistant, "", "UNKNOWN"},
		{"empty", "", "", "UNKNOWN"},
		{"malformed", start + "not JSON\n" + assistant + finish, "", "UNKNOWN"},
		{"null-event", start + "null\n" + assistant + finish, "", "UNKNOWN"},
		{"blank-line", start + "\n" + assistant + finish, "", "UNKNOWN"},
		{"truncated", valid + `{"type":`, "", "UNKNOWN"},
		{"line-limit", start + strings.Repeat("x", 1024*1024+1), "", "UNKNOWN"},
		{"after-completion", valid + assistant, "", "UNKNOWN"},
		{"duplicate-completion", valid + finish, "", "UNKNOWN"},
		{"unknown-event", start + `{"type":"idle"}` + "\n" + assistant + finish, "", "UNKNOWN"},
		{"unknown-system-subtype", start + strings.Replace(start, "init", "future_status", 1) + assistant + finish, "", "UNKNOWN"},
		{"user-echo", start + strings.ReplaceAll(assistant, `"assistant"`, `"user"`) + finish, "", "UNKNOWN"},
		{"subagent", start + strings.Replace(assistant, `"parent_tool_use_id":null`, `"parent_tool_use_id":"tool-1"`, 1) + finish, "", "UNKNOWN"},
		{"missing-parent", start + strings.Replace(assistant, `"parent_tool_use_id":null,`, "", 1) + finish, "", "UNKNOWN"},
		{"agent-id", start + strings.Replace(assistant, `"message":`, `"agent_id":"sub","message":`, 1) + finish, "", "UNKNOWN"},
		{"aborted", start + strings.Replace(assistant, `"message":`, `"aborted":true,"message":`, 1) + finish, "", "UNKNOWN"},
		{"reasoning-only", start + strings.Replace(assistant, `"type":"text"`, `"type":"thinking"`, 1) + finish, "", "UNKNOWN"},
		{"tool-only", start + strings.Replace(assistant, `"type":"text"`, `"type":"tool_use"`, 1) + finish, "", "UNKNOWN"},
		{"empty-text", start + claudeFixtureAssistant("msg-final", "") + finish, "", "UNKNOWN"},
		{"result-mismatch", start + assistant + strings.Replace(finish, `"result":"final"`, `"result":"other"`, 1), "", "UNKNOWN"},
		{"unknown-stop", start + assistant + strings.Replace(finish, "end_turn", "max_tokens", 1), "", "UNKNOWN"},
		{"assistant-stop", start + strings.Replace(assistant, `"stop_reason":null`, `"stop_reason":"max_tokens"`, 1) + finish, "", "UNKNOWN"},
		{"result-subtype", start + assistant + strings.Replace(finish, "success", "new_status", 1), "", "UNKNOWN"},
		{"missing-error-bit", start + assistant + strings.Replace(finish, `"is_error":false,`, "", 1), "", "UNKNOWN"},
		{"null-error-bit", start + assistant + strings.Replace(finish, `"is_error":false`, `"is_error":null`, 1), "", "UNKNOWN"},
		{"string-error-bit", start + assistant + strings.Replace(finish, `"is_error":false`, `"is_error":"false"`, 1), "", "UNKNOWN"},
		{"zero-turns", start + assistant + strings.Replace(finish, `"num_turns":1`, `"num_turns":0`, 1), "", "UNKNOWN"},
		{"terminal-reason", start + assistant + strings.Replace(finish, `"subtype":`, `"terminal_reason":"aborted_tools","subtype":`, 1), "", "FAILED"},
		{"api-failure-on-success", start + assistant + strings.Replace(finish, `"subtype":`, `"api_error_status":401,"subtype":`, 1), "", "UNKNOWN"},
		{"lost-result", start + assistant + strings.Replace(finish, `"subtype":`, `"result_index":1,"subtype":`, 1), "", "UNKNOWN"},
		{"contradictory-errors", start + assistant + strings.Replace(finish, `"subtype":`, `"errors":["bad"],"subtype":`, 1), "", "UNKNOWN"},
		{"structured-output", start + assistant + strings.Replace(finish, `"subtype":`, `"structured_output":{"kind":"approve"},"subtype":`, 1), "", "UNKNOWN"},
		{"error-field-on-assistant", start + strings.Replace(assistant, `"message":`, `"error":"authentication_failed","message":`, 1) + finish, "", "UNKNOWN"},
		{"error-field-on-result", start + assistant + strings.Replace(finish, `"subtype":`, `"error":"failed","subtype":`, 1), "", "UNKNOWN"},
		{"explicit-failure", start + errorResult, "", "FAILED"},
		{"error-bit", start + assistant + strings.Replace(finish, `"is_error":false`, `"is_error":true`, 1), "", "FAILED"},
		{"contradicted-failure", start + strings.Replace(errorResult, `"is_error":true`, `"is_error":false`, 1), "", "UNKNOWN"},
		{"failure-then-success", start + errorResult + finish, "", "UNKNOWN"},
		{"failure-malformed-tail", start + errorResult + "not JSON\n", "", "UNKNOWN"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, err := parseClaudeTurn([]byte(tc.body), tc.expected)
			if tc.outcome == "" {
				if err != nil || r.session != claudeFixtureSession || r.text != "final" {
					t.Fatal(r, err)
				}
			} else {
				var failure *NativeSubmitError
				if !errors.As(err, &failure) || failure.Outcome != tc.outcome || r != (nativeTurn{}) {
					t.Fatal(r, err)
				}
			}
		})
	}
	for _, key := range []string{"type", "uuid", "session_id", "message", "role", "content", "text", "stop_reason", "parent_tool_use_id"} {
		for _, mutation := range []string{"alias", "duplicate", "escaped-duplicate", "unknown"} {
			t.Run(key+"/"+mutation, func(t *testing.T) {
				original := `"` + key + `":`
				replacement := `"` + strings.ToUpper(key[:1]) + key[1:] + `":`
				switch mutation {
				case "duplicate":
					replacement = original + `null,` + original
				case "escaped-duplicate":
					replacement = fmt.Sprintf(`"\u%04x%s":null,`, key[0], key[1:]) + original
				case "unknown":
					replacement = `"unknown":true,` + original
				}
				bad := strings.Replace(assistant, original, replacement, 1)
				if _, err := parseClaudeTurn([]byte(start+bad+finish), ""); err == nil {
					t.Fatal("accepted ambiguous assistant", bad)
				}
			})
		}
	}
	for _, key := range []string{"type", "session_id", "subtype", "is_error", "result", "stop_reason", "num_turns"} {
		bad := strings.Replace(finish, `"`+key+`":`, `"`+strings.ToUpper(key[:1])+key[1:]+`":`, 1)
		if _, err := parseClaudeTurn([]byte(start+assistant+bad), ""); err == nil {
			t.Fatal("accepted result alias", key)
		}
	}
}

func TestHeadlessClaudeCommandAndLookup(t *testing.T) {
	a := config.ResolvedAgent{Tool: "claude", Cwd: "/cwd spaces/$(literal)", Model: "model;literal", Permissions: "default", Session: "persistent"}
	command, err := NativeCommand(a, "/native spaces/claude")
	want := []string{"--print", "--bare", "--verbose", "--output-format", "stream-json", "--input-format", "text", "--permission-prompts", "none", "--prompt-suggestions", "false", "--strict-mcp-config", "--setting-sources", "", "--disable-slash-commands", "--no-chrome", "--model", a.Model}
	if err != nil || command.Binary != "/native spaces/claude" || !reflect.DeepEqual(command.Args, want) {
		t.Fatal(command, err)
	}
	resume := nativeResumeCommand(command, "claude", claudeFixtureSession)
	if !reflect.DeepEqual(resume.Args, append(want, "--resume", claudeFixtureSession)) {
		t.Fatal(resume)
	}
	for _, mode := range []string{"--bare", "--safe-mode"} {
		a.Args = []string{mode}
		explicit, err := NativeCommand(a, "/native spaces/claude")
		wantExplicit := append([]string(nil), want...)
		wantExplicit[1] = mode
		if err != nil || !reflect.DeepEqual(explicit.Args, wantExplicit) {
			t.Fatal(explicit, err)
		}
		if resumed := nativeResumeCommand(explicit, "claude", claudeFixtureSession); !reflect.DeepEqual(resumed.Args, append(wantExplicit, "--resume", claudeFixtureSession)) {
			t.Fatal(resumed)
		}
	}
	a.Args = nil
	a.Permissions = "full"
	command, err = NativeCommand(a, "/native")
	if err != nil || !strings.Contains(strings.Join(command.Args, " "), "--dangerously-skip-permissions") {
		t.Fatal(command, err)
	}
	for _, args := range [][]string{{"--bare", "--bare"}, {"--safe-mode", "--safe-mode"}, {"--bare", "--safe-mode"}, {"--safe-mode", "--bare"}, {"--safe-mode=true"}, {"--safe-mode", "--settings", "{}"}, {"--provider", "anthropic"}, {"--bare", "--settings", "{}"}, {"--resume", claudeFixtureSession}, {"--no-session-persistence"}, {"--output-format", "text"}, {"--bg"}, {"--settings", "{}"}, {"--effort", "high"}, {"--profile", "name"}, {""}, {"--safe-mode\x00"}} {
		a.Args = args
		if _, err := NativeCommand(a, "/native"); err == nil {
			t.Fatal("accepted unmanaged Claude args", args)
		}
	}
	for _, tool := range []string{"codex", "opencode", "unknown-provider"} {
		a.Tool, a.Args = tool, []string{"--safe-mode"}
		if _, err := NativeCommand(a, "/native"); err == nil {
			t.Fatal("accepted Claude OAuth opt-in for other provider", tool)
		}
	}
	root := t.TempDir()
	shim, real := filepath.Join(root, "cmux-cli-shims", "test"), filepath.Join(root, "bin")
	for _, dir := range []string{shim, real} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "claude"), []byte("fixture, never executed"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", shim+string(os.PathListSeparator)+"."+string(os.PathListSeparator)+real)
	if path, err := NativeExecutable("claude"); err != nil || path != filepath.Join(real, "claude") {
		t.Fatal(path, err)
	}
}

// Captured from an explicitly selected, tool-free non-bare OAuth probe, with
// paths/native/request IDs normalized. This does not validate the bare default.
// apiKeySource=none can still succeed; it never selects the authentication mode.
func TestHeadlessClaudeOAuthNonceFixture(t *testing.T) {
	body, err := os.ReadFile("testdata/claude-oauth-nonce.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	turn, err := parseClaudeTurn(body, claudeFixtureSession)
	if err != nil || turn.session != claudeFixtureSession || turn.text != "CLAUDE_OAUTH_HEADLESS_COMPAT_20261104_01" {
		t.Fatal(turn, err)
	}
	// The real reviewer also emitted a separate empty thinking block carrying
	// display/timing metadata before its final text. It cannot supply an answer.
	lines := strings.SplitAfter(string(body), "\n")
	thinking := strings.Replace(lines[1], `"request_id":`, `"thinking_display":"summary","thinking_duration_ms":123,"request_id":`, 1)
	thinking = strings.Replace(thinking, `{"type":"text","text":"CLAUDE_OAUTH_HEADLESS_COMPAT_20261104_01"}`, `{"type":"thinking","thinking":"","signature":"fixture"}`, 1)
	withThinking := lines[0] + thinking + strings.Join(lines[1:], "")
	if got, err := parseClaudeTurn([]byte(withThinking), claudeFixtureSession); err != nil || got != turn {
		t.Fatal(got, err)
	}
	if _, err := parseClaudeTurn([]byte(lines[0]+thinking+strings.Join(lines[2:], "")), claudeFixtureSession); err == nil {
		t.Fatal("accepted thinking as nonce")
	}
	// New metadata must not weaken recursive uniqueness or exact-key checks.
	for _, field := range []string{"analytics_disabled", "request_id", "stop_details", "rateLimitType", "subagent_stats", "credential_expires_at"} {
		key := `"` + field + `":`
		for _, replacement := range []string{key + `null,` + key, `"Unknown_` + field + `":`} {
			bad := strings.Replace(string(body), key, replacement, 1)
			if _, err := parseClaudeTurn([]byte(bad), claudeFixtureSession); err == nil {
				t.Fatal("accepted ambiguous metadata", field)
			}
		}
	}
	for name, bad := range map[string]string{
		"metadata-only": lines[0] + lines[2],
		"result-only":   lines[0] + lines[2] + lines[3],
		"wrong-session": strings.Replace(string(body), `"uuid":"result-1"`, `"session_id":"00000000-0000-4000-8000-000000000099","uuid":"result-1"`, 1),
		"failed-result": strings.Replace(string(body), `"is_error":false`, `"is_error":true`, 1),
		"wrong-text":    strings.Replace(string(body), `"result":"CLAUDE_OAUTH_HEADLESS_COMPAT_20261104_01"`, `"result":"other"`, 1),
	} {
		if _, err := parseClaudeTurn([]byte(bad), claudeFixtureSession); err == nil {
			t.Fatal("metadata supplied completion", name)
		}
	}
}
