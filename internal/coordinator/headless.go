package coordinator

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"

	"agentflow/internal/runtime"
	"agentflow/internal/store"
)

// OpenHeadlessCodex installs native adapters only for an explicitly initialized
// headless run. It never performs a startup handshake or drains on open.
func OpenHeadlessCodex(ctx context.Context, o runtime.HeadlessCodexOptions) (*Coordinator, error) {
	return openHeadless(ctx, o, "codex")
}

func OpenHeadlessOpenCode(ctx context.Context, o runtime.HeadlessOpenCodeOptions) (*Coordinator, error) {
	return openHeadless(ctx, o, "opencode")
}

func OpenHeadlessClaude(ctx context.Context, o runtime.HeadlessClaudeOptions) (*Coordinator, error) {
	return openHeadless(ctx, o, "claude")
}

func openHeadless(ctx context.Context, o runtime.HeadlessOptions, tool string) (*Coordinator, error) {
	if o.Tool != "" && o.Tool != tool {
		return nil, errors.New("headless reopen tool mismatch")
	}
	o.Tool = tool
	c, dir, err := lockRun(o.RunDir, o.RunID, []string{"agentflow", "coordinator", "--headless-" + tool})
	if err != nil {
		return nil, err
	}
	fail := func(err error) (*Coordinator, error) { _ = c.Close(); return nil, err }
	modePath := filepath.Join(dir, "headless."+tool+".json")
	info, err := os.Lstat(modePath)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return fail(errors.New("existing run is not an explicit headless " + tool + " run"))
	}
	f, err := os.Open(modePath)
	if err != nil {
		return fail(err)
	}
	var mode string
	d := json.NewDecoder(io.LimitReader(f, 1024))
	decodeErr := d.Decode(&mode)
	endErr := d.Decode(new(any))
	closeErr := f.Close()
	if decodeErr != nil || endErr != io.EOF || closeErr != nil || mode != "headless-"+tool+"-v1" {
		return fail(errors.New("headless mode marker invalid"))
	}
	c.db, err = store.OpenExisting(filepath.Join(dir, "state.db"), true)
	if err != nil {
		return fail(err)
	}
	run, err := c.db.GetRun(ctx)
	if err != nil {
		return fail(err)
	}
	snapshot, err := runtime.ReadSnapshot(filepath.Join(dir, "config.resolved.json"))
	if err != nil {
		return fail(err)
	}
	if run.RunID != o.RunID || snapshot.RunID != o.RunID || snapshot.Config.Digest != run.ConfigDigest {
		return fail(errors.New("headless run snapshot mismatch"))
	}
	o.RunDir = dir
	a, err := runtime.NewHeadless(snapshot.Config, o)
	if err != nil {
		return fail(err)
	}
	if err := c.listenWithAdapters(dir, a.ResetAdapter(), a, true); err != nil {
		return fail(err)
	}
	return c, nil
}
