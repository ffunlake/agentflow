package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"agentflow/internal/config"
	"agentflow/internal/layout"
)

// SourcePath is metadata: equivalent YAML/JSON files share Config and Layout.
type teamPlan struct {
	SourcePath string           `json:"source_path"`
	Config     *config.Resolved `json:"config"`
	Layout     *layout.Node     `json:"layout"`
}

type teamDiagnostic struct {
	Code    string `json:"code"`
	Path    string `json:"path"`
	Message string `json:"message"`
}

type toolLookup struct {
	Tool       string   `json:"tool"`
	Agents     []string `json:"agents"`
	Status     string   `json:"status"`
	Executable string   `json:"executable,omitempty"`
}

type doctorReport struct {
	SourcePath           string           `json:"source_path"`
	Status               string           `json:"status"`
	Configuration        string           `json:"configuration"`
	Layout               string           `json:"layout"`
	Tools                []toolLookup     `json:"tools"`
	RuntimeCommunication string           `json:"runtime_communication"`
	Diagnostics          []teamDiagnostic `json:"diagnostics"`
}

func runTeamCommand(command string, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	var usage bytes.Buffer
	flags.SetOutput(&usage)
	file := flags.String("f", "", "team configuration file (.yaml, .yml or .json), required")
	tool := flags.String("tool", "", "override default tool: opencode, codex or claude; agent.tool takes precedence")
	var asJSON bool
	if command != "validate" {
		flags.BoolVar(&asJSON, "json", false, "output stable JSON")
	}
	flags.Usage = func() {
		fmt.Fprintf(&usage, "Usage: agentflow %s -f <config> [--tool <opencode|codex|claude>]", command)
		if command != "validate" {
			usage.WriteString(" [--json]")
		}
		usage.WriteString("\n\n")
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return writeTeamOutput(stdout, stderr, usage.String())
		}
		fmt.Fprintf(stderr, "agentflow: error: %s arguments: %v\n", command, err)
		return 2
	}
	if flags.NArg() != 0 || strings.TrimSpace(*file) == "" {
		fmt.Fprintf(stderr, "agentflow: error: %s requires -f <config> and accepts no positional arguments\n", command)
		return 2
	}
	invalidTool := false
	flags.Visit(func(f *flag.Flag) {
		if f.Name == "tool" && *tool != "opencode" && *tool != "codex" && *tool != "claude" {
			invalidTool = true
		}
	})
	if invalidTool {
		fmt.Fprintln(stderr, "agentflow: error: --tool must be opencode, codex or claude")
		return 2
	}

	source, err := filepath.Abs(*file)
	if err != nil {
		fmt.Fprintf(stderr, "agentflow: error: config file %q: %v\n", *file, err)
		return 1
	}
	report := doctorReport{
		SourcePath: source, Status: "FAILED", Configuration: "NOT_CHECKED", Layout: "NOT_CHECKED",
		Tools: []toolLookup{}, RuntimeCommunication: "UNVERIFIED", Diagnostics: []teamDiagnostic{},
	}
	plan, err := loadTeamPlan(source, *tool, &report)
	if err != nil {
		diagnostic := teamDiagnostic{Code: "CONFIG_INVALID", Path: "$", Message: err.Error()}
		var field *config.FieldError
		if errors.As(err, &field) {
			diagnostic.Path = field.Path
		}
		if report.Layout == "INVALID" {
			diagnostic.Code = "LAYOUT_INVALID"
		}
		report.Diagnostics = append(report.Diagnostics, diagnostic)
		if asJSON {
			if code := writeTeamJSON(stdout, stderr, report); code != 0 {
				return code
			}
		} else if command == "doctor" {
			if code := writeTeamOutput(stdout, stderr, formatDoctor(report)); code != 0 {
				return code
			}
		}
		fmt.Fprintf(stderr, "agentflow: error: config file %q: %v\n", source, err)
		return 1
	}
	switch command {
	case "validate":
		return writeTeamOutput(stdout, stderr, fmt.Sprintf("Configuration valid: %s (configuration and layout)\n", source))
	case "plan":
		if asJSON {
			return writeTeamJSON(stdout, stderr, plan)
		}
		return writeTeamOutput(stdout, stderr, formatPlan(plan))
	default: // doctor never executes a discovered binary, including help/version.
		lookupTeamTools(plan.Config, &report)
		if asJSON {
			if code := writeTeamJSON(stdout, stderr, report); code != 0 {
				return code
			}
		} else if code := writeTeamOutput(stdout, stderr, formatDoctor(report)); code != 0 {
			return code
		}
		for _, d := range report.Diagnostics {
			fmt.Fprintf(stderr, "agentflow: error: config file %q: %s: %s\n", source, d.Path, d.Message)
		}
		if report.Status != "OK" {
			return 1
		}
		return 0
	}
}

