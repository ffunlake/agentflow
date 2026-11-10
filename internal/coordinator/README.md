# Single-run foreground coordinator (S5 thin slice)

## Default cmux layout launch

`agentflow up -f examples/team.yaml --run NEW_ID [--window window:N] [--run-dir DIR] --json`
creates a new cmux workspace with one `new-workspace --layout` call. If the
frozen config contains a workflow, the CLI opens the foreground coordinator
before returning; `--task` starts the first dispatch, otherwise the run waits
for the main seat to receive a new user task. Without a workflow, `up` exits
after launch.
YAML/JSON layout geometry and each agent's tool/cwd/role are used. Tool overrides
are `--codex-binary`, `--opencode-binary`, `--claude-binary`, plus `--cmux-binary`;
single-tool layouts also accept `--native-binary`. Existing workspace/surface
targets are rejected. The startup deadline remains 85s by default, maximum 90s.

`Up(ctx, plan, UpOptions{CMUX: &options})` dispatches to `UpCMUX` and returns a nil
coordinator owner with a result. `UpCMUX` acquires the original run lock, publishes
`up.requested.json` and the private frozen snapshot, initializes the existing store
with UNVERIFIED launch identities, launches the workspace, persists verified
LAUNCHED surface registrations and `cmux.launch.json`, then releases the lock. Existing launch artifacts
reject replay after success, failure or uncertain cmux outcome. Only the new
workspace is inspected for UUID/ref and visual agent/surface bindings.

Success is `status=LAUNCHED`, `executed=true` for workspace creation,
`native_process=LAUNCH_REQUESTED`, `runtime_communication=UNVERIFIED`,
`registration=SURFACE_REGISTERED`, `coordinator_ready=false`. Current seats and
surface UUID/ref/launch identities are persisted without a native session or READY
proof. Uncertain launches remain UNVERIFIED. The initial snapshot remains STARTING.
This lower-level launch API does not open the coordinator or deliver workflow
messages; the CLI performs that step after workspace creation.
The explicit headless flags below retain their existing execution/service paths.
See [launcher evidence](../../docs/evidence/cmux-layout-launcher-v1-result.md).

`OpenCMUXTransport(ctx, dir, runID, absoluteBinary)` / CLI
`coordinator --cmux-transport` reopens a launcher run under the same private run
lock and IPC actor validator. It requires persisted surface registrations and
installs the cmux workflow adapter and two explicit message methods:

For workflows whose execution/reviewer seats (or pipeline hops) are all persistent OpenCode roles,
the adapter binds each worker to the session reported for its exact surface and
uses OpenCode's native session database for submit/result collection. It does not
send workflow assignments through `cmux agent message`. Other tool mixes use the
generic cmux message/inbox adapter and require a live native message recipient.

| Method | Params | Result |
| --- | --- | --- |
| cmux-send | recipient, recipient_generation, body | cmux_send: local outbox record, launch binding, native evidence |
| cmux-inbox | empty object | cmux_inbox: binding, observed native messages (including matching history), raw process evidence |

Caller-controlled surface/workspace overrides, unknown/duplicate/null params and
stale run/agent/generation identities are rejected. The sender/request namespace
remains shared with message/ACK/stop operations. Native failure leaves a durable
intent and UNVERIFIED receipt; retries return evidence without another send.
Inbox reads never mark messages read. Matching historical IDs are skipped using
persisted source/target/body and receiving agent/generation; only new IDs append
events. Reopened mixed batches work in either order. Conflicting history, duplicate
IDs within a batch, unknown new sources and wrong surfaces reject the batch;
transaction failures roll back all writes. State/raw changes retain the first
receipt and create no new event. Neither message method establishes native READY or
advances workflow. Cmux mode normally installs `NewWorkflowTransportRunner(db,
transport)`: accepted start/resume events send only their new outbox; explicit lead
drain reads the original dispatch sender's bound inbox and commits structured replies.
When every non-lead workflow role is a persistent OpenCode seat and the lead is only
an approver, it instead uses the registered OpenCode TUI adapter: it submits to the
exact surface, reads that session's final structured result, and yields when the
dispatch is addressed to the lead. The lead records `approve`/`changes` through
`workflow decide`. The generic inbox route still requires exact native source name
and current source/target launch bindings. Opening/serving never drains. The opt-in
is mutually exclusive with headless mode. `workflow --cmux-transport` owns the same
foreground coordinator for one CLI invocation and closes it afterwards; a competing
owner is rejected.
See [workflow protocol and evidence](../../docs/evidence/cmux-workflow-transport-v1-result.md).

