package coordinator

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"agentflow/internal/ipc"
	"agentflow/internal/lock"
	"agentflow/internal/store"
)

func fixture(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "af-coord-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	db, err := store.Open(filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateRun(t.Context(), store.Run{RunID: "run", ConfigSnapshot: `{"agents":{"main":{"session":"persistent"},"builder":{"session":"fresh"}}}`, ConfigDigest: "digest"}); err != nil {
		t.Fatal(err)
	}
	for _, agent := range []string{"main", "builder"} {
		if err := db.CreateSeat(t.Context(), store.Seat{RunID: "run", AgentID: agent, Generation: 1, Current: true, Status: "READY"}); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func serve(t *testing.T, dir string) func() {
	t.Helper()
	c, err := Open(t.Context(), dir, "run")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.Serve(ctx) }()
	var once sync.Once
	stop := func() {
		once.Do(func() {
			cancel()
			select {
			case err := <-done:
				if err != nil {
					t.Error(err)
				}
			case <-time.After(5 * time.Second):
				t.Error("shutdown did not drain")
			}
		})
	}
	t.Cleanup(stop)
	return stop
}

func request(actor, method, id string, p Params) ipc.Request {
	raw, _ := json.Marshal(p)
	return ipc.Request{V: ipc.Version, Actor: ipc.Actor{RunID: "run", AgentID: actor, Generation: 1}, Method: method, RequestID: id, Params: raw}
}

