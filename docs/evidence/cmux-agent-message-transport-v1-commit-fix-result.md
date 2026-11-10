# cmux inbox — failed commit cleanup P2

Session: `cmux-agent-message-transport-v1-commit-fix-20261105-01`  
Date: 2026-11-05  
Role: 资深研发（写代码）  
Mode: ralph-loop

Status: **DONE** — production fix, fault regressions and every requested offline
acceptance check pass.

## Root cause and red check

`database/sql.Tx.Commit` marks the transaction done even when the SQLite driver
returns a commit error. SQLite can still have an active transaction, for example
after a deferred foreign key constraint rejects COMMIT. The deferred
`tx.Rollback()` then returns `sql.ErrTxDone`, leaving the physical connection in
the pool with its transaction still open.

Added `store.TestCMUXInboxFailedCommitDiscardsConnectionAndRecovers` before the
production change and ran it against the original implementation:

```text
go test -count=1 -v ./internal/store -run '^TestCMUXInboxFailedCommitDiscardsConnectionAndRecovers$' -timeout 30s
failed COMMIT connection was returned to the pool:
{MaxOpenConnections:1 OpenConnections:1 InUse:0 Idle:1 ...}
FAIL
```

The fault is an actual deferred SQLite foreign key violation produced by a
test-only journal trigger. Both new journal inserts succeed before COMMIT fails.
It is distinct from the existing insert-abort test.

## Change

`internal/store/cmux_transport.go` now acquires one `sql.Conn` for the entire
`AcceptCMUXInbox` operation. `BEGIN IMMEDIATE` preserves the store's existing
`_txlock=immediate` behavior. Every actor/binding/history/source check, journal
insert, COMMIT and cleanup uses that same connection.

Any unsuccessful transaction explicitly runs `ROLLBACK` with a fresh
`context.Background()` context and a two-second timeout. Caller cancellation
does not cancel this cleanup context. If COMMIT returns an error, or ROLLBACK
fails/times out, `Conn.Raw` returns `driver.ErrBadConn` to force physical disposal
before `Conn.Close` could return the connection to the pool. A failed BEGIN also
discards its connection. Rollback errors are joined with the original error so
`errors.Is`/`errors.As` can still inspect the original failure.

The five read-only helpers used by the inbox now accept the existing `queryRower`
interface in place of `*sql.Tx`: `surfaceBinding`, `validateMessageActor`,
`currentMessageSeat`, `recoverySeat`, and `bindingGate`. Their SQL and other call
sites' transaction ownership are preserved. The production schema, dependencies,
services and runtime/IPC APIs are unchanged.

## Regression coverage

| Test | Evidence |
| --- | --- |
| `store.TestCMUXInboxFailedCommitDiscardsConnectionAndRecovers` | Existing message/delivery and historical inbox A form a nonempty baseline. `[new-B,old-A,new-C]` reaches an actual deferred-FK COMMIT failure; the pool has zero open connections afterward. A replacement has no old TEMP marker, can BEGIN/ROLLBACK, and retains the configured PRAGMAs. Message/delivery/journal counts and the entire journal equal the pre-call baseline. Close/reopen preserves that baseline. Removing the fault admits B/C once and an exact replay preserves counts and original receipts. |
| `store.TestCMUXInboxCleanupFailuresDiscardPhysicalConnection` | Four real-SQLite driver-wrapper cases: failed COMMIT with successful rollback, failed COMMIT and failed rollback, failed COMMIT and rollback timeout, failed journal write and failed rollback. The fault cancels the caller. Explicit rollback uses the same physical connection, enters with a live independent context bounded by two seconds, and physical Close occurs before return. Both original and cleanup errors remain inspectable. A new connection can BEGIN and uncommitted journal/message/delivery counts equal baseline. |
| Existing `TestCMUXInboxHistoryConflictsRejectWholeBatch` | Historical conflicts, duplicate IDs, unknown sources, target/generation/recipient gates still reject the complete batch. |
| Existing `TestCMUXInboxMixedBatchWriteFailureRollsBack` and runtime/CLI cmux tests | Insert failure remains atomic; normal commits, history skipping, both mixed orders, raw receipt preservation and coordinator/store graceful reopen keep their established behavior. |

