# Headless Claude Code v1 — 2026-11-04

Session: `headless-claude-v1-20261104-01`.

Implementation: **PARTIAL, fixture verified**. Claude native model execution,
authentication, effective permissions and real persistent resume are
**UNVERIFIED**. This report does not claim a real Claude workflow completed.

## Read-only protocol evidence

The installed real executable, bypassing cmux routing shims, is
`/Users/funlake/.nvm/versions/node/v26.8.1/bin/claude`. It reports
`2.1.318 (Claude Code)`. Only `--version` and `--help` were run against Claude.
The PATH initially resolves a cmux shim; runtime lookup explicitly skips it.

Retained outputs: [version](headless-claude-v1-cli-version.log) and
[complete help](headless-claude-v1-cli-help.log). Help confirms:

- `--print`: print the response and exit, with non-interactive stdout.
- `--output-format`: `text`, `json`, or `stream-json`.
- `--input-format`: `text` or `stream-json` in print mode.
- `--resume`: continue a specified conversation; `--session-id` requires a UUID.
- `--no-session-persistence` disables resume, so this adapter never supplies it.
- `--bare`: skips hooks/plugins/auto-discovery and keychain/OAuth reads.
- `--permission-prompts none`: nobody answers a prompt; unresolved calls deny.

Official references read on this date:

