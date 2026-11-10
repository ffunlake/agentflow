// Package ipc implements the version 1 local NDJSON request/response transport.
// A successful response describes only the operation performed by its handler;
// transport success is not evidence of message delivery or execution.
package ipc

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"agentflow/internal/store"
)

const (
	Version      = 1
	MaxFrameSize = 1 << 20 // UTF-8 bytes, excluding the terminating LF

	InvalidRequest      = "INVALID_REQUEST"
	FrameTooLarge       = "FRAME_TOO_LARGE"
	ActorMismatch       = "ACTOR_MISMATCH"
	GenerationMismatch  = "GENERATION_MISMATCH"
	MethodNotFound      = "METHOD_NOT_FOUND"
	Internal            = "INTERNAL"
	IdempotencyConflict = "IDEMPOTENCY_CONFLICT"
)

// Actor identifies a caller's claimed seat. ValidateActor must verify this claim
// against trusted runtime identity and the current generation, on every request.
// Generation zero is reserved for an external actor without a seat binding;
// accepting such actors is a decision for ValidateActor.
type Actor struct {
	RunID      string `json:"runId"`
	AgentID    string `json:"agentId"`
	Generation int64  `json:"generation"`
}

type Request struct {
	V         int             `json:"v"`
	RequestID string          `json:"requestId"`
	Actor     Actor           `json:"actor"`
	Method    string          `json:"method"`
	Params    json.RawMessage `json:"params"`
}

// Response always includes requestId and ok. Success includes result (even null)
// and failure includes error, never both. An unreadable ID is returned as "".
type Response struct {
	RequestID string          `json:"requestId"`
	OK        bool            `json:"ok"`
	Result    json.RawMessage `json:"result,omitempty"`
	Error     *Error          `json:"error,omitempty"`
}

// Error is a public wire error. Only explicitly returned Errors expose their
// message; unexpected Go errors and panics become a generic INTERNAL response.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

func wireError(err error) *Error {
	if errors.Is(err, store.ErrIdempotencyConflict) {
		return &Error{Code: IdempotencyConflict, Message: "request ID conflicts with an existing message"}
	}
	var public *Error
	if errors.As(err, &public) && public != nil && public.Code != "" && public.Message != "" {
		return &Error{Code: public.Code, Message: public.Message}
	}
	return &Error{Code: Internal, Message: "internal error"}
}

func failure(id string, err *Error) Response { return Response{RequestID: id, Error: err} }

// object requires exact, unique keys. encoding/json's struct decoder otherwise
// permits case-insensitive field matching and silently overwrites duplicates.
// Values (in particular params) remain opaque JSON for the handler.
func object(data []byte, allowed ...string) (map[string]json.RawMessage, error) {
	if !utf8.Valid(data) || !json.Valid(data) {
		return nil, errors.New("expected valid UTF-8 JSON")
	}
	d := json.NewDecoder(bytes.NewReader(data))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return nil, errors.New("expected an object")
	}
	fields := make(map[string]json.RawMessage)
	for d.More() {
		token, err := d.Token()
		if err != nil {
			return nil, err
		}
		key, ok := token.(string)
		if !ok {
			return nil, errors.New("expected a field name")
		}
		if _, exists := fields[key]; exists {
			return nil, fmt.Errorf("duplicate field %q", key)
		}
		known := false
		for _, name := range allowed {
			known = known || key == name
		}
		if !known {
			return nil, fmt.Errorf("unknown field %q", key)
		}
		var value json.RawMessage
		if err := d.Decode(&value); err != nil {
			return nil, err
		}
		fields[key] = value
	}
	return fields, nil
}

func decodeRequired(fields map[string]json.RawMessage, key string, dst any) error {
	value, exists := fields[key]
	if !exists || bytes.Equal(value, []byte("null")) {
		return fmt.Errorf("missing or null field %q", key)
	}
	return json.Unmarshal(value, dst)
}

func decodeRequest(frame []byte) (Request, error) {
	var r Request
	// Preserve an unambiguous string ID even if another envelope field is invalid.
	// Invalid JSON or duplicate requestId fields cannot supply a trustworthy ID.
	if utf8.Valid(frame) && json.Valid(frame) {
		d := json.NewDecoder(bytes.NewReader(frame))
		if token, _ := d.Token(); token == json.Delim('{') {
			seen := false
			for d.More() {
				key, _ := d.Token()
				var value json.RawMessage
				if d.Decode(&value) != nil {
					break
				}
				if key == "requestId" {
					if seen {
						r.RequestID = ""
						break
					}
					seen = true
					_ = json.Unmarshal(value, &r.RequestID)
				}
			}
		}
	}
	fields, err := object(frame, "v", "requestId", "actor", "method", "params")
	if err != nil {
		return r, err
	}
	for _, field := range []struct {
		name string
		dst  any
	}{{"v", &r.V}, {"requestId", &r.RequestID}, {"method", &r.Method}} {
		if err := decodeRequired(fields, field.name, field.dst); err != nil {
			return r, err
		}
	}
	actor, err := object(fields["actor"], "runId", "agentId", "generation")
	if err != nil {
		return r, fmt.Errorf("actor: %w", err)
	}
	for _, field := range []struct {
		name string
		dst  any
	}{{"runId", &r.Actor.RunID}, {"agentId", &r.Actor.AgentID}, {"generation", &r.Actor.Generation}} {
		if err := decodeRequired(actor, field.name, field.dst); err != nil {
			return r, fmt.Errorf("actor: %w", err)
		}
	}
	r.Params = fields["params"]
	if r.V != Version || strings.TrimSpace(r.RequestID) == "" || strings.TrimSpace(r.Method) == "" ||
		strings.TrimSpace(r.Actor.RunID) == "" || strings.TrimSpace(r.Actor.AgentID) == "" ||
		r.Actor.Generation < 0 || len(r.Params) == 0 {
		return r, errors.New("invalid version, identity, method or missing params")
	}
	return r, nil
}

var errFrameTooLarge = errors.New("frame exceeds 1 MiB")

// readFrame has bounded memory even if a peer never sends LF. Overflow is fatal
// for that connection: do not drain arbitrary input or interpret its suffix.
func readFrame(reader *bufio.Reader) ([]byte, error) {
	var frame []byte
	for {
		// ReadByte consumes the reader's buffer without issuing a syscall for
		// each byte. Unlike ReadSlice it cannot wait past the size boundary for
		// an additional full buffer or a newline from a stalled peer.
		b, err := reader.ReadByte()
		if errors.Is(err, io.EOF) && len(frame) != 0 {
			return frame, io.ErrUnexpectedEOF
		}
		if err != nil {
			return frame, err
		}
		if b == '\n' {
			return frame, nil
		}
		if len(frame) == MaxFrameSize {
			return nil, errFrameTooLarge
		}
		frame = append(frame, b)
	}
}

func writeFrame(w io.Writer, frame []byte) error {
	if len(frame) > MaxFrameSize {
		return errFrameTooLarge
	}
	frame = append(frame, '\n')
	for len(frame) > 0 {
		n, err := w.Write(frame)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		frame = frame[n:]
	}
	return nil
}
