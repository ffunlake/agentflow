package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"agentflow/internal/config"
	"agentflow/internal/coordinator"
	"agentflow/internal/ipc"
	"agentflow/internal/runtime"
	"agentflow/internal/store"
)

// The legacy foreground handshake remains an explicit API. Default CLI up now
// creates a new workspace; its executable coverage lives in cli/up_cmux_test.go.
func TestForegroundUpAPIWithNativeFixtures(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	tools := shortRunDir(t)
	binary := filepath.Join(tools, "agentflow")
	build := exec.Command("go", "build", "-mod=readonly", "-o", binary, "./cmd/agentflow")
	build.Dir = root
	if b, err := build.CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, b)
	}
	source := filepath.Join(tools, "fixture.go")
	if err := os.WriteFile(source, []byte(nativeFixtureSource), 0600); err != nil {
		t.Fatal(err)
	}
	native := filepath.Join(tools, "native")
	if b, err := exec.Command("go", "build", "-o", native, source).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, b)
	}
	for _, name := range []string{"opencode", "cmux"} {
		if err := os.Symlink(native, filepath.Join(tools, name)); err != nil {
			t.Fatal(err)
		}
	}
	for _, mode := range []string{"success", "no-ready", "stale", "nonce", "fail", "wrong-target", "wait"} {
		t.Run(mode, func(t *testing.T) {
			dir := shortRunDir(t)
			configPath := filepath.Join(tools, "team-"+mode+".json")
			body, _ := json.Marshal(map[string]any{"version": 1, "name": "real-up", "terminal": "cmux", "lead": "solo", "context_dir": filepath.Join(dir, "context"), "agents": map[string]any{"solo": map[string]any{"role": "Developer", "tool": "opencode", "cwd": root, "permissions": "default", "session": "persistent", "prompt": "Acknowledge the startup challenge only; no tools or file changes."}}, "layout": map[string]string{"agent": "solo"}})
			if err := os.WriteFile(configPath, body, 0600); err != nil {
				t.Fatal(err)
			}
			record, log := filepath.Join(dir, "fixture.json"), filepath.Join(dir, "launches")
			t.Setenv("FIXTURE_MODE", mode)
			t.Setenv("FIXTURE_RECORD", record)
			t.Setenv("FIXTURE_LAUNCH_LOG", log)
			foregroundCommand := func() *exec.Cmd {
				cmd := exec.Command(os.Args[0], "-test.run=^TestForegroundAPIHelper$")
				cmd.Env = append(os.Environ(), "AGENTFLOW_FOREGROUND_HELPER=1", "AGENTFLOW_TEST_CONFIG="+configPath, "AGENTFLOW_TEST_DIR="+dir, "AGENTFLOW_TEST_TOOLS="+tools)
				return cmd
			}
			cmd := foregroundCommand()
			var diagnostic bytes.Buffer
			cmd.Stderr = &diagnostic
			out, err := cmd.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			var r coordinator.UpResult
			decoded := make(chan error, 1)
			go func() { decoded <- json.NewDecoder(out).Decode(&r) }()
			wait := make(chan error, 1)
			select {
			case err := <-decoded:
				go func() { wait <- cmd.Wait() }()
				if err != nil {
					_ = cmd.Process.Kill()
					<-wait
					t.Fatal(err, &diagnostic)
				}
			case <-time.After(12 * time.Second):
				_ = cmd.Process.Kill()
				_ = cmd.Wait()
				t.Fatal("up result timed out")
			}
			var once sync.Once
			stop := func() {
				once.Do(func() {
					_ = cmd.Process.Signal(os.Interrupt)
					select {
					case err := <-wait:
						if err != nil {
							t.Error(err, &diagnostic)
						}
					case <-time.After(8 * time.Second):
						_ = cmd.Process.Kill()
						<-wait
						t.Error("up process did not exit")
					}
				})
			}
			if mode == "success" {
				t.Cleanup(stop)
			} else {
				err := <-wait
				var exit *exec.ExitError
				if !errors.As(err, &exit) || exit.ExitCode() != 1 || r.Status == "READY" || r.CoordinatorReady {
					t.Fatalf("%+v %v %s", r, err, &diagnostic)
				}
			}
			if mode == "success" {
				if !r.Executed || r.Status != "READY" || !r.CoordinatorReady || r.Seat == nil || r.ReadyProof == nil || len(r.ReadyProof.Nonce) != 48 || r.CapabilityStatus != "PARTIAL" || r.NativeProcess != "EXITED_AFTER_HANDSHAKE" {
					t.Fatalf("%+v", r)
				}
				verifyUpEvidence(t, r)
				inbox := exec.Command(binary, "inbox", "--run", "real-up", "--run-dir", dir, "--agent", "solo", "--generation", "1", "--json")
				if output, err := inbox.CombinedOutput(); err != nil || !strings.Contains(string(output), `"status": "QUERIED"`) {
					t.Fatal(string(output), err)
				}
				if output, err := foregroundCommand().CombinedOutput(); err == nil || !strings.Contains(string(output), "ALREADY_RUNNING") {
					t.Fatal("duplicate active up", string(output), err)
				}
				stop()
			}
			db, err := store.OpenExisting(filepath.Join(dir, "state.db"), false)
			if err != nil {
				t.Fatal(err)
			}
			seat, seatErr := db.GetSeat(t.Context(), "real-up", "solo", 1)
			events, eventErr := db.JournalAfter(t.Context(), "real-up", 0)
			db.Close()
			if eventErr != nil {
				t.Fatal(eventErr)
			}
			proofs := 0
			for _, e := range events {
				if e.Event == store.StartupReadyVerified {
					proofs++
				}
			}
			if mode == "success" {
				if seatErr != nil || seat.Status != "READY" || seat.NativeSessionID != "native-opencode" || proofs != 1 {
					t.Fatal(seat, seatErr, events)
				}
			} else if seatErr == nil || proofs != 0 {
				t.Fatal("failed up persisted READY", seat, seatErr, events)
			}
			before, err := os.ReadFile(r.SnapshotPath)
			if err != nil {
				t.Fatal(err)
			}
			evidenceBefore, _ := os.ReadFile(r.EvidencePath)
			if output, err := foregroundCommand().CombinedOutput(); err == nil || !strings.Contains(string(output), "UP_ALREADY_ATTEMPTED") {
				t.Fatal("reopened up replayed", string(output), err)
			}
			after, err := os.ReadFile(r.SnapshotPath)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("snapshot changed", err)
			}
			evidenceAfter, _ := os.ReadFile(r.EvidencePath)
			if !bytes.Equal(evidenceBefore, evidenceAfter) {
				t.Fatal("native evidence changed")
			}
			launches, err := os.ReadFile(log)
			if mode == "wrong-target" {
				if !errors.Is(err, os.ErrNotExist) {
					t.Fatal("target mismatch ran native", string(launches), err)
				}
			} else if err != nil || len(strings.Fields(string(launches))) != 1 {
				t.Fatal("native process repeated or missing", string(launches), err)
			}
			if mode == "success" {
				startCoordinatorProcess(t, binary, dir, "real-up")
			}
		})
	}
}