## Explicit headless OpenCode v1

`up --headless-opencode` and `coordinator --headless-opencode` explicitly install
the same headless execution kernel as Codex, with OpenCode native JSONL and session
resume. The existing workflow start/resume/drain/status CLI works unchanged. Only
OpenCode persistent roles at generation 1 are supported; tool flags are mutually
exclusive. Reopen checks a private `headless.opencode.json` version marker plus the
frozen tool/config identity, holds the existing run lock, and never relaunches or
drains automatically. Default `coordinator` still opens without native adapters.
Stop intent prevents new native work, while process shutdown remains unimplemented.
See [example](../../examples/headless-opencode.yaml) and
[delivery evidence](../../docs/evidence/headless-opencode-v1-result.md).

## Explicit headless Codex v1

`up -f examples/headless-codex.yaml --headless-codex --run ID --run-dir DIR --json`
uses the same run lock, Start/Registry, private snapshot/context, store and IPC
server. Every configured agent must use Codex and persistent sessions. Multiple
logical roles run sequentially without creating terminal panes. Native thread IDs
come from completed JSONL READY turns; `headless:RUN` / `headless:AGENT` coordinates
are logical identities, never cmux resources. Passing cmux target flags is rejected.

The explicit adapter installs WorkflowRunner plus native result collection.
`workflow start|resume --run ID --agent LEAD --generation N --revision R
--request-id STABLE [--reason DECISION] [--run-dir DIR] [--json]` commits the lead
event, claims the new outbox and synchronously collects execution/review results.
`workflow drain` uses the current revision and lead identity to reconsider the
current dispatch; it has no independent event receipt. The underlying dispatch
claim and deterministic native reply event ID provide deduplication. A stale
drain revision is rejected. Exact start/resume event retries return their original
receipt without executing native work. `workflow_current` reports the latest
cursor separately from that original `workflow.cursor` receipt. For CMUX message
transport, if the current approval is addressed to the lead itself, drain returns
`awaiting_actor=true` after checking for an already-arrived reply; the lead can
process the approval without blocking on its own TUI, then explicitly drain again.
Diagnostics cause the workflow CLI to exit 1 even if the initial event was durably accepted.

`workflow status --run ID --agent LEAD --generation N --request-id QUERY` reads
the latest cursor with no native execution or event write; it needs no revision.
Use this after an ambiguous IPC outcome to obtain the revision for an explicit
drain. Up includes all `seats` and `ready_proofs`, while the legacy singular
seat/proof fields describe the same first logical role.

After graceful shutdown, `coordinator --headless-codex --run ID --run-dir DIR`
validates the headless marker and frozen snapshot and installs the adapter without
launching native processes. Opening never drains automatically. Explicit drain
can consume a queued current dispatch or recover a saved SENT native result whose
workflow event was not yet committed. A previous SUBMIT_REQUESTED/unknown action
is never reissued, even if a capture exists. A crash before SENT confirmation needs
manual reconciliation. No stale lock stealing or crash recovery daemon is added.

Each native invocation has a configurable bounded deadline (`--turn-timeout`,
default 85s, maximum 30m). Headless up has a total startup deadline (default 5m,
maximum 30m); workflow CLI IPC deadline defaults to 30m (`--timeout`). A single
foreground call consumes at most 128 dispatches, then needs another explicit drain.
Individual native captures are bounded at 4 MiB; stderr is never published.
The existing 1 MiB IPC response limit still applies. The process inherits the
installed Codex configuration/authentication; no login or credential copying is
implemented. Account/profile/model changes are not recovery actions.

`assign` can also use the headless native submission adapter but ordinary messages
do not become workflow events. `send` stays persistence-only. A `--session fresh`
override queues with RESET_ADAPTER_UNIMPLEMENTED and does not claim a reset.
Structured workflow events still require handoff/review judgments and are agent
claims. A native `turn.failed` preserves failure evidence and conservatively leaves
the existing submission ledger unknown. A structured `retry` is a distinct explicit
workflow event and consumes the frozen finite attempt budget.

This opt-in is the only production headless path. The older default paths below
retain their persistence-only/TUI limitations. See [v1 evidence](../../docs/evidence/headless-codex-v1-result.md).

