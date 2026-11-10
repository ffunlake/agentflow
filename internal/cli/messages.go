package cli

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"agentflow/internal/coordinator"
	"agentflow/internal/ipc"
	"agentflow/internal/runtime"
	"agentflow/internal/store"
)

type messageOutput = coordinator.Result

func runMessages(command string, args []string, stdout, stderr io.Writer) int {
	transport := command == "cmux-send" || command == "cmux-inbox"
	ipcMethod := command
	if command == "cmux-send" {
		command = "send"
	}
	if command == "cmux-inbox" {
		command = "inbox"
	}
	recovery := command == "reconcile" || command == "resume"
	// Match the documented positional recipient/message form and also accept a
	// trailing positional argument after flags, as the standard flag parser does.
	position := ""
	if command != "inbox" && len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		position, args = args[0], args[1:]
	}
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	var usage bytes.Buffer
	flags.SetOutput(&usage)
	runID := flags.String("run", "", "existing run ID, required")
	runDir := flags.String("run-dir", "", "existing run directory (defaults to XDG state)")
	agent := flags.String("agent", "", "registered caller agent ID, required")
	generation := flags.Int64("generation", 0, "current caller generation, required")
	asJSON := flags.Bool("json", false, "output stable JSON")
	var requestID, bodyFile, reportStatus, session, mode string
	var targetGeneration, after int64
	var pending bool
	if command != "inbox" && !recovery {
		flags.StringVar(&requestID, "request-id", "", "stable sender request ID, required")
	}
	if command == "send" || command == "assign" || command == "reply" {
		flags.StringVar(&bodyFile, "body-file", "", "UTF-8 body file, required")
	}
	if command == "send" || command == "assign" {
		flags.Int64Var(&targetGeneration, "recipient-generation", 0, "expected recipient generation (default: current)")
	}
	if command == "reply" {
		flags.StringVar(&reportStatus, "status", "", "result status to preserve on the reply")
	}
	if command == "assign" {
		flags.StringVar(&session, "session", "", "fresh or persistent (default: frozen configuration)")
		flags.StringVar(&mode, "mode", "", "alias for --session")
	}
	if command == "inbox" {
		if !transport {
			flags.Int64Var(&after, "after", 0, "read after this message sequence")
			flags.BoolVar(&pending, "pending", false, "only messages not explicitly ACKED")
		}
	}
	flags.Usage = func() {
		fmt.Fprintf(&usage, "Usage: agentflow %s [recipient|message-id] --run <id> --agent <id> --generation <n> [options]\n\n", command)
		flags.PrintDefaults()
		usage.WriteString("\nUses existing state; assign selects the frozen session mode. Native adapters are UNIMPLEMENTED. Runtime communication: UNVERIFIED.\n")
	}
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return writeTeamOutput(stdout, stderr, usage.String())
		}
		fmt.Fprintf(stderr, "agentflow: error: %s arguments: %v\n", command, err)
		return 2
	}
	if command != "inbox" && position == "" && flags.NArg() == 1 {
		position = flags.Arg(0)
	} else if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "agentflow: error: unexpected positional arguments")
		return 2
	}
	if strings.TrimSpace(*runID) == "" || *runID == "." || *runID == ".." || strings.ContainsAny(*runID, "/\\\x00") ||
		strings.TrimSpace(*agent) == "" || *generation < 1 || targetGeneration < 0 || after < 0 ||
		(command != "inbox" && (strings.TrimSpace(position) == "" || (!recovery && strings.TrimSpace(requestID) == ""))) ||
		((command == "send" || command == "assign" || command == "reply") && bodyFile == "") ||
		(session != "" && session != "fresh" && session != "persistent") || (mode != "" && mode != "fresh" && mode != "persistent") || (mode != "" && session != "" && mode != session) {
		fmt.Fprintln(stderr, "agentflow: error: provide --run, --agent, positive --generation and required message/body/request arguments; see --help")
		return 2
	}
	result := messageOutput{Status: "FAILED", CapabilityStatus: "PARTIAL", RuntimeCommunication: "UNVERIFIED", Diagnostics: []runtime.Diagnostic{}}
	fail := func(err error, code, path string) int {
		message := "message operation failed"
		var public *store.MessagingError
		var remote *ipc.Error
		if errors.As(err, &public) {
			code, path, message = public.Code, public.Path, public.Message
		} else if errors.Is(err, store.ErrIdempotencyConflict) {
			code, path, message = ipc.IdempotencyConflict, "request_id", "request ID conflicts with a stored operation"
		} else if errors.As(err, &remote) {
			code, message = remote.Code, remote.Message
		}
		result.Diagnostics = append(result.Diagnostics, runtime.Diagnostic{Code: code, Path: path, Message: message})
		if *asJSON {
			writeTeamJSON(stdout, stderr, result)
		}
		fmt.Fprintf(stderr, "agentflow: error: %s: %s: %s\n", code, path, message)
		return 1
	}
	dir := *runDir
	if dir == "" {
		root := os.Getenv("XDG_STATE_HOME")
		if root == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return fail(err, "STORE_UNAVAILABLE", "store.path")
			}
			root = filepath.Join(home, ".local", "state")
		}
		dir = filepath.Join(root, "agentflow", "runs", *runID)
	}
	actor := ipc.Actor{RunID: *runID, AgentID: *agent, Generation: *generation}
	body := ""
	if bodyFile != "" {
		data, err := os.ReadFile(bodyFile)
		if err != nil {
			return fail(err, "BODY_UNAVAILABLE", "body_file")
		}
		if !utf8.Valid(data) {
			return fail(errors.New("invalid UTF-8"), "BODY_INVALID", "body_file")
		}
		body = string(data)
	}
	// Read-only calls still require a transport correlation ID; only mutating
	// message commands consume the caller's durable sender/request namespace.
	if requestID == "" {
		requestID = rand.Text()
	}
	p := coordinator.Params{}
	switch command {
	case "assign", "send":
		p.Recipient, p.TargetGeneration, p.Body, p.Session, p.Mode = position, targetGeneration, body, session, mode
	case "reply":
		p.MessageID, p.Body, p.Status = position, body, reportStatus
	case "ack", "reconcile", "resume":
		p.MessageID = position
	case "inbox":
		p.After, p.Pending = after, pending
	}
	params, _ := json.Marshal(p)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	response, err := coordinator.Call(ctx, dir, ipc.Request{V: ipc.Version, RequestID: requestID, Actor: actor, Method: ipcMethod, Params: params})
	if err != nil {
		return fail(err, "COORDINATOR_IPC_FAILED", "coordinator.socket")
	}
	if err := json.Unmarshal(response.Result, &result); err != nil {
		return fail(err, "COORDINATOR_RESPONSE_INVALID", "coordinator.result")
	}
	if transport {
		if result.Status == "" || (result.Status != "FAILED" && ((command == "send" && result.CMUXSend == nil) || (command == "inbox" && result.CMUXInbox == nil))) {
			return fail(errors.New("invalid result"), "COORDINATOR_RESPONSE_INVALID", "coordinator.result")
		}
		code := 0
		if *asJSON {
			code = writeTeamJSON(stdout, stderr, result)
		} else {
			text := fmt.Sprintf("Status: %s\nRuntime communication: UNVERIFIED\n", result.Status)
			if result.CMUXSend != nil {
				text += fmt.Sprintf("Message: %s\nSurface: %s\nLaunch: %s\n", result.CMUXSend.Record.Message.ID, result.CMUXSend.Binding.SurfaceID, result.CMUXSend.Binding.LaunchID)
			}
			if result.CMUXInbox != nil {
				for _, e := range result.CMUXInbox.Messages {
					text += fmt.Sprintf("Message %s from=%s target=%s state=%s\n%s\n", e.ID, e.Source, e.TargetSurface, e.State, e.Body)
				}
			}
			code = writeTeamOutput(stdout, stderr, text)
		}
		if result.Status == "FAILED" || result.Status == "UNVERIFIED" {
			for _, d := range result.Diagnostics {
				fmt.Fprintf(stderr, "agentflow: error: %s: %s\n", d.Code, d.Message)
			}
			return 1
		}
		return code
	}
	if !validMessageResult(command, result) {
		result = messageOutput{Status: "FAILED", CapabilityStatus: "PARTIAL", RuntimeCommunication: "UNVERIFIED", Diagnostics: []runtime.Diagnostic{}}
		return fail(errors.New("invalid result"), "COORDINATOR_RESPONSE_INVALID", "coordinator.result")
	}
	if result.Status == "FAILED" {
		if *asJSON {
			if code := writeTeamJSON(stdout, stderr, result); code != 0 {
				return code
			}
		}
		for _, d := range result.Diagnostics {
			fmt.Fprintf(stderr, "agentflow: error: %s: %s: %s\n", d.Code, d.Path, d.Message)
		}
		return 1
	}
	var text string
	switch command {
	case "reconcile", "resume":
		decision := result.Decision
		text = fmt.Sprintf("Status: RECONCILED (decision persisted only)\nMessage: %s\nDecision: %s\nReason: %s\nNext action: %s\nExecuted: false\nRuntime communication: UNVERIFIED\n", decision.MessageID, decision.State, decision.Reason, decision.NextAction)
	case "inbox":
		messages := result.Messages
		// Use a concrete envelope to preserve [] for an empty inbox.
		if *asJSON {
			return writeTeamJSON(stdout, stderr, struct {
				Status               string                `json:"status"`
				CapabilityStatus     string                `json:"capability_status"`
				RuntimeCommunication string                `json:"runtime_communication"`
				Messages             []store.MessageRecord `json:"messages"`
				Diagnostics          []runtime.Diagnostic  `json:"diagnostics"`
			}{"QUERIED", "PARTIAL", "UNVERIFIED", messages, result.Diagnostics})
		}
		text = fmt.Sprintf("Status: QUERIED\nMessages: %d\nRuntime communication: UNVERIFIED\n", len(messages))
		for _, record := range messages {
			text += fmt.Sprintf("Message %s: %s from=%s kind=%s generation=%d\n%s\n", record.Message.ID, record.Message.Status, record.Message.Sender, record.Message.Kind, record.Delivery.TargetGeneration, record.Message.Body)
		}
	case "ack":
		ack := result.Ack
		text = fmt.Sprintf("Status: ACKED (receipt only)\nMessage: %s\nJournal sequence: %d\nRuntime communication: UNVERIFIED\n", ack.MessageID, ack.JournalSeq)
	default:
		record := result.Record
		text = fmt.Sprintf("Status: %s\nMessage: %s\nKind: %s\nRecipient: %s\nDelivery: %s\nAttempts: %d\nRuntime communication: UNVERIFIED\n", record.Message.Status, record.Message.ID, record.Message.Kind, record.Message.Recipient, record.Delivery.Phase, record.Delivery.Attempt)
		if record.Session != "" {
			text += fmt.Sprintf("Session: %s\nReset: %s\nCoordination: %s\n", record.Session, record.Delivery.ResetPhase, record.Coordination)
		}
	}
	if *asJSON {
		return writeTeamJSON(stdout, stderr, result)
	}
	return writeTeamOutput(stdout, stderr, text)
}

func validMessageResult(command string, r messageOutput) bool {
	if r.Status == "FAILED" {
		return len(r.Diagnostics) > 0
	}
	switch command {
	case "inbox":
		return r.Status == "QUERIED" && r.Messages != nil
	case "ack":
		return r.Status == "ACKED" && r.Ack != nil
	case "reconcile", "resume":
		return r.Status == "RECONCILED" && r.Decision != nil
	default:
		return r.Record != nil && r.Record.Message.ID != "" && r.Status == r.Record.Message.Status
	}
}
