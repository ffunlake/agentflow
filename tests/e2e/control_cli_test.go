package e2e_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"agentflow/internal/config"
	"agentflow/internal/runtime"
	"agentflow/internal/store"
)

func TestControlCLIProcessOffline(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(t.TempDir(), "agentflow")
	build := exec.Command("go", "build", "-mod=readonly", "-o", binary, "./cmd/agentflow")
	build.Dir = root
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, output)
	}
	dir := shortRunDir(t)
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	c, err := config.Load(filepath.Join(root, "examples/team.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := config.Resolve(c, config.ResolveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := runtime.Compile(resolved, runtime.Options{})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := runtime.NewSnapshot(plan, "offline-run")
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.WriteSnapshot(filepath.Join(dir, "config.resolved.json"), snapshot); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.NewRegistry(t.Context(), db, snapshot); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateSeat(t.Context(), store.Seat{RunID: "offline-run", AgentID: "main", Generation: 1, Current: true, Tool: resolved.Agents["main"].Tool, NativeSessionID: "offline-native", WorkspaceID: "offline-workspace", SurfaceID: "offline-surface", Status: "READY"}); err != nil {
		t.Fatal(err)
	}
	db.Close()
	startCoordinatorProcess(t, binary, dir, "offline-run")
	tools := t.TempDir()
	marker := filepath.Join(tools, "launched")
	for _, tool := range []string{"opencode", "codex", "claude", "cmux"} {
		if err := os.WriteFile(filepath.Join(tools, tool), []byte("#!/bin/sh\nprintf launched > \"$AGENTFLOW_TEST_MARKER\"\nexit 99\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	var firstStop string
	for _, command := range []string{"status", "attach", "stop", "stop", "status"} {
		args := []string{command, "--run", "offline-run", "--run-dir", dir, "--json"}
		if command == "stop" {
			args = append(args, "--agent", "main", "--generation", "1", "--request-id", "stop")
		}
		cmd := exec.Command(binary, args...)
		for _, entry := range os.Environ() {
			if !strings.HasPrefix(entry, "PATH=") && !strings.HasPrefix(entry, "AGENTFLOW_TEST_MARKER=") {
				cmd.Env = append(cmd.Env, entry)
			}
		}
		cmd.Env = append(cmd.Env, "PATH="+tools, "AGENTFLOW_TEST_MARKER="+marker)
		var out, diagnostic bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &diagnostic
		err := cmd.Run()
		if command == "attach" {
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 1 || !strings.Contains(out.String(), `"attached": false`) {
				t.Fatalf("%v %s %s", err, &out, &diagnostic)
			}
		} else if err != nil || diagnostic.Len() != 0 {
			t.Fatalf("%v %s %s", err, &out, &diagnostic)
		}
		if !json.Valid(out.Bytes()) {
			t.Fatal(out.String())
		}
		if command == "stop" {
			if firstStop != "" && firstStop != out.String() {
				t.Fatal("stop response changed across processes")
			}
			firstStop = out.String()
		}
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("control CLI executed native tool")
	}
	db, err = store.OpenExisting(filepath.Join(dir, "state.db"), false)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	events, err := db.JournalAfter(t.Context(), "offline-run", 0)
	if err != nil || len(events) != 2 || events[0].Event != store.StopRequested || events[1].Event != "STOP_REQUEST_ACCEPTED" {
		t.Fatalf("%+v %v", events, err)
	}
}
