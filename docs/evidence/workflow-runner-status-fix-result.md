# Structured workflow reply terminal status fix

Task: `workflow-runner-status-fix`  
Result: **DONE / PASS**, pending main agent review.

## Root cause and fix

`store.replyWorkflow` previously validated reply/event identity without requiring a matching terminal report status. An omitted or `WORKING` report could therefore commit its reply, cursor and next dispatch, while `storeMessage` left the reporting seat BUSY on its previous work. A subsequent dispatch to the same seat could stall.

`internal/store/workflow.go` now rejects invalid status before entering the transaction shared by `ReplyWorkflow` and `ReplyWorkflowWithSessions`:

| Event | Required report status |
| --- | --- |
| handoff | DONE |
| approve | DONE |
| changes | DONE |
| retry | FAILED |
| blocked | BLOCKED_CONFIRM |

The rejection uses the existing `WORKFLOW_INVALID_TRANSITION` response. It writes no reply, cursor, dispatch or request receipt and invokes no adapter. The valid terminal reply retains the existing atomic release of the reporting seat and workflow persistence. The coordinator IPC contract is updated in `internal/coordinator/README.md`.

## Regression evidence

Added `TestWorkflowRunnerReplyRequiresMatchingTerminalStatusIPC` in `internal/coordinator/workflow_runner_test.go`. Each of the five event kinds is exercised through the Unix socket and injected fake runner. Omitted status, `WORKING` and the two mismatched terminal statuses are rejected; the test compares all messages/deliveries/journal rows, complete seat state and adapter call counts. It then submits the matching terminal status under the same request ID, verifies the old work is released and the next dispatch reaches SENT (after explicit resume for blocked), and checks exact replay does not dispatch or mutate the ledger.

Before the fix, the new test failed for all five event kinds: omitted status returned `WORKFLOW_ADVANCED`, saved a reply and advanced the cursor. After the fix, all cases pass. The existing `TestWorkflowRunnerFakeThreeRolesTwoStepsIPC` is retained and passes.

## Validation

```text
go test ./internal/coordinator ./internal/store -run Workflow -count=1
ok agentflow/internal/coordinator
ok agentflow/internal/store

make check
all modules verified
go vet ./... PASS
go test ./... PASS
go build ... ./cmd/agentflow PASS

CGO_ENABLED=1 go test -race ./internal/coordinator ./internal/runtime ./internal/store -count=1
ok agentflow/internal/coordinator
ok agentflow/internal/runtime
ok agentflow/internal/store
```

No tested window was accessed or operated. Validation uses local fake adapters and temporary IPC/store fixtures. This directory has no Git metadata; no commit was created. Main review and acceptance remain separate from handoff transport receipt.

## Formal handoff

Target: `surface:1000060018`. Payload: `workflow-runner-status-fix-handoff.json`.

`cmux agent message surface:1000060018 --from '资深研发' --json - < docs/evidence/workflow-runner-status-fix-handoff.json` exited 0. Receipt ID: `439aebdd-e416-4e4d-971f-e06352bd24fa`; state: `queued`; `recipient_has_agent=true`; `delivered_at=null`; `read_at=null`. Recipient surface UUID: `3637E165-9BD7-4EF5-B8B6-C4B46DEB2867`, workspace: `workspace:1000060006`. Formal queue acceptance is confirmed; main ACK/review is pending.
