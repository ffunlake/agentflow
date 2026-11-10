# cmux terminal input ownership investigation

Task: `cmux-input-ownership-guard`. Date: 2026-11-04. Role: 资深研发.

**Result: real cmux TUI `NativeSubmitGuard` remains UNIMPLEMENTED.** The installed cmux 0.65.2 (113), commit `12023d89f`, exposes no demonstrated exclusive terminal input ownership/lease primitive sufficient for the current guard contract. The live inventory, public contract and matching source establish screen inspection and per-stream input deduplication, not exclusive access lasting through native CLI resume and result parsing. No production guard or advisory substitute was added.

The [machine-readable evidence](cmux-input-ownership-guard-2026-11-04.json) contains the full relevant capability/method projection, response counts/hash and an observation of this worker's own surface. This conclusion is limited to the observed version/API; it is not a claim about every possible future cmux release.

## Required guarantee

`internal/runtime/native_submit.go` requires `Acquire(ctx, SeatSnapshot)` to verify the exact actor/generation/native binding and protect its input against user edits and competing submissions until `release`. `Submit` holds that protection across `captureNative` and native result parsing. A READY seat, an idle lifecycle, an empty prompt, a run coordinator lock or a native transcript writer lock alone cannot supply that guarantee. The native adapter starts a foreground CLI resume, so fencing terminal keystrokes alone would also not establish exclusion against other native session writers.

## Read-only local observations

Commands executed successfully:

```text
cmux --version
cmux --help
cmux capabilities --json
cmux docs api
cmux identify --json
cmux help agents
cmux rpc surface.input_state '{"workspace_id":"D9F41024-5C40-4E98-8B34-BD39785B0C81","surface_id":"CC1C9123-FA9B-42CD-98C0-4974FD9BC3CF"}'
```

The explicit input-state target is this worker's own environment-bound surface, `surface:1000060016`; it is not a user test target. No screen text was read. `identify` returned cmux 0.65.2/build 113 and the worker's caller identity; its focused identity was not used as a target.

The server returned 57 capabilities and 512 methods. Relevant capabilities include `terminal.input.ordered.v1`, `terminal.input.latency.v1`, `terminal.input.exactly_once.v1`, `terminal.shared_sizing.v1` and `simulator.ownership.v1`. The terminal/surface method inventory includes writes, input inspection, sizing and participant disconnect, but no advertised input acquire/release, lease, owner, claim or lock method. `agent.restore.admit` / `agent.restore.release` concern agent restore launch admission, not a terminal input lease. `simulator.ownership.v1` concerns the Simulator domain and supplies no demonstrated terminal ownership guarantee.

The own-surface snapshot returned:

```json
{
  "agent": false,
  "blocks_typing": false,
  "lifecycle": "unknown",
  "state": "unknown",
  "terminal": true,
  "waiting_on_human": false
}
```

This is a direct example of why `blocks_typing=false` cannot mean verified empty/idle input. Even an `empty`/`idle` response would still be a snapshot without ownership. Method-name absence is supporting evidence; the behavioral source below is the stronger reason to reject the proposed substitutions.

## Same-version upstream evidence

The source links are pinned to the full commit corresponding to the installed CLI's advertised short commit: `12023d89feb5425c766ba05e8d8e4cfc176e3991`. The local server independently reports matching version/build through `identify`. Upstream source is review evidence, not a byte-for-byte verification of the installed app binary.

