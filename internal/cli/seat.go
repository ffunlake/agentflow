package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"agentflow/internal/runtime"
)

var entryAgentID = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,47}$`)

// parseSeatArgs accepts exactly one of each long option, in either order. In
// particular flag aliases, duplicate options, positional args and -- are errors.
// Errors never echo untrusted argument values.
func parseSeatArgs(args []string) (snapshot, agent string, err error) {
	if len(args) != 4 {
		return "", "", errors.New("_seat requires --snapshot <absolute> --agent <id>")
	}
	seen := map[string]bool{}
	for i := 0; i < len(args); i += 2 {
		key, value := args[i], args[i+1]
		if (key != "--snapshot" && key != "--agent") || seen[key] || value == "" || strings.ContainsRune(value, '\x00') {
			return "", "", errors.New("invalid _seat options")
		}
		seen[key] = true
		if key == "--snapshot" {
			snapshot = value
		} else {
			agent = value
		}
	}
	if !filepath.IsAbs(snapshot) || !entryAgentID.MatchString(agent) {
		return "", "", errors.New("_seat requires an absolute snapshot path and a valid agent ID")
	}
	return snapshot, agent, nil
}

func runSeat(args []string, stdout, stderr io.Writer) int {
	path, id, err := parseSeatArgs(args)
	if err != nil {
		fmt.Fprintf(stderr, "agentflow: error: %s\n", err)
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	var options runtime.NativeOptions
	// Missing/invalid explicit authorization fails before executable lookup or
	// native execution. Never infer the target from the focused terminal.
	if raw := os.Getenv("AGENTFLOW_NATIVE_OPTIONS"); raw != "" {
		d := json.NewDecoder(strings.NewReader(raw))
		d.DisallowUnknownFields()
		if d.Decode(&options) != nil || d.Decode(new(any)) != io.EOF {
			options = runtime.NativeOptions{}
		}
	}
	execution := runtime.ExecuteNativeSeat(ctx, path, id, options)
	result := struct {
		Status   string                 `json:"status"`
		Executed bool                   `json:"executed"`
		Native   *runtime.NativeBinding `json:"native,omitempty"`
		Claim    *runtime.Registration  `json:"claim,omitempty"`
		Error    *runtime.SeatError     `json:"error,omitempty"`
	}{Status: execution.Status, Executed: execution.Executed, Claim: execution.Claim, Error: execution.Error}
	if execution.Native.SessionID != "" {
		result.Native = &execution.Native
	}
	if err := json.NewEncoder(stdout).Encode(result); err != nil {
		fmt.Fprintln(stderr, "agentflow: error: write output failed")
		return 1
	}
	if execution.Error != nil {
		fmt.Fprintf(stderr, "agentflow: error: %s\n", execution.Error)
		return 1
	}
	return 0
}
