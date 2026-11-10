# Coordinator loop wiring — local result

Date: 2026-11-03. Task: `coordinator-loop-wiring`. Coordination session: `567d3a37-8193-431d-97f4-c295666276af` (assigned for this handoff; INIT supplied no session ID).

Scope result: **PASS**. Product capability: **PARTIAL**, native communication: **UNVERIFIED**.

The existing single-run foreground coordinator now accepts explicit lead workflow controls and structured executor/reviewer replies on its existing v1 Unix IPC. A valid structured reply commits its ordinary reply message, next workflow dispatch, delivery and cursor journal in one immediate SQLite transaction. No dispatch is submitted to a native tool. Opening the coordinator or registering READY does not start a workflow automatically.

## Implementation

- `internal/coordinator/workflow.go`: `WorkflowParams`, envelope-derived event identity, lead `workflow` start/resume controls, structured `reply` events and stable persistence/failure responses.
- `internal/coordinator/coordinator.go`: method routing, optional result/params fields and reusable exact-key object validation. Nested workflow/handoff objects reject duplicate, unknown, case-variant and null fields; revision/kind are required.
- `internal/store/workflow.go`: `ReplyWorkflow`, optional reply in the durable workflow result/receipt, exact replay including reply body/status, shared caller/request namespace checks and collision checks on generated lead dispatch IDs.
- `internal/store/messaging.go`: extracted the existing in-transaction message writer for atomic reuse; standalone StoreMessage retains its actor check, transaction and late-result behavior. Workflow receipts reserve request IDs alongside existing ACK/stop receipts.
- `internal/coordinator/workflow_test.go`, `internal/store/workflow_test.go`: IPC integration, requested P3 boundaries and reply rollback tests.
- Updated root/docs/config/runtime/coordinator/loop-core READMEs, the YAML example header and the core result's integration pointer.

## Event contract

Lead starts with method `workflow`, params `{"workflow":{"revision":0,"kind":"start"}}`. Resume uses the same method with current PAUSED revision, kind `resume` and a nonempty reason. Only the frozen lead may control these transitions.

Execution/review results use ordinary `reply` params `message_id/body/status` plus `workflow` containing `revision/kind` and event-specific `handoff/accepted/reason`. Event ID is the envelope requestId, actor is the envelope agent, and dispatch ID is the reply route. The reducer checks configured roles, formal handoff, explicit per-criterion approval and saved attempt limits. Ordinary ACK/DONE/free text and file report publication do not advance or approve work. Reply status remains a saved agent claim; structured event kind/judgments select the transition.

Events must have a unique ID across the run, retaining the core tick contract, and also reserve the existing caller/request namespace. Generated lead `workflow:<revision>` IDs are reserved for dispatches. Exact event+reply replay returns the original message/journal identities across restart and after stop. Changed input conflicts. New events after stop, stale actors/revisions/dispatches, invalid handoff/approval and completed/exhausted advancement fail without saving a workflow reply or transition.

## Verification

All final commands below returned exit 0. Go commands used the existing project `.cache` directories, `GOTOOLCHAIN=local` and `GOFLAGS=-mod=readonly`; ordinary builds/tests used `CGO_ENABLED=0`, race used `CGO_ENABLED=1`.

| Command | Result |
| --- | --- |
| `go test ./internal/loopcore ./internal/store ./internal/coordinator -run 'Test.*Workflow' -count=1` | PASS, all three packages |
| `make check` | PASS: gofmt check, module download/verify, `go vet ./...`, `go test ./...`, CLI build |
| `go build ./...` | PASS |
| `go mod tidy -diff` | PASS, empty diff |
| `CGO_ENABLED=1 go test -race ./internal/loopcore ./internal/store ./internal/coordinator ./internal/ipc ./internal/runtime -count=1` | PASS, all five packages |

Requested P3 coverage now includes:

- Different event IDs competing for the same revision through two store handles: exactly one winner; one message, one delivery, two journal records; no loser writes.
- Exact replay after stop and coordinator/store reopen: identical result and unchanged persisted rows/journal.
- Missing/empty/whitespace handoff fields: rejected without outbox/cursor writes.
- New events after COMPLETED: rejected without journal changes; IPC test also compares all message/delivery/journal rows.
- Review blocked/resume: only lead resumes, handoff clears, executor receives a new attempt, budget remains finite.

Additional integration coverage uses the example's three roles and two steps over real local IPC, ordinary inbox/ACK/plain reply, explicit handoff and per-criterion approval to completion, coordinator reopen, incomplete/false approvals, actor/generation/revision/route rejection, strict nested JSON and namespace conflicts in both directions. Full-row snapshots detect inserts and updates in messages/deliveries/journal. Fault triggers at MESSAGE_STORED, WORKFLOW_DISPATCH_STORED and WORKFLOW_ADVANCED prove ordinary reply plus next dispatch/cursor roll back together. Existing four-point outbox rollback, stale native generation and frozen-config tests still pass. Full runtime, CLI, integration and process E2E suites pass through make check.

## Limits and handoff

Workflow CLI flags/command, automatic start/scheduling, authenticated native event collection, native submit/reset and evidence-content verification remain unimplemented. Dispatches remain PENDING/UNVERIFIED with attempt 0; successful response is WORKFLOW_ADVANCED with executed=false. If generation changes during EXECUTING/REVIEWING, old-generation actors and new-generation replies to the old dispatch both fail; in-flight takeover/re-dispatch needs a separate explicit policy. PAUSED lead resume is the only recovery control in this slice and consumes the saved attempt budget.

No task table, migration, MQ, DAG, background service, native adapter or dependency was added. Only agentflow source/docs and normal build/cache artifacts were changed. No tested native window/workspace/surface was operated. The working directory has no `.git`; no commit was made. Local IPC tests verify the existing local-user trust boundary, not independent native-process identity.

Formal handoff target: `surface:1000060018`. Payload: `coordinator-loop-wiring-handoff.json`. Transport receipt: `coordinator-loop-wiring-handoff-receipt.json`. Next: main agent independently reviews atomic reply/transition persistence, exact-key decoding, namespace gates and capability limits; native work remains a separate acceptance scope. A queue receipt establishes transport acceptance only.
