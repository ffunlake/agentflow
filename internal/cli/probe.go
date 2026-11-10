package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"time"

	"agentflow/internal/probe"
)

func runProbe(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("probe", flag.ContinueOnError)
	var usage bytes.Buffer
	flags.SetOutput(&usage)
	tool := flags.String("tool", "all", "all, opencode, codex, claude, or cmux")
	dir := flags.String("evidence-dir", "", "new directory inside current project (parent must exist)")
	timeout := flags.Duration("timeout", 10*time.Second, "per-command timeout, >0 and <=1m")
	runtime := flags.Bool("require-runtime", false, "fail unless real runtime acceptance is verified (currently unverified)")
	if err := flags.Parse(args); err != nil {
		if err == flag.ErrHelp {
			if _, err := io.Copy(stdout, &usage); err != nil {
				fmt.Fprintf(stderr, "agentflow: error: write output: %v\n", err)
				return 1
			}
			return 0
		}
		fmt.Fprintf(stderr, "agentflow: error: probe arguments: %v\n", err)
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "agentflow: error: probe does not accept positional arguments")
		return 2
	}
	report, code := probe.Run(context.Background(), probe.Options{Tool: *tool, EvidenceDir: *dir, Timeout: *timeout, RequireRuntime: *runtime})
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(report); err != nil {
		fmt.Fprintf(stderr, "agentflow: error: write output: %v\n", err)
		return 1
	}
	if code != 0 {
		fmt.Fprintln(stderr, "agentflow: error: probe incomplete; see JSON diagnostics and command evidence")
	}
	return code
}
