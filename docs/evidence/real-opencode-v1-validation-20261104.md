# Real headless OpenCode v1 validation — 2026-11-04

Session/run: `real-opencode-v1-validation-20261104-01`  
Role: 资深研发（现场验证）；collaboration: `ralph-loop`.

**真实端到端验收 PASS。** Installed OpenCode `1.19.5` completed two persistent logical-role READY handshakes, an intentional workflow pause, explicit lead resume, builder handoff, reviewer `approve` with `accepted:[true]`, and workflow completion at revision 5. Clean coordinator shutdown/reopen and exact request replay passed at both paused and completed boundaries. Effective permission enforcement remains **UNVERIFIED**.

本轮未修改生产代码。所有新增文件位于本报告及其同名证据目录。真实执行使用已安装 `/opt/homebrew/bin/opencode`，没有 fixture、模拟 provider 或预录响应。未发生登录要求、额度不足或认证绕过。首次 `blocked` 是 nonce 指令显式要求的验收步骤，并非认证或额度阻塞。

## Result matrix

| Item | Result | Real evidence |
| --- | --- | --- |
| Installed CLI | PASS | `opencode --version` → `1.19.5`, exit 0; `run --help`, exit 0 |
| Two persistent logical roles | PASS | `up` → READY, two matching generation-1 READY proofs, distinct real session IDs |
| Execute / nonce handoff | PASS | Builder revision-3 response contains the exact nonce in summary and evidence |
| Review | PASS | Main revision-4 response is `{"kind":"approve","accepted":[true]}` |
| Workflow completion | PASS | `workflow resume --revision 2` → `COMPLETED`, revision 5, step `done`, attempt 2 |
| Paused restart and exact replay | PASS | Close/reopen at revision 2; identical start receipt, executed=false, no dispatch; all DB tables unchanged |
| Completed restart and exact replay | PASS | Close/reopen at revision 5; identical start/resume receipts; status and terminal drain execute no native turn; all DB tables unchanged |
| Persistent resume / second delivery | PASS | Builder execute revisions 1 and 3 use the same explicit `--session`; separate dispatch and native message IDs; each reaches durable SENT, attempt 1 |
| Run / agent / generation / session binding | PASS | Archived captures, real raw JSONL, native argv, READY proofs, messages, deliveries and replies agree |
| No tool calls observed | PASS | Exactly 15 JSONL events: 5 step_start, 5 text, 5 step_finish; no tool events |
| Cwd / source files | PASS | Empty isolated cwd before and after; SHA-256 of cmd/internal/tests/examples, go.mod/go.sum/Makefile/README unchanged |
| Effective permissions | UNVERIFIED | `permissions: default`; no independent denial/enforcement probe was authorized or performed |
| Cleanup | PASS | Three owned coordinator processes exit 0; lock/socket removed; own temporary root removed; DB and captures archived |

## Identity and actual response chain

Workflow nonce: `AF_OC_REAL_fc8f5f536e86465783050809cb85fb7a`.

| Agent | Generation | Native OpenCode session ID | Startup challenge nonce |
| --- | --- | --- | --- |
| builder | 1 | `ses_e5a3e5e7dffeS2C3PEVyYyLTrC` | `91640d2d002d029d80aac56e41a82c961c7cc1525e56a58b` |
| main | 1 | `ses_e5a3e4353ffeENMzP4cC2rWC01` | `56f1ac29fb4153568ed4043e46ca29ea500f7fa39b5574a1` |

OpenCode exposes `sessionID`; it does not emit a separate Codex-style `thread_id`. Both sessions are bound to the same run above and to `headless:builder` / `headless:main`, with logical workspace `headless:real-opencode-v1-validation-20261104-01`.

| Dispatch revision | Agent / result | AgentFlow message ID | Native message ID |
| --- | --- | --- | --- |
| 1 | builder / blocked | `PG675TXG6TNCUU3FBEIIVN5IAW` | `msg_1a5c1e6dc001xQGdQkJW7KAyEE` |
| 3 | builder / handoff | `YHMK2NV25HXZ3K7JZRP5URQRRL` | `msg_1a5c207b8001kIY5hYehHMw2y0` |
| 4 | main / approve | `K5M3Z6POPHDNPG5ADZ4ATW4OGB` | `msg_1a5c24f39001J7gBBXCg1gM7Ai` |

The two startup turns precede those three dispatch turns. Builder's first execute returns a requested `blocked`, producing PAUSED revision 2. After clean reopen, the same start request `start-real-1` returns its original receipt without running a model. Lead then issues `resume-real-1` with reason `No-tools nonce protocol approved; continue AF_OC_REAL_fc8f5f536e86465783050809cb85fb7a`. The second builder execute returns:

