// A deterministic executable fixture. It never contacts a cmux socket or model.
package main

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// terminalCommand models the accepted layout-command subset of cmux's
// Workspace+CustomLayout.dequeueInitialTerminalInput and
// TerminalSurface.parsedSocketInputEvents at commit
// 12023d89feb5425c766ba05e8d8e4cfc176e3991. Layout appends LF; sendInput turns
// LF/CR into raw Return (coalescing CRLF), TAB/BS/DEL/ESC into key events,
// and complete ESC/C1 sequences into terminal output rather than shell text.
// We fail closed on key/control events instead of pretending sh -c can replay
// them. Only one final Return may submit a command. No PTY or cmux is opened.
func terminalCommand(input string) (string, error) {
	var text strings.Builder
	returns := 0
	previousCR := false
	for _, r := range input {
		switch r {
		case '\n':
			if !previousCR {
				returns++
			}
			previousCR = false
		case '\r':
			returns++
			previousCR = true
		case '\t':
			return "", errors.New("terminal key event: tab")
		case '\b', '\x7f':
			return "", errors.New("terminal key event: backspace")
		case '\x1b':
			return "", errors.New("terminal escape key or control sequence")
		default:
			if r < 0x20 || (r >= 0x80 && r <= 0x9f) {
				return "", errors.New("terminal control input")
			}
			if returns != 0 {
				return "", errors.New("text after terminal Return")
			}
			previousCR = false
			text.WriteRune(r)
		}
	}
	if returns != 1 {
		return "", errors.New("expected one terminal Return")
	}
	return text.String(), nil
}

const workspaceID = "10000000-0000-0000-0000-000000000071"
const windowID = "10000000-0000-0000-0000-000000000009"

func writeLog(path string, value any) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		panic(err)
	}
	defer f.Close()
	if err := json.NewEncoder(f).Encode(value); err != nil {
		panic(err)
	}
}

func option(name string) string {
	for i, arg := range os.Args[1:] {
		if arg == name && i+2 < len(os.Args) {
			return os.Args[i+2]
		}
	}
	return ""
}

