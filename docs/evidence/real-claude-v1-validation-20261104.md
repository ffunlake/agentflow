# Real headless Claude v1 validation — 2026-11-04

Session / planned run: `real-claude-v1-validation-20261104-01`. Role: 资深研发（现场验证）. Collaboration: `ralph-loop`.

**BLOCKED_CONFIRM — bare-mode authentication preflight blocked real execution.** Installed Claude Code is **2.1.318**. The current process has no `ANTHROPIC_API_KEY` and no enabled Bedrock / Vertex / Foundry provider. The implemented adapter always uses `--bare`, and the installed CLI help states that Anthropic authentication in this mode is strictly `ANTHROPIC_API_KEY` or an `apiKeyHelper` explicitly supplied through `--settings`; OAuth and keychain are never read. The adapter supplies no `--settings`. Following the supplied stop condition, this validation made **zero model requests**.

This is a local preflight conclusion, not an observed server-side authentication rejection. The user's subscription/OAuth login status is **UNVERIFIED**. No credential or user configuration file was inspected or copied, and no login was attempted. Existing fixture verification is not counted as real acceptance.

## Acceptance matrix

| Item | Status | Evidence / limitation |
| --- | --- | --- |
| Installed CLI version / help | PASS | Actual executable `--version` / `--help`, both exit 0 |
| Isolated configuration | PASS, plan only | Two persistent Claude roles, default permissions; dry-run `PLANNED`, `executed:false` |
| Authentication gate | BLOCKED_CONFIRM | Required bare-mode environment source absent; OAuth/keychain status not inspected |
| Real startup / two READY proofs | UNVERIFIED | Neither role launched; no READY challenge or response |
| Execute / real nonce handoff | UNVERIFIED | No model execution; generated planned nonce is not a real nonce result |
| Review / approve | UNVERIFIED | No review response |
| Workflow COMPLETED | UNVERIFIED | No workflow started |
| Persistent resume / second safe delivery | UNVERIFIED | No native session or first delivery exists |
| Coordinator close/reopen / exact replay / idempotency | UNVERIFIED | No coordinator started and no durable request receipt exists |
| Effective permissions | UNVERIFIED | No independent enforcement/denial probe; `permissions: default` is configuration only |
| No tool calls / file changes by a model | NOT_EXERCISED | No model process was invoked; no claim about a completed native turn |
| Production files unchanged | PASS | Before/after/final SHA-256 equality for cmd/internal/tests/examples and root build/module/readme files |
| Cleanup | PASS | Empty isolated cwd/run; no DB, socket or lock created; owned temporary root removed |

## Binding and absent runtime evidence

| Planned run | Agent | Generation | Native session | Runtime status |
| --- | --- | --- | --- | --- |
| `real-claude-v1-validation-20261104-01` | `builder` | 1 | `null` | NOT_STARTED |
| `real-claude-v1-validation-20261104-01` | `main` | 1 | `null` | NOT_STARTED |

Planned workflow nonce: `AF_CLAUDE_REAL_162c21930bf04d86af7785444c9bf698`, status **NOT_EXECUTED**. It appears in the archived configuration and dry-run plan only. There is no real nonce response. No native session IDs or JSONL captures can be reported; `native_invocations`, `captures`, and `observed_sessions` in the manifest are empty. No synthetic capture or DB was substituted.

Run/DB state: `NOT_STARTED` / `NOT_CREATED`. Messages, delivery rows, journal, READY proofs and workflow receipts do not exist. Reopen/idempotency and permission effectiveness remain unproven; the real acceptance gate is not satisfied.

## Actual commands and exits

The installed executable was called directly at `/Users/funlake/.nvm/versions/node/v26.8.1/bin/claude`, avoiding the PATH cmux routing shim. Only version and help were executed against Claude.

| Log label | Action | Exit |
| --- | --- | --- |
| `claude-version` | Actual Claude `--version` → `2.1.318 (Claude Code)` | 0 |
| `claude-help` | Actual Claude `--help`, including bare authentication contract | 0 |
| `build` | `go build -o <owned-temp>/agentflow ./cmd/agentflow` | 0 |
| `up-dry-run` | Built CLI `up -f <evidence>/team.json --headless-claude --run real-claude-v1-validation-20261104-01 --run-dir <owned-temp>/run --dry-run --json` | 0 |