```json
{"kind":"handoff","handoff":{"summary":"No-tools nonce protocol completed: AF_OC_REAL_fc8f5f536e86465783050809cb85fb7a","evidence":["Supplied instruction nonce: AF_OC_REAL_fc8f5f536e86465783050809cb85fb7a"],"next":"review"}}
```

The reviewer receives that persisted handoff and returns `{"kind":"approve","accepted":[true]}`. Workflow transitions through EXECUTING revision 3 and REVIEWING revision 4 to COMPLETED revision 5.

## Execution and exit codes

Configuration is a JSON equivalent of the two-role persistent configuration in `examples/headless-opencode.yaml`, with absolute isolated cwd/context paths, a fresh nonce, and the revision-1 pause instruction. See [team.json](real-opencode-v1-validation-20261104/team.json).

Temporary root was `/private/tmp/af-oc-v1-awlj7off`; native cwd was its empty `cwd` directory, run/DB lived under `run`, and context output lived under `context`. The built AgentFlow binary was inside that root. The root has been removed after archiving.

The evidence-only [native-observer.py](real-opencode-v1-validation-20261104/native-observer.py) is passed via `--native-binary`. It executes `/opt/homebrew/bin/opencode` with unchanged adapter argv, stdin and inherited environment, records actual exit codes and exact stdin/stdout/stderr, then returns the same stdout/stderr and exit status. It buffers output until process completion; it supplies no model answer. No configuration or credential files are opened by this observer. The installed CLI uses its existing authentication and default model normally; the model/provider configuration was not inspected or independently verified.

Native commands were exactly:

```text
/opt/homebrew/bin/opencode run --format json --dir /private/tmp/af-oc-v1-awlj7off/cwd
/opt/homebrew/bin/opencode run --format json --dir /private/tmp/af-oc-v1-awlj7off/cwd
/opt/homebrew/bin/opencode run --format json --dir /private/tmp/af-oc-v1-awlj7off/cwd --session ses_e5a3e5e7dffeS2C3PEVyYyLTrC
/opt/homebrew/bin/opencode run --format json --dir /private/tmp/af-oc-v1-awlj7off/cwd --session ses_e5a3e5e7dffeS2C3PEVyYyLTrC
/opt/homebrew/bin/opencode run --format json --dir /private/tmp/af-oc-v1-awlj7off/cwd --session ses_e5a3e4353ffeENMzP4cC2rWC01
```

All five native processes exited **0**, each emitted a final `step_finish` with `reason:stop`, and all five native stderr files are empty. The native call count remains five across both coordinator reopens and all replay/status/drain operations.

Exact AgentFlow argv, PID where applicable, and exit codes are retained in [validation.json](real-opencode-v1-validation-20261104/validation.json). Each command has a corresponding `<label>.stdout.log` and `<label>.stderr.log`:

| Command/log label | Relevant action | Exit |
| --- | --- | --- |
| build | `go build -o <temp>/agentflow ./cmd/agentflow` | 0 |
| opencode-version / opencode-run-help | Installed CLI metadata | 0 / 0 |
| up | `up -f <evidence>/team.json --headless-opencode --native-binary <observer> --turn-timeout 3m --startup-timeout 7m --run <run> --run-dir <temp>/run --json`; PID 26318, clean SIGTERM | 0 |
| workflow-start | `workflow start`, lead main/generation 1, revision 0, request `start-real-1` | 0 |
| coordinator-reopen-paused | Explicit headless OpenCode reopen; PID 26435, clean SIGTERM | 0 |
| start-exact-replay | Same start argv and request ID as initial start | 0 |
| workflow-resume | `workflow resume`, revision 2, request `resume-real-1`, explicit reason above | 0 |
| coordinator-reopen-completed | Explicit headless OpenCode reopen; clean SIGTERM; PID in manifest | 0 |
| completed-start-replay | Same initial start request | 0 |
| completed-resume-replay | Same resume request including identical reason | 0 |
| workflow-status | Status at completion | 0 |
| terminal-drain | Drain with expected revision 5 | 0 |

Workflow CLI requests use `--timeout 8m --json`; all lead actor metadata is run/main/generation 1. No fixture suite is used to establish these results.

## Durable state and replay

The archive includes [state.db](real-opencode-v1-validation-20261104/state.db), captured through SQLite backup from the closed run DB, and full table snapshots at startup, pause, both replay boundaries and final cleanup.

