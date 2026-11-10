# Single OpenCode real up — local and field PASS, product PARTIAL

Task: `s4-real-up-single-seat`  
Session: `3117cc8e-a3c2-4eaf-828e-971ca29181f8`  
Date: 2026-11-03

The installed OpenCode completed exactly **one** real `agentflow up` invocation in one newly created terminal pane. Native JSONL, the exact startup actor/generation/nonce, registered session/location and reopened SQLite proof agree. The same up process then served coordinator IPC under its original lock. Active and reopened duplicate up were rejected without replay or artifact changes. This is a one-shot foreground startup handshake; the native process exited and product capability remains **PARTIAL**.

## Code and command

```sh
agentflow up -f examples/single-opencode.yaml --run new-run \
  --run-dir /absolute/private/new-run --window window:N \
  --workspace workspace:N --surface surface:N --json
```

- `internal/cli/up.go`, `up_real.go`, `run.go`: explicit single-OpenCode up flags, bounded startup context, text/JSON results and foreground coordinator serving. Unsupported layouts/tools return structured PARTIAL / UP_LAYOUT_UNIMPLEMENTED before any native action.
- `internal/coordinator/up.go`, `coordinator.go`: reuse the existing run lock before attempt marker, store migration, frozen snapshot, file context or native launch. Reuse Compile/Start/ForegroundLauncher; context is published before Launch. The successful coordinator owns the same store/lock for IPC. A durable exclusive `up.requested.json` prevents retry after failure, cancellation or shutdown, and existing artifacts are never overwritten.
- `internal/runtime/native.go`, `registration.go`, `internal/store/binding_recovery.go`: carry the exact verified native nonce in Registration and atomically persist the READY seat plus STARTUP_READY_VERIFIED journal proof. Journal reuse requires no new table or dependency. The immutable startup snapshot remains STARTING; the current seat/proof are stored in SQLite.
- `examples/single-opencode.yaml`, coordinator/runtime/root documentation: invocation, cancellation, explicit existing-run coordinator reuse, native process state and PARTIAL boundaries.

Runtime up only supports one OpenCode foreground seat. Codex's existing low-level native path continues to pass offline fixture tests; this field task launched no real Codex or Claude process.

## Field scope and identity

| Item | Observed value |
| --- | --- |
| Window | `window:1000060000` (control window; tested `window:1000060005` was not addressed) |
| New temporary workspace | `workspace:1000060012`, UUID `01882699-0FDE-4780-BBEB-6AC0B0128848` |
| Only temporary surface | `surface:1000060039`, UUID `40E86A14-6021-4B07-BD21-B163D656E15C` |
| Registered actor | run `real-up-3117cc8e`, agent `solo`, generation `1` |
| Installed native command | `/opt/homebrew/bin/opencode run --format json --dir /Users/funlake/Projects/agentflow` |
| Native session | `ses_e5fa69984ffexqVYX6Vh770KbJ` |
| Exact nonce | `f4aaeb02a5e2e87162e962d83fe0e0eac62968679750c52f` |
| Permissions | `default`; no full-permission claim |
| Native invocations | OpenCode **1**, Codex **0**, Claude **0** |
| Coordinator | PID `50619`, clean SIGINT exit `0` after startup/IPC checks |

[Scope](scope.json), [creation receipt](create-workspace.json) and [temporary topology](temporary-tree.json) identify the single new pane. The helper used one paste and one Enter into this new surface only ([send](send-once.json), [submit](enter-once.json)). It did not type commands into any original agent/control input. No command addressed protected `surface:1000060034/35`, the tested window or its workspace. No login, credentials copy or configuration change was performed. The field config/prompt is a new private test artifact, not a modification of the user's native configuration.

The legacy cmux `new-workspace --json` returned `OK workspace:1000060012` text. The helper stopped after that successful creation, then parsed the saved receipt and inspected that same workspace; it never repeated the creation or native action. The exclusive [native attempt marker](native-attempt.json) was reserved before up started.

## Evidence chain

