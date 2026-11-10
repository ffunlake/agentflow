# cmux agent message transport v1 — result

Session: `cmux-agent-message-transport-v1-20261104-01`  
Date: 2026-11-05  
Role: 资深研发（写代码）  
Mode: ralph-loop

Implemented the minimal explicit cmux transport on the existing workspace launcher,
store and IPC coordinator. Product capability remains PARTIAL; native readiness,
actual hook consumption and complete automatic collaboration remain UNVERIFIED.

## Delivered

- `internal/coordinator/up_cmux.go`: creates the migrated existing-schema DB and
  registers every logical agent as UNVERIFIED before launching. After scoped tree
  verification, saves LAUNCHED workspace/surface UUIDs and refs. Each record includes
  run/agent/generation/tool and a launch attempt ID also saved in `up.requested.json`.
  Interrupted/failed launches retain their identity without synthesizing native
  sessions or READY. Existing immutable snapshot remains STARTING.
- `internal/store/cmux_transport.go`: surface registration and send/event evidence
  use existing seats, messages, deliveries and append-only journal. No new schema,
  migration version, dependency or parallel identity database. Exact surface binding,
  actor/run/agent/generation, stop and binding recovery gates are rechecked in the
  transaction. Shared sender/request namespace retains cross-operation conflicts.
- `internal/runtime/cmux_transport.go`: `NewCMUXTransport`, explicit `Send` and
  `Inbox`. Send uses `exec.CommandContext` with individual argv words:
  `agent message <surface-UUID> --from agentflow --json -- <text>`.
  It never uses a shell, workspace titles, terminal send/paste or screen parsing.
  Stdout/stderr and exit are retained separately, bounded at 64 KiB each; timeout is
  at most eight seconds. Body is UTF-8, nonempty and at most 32 KiB; unsupported
  control bytes and the special stdin sentinel `-` are rejected before sending.
- Durable `CMUX_SEND_REQUESTED` claim prevents duplicate native actions, including
  concurrent request retries, coordinator/store reopen, timeout or missing result.
  `CMUX_SEND_RESULT` stores native ID/source/target/body/state/raw JSON and process
  evidence. Only exact matching receipt yields `CMUX_QUEUED`. It means native queue
  acceptance, with agent receipt/execution/READY unverified. Failures, malformed or
  truncated output and wrong targets remain UNVERIFIED without automatic replay.
  Stop/generation/binding changes after the claim retain raw evidence but reject
  success confirmation. Generic ACK/reply cannot overwrite an unknown cmux send.
- Inbox executes only `agent inbox --surface <bound-UUID> --limit 100 --json`.
  The explicit `AcceptCMUXInbox` event entry atomically rejects unknown sources,
  incorrect targets and duplicate message IDs within a batch or across accepted
  batches. A source must match a current registered surface/workspace, or be an
  agentflow echo matching the exact known successful outbound receipt. Accepted
  events retain native ID, source/target, body and raw JSON in `CMUX_INBOX_EVENT`.
  No mark-read, automatic ACK or interpretation of READY/DONE text occurs.
- `internal/coordinator/cmux_transport.go`: `OpenCMUXTransport` and explicit IPC
  `cmux-send` / `cmux-inbox`, reusing the existing private run lock, strict parameter
  checks and per-request actor validator. No cmux WorkflowRunner/SubmitAdapter is
  installed; workflow start/drain does not silently send native messages.
- CLI: `coordinator --cmux-transport [--cmux-binary PATH]`, `cmux-send`, `cmux-inbox`
  and status JSON/text surface registration output. Headless flags are mutually
  exclusive with cmux transport. README, how-to-use, runtime and coordinator contracts
  document the explicit APIs, queue semantics and limits.

## Runtime evidence and fake coverage

Only installed cmux help was consulted. These read-only help commands succeeded:

```text
cmux agent message --help
Usage: cmux agent message <target> [--from <name>] [--thread <id>] [--json] [--] <text|->
cmux agent inbox --help
Usage: cmux agent inbox [--surface <target>] [--state queued|delivered|read|failed] [--limit <n>] [--mark-read] [--json]
cmux agent messages --help
Usage: cmux agent messages [on|off|status] [<target>] [--workspace] [--json]
```

Payload field names were cross-checked against upstream `CLI/CMUXCLI+AgentMessages.swift`,
`Sources/TerminalController+AgentMessages.swift` and `Sources/AgentMessageCenter.swift`
as read on 2026-11-05. This supports the fixture/parser contract and is not a live
acceptance result for any user's cmux surfaces or installed tool hook integration.

