# Claude headless parser P1/P2 fix

Session: `headless-claude-v1-fix-20261104-01`. Date: 2026-11-04.

Scope: the two findings in the independent review
`headless-claude-v1-review-20261104-01`. This delivery is a parser fix verified
with synthetic native streams and executable fixtures. Real Claude startup,
effective permissions, model execution and persistent native resume remain
**UNVERIFIED**. The prior implementation's PARTIAL/native-UNVERIFIED status and
the independent review's product stage gate are not replaced by fixture results.

## Change and protocol evidence

The review's overlay reproduced valid `permission_denied`, `api_retry`,
`compact_boundary` and foreground-subagent messages being rejected. The parser
previously selected init fields for every system message, then required every
system message to be the first init. It also rejected all subagent messages
before the main assistant could finish.

Production changes are confined to
[headless_claude.go](../../internal/runtime/headless_claude.go) and its protocol
helpers [headless_claude_events.go](../../internal/runtime/headless_claude_events.go):

- Select exact system fields by exact subtype before consuming metadata. Validate
  the session, required fields, numeric types/ranges and documented enumerations.
  Unknown subtype, cross-subtype fields, aliases and recursive duplicate keys
  still fail closed. Metadata cannot establish init, final text or completion.
- Ignore attributed child assistant/user payloads after checking their exact
  envelope, bound session, attribution and message role/content presence. Child
  text, tools, errors and aborts cannot replace or poison the main candidate.
  Payloads excluded from the answer are opaque; they are not decoded as workflow
  JSON. Main `/context` and `/usage` assistant reports are excluded as well.
- Accept only a main result: absent origin or exactly `{kind:"human"}`. Injected
  task/peer/automatic results cannot complete the stdin turn. When prompt UUID
  echoes exist, require one consistent prompt across main assistant/result; a
  result cannot drop a previously bound prompt UUID. Merged and interrupted turns
  are rejected. This uses native attribution fields, not a guessed message ID.
- Known error result subtypes and supported terminal failure reasons return
  `NativeSubmitError.Outcome=FAILED`; unknown reasons remain UNKNOWN. Result index
  checks apply to error results too. Successful completion still requires a main
  assistant, matching terminal text, positive turn count and `end_turn`.

Official documentation read during this fix:

