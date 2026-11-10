# S1 Codex 原生中断工具执行语义

协作 session `0cf09894-116f-4abe-b601-93b8572a86bb`，task `s1-codex-interrupt-semantics`，模式 `ralph-loop`。2026-11-02，Codex `0.166.0`。

**本轮不是整体 PASS：Ctrl-C 单独终止已 yield 的后台工具为 FAIL_OBSERVED。** 原生 turn 中断、追加 `/stop` 的工具清理、同 thread 手动恢复后新请求仅执行一次和真实 nonce ACK 均已观察。保留 19 项历史 PASS，`new_runtime_passes=[]`；完整 S1 保持 PARTIAL。

## 实际身份与动作

只操作授权 window `2B9E10E2-0C0A-47DC-AED7-206B833A4462` / workspace `28A52619-270D-4FC5-A692-C730028054E2` / surface `4E483942-928F-455C-B03D-75DAEDE9F38F`。原 A thread `01a19ad4-2958-75f3-82b6-f488745caec4`，原 Codex PID `96559` / wrapper `96558` / TTY `ttys028`；原 cwd 为 `S1-manual-login-retry-a128f1ae/scratch/codex/cwd`。恢复后同 thread / surface / cwd，Codex PID `11697` / wrapper `11696`，仍为 `ttys028`。没有创建新 thread 或 surface。

长工具 nonce `S1-interrupt-e70530c3-84ed-4de3-a697-26ad09251dfe`；原生工具执行 `/usr/bin/python3 …/recorder.py tool <nonce>`，只写本目录事件与独占启动标记，然后睡眠 240 秒。操作前已核对空闲、空输入栏、native `/status`、PID、cwd 与目标 surface；工具由 Codex 直接启动，实际 OS PID / PGID `10800`，PPID `96559`。原生工具 session `38086` 是工具会话标识，**不是 OS PID**。

| UTC 时间 | 实际事件 | 证据 |
|---|---|---|
| 07:28:47.358 | 工具 PID `10800` 写 TOOL_BEGIN 并睡眠 | [生命周期](tool-events.jsonl)、[PID / PPID / PGID](arm-tool-process.json) |
| 07:29:12.658 | 定向发送一次 `ctrl+c` 后，native `turn_aborted`，reason=`interrupted`，turn `01a19b29-0827-7202-8ebf-59971d59c316` | [实际按键](native-ctrl-c.json)、[动作记录](native-interrupt-action.json)、[中断后 transcript](post-interrupt-native.json) |
| Ctrl-C 后至 `/stop` 前 | 两次 `ps` 仍查到 PID `10800`，屏幕显示 `1 background terminal running · /stop to close` | [中断后 PID](post-interrupt-tool-process.json)、[清理前 PID](pre-stop-tool-process.json)、[原始屏幕](pre-stop-screen.json) |
| 07:30:18.919–.920 | 使用 TUI 提示的原生 `/stop`；native CommandExecution status=`failed` / exit_code=`-1`；独立 Darwin EVFILT_PROC / NOTE_EXIT 监听到准确 PID 的退出 | [原生清理动作](native-stop-action.json)、[退出事件](tool-kernel-exit.json)、[工具失败记录](post-stop-native.json) |
| 07:30:36.408 | 确认工具 PID 不存在且空输入栏后正常 `/quit`；旧 wrapper 捕获 Codex exit=0 | [工具不存在](post-stop-tool-process.json)、[wrapper 原始退出副本](original-wrapper-exit.json) |
| 07:30:45.178 | 同 surface 手动 `resume 01a19ad4-2958-75f3-82b6-f488745caec4` | [精确启动信息](resume-process.json)、[原生状态与屏幕](resumed-before-request-screen.json) |
| 07:31:11.200 / 07:31:17.454 | 新 nonce 的工具 PID `12780` 执行一次、exit=0，native assistant 返回唯一 ACK | [执行尝试](recovery-attempts.jsonl)、[工具结果](recovery-once.json)、[native transcript](recovery-observed-native.json)、[可见 ACK](recovery-observed-screen.json) |

