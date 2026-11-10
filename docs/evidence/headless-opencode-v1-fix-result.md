# Headless OpenCode v1 strict parsing fix — 2026-11-04

Session: `headless-opencode-v1-fix-20261104-02`.

## Root cause and implementation

The OpenCode headless stream previously decoded envelope and part objects with
plain `json.Unmarshal`. Unknown fields were discarded and struct fields matched
case-insensitively. The shared READY decoder used `DisallowUnknownFields`, which
still permits case aliases and duplicate keys. A later correct nonce could
overwrite an earlier conflicting nonce.

- [headless_opencode.go](../../internal/runtime/headless_opencode.go) now decodes
  envelope and part keys through explicit, case-sensitive field switches. Only
  the protocol spellings `type`, `sessionID`, `part`, `error`, `timestamp` and the
  supported part fields are accepted. Known native metadata remains opaque and
  cannot supply identity, final text, completion or READY. Unknown envelope/part
  fields, including finish/error/session/message aliases, fail closed. An error
  field on a non-error event is invalid; a canonical native error event remains
  FAILED. Unsupported event types cannot be ignored inside an otherwise valid turn.
- [native.go](../../internal/runtime/native.go) adds exact READY and actor decoding:
  `actor/tool/status/nonce` and `runId/agentId/generation`. The shared decoder
  rejects duplicate keys before assigning values, including repeated equal nonces,
  conflicting nonces and JSON-escaped duplicate spellings. Its object and EOF
  checks retain unknown-field and trailing-data rejection. No case normalization,
  permissive map decoding or configurable parser DSL is introduced.
- Existing session/message identity, step start/finish, final `stop`, intermediate
  tool-call text discard, duplicate keys, unknown finish reasons, malformed/oversized
  streams and completion checks remain enforced. Codex and OpenCode share the
  stricter READY decoder; the other native stream parsers retain their contracts.

## Five rejection regressions

[headless_opencode_test.go](../../internal/runtime/headless_opencode_test.go) adds
four native rejection groups and one shared READY rejection regression:

| Regression | Rejected examples |
| --- | --- |
| `TestHeadlessOpenCodeRejectsReservedFieldAliases/error` | `error` on a text event, `Error`/`ERROR`, error fields inside a part |
| `…/session` | incorrect `sessionid`, unknown `session_id`/`session`, envelope and part aliases, a later case alias hiding a wrong canonical session |
| `…/message` | `messageId`, `message_id`, `message`, misplaced envelope message identity |
| `…/finish` and finish mutations | unknown `finish`/`finish_reason`, native key case aliases, `Reason` hiding an unknown canonical reason |
| `TestHeadlessReadyRejectsNonceConflictsAndFieldAliases` | conflicting or equal duplicate nonce, both `nonce`/`Nonce` orders, all READY/actor key case aliases, unknown actor field; exercised for both tools |

The initial regressions were run before changing the parser. All four native
groups accepted malformed input, and both tools accepted conflicting duplicate
READY nonces. The same tests passed after the fix. A positive metadata case also
checks the timestamp, part ID/time/metadata and step snapshot/cost/tokens fields.
Existing executable fixtures, recorded Codex parsing and shared native READY tests
are included in the full acceptance checks.

Retained focused regression output: [focused log](headless-opencode-v1-fix-focused.log).

## Verification

Platform: Go 1.25.5, darwin/arm64. All five final checks exited 0.

| Required command | Result | Output |
| --- | --- | --- |
| `go test ./...` | PASS, exit 0 | [final test log](headless-opencode-v1-fix-test-final.log) |
| `go build ./...` | PASS, exit 0, no output | [build log](headless-opencode-v1-fix-build.log) |
| `make check` | PASS, exit 0; format/modules/vet/tests/build | [check log](headless-opencode-v1-fix-check.log) |
| `go mod tidy -diff` | PASS, exit 0, no diff/output | [tidy log](headless-opencode-v1-fix-tidy.log) |
| `go test -race ./internal/runtime ./internal/coordinator ./internal/cli ./internal/store ./internal/loopcore ./tests/e2e` | PASS, exit 0, all six packages | [race log](headless-opencode-v1-fix-race.log) |

Race package durations: runtime 109.816s, coordinator 38.166s, CLI 6.182s,
store 9.906s, loopcore 7.271s and e2e 35.227s. The final race invocation completed
normally in 111.794s; no timeout or interrupted invocation is counted as a pass.

The first full test invocation failed in the existing
`TestNativeSubmitExecutableFixtures/opencode/wait` synchronization, with an empty
transport record ([initial failure log](headless-opencode-v1-fix-test.log)). Its
cancel goroutine checked file existence; the fixture creates the file before
encoding its contents, so cancellation could interrupt the write. The existing
test now waits for a complete JSON record before canceling. This only changes
test synchronization in
[native_submit_test.go](../../internal/runtime/native_submit_test.go).

## Reopen and runtime boundaries

The [README](../../README.md) and [original v1 result](headless-opencode-v1-result.md)
now explicitly distinguish **normal shutdown, lock release and reopen** from
**forced exit/crash with a leftover lock**. The clean reopen fixtures do not prove
forced-exit recovery. Existing locks, including stale or malformed locks, block
startup with `ALREADY_RUNNING`; this version does not automatically remove,
replace or take over stale locks. No lock recovery behavior is added.

Validation uses local Go executable fixtures only. The real-model opt-in
`AGENTFLOW_REAL_CODEX` and its evidence-directory variable are explicitly removed
from verification child environments. No real model was started, no credentials
were read, and no user old window was operated. The separately authorized handoff
notification is collaboration transport, not model integration evidence.

Scope remains homogeneous persistent tools, generation 1 and bounded foreground
processes. No Claude, TUI, background service, mixed tools or parser DSL expansion.
This workspace has no git metadata; changed files and retained test outputs are
the reviewable evidence.

## Handoff

Executed the required
`/Users/funlake/.codex/skills/okra-agent-init/scripts/agent-handoff.sh send`
with window 1, this session ID, status DONE and the summary/evidence/next fields.
The command exited 0 and confirmed:

```text
[DONE] handoff sent: id=5f860523-4072-4946-b339-eec101ba7063 window=1 status=DONE
```

This confirms transport delivery. Main-agent acceptance and independent review
remain separate from the worker's completed verification.

The main agent acknowledged this handoff ID with:
“主 agent 已收到修复交付，开始 2 号独立 review。”
