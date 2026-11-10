package runtime

import (
	"errors"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"agentflow/internal/config"
)

// TUICommand starts an interactive tool with frozen role instructions. It is
// argv data only: prompts/models/extensions never pass through a shell. Session
// policy is retained in the snapshot; resume/reset ownership is not implemented.
func TUICommand(a config.ResolvedAgent, binary string) (Command, error) {
	for _, value := range append([]string{a.Role, a.Prompt, a.Model, a.Cwd, binary}, a.Args...) {
		if strings.ContainsRune(value, '\x00') || !utf8.ValidString(value) {
			return Command{}, errors.New("invalid TUI command text")
		}
	}
	if !identityText(a.Role) || !identityText(a.Prompt) {
		return Command{}, errors.New("TUI role and prompt are required")
	}
	// Reuse the native adapter's tool/permission/model/extension allowlist.
	if _, err := NativeCommand(a, binary); err != nil {
		return Command{}, err
	}
	if strings.Contains(filepath.ToSlash(binary), "/cmux-cli-shims/") {
		return Command{}, errors.New("TUI binary must not be a cmux routing shim")
	}
	prompt := "You are a managed AgentFlow seat.\nUse only the frozen run, agent and workflow identity supplied by AgentFlow. Do not discover, bind, reset or create cmux windows or other agent sessions. Do not run external collaboration initialization. Wait for workflow messages and report results through the current seat.\nRole: " + a.Role + "\nFrozen role instructions:\n" + a.Prompt
	var args []string
	switch a.Tool {
	case "codex":
		args = []string{"--cd", a.Cwd}
	case "opencode":
		if len(a.Args) != 0 {
			return Command{}, errors.New("OpenCode TUI extensions are unsupported")
		}
		args = []string{a.Cwd}
	case "claude":
		mode := "--bare"
		if len(a.Args) != 0 {
			mode = a.Args[0]
		}
		args = []string{mode, "--strict-mcp-config", "--setting-sources", "", "--disable-slash-commands", "--no-chrome"}
	}
	if a.Permissions == "full" {
		switch a.Tool {
		case "codex":
			args = append(args, "--dangerously-bypass-approvals-and-sandbox")
		case "opencode":
			args = append(args, "--auto")
		case "claude":
			args = append(args, "--dangerously-skip-permissions")
		}
	}
	if a.Model != "" {
		args = append(args, "--model", a.Model)
	}
	if a.Tool == "codex" {
		args = append(args, a.Args...)
	}
	if a.Tool == "opencode" {
		args = append(args, "--prompt", prompt)
	} else {
		args = append(args, "--", prompt)
	}
	return Command{Binary: binary, Args: args}, nil
}

// LoadTUICommand validates the private startup artifact again inside the new
// terminal. It deliberately does not produce a registration or READY claim.
func LoadTUICommand(path, id, binary string) (Command, string, error) {
	seat, err := LoadStartupSeat(path, id)
	if err != nil {
		return Command{}, "", err
	}
	snapshot, err := ReadSnapshot(path)
	if err != nil {
		return Command{}, "", err
	}
	command, err := TUICommand(snapshot.Config.Agents[id], binary)
	return command, seat.Cwd, err
}
