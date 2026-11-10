package config

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// ResolveOptions.Tool is the future CLI --tool override. Agent-specific tool
// choices take precedence. An empty option means no CLI override.
type ResolveOptions struct {
	Tool string
}

// Resolved is a detached snapshot. SourcePath is diagnostic metadata, excluded
// from JSON and Digest so equivalent YAML/JSON sources have the same digest.
// Digest is SHA-256 of this struct's JSON with the empty digest field omitted.
type Resolved struct {
	Version    int                      `json:"version"`
	Name       string                   `json:"name"`
	Terminal   string                   `json:"terminal"`
	Lead       string                   `json:"lead"`
	ContextDir string                   `json:"context_dir"`
	Agents     map[string]ResolvedAgent `json:"agents"`
	Layout     Layout                   `json:"layout"`
	Workflow   *Workflow                `json:"workflow,omitempty"`
	Digest     string                   `json:"digest,omitempty"`
	SourcePath string                   `json:"-"`
}

type ResolvedAgent struct {
	Role        string   `json:"role"`
	Prompt      string   `json:"prompt"`
	PromptFile  string   `json:"prompt_file,omitempty"`
	Tool        string   `json:"tool"`
	Cwd         string   `json:"cwd"`
	Permissions string   `json:"permissions"`
	Session     string   `json:"session"`
	Model       string   `json:"model,omitempty"`
	Args        []string `json:"args"`
}

// Resolve validates again, normalizes paths relative to the configuration file,
// applies overrides, and reads prompt_file into the snapshot. It does not
// expand ~, environment variables or shell text, check tool/cwd availability,
// write files, or start processes. SourcePath must identify the source file.
func Resolve(c *Config, options ResolveOptions) (*Resolved, error) {
	if err := Validate(c); err != nil {
		return nil, err
	}
	if options.Tool != "" {
		if err := validateOptions("options", &options.Tool, nil, nil, nil); err != nil {
			return nil, err
		}
	}
	if c.SourcePath == "" {
		return nil, invalid("source_path", "is required; use Load or set the configuration file path")
	}
	abs, err := filepath.Abs(c.SourcePath)
	if err != nil {
		return nil, &FieldError{Path: "source_path", Message: "resolve config path", Cause: err}
	}
	base := filepath.Dir(abs)
	r := &Resolved{
		Version: c.Version, Name: c.Name, Terminal: c.Terminal, Lead: c.Lead,
		ContextDir: absolute(base, c.ContextDir), SourcePath: abs,
		Agents: make(map[string]ResolvedAgent, len(c.Agents)), Layout: cloneLayout(*c.Layout),
		Workflow: cloneWorkflow(c.Workflow),
	}
	tool := fallback(c.Defaults.Tool, "opencode")
	if options.Tool != "" {
		tool = options.Tool
	}
	for _, id := range sortedIDs(c.Agents) {
		a := c.Agents[id]
		resolved := ResolvedAgent{
			Role: a.Role, Tool: fallback(a.Tool, tool),
			Cwd:         absolute(base, fallback(a.Cwd, fallback(c.Defaults.Cwd, "."))),
			Permissions: fallback(a.Permissions, fallback(c.Defaults.Permissions, "full")),
			Session:     fallback(a.Session, fallback(c.Defaults.Session, "fresh")),
			Model:       a.Model, Args: append([]string{}, a.Args...),
		}
		if a.Prompt != nil {
			resolved.Prompt = *a.Prompt
		} else {
			resolved.PromptFile = absolute(base, *a.PromptFile)
			body, err := os.ReadFile(resolved.PromptFile)
			if err != nil {
				return nil, &FieldError{Path: "agents." + id + ".prompt_file", Message: "read prompt file", Cause: err}
			}
			resolved.Prompt = string(body)
		}
		if !utf8.ValidString(resolved.Prompt) || strings.TrimSpace(resolved.Prompt) == "" {
			return nil, invalid("agents."+id+".prompt", "resolved prompt must be non-empty UTF-8 text")
		}
		r.Agents[id] = resolved
	}
	body, err := json.Marshal(r)
	if err != nil {
		return nil, &FieldError{Path: "$", Message: "encode snapshot", Cause: err}
	}
	digest := sha256.Sum256(body)
	r.Digest = hex.EncodeToString(digest[:])
	return r, nil
}

func fallback(value *string, defaultValue string) string {
	if value != nil {
		return *value
	}
	return defaultValue
}

func absolute(base, path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Join(base, path)
}

func cloneLayout(node Layout) Layout {
	result := node
	if node.Ratio != nil {
		ratio := *node.Ratio
		result.Ratio = &ratio
	}
	if node.Children != nil {
		result.Children = make([]Layout, len(node.Children))
		for i, child := range node.Children {
			result.Children[i] = cloneLayout(child)
		}
	}
	return result
}
