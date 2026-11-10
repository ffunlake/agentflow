# S1 用户窗口 busy 与非空草稿保护：468c9508

协作 session `468c9508-c700-42a7-9d91-81ffdeb46ff2`，task `s1-user-window-busy-draft`，模式 `ralph-loop`。本轮交付完成，完整 S1 仍 **PARTIAL**。Codex 原生 queue 的 busy 顺序交付和非空草稿保护 **PASS，仅本次存活用户 TUI 的受控场景**；OpenCode 对应两项 **UNVERIFIED**。

## 原目标与输入门禁

共同 window `73FEB4F4-BE2B-4F4D-9B48-E872BBC51EA3`，workspace `3AB7D16C-B09D-44AE-8CD5-5B6225388D45`，cwd `/Users/funlake/Projects/okra-wiki`。

| 工具 | surface | 原 native ID | PID / TTY | 本轮状态 |
|---|---|---|---|---|
| OpenCode | `5265DA42-9A45-4C71-BABE-155E458FCF33` | `ses_e64c2fa39ffeI5sYf6iCWDKYpv` | `23097 / ttys003` | 两项 UNVERIFIED，新增模型请求 0 |
| Codex | `94EBBAAE-5EA1-4352-B183-580E24F0F23C` | `01a19b3b-9fc2-7622-a43f-af895cfac312` | `23477 / ttys004` | busy / 草稿两项受控 PASS |

[前置门禁](pre-input-gate.json) 关联原始 [拓扑](before-tree.json)、[进程](before-processes.json)、[Codex screen](before-codex-screen.json)、[OpenCode screen](before-opencode-screen.json)、[原生 Codex](before-codex-native.json)、[原生 OpenCode](before-opencode-native.json)。输入前人工核对两侧均空闲且无草稿；可见上一轮原 ID、原 ACK 与 native 日志一致。`lsof` 仅作文件/进程关联，不单独决定活跃 thread。

## Codex busy：真实工具期间入队，原 turn 完成后 ACK

唯一 busy nonce `S1-busy-468c9508-b460c0b5-449c-46c8-a0cf-eb37a9e9bdb1`。通过已确认空输入栏发送一次 [只读 busy 请求](busy-request.json)，仅运行 Python 打印 PID/cwd、睡眠 45 秒并打印结束标记。native CommandExecution `exec-bbf9116c-0ef3-4cfc-8345-32753c997515`，原生工具 session `46150`，实际工具输出 OS PID `60102`。工具 session 不是 OS PID。

| 事件 | UTC / 事实 |
|---|---|
| 原生真实工具区间 | `08:38:53.283–08:39:38.211`；cwd 与用户目标一致；最终 exit=0 |
| 30 秒后原生工具状态 | `08:39:23.301` 返回 live `session_id=46150`，仅 `BUSY_BEGIN`，尚无 `BUSY_END` |
| [原生 queue](busy-queue.json) 入队 | `08:39:33.879498–08:39:34.131827`，完整位于工具区间内 |
| queue ID | `01a19b69-f1eb-7ee0-9c0c-526623c9d5ea` |
| 原 busy turn 完成 | `08:39:39.791`，turn `01a19b69-2f58-7591-9922-79d88bfa4429`，先有真实 assistant `BUSY_DONE` |
| queue turn 开始 | `08:39:39.805`，`turn_trigger=queue`，turn `01a19b6a-0811-7ee0-a1dc-a8626b9ac04d` |
| 接收端原生 assistant ACK | `08:39:41.497`，`msg_05c58aa9a1c62b8e016ae84c4d6c54819b92187aff83559296` |

队列 nonce `S1-busy-queue-468c9508-704c41fa-4438-477d-8525-1232d6b05d7b`。正文由 `codex queue --thread <原ID> --message ...` 投递，不经终端输入栏；没有 `--resume`。完整 [native transcript](final-codex-native.json) 和 [同 surface 屏幕](busy-after-queue-screen.json) 共同证明原任务完成后才产生唯一 `QUEUE_ACK`。

## Codex 非空草稿：ACK 后逐字保留，未形成 user message

在 busy 验证结束、输入栏重新为空后，仅输入自有 marker `S1_OWN_DRAFT_468c9508_5f562e71350f`，没有按 Enter。比较 [投递前原屏幕](draft-before-queue.json) 与 [ACK 后原屏幕](draft-after-queue.json)，末尾输入行逐字相同。另一 nonce `S1-draft-queue-468c9508-4e470bad-bdf6-402d-948e-2028b564fbba` 通过原生 queue 投递：