- [Run Claude Code programmatically](https://code.claude.com/docs/en/headless):
  stdin transport, print/verbose/stream-json, terminal `result`, exit semantics,
  session resume, bare-mode authentication and disabled background work.
- [SDKSystemMessage](https://code.claude.com/docs/en/agent-sdk/typescript#sdksystemmessage):
  `system/init`, `uuid`, `session_id` and session metadata.
- [SDKAssistantMessage](https://code.claude.com/docs/en/agent-sdk/typescript#sdkassistantmessage):
  main/subagent attribution, full Anthropic message content, error/abort fields;
  one API turn can emit multiple messages with the same message ID.
- [SDKResultMessage](https://code.claude.com/docs/en/agent-sdk/typescript#sdkresultmessage):
  `success` versus known error subtypes, `is_error`, `result`, `stop_reason`,
  `num_turns`, `terminal_reason`, `result_index` and diagnostic metadata.
- [SDKRateLimitEvent](https://code.claude.com/docs/en/agent-sdk/typescript#sdkratelimitevent):
  independent rate status; it is neither final text nor completion.

This establishes an inspectable documented non-interactive interface. It does
not establish that any authenticated native turn succeeded on this host.

## Implementation and boundaries

[Design](../plans/2026-11-04-headless-claude-v1-design.md) records the supplied INIT
scope and the shared-kernel approach.

- [headless.go](../../internal/runtime/headless.go) adds Claude aliases/constructor
  and a shared session/text turn result. Existing process deadlines, exclusive
  captures, READY nonce verification and workflow JSON decoding are reused.
- [headless_claude.go](../../internal/runtime/headless_claude.go) implements Claude
  JSONL parsing. Keys are exact and case-sensitive, duplicate keys are rejected
  recursively, and every event must match the native UUID session. Unknown event
  types/fields/subtypes fail closed. Per-line limit is 1 MiB, capture limit 4 MiB.
- Native command construction adds `--print --bare --verbose --output-format
  stream-json --input-format text --permission-prompts none --prompt-suggestions
  false`. Input is stdin; cwd is a process attribute. Resume appends exactly
  `--resume <bound UUID>`; paths, session names, option values and changed native
  IDs cannot become resume bindings. Claude raw args are rejected. `model` stays
  a literal argv value. `permissions=full` adds `--dangerously-skip-permissions`;
  default does not. Effective permissions have not been tested with a model.
- Require init, main assistant text and one terminal result. Text blocks with the
  same assistant message ID concatenate; a different ID replaces the intermediate
  candidate. User/tool/thinking/subagent text cannot acknowledge READY or workflow
  completion. A success result must match that text, have `is_error=false`,
  `num_turns>0`, `stop_reason=end_turn`, and, if supplied,
  `terminal_reason=completed` and `result_index=0`. Aborted, deferred, structured
  output, local-command, API-error and contradictory error data cannot confirm it.
- Known terminal native failures return FAILED. Missing init/result/assistant,
  malformed/oversized streams, unknown finish reasons, nonzero exits without a
  valid native failure and timeouts remain UNKNOWN. Both retain the existing
  conservative delivery gate and never authorize automatic native replay.
- Explicit mutually exclusive `up --headless-claude` and `coordinator
  --headless-claude` select the adapter. Reopen requires a private
  `headless.claude.json` marker with `headless-claude-v1` and matching frozen config.
  Default coordinator remains persistence-only; Codex is the default Headless
  selector. Claude `_seat`/TUI and guarded NativeSubmit remain UNIMPLEMENTED.
- [Example](../../examples/headless-claude.yaml), CLI help and README document
  startup/reopen and auth boundaries. Bare-mode execution needs an existing API
  key/provider environment; no OAuth subscription login is consumed or copied.

No coordinator, WorkflowRunner, store, outbox, idempotency, generation or drain
state machine is copied. Scope remains homogeneous persistent agents, generation
1, foreground bounded processes. No background service, fresh/reset, mixed-tool
scheduler or user configuration changes were added.

## Fixtures and regression coverage

The shared [executable fixture](../../internal/runtime/testdata/headless-codex/main.go)
emits synthetic native events and records argv. It never invokes a model or tool.

- [Protocol tests](../../internal/runtime/headless_claude_test.go): success,
  session discovery, multiple assistant text blocks, final candidate replacement,
  known native failures and UNKNOWN outcomes; malformed/null/blank/truncated and
  oversized JSONL; aliases, duplicate and escaped duplicate fields; session paths,
  names, option injection and mismatches; missing init/assistant/completion;
  user/subagent/thinking/tool-only text; aborted and contradictory success; unknown
  events, finish/result/terminal reasons; rate metadata cannot establish completion;
  literal argv, managed flags, full/default permissions and real PATH shim skipping.
- Shared READY tests include Claude and reject nonce conflicts, duplicates,
  aliases and actor/generation mismatches. Restrictions cover fresh, mixed tools,
  shim binaries, invalid deadlines and direct generation-2 Launch/Inspect/Submit.
- [Coordinator matrix](../../internal/coordinator/headless_test.go): two-seat READY,
  handoff/approve, false review, malformed workflow JSON, duplicate/case fields,
  blocked→decision→resume, retry/changes exhaustion, explicit native failure,
  missing result, wrong session, bad finish, event after completion, nonzero exit,
  deadline, wrong nonce/generation and failed startup with no replay.
- The same matrix covers normal close/reopen, request and resume replay with table
  snapshots/call counts, four concurrent starts, fresh override without reset,
  stop intent, old/future generation requests, cross-tool/invalid marker rejection,
  default coordinator with no execution, and queued/SENT-before-event/
  SUBMIT_REQUESTED restart cuts. SENT capture consumption continues; unknown
  submission cannot be ACKed, free the seat, or cause another native invocation.
- [CLI e2e](../../tests/e2e/headless_codex_test.go) starts real AgentFlow CLI
  processes with fixture native binaries, runs workflow success and blocked/resume,
  reopens with the explicit Claude flag and verifies replay/status/drain. It also
  asserts the actual print/bare/resume UUID argv and default permission flags.

Reopen here means normal coordinator shutdown and lock release. A crash or forced
exit leaving a lock remains `ALREADY_RUNNING`; v1 never takes over/removes stale
locks. Passing these fixtures does not prove crash recovery or native resume.

## Acceptance

Platform: Go 1.25.5, darwin/arm64. Final source was held fixed for this verification
sequence. Raw command argv, durations and exit codes are retained in
[verification.json](headless-claude-v1-verification.json).

| Required command | Result | Output |
| --- | --- | --- |
| `go test ./...` | PASS, exit 0 | [test log](headless-claude-v1-test.log) |
| `go build ./...` | PASS, exit 0, no output | [build log](headless-claude-v1-build.log) |
| `make check` | PASS, exit 0; format/modules/vet/tests/build | [check log](headless-claude-v1-check.log) |
| `go mod tidy -diff` | PASS, exit 0, no diff/output | [tidy log](headless-claude-v1-tidy.log) |
| `CGO_ENABLED=1 go test -race ./internal/runtime ./internal/coordinator ./internal/cli ./internal/store ./internal/loopcore ./tests/e2e` | PASS, exit 0; all six packages | [race log](headless-claude-v1-race.log) |

Final race invocation completed normally in 111.441 seconds. Runtime took
109.635s, coordinator 46.820s, CLI 8.151s, store 15.598s and e2e 38.778s;
loopcore reused its passing test cache. No timeout/interruption is counted as PASS.

Detailed Claude/READY/CLI focused tests also passed with `-count=1`:
[focused log](headless-claude-v1-focused.log). Full test/check include Codex and
OpenCode integration/e2e regressions.

An earlier race attempt is explicitly **not counted as a pass**. I changed the
executable fixture while the earlier test process was running, so the already
compiled coordinator parser rejected the new `rate_limit_event` emitted by its
subsequently rebuilt fixture (`CLAUDE_EVENT_UNKNOWN`). No data race was reported;
the attempt failed because the test and fixture used different source snapshots.
The process finished before final verification was restarted with frozen source.
Retained evidence: [initial failed race](headless-claude-v1-initial-race.log) and
[initial exit codes](headless-claude-v1-initial-verification.json).

Validation subprocess environments remove `AGENTFLOW_REAL_CODEX` and
`AGENTFLOW_REAL_EVIDENCE_DIR`. Claude model calls have no opt-in in these tests.
No model was started, credentials were not read/copied, no login occurred, and no
user terminal was operated for integration validation. The explicitly requested
handoff is collaboration transport and is not native integration evidence.

The workspace has no git metadata. Source files, tests, docs and command output
form the reviewable delivery; no commit or push was performed.

## Handoff

Executed the required
`/Users/funlake/.codex/skills/okra-agent-init/scripts/agent-handoff.sh send` with
window 1, session `headless-claude-v1-20261104-01`, status DONE and summary/evidence/
next fields. It exited 0 and confirmed:

```text
[DONE] handoff sent: id=fadf588b-2fe7-4b50-992f-34e5f481161c window=1 status=DONE
```

This confirms delivery through the collaboration transport. Main-agent acceptance
and independent review remain separate from fixture verification.

The main agent acknowledged this handoff ID with:
“主 agent 已收到 Claude headless v1 handoff，开始 2 号独立 review。”
