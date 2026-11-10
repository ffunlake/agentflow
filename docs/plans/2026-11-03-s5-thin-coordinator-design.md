# S5 thin coordinator

Scope: a foreground `agentflow coordinator --run <id> [--run-dir <dir>]` for an existing private run directory and migrated store. Reuse lock, NDJSON Unix IPC, MessageService and its SessionCoordinator. No new dependency, daemon, pane launcher or recovery scheduler.

The coordinator acquires `coordinator.lock` before opening the writable store, verifies the singleton run, binds a private socket, then serves until context cancellation. Shutdown drains IPC handlers before closing the store and releasing its own lock. Existing locks/sockets are never stolen or removed to start another instance. Overly long Unix socket paths fail explicitly.

assign/send/inbox/ack/reply and reconcile/resume clients locate the socket from a matching lock and call IPC with a bounded deadline. They never open the database. Stop also uses IPC and requires actor/current generation/request ID; it records only an intent. Store transactions recheck identity and share the durable sender/request namespace across message, ACK and stop requests. Read-only status/attach remain local diagnostics. File-only bootstrap retains its immediate generation fence without mutating database rows.

Native reset/submission adapters remain absent: assignments are durably queued with existing UNIMPLEMENTED coordination reasons. The response distinguishes successful persistence/IPC from PARTIAL native capability and UNVERIFIED native delivery. Missing coordinator returns a structured diagnostic without direct-write fallback or automatic retry. Unsupported methods return structured UNIMPLEMENTED.

Validation covers lock contention, IPC forwarding, stale/unknown actors, durable idempotency and conflicts, clean shutdown/reopen, failed startup cleanup, no-coordinator/no-fallback behavior and independent CLI subprocesses. Required checks: `go test ./...`, `go build ./...`, `make check`, `go mod tidy -diff`.
