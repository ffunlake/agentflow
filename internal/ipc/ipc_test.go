package ipc

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"agentflow/internal/store"
)

func socketPath(t *testing.T) string {
	t.Helper()
	// Short names are necessary for macOS sockaddr_un, even under project TMPDIR.
	dir, err := os.MkdirTemp("", "ipc-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "s")
}

func validRequest(id string) Request {
	return Request{V: Version, RequestID: id, Actor: Actor{RunID: "run", AgentID: "builder", Generation: 2}, Method: "echo", Params: json.RawMessage(`{"body":"hello"}`)}
}

func validateActor(_ context.Context, actor Actor) error {
	if actor.RunID != "run" || actor.AgentID != "builder" {
		return &Error{Code: ActorMismatch, Message: "unknown actor"}
	}
	if actor.Generation != 2 {
		return &Error{Code: GenerationMismatch, Message: "stale generation"}
	}
	return nil
}

func echoHandler(_ context.Context, r Request) (any, error) {
	if r.Method != "echo" {
		return nil, &Error{Code: MethodNotFound, Message: "unknown method"}
	}
	return r.Params, nil
}

func startServer(t *testing.T, handler Handler, options ServerOptions) (Client, *Server) {
	t.Helper()
	path := socketPath(t)
	server, err := Listen(path, handler, options)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- server.Serve(context.Background()) }()
	t.Cleanup(func() {
		if err := server.Close(); err != nil {
			t.Error(err)
		}
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Error("server failed to stop")
		}
	})
	return Client{Path: path}, server
}

func callContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func dial(t *testing.T, path string) *net.UnixConn {
	t.Helper()
	conn, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	return conn
}

func requestFrame(t *testing.T, r Request) []byte {
	t.Helper()
	frame, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	return append(frame, '\n')
}

func readResponse(t *testing.T, reader *bufio.Reader) Response {
	t.Helper()
	frame, err := readFrame(reader)
	if err != nil {
		t.Fatal(err)
	}
	var response Response
	if err := json.Unmarshal(frame, &response); err != nil {
		t.Fatalf("response %q: %v", frame, err)
	}
	if _, err := decodeResponse(frame, response.RequestID); err != nil {
		t.Fatalf("invalid response envelope: %v", err)
	}
	return response
}

func requireCode(t *testing.T, response Response, id, code string) {
	t.Helper()
	if response.RequestID != id || response.OK || response.Result != nil || response.Error == nil || response.Error.Code != code || response.Error.Message == "" {
		t.Fatalf("response = %+v, want id=%q error=%s", response, id, code)
	}
}

func TestServerClientAndPermissions(t *testing.T) {
	client, _ := startServer(t, echoHandler, ServerOptions{ValidateActor: validateActor})
	info, err := os.Stat(client.Path)
	if err != nil || info.Mode().Perm() != 0600 || info.Mode()&os.ModeSocket == 0 {
		t.Fatalf("socket info=%v err=%v", info, err)
	}
	request := validRequest("  原样 / \"id\"\n  ")
	response, err := client.Call(callContext(t), request)
	if err != nil || !response.OK || response.RequestID != request.RequestID || string(response.Result) != string(request.Params) || response.Error != nil {
		t.Fatalf("response=%+v err=%v", response, err)
	}
	request.Params = json.RawMessage(`null`)
	response, err = client.Call(callContext(t), request)
	if err != nil || !response.OK || string(response.Result) != "null" {
		t.Fatalf("null result response=%+v err=%v", response, err)
	}
}

