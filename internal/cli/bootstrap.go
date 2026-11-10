package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"agentflow/internal/config"
	runcontext "agentflow/internal/context"
	"agentflow/internal/ipc"
	"agentflow/internal/runtime"
	"agentflow/internal/store"
)

func runBootstrap(command string, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(stderr)
	runID := flags.String("run", "", "existing run ID, required")
	agent := flags.String("agent", "", "logical agent ID, required")
	generation := flags.Int64("generation", 0, "current seat generation, required")
	runDir := flags.String("run-dir", "", "existing run directory (defaults to XDG state)")
	asJSON := flags.Bool("json", false, "output stable JSON")
	flags.Usage = func() {
		fmt.Fprintf(stderr, "Usage: agentflow %s --run <id> --agent <id> --generation <n> [--run-dir <dir>] [--json]\nGenerates/reads a bootstrap file only; native session recovery is UNVERIFIED.\n", command)
	}
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 || *runID == "" || strings.ContainsAny(*runID, "/\\\x00") || *runID == "." || *runID == ".." || !entryAgentID.MatchString(*agent) || *generation < 1 {
		fmt.Fprintln(stderr, "agentflow: error: rebuild/bootstrap requires --run, --agent and current --generation; no positional arguments")
		return 2
	}
	dir := *runDir
	if dir == "" {
		root := os.Getenv("XDG_STATE_HOME")
		if root == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				fmt.Fprintln(stderr, "agentflow: error:", err)
				return 1
			}
			root = filepath.Join(home, ".local", "state")
		}
		dir = filepath.Join(root, "agentflow", "runs", *runID)
	}
	dir, err := filepath.Abs(dir)
	var result *runtime.BootstrapResult
	if err == nil {
		result, err = queryBootstrap(dir, ipc.Actor{RunID: *runID, AgentID: *agent, Generation: *generation})
	}
	if err != nil {
		if *asJSON {
			if code := writeTeamJSON(stdout, stderr, struct {
				Status               string `json:"status"`
				Executed             bool   `json:"executed"`
				RuntimeCommunication string `json:"runtime_communication"`
				Error                string `json:"error"`
			}{Status: "FAILED", RuntimeCommunication: "UNVERIFIED", Error: err.Error()}); code != 0 {
				return code
			}
		}
		fmt.Fprintln(stderr, "agentflow: error:", err)
		return 1
	}
	if *asJSON {
		return writeTeamJSON(stdout, stderr, result)
	}
	return writeTeamOutput(stdout, stderr, fmt.Sprintf("Status: %s\nBootstrap: %s\nExecuted: false\nRuntime communication: UNVERIFIED\nRecommendation: %s\nNext: %s\n", result.Status, result.Path, result.Bootstrap.Recommendation, result.Bootstrap.Next))
}

func queryBootstrap(dir string, actor ipc.Actor) (*runtime.BootstrapResult, error) {
	db, err := store.OpenExisting(filepath.Join(dir, "state.db"), true)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	r, err := db.GetRun(context.Background())
	if err != nil {
		return nil, err
	}
	if r.RunID != actor.RunID {
		return nil, fmt.Errorf("%w: requested run differs from store", runcontext.ErrIdentity)
	}
	var frozen config.Resolved
	if err := json.Unmarshal([]byte(r.ConfigSnapshot), &frozen); err != nil {
		return nil, err
	}
	run, err := runcontext.Open(frozen.ContextDir, actor.RunID)
	if err != nil {
		return nil, err
	}
	svc, err := runtime.NewBootstrapService(db, run)
	if err != nil {
		return nil, err
	}
	return svc.Bootstrap(context.Background(), actor)
}