1. [Actual command](up-command.json) → installed `agentflow up` in the owned new terminal. Up acquired the unique lock, created its private [attempt marker](run/up.requested.json), initialized [state.db](run/state.db), froze [snapshot](run/config.resolved.json) and published [file context](context/real-up-3117cc8e/run.json).
2. [Native evidence](run/native.jsonl) contains exactly one pre-execution challenge and one completed process record. Its real assistant text exactly equals actor/run/generation/tool/status/nonce; step_finish has reason stop, all native session IDs agree, process succeeded and output was not truncated. No user echo, idle event, process start or cmux success is used as READY.
3. [Up result](up-result.json) reports READY, STARTUP_READY_VERIFIED, coordinator_ready=true, capability PARTIAL and EXITED_AFTER_HANDSHAKE. The reopened [field result](field-result.json) confirms the exact persisted seat/proof. Journal events are exactly LAUNCH_REQUESTED, LAUNCH_BOUND, STARTUP_READY_VERIFIED; READY and nonce proof commit together.
4. A separate [CLI inbox](ipc-inbox.json) returned QUERIED via coordinator IPC. [Active duplicate up](duplicate-active.json) returned ALREADY_RUNNING. The owned coordinator then [exited 0](up-exit.json), removed its lock/socket and closed the store.
5. [Reopened duplicate up](duplicate-reopened.json) returned UP_ALREADY_ATTEMPTED before native launch. Snapshot, native evidence, attempt marker and every managed context file retained the [same SHA-256](immutable-artifacts.json).
6. The [temporary workspace close](close-temporary-workspace.json) affected only the new workspace. [Control before](control-before.json), [control after](control-after.json) and [cleanup verification](cleanup-verification.json) confirm the original control layout and surface sets are identical. [Offline verification](offline-verification.json) independently reparses JSONL/SQLite and checks artifact hashes and scope without any native or cmux actions.

Do not rerun the field create/submit/execute actions. Their receipts and markers deliberately reject replay. The verifier may be run on a fresh copy of the evidence; its output uses exclusive creation too.

## Offline tests and engineering checks

- `internal/coordinator/up_test.go`: lock acquisition before all initialized artifacts, context before native Launch, confirmed READY proof, duplicate up while locked and after close, launch/READY/nonce/generation/cancellation failures, and IPC-bind failure after a confirmed READY. All attempted failures retain their marker and never call Launch twice.
- `internal/store/startup_proof_test.go`: invalid nonce rejection and an injected journal failure prove READY/nonce atomic rollback; successful persisted proof exactly equals the stored seat and nonce.
- `tests/e2e/up_real_cli_test.go`: real CLI/wrapper/coordinator processes with **native fixtures**, exercising success, no READY, stale generation, wrong nonce, process failure, target mismatch and timeout cancellation. It verifies IPC, exclusive/reopened duplicate refusal, private artifacts, stable evidence, native fixture launch count and explicit existing-run coordinator reuse. Existing OpenCode/Codex native fixture and messaging regressions also pass.

The final source completed all required commands with exit **0**, using project-local caches/temp directories, GOTOOLCHAIN=local, CGO_ENABLED=0 and GOFLAGS=-mod=readonly:

```text
go test ./...
go build ./...
make check
go mod tidy -diff
```

`make check` passed formatting, module verification, vet, all tests and CLI build. Tidy produced no diff. Focused coordinator/CLI/runtime/store/E2E tests also passed. The test suite invokes only fixtures, never this installed-provider field action.

## PASS / PARTIAL boundary

**PASS:** single OpenCode real up, lock-before-initialization, private snapshot/context publication, exact completed JSONL READY and atomic nonce proof, local coordinator IPC, duplicate refusal, reopened persistence, owned-process cleanup and restored control topology.

**PARTIAL / UNIMPLEMENTED:** persistent native TUI liveness after the handshake, real native submit/reset and ACK delivery, effective full permissions, full native context consumption/restoration, multiple seats or pane layout creation, Claude, Codex real up, authenticated native events, automatic recovery, and process/workspace attach/stop. Up's executed flag means execution confirmed from wrapper output; cancellation can leave execution unknown. Startup artifacts are retained; neither failure nor a new up invocation authorizes replay. The run's historical startup phase and immutable artifact remain STARTING; the SQLite current seat and proof carry READY.

Formal [agent_handoff](agent_handoff.json) ID `s4-real-up-single-seat-3117cc8e` was submitted to the supplied control surface `surface:1000060016`. cmux exited 0 with [receipt](handoff-receipt.json) ID `bdb82f2d-d619-4418-aad5-995e3b9b4013`, `state=queued`, `recipient_has_agent=false`, and no delivered/read timestamp. Queue acceptance is confirmed; main receiver/review ACK is pending.
