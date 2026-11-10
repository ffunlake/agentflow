// Package coordinator wires the existing single-run store and runtime services
// to local IPC, with an explicit single-seat foreground startup path.
package coordinator

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"

	"agentflow/internal/ipc"
	"agentflow/internal/lock"
	"agentflow/internal/loopcore"
	"agentflow/internal/runtime"
	"agentflow/internal/store"
)

type Result struct {
	Status               string                   `json:"status"`
	CapabilityStatus     string                   `json:"capability_status"`
	RuntimeCommunication string                   `json:"runtime_communication"`
	Record               *store.MessageRecord     `json:"record,omitempty"`
	Messages             []store.MessageRecord    `json:"messages,omitempty"`
	Ack                  *store.AckResult         `json:"ack,omitempty"`
	Decision             *store.RecoveryDecision  `json:"decision,omitempty"`
	Workflow             *store.WorkflowResult    `json:"workflow,omitempty"`
	WorkflowCurrent      *loopcore.Cursor         `json:"workflow_current,omitempty"`
	AwaitingActor        bool                     `json:"awaiting_actor,omitempty"`
	Dispatch             *store.MessageRecord     `json:"dispatch,omitempty"`
	Executed             *bool                    `json:"executed,omitempty"`
	Diagnostics          []runtime.Diagnostic     `json:"diagnostics"`
	CMUXSend             *store.CMUXTransportWork `json:"cmux_send,omitempty"`
	CMUXInbox            *runtime.CMUXInboxResult `json:"cmux_inbox,omitempty"`
}

// Params contains no actor or request ID: both come only from the IPC envelope.
type Params struct {
	Recipient        string          `json:"recipient,omitempty"`
	TargetGeneration int64           `json:"recipient_generation,omitempty"`
	Body             string          `json:"body,omitempty"`
	Session          string          `json:"session,omitempty"`
	Mode             string          `json:"mode,omitempty"`
	MessageID        string          `json:"message_id,omitempty"`
	Status           string          `json:"status,omitempty"`
	After            int64           `json:"after,omitempty"`
	Pending          bool            `json:"pending,omitempty"`
	Workflow         *WorkflowParams `json:"workflow,omitempty"`
}

type StopResult struct {
	runtime.StopResult
	CapabilityStatus     string               `json:"capability_status"`
	RuntimeCommunication string               `json:"runtime_communication"`
	Diagnostics          []runtime.Diagnostic `json:"diagnostics"`
}

type Coordinator struct {
	db        *store.Store
	lock      *lock.Lock
	server    *ipc.Server
	closeOnce sync.Once
	closeErr  error
}

// Open requires an existing private directory, migrated store and matching run.
// Lock acquisition precedes all writable store access and listener creation.
func Open(ctx context.Context, dir, runID string) (*Coordinator, error) {
	return open(ctx, dir, runID, nil, nil, false)
}

// OpenWithAdapters explicitly enables workflow dispatch after accepted events.
// Open and the CLI retain persistence-only workflow behavior. Adapters must not
// synchronously reply from Submit; submission must first be durably confirmed.
func OpenWithAdapters(ctx context.Context, dir, runID string, reset runtime.ResetAdapter, submit runtime.SubmitAdapter) (*Coordinator, error) {
	return open(ctx, dir, runID, reset, submit, true)
}

func open(ctx context.Context, dir, runID string, reset runtime.ResetAdapter, submit runtime.SubmitAdapter, workflows bool) (*Coordinator, error) {
	c, dir, err := lockRun(dir, runID, []string{"agentflow", "coordinator"})
	if err != nil {
		return nil, err
	}
	fail := func(err error) (*Coordinator, error) { _ = c.Close(); return nil, err }
	c.db, err = store.OpenExisting(filepath.Join(dir, "state.db"), true)
	if err != nil {
		return fail(err)
	}
	run, err := c.db.GetRun(ctx)
	if err != nil {
		return fail(err)
	}
	if run.RunID != runID {
		return fail(&ipc.Error{Code: ipc.ActorMismatch, Message: "coordinator run differs from stored run"})
	}
	if err := c.listenWithAdapters(dir, reset, submit, workflows); err != nil {
		return fail(err)
	}
	return c, nil
}

