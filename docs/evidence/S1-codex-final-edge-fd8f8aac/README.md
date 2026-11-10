# S1 Codex 最后两项边界补证（fd8f8aac）

本轮两项受控验收 **PASS**：两个独立 native thread 的不同 cwd / 消息 / 私有上下文隔离，以及工具执行期间单进程 SIGKILL 后手动恢复并继续交付。完整 S1 保持 **PARTIAL**，没有进入 S2。协作 session `fd8f8aac-71a7-4e9e-845e-8cd6b458e468`，task `s1-codex-final-edge`，角色“资深研发”。

机器结果：[runtime-matrix.json](runtime-matrix.json)、[dependencies.json](dependencies.json)。原始索引与断言：[codex-verification.json](codex-verification.json)、[evidence-validation.json](evidence-validation.json)。前轮 16 项 Codex 受控通过保留，本轮新增 2 项，总计 18 项；这不是完整 S1 出口通过。

## 授权目标

仅操作既有 window `2B9E10E2-0C0A-47DC-AED7-206B833A4462` / workspace `28A52619-270D-4FC5-A692-C730028054E2` / surface `4E483942-928F-455C-B03D-75DAEDE9F38F`，TTY `ttys028`。同一授权可见 surface **依次**运行两个独立 thread，没有新增窗口或分屏。原生版本 `0.166.0`，复用既有隔离 CODEX_HOME 登录态；没有登录、购买、输入或复制凭据、修改 provider 配置。初始空输入栏和身份绑定见 [screen](target-screen-initial.json)、[process](target-process-initial.json)、[tree](target-tree-initial.json)。

| 身份 | Native thread ID | 实际默认 cwd | 私有 nonce |
|---|---|---|---|
| A，原 thread | `01a19ad4-2958-75f3-82b6-f488745caec4` | `/Users/funlake/Projects/agentflow/docs/evidence/S1-manual-login-retry-a128f1ae/scratch/codex/cwd` | `S1-private-A-7706d222-65e2-421f-b679-32d6c3d20756` |
| B，本轮新建 | `01a19b0a-7662-7582-a5f0-36ea08fb1205` | `/Users/funlake/Projects/agentflow/docs/evidence/S1-codex-final-edge-fd8f8aac/scratch/cwd-b` | `S1-private-B-e9b0ebc9-28d0-43fb-be71-0055d4290ee7` |

## 双 thread 隔离

结论 **PASS_FOR_CONTROLLED_SEQUENTIAL_NATIVE_THREADS**。A、B 分别真实运行默认 cwd 的打印命令，工具输出包含私有 nonce、实际 cwd、workspace 和 surface，模型返回 `PRIVATE_ACK`。原始请求：[A](seed-A-request.json)、[B](seed-B-request.json)；native 工具和 assistant message：[A](native-a-seeded.json)、[B](native-b-seeded.json)；可见结果：[A](a-seeded-screen.json)、[B](b-seeded-screen.json)。B 原生 ID 和 cwd 另有 [/status](b-status-screen.json)。

| 定向投递 | Queue ID / 实际接收 |
|---|---|
| cwd B 发给暂停 A，同时可见 TUI 为 B | `01a19b0c-5dd3-7311-8d03-acd65840fe54`；A rollout 不变，B 未接收；手动 resume A 后才返回 A 的真实 ROUTE_ACK |
| cwd A 发给可见 B | `01a19b0c-5e0e-7413-a834-71578ecb1128`；B 直接返回 B 的真实 ROUTE_ACK |

原始 queue 命令：[发 A](route-a-while-b-live-retry.json)、[发 B](route-b-from-a-cwd-retry.json)。请求没有重发私有 nonce，两者正确回忆自身 nonce，并声明当前对话没有另一 label 的私有 nonce。断言器实际核对：A 暂停期间 rollout 与 seed 后一致；B 完整 native transcript 不含 A 私有 nonce 或 A 的路由 nonce，A 完整 native transcript 不含 B 私有 nonce 或 B 的路由 nonce。见 [暂停 A](native-a-stopped-with-queued-message.json)、[A ACK](native-a-route-ack.json)、[A 可见 ACK](a-route-ack-screen.json)、[B ACK](native-b-route-complete.json)、[B 可见 ACK](b-route-screen.json)。

结论限于同 surface 顺序运行的两个独立 thread 与显式 native ID queue 路由。**两个 TUI 同时存活的并发隔离未测试**；Full Access 环境不构成文件系统安全隔离。旧的单 thread cwd 切换不作为本轮双 thread 证据。

## 执行中退出和恢复交付

结论 **PASS_FOR_MANUAL_MID_TOOL_SIGKILL_RECOVERY**。只对新建 B 的 Codex PID `93342` 发送一次 SIGKILL；原 A 当时已经正常退出并保存，未作为信号目标。wrapper `93341`、TTY `ttys028`、`resume 01a19b0a-7662-7582-a5f0-36ea08fb1205` argv、实际 cwd、同 surface `/status`、进行中 native turn 和带 nonce 的实际工具进程均在信号前核对：[安全决定](signal-safety-decision.json)、[即时 PID](immediate-pre-signal-process.json)、[实际 cwd](pre-signal-cwd.json)、[即时工具进程](immediate-pre-signal-tool.json)。

