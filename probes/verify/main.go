// This evidence recorder runs only the fixed S1 validation commands below.
// Run from the checkout using make verify-probes. It preserves real exit codes
// and raw streams instead of copying successful-looking terminal excerpts.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type result struct {
	Argv             []string          `json:"argv"`
	Overrides        map[string]string `json:"environment_overrides,omitempty"`
	StartedAt        string            `json:"started_at"`
	ExitCode         int               `json:"exit_code"`
	ExpectedExitCode int               `json:"expected_exit_code"`
	Stdout           string            `json:"stdout"`
	Stderr           string            `json:"stderr"`
}

func main() {
	if err := verify(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func verify() error {
	runID := flag.String("run-id", "validation", "fresh evidence run name (letters, digits, hyphens)")
	flag.Parse()
	if flag.NArg() != 0 || *runID == "" || strings.Trim(*runID, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-") != "" {
		return fmt.Errorf("run-id must contain only letters, digits and hyphens")
	}
	if _, err := os.Stat("go.mod"); err != nil {
		return fmt.Errorf("project root required: %w", err)
	}
	dir := filepath.Join("docs", "evidence", "S1-"+*runID)
	if err := os.Mkdir(dir, 0700); err != nil {
		return err
	}
	commands := []struct {
		name       string
		argv       []string
		overrides  map[string]string
		code       int
		diagnostic string
	}{
		{"go-test", []string{"go", "test", "./..."}, nil, 0, ""},
		{"go-build", []string{"go", "build", "./..."}, nil, 0, ""},
		{"make-check", []string{"make", "check"}, nil, 0, ""},
		{"probe-tests", []string{"go", "test", "-count=1", "-v", "./internal/probe/...", "./tests/e2e/..."}, nil, 0, ""},
		{"local-probe", []string{"./bin/agentflow", "probe", "--tool", "all", "--evidence-dir", filepath.Join(dir, "local-probe"), "--timeout", "10s"}, nil, 0, ""},
		{"runtime-guard", []string{"./bin/agentflow", "probe", "--tool", "cmux", "--evidence-dir", filepath.Join(dir, "runtime-guard"), "--require-runtime"}, nil, 1, "RUNTIME_UNVERIFIED"},
		{"missing-tool", []string{"./bin/agentflow", "probe", "--tool", "opencode", "--evidence-dir", filepath.Join(dir, "missing-tool")}, map[string]string{"PATH": ""}, 1, "EXECUTABLE_MISSING"},
		{"unavailable-cmux", []string{"./bin/agentflow", "probe", "--tool", "cmux", "--evidence-dir", filepath.Join(dir, "unavailable-cmux")}, map[string]string{"CMUX_SOCKET_PATH": filepath.Join(mustCWD(), ".cache", "s1-no-such-socket.sock")}, 1, "COMMAND_FAILED"},
	}
	for _, spec := range commands {
		r := result{Argv: spec.argv, Overrides: spec.overrides, StartedAt: time.Now().UTC().Format(time.RFC3339Nano), ExpectedExitCode: spec.code}
		cmd := exec.Command(spec.argv[0], spec.argv[1:]...)
		cmd.Env = os.Environ()
		for key, value := range spec.overrides {
			var filtered []string
			for _, entry := range cmd.Env {
				if len(entry) < len(key)+1 || entry[:len(key)+1] != key+"=" {
					filtered = append(filtered, entry)
				}
			}
			cmd.Env = append(filtered, key+"="+value)
		}
		var stdout, stderr capture
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		err := cmd.Run()
		r.Stdout, r.Stderr = string(stdout), string(stderr)
		if cmd.ProcessState != nil {
			r.ExitCode = cmd.ProcessState.ExitCode()
		} else {
			r.ExitCode = -1
			r.Stderr += fmt.Sprint(err)
		}
		data, marshalErr := json.MarshalIndent(r, "", "  ")
		if marshalErr != nil {
			return marshalErr
		}
		path := filepath.Join(dir, spec.name+".json")
		if err := os.WriteFile(path, append(data, '\n'), 0600); err != nil {
			return err
		}
		fmt.Printf("%s: exit=%d expected=%d evidence=%s\n", spec.name, r.ExitCode, spec.code, path)
		if r.ExitCode != spec.code {
			return fmt.Errorf("%s unexpected exit code; inspect evidence", spec.name)
		}
		if spec.diagnostic != "" {
			var report struct {
				Diagnostics []struct {
					Code string `json:"code"`
				} `json:"diagnostics"`
			}
			if err := json.Unmarshal(stdout, &report); err != nil {
				return fmt.Errorf("%s: invalid JSON: %w", spec.name, err)
			}
			found := false
			for _, d := range report.Diagnostics {
				if d.Code == spec.diagnostic {
					found = true
				}
			}
			if !found {
				return fmt.Errorf("%s: expected diagnostic %s not found", spec.name, spec.diagnostic)
			}
		}
	}
	return nil
}

type capture []byte

func (b *capture) Write(p []byte) (int, error) { *b = append(*b, p...); return len(p), nil }
func mustCWD() string {
	cwd, err := os.Getwd()
	if err != nil {
		panic(err)
	}
	return cwd
}