func main() {
	if len(os.Args) == 3 && os.Args[1] == "terminal-command" {
		command, err := terminalCommand(os.Args[2])
		message := ""
		if err != nil {
			message = err.Error()
		}
		_ = json.NewEncoder(os.Stdout).Encode(map[string]string{"command": command, "error": message})
		return
	}
	if os.Getenv("CMUX_FAKE_TOOL") == "1" {
		cwd, _ := os.Getwd()
		writeLog(os.Getenv("CMUX_FAKE_TOOL_LOG"), map[string]any{"cwd": cwd, "args": os.Args[1:]})
		return
	}
	if len(os.Args) > 3 && os.Args[1] == "surface" && os.Args[2] == "resume" && os.Args[3] == "show" {
		surface := option("--surface")
		session := "ses_" + surface[len(surface)-12:]
		ref := os.Getenv("CMUX_FAKE_SURFACE_REF")
		if os.Getenv("CMUX_FAKE_SURFACE_REF_BY_ID") == "1" {
			ref = "surface:8" + surface[len(surface)-1:]
		}
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{
			"surface_ref": ref, "workspace_ref": os.Getenv("CMUX_FAKE_WORKSPACE_REF"),
			"resume_binding": map[string]any{"kind": "opencode", "checkpoint_id": session, "cwd": os.Getenv("CMUX_FAKE_CWD"),
				"launch_command": map[string]string{"executable_path": os.Getenv("CMUX_FAKE_OPENCODE")}},
		})
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "send" {
		if err := os.WriteFile(os.Getenv("CMUX_FAKE_PROMPT"), []byte(os.Args[len(os.Args)-1]), 0600); err != nil {
			panic(err)
		}
		fmt.Println("OK")
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "send-key" {
		if err := os.WriteFile(os.Getenv("CMUX_FAKE_ENTER"), []byte("1"), 0600); err != nil {
			panic(err)
		}
		fmt.Println("OK")
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "record" {
		_ = json.NewEncoder(os.Stdout).Encode(os.Args[2:])
		return
	}
	writeLog(os.Getenv("CMUX_FAKE_LOG"), os.Args[1:])
	mode := os.Getenv("CMUX_FAKE_MODE")
	if len(os.Args) > 2 && os.Args[1] == "agent" {
		if mode == "message-fail" {
			fmt.Fprintln(os.Stderr, "fixture send refused")
			fmt.Println("fixture partial stdout")
			os.Exit(7)
		}
		if mode == "message-delay" {
			time.Sleep(5 * time.Second)
		}
		if os.Args[2] == "message" {
			if len(os.Args) != 9 || os.Args[4] != "--from" || os.Args[5] != "agentflow" || os.Args[6] != "--json" || os.Args[7] != "--" {
				panic(fmt.Sprintf("unexpected message argv: %q", os.Args))
			}
			target := os.Args[3]
			if mode == "message-wrong-surface" {
				target = "30000000-0000-0000-0000-000000000999"
			}
			hasAgent := mode != "message-no-agent"
			m := map[string]any{"id": "native-message-1", "sender_name": "agentflow", "sender_surface_id": nil, "sender_workspace_id": nil, "recipient_surface_id": target, "recipient_workspace_id": workspaceID, "body": os.Args[8], "state": "queued", "recipient_has_agent": hasAgent}
			if mode == "message-no-agent-field" {
				delete(m, "recipient_has_agent")
			}
			if os.Getenv("CMUX_FAKE_WORKFLOW") == "1" {
				digest := sha256.Sum256([]byte(os.Args[8]))
				m["id"] = fmt.Sprintf("native-%x", digest[:12])
			}
			if mode == "message-invalid" {
				fmt.Println("bad-json")
				return
			}
			if mode == "message-truncated" {
				fmt.Println(strings.Repeat("x", 5*1024*1024))
				return
			}
			fmt.Fprintln(os.Stderr, "No agent activity seen; message waits for hooks.")
			json.NewEncoder(os.Stdout).Encode(m)
			return
		}
		if os.Args[2] == "inbox" {
			if len(os.Args) != 8 || os.Args[3] != "--surface" || os.Args[5] != "--limit" || os.Args[6] != "100" || os.Args[7] != "--json" {
				panic("unexpected inbox argv")
			}
			if os.Getenv("CMUX_FAKE_WORKFLOW") == "1" && os.Getenv("CMUX_FAKE_INBOX") == "" {
				fmt.Println(`{"messages":[]}`)
				return
			}
			body, err := os.ReadFile(os.Getenv("CMUX_FAKE_INBOX"))
			if err != nil {
				panic(err)
			}
			os.Stdout.Write(body)
			return
		}
		panic("unexpected agent mutation")
	}
	command := ""
	for _, arg := range os.Args[1:] {
		if arg == "new-workspace" || arg == "tree" {
			command = arg
			break
		}
	}
	if mode == "fail" || mode == "tree-fail" && command == "tree" {
		fmt.Fprintln(os.Stderr, "fixture-secret native stderr must not leak")
		os.Exit(7)
	}
	if mode == "delay" {
		time.Sleep(5 * time.Second)
	}
	if command == "new-workspace" {
		var root map[string]any
		if err := json.Unmarshal([]byte(option("--layout")), &root); err != nil {
			panic(err)
		}
		var panes []any
		count := 0
		var visit func(map[string]any)
		visit = func(node map[string]any) {
			if pane, ok := node["pane"].(map[string]any); ok {
				count++
				id := fmt.Sprintf("20000000-0000-0000-0000-%012d", count)
				surfaceID := fmt.Sprintf("30000000-0000-0000-0000-%012d", count)
				if mode == "duplicate-surface" {
					surfaceID = "30000000-0000-0000-0000-000000000001"
				}
				surface := map[string]any{"id": surfaceID, "ref": fmt.Sprintf("surface:%d", count+80), "type": "terminal"}
				if mode == "browser" {
					surface["type"] = "browser"
				}
				panes = append(panes, map[string]any{"id": id, "ref": fmt.Sprintf("pane:%d", count+70), "surfaces": []any{surface}})
				initial := pane["surfaces"].([]any)[0].(map[string]any)
				command, err := terminalCommand(initial["command"].(string) + "\n")
				if err != nil {
					panic(err)
				}
				if os.Getenv("CMUX_FAKE_EXEC") == "1" {
					child := exec.Command("/bin/sh", "-c", command)
					child.Dir = initial["cwd"].(string)
					child.Env = append(os.Environ(), "CMUX_FAKE_TOOL=1")
					if out, err := child.CombinedOutput(); err != nil {
						panic(fmt.Sprintf("initial command: %v %s", err, out))
					}
				}
				node["pane"] = map[string]any{"id": id}
				return
			}
			for _, child := range node["children"].([]any) {
				visit(child.(map[string]any))
			}
		}
		visit(root)
		// Reversed pane enumeration proves binding uses structural leaf order.
		for i, j := 0, len(panes)-1; i < j; i, j = i+1, j-1 {
			panes[i], panes[j] = panes[j], panes[i]
		}
		if mode == "wrong-layout" {
			root["direction"] = "vertical"
		}
		wsID := workspaceID
		if mode == "wrong-workspace" {
			wsID = "10000000-0000-0000-0000-000000000072"
		}
		ws := map[string]any{"id": wsID, "ref": "workspace:71", "layout": root, "panes": panes}
		w := map[string]any{"id": windowID, "ref": "window:9", "workspaces": []any{ws}}
		tree := map[string]any{"windows": []any{w}}
		body, _ := json.Marshal(tree)
		if err := os.WriteFile(os.Getenv("CMUX_FAKE_STATE"), body, 0600); err != nil {
			panic(err)
		}
		switch mode {
		case "invalid":
			fmt.Println("not a workspace response")
		case "json", "wrong-workspace":
			fmt.Printf("{\"workspace_id\":%q,\"workspace_ref\":\"workspace:71\"}\n", workspaceID)
		case "duplicate-json":
			fmt.Printf("{\"workspace_id\":%q,\"workspace_id\":%q}\n", workspaceID, workspaceID)
		case "trailing-json":
			fmt.Printf("{\"workspace_id\":%q} trailing\n", workspaceID)
		case "uuid":
			fmt.Println("OK " + workspaceID)
		default:
			fmt.Println("OK workspace:71")
		}
		return
	}
	if command == "tree" {
		if got := option("--workspace"); got != workspaceID && got != "workspace:71" {
			panic("unscoped tree")
		}
		body, err := os.ReadFile(os.Getenv("CMUX_FAKE_STATE"))
		if err != nil {
			panic(err)
		}
		if mode == "bad-tree" {
			body = []byte("{}")
		}
		if mode == "duplicate-tree" {
			body = []byte(strings.Replace(string(body), `"windows":`, `"windows":[],"windows":`, 1))
		}
		_, _ = os.Stdout.Write(body)
		return
	}
	panic("unexpected cmux command")
}
