package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"agentflow/internal/config"
	runcontext "agentflow/internal/context"
	"agentflow/internal/ipc"
	"agentflow/internal/store"
)

type BootstrapResult struct {
	Status               string                `json:"status"`
	Path                 string                `json:"path"`
	Bootstrap            *runcontext.Bootstrap `json:"bootstrap"`
	Executed             bool                  `json:"executed"`
	RuntimeCommunication string                `json:"runtime_communication"`
}

// BootstrapService generates file continuation instructions while preserving all
// session/message state. Caller authentication belongs to the transport, as for
// ReportService; generation checks and publication share one store transaction.
type BootstrapService struct {
	db  *store.Store
	run *runcontext.Run
}

func NewBootstrapService(db *store.Store, run *runcontext.Run) (*BootstrapService, error) {
	if db == nil || run == nil {
		return nil, fmt.Errorf("%w: bootstrap requires store and context run", runcontext.ErrInvalid)
	}
	return &BootstrapService{db: db, run: run}, nil
}

// Rebuild is an alias for Bootstrap, not a native reset/resume operation.
func (s *BootstrapService) Rebuild(ctx context.Context, actor ipc.Actor) (*BootstrapResult, error) {
	return s.Bootstrap(ctx, actor)
}

func (s *BootstrapService) Bootstrap(ctx context.Context, actor ipc.Actor) (*BootstrapResult, error) {
	if actor.RunID != s.run.ID() {
		return nil, fmt.Errorf("%w: bootstrap caller run", runcontext.ErrIdentity)
	}
	var result *BootstrapResult
	err := s.db.WithBootstrapContext(ctx, store.ReportContext{Actor: messageActor(actor), ConfigDigest: s.run.ConfigDigest(),
		Lead: s.run.Lead(), Review: actor.AgentID == s.run.Lead()}, func(run store.Run, seat store.Seat, check func(store.ReportBinding) error) error {
		var frozen config.Resolved
		if err := json.Unmarshal([]byte(run.ConfigSnapshot), &frozen); err != nil {
			return fmt.Errorf("%w: frozen bootstrap config", runcontext.ErrCorrupt)
		}
		plan, err := Compile(&frozen, Options{})
		if err != nil {
			return err
		}
		// Reuses full frozen config digest validation; no source prompt is read.
		if _, err := NewSnapshot(plan, run.RunID); err != nil {
			return err
		}
		if frozen.Digest != run.ConfigDigest || filepath.Join(frozen.ContextDir, run.RunID) != s.run.Path() || frozen.Lead != s.run.Lead() {
			return fmt.Errorf("%w: frozen context binding", runcontext.ErrIdentity)
		}
		ids := make([]string, 0, len(frozen.Agents))
		for id := range frozen.Agents {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		if !reflect.DeepEqual(ids, s.run.AgentIDs()) {
			return fmt.Errorf("%w: context agent membership", runcontext.ErrIdentity)
		}
		role, ok := frozen.Agents[actor.AgentID]
		if !ok || seat.Tool != role.Tool || (run.WorkspaceID != "" && seat.WorkspaceID != "" && run.WorkspaceID != seat.WorkspaceID) {
			return fmt.Errorf("%w: runtime seat/config binding", runcontext.ErrIdentity)
		}
		a, err := s.run.Agent(actor.AgentID)
		if err != nil {
			return err
		}
		b, reports, err := a.PrepareBootstrap(actor.Generation)
		if err != nil {
			return err
		}
		sum := sha256.Sum256([]byte(role.Prompt))
		p := b.Protocol
		if p.Role != role.Role || p.Tool != role.Tool || p.Cwd != role.Cwd || p.Permissions != role.Permissions || p.Session != role.Session ||
			p.ConfigDigest != frozen.Digest || p.PromptSHA256 != hex.EncodeToString(sum[:]) {
			return fmt.Errorf("%w: role protocol differs from frozen config", runcontext.ErrIdentity)
		}
		for _, report := range reports {
			binding := store.ReportBinding{Author: store.MessageActor{RunID: report.RunID, AgentID: report.AgentID, Generation: report.Generation},
				MessageID: report.MessageID, RequestID: report.RequestID}
			// PrepareBootstrap has verified the exact Merge summary, report SHA,
			// evidence and reference list. Only these executor reports may use
			// persisted merge authority instead of the author's current generation.
			if actor.AgentID == s.run.Lead() && report.AgentID != actor.AgentID {
				for _, reviewed := range b.Reports {
					if reviewed.AgentID == report.AgentID && reviewed.Generation == report.Generation && reviewed.ReportID == report.ReportID {
						binding.ReportID, binding.ReportSHA256 = report.ReportID, reviewed.ReportSHA256
						binding.MergedRevision = b.Revision
					}
				}
			}
			if err := check(binding); err != nil {
				return err
			}
		}
		b.Seat = runcontext.BootstrapSeat{Status: seat.Status, NativeSessionID: seat.NativeSessionID, WorkspaceID: seat.WorkspaceID,
			SurfaceID: seat.SurfaceID, CurrentMessageID: seat.CurrentMessageID, StopRequested: run.StopRequested}
		if p.Session == "fresh" {
			b.Recommendation = "Create a fresh native session, load the frozen role and verified context files, then independently register READY before submitting work."
		} else {
			b.Recommendation = "Verify the recorded persistent session before reuse; if unavailable, create a new session from these files and independently register READY."
		}
		b.Next = strings.TrimSpace(b.Pending.Text)
		if b.Next == "" && len(b.Reports) > 0 {
			var next []string
			for _, report := range b.Reports {
				next = append(next, report.AgentID+": "+report.Next)
			}
			b.Next = strings.Join(next, "\n")
		}
		if b.Next == "" {
			b.Next = "Review the current progress and pending files with the lead before continuing."
		}
		if run.StopRequested {
			b.Recommendation = "Stop intent is recorded. Resolve it with the operator before continuing. " + b.Recommendation
		}
		path, err := a.PublishBootstrap(b)
		if err != nil {
			return err
		}
		result = &BootstrapResult{Status: "BOOTSTRAP_READY", Path: path, Bootstrap: b, RuntimeCommunication: "UNVERIFIED"}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}
