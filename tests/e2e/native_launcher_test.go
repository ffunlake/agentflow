package e2e_test

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agentflow/internal/config"
	"agentflow/internal/runtime"
	"agentflow/internal/store"
)

// Native fixtures emit the public CLI JSONL shapes. No installed model CLI,
// network, login or cmux socket is used by this integration test.
const nativeFixtureSource = `package main
import("bufio";"encoding/json";"fmt";"os";"path/filepath";"strings";"time")
func emit(v any){b,_:=json.Marshal(v);fmt.Println(string(b))}
func main(){
 if filepath.Base(os.Args[0])=="cmux" {
  if os.Getenv("FIXTURE_MODE")=="wrong-target" {emit(map[string]any{"caller":map[string]string{"window_id":"other"}});return}
  emit(map[string]any{"caller":map[string]string{"window_id":"11111111-1111-1111-1111-111111111111","window_ref":"window:1","workspace_id":"22222222-2222-2222-2222-222222222222","workspace_ref":"workspace:2","surface_id":"33333333-3333-3333-3333-333333333333","surface_ref":"surface:3"}});return
 }
  if log:=os.Getenv("FIXTURE_LAUNCH_LOG");log!=""{f,err:=os.OpenFile(log,os.O_CREATE|os.O_APPEND|os.O_WRONLY,0600);if err!=nil{panic(err)};fmt.Fprintln(f,os.Getpid());f.Close()}
  cwd,_:=os.Getwd();emitRecord:=map[string]any{"cwd":cwd,"argv":os.Args[1:]};b,_:=json.Marshal(emitRecord);os.WriteFile(os.Getenv("FIXTURE_RECORD"),b,0600)
 scanner:=bufio.NewScanner(os.Stdin);scanner.Buffer(make([]byte,4096),1024*1024);last:="";for scanner.Scan(){if scanner.Text()!=""{last=scanner.Text()}}
 mode:=os.Getenv("FIXTURE_MODE")
 if mode=="wait" {for{time.Sleep(time.Second)}}
  if mode=="stale" {last=strings.ReplaceAll(last,"\"generation\":1","\"generation\":2")}
  if mode=="nonce" {last=strings.ReplaceAll(last,"\"nonce\":\"","\"nonce\":\"wrong")}
 tool:=filepath.Base(os.Args[0]);native:="native-"+tool
 if tool=="codex" {
  emit(map[string]string{"type":"thread.started","thread_id":native})
  if mode!="no-ready"{emit(map[string]any{"type":"item.completed","item":map[string]string{"type":"agent_message","text":last}})}
  emit(map[string]string{"type":"turn.completed"})
 }else{
  if mode!="no-ready"{emit(map[string]any{"type":"text","sessionID":native,"part":map[string]string{"type":"text","sessionID":native,"text":last}})}
  emit(map[string]any{"type":"step_finish","sessionID":native,"part":map[string]string{"reason":"stop"}})
 }
 if mode=="fail"{fmt.Fprintln(os.Stderr,"fixture-secret");os.Exit(17)}
}`

