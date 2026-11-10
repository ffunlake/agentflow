# Headless OpenCode v1 result — 2026-11-04

Session: `headless-opencode-v1-20261104-01`.

## Delivered behavior

`up --headless-opencode` initializes homogeneous OpenCode persistent logical roles
at generation 1. `coordinator --headless-opencode` reopens only a matching private
version marker and frozen config under the existing run lock; it never performs
startup or drain. The headless tool flags are mutually exclusive. CLI workflow
start/resume/drain/status uses the existing handlers and runner. Default Codex
selection and default coordinator persistence behavior are preserved.

Reopen here means **clean coordinator shutdown and lock release, then reopen**.
It does not establish recovery after a forced exit or crash that leaves a stale
run lock. Existing locks, including stale or malformed locks, still prevent startup
with `ALREADY_RUNNING`; the current implementation does not automatically delete,
replace or take over them. The restart fixtures below verify clean shutdown/reopen
only. Strict field parsing follow-up and fresh verification are recorded in
[the fix result](headless-opencode-v1-fix-result.md).

The former `headless_codex.go` is now the shared `internal/runtime/headless.go`.
`HeadlessCodex`/options remain aliases; `NewHeadlessCodex` still selects Codex, and
`NewHeadlessOpenCode` selects OpenCode. `NativeCommand`, bounded capture, exclusive
capture reservation, context cancellation, SessionCoordinator, WorkflowRunner,
store/outbox/action/journal/idempotency and stop/generation gates are reused.
Only the native resume argv helper is extracted from the existing submit adapter.
No dependency, schema, scheduling service or second execution architecture is added.

OpenCode uses `run --format json --dir CWD` and explicit `--session ID` on resume.
Prompts and work bodies use stdin. `headless_opencode.go` parses JSONL with stable
session/message identity, matching step start/finish, text parts and final finish
reason `stop`. Text from intermediate `tool-calls` steps is discarded. Reasoning,
user echoes and tool output cannot supply final text. Duplicate JSON fields,
changed identities, unknown finish reasons, text after completion, multiple turns,
malformed/oversized/incomplete streams and failed processes cannot confirm success.
Explicit native error reports FAILED; other uncertain stream outcomes report UNKNOWN.
Neither permits automatic replay.

Startup final JSON must echo exact actor/generation/tool/nonce READY. Workflow
final JSON reuses handoff/approve/changes/retry/blocked, strict result fields and
explicit boolean acceptance. Captured results can advance only after matching
durable SENT. Captures do not substitute for a missing submission confirmation.
Accepted event, reply and next dispatch retain the existing atomic store path.

## Fixture matrix

All tests here use an executable Go fixture at
`internal/runtime/testdata/headless-codex` that emits either tool's protocol based
on its CLI argv. It starts no model and invokes neither installed OpenCode nor cmux.
The same integration helper runs for Codex and OpenCode, retaining the Codex
recorded-native parser regression separately.

| Area | OpenCode fixtures and observed invariant |
| --- | --- |
| Success | 2 READY + execute handoff + reviewer approve, revision 3 / COMPLETED; multiple text parts and intermediate tool-call step text handled |
| Native failure / uncertainty | failed, unknown, wrong-thread, malformed, exit, wait timeout retain SUBMIT_REQUESTED / attempt 1; restart/drain makes no extra call |
| Step/session errors | no-finish, wrong-part-session, unknown-finish, after-finish cannot become SENT or workflow completion |
| Structured result | invalid-result, duplicate-result, case-result, false-review retain SENT without advancing on invalid judgments |
| Structured control | blocked pauses; explicit lead resume completes at revision 5; retry and changes consume finite budget and exhaust |
| Idempotency | exact start and resume retries preserve saved receipt/state; concurrent start invokes only one execute/review chain |
| Restart | queued dispatch explicitly drains; SENT-before-event collects its existing capture; SUBMIT_REQUESTED never replays, premature ACK fails, a second assignment remains queued |
| Fresh | explicit fresh override queues without reset or native submission |
| Stop | persisted stop survives reopen, blocks queued drain/new start; status executes no native work |
| Generations | workflow control with generation 2 rejected before state writes; adapter Inspect/Submit/Launch reject generation 2; existing store old-dispatch generation tests remain in full suite |
| Startup rejection | wrong-nonce, wrong-generation, unknown, failed, malformed, no-finish, wrong-part-session, unknown-finish, after-finish never produce READY or restart native on repeated up |
| Mode/default | Codex reopen rejects OpenCode run, default coordinator saves workflow without native execution, invalid marker content rejects reopen |
| CLI processes | separate up/workflow/coordinator processes cover successful start, blocked→resume, resume retry, status, terminal drain, stale revision and completed-run reopen without extra native calls |