func TestSplitAndCoalescedFrames(t *testing.T) {
	client, _ := startServer(t, echoHandler, ServerOptions{ValidateActor: validateActor})
	conn := dial(t, client.Path)
	reader := bufio.NewReader(conn)
	first := requestFrame(t, validRequest("split"))
	// Deliberately fragment inside an envelope and a JSON string.
	for _, part := range [][]byte{first[:7], first[7:37], first[37 : len(first)-2], first[len(first)-2:]} {
		if _, err := conn.Write(part); err != nil {
			t.Fatal(err)
		}
	}
	if response := readResponse(t, reader); !response.OK || response.RequestID != "split" {
		t.Fatalf("split response=%+v", response)
	}
	frames := append(requestFrame(t, validRequest("coalesced-1")), requestFrame(t, validRequest("coalesced-2"))...)
	if _, err := conn.Write(frames); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"coalesced-1", "coalesced-2"} {
		if response := readResponse(t, reader); !response.OK || response.RequestID != id {
			t.Fatalf("coalesced response=%+v, want id=%s", response, id)
		}
	}
}

func TestInvalidFramesAndRecovery(t *testing.T) {
	var handled atomic.Int64
	client, _ := startServer(t, func(ctx context.Context, r Request) (any, error) {
		handled.Add(1)
		return echoHandler(ctx, r)
	}, ServerOptions{ValidateActor: validateActor})
	valid := strings.TrimSuffix(string(requestFrame(t, validRequest("id"))), "\n")
	tests := []struct {
		name, frame, id string
	}{
		{"empty", "", ""},
		{"whitespace", " \t\r", ""},
		{"invalid JSON", `{"requestId":"id",`, ""},
		{"two documents", valid + valid, ""},
		{"array", `[]`, ""},
		{"null", `null`, ""},
		{"unknown", strings.Replace(valid, `"v":1`, `"extra":0,"v":1`, 1), "id"},
		{"wrong case", strings.Replace(valid, `"method"`, `"Method"`, 1), "id"},
		{"duplicate method", strings.Replace(valid, `"method":"echo"`, `"method":"a","method":"echo"`, 1), "id"},
		{"duplicate ID", strings.Replace(valid, `"requestId":"id"`, `"requestId":"other","requestId":"id"`, 1), ""},
		{"unknown actor field", strings.Replace(valid, `"runId":"run"`, `"runId":"run","session":"unexpected"`, 1), "id"},
		{"duplicate actor field", strings.Replace(valid, `"generation":2`, `"generation":1,"generation":2`, 1), "id"},
		{"negative generation", strings.Replace(valid, `"generation":2`, `"generation":-1`, 1), "id"},
		{"fractional generation", strings.Replace(valid, `"generation":2`, `"generation":2.5`, 1), "id"},
		{"version", strings.Replace(valid, `"v":1`, `"v":2`, 1), "id"},
		{"version string", strings.Replace(valid, `"v":1`, `"v":"1"`, 1), "id"},
		{"empty method", strings.Replace(valid, `"method":"echo"`, `"method":" "`, 1), "id"},
		{"ID type", strings.Replace(valid, `"requestId":"id"`, `"requestId":3`, 1), ""},
		{"invalid UTF8", strings.Replace(valid, "hello", string([]byte{0xff}), 1), ""},
	}
	// Every required field is rejected independently when absent or null.
	for _, key := range []string{"v", "requestId", "actor", "method", "params"} {
		var fields map[string]json.RawMessage
		_ = json.Unmarshal([]byte(valid), &fields)
		delete(fields, key)
		frame, _ := json.Marshal(fields)
		id := "id"
		if key == "requestId" {
			id = ""
		}
		tests = append(tests, struct{ name, frame, id string }{"missing " + key, string(frame), id})
		if key != "params" { // params:null is an explicit, valid payload
			fields[key] = json.RawMessage(`null`)
			frame, _ = json.Marshal(fields)
			tests = append(tests, struct{ name, frame, id string }{"null " + key, string(frame), id})
		}
	}
	for _, key := range []string{"runId", "agentId", "generation"} {
		var fields map[string]json.RawMessage
		_ = json.Unmarshal([]byte(valid), &fields)
		var actor map[string]json.RawMessage
		_ = json.Unmarshal(fields["actor"], &actor)
		delete(actor, key)
		fields["actor"], _ = json.Marshal(actor)
		frame, _ := json.Marshal(fields)
		tests = append(tests, struct{ name, frame, id string }{"missing actor " + key, string(frame), "id"})
		actor[key] = json.RawMessage(`null`)
		fields["actor"], _ = json.Marshal(actor)
		frame, _ = json.Marshal(fields)
		tests = append(tests, struct{ name, frame, id string }{"null actor " + key, string(frame), "id"})
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			conn := dial(t, client.Path)
			reader := bufio.NewReader(conn)
			if _, err := conn.Write(append([]byte(tc.frame+"\n"), requestFrame(t, validRequest("recovered"))...)); err != nil {
				t.Fatal(err)
			}
			requireCode(t, readResponse(t, reader), tc.id, InvalidRequest)
			if response := readResponse(t, reader); !response.OK || response.RequestID != "recovered" {
				t.Fatalf("did not recover after invalid frame: %+v", response)
			}
		})
	}
	if got := handled.Load(); got != int64(len(tests)) {
		t.Fatalf("invalid frames reached handler: got %d calls, want %d", got, len(tests))
	}
}

