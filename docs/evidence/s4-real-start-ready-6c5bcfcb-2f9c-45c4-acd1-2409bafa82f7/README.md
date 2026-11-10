# S4 真实单 seat：native JSONL → _seat → Start → READY

日期：2026-11-03。Session：`6c5bcfcb-2f9c-45c4-acd1-2409bafa82f7`。Task：`s4-real-start-ready`。角色：资深研发。

**本任务两条真实单 seat 链路均 PASS。** OpenCode 和 Codex 各启动一次已安装 CLI，经私有 snapshot、显式 `AGENTFLOW_NATIVE_OPTIONS`、真实 native JSONL、`_seat`、持有 run lock 的 `Start`，登记了精确 actor/generation/nonce 的 READY；关闭并重开 SQLite 后再次核对成功。S4 全团队启动仍为 PARTIAL，不能由此推断三工具、持续通信或原生 attach/stop 通过。

## 现场结果

| Provider | 真实 native session/thread ID | Actor | READY | 状态 |
|---|---|---|---|---|
| OpenCode | `ses_e601a1328ffeCSZxJndBqKYWx0` | `s4-real-6c5bcfcb-opencode / solo / 1` | 精确 nonce 响应、完成事件、store 当前 seat 均匹配 | **PASS** |
| Codex | `01a19fe6-b055-7092-b1fd-ff80ef74cb40` | `s4-real-6c5bcfcb-codex / solo / 1` | 精确 nonce 响应、完成事件、store 当前 seat 均匹配 | **PASS** |

OpenCode nonce：`ded2e3916dac83c351e4a94910203c8ccfe597c1707aa218`。Codex nonce：`4284670e823614d28200b072bfc7b33467615c5521edc100`。两者由 `_seat` 在执行前独立随机生成，发送后没有改写 challenge 或 assistant 输出。

OpenCode 的原生事件为 `step_start → text → step_finish(reason=stop)`，约 6 秒完成。Codex 为 `thread.started → item.completed(error diagnostic) → turn.started → item.completed(agent_message) → turn.completed`，约 27 秒完成。两者原生进程均成功退出，输出未截断，没有 tool execution 事件。

Codex 的非终止诊断原文：`loading hooks from both /Users/funlake/.codex/hooks.json and /Users/funlake/.codex/config.toml; prefer a single representation for this layer`。它是 `item.completed` 中的 `item.type=error`，不是 `turn.failed` 或顶层 `error`；随后出现完整匹配的 assistant READY 和 `turn.completed`。本轮保留该诊断，不修改用户配置。PASS 仅指本次完成的启动握手与登记。

每个 provider 的 `native.jsonl` 有两条证据记录：执行前的 challenge/command/location，以及执行后的原始 stdout、executed/process_succeeded/truncated。`native-stdout.jsonl` 是从该记录逐字提取的原生 JSONL，SHA-256 见 `verification.json`。`snapshot.json` 为 0600 初始快照，所在 provider 目录为 0700；快照保持 STARTING，实时 READY 在 `state.db`、`result.json` 的 registry 和 stored_seat 中。

## 操作范围与清理

所有现场终端动作限定在 `window:1000060005 / workspace:1000060011`。原 OpenCode `surface:1000060034`、Codex `surface:1000060035` 仅执行只读 screen/tree 检查，没有向原两屏发送命令或输入。

本轮恰好创建一个临时 pane：`pane:1000060035 / surface:1000060038`，surface UUID `00D33DBF-35BE-4E2E-A5A2-326D64879699`，tty `ttys008`。workspace UUID 为 `3AB7D16C-B09D-44AE-8CD5-5B6225388D45`。临时 shell 的实际 cwd 经 `os.Getwd` 核对为 `/Users/funlake/Projects/agentflow`；`_seat` 再使用真实 cmux caller 核对 window/workspace/surface，最终登记 UUID。

只向此临时 surface 顺序发送两条 `bin/real-seat` 命令，每个 provider 各一次 send 和独立 enter。wrapper 与原生 CLI 继续以独立 argv、冻结 cwd 和 stdin 调用；临时 shell 启动验证器的命令使用固定参数和 shell quoting。原生路径为 `/opt/homebrew/bin/opencode` 与 `/opt/homebrew/bin/codex`，跳过 cmux shims。

两条链路均正常结束并返回空 shell 提示符后，仅关闭本轮临时 surface。关闭命令退出 0；立即查询的 `tree-after.json` 仍保留旧布局，故没有据此声称清理完成，也没有再次关闭。稍后一次只读查询 `tree-final.json` 确认临时 pane 已消失，原两屏的 pane/surface UUID、左右顺序及 0.5 比例与 `tree-before.json` 完全一致。离线验证再次比较了布局。

没有登录、复制凭据、修改用户配置、重置原有会话或启动 Claude。provider 使用已有普通运行环境，配置权限设为 `default`；不据此声称配置隔离或 full 权限生效。未遇到 busy、草稿、权限弹窗、身份不一致或未知原生结果，无现场重试。