`TestHeadlessOpenCodeIntegratedFixtures` has 34 subcases. Protocol unit tests add
missing session, option-like session, identity drift, missing message/step/text,
reasoning-only, user echo, unsupported event alone, duplicate fields, truncated or
over-limit line, post-completion output and explicit failure classification.

Focused reproduction:

```sh
go test -v ./internal/runtime -run TestHeadlessOpenCode -count=1
go test -v ./internal/coordinator -run TestHeadlessOpenCodeIntegratedFixtures -count=1
go test -v ./tests/e2e -run TestHeadlessOpenCodeCLIWorkflowAndRestart -count=1
```

## Real OpenCode boundary

**Real model integration: NOT RUN / UNVERIFIED in this delivery.** The installed
`/opt/homebrew/bin/opencode run --help` was read to confirm the format, directory,
session and permission flags. This help invocation launches no model. No real
READY, model result, file-edit permission or live model workflow completion is
claimed. Prior OpenCode transport/up evidence is historical and is not counted as
this headless v1 integration passing. No credentials/login state were copied and
no user windows were operated for implementation or verification.

The requested final handoff uses the authorized collaboration script separately;
its notification transport is not native workflow verification.

## Verification

Platform: Go 1.25.5, darwin/arm64. All required final commands exited 0:

| Command | Result and retained output |
| --- | --- |
| `go test ./...` | PASS, all packages; [test log](headless-opencode-v1-test.log) |
| `go build ./...` | PASS, no output; [build log](headless-opencode-v1-build.log) |
| `make check` | PASS, format/modules/vet/all tests/CLI build; [check log](headless-opencode-v1-check.log) |
| `go mod tidy -diff` | PASS, no diff/output; [tidy log](headless-opencode-v1-tidy.log) |
| `go test -race ./internal/runtime ./internal/coordinator ./internal/cli ./internal/store ./internal/loopcore ./tests/e2e` | PASS, all relevant packages; [race log](headless-opencode-v1-race.log) |

Focused runtime/coordinator/CLI/e2e checks also passed during implementation.
An initial concurrent make/race invocation hit the outer shell's 60-second deadline
before completion; it is not counted as a pass. Final make and race were run
separately with a 120-second outer deadline; runtime race finished in 104.573s.
The retained logs above are the complete successful invocations.

Codex regression includes its original executable/CLI paths and archived native
capture parser test; the existing Codex model test remains opt-in. No real model
execution is part of the default fixture or final verification commands.

## Limits

Persistent, generation 1 and a single tool per run only. Work runs as bounded
foreground subprocesses; no TUI/cmux ownership, live attach/process stop, Claude,
fresh/reset, background scheduler/service, mixed tools or complex scheduling.
Each explicit drain remains bounded to 128 dispatches. Stop is the existing intent
gate and does not claim to kill an already running native child. Native defaults,
plugins and credentials are inherited by an actual opt-in launch; effective tool
permissions and evidence content are not independently verified by this adapter.

This workspace has no git metadata; changes are reviewable as files and tests,
with no commit/diff available. Configuration example:
[headless-opencode.yaml](../../examples/headless-opencode.yaml). Design:
[headless OpenCode v1](../plans/2026-11-04-headless-opencode-v1-design.md).

## Handoff

The requested
`/Users/funlake/.codex/skills/okra-agent-init/scripts/agent-handoff.sh send`
was executed for window 1, session `headless-opencode-v1-20261104-01`, status DONE,
with the result document, successful check commands/logs and real-model boundary.
The script returned exit 0 and:

```text
[DONE] handoff sent: id=dbfa3611-3350-4d82-bed4-3e5a37f68bf5 window=1 status=DONE
```

Main agent acknowledged this handoff ID with message:
“主 agent 已收到 OpenCode v1 handoff，开始最小复核并交 2 号独立 review。”
ACK confirms receipt; the main agent's acceptance and independent review are separate.
