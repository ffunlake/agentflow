# Configurable workflow loop-core — local result

Scope: deterministic workflow advancement, atomic cursor/journal/outbox persistence, and fake three-role collaboration. Local implementation and validation: **PASS**. Real native continuous adapters and production delivery remain unimplemented by this change.

Implemented files:

- `internal/loopcore/loop.go`: pure state machine and detached event/cursor/dispatch types.
- `internal/store/workflow.go`: `AdvanceWorkflow` and `WorkflowCursor`; uses existing append-only journal as durable cursor history without a schema migration.
- `internal/loopcore/loop_test.go`, `internal/store/workflow_test.go`: reducer, transaction and fake collaboration coverage.
- `internal/loopcore/README.md`: invocation, event, persistence, retry and integration contract.

Behavior verified:

- Frozen `examples/workflow.yaml` drives main → builder → reviewer, followed by reviewer execution → main review → completion. Execution handoff cannot itself approve a step. Approval requires the configured reviewer and an explicit passing judgment for every acceptance criterion.
- Formal handoff requires summary, evidence references and next action. Evidence references and review judgments remain agent claims; file-content integrity is outside this API.
- Changes/retry repeat the current step; max attempts are finite. Blocked pauses without dispatch. Only lead resumes, consuming a new execution attempt. Completed/exhausted workflows cannot restart through a new event ID.
- Exact event retries reuse cursor, message and journal identities, including after reopen and simultaneous calls from two independent SQLite handles. Different input under the same event ID conflicts.
- Stale revision, actor spoofing, old/current generation attempts to satisfy an obsolete dispatch, config digest tampering and stop intent reject advancement.
- SQLite fault triggers at message insertion, delivery insertion, dispatch journal insertion and cursor journal insertion leave all three tables unchanged. Retrying after removing each fault succeeds.
- Existing message/ACK/native delivery state is not implicitly advanced by workflow events. New outbox entries are `PENDING`, channel `UNVERIFIED`, with no submission or reset attempt.

Executed verification (all passed):

```sh
go test ./internal/loopcore ./internal/store -run 'Test.*Workflow' -count=1
make check
CGO_ENABLED=1 go test -race ./internal/loopcore ./internal/store -count=1
```

Commands used repository-local caches and Go module readonly mode. `make check` included gofmt validation, module download/verify, `go vet ./...`, `go test ./...`, and CLI build. No new dependencies or schema changes.

At this core-only checkpoint, integration next was to expose `store.AdvanceWorkflow` through coordinator events. That local IPC wiring is now implemented in the [coordinator loop wiring result](coordinator-loop-wiring-result.md): explicit lead controls and structured replies commit reply/next dispatch/cursor atomically. The API still advances only on explicit events; it does not run a scheduler, open native sessions, deliver outbox items or prove native collaboration. The local-user socket does not authenticate a process as a native agent. See `internal/loopcore/README.md` for the current contract.
