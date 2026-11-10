package runtime

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"agentflow/internal/config"
	"agentflow/internal/ipc"
)

func nativeEvents(t *testing.T, tool, id, text string) []byte {
	t.Helper()
	var events []any
	if tool == "codex" {
		events = []any{map[string]any{"type": "thread.started", "thread_id": id},
			map[string]any{"type": "item.completed", "item": map[string]string{"type": "agent_message", "text": text}}, map[string]string{"type": "turn.completed"}}
	} else {
		events = []any{map[string]any{"type": "text", "sessionID": id, "part": map[string]string{"type": "text", "sessionID": id, "text": text}},
			map[string]any{"type": "step_finish", "sessionID": id, "part": map[string]string{"reason": "stop"}}}
	}
	var body []byte
	for _, e := range events {
		b, _ := json.Marshal(e)
		body = append(body, append(b, '\n')...)
	}
	return body
}

func TestNativeReadyRequiresIndependentAssistantAndExactIdentity(t *testing.T) {
	seat := SeatSnapshot{RunID: "run-test", AgentID: "builder", Generation: 2,
		Native: NativeBinding{WorkspaceID: "workspace:2", SurfaceID: "surface:3"}}
	for _, tool := range []string{"opencode", "codex"} {
		t.Run(tool, func(t *testing.T) {
			seat.Tool = tool
			ready := nativeReady{Actor: ipc.Actor{RunID: seat.RunID, AgentID: seat.AgentID, Generation: 2}, Tool: tool, Status: Ready, Nonce: "unique"}
			text, _ := json.Marshal(ready)
			body := nativeEvents(t, tool, "native-session", string(text))
			native, claim, err := ParseNativeReady(tool, body, seat, "unique")
			if err != nil || claim == nil || native.SessionID != "native-session" || claim.Native != native || claim.Actor != ready.Actor {
				t.Fatalf("matching assistant rejected: %+v %+v %v", native, claim, err)
			}
			for _, field := range []string{"run", "agent", "generation", "tool", "status", "nonce", "unknown", "trailing", "fenced", "empty"} {
				t.Run(field, func(t *testing.T) {
					bad := ready
					switch field {
					case "run":
						bad.Actor.RunID = "other"
					case "agent":
						bad.Actor.AgentID = "other"
					case "generation":
						bad.Actor.Generation = 1
					case "tool":
						bad.Tool = "claude"
					case "status":
						bad.Status = Starting
					case "nonce":
						bad.Nonce = "replay"
					}
					b, _ := json.Marshal(bad)
					s := string(b)
					switch field {
					case "unknown":
						s = strings.TrimSuffix(s, "}") + `,"extra":true}`
					case "trailing":
						s += `{}`
					case "fenced":
						s = "```json\n" + s + "\n```"
					case "empty":
						s = ""
					}
					if _, claim, err := ParseNativeReady(tool, nativeEvents(t, tool, "native-session", s), seat, "unique"); err == nil || claim != nil {
						t.Fatal("accepted wrong READY")
					}
				})
			}
			for _, bad := range [][]byte{
				nativeEvents(t, tool, "", string(text)),
				append(append([]byte{}, body...), []byte(`{"type":"error","message":"fixture-secret"}`)...),
				append(append([]byte{}, body...), []byte("not json\n")...),
				[]byte(`{"type":"thread.started","thread_id":"native-session"}`),
				[]byte(`{"type":"step_finish","sessionID":"native-session"}`),
				[]byte(strings.ReplaceAll(strings.ReplaceAll(string(body), `"agent_message"`, `"command_execution"`), `"type":"text"`, `"type":"reasoning"`)),
			} {
				if _, claim, err := ParseNativeReady(tool, bad, seat, "unique"); err == nil || claim != nil {
					t.Fatal("accepted non-assistant/error/idle event")
				}
			}
			var changed []byte
			if tool == "codex" {
				changed = []byte("\n" + `{"type":"thread.started","thread_id":"different"}`)
			} else {
				changed = []byte("\n" + `{"type":"step_finish","sessionID":"different"}`)
			}
			if _, claim, err := ParseNativeReady(tool, append(body, changed...), seat, "unique"); err == nil || claim != nil {
				t.Fatal("accepted native identity switch")
			}
		})
	}
}

func TestNativeCommandLiteralArgvAndManagedFlags(t *testing.T) {
	for _, tool := range []string{"opencode", "codex"} {
		a := config.ResolvedAgent{Tool: tool, Cwd: "/cwd with spaces/$(touch bad);'", Model: "model;$(literal)", Permissions: "full", Session: "fresh"}
		cmd, err := NativeCommand(a, "/bin with spaces/"+tool)
		if err != nil || cmd.Binary != "/bin with spaces/"+tool {
			t.Fatal(cmd, err)
		}
		joined := strings.Join(cmd.Args, "\n")
		if !strings.Contains(joined, a.Cwd) || !strings.Contains(joined, a.Model) || strings.Contains(joined, "sh -c") {
			t.Fatal(cmd)
		}
		for _, bad := range [][]string{{"--session", "old"}, {"--format", "default"}, {"--cd", "/wrong"}, {"--command", "shell"}, {"--credential=fixture-secret"}, {"--", "prompt"}} {
			a.Args = bad
			if cmd, err := NativeCommand(a, "/native"); err == nil || !reflect.DeepEqual(cmd, Command{}) {
				t.Fatal("accepted raw flags", cmd)
			}
		}
	}
	if _, err := NativeCommand(config.ResolvedAgent{Tool: "claude"}, "/claude"); err == nil {
		t.Fatal("Claude implemented unexpectedly")
	}
}