- [Headless: API retries](https://code.claude.com/docs/en/headless#handle-api-retries):
  required retry counters, nullable HTTP status, error category and optional
  `no_response` object. Persistent retry attempts may exceed the retry limit.
- [SDKPermissionDeniedMessage](https://code.claude.com/docs/en/agent-sdk/typescript#sdkpermissiondeniedmessage):
  denial under `permissionPrompts=none` is a nonterminal event. The final result's
  `permission_denials` is authoritative; a denial event does not itself mean the
  whole workflow is blocked or failed.
- [SDKCompactBoundaryMessage](https://code.claude.com/docs/en/agent-sdk/typescript#sdkcompactboundarymessage)
  and [SDKStatusMessage](https://code.claude.com/docs/en/agent-sdk/typescript#sdkstatusmessage).
- [Subagent stream attribution](https://code.claude.com/docs/en/headless#follow-subagent-messages),
  [SDKAssistantMessage](https://code.claude.com/docs/en/agent-sdk/typescript#sdkassistantmessage),
  [SDKTaskStartedMessage](https://code.claude.com/docs/en/agent-sdk/typescript#sdktaskstartedmessage),
  [SDKTaskProgressMessage](https://code.claude.com/docs/en/agent-sdk/typescript#sdktaskprogressmessage),
  [SDKToolProgressMessage](https://code.claude.com/docs/en/agent-sdk/typescript#sdktoolprogressmessage).
- [SDKResultMessage](https://code.claude.com/docs/en/agent-sdk/typescript#sdkresultmessage):
  result origin, prompt echo, terminal reasons and delivery index.

`native.go` argv stays the existing documented print/bare/stdin/stream-json
command. Foreground subagent traffic is handled by attribution; this fix adds no
native Agent launch, delegation option or background task feature. All native
execution in verification uses the inspected Go fixture, never an installed CLI.

## Supported events and outcomes

| Event | Supported schema / effect |
| --- | --- |
| `system/init` | Exactly one init, establishes bound UUID session; existing metadata fields retained. |
| `system/permission_denied` | `tool_name`, `tool_use_id`, `message`; optional `agent_id`, `decision_reason_type`, `decision_reason`; metadata only. |
| `system/api_retry` | Integer `attempt>=1`, `max_retries>=0`, `retry_delay_ms>=0`; required null or HTTP `error_status`; documented error category; optional exact `no_response={waited_ms,retry_wait_ms}` with nonnegative integers and null status. |
| `system/compact_boundary` | Exact `compact_metadata={trigger:manual\|auto,pre_tokens:nonnegative integer}`. |
| `system/status` | `status=compacting\|null`; optional supported permission mode. |
| `system/task_started` | Exact documented fields, local foreground agent only (`task_type=local_agent`); supplied background/ambient flags must be false, supplied spawn depth positive. |
| `system/task_progress` | Task ID/description and exact nonnegative token/tool/duration usage; documented optional progress fields; metadata only. |
| `tool_progress` | Tool identity, nullable parent attribution, nonnegative elapsed seconds; optional task/type/heartbeat and typed retry metadata; no completion effect. |
| `rate_limit_event` | Existing `allowed`, `allowed_warning`, `rejected` metadata; no completion effect. |
| `assistant`, `user` | Only main assistant text can supply the final candidate; attributed child/user/internal report text cannot. |
| `result/success` | Main text match plus existing completion checks. Final structured `blocked` maps through the shared workflow decoder to BLOCKED_CONFIRM/PAUSED; `retry` maps to FAILED and consumes the existing attempt budget. |
| Error result subtypes | `error_during_execution`, `error_max_turns`, `error_max_budget_usd`, `error_max_structured_output_retries`, with `is_error=true`: native FAILED. |

Supported terminal reasons are `completed` and these failure reasons:
`max_turns`, `aborted_streaming`, `aborted_tools`, `hook_stopped`,
`stop_hook_prevented`, `blocking_limit`, `rapid_refill_breaker`, `prompt_too_long`,
`image_error`, `model_error`, `api_error`, `malformed_tool_use_exhausted`,
`budget_exhausted`, `structured_output_retry_exhausted`,
`tool_deferred_unavailable`, `turn_setup_failed`. A supported terminal failure
does not deliver assistant workflow text as a success. The existing delivery
gate remains conservative: native FAILED does not authorize replay and may
leave SUBMIT_REQUESTED unresolved. It is distinct from a successfully delivered
structured workflow `retry`.

No denial event alone manufactures a blocked/retry result. A recovered retry
followed by a valid final main result completes normally. Permission denial
followed by a valid main blocked JSON pauses until an explicit lead decision and
resume; permissions are never changed automatically.

## Refused and unknown boundaries

- Any unlisted event/subtype or unknown field is UNKNOWN. This includes documented
  protocols outside this adapter's scope: hook/plugin/auth events,
  `informational`, `task_notification`, `task_updated`, background-task changes,
  remote tasks, partial `stream_event`, command output and tool-use summaries.
- `tool_deferred` and `background_requested` terminal reasons remain UNKNOWN;
  they require continuation semantics this adapter does not implement.
- A background/ambient/remote `task_started`, merged prompts, interrupted-turn
  `resume_reason`, nonhuman/injected result origin, missing init or result,
  changed session, unexpected result index, incomplete/malformed output, and
  traffic after the terminal result remain rejected. A matching child or
  internal report without a verified main assistant cannot establish READY.
- Text stdin has no caller-supplied prompt UUID. Session/process ownership plus
  native main-message attribution and a single matching terminal result are the
  binding when UUID echoes are absent. Optional echoes are consistency checks,
  not independent authentication of an externally supplied turn.
- Exact keys and duplicate detection cover opaque nested metadata, but this is
  not a full validator for every usage/model/tool payload. Opaque data supplies
  no final text, session identity, success or workflow event.
- Native permissions, authenticated behavior and real durable resume were not
  exercised. No TUI/service/reset/fresh/permission recovery was added.

## Fixtures and verification

New [schema/isolation tests](../../internal/runtime/headless_claude_events_test.go)
cover compatibility, before/after-main placement, missing completion, wrong
session, subtype aliases/unknowns/duplicates, cross-schema field injection,
malformed counters/nullable status/compaction fields, child-only forged final,
late child error/abort, internal report exclusion, injected result origins,
prompt UUID switches and known/unknown terminal classification.

The [shared executable fixture](../../internal/runtime/testdata/headless-codex/main.go)
and [coordinator matrix](../../internal/coordinator/headless_test.go) cover
permission-denied→blocked→reopen/drain without replay→explicit resume and resume
idempotency, denial followed by structured retry exhaustion, recovered API retry,
retry ending in native failure, compaction and interleaved child/internal traffic.
The [Claude CLI e2e blocked/resume case](../../tests/e2e/headless_codex_test.go)
now includes the native permission denial before the blocked result.

Command argv, exit codes, durations and source fingerprints are recorded in
[verification.json](headless-claude-v1-fix-verification.json). Platform:
Go 1.25.5, darwin/arm64. Real-native opt-ins `AGENTFLOW_REAL_CODEX` and
`AGENTFLOW_REAL_EVIDENCE_DIR` are removed from verification subprocesses.

| Check | Result | Evidence |
| --- | --- | --- |
| Independent review overlay, `TestReviewClaude`, `-count=1` | PASS, exit 0 | [overlay log](headless-claude-v1-fix-overlay.log) |
| Claude/Codex/OpenCode/READY/CLI focused regression, `-count=1` | PASS, exit 0 | [focused log](headless-claude-v1-fix-focused.log) |
| `go test ./...` | PASS, exit 0 | [test log](headless-claude-v1-fix-test.log) |
| `go build ./...` | PASS, exit 0 | [build log](headless-claude-v1-fix-build.log) |
| `make check` | PASS, exit 0; formatting/modules/vet/tests/build | [check log](headless-claude-v1-fix-check.log) |
| `go mod tidy -diff` | PASS, exit 0; no output/diff | [tidy log](headless-claude-v1-fix-tidy.log) |
| Related full package race | PASS, exit 0; all seven packages | [race log](headless-claude-v1-fix-race.log) |

The full race command is:

```text
CGO_ENABLED=1 go test -race ./internal/runtime ./internal/coordinator ./internal/cli ./internal/store ./internal/loopcore ./internal/lock ./tests/e2e
```

Race completed normally in 116.984 seconds. Runtime took 115.118s, coordinator
51.778s, CLI 3.907s, store 16.115s, lock 3.670s and e2e 34.533s; loopcore reused
its passing race cache. No timeout or interrupted run is counted as passing.
Verification recorded `source_unchanged=true` and `changed_sources=[]` across
the complete sequence, including Go source, fixtures, Makefile and module files.

Codex and OpenCode shared regression and CLI e2e tests are included in focused,
full-test/check and race commands. No installed Claude/Codex/OpenCode was
started, no login or credential-file access occurred, and no product/user window
was operated. The explicitly requested handoff uses only the collaboration
transport. This workspace has no git metadata; no commit or push was performed.

## Handoff

All required acceptance checks passed. Executed
`/Users/funlake/.codex/skills/okra-agent-init/scripts/agent-handoff.sh send`
with window 1, session `headless-claude-v1-fix-20261104-01`, status DONE and the
summary/evidence/next fields. The command exited 0 and confirmed:

```text
[DONE] handoff sent: id=4addbfd6-6bde-4ace-bcb6-97a4656c5cf8 window=1 status=DONE
```

This confirms collaboration delivery. Independent review and the main agent's
acceptance remain separate from these fixture verification results.

The main agent acknowledged this handoff ID: “主 agent 已收到 Claude P1/P2
修复，开始 2 号最终独立 review。”
