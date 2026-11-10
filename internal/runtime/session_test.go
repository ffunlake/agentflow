package runtime

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"

	"agentflow/internal/config"
	"agentflow/internal/ipc"
	"agentflow/internal/store"
)

type fakeResetAdapter struct {
	inspect     func(context.Context, SeatSnapshot) (InputState, error)
	reset       func(context.Context, ResetRequest) (NativeBinding, error)
	wait        func(context.Context, ResetRequest, NativeBinding) (Registration, error)
	inspections atomic.Int32
	resets      atomic.Int32
	waits       atomic.Int32
}

func (f *fakeResetAdapter) Inspect(ctx context.Context, seat SeatSnapshot) (InputState, error) {
	f.inspections.Add(1)
	if f.inspect != nil {
		return f.inspect(ctx, seat)
	}
	return InputState{Actor: seatActor(seat), Native: seat.Native, Verified: true}, nil
}
func (f *fakeResetAdapter) Reset(ctx context.Context, r ResetRequest) (NativeBinding, error) {
	f.resets.Add(1)
	if f.reset != nil {
		return f.reset(ctx, r)
	}
	n := r.Previous.Native
	n.SessionID = fmt.Sprintf("new-session-%d", r.Generation)
	return n, nil
}
func (f *fakeResetAdapter) WaitReady(ctx context.Context, r ResetRequest, n NativeBinding) (Registration, error) {
	f.waits.Add(1)
	if f.wait != nil {
		return f.wait(ctx, r, n)
	}
	seat := r.Previous
	seat.Generation, seat.Native = r.Generation, n
	return readyClaim(seat), nil
}

type fakeSubmitAdapter struct {
	submit func(context.Context, SeatSnapshot, store.Message) (Submission, error)
	calls  atomic.Int32
}

func (f *fakeSubmitAdapter) Submit(ctx context.Context, seat SeatSnapshot, m store.Message) (Submission, error) {
	f.calls.Add(1)
	if f.submit != nil {
		return f.submit(ctx, seat, m)
	}
	return Submission{Actor: seatActor(seat), Native: seat.Native, Accepted: true, Channel: "fake-visible-channel"}, nil
}

