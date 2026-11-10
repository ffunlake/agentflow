package runtime

import (
	"context"
	"fmt"

	runcontext "agentflow/internal/context"
	"agentflow/internal/ipc"
	"agentflow/internal/store"
)

// ReportService combines current S5 authority with immutable S6 files. Like the
// message service it expects its caller identity from an authenticated transport;
// it does not itself authenticate OS processes or run native tools.
type ReportService struct {
	db  *store.Store
	run *runcontext.Run
}

func NewReportService(db *store.Store, run *runcontext.Run) (*ReportService, error) {
	if db == nil || run == nil {
		return nil, fmt.Errorf("%w: report service requires store and context run", runcontext.ErrInvalid)
	}
	return &ReportService{db: db, run: run}, nil
}

func (s *ReportService) gate(ctx context.Context, actor ipc.Actor, review bool, operate func(func(store.ReportBinding) error) error) error {
	if actor.RunID != s.run.ID() {
		return fmt.Errorf("%w: report caller run", runcontext.ErrIdentity)
	}
	return s.db.WithReportContext(ctx, store.ReportContext{Actor: messageActor(actor), ConfigDigest: s.run.ConfigDigest(),
		Lead: s.run.Lead(), Review: review}, operate)
}

func (s *ReportService) PublishEvidence(ctx context.Context, actor ipc.Actor, name, purpose string, body []byte) (*runcontext.Evidence, error) {
	var result *runcontext.Evidence
	err := s.gate(ctx, actor, false, func(_ func(store.ReportBinding) error) error {
		a, err := s.run.Agent(actor.AgentID)
		if err != nil {
			return err
		}
		result, err = a.PublishEvidence(actor.Generation, name, purpose, body)
		return err
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (s *ReportService) Publish(ctx context.Context, actor ipc.Actor, report runcontext.Report) (*runcontext.ReportManifest, error) {
	if report.RunID != actor.RunID || report.AgentID != actor.AgentID || report.Generation != actor.Generation {
		return nil, fmt.Errorf("%w: report author differs from caller", runcontext.ErrIdentity)
	}
	var result *runcontext.ReportManifest
	err := s.gate(ctx, actor, false, func(check func(store.ReportBinding) error) error {
		if err := check(store.ReportBinding{Author: messageActor(actor), MessageID: report.MessageID, RequestID: report.RequestID}); err != nil {
			return err
		}
		a, err := s.run.Agent(actor.AgentID)
		if err != nil {
			return err
		}
		result, err = a.PublishReport(report)
		return err
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (s *ReportService) Read(ctx context.Context, actor ipc.Actor, ref runcontext.ReportRef) (*runcontext.Report, *runcontext.ReportManifest, error) {
	if actor.AgentID != s.run.Lead() && (actor.AgentID != ref.AgentID || actor.Generation != ref.Generation) {
		return nil, nil, fmt.Errorf("%w: only author or lead may read report", runcontext.ErrIdentity)
	}
	var report *runcontext.Report
	var manifest *runcontext.ReportManifest
	err := s.gate(ctx, actor, actor.AgentID == s.run.Lead(), func(check func(store.ReportBinding) error) error {
		binding := store.ReportBinding{Author: store.MessageActor{RunID: actor.RunID, AgentID: ref.AgentID, Generation: ref.Generation}}
		if err := check(binding); err != nil {
			return err
		}
		a, err := s.run.Agent(ref.AgentID)
		if err != nil {
			return err
		}
		report, manifest, err = a.ReadReport(ref.Generation, ref.ReportID)
		if err != nil {
			return err
		}
		binding.MessageID, binding.RequestID = report.MessageID, report.RequestID
		return check(binding)
	})
	if err != nil {
		return nil, nil, err
	}
	return report, manifest, nil
}

// reviewBindings checks persisted dispatch references in the same transaction
// that reads and merges reports. It never trusts caller-provided message state.
func (s *ReportService) reviewBindings(actor ipc.Actor, refs []runcontext.ReportRef, check func(store.ReportBinding) error) error {
	if len(refs) == 0 || len(refs) > 4 {
		return fmt.Errorf("%w: review requires 1..4 reports", runcontext.ErrInvalid)
	}
	for _, ref := range refs {
		binding := store.ReportBinding{Author: store.MessageActor{RunID: actor.RunID, AgentID: ref.AgentID, Generation: ref.Generation}}
		if err := check(binding); err != nil {
			return err
		}
		author, err := s.run.Agent(ref.AgentID)
		if err != nil {
			return err
		}
		report, manifest, err := author.ReadReport(ref.Generation, ref.ReportID)
		if err != nil {
			return err
		}
		binding.MessageID, binding.RequestID = report.MessageID, report.RequestID
		binding.ReportID, binding.ReportSHA256 = report.ReportID, manifest.File.SHA256
		if err := check(binding); err != nil {
			return err
		}
	}
	return nil
}

func (s *ReportService) Review(ctx context.Context, actor ipc.Actor, refs []runcontext.ReportRef) ([]runcontext.ReviewedReport, error) {
	var result []runcontext.ReviewedReport
	err := s.gate(ctx, actor, true, func(check func(store.ReportBinding) error) error {
		if err := s.reviewBindings(actor, refs, check); err != nil {
			return err
		}
		a, err := s.run.Agent(actor.AgentID)
		if err != nil {
			return err
		}
		result, err = a.Review(refs)
		return err
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (s *ReportService) Merge(ctx context.Context, actor ipc.Actor, baseRevision, revision string, refs []runcontext.ReportRef) (*runcontext.Manifest, error) {
	if actor.RunID != s.run.ID() {
		return nil, fmt.Errorf("%w: report caller run", runcontext.ErrIdentity)
	}
	var result *runcontext.Manifest
	err := s.db.WithReportContext(ctx, store.ReportContext{Actor: messageActor(actor), ConfigDigest: s.run.ConfigDigest(),
		Lead: s.run.Lead(), Review: true, MergeRevision: revision}, func(check func(store.ReportBinding) error) error {
		if err := s.reviewBindings(actor, refs, check); err != nil {
			return err
		}
		a, err := s.run.Agent(actor.AgentID)
		if err != nil {
			return err
		}
		result, err = a.Merge(baseRevision, revision, refs)
		return err
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}