Final DB has 1 run, 2 current generation-1 READY seats, 6 messages, 6 delivery rows and 26 journal rows. There are three assign/reply pairs. All three assign deliveries are SENT, target generation 1, attempt 1, channel `headless-opencode-resume`, reset NONE; the three reply deliveries remain PENDING as stored records, without further native dispatch. Both seats have `current_message_id=null`.

Raw run metadata remains `startup_phase=STARTING`, `workspace_id=""`, `stop_requested=0`; that stored startup metadata is reported literally. The successful startup claim comes from `up` READY/proofs and the seat rows, and workflow completion comes from the accepted journal/CLI cursor. This report does not reinterpret STARTING as a run-level READY value.

`paused.db.json == paused-after-replay.db.json`; `completed.db.json == completed-after-replays.db.json == final.db.json`, including journal and SQLite sequence rows. Replayed start/resume `workflow` receipts exactly match their saved original receipts; replay responses have `executed:false` and no dispatch. Status/drain likewise execute no native work. This validates clean shutdown/reopen, not crash recovery or stale-lock takeover.

The [final read-only consistency check](real-opencode-v1-validation-20261104/check-evidence.py) exited **0**. Its [result](real-opencode-v1-validation-20261104/consistency-final.stdout.json) verifies SQLite integrity and foreign keys, DB archive equality, all-table replay equality, exact receipts, startup actor/nonce/session equality, native part/message/session stability, raw stdout ↔ adapter capture byte equality, stdin ↔ stored dispatch equality, durable SENT/reply binding, nonce acceptance, source hashes and cleanup. Invocation/exit are recorded in `consistency-final-command.json`; stderr is empty.

An initial archive-check invocation exited 1 because the checker incorrectly expected a new session ID in the *pre-handshake* startup seat capture. The checker was corrected to validate startup identity against the actual JSONL and persisted READY proof, while requiring the established session in submission captures. The original failure and exit code are preserved in `consistency.stderr.log` / `consistency-command.json`; it caused no new model invocation and no production change.

## Evidence index and boundaries

- [validation.json](real-opencode-v1-validation-20261104/validation.json): exact command manifest, exit codes, checks, source hashes, READY and workflow results, capture identity index and cleanup.
- [native-invocations.jsonl](real-opencode-v1-validation-20261104/native-invocations.jsonl): actual installed CLI argv and each native exit code.
- `<invocation-id>.stdin.txt`, `.stdout.jsonl`, `.stderr.log`: raw real process inputs and outputs for all five turns.
- `headless-start-{builder,main}.json` / `.jsonl`: untouched adapter capture envelopes and extracted raw startup JSONL.
- `headless-result-<sha256(message-id)>.json` / `.jsonl`: three real workflow result envelopes and raw JSONL.
- `*.db.json`, `state.db`, `config.resolved.json`, `headless.opencode.json`, `up.requested.json`: run persistence evidence.
- [validate.py](real-opencode-v1-validation-20261104/validate.py), `harness.log`, and [check-evidence.py](real-opencode-v1-validation-20261104/check-evidence.py): evidence-only orchestration and reproducible archive checks. Running `validate.py` would make new paid/native requests; checking the archive does not.

Only observed absence of tool calls and unchanged empty cwd/source files is claimed. Permissions stayed `default`, no enforcement probe was made, and native defaults/plugins were inherited; **effective permission enforcement is UNVERIFIED**. No global filesystem audit is claimed. Existing CLI session records may remain in OpenCode's normal state; their IDs are listed above, and user state/configuration was not edited or cleaned. No credentials were read/copied by the validation harness, no login was attempted, no wiki was accessed, and no user cmux workflow window was operated. The explicitly requested handoff transport below is separate from model/workflow verification.

Cleanup preserved all raw evidence and an independent DB archive before deleting only this harness's unique temporary root. Owned coordinator processes exited 0, their lock/socket were removed, and their PIDs are absent in the final read-only check. Source hashes match before, after and final verification.

## Handoff

The requested `agent-handoff.sh send --window 1 --session-id real-opencode-v1-validation-20261104-01 --status DONE` is the final collaboration notification. Its actual output and exit code are retained in `handoff.stdout.log`, `handoff.stderr.log` and `handoff-command.json` beside the raw evidence.

Executed successfully, exit **0**; stderr empty. Confirmation:

```text
[DONE] handoff sent: id=b5601707-d2fd-4fdc-bd9d-199abee30dae window=1 status=DONE
```