func sessionFixture(t *testing.T) (*store.Store, *Registry, *StartupPlan, StartOptions) {
	t.Helper()
	plan, db, options := startupFixture(t)
	r, err := Start(t.Context(), plan, options, db, fakeLauncher{
		launch: func(_ context.Context, r LaunchRequest) (NativeBinding, error) {
			return fakeNative(r.Seat.AgentID), nil
		},
		wait: func(_ context.Context, s SeatSnapshot) (Registration, error) { return readyClaim(s), nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	return db, r, plan, options
}

func assignment() SendMessage {
	return SendMessage{Actor: ipc.Actor{RunID: "run-test", AgentID: "main", Generation: 1}, RequestID: "work-1", Recipient: "builder", Body: "work body\n中文"}
}

func requireSessionCode(t *testing.T, err error, code string) {
	t.Helper()
	var public *store.MessagingError
	if !errors.As(err, &public) || public.Code != code {
		t.Fatalf("got %v, want %s", err, code)
	}
}

func TestSessionFreshTwoWorksAndQueuedRebase(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	db, registry, plan, opts := sessionFixture(t)
	initial := registry.Snapshot()
	fake := &fakeResetAdapter{}
	submit := &fakeSubmitAdapter{}
	fake.reset = func(ctx context.Context, r ResetRequest) (NativeBinding, error) {
		w, err := db.SessionWork(ctx, r.MessageID)
		if err != nil || w.Record.Delivery.ResetPhase != "RESET_REQUESTED" || w.Seat.Generation != r.Generation-1 {
			t.Fatalf("reset before intent: %+v %v", w, err)
		}
		role := plan.Config.Agents["builder"]
		if !reflect.DeepEqual(r.Role, role) || r.Previous.Role != role.Role || r.Previous.Cwd != role.Cwd || r.ContextDir != filepath.Join(plan.Config.ContextDir, "run-test") || r.RequestID == "" {
			t.Fatalf("reinjection lost frozen metadata: %+v", r)
		}
		n := r.Previous.Native
		n.SessionID = fmt.Sprintf("new-session-%d", r.Generation)
		return n, nil
	}
	fake.wait = func(ctx context.Context, r ResetRequest, n NativeBinding) (Registration, error) {
		if _, err := db.GetSeat(ctx, "run-test", "builder", r.Generation); err == nil {
			t.Fatal("generation advanced before READY")
		}
		s := r.Previous
		s.Generation, s.Native = r.Generation, n
		return readyClaim(s), nil
	}
	submit.submit = func(ctx context.Context, seat SeatSnapshot, m store.Message) (Submission, error) {
		w, err := db.SessionWork(ctx, m.ID)
		if err != nil || w.Record.Delivery.ResetPhase != "READY" || w.Record.Delivery.Phase != "SUBMIT_REQUESTED" || w.Record.Delivery.Attempt != 1 || seat.Generation != w.Seat.Generation || m.Body != assignment().Body {
			t.Fatalf("submit before READY/intent: %+v %v", w, err)
		}
		return Submission{Actor: seatActor(seat), Native: seat.Native, Accepted: true, Channel: "fake"}, nil
	}
	s, _ := NewMessageServiceWithAdapters(db, fake, submit)
	c := assignment()
	first, err := s.Assign(t.Context(), c)
	if err != nil || first.Session != "fresh" || first.Delivery.TargetGeneration != 2 || first.Delivery.Phase != "SENT" || first.Delivery.Attempt != 1 {
		t.Fatalf("%+v %v", first, err)
	}
	// Reusing the request preserves its first mode even if the retry asks otherwise.
	c.Session = "persistent"
	again, err := s.Assign(t.Context(), c)
	if err != nil || !reflect.DeepEqual(first, again) || fake.resets.Load() != 1 || submit.calls.Load() != 1 {
		t.Fatalf("retry: %+v %v", again, err)
	}
	c.RequestID, c.Session = "work-2", ""
	second, err := s.Assign(t.Context(), c)
	if err != nil || second.Delivery.Phase != "QUEUED" || second.Coordination != "SEAT_BUSY" || fake.resets.Load() != 1 {
		t.Fatalf("queue: %+v %v", second, err)
	}
	old := ipc.Actor{RunID: "run-test", AgentID: "builder", Generation: 1}
	_, err = s.Ack(t.Context(), old, "old-ack", first.Message.ID)
	requireSessionCode(t, err, "GENERATION_MISMATCH")
	_, err = s.Reply(t.Context(), ReplyMessage{Actor: old, RequestID: "old-result", MessageID: first.Message.ID, Status: "DONE"})
	requireSessionCode(t, err, "GENERATION_MISMATCH")
	current := old
	current.Generation = 2
	if _, err := s.Ack(t.Context(), current, "receipt", first.Message.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Reply(t.Context(), ReplyMessage{Actor: current, RequestID: "result", MessageID: first.Message.ID, Status: "DONE", Body: "done"}); err != nil {
		t.Fatal(err)
	}
	second, err = s.sessions.Advance(t.Context(), c.Actor, second.Message.ID)
	if err != nil || second.Delivery.TargetGeneration != 3 || second.Delivery.Phase != "SENT" || fake.resets.Load() != 2 || submit.calls.Load() != 2 {
		t.Fatalf("second: %+v %v", second, err)
	}
	_, err = s.Ack(t.Context(), current, "late-ack", first.Message.ID)
	requireSessionCode(t, err, "GENERATION_MISMATCH")
	// Durable current bindings are authoritative; the startup registry/artifact
	// remains the immutable registration snapshot.
	if !reflect.DeepEqual(initial, registry.Snapshot()) {
		t.Fatal("startup snapshot mutated")
	}
	latest, err := db.GetSeat(t.Context(), "run-test", "builder", 3)
	if err != nil || !latest.Current || latest.NativeSessionID != "new-session-3" {
		t.Fatalf("%+v %v", latest, err)
	}
	status := ReadStatus(t.Context(), db, QueryOptions{RunID: "run-test", SnapshotPath: opts.SnapshotPath, LockPath: filepath.Join(filepath.Dir(opts.SnapshotPath), "absent.lock")})
	if status.Run == nil || status.Run.StartupProgress != "REGISTERED" || len(status.Diagnostics) != 0 || status.Seats[0].Generation != 3 || status.Seats[0].Status != Busy {
		t.Fatalf("status lost live binding: %+v", status)
	}
}

func TestSessionInputGatesKeepWorkAndResume(t *testing.T) {
	for _, gate := range []string{"busy", "draft", "incomplete", "unknown", "wrong-native", "wrong-generation", "inspect-error"} {
		t.Run(gate, func(t *testing.T) {
			db, _, _, _ := sessionFixture(t)
			fake, submit := &fakeResetAdapter{}, &fakeSubmitAdapter{}
			fake.inspect = func(_ context.Context, seat SeatSnapshot) (InputState, error) {
				x := InputState{Actor: seatActor(seat), Native: seat.Native, Verified: true}
				switch gate {
				case "busy":
					x.Busy = true
				case "draft":
					x.Draft = " " // whitespace is still user input
				case "incomplete":
					x.IncompleteInput = true
				case "unknown":
					x.Verified = false
				case "wrong-native":
					x.Native.SessionID = "other"
				case "wrong-generation":
					x.Actor.Generation--
				case "inspect-error":
					return x, errors.New("inspection unavailable")
				}
				return x, nil
			}
			s, _ := NewMessageServiceWithAdapters(db, fake, submit)
			r, err := s.Assign(t.Context(), assignment())
			if err != nil || r.Delivery.Phase != "QUEUED" || r.Delivery.ResetPhase != "NONE" || r.Message.Body != assignment().Body || fake.resets.Load() != 0 || submit.calls.Load() != 0 {
				t.Fatalf("%+v %v", r, err)
			}
			_, err = s.Ack(t.Context(), ipc.Actor{RunID: "run-test", AgentID: "builder", Generation: 1}, "premature", r.Message.ID)
			requireSessionCode(t, err, "SESSION_NOT_READY")
			fake.inspect = nil
			r, err = s.sessions.Advance(t.Context(), assignment().Actor, r.Message.ID)
			if err != nil || r.Delivery.Phase != "SENT" || fake.resets.Load() != 1 {
				t.Fatalf("%+v %v", r, err)
			}
		})
	}
}

func TestSessionUnknownResetNeverReplaysAfterReopen(t *testing.T) {
	for _, fault := range []string{"reset-error", "same-native", "wrong-surface", "missing-ready", "old-ready", "wrong-run", "wrong-tool", "wrong-ready-native", "no-ready-status", "cancelled"} {
		t.Run(fault, func(t *testing.T) {
			db, _, _, opts := sessionFixture(t)
			fake, submit := &fakeResetAdapter{}, &fakeSubmitAdapter{}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			fake.reset = func(_ context.Context, r ResetRequest) (NativeBinding, error) {
				n := r.Previous.Native
				n.SessionID = "fresh-native"
				switch fault {
				case "reset-error":
					return n, errors.New("lost reset response")
				case "same-native":
					return r.Previous.Native, nil
				case "wrong-surface":
					n.SurfaceID = "other"
				case "cancelled":
					cancel()
					return n, context.Canceled
				}
				return n, nil
			}
			fake.wait = func(_ context.Context, r ResetRequest, n NativeBinding) (Registration, error) {
				seat := r.Previous
				seat.Generation, seat.Native = r.Generation, n
				c := readyClaim(seat)
				switch fault {
				case "missing-ready":
					return c, errors.New("READY missing")
				case "old-ready":
					c.Actor.Generation--
				case "wrong-run":
					c.Actor.RunID = "other"
				case "wrong-tool":
					c.Tool = "opencode"
				case "wrong-ready-native":
					c.Native.SessionID = "other"
				case "no-ready-status":
					c.Status = Starting
				}
				return c, nil
			}
			s, _ := NewMessageServiceWithAdapters(db, fake, submit)
			_, err := s.Assign(ctx, assignment())
			if fault != "cancelled" && err != nil {
				t.Fatal(err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := store.OpenExisting(filepath.Join(filepath.Dir(opts.SnapshotPath), "state.db"), true)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			s, _ = NewMessageServiceWithAdapters(reopened, fake, submit)
			r, err := s.Assign(t.Context(), assignment())
			if err != nil || r.Delivery.ResetPhase != "RESET_REQUESTED" || r.Delivery.TargetGeneration != 1 || r.Delivery.Attempt != 0 || fake.resets.Load() != 1 || submit.calls.Load() != 0 {
				t.Fatalf("replayed: %+v %v", r, err)
			}
			seat, err := reopened.GetSeat(t.Context(), "run-test", "builder", 1)
			if err != nil || !seat.Current || seat.NativeSessionID != fakeNative("builder").SessionID {
				t.Fatalf("%+v %v", seat, err)
			}
		})
	}
}

func TestSessionPersistentSendAndMissingAdapters(t *testing.T) {
	db, _, _, _ := sessionFixture(t)
	fake, submit := &fakeResetAdapter{}, &fakeSubmitAdapter{}
	s, _ := NewMessageServiceWithAdapters(db, fake, submit)
	c := assignment()
	c.Mode = "persistent"
	r, err := s.Assign(t.Context(), c)
	if err != nil || r.Session != "persistent" || r.Delivery.TargetGeneration != 1 || r.Delivery.ResetPhase != "NONE" || r.Delivery.Phase != "SENT" || fake.inspections.Load() != 1 || fake.resets.Load() != 0 {
		t.Fatalf("%+v %v", r, err)
	}
	c.RequestID, c.Mode, c.Session = "supplement", "", "fresh"
	if _, err := s.Send(t.Context(), c); err != nil || fake.inspections.Load() != 1 || fake.resets.Load() != 0 || submit.calls.Load() != 1 {
		t.Fatal("send triggered session adapters", err)
	}
	db2, _, _, _ := sessionFixture(t)
	s2, _ := NewMessageService(db2)
	r, err = s2.Assign(t.Context(), assignment())
	if err != nil || r.Coordination != "RESET_ADAPTER_UNIMPLEMENTED" || r.Delivery.ResetPhase != "NONE" || r.Delivery.Attempt != 0 {
		t.Fatalf("%+v %v", r, err)
	}
	c = assignment()
	c.Session, c.RequestID, c.Recipient = "persistent", "persistent", "reviewer"
	r, err = s2.Assign(t.Context(), c)
	if err != nil || r.Coordination != "SUBMIT_ADAPTER_UNIMPLEMENTED" || r.Delivery.ResetPhase != "NONE" {
		t.Fatalf("%+v %v", r, err)
	}
}

func TestSessionSubmitUnknownNeverResetsOrResends(t *testing.T) {
	for _, fault := range []string{"error", "wrong-generation", "wrong-native", "not-accepted", "no-channel"} {
		t.Run(fault, func(t *testing.T) {
			db, _, _, _ := sessionFixture(t)
			fake, submit := &fakeResetAdapter{}, &fakeSubmitAdapter{}
			submit.submit = func(_ context.Context, seat SeatSnapshot, _ store.Message) (Submission, error) {
				x := Submission{Actor: seatActor(seat), Native: seat.Native, Accepted: true, Channel: "fake"}
				switch fault {
				case "error":
					return x, errors.New("lost submit response")
				case "wrong-generation":
					x.Actor.Generation--
				case "wrong-native":
					x.Native.SessionID = "other"
				case "not-accepted":
					x.Accepted = false
				case "no-channel":
					x.Channel = ""
				}
				return x, nil
			}
			s, _ := NewMessageServiceWithAdapters(db, fake, submit)
			for range 3 {
				r, err := s.Assign(t.Context(), assignment())
				if err != nil || r.Delivery.ResetPhase != "READY" || r.Delivery.Phase != "SUBMIT_REQUESTED" || r.Delivery.TargetGeneration != 2 || r.Delivery.Attempt != 1 || r.Coordination != "UNVERIFIED" {
					t.Fatalf("%+v %v", r, err)
				}
			}
			if fake.resets.Load() != 1 || submit.calls.Load() != 1 {
				t.Fatal("unknown side effect repeated")
			}
		})
	}
}

func TestSessionConcurrentIdempotencyAcrossStores(t *testing.T) {
	db, _, _, opts := sessionFixture(t)
	other, err := store.OpenExisting(filepath.Join(filepath.Dir(opts.SnapshotPath), "state.db"), true)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	fake, submit := &fakeResetAdapter{}, &fakeSubmitAdapter{}
	services := make([]*MessageService, 2)
	services[0], _ = NewMessageServiceWithAdapters(db, fake, submit)
	services[1], _ = NewMessageServiceWithAdapters(other, fake, submit)
	var wg sync.WaitGroup
	for i := range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := services[i%2].Assign(t.Context(), assignment()); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if fake.resets.Load() != 1 || submit.calls.Load() != 1 {
		t.Fatalf("reset=%d submit=%d", fake.resets.Load(), submit.calls.Load())
	}
}

func TestSessionDraftAfterReadyAndPersistentDraft(t *testing.T) {
	for _, mode := range []string{"fresh", "persistent"} {
		t.Run(mode, func(t *testing.T) {
			db, _, _, _ := sessionFixture(t)
			fake, submit := &fakeResetAdapter{}, &fakeSubmitAdapter{}
			fake.inspect = func(_ context.Context, seat SeatSnapshot) (InputState, error) {
				x := InputState{Actor: seatActor(seat), Native: seat.Native, Verified: true}
				if mode == "persistent" || seat.Generation == 2 {
					x.Draft = "user draft"
				}
				return x, nil
			}
			s, _ := NewMessageServiceWithAdapters(db, fake, submit)
			c := assignment()
			c.Session = mode
			r, err := s.Assign(t.Context(), c)
			if err != nil || r.Delivery.Phase != "QUEUED" || r.Coordination != "INPUT_DRAFT" || submit.calls.Load() != 0 {
				t.Fatalf("%+v %v", r, err)
			}
			wantResets := int32(0)
			if mode == "fresh" {
				wantResets = 1
				if r.Delivery.ResetPhase != "READY" || r.Delivery.TargetGeneration != 2 {
					t.Fatal(r)
				}
			}
			fake.inspect = nil
			r, err = s.Assign(t.Context(), c)
			if err != nil || r.Delivery.Phase != "SENT" || fake.resets.Load() != wantResets || submit.calls.Load() != 1 {
				t.Fatalf("%+v %v", r, err)
			}
		})
	}
}

func TestSessionQueueOrderAndUnknownSubmissionBlockLaterReset(t *testing.T) {
	db, _, _, _ := sessionFixture(t)
	fake, submit := &fakeResetAdapter{}, &fakeSubmitAdapter{}
	fake.inspect = func(_ context.Context, seat SeatSnapshot) (InputState, error) {
		return InputState{Actor: seatActor(seat), Native: seat.Native, Verified: true, Draft: "draft"}, nil
	}
	submit.submit = func(context.Context, SeatSnapshot, store.Message) (Submission, error) {
		return Submission{}, errors.New("unknown submission")
	}
	s, _ := NewMessageServiceWithAdapters(db, fake, submit)
	first, err := s.Assign(t.Context(), assignment())
	if err != nil {
		t.Fatal(err)
	}
	c := assignment()
	c.RequestID = "second"
	second, err := s.Assign(t.Context(), c)
	if err != nil || second.Coordination != "SEAT_QUEUE" {
		t.Fatalf("%+v %v", second, err)
	}
	fake.inspect = nil
	second, err = s.sessions.Advance(t.Context(), c.Actor, second.Message.ID)
	if err != nil || second.Coordination != "SEAT_QUEUE" || fake.resets.Load() != 0 {
		t.Fatalf("queue bypass: %+v %v", second, err)
	}
	first, err = s.sessions.Advance(t.Context(), c.Actor, first.Message.ID)
	if err != nil || first.Delivery.Phase != "SUBMIT_REQUESTED" {
		t.Fatalf("%+v %v", first, err)
	}
	second, err = s.sessions.Advance(t.Context(), c.Actor, second.Message.ID)
	if err != nil || second.Coordination != "SEAT_QUEUE" || fake.resets.Load() != 1 || submit.calls.Load() != 1 {
		t.Fatalf("unknown bypass: %+v %v", second, err)
	}
}

func TestSessionReinjectsFrozenPromptFile(t *testing.T) {
	plan, db, opts := startupFixture(t)
	c, err := config.Load("../../examples/team.yaml")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "role prompt.md")
	const frozen = "original frozen role\n中文"
	if err := os.WriteFile(path, []byte(frozen), 0600); err != nil {
		t.Fatal(err)
	}
	a := c.Agents["builder"]
	a.Prompt, a.PromptFile = nil, &path
	c.Agents["builder"] = a
	resolved, err := config.Resolve(c, config.ResolveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	plan, err = Compile(resolved, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Start(t.Context(), plan, opts, db, fakeLauncher{
		launch: func(_ context.Context, r LaunchRequest) (NativeBinding, error) {
			return fakeNative(r.Seat.AgentID), nil
		},
		wait: func(_ context.Context, s SeatSnapshot) (Registration, error) { return readyClaim(s), nil },
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	fake := &fakeResetAdapter{}
	fake.reset = func(_ context.Context, r ResetRequest) (NativeBinding, error) {
		if r.Role.Prompt != frozen || r.Role.PromptFile != path || !filepath.IsAbs(r.Previous.Cwd) || !filepath.IsAbs(r.ContextDir) {
			t.Fatalf("reinjection changed: %+v", r)
		}
		n := r.Previous.Native
		n.SessionID = "new-session"
		return n, nil
	}
	s, _ := NewMessageServiceWithAdapters(db, fake, &fakeSubmitAdapter{})
	r, err := s.Assign(t.Context(), assignment())
	if err != nil || r.Delivery.Phase != "SENT" {
		t.Fatalf("%+v %v", r, err)
	}
}
