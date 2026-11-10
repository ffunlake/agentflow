package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"agentflow/internal/config"
	"agentflow/internal/store"
)

type submitGuard struct {
	acquire func(context.Context, SeatSnapshot) (InputState, func(), error)
	held    bool
	calls   int
}

func (g *submitGuard) Acquire(ctx context.Context, s SeatSnapshot) (InputState, func(), error) {
	g.calls++
	if g.acquire != nil {
		return g.acquire(ctx, s)
	}
	g.held = true
	return InputState{Actor: seatActor(s), Native: s.Native, Verified: true}, func() { g.held = false }, nil
}

func buildSubmitFixture(t *testing.T) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "native submit fixture")
	cmd := exec.Command("go", "build", "-mod=readonly", "-o", binary, "./testdata/native-submit")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build fixture: %s %v", out, err)
	}
	return binary
}

func submitOptions(t *testing.T, tool, binary string) NativeSubmitOptions {
	t.Helper()
	cwd := filepath.Join(t.TempDir(), "cwd spaces $(touch bad);' 中文")
	if err := os.Mkdir(cwd, 0700); err != nil {
		t.Fatal(err)
	}
	a := config.ResolvedAgent{Tool: tool, Cwd: cwd, Role: "Developer", Permissions: "default", Session: "persistent", Model: "model;$(literal)", Args: []string{"--variant", "high"}}
	if tool == "codex" {
		a.Args = []string{"--profile", "literal profile"}
	}
	s := SeatSnapshot{RunID: "run-test", AgentID: "builder", Generation: 1, Tool: tool, Cwd: cwd, Role: a.Role, Permissions: a.Permissions, Session: a.Session,
		Native: NativeBinding{SessionID: "native-session", WorkspaceID: "workspace:2", SurfaceID: "surface:3"}, Status: Ready}
	return NativeSubmitOptions{Seat: s, Agent: a, ToolBinary: binary, Guard: &submitGuard{}}
}

func submitMessage() store.Message {
	return store.Message{RunID: "run-test", ID: "message-1", RequestID: "request-1", Recipient: "builder", Kind: "assign", Body: "frozen fixture-secret\n中文\r\n--session evil; $(touch bad) `false` ' \"\x00tail\n"}
}

func configureSubmitFixture(t *testing.T, tool, session, mode string) string {
	t.Helper()
	record := filepath.Join(t.TempDir(), "process.json")
	t.Setenv("SUBMIT_FIXTURE_TOOL", tool)
	t.Setenv("SUBMIT_FIXTURE_SESSION", session)
	t.Setenv("SUBMIT_FIXTURE_MODE", mode)
	t.Setenv("SUBMIT_FIXTURE_RECORD", record)
	return record
}

