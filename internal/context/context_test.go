package context

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func temp(t *testing.T) string {
	t.Helper()
	p, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, 0700); err != nil {
		t.Fatal(err)
	}
	return p
}

func fixture(t *testing.T) (*Run, *Agent, Snapshot) {
	t.Helper()
	r, err := Create(filepath.Join(temp(t), "contexts"), "run-a", "main", strings.Repeat("a", 64), []string{"worker", "main"})
	if err != nil {
		t.Fatal(err)
	}
	a, err := r.Agent("main")
	if err != nil {
		t.Fatal(err)
	}
	s := Snapshot{Version: Version, RunID: "run-a", AgentID: "main", Revision: "v1",
		Protocol: Protocol{Role: "lead", Tool: "codex", Cwd: "/project", Permissions: "full", Session: "persistent",
			Messages:     S5Protocol(),
			ConfigDigest: strings.Repeat("a", 64), PromptSHA256: strings.Repeat("b", 64),
			FrozenPrompt: Reference{Path: "/private/run/config.resolved.json", Purpose: "frozen role", Fragment: "config.agents.main.prompt"}},
		Background: Section{Text: "Necessary background", References: []Reference{{Path: "/project/README.md", Purpose: "project documentation"}}},
		Decisions:  Section{Text: "Keep the API small"}, Progress: Section{Text: "Implementation complete"}, Pending: Section{Text: "Independent review"}}
	return r, a, s
}

func requireError(t *testing.T, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("want %v, got %v", want, err)
	}
}

