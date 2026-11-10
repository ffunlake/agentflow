package runtime

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	runcontext "agentflow/internal/context"
)

func contextTestDigest(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func TestCreateContextRejectsInvalidSourceBeforeWriting(t *testing.T) {
	for _, fault := range []string{"relative", "traversal", "missing", "symlink", "ancestor-symlink", "run", "digest", "unknown", "public"} {
		t.Run(fault, func(t *testing.T) {
			dir, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(dir, 0700); err != nil {
				t.Fatal(err)
			}
			frozen := example(t, "json")
			frozen.ContextDir = filepath.Join(dir, "contexts")
			// ContextDir is part of the frozen config digest.
			frozen.Digest = ""
			body, err := json.Marshal(frozen)
			if err != nil {
				t.Fatal(err)
			}
			frozen.Digest = contextTestDigest(body)
			plan, err := Compile(frozen, Options{})
			if err != nil {
				t.Fatal(err)
			}
			s, err := NewSnapshot(plan, "run-a")
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "config.resolved.json")
			if err := WriteSnapshot(path, s); err != nil {
				t.Fatal(err)
			}
			runID := "run-a"
			switch fault {
			case "relative":
				path = "relative.json"
			case "traversal":
				path = dir + "/../" + filepath.Base(dir) + "/config.resolved.json"
			case "missing":
				path = filepath.Join(dir, "missing.json")
			case "symlink":
				link := filepath.Join(dir, "link.json")
				if err := os.Symlink(path, link); err != nil {
					t.Fatal(err)
				}
				path = link
			case "ancestor-symlink":
				link := filepath.Join(dir, "link-dir")
				if err := os.Symlink(dir, link); err != nil {
					t.Fatal(err)
				}
				path = filepath.Join(link, "config.resolved.json")
			case "run":
				runID = "run-b"
			case "digest", "unknown":
				if fault == "digest" {
					s.Config.Digest = "bad"
				}
				raw, _ := json.Marshal(s)
				if fault == "unknown" {
					raw = append([]byte(`{"unknown":true,`), raw[1:]...)
				}
				if err := os.WriteFile(path, raw, 0600); err != nil {
					t.Fatal(err)
				}
			case "public":
				if err := os.Chmod(path, 0644); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := CreateContext(path, runID); err == nil {
				t.Fatal("accepted invalid context source")
			}
			if _, err := os.Stat(frozen.ContextDir); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("created context after invalid source: %v", err)
			}
		})
	}
}

func TestCreateContextRejectsPublicContextBase(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	frozen := example(t, "json")
	frozen.ContextDir = filepath.Join(dir, "public")
	if err := os.Mkdir(frozen.ContextDir, 0755); err != nil {
		t.Fatal(err)
	}
	frozen.Digest = ""
	body, _ := json.Marshal(frozen)
	frozen.Digest = contextTestDigest(body)
	plan, err := Compile(frozen, Options{})
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewSnapshot(plan, "run-a")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.resolved.json")
	if err := WriteSnapshot(path, s); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateContext(path, "run-a"); !errors.Is(err, runcontext.ErrUnsafe) {
		t.Fatalf("accepted public base: %v", err)
	}
}