// lockRun does not open a store or create a listener. Both Open and Up keep this
// same ownership capability until all IPC/store work has finished.
func lockRun(dir, runID string, command []string) (*Coordinator, string, error) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return nil, "", err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return nil, "", err
	}
	if !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return nil, "", errors.New("coordinator requires an existing private run directory (0700)")
	}
	socket := filepath.Join(dir, "ipc.sock")
	l, err := lock.Acquire(filepath.Join(dir, "coordinator.lock"), lock.Owner{RunID: runID, SocketPath: socket, Command: command})
	if err != nil {
		return nil, "", err
	}
	return &Coordinator{lock: l}, dir, nil
}

func (c *Coordinator) listen(dir string) error {
	return c.listenWithAdapters(dir, nil, nil, false)
}

func (c *Coordinator) listenWithAdapters(dir string, reset runtime.ResetAdapter, submit runtime.SubmitAdapter, workflows bool) error {
	service, err := runtime.NewMessageServiceWithAdapters(c.db, reset, submit)
	if err != nil {
		return err
	}
	var runner *runtime.WorkflowRunner
	if workflows {
		sessions, err := runtime.NewSessionCoordinator(c.db, reset, submit)
		if err != nil {
			return err
		}
		runner, err = runtime.NewWorkflowRunner(sessions)
		if err != nil {
			return err
		}
	}
	c.server, err = ipc.Listen(filepath.Join(dir, "ipc.sock"), handler(c.db, service, runner), ipc.ServerOptions{ValidateActor: service.ValidateActor})
	return err
}

// Serve owns shutdown: IPC drains handlers before the store closes and the lock
// releases. Stop requests only persist intent and do not shut down this process.
func (c *Coordinator) Serve(ctx context.Context) error {
	err := c.server.Serve(ctx)
	return errors.Join(err, c.Close())
}

// Close is for failed startup or after Serve has drained all handlers.
func (c *Coordinator) Close() error {
	c.closeOnce.Do(func() {
		if c.server != nil {
			c.closeErr = errors.Join(c.closeErr, c.server.Close())
		}
		if c.db != nil {
			c.closeErr = errors.Join(c.closeErr, c.db.Close())
		}
		if c.lock != nil {
			c.closeErr = errors.Join(c.closeErr, c.lock.Release())
		}
	})
	return c.closeErr
}

// Call only consults a matching lock and IPC. No database fallback or retries.
func Call(ctx context.Context, dir string, request ipc.Request) (ipc.Response, error) {
	owner, err := lock.Inspect(filepath.Join(dir, "coordinator.lock"))
	if err != nil {
		return ipc.Response{}, &ipc.Error{Code: "COORDINATOR_UNAVAILABLE", Message: "coordinator lock unavailable; start the foreground coordinator for this run"}
	}
	if owner.RunID != request.Actor.RunID {
		return ipc.Response{}, &ipc.Error{Code: ipc.ActorMismatch, Message: "coordinator lock belongs to another run"}
	}
	if !filepath.IsAbs(owner.SocketPath) {
		return ipc.Response{}, &ipc.Error{Code: "COORDINATOR_UNAVAILABLE", Message: "coordinator lock has no absolute socket path"}
	}
	return (ipc.Client{Path: owner.SocketPath}).Call(ctx, request)
}

