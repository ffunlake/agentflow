# S1 Codex 边界证据（eb2e9a87）

日期：2026-11-02；协作 session `eb2e9a87-d909-47c1-8015-ae54615ace35`；task `s1-codex-edge-cases`。**本轮指定边界实验完成；完整 S1 仍 PARTIAL。** 新增 7 项受控 runtime_passes，继承此前 9 项 Codex 通过记录。机器结果见 [runtime-matrix.json](runtime-matrix.json)、[Codex 验证](codex-verification.json)、[断言](evidence-validation.json)、[依赖](dependencies.json)。全部新记录独立保存，记录器使用排他创建，不覆盖已有证据。

## 唯一实验目标

| 字段 | 真实绑定 |
|---|---|
| window | `2B9E10E2-0C0A-47DC-AED7-206B833A4462` |
| workspace | `28A52619-270D-4FC5-A692-C730028054E2` |
| surface / TTY | `4E483942-928F-455C-B03D-75DAEDE9F38F` / `ttys028` |
| native thread（全程相同） | `01a19ad4-2958-75f3-82b6-f488745caec4` |
| 初始 PID / 最终 PID | `59794` / `70876` |
| 原 cwd A / 最终 cwd | `../S1-manual-login-retry-a128f1ae/scratch/codex/cwd`（相对本证据目录） |
| 临时 cwd B | `scratch/cwd-b` |
| CODEX_HOME | 复用前轮 `scratch/codex/codex`，原生进程读取其已有登录态；没有代理读取或复制 auth 文件 |

初始 [tree](target-tree-initial.json)、[进程](target-process-initial.json)、[屏幕](target-screen-initial.json)、[native rollout](native-initial.json)，最终 [tree](target-tree-final.json)、[进程](target-process-final.json)、[原生 /status](final-status-screen.json)、[native rollout](native-final.json) 相互核对。SQLite 以只读模式读取且只按指定 thread 查询；rollout 保留原生 user/assistant/tool/event 记录，省略 system/developer 消息及 session_meta 基础指令。原始屏幕 JSON 包含 cmux stdout/base64，`*-decoded.json` 为可读副本。

## 实际实验结果

| 场景 | 真实结果 | 原始证据 |
|---|---|---|
| 非空草稿 + 后台 queue | **PASS，仅 native queue 路径**。先粘贴未提交的唯一草稿；两条队列消息分别获得同 thread 原生 assistant ACK，草稿保持完整，未进入原生 user 消息。最后仅用 Ctrl+U 清除本实验草稿。 | [草稿前](draft-before-queue.json)、[queue 命令](draft-queue.json)、[第二条命令及警告](draft-resume-queue.json)、[两条 ACK 后草稿](draft-complete-screen.json)、[rollout](native-draft-complete.json)、[清理](draft-cleared.json) |
| busy 投递 | **PASS，原生队列延后执行**。模型实际执行 40 秒只读等待命令，queue 的开始时间位于原生 CommandExecution 开始/结束区间内。原任务 `BUSY_DONE` 后才出现队列 user 消息与 ACK，没有打断原任务。 | [busy 请求](busy-request.json)、[busy 屏幕](busy-before-queue.json)、[queue](busy-queue.json)、[投递后](busy-after-queue.json)、[原生完成记录](native-busy-complete.json)、[可见 ACK](busy-complete-screen.json) |
| 发送者 cwd 不同 | **PASS，显式 thread 路由**。queue 子进程从 cwd B 执行，接收 TUI 仍在 cwd A，唯一 nonce 被同一 thread ACK。 | [准确 argv/cwd/退出结果](different-sender-cwd-queue.json)、[rollout](native-different-sender-cwd.json)、[同 surface 屏幕](before-abnormal-exit-screen.json) |
| 同 thread 的 cwd A/B 对照 | **PASS，单 thread 受控切换**。A 中实际工具打印 cwd A；从 B resume 时原生菜单明确询问 session/current directory，选择一次性“Use current directory”，工具实际打印 cwd B，workspace/surface 不变。最后用同样的一次性选择恢复 A。 | [A 工具执行](native-cwd-a.json)、[A 屏幕](cwd-a-screen.json)、[恢复菜单](resume-cwd-b-screen.json)、[选择](resume-cwd-select-current.json)、[B 工具执行](native-cwd-b.json)、[B 屏幕](cwd-b-screen.json)、[最终菜单](resume-final-cwd-menu.json)、[最终状态](final-status-screen.json) |
| 异常退出后的上下文恢复 | **PASS，仅空闲时 SIGKILL**。先核对 PID/TTY/argv/native ID，再仅 kill PID 59794；既有包装器记录 -9，ps 返回 1 且无输出，surface 留有 shell。同 surface、同 CODEX_HOME 手动 resume 原 thread，模型从历史返回 `CRASH_RESTORED` + 原 nonce；恢复提示未包含完整 nonce。 | [进程门禁](pre-abnormal-exit-process.json)、[真实信号命令](abnormal-exit-signal.json)、[包装器退出](abnormal-exit-wrapper-result.json)、[无原进程](post-abnormal-exit-process.json)、[保留 shell](shell-after-abnormal-exit.json)、[恢复启动](resume-cwd-b-launch.json)、[恢复请求](crash-recovery-request.json)、[原生恢复](native-crash-recovered.json)、[可见恢复](crash-recovered-screen.json) |
| 存活 TUI 的原生 queue 后台接收 | **PASS**。queue 使用明确 thread ID 和原隔离 CODEX_HOME；不向终端输入消息正文，由存活的 `--no-daemon` TUI 接收；五个实际 queue ID、native assistant message ID、nonce、屏幕均列于机器结果。 | [全部逐项结果](codex-verification.json)、[最终原生记录](native-final.json) |
| 停止期间入队，随后手动 resume | **PASS，仅持久化与手动恢复接收**。停止后 `queue --resume` 返回 0，同时明确警告无法 resume。观测时 rollout 保持不变；手动启动 TUI 后才出现该消息与模型 ACK。 | [入队及警告](stopped-resume-queue.json)、[停止期间 native 快照](native-stopped-after-queue.json)、[停止屏幕](stopped-after-queue-screen.json)、[手动恢复后 native ACK](native-stopped-queue-after-resume.json)、[可见 ACK](stopped-queue-after-manual-resume-screen.json) |

