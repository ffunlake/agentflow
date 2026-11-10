package cli

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"agentflow/internal/config"
	"agentflow/internal/runtime"
)

func runUp(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("up", flag.ContinueOnError)
	var usage bytes.Buffer
	flags.SetOutput(&usage)
	file := flags.String("f", "", "team configuration file (.yaml, .yml or .json), required")
	tool := flags.String("tool", "", "override default tool: opencode, codex or claude; agent.tool takes precedence")
	dryRun := flags.Bool("dry-run", false, "output a startup plan without launching processes or writing state")
	asJSON := flags.Bool("json", false, "output stable JSON")
	var real realUpOptions
	flags.BoolVar(&real.headless, "headless-codex", false, "explicit headless Codex process adapter; no cmux operations")
	flags.BoolVar(&real.headlessOpenCode, "headless-opencode", false, "explicit headless OpenCode process adapter; no cmux operations")
	flags.BoolVar(&real.headlessClaude, "headless-claude", false, "explicit headless Claude Code process adapter; no cmux operations")
	flags.DurationVar(&real.turnTimeout, "turn-timeout", 85*time.Second, "headless native turn deadline in (0,30m]")
	flags.StringVar(&real.runID, "run", "", "new run ID, required for real startup")
	flags.StringVar(&real.task, "task", "", "initial workflow task; included in the first builder dispatch")
	flags.StringVar(&real.runDir, "run-dir", "", "private run directory (default: unique os.TempDir directory; reuse output run_dir to reopen)")
	flags.StringVar(&real.window, "window", "", "optional cmux target window (default: caller's window)")
	flags.StringVar(&real.workspace, "workspace", "", "unsupported: up always creates a new cmux workspace")
	flags.StringVar(&real.surface, "surface", "", "unsupported: up creates new terminal surfaces")
	flags.StringVar(&real.nativeBinary, "native-binary", "", "absolute native executable (default: real PATH lookup)")
	flags.StringVar(&real.cmuxBinary, "cmux-binary", "", "absolute cmux executable (default: real PATH lookup)")
	for _, name := range []string{"codex", "opencode", "claude"} {
		flags.Func(name+"-binary", "absolute "+name+" executable for cmux TUI launch", func(value string) error {
			if value == "" {
				return errors.New("tool binary override must be non-empty")
			}
			if real.toolBinaries == nil {
				real.toolBinaries = make(map[string]string)
			}
			real.toolBinaries[name] = value
			return nil
		})
	}
	flags.DurationVar(&real.timeout, "startup-timeout", 85*time.Second, "startup deadline: cmux <=90s; headless <=30m (headless default 5m)")
	flags.Usage = func() {
		usage.WriteString("Usage: agentflow up -f <config> [--dry-run] [--tool <opencode|codex|claude>] [--json]\nReal startup: --run ID [--task TEXT] [--window REF] [--run-dir DIR]\n\n")
		flags.PrintDefaults()
		usage.WriteString("\nDefault real up creates a new cmux workspace from layout and starts interactive tools. If the frozen config contains workflow, up also starts the foreground cmux coordinator and keeps running; otherwise it returns after LAUNCHED. Native READY and runtime communication remain UNVERIFIED. Explicit --headless-codex, --headless-opencode or --headless-claude supports same-tool persistent logical seats and workflow results without cmux. Capability remains PARTIAL.\n")
	}
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return writeTeamOutput(stdout, stderr, usage.String())
		}
		fmt.Fprintf(stderr, "agentflow: error: up arguments: %v\n", err)
		return 2
	}
	if flags.NArg() != 0 || strings.TrimSpace(*file) == "" {
		fmt.Fprintln(stderr, "agentflow: error: up requires -f <config> and accepts no positional arguments")
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
	if conflictingHeadless(real.headless, real.headlessOpenCode, real.headlessClaude) {
		fmt.Fprintln(stderr, "agentflow: error: headless tool flags are mutually exclusive")
		return 2
	}
	if real.isHeadless() {
		explicitDeadline := false
		flags.Visit(func(f *flag.Flag) {
			if f.Name == "startup-timeout" {
				explicitDeadline = true
			}
		})
		if !explicitDeadline {
			real.timeout = 5 * time.Minute
		}
	}
	if !*dryRun {
		return runRealUp(*file, *tool, real, *asJSON, stdout, stderr)
	}
	c, err := config.Load(*file)
	if err != nil {
		fmt.Fprintf(stderr, "agentflow: error: config file %q: %v\n", *file, err)
		return 1
	}
	resolved, err := config.Resolve(c, config.ResolveOptions{Tool: *tool})
	if err != nil {
		fmt.Fprintf(stderr, "agentflow: error: config file %q: %v\n", *file, err)
		return 1
	}
	plan, err := runtime.Compile(resolved, runtime.Options{})
	if err != nil {
		fmt.Fprintf(stderr, "agentflow: error: config file %q: %v\n", resolved.SourcePath, err)
		return 1
	}
	if *asJSON {
		return writeTeamJSON(stdout, stderr, plan)
	}
	return writeTeamOutput(stdout, stderr, formatStartupPlan(plan))
}

func formatStartupPlan(plan *runtime.StartupPlan) string {
	var out strings.Builder
	fmt.Fprintf(&out, "Startup plan: %s (S4.1 partial)\nStatus: %s\nExecuted: %t\nTerminal: %s\nLead: %s\nContext directory: %s\nDigest: %s\nSnapshot artifact: %s (relative to future run directory; not written)\nSeats (sorted by agent ID):\n",
		plan.Run.Name, plan.Status, plan.Executed, plan.Run.Terminal, plan.Run.Lead,
		plan.Run.ContextDir, plan.Run.ConfigDigest, plan.SnapshotPath)
	for _, seat := range plan.Seats {
		fmt.Fprintf(&out, "  %d %s: tool=%s role=%q permissions=%s session=%s\n    cwd: %s\n    prompt text ref: %s\n",
			seat.Order, seat.ID, seat.Tool, seat.Role, seat.Permissions, seat.Session, seat.Cwd, seat.Prompt.TextRef)
		if seat.Prompt.File != "" {
			fmt.Fprintf(&out, "    prompt file: %s\n", seat.Prompt.File)
		}
		fmt.Fprintf(&out, "    wrapper binary: %q\n    wrapper argv: %q\n    pane path:", seat.Wrapper.Binary, seat.Wrapper.Args)
		if len(seat.PanePath) == 0 {
			out.WriteString(" root")
		}
		for _, step := range seat.PanePath {
			fmt.Fprintf(&out, " %s ratio=%g child=%d;", step.Direction, step.Ratio, step.Child)
		}
		out.WriteByte('\n')
	}
	out.WriteString("Plan only; no processes launched or state written. Default real up launches a new cmux workspace and persists surface registration; headless requires explicit --headless-codex, --headless-opencode or --headless-claude startup. Native READY, runtime communication and effective permissions remain UNVERIFIED.\n")
	return out.String()
}
