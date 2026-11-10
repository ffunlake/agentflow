// Package config loads and resolves team configuration without starting processes.
// Layout geometry and compilation belong to S2.2; this package checks only its
// schema and agent references, so successful validation is not a layout plan.
package config

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Config is the source schema. Optional overrides use pointers so an explicitly
// empty value cannot silently fall back to a default. SourcePath is set by Load.
type Config struct {
	Version    int              `yaml:"version" json:"version"`
	Name       string           `yaml:"name" json:"name"`
	Terminal   string           `yaml:"terminal" json:"terminal"`
	Lead       string           `yaml:"lead" json:"lead"`
	ContextDir string           `yaml:"context_dir" json:"context_dir"`
	Defaults   Defaults         `yaml:"defaults" json:"defaults"`
	Agents     map[string]Agent `yaml:"agents" json:"agents"`
	Layout     *Layout          `yaml:"layout" json:"layout"`
	Workflow   *Workflow        `yaml:"workflow" json:"workflow,omitempty"`
	SourcePath string           `yaml:"-" json:"-"`
}

type Defaults struct {
	Tool        *string `yaml:"tool" json:"tool,omitempty"`
	Cwd         *string `yaml:"cwd" json:"cwd,omitempty"`
	Permissions *string `yaml:"permissions" json:"permissions,omitempty"`
	Session     *string `yaml:"session" json:"session,omitempty"`
}

type Agent struct {
	Role        string   `yaml:"role" json:"role"`
	Prompt      *string  `yaml:"prompt" json:"prompt,omitempty"`
	PromptFile  *string  `yaml:"prompt_file" json:"prompt_file,omitempty"`
	Tool        *string  `yaml:"tool" json:"tool,omitempty"`
	Cwd         *string  `yaml:"cwd" json:"cwd,omitempty"`
	Permissions *string  `yaml:"permissions" json:"permissions,omitempty"`
	Session     *string  `yaml:"session" json:"session,omitempty"`
	Model       string   `yaml:"model" json:"model,omitempty"`
	Args        []string `yaml:"args" json:"args,omitempty"`
}

// Layout retains the source tree, including omitted ratios. S2.1 deliberately
// does not default ratios or validate split direction, arity or ratio bounds.
type Layout struct {
	Agent    string   `yaml:"agent" json:"agent,omitempty"`
	Split    string   `yaml:"split" json:"split,omitempty"`
	Ratio    *float64 `yaml:"ratio" json:"ratio,omitempty"`
	Children []Layout `yaml:"children" json:"children,omitempty"`
}

// FieldError provides a machine-readable field path and preserves I/O causes.
type FieldError struct {
	Path    string
	Message string
	Cause   error
}

func (e *FieldError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("%s: %s: %v", e.Path, e.Message, e.Cause)
	}
	return e.Path + ": " + e.Message
}

func (e *FieldError) Unwrap() error { return e.Cause }

func invalid(path, message string) error {
	return &FieldError{Path: path, Message: message}
}

var agentID = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,47}$`)

// Validate checks source values and exactly-once layout references. It does not
// read prompt files, check filesystem existence or validate layout geometry.
func Validate(c *Config) error {
	if c == nil {
		return invalid("$", "configuration is required")
	}
	if c.Version != 1 {
		return invalid("version", "must be 1")
	}
	for _, field := range []struct{ path, value string }{
		{"name", c.Name}, {"terminal", c.Terminal}, {"lead", c.Lead}, {"context_dir", c.ContextDir},
	} {
		if strings.TrimSpace(field.value) == "" {
			return invalid(field.path, "must be non-empty")
		}
	}
	if c.Terminal != "cmux" {
		return invalid("terminal", "must be cmux")
	}
	if err := validateOptions("defaults", c.Defaults.Tool, c.Defaults.Cwd, c.Defaults.Permissions, c.Defaults.Session); err != nil {
		return err
	}
	if len(c.Agents) == 0 {
		return invalid("agents", "must contain at least one agent")
	}
	if _, ok := c.Agents[c.Lead]; !ok {
		return invalid("lead", "must reference an existing agent")
	}
	ids := sortedIDs(c.Agents)
	for _, id := range ids {
		path := "agents." + id
		if !agentID.MatchString(id) {
			return invalid(path, "ID must match [a-z][a-z0-9_-]{0,47}")
		}
		a := c.Agents[id]
		if strings.TrimSpace(a.Role) == "" {
			return invalid(path+".role", "must be non-empty")
		}
		if (a.Prompt == nil) == (a.PromptFile == nil) {
			return invalid(path+".prompt", "exactly one of prompt and prompt_file is required")
		}
		for _, field := range []struct {
			name  string
			value *string
		}{{"prompt", a.Prompt}, {"prompt_file", a.PromptFile}} {
			if field.value != nil && strings.TrimSpace(*field.value) == "" {
				return invalid(path+"."+field.name, "must be non-empty")
			}
		}
		if err := validateOptions(path, a.Tool, a.Cwd, a.Permissions, a.Session); err != nil {
			return err
		}
	}
	if c.Layout == nil {
		return invalid("layout", "is required")
	}
	seen := make(map[string]string, len(c.Agents))
	var visit func(Layout, string) error
	visit = func(node Layout, path string) error {
		if node.Agent != "" {
			if _, ok := c.Agents[node.Agent]; !ok {
				return invalid(path+".agent", "references unknown agent "+node.Agent)
			}
			if previous, ok := seen[node.Agent]; ok {
				return invalid(path+".agent", "duplicate agent reference; first at "+previous)
			}
			seen[node.Agent] = path + ".agent"
		}
		for i, child := range node.Children {
			if err := visit(child, fmt.Sprintf("%s.children[%d]", path, i)); err != nil {
				return err
			}
		}
		return nil
	}
	if err := visit(*c.Layout, "layout"); err != nil {
		return err
	}
	for _, id := range ids {
		if _, ok := seen[id]; !ok {
			return invalid("layout", "missing agent reference "+id)
		}
	}
	return ValidateWorkflow(c.Workflow, ids, c.Lead)
}

func validateOptions(path string, tool, cwd, permissions, session *string) error {
	for _, field := range []struct {
		name    string
		value   *string
		allowed []string
	}{
		{"tool", tool, []string{"opencode", "codex", "claude"}},
		{"permissions", permissions, []string{"full", "default"}},
		{"session", session, []string{"fresh", "persistent"}},
	} {
		if field.value == nil {
			continue
		}
		valid := false
		for _, value := range field.allowed {
			valid = valid || *field.value == value
		}
		if !valid {
			return invalid(path+"."+field.name, "must be one of "+strings.Join(field.allowed, ", "))
		}
	}
	if cwd != nil && strings.TrimSpace(*cwd) == "" {
		return invalid(path+".cwd", "must be non-empty")
	}
	return nil
}

func sortedIDs(agents map[string]Agent) []string {
	ids := make([]string, 0, len(agents))
	for id := range agents {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