func TestRoundTripReopenAndImmutableRevisions(t *testing.T) {
	r, a, s := fixture(t)
	m, err := a.Publish(s)
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(r.base, s.RunID)
	if err != nil {
		t.Fatal(err)
	}
	bound, _ := reopened.Agent(s.AgentID)
	got, manifest, err := bound.Read(s.Revision)
	if err != nil || !reflect.DeepEqual(got, &s) || !reflect.DeepEqual(m, manifest) {
		t.Fatalf("round trip: %+v %+v %v", got, manifest, err)
	}
	if err := filepath.Walk(r.base, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		want := os.FileMode(0600)
		if info.IsDir() {
			want = 0700
		}
		if info.Mode().Perm() != want || strings.HasPrefix(info.Name(), ".tmp-") {
			t.Fatalf("unexpected mode/temp: %s %s", path, info.Mode())
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	_, err = a.Publish(s)
	requireError(t, err, ErrExists)
	_, err = Create(r.base, "run-a", "main", s.Protocol.ConfigDigest, []string{"main"})
	requireError(t, err, ErrExists)
	s.Revision, s.Progress.Text = "v2", "Reviewed"
	if _, err := a.Publish(s); err != nil {
		t.Fatal(err)
	}
	old, _, err := a.Read("v1")
	if err != nil || old.Progress.Text != "Implementation complete" {
		t.Fatalf("old revision changed: %+v %v", old, err)
	}
}

func TestBoundIdentityAndInvalidInput(t *testing.T) {
	for _, change := range []string{"run", "agent", "digest", "version", "revision", "role", "tool", "cwd", "permissions", "session", "prompt-digest", "fragment", "s5-protocol", "long-text", "long-refs", "relative-ref", "traversal-ref", "nul", "utf8"} {
		t.Run(change, func(t *testing.T) {
			_, a, s := fixture(t)
			want := ErrInvalid
			switch change {
			case "run":
				s.RunID, want = "run-b", ErrIdentity
			case "agent":
				s.AgentID, want = "worker", ErrIdentity
			case "digest":
				s.Protocol.ConfigDigest, want = strings.Repeat("c", 64), ErrIdentity
			case "version":
				s.Version = 2
			case "revision":
				s.Revision = "../worker/v1"
			case "role":
				s.Protocol.Role = " "
			case "tool":
				s.Protocol.Tool = "shell"
			case "cwd":
				s.Protocol.Cwd = "relative"
			case "permissions":
				s.Protocol.Permissions = "unknown"
			case "session":
				s.Protocol.Session = "unknown"
			case "prompt-digest":
				s.Protocol.PromptSHA256 = "bad"
			case "fragment":
				s.Protocol.FrozenPrompt.Fragment = "config.agents.worker.prompt"
			case "s5-protocol":
				s.Protocol.Messages.ACKMeaning = "completed"
			case "long-text":
				s.Background.Text = strings.Repeat("x", 8193)
			case "long-refs":
				s.Background.References = make([]Reference, 17)
			case "relative-ref":
				s.Background.References[0].Path = "relative"
			case "traversal-ref":
				s.Background.References[0].Path = "/project/../other"
			case "nul":
				s.Pending.Text = "\x00"
			case "utf8":
				s.Pending.Text = string([]byte{255})
			}
			_, err := a.Publish(s)
			requireError(t, err, want)
			if _, err := os.Stat(filepath.Join(a.Path(), s.Revision)); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("invalid publish touched disk: %v", err)
			}
		})
	}
	r, a, _ := fixture(t)
	for _, id := range []string{"other", "../worker", "/worker", "main/../worker"} {
		_, err := r.Agent(id)
		requireError(t, err, ErrIdentity)
	}
	_, _, err := a.Read("../worker/v1")
	requireError(t, err, ErrInvalid)
	for _, path := range []string{"relative", r.base + "/../contexts", "/"} {
		if _, err := Create(path, "another", "main", strings.Repeat("a", 64), []string{"main"}); err == nil {
			t.Fatalf("accepted base %s", path)
		}
	}
	for _, id := range []string{"", "..", "../run-b", "/run-b", "a/b", "a\\b", "a\x00b"} {
		if _, err := Create(r.base, id, "main", strings.Repeat("a", 64), []string{"main"}); err == nil {
			t.Fatalf("accepted run ID %q", id)
		}
	}
	if _, err := Create(r.base, "another", "main", strings.Repeat("a", 64), []string{"main", "main"}); err == nil {
		t.Fatal("accepted duplicate agents")
	}
}

func TestPermissionsAndSymlinksAtEveryLevel(t *testing.T) {
	for _, level := range []string{"base", "run", "agent", "revision", "manifest", "document", "descriptor", "agent-descriptor"} {
		for _, kind := range []string{"public", "symlink"} {
			t.Run(level+"/"+kind, func(t *testing.T) {
				r, a, s := fixture(t)
				if _, err := a.Publish(s); err != nil {
					t.Fatal(err)
				}
				revision := filepath.Join(a.Path(), "v1")
				paths := map[string]string{"base": r.base, "run": r.Path(), "agent": a.Path(), "revision": revision,
					"manifest": filepath.Join(revision, "manifest.json"), "document": filepath.Join(revision, "background.json"), "descriptor": filepath.Join(r.Path(), "run.json"), "agent-descriptor": filepath.Join(a.Path(), "agent.json")}
				path := paths[level]
				if kind == "public" {
					mode := os.FileMode(0644)
					if info, _ := os.Stat(path); info.IsDir() {
						mode = 0755
					}
					if err := os.Chmod(path, mode); err != nil {
						t.Fatal(err)
					}
				} else {
					if err := os.Rename(path, path+"-real"); err != nil {
						t.Fatal(err)
					}
					if err := os.Symlink(filepath.Base(path)+"-real", path); err != nil {
						t.Fatal(err)
					}
				}
				_, _, err := a.Read("v1")
				requireError(t, err, ErrUnsafe)
				// Any altered ancestor must also prevent new writes.
				if level == "base" || level == "run" || level == "agent" || level == "descriptor" || level == "agent-descriptor" {
					s.Revision = "v2"
					_, err := a.Publish(s)
					requireError(t, err, ErrUnsafe)
				}
			})
		}
	}
}

func TestSwappedAgentDirectoryRejectsWrites(t *testing.T) {
	for _, otherRun := range []bool{false, true} {
		t.Run(map[bool]string{false: "cross-agent", true: "cross-run"}[otherRun], func(t *testing.T) {
			r, a, s := fixture(t)
			other, _ := r.Agent("worker")
			if otherRun {
				second, err := Create(r.base, "run-b", "main", r.desc.ConfigDigest, []string{"main"})
				if err != nil {
					t.Fatal(err)
				}
				other, _ = second.Agent("main")
			}
			if err := os.Rename(a.Path(), a.Path()+"-saved"); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(other.Path(), a.Path()); err != nil {
				t.Fatal(err)
			}
			_, err := a.Publish(s)
			requireError(t, err, ErrIdentity)
			if _, err := os.Stat(filepath.Join(a.Path(), s.Revision)); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("wrote to substituted directory: %v", err)
			}
		})
	}
}