`agentflow coordinator --run ID [--run-dir DIR] [--json]` serves an **existing**, migrated run. The directory must be private (0700). Real `up` without `--run-dir` creates a unique directory under Go's `os.TempDir()` and returns its absolute canonical `run_dir`; pass that exact directory to coordinator, workflow and status commands, including after shutdown. They never allocate or guess a temporary directory. Omission retains only the legacy lookup at `$XDG_STATE_HOME/agentflow/runs/ID`, falling back to `~/.local/state/agentflow/runs/ID`. Explicit up directories retain their original behavior. Unused empty temporary allocations are removed before runtime handoff on CLI failure; after handoff the directory/evidence is retained, even on failure or normal shutdown. Default cmux launch creates the migrated database and surface seats; add `--cmux-transport` for explicit native messages and foreground workflow dispatch/result collection. Headless startup provides its own synchronous native execution path. `up --dry-run` creates only a plan.

```sh
# Keep this foreground process running for an already initialized run.
agentflow coordinator --run demo --run-dir /absolute/private/run --json

# Other CLI processes use only the matching lock and Unix IPC.
agentflow assign builder --run demo --run-dir /absolute/private/run \
  --agent main --generation 1 --request-id work-1 --body-file work.md --json
agentflow inbox --run demo --run-dir /absolute/private/run \
  --agent builder --generation 1 --pending --json
agentflow stop --run demo --run-dir /absolute/private/run \
  --agent main --generation 1 --request-id stop-1 --json
```

## Ownership and lifecycle

### Legacy single OpenCode foreground API

The original handshake is retained through `Up(ctx, plan, UpOptions{Native: ...})`
without CMUX or Headless options. It must run in the exact owned foreground
surface, with explicit Native window/workspace/surface and absolute native/cmux
binaries. This legacy API supports a single OpenCode seat; default CLI up uses
the new workspace launcher instead. Codex `_seat` remains separately callable.

`Up(ctx, plan, UpOptions)` takes the same run lock before initializing any store, snapshot, context or native evidence. Under it, an exclusive `up.requested.json` records the attempt before database migration. Existing attempt/database/snapshot/native/socket artifacts reject startup without overwrite. Compile/Start/ForegroundLauncher are reused, and a small launcher wrapper creates the run/agent file context before the native adapter runs. Source prompts are frozen in the private snapshot; stdin carries the startup role/challenge.

The adapter requires completed native JSONL, exact assistant actor/generation/tool/nonce and independently observed native session/workspace/surface. Registration atomically commits the READY seat and a `STARTUP_READY_VERIFIED` journal proof with its nonce. The existing store schema is sufficient. The snapshot stays immutable STARTING; SQLite and the proof hold current registration.

Success binds the existing IPC server on the same store and lock, emits structured `status=READY`, `capability_status=PARTIAL`, `runtime_communication=STARTUP_READY_VERIFIED`, artifact paths, current seat/proof and `coordinator_ready=true`, then keeps serving in the foreground. Native process state is `EXITED_AFTER_HANDSHAKE`: this does not establish persistent native TUI liveness, full permissions or real message submission. `executed` reports native execution confirmed by the wrapper; cancellation can leave it false while `native_process=OUTCOME_UNVERIFIED`.

SIGINT/SIGTERM cancels startup and lets the wrapper reap its native child, or stops only the owned coordinator after startup. The default startup deadline is 85 seconds, configurable with `--startup-timeout` in `(0,90s]`; it does not expire the later IPC service. Native failure, target mismatch, missing/wrong READY and cancellation preserve all attempted artifacts and never retry. Failed IPC binding after READY preserves the confirmed seat/proof but reports coordinator failure. A duplicate running up returns ALREADY_RUNNING; any later up in that directory returns UP_ALREADY_ATTEMPTED. To serve an initialized run later without relaunching native, use the explicit `coordinator` command.

The installed OpenCode field result, exact nonce/SQLite verification, duplicate refusals and cleanup are recorded in [single-seat up evidence](../../docs/evidence/s4-real-up-single-seat-3117cc8e/README.md). File context creation is established; full native context consumption/restoration, automatic recovery, native submit/reset, process attach/stop and Claude remain PARTIAL/UNIMPLEMENTED.

`Open(ctx, dir, runID)` acquires the existing `internal/lock` lock before opening `state.db` writable. It checks the singleton run, constructs `runtime.MessageService` (which owns `SessionCoordinator`) and binds `ipc.sock` at mode 0600. Another instance receives `ALREADY_RUNNING` before it opens the store or listener. Failed startup releases only its own lock; existing locks, sockets and files are preserved. Long Unix socket paths produce an explicit startup failure; select a shorter private run directory.