func TestNativeForegroundLauncherProcessAndRegistration(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	wrapper := filepath.Join(dir, "agentflow wrapper")
	build := exec.Command("go", "build", "-mod=readonly", "-o", wrapper, "./cmd/agentflow")
	build.Dir = root
	if b, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build wrapper: %s %v", b, err)
	}
	source := filepath.Join(dir, "fixture.go")
	if err := os.WriteFile(source, []byte(nativeFixtureSource), 0600); err != nil {
		t.Fatal(err)
	}
	fixture := filepath.Join(dir, "native")
	build = exec.Command("go", "build", "-o", fixture, source)
	if b, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build fixture: %s %v", b, err)
	}
	for _, name := range []string{"opencode", "codex", "cmux"} {
		if err := os.Symlink(fixture, filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
	}
	for _, tool := range []string{"opencode", "codex"} {
		for _, mode := range []string{"success", "no-ready", "stale", "fail", "wrong-target", "wait"} {
			t.Run(tool+"/"+mode, func(t *testing.T) {
				runDir := t.TempDir()
				if err := os.Chmod(runDir, 0700); err != nil {
					t.Fatal(err)
				}
				cwd := filepath.Join(runDir, "cwd spaces $(literal);' quotation")
				if err := os.Mkdir(cwd, 0700); err != nil {
					t.Fatal(err)
				}
				prompt, permissions, session := "Frozen private prompt fixture-secret", "default", "fresh"
				c := &config.Config{Version: 1, Name: "native-test", Terminal: "cmux", Lead: "solo", ContextDir: "context", SourcePath: filepath.Join(runDir, "team.yaml"),
					Agents: map[string]config.Agent{"solo": {Role: "Developer", Prompt: &prompt, Tool: &tool, Cwd: &cwd, Permissions: &permissions, Session: &session}}, Layout: &config.Layout{Agent: "solo"}}
				resolved, err := config.Resolve(c, config.ResolveOptions{})
				if err != nil {
					t.Fatal(err)
				}
				snapshotPath := filepath.Join(runDir, "snapshot.json")
				plan, err := runtime.Compile(resolved, runtime.Options{WrapperBinary: wrapper, SnapshotPath: snapshotPath})
				if err != nil {
					t.Fatal(err)
				}
				// Reuse the existing topology builder; argv creation is deliberately
				// kept separate from execution in an owned foreground surface.
				if _, err := runtime.BuildCMUXCommand(plan, "solo", runtime.CMUXTarget{WindowID: "window:1"}); err != nil {
					t.Fatal(err)
				}
				db, err := store.Open(filepath.Join(runDir, "state.db"))
				if err != nil {
					t.Fatal(err)
				}
				defer db.Close()
				if err := db.Migrate(t.Context()); err != nil {
					t.Fatal(err)
				}
				record := filepath.Join(runDir, "process.json")
				t.Setenv("FIXTURE_MODE", mode)
				t.Setenv("FIXTURE_RECORD", record)
				launcher := &runtime.ForegroundLauncher{Options: runtime.NativeOptions{Target: runtime.CMUXTarget{WindowID: "window:1", WorkspaceID: "workspace:2", SurfaceID: "surface:3"}, ToolBinary: filepath.Join(dir, tool), CMUXBinary: filepath.Join(dir, "cmux")}}
				launcher.Options.EvidencePath = filepath.Join(runDir, "native.jsonl")
				ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
				if mode == "wait" {
					cancel()
					ctx, cancel = context.WithTimeout(t.Context(), time.Second)
				}
				defer cancel()
				registry, startErr := runtime.Start(ctx, plan, runtime.StartOptions{RunID: "native-run", SnapshotPath: snapshotPath}, db, launcher)
				if registry == nil {
					t.Fatal("missing partial registry", startErr)
				}
				stored, storeErr := db.GetSeat(t.Context(), "native-run", "solo", 1)
				if mode == "success" {
					if startErr != nil || storeErr != nil || stored.Status != runtime.Ready || stored.NativeSessionID != "native-"+tool || stored.Generation != 1 || stored.SurfaceID != "33333333-3333-3333-3333-333333333333" {
						t.Fatalf("READY not registered: %+v %v %v", stored, startErr, storeErr)
					}
					// Recorded stdout must independently reproduce the registered
					// identity against the exact pre-execution nonce, without stderr.
					f, err := os.Open(launcher.Options.EvidencePath)
					if err != nil {
						t.Fatal(err)
					}
					defer f.Close()
					d := json.NewDecoder(f)
					var challenge struct {
						Challenge struct{ Nonce string }
						Location  runtime.NativeBinding
					}
					var finished struct {
						Stdout string
					}
					if d.Decode(&challenge) != nil || d.Decode(&finished) != nil || d.Decode(new(any)) != io.EOF {
						t.Fatal("invalid evidence")
					}
					initial, _ := runtime.LoadStartupSeat(snapshotPath, "solo")
					initial.Native = challenge.Location
					binding, claim, err := runtime.ParseNativeReady(tool, []byte(finished.Stdout), initial, challenge.Challenge.Nonce)
					if err != nil || claim == nil || binding.SessionID != stored.NativeSessionID {
						t.Fatal("saved evidence did not verify", err)
					}
					// An existing evidence artifact must block execution, preserve
					// its bytes, and never overwrite or replay a native process.
					before, _ := os.ReadFile(launcher.Options.EvidencePath)
					execution := runtime.ExecuteNativeSeat(t.Context(), snapshotPath, "solo", launcher.Options)
					after, _ := os.ReadFile(launcher.Options.EvidencePath)
					if execution.Executed || execution.Error == nil || execution.Error.Code != "SEAT_NATIVE_EVIDENCE_INVALID" || string(before) != string(after) {
						t.Fatal("existing evidence allowed execution or was overwritten")
					}
					outside := launcher.Options
					outside.EvidencePath = filepath.Join(t.TempDir(), "outside.jsonl")
					execution = runtime.ExecuteNativeSeat(t.Context(), snapshotPath, "solo", outside)
					if execution.Executed || execution.Error == nil || execution.Error.Code != "SEAT_NATIVE_EVIDENCE_INVALID" {
						t.Fatal("accepted evidence outside the private run directory")
					}
				} else if startErr == nil || storeErr == nil || registry.Snapshot().Seats[0].Status == runtime.Ready {
					t.Fatalf("failure registered READY: %+v %v %v", stored, startErr, storeErr)
				}
				if startErr != nil && strings.Contains(startErr.Error(), "fixture-secret") {
					t.Fatal("native stderr leaked")
				}
				if evidence, err := os.ReadFile(launcher.Options.EvidencePath); err == nil && strings.Contains(string(evidence), "fixture-secret") {
					t.Fatal("prompt or native stderr entered evidence")
				}
				if mode == "wrong-target" {
					if _, err := os.Stat(record); !os.IsNotExist(err) {
						t.Fatal("wrong target started a tool")
					}
				} else if mode != "wait" {
					var actual struct {
						Cwd  string
						Argv []string
					}
					body, err := os.ReadFile(record)
					if err != nil || json.Unmarshal(body, &actual) != nil || actual.Cwd != cwd {
						t.Fatalf("cwd not preserved: %s %v", body, err)
					}
					if strings.Contains(strings.Join(actual.Argv, "\n"), "fixture-secret") {
						t.Fatal("prompt entered argv")
					}
				}
				// One-shot launcher cannot replay even after an unknown/failure.
				if _, err := launcher.Launch(t.Context(), runtime.LaunchRequest{}); err == nil {
					t.Fatal("launcher replayed")
				}
			})
		}
	}
}