func TestCreateRejectsPublicBaseAndSymlinkAncestor(t *testing.T) {
	parent := temp(t)
	public := filepath.Join(parent, "public")
	if err := os.Mkdir(public, 0755); err != nil {
		t.Fatal(err)
	}
	_, err := Create(public, "r", "main", strings.Repeat("a", 64), []string{"main"})
	requireError(t, err, ErrUnsafe)
	if err := os.Symlink(parent, filepath.Join(parent, "link")); err != nil {
		t.Fatal(err)
	}
	_, err = Create(filepath.Join(parent, "link", "context"), "r", "main", strings.Repeat("a", 64), []string{"main"})
	requireError(t, err, ErrUnsafe)
}

func overwrite(t *testing.T, path string, body []byte) {
	t.Helper()
	if err := os.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestManifestAndDocumentCorruption(t *testing.T) {
	for _, fault := range []string{"missing-manifest", "missing-document", "checksum", "unknown-manifest", "unknown-document", "trailing", "malformed", "null", "oversized", "manifest-run", "manifest-agent", "manifest-revision", "manifest-version", "traversal", "duplicate-path", "timestamp", "document-identity", "document-payload", "protocol-config"} {
		t.Run(fault, func(t *testing.T) {
			_, a, s := fixture(t)
			m, err := a.Publish(s)
			if err != nil {
				t.Fatal(err)
			}
			dir := filepath.Join(a.Path(), "v1")
			manifest := filepath.Join(dir, "manifest.json")
			doc := filepath.Join(dir, "role.json")
			body, _ := os.ReadFile(doc)
			rehash := false
			switch fault {
			case "missing-manifest":
				if err := os.Remove(manifest); err != nil {
					t.Fatal(err)
				}
			case "missing-document":
				if err := os.Remove(doc); err != nil {
					t.Fatal(err)
				}
			case "checksum":
				overwrite(t, doc, append(body, ' '))
			case "unknown-manifest":
				raw, _ := os.ReadFile(manifest)
				overwrite(t, manifest, append([]byte(`{"unknown":true,`), raw[1:]...))
			case "unknown-document":
				body, rehash = append([]byte(`{"unknown":true,`), body[1:]...), true
			case "trailing":
				overwrite(t, manifest, []byte(`{} {}`))
			case "malformed":
				overwrite(t, manifest, []byte(`{bad`))
			case "null":
				overwrite(t, manifest, []byte(`null`))
			case "oversized":
				overwrite(t, manifest, []byte(strings.Repeat(" ", 128*1024+1)))
			case "manifest-run":
				m.RunID = "run-b"
			case "manifest-agent":
				m.AgentID = "worker"
			case "manifest-revision":
				m.Revision = "v2"
			case "manifest-version":
				m.Version = 2
			case "traversal":
				m.Files[0].Path = "../../worker/v1/role.json"
			case "duplicate-path":
				m.Files[1] = m.Files[0]
			case "timestamp":
				m.Files[0].UpdatedAt = m.Files[0].UpdatedAt.Add(-1)
			case "document-identity", "document-payload", "protocol-config":
				var d document
				if err := json.Unmarshal(body, &d); err != nil {
					t.Fatal(err)
				}
				switch fault {
				case "document-identity":
					d.RunID = "run-b"
				case "document-payload":
					d.Content = &Section{}
				case "protocol-config":
					d.Protocol.ConfigDigest = strings.Repeat("c", 64)
				}
				body, _ = encode(d)
				rehash = true
			}
			if rehash {
				overwrite(t, doc, body)
				m.Files[0].SHA256 = digest(body)
			}
			if rehash || strings.HasPrefix(fault, "manifest-") || fault == "traversal" || fault == "duplicate-path" || fault == "timestamp" {
				raw, _ := encode(m)
				overwrite(t, manifest, raw)
			}
			_, _, err = a.Read("v1")
			if err == nil {
				t.Fatal("accepted corrupt context")
			}
		})
	}
}

func TestDescriptorReplacementAndUnknownFields(t *testing.T) {
	for _, fault := range []string{"run", "unknown", "timestamp"} {
		t.Run(fault, func(t *testing.T) {
			r, a, s := fixture(t)
			path := filepath.Join(r.Path(), "run.json")
			d := r.desc
			if fault == "run" {
				d.RunID = "run-b"
			}
			if fault == "timestamp" {
				d.CreatedAt = d.CreatedAt.Add(1)
			}
			body, _ := encode(d)
			if fault == "unknown" {
				body = append([]byte(`{"unknown":true,`), body[1:]...)
			}
			overwrite(t, path, body)
			if _, err := a.Publish(s); err == nil {
				t.Fatal("published after descriptor replacement")
			}
		})
	}
}

func TestConcurrentDuplicatePublicationAndIncompleteRevision(t *testing.T) {
	_, a, s := fixture(t)
	var wg sync.WaitGroup
	errs := make(chan error, 12)
	for range 12 {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := a.Publish(s); errs <- err }()
	}
	wg.Wait()
	close(errs)
	winners := 0
	for err := range errs {
		if err == nil {
			winners++
		} else {
			requireError(t, err, ErrExists)
		}
	}
	if winners != 1 {
		t.Fatalf("publication winners: %d", winners)
	}
	if _, _, err := a.Read("v1"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(a.Path(), "incomplete"), 0700); err != nil {
		t.Fatal(err)
	}
	_, _, err := a.Read("incomplete")
	requireError(t, err, ErrCorrupt)
	s.Revision = "incomplete"
	_, err = a.Publish(s)
	requireError(t, err, ErrExists)
}

