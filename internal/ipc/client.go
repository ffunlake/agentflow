package ipc

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
)

// Client opens one connection per Call and is safe for concurrent calls when
// Path is not mutated. It never retries automatically: a lost response does not
// prove that a handler failed to commit. Retry using the same store request ID.
type Client struct {
	Path string
}

// Call returns the response and, on remote rejection, its typed *Error. Transport
// or invalid-response errors imply an unknown application outcome. The supplied
// context controls dialing, reading and writing; callers should set a deadline.
func (c Client) Call(ctx context.Context, request Request) (Response, error) {
	frame, err := json.Marshal(request)
	if err != nil {
		return Response{}, fmt.Errorf("encode IPC request: %w", err)
	}
	if len(frame) > MaxFrameSize {
		return Response{}, &Error{Code: FrameTooLarge, Message: "frame exceeds 1 MiB"}
	}
	if _, err := decodeRequest(frame); err != nil {
		return Response{}, &Error{Code: InvalidRequest, Message: err.Error()}
	}
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", c.Path)
	if err != nil {
		return Response{}, fmt.Errorf("dial IPC: %w", err)
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	if err := writeFrame(conn, frame); err != nil {
		return Response{}, callError(ctx, err)
	}
	frame, err = readFrame(bufio.NewReader(conn))
	if err != nil {
		return Response{}, callError(ctx, err)
	}
	response, err := decodeResponse(frame, request.RequestID)
	if err != nil {
		return Response{}, fmt.Errorf("invalid IPC response: %w", err)
	}
	if !response.OK {
		return response, response.Error
	}
	return response, nil
}

func callError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return fmt.Errorf("IPC transport (operation outcome unknown): %w", err)
}

func decodeResponse(frame []byte, id string) (Response, error) {
	var r Response
	fields, err := object(frame, "requestId", "ok", "result", "error")
	if err != nil {
		return r, err
	}
	if err := decodeRequired(fields, "requestId", &r.RequestID); err != nil {
		return r, err
	}
	if err := decodeRequired(fields, "ok", &r.OK); err != nil {
		return r, err
	}
	if r.RequestID != id {
		return r, errors.New("requestId mismatch")
	}
	result, hasResult := fields["result"]
	errorJSON, hasError := fields["error"]
	if r.OK {
		if !hasResult || hasError {
			return r, errors.New("success requires result and no error")
		}
		r.Result = result
		return r, nil
	}
	if hasResult || !hasError || bytes.Equal(errorJSON, []byte("null")) {
		return r, errors.New("failure requires error and no result")
	}
	errorFields, err := object(errorJSON, "code", "message")
	if err != nil {
		return r, err
	}
	r.Error = &Error{}
	if err := decodeRequired(errorFields, "code", &r.Error.Code); err != nil {
		return r, err
	}
	if err := decodeRequired(errorFields, "message", &r.Error.Message); err != nil {
		return r, err
	}
	if r.Error.Code == "" || r.Error.Message == "" {
		return r, errors.New("error code and message must be nonempty")
	}
	return r, nil
}
