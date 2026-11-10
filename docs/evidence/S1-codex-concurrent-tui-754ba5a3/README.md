# S1 Codex 双 native TUI 并发隔离证据

协作 session：`754ba5a3-ca1d-43a7-ba19-b14301cc33f1`；task：`s1-codex-concurrent-tui`；角色：资深研发。日期：2026-11-02。本轮结果：**PASS，受控双存活 native TUI 的消息、输出、cwd 与对话上下文隔离**。新增 `codex.concurrent_live_two_tui_isolation`，累计 19 项受控通过；完整 S1 仍 **PARTIAL**。

本轮只复用既有授权 Codex window `2B9E10E2-0C0A-47DC-AED7-206B833A4462` / workspace `28A52619-270D-4FC5-A692-C730028054E2` 及其已登录的独立 CODEX_HOME。在其中新增一个自有分屏恢复历史 B，保留原 A 同时运行；没有顺序复用一个 surface 作为并发证据。前轮输入：[历史报告](../S1-codex-final-edge-fd8f8aac/README.md)。本轮机器结果：[runtime-matrix.json](runtime-matrix.json)、[dependencies.json](dependencies.json)。

## 同时存活与可见身份

| 绑定 | A，保留原 TUI | B，本轮新分屏恢复 |
|---|---|---|
| native thread | `01a19ad4-2958-75f3-82b6-f488745caec4` | `01a19b0a-7662-7582-a5f0-36ea08fb1205` |
| surface | `4E483942-928F-455C-B03D-75DAEDE9F38F` | `2C84977B-4E1E-4790-BC80-2065AFF9D323` |
| pane | `F5B28B85-A830-4A7A-A976-FF3EFE0FD52C` | `9EC38438-F2F6-466F-8A1D-D1C4FEBFE909` |
| PID / wrapper | `96559` / `96558` | `4776` / `4775` |
| TTY | `ttys028` | `ttys003` |
| 默认 cwd，均相对于项目根 | `docs/evidence/S1-manual-login-retry-a128f1ae/scratch/codex/cwd` | `docs/evidence/S1-codex-final-edge-fd8f8aac/scratch/cwd-b` |
| 本轮私有 nonce | `S1-concurrent-private-A-9deeaae6-4f9d-4534-97b5-24b0ebbd066c` | `S1-concurrent-private-B-494e8013-8b98-4177-b51b-243ecb59a55a` |

B 于 `07:18:01Z` 启动。两次 checkpoint 分别于 `07:18:53Z`、`07:19:28Z` 完成，均记录两个不同 pane 里的 surface 为 selected、所在窗口 visible；每次还以单次 `ps` 同时取得两条 Codex 进程记录，并逐个核对 PID、PPID、TTY、resume argv、`lsof` cwd、只读 SQLite thread/rollout 以及原生屏幕。具体精确时间以原始记录为准。

原始绑定：[首次双存活绑定](both-seeded-binding.json)、[首次同时进程](both-seeded-both-live.json)、[后续双存活绑定](both-recall-inflight-binding.json)、[后续同时进程](both-recall-inflight-both-live.json)、[初始 A 屏幕](initial-A-screen.json)、[B 启动](b-launch.json)。文件名 `both-recall-inflight` 是 checkpoint 标签；实际采集时两边回忆回复已完成，未据此宣称当时正在推理或工具并行执行。

## 原生结果与隔离判断

1. 从 B cwd 用显式 A native ID 入队 A 私有 nonce，从 A cwd 用显式 B native ID 入队 B 私有 nonce；使用原授权环境，没有更换模型或 provider。两边均执行原生终端工具，在默认 cwd 打印各自 nonce、实际 cwd、workspace、surface 与工具 PID；native `CommandExecution` 退出 0，输出的 cwd/surface 与上述绑定一致。
2. 两边 native assistant 分别交付 `CONCURRENT_PRIVATE_ACK`，其原生 message ID、timestamp、工具事件、queue ID 与同 surface 可见屏幕在 [codex-verification.json](codex-verification.json) 中逐项关联。首次屏幕：[A](both-seeded-A-screen.json)、[B](both-seeded-B-screen.json)。
3. 再从对方 cwd 各自入队带不同 route nonce 的回忆请求，**没有重发私有 nonce**。两个 native assistant 各自返回 `CONCURRENT_RECALL`、自己的完整私有 nonce，并对其他 label 的私有 nonce 回答 UNKNOWN。后续屏幕：[A](both-recall-inflight-A-screen.json)、[B](both-recall-inflight-B-screen.json)。
4. 完整 native 记录和上述可见屏幕均不含另一边本轮 private/route nonce；A/B 原有 native 历史保留为前缀，新实验 turn 追加其后。最终只读快照：[A](final-A-native.json)、[B](final-B-native.json)。

因此本轮证明：两个同时存活、各有独立可见 surface 的 native TUI，在本次显式 native ID 路由实验中，消息、输出、默认 cwd 和对话上下文未串线。所有判断来自原生 assistant/工具记录和屏幕，cmux 命令成功、queue 退出 0、协作 handoff 均不单独构成模型 ACK。

## 清理与保护核对

只向核对身份后的本轮 B 发送 `/quit`，wrapper 捕获 [退出码 0](b-exit.json)，随后 [PID 查询为空](cleanup-b-process-absent.json)。只关闭本轮新建 B surface。关闭 RPC 退出 0 后的 [即时拓扑](final-tree.json) 尚显示旧布局；后续 [稳定拓扑](final-tree-settled.json) 确认新 pane/surface 已消失，原控制 shell 和 A 的布局、pane/surface ID 集合与初始完全相同。保留两次观察，不以 RPC 成功替代清理完成。

A 从始至终保留原 PID `96559`，native ID 和 cwd 未切换，最终 [原进程](final-A-process.json) 存活、[屏幕输入栏为空](final-A-screen-settled.json)。本轮没有发送进程终止信号。B 历史保留，可按原 ID 后续恢复；没有删除 native 会话或原证据。

实际执行 `python3 docs/evidence/S1-codex-concurrent-tui-754ba5a3/finalize.py`，断言 **PASS**：[evidence-validation.json](evidence-validation.json)、[protected-files-verification.json](protected-files-verification.json)。Go 源码、构建定义、原独立配置以及输入目录中所有既有非 scratch 证据文件哈希前后相同；scratch 中的原生 rollout 合法追加探针记录，不据此宣称整个 CODEX_HOME 字节不变。原生凭据由 Codex 在既有授权环境中使用，记录器没有读取或复制凭据。

本轮未运行 Go test/build/check，变更只有证据记录。OpenCode 模型调用 0，Claude 操作 0，没有登录、购买、输入凭据或修改 provider 配置；没有向当前协作 main/worker 发送实验输入，不进入 S2。`ochist` / `jev-judge` 不在 PATH，见 [可用性记录](cli-availability.json)，没有伪造历史查询或风险判断。

共享 CODEX_HOME 与操作系统文件系统不构成安全隔离，本轮不推断文件访问权限、任意负载或崩溃场景。hook、AgentFlow 适配器、无 TUI 自动唤醒、主机/自动恢复、中断工具完成/恰好一次及通用草稿门禁继续 UNVERIFIED；OpenCode 的模型 provider 依赖沿用前轮。正式协作回传后另存 `handoff.json`，仅表示此证据任务交付完成。