func TestPublishHelperNeverReplacesTarget(t *testing.T) {
	dir := temp(t)
	r, err := openAbsolute(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if err := publish(r, "target", []byte("original")); err != nil {
		t.Fatal(err)
	}
	requireError(t, publish(r, "target", []byte("replacement")), ErrExists)
	if err := r.Symlink("target", "link"); err != nil {
		t.Fatal(err)
	}
	requireError(t, publish(r, "link", []byte("replacement")), ErrExists)
	body, err := r.ReadFile("target")
	if err != nil || string(body) != "original" {
		t.Fatalf("target overwritten: %s %v", body, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 2 {
		t.Fatalf("temporary files leaked: %v %v", entries, err)
	}
}

func TestEncodedSizeLimitRejectsBeforeReservingRevision(t *testing.T) {
	_, a, s := fixture(t)
	// JSON escaping can expand individually valid compact fields past the reader
	// limit. Such a publication must fail before reserving a revision name.
	s.Background.Text = strings.Repeat("\x01", 8192)
	s.Background.References = nil
	for range 16 {
		s.Background.References = append(s.Background.References, Reference{Path: "/" + strings.Repeat("\x01", 4095), Purpose: "source"})
	}
	_, err := a.Publish(s)
	requireError(t, err, ErrInvalid)
	if _, err := os.Stat(filepath.Join(a.Path(), s.Revision)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("oversized publication reserved revision: %v", err)
	}
}