## 代码与保护验证

- 复用 `internal/runtime/native.go` 的现有 `ForegroundLauncher`、`NativeCommand`、`ParseNativeReady`，以及 `Start`、`Registry.Register` 和 `_seat`，没有新增 schema、依赖或协调架构。
- `NativeOptions.EvidencePath` 为可选字段，经 `AGENTFLOW_NATIVE_OPTIONS` 传给 `_seat`。证据必须是与 snapshot 同目录的新 0600 文件，在原生执行前独占创建并同步 challenge。已存在的文件、symlink、目录外路径或写入失败均拒绝执行或登记；不记录 native stderr 或完整角色正文。证据记录只用于审计，后续启动不以旧证据恢复或重放。
- 新增显式手动入口 `probes/real-seat`。`go test`/`make check` 不运行真实 provider。每个 provider 子目录本身是独占意图，已有目录会拒绝再次运行。验证器持有现有 `internal/lock` 锁，再使用现有 `Start` 接口。
- 两个运行都用独立子进程竞争同一 `coordinator.lock`，均返回 **ALREADY_RUNNING**；原拥有者最终成功释放锁。它验证锁竞争，不将锁存在当成 READY。
- 首次 `Start` 后再次调用同一 `Start`，以及关闭、重开数据库后的再次调用，都返回 `startup store already contains a run`。`launch_calls` 始终为 **1**，没有第二次 native 启动。原生 JSONL 与初始 challenge 被独立解析，并与重开 SQLite 后的当前 seat 逐字段匹配。
- `tests/e2e/native_launcher_test.go` 补充证据回放核对、已存在证据拒绝覆盖/执行、目录外证据拒绝和 stderr/角色正文不进入证据。此处 fixture 只用于工程测试；现场 PASS 来自本目录两份真实 native stdout 与 SQLite。

`runs.startup_phase` 仍为现有 Start API 保存的 `STARTING`，未把它改为团队 READY。当前单 seat 为 `is_current=1/status=READY`；journal 为各一次 `LAUNCH_REQUESTED/LAUNCH_BOUND`。native CLI 是一次性握手进程，正常退出后不据此推断持续存活。直接 `_seat` 不提供 store 级重放保护，正式使用仍须 run lock 和 `Start`。

## 工程检查与离线复核

| 检查 | 结果 | 原始输出 |
|---|---|---|
| 定向 runtime/cli/e2e/probe 测试 | PASS | `focused-test.json` |
| `go test ./...` | PASS | `go-test.json` |
| `go build ./...` | PASS | `go-build.json` |
| `make check`（格式、deps、vet、全量测试、build） | PASS | `make-check.json` |
| `go mod tidy -diff` | PASS，无差异 | `go-tidy.json` |
| 真实输出、只读 SQLite、锁释放、动作范围、布局复核 | PASS | `verification.json` |

最初一次直接 Go 定向测试在默认缓存环境中 120 秒超时且没有输出；随后按 Makefile 的既定缓存环境完成全部检查。超时发生于真实 provider 启动之前。历史 CLI `ochist` 不在 PATH；上一轮实现与磁盘证据已审阅。`jev-judge` 未安装，没有伪造风险判断。

可重复执行的离线核对命令（不调用 cmux 或 provider）：

```sh
python3 docs/evidence/s4-real-start-ready-6c5bcfcb-2f9c-45c4-acd1-2409bafa82f7/verify_evidence.py
```

`collect.py` 保存有独占意图的现场命令回执。禁止重跑本轮 `create/run/close`；已有回执和 provider 目录会阻止同名动作重放。

## 状态边界与下一步

本任务没有阻断项。OpenCode 单 seat、Codex 单 seat、run lock 和重复 Start 保护均 PASS。全团队 `up`、多 pane argv 传输保持 **PARTIAL**；有效 full 权限、持续通信、现场 attach/stop、S5/S7 native reset/submit/recovery 保持 **UNVERIFIED**；Claude adapter 保持未实现。下一步应先 review 本次代码和证据，再为多 seat 传输选择可验证的 argv/cwd 执行通道。

正式 `agent_handoff` payload 位于 `agent_handoff.json`，通过已确认 main 路由 `surface:1000060018` 的 cmux agent 消息队列回传；传输结果以 `handoff-receipt.json` 为准。系统现有 `agent-handoff` 包装脚本引用缺失文件，因此沿用上一轮的原生消息通道，避免向控制终端输入框注入文本。队列接受与主控验收 ACK 分别记录。

2026-11-03T05:39:19Z 已正式发送，命令退出 0；cmux 消息 ID 为 **`70d80d85-029c-461e-974f-79e7307372b5`**，`recipient_has_agent=true`，回执状态 **queued**。`delivered_at/read_at` 为空，尚无主控验收 ACK。本目录及相关源码/二进制的封存 SHA-256 位于 `sha256.json`。