All four stderr logs are empty. Exact argv, cwd, timestamps and exits are in [validation.json](real-claude-v1-validation-20261104/validation.json). No real `up`, `coordinator`, workflow command, resume, retry, tool probe, or native model request was sent after the authentication gate failed.

The isolated root was `/private/var/folders/wv/x8nwj9tj7xz5dhqf5f2mt3hw0000gn/T/opencode/af-claude-preflight-3m9juc9c`. Both `cwd` and `run` remained empty. The built binary and only this unique temporary root were removed. The evidence configuration retains the original paths for reproducibility and is an archived plan, not a live run.

`risky-ask` was loaded for execution-risk guidance, but `jev-judge` was absent from PATH. Its status is `UNAVAILABLE`; no judgment or probabilities were fabricated. The preflight performed only metadata collection, a local build and a dry-run. `okra-agent-init` was used solely for the explicitly requested final collaboration handoff; no workers were spawned or dispatched.

## Evidence and read-only consistency

- [validation.json](real-claude-v1-validation-20261104/validation.json): authentication variable presence booleans only, planned actors, checks, source fingerprints, exact commands/exits and cleanup.
- [claude-version.stdout.log](real-claude-v1-validation-20261104/claude-version.stdout.log), [claude-help.stdout.log](real-claude-v1-validation-20261104/claude-help.stdout.log): raw installed CLI metadata.
- [team.json](real-claude-v1-validation-20261104/team.json), [up-dry-run.stdout.log](real-claude-v1-validation-20261104/up-dry-run.stdout.log): isolated two-role configuration and plan.
- `build.stdout.log`, `build.stderr.log`, and each command's corresponding stderr log: original command output, including empty outputs.
- [validate-preflight.py](real-claude-v1-validation-20261104/validate-preflight.py): evidence collection, no model execution; refuses to overwrite an existing validation manifest.
- [check-evidence.py](real-claude-v1-validation-20261104/check-evidence.py): read-only archive verification, no CLI/model execution. Verifies all unperformed items remain UNVERIFIED, no fabricated native evidence, exact version/plan, 2 persistent roles, source equality and cleanup.
- `consistency.stdout.log`, `consistency.stderr.log`, `consistency-command.json`: final archived consistency result and invocation/exit. A PASS here establishes blocked-preflight evidence consistency only.
- `handoff.stdout.log`, `handoff.stderr.log`, `handoff-command.json`: actual final collaboration transport output/exit.

No production code was edited. No wiki was accessed. No old agentflow cmux workflow window was operated. The final handoff uses only the explicitly required collaboration transport; that transport internally sends its notification and submit key to the main-agent route and supplies no runtime acceptance evidence.

## Handoff and next action

Real acceptance remains **BLOCKED_CONFIRM**. Continue only in a process where an existing supported bare-mode authentication source is available, without reading/copying credentials, logging in, changing user settings, or bypassing the adapter's authentication boundary. Then perform real READY → execute → review → COMPLETED, persistent native resume, normal coordinator reopen and exact replay with real capture/DB/session binding. A subscription login alone does not prove this adapter can authenticate.

The final archived read-only consistency check passed all 18 checks, exit **0**; stderr was empty. The required final command used `--window 1 --session-id real-claude-v1-validation-20261104-01 --status BLOCKED_CONFIRM`, exited **0**, and confirmed:

```text
[DONE] handoff sent: id=da30b552-9c76-4c38-9a5f-e09b2009017b window=1 status=BLOCKED_CONFIRM
```

Actual stdout, stderr and exact argv/exit are retained in the handoff artifacts listed above. This confirms formal delivery of the blocked validation result; it does not satisfy the real execution acceptance gate.

The main agent acknowledged the matching handoff ID: “主 agent 已收到 Claude 真实验收 BLOCKED_CONFIRM；保留零模型请求和未认证边界，不绕过认证。” The received ACK is preserved in [handoff-ack.json](real-claude-v1-validation-20261104/handoff-ack.json).