- queue ID `01a19b6b-3a1a-7fb1-b933-4ed36dab9b04`；[原始命令](draft-queue.json)。
- 原生 turn `01a19b6b-5496-7233-8d11-ea07e82d988f`，`turn_trigger=queue`。
- 接收端 assistant `msg_05c58aa9a1c62b8e016ae84ca29248819b94dd1034f4e538b3` 于 `08:41:06.538Z` 返回完整 nonce ACK。
- ACK 后的 [native transcript](draft-after-queue-codex-native.json) 与最终 transcript 均无 marker，native user message 计数为 0；草稿既未覆盖，也未自动提交。
- 核对输入栏仍为完整自有 marker 后，仅一次 [ctrl+u](draft-clear-owned.json) 清除；[最终屏幕](final-codex-screen.json) 回到 `Ask Codex to do anything` 空输入栏。

每条 queue nonce 在 canonical `response_item` 中恰好一条 user、一条 assistant；原生 `item_completed` 为同 ID 的事件镜像，不计作第二次交付。计数限于最终 native 快照 `08:41:31.400365Z`；不推广为通用 exactly-once 保证。两条 ACK 均有同 surface 的实际模型回复，不以 queue exit=0 或协作 handoff 代替。

## OpenCode：UNVERIFIED，未强行投递

原 PID 的 [TCP 监听查询](opencode-listening-tcp.json) 返回无监听；[Unix socket 查询](opencode-unix-sockets.json) 只显示未命名 socket，没有可绑定本会话的地址；[CLI 帮助](opencode-help.json) 没有 queue 命令。帮助中的 `run` / `serve` / `attach` 不证明能够向本运行中 TUI 安全排队。

因此本轮没有启动 OpenCode busy、放入草稿或发 nonce，没有另启 runtime、直接插库或借终端提交冒充 native queue。用户明确允许安全外部 queue 不可确认时记录 UNVERIFIED。结论只适用于当前原进程的已查入口，不断言所有 OpenCode 部署不支持队列。原生 session/messages/parts 全部前后相同，原屏幕也相同。

## 最终状态、文件保护与证据边界

[最终拓扑](final-tree.json)、[进程](final-processes.json)、两侧 cwd 及屏幕表明原 window/workspace/surface、PID/TTY/cwd 和布局保留，两侧输入栏均为空。长工具自然完成，[最终 PID 查询](busy-tool-final-process.json) 查无该 PID。Codex 原 rollout 的原始字节前缀 SHA-256 保留；OpenCode 原 native 内容完全未变。

[证据断言](evidence-validation.json) 核对真实工具区间、排队 turn 顺序、唯一 native user/assistant IDs、草稿逐字保留与未提交、清理后空栏、原 native 历史和目标绑定；[机器矩阵](runtime-matrix.json) 将 PASS 与 UNVERIFIED 分开。[文件保护核验](file-protection-verification.json) 对比 wiki 399 个原文件及 Git status、agentflow 既有非 scratch 证据和源码；[SHA-256 清单](sha256-manifest.json) 为最终文件清单。

本轮仅新增此证据目录并更新两份授权 S1 总文档。无 reset/clear/new、退出、kill、故障注入、provider 修改、Go 修改、wiki 文件写入、Claude 操作或向协作窗口发送实验消息。没有运行 Go test/build/check 或 doctor。新增 canonical user 请求共 3 条，assistant 最终回复 3 条，真实 CommandExecution 1 次；原生 wrapper 的 exec / wait 不计成两次命令执行。

一次 `ps command=` 采样意外包含无关 MCP 的敏感启动参数；当前轮原始采样已替换为 [脱敏元数据](busy-tool-process.json)，没有把这些值留在交付文件中，也未主动读取认证文件。该采样没有捕获独立 TTY 的 busy Python，未用于 busy 判定；busy 证据来自原生真实工具输出、live session 和 CommandExecution 时间。其他进程记录只保留 `ucomm`。未覆盖任何前轮证据。

OpenCode 安全外部 queue、通用草稿门禁、AgentFlow hook/queue 适配器、自动恢复等仍 UNVERIFIED。上一轮 reset/角色/手动恢复结果及更早 19 项受控通过保留各自环境边界，本轮两项新观察不作重复累计。完整 S1 仍 PARTIAL。正式协作回传另存 [handoff.json](handoff.json)，仅表示本轮交付完成。
