// A deterministic OpenCode database-query fixture. It never starts a model.
package main

import (
	"encoding/json"
	"os"
	"strings"
)

func sqlLiteral(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func write(value any) {
	if err := json.NewEncoder(os.Stdout).Encode(value); err != nil {
		panic(err)
	}
}

func sessionID(query string) string {
	if id := os.Getenv("CMUX_FAKE_SESSION_ID"); id != "" {
		return id
	}
	for _, marker := range []string{"WHERE id = '", "session_id = '"} {
		if _, rest, ok := strings.Cut(query, marker); ok {
			id, _, _ := strings.Cut(rest, "'")
			return id
		}
	}
	return ""
}

func main() {
	if len(os.Args) >= 3 && os.Args[1] == "export" {
		// A full transcript can exceed OpenCode export's bounded stdout.
		_, _ = os.Stdout.WriteString(strings.Repeat("{", 192*1024))
		return
	}
	if len(os.Args) < 5 || os.Args[1] != "db" || os.Args[len(os.Args)-2] != "--format" || os.Args[len(os.Args)-1] != "json" {
		os.Exit(2)
	}
	query := os.Args[2]
	session := sessionID(query)
	cwd := os.Getenv("CMUX_FAKE_CWD")
	if strings.Contains(query, "FROM session") {
		write([]any{map[string]string{"id": session, "directory": cwd}})
		return
	}
	if strings.Contains(query, "FROM part") {
		prompt, _ := os.ReadFile(os.Getenv("CMUX_FAKE_PROMPT"))
		response := `{"kind":"handoff","handoff":{"summary":"done","evidence":["test"],"next":"review"}}`
		intentText := strings.TrimPrefix(string(prompt), os.Getenv("CMUX_FAKE_PROMPT_PREFIX"))
		_, intentText, _ = strings.Cut(intentText, "\n")
		first, _, _ := strings.Cut(intentText, "\nAgentFlow result protocol:")
		var intent struct {
			Purpose    string   `json:"purpose"`
			Acceptance []string `json:"acceptance"`
		}
		if json.Unmarshal([]byte(first), &intent) == nil && intent.Purpose == "review" {
			accepted := make([]bool, len(intent.Acceptance))
			for i := range accepted {
				accepted[i] = true
			}
			encoded, _ := json.Marshal(map[string]any{"kind": "approve", "accepted": accepted})
			response = string(encoded)
		}
		write([]any{map[string]string{"text": response}})
		return
	}
	if strings.Contains(query, "instr(") {
		prompt, _ := os.ReadFile(os.Getenv("CMUX_FAKE_PROMPT"))
		prompt = []byte(strings.TrimPrefix(string(prompt), os.Getenv("CMUX_FAKE_PROMPT_PREFIX")))
		marker, _, _ := strings.Cut(string(prompt), "\n")
		if _, err := os.Stat(os.Getenv("CMUX_FAKE_ENTER")); err == nil && strings.Contains(query, sqlLiteral(marker)) {
			write([]any{map[string]string{"id": "user"}})
		} else {
			write([]any{})
		}
		return
	}
	if strings.Contains(query, "FROM message") && strings.Contains(query, "AND json_extract(data, '$.parentID') =") {
		if _, err := os.Stat(os.Getenv("CMUX_FAKE_ENTER")); err == nil {
			write([]any{map[string]string{"id": "assistant"}})
		} else {
			write([]any{})
		}
		return
	}
	if strings.Contains(query, "FROM message") {
		write([]any{map[string]any{"id": "ready", "parent_id": "", "role": "assistant", "finish": "stop", "completed": 1}})
		return
	}
	os.Exit(2)
}