The CLI prints `COORDINATOR_READY` only after lock/store/listener setup. `Serve(ctx)` handles IPC until SIGINT/SIGTERM or context cancellation, drains handlers, closes the store and releases the lock. `Close` is for failed startup or after Serve has drained, and must not be called concurrently with Serve. A crashed process can leave a lock/socket; no stale-lock stealing, automatic recovery or daemon is added.

`stop` records intent and keeps the coordinator available for queries and idempotent retries. Its response has `executed=false`, `capability_status=PARTIAL` and diagnostic `PROCESS_STOP_UNIMPLEMENTED`. It does not kill tools, close panes or implement process-level shutdown.

## IPC methods and identity

The existing v1 NDJSON envelope supplies `actor` and `requestId`. Params reject unknown, duplicate, null and method-inapplicable fields:

| Method | Params |
| --- | --- |
| assign | recipient, recipient_generation, body, session, mode |
| send | recipient, recipient_generation, body |
| inbox | after, pending |
| ack | message_id |
| reply | message_id, body, status, optional workflow |
| workflow | workflow (lead start/resume control) |
| reconcile / resume | message_id |
| stop | empty object |

Every request checks the claimed run/agent/current generation against the store through the IPC validator. Runtime/store operations recheck inside their own transaction, including every retry. The private socket grants access to the local OS user; these checks do not authenticate one same-user process as a particular native agent. Native event authentication/READY registration are outside this slice.

Message body, routing and fresh/persistent idempotency contracts are inherited unchanged. Stop uses the existing journal for `STOP_REQUEST_ACCEPTED` receipts and shares the sender/request namespace with messages and ACKs. Intent, receipt and generation validation are atomic. A retry returns the first stop event sequence/time; old generations and cross-operation request reuse are rejected.

## Workflow event flow

An existing run with a frozen workflow and registered current seats can advance via the same private v1 socket and `coordinator.Call`. The workflow CLI exposes lead start/resume/drain/status/decide. Opening a coordinator or recording READY does not automatically start a workflow. Start requires revision 0; resume requires the current PAUSED revision and a nonempty reason. Worker/reviewer events use `reply` with an additional structured `workflow` object. The generic cmux route consumes that object from the bound inbox; the OpenCode TUI adapter parses the native assistant result and commits the same event shape. Lead approval uses `workflow decide` against the current dispatch:

```sh
agentflow workflow decide --run demo --run-dir "$RUN_DIR" --agent main \
  --generation 1 --revision 6 --message-id "$DISPATCH_ID" \
  --request-id "decision-$DISPATCH_ID" --kind approve --accepted true
```

Use `--kind changes --reason "specific correction"` to return work instead.

The generic IPC event shape remains:

```json
{"v":1,"requestId":"start-1","actor":{"runId":"demo","agentId":"main","generation":1},"method":"workflow","params":{"workflow":{"revision":0,"kind":"start"}}}
```

The returned `result.workflow.cursor` contains the current revision and dispatch ID. The generated `assign` is visible through ordinary `inbox`, with a JSON `loopcore.Intent` body. Its target is the current executor/reviewer generation; delivery stays `PENDING/UNVERIFIED` with no native attempt. The recipient can explicitly ACK through the existing method, then formally reply (replace the sample dispatch ID with the returned ID):

```json
{"v":1,"requestId":"handoff-1","actor":{"runId":"demo","agentId":"builder","generation":1},"method":"reply","params":{"message_id":"<execute-dispatch-id>","body":"Implementation and test evidence saved.","status":"DONE","workflow":{"revision":1,"kind":"handoff","handoff":{"summary":"Implemented the change","evidence":["tests.log"],"next":"Review the saved work"}}}}
```

This saves the ordinary reply, review dispatch and cursor in one immediate transaction. The configured reviewer replies to the **review dispatch** with `kind=approve` and `accepted=[true,...]`, one judgment per frozen criterion. Structured replies require an explicit matching terminal status: `handoff/approve/changes` use `DONE`, `retry` uses `FAILED`, and `blocked` uses `BLOCKED_CONFIRM`. Missing, nonterminal or mismatched status rejects the entire operation before any reply/cursor/dispatch write, so superseded work cannot leave the reporting seat busy. `changes/retry/blocked` require a reason; changes/retry consume the finite attempt budget, blocked pauses, and lead resume starts a new execution attempt. Completed/exhausted states reject new events. Plain ACK, `status=DONE`, free text and S6 file report publication do not implicitly advance or approve anything. Structured event kind and judgments drive the reducer; reply status remains a saved agent claim.

