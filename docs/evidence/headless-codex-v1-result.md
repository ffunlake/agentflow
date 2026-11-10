# Headless Codex v1 result — 2026-11-04

Session: `headless-codex-v1-20261104-01`.

## Delivered scope

Explicit `up --headless-codex` and `coordinator --headless-codex` install a real
Codex child-process adapter on the existing coordinator/store/WorkflowRunner path.
The example uses two persistent logical roles, a no-tools nonce handoff and a
separate explicit review judgment. No services, task tables, MQ, DAG, terminal
layout execution or cmux ownership guard were added.

`workflow start|resume|drain` is the explicit foreground CLI; `workflow status`
reads the current cursor without executing work. Default coordinator
without the headless flag remains persistence-only. Configuration still uses the
existing terminal/layout schema; in headless mode those describe logical roles,
and all tools must resolve to Codex with session=persistent.

The adapter validates native JSONL thread identity and completion, selects the
final completed assistant response, saves private per-invocation captures, and
collects structured handoff/review/failure/block events after durable SENT.
Reply/event/next dispatch commit atomically through the existing store API.
`workflow.cursor` is the original event receipt; `workflow_current` is the latest
cursor after collection. DONE, zero exit, tool output, and completion alone are
insufficient for workflow acceptance.

## Fixture integration boundary

`TestHeadlessCodexIntegratedFixtures` uses an executable Go Codex substitute with
the real Up, Start/Registry, SQLite, Unix IPC, native process invocation,
SessionCoordinator and WorkflowRunner. It exercises successful execution/review,
native failure, incomplete stream, wrong thread, malformed JSONL, nonzero exit,
non-JSON final answer, duplicate/case-variant result fields, false review judgment,
blocked events, finite retries and a fresh override that queues without reset.

Restart coverage includes queued-before-submit, SENT-before-workflow-event,
SUBMIT_REQUESTED with no confirmation, unknown native results and completed runs.
The executable invocation log verifies exact call counts across replay/restart.
SENT-before-event can collect the existing capture and continue; requested/unknown
submissions never replay. Exact start event replay preserves the original receipt
and makes no state changes. A terminal drain performs no native action.

`TestHeadlessCodexCLIWorkflowAndRestart` uses separate AgentFlow up, workflow and
coordinator CLI processes with the native fixture. It completes revision 3,
restarts, retries start, drains terminal state and rejects stale drain revision
without new Codex calls. These are fixture integration checks, not real native
model or tool verification.

## Real Codex evidence

Real testing is opt-in through `AGENTFLOW_REAL_CODEX=/absolute/real/codex` and
`TestHeadlessCodexRealNonceIntegration`. It uses a new isolated temporary empty cwd,
no-tools nonce instructions, real Up/IPC/outbox/runner/review and a coordinator
restart with exact event replay. No cmux command or user terminal is part of the
test. Optional AGENTFLOW_REAL_EVIDENCE_DIR stores native captures and the integration
receipt without copying credentials or stderr.

**Real nonce integration: PASS, within this bounded transport/workflow scope.**
`TestHeadlessCodexRealNonceIntegration` passed in 139.97 seconds using
`/opt/homebrew/bin/codex`. The run initialized both real threads, submitted the
execute outbox through exec resume, collected the nonce handoff, submitted the
review outbox, collected `approve` with `accepted:[true]`, and committed revision
3 / COMPLETED. The coordinator then shut down normally and reopened; exact start
replay had executed=false, no dispatch and an unchanged database snapshot.

The test verified four completed native captures, no native tool-call items and an
unchanged empty temporary cwd. All four streams contain the installed CLI's hooks
configuration warning as an item diagnostic. Those warnings are retained rather
than rewritten or promoted to native failure/success evidence.

- Isolated cwd: `/private/var/folders/wv/x8nwj9tj7xz5dhqf5f2mt3hw0000gn/T/af-hc-1219310023/cwd`
  (removed by test cleanup after empty-directory verification).
- Builder thread: `01a1a4ad-d919-7a33-9d8c-65cd500972dd`.
- Reviewer/main thread: `01a1a4ae-55e3-74a2-8ec1-1692381c832c`.
- Nonce: `AGENTFLOW_HEADLESS_V1_NONCE`.
- Raw captures and receipt: [native evidence directory](headless-codex-v1-native-complete/).
- Exact test output: [native-test.log](headless-codex-v1-native-complete/native-test.log).
- Original IPC/up/replay output: [integration.json](headless-codex-v1-native-complete/integration.json).