The wrapper's COMMIT faults occur before the underlying SQLite COMMIT. Its
rollback-error/timeout cases intentionally leave the transaction active until
physical close. The separate deferred-FK test exercises a real SQLite COMMIT
rejection through the production driver.

## Verification

Commands and results are recorded in:

- `.cache/cmux-agent-message-transport-v1-commit-fix-checks-verification.json`
- `.cache/cmux-agent-message-transport-v1-commit-fix-race-verification.json`

The runner records argv, exit code, duration, individual logs and before/after
SHA-256 for all changed Go files plus go.mod/go.sum. It uses Go 1.25.5,
darwin/arm64, local project caches, `GOTOOLCHAIN=local`, `GOPROXY=off`,
`GOSUMDB=off`, `GOFLAGS=-mod=readonly`. Normal checks use `CGO_ENABLED=0`;
race uses `CGO_ENABLED=1`. The real Codex integration opt-in is removed.

| Command | Result | Wall time / log suffix |
| --- | --- | --- |
| `go test -count=1 -v ./internal/runtime ./internal/store ./internal/coordinator ./internal/cli -run TestCMUX -timeout 120s` | PASS | 18.764s / `-focused.log` |
| `go test ./...` | PASS, including e2e/integration | 81.061s / `-test.log` |
| `go build ./...` | PASS | 0.969s / `-build.log` |
| `make check` | PASS: format, modules, vet, full tests, build | 85.392s / `-check.log` |
| `go mod tidy -diff` | PASS, no diff | 0.338s / `-tidy.log` |
| `CGO_ENABLED=1 go test -race -count=1 ./internal/runtime ./internal/coordinator ./internal/cli ./internal/store -timeout 300s` | PASS, all four packages | 194.355s / `-race.log` |
| `go test -count=1 -v ./internal/coordinator -run '^TestHeadless(Codex\|OpenCode\|Claude)IntegratedFixtures$' -timeout 180s` | PASS: Codex 5.45s, OpenCode 8.25s, Claude 8.80s | 24.049s / `-headless.log` |

All log suffixes use prefix
`.cache/cmux-agent-message-transport-v1-commit-fix`. Final race output:

```text
ok  agentflow/internal/runtime      191.277s
ok  agentflow/internal/coordinator   77.928s
ok  agentflow/internal/cli            9.349s
ok  agentflow/internal/store         17.137s
EXIT_CODE=0
```

Both verification manifests have `done=true`, `passed=true` and
`source_unchanged=true`. The production/test files and module files have the same
SHA-256 before and after all checks. The runner is retained at
`.cache/cmux-agent-message-transport-v1-commit-fix-verify.py`.

## Scope of the commit guarantee

The verified unchanged counts apply to the specified uncommitted fault cases:
SQLite deferred-FK rejection and injected failures before COMMIT executes. They
are not a universal promise that every commit error means no data became durable.
If a driver/storage error occurs after a commit was applied, an attempted rollback
cannot undo that commit; it may itself report that no transaction is active.
The error is returned and the physical connection is still discarded. Recovery
must inspect persisted history; an error alone is not authority to assume an
event is absent. Existing matching-ID idempotency and conflict rules still apply.

Tests use temporary databases, executable fake cmux/provider fixtures and local
IPC. Product validation does not operate real workspaces or request models. The
explicitly requested collaboration handoff is sent separately using the supplied
script. Crash/power-loss recovery and real provider hooks are not established by
these offline checks.

## Handoff

All required checks are complete. Formal delivery sends status DONE for logical
window 1 and this session using
`/Users/funlake/.codex/skills/okra-agent-init/scripts/agent-handoff.sh`, with this
report and verification manifests as evidence. Next: independent review of the
pinned connection and failed-commit cleanup.

Formal send completed with exit code 0:

```text
[DONE] handoff sent: id=d147ce44-2fdd-42b8-b3da-6d390c3aa1c3 window=1 status=DONE
```