func handler(db *store.Store, svc *runtime.MessageService, runner *runtime.WorkflowRunner) ipc.Handler {
	return func(ctx context.Context, req ipc.Request) (any, error) {
		p, err := decodeParams(req.Method, req.Params)
		if err != nil {
			return nil, err
		}
		r := Result{Status: "FAILED", CapabilityStatus: "PARTIAL", RuntimeCommunication: "UNVERIFIED", Diagnostics: []runtime.Diagnostic{}}
		switch req.Method {
		case "assign", "send":
			c := runtime.SendMessage{Actor: req.Actor, RequestID: req.RequestID, Recipient: p.Recipient, TargetGeneration: p.TargetGeneration, Body: p.Body, Session: p.Session, Mode: p.Mode}
			var record store.MessageRecord
			if req.Method == "assign" {
				record, err = svc.Assign(ctx, c)
			} else {
				record, err = svc.Send(ctx, c)
			}
			r.Record, r.Status = &record, record.Message.Status
		case "reply":
			if p.Workflow != nil {
				return workflowReply(ctx, db, runner, req, p)
			}
			record, e := svc.Reply(ctx, runtime.ReplyMessage{Actor: req.Actor, RequestID: req.RequestID, MessageID: p.MessageID, Body: p.Body, Status: p.Status})
			err, r.Record, r.Status = e, &record, record.Message.Status
		case "inbox":
			r.Messages, err = svc.Inbox(ctx, req.Actor, p.After, p.Pending)
			r.Status = "QUERIED"
			if err == nil {
				return struct {
					Result
					Messages []store.MessageRecord `json:"messages"`
				}{r, r.Messages}, nil
			}
		case "ack":
			ack, e := svc.Ack(ctx, req.Actor, req.RequestID, p.MessageID)
			err, r.Ack, r.Status = e, &ack, "ACKED"
		case "reconcile", "resume":
			decision, e := svc.Reconcile(ctx, req.Actor, p.MessageID)
			err, r.Decision, r.Status = e, &decision, "RECONCILED"
			r.Executed = new(bool)
		case "stop":
			if p != (Params{}) {
				return nil, &ipc.Error{Code: ipc.InvalidRequest, Message: "stop params must be empty"}
			}
			intent, e := db.RequestStopAsActor(ctx, store.MessageActor{RunID: req.Actor.RunID, AgentID: req.Actor.AgentID, Generation: req.Actor.Generation}, req.RequestID)
			if e == nil {
				return StopResult{StopResult: runtime.StopResult{StopIntent: intent}, CapabilityStatus: "PARTIAL", RuntimeCommunication: "UNVERIFIED", Diagnostics: []runtime.Diagnostic{{Code: "PROCESS_STOP_UNIMPLEMENTED", Path: "stop.executed", Message: "stop intent recorded; native process and workspace shutdown are unimplemented"}}}, nil
			}
			err = e
		case "workflow":
			return workflowControl(ctx, db, runner, req, p)
		default:
			return nil, &ipc.Error{Code: "UNIMPLEMENTED", Message: "coordinator method is not implemented"}
		}
		if err != nil {
			r = Result{Status: "FAILED", CapabilityStatus: "PARTIAL", RuntimeCommunication: "UNVERIFIED", Diagnostics: []runtime.Diagnostic{{Code: "MESSAGE_FAILED", Path: "message", Message: "coordinator operation failed"}}}
			var public *store.MessagingError
			if errors.As(err, &public) {
				r.Diagnostics[0] = runtime.Diagnostic{Code: public.Code, Path: public.Path, Message: public.Message}
			} else if errors.Is(err, store.ErrIdempotencyConflict) {
				r.Diagnostics[0] = runtime.Diagnostic{Code: ipc.IdempotencyConflict, Path: "request_id", Message: "request ID conflicts with a stored operation"}
			}
		}
		return r, nil
	}
}

// Reject duplicate, unknown and method-inapplicable fields rather than silently
// discarding inputs at a write boundary. Actor/request identity is envelope-only.
func decodeParams(method string, raw json.RawMessage) (Params, error) {
	allowed := map[string]map[string]bool{
		"send":      {"recipient": true, "recipient_generation": true, "body": true},
		"assign":    {"recipient": true, "recipient_generation": true, "body": true, "session": true, "mode": true},
		"reply":     {"message_id": true, "body": true, "status": true, "workflow": true},
		"workflow":  {"workflow": true},
		"inbox":     {"after": true, "pending": true},
		"ack":       {"message_id": true},
		"reconcile": {"message_id": true},
		"resume":    {"message_id": true},
		"stop":      {},
	}
	fields, ok := allowed[method]
	if !ok {
		return Params{}, &ipc.Error{Code: "UNIMPLEMENTED", Message: "coordinator method is not implemented"}
	}
	if err := validateParamsObject(raw, fields); err != nil {
		return Params{}, err
	}
	var p Params
	if json.Unmarshal(raw, &p) != nil {
		return Params{}, &ipc.Error{Code: ipc.InvalidRequest, Message: "invalid params field type"}
	}
	if err := validateWorkflowParams(raw, p.Workflow); err != nil {
		return Params{}, err
	}
	return p, nil
}

func validateParamsObject(raw json.RawMessage, fields map[string]bool) error {
	invalid := &ipc.Error{Code: ipc.InvalidRequest, Message: "params must be an object with unique fields valid for this method"}
	d := json.NewDecoder(bytes.NewReader(raw))
	if token, err := d.Token(); err != nil || token != json.Delim('{') {
		return invalid
	}
	seen := map[string]bool{}
	for d.More() {
		token, err := d.Token()
		key, ok := token.(string)
		if err != nil || !ok || seen[key] || !fields[key] {
			return invalid
		}
		seen[key] = true
		var value json.RawMessage
		if d.Decode(&value) != nil || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return invalid
		}
	}
	if _, err := d.Token(); err != nil {
		return invalid
	}
	if d.Decode(new(any)) != io.EOF {
		return invalid
	}
	return nil
}
