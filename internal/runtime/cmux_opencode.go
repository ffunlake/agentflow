package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"agentflow/internal/config"
	"agentflow/internal/loopcore"
	"agentflow/internal/store"
)

// CMUXOpenCode drives launcher-owned OpenCode TUIs through cmux's guarded paste
// path and reads targeted rows from the native session database. It never creates a pane.
type CMUXOpenCode struct {
	transport *CMUXTransport
	runDir    string
	config    *config.Resolved
	seats     map[string]cmuxOpenCodeSeat
}

type cmuxOpenCodeSeat struct {
	binding store.SurfaceRegistration
	session string
	binary  string
}

type cmuxOpenCodeCapture struct {
	Seat      SeatSnapshot `json:"seat"`
	MessageID string       `json:"message_id"`
	UserID    string       `json:"user_id"`
	Assistant string       `json:"assistant_id"`
	FinalText string       `json:"final_text"`
	Completed bool         `json:"completed"`
}

type openCodeSession struct {
	ID        string `json:"id"`
	Directory string `json:"directory"`
}

type openCodeMessage struct {
	ID        string `json:"id"`
	ParentID  string `json:"parent_id"`
	Role      string `json:"role"`
	Finish    string `json:"finish"`
	Completed int64  `json:"completed"`
}

func NewCMUXOpenCode(ctx context.Context, transport *CMUXTransport, runDir string, resolved *config.Resolved) (*CMUXOpenCode, error) {
	if transport == nil || transport.db == nil || resolved == nil || resolved.Workflow == nil || !filepath.IsAbs(runDir) {
		return nil, errors.New("cmux OpenCode requires transport, workflow and absolute run directory")
	}
	a := &CMUXOpenCode{transport: transport, runDir: runDir, config: resolved, seats: map[string]cmuxOpenCodeSeat{}}
	run, err := transport.db.GetRun(ctx)
	if err != nil {
		return nil, err
	}
	bindings, err := transport.db.CMUXRegistrations(ctx, run.RunID)
	if err != nil {
		return nil, err
	}
	for id, role := range resolved.Agents {
		if role.Tool != "opencode" {
			continue
		}
		actor := store.MessageActor{RunID: run.RunID, AgentID: id, Generation: 1}
		found := false
		for _, b := range bindings {
			if b.AgentID == id {
				actor.Generation, found = b.Generation, true
				break
			}
		}
		if !found {
			return nil, errors.New("OpenCode seat has no cmux registration")
		}
		binding, err := transport.db.CMUXSurface(ctx, actor)
		if err != nil {
			return nil, err
		}
		var seat cmuxOpenCodeSeat
		var resolveErr error
		ready, cancel := context.WithTimeout(ctx, 20*time.Second)
		for {
			seat, resolveErr = a.resolve(ready, binding, role)
			if resolveErr == nil {
				break
			}
			select {
			case <-ready.Done():
				cancel()
				return nil, errors.New("OpenCode managed TUI did not publish a valid session within 20s")
			case <-time.After(250 * time.Millisecond):
			}
		}
		cancel()
		if err := transport.db.BindCMUXNativeSession(ctx, actor, binding, seat.session); err != nil {
			return nil, err
		}
		a.seats[id] = seat
	}
	if len(a.seats) == 0 {
		return nil, errors.New("workflow has no OpenCode worker seats")
	}
	return a, nil
}

func (a *CMUXOpenCode) resolve(ctx context.Context, binding store.SurfaceRegistration, role config.ResolvedAgent) (cmuxOpenCodeSeat, error) {
	evidence := a.transport.capture(ctx, []string{"surface", "resume", "show", "--surface", binding.SurfaceID, "--json"}, binding)
	if evidence.ExitCode != 0 || evidence.Truncated {
		return cmuxOpenCodeSeat{}, errors.New("OpenCode cmux resume binding unavailable")
	}
	var data struct {
		SurfaceRef   string `json:"surface_ref"`
		WorkspaceRef string `json:"workspace_ref"`
		Resume       struct {
			Kind       string `json:"kind"`
			Checkpoint string `json:"checkpoint_id"`
			Cwd        string `json:"cwd"`
			Launch     struct {
				Executable string `json:"executable_path"`
			} `json:"launch_command"`
		} `json:"resume_binding"`
	}
	if json.Unmarshal([]byte(evidence.Stdout), &data) != nil || data.SurfaceRef != binding.SurfaceRef || data.WorkspaceRef != binding.WorkspaceRef ||
		data.Resume.Kind != "opencode" || data.Resume.Cwd != role.Cwd || !identityText(data.Resume.Checkpoint) ||
		!filepath.IsAbs(data.Resume.Launch.Executable) || filepath.Base(data.Resume.Launch.Executable) != "opencode" {
		return cmuxOpenCodeSeat{}, errors.New("OpenCode cmux resume binding does not match frozen seat")
	}
	seat := cmuxOpenCodeSeat{binding: binding, session: data.Resume.Checkpoint, binary: data.Resume.Launch.Executable}
	if err := a.verifySession(ctx, seat, role.Cwd); err != nil {
		return cmuxOpenCodeSeat{}, errors.New("OpenCode session does not match frozen seat")
	}
	return seat, nil
}

