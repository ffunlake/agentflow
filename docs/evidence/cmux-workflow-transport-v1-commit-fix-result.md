# CMUX workflow transport — COMMIT failure connection cleanup P1

Session: `cmux-workflow-transport-v1-commit-fix-20261105-01`  
Date: 2026-11-05  
Role: 资深研发（写代码）  
Mode: ralph-loop

Status: **DONE** — production fix, fault regressions and every requested offline
acceptance check pass. Formal handoff was sent successfully; receipt is below.

## Root cause and red regression

`database/sql.Tx.Commit` marks the transaction done even if the SQLite driver
rejects COMMIT. A deferred `Tx.Rollback` then returns `sql.ErrTxDone`, while the
physical SQLite connection can retain the active transaction. Returning that
connection to the pool exposes uncommitted workflow journal/cursor, reply,
delivery and seat updates to later calls, and the next BEGIN can fail.

Before changing production code, the new regression ran:

```text
go test -count=1 -v ./internal/store -run '^TestWorkflowFailedCommitDiscardsConnectionAndRecovers$' -timeout 30s
```

All seven subcases failed with the same observed condition:

```text
failed COMMIT connection returned to pool:
{MaxOpenConnections:1 OpenConnections:1 InUse:0 Idle:1 ...}
FAIL agentflow/internal/store
```

Subcases: `start`, `event`, `reply`, `cmux-reply`, `claim`, `result`,
`unknown-result`. The fault is an actual deferred SQLite foreign key violation
created by a test journal trigger; writes succeed before the underlying COMMIT
rejects the transaction. This reproduction uses the production SQLite driver.

## Fix

`internal/store/transaction.go` supplies `beginImmediate`, an owned SQL
transaction on a single pinned `sql.Conn`, and deferred `finish` cleanup:

- `BEGIN IMMEDIATE`, every transactional query/write and COMMIT use that same
  connection, preserving the existing immediate-lock transaction behavior.
- Every unsuccessful transaction attempts `ROLLBACK` using an independent
  `context.Background()` context bounded by two seconds. Caller cancellation
  cannot cancel that cleanup context.
- Any COMMIT error or ROLLBACK failure/timeout discards the physical connection
  through `Conn.Raw` returning `driver.ErrBadConn`. `Conn.Close` then cannot
  return the uncertain connection to the pool. A failed BEGIN also discards its
  connection.
- Cleanup errors join the original failure, preserving `errors.Is` inspection.

The helper owns `advanceWorkflowBound` (event/reply, including session and cmux
variants and historical receipts), `BeginCMUXDispatch` (new and already claimed
outboxes), and `FinishCMUXSend` (the shared cmux send-result transaction used by
workflow dispatch).

The required internal data helpers accept narrow query/write interfaces so both
`sql.Tx` and the pinned connection can use the existing SQL and identity gates:
`storeMessage`, `receivedMessage`, `findAckRequest`, `saveOutbox`,
`sessionRecord`, `confirmedSessionSubmission`, `workflowDispatch`, and
`validateCMUXWorkflow`.

## Regression coverage

Tests are in `internal/store/workflow_commit_test.go`. Fault wrappers in
`cmux_inbox_commit_test.go` execute real SQLite except at a selected boundary;
they now also support reporting an error after actually applying COMMIT.

| Test | Evidence |
| --- | --- |
| `TestWorkflowFailedCommitDiscardsConnectionAndRecovers` | Seven actual deferred-FK COMMIT rejections. The pool has zero open connections before any subsequent access. The same Store's replacement connection can BEGIN/ROLLBACK. Full snapshots of every column in runs/seats/messages/deliveries/journal and the public workflow cursor equal the committed baseline, including BUSY seat, reply, cursor, outbox, send attempt and result phase. Reopening retains the baseline. Removing the fault allows the original request to succeed, workflow revision advances exactly once, replay changes no durable rows, and reopening again preserves the recovered revision/state. |
| `TestWorkflowCommitCleanupFaults` | 47 boundary cases: seven operations across COMMIT error, canceled COMMIT, canceled caller plus failed ROLLBACK, canceled caller plus ROLLBACK timeout, and journal-write plus ROLLBACK failure; three historical-receipt paths (`start-replay`, `cmux-reply-replay`, `claim-replay`) additionally exercise the four COMMIT modes. The wrapper measures same physical connection, live cleanup context and its two-second deadline, and physical close before return. Original/cleanup errors remain inspectable. The next BEGIN and original request succeed on the same Store; pre-COMMIT faults leave the baseline and recovered replay writes no duplicates. |
| `TestWorkflowAppliedCommitErrorPreservesDurableReceipts` | Seven cases execute the underlying SQLite COMMIT successfully, then report a driver error. Cleanup still runs and the physical connection is discarded. Public cursor/revision reflects the durable transaction; close/reopen retains it. Workflow requests return their original receipt on replay. Durable send claims cannot be reclaimed; send results cannot be recorded twice; unknown results also forbid a native resend. |
| Existing workflow/cmux/headless suites | Normal commits, session workflows, exact receipt retries and conflicts, stop/current-generation/binding gates, frozen configuration, unknown-send non-replay, strict native replies and ordinary workflow behavior remain covered by the requested full and race runs. |

