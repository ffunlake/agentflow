package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"agentflow/internal/ipc"
	"agentflow/internal/store"
)

// CMUXTransport only addresses durable launcher-owned UUIDs. It never sends
// terminal input and never establishes provider session readiness.
type CMUXTransport struct {
	db     *store.Store
	binary string
}

type cmuxOutput struct {
	buffer    bytes.Buffer
	truncated bool
}

func (b *cmuxOutput) String() string { return b.buffer.String() }

func (b *cmuxOutput) Write(p []byte) (int, error) {
	n := len(p)
	remaining := 64*1024 - b.buffer.Len()
	if len(p) > remaining {
		p, b.truncated = p[:remaining], true
	}
	_, _ = b.buffer.Write(p)
	return n, nil
}

func NewCMUXTransport(db *store.Store, binary string) (*CMUXTransport, error) {
	if db == nil || !filepath.IsAbs(binary) || strings.ContainsRune(binary, '\x00') {
		return nil, errors.New("cmux transport requires a store and absolute executable")
	}
	return &CMUXTransport{db: db, binary: binary}, nil
}

func cmuxSourceEnv(source store.SurfaceRegistration) []string {
	env := os.Environ()
	for key, value := range map[string]string{
		"CMUX_WORKSPACE_ID": source.WorkspaceID,
		"CMUX_TAB_ID":       source.WorkspaceID,
		"CMUX_SURFACE_ID":   source.SurfaceID,
		"CMUX_PANEL_ID":     source.SurfaceID,
	} {
		prefix := key + "="
		found := false
		for i := range env {
			if strings.HasPrefix(env[i], prefix) {
				env[i], found = prefix+value, true
				break
			}
		}
		if !found {
			env = append(env, prefix+value)
		}
	}
	return env
}

func (t *CMUXTransport) capture(ctx context.Context, args []string, source store.SurfaceRegistration) store.CMUXEvidence {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, t.binary, args...)
	cmd.Env = cmuxSourceEnv(source)
	cmd.WaitDelay = 250 * time.Millisecond
	var out, diagnostic cmuxOutput
	cmd.Stdout, cmd.Stderr = &out, &diagnostic
	err := cmd.Run()
	e := store.CMUXEvidence{Stdout: out.String(), Stderr: diagnostic.String(), ExitCode: -1, Truncated: out.truncated || diagnostic.truncated}
	if cmd.ProcessState != nil {
		e.ExitCode = cmd.ProcessState.ExitCode()
	}
	if err != nil && e.ExitCode == 0 {
		e.ExitCode = -1
	}
	return e
}

type cmuxMessage struct {
	ID                 string `json:"id"`
	SenderName         string `json:"sender_name"`
	SenderSurface      string `json:"sender_surface_id"`
	SenderWorkspace    string `json:"sender_workspace_id"`
	RecipientSurface   string `json:"recipient_surface_id"`
	RecipientWorkspace string `json:"recipient_workspace_id"`
	Body               string `json:"body"`
	State              string `json:"state"`
	RecipientHasAgent  *bool  `json:"recipient_has_agent"`
}

func parseCMUXMessage(raw []byte, e store.CMUXEvidence) (store.CMUXEvidence, error) {
	var m cmuxMessage
	var fields map[string]json.RawMessage
	if !utf8.Valid(raw) || uniqueJSON(raw) != nil || json.Unmarshal(raw, &fields) != nil || fields == nil {
		return e, errors.New("invalid cmux message JSON")
	}
	// Read exact wire keys. Unknown metadata stays in Raw; differently cased
	// aliases cannot overwrite canonical identity fields through struct decoding.
	for key, dst := range map[string]*string{"id": &m.ID, "sender_name": &m.SenderName, "sender_surface_id": &m.SenderSurface, "sender_workspace_id": &m.SenderWorkspace, "recipient_surface_id": &m.RecipientSurface, "recipient_workspace_id": &m.RecipientWorkspace, "body": &m.Body, "state": &m.State} {
		value, exists := fields[key]
		optional := key == "sender_surface_id" || key == "sender_workspace_id"
		if optional && !exists {
			continue
		}
		if !exists || json.Unmarshal(value, dst) != nil {
			return e, errors.New("missing or invalid cmux message field")
		}
	}
	if value, exists := fields["recipient_has_agent"]; exists {
		var hasAgent bool
		if json.Unmarshal(value, &hasAgent) != nil {
			return e, errors.New("invalid cmux recipient agent status")
		}
		m.RecipientHasAgent = &hasAgent
	}
	if !identityText(m.ID) || !cmuxUUID.MatchString(m.RecipientSurface) || !cmuxUUID.MatchString(m.RecipientWorkspace) || m.SenderName == "" || !validCMUXBody(m.Body) || (m.State != "queued" && m.State != "delivered" && m.State != "read" && m.State != "failed") {
		return e, errors.New("invalid cmux message evidence")
	}
	e.ID, e.Source, e.SourceSurface, e.SourceWorkspace = m.ID, m.SenderName, m.SenderSurface, m.SenderWorkspace
	e.TargetSurface, e.TargetWorkspace, e.Body, e.State = m.RecipientSurface, m.RecipientWorkspace, m.Body, m.State
	e.RecipientHasAgent = m.RecipientHasAgent
	e.Raw = append(json.RawMessage(nil), raw...)
	return e, nil
}