每个场景的 native ID、surface/workspace、命令与原始记录均由 [codex-verification.json](codex-verification.json) 关联。模型最终自述中的身份/cwd 不用于证明工具执行；cwd 对照使用原生 CommandExecution 的真实输出，ACK 使用原生 assistant message 与屏幕交叉核对。协作 READY/handoff 不构成 native 证据。

## 保留的 UNVERIFIED 与实现范围

- `hook_background_delivery`：没有执行 hook 实验；Claude 排除，没有安装或更改 hook。
- `agentflow_hook_queue_adapter`：当前 Go 工程只有 probe 候选命令/帮助检查，完整运行时适配器未实现；见 [源码清单及相关行](source-before-checks.json)。本轮原生命令实测不代表 AgentFlow 已实现 send 或 queue。
- `queue_autowake_without_live_tui`：真实 `--resume` 警告与停止期间不变的 rollout 证明这条尝试未唤醒目标。没有启动 daemon/server 来扩大本轮范围，自动唤醒能力继续 UNVERIFIED。
- `different_cwd_two_native_threads`：本轮只授权一个 native thread，同 thread 的 A/B 对照不能证明两个独立 thread 的 cwd 隔离。
- `crash_mid_turn_or_machine_failure_recovery`：只验证空闲进程被强制终止后的手动恢复；没有验证执行中、机器故障、真实偶发崩溃或 supervisor 自动恢复。
- `general_terminal_draft_guard`：原生 queue 保留草稿不证明通用终端注入有草稿拒绝门禁。AgentFlow 尚未实现该门禁。

## 工程检查与操作边界

| 实际命令 | 退出码 | 原始记录 |
|---|---|---|
| `go test ./...` | 0 | [go-test.json](go-test.json) |
| `go build ./...` | 0 | [go-build.json](go-build.json) |
| `make check` | 0 | [make-check.json](make-check.json) |
| `python3 docs/evidence/S1-codex-edge-cases-eb2e9a87/validate.py` | 0 | [逐项断言](evidence-validation.json) |

本轮新增证据记录器 [harness.py](harness.py) 和证据校验器 [validate.py](validate.py)，没有修改 Go 源码。源码/Makefile/go.mod/go.sum 在工程检查前后哈希一致，见 `source-before-checks.json` / `source-after-checks.json`；快照始于实验过程中，不能声称是任务开始前全盘快照。终端变更命令逐条断言仅定向授权的 window/workspace/surface，没有向 main/worker 或其他项目发送实验输入，没有创建窗口/split/worktree/提交，没有进入 S2。

原生状态变更是实验预期结果：原隔离 CODEX_HOME 中指定 thread 的 rollout/SQLite 继续追加，既有前轮包装器在 SIGKILL 时自动新增 `../S1-manual-login-retry-a128f1ae/codex-resume-exit.json`（此前不存在），其 -9 结果已保存到本目录；前轮既有证据未覆盖。恢复菜单只选择一次性的 current directory，没有选择 Always，没有改全局配置。

OpenCode 保留 [前轮 transport 证据](../S1-manual-login-retry-a128f1ae/opencode-verification.json)，native `ses_e65a4cf24ffe3wZAiM2U7og4Px`，本轮请求数 0，未调用模型、登录或购买。Claude 本轮调用数 0。ochist 与 jev-judge 不在 PATH，分别使用指定输入证据继续、记录判断不可用，没有伪造任何工具判断。

最终 PID 70876、wrapper PID 70874 保持存活，原 thread、原 cwd A、空输入栏，供后续已授权 S1 使用。继续完整 S1 的依赖见 [dependencies.json](dependencies.json)。正式回传 `status=DONE` 已退出 0，确认 `[DONE] handoff sent: id=c97e64db-f03f-486f-b108-ac9488cca97f window=1 status=DONE`，原始 argv/stdout/stderr 见 [handoff.json](handoff.json)。该回传不计为 native ACK。[文档校验](document-validation.json) 保存回传前的 170 个有效链接及 129 个文件哈希快照；本段回传说明与 handoff 文件在该快照之后追加。