func TestForegroundAPIHelper(t *testing.T) {
	if os.Getenv("AGENTFLOW_FOREGROUND_HELPER") != "1" {
		return
	}
	dir, tools := os.Getenv("AGENTFLOW_TEST_DIR"), os.Getenv("AGENTFLOW_TEST_TOOLS")
	c, err := config.Load(os.Getenv("AGENTFLOW_TEST_CONFIG"))
	if err != nil {
		panic(err)
	}
	resolved, err := config.Resolve(c, config.ResolveOptions{})
	if err != nil {
		panic(err)
	}
	plan, err := runtime.Compile(resolved, runtime.Options{WrapperBinary: filepath.Join(tools, "agentflow"), SnapshotPath: filepath.Join(dir, "config.resolved.json")})
	if err != nil {
		panic(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	deadline := 10 * time.Second
	if os.Getenv("FIXTURE_MODE") == "wait" {
		deadline = 1500 * time.Millisecond
	}
	startup, cancel := context.WithTimeout(ctx, deadline)
	owner, result, startErr := coordinator.Up(startup, plan, coordinator.UpOptions{RunID: "real-up", RunDir: dir,
		Native: runtime.NativeOptions{Target: runtime.CMUXTarget{WindowID: "window:1", WorkspaceID: "workspace:2", SurfaceID: "surface:3"},
			ToolBinary: filepath.Join(tools, "opencode"), CMUXBinary: filepath.Join(tools, "cmux"), EvidencePath: filepath.Join(dir, "native.jsonl")}})
	cancel()
	if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
		panic(err)
	}
	if startErr != nil {
		os.Exit(1)
	}
	if err := owner.Serve(ctx); err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

func verifyUpEvidence(t *testing.T, r coordinator.UpResult) {
	t.Helper()
	f, err := os.Open(r.EvidencePath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var challenge struct {
		Challenge struct {
			Actor ipc.Actor `json:"actor"`
			Nonce string    `json:"nonce"`
		}
		Location runtime.NativeBinding
	}
	var finished struct {
		Stdout    string
		Executed  bool
		Truncated bool
		Succeeded bool `json:"process_succeeded"`
	}
	d := json.NewDecoder(f)
	if d.Decode(&challenge) != nil || d.Decode(&finished) != nil || d.Decode(new(any)) != io.EOF || !finished.Executed || !finished.Succeeded || finished.Truncated {
		t.Fatal("incomplete native evidence")
	}
	snapshot, err := runtime.ReadSnapshot(r.SnapshotPath)
	if err != nil {
		t.Fatal(err)
	}
	seat := snapshot.Seats[0]
	seat.Native = challenge.Location
	native, claim, err := runtime.ParseNativeReady("opencode", []byte(finished.Stdout), seat, challenge.Challenge.Nonce)
	if err != nil || claim == nil || claim.Actor != challenge.Challenge.Actor || claim.Nonce != r.ReadyProof.Nonce || native != r.Seat.Native {
		t.Fatal("proof differs from native evidence", claim, err)
	}
	for _, path := range []string{r.SnapshotPath, r.EvidencePath, filepath.Join(r.RunDir, "up.requested.json")} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatal(path, info, err)
		}
	}
	info, err := os.Stat(r.ContextPath)
	if err != nil || info.Mode().Perm() != 0700 {
		t.Fatal(info, err)
	}
}
