package coordinator

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"agentflow/internal/ipc"
)

func TestInvalidParamsLeaveMessageDeliveryJournalUnchanged(t *testing.T) {
	for _, c := range []struct{ name, method, actor, params string }{
		{"send-compound", "send", "main", `{"recipient":"builder","body":"work","recipient recipient_generation":123}`},
		{"send-all-fields-compound", "send", "main", `{"recipient":"builder","body":"work","recipient recipient_generation body":123}`},
		{"assign-compound", "assign", "main", `{"recipient":"builder","body":"work","body session":123}`},
		{"assign-session-compound", "assign", "main", `{"recipient":"builder","body":"work","session mode":123}`},
		{"reply-compound", "reply", "builder", `{"message_id":%s,"body":"result","message_id body":123}`},
		{"reply-status-compound", "reply", "builder", `{"message_id":%s,"body":"result","body status":123}`},
		{"inbox-compound", "inbox", "builder", `{"after":0,"after pending":123}`},
		{"send-duplicate", "send", "main", `{"recipient":"builder","body":"work","body":"other"}`},
		{"assign-null", "assign", "main", `{"recipient":"builder","body":"work","session":null}`},
		{"send-body-type", "send", "main", `{"recipient":"builder","body":123}`},
		{"send-generation-type", "send", "main", `{"recipient":"builder","body":"work","recipient_generation":"1"}`},
		{"inbox-pending-type", "inbox", "builder", `{"pending":"true"}`},
		{"ack-unknown", "ack", "builder", `{"message_id":%s,"message_id unknown":123}`},
		{"reconcile-unknown", "reconcile", "main", `{"message_id":%s,"message_id unknown":123}`},
		{"resume-null", "resume", "main", `{"message_id":null}`},
		{"stop-unknown", "stop", "main", `{"unknown":123}`},
		{"send-case", "send", "main", `{"recipient":"builder","body":"work","Body":"other"}`},
		{"send-empty-field", "send", "main", `{"recipient":"builder","body":"work","":123}`},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := fixture(t)
			serve(t, dir)
			seed := result(t, dir, request("main", "send", "seed", Params{Recipient: "builder", Body: "existing work"}))
			if seed.Record == nil || seed.Status != "STORED" {
				t.Fatalf("seed: %+v", seed)
			}
			before := messageTablesSnapshot(t, dir)
			raw := c.params
			if strings.Contains(raw, "%s") {
				id, _ := json.Marshal(seed.Record.Message.ID)
				raw = fmt.Sprintf(raw, id)
			}
			req := request(c.actor, c.method, "invalid", Params{})
			req.Params = json.RawMessage(raw)
			response, err := Call(t.Context(), dir, req)
			var public *ipc.Error
			if !errors.As(err, &public) || public.Code != ipc.InvalidRequest || response.OK || response.Error == nil || response.Error.Code != ipc.InvalidRequest {
				t.Errorf("want INVALID_REQUEST: ok=%t error=%v result=%s err=%v", response.OK, response.Error, response.Result, err)
			}
			if after := messageTablesSnapshot(t, dir); before != after {
				t.Errorf("invalid params mutated message/delivery/journal\nbefore=%s\nafter=%s", before, after)
			}
		})
	}
}

// Compare complete persisted rows, including delivery status/timestamps and
// journal contents, so both insertions and updates are detected.
func messageTablesSnapshot(t *testing.T, dir string) string {
	t.Helper()
	u := url.URL{Scheme: "file", Path: filepath.Join(dir, "state.db"), RawQuery: "mode=ro"}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	tx, err := db.BeginTx(t.Context(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	tables := map[string][][]any{}
	for _, table := range []string{"messages", "deliveries", "journal"} {
		rows, err := tx.QueryContext(t.Context(), "SELECT * FROM "+table+" ORDER BY rowid")
		if err != nil {
			t.Fatal(err)
		}
		columns, err := rows.Columns()
		if err != nil {
			rows.Close()
			t.Fatal(err)
		}
		tables[table] = [][]any{}
		for rows.Next() {
			values := make([]any, len(columns))
			pointers := make([]any, len(columns))
			for i := range values {
				pointers[i] = &values[i]
			}
			if err := rows.Scan(pointers...); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			tables[table] = append(tables[table], values)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		rows.Close()
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(tables)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