func TestFrameLimitAndTruncatedFrame(t *testing.T) {
	var handled atomic.Int64
	client, _ := startServer(t, func(_ context.Context, r Request) (any, error) {
		handled.Add(1)
		return len(r.Params), nil
	}, ServerOptions{ValidateActor: validateActor})
	request := validRequest("boundary")
	request.Params = json.RawMessage(`""`)
	base := len(requestFrame(t, request)) - 1
	request.Params = json.RawMessage(`"` + strings.Repeat("x", MaxFrameSize-base) + `"`)
	frame := requestFrame(t, request)
	if len(frame) != MaxFrameSize+1 {
		t.Fatalf("bad boundary fixture length=%d", len(frame))
	}
	conn := dial(t, client.Path)
	if _, err := conn.Write(frame); err != nil {
		t.Fatal(err)
	}
	if response := readResponse(t, bufio.NewReader(conn)); !response.OK {
		t.Fatalf("exactly 1 MiB must be accepted: %+v", response)
	}
	for _, suffix := range []string{"\n", ""} {
		t.Run(fmt.Sprintf("overflow LF=%v", suffix != ""), func(t *testing.T) {
			conn := dial(t, client.Path)
			// No terminating LF is required for the server to detect overflow.
			_, _ = conn.Write([]byte(strings.Repeat("x", MaxFrameSize+1) + suffix))
			reader := bufio.NewReader(conn)
			requireCode(t, readResponse(t, reader), "", FrameTooLarge)
			if _, err := reader.ReadByte(); err == nil {
				t.Fatal("oversized connection remained readable")
			}
		})
	}
	conn = dial(t, client.Path)
	_, _ = conn.Write(bytes.TrimSuffix(requestFrame(t, validRequest("unfinished")), []byte{'\n'}))
	if err := conn.CloseWrite(); err != nil {
		t.Fatal(err)
	}
	requireCode(t, readResponse(t, bufio.NewReader(conn)), "", InvalidRequest)
	if got := handled.Load(); got != 1 {
		t.Fatalf("invalid frame dispatched: calls=%d", got)
	}
	request.Params = json.RawMessage(`"` + strings.Repeat("x", MaxFrameSize) + `"`)
	_, err := client.Call(callContext(t), request)
	var remote *Error
	if !errors.As(err, &remote) || remote.Code != FrameTooLarge {
		t.Fatalf("client oversized request error=%v", err)
	}
}

