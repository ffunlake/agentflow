# Headless Codex v1 gate and drain fixes — 2026-11-04

Session: `headless-codex-v1-fix-20261104-01`.

## Scope and root causes

This round addresses the two findings from worker 2's read-only review. It uses
offline fixtures, temporary SQLite databases and local IPC. No real Codex process
was started and no existing user window was inspected or operated for testing.
The only collaboration action is the explicitly requested final worker handoff.

P1: ACK previously replaced any delivery phase with `ACKED`, including a committed
`SUBMIT_REQUESTED` whose native result was unknown. The foreground collector
accepted `ACKED` as submission evidence, and the seat was only marked BUSY after
native submission returned. Together these allowed an ACK to bypass missing SENT
evidence and potentially admit a second assignment on the same native binding.

P2: drain read its cursor at the start of each iteration. When the 128th result
committed a terminal or paused cursor, the loop returned the preceding cursor and
the action-limit diagnostic without observing the committed state.

## Production changes

- `internal/store/session.go`: the submit claim now atomically records
  `SUBMIT_REQUESTED`, attempt and BUSY/current-message seat reservation before the
  adapter runs. Unknown results retain that reservation. Submission confirmation
  cannot overwrite another assignment's seat ownership. A derived, uncached
  `SessionWork.SubmissionConfirmed` checks the delivery's SENT/ACKED phase, positive
  attempt, verified channel and matching run/message/recipient/generation
  `MESSAGE_SENT` journal evidence, plus the current seat generation.
- `internal/store/messaging.go`: once a native submit has started, ACK and result
  writes require that matching durable SENT confirmation. Rejection returns
  `SESSION_NOT_SENT` without replacing the intent or persisting a terminal reply.
  ACK remains receipt-only; the matching terminal reply releases the seat. The
  existing persistence-only message API still records receipts when no native
  action has begun, and these receipts cannot satisfy the native drain gate.
- `internal/store/session.go` and `internal/store/reconcile.go`: historical ACKED
  rows with unresolved native intents remain unknown and block later assignments
  even if an older process left the seat READY. Matching generation and recipient
  evidence is required. Existing stop, generation, reset READY and recovery gates
  remain in the transaction path.
- `internal/runtime/workflow.go`: collection requires the derived durable SENT
  confirmation independently of ACK. After every successful result/event commit,
  drain rereads the latest database cursor and returns immediately for COMPLETED,
  PAUSED, EXHAUSTED or no dispatch. Only unfinished work receives the 128-action
  diagnostic, with its latest cursor available for the next explicit drain.
- Root README links this report and describes reservation at submit claim time.

No schema migration, dependency, scheduler, cache or new runtime mode was added.

## Focused regressions

The new runtime regressions were first run against the original production code.
They reproduced both findings: ACK without SENT was accepted and drain advanced;
all four 128-action cases returned the old cursor and the action-limit error.
They pass with the production fixes.

- `internal/runtime/session_ack_test.go`,
  `TestSessionRunningAckCannotReleaseSecondAssignment`: ACK inside the first native
  Submit callback is rejected; another coordinator using a separate Store handle
  queues the second assign with attempt 0. Both successful and unknown first
  submissions are covered. Post-SENT ACK still does not release the first seat;
  a matching terminal reply permits the second submit. Unknown results reject
  both ACK and terminal reply and retain BUSY/current-message ownership.
- `internal/runtime/workflow_drain_test.go`,
  `TestWorkflowDrainMissingSentCannotBeReplacedByAck`: requested/unknown submission
  plus ACK cannot invoke the collector, advance the cursor or replay native work.
- `internal/store/session_ack_test.go`,
  `TestSessionHistoricalAckCannotReplaceMatchingSent`: historical ACKED delivery
  with missing, wrong-generation or wrong-recipient SENT journal evidence cannot
  accept a new ACK/result, reconcile to confirmed completion or claim a second
  native action, even with a READY seat and plausible channel.