func loadTeamPlan(source, tool string, report *doctorReport) (*teamPlan, error) {
	report.Configuration = "INVALID"
	c, err := config.Load(source)
	if err != nil {
		return nil, err
	}
	resolved, err := config.Resolve(c, config.ResolveOptions{Tool: tool})
	if err != nil {
		return nil, err
	}
	report.Configuration = "VALID"
	report.Layout = "INVALID"
	compiled, err := layout.Compile(resolved)
	if err != nil {
		return nil, err
	}
	report.Layout = "VALID"
	return &teamPlan{SourcePath: source, Config: resolved, Layout: compiled}, nil
}

func lookupTeamTools(snapshot *config.Resolved, report *doctorReport) {
	users := make(map[string][]string)
	for id, agent := range snapshot.Agents {
		users[agent.Tool] = append(users[agent.Tool], id)
	}
	tools := make([]string, 0, len(users))
	for tool := range users {
		tools = append(tools, tool)
	}
	sort.Strings(tools)
	for _, tool := range tools {
		sort.Strings(users[tool])
		result := toolLookup{Tool: tool, Agents: users[tool], Status: "FOUND"}
		path, err := exec.LookPath(tool)
		if err == nil {
			path, err = filepath.Abs(path)
		}
		if err != nil {
			result.Status = "UNAVAILABLE"
			code := "EXECUTABLE_LOOKUP_FAILED"
			if errors.Is(err, exec.ErrNotFound) {
				result.Status, code = "MISSING", "EXECUTABLE_MISSING"
			} else if errors.Is(err, os.ErrPermission) {
				code = "PERMISSION_DENIED"
			}
			report.Diagnostics = append(report.Diagnostics, teamDiagnostic{
				Code: code, Path: "tools." + tool + ".executable", Message: err.Error(),
			})
		} else {
			result.Executable = path
		}
		report.Tools = append(report.Tools, result)
	}
	if len(report.Diagnostics) == 0 {
		report.Status = "OK"
	}
}

func formatPlan(plan *teamPlan) string {
	var out strings.Builder
	c := plan.Config
	fmt.Fprintf(&out, "Plan: %s\nConfiguration: %s\nTerminal: %s\nLead: %s\nContext directory: %s\nDigest: %s\nAgents:\n", c.Name, plan.SourcePath, c.Terminal, c.Lead, c.ContextDir, c.Digest)
	ids := make([]string, 0, len(c.Agents))
	for id := range c.Agents {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		a := c.Agents[id]
		fmt.Fprintf(&out, "  %s: tool=%s role=%q permissions=%s session=%s\n    cwd: %s\n    prompt: %q (%d bytes)\n", id, a.Tool, a.Role, a.Permissions, a.Session, a.Cwd, promptSummary(a.Prompt), len(a.Prompt))
		if a.PromptFile != "" {
			fmt.Fprintf(&out, "    prompt_file: %s\n", a.PromptFile)
		}
		if a.Model != "" || len(a.Args) != 0 {
			fmt.Fprintf(&out, "    model: %q args: %q\n", a.Model, a.Args)
		}
	}
	out.WriteString("Layout:\n")
	var visit func(*layout.Node, string)
	visit = func(node *layout.Node, indent string) {
		if node.Agent != nil {
			fmt.Fprintf(&out, "%sagent: %s\n", indent, node.Agent.ID)
			return
		}
		fmt.Fprintf(&out, "%ssplit: %s ratio=%g\n", indent, node.Direction, node.Ratio)
		for i := range node.Children {
			visit(&node.Children[i], indent+"  ")
		}
	}
	visit(plan.Layout, "  ")
	return out.String()
}

func promptSummary(prompt string) string {
	summary := strings.Join(strings.Fields(prompt), " ")
	if utf8.RuneCountInString(summary) > 120 {
		summary = string([]rune(summary)[:120]) + "…"
	}
	return summary
}

func formatDoctor(report doctorReport) string {
	var out strings.Builder
	fmt.Fprintf(&out, "Doctor: %s\nConfiguration: %s (%s)\nLayout: %s\nTool executable lookup:\n", report.Status, report.SourcePath, report.Configuration, report.Layout)
	if len(report.Tools) == 0 {
		out.WriteString("  NOT_CHECKED\n")
	}
	for _, tool := range report.Tools {
		fmt.Fprintf(&out, "  %s: %s agents=%s", tool.Tool, tool.Status, strings.Join(tool.Agents, ","))
		if tool.Executable != "" {
			fmt.Fprintf(&out, " path=%s", tool.Executable)
		}
		out.WriteByte('\n')
	}
	fmt.Fprintf(&out, "Runtime communication: %s (executable lookup only; no tools launched)\n", report.RuntimeCommunication)
	return out.String()
}

func writeTeamJSON(stdout, stderr io.Writer, value any) int {
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		fmt.Fprintf(stderr, "agentflow: error: write output: %v\n", err)
		return 1
	}
	return 0
}

func writeTeamOutput(stdout, stderr io.Writer, output string) int {
	if _, err := io.WriteString(stdout, output); err != nil {
		fmt.Fprintf(stderr, "agentflow: error: write output: %v\n", err)
		return 1
	}
	return 0
}
