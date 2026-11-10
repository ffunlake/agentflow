package runtime

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agentflow/internal/config"
	"agentflow/internal/store"
)

func openCodeFixtureEvent(kind, partType, session, message, text, reason string) string {
	b, _ := json.Marshal(map[string]any{"type": kind, "sessionID": session, "part": map[string]string{
		"type": partType, "sessionID": session, "messageID": message, "text": text, "reason": reason,
	}})
	return string(b) + "\n"
}

func TestHeadlessOpenCodeProtocol(t *testing.T) {
	start := openCodeFixtureEvent("step_start", "step-start", "ses_1", "msg_1", "", "")
	text := openCodeFixtureEvent("text", "text", "ses_1", "msg_1", `{"kind":"approve","accepted":[true]}`, "")
	finish := openCodeFixtureEvent("step_finish", "step-finish", "ses_1", "msg_1", "", "stop")
	valid := start + text + finish
	for _, tc := range []struct{ name, body, expected, outcome string }{
		{"success", valid, "ses_1", ""},
		{"discover-session", valid, "", ""},
		{"native-metadata", start + strings.Replace(text, `"part":{`, `"timestamp":1730000000000,"part":{"id":"part_1","time":{"start":1,"end":2},"metadata":{},`, 1) +
			strings.Replace(finish, `"part":{`, `"part":{"snapshot":"hash","cost":0,"tokens":{"input":1,"output":1},`, 1), "ses_1", ""},
		{"wrong-session", valid, "ses_other", "UNKNOWN"},
		{"missing-id", strings.ReplaceAll(valid, "ses_1", ""), "", "UNKNOWN"},
		{"option-session", strings.ReplaceAll(valid, "ses_1", "--continue"), "", "UNKNOWN"},
		{"changed-session", start + strings.ReplaceAll(text+finish, "ses_1", "ses_2"), "", "UNKNOWN"},
		{"changed-message", start + strings.ReplaceAll(text, "msg_1", "msg_2") + finish, "", "UNKNOWN"},
		{"missing-message", strings.ReplaceAll(valid, "msg_1", ""), "", "UNKNOWN"},
		{"no-start", text + finish, "", "UNKNOWN"},
		{"no-text", start + finish, "", "UNKNOWN"},
		{"no-finish", start + text, "", "UNKNOWN"},
		{"wrong-part-type", start + strings.Replace(text, `"type":"text"`, `"type":"tool"`, 1) + finish, "", "UNKNOWN"},
		{"reasoning-only", start + openCodeFixtureEvent("reasoning", "reasoning", "ses_1", "msg_1", "READY", "") + finish, "", "UNKNOWN"},
		{"user-echo", start + openCodeFixtureEvent("message", "user", "ses_1", "msg_1", "READY", "") + finish, "", "UNKNOWN"},
		{"unknown-event-only", `{"type":"idle","sessionID":"ses_1"}` + "\n", "", "UNKNOWN"},
		{"unknown-finish", start + text + strings.Replace(finish, "stop", "length", 1), "", "UNKNOWN"},
		{"after-complete", valid + text, "", "UNKNOWN"},
		{"multiple-turns", valid + valid, "", "UNKNOWN"},
		{"malformed", start + "not json\n" + text + finish, "", "UNKNOWN"},
		{"duplicate-field", start + strings.Replace(text, `"type":"text"`, `"type":"text","type":"text"`, 1) + finish, "", "UNKNOWN"},
		{"truncated-line", valid + `{"type":`, "", "UNKNOWN"},
		{"line-limit", start + strings.Repeat("x", 1024*1024+1), "", "UNKNOWN"},
		{"explicit-failure", start + `{"type":"error","sessionID":"ses_1"}` + "\n", "", "FAILED"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, err := parseOpenCodeTurn([]byte(tc.body), tc.expected)
			if tc.outcome == "" {
				if err != nil || r.session != "ses_1" || r.text != `{"kind":"approve","accepted":[true]}` {
					t.Fatal(r, err)
				}
			} else {
				var failure *NativeSubmitError
				if !errors.As(err, &failure) || failure.Outcome != tc.outcome {
					t.Fatal(r, err)
				}
			}
		})
	}
}