func validCMUXBody(body string) bool {
	if !utf8.ValidString(body) || strings.TrimSpace(body) == "" || len(body) > 32*1024 || body == "-" {
		return false
	}
	for _, r := range body {
		if (r < 0x20 && r != '\n' && r != '\t') || (r >= 0x7f && r <= 0x9f) {
			return false
		}
	}
	return true
}

func (t *CMUXTransport) Send(ctx context.Context, c SendMessage) (store.CMUXTransportWork, error) {
	if err := ctx.Err(); err != nil {
		return store.CMUXTransportWork{}, err
	}
	if !validCMUXBody(c.Body) {
		return store.CMUXTransportWork{}, &ipc.Error{Code: ipc.InvalidRequest, Message: "cmux requires nonempty UTF-8 text within 32 KiB; literal '-' and control bytes are unsupported"}
	}
	w, claimed, err := t.db.BeginCMUXSend(ctx, store.MessageCommand{Actor: messageActor(c.Actor), RequestID: c.RequestID, Recipient: c.Recipient, TargetGeneration: c.TargetGeneration, Body: c.Body})
	if err != nil || !claimed {
		return w, err
	}
	if !cmuxUUID.MatchString(w.Binding.SurfaceID) || !cmuxUUID.MatchString(w.Binding.WorkspaceID) {
		return w, errors.New("invalid stored cmux UUID")
	}
	source, sourceErr := t.db.CMUXSurface(ctx, messageActor(c.Actor))
	if sourceErr != nil {
		e := store.CMUXEvidence{ExitCode: -1, Stderr: sourceErr.Error()}
		saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		return t.db.FinishCMUXSend(saveCtx, messageActor(c.Actor), w, e)
	}
	e := t.capture(ctx, []string{"agent", "message", w.Binding.SurfaceID, "--from", "agentflow", "--json", "--", c.Body}, source)
	if e.ExitCode == 0 && !e.Truncated {
		if parsed, err := parseCMUXMessage([]byte(e.Stdout), e); err == nil {
			e = parsed
		}
	}
	// Persist process evidence even when the caller's deadline expired. A stop or
	// generation change still rejects confirmation and leaves the durable intent.
	saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	return t.db.FinishCMUXSend(saveCtx, messageActor(c.Actor), w, e)
}

type CMUXInboxResult struct {
	Binding  store.SurfaceRegistration `json:"binding"`
	Messages []store.CMUXEvidence      `json:"messages"`
	Evidence store.CMUXEvidence        `json:"evidence"`
}

func (t *CMUXTransport) Inbox(ctx context.Context, a ipc.Actor) (CMUXInboxResult, error) {
	r := CMUXInboxResult{Messages: []store.CMUXEvidence{}}
	b, err := t.db.CMUXSurface(ctx, messageActor(a))
	if err != nil {
		return r, err
	}
	r.Binding = b
	if !cmuxUUID.MatchString(b.SurfaceID) {
		return r, errors.New("invalid stored surface UUID")
	}
	r.Evidence = t.capture(ctx, []string{"agent", "inbox", "--surface", b.SurfaceID, "--limit", "100", "--json"}, b)
	if r.Evidence.ExitCode != 0 || r.Evidence.Truncated {
		return r, fmt.Errorf("cmux inbox failed or truncated (exit=%d truncated=%t)", r.Evidence.ExitCode, r.Evidence.Truncated)
	}
	var fields map[string]json.RawMessage
	var messages []json.RawMessage
	if !utf8.ValidString(r.Evidence.Stdout) || uniqueJSON([]byte(r.Evidence.Stdout)) != nil || json.Unmarshal([]byte(r.Evidence.Stdout), &fields) != nil || json.Unmarshal(fields["messages"], &messages) != nil || messages == nil {
		return r, errors.New("invalid cmux inbox evidence")
	}
	for _, raw := range messages {
		e, err := parseCMUXMessage(raw, store.CMUXEvidence{})
		if err != nil {
			return r, err
		}
		r.Messages = append(r.Messages, e)
	}
	err = t.db.AcceptCMUXInbox(ctx, messageActor(a), b, r.Messages)
	return r, err
}