func TestNativeSubmitExecutableFixtures(t *testing.T) {
	binary := buildSubmitFixture(t)
	for _, tool := range []string{"opencode", "codex"} {
		for _, mode := range []string{"success", "full", "lost", "wrong-id", "missing-id", "no-assistant", "no-completion", "failed", "exit", "malformed", "truncated", "stderr-truncated", "wait"} {
			t.Run(tool+"/"+mode, func(t *testing.T) {
				o := submitOptions(t, tool, binary)
				if mode == "full" {
					o.Agent.Permissions, o.Seat.Permissions = "full", "full"
				}
				adapter, err := NewNativeSubmitAdapter(o)
				if err != nil {
					t.Fatal(err)
				}
				record := configureSubmitFixture(t, tool, o.Seat.Native.SessionID, mode)
				guard := o.Guard.(*submitGuard)
				ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
				defer cancel()
				if mode == "wait" {
					// Wait for the complete record, not just its creation: killing
					// the fixture before Encode finishes leaves a partial record.
					// A deadline bounds unexpected fixture failure.
					guard.acquire = func(ctx context.Context, s SeatSnapshot) (InputState, func(), error) {
						guard.held = true
						go func() {
							for {
								if body, err := os.ReadFile(record); err == nil && json.Valid(body) {
									cancel()
									return
								}
								select {
								case <-ctx.Done():
									return
								case <-time.After(5 * time.Millisecond):
								}
							}
						}()
						return InputState{Actor: seatActor(s), Native: s.Native, Verified: true}, func() { guard.held = false }, nil
					}
				}
				m := submitMessage()
				result, err := adapter.Submit(ctx, o.Seat, m)
				if mode == "success" || mode == "full" {
					if err != nil || !result.Accepted || result.Actor != seatActor(o.Seat) || result.Native != o.Seat.Native || result.Channel != "native-cli-"+tool+"-resume" {
						t.Fatalf("submission: %+v %v", result, err)
					}
				} else {
					var failure *NativeSubmitError
					if !errors.As(err, &failure) || result != (Submission{}) || strings.Contains(err.Error(), "fixture-secret") {
						t.Fatalf("unsafe failure: %+v %v", result, err)
					}
					outcome := "UNKNOWN"
					if mode == "failed" {
						outcome = "FAILED"
					}
					if failure.Outcome != outcome {
						t.Fatalf("outcome: %+v", failure)
					}
				}
				if guard.held || guard.calls != 1 {
					t.Fatal("guard not acquired/released")
				}
				var actual struct {
					Argv  []string
					Cwd   string
					Stdin string
				}
				body, readErr := os.ReadFile(record)
				if readErr != nil || json.Unmarshal(body, &actual) != nil || actual.Stdin != m.Body || actual.Cwd != o.Seat.Cwd {
					t.Fatalf("transport mismatch: %s %v", body, readErr)
				}
				want := []string{"run", "--format", "json", "--dir", o.Seat.Cwd}
				permission := "--auto"
				if tool == "codex" {
					want = []string{"exec", "--json", "--color", "never", "--skip-git-repo-check", "--cd", o.Seat.Cwd}
					permission = "--dangerously-bypass-approvals-and-sandbox"
				}
				if mode == "full" {
					want = append(want, permission)
				}
				want = append(want, "--model", o.Agent.Model)
				want = append(want, o.Agent.Args...)
				if tool == "codex" {
					want = append(want, "resume", o.Seat.Native.SessionID, "-")
				} else {
					want = append(want, "--session", o.Seat.Native.SessionID)
				}
				if !reflect.DeepEqual(actual.Argv, want) || strings.Contains(strings.Join(actual.Argv, "\n"), "fixture-secret") {
					t.Fatalf("argv: %q, want %q", actual.Argv, want)
				}
			})
		}
	}
}

func TestNativeSubmitRejectsUnsafeOptionsAndBindingBeforeExecution(t *testing.T) {
	o := submitOptions(t, "codex", "/explicit/native")
	for _, mutate := range []func(*NativeSubmitOptions){
		func(o *NativeSubmitOptions) { o.Guard = nil },
		func(o *NativeSubmitOptions) { o.ToolBinary = "codex" },
		func(o *NativeSubmitOptions) { o.ToolBinary = "/tmp/cmux-cli-shims/codex" },
		func(o *NativeSubmitOptions) { o.Seat.Native.SessionID = "--last" },
		func(o *NativeSubmitOptions) { o.Seat.Native.SurfaceID = "" },
		func(o *NativeSubmitOptions) { o.Agent.Permissions = "full" },
		func(o *NativeSubmitOptions) { o.Agent.Args = []string{"--session", "evil"} },
		func(o *NativeSubmitOptions) { o.Agent.Tool, o.Seat.Tool = "claude", "claude" },
	} {
		bad := o
		mutate(&bad)
		if adapter, err := NewNativeSubmitAdapter(bad); err == nil || adapter != nil {
			t.Fatal("accepted unsafe options")
		}
	}
	adapter, err := NewNativeSubmitAdapter(o)
	if err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*SeatSnapshot){
		func(s *SeatSnapshot) { s.RunID = "other" },
		func(s *SeatSnapshot) { s.AgentID = "other" },
		func(s *SeatSnapshot) { s.Generation++ },
		func(s *SeatSnapshot) { s.Native.SessionID = "other" },
		func(s *SeatSnapshot) { s.Native.WorkspaceID = "other" },
		func(s *SeatSnapshot) { s.Native.SurfaceID = "other" },
		func(s *SeatSnapshot) { s.Tool = "opencode" },
		func(s *SeatSnapshot) { s.Permissions = "full" },
		func(s *SeatSnapshot) { s.Cwd = "/other" },
		func(s *SeatSnapshot) { s.Status = Busy },
	} {
		seat := o.Seat
		mutate(&seat)
		if r, err := adapter.Submit(t.Context(), seat, submitMessage()); err == nil || r.Accepted {
			t.Fatal("accepted mismatched seat")
		}
	}
	if o.Guard.(*submitGuard).calls != 0 {
		t.Fatal("identity mismatch reached guard")
	}
}

