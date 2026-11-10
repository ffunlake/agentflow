package ipc

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"agentflow/internal/store"
)

// The first request commits but the caller discards its response. Reconnects
// must validate identity again and deduplicate in SQLite, never in an IPC cache.
func TestFaultMatrixIPCResponseLost(t *testing.T) {
	for _, tc := range []struct{ fault, code string }{
		{"same-request", ""},
		{"conflicting-body", IdempotencyConflict},
		{"old-generation", GenerationMismatch},
		{"cross-run", ActorMismatch},
	} {
		t.Run(tc.fault, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "state.db")
			db, err := store.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { db.Close() }()
			if err := db.Migrate(t.Context()); err != nil {
				t.Fatal(err)
			}
			if err := db.CreateRun(t.Context(), store.Run{RunID: "run", ConfigSnapshot: `{}`, ConfigDigest: "digest"}); err != nil {
				t.Fatal(err)
			}
			if err := db.CreateSeat(t.Context(), store.Seat{RunID: "run", AgentID: "builder", Generation: 2, Current: true, Tool: "codex", Status: "READY"}); err != nil {
				t.Fatal(err)
			}
			committed := make(chan store.Message, 3)
			client, server := startServer(t, func(ctx context.Context, r Request) (any, error) {
				var params struct{ Body string }
				if err := json.Unmarshal(r.Params, &params); err != nil {
					return nil, err
				}
				m, err := db.StoreMessage(ctx, store.MessageCommand{Actor: store.MessageActor{RunID: r.Actor.RunID, AgentID: r.Actor.AgentID, Generation: r.Actor.Generation}, RequestID: r.RequestID, Recipient: "builder", Kind: "send", Body: params.Body})
				if err == nil {
					committed <- m.Message
				}
				return m, err
			}, ServerOptions{ValidateActor: validateActor})
			request := validRequest("lost-response")
			conn := dial(t, client.Path)
			if _, err := conn.Write(requestFrame(t, request)); err != nil {
				t.Fatal(err)
			}
			var original store.Message
			select {
			case original = <-committed:
			case <-time.After(5 * time.Second):
				t.Fatal("request did not commit")
			}
			_ = conn.Close() // no response is read, so caller outcome is unknown
			if err := server.Close(); err != nil {
				t.Fatal(err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			db, err = store.OpenExisting(path, true)
			if err != nil {
				t.Fatal(err)
			}
			// New transport and database handles emulate coordinator restart.
			client, _ = startServer(t, func(ctx context.Context, r Request) (any, error) {
				var params struct{ Body string }
				if err := json.Unmarshal(r.Params, &params); err != nil {
					return nil, err
				}
				return db.StoreMessage(ctx, store.MessageCommand{Actor: store.MessageActor{RunID: r.Actor.RunID, AgentID: r.Actor.AgentID, Generation: r.Actor.Generation}, RequestID: r.RequestID, Recipient: "builder", Kind: "send", Body: params.Body})
			}, ServerOptions{ValidateActor: validateActor})
			switch tc.fault {
			case "conflicting-body":
				request.Params = json.RawMessage(`{"body":"different"}`)
			case "old-generation":
				request.Actor.Generation--
			case "cross-run":
				request.Actor.RunID = "other"
			}
			for range 2 {
				response, err := client.Call(callContext(t), request)
				if tc.code != "" {
					requireCode(t, response, request.RequestID, tc.code)
					var remote *Error
					if !errors.As(err, &remote) || remote.Code != tc.code {
						t.Fatal(err)
					}
				} else {
					var record store.MessageRecord
					if err != nil || json.Unmarshal(response.Result, &record) != nil || !reflect.DeepEqual(original, record.Message) {
						t.Fatal("lost response retry changed message", response, err)
					}
				}
			}
			got, err := db.GetMessage(t.Context(), original.ID)
			if err != nil || !reflect.DeepEqual(original, got) {
				t.Fatal("committed work changed", got, err)
			}
			journal, err := db.JournalAfter(t.Context(), "run", 0)
			if err != nil || len(journal) != 1 || journal[0].Event != "MESSAGE_STORED" {
				t.Fatal("IPC retry created duplicate body/action/ACK", journal, err)
			}
		})
	}
}
