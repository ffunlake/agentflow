package config

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const minimal = `{
  "version": 1, "name": "team", "terminal": "cmux", "lead": "main",
  "context_dir": "../context", "defaults": {},
  "agents": {"main": {"role": "lead role", "prompt": "lead prompt"}},
  "layout": {"agent": "main"}
}`

func writeConfig(t *testing.T, dir, name, text string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func loadText(t *testing.T, text string) *Config {
	t.Helper()
	c, err := Load(writeConfig(t, t.TempDir(), "team.json", text))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func requireField(t *testing.T, err error, path, message string) {
	t.Helper()
	var field *FieldError
	if !errors.As(err, &field) || field.Path != path || !strings.Contains(err.Error(), message) {
		t.Fatalf("want field %q containing %q, got %v", path, message, err)
	}
}

func TestExamples(t *testing.T) {
	var snapshots []*Resolved
	for _, ext := range []string{"yaml", "json"} {
		t.Run(ext, func(t *testing.T) {
			path, err := filepath.Abs(filepath.Join("..", "..", "examples", "team."+ext))
			if err != nil {
				t.Fatal(err)
			}
			c, err := Load(filepath.Join("..", "..", "examples", "team."+ext))
			if err != nil {
				t.Fatal(err)
			}
			if c.Defaults.Cwd == nil {
				t.Fatal("example defaults.cwd is required")
			}
			expectedCwd := filepath.Clean(*c.Defaults.Cwd)
			r, err := Resolve(c, ResolveOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if r.SourcePath != path || r.ContextDir != filepath.Clean(filepath.Join(filepath.Dir(path), "../.agentflow/context")) {
				t.Fatalf("incorrect paths: %+v", r)
			}
			for _, want := range []struct{ id, tool, session string }{
				{"main", "codex", "persistent"}, {"builder", "opencode", "persistent"}, {"reviewer", "opencode", "persistent"},
			} {
				a := r.Agents[want.id]
				if a.Tool != want.tool || a.Session != want.session || a.Permissions != "full" || a.Cwd != expectedCwd || a.Prompt == "" || a.Role == "" {
					t.Fatalf("%s resolved incorrectly: %+v", want.id, a)
				}
			}
			r.SourcePath = ""
			snapshots = append(snapshots, r)
		})
	}
	if len(snapshots) != 2 || !reflect.DeepEqual(snapshots[0], snapshots[1]) {
		t.Fatalf("YAML/JSON snapshots differ: %+v", snapshots)
	}
}

func TestResolveDefaultsAndOverrides(t *testing.T) {
	for _, tc := range []struct {
		name, defaults, overrides, cliTool string
		tool, cwd, permissions, session    string
	}{
		{"documented defaults", `{}`, ``, "", "opencode", ".", "full", "fresh"},
		{"defaults", `{"tool":"claude","cwd":"../work","permissions":"default","session":"persistent"}`, ``, "", "claude", "../work", "default", "persistent"},
		{"CLI over defaults", `{"tool":"claude"}`, ``, "codex", "codex", ".", "full", "fresh"},
		{"agent over CLI and defaults", `{"tool":"claude","cwd":"../work","permissions":"default","session":"persistent"}`, `,"tool":"opencode","cwd":"local","permissions":"full","session":"fresh","model":"chosen","args":["--flag","$(touch forbidden)","../literal"]`, "codex", "opencode", "local", "full", "fresh"},
		{"CLI without defaults", `{}`, ``, "claude", "claude", ".", "full", "fresh"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			text := strings.Replace(minimal, `"defaults": {}`, `"defaults": `+tc.defaults, 1)
			text = strings.Replace(text, `"prompt": "lead prompt"`, `"prompt": "lead prompt"`+tc.overrides, 1)
			c := loadText(t, text)
			r, err := Resolve(c, ResolveOptions{Tool: tc.cliTool})
			if err != nil {
				t.Fatal(err)
			}
			a := r.Agents["main"]
			base := filepath.Dir(c.SourcePath)
			if a.Tool != tc.tool || a.Cwd != filepath.Join(base, tc.cwd) || a.Permissions != tc.permissions || a.Session != tc.session || a.Role != "lead role" || a.Prompt != "lead prompt" {
				t.Fatalf("incorrect resolved values: %+v", a)
			}
			if r.ContextDir != filepath.Join(base, "../context") || !filepath.IsAbs(a.Cwd) {
				t.Fatalf("paths not relative to config: %+v", r)
			}
			if tc.overrides != "" && (a.Model != "chosen" || !reflect.DeepEqual(a.Args, []string{"--flag", "$(touch forbidden)", "../literal"})) {
				t.Fatalf("model/args altered: %+v", a)
			}
			if _, err := os.Stat(filepath.Join(base, "forbidden")); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("args were executed")
			}
		})
	}
}

func TestStrictSchemaAndValues(t *testing.T) {
	for _, tc := range []struct {
		name, old, replacement, path, message string
	}{
		{"version missing", `"version": 1,`, ``, "version", "must be 1"},
		{"version unsupported", `"version": 1`, `"version": 2`, "version", "must be 1"},
		{"version string", `"version": 1`, `"version": "1"`, "version", "integer"},
		{"version fractional", `"version": 1`, `"version": 1.5`, "version", "integer"},
		{"version overflow", `"version": 1`, `"version": 99999999999999999999999999`, "version", "integer"},
		{"name empty", `"name": "team"`, `"name": "  "`, "name", "non-empty"},
		{"name missing", `"name": "team",`, ``, "name", "non-empty"},
		{"name numeric", `"name": "team"`, `"name": 42`, "name", "string"},
		{"terminal missing", `"terminal": "cmux",`, ``, "terminal", "non-empty"},
		{"terminal invalid", `"terminal": "cmux"`, `"terminal": "native"`, "terminal", "cmux"},
		{"context missing", `"context_dir": "../context",`, ``, "context_dir", "non-empty"},
		{"lead missing", `"lead": "main",`, ``, "lead", "non-empty"},
		{"lead unknown", `"lead": "main"`, `"lead": "other"`, "lead", "existing agent"},
		{"agents missing", `"agents": {"main": {"role": "lead role", "prompt": "lead prompt"}},`, ``, "agents", "at least one"},
		{"agents array", `"agents": {"main": {"role": "lead role", "prompt": "lead prompt"}}`, `"agents": []`, "agents", "object"},
		{"agent null", `"main": {"role": "lead role", "prompt": "lead prompt"}`, `"main": null`, "agents.main", "null"},
		{"role missing", `"role": "lead role",`, ``, "agents.main.role", "non-empty"},
		{"role empty", `"role": "lead role"`, `"role": " "`, "agents.main.role", "non-empty"},
		{"role boolean", `"role": "lead role"`, `"role": true`, "agents.main.role", "string"},
		{"prompt missing", `, "prompt": "lead prompt"`, ``, "agents.main.prompt", "exactly one"},
		{"prompt conflict", `"prompt": "lead prompt"`, `"prompt": "lead prompt", "prompt_file": "role.md"`, "agents.main.prompt", "exactly one"},
		{"prompt empty", `"prompt": "lead prompt"`, `"prompt": " "`, "agents.main.prompt", "non-empty"},
		{"prompt file empty", `"prompt": "lead prompt"`, `"prompt_file": ""`, "agents.main.prompt_file", "non-empty"},
		{"prompt null", `"prompt": "lead prompt"`, `"prompt": null`, "agents.main.prompt", "null"},
		{"tool invalid", `"defaults": {}`, `"defaults": {"tool":"unknown"}`, "defaults.tool", "one of"},
		{"tool empty", `"defaults": {}`, `"defaults": {"tool":""}`, "defaults.tool", "one of"},
		{"tool null", `"defaults": {}`, `"defaults": {"tool":null}`, "defaults.tool", "null"},
		{"permissions invalid", `"defaults": {}`, `"defaults": {"permissions":"all"}`, "defaults.permissions", "one of"},
		{"session invalid", `"defaults": {}`, `"defaults": {"session":"reuse"}`, "defaults.session", "one of"},
		{"default cwd empty", `"defaults": {}`, `"defaults": {"cwd":""}`, "defaults.cwd", "non-empty"},
		{"default cwd wrong type", `"defaults": {}`, `"defaults": {"cwd":[]}`, "defaults.cwd", "string"},
		{"agent tool invalid", `"prompt": "lead prompt"`, `"prompt": "lead prompt", "tool":"Codex"`, "agents.main.tool", "one of"},
		{"agent permissions invalid", `"prompt": "lead prompt"`, `"prompt": "lead prompt", "permissions":"all"`, "agents.main.permissions", "one of"},
		{"agent session invalid", `"prompt": "lead prompt"`, `"prompt": "lead prompt", "session":"reuse"`, "agents.main.session", "one of"},
		{"agent cwd empty", `"prompt": "lead prompt"`, `"prompt": "lead prompt", "cwd":""`, "agents.main.cwd", "non-empty"},
		{"model numeric", `"prompt": "lead prompt"`, `"prompt": "lead prompt", "model":42`, "agents.main.model", "string"},
		{"args string", `"prompt": "lead prompt"`, `"prompt": "lead prompt", "args":"--flag"`, "agents.main.args", "array"},
		{"args numeric", `"prompt": "lead prompt"`, `"prompt": "lead prompt", "args":["--flag",42]`, "agents.main.args[1]", "string"},
		{"unknown root", `"version": 1`, `"typo": 1, "version": 1`, "typo", "unknown field"},
		{"unknown defaults", `"defaults": {}`, `"defaults": {"model":"x"}`, "defaults.model", "unknown field"},
		{"defaults null", `"defaults": {}`, `"defaults": null`, "defaults", "null"},
		{"unknown agent", `"prompt": "lead prompt"`, `"prompt": "lead prompt", "typo":true`, "agents.main.typo", "unknown field"},
		{"source metadata rejected", `"version": 1`, `"SourcePath":"/tmp/other", "version": 1`, "SourcePath", "unknown field"},
		{"duplicate root", `"version": 1`, `"version": 1, "version": 1`, "version", "duplicate object key"},
		{"duplicate defaults", `"defaults": {}`, `"defaults": {"tool":"codex","tool":"claude"}`, "defaults.tool", "duplicate object key"},
		{"duplicate agent key", `"main": {"role": "lead role", "prompt": "lead prompt"}`, `"main": {"role":"a","prompt":"p"}, "main": {"role":"b","prompt":"q"}`, "agents.main", "duplicate object key"},
		{"escaped duplicate key", `"prompt": "lead prompt"`, `"prompt": "lead prompt", "pr\u006fmpt":"other"`, "agents.main.prompt", "duplicate object key"},
		{"layout missing", `,\n  "layout": {"agent": "main"}`, ``, "layout", "is required"},
		{"layout type", `"layout": {"agent": "main"}`, `"layout": []`, "layout", "object"},
		{"layout unknown", `"layout": {"agent": "main"}`, `"layout": {"agent":"main","typo":1}`, "layout.typo", "unknown field"},
		{"layout duplicate key", `"layout": {"agent": "main"}`, `"layout": {"agent":"main","agent":"main"}`, "layout.agent", "duplicate object key"},
		{"layout agent unknown", `"layout": {"agent": "main"}`, `"layout": {"agent":"other"}`, "layout.agent", "unknown agent"},
		{"layout agent omitted", `"layout": {"agent": "main"}`, `"layout": {}`, "layout", "missing agent reference"},
		{"layout duplicate reference", `"layout": {"agent": "main"}`, `"layout": {"split":"rows","children":[{"agent":"main"},{"agent":"main"}]}`, "layout.children[1].agent", "duplicate agent reference"},
		{"layout child unknown", `"layout": {"agent": "main"}`, `"layout": {"children":[{"agent":"main","typo":1}]}`, "layout.children[0].typo", "unknown field"},
		{"layout ratio string", `"layout": {"agent": "main"}`, `"layout": {"agent":"main","ratio":"0.5"}`, "layout.ratio", "finite number"},
		{"layout children type", `"layout": {"agent": "main"}`, `"layout": {"children":{}}`, "layout.children", "array"},
		{"layout split type", `"layout": {"agent": "main"}`, `"layout": {"split":42}`, "layout.split", "string"},
		{"layout empty agent", `"layout": {"agent": "main"}`, `"layout": {"agent":""}`, "layout.agent", "non-empty"},
		{"layout empty split on leaf", `"layout": {"agent": "main"}`, `"layout": {"agent":"main","split":""}`, "layout.split", "non-empty"},
		{"layout empty agent on split", `"layout": {"agent": "main"}`, `"layout": {"split":"rows","agent":"","children":[{"agent":"main"}]}`, "layout.agent", "non-empty"},
	} {
		for _, ext := range []string{"yaml", "json"} {
			t.Run(tc.name+"/"+ext, func(t *testing.T) {
				old := strings.ReplaceAll(tc.old, `\n`, "\n")
				if !strings.Contains(minimal, old) {
					t.Fatalf("fixture does not contain %q", old)
				}
				text := strings.Replace(minimal, old, tc.replacement, 1)
				_, err := Load(writeConfig(t, t.TempDir(), "team."+ext, text))
				requireField(t, err, tc.path, tc.message)
			})
		}
	}
}

func TestAgentIDs(t *testing.T) {
	for _, id := range []string{"a", "a_0-b", "a" + strings.Repeat("b", 47), "Main", "1main", "_main", "main.dot", "a" + strings.Repeat("b", 48), "主"} {
		t.Run(id, func(t *testing.T) {
			text := strings.ReplaceAll(minimal, `"main"`, `"`+id+`"`)
			_, err := Load(writeConfig(t, t.TempDir(), "team.json", text))
			if id == "a" || id == "a_0-b" || len(id) == 48 {
				if err != nil {
					t.Fatal(err)
				}
			} else {
				requireField(t, err, "agents."+id, "ID must match")
			}
		})
	}
}

func TestOmittedDefaultsAndMissingMemberReference(t *testing.T) {
	c := loadText(t, strings.Replace(minimal, `, "defaults": {}`, "", 1))
	r, err := Resolve(c, ResolveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	a := r.Agents["main"]
	if a.Tool != "opencode" || a.Cwd != filepath.Dir(c.SourcePath) || a.Permissions != "full" || a.Session != "fresh" {
		t.Fatalf("absent defaults resolved incorrectly: %+v", a)
	}
	c.Agents["builder"] = Agent{Role: "builder", Prompt: &a.Prompt}
	requireField(t, Validate(c), "layout", "missing agent reference builder")
}

func TestYAMLSpecificStrictness(t *testing.T) {
	const header = "version: 1\nname: team\nterminal: cmux\nlead: main\ncontext_dir: context\n"
	const agents = "agents:\n  main:\n    role: lead\n    prompt: |\n      first line\n      second line\nlayout:\n  agent: main\n"
	for _, tc := range []struct{ name, text, path, message string }{
		{"numeric coercion", strings.Replace(header+agents, "role: lead", "role: 123", 1), "agents.main.role", "string"},
		{"duplicate", header + "name: duplicate\n" + agents, "name", "duplicate object key"},
		{"alias", header + "defaults: &d {tool: codex}\n" + strings.Replace(agents, "role: lead", "role: *d", 1), "agents.main.role", "aliases"},
		{"merge", header + "defaults: {<<: {tool: codex}}\n" + agents, "defaults", "merge keys"},
		{"non-string key", header + "defaults: {1: codex}\n" + agents, "defaults", "keys must be strings"},
		{"extra document", header + agents + "---\n{}\n", "$", "one document"},
		{"null document", "null\n", "$", "null"},
		{"custom tag", header + "defaults: !custom {}\n" + agents, "defaults", "object"},
		{"non-finite ratio", header + strings.Replace(agents, "agent: main", "agent: main\n  ratio: .nan", 1), "layout.ratio", "finite number"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Load(writeConfig(t, t.TempDir(), "team.yml", tc.text))
			requireField(t, err, tc.path, tc.message)
		})
	}
	c, err := Load(writeConfig(t, t.TempDir(), "team.yaml", header+agents))
	if err != nil || c.Agents["main"].Prompt == nil || *c.Agents["main"].Prompt != "first line\nsecond line\n" {
		t.Fatalf("multiline prompt changed: %+v, %v", c, err)
	}
}

func TestDocumentAndIOErrors(t *testing.T) {
	for _, tc := range []struct{ name, text string }{
		{"YAML in JSON", "version: 1\n"}, {"trailing JSON", minimal + " {}"},
		{"trailing comma", `{"version":1,}`}, {"empty", ""}, {"array root", "[]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Load(writeConfig(t, t.TempDir(), "team.json", tc.text))
			requireField(t, err, "$", "")
		})
	}
	_, err := Load(filepath.Join(t.TempDir(), "missing.yaml"))
	requireField(t, err, "$", "read config")
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatal("missing file cause was lost")
	}
	_, err = Load("team.txt")
	requireField(t, err, "$", "extension")
	_, err = Load(writeConfig(t, t.TempDir(), "team.yaml", "{broken"))
	requireField(t, err, "$", "decode")
}

