# cmux agent message transport v1 — inbox batch idempotency fix

Session: `cmux-agent-message-transport-v1-fix-20261105-01`  
Date: 2026-11-05  
Role: 资深研发（写代码）  
Mode: ralph-loop

Status: **DONE** — production fix and all requested offline acceptance checks pass.

## Root cause and reproduction

The native inbox read retains historical messages (`--limit 100`, without
mark-read). `AcceptCMUXInbox` previously rejected every ID already present in
`CMUX_INBOX_EVENT`, before its write loop. Once A was accepted, either `[A,B]`
or `[B,A]` rejected the whole batch and B could never enter the journal while
A remained in the native read window. Reopening preserved the same failure.

Before modifying production code, ran reviewer 2's original overlay:

```text
go test -overlay /var/folders/wv/x8nwj9tj7xz5dhqf5f2mt3hw0000gn/T/opencode/cmux-agent-message-transport-v1-review-20261105-01-overlay.json -count=1 -v ./internal/runtime -run '^TestCMUXReviewMixedHistoryAfterReopen$' -timeout 45s
CONFIRMED: initial [A] accepted; after store reopen both [A,B] and [B,A] reject CMUX_EVENT_DUPLICATE; journal IDs=[history-A], B never persisted
PASS (the reviewer probe asserts the defective behavior)
```

Then added `TestCMUXInboxMixedHistoryAfterReopen` to the normal runtime suite.
All four combinations (old-first/new-first × same store/reopened store) failed
on the original production implementation with `CMUX_EVENT_DUPLICATE`. This is
the regression's red check; the reviewer's original probe is preserved intact.

## Production change and contract

The production fix is confined to `internal/store/cmux_transport.go`, in the
existing `AcceptCMUXInbox` transaction:

- Read the persisted event and receiving agent/generation for a historical ID.
  Compare source name, source surface/workspace, target surface/workspace and body.
  Native UUID comparisons follow the existing case-insensitive addressing rule;
  source names and bodies compare exactly.
- Matching history is an idempotent skip. Changes to native state or raw metadata
  preserve the first journal receipt and create no additional event.
- Collect valid new IDs and append them only after every observation is checked.
  Mixed `[old,new]` and `[new,old]` batches therefore admit the same new messages.
- Historical content/identity conflicts keep `CMUX_EVENT_DUPLICATE`. Same-batch
  duplicate IDs and wrong targets keep `CMUX_EVENT_REJECTED`; unknown new sources
  keep `CMUX_SOURCE_UNKNOWN`. Every such failure rejects the entire batch.
- Existing actor, current generation, stop and binding gates run before history
  handling. New messages retain registered-source / successful outbound-echo gates.
  The write-error rollback case is covered by the injected insert-abort test below.
  The original deferred `sql.Tx.Rollback` did not clean up an open SQLite
  transaction after a failed `Commit`; the later [commit cleanup fix](cmux-agent-message-transport-v1-commit-fix-result.md)
  pins the connection, explicitly rolls back and discards it on commit failure.
  A commit error alone does not prove that no data became durable.

The response `messages` remains the full parsed native observation, including
matching history; consumers must not treat each returned observation as new work.
Root README, `how-to-use.md`, runtime README and coordinator README now describe
this contract and the preservation of original receipts.

## Regression evidence

| Test | What it verifies |
| --- | --- |
| `runtime.TestCMUXInboxMixedHistoryAfterReopen` | Independent A→[A,B] and A→[B,A], both before and after reopening; repeated mixed reads append B exactly once, preserve the entire original journal prefix and retain B's raw JSON; native queued→read metadata does not rewrite A |
| `runtime.TestCMUXInboxEventsExactSurfaceAndRejects` | Repeated identical history succeeds; same-batch duplicates, wrong targets, unknown sources and malformed JSON remain rejected; READY is not synthesized |
| `store.TestCMUXInboxHistoryConflictsRejectWholeBatch` | 22 reopened-store cases: changed body, source name/surface/workspace, target surface/workspace, receiving agent or generation, duplicate new/history IDs and unknown new source, in both orders; each rejected batch leaves the journal unchanged |
| `store.TestCMUXInboxMixedBatchWriteFailureRollsBack` | `[new-B,old-A,new-C]` reaches an injected SQLite abort at C after B's insert; journal is identical to baseline, then removing the fault allows B/C exactly once, in order, even after retry |
| `cli.TestCMUXTransportCLIIPCAndReopen` | Fake native send receipt and source-registered messages through real local Unix IPC; coordinator graceful reopen before both mixed orders; historical agentflow echo and native state updates are idempotent; conflicting history rejects an accompanying new ID; reopened DB contains exactly three accepted events; native send executes once |