func (a *CMUXOpenCode) query(ctx context.Context, seat cmuxOpenCodeSeat, cwd, query string, result any) error {
	body, _, truncated, err := captureNative(ctx, Command{Binary: seat.binary, Args: []string{"db", query, "--format", "json"}}, cwd, "")
	if err != nil || truncated {
		return errors.New("OpenCode session query failed")
	}
	d := json.NewDecoder(strings.NewReader(string(body)))
	if d.Decode(result) != nil || d.Decode(new(any)) != io.EOF {
		return errors.New("OpenCode session query response invalid")
	}
	return nil
}

func openCodeSQLLiteral(value string) (string, error) {
	if strings.IndexByte(value, 0) >= 0 {
		return "", errors.New("OpenCode query value contains NUL")
	}
	return "'" + strings.ReplaceAll(value, "'", "''") + "'", nil
}

func (a *CMUXOpenCode) verifySession(ctx context.Context, seat cmuxOpenCodeSeat, cwd string) error {
	id, err := openCodeSQLLiteral(seat.session)
	if err != nil {
		return err
	}
	var rows []openCodeSession
	if err := a.query(ctx, seat, cwd, "SELECT id, directory FROM session WHERE id = "+id, &rows); err != nil {
		return err
	}
	if len(rows) != 1 || rows[0].ID != seat.session || rows[0].Directory != cwd {
		return errors.New("OpenCode session identity mismatch")
	}
	return nil
}

func (a *CMUXOpenCode) idle(ctx context.Context, seat cmuxOpenCodeSeat, cwd string) (bool, error) {
	id, err := openCodeSQLLiteral(seat.session)
	if err != nil {
		return false, err
	}
	var rows []openCodeMessage
	query := "SELECT id, COALESCE(json_extract(data, '$.parentID'), '') AS parent_id, " +
		"COALESCE(json_extract(data, '$.role'), '') AS role, COALESCE(json_extract(data, '$.finish'), '') AS finish, " +
		"COALESCE(json_extract(data, '$.time.completed'), 0) AS completed FROM message WHERE session_id = " + id +
		" ORDER BY time_created DESC, id DESC LIMIT 1"
	if err := a.query(ctx, seat, cwd, query, &rows); err != nil {
		return false, err
	}
	if len(rows) != 1 {
		return false, errors.New("OpenCode session has no messages")
	}
	last := rows[0]
	return last.Role == "assistant" && last.Completed > 0 && last.Finish == "stop", nil
}

func (*CMUXOpenCode) SupportsFresh() bool { return false }

func (a *CMUXOpenCode) Inspect(ctx context.Context, s SeatSnapshot) (InputState, error) {
	seat, ok := a.seats[s.AgentID]
	role, configured := a.config.Agents[s.AgentID]
	if !ok || !configured || s.Tool != "opencode" || s.Status != Ready || s.Native.SessionID != seat.session ||
		s.Native.WorkspaceID != seat.binding.WorkspaceID || s.Native.SurfaceID != seat.binding.SurfaceID {
		return InputState{}, errors.New("OpenCode managed TUI binding mismatch")
	}
	current, err := a.resolve(ctx, seat.binding, role)
	if err != nil || current.session != seat.session || current.binary != seat.binary {
		return InputState{}, errors.New("OpenCode managed TUI binding changed")
	}
	idle, err := a.idle(ctx, seat, role.Cwd)
	if err != nil {
		return InputState{}, err
	}
	return InputState{Actor: seatActor(s), Native: s.Native, Verified: true, Busy: !idle}, nil
}