func TestValidationAndHandlerErrors(t *testing.T) {
	var handled atomic.Int64
	client, _ := startServer(t, func(ctx context.Context, r Request) (any, error) {
		handled.Add(1)
		switch r.Method {
		case "conflict":
			return nil, fmt.Errorf("wrapped: %w", store.ErrIdempotencyConflict)
		case "internal":
			return nil, errors.New("private diagnostic must not leak")
		case "panic":
			panic("private panic must not leak")
		case "unmarshalable":
			return make(chan int), nil
		case "large":
			return strings.Repeat("x", MaxFrameSize), nil
		default:
			return echoHandler(ctx, r)
		}
	}, ServerOptions{ValidateActor: validateActor})
	tests := []struct {
		name, code string
		change     func(*Request)
	}{
		{"actor", ActorMismatch, func(r *Request) { r.Actor.AgentID = "other" }},
		{"run", ActorMismatch, func(r *Request) { r.Actor.RunID = "other" }},
		{"generation", GenerationMismatch, func(r *Request) { r.Actor.Generation = 1 }},
		{"external", GenerationMismatch, func(r *Request) { r.Actor.Generation = 0 }},
		{"method", MethodNotFound, func(r *Request) { r.Method = "absent" }},
		{"conflict", IdempotencyConflict, func(r *Request) { r.Method = "conflict" }},
		{"internal", Internal, func(r *Request) { r.Method = "internal" }},
		{"panic", Internal, func(r *Request) { r.Method = "panic" }},
		{"unmarshalable", Internal, func(r *Request) { r.Method = "unmarshalable" }},
		{"large", FrameTooLarge, func(r *Request) { r.Method = "large" }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := validRequest(tc.name)
			tc.change(&r)
			response, err := client.Call(callContext(t), r)
			requireCode(t, response, r.RequestID, tc.code)
			var remote *Error
			if !errors.As(err, &remote) || remote.Code != tc.code {
				t.Fatalf("client error=%v", err)
			}
			if strings.Contains(remote.Message, "private") {
				t.Fatal("internal diagnostics leaked")
			}
		})
	}
	if got := handled.Load(); got != 6 {
		t.Fatalf("identity rejection reached handler: calls=%d", got)
	}
}

func TestConcurrentClientsDoNotCrossResponses(t *testing.T) {
	const count = 16
	entered := make(chan struct{}, count)
	release := make(chan struct{})
	client, _ := startServer(t, func(ctx context.Context, r Request) (any, error) {
		entered <- struct{}{}
		select {
		case <-release:
			return map[string]any{"id": r.RequestID, "params": r.Params}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}, ServerOptions{ValidateActor: validateActor})
	ctx := callContext(t)
	errorsCh := make(chan error, count)
	for i := 0; i < count; i++ {
		go func() {
			r := validRequest(fmt.Sprintf("request-%d", i))
			r.Params = json.RawMessage(fmt.Sprintf(`{"client":%d}`, i))
			response, err := client.Call(ctx, r)
			if err == nil {
				var result struct {
					ID     string          `json:"id"`
					Params json.RawMessage `json:"params"`
				}
				err = json.Unmarshal(response.Result, &result)
				if err == nil && (response.RequestID != r.RequestID || result.ID != r.RequestID || !bytes.Equal(result.Params, r.Params)) {
					err = fmt.Errorf("crossed response for %s: %+v", r.RequestID, response)
				}
			}
			errorsCh <- err
		}()
	}
	// All handlers must enter before any is released: this also proves the
	// server is not processing independent client connections serially.
	for i := 0; i < count; i++ {
		select {
		case <-entered:
		case <-ctx.Done():
			close(release)
			t.Fatal("client connections did not execute concurrently")
		}
	}
	close(release)
	for i := 0; i < count; i++ {
		if err := <-errorsCh; err != nil {
			t.Error(err)
		}
	}
}

func TestDisconnectDoesNotRetryOrStopServer(t *testing.T) {
	var handled atomic.Int64
	entered, release, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
	client, _ := startServer(t, func(ctx context.Context, r Request) (any, error) {
		handled.Add(1)
		if r.RequestID == "disconnect" {
			close(entered)
			select {
			case <-release:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			defer close(finished)
		}
		return echoHandler(ctx, r)
	}, ServerOptions{ValidateActor: validateActor})
	conn := dial(t, client.Path)
	_, _ = conn.Write([]byte(`{"v":`))
	_ = conn.Close() // incomplete request must never reach the handler
	conn = dial(t, client.Path)
	_, _ = conn.Write(requestFrame(t, validRequest("disconnect")))
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("handler not started")
	}
	_ = conn.Close() // outcome is now unknown to the caller
	close(release)
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("handler not finished")
	}
	response, err := client.Call(callContext(t), validRequest("after"))
	if err != nil || !response.OK || handled.Load() != 2 {
		t.Fatalf("server failed after disconnect or retried: %+v %v calls=%d", response, err, handled.Load())
	}
}

