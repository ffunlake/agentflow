# S1 Codex 无 TUI 自动唤醒实测

结论：**UNVERIFIED / WAITING_DEPENDENCY**。本次原授权 `--no-daemon` 环境中，外部原生 `queue --resume` 只持久化消息，未观察到自动执行或自动重建可见 TUI；手动恢复原 thread 后才出现真实工具结果和模型 ACK。完整 S1 保持 **PARTIAL**，19 项历史受控 PASS 保留，`new_runtime_passes=[]`。证据断言 PASS 只表示正向及负向记录一致。

协作 session：`5ff16448-f0a6-44a5-a827-350891d0429c`；task：`s1-codex-no-tui-wakeup`；模式：`ralph-loop`。本轮新建独立证据目录，不覆盖前轮证据。

## 授权目标与基线

| 字段 | 实际值 |
|---|---|
| window | `2B9E10E2-0C0A-47DC-AED7-206B833A4462` |
| workspace | `28A52619-270D-4FC5-A692-C730028054E2` |
| surface | `4E483942-928F-455C-B03D-75DAEDE9F38F` |
| native thread A | `01a19ad4-2958-75f3-82b6-f488745caec4` |
| 原进程 / TTY | PID `11697` / wrapper `11696` / `ttys028` |
| 恢复后进程 / TTY | PID `19007` / wrapper `19006` / `ttys028` |
| cwd | `/Users/funlake/Projects/agentflow/docs/evidence/S1-manual-login-retry-a128f1ae/scratch/codex/cwd` |
| CODEX_HOME | 原手动授权的隔离环境；具体路径见 `resume-process.json` |
| nonce | `S1-no-tui-13bf49bb-1e33-4b8e-b57e-8237a9888a03` |
| queue ID | `01a19b33-c278-7161-ae58-a1b342e3568e` |

`initial-*`、`baseline-*` 和 `baseline-gate.json` 记录窗口 visible、surface selected、空闲且空输入栏、原生 `/status`、SQLite thread/rollout 绑定、PID/TTY/argv 与 lsof cwd。没有清除用户草稿。复用既有 argv 和最小环境，只使用原授权环境，不修改 provider/model/config，也没有读取或复制凭据。`ochist`、`jev-judge` 不在 PATH，未虚构其结果，依据指定前轮证据和用户明确授权执行可恢复操作。

## 实际时间边界（UTC）

| 时间 | 事件与证据 |
|---|---|
| `07:40:18.380646Z` | 原生 `/quit` 正常退出，原 wrapper 记录 PID `11697` exit=0，见 `original-wrapper-exit.json` |
| `07:40:22.941771Z`—`07:40:23.040497Z` | 项目根目录外部执行原生 `codex queue --thread <A> --message <唯一 nonce 请求> --resume`，退出 0；stderr 明确警告不能恢复，见 `queue-result.json` |
| queue 后三个有界观察点 | `no-tui-plus30-*`、`no-tui-plus60-*`、`no-tui-plus90-*`：同 surface 仍是 shell，目标 TTY 无 Codex，rollout 完全不变，未出现新 TUI |
| `07:42:23.003781Z` 左右 | 手动恢复前最后快照，queue 返回后 **119.963 秒**仍无原生进展，精确时间见 `immediate-pre-manual-resume-native.json` |
| `07:42:23.133548Z` | 明确手动恢复边界，见 `manual-resume-boundary.json`；恢复命令仅含原 thread，没有重发 queue 消息 |
| `07:42:23.212590Z` | 同 surface 启动原 argv 的 `resume <A>`，新 PID `19007`，见 `resume-process.json` |
| `07:42:24.409Z` | 原 rollout 首次新增 `task_started`，trigger=`queue` |
| `07:42:27.013Z` | 唯一原生 user message 出现 |
| `07:42:30.542Z` | 唯一 CommandExecution `/bin/pwd` 成功，exit=0，真实输出为原 cwd |
| `07:42:34.001Z` | 唯一原生 assistant 最终消息 `NO_TUI_ACK <nonce> <原 cwd>` |

原生 stderr：

```text
Warning: message was queued, but could not resume thread 01a19ad4-2958-75f3-82b6-f488745caec4: start a local daemon or specify a server with --remote
```

因此本轮证明了消息持久化及手动恢复后的接收，没有满足“无 TUI 时原生 ACK 或自动重建并完整交付”的验收条件。未启动 daemon 或选择 remote server；其他项目的 Codex/app-server 进程存在，不等于本隔离环境有可用的 thread 接收服务。结论仅覆盖此次原环境及有界观察，不断言所有 daemon/remote 部署均无法自动唤醒。

## 原始证据与恢复

- `queue-request.json`、`queue-result.json`：唯一 nonce、精确 argv、发送 cwd、退出码和两条原始输出流。
- `no-tui-before-queue-*`、`no-tui-plus{30,60,90}-*`、`immediate-pre-manual-resume-*`：原始 cmux 屏幕/base64 及解码、拓扑、目标 TTY 与系统 Codex 进程快照、native rollout。观察标签表示逐次等待 30 秒，实际间隔以记录时间为准。
- `codex-verification.json`、`native-transcript-delta.json`：queue、手动边界、同 thread 原生用户/工具/assistant 关联；用户消息和 ACK 各一条，CommandExecution 一次，均在手动恢复后。
- `final-*`：原 thread/surface/cwd/TTY、同屏 ACK、原生 `/status`、存活进程和空输入栏；目标布局/pane/surface 集合与初始一致。
- `native-history-preservation.json`：A 原始 417908 字节前缀 SHA-256 保留，B 全文 222975 字节 SHA-256 不变。
- `runtime-matrix.json`、`dependencies.json`：自动唤醒 UNVERIFIED / WAITING_DEPENDENCY，下一步需要单独授权 daemon/remote 路径并获得真实原生证据。
- `protected-files-verification.json`、`sha256-manifest.json`、`hash-verification.json`：Go/构建定义/原配置/已有非 scratch 证据对账，当前证据文件及两份总文档哈希清单。

无外部 OS signal；只正常 `/quit` 和显式手动 resume。前轮仍在运行的 wrapper 因正常退出自动创建此前不存在的 `S1-codex-interrupt-0cf09894/resume-exit.json`，不是覆盖已有证据。原生 rollout 的 system/developer 消息在证据投影中省略，原文件前缀另按原始字节核验。

本轮未改 Go、未运行 Go test/build/check 或 doctor，不进入 S2；OpenCode 模型调用 0，Claude 无操作。所有实验 cmux 变更均显式指定授权 UUID；当前协作 main/worker 仅通过规定脚本 READY/handoff 通信，不作为实验目标。未登录、购买、复制凭据、切换模型或改 provider 配置。`handoff.json` 的 DONE 仅表示调查交付，不构成模型 ACK 或自动唤醒 PASS。