func TestPromptFilesAndDetachedSnapshot(t *testing.T) {
	dir := t.TempDir()
	prompt := writeConfig(t, dir, "role.md", "original role text\n")
	text := strings.Replace(minimal, `"prompt": "lead prompt"`, `"prompt_file":"role.md","args":["original"]`, 1)
	text = strings.Replace(text, `"layout": {"agent": "main"}`, `"layout":{"split":"rows","ratio":0.5,"children":[{"agent":"main"}]}`, 1)
	c, err := Load(writeConfig(t, dir, "team.json", text))
	if err != nil {
		t.Fatal(err)
	}
	r, err := Resolve(c, ResolveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if r.Agents["main"].Prompt != "original role text\n" || r.Agents["main"].PromptFile != prompt {
		t.Fatalf("prompt not resolved relative to config: %+v", r.Agents["main"])
	}
	before, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	digest := r.Digest
	r.Digest = ""
	body, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(body)
	if digest != hex.EncodeToString(hash[:]) {
		t.Fatal("snapshot digest does not match normalized JSON")
	}
	r.Digest = digest
	writeConfig(t, dir, "role.md", "changed on disk")
	updated, err := Resolve(c, ResolveOptions{})
	if err != nil || updated.Digest == digest {
		t.Fatalf("changed prompt did not change new snapshot digest: %v", err)
	}
	a := c.Agents["main"]
	a.Args[0] = "mutated"
	a.Role = "mutated"
	c.Agents["main"] = a
	c.Layout.Children[0].Agent = "mutated"
	*c.Layout.Ratio = 0.9
	after, err := json.Marshal(r)
	if err != nil || string(before) != string(after) {
		t.Fatal("source/file edits mutated snapshot")
	}
}

func TestResolveErrorsAndAbsolutePaths(t *testing.T) {
	for _, tc := range []struct{ name, body, path, message string }{
		{"empty file", " \n", "agents.main.prompt", "non-empty"},
		{"invalid UTF-8", string([]byte{0xff}), "agents.main.prompt", "UTF-8"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := loadText(t, strings.Replace(minimal, `"prompt": "lead prompt"`, `"prompt_file":"role.md"`, 1))
			writeConfig(t, filepath.Dir(c.SourcePath), "role.md", tc.body)
			_, err := Resolve(c, ResolveOptions{})
			requireField(t, err, tc.path, tc.message)
		})
	}
	c := loadText(t, strings.Replace(minimal, `"prompt": "lead prompt"`, `"prompt_file":"missing.md"`, 1))
	_, err := Resolve(c, ResolveOptions{})
	requireField(t, err, "agents.main.prompt_file", "read prompt file")
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatal("prompt read cause lost")
	}
	c = loadText(t, minimal)
	_, err = Resolve(c, ResolveOptions{Tool: "bad"})
	requireField(t, err, "options.tool", "one of")
	c.SourcePath = ""
	_, err = Resolve(c, ResolveOptions{})
	requireField(t, err, "source_path", "required")
	c.Version = 2
	_, err = Resolve(c, ResolveOptions{})
	requireField(t, err, "version", "must be 1")
	requireField(t, Validate(nil), "$", "required")

	base := t.TempDir()
	absCwd := filepath.Join(base, "nonexistent", "work")
	absContext := filepath.Join(base, "context")
	text := strings.Replace(minimal, `"defaults": {}`, `"defaults":{"cwd":`+quote(t, absCwd)+`}`, 1)
	text = strings.Replace(text, `"context_dir": "../context"`, `"context_dir":`+quote(t, absContext), 1)
	r, err := Resolve(loadText(t, text), ResolveOptions{})
	if err != nil || r.Agents["main"].Cwd != absCwd || r.ContextDir != absContext {
		t.Fatalf("absolute paths changed: %+v, %v", r, err)
	}
	text = strings.Replace(minimal, `"defaults": {}`, `"defaults":{"cwd":"$HOME/~/literal"}`, 1)
	c = loadText(t, text)
	r, err = Resolve(c, ResolveOptions{})
	if err != nil || r.Agents["main"].Cwd != filepath.Join(filepath.Dir(c.SourcePath), "$HOME/~/literal") {
		t.Fatalf("path text was expanded: %+v, %v", r, err)
	}
}

func quote(t *testing.T, text string) string {
	t.Helper()
	b, err := json.Marshal(text)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestLayoutGeometryIsDeferredWithoutRewriting(t *testing.T) {
	for _, layout := range []string{
		`{"agent":"main","ratio":0.01}`,
		`{"split":"diagonal","ratio":1.5,"children":[{"agent":"main"}]}`,
		`{"agent":"main","split":"rows","children":[]}`,
	} {
		t.Run(layout, func(t *testing.T) {
			c := loadText(t, strings.Replace(minimal, `{"agent": "main"}`, layout, 1))
			r, err := Resolve(c, ResolveOptions{})
			if err != nil || !reflect.DeepEqual(*c.Layout, r.Layout) {
				t.Fatalf("S2.1 reinterpreted geometry: %+v, %v", r, err)
			}
		})
	}
}
