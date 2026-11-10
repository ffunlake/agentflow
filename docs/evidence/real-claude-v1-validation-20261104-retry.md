# Real Claude headless v1 retry — 2026-11-04

Session/run: `real-claude-v1-validation-20261104-02`。固定角色：资深研发（现场验证）；协作模式：`ralph-loop`。

**BLOCKED_CONFIRM：已实际调用现有 adapter，Claude headless 仍无法使用当前认证。** Claude Code **2.1.318** 的原生启动 JSONL 返回 `apiKeySource:"none"`、`error:"authentication_failed"`、`Not logged in · Please run /login`；原生 CLI 和 AgentFlow `up` 均退出 **1**。按任务停止条件，本轮在第一条失败调用后停止。真实 READY、execute、review、COMPLETED、resume 和 coordinator 重开验收均未通过；权限效果 **UNVERIFIED**。

本轮的认证失败来自实际原生输出。原生 assistant 的 `model` 为 `<synthetic>`，result 同时报告 `is_error:true`、`terminal_reason:"api_error"`、`duration_api_ms:0`、`total_cost_usd:0`。虽然 result 的 `subtype` 字面值是 `success`，这些错误字段和退出码明确表示失败，不能计为成功生成。没有服务器响应证据，也不能声称成功执行了模型请求。

现有 adapter 在 `internal/runtime/native.go` 固定使用 `--bare`。本轮实际采集的 CLI help 说明：此模式不会读取 OAuth/keychain，只支持 `ANTHROPIC_API_KEY`、显式 `--settings` 的 `apiKeyHelper` 或第三方 provider 的认证。adapter 没有传入 `--settings`；本进程相应 key/provider 环境变量存在性均为 false，仅记录布尔值。用户在验证窗口完成官方登录的状态未被检查。该登录不会使现有 bare-mode adapter 自动读取 OAuth/keychain。

| 验收项 | 结果 | 证据和边界 |
| --- | --- | --- |
| CLI 版本、help、构建 | PASS | 真实 CLI `2.1.318 (Claude Code)`；三条命令均 exit 0 |
| 隔离 run/cwd、两个 persistent roles 配置 | PASS | `team.json` 配置 builder/main，`permissions:default`，全新临时目录 |
| 真实启动 | BLOCKED_CONFIRM | `up` 实际启动 builder；原生认证失败，未达到 coordinator ready |
| 两个真实 READY | UNVERIFIED | 没有 READY proof；main 尚未启动 |
| Execute / nonce handoff | UNVERIFIED | 未发送 workflow 请求；没有真实 nonce 响应 |
| Review / approve | UNVERIFIED | 未执行 review |
| Workflow COMPLETED | UNVERIFIED | workflow 未启动 |
| Persistent resume / 第二次安全投递 | UNVERIFIED | 没有已建立的 READY seat/session；未调用 `--resume` |
| Coordinator 关闭重开、exact replay、幂等 | UNVERIFIED | `coordinator_ready:false`，无 workflow receipt 可重放；停止后未重开 |
| Run/agent/generation/session 关联 | 部分可观察 | 失败 startup 的 stdin、capture 和 JSONL 可关联到 run/builder/1；未形成有效 READY/持久化 seat 绑定 |
| 无工具调用 | PASS，仅失败调用范围 | 三个 JSONL 事件，无 `tool_use` / `tool_progress`；未完成成功模型 turn |
| 隔离 cwd 未修改 | PASS | 执行前后均为空 |
| 生产源文件未修改 | PASS | cmd/internal/tests/examples 及根构建文件 SHA-256 前后相等 |
| 权限效果 | UNVERIFIED | `permissions:default` 和 `--permission-prompts none` 仅是配置；没有独立 enforcement/denial 探针 |
| 清理 | PASS | 自有 `up` 进程已退出，lock/socket 不存在，原始 DB/captures 归档后仅删除本轮唯一临时根目录 |