工具仅打印并睡眠 120 秒，nonce `S1-midflight-8fb7cf0b-801d-4b15-ac5b-077e4232e076`，OS PID `95169`。原生 tool output 已打印 `MIDFLIGHT_BEGIN`、PID 和 cwd，返回未结束的 tool session `35636`；信号前该工具仍是 B 的直接子进程。native turn `01a19b10-9f15-7892-85cd-6ec20c8fe84e` 尚无 task_complete / task_aborted。见 [请求](midflight-retry-request.json)、[native BEGIN 与进行中 turn](native-b-pre-signal.json)、[执行中屏幕](pre-signal-screen.json)。

`2026-11-02T07:02:21.810260Z` 发 SIGKILL，wrapper 实际退出 **-9**，surface 返回自有 shell：[信号](signal-result.json)、[退出](b-resume-exit.json)、[shell](b-after-signal-shell.json)。随后原 cwd、原 surface 手动 resume **相同 B native ID**，新 PID `95478`；界面显示 `Conversation interrupted`：[进程](b-recovered-process.json)、[屏幕](b-after-resume-screen.json)。

[恢复请求](recovery-request.json) 未重发 B 私有 nonce，并明确不重跑 sleep。恢复后同 thread 真实执行 `/bin/pwd`，模型交付：

```text
RECOVERY_DELIVERED B S1-private-B-e9b0ebc9-28d0-43fb-be71-0055d4290ee7 /Users/funlake/Projects/agentflow/docs/evidence/S1-codex-final-edge-fd8f8aac/scratch/cwd-b
```

实际工具、assistant message ID、时间和可见 surface 一致：[native 交付](native-b-recovery-delivered.json)、[可见交付](b-recovery-delivered-screen.json)。B 随后 `/quit` 正常退出 0：[退出](b-recovered-exit.json)。

限制：需要手动 resume 和恢复请求。被中断工具未交付 `MIDFLIGHT_DONE`；不证明原工具自动完成、恰好执行一次、事务恢复、主机故障或 AgentFlow 自动 supervisor。旧睡眠 PID `95169` 一度成为 PPID 1 的孤儿：[观察](post-signal-process.json)；最终记录 PID 不存在：[核对](bounded-tool-final-process.json)。没有向该子进程、进程组或其他会话发第二个信号，没有执行用户数据操作。

## 最终状态和核验

同 surface 已恢复原 A ID、原 cwd。模型在未重发 A nonce 时返回 `ORIGINAL_RESTORED`，并真实执行 `/bin/pwd`：[native A](native-a-final.json)、[交付屏幕](a-final-delivered-screen.json)。最终 Codex PID `96559` / wrapper `96558` / TTY `ttys028`、空输入栏：[最终 /status](a-final-status-screen.json)、[进程](target-process-final.json)、[拓扑](target-tree-final.json)。B 保留 native 记录并正常停止。

Go 源码、Makefile、go.mod、go.sum、既有隔离配置和前轮已经存在的顶层证据哈希前后相等。前轮存活 wrapper 在 A 第一次 `/quit` 时自动新增此前不存在的 `S1-codex-edge-cases-eb2e9a87/resume-final-exit.json`，退出 0；没有覆盖原证据。所有 cmux 实验变更命令逐条审计仅指向授权目标，目标拓扑保持一致：[文件与命令核验](protected-files-verification.json)。未改 Go 代码，按本轮要求 **未运行** `go test ./...`、`go build ./...`、`make check`。

[harness.py](harness.py) 只采集实验，[finalize.py](finalize.py) 从 native 消息、工具输出、屏幕、PID 和哈希推导矩阵；不是 AgentFlow 新功能。实际 [证据断言 PASS](evidence-validation.json)。文档链接、JSON 和证据清单另由 `finalize.py documents` 核验。

保留失败尝试：首批 queue 误带 TUI 参数 `--no-alt-screen`，原生退出 2：[A](route-a-while-b-live.json)、[B](route-b-from-a-cwd.json)；修正 argv 后生成新 nonce，只有真实 native ACK 计入通过。首条 45 秒任务在信号前已自然完成：[原生结果](native-b-first-sleep-completed.json)，不计为异常退出证据。

`ochist` 和 `jev-judge` 不可用：[历史 CLI](history-cli-availability.json)、[风险 CLI](risk-cli-availability.json)。复用指定 S1 原生证据，无虚构风险判断；仅在明确授权、无用户数据副作用且 native/PID/TTY/cwd/活动工具均核对后终止本轮 B。OpenCode 本轮新模型请求 0，沿用 **VERIFIED_TRANSPORT_ONLY / WAITING_DEPENDENCY**；Claude 排除；main/worker 未作为实验目标。

正式回传执行绝对路径 `agent-handoff.sh send` 并捕获到本目录 `handoff.json`；以实际出现 `[DONE] handoff sent` 确认成功。DONE 表示本轮交付，不是 native ACK 或完整 S1 通过。
