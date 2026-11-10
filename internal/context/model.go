// Package context stores compact, immutable run/agent context revisions.
// It does not read prompt sources or invoke native tools.
package context

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	Version     = 1
	maxFileSize = 128 * 1024
)

var (
	ErrInvalid  = errors.New("invalid context")
	ErrIdentity = errors.New("context identity mismatch")
	ErrUnsafe   = errors.New("unsafe context filesystem")
	ErrExists   = errors.New("context already exists")
	ErrCorrupt  = errors.New("corrupt or incomplete context")
	identifier  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)
	agentID     = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,47}$`)
	hexDigest   = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

var kinds = []string{"role", "background", "decisions", "progress", "pending"}

// Reference is descriptive provenance. Its target is never opened by this package.
// Fragment can name a frozen JSON field, e.g. config.agents.builder.prompt.
type Reference struct {
	Path     string `json:"path"`
	Purpose  string `json:"purpose"`
	Fragment string `json:"fragment,omitempty"`
}

type Section struct {
	Text       string      `json:"text"`
	References []Reference `json:"references"`
}

// MessageProtocol describes the existing S5 contract, never live message state.
// ACK confirms receipt; a completion report is an agent claim, not acceptance.
type MessageProtocol struct {
	Version         int      `json:"version"`
	ActorFields     []string `json:"actor_fields"`
	TerminalReports []string `json:"terminal_reports"`
	ACKMeaning      string   `json:"ack_meaning"`
	ReportMeaning   string   `json:"report_meaning"`
}

func S5Protocol() MessageProtocol {
	return MessageProtocol{Version: 1, ActorFields: []string{"run_id", "agent_id", "generation"},
		TerminalReports: []string{"DONE", "FAILED", "BLOCKED_CONFIRM"}, ACKMeaning: "receipt_only", ReportMeaning: "agent_claim"}
}

type Protocol struct {
	Role         string          `json:"role"`
	Tool         string          `json:"tool"`
	Cwd          string          `json:"cwd"`
	Permissions  string          `json:"permissions"`
	Session      string          `json:"session"`
	ConfigDigest string          `json:"config_digest"`
	PromptSHA256 string          `json:"prompt_sha256"`
	FrozenPrompt Reference       `json:"frozen_prompt"`
	Messages     MessageProtocol `json:"messages"`
}

// Snapshot is a small work context, not a transcript. A new revision never
// replaces old files. Its explicit identity must match the bound Agent handle.
type Snapshot struct {
	Version    int      `json:"version"`
	RunID      string   `json:"run_id"`
	AgentID    string   `json:"agent_id"`
	Revision   string   `json:"revision"`
	Protocol   Protocol `json:"protocol"`
	Background Section  `json:"background"`
	Decisions  Section  `json:"decisions"`
	Progress   Section  `json:"progress"`
	Pending    Section  `json:"pending"`
}

type Entry struct {
	Path      string    `json:"path"`
	Purpose   string    `json:"purpose"`
	SHA256    string    `json:"sha256"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Manifest struct {
	Version   int       `json:"version"`
	RunID     string    `json:"run_id"`
	AgentID   string    `json:"agent_id"`
	Revision  string    `json:"revision"`
	UpdatedAt time.Time `json:"updated_at"`
	Files     []Entry   `json:"files"`
}

type document struct {
	Version   int       `json:"version"`
	RunID     string    `json:"run_id"`
	AgentID   string    `json:"agent_id"`
	Revision  string    `json:"revision"`
	Kind      string    `json:"kind"`
	UpdatedAt time.Time `json:"updated_at"`
	Protocol  *Protocol `json:"protocol,omitempty"`
	Content   *Section  `json:"content,omitempty"`
}

func digest(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func absolute(path string) bool {
	return filepath.IsAbs(path) && filepath.Clean(path) == path && utf8.ValidString(path) && !strings.ContainsRune(path, '\x00')
}

func text(s string, limit int) bool {
	return len(s) <= limit && utf8.ValidString(s) && !strings.ContainsRune(s, '\x00')
}

func validReference(r Reference) bool {
	return absolute(r.Path) && len(r.Path) <= 4096 && strings.TrimSpace(r.Purpose) != "" &&
		text(r.Purpose, 512) && text(r.Fragment, 256)
}

func (s Snapshot) validate() error {
	if s.Version != Version || !identifier.MatchString(s.RunID) || !agentID.MatchString(s.AgentID) || !identifier.MatchString(s.Revision) {
		return fmt.Errorf("%w: version or identity", ErrInvalid)
	}
	p := s.Protocol
	if strings.TrimSpace(p.Role) == "" || !text(p.Role, 1024) || !absolute(p.Cwd) ||
		(p.Tool != "opencode" && p.Tool != "codex" && p.Tool != "claude") ||
		(p.Permissions != "full" && p.Permissions != "default") ||
		(p.Session != "fresh" && p.Session != "persistent") ||
		!hexDigest.MatchString(p.ConfigDigest) || !hexDigest.MatchString(p.PromptSHA256) || !validReference(p.FrozenPrompt) ||
		p.FrozenPrompt.Fragment != "config.agents."+s.AgentID+".prompt" || !reflect.DeepEqual(p.Messages, S5Protocol()) {
		return fmt.Errorf("%w: role protocol", ErrInvalid)
	}
	for _, section := range []Section{s.Background, s.Decisions, s.Progress, s.Pending} {
		if !text(section.Text, 8192) || len(section.References) > 16 {
			return fmt.Errorf("%w: section exceeds compact context limits", ErrInvalid)
		}
		for _, ref := range section.References {
			if !validReference(ref) {
				return fmt.Errorf("%w: file reference", ErrInvalid)
			}
		}
	}
	return nil
}