Workflow identity and event ID derive only from the envelope; message ID derives only from the reply route. Nested workflow/handoff keys reject unknown/duplicate/case-variant/null fields. Every event rechecks current actor, dispatch generation, revision, frozen config and stop intent in the committing transaction. Exact retries return the original reply, dispatch and journal receipt across coordinator restart, including after stop; changed event/body conflicts, and an invalid status fails the terminal-status gate. Requests share message/ACK/stop namespaces, event IDs are unique across the run, and lead `workflow:<revision>` IDs are reserved for generated dispatches. Failed events save neither reply nor cursor/dispatch. Responses use `status=WORKFLOW_ADVANCED`, `executed=false`, `capability_status=PARTIAL`, `runtime_communication=UNVERIFIED`; this confirms persistence only.

Native identity authentication, automatic event collection/scheduling, production native submit/reset adapters, evidence-content verification and in-flight generation takeover remain unimplemented. The private socket retains its existing local-user trust boundary. A new seat generation cannot consume a prior-generation workflow dispatch; do not treat generation rejection as successful recovery. See [loop-core](../loopcore/README.md) and [local wiring evidence](../../docs/evidence/coordinator-loop-wiring-result.md).

### Injected workflow dispatch

Programmatic `OpenWithAdapters(ctx, dir, runID, reset, submit)` additionally enables `runtime.WorkflowRunner` on the same IPC methods. It synchronously consumes the next outbox item after a new event commits. The new dispatch stores its recipient's frozen session policy, and the existing SessionCoordinator claims/inspects/submits it. In this mode, `result.dispatch` reports the observed delivery; `executed=true` means a reset/submit action was requested, including an unknown outcome. A saved/queued event, exact event replay, pause or terminal transition has no new native action. `result.workflow` remains the durable receipt, and `result.record` remains its ordinary reply. Dispatch failure after commit preserves `WORKFLOW_ADVANCED` plus `WORKFLOW_DISPATCH_FAILED`; inspect/reconcile the saved dispatch explicitly.

Exact event retries return the original workflow receipt with no `dispatch` and `executed=false`; they do not repeat or retry native work, even if an adapter is now available. Opening/reopening does not start a workflow or drain old pending/queued/unknown outbox entries. Stop between event commit and action claim still blocks the adapter. Workflow assignments retain their saved target generation and can only be claimed while their cursor is active/current. The default `Open`/CLI/real `up` behavior above remains the persistence-only path.

The local fake test completes both example steps with registered persistent seats: builder executes → reviewer reviews/approves → reviewer executes → main reviews/approves. Four adapter submissions and five explicit events complete the workflow; approval is the decision. Adapter callbacks must return submission evidence before sending workflow replies, and native event authentication remains the embedding process's responsibility. No real native adapter or event collector is provided. See [runtime contract](../runtime/README.md#workflow-runner) and [fake validation](../../docs/evidence/workflow-runner-fake-e2e-result.md).

Transport failures use `ok=false/error`; service operation failures use `result.status=FAILED` and structured diagnostics. CLI maps both to exit 1; invalid usage exits 2. Successful persistence/query/receipt exits 0 while native capability remains PARTIAL and native communication UNVERIFIED. Unsupported methods return `UNIMPLEMENTED`.

`assign/send/inbox/ack/reply/stop` and `reconcile/resume` CLI paths never open a database. They locate the socket only from a matching lock and make one call with a 10-second deadline. No coordinator yields `COORDINATOR_UNAVAILABLE`; dead sockets or transport loss yield `COORDINATOR_IPC_FAILED`. There is no direct-write fallback, automatic process start or automatic retry. A lost response may hide a committed write; retry with the same durable request ID.

`status/attach` continue to use read-only database diagnostics. `bootstrap/rebuild` generate/read continuation files without database mutations; their existing immediate transaction fences generation throughout publication, so they still require a writable SQLite handle. `_seat` remains the separate explicit native single-seat executor. The default coordinator installs no reset/submit adapters: fresh/persistent assignments queue with existing `RESET_ADAPTER_UNIMPLEMENTED` / `SUBMIT_ADAPTER_UNIMPLEMENTED` reasons, without native delivery or generation advancement.

## Validation

`internal/coordinator` tests cover ownership, concurrent IPC idempotency, ACK/reply/stop replay after reopening, namespace/identity gates, unimplemented adapters, strict params and startup cleanup. CLI tests verify absence/dead-socket/wrong-run paths leave DB/journal/stop intent unchanged. E2E tests start a real foreground coordinator subprocess, reject a second instance and use independent CLI subprocesses across coordinator/store restart. All native tools in these tests are fixtures or excluded; no field cmux/native sessions are used.