## Meaning of a COMMIT error

Unchanged snapshots are established specifically for deferred-FK rejection and
wrapper faults injected **before** the underlying COMMIT. They do not establish
that every COMMIT error means no transaction became durable.

A driver/storage failure can report an error after applying the transaction.
An attempted ROLLBACK cannot undo an already applied commit and may itself report
that no transaction is active. The error is returned and the physical connection
is discarded in either case. Recovery must consult durable cursor, journal,
claim and result receipts, using the existing idempotency/conflict and non-replay
rules. The post-apply regression makes that distinction executable.

## Verification

Runner: `.cache/cmux-workflow-transport-v1-commit-fix-verify.py`.

The runner records exact argv, individual logs, exit codes, wall times and SHA-256
of all nine changed Go files plus `go.mod`/`go.sum` before/after checks in:

- `.cache/cmux-workflow-transport-v1-commit-fix-checks-verification.json`
- `.cache/cmux-workflow-transport-v1-commit-fix-race-verification.json`

It uses Go 1.25.5 on darwin/arm64, local project caches, `GOTOOLCHAIN=local`, `GOPROXY=off`, `GOSUMDB=off`,
and `GOFLAGS=-mod=readonly`. Normal checks use `CGO_ENABLED=0`; race uses
`CGO_ENABLED=1`. Real-provider integration opt-ins and external evidence output
overrides are removed from the check environment.

| Command | Result | Wall time / log suffix |
| --- | --- | --- |
| `go test -count=1 -v ./internal/runtime ./internal/store ./internal/coordinator ./internal/cli ./internal/loopcore -run 'TestCMUX\|TestWorkflow' -timeout 180s` | PASS | 30.626s / `-focused.log` |
| `go test ./...` | PASS, including e2e/integration | 77.390s / `-test.log` |
| `go build ./...` | PASS | 1.074s / `-build.log` |
| `make check` | PASS: format, module verification, vet, full tests and build | 78.077s / `-check.log` |
| `go mod tidy -diff` | PASS, no diff | 0.599s / `-tidy.log` |
| `go test -count=1 -v ./internal/coordinator -run '^TestHeadless(Codex\|OpenCode\|Claude)IntegratedFixtures$' -timeout 180s` | PASS: Codex 8.32s, OpenCode 7.76s, Claude 8.43s | 26.498s / `-headless.log` |
| `CGO_ENABLED=1 go test -race -count=1 ./internal/runtime ./internal/coordinator ./internal/cli ./internal/store ./internal/loopcore -timeout 300s` | PASS, all five packages | 245.157s / `-race.log` |

All log suffixes use prefix `.cache/cmux-workflow-transport-v1-commit-fix`.

Both verification manifests have `done=true`, `passed=true` and
`source_unchanged=true`. Every recorded command exits 0, and all nine Go files and
both module files have identical SHA-256 before and after verification. Final
race output:

```text
ok  agentflow/internal/runtime      241.594s
ok  agentflow/internal/coordinator   98.468s
ok  agentflow/internal/cli           16.800s
ok  agentflow/internal/store         55.565s
ok  agentflow/internal/loopcore       6.271s
EXIT_CODE=0
```

All product verification uses temporary databases, local IPC and executable
cmux/provider fixtures. It does not operate real user workspaces or request
models. The explicitly required collaboration handoff uses the supplied script
as a separate delivery operation. These checks do not establish real provider
hooks, model execution or power-loss recovery.

## Handoff

All required acceptance checks are complete. Formal delivery sends DONE to
window 1 using the exact session above and
`/Users/funlake/.codex/skills/okra-agent-init/scripts/agent-handoff.sh`, with this
report and both verification manifests as evidence. Next: independent review of
the pinned transaction cleanup and durable-receipt recovery boundary.

Formal send completed with exit code 0:

```text
[DONE] handoff sent: id=bfcf18ef-4f6a-4c30-8362-271fcb738343 window=1 status=DONE
```

Collaboration protocol: [okra-agent-init SKILL.md](/Users/funlake/.claude/skills/okra-agent-init/SKILL.md).

Main agent ACK received for the same handoff ID:
“主 agent 已收到最后 transport P1 修复；按当前收敛方向冻结范围，不再派发新的架构扩展。”
ACK confirms receipt. Scope is frozen at this P1 fix; independent acceptance is
not inferred from the ACK.