func TestHeadlessOpenCodeRejectsReservedFieldAliases(t *testing.T) {
	start := openCodeFixtureEvent("step_start", "step-start", "ses_1", "msg_1", "", "")
	text := openCodeFixtureEvent("text", "text", "ses_1", "msg_1", `{"kind":"approve","accepted":[true]}`, "")
	finish := openCodeFixtureEvent("step_finish", "step-finish", "ses_1", "msg_1", "", "stop")
	for _, tc := range []struct {
		name string
		bad  []string
	}{
		{"error", []string{
			strings.Replace(text, `"part":`, `"error":{"message":"failed"},"part":`, 1),
			strings.Replace(text, `"part":`, `"Error":{"message":"failed"},"part":`, 1),
			strings.Replace(text, `"part":`, `"ERROR":true,"part":`, 1),
			strings.Replace(text, `"messageID":`, `"error":true,"messageID":`, 1),
			strings.Replace(text, `"messageID":`, `"Error":true,"messageID":`, 1),
		}},
		{"session", []string{
			strings.Replace(text, `"sessionID":"ses_1"`, `"sessionid":"wrong"`, 1),
			strings.Replace(text, `"sessionID":"ses_1"`, `"session_id":"wrong"`, 1),
			strings.Replace(text, `"type":"text"`, `"session":"wrong","type":"text"`, 1),
			strings.Replace(text, `"sessionID":"ses_1"`, `"sessionID":"wrong","SessionID":"ses_1"`, 1),
			strings.Replace(text, `},"sessionID":"ses_1"`, `},"sessionid":"wrong"`, 1),
			strings.Replace(text, `},"sessionID":"ses_1"`, `},"session_id":"wrong"`, 1),
			strings.Replace(text, `},"sessionID":"ses_1"`, `},"sessionID":"wrong","SessionID":"ses_1"`, 1),
		}},
		{"message", []string{
			strings.Replace(text, `"messageID":"msg_1"`, `"messageID":"msg_1","messageId":"wrong"`, 1),
			strings.Replace(text, `"messageID":"msg_1"`, `"messageID":"msg_1","message_id":"wrong"`, 1),
			strings.Replace(text, `"part":`, `"messageID":"wrong","part":`, 1),
			strings.Replace(text, `"messageID":"msg_1"`, `"messageId":"msg_1"`, 1),
			strings.Replace(text, `"messageID":"msg_1"`, `"message":"wrong","messageID":"msg_1"`, 1),
		}},
		{"finish", []string{
			strings.Replace(text, `"part":`, `"finish":"length","part":`, 1),
			strings.Replace(text, `"reason":""`, `"reason":"","finish":"length"`, 1),
			strings.Replace(text, `"type":"text"`, `"Type":"text"`, 1),
			strings.Replace(text, `"part":`, `"Part":`, 1),
			strings.Replace(text, `"text":`, `"Text":`, 1),
			strings.Replace(text, `"reason":`, `"Reason":`, 1),
			strings.Replace(text, `,"type":"text"}`+"\n", `,"Type":"text"}`+"\n", 1),
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, bad := range tc.bad {
				if r, err := parseOpenCodeTurn([]byte(start+bad+finish), "ses_1"); err == nil || r != (openCodeTurn{}) {
					t.Fatalf("accepted reserved field in %s: %+v %v", bad, r, err)
				}
			}
		})
	}
	for _, bad := range []string{
		strings.Replace(finish, `"reason":"stop"`, `"Reason":"stop"`, 1),
		strings.Replace(finish, `"reason":"stop"`, `"reason":"length","Reason":"stop"`, 1),
		strings.Replace(finish, `"reason":"stop"`, `"reason":"stop","finish_reason":"length"`, 1),
	} {
		if _, err := parseOpenCodeTurn([]byte(start+text+bad), "ses_1"); err == nil {
			t.Fatalf("accepted ambiguous finish: %s", bad)
		}
	}
}

