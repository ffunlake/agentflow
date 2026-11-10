package runtime

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"

	runcontext "agentflow/internal/context"
)

// CreateContext consumes only the existing frozen startup artifact. It never
// rereads prompt_file, starts tools, injects context, or changes S5 state.
// Partial failures leave explicit incomplete artifacts and are not overwritten.
func CreateContext(snapshotPath, runID string) (*runcontext.Run, error) {
	if !filepath.IsAbs(snapshotPath) || filepath.Clean(snapshotPath) != snapshotPath {
		return nil, fmt.Errorf("%w: frozen snapshot path must be canonical and absolute", runcontext.ErrInvalid)
	}
	canonical, err := filepath.EvalSymlinks(snapshotPath)
	if err != nil {
		return nil, err
	}
	if canonical != snapshotPath {
		return nil, fmt.Errorf("%w: frozen snapshot path contains a symlink", runcontext.ErrUnsafe)
	}
	s, err := ReadSnapshot(snapshotPath)
	if err != nil {
		return nil, err
	}
	if s.RunID != runID {
		return nil, fmt.Errorf("%w: frozen snapshot run", runcontext.ErrIdentity)
	}
	ids := make([]string, 0, len(s.Seats))
	for _, seat := range s.Seats {
		ids = append(ids, seat.AgentID)
	}
	r, err := runcontext.Create(s.Config.ContextDir, runID, s.Config.Lead, s.Config.Digest, ids)
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		role := s.Config.Agents[id]
		sum := sha256.Sum256([]byte(role.Prompt))
		a, err := r.Agent(id)
		if err != nil {
			return nil, err
		}
		_, err = a.Publish(runcontext.Snapshot{Version: runcontext.Version, RunID: runID, AgentID: id, Revision: "initial",
			Protocol: runcontext.Protocol{Role: role.Role, Tool: role.Tool, Cwd: role.Cwd, Permissions: role.Permissions, Session: role.Session,
				ConfigDigest: s.Config.Digest, PromptSHA256: hex.EncodeToString(sum[:]),
				Messages:     runcontext.S5Protocol(),
				FrozenPrompt: runcontext.Reference{Path: snapshotPath, Purpose: "frozen role instructions", Fragment: "config.agents." + id + ".prompt"}},
			Background: runcontext.Section{References: []runcontext.Reference{{Path: snapshotPath, Purpose: "frozen run configuration"}}}})
		if err != nil {
			return nil, err
		}
	}
	return r, nil
}
