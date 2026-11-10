# Runtime planning, coordination and file context (S4–S6 partial)

## CMUX workspace launcher v1

Default real CLI `up` uses `BuildCMUXWorkspaceCommand(plan, CMUXWorkspaceOptions)`
and `LaunchCMUXWorkspace(ctx, plan, options)`. The pure builder validates the frozen
canonical plan and emits one `new-workspace --layout` with binary splits/ratios,
one terminal per leaf, absolute cwd and a fully quoted `_tui-seat` command. Options
provide an optional target window and absolute cmux/per-tool binary overrides.
`PreflightCMUXWorkspace` checks executable files and existing cwd directories.
Launch requires a matching private materialized snapshot, never retries cmux,
and reads `tree` only for the returned workspace to verify structural leaf order
and unique terminal surfaces. JSON creation and legacy `OK <workspace-ref|UUID>`
responses are supported. Returned UUIDs/short refs are topology evidence only.

cmux transports each layout `command` through terminal `sendInput`, appending a
submission newline; it does not directly exec the command argv. Shell quoting
protects shell syntax but cannot stop terminal key/control interpretation. Before
any cmux call, the builder rejects C0 (`U+0000–U+001F`), DEL (`U+007F`) and C1
(`U+0080–U+009F`) in every command token, including wrapper/snapshot/tool paths,
and in role/cwd and the cmux binary path. Spaces, quotes and valid Unicode outside
those ranges retain the existing POSIX per-word quoting. Executable fake-cmux
tests apply the verified terminal transport subset before invoking the shell;
key/control input fails closed, and rejection tests assert zero cmux calls.
The fixture is pinned to upstream
[layout submission](https://github.com/manaflow-ai/cmux/blob/12023d89feb5425c766ba05e8d8e4cfc176e3991/Sources/Workspace%2BCustomLayout.swift)
and [terminal input parsing](https://github.com/manaflow-ai/cmux/blob/12023d89feb5425c766ba05e8d8e4cfc176e3991/Packages/macOS/CmuxTerminal/Sources/CmuxTerminal/Surface/TerminalSurface%2BInput.swift);
it validates the supported command subset, without emulating a full terminal.

`TUICommand` constructs native argv from the frozen role/prompt/model. `_tui-seat`
validates the snapshot, changes cwd and execs the interactive CLI with terminal
stdio. Codex uses its interactive default, OpenCode its default TUI plus `--prompt`,
and Claude its interactive default with the existing bare/safe-mode choice.
Prompts/models/raw args never enter layout shell source. Multiline prompts remain
snapshot data delivered directly to native argv. NUL and invalid UTF-8
fail before cmux creation. OpenCode extra TUI args are unsupported; Codex accepts
`--profile NAME`, Claude only `--bare`/`--safe-mode`. Session policy is metadata;
resume/reset is outside this launcher. `_seat`/ForegroundLauncher remain separate
one-shot handshake facilities.

`coordinator.UpCMUX` owns the lock, exclusive attempt marker, private snapshot and
`cmux.launch.json` evidence, then releases the lock and exits. Success reports
`LAUNCHED`, `runtime_communication=UNVERIFIED`, `registration=SURFACE_REGISTERED`,
`coordinator_ready=false`. The snapshot stays STARTING without native bindings.
SQLite stores current LAUNCHED seats and append-only surface registrations with
run/agent/generation, launch ID, UUIDs and refs. Before native launch each seat is
persisted as UNVERIFIED, so interruption cannot erase the attempted identity.
No native session or READY proof is inferred. Explicit headless flags retain their
paths. See [transport evidence](../../docs/evidence/cmux-agent-message-transport-v1-result.md).

## Explicit cmux agent transport

`CMUXTransport` also implements `WorkflowTransportAdapter.Dispatch/Collect` for
`NewWorkflowTransportRunner`. Dispatch claims the existing workflow outbox with
`BeginCMUXDispatch`; it preserves message/request/generation and wraps the intent
with the sender/recipient launch registrations. Collect reads the original sender's
inbox and accepts only strict `workflow_reply` JSON with matching terminal status,
native source name, source/target UUIDs and actor/current generation. The store
rechecks this binding and exact native body in the atomic workflow transaction.
`CMUX_WORKFLOW_APPLIED` links accepted native evidence to reply/cursor/next outbox.
No terminal input, native reset or READY is involved. Start/resume dispatch new
work once; a cmux drain request claims and sends the current outbox, then reads
the lead inbox once. The CLI repeats this short IPC request while waiting, so
server handlers remain cancellable and one CLI command drives phases to a
terminal state. A claimed unknown send cannot be reclaimed. See [protocol](../../how-to-use.md#cmux-workflow-前台协作)
and [evidence](../../docs/evidence/cmux-workflow-transport-v1-result.md).

`NewCMUXTransport(db, absoluteCMUXBinary)` provides `Send(ctx, SendMessage)` and
`Inbox(ctx, ipc.Actor)`. Production callers use `coordinator.OpenCMUXTransport`
under the existing run lock. No input-ownership guard is needed: this transport
only uses `agent message` and read-only `agent inbox`, never terminal input.

Send atomically rechecks actor/current target generation, exact surface registration,
binding recovery/stop gates and the shared sender/request namespace. It stores an
ordinary outbox message plus `CMUX_SEND_REQUESTED` before executing argv
`agent message <surface-UUID> --from agentflow --json -- <body>`. No workspace-title
lookup or shell interpolation occurs. A matching native ID/body/target receipt
produces `CMUX_QUEUED`; receipt failure, nonzero exit, truncation or cancellation
produces UNVERIFIED. The process deadline is at most eight seconds and stdout/stderr
are each capped at 64 KiB. `CMUX_SEND_RESULT` retains raw process output, exit,
native source/target/body and raw JSON. A late stop/generation change records the
evidence without confirming success. Reopened stores and concurrent duplicate
requests never reclaim an intent. Generic ACK/reply cannot cover an unknown send.
`CMUX_QUEUED` also requires cmux's `recipient_has_agent=true`; absent/false means
UNVERIFIED and the durable intent is not replayed.

Inbox reads `agent inbox --surface <registered-UUID> --limit 100 --json` without
mark-read. `AcceptCMUXInbox` explicitly accepts an atomic batch of native events
under the same current-binding/stop checks. IDs must be unique within the batch.
An already accepted ID is skipped only when source name/surface/workspace,
target surface/workspace, body and receiving agent/generation match its journal
receipt (UUID comparisons remain case-insensitive). Both `[old,new]` and `[new,old]`
atomically append only new IDs, including after reopening. Conflicting history,
wrong targets and new sources outside current cmux registrations reject the whole
batch. A new agentflow echo instead needs its exact successful outbound receipt.
All validation precedes writes; a write or commit failure rolls back the batch.
State/raw metadata changes do not append events or replace the first receipt.
The returned `Messages` still contains all parsed observations, including history.
Raw event JSON remains in `CMUX_INBOX_EVENT`. Case variants cannot
replace canonical wire identity keys. No text, READY token, ACK, provider hook state
or native delivery state becomes a workflow completion or a native READY proof.

This is a bounded explicit event entry, with no incremental cursor, background
poller, automatic workflow collector, fresh/reset or native session recovery.
WorkflowRunner is not installed as a cmux submit adapter; future workflow integration
must call the explicit transport API and separately validate structured results.

Current headless v1: `NewHeadlessOpenCode` and `NewHeadlessCodex` share `Headless`
in `headless.go`; legacy Codex types/options remain aliases and the empty tool
selector defaults to Codex. Only explicitly selected homogeneous persistent roles
at generation 1 execute. OpenCode uses NativeCommand plus `--session ID` for resume,
and `headless_opencode.go` validates session/message identity, step boundaries and
final text completed by `step_finish` reason `stop`. Intermediate tool-call step
text is discarded. Malformed/ambiguous/incomplete output cannot become SENT or a
workflow event. Private immutable captures, exclusive action claims, SENT result
collection and stop/restart/drain gates are the existing implementations. See
[OpenCode v1 evidence](../../docs/evidence/headless-opencode-v1-result.md) for fixture
coverage and the separate real-model boundary. Historical phase descriptions below
refer to their original default paths; explicit headless startup installs adapters
and a result collector for workflow start/resume/drain/status.

`NewHeadlessClaude` shares that core. Claude headless/native argv defaults to
strict `--print --bare`; empty args never opt into OAuth/keychain. Only the exact
resolved agent args `["--safe-mode"]` select non-bare CLI-managed authentication;
`["--bare"]` is also accepted explicitly. Other Claude args and using this opt-in
with another provider fail before execution. Args participate in the frozen config
digest and are persisted in the startup snapshot and DB; startup, persistent resume
and normal coordinator reopen use that choice even after the source config changes.
No auth fallback or login occurs. Both modes retain strict MCP/settings/extension
flags and permission prompts disabled. Full permissions and crash recovery remain
UNVERIFIED. Historical attempt-2 real acceptance used explicit non-bare OAuth argv
and does not validate the strict default. See the [default fix evidence](../../docs/evidence/claude-oauth-headless-default-fix-20261104.md)
for offline fixture DB snapshots and independently recomputable hashes.

The legacy single-OpenCode foreground API reuses Compile/Start/ForegroundLauncher and CreateContext under the coordinator's original run lock. It remains available through explicit Native options in `coordinator.Up`; default CLI startup uses the workspace launcher above. Native Registration carries the verified nonce; the store atomically publishes its `STARTUP_READY_VERIFIED` journal proof with the READY seat. The immutable startup artifact stays STARTING. A one-shot native handshake exits before coordinator IPC begins. See [coordinator contract](../coordinator/README.md) and [historical OpenCode evidence](../../docs/evidence/s4-real-up-single-seat-3117cc8e/README.md).

The [S5 thin coordinator](../coordinator/README.md) now serves assign/send/inbox/ack/reply/stop and reconcile/resume over local IPC under the existing run lock. It opens an existing store, reuses MessageService/SessionCoordinator without native adapters, and reports PARTIAL native capability. Message/stop/reconciliation CLI paths no longer open databases; status/attach use read-only handles. File-only bootstrap retains its immediate transaction generation fence without mutating database rows. The low-level APIs and historical phase evidence below remain independently callable.

`Compile(snapshot *config.Resolved, options Options) (*StartupPlan, error)` consumes a `config.Resolve` snapshot and calls `layout.Compile`. It is a pure data transformation: no filesystem reads/writes, executable lookup, process execution, cmux access, run allocation, or session registration. Resolve may read configured prompt files before this boundary.

An optional [workflow configuration](../config/README.md#可选-workflow-配置与冻结) is validated through the same `config.ValidateWorkflow` contract during source validation, layout/startup compilation and snapshot restoration. Resolve normalizes `build_review` and `approval` steps by entry/next order or pipeline route spacing and includes the detached workflow in the config digest. `StartupPlan.Config`, `RunSnapshot.Config` and the existing run config JSON freeze the same workflow without rereading its source. Frozen JSON rejects ambiguous workflow nulls/duplicate keys as well as invalid values. The [loop-core reducer and store tick](../loopcore/README.md) persist progress in the existing journal; coordinator IPC saves replies, next dispatch and cursor atomically. ACK/DONE alone never advances a workflow. Native communication remains bounded by the selected adapter's verification status. See [approval](../../examples/approval.yaml), [build_review](../../examples/workflow.yaml) and [pipeline syntax](../../how-to-use.md#工作流类型).

## S6.1 run/agent file context

`CreateContext(snapshotPath, runID) (*runcontext.Run, error)` consumes an existing private frozen `RunSnapshot`, validates the explicit run identity, and exclusively creates `<config.context_dir>/<run_id>` with a private directory for every configured agent. It publishes an `initial` immutable revision containing role/background/decisions/progress/pending documents and a SHA-256 manifest. Role instructions remain referenced in the frozen snapshot; original prompt files are never reread. The role protocol includes S5 identity/ACK/report semantics, while live generation, message and report state stays in the existing store.

The configured base must be a canonical absolute path with real ancestors; existing managed directories require 0700 and files require 0600. The API rejects symlink snapshot paths and context paths. Each bound Agent can publish/read explicit revisions within its identity, validating the run/agent descriptors and every document's schema, identity, digest and logical update time. Duplicate publications fail without replacement; interrupted revisions without a manifest remain unreadable. See the [context API](../context/README.md), [implementation plan](../../docs/plans/2026-11-03-s6.1-context-plan.md) and [S6.1 evidence](../../docs/evidence/S6.1-result.md).

This is an explicit materialization API. Startup, `_seat`, S5 adapter injection and message processing do not automatically invoke it. CreateContext performs no native tool execution, report merging, or session reconstruction. S6.2 report and S6.3 file bootstrap APIs are described below.

The plan contains `status: PLANNED`, `executed: false`, a run summary with the configuration digest, a detached resolved configuration, the compiled layout, and seats sorted by logical agent ID. Seat `order` is zero based and independent of visual order. Each seat records tool/cwd/permissions/session/role, prompt references, every ancestor split's direction/ratio/child, and wrapper binary/argv. A single seat has an empty pane path. Ratios describe the first child, matching `layout.Node`.

`prompt.text_ref` addresses frozen text in `config.agents.<id>.prompt`. A prompt file path is provenance; future launch must use the frozen text rather than reread a possibly edited source. Model and raw tool args also remain in the snapshot.

`WrapperCommand(id, tool string, options Options) (Command, error)` accepts only `opencode`, `codex`, and `claude`. Its default result is data:

```json
{"binary":"agentflow","args":["_seat","--snapshot","config.resolved.json","--agent","builder"]}
```

Pass binary and args separately to a future `exec.CommandContext`; never join them into shell source. Paths containing spaces or shell metacharacters remain literal single arguments. Role text, prompt bodies, models, tool args, and credentials are not copied to wrapper argv. The planner does not read credentials. The resolved snapshot itself contains full configured text and args, so a future launcher must store it privately.

`Options.WrapperBinary` and `Options.SnapshotPath` permit explicit artifact paths without inspecting the filesystem. Defaults are `agentflow` and `config.resolved.json`. A relative snapshot reference is relative to the future run directory, **not** the agent's cwd or the invoking terminal. No snapshot artifact is created by planning. The future launcher must select a trusted executable, write the snapshot privately, replace the reference with an absolute snapshot path, and use the seat cwd when starting the wrapper/tool. The S4.4 `_seat` entry rejects relative paths.

## S4.2 minimal startup API

`Start(ctx, plan, StartOptions{RunID, SnapshotPath}, db, launcher)` consumes an unchanged `StartupPlan`, a migrated `*store.Store`, and an injected `Launcher`. Supply a unique run ID and an absolute snapshot path in an existing private run directory (0700). Production coordination must use the existing run lock before calling this API; this API does not acquire a lock, listen on IPC, or recover a coordinator. Exclusive snapshot publication and the store's singleton run constraint reject repeated startup attempts.

```go
type Launcher interface {
    Launch(context.Context, LaunchRequest) (NativeBinding, error)
    WaitReady(context.Context, SeatSnapshot) (Registration, error)
}
```

`LaunchRequest` carries a wrapper binary and argv, `SeatSnapshot` metadata (including tool cwd), and a detached pane path. An implementation must use `Seat.Cwd` as the wrapper/tool working directory and honor context cancellation. `Start` supplies an absolute snapshot argument so agent cwd cannot change its meaning. It regenerates the wrapper from the plan's binary and materialized snapshot path; prompt, role text, model and tool args remain out of argv.

Startup is sequential in the plan's stable agent ID order:

1. Validate the plan against its frozen configuration, publish the complete private snapshot, and save the run with its configuration/digest in the existing store.
2. Call `Launch` for one seat, validate the returned session/workspace/surface binding, and keep that seat `STARTING`.
3. Call `WaitReady` with the expected identity. Only the agent's own READY is acceptable; a wrapper start, native idle or returned launch binding is insufficient evidence.
4. Check the READY's `ipc.Actor` run/agent/generation, tool, status and complete native binding. `Registry.Register` saves the current READY seat through `store.RegisterStartupReady`, which also checks stop, exit and recovery gates, then changes the in-memory state to `READY`. Continue with the next seat.

`Registry` serializes concurrent registrations. Wrong or unknown identities return `ACTOR_MISMATCH`; old and future generations return `GENERATION_MISMATCH`; duplicate READY returns `ALREADY_REGISTERED`. Unbound READYs and native bindings already assigned to another seat are rejected. Persistence failure never marks a seat READY in memory. READY is only a registration status here, not proof of effective permissions or a work result.

`RunSnapshot` is a version 1 JSON envelope containing run ID, the frozen resolved configuration, and sorted `SeatSnapshot` values: run/agent/generation, tool, cwd, role, permissions, session policy, native session/workspace/surface IDs, and status. `NewSnapshot` starts all seats at generation 1 with empty native IDs and `STARTING`. Validation checks the configuration digest, compiled layout, metadata consistency, positive generations, supported states and native binding uniqueness. Prompt files are never reread. `WriteSnapshot` atomically publishes a new 0600 file using the same temporary-file/hard-link convention as `internal/lock`; existing paths are never overwritten. `ReadSnapshot` rejects public files/directories, nonregular files, unknown fields, trailing data and invalid snapshot values.

The on-disk startup artifact remains immutable and `STARTING`; `Registry.Snapshot()` exposes the startup registration attempt. After S5.2 session transitions, SQLite is the authority for current generations/bindings; the startup registry/artifact is not rewritten. SQLite stores the frozen configuration/digest in `runs` and accepted native identities/status in `seats`; cwd/role/permissions/session are retained in the configuration and private runtime snapshot. No schema changes or dependencies are added. The run's `startup_phase` remains `STARTING`, because full team startup and reconciliation are not implemented.

If launch, binding, waiting or registration fails, `Start` stops before the next seat and returns the error plus the registry after initialization. Already registered seats stay committed; unconfirmed seats are not recorded as READY. The initial artifact and run record may remain after failure. No automatic cleanup, relaunch, rollback of external processes, generation advancement or resume is implemented. A subsequent fresh attempt against the same artifact/store is rejected, avoiding blind duplicate launches.

S4.2's fake tests exercise registration, wrong identities/generations, cancellation, partial progress and reopen persistence. S7.2 records LAUNCH_REQUESTED before Launch and LAUNCH_BOUND after a validated native result, with no READY seat until registration. The `ForegroundLauncher` below supplies the explicit single-seat implementation; installed OpenCode and Codex each passed a real JSONL startup and persisted READY under a run lock (see the S4 evidence below). Default CLI `up` now creates the full workspace through the launcher above. A native-authenticated continuous TUI READY route and effective permissions remain unimplemented or unverified.

## S4.3 status, attach query and stop intent

`ReadStatus(ctx, db, QueryOptions{RunID, SnapshotPath, LockPath}) StatusResult` reads a consistent SQLite run/current-seat view. Open with `store.OpenExisting(path, false)` for read-only access without creation, migrations or journal-mode changes. Frozen database configuration supplies planned STARTING seats; current database generations, status and native bindings take precedence. The artifact is independently validated against the run/config identity. Missing or invalid artifacts produce stable diagnostics while preserving readable database evidence. Source team and prompt files are not read.

`run.startup_phase` is the stored phase. `run.startup_progress` is STARTING, PARTIAL or REGISTERED according to persisted registrations. `run.status` follows that progress unless `stop_requested` is true, when it is STOP_REQUESTED. None of these prove process liveness. Overall status remains PARTIAL and runtime communication UNVERIFIED. Lock inspection exposes no token, rejects a different run, and optionally probes PID and socket (250ms timeout, no protocol bytes). Missing locks authorize no recovery or cleanup.

`QueryAttach(StatusResult) AttachResult` copies unambiguous recorded run/workspace/socket coordinates. Conflicting workspaces yield an empty coordinate and diagnostic. It always returns `attached=false`, PARTIAL and UNVERIFIED, even for fake launcher bindings or a connectable socket. It never invokes cmux.

`RequestStop(ctx, db, runID) (StopResult, error)` records only a durable stop intent. The store atomically sets the flag and appends one STOP_REQUESTED event, serializing concurrent requests with BEGIN IMMEDIATE. Retries return the same original event sequence and timestamp, including after process restart. A journal failure rolls back the flag. `executed=false` explicitly excludes actual shutdown. No process kill, workspace close, snapshot deletion, retries, generation changes or restart occurs.

CLI `status/attach --run ID [--run-dir DIR] [--json]` uses read-only state.db/config.resolved.json/coordinator.lock diagnostics. Stop now requires `--agent/--generation/--request-id` and forwards exclusively to the coordinator, which calls `Store.RequestStopAsActor` to validate current identity, deduplicate and save the intent/receipt atomically. It reports PARTIAL with PROCESS_STOP_UNIMPLEMENTED and executed=false. Default directory follows XDG state selection. Status exits 0 when persisted state can be read without diagnostics; artifact errors exit 1. Attach always exits 1. Stop exits 0 only after the intent transaction commits. All usage errors exit 2. Historical read-only diagnostic evidence remains in [S4.3](../../docs/evidence/S4.3-result.md).

## S4.4 seat entry and cmux command data

`LoadStartupSeat(absolutePath, agentID) (SeatSnapshot, error)` reuses strict `ReadSnapshot` and checks the immutable initial startup envelope. Every seat must be STARTING at generation 1 with no native binding; a live snapshot, even one otherwise valid for status, cannot authorize startup. Unknown IDs return `SEAT_AGENT_UNKNOWN`; file/encoding/digest/metadata validation returns `SEAT_SNAPSHOT_INVALID`; a valid snapshot containing live state or a future generation returns `SEAT_STARTUP_INVALID`. Errors contain fixed code/path/message fields and exclude decoder details and private content. It never reads source team/prompt files or looks up/executes tools.

The hidden CLI `_seat --snapshot <absolute> --agent <id>` accepts exactly these two separate long options in either order, once each. Usage errors exit 2 without stdout. Runtime failures emit JSON `{status:"FAILED", executed, error:{code,path,message}}` and a prefixed diagnostic, exiting 1. `executed` reports whether a child ran; it never means READY. OpenCode/Codex now require explicit foreground options described below. Claude still returns `SEAT_TOOL_ADAPTER_UNIMPLEMENTED` before executable lookup. Missing options return `SEAT_NATIVE_TARGET_REQUIRED`.

`BuildCMUXCommand(plan *StartupPlan, id string, target CMUXTarget) (CMUXLaunchCommand, error)` validates the canonical plan, frozen digest and absolute snapshot reference without I/O. It returns detached tool/cwd/PanePath/target metadata, a `CMUX` binary/argv pair and a separate `Wrapper` binary/argv pair. Only supported tools can pass; altered order, wrappers, layout, metadata or unknown seats/targets return an error with no partial command.

Supply an explicit `WindowID` as a UUID or typed ref (`window:N`); split targets also require typed workspace/surface refs or UUIDs. No ambient cmux target is selected. For the first visual leaf (all PanePath children 0), omit workspace/surface to build `cmux --json --id-format uuids new-workspace --window ID --cwd CWD --focus false`. For a new right/lower subtree, supply the verified existing left/upper sibling surface and `SplitDepth` pointing to its child-1 ancestor; remaining path children must be 0. Horizontal maps to `new-split right`, vertical to `new-split down`, with explicit window/workspace/surface and focus false. A future launcher must traverse visual leaf order and verify returned targets; `Start`'s seat-ID order is not a cmux topology execution order.

The historical builder's verified evidence is [cmux help](../../docs/evidence/S1-probe-final/cmux-help.json) and the existing [right](../../docs/evidence/S1-runtime-b7bf333c/codex-create-surface.json)/[down](../../docs/evidence/S1-runtime-b7bf333c/claude-create-surface.json) split argv. This older `BuildCMUXCommand` builder retains cwd/ratios as metadata and emits no shell transport. Default workspace creation now uses the separate `BuildCMUXWorkspaceCommand`/`LaunchCMUXWorkspace` API above, with whole-layout cwd/ratio data and explicitly quoted TUI commands. Tests execute only fake cmux. The launcher itself does not establish native READY; the CLI workflow path separately opens `OpenCMUXTransport`, which binds supported persistent OpenCode sessions before reporting the coordinator ready.

## S4 foreground OpenCode/Codex wiring (partial)

`ForegroundLauncher{Options: NativeOptions{Target, ToolBinary, CMUXBinary}}` implements `Launcher` for **one owned foreground terminal and a single-seat plan**. Supply an absolute wrapper binary in `Compile` and absolute native/cmux executables in options. It validates canonical launch data through `BuildCMUXCommand`, executes the wrapper via `exec.CommandContext` with the frozen cwd, and passes options through `AGENTFLOW_NATIVE_OPTIONS`. It never executes the topology command, shell text or a command typed into a different terminal. `tree --window ... --workspace ...` must report this target as the actual caller; targeted `identify` alone cannot prove ownership because its caller field reflects the requested surface. Short refs are normalized to UUIDs from that response. The adapter does not use persisted cmux restore commands as current identity evidence.

`ExecuteNativeSeat` reuses `LoadStartupSeat`/`ReadSnapshot`, checks that cwd exists, builds a native argv, and submits role instructions plus a cryptographic nonce over stdin. OpenCode uses `run --format json --dir CWD`; Codex uses `exec --json --color never --skip-git-repo-check --cd CWD -`. Model is an independent `--model` value. Configured `full` maps to OpenCode `--auto` or Codex `--dangerously-bypass-approvals-and-sandbox`; actual permissions remain UNVERIFIED. `default` leaves native defaults in place. Optional raw args currently accept exactly one `--variant VALUE` (OpenCode) or `--profile VALUE` (Codex); unknown args and managed flag overrides fail before tool execution. Initial fresh/persistent policies both create their first session; subsequent reset/resume is not installed by this adapter. `NativeExecutable` optionally resolves real CLIs while skipping relative PATH entries and cmux shims.

`ParseNativeReady` accepts bounded JSONL only. Native OpenCode `sessionID` or Codex `thread.started.thread_id` is retained; changed IDs, missing completion, native errors, malformed output and truncated capture fail. Only text from native assistant events can acknowledge READY. Codex requires `item.completed`/`agent_message` and `turn.completed`; OpenCode requires a `text` part and final `step_finish` with reason `stop`. The exact JSON response must echo run/agent/generation/tool/status and nonce. The adapter enriches that response with independently collected native IDs; the model cannot choose them. Process start, native ID, idle, user echo and tool output alone are insufficient. Wrapper/native timeouts and cancellation are bounded, and native stderr/private text is excluded from public diagnostics. A received native ID remains inspectable on failed READY; registry state remains STARTING.

`Launch` waits for the foreground handshake subprocess, then exposes its native binding; `WaitReady` independently checks the saved result against the expected seat. `Start` still performs final registration/persistence validation. A launcher instance is single-use, including failures; snapshot/store exclusivity prevents `Start` replay. Use `Start` under the existing run lock. Direct `_seat` calls are a low-level executor and do not provide store-level replay protection or register a seat. No socket service, schema, dependency, login or configuration mutation is added.

`NativeOptions.EvidencePath` optionally saves the pre-execution challenge/command/location and bounded native stdout/process outcome as two JSONL records. It requires a new exclusive 0600 file beside the private startup snapshot; an existing path or an invalid parent fails before native execution. Each record is synced; a recording failure cannot produce READY. Native stderr and full role instructions are excluded. The saved stdout can be independently checked with `ParseNativeReady` against the saved challenge; it is audit evidence, not a replay input for `Start`.

Process integration tests build an actual wrapper and native fixtures, validating safe cwd/argv, READY registration, native IDs, wrong-target refusal, stale generation, missing READY, child failure, cancellation and evidence boundaries. Installed OpenCode and Codex each separately passed a real one-shot JSONL → `_seat` → `Start` → persisted READY chain in one explicitly created temporary cmux surface, with exact actor/generation/nonce checks, cross-process lock contention and duplicate Start rejection before/after SQLite reopen. See [real single-seat evidence](../../docs/evidence/s4-real-start-ready-6c5bcfcb-2f9c-45c4-acd1-2409bafa82f7/README.md) and `probes/real-seat`. The prior [TUI ACK evidence](../../docs/evidence/s4-real-opencode-codex-83a87513-a6ab-47fe-b23d-1d5e466569a6/README.md) remains separate. Team TUI launch is implemented above; continuous registration, live attach/stop, authenticated native events and TUI S5/S7 adapter installation remain unimplemented/unverified. Claude headless has its own explicit adapter.

## S5.1 message persistence service

`NewMessageService(db)` exposes `Assign(ctx, SendMessage)`, `Send(ctx, SendMessage)`, `Reply(ctx, ReplyMessage)`, `Inbox(ctx, ipc.Actor, after, pending)` and `Ack(ctx, ipc.Actor, requestID, messageID)`. It accepts registered current seats in the existing store. Assign records a new work intent, send records a supplement and reply targets the original sender/generation through reply_to. Outbox persistence is shared with `SaveOutbox`. S5.2 extends Assign through the session coordinator below. `ValidateActor` supplies a current-store validator compatible with `ipc.ServerOptions.ValidateActor`, mapping identity failures to typed IPC codes; service writes repeat validation inside their transaction. A future coordinator must authenticate/bind claimed identity to transport; no server/adapter is started by the constructor.

Messages use sender/request_id body deduplication, retaining the original routing and report fields. ACK uses the same namespace with journal receipts; repeating the same request is write-free, while a distinct key confirming an already ACKED message records only its request receipt and returns the original confirmation event. Inbox returns a stable sequence order and an empty array when nothing matches. Store/CLI tests cover generation changes, reply authorization, rollback, concurrency, reopen persistence and output stability. See [store API](../store/README.md) for error and transaction details.

CLI `assign/send <recipient>`, `reply/ack <message-id>` (also `message ack`) and `inbox` require explicit `--run/--agent/--generation`; writes require `--request-id`, and send/assign/reply require a UTF-8 `--body-file`. All support `--run-dir/--json`. Inbox adds `--pending/--after`, send/assign add `--recipient-generation`, reply adds `--status`. An explicit ACK reports ACKED receipt only; result reports are stored verbatim. Every CLI envelope retains runtime_communication=UNVERIFIED.

## S5.2 session coordination

`NewSessionCoordinator(db, reset, submit)` exposes `Assign(ctx, SendMessage)` and `Advance(ctx, originalSender, messageID)`. `NewMessageServiceWithAdapters` wires the same coordinator into MessageService. `SendMessage.Session` or its `Mode` alias selects fresh/persistent; omitted values select the frozen recipient configuration inside the save transaction. Conflicting aliases fail. The selected session is saved in the existing MESSAGE_STORED journal details, atomically with the message and delivery. Retries preserve that decision and the original body/route, including after generation changes. Sender identity must still be current.

`ResetAdapter` provides `Inspect`, `Reset` and `WaitReady`. Inspection includes exact actor/native identity, verified status, busy, literal draft and incomplete input. Unknown/mismatched inspection queues the work. Reset receives no work body: `ResetRequest` contains the previous binding, next generation, message/request IDs, frozen `ResolvedAgent` (prompt text, source prompt_file, cwd, permissions and args) and `<context_dir>/<run_id>`. Source prompt files are never reread. Reset/submit implementations must protect input again at the native operation boundary to close user-edit races.

The store serializes each action claim across connections. Busy seats, input drafts, unknown input, earlier assignments and stop intent queue messages without deleting them. Fresh commits RESET_REQUESTED before invoking Reset. Returned native session/thread must be new, with the same workspace/surface; independent READY must match the expected run/agent/next generation/tool/native/status. Only then does one transaction preserve the old seat, install the new current READY seat, and move the same delivery to the new generation with reset_phase=READY. Historical native session reuse and duplicate/late confirmation fail. Persistent skips Reset/WaitReady and retains the current binding. Both modes inspect input before submitting when adapters are present.

Submission is a separate adapter call after a committed SUBMIT_REQUESTED and attempt increment. Matching accepted submission evidence records SENT/channel and marks the seat BUSY/current_message_id. ACK confirms receipt only. A new terminal reply (DONE/FAILED/BLOCKED_CONFIRM) for the exact active work releases the seat to READY; changing status on an idempotent reply retry cannot release it. Status includes BUSY as a registered seat and reads the current database binding. No task table or acceptance step is added.

`NewNativeSubmitAdapter(NativeSubmitOptions)` is an explicit Go injection option for OpenCode/Codex foreground CLI resume. Supply a complete READY seat, matching frozen agent configuration, an absolute native executable, and a `NativeSubmitGuard` that verifies and exclusively protects the exact input until release. No native guard implementation or default CLI wiring is provided. The adapter reuses `NativeCommand`, submits the original message body exclusively through stdin, checks the bound actor/generation/tool/native identity, and requires matching session plus assistant/completion JSONL evidence before returning an accepted Submission. Missing output, identity drift, truncation, cancellation and failed execution cannot confirm delivery; all adapter errors preserve the coordinator's existing unknown/no-replay behavior. The adapter is bound to a single generation/session and does not rebind automatically. See the [design](../../docs/plans/2026-11-03-native-submit-adapter-design.md) and [fixture evidence](../../docs/evidence/native-submit-adapter-result.md). Continued TUI operation and multiple windows with persistent agent sessions remain UNIMPLEMENTED; fixture PASS is not native PASS.

**cmux input ownership guard: UNIMPLEMENTED (0.65.2, build 113).** The live API inventory and matching upstream source expose no proven terminal input ownership/lease primitive. `surface.input_state` reads prompt/lifecycle state; `terminal.input.exactly_once.v1` deduplicates identified stream units. Neither excludes user edits, other streams or native CLI submissions for the duration of a foreground resume. Participant disconnect and terminal sizing ownership also provide no such lease. Do not substitute an advisory mutex/file lock, screen polling, or a successful input-state snapshot for `NativeSubmitGuard.Acquire`. See the [versioned investigation and read-only evidence](../../docs/evidence/cmux-input-ownership-guard-2026-11-04.md). This is a capability boundary, not a new public error code or an enabled adapter.

Queued work can be reconsidered by the original current sender using Advance or the same assign request. A queued assignment with no native action can follow the current generation after an earlier work finishes. Completed reset work stays bound to its confirmed generation. RESET_REQUESTED, SUBMIT_REQUESTED, SENT and ACKED are never replayed by Advance. Failed or mismatched native evidence leaves the intent intact and records UNVERIFIED; cancellation also preserves the already committed intent even if the diagnostic write cannot complete. S5.3 adds the durable decision gate below; S7 automatic recovery remains pending.

The default constructor/CLI supplies no native adapters. Assign therefore reports STORED/QUEUED with session and coordination=RESET_ADAPTER_UNIMPLEMENTED or SUBMIT_ADAPTER_UNIMPLEMENTED, reset_phase=NONE, channel=UNVERIFIED and attempt=0. Success means saved/queued. Ordinary Send/Reply remain persistence operations and never invoke the adapters. Fake adapters exercise real SQLite transitions; native OpenCode/Codex/Claude/cmux are not launched. See [S5.2 evidence](../../docs/evidence/S5.2-result.md).

## S5.3 durable reconciliation and recovery decisions

Workflow dispatches are an exception to ordinary untouched queued assignment relocation: their saved target generation is retained, and the action transaction also verifies the current workflow cursor/active phase. A later generation or superseded/paused dispatch requires explicit coordination instead of automatic takeover.

`SessionCoordinator.Reconcile(ctx, originalSender, messageID)` and `MessageService.Reconcile/Resume` return `store.RecoveryDecision`. They query/persist evidence without calling Inspect, Reset, WaitReady or Submit. Advance also reconciles before inspecting or claiming native work; every later side effect still requires the transactional BeginSessionAction gate. A durable SAFE_TO_CONTINUE decision is advisory and cannot be reused as an exclusive execution claim.

The decision includes delivery/reset/attempt/channel, current generation/tool/native/workspace/surface, seat status/current work, stop intent and evidence/decision journal receipts. States are SAFE_TO_CONTINUE (recheck input and claim RESET or SUBMIT), QUEUED (preserved blocker), UNKNOWN_NEEDS_RECONCILE (requested action outcome missing or contradictory durable evidence), REJECTED_OLD_GENERATION, SENT and ACKED. Requested actions remain unknown across restart even if NoteSessionUnknown never ran. SENT/ACKED require matching journal evidence and are never replayed. A confirmed READY authorizes only the subsequent submit path, never another reset. Stop intent prevents native calls and queued relocation.

Only coordinated QUEUED work with reset NONE, attempt zero and no native action events can follow a newer binding. PENDING work tied to an old generation and any started/READY work keep their target and cannot execute. Historical ACK/reply/reset/submit confirmations are rejected and audited once per message/generation/action; no rejected body is stored.

CLI `reconcile|resume <message-id> --run ID --agent ID --generation N [--run-dir DIR] [--json]` forwards decision queries/writes through coordinator IPC, without CLI request IDs. Both return status RECONCILED, executed=false, runtime_communication=UNVERIFIED and exit 0 after persistence/query completes, including UNKNOWN/QUEUED/old-target decisions. Identity, missing-record, coordinator and store failures exit 1; usage errors exit 2. Resume performs no real recovery. External evidence acquisition, authenticated native runtime wiring and S7 scheduling remain unimplemented. See [S5.3 evidence](../../docs/evidence/S5.3-result.md).

## Workflow runner

`NewWorkflowRunner(sessions *SessionCoordinator)` exposes `Advance(ctx, actor, event)` and `Reply(ctx, ReplyMessage, event)`. The embedding process explicitly supplies the existing session coordinator/adapters and invokes the same workflow start/resume or structured reply events. A newly accepted event atomically stores its cursor, reply (if present), next dispatch and frozen recipient session mode using `store.AdvanceWorkflowWithSessions` / `ReplyWorkflowWithSessions`. The session choice lives in existing `WORKFLOW_DISPATCH_STORED` journal details; no table, schema migration or queue is added.

After that commit, the runner synchronously calls `SessionCoordinator.Advance` for just the newly produced current dispatch, as its original lead sender. Input/busy/draft checks, stop/exit/recovery gates, independent fresh READY, exclusive action claims and submission evidence retain their S5 behavior. A successful terminal reply releases its current seat before the next dispatch, allowing the reviewer to execute the next step. The runner never invents handoffs, acceptance judgments, ACKs or retry/resume events. The reducer's reviewer `approve` event is the explicit decision; there is no extra decision agent/phase.

`WorkflowRunResult.Workflow` is the durable event receipt. `Dispatch` is the submission/queue observation from this call and is absent on historical event retries or terminal/paused transitions. `DispatchError` means the event already committed but the dispatch operation failed; the receipt remains available and no rollback or automatic retry is attempted. Unknown/rejected/mismatched submit evidence retains `SUBMIT_REQUESTED` and its existing coordination diagnostic. Missing adapters/input blockers retain queued work. Exact event retries return only the original workflow receipt, across restart and stop, and never reconsider that queued/unknown/failed dispatch. Opening the runner does not scan or drain existing outbox entries. Explicit recovery remains a separate API.

Every workflow action claim rechecks the latest cursor and original target generation in the database transaction. Even untouched queued workflow dispatches cannot relocate to a replacement generation. Fresh may advance only through its own confirmed reset/READY transaction; it does not authorize general generation takeover. Outstanding messages still require their original current sender, so resetting a lead that owns those messages is not supported as automatic workflow continuation. The fake complete-flow fixture uses three registered persistent seats.

`coordinator.OpenWithAdapters(ctx, dir, runID, reset, submit)` wires this runner to the existing private IPC handlers and preserves the single foreground owner lock. The default `Open`, real single-seat `up` and CLI do not install this runner/adapters. `Submit` must return before the agent sends its event: synchronous reentrant workflow replies from inside an adapter are unsupported. The embedding transport must authenticate events and protect native input at the operation boundary. No native adapter, background service, task table, MQ, DAG, workflow CLI or event collector is supplied. Fake local IPC success remains PARTIAL/UNVERIFIED for native communication. See [runner evidence](../../docs/evidence/workflow-runner-fake-e2e-result.md).

## S6.2 independent reports and compact lead merge

`NewReportService(db, contextRun)` binds existing S5 state to a run opened/created by `internal/context`. It exposes `PublishEvidence(ctx, actor, name, purpose, body)`, `Publish(ctx, actor, Report)`, `Read(ctx, actor, ReportRef)`, `Review(ctx, leadActor, []ReportRef)` and `Merge(ctx, leadActor, baseRevision, newRevision, []ReportRef)`. No report CLI is added. Open the existing state.db and use `runcontext.Open(contextDir, runID)` after a process restart; no new migration or dependency is needed.

Every operation checks the current registered actor and matching frozen run/config digest/lead inside an immediate SQLite transaction spanning file operations. Publish also requires exact report author/run/generation. Optional message_id/request_id references the originating lead dispatch; request-only is resolved by lead/recipient/request ID. Message recipient/current target generation, both actors and fresh READY are verified using S5 rules. Read permits only author or lead; Review/Merge require the frozen lead and current executor authors. A successful Merge appends a `REPORT_MERGED` journal authorization bound to the exact report, SHA, dispatch and merge revision; that authorization permits the lead to restore the historical merged report after the executor advances, but does not permit new writes or reviews by a stale executor. File-only historical reads remain available for audit, while the runtime rejects stale generations and invalid persisted message bindings.

Reports and evidence are private immutable files with manifest/SHA verification; duplicate report/request/revision returns `runcontext.ErrExists`. Reports require compact summary/next and 1–8 verified evidence references. Merge selects at most four reports, preserves source artifacts, and writes only statuses, summaries, next steps and evidence/source report references to a new lead revision. Existing context size limits still apply. Results are always `INTEGRITY_VERIFIED` / `agent_claim`; a DONE claim never becomes PASS. Evidence, dispatch and prompt bodies are not copied into lead context. S5 ACK/delivery/seat/journal are unchanged, and no native tool or cmux operation is performed. See [context contract](../context/README.md) and [S6.2 evidence](../../docs/evidence/S6.2-result.md).

## S6.3 file session bootstrap

`NewBootstrapService(db, contextRun)` exposes equivalent `Bootstrap(ctx, actor)` and `Rebuild(ctx, actor)`. Within one immediate store transaction it verifies the current run/agent/generation, frozen configuration digest and membership, context directory, lead, seat tool/workspace and full role protocol. It then reads the latest complete context and current own report, or revalidates a lead's merged executor reports and dispatch bindings. Corrupt/incomplete visible revisions, stale actors/authors, cross-agent context references, changed roles and damaged reports/evidence fail before bootstrap publication. Native availability is not required; persisted seat state is explicitly evidence only.

The result has status BOOTSTRAP_READY, a content-addressed private JSON path and a compact Bootstrap containing role/cwd/permissions/session policy, progress/pending, report/evidence references, recorded seat coordinates, recommendation and next step. Existing pending text takes priority over report next fields. Fresh recommends a fresh session and independent READY; persistent recommends verifying reuse and starting from files if unavailable. Stop intent remains visible. Both modes retain executed=false and runtime_communication=UNVERIFIED. The service has no native adapter and changes no S5 state or source artifacts. Prompt sources and full chats are never read/copied; the frozen prompt remains a reference, with its digest checked against the store's frozen configuration. Actual injection must ensure that reference is available.

CLI `rebuild|bootstrap --run ID --agent ID --generation N [--run-dir DIR] [--json]` opens existing state without migration, locates context using frozen configuration and generates/reads `.bootstrap/g-N/<sha>.json`. Exit 0 means file availability only; missing/stale/corrupt identity or context exits 1, invalid usage exits 2. Repeated calls and new processes return stable bytes/path for unchanged inputs; existing corrupt bootstrap bytes are rejected. See [S6.3 evidence](../../docs/evidence/S6.3-result.md).

## S7.1 finite recovery ticks

`NewRecoveryRunner(coordinator, RecoveryPolicy, clock func() time.Time, RecoveryDependency)` exposes `Recover(ctx, actor, messageID) (store.RetryDecision, error)`. Use `DefaultRecoveryPolicy()` for three attempts with waits of 1s, 5s, 30s. A custom policy requires a positive finite maximum and exactly one positive delay per attempt. The first call journals WAITING; a due tick atomically reserves one attempt before probing dependencies. The original policy, attempt count and deadline survive reopening the database. The runner does not sleep or create a background service. An embedding coordinator decides when to invoke the next tick.

`RecoveryDependency.Check` receives the current persisted binding/evidence and must explicitly set `CanContinue=true`. A typed `RecoveryError` distinguishes dependency unavailability from permission/configuration/auth failures; untyped probe errors and unspecified readiness fail closed to manual reconciliation. An affirmative probe still requires fresh S5 reconciliation, binding equality, trusted native input inspection and the existing exclusive action claim. The same action transaction verifies that the recovery reservation is still current. A recovery tick performs at most one reset (including independent READY) or one submit. A confirmed reset waits for the next tick before submitting. ACK stays explicit.

Receipts contain RECOVERABLE, DEPENDENCY_UNAVAILABLE, NON_RECOVERABLE or NEEDS_MANUAL_RECONCILE classification, reason, attempts, next_attempt_at, frozen policy and reconciliation evidence. States are WAITING, ATTEMPT_RESERVED, EXHAUSTED, STOPPED, MANUAL_RECONCILE or COMPLETE. COMPLETE means only durable SENT/ACKED evidence, never task completion or verified tool recovery. Attempts count recovery opportunities, not delivery body submissions. All changed decisions append RECOVERY_DECIDED and atomically update the existing delivery.next_retry_at. Repeated waiting/terminal calls return the same receipt. No schema/dependency/task table is added.

Stop intent, old delivery generations, permission/configuration/auth errors and cancellation prevent native actions. Recovery uses reconciliation without S5's optional untouched-queue relocation. Cancellation preserves a durable receipt with a separate context limited to 5 seconds, while probes and native adapters always receive the caller context. Native request intents left by cancellation remain UNKNOWN_NEEDS_RECONCILE and take precedence when finalizing. A crash after reservation leaves ATTEMPT_RESERVED with manual classification; retries never steal it. Reviewing such interrupted attempts and resuming native sessions are outside S7.1. Missing adapters/probes retain UNIMPLEMENTED/UNVERIFIED. Bootstrap/Rebuild is independent file context recovery and grants no execution rights. No recovery CLI is added; reconcile/resume retains its decision-only semantics. See [S7.1 evidence](../../docs/evidence/S7.1-result.md).

## S7.2 original session and binding recovery

`NewSessionRecovery(coordinator, BindingRecoveryAdapter, RecoveryPolicy, clock, *BootstrapService)` exposes `Recover(ctx, caller, target ipc.Actor, requestID) (store.BindingRecovery, error)`, `NoteExit(ctx, SessionObservation)` and `ValidateBinding(ctx, BindingReady)`. Recovery targets logical identity, never pane order. Only the current lead or seat owner may request recovery. A trusted startup coordinator may use the frozen lead at generation 1 before any lead seat exists; transport authentication remains the embedding coordinator's responsibility. A BootstrapService, when supplied, must share the recovery store.

The first tick records WAITING. At its persisted deadline an exclusive ATTEMPT_RESERVED permits a read-only `InspectOriginal` on the exact recorded session/thread. Observations must verify run/agent/generation/tool/permissions/config digest. AVAILABLE with unchanged coordinates completes with the original binding/generation and no create/reset/READY call. A location change retains native session identity and generation but requires `WaitBindingReady`; the independent claim must also match the complete new native binding and READY status. A partially launched LAUNCH_BOUND seat follows the same path and registers its original generation only after READY.

UNAVAILABLE must identify the exact previous binding. A replacement requires the existing S6.3 BootstrapService to validate the file checkpoint, then a transactional CREATE_REQUESTED receipt that rechecks all S5.3 deliveries/journal, stop, exits, current binding and active work. `Create` receives the frozen ResolvedAgent (including model/tool/account arguments), context path, prior binding, proposed generation and checkpoint. A matching independent READY commits the next generation and current binding atomically. A seat with active confirmed work requires manual review before replacement; queued delivery routing remains owned by explicit S5.3 reconciliation. Recovery does not submit work, reset or ACK.

Every receipt, policy, attempt count and deadline is durable in the existing journal. The first policy and spent budget are retained per agent/generation across request IDs and restarts. Competing requests return the active receipt; WAITING cannot shorten the delay, and ATTEMPT_RESERVED/CREATE_REQUESTED cannot be stolen. Unknown native/READY outcomes become MANUAL_RECONCILE; missing history or an interrupted LAUNCH_REQUESTED without a bound native is never proof that launch did not happen. Dependency failures consume the finite budget, while permission/configuration/auth faults and cancellation stop continuation. Native adapters receive the original context; durable cancellation/unknown writes use a detached context capped at five seconds.

Rebinding appends BINDING_CHANGED with the full previous/current binding, preserving old locations even within one generation. Previous native generations remain in seats. `NoteExit` rejects and deduplicates audits of old location/generation events; USER_CLOSED and verified terminal exits are sticky, unknown exits record EXIT_UNVERIFIED and block automatic continuation. Native close events must supply trusted exact native identity; absence of an exit reason never implies a recoverable crash. S5 action claims/confirmations, S7.1 reconciliation and late startup READY honor these gates. Transport integrations must authenticate and call ValidateBinding for native events: an actor-only ACK/result API cannot distinguish locations in the same generation. No authenticated native transport is installed by this API.

Tests use real SQLite files and injected adapters/clocks, including checkpoint continuation, location/native changes, partial startup, mismatched READY/permissions/digest, unknown create/submit results, close/stop races, late events, rollback, independent handles and database reopen. No real tools/cmux, scheduler or wrapper restart is included. See [S7.2 evidence](../../docs/evidence/S7.2-result.md).

S7.3 adds 30 table-driven local fault cases across runtime/store/IPC and runs the complete existing suite plus race checks for runtime/store/context/IPC/lock. Local validation is PASS; native three-tool e2e, effective permissions, live attach/stop and real recovery remain PARTIAL/UNVERIFIED. Reproduce with `go test -v ./internal/runtime ./internal/store ./internal/ipc -run TestFaultMatrix -count=1` from the project root. See [S7.3 final evidence and remaining work](../../docs/evidence/S7.3-result.md).
# Headless Codex adapter v1

`NewHeadlessCodex(frozenConfig, HeadlessCodexOptions)` supplies Launcher,
SubmitAdapter, an inspection-only persistent ResetAdapter, and WorkflowResultAdapter.
It owns foreground `exec --json`/`exec resume` child processes, with prompt input
through stdin and no shell or terminal injection. Configuration is detached through
Compile; native command construction reuses NativeCommand and its option allowlist.
Fresh sessions and other tools are rejected before initialization.

Start records its existing LAUNCH_REQUESTED/LAUNCH_BOUND/READY proof. Headless READY
requires a matching final assistant JSON nonce, actor and actual native thread ID.
Codex commentary before the final answer is ignored for result selection; the
entire stream is still parsed for failure, identity mismatch and completion.
Logical workspace/surface IDs start with `headless:` and do not refer to terminals.

SessionCoordinator commits SUBMIT_REQUESTED before Submit and SENT before native
workflow collection. Each invocation exclusively reserves a private capture file
before execution and saves bounded stdout/process outcome; interrupted or missing
captures cannot authorize a new process. WorkflowReply reads that message's capture,
validates actor/thread/message binding, and decodes the final assistant JSON.
Only `handoff`, `approve`, `changes`, `retry`, `blocked` can be collected; duplicate,
unknown or incorrectly cased result fields are rejected. The existing reducer
validates phase, revision, handoff and explicit acceptance judgments. A zero exit,
plain DONE or turn.completed alone never advances the workflow.

WorkflowRunner's collector runs after SENT, outside the submission callback, and
commits reply/event/next outbox with ReplyWorkflowWithSessions. Native replies use
stable `headless:<message-id>` event/request IDs. Explicit lead Drain can finish
the SENT-before-event crash window without resubmitting. SUBMIT_REQUESTED/unknown,
missing/invalid results, old generations, stopped runs and ended workflows cannot
produce another submission. Opening and exact lead event retries never drain.
Captures are same-user evidence files, not a cryptographic authentication boundary;
content references and reviewer claims are not independently verified.

See [coordinator usage](../coordinator/README.md#explicit-headless-codex-v1) and
[v1 result](../../docs/evidence/headless-codex-v1-result.md). Earlier sections describe
the original TUI/default paths and their unchanged verification boundaries.