## Verification

Environment: Go 1.25.5, darwin/arm64, `GOTOOLCHAIN=local`, `GOPROXY=off`,
`GOSUMDB=off`, existing project dependency caches and `GOFLAGS=-mod=readonly`.
Normal checks use `CGO_ENABLED=0`; the race check explicitly uses `CGO_ENABLED=1`.
Every command below completed with exit code 0 on the final code and tests.

| Check | Result | Log |
| --- | --- | --- |
| `go test -count=1 -v ./internal/runtime ./internal/store ./internal/coordinator ./internal/cli -run TestCMUX -timeout 120s` | PASS, 20.294s wall time | `.cache/cmux-agent-message-transport-v1-fix-focused.log` |
| `go test ./...` | PASS, including e2e/integration, 72.085s wall time | `.cache/cmux-agent-message-transport-v1-fix-test.log` |
| `go build ./...` | PASS | `.cache/cmux-agent-message-transport-v1-fix-build.log` |
| `make check` | PASS: formatting, dependency download/verification, vet, full test suite, CLI build; 66.295s wall time | `.cache/cmux-agent-message-transport-v1-fix-check.log` |
| `go mod tidy -diff` | PASS, no diff/output | `.cache/cmux-agent-message-transport-v1-fix-tidy.log` |
| `CGO_ENABLED=1 go test -race -count=1 ./internal/runtime ./internal/coordinator ./internal/cli ./internal/store -timeout 300s` | PASS, all four packages; 174.425s wall time | `.cache/cmux-agent-message-transport-v1-fix-race.log` |
| `go test -count=1 -v ./internal/coordinator -run '^TestHeadless(Codex\|OpenCode\|Claude)IntegratedFixtures$' -timeout 180s` | PASS: Codex 4.94s, OpenCode 7.66s, Claude 10.00s | `.cache/cmux-agent-message-transport-v1-fix-headless.log` |

Final race package output:

```text
ok  agentflow/internal/runtime      172.046s
ok  agentflow/internal/coordinator   72.335s
ok  agentflow/internal/cli           11.126s
ok  agentflow/internal/store         16.225s
EXIT_CODE=0
```

Machine-readable command arrays, exit codes, durations and log paths are saved in
`.cache/cmux-agent-message-transport-v1-fix-checks-verification.json` and
`.cache/cmux-agent-message-transport-v1-fix-race-verification.json`; both have
`done=true` and `passed=true`. The temporary evidence runner lives at
`.cache/cmux-agent-message-transport-v1-fix-verify.py`.

## Scope and limits

Product validation uses executable fake cmux/provider fixtures, temporary SQLite
databases and local IPC. No real user workspace is accessed or mutated for product
validation, and no model request is made. The explicitly requested collaboration
handoff uses its separate formal channel.

The bounded 100-message / 64 KiB capture limits still apply. This change enables
progress when a valid bounded inbox contains history and new messages; incremental
cursors, automatic drain, live hooks and provider execution remain UNVERIFIED.
Native queue/read metadata remains separate from READY, workflow completion and
explicit ACK. Existing tables, journal, schema and dependencies are reused.

## Handoff

Verification is complete. Formal delivery uses
`/Users/funlake/.codex/skills/okra-agent-init/scripts/agent-handoff.sh send`
with logical window 1, session `cmux-agent-message-transport-v1-fix-20261105-01`,
status DONE and this report as evidence. The next step is reviewer 2's independent
verification of the inbox fix.

Formal send completed successfully:

```text
[DONE] handoff sent: id=98de092d-4298-4ea9-9424-cde34b6d6d6c window=1 status=DONE
```