本轮 workflow nonce 为 `AF_CLAUDE_REAL_38b28d222fbe4ecba8f2d4bb0fc03d95`，状态 **NOT_EXECUTED**。启动 challenge nonce 为 `f0aa0cae5d7eb5d288ed3acf17acf048a1b2f40e65c9ccd5`，已通过 stdin 发出，但未收到回显。两者都不能算真实 nonce 验收通过。

| Run | Agent | Generation | 原生 session UUID | 状态 |
| --- | --- | --- | --- | --- |
| `real-claude-v1-validation-20261104-02` | builder | 1 | `60a424e9-9aac-4bab-8242-b0b41f09680a` | 仅失败调用 JSONL 中的本地 UUID；没有 READY proof |
| 同上 | main | 1 | 无 | NOT_LAUNCHED |

实际原生命令为：

```text
/Users/funlake/.nvm/versions/node/v26.8.1/bin/claude --print --bare --verbose --output-format stream-json --input-format text --permission-prompts none --prompt-suggestions false
```

原生 cwd：`/private/var/folders/wv/x8nwj9tj7xz5dhqf5f2mt3hw0000gn/T/opencode/ac-o_n3gjnc/cwd`。同一临时根目录中的 `run` 保存运行状态。临时目录和构建二进制已清理。

`--native-binary` 指向本轮的透明 observer，它调用上述已安装的真实 CLI，原样传递 adapter 参数、stdin 和继承环境，记录完整 stdout/stderr 和实际退出码，再原样返回给 adapter。没有删除 `--bare`、替换认证、改造模型响应或模拟 provider。observer 不打开任何配置/凭据文件。

| 命令/日志标签 | 实际动作 | 退出码 |
| --- | --- | --- |
| `claude-version` | 已安装 CLI `--version` | 0 |
| `claude-help` | 已安装 CLI `--help` | 0 |
| `build` | `go build -o <本轮临时根>/agentflow ./cmd/agentflow` | 0 |
| `up` | `up -f <evidence>/team.json --headless-claude --native-binary <observer> --turn-timeout 3m --startup-timeout 7m --run real-claude-v1-validation-20261104-02 --run-dir <本轮临时根>/run --json`；PID 36947 | 1 |
| 原生调用 `a6ec96a6f51145e9be35c2959dd6bb41` | 上述 Claude print-mode 命令 | 1 |

版本、help、build 和原生 Claude 调用的 stderr 均为空；`up.stderr.log` 包含一条 `UP_START_FAILED` 错误。AgentFlow `up` 输出 `status:FAILED`、`native_process:OUTCOME_UNVERIFIED`、`UP_START_FAILED`，并保留启动前的两角色计划信息；其中 `executed:false` 不能解释为原生进程没有运行。原生调用记录和 capture 证明 builder 已被实际调用一次。

DB 归档包含 **1 run、1 journal、0 seats、0 messages、0 deliveries**。唯一 journal 事件为 `LAUNCH_REQUESTED`，details 指向 builder/generation 1；run 的 `startup_phase` 字面值为 `STARTING`，workspace 为空。没有 `NATIVE_READY`、工作流请求收据或已接受的投递，因此无法证明成功的 durable session 绑定、幂等或恢复能力。

本轮未对失败输出做协议放宽，成功 turn 的协议兼容性仍为 UNVERIFIED。原始失败流含若干当前严格 allowlist 未覆盖字段（例如 assistant 的 `is_api_error_message` 与 message 的 `diagnostics`），没有进一步调用以探测或修复协议。

交付文件位于 [本轮证据目录](real-claude-v1-validation-20261104-retry/)：