func (*CMUXOpenCode) Reset(context.Context, ResetRequest) (NativeBinding, error) {
	return NativeBinding{}, errors.New("managed OpenCode fresh reset is unsupported")
}

func (*CMUXOpenCode) WaitReady(context.Context, ResetRequest, NativeBinding) (Registration, error) {
	return Registration{}, errors.New("managed OpenCode fresh reset is unsupported")
}

func (a *CMUXOpenCode) resultPath(id string) string {
	sum := sha256.Sum256([]byte(id))
	return filepath.Join(a.runDir, "cmux-opencode-result-"+hex.EncodeToString(sum[:])+".json")
}

func (a *CMUXOpenCode) Submit(ctx context.Context, s SeatSnapshot, m store.Message) (Submission, error) {
	state, err := a.Inspect(ctx, s)
	if err != nil || state.Busy || m.RunID != s.RunID || m.Recipient != s.AgentID || m.Kind != "assign" {
		return Submission{}, submitFailure("CMUX_OPENCODE_NOT_IDLE", "UNKNOWN")
	}
	seat := a.seats[s.AgentID]
	path := a.resultPath(m.ID)
	if err := privateParent(path); err != nil {
		return Submission{}, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return Submission{}, submitFailure("CMUX_OPENCODE_EVIDENCE_EXISTS", "UNKNOWN")
	}
	defer f.Close()
	marker := "AgentFlow dispatch ID: " + m.ID
	prompt := marker + "\n" + workflowNativePrompt(m.Body)
	paste := a.transport.capture(ctx, []string{"send", "--paste", "--workspace", seat.binding.WorkspaceID, "--surface", seat.binding.SurfaceID, "--", prompt}, seat.binding)
	if paste.ExitCode != 0 || paste.Truncated {
		return Submission{}, submitFailure("CMUX_OPENCODE_PASTE_FAILED", "UNKNOWN")
	}
	enter := a.transport.capture(ctx, []string{"send-key", "--workspace", seat.binding.WorkspaceID, "--surface", seat.binding.SurfaceID, "enter"}, seat.binding)
	if enter.ExitCode != 0 || enter.Truncated {
		return Submission{}, submitFailure("CMUX_OPENCODE_SUBMIT_FAILED", "UNKNOWN")
	}
	result, err := a.waitResult(ctx, seat, a.config.Agents[s.AgentID].Cwd, marker)
	if err != nil {
		return Submission{}, err
	}
	capture := cmuxOpenCodeCapture{Seat: s, MessageID: m.ID, UserID: result.userID, Assistant: result.assistantID, FinalText: result.text, Completed: true}
	if json.NewEncoder(f).Encode(capture) != nil || f.Sync() != nil {
		return Submission{}, submitFailure("CMUX_OPENCODE_EVIDENCE_WRITE_FAILED", "UNKNOWN")
	}
	return Submission{Actor: seatActor(s), Native: s.Native, Accepted: true, Channel: "cmux-opencode-tui"}, nil
}

type openCodeResult struct{ userID, assistantID, text string }

func (a *CMUXOpenCode) waitResult(ctx context.Context, seat cmuxOpenCodeSeat, cwd, marker string) (openCodeResult, error) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		userID, err := a.userMessage(ctx, seat, cwd, marker)
		if err != nil {
			return openCodeResult{}, submitFailure("CMUX_OPENCODE_RESULT_UNKNOWN", "UNKNOWN")
		}
		if userID != "" {
			result, complete, err := a.assistantResult(ctx, seat, cwd, userID)
			if err != nil {
				return openCodeResult{}, submitFailure("CMUX_OPENCODE_RESULT_UNKNOWN", "UNKNOWN")
			}
			if complete {
				return result, nil
			}
		}
		select {
		case <-ctx.Done():
			return openCodeResult{}, submitFailure("CMUX_OPENCODE_RESULT_UNKNOWN", "UNKNOWN")
		case <-ticker.C:
		}
	}
}

