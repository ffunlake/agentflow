package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTUIAgentEnvironmentPinsCMUXIdentity(t *testing.T) {
	got := tuiAgentEnvironment([]string{
		"PATH=/bin",
		"CMUX_AGENT_LAUNCH_KIND=codex",
		"CMUX_AGENT_LAUNCH_EXECUTABLE=/old/codex",
		"CMUX_AGENT_LAUNCH_CWD=/old",
	}, "opencode", "/opt/opencode", "/work")
	values := map[string]string{}
	for _, entry := range got {
		for i := 0; i < len(entry); i++ {
			if entry[i] == '=' {
				values[entry[:i]] = entry[i+1:]
				break
			}
		}
	}
	for key, want := range map[string]string{
		"PATH":                         "/bin",
		"CMUX_AGENT_LAUNCH_KIND":       "opencode",
		"CMUX_AGENT_LAUNCH_EXECUTABLE": "/opt/opencode",
		"CMUX_AGENT_LAUNCH_CWD":        "/work",
	} {
		if values[key] != want {
			t.Fatalf("%s=%q, want %q", key, values[key], want)
		}
	}
}

func TestWorkflowCodexWrapperIsBoundToCurrentCMUXSurface(t *testing.T) {
	surface := "844CAE5C-AC27-41DA-AD5C-50EFC1F3FC6E"
	shim := filepath.Join(t.TempDir(), "cmux-cli-shims", surface, "codex")
	if err := os.MkdirAll(filepath.Dir(shim), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(shim, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	got, err := workflowCodexWrapper([]string{
		"CMUX_CODEX_WRAPPER_SHIM=" + shim,
		"CMUX_SURFACE_ID=" + surface,
	})
	if err != nil || got != shim {
		t.Fatalf("workflowCodexWrapper() = %q, %v", got, err)
	}
	for _, env := range [][]string{
		{"CMUX_CODEX_WRAPPER_SHIM=" + shim},
		{"CMUX_CODEX_WRAPPER_SHIM=" + shim, "CMUX_SURFACE_ID=00000000-0000-0000-0000-000000000000"},
	} {
		if got, err := workflowCodexWrapper(env); err == nil || got != "" {
			t.Fatalf("accepted mismatched wrapper %q, %v", got, err)
		}
	}
}