Executable fixture: `internal/runtime/testdata/cmux/main.go`. Coverage includes:

| Boundary | Evidence |
| --- | --- |
| Layout launch → persistent registration → status | CLI executable up fixtures, all three tools; reopened state.db and status preserve exact visual leaf mapping, refs and launch IDs, empty native session and LAUNCHED/UNVERIFIED distinction |
| Exact target and literal text | UUID target, `--from agentflow`, `--json --`, quotes, leading `--help`, shell syntax, newline/tab and Unicode checked as exact argv |
| Process failure and uncertainty | exit 7 preserves stdout/stderr; malformed JSON, wrong surface, oversized output and cancellation preserve UNVERIFIED evidence and never resend |
| Request deduplication | eight concurrent send calls, reopened DB/service and coordinator reopen produce a single native send; changed body/recipient and persistence-only request collision rejected |
| Identity and lifecycle | wrong run/agent, old/future generation, wrong target generation, unbound/unknown launch, stop and canceled context reject before execution |
| Late outcomes | sender/target generation switch, stop and surface change reject confirmation while persisting raw native evidence |
| Inbox events | exact read-only `--surface`, valid registered source and known outbound echo, empty inbox, unknown source, unknown agentflow ID, duplicate IDs, wrong target, malformed/duplicate JSON |
| ACK integrity | pending native intent cannot be ACKed or replied DONE; explicit receipt is accepted after queue confirmation and cannot mark a LAUNCHED seat READY |
| IPC/CLI | local Unix IPC send/inbox/stop, coordinator graceful reopen, request replay, unknown actors and stop fences; native target override/null/duplicate parameter rejection |
| Headless regression | full runtime/coordinator/CLI suite retains Codex, OpenCode and Claude fixtures, including all three `TestHeadless*IntegratedFixtures` tests |

Key tests are in `internal/runtime/cmux_transport_test.go`,
`internal/store/cmux_transport_test.go`, `internal/coordinator/cmux_transport_test.go`,
`internal/cli/cmux_transport_test.go` and the updated `internal/cli/up_cmux_test.go`.

## Verification

- `go test ./...` — PASS, including tests/e2e and tests/integration.
- `go build ./...` — PASS.
- `make check` — PASS; final log `.cache/cmux-agent-message-transport-v1-check.log`
  includes `CHECK_EXIT=0`, dependency verification, vet, full test suite and CLI build.
- `go mod tidy -diff` — PASS, no output/diff.
- Full race: `CGO_ENABLED=1 go test -race ./internal/runtime ./internal/coordinator ./internal/cli ./internal/store`
  — PASS on final code/tests (`.cache/cmux-agent-message-transport-v1-race-final.log`,
  `RACE_EXIT=0`): runtime 123.872s, coordinator 43.113s, cli 8.017s, store 11.207s.
- `make fmt-check` — PASS after the final edits.

The first foreground race attempt exceeded the shell's 120-second limit without
reporting a test result. The subsequent complete run passed. A later targeted race
run exposed a fixture deadline that could cancel before the instrumented child
wrote its invocation log; it was changed from 100ms to 2s against a five-second
fixture delay. The final full race run above passed with that correction.

## Scope and limits

No real user workspace was created, messaged, read or mutated for product validation;
no real model request, provider TUI screen parsing, ownership guard, background
scheduler, fresh/reset or fabricated native READY was implemented. The explicitly
requested collaboration handoff is separate from product validation.

Native event acceptance is an explicit bounded batch entry: newest 100 messages,
64 KiB captured stdout limit, no incremental cursor or automatic drain. A repeated
accepted ID rejects the batch, including later native delivery-state updates.
Unknown outcomes need evidence review; changing request IDs is not safe recovery.
The existing private local IPC trust boundary remains: a claimed actor is validated
against persistent state, without authenticating a same-user process as a provider.
No full automatic cmux workflow or live hook support for the three providers is
claimed by these offline fixtures.

## Handoff

All requested verification is complete. Formal delivery uses the requested
`/Users/funlake/.codex/skills/okra-agent-init/scripts/agent-handoff.sh send`
for logical window 1, session `cmux-agent-message-transport-v1-20261104-01`,
status DONE, this report as evidence and explicit workflow integration/live hook
validation as follow-up. The transport receipt is recorded after sending.

Formal send completed successfully:

```text
[DONE] handoff sent: id=ff2918a3-c1db-4d58-a0a7-126191986bc2 window=1 status=DONE
```
