# CMUX layout launcher v1 result

Session: `cmux-layout-launcher-v1-20261104-01`  
Date: 2026-11-04  
Role: 资深研发（写代码）

## Delivered behavior

Default real `agentflow up -f CONFIG --run NEW_ID [--window REF] [--run-dir DIR]`
now creates a new cmux workspace from the compiled YAML/JSON binary layout.
One `cmux new-workspace --layout <JSON> --focus false` mutation creates every
terminal pane, preserving columns→horizontal, rows→vertical, nested geometry,
configured ratios and visual leaf order. Window is optional and omitted from
cmux argv by default, selecting the caller's window. Existing workspace/surface
targets are rejected by CLI validation.

Each terminal command invokes the separate `_tui-seat` entry. It rereads the
immutable private startup snapshot, selects the frozen agent/tool/role/prompt,
changes to that agent's cwd and `exec`s Codex, OpenCode or Claude Code TUI with
the existing terminal stdio. TUI launch does not use the old `_seat` one-shot
JSONL handshake. Codex uses its interactive default and `--cd`; OpenCode its
interactive default with project cwd and `--prompt`; Claude its interactive
default with the strict bare / explicit safe-mode choice. Permissions/model
remain argv values; effective permissions are UNVERIFIED. OpenCode TUI extra
args are unsupported, Codex accepts `--profile NAME`, and Claude accepts only
the existing exact `--bare` / `--safe-mode` authentication selection.

Tool binaries resolve through `NativeExecutable`, skipping cmux routing shims,
or use absolute `--codex-binary`, `--opencode-binary`, `--claude-binary` overrides.
`--cmux-binary` overrides cmux. Single-tool layouts also accept `--native-binary`;
mixed-tool layouts require named overrides when overriding tools. All required
binaries must exist and be executable; all agent cwd directories must exist.

## Runtime/API and evidence boundary

- `runtime.BuildCMUXWorkspaceCommand`: pure canonical-plan validator and closed
  layout serializer; only topology fields plus terminal `type/cwd/command` data.
- `runtime.ShellCommand`: POSIX single-word quoting of every executable/argument,
  including empty values and embedded single quotes; rejects NUL and invalid UTF-8.
- `runtime.PreflightCMUXWorkspace` / `LaunchCMUXWorkspace`: filesystem preflight,
  matching materialized snapshot, one creation request, bounded output and no retry.
- `runtime.TUICommand` / `LoadTUICommand`: frozen role/prompt/model argv builders,
  allowlisted extensions, no prompt/model/raw args in layout shell source.
- `coordinator.UpCMUX` or `UpOptions.CMUX`: original run-lock ownership, exclusive
  attempt marker, 0600 snapshot/result artifacts and release after launch.
- `ForegroundLauncher`, `BuildCMUXCommand`, `_seat` and explicit Native coordinator
  API retain the earlier one-shot handshake contract. Headless flags select their
  original coordinator/adapter execution paths.

Creation response parsing accepts JSON `workspace_id/workspace_ref` and legacy
`OK <workspace-ref|UUID>`. Malformed/duplicate JSON and trailing data are rejected.
A scoped read-only `tree --workspace <returned-identity>` verifies one matching
workspace/window, binary split directions and leaf-to-pane membership, exactly
one terminal per leaf and unique surface UUIDs/refs. Pane enumeration order is
not used to infer role order. Results contain window/workspace UUIDs and short
refs plus visual `agent_id/tool/surface_id/surface_ref` mappings. Exact pixel
geometry is subject to cmux terminal constraints; ratios are requested in JSON,
not treated as independently measured post-launch pixel evidence.

Success is `status=LAUNCHED`, `executed=true` for confirmed workspace creation,
`native_process=LAUNCH_REQUESTED`, `runtime_communication=UNVERIFIED`,
`registration=NOT_IMPLEMENTED`, `coordinator_ready=false`. No READY proof, native
session identity, registered store seat, automatic workflow dispatch, background
service or input ownership protocol is claimed. Native liveness/model readiness
is not established by a terminal surface existing. Initial snapshot seats remain
STARTING with empty native bindings. Session policy is retained metadata;
resume/fresh/reset and mixed headless+TUI execution are outside this change.

Private run artifacts are `up.requested.json`, `config.resolved.json` and
`cmux.launch.json`. Existing artifacts reject replay after success or failure.
Creation errors, timeouts, malformed responses or failed tree inspection may
leave a workspace; no automatic retry, pane cleanup or existing-window mutation
is attempted. Parseable creation coordinates are retained when inspection fails.
No state.db or workflow IPC is created by this launch-only path.

## Tests and observed validation

Fake executable: `internal/runtime/testdata/cmux/main.go`. It never contacts a
cmux socket or model. Tests build the real AgentFlow CLI, have fake cmux execute
the generated shell commands using `/bin/sh`, and capture each fake native tool's
actual argv and cwd. The fake tree reverses pane enumeration to test structural
mapping independently from list order.

Coverage:

- columns, rows, nested layouts, configured/default ratios, equivalent YAML/JSON;
- all three mixed tools, per-tool binary overrides, distinct cwd paths, frozen
  role and multiline prompt, model values with shell metacharacters;
- spaces, single/double quotes, empty argv, newlines, substitutions/backticks,
  semicolons and redirection characters remain literal through shell transport;
- NUL in cwd/role/prompt/model/args/binary/snapshot, invalid window, mutated plan,
  unavailable binary/cwd fail before fake cmux is invoked;
- JSON/short-ref/UUID creation responses; malformed/duplicate/trailing JSON,
  missing/wrong tree, wrong workspace/layout, duplicate surfaces and browser
  surfaces reject success;
- cmux nonzero exit, tree failure, cancellation before/during creation, retained
  attempt/result evidence, private snapshot unchanged and duplicate up no replay;
- TUI snapshots stay STARTING with no READY/registration/database/service claim;
- legacy foreground API nonce/READY/target/timeout/replay regression tests remain
  executable, alongside original Codex/OpenCode/Claude headless tests.

Validation observed on Go 1.25.5, darwin/arm64:

| Command | Result |
| --- | --- |
| `go test ./...` | PASS |
| `go build ./...` | PASS |
| `make check` | PASS (format, module download/verify, vet, all tests, CLI build) |
| `go mod tidy -diff` | PASS, no diff |
| `CGO_ENABLED=1 go test -race -json ./internal/runtime ./internal/coordinator ./internal/cli` | PASS; CLI 5.722s, coordinator 45.442s, runtime 111.149s; no race reports |

Full race events are captured in `.cache/cmux-layout-launcher-v1-race.jsonl`.
All three packages ended with package-level `Action: pass` and there are no
`Action: fail` or data race warnings. The detached verification command used
`-json` to allow progress monitoring without terminating long-running packages
at the tool's foreground deadline. Earlier two foreground race invocations hit
that deadline and do not count as PASS; the complete captured run is the evidence.

Installed cmux `0.65.2 (113) [12023d89f]` read-only `new-workspace --help` confirms
`--layout`, optional `--window`, surface-owned commands and the sample binary
`direction/split/children/pane/surfaces` shape. Tool `--help` output confirms the
interactive entry/flags used. Upstream CLI source was inspected for legacy
creation-output compatibility and tree `layout/panes/surfaces` structure.

This delivery did not create a real test workspace or operate existing user
panes. Real cmux rendering, model startup, login/trust prompts, continuous runtime
communication and workflow acceptance remain unverified. README/how-to-use and
runtime/coordinator contracts now distinguish the default TUI launcher from
explicit headless workflow execution.