func TestValidationOnEveryRequestNoIPCCache(t *testing.T) {
	var generation atomic.Int64
	generation.Store(2)
	var handled atomic.Int64
	client, _ := startServer(t, func(_ context.Context, r Request) (any, error) {
		return handled.Add(1), nil
	}, ServerOptions{ValidateActor: func(_ context.Context, actor Actor) error {
		if actor.Generation != generation.Load() {
			return &Error{Code: GenerationMismatch, Message: "stale generation"}
		}
		return nil
	}})
	conn := dial(t, client.Path)
	reader := bufio.NewReader(conn)
	r := validRequest("same-id")
	for i := 1; i <= 2; i++ {
		_, _ = conn.Write(requestFrame(t, r))
		response := readResponse(t, reader)
		if !response.OK || string(response.Result) != fmt.Sprint(i) {
			t.Fatalf("IPC cached retry response: %+v", response)
		}
	}
	generation.Store(3)
	_, _ = conn.Write(requestFrame(t, r))
	requireCode(t, readResponse(t, reader), r.RequestID, GenerationMismatch)
	if handled.Load() != 2 {
		t.Fatal("stale request dispatched")
	}
}

func TestStoreIdempotencyAcrossIPCConnections(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := callContext(t)
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateRun(ctx, store.Run{RunID: "run", ConfigSnapshot: `{}`, ConfigDigest: "digest", WorkspaceID: "workspace"}); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateSeat(ctx, store.Seat{RunID: "run", AgentID: "builder", Generation: 2, Current: true,
		Tool: "opencode", NativeSessionID: "session", WorkspaceID: "workspace", SurfaceID: "surface", Status: "READY"}); err != nil {
		t.Fatal(err)
	}
	client, _ := startServer(t, func(ctx context.Context, r Request) (any, error) {
		var params struct {
			Body string `json:"body"`
		}
		if err := json.Unmarshal(r.Params, &params); err != nil {
			return nil, &Error{Code: InvalidRequest, Message: "invalid params"}
		}
		message, err := db.SaveOutbox(ctx, store.Outbox{
			Message: store.Message{RunID: r.Actor.RunID, Sender: r.Actor.AgentID, SenderGeneration: r.Actor.Generation,
				Recipient: "builder", Kind: "message", Body: params.Body, RequestID: r.RequestID},
			Delivery: store.Delivery{TargetGeneration: 2},
		})
		if err != nil {
			return nil, err
		}
		return map[string]string{"id": message.ID, "status": message.Status}, nil
	}, ServerOptions{ValidateActor: validateActor})
	r := validRequest("store-retry")
	first, err := client.Call(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	second, err := client.Call(ctx, r)
	if err != nil || !bytes.Equal(first.Result, second.Result) || !bytes.Contains(second.Result, []byte(`"status":"STORED"`)) {
		t.Fatalf("store retry changed response: first=%+v second=%+v err=%v", first, second, err)
	}
	r.Params = json.RawMessage(`{"body":"different"}`)
	conflict, err := client.Call(ctx, r)
	requireCode(t, conflict, r.RequestID, IdempotencyConflict)
	var remote *Error
	if !errors.As(err, &remote) || remote.Code != IdempotencyConflict {
		t.Fatalf("conflict error=%v", err)
	}
	journal, err := db.JournalAfter(ctx, "run", 0)
	if err != nil || len(journal) != 1 {
		t.Fatalf("retry/conflict generated extra writes: journal=%+v err=%v", journal, err)
	}
}

func TestLifecycleAndClientCancellation(t *testing.T) {
	entered := make(chan struct{})
	var once sync.Once
	client, server := startServer(t, func(ctx context.Context, _ Request) (any, error) {
		once.Do(func() { close(entered) })
		<-ctx.Done()
		return nil, ctx.Err()
	}, ServerOptions{ValidateActor: validateActor})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := client.Call(ctx, validRequest("cancel")); done <- err }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("handler did not enter")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation error=%v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("client cancellation did not interrupt read")
	}
	idle := dial(t, client.Path)
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := idle.Read(make([]byte, 1)); err == nil {
		t.Fatal("shutdown left idle connection open")
	}
	if _, err := os.Stat(client.Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("socket not cleaned up: %v", err)
	}
	if err := server.Serve(context.Background()); err == nil {
		t.Fatal("second Serve unexpectedly succeeded")
	}
}

