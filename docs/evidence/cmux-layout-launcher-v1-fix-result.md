# CMUX layout launcher v1 P2 input boundary fix

Session: `cmux-layout-launcher-v1-fix-20261104-01`  
Role: 资深研发（写代码）  
Status: DONE

Formal handoff used the requested `okra-agent-init/scripts/agent-handoff.sh send`
with `--window 1`, the session above and `--status DONE`. The script exited 0:
`[DONE] handoff sent: id=1fc6dbfb-59e9-4fdf-8687-27f6b1a134fe window=1 status=DONE`.
The matching main-agent ACK was received: “主 agent 已收到 P2 修复，开始 2 号最终复核。”
Final review remains the main agent/reviewer’s next step.

The workspace builder now rejects C0 (`U+0000–U+001F`), DEL (`U+007F`) and
C1 (`U+0080–U+009F`) before any cmux invocation. `ShellCommand` validates every
binary/argument token by Unicode scalar before applying the existing POSIX
single-quote escaping. The builder also validates role/cwd and cmux/per-tool
binary paths. Wrapper and snapshot paths are checked as generated command
tokens. Invalid UTF-8 continues to fail validation. Errors return no executable
workspace command; launch results remain unattempted.

Root cause: shell quoting preserves literal argv only after terminal input
processing. cmux appends a newline to each layout command and sends it through
terminal `sendInput`; Return, Tab, Backspace and escape/control sequences can
therefore alter input before the shell parses quotes. The previous executable
fixture invoked `/bin/sh -c` directly and missed this boundary. A new regression
test was observed failing before the fix for a canonical plan with CR in role.

The fake cmux now checks the supported terminal transport subset on every
layout command before any optional shell execution. It models the final Return
and CRLF coalescing and fails closed on key/control input; it does not emulate a
complete terminal. This is pinned to cmux commit
`12023d89feb5425c766ba05e8d8e4cfc176e3991`:

- [Layout command newline and submission](https://github.com/manaflow-ai/cmux/blob/12023d89feb5425c766ba05e8d8e4cfc176e3991/Sources/Workspace%2BCustomLayout.swift)
- [Terminal sendInput parsing](https://github.com/manaflow-ai/cmux/blob/12023d89feb5425c766ba05e8d8e4cfc176e3991/Packages/macOS/CmuxTerminal/Sources/CmuxTerminal/Surface/TerminalSurface%2BInput.swift)

Regression coverage:

- All 65 C0/DEL/C1 scalars plus CRLF, ESC navigation/OSC and C1 CSI/OSC sequences
  across role, cwd, tool binary, snapshot path, wrapper binary and cmux binary.
  Tests recompile canonical plans where the config decoder permits the value;
  values already rejected by Compile remain rejected before execution as well.
  Builder, preflight and launcher reject with no fake cmux call/state file.
- Every shell binary/argument token rejects the same range and invalid UTF-8.
- Twelve transport cases cover LF/CR/CRLF submission and quoted terminal keys
  and controls, including multiple submission rejection.
- CLI cases use an existing cwd containing TAB and an executable binary with
  DEL, plus role CR and snapshot-path ESC, proving rejection before run-directory
  creation, cmux calls, fake workspace state or native tool execution.
- Spaces, single/double quotes, shell metacharacters, literal backslash escapes,
  Chinese, emoji and Unicode outside the rejected ranges retain their literal
  meaning. Executable CLI fixtures exercise Unicode wrapper/tool/cwd/snapshot
  paths and mixed Codex/OpenCode/Claude startup.

Validation completed:

| Command | Result |
| --- | --- |
| `go test ./...` | PASS; runtime 44.057s, coordinator 40.540s, CLI 4.614s, e2e 37.963s |
| `go build ./...` | PASS |
| `make check` | PASS; format, module download/verification, vet, all tests and CLI build |
| `go mod tidy -diff` | PASS; no diff |
| `CGO_ENABLED=1 go test -race -json ./internal/runtime ./internal/coordinator ./internal/cli` | PASS; runtime 167.171s, coordinator 82.859s, CLI 11.509s; no race warnings or failed tests |
| `go test ./internal/runtime ./internal/coordinator ./tests/e2e -run '^TestHeadless(Codex\|OpenCode\|Claude)(RecordedNativeProtocol\|Protocol\|IntegratedFixtures\|CLIWorkflowAndRestart)$' -count=1` | PASS; runtime 0.788s, coordinator 26.186s, e2e 16.258s |

Full check, race and three-tool headless logs are respectively:

- `.cache/cmux-layout-launcher-v1-fix-check.log` (`MAKE_CHECK_PASS`)
- `.cache/cmux-layout-launcher-v1-fix-race.jsonl` (three package-level pass events)
- `.cache/cmux-layout-launcher-v1-fix-headless.log` (`HEADLESS_THREE_TOOLS_PASS`)

Initial foreground `make check` and focused headless commands exceeded the
tool's 60-second foreground limit. The complete background runs above supply
the passing evidence; timeouts were not counted as passes.

The successful launcher still reports `LAUNCHED`, communication `UNVERIFIED`,
registration `NOT_IMPLEMENTED`, and `coordinator_ready=false`; snapshots stay
STARTING without native bindings. Multiline prompt bodies remain private
snapshot data delivered directly to native argv. Headless paths continue to
use their existing adapters. Verification used executable fixtures only; no
real workspace was created and no user window was operated. The optional
real Codex nonce test remained skipped, as expected without explicit native
integration opt-in.

Changed source/contract files (SHA-256):

```text
4b11dc6aa1e3de416d5a703857c1df68ed36ea3e0aafe8578b33afa0b384739f  internal/runtime/cmux_workspace.go
9f20652977351ae49581d773c27bfdf4d5363883831f6c540c8970f58af12ecd  internal/runtime/cmux_workspace_test.go
baa056c42c4749e30bc1975e5dd2aa14ff51bde1360d666c87c2862f9d479e8e  internal/runtime/testdata/cmux/main.go
7532a2e57d5abd00e27f8170b746f1bcdd54baa1ad6f8dc2460aa0c0fa36f1b0  internal/cli/up_cmux_test.go
cbbb19e757747bd7adb939206ed0d2426911ff1622944c1913697f781d176e6a  internal/runtime/README.md
```