| API or candidate | Evidence | Ownership consequence |
|---|---|---|
| `surface.input_state` | [TerminalController+SurfaceInputState.swift](https://github.com/manaflow-ai/cmux/blob/12023d89feb5425c766ba05e8d8e4cfc176e3991/Sources/TerminalController%2BSurfaceInputState.swift), `v2SurfaceInputState` and `agentPromptInputState` | Reads lifecycle and active screen via render-grid export; reports `empty/draft/dialog/unknown`. It returns no ownership token or protected interval and does not acquire an input reservation. |
| CLI draft guard | [cli-contract.md, Draft Guard](https://github.com/manaflow-ai/cmux/blob/12023d89feb5425c766ba05e8d8e4cfc176e3991/docs/cli-contract.md#draft-guard) | `send`/`paste` first inspect; `--force` skips the check. When input-state inspection is unavailable, commands write as before. Enter-only sends check dialogs rather than drafts. This cannot implement a fail-closed exclusive guard. |
| `terminal.input.exactly_once.v1` | [MobileHostService+Capabilities.swift](https://github.com/manaflow-ai/cmux/blob/12023d89feb5425c766ba05e8d8e4cfc176e3991/Sources/Mobile/MobileHostService%2BCapabilities.swift), `MobileTerminalInputDelivery.capability` advertisement | Source describes per-terminal stream ID/sequence, ordered application and acknowledgement. It makes no exclusive-writer claim. |
| Input admission | [MobileHostTerminalInputApplier.swift](https://github.com/manaflow-ai/cmux/blob/12023d89feb5425c766ba05e8d8e4cfc176e3991/Sources/Mobile/MobileHostTerminalInputApplier.swift), `admit` / `complete` | Checks delivery/surface and ledger duplicate/gap/mismatch results; absent delivery identity returns `.proceed`. Accepted writes advance their stream. No owner reservation is acquired for the surface. |
| `terminal.input` dispatch | [TerminalController.swift](https://github.com/manaflow-ai/cmux/blob/12023d89feb5425c766ba05e8d8e4cfc176e3991/Sources/TerminalController.swift), `handleMobileHost` and `v2MobileTerminalInput` | `terminal.input` and `mobile.terminal.input` use the same input path: admit delivery, send terminal input, acknowledge. The per-unit operation does not enclose a separate native CLI resume process. |
| Participant disconnect / sizing | [shared-terminal-sizing.md, Detach](https://github.com/manaflow-ai/cmux/blob/12023d89feb5425c766ba05e8d8e4cfc176e3991/docs/shared-terminal-sizing.md#detach) | Disconnect removes a view and blocks its input until Reattach. Other views continue; participants can reattach. Sizing owners govern the PTY grid. Neither is an exclusive input lease that fences future writers. No detach or sizing mutation was attempted. |

Decisive excerpts from `MobileHostTerminalInputApplier`:

```swift
func admit(_ delivery: MobileTerminalInputDelivery?, surfaceID: UUID) -> Admission {
    guard let delivery else { return .proceed }
    // ... surface mismatch and ledger duplicate/gap checks ...
}
```

```text
Input without an identity (an older phone) is written as before.
```

Exactly-once transport of identified units does not exclude another stream, a local human or an untagged writer. Serial execution of one RPC body on the main actor also does not reserve the input across later RPCs or the lifetime of a foreground child.

## Implementation boundary

- Keep the mandatory injected `NativeSubmitGuard` interface and nil-guard constructor rejection. Unknown/busy/draft/incomplete/mismatched input and missing release continue to fail closed.
- Do not map `blocks_typing=false`, `state=empty`, lifecycle idle, an ordered/exactly-once capability or a participant count to `InputState.Verified=true` plus a dummy release function.
- A standard-library mutex, file lock or durable coordinator claim coordinates only cooperating AgentFlow callers. It cannot fence human edits, other cmux writers or independent native session clients. Screen polling, two empty snapshots, focus changes and disconnecting existing viewers cannot close this race.
- A future implementation needs an enforced acquisition primitive bound to the exact current surface/session, atomic state validation and exclusion of relevant writers for the complete operation. Lease expiry/revocation, cancellation, process exit and release must preserve that guarantee; binding drift or lost ownership must stop confirmation and preserve UNKNOWN/no-replay semantics. An upstream capability name alone would require renewed behavioral verification.

`UNIMPLEMENTED` here is a documented capability status, not a new executable guard/error code. Existing constructor and coordinator diagnostics retain their existing meaning. Continued TUI/multiple-window native acceptance remains unverified.

## Verification and delivery

```text
go test ./internal/runtime -run '^TestNativeSubmit' -count=1
ok agentflow/internal/runtime 2.356s

gofmt -d internal/runtime/native_submit.go
PASS (no diff)

python3 -m json.tool docs/evidence/cmux-input-ownership-guard-2026-11-04.json /dev/null
PASS
```

This reuses existing executable fixtures, including nil guard rejection and unsafe input/release rejection. It is not a real ownership/lease test or a model submission. The code change is a contract comment; documentation records the unsupported boundary. No dependency, service, MQ or task table was added. No terminal input, user test-window operation, native submission or ownership mutation was performed.

The installed `agent-handoff --help` failed because `/Users/funlake/.local/bin/agent-handoff` points to missing `/Users/funlake/.codex/skills/agent-init/scripts/agent-handoff.sh`. The available Okra fallback script uses `cmux send --paste` followed by `send-key`, which conflicts with this task's no-input restriction. Formal handoff therefore uses `cmux agent message surface:1000060018` with a structured `agent_handoff` payload through agent hooks. Its queued transport receipt is distinct from main agent delivery, ACK and review.

Payload: [cmux-input-ownership-guard-handoff.json](cmux-input-ownership-guard-handoff.json). The send command exited 0 with message ID `d135b848-37e3-4333-8335-c738ff50b818`, `state=queued`, `recipient_has_agent=true`, `delivered_at=null` and `read_at=null`. [Transport receipt metadata](cmux-input-ownership-guard-handoff-receipt.json) records the exact recipient `surface:1000060018` and sender identity. Formal handoff has been queued; delivery and main ACK/review remain pending. No polling or input-based retry was performed.

Subsequent main agent ACK received in this session, correlated by handoff ID `d135b848-37e3-4333-8335-c738ff50b818`:

```json
{"type":"agent_handoff_ack","id":"d135b848-37e3-4333-8335-c738ff50b818","message":"已收到 ownership/lease 调查结论；确认 cmux 无合格 API，不实现假锁，TUI guard 保持 UNIMPLEMENTED。"}
```

The main agent acknowledged and confirmed the investigation conclusion. The earlier queued transport receipt is retained as the original send-time snapshot; this ACK is separate subsequent evidence. Real TUI input ownership remains UNIMPLEMENTED.