`/stop` 前确认只有该自有后台工具；没有发送外部 OS signal、SIGKILL 或 kill 命令。Python SIGINT/SIGTERM handler 和 atexit 没有留下事件；内核 NOTE_EXIT 的原始 data=0、native exit=-1 均不能推出具体终止信号或成功退出，**实际终止 OS signal 保持 UNVERIFIED**。PID 退出由内核事件和随后多次 `ps` 不存在共同证明。

## 无伪造完成、重复交付与手动恢复

长工具只启动一次。生命周期只有 TOOL_BEGIN，没有 TOOL_COMPLETE；原生 CommandExecution 最终是 failed，原中断 turn 没有 task_complete，assistant 没有交付 `INTERRUPT_TOOL_DONE`。原生 `Script completed` 是 exec 包装器返回，带着仍存活的工具 session `38086`；`item_completed` 是工具生命周期事件，不等同于自然成功或业务交付。恢复后屏幕虽显示历史 `Ran` 工具摘要，其正文只有 TOOL_BEGIN，native failed 记录保留。

新请求 nonce 为 `S1-resume-once-d0bb72df-547a-4abd-b811-3358e9154b6c`。原生 user message 只出现一次，新 `exec_command` 只有一次；工具每次尝试先追加审计事件，再创建独占结果文件，因此重复调用不会被独占文件隐藏。实际只有一个 EXECUTION_ATTEMPT、一个成功 CommandExecution、一个 canonical assistant ACK：

```text
RESUME_ONCE_ACK S1-resume-once-d0bb72df-547a-4abd-b811-3358e9154b6c
Interrupted tool nonce: S1-interrupt-e70530c3-84ed-4de3-a697-26ad09251dfe
Concurrent private label A nonce: S1-concurrent-private-A-9deeaae6-4f9d-4534-97b5-24b0ebbd066c
Long tool: interrupted; no TOOL_COMPLETE observed.
```

模型最终消息 ID `msg_06713d29b1c1110c016ae83c4245ac819b8b97f2b45b018503`，恢复 turn `01a19b2b-3c54-7cf1-910c-9b94d416db79`。两个回忆值均未出现在[恢复请求](recovery-request.json)中。实际工具 cwd / workspace / surface 与原身份一致，ACK 和工具结果均在同 surface 可见。canonical response_item 与 event_msg 对同一消息的投影按 message ID 区分，不把两个投影计为重复 ACK。

最终观察覆盖原工具开始后的 240 秒自然完成期限；PID 仍不存在，原工具没有恢复或迟到交付。仅证明本次有界观测中新请求执行一次，不证明通用 exactly-once、事务、任意工具、自动恢复、自动重放或主机故障恢复。

## 交付与核验

[运行矩阵](runtime-matrix.json)、[依赖](dependencies.json)、[原生关联与边界](codex-verification.json)、[仅本轮 native transcript](native-transcript-delta.json)、[完整最终 native 记录](final-native.json)、[最终屏幕](final-screen.json)、[最终进程](final-process.json)。原始 cmux stdout/base64 与逐行解码副本均保留；native 记录只过滤 system/developer 消息，保存源路径、原始字节数和 SHA-256。没有修改原生 transcript。

[证据断言](evidence-validation.json) 的 PASS 表示记录自洽且断言符合事实，包含 Ctrl-C 工具仍存活的负向断言，**不表示中断功能整体验收 PASS**。[文件核验](protected-files-verification.json) 对账 Go、构建定义、既有非 scratch 证据及原配置；[native 历史保护](native-history-preservation.json) 核对 A/B 历史字节前缀，B 全文未变化。旧 wrapper 在正常退出时自动新增此前不存在的 `S1-codex-final-edge-fd8f8aac/a-final-exit.json`，没有覆盖旧文件。顶层两份 S1 文档更新为本轮实际结果。

本轮未改 Go、未运行 Go test/build/check 或 doctor、未进入 S2。OpenCode 模型调用 0，Claude 无操作；没有登录、购买、读取/复制凭据或 provider 配置变更，没有向协作 main/worker 发送实验输入。历史搜索 CLI 和 Jev CLI 均不可用，未伪造判断；复用用户提供的前轮证据。最终正式回传存于本目录 `handoff.json`，DONE 只表示报告交付；哈希清单与回读核验存于 `sha256-manifest.json`、`hash-verification.json`。