func TestHeadlessReadyRejectsNonceConflictsAndFieldAliases(t *testing.T) {
	seat := SeatSnapshot{RunID: "run", AgentID: "builder", Generation: 1,
		Native: NativeBinding{SessionID: "ses_1", WorkspaceID: "headless:run", SurfaceID: "headless:builder"}}
	for _, tool := range []string{"opencode", "codex", "claude"} {
		ready := `{"actor":{"runId":"run","agentId":"builder","generation":1},"tool":"` + tool + `","status":"READY","nonce":"expected"}`
		bad := []string{
			strings.Replace(ready, `"nonce":"expected"`, `"nonce":"wrong","nonce":"expected"`, 1),
			strings.Replace(ready, `"nonce":"expected"`, `"nonce":"expected","nonce":"expected"`, 1),
			strings.Replace(ready, `"nonce":"expected"`, `"nonce":"wrong","Nonce":"expected"`, 1),
			strings.Replace(ready, `"nonce":"expected"`, `"Nonce":"wrong","nonce":"expected"`, 1),
			strings.Replace(ready, `"actor":{`, `"actor":{"unknown":true,`, 1),
		}
		for _, key := range []string{"actor", "tool", "status", "nonce", "runId", "agentId", "generation"} {
			alias := strings.ToUpper(key[:1]) + key[1:]
			bad = append(bad, strings.Replace(ready, `"`+key+`":`, `"`+alias+`":`, 1))
		}
		t.Run(tool, func(t *testing.T) {
			if _, claim, err := decodeNativeReady(ready, seat, tool, "expected", seat.Native); err != nil || claim == nil {
				t.Fatalf("valid READY rejected: %v", err)
			}
			for _, text := range bad {
				if _, claim, err := decodeNativeReady(text, seat, tool, "expected", seat.Native); err == nil || claim != nil {
					t.Fatalf("accepted ambiguous READY: %s", text)
				}
			}
		})
	}
}

func TestHeadlessOpenCodeRestrictionsAndGeneration(t *testing.T) {
	testHeadlessRestrictionsAndGeneration(t, "opencode")
}

func TestHeadlessClaudeRestrictionsAndGeneration(t *testing.T) {
	testHeadlessRestrictionsAndGeneration(t, "claude")
}

func testHeadlessRestrictionsAndGeneration(t *testing.T, tool string) {
	newAdapter := NewHeadlessOpenCode
	if tool == "claude" {
		newAdapter = NewHeadlessClaude
	}
	source, err := config.Load(filepath.Join("..", "..", "examples", "headless-"+tool+".yaml"))
	if err != nil {
		t.Fatal(err)
	}
	c, err := config.Resolve(source, config.ResolveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	o := HeadlessOptions{RunID: "run", RunDir: t.TempDir(), Binary: "/unused/" + tool, TurnTimeout: time.Second}
	a, err := newAdapter(c, o)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewHeadlessCodex(c, o); err == nil {
		t.Fatal("Codex accepted OpenCode plan")
	}
	s := SeatSnapshot{RunID: "run", AgentID: "builder", Generation: 2, Tool: tool, Status: Ready, Native: NativeBinding{WorkspaceID: "headless:run", SurfaceID: "headless:builder", SessionID: "ses_1"}}
	if tool == "claude" {
		s.Native.SessionID = claudeFixtureSession
	}
	if _, err := a.Inspect(t.Context(), s); err == nil {
		t.Fatal("generation 2 inspected")
	}
	if _, err := a.Submit(t.Context(), s, store.Message{RunID: "run", ID: "id", Recipient: "builder", Kind: "assign"}); err == nil {
		t.Fatal("generation 2 submitted")
	}
	if _, err := a.Launch(t.Context(), LaunchRequest{Seat: s}); err == nil {
		t.Fatal("generation 2 launched")
	}
	s.Generation = 1
	if _, err := a.Inspect(t.Context(), s); err != nil {
		t.Fatal("generation 1 should inspect", err)
	}
	for _, tc := range []struct {
		name   string
		mutate func(*config.Config, *HeadlessOptions)
	}{
		{"fresh", func(c *config.Config, _ *HeadlessOptions) {
			a := c.Agents["builder"]
			policy := "fresh"
			a.Session = &policy
			c.Agents["builder"] = a
		}},
		{"mixed", func(c *config.Config, _ *HeadlessOptions) {
			a := c.Agents["builder"]
			tool := "codex"
			a.Tool = &tool
			c.Agents["builder"] = a
		}},
		{"shim", func(_ *config.Config, o *HeadlessOptions) { o.Binary = "/tmp/cmux-cli-shims/opencode" }},
		{"timeout", func(_ *config.Config, o *HeadlessOptions) { o.TurnTimeout = 0 }},
		{"tool", func(_ *config.Config, o *HeadlessOptions) { o.Tool = "codex" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source, err := config.Load(filepath.Join("..", "..", "examples", "headless-"+tool+".yaml"))
			if err != nil {
				t.Fatal(err)
			}
			options := o
			tc.mutate(source, &options)
			frozen, err := config.Resolve(source, config.ResolveOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := newAdapter(frozen, options); err == nil {
				t.Fatal("unsupported options accepted")
			}
		})
	}
}