- `TestWorkflowDrainActionLimitReturnsLatestCursor`: exactly 128 submissions and
  result commits return revision 129 from the database. COMPLETED, PAUSED and
  EXHAUSTED return without a diagnostic or further native work on terminal drain.
  A 65-step chain instead returns the current revision and limit diagnostic, then
  completes after two remaining dispatches on the next explicit drain.
- The existing executable fixture's `restart/submit-requested` case now also
  rejects ACK via real IPC and queues a second assign, with the invocation log
  remaining at the two startup calls. Its original missing-SENT drain assertion
  continues to hold after that attempted ACK.

## Verification

Commands that can include the opt-in native integration explicitly unset
`AGENTFLOW_REAL_CODEX` and `AGENTFLOW_REAL_EVIDENCE_DIR` for this round.

| Check | Result |
| --- | --- |
| Focused runtime/store regressions, `-count=1` | PASS |
| Original 17 `TestHeadlessCodexIntegratedFixtures` cases, `-count=1` | PASS |
| `TestHeadlessCodexCLIWorkflowAndRestart`, `-count=1` | PASS |
| `make check`: formatting, modules, vet, all packages, CLI build | PASS |
| `go build ./...` | PASS |
| `go mod tidy -diff` | PASS, no diff |
| Core race: runtime/store/coordinator/context/ipc/lock/loopcore, `-count=1` | PASS, all seven packages |

The full `make check` includes workflow/loopcore, coordinator, runtime, store, CLI,
integration and e2e suites. It exited 0. Focused reproduction commands:

```sh
env -u AGENTFLOW_REAL_CODEX -u AGENTFLOW_REAL_EVIDENCE_DIR \
  go test -v ./internal/runtime ./internal/store \
  -run 'TestWorkflowDrain|TestSessionRunningAck|TestSessionHistoricalAck' -count=1
env -u AGENTFLOW_REAL_CODEX -u AGENTFLOW_REAL_EVIDENCE_DIR \
  go test -v ./internal/coordinator -run '^TestHeadlessCodexIntegratedFixtures$' -count=1
env -u AGENTFLOW_REAL_CODEX -u AGENTFLOW_REAL_EVIDENCE_DIR \
  go test -v ./tests/e2e -run '^TestHeadlessCodexCLIWorkflowAndRestart$' -count=1
env -u AGENTFLOW_REAL_CODEX -u AGENTFLOW_REAL_EVIDENCE_DIR make check
go build ./...
go mod tidy -diff
env -u AGENTFLOW_REAL_CODEX -u AGENTFLOW_REAL_EVIDENCE_DIR CGO_ENABLED=1 \
  go test -race ./internal/runtime ./internal/store ./internal/coordinator \
  ./internal/context ./internal/ipc ./internal/lock ./internal/loopcore -count=1
```

The first race command exceeded the tool's 60-second execution limit without
producing a test result and was terminated. The complete retry uses `-json` and
records its output at `.cache/headless-v1-fix-race.jsonl`; that retry determines
the race result above.

The retry finished with seven package-level `pass` records and no failing tests
or race reports: runtime 115.239s, store 23.657s, coordinator 29.536s, context
27.975s, IPC 4.719s, lock 4.490s and loopcore 7.061s. Environment:
`go1.25.5 darwin/arm64`; the race run enables CGO, while `make check` retains the
repository's CGO-disabled product build configuration.

This is a local correctness fix. The archived real-native evidence from the
original v1 delivery remains historical evidence; this round adds no new native
acceptance claim. Unknown native outcomes still require manual reconciliation.
There is no git metadata in this workspace, so no commit or git diff is available.

## Handoff

The required `/Users/funlake/.codex/skills/okra-agent-init/scripts/agent-handoff.sh
send` completed for window 1, session `headless-codex-v1-fix-20261104-01`, status
`DONE`, with the fixes, verification evidence and request for worker 2's read-only
recheck. Transport confirmation:

```text
[DONE] handoff sent: id=8a15d51a-6089-42bd-8a6b-521e66886b90 window=1 status=DONE
```

The main agent acknowledged the same handoff ID: “主 agent 已收到 P1/P2 修复，
开始交给 2 号独立复核。” That ACK confirms receipt; independent review is pending.
