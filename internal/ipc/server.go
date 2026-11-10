package ipc

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
)

// Handler runs after envelope and actor validation. It may be called concurrently
// for different connections; requests on one connection are handled in order.
// Return *Error{Code: MethodNotFound, ...} for an unsupported method.
type Handler func(context.Context, Request) (any, error)

type ServerOptions struct {
	// ValidateActor is mandatory and called for every request, including retries.
	// Return ACTOR_MISMATCH or GENERATION_MISMATCH as a typed *Error. Never trust
	// the request's claimed identity without consulting trusted runtime state.
	ValidateActor func(context.Context, Actor) error
}

type Server struct {
	listener *net.UnixListener
	handler  Handler
	options  ServerOptions
	mu       sync.Mutex
	closed   bool
	serving  bool
	conns    map[net.Conn]context.CancelFunc
}

// Listen binds a caller-supplied path in an existing private parent directory
// and chmods the socket to 0600 before accepting connections. It never removes
// an existing socket or file to make the bind succeed. Call Serve exactly once.
func Listen(path string, handler Handler, options ServerOptions) (*Server, error) {
	if path == "" || handler == nil || options.ValidateActor == nil {
		return nil, errors.New("IPC requires a socket path, handler and actor validator")
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return nil, fmt.Errorf("listen IPC: %w", err)
	}
	if err := os.Chmod(path, 0600); err != nil {
		_ = listener.Close()
		return nil, fmt.Errorf("chmod IPC socket: %w", err)
	}
	return &Server{listener: listener, handler: handler, options: options, conns: make(map[net.Conn]context.CancelFunc)}, nil
}

// Serve accepts concurrent clients until Close or context cancellation, then
// waits for connection handlers. Handlers must honor context cancellation.
func (s *Server) Serve(ctx context.Context) error {
	s.mu.Lock()
	if s.serving {
		s.mu.Unlock()
		return errors.New("IPC Serve may only be called once")
	}
	s.serving = true
	s.mu.Unlock()
	stop := context.AfterFunc(ctx, func() { _ = s.Close() })
	defer stop()
	defer s.Close()
	var clients sync.WaitGroup
	defer clients.Wait()
	for {
		conn, err := s.listener.AcceptUnix()
		if err != nil {
			s.mu.Lock()
			closed := s.closed
			s.mu.Unlock()
			if closed {
				return nil
			}
			// Close clients before waiting if accept fails unexpectedly.
			_ = s.Close()
			return fmt.Errorf("accept IPC: %w", err)
		}
		clientCtx, cancel := context.WithCancel(ctx)
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			cancel()
			_ = conn.Close()
			return nil
		}
		s.conns[conn] = cancel
		s.mu.Unlock()
		clients.Add(1)
		go func() {
			defer clients.Done()
			defer func() {
				cancel()
				_ = conn.Close()
				s.mu.Lock()
				delete(s.conns, conn)
				s.mu.Unlock()
			}()
			s.serveConn(clientCtx, conn)
		}()
	}
}

// Close interrupts idle readers/writers and cancels handlers. Serve waits for
// handlers to return. Repeated calls are safe.
func (s *Server) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	err := s.listener.Close()
	for conn, cancel := range s.conns {
		cancel()
		_ = conn.Close()
	}
	return err
}

func (s *Server) serveConn(ctx context.Context, conn net.Conn) {
	reader := bufio.NewReader(conn)
	for {
		frame, err := readFrame(reader)
		if errors.Is(err, errFrameTooLarge) {
			_ = sendResponse(conn, failure("", &Error{Code: FrameTooLarge, Message: "frame exceeds 1 MiB"}))
			return
		}
		if errors.Is(err, io.ErrUnexpectedEOF) {
			_ = sendResponse(conn, failure("", &Error{Code: InvalidRequest, Message: "frame requires a terminating newline"}))
			return
		}
		if err != nil {
			return // EOF, disconnect or shutdown: no speculative retry
		}
		request, err := decodeRequest(frame)
		var response Response
		if err != nil {
			response = failure(request.RequestID, &Error{Code: InvalidRequest, Message: err.Error()})
		} else {
			response = s.dispatch(ctx, request)
		}
		if err := sendResponse(conn, response); err != nil {
			return
		}
	}
}

func (s *Server) dispatch(ctx context.Context, request Request) (response Response) {
	// Keep a faulty handler/validator from taking down other clients. Do not expose
	// panic values, which can contain application internals.
	defer func() {
		if recover() != nil {
			response = failure(request.RequestID, wireError(errors.New("panic")))
		}
	}()
	if err := s.options.ValidateActor(ctx, request.Actor); err != nil {
		return failure(request.RequestID, wireError(err))
	}
	result, err := s.handler(ctx, request)
	if err != nil {
		return failure(request.RequestID, wireError(err))
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return failure(request.RequestID, wireError(err))
	}
	return Response{RequestID: request.RequestID, OK: true, Result: encoded}
}

func sendResponse(conn net.Conn, response Response) error {
	frame, err := json.Marshal(response)
	if err != nil {
		response = failure(response.RequestID, wireError(err))
		frame, err = json.Marshal(response)
	}
	if err != nil {
		return err
	}
	if len(frame) > MaxFrameSize {
		frame, err = json.Marshal(failure(response.RequestID, &Error{Code: FrameTooLarge, Message: "response exceeds 1 MiB; use a file reference"}))
		if err != nil {
			return err
		}
	}
	return writeFrame(conn, frame)
}