- [validation.json](real-claude-v1-validation-20261104-retry/validation.json)：argv、cwd、时间、退出码、验收矩阵、认证观察、session、前后指纹和清理。
- [native-invocations.jsonl](real-claude-v1-validation-20261104-retry/native-invocations.jsonl)：唯一真实调用的开始/退出记录；同 ID 的 `.stdin.txt`、`.stdout.jsonl`、`.stderr.log` 是原始输入输出。
- [headless-start-builder.json](real-claude-v1-validation-20261104-retry/headless-start-builder.json) 和 `.jsonl`：adapter 原始 capture 及提取的 JSONL，和 observer stdout 逐字节相等。
- [up.stdout.log](real-claude-v1-validation-20261104-retry/up.stdout.log)、各命令 `.stdout.log/.stderr.log`、`failure.log`：启动结果与 harness 停止位置。
- [state.db](real-claude-v1-validation-20261104-retry/state.db)、[final.db.json](real-claude-v1-validation-20261104-retry/final.db.json)、`config.resolved.json`、`headless.claude.json`、`up.requested.json`：独立 SQLite backup、全表快照与 run 元数据。
- [validate.py](real-claude-v1-validation-20261104-retry/validate.py)、[native-observer.py](real-claude-v1-validation-20261104-retry/native-observer.py)：本轮证据收集脚本。重新运行会尝试真实请求；已有 manifest 时拒绝覆盖。
- [check-evidence.py](real-claude-v1-validation-20261104-retry/check-evidence.py)：只读核对归档，不调用 CLI/model。检查输出与调用/退出码另存为 `consistency.stdout.json`、`consistency.stderr.log`、`consistency-command.json`。

本轮使用此前 Claude preflight、真实 OpenCode 的 pause/resume/reopen 验收模式和 Codex nonce 验证边界。`agent-history` 的 `ochist` 不在 PATH，因而读取仓库中的已有证据；`risky-ask` 的 `jev-judge` 也不在 PATH，没有生成或伪造风险判断。本轮没有登录、读取/复制凭据、编辑用户配置、访问 wiki，也没有操作或读取用户官方登录窗口。新增内容仅是本报告和本轮证据目录。

后续需要由主 agent 确认认证模式与现有 bare-mode adapter 的产品边界。只有运行环境已有 adapter 支持的认证来源，或另行授权调整认证策略并完成实现审查后，才可开启新的真实验收。当前范围内不改 adapter、不要求用户再登录、不尝试绕过。官方登录状态和权限效果均保持 UNVERIFIED。

正式回传通过用户指定的 `agent-handoff.sh send --window 1 --session-id real-claude-v1-validation-20261104-02 --status BLOCKED_CONFIRM`；实际 stdout/stderr、argv 与退出码保存在本轮目录的 handoff 文件。该协作通知发往主 agent 路由，独立于用户的登录窗口和运行时验收证据。

首次只读归档检查 exit 1：checker 错误要求 `up.stderr.log` 也为空，实际失败命令正常打印了 `UP_START_FAILED`。已修正 checker 分别核对原生 stderr 为空和 up 的失败诊断；首次检查的输出/退出码保留在 `consistency-initial*` 文件。没有因此重跑 CLI/model 或改变原始证据。

最终归档一致性检查 **29 项通过，exit 0**。正式回传已执行，**exit 0**，stderr 为空，确认：

```text
[DONE] handoff sent: id=0a9b8363-03c4-49dd-82d6-e8dea06bf540 window=1 status=BLOCKED_CONFIRM
```

实际调用和原始输出分别见 `handoff-command.json`、`handoff.stdout.log`、`handoff.stderr.log`。这仅确认主 agent 通知已发送；真实端到端验收仍为 BLOCKED_CONFIRM。

已收到匹配 handoff ID 的主 agent ACK，保存于 [handoff-ack.json](real-claude-v1-validation-20261104-retry/handoff-ack.json)。主 agent 确认 `--bare` 不读取 OAuth 是本轮阻塞，并将最小 non-bare OAuth 可见性调查列为下一步；本轮验收状态保持 BLOCKED_CONFIRM。
