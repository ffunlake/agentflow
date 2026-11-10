// An executable protocol/transport fixture, never a native model result.
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

func main() {
	input, _ := io.ReadAll(os.Stdin)
	cwd, _ := os.Getwd()
	record := struct {
		Argv  []string
		Cwd   string
		Stdin string
	}{os.Args[1:], cwd, string(input)}
	f, err := os.OpenFile(os.Getenv("SUBMIT_FIXTURE_RECORD"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		os.Exit(90)
	}
	_ = json.NewEncoder(f).Encode(record)
	_ = f.Close()
	mode, tool := os.Getenv("SUBMIT_FIXTURE_MODE"), os.Getenv("SUBMIT_FIXTURE_TOOL")
	if mode == "wait" {
		for {
			time.Sleep(time.Second)
		}
	}
	if mode == "lost" {
		return
	}
	if mode == "truncated" {
		fmt.Print(strings.Repeat("x", 4*1024*1024+1))
		return
	}
	if mode == "stderr-truncated" {
		fmt.Fprint(os.Stderr, strings.Repeat("x", 4*1024*1024+1))
		return
	}
	if mode == "malformed" {
		fmt.Println("fixture-secret invalid JSON")
		return
	}
	id := os.Getenv("SUBMIT_FIXTURE_SESSION")
	if mode == "wrong-id" {
		id = "other-native-session"
	}
	if tool == "codex" {
		if mode != "missing-id" {
			emit(map[string]string{"type": "thread.started", "thread_id": id})
		}
		if mode != "no-assistant" {
			emit(map[string]any{"type": "item.completed", "item": map[string]string{"type": "agent_message", "text": "fixture assistant"}})
		}
		if mode != "no-completion" {
			emit(map[string]string{"type": "turn.completed"})
		}
	} else {
		if mode == "missing-id" {
			id = ""
		}
		if mode != "no-assistant" {
			emit(map[string]any{"type": "text", "sessionID": id, "part": map[string]string{"type": "text", "sessionID": id, "text": "fixture assistant"}})
		}
		if mode != "no-completion" {
			emit(map[string]any{"type": "step_finish", "sessionID": id, "part": map[string]string{"reason": "stop"}})
		}
	}
	if mode == "failed" {
		emit(map[string]string{"type": "turn.failed", "message": "fixture-secret"})
	}
	if mode == "exit" || mode == "failed" {
		fmt.Fprintln(os.Stderr, "fixture-secret diagnostic")
		os.Exit(17)
	}
}