func TestServeContextCancellation(t *testing.T) {
	path := socketPath(t)
	server, err := Listen(path, echoHandler, ServerOptions{ValidateActor: validateActor})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx) }()
	conn := dial(t, path)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not stop on cancellation")
	}
	if _, err := conn.Read(make([]byte, 1)); err == nil {
		t.Fatal("context cancellation left connection open")
	}
}

func TestListenValidationAndExistingPath(t *testing.T) {
	path := socketPath(t)
	for _, tc := range []struct {
		path    string
		handler Handler
		options ServerOptions
	}{
		{"", echoHandler, ServerOptions{ValidateActor: validateActor}},
		{path, nil, ServerOptions{ValidateActor: validateActor}},
		{path, echoHandler, ServerOptions{}},
		{filepath.Join(path, "missing", "s"), echoHandler, ServerOptions{ValidateActor: validateActor}},
	} {
		if server, err := Listen(tc.path, tc.handler, tc.options); err == nil {
			_ = server.Close()
			t.Fatal("Listen accepted invalid arguments")
		}
	}
	if err := os.WriteFile(path, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if server, err := Listen(path, echoHandler, ServerOptions{ValidateActor: validateActor}); err == nil {
		_ = server.Close()
		t.Fatal("Listen replaced existing file")
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "keep" {
		t.Fatalf("existing file changed: %q %v", data, err)
	}
}

func TestClientRejectsInvalidResponses(t *testing.T) {
	tests := []string{
		`{"requestId":"wrong","ok":true,"result":1}`,
		`{"requestId":"id","ok":true}`,
		`{"requestId":"id","ok":true,"result":1,"error":null}`,
		`{"requestId":"id","ok":false,"error":{"code":"X","message":"x"},"result":null}`,
		`{"requestId":"id","ok":false,"error":null}`,
		`{"requestId":"id","ok":false,"error":{"code":"X"}}`,
		`{"requestId":"id","ok":null,"result":1}`,
		`{"requestId":"id","ok":true,"ok":false,"result":1}`,
		`{"requestId":"id","ok":true,"result":1,"unknown":0}`,
		`not JSON`,
		strings.Repeat("x", MaxFrameSize+1),
	}
	for i, frame := range tests {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			path := socketPath(t)
			listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = listener.Close() })
			done := make(chan struct{})
			go func() {
				defer close(done)
				conn, err := listener.AcceptUnix()
				if err != nil {
					return
				}
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
				_, _ = readFrame(bufio.NewReader(conn))
				_, _ = io.WriteString(conn, frame+"\n")
			}()
			if response, err := (Client{Path: path}).Call(callContext(t), validRequest("id")); err == nil || response.OK {
				t.Fatalf("invalid response accepted: %+v %v", response, err)
			}
			<-done
		})
	}
}
