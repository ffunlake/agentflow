package context

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestBootstrapLatestAndConcurrentImmutableCache(t *testing.T) {
	r, a, s := fixture(t)
	if _, err := a.Publish(s); err != nil {
		t.Fatal(err)
	}
	s.Revision, s.Progress.Text = "a-later", "[x] New progress despite lexical order"
	if _, err := a.Publish(s); err != nil {
		t.Fatal(err)
	}
	r, err := Open(r.base, r.ID())
	if err != nil {
		t.Fatal(err)
	}
	a, _ = r.Agent(a.id)
	b, reports, err := a.PrepareBootstrap(2)
	if err != nil || b.Revision != "a-later" || len(reports) != 0 {
		t.Fatalf("%+v %v", b, err)
	}
	b.Recommendation, b.Next = "Load files into a new session", "Independent review"
	var wg sync.WaitGroup
	paths := make(chan string, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			path, err := a.PublishBootstrap(b)
			if err != nil {
				t.Error(err)
				return
			}
			paths <- path
		}()
	}
	wg.Wait()
	close(paths)
	path := ""
	for p := range paths {
		if path != "" && path != p {
			t.Fatal("unstable cache path")
		}
		path = p
	}
	files, err := os.ReadDir(filepath.Dir(path))
	if err != nil || len(files) != 1 {
		t.Fatalf("duplicate cache: %v %v", files, err)
	}
	body, _ := os.ReadFile(path)
	if bytes.Contains(body, []byte("transcript")) {
		t.Fatal("unexpected transcript")
	}
	if err := os.WriteFile(path, []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err = a.PublishBootstrap(b)
	if !errors.Is(err, ErrCorrupt) {
		t.Fatal(err)
	}
	old, _, err := a.Read("v1")
	if err != nil || old.Progress.Text != "Implementation complete" {
		t.Fatalf("old context changed: %+v %v", old, err)
	}
}

func TestBootstrapRejectsBrokenAndForeignSources(t *testing.T) {
	for _, fault := range []string{"no-revision", "incomplete", "corrupt-manifest", "corrupt-document", "symlink", "foreign-agent", "foreign-run", "foreign-fragment", "foreign-frozen-path", "uncommitted-reference", "role-reference"} {
		t.Run(fault, func(t *testing.T) {
			r, a, s := fixture(t)
			switch fault {
			case "foreign-agent":
				s.Progress.References = []Reference{{Path: filepath.Join(r.Path(), "worker", "v1", "progress.json"), Purpose: "foreign"}}
			case "foreign-run":
				s.Progress.References = []Reference{{Path: filepath.Join(r.base, "other-run", "main", "v1", "manifest.json"), Purpose: "foreign"}}
			case "foreign-fragment":
				s.Background.References = []Reference{{Path: s.Protocol.FrozenPrompt.Path, Fragment: "config.agents.worker.prompt", Purpose: "foreign"}}
			case "foreign-frozen-path":
				s.Protocol.FrozenPrompt.Path = filepath.Join(r.Path(), "worker", "v1", "role.json")
			case "uncommitted-reference":
				s.Pending.References = []Reference{{Path: filepath.Join(a.Path(), "missing", "manifest.json"), Purpose: "missing"}}
			case "role-reference":
				s.Decisions.References = []Reference{{Path: filepath.Join(a.Path(), "v1", "unmanaged.json"), Purpose: "missing"}}
			}
			if fault != "no-revision" {
				if _, err := a.Publish(s); err != nil {
					t.Fatal(err)
				}
			}
			switch fault {
			case "incomplete":
				if err := os.Mkdir(filepath.Join(a.Path(), "unfinished"), 0700); err != nil {
					t.Fatal(err)
				}
			case "corrupt-manifest":
				if err := os.WriteFile(filepath.Join(a.Path(), "v1", "manifest.json"), []byte("{}"), 0600); err != nil {
					t.Fatal(err)
				}
			case "corrupt-document":
				if err := os.WriteFile(filepath.Join(a.Path(), "v1", "pending.json"), []byte("{}"), 0600); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Symlink(filepath.Join(a.Path(), "v1"), filepath.Join(a.Path(), "alias")); err != nil {
					t.Fatal(err)
				}
			}
			if _, _, err := a.PrepareBootstrap(2); err == nil {
				t.Fatal("unsafe source accepted")
			}
		})
	}
}

func TestBootstrapReverifiesMergedReports(t *testing.T) {
	_, lead, worker, r := reportFixture(t)
	if _, err := worker.PublishReport(r); err != nil {
		t.Fatal(err)
	}
	if _, err := lead.Merge("v1", "merged", []ReportRef{{AgentID: r.AgentID, Generation: r.Generation, ReportID: r.ReportID}}); err != nil {
		t.Fatal(err)
	}
	b, reports, err := lead.PrepareBootstrap(2)
	if err != nil || b.Revision != "merged" || len(reports) != 1 || len(b.Reports) != 1 || b.Reports[0].Meaning != "agent_claim" {
		t.Fatalf("%+v %v", b, err)
	}
	if err := os.WriteFile(r.Evidence[0].Path, []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := lead.PrepareBootstrap(2); !errors.Is(err, ErrCorrupt) {
		t.Fatal(err)
	}
}