func TestNativeSubmitBoundaryInputGuard(t *testing.T) {
	for _, gate := range []string{"busy", "draft", "incomplete", "unverified", "actor", "native", "error", "no-release"} {
		t.Run(gate, func(t *testing.T) {
			o := submitOptions(t, "opencode", "/must-not-execute")
			released := false
			o.Guard = &submitGuard{acquire: func(_ context.Context, s SeatSnapshot) (InputState, func(), error) {
				input := InputState{Actor: seatActor(s), Native: s.Native, Verified: true}
				release := func() { released = true }
				var err error
				switch gate {
				case "busy":
					input.Busy = true
				case "draft":
					input.Draft = "fixture-secret"
				case "incomplete":
					input.IncompleteInput = true
				case "unverified":
					input.Verified = false
				case "actor":
					input.Actor.Generation++
				case "native":
					input.Native.SurfaceID = "other"
				case "error":
					err = errors.New("fixture-secret")
				case "no-release":
					release = nil
				}
				return input, release, err
			}}
			adapter, err := NewNativeSubmitAdapter(o)
			if err != nil {
				t.Fatal(err)
			}
			r, err := adapter.Submit(t.Context(), o.Seat, submitMessage())
			var failure *NativeSubmitError
			if !errors.As(err, &failure) || failure.Code != "NATIVE_SUBMIT_INPUT_UNVERIFIED" || r.Accepted || strings.Contains(err.Error(), "fixture-secret") || released != (gate != "no-release") {
				t.Fatalf("unsafe guard result: %+v %v released=%v", r, err, released)
			}
		})
	}
}

func TestNativeSubmitProtocolCannotUseEchoOrForeignIdentity(t *testing.T) {
	for _, tool := range []string{"opencode", "codex"} {
		t.Run(tool, func(t *testing.T) {
			seat := submitOptions(t, tool, "/explicit/native").Seat
			valid := nativeEvents(t, tool, seat.Native.SessionID, "assistant result")
			for _, body := range [][]byte{
				[]byte("native PASS"),
				append(append([]byte{}, valid...), []byte("\nnot json")...),
				append(append([]byte{}, valid...), []byte("\n"+`{"type":"error"}`)...),
				append(append([]byte{}, valid...), []byte("\n"+`{"type":"thread.started","thread_id":"other"}`)...),
				[]byte(strings.ReplaceAll(strings.ReplaceAll(string(valid), `"agent_message"`, `"user_message"`), `"type":"text"`, `"type":"reasoning"`)),
				[]byte(strings.ReplaceAll(string(valid), `"thread_id":`, `"sessionID":`)),
			} {
				// The last replacement is foreign identity only for Codex.
				if tool == "opencode" && string(body) == string(valid) {
					continue
				}
				if r, err := parseNativeSubmission(seat, body); err == nil || r.Accepted {
					t.Fatalf("accepted unverified protocol: %s", body)
				}
			}
		})
	}
}

