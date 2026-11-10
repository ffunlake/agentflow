package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// ReportBinding identifies a report author and optional originating dispatch.
// RequestID refers to a dispatch request, never to a reply or an ACK request.
// MergedRevision is supplied only after bootstrap verifies a lead's exact merged
// report and SHA; the journal must independently prove its prior authorization.
type ReportBinding struct {
	Author         MessageActor
	MessageID      string
	RequestID      string
	ReportID       string
	ReportSHA256   string
	MergedRevision string
}

type ReportContext struct {
	Actor         MessageActor
	ConfigDigest  string
	Lead          string
	Review        bool
	MergeRevision string
}

// WithReportContext holds an immediate transaction while operating on immutable
// context artifacts. This serializes generation switches across connections and
// processes. The callback must not call Store methods or invoke native tools.
// A merge records report authorization in journal; S5 message, ACK, delivery and
// seat state are unchanged. A file publication can survive a commit failure:
// callers must read it explicitly, never overwrite it.
func (s *Store) WithReportContext(ctx context.Context, q ReportContext, operate func(check func(ReportBinding) error) error) error {
	if operate == nil {
		return messagingError("INVALID_REQUEST", "report", "report operation is required")
	}
	return s.WithBootstrapContext(ctx, q, func(_ Run, _ Seat, check func(ReportBinding) error) error {
		return operate(check)
	})
}

// WithBootstrapContext holds the same generation fence as report publication,
// exposing persisted identity to a file-only bootstrap operation. Native session
// availability and seat status are not prerequisites for continuing from files.
// The callback must not call Store methods or launch tools.
func (s *Store) WithBootstrapContext(ctx context.Context, q ReportContext, operate func(Run, Seat, func(ReportBinding) error) error) error {
	if operate == nil {
		return messagingError("INVALID_REQUEST", "bootstrap", "bootstrap operation is required")
	}
	if q.MergeRevision != "" && !q.Review {
		return messagingError("ACTOR_MISMATCH", "report.reviewer", "merge requires lead review")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := validateMessageActor(ctx, tx, q.Actor); err != nil {
		return err
	}
	run, err := scanRun(tx.QueryRowContext(ctx, selectRun))
	if err != nil {
		return err
	}
	var frozen struct {
		Lead string `json:"lead"`
	}
	if err := json.Unmarshal([]byte(run.ConfigSnapshot), &frozen); err != nil || frozen.Lead == "" || frozen.Lead != q.Lead || run.ConfigDigest != q.ConfigDigest {
		return messagingError("ACTOR_MISMATCH", "report.context", "context does not match frozen run configuration")
	}
	if q.Review && q.Actor.AgentID != frozen.Lead {
		return messagingError("ACTOR_MISMATCH", "report.reviewer", "only the frozen lead can review executor reports")
	}
	check := func(binding ReportBinding) error {
		if !q.Review && binding.Author != q.Actor {
			return messagingError("ACTOR_MISMATCH", "report.author", "caller must be the report author")
		}
		historical := false
		if q.Review && q.MergeRevision == "" && binding.Author.RunID == run.RunID && binding.Author.AgentID != frozen.Lead &&
			binding.MergedRevision != "" && binding.ReportID != "" && binding.ReportSHA256 != "" {
			body, err := json.Marshal(binding)
			if err != nil {
				return err
			}
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM journal WHERE run_id = ? AND agent_id = ? AND generation = ? AND event = 'REPORT_MERGED' AND details = ?)`,
				run.RunID, binding.Author.AgentID, binding.Author.Generation, string(body)).Scan(&historical); err != nil {
				return err
			}
		}
		if !historical {
			if err := validateMessageActor(ctx, tx, binding.Author); err != nil {
				return err
			}
		}
		if binding.MessageID == "" && binding.RequestID == "" {
			return nil
		}
		id := binding.MessageID
		if id == "" {
			err := tx.QueryRowContext(ctx, `SELECT id FROM messages WHERE run_id = ? AND sender = ? AND recipient = ? AND request_id = ? AND kind IN ('assign', 'send')`,
				binding.Author.RunID, frozen.Lead, binding.Author.AgentID, binding.RequestID).Scan(&id)
			if errors.Is(err, sql.ErrNoRows) {
				return messagingError("MESSAGE_NOT_FOUND", "report.request_id", "dispatch request is not stored for this author")
			}
			if err != nil {
				return err
			}
		}
		message, err := receivedMessage(ctx, tx, binding.Author, id)
		if err != nil {
			return err
		}
		if (message.Kind != "assign" && message.Kind != "send") || message.Sender != frozen.Lead ||
			(binding.RequestID != "" && binding.RequestID != message.RequestID) {
			return messagingError("ACTOR_MISMATCH", "report.message", "report must reference its originating lead dispatch")
		}
		if !historical {
			if _, err := currentMessageSeat(ctx, tx, message.RunID, message.Sender, message.SenderGeneration, "recipient"); err != nil {
				return err
			}
		}
		return nil
	}
	var seat Seat
	var created string
	err = tx.QueryRowContext(ctx, `SELECT run_id, agent_id, generation, is_current, tool,
		native_session_id, workspace_id, surface_id, status, COALESCE(current_message_id, ''), created_at
		FROM seats WHERE run_id = ? AND agent_id = ? AND generation = ? AND is_current = 1`,
		q.Actor.RunID, q.Actor.AgentID, q.Actor.Generation).Scan(&seat.RunID, &seat.AgentID, &seat.Generation, &seat.Current,
		&seat.Tool, &seat.NativeSessionID, &seat.WorkspaceID, &seat.SurfaceID, &seat.Status, &seat.CurrentMessageID, &created)
	if err != nil {
		return err
	}
	seat.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
	if err != nil {
		return err
	}
	var merged []ReportBinding
	if err := operate(run, seat, func(binding ReportBinding) error {
		if err := check(binding); err != nil {
			return err
		}
		if q.MergeRevision != "" && binding.ReportID != "" && binding.ReportSHA256 != "" {
			binding.MergedRevision = q.MergeRevision
			merged = append(merged, binding)
		}
		return nil
	}); err != nil {
		return err
	}
	// Only a successful merge under the generation fence authorizes this exact
	// report/revision for historical bootstrap. A failed commit grants no authority.
	for _, binding := range merged {
		body, err := json.Marshal(binding)
		if err != nil {
			return err
		}
		if _, err := appendJournal(ctx, tx, JournalEntry{RunID: run.RunID, AgentID: binding.Author.AgentID,
			Generation: binding.Author.Generation, Event: "REPORT_MERGED", Details: string(body)}); err != nil {
			return err
		}
	}
	return tx.Commit()
}