The previous one-shot smoke in native-headless-codex-2026-11-04.md is independent
evidence and is not used to claim integration success. The successful test above
traverses coordinator Up, existing durable outbox claims, native result parsing,
workflow event writes and coordinator/store reopen.

After this real run, the final code tightened duplicate/case-variant result parsing,
added read-only workflow status and corrected multi-role readiness output. Up now
returns all `seats`/`ready_proofs`, and the legacy singular seat/proof pair matches
the same actor. The archived raw integration output predates that metadata fix:
its singular seat is builder while its singular ready_proof is main. Both real
READY turns are independently present in the raw captures. This output correction
is covered by the final executable fixture checks. The final strict result parser
also successfully replays both unchanged real result captures in
`TestHeadlessCodexRecordedNativeProtocol`; that is offline regression evidence,
not a second native run.

Observed unsuccessful attempts are not counted as acceptance: the initial READY
check failed; a subsequent outer 120s execution deadline interrupted review after
both READY turns and one real nonce handoff; another run hit the 85s native turn
deadline before startup completion. The latter raw incomplete stream is retained
in `headless-codex-v1-native/headless-start-builder.json`. Unknown requests are
left unreplayed. The successful fresh run used a 3-minute per-turn limit.
Codex emits a hooks-configuration diagnostic item, which is
distinct from top-level error/turn.failed and does not itself confirm or fail a turn.

## Limits and unfinished work

- Only Codex persistent logical roles, generation 1; fresh/reset/recovery adapters
  and other tools are not implemented in this path.
- Foreground bounded consumption only, at most 128 dispatches per call. Opening a
  coordinator never drains, and no background worker/service is installed.
- Native failure/timeout after action claim remains SUBMIT_REQUESTED plus unknown
  journal evidence. It requires manual reconciliation; even a saved result does
  not bypass missing SENT confirmation.
- A crash after accepted workflow event but before submission needs explicit drain.
  Missing/invalid captures or structured results stop advancement; no automatic
  retry, login, account/model switch, stale lock takeover or credential copying.
- Native success proves transport/result processing, not file-editing permission,
  arbitrary coding-task correctness, or independent evidence-content validation.
- Native default configuration/hooks are inherited; warnings are retained in stdout
  captures. No effective permissions or sandbox claim is made.
- TUI input, user cmux windows, attach/process stop, multi-tool layouts, wiki and
  complex scheduling are outside this delivery.

## Verification

Final required verification, all exit 0:

- `go test ./...` — all packages pass; actual Codex integration is opt-in and skipped
  by default. Fixture integration and archived-native parser regression are included.
- `go build ./...` — pass.
- `make check` — formatting, module download/verification, go vet, all tests and CLI
  build pass using the repository's local cache and CGO_ENABLED=0 settings.
- `go mod tidy -diff` — pass, no diff; no dependency or migration added.

Focused final verification also passes for runtime/coordinator/CLI/e2e packages.
The headless fixture matrix has 17 subcases, including deadline cancellation,
and separate CLI process coverage passes. All final code checks were performed
after the parsing/readiness/status corrections described above. No git metadata
exists in this workspace, so no commit or git diff is available.

Focused fixture reproduction:

```sh
go test -v ./internal/coordinator -run TestHeadlessCodexIntegratedFixtures -count=1
go test -v ./tests/e2e -run TestHeadlessCodexCLIWorkflowAndRestart -count=1
```

Real reproduction (uses the installed authenticated Codex, isolated cwd, no tools):

```sh
AGENTFLOW_REAL_CODEX=/absolute/real/codex \
  go test -v ./internal/coordinator -run '^TestHeadlessCodexRealNonceIntegration$' -count=1
```

See the root README and internal/coordinator/README.md for user commands and deadlines.

## Handoff

The requested agent-handoff script sent status DONE for window 1 and session
`headless-codex-v1-20261104-01`, returning:

```text
[DONE] handoff sent: id=595cca9d-d0ce-4276-be7d-1acf5013ea94 window=1 status=DONE
```