func TestNativeSubmitCoordinatorGateAndUnknownNeverReplay(t *testing.T) {
	binary := buildSubmitFixture(t)
	for _, mode := range []string{"success", "lost", "failed", "draft"} {
		t.Run(mode, func(t *testing.T) {
			db, registry, plan, _ := sessionFixture(t)
			var seat SeatSnapshot
			for _, s := range registry.Snapshot().Seats {
				if s.AgentID == "builder" {
					seat = s
				}
			}
			record := configureSubmitFixture(t, seat.Tool, seat.Native.SessionID, mode)
			c := assignment()
			c.Session = "persistent"
			queued, _ := NewSessionCoordinator(db, nil, nil)
			saved, err := queued.Assign(t.Context(), c)
			if err != nil || saved.Delivery.Phase != "QUEUED" {
				t.Fatalf("initial storage: %+v %v", saved, err)
			}
			guard := &submitGuard{}
			inspect := &fakeResetAdapter{inspect: func(ctx context.Context, s SeatSnapshot) (InputState, error) {
				input := InputState{Actor: seatActor(s), Native: s.Native, Verified: true}
				if mode == "draft" {
					input.Draft = "draft"
				}
				return input, nil
			}}
			guard.acquire = func(ctx context.Context, s SeatSnapshot) (InputState, func(), error) {
				w, err := db.SessionWork(ctx, saved.Message.ID)
				if err != nil || w.Record.Delivery.Phase != "SUBMIT_REQUESTED" || w.Record.Delivery.Attempt != 1 {
					t.Fatalf("executed without durable submit gate: %+v %v", w, err)
				}
				return InputState{Actor: seatActor(s), Native: s.Native, Verified: true}, func() {}, nil
			}
			adapter, err := NewNativeSubmitAdapter(NativeSubmitOptions{Seat: seat, Agent: plan.Config.Agents["builder"], ToolBinary: binary, Guard: guard})
			if err != nil {
				t.Fatal(err)
			}
			service, _ := NewMessageServiceWithAdapters(db, inspect, adapter)
			first, err := service.sessions.Advance(t.Context(), c.Actor, saved.Message.ID)
			if err != nil {
				t.Fatal(err)
			}
			phase := "SUBMIT_REQUESTED"
			if mode == "success" {
				phase = "SENT"
			} else if mode == "draft" {
				phase = "QUEUED"
			}
			if first.Delivery.Phase != phase {
				t.Fatalf("delivery: %+v", first)
			}
			if phase == "SUBMIT_REQUESTED" && first.Coordination != "UNVERIFIED" {
				t.Fatal("unknown result lost its unverified state")
			}
			if mode == "draft" {
				if _, err := os.Stat(record); !os.IsNotExist(err) || guard.calls != 0 {
					t.Fatal("draft reached executable")
				}
				return
			}
			before, err := os.ReadFile(record)
			if err != nil {
				t.Fatal(err)
			}
			c.Body = "changed retry must not reach stdin"
			if _, err := service.Assign(t.Context(), c); err == nil {
				t.Fatal("changed request body did not conflict")
			}
			c.Body = assignment().Body
			if _, err := service.Assign(t.Context(), c); err != nil {
				t.Fatal(err)
			}
			if _, err := service.sessions.Advance(t.Context(), c.Actor, first.Message.ID); err != nil {
				t.Fatal(err)
			}
			after, _ := os.ReadFile(record)
			if string(before) != string(after) || guard.calls != 1 {
				t.Fatal("submitted result was replayed")
			}
			var actual struct{ Stdin string }
			if json.Unmarshal(before, &actual) != nil || actual.Stdin != assignment().Body {
				t.Fatal("did not submit original frozen body")
			}
		})
	}
}