func call(t *testing.T, dir string, req ipc.Request) ipc.Response {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	r, err := Call(ctx, dir, req)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func result(t *testing.T, dir string, req ipc.Request) Result {
	t.Helper()
	var r Result
	response := call(t, dir, req)
	if err := json.Unmarshal(response.Result, &r); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestSingleOwnerAndStartupCleanup(t *testing.T) {
	dir := fixture(t)
	stop := serve(t, dir)
	before, err := lock.Inspect(filepath.Join(dir, "coordinator.lock"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Open(t.Context(), dir, "run"); !errors.Is(err, lock.ErrAlreadyRunning) {
		t.Fatal(err)
	}
	after, err := lock.Inspect(filepath.Join(dir, "coordinator.lock"))
	if err != nil || before.Token != after.Token {
		t.Fatal("owner replaced", err)
	}
	if r := result(t, dir, request("builder", "inbox", "query", Params{})); r.Status != "QUERIED" || r.Messages == nil {
		t.Fatalf("%+v", r)
	}
	stop()
	for _, name := range []string{"coordinator.lock", "ipc.sock"} {
		if _, err := os.Lstat(filepath.Join(dir, name)); !errors.Is(err, os.ErrNotExist) {
			t.Fatal(name, err)
		}
	}
	serve(t, dir)
}

func TestIPCConcurrentIdempotencyAndReopen(t *testing.T) {
	dir := fixture(t)
	stop := serve(t, dir)
	send := request("main", "send", "send", Params{Recipient: "builder", Body: "work\n中文 ' ; $(no-exec)"})
	var group sync.WaitGroup
	responses := make(chan string, 12)
	for range 12 {
		group.Add(1)
		go func() { defer group.Done(); r := call(t, dir, send); responses <- string(r.Result) }()
	}
	group.Wait()
	close(responses)
	var original string
	for r := range responses {
		if original != "" && original != r {
			t.Fatal("concurrent retry changed result", original, r)
		}
		original = r
	}
	var saved Result
	if err := json.Unmarshal([]byte(original), &saved); err != nil || saved.Record == nil || saved.Status != "STORED" {
		t.Fatal(original, err)
	}
	id := saved.Record.Message.ID
	ack := request("builder", "ack", "ack", Params{MessageID: id})
	a1 := call(t, dir, ack)
	reply := request("builder", "reply", "reply", Params{MessageID: id, Body: "result", Status: "DONE"})
	r1 := call(t, dir, reply)
	stopReq := request("main", "stop", "stop", Params{})
	s1 := call(t, dir, stopReq)
	stop()
	serve(t, dir)
	if again := call(t, dir, ack); string(again.Result) != string(a1.Result) {
		t.Fatal("ACK changed after reopen")
	}
	if again := call(t, dir, reply); string(again.Result) != string(r1.Result) {
		t.Fatal("reply changed after reopen")
	}
	if again := call(t, dir, stopReq); string(again.Result) != string(s1.Result) {
		t.Fatal("stop changed after reopen")
	}
	var stopResult StopResult
	if err := json.Unmarshal(s1.Result, &stopResult); err != nil || stopResult.Executed || stopResult.CapabilityStatus != "PARTIAL" || stopResult.Diagnostics[0].Code != "PROCESS_STOP_UNIMPLEMENTED" {
		t.Fatal(string(s1.Result), err)
	}
	inbox := result(t, dir, request("builder", "inbox", "query", Params{Pending: true}))
	if inbox.Status != "QUERIED" || len(inbox.Messages) != 0 {
		t.Fatalf("%+v", inbox)
	}
	db, err := store.OpenExisting(filepath.Join(dir, "state.db"), false)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	events, err := db.JournalAfter(t.Context(), "run", 0)
	if err != nil || len(events) != 5 {
		t.Fatalf("duplicate writes: %+v %v", events, err)
	}
}

func TestActorGenerationAndNamespaceGates(t *testing.T) {
	dir := fixture(t)
	serve(t, dir)
	send := request("main", "send", "send", Params{Recipient: "builder", Body: "first"})
	saved := result(t, dir, send)
	id := saved.Record.Message.ID
	changed := send
	changed.Params = json.RawMessage(`{"recipient":"builder","body":"changed"}`)
	for _, req := range []ipc.Request{
		changed,
		request("main", "stop", "send", Params{}),
		request("main", "ack", "send", Params{MessageID: id}),
	} {
		r := result(t, dir, req)
		if r.Status != "FAILED" || r.Diagnostics[0].Code != ipc.IdempotencyConflict {
			t.Fatalf("%+v", r)
		}
	}
	call(t, dir, request("builder", "ack", "ack", Params{MessageID: id}))
	r := result(t, dir, request("builder", "stop", "ack", Params{}))
	if r.Status != "FAILED" || r.Diagnostics[0].Code != ipc.IdempotencyConflict {
		t.Fatalf("%+v", r)
	}
	call(t, dir, request("main", "stop", "stop", Params{}))
	for _, req := range []ipc.Request{request("main", "send", "stop", Params{Recipient: "builder", Body: "work"}), request("main", "ack", "stop", Params{MessageID: id})} {
		r := result(t, dir, req)
		if r.Status != "FAILED" || r.Diagnostics[0].Code != ipc.IdempotencyConflict {
			t.Fatalf("%+v", r)
		}
	}
	for _, method := range []string{"assign", "send", "inbox", "ack", "reply", "stop"} {
		for _, actor := range []ipc.Actor{{RunID: "run", AgentID: "missing", Generation: 1}, {RunID: "run", AgentID: "main", Generation: 2}, {RunID: "other", AgentID: "main", Generation: 1}} {
			req := request("main", method, "stop", Params{})
			req.Actor = actor
			_, err := Call(t.Context(), dir, req)
			var public *ipc.Error
			if !errors.As(err, &public) || (public.Code != ipc.ActorMismatch && public.Code != ipc.GenerationMismatch) {
				t.Fatal(method, actor, err)
			}
		}
	}
}

func TestUnimplementedAdaptersAndStrictParams(t *testing.T) {
	dir := fixture(t)
	serve(t, dir)
	for _, c := range []struct{ target, reason string }{{"builder", "RESET_ADAPTER_UNIMPLEMENTED"}, {"main", "SUBMIT_ADAPTER_UNIMPLEMENTED"}} {
		r := result(t, dir, request("main", "assign", c.target, Params{Recipient: c.target, Body: "work"}))
		if r.CapabilityStatus != "PARTIAL" || r.RuntimeCommunication != "UNVERIFIED" || r.Record == nil || r.Record.Coordination != c.reason || r.Record.Delivery.Attempt != 0 || r.Record.Delivery.ResetPhase != "NONE" {
			t.Fatalf("%+v", r)
		}
	}
	for _, raw := range []string{`null`, `[]`, `{"actor":{}}`, `{"recipient":"builder","recipient":"main"}`, `{"session":"fresh"}`, `{"body":null}`, `{"Body":"bad-case"}`} {
		req := request("main", "send", "invalid", Params{})
		req.Params = json.RawMessage(raw)
		_, err := Call(t.Context(), dir, req)
		var public *ipc.Error
		if !errors.As(err, &public) || public.Code != ipc.InvalidRequest {
			t.Fatal(raw, err)
		}
	}
	for _, method := range []string{"up", "attach", "recover", "claude"} {
		_, err := Call(t.Context(), dir, request("main", method, "unsupported", Params{}))
		var public *ipc.Error
		if !errors.As(err, &public) || public.Code != "UNIMPLEMENTED" {
			t.Fatal(method, err)
		}
	}
}

func TestFailedStartupPreservesExistingArtifacts(t *testing.T) {
	for _, mode := range []string{"wrong-run", "occupied-socket", "missing-store", "nonprivate"} {
		t.Run(mode, func(t *testing.T) {
			dir := fixture(t)
			runID := "run"
			switch mode {
			case "wrong-run":
				runID = "other"
			case "occupied-socket":
				if err := os.WriteFile(filepath.Join(dir, "ipc.sock"), []byte("keep"), 0600); err != nil {
					t.Fatal(err)
				}
			case "missing-store":
				if err := os.Remove(filepath.Join(dir, "state.db")); err != nil {
					t.Fatal(err)
				}
			case "nonprivate":
				if err := os.Chmod(dir, 0755); err != nil {
					t.Fatal(err)
				}
			}
			if c, err := Open(t.Context(), dir, runID); err == nil {
				c.Close()
				t.Fatal("startup succeeded")
			}
			if _, err := os.Lstat(filepath.Join(dir, "coordinator.lock")); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("startup leaked lock", err)
			}
			if mode == "occupied-socket" {
				data, err := os.ReadFile(filepath.Join(dir, "ipc.sock"))
				if err != nil || string(data) != "keep" {
					t.Fatal(string(data), err)
				}
			}
			if mode == "missing-store" {
				if _, err := os.Stat(filepath.Join(dir, "state.db")); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("created store", err)
				}
			}
		})
	}
}