func (a *CMUXOpenCode) userMessage(ctx context.Context, seat cmuxOpenCodeSeat, cwd, prompt string) (string, error) {
	session, err := openCodeSQLLiteral(seat.session)
	if err != nil {
		return "", err
	}
	needle, err := openCodeSQLLiteral(prompt)
	if err != nil {
		return "", err
	}
	query := "SELECT m.id FROM message m JOIN part p ON p.message_id = m.id WHERE m.session_id = " + session +
		" AND json_extract(m.data, '$.role') = 'user' AND json_extract(p.data, '$.type') = 'text' AND " +
		"instr(COALESCE(json_extract(p.data, '$.text'), ''), " + needle + ") > 0 ORDER BY m.time_created DESC LIMIT 1"
	var rows []struct {
		ID string `json:"id"`
	}
	if err := a.query(ctx, seat, cwd, query, &rows); err != nil {
		return "", err
	}
	if len(rows) == 0 {
		return "", nil
	}
	return rows[0].ID, nil
}

func (a *CMUXOpenCode) assistantResult(ctx context.Context, seat cmuxOpenCodeSeat, cwd, userID string) (openCodeResult, bool, error) {
	session, err := openCodeSQLLiteral(seat.session)
	if err != nil {
		return openCodeResult{}, false, err
	}
	parent, err := openCodeSQLLiteral(userID)
	if err != nil {
		return openCodeResult{}, false, err
	}
	query := "SELECT id FROM message WHERE session_id = " + session + " AND json_extract(data, '$.role') = 'assistant' AND " +
		"json_extract(data, '$.parentID') = " + parent + " AND json_extract(data, '$.finish') = 'stop' AND " +
		"COALESCE(json_extract(data, '$.time.completed'), 0) > 0 ORDER BY time_created DESC, id DESC LIMIT 1"
	var messages []struct {
		ID string `json:"id"`
	}
	if err := a.query(ctx, seat, cwd, query, &messages); err != nil {
		return openCodeResult{}, false, err
	}
	if len(messages) == 0 {
		return openCodeResult{}, false, nil
	}
	messageID, err := openCodeSQLLiteral(messages[0].ID)
	if err != nil {
		return openCodeResult{}, false, err
	}
	query = "SELECT json_extract(data, '$.text') AS text FROM part WHERE message_id = " + messageID +
		" AND json_extract(data, '$.type') = 'text' ORDER BY time_created, id"
	var parts []struct {
		Text string `json:"text"`
	}
	if err := a.query(ctx, seat, cwd, query, &parts); err != nil {
		return openCodeResult{}, false, err
	}
	var text strings.Builder
	for _, part := range parts {
		if text.Len()+len(part.Text) > 4*1024*1024 {
			return openCodeResult{}, false, errors.New("OpenCode result exceeds capture limit")
		}
		text.WriteString(part.Text)
	}
	if text.Len() == 0 {
		return openCodeResult{}, false, nil
	}
	return openCodeResult{userID: userID, assistantID: messages[0].ID, text: text.String()}, true, nil
}

func (a *CMUXOpenCode) WorkflowReply(ctx context.Context, s SeatSnapshot, m store.Message, c loopcore.Cursor) (ReplyMessage, loopcore.Event, error) {
	if ctx.Err() != nil || c.DispatchID != m.ID {
		return ReplyMessage{}, loopcore.Event{}, errors.New("OpenCode workflow binding mismatch")
	}
	f, err := os.Open(a.resultPath(m.ID))
	if err != nil {
		return ReplyMessage{}, loopcore.Event{}, err
	}
	defer f.Close()
	var capture cmuxOpenCodeCapture
	d := json.NewDecoder(io.LimitReader(f, 4*1024*1024))
	d.DisallowUnknownFields()
	if d.Decode(&capture) != nil || d.Decode(new(any)) != io.EOF || !capture.Completed || capture.MessageID != m.ID || seatActor(capture.Seat) != seatActor(s) || capture.Seat.Native != s.Native || capture.Seat.Tool != s.Tool || capture.UserID == "" || capture.Assistant == "" {
		return ReplyMessage{}, loopcore.Event{}, errors.New("OpenCode workflow capture invalid")
	}
	return workflowNativeReply("cmux-opencode:", capture.FinalText, s, m, c)
}

var _ ResetAdapter = (*CMUXOpenCode)(nil)
var _ SubmitAdapter = (*CMUXOpenCode)(nil)
var _ WorkflowResultAdapter = (*CMUXOpenCode)(nil)
