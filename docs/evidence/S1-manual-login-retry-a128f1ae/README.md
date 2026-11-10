# S1 OpenCode 无登录 / Codex 手动登录重试

协作 session：`a128f1ae-db2d-4cf5-98c1-ff3a995fdb51`；task：`s1-manual-login-retry`。

状态：**本轮受控验证完成，完整 S1 PARTIAL**。Codex 在用户手动登录后，原生可见绑定、唯一 nonce ACK、reset READY、角色重注入、调用身份、受控 full 权限及原会话恢复得到真实证据。OpenCode 仅为 **VERIFIED_TRANSPORT_ONLY**，真实模型 ACK 受免费额度限制，保持 **WAITING_DEPENDENCY**。Claude 排除。

本轮运行时间约 `2026-11-02T05:55Z–06:04Z`。机器结论见 [runtime-matrix.json](runtime-matrix.json)，逐项断言见 [evidence-validation.json](evidence-validation.json)。协作 session / READY / handoff 均不是工具 native ID 或模型 ACK。

## Codex：同一隔离可见目标

用户明确确认本轮窗口登录完成后，立即停止登录界面操作；只在这个目标执行后续检查：

| 身份 | 实际值 |
|---|---|
| window | `2B9E10E2-0C0A-47DC-AED7-206B833A4462` |
| workspace | `28A52619-270D-4FC5-A692-C730028054E2` |
| native surface | `4E483942-928F-455C-B03D-75DAEDE9F38F` |
| TTY | `ttys028` |
| 原 thread / 当前恢复 thread | `01a19ad4-2958-75f3-82b6-f488745caec4` |
| `/new` 后 thread | `01a19ad6-edd3-7e10-bb5e-0a04a699da32` |
| 独立 CODEX_HOME | 本目录 `scratch/codex/codex`，新建且未复制旧凭据 |
| 当前存活进程 | PID `59794`，显式 `resume` 原 thread；原进程 PID `56438` 主动退出 0 |

启动参数为 `--no-daemon --no-alt-screen --dangerously-bypass-approvals-and-sandbox -c 'cli_auth_credentials_store="file"' -c 'check_for_update_on_startup=false'`。子进程只继承最小终端环境，覆盖独立 XDG、CODEX_HOME 和 TMPDIR；保留 HOME，未复制或读取已有 auth.json/keychain/cookies，未输入凭据。用户自行完成登录，Codex 原生运行时正常使用这次新授权。

登录后的首个只读线程表仍空；原生 `/status` 已显示分配的 Session ID，输入栏为 `Ask Codex to do anything`，没有忙碌任务或草稿。先保存 [发送门禁](codex-pre-nonce-gate.json)，再粘贴唯一 nonce，并单独发送 Enter。首轮消息完成后，SQLite 和原生 rollout 的 thread ID、cwd、provider 与 `/status` 一致。

| 能力 | 真实结果 | 原始证据 |
|---|---|---|
| 同会话真实 ACK | `ACK S1-codex-a128-5633e5b0-be0f-4c85-ab21-cc361ec03a35`；native assistant message `msg_024e894cfd265b59016ae8265c34b8819a9c7fb2cb6825d9f0`，output tokens=35 | [native rollout 快照](codex-original-native-transcript.json)、[可见屏幕](codex-after-nonce.json) |
| reset READY / 角色注入 | `/new` 改变 native ID，新的模型返回 `READY role=资深研发 nonce=S1-reset-role-a128-83c47a29-bf1e-493d-a2e7-23da9fdc691d` | [新 ID](codex-reset-status.json)、[READY rollout](codex-reset-ready-transcript.json)、[屏幕](codex-reset-ready-screen.json) |
| 实际调用身份 | 新 thread 的 `CommandExecution` 在独立 cwd 执行，输出正确 workspace/surface UUID，exit_code=0；后续模型继续报告角色为资深研发 | [调用原始记录](codex-identity-transcript.json)、[屏幕](codex-identity-screen.json) |
| full 权限实际生效 | 原生 `turn_context` 为 `approval_policy=never`、`sandbox_policy=danger-full-access`、`permission_profile=disabled`；模型实际在 cwd 外的自有 scratch 写入并回读唯一标记，无审批交互 | [调用及权限记录](codex-identity-transcript.json)、[断言与标记副本](codex-verification.json) |
| 主动退出 / 原会话恢复 | `/quit` 退出 0，shell/surface/window 保留；同 surface 使用相同独立 CODEX_HOME 恢复原 ID。恢复提示未包含原 nonce，模型正确返回 `RESTORED S1-codex-a128-5633e5b0-be0f-4c85-ab21-cc361ec03a35` | [退出](codex-exit.json)、[恢复启动](codex-resume-launch.json)、[身份核对](codex-resume-status.json)、[恢复 rollout](codex-restored-native-transcript.json)、[最终屏幕](codex-restored-final-screen.json) |

实际调用的 native thread 从 `/status`、SQLite 和 rollout 绑定，未依赖模型自述。身份探针的模型明确报告自己无法读取 native session id，这不影响外部证据绑定，也不把其自述当作 ID。

完整断言见 [codex-verification.json](codex-verification.json)。full 的 PASS 仅限原生生效配置与这次受控实际调用，不代表穷尽所有 OS/网络操作；角色验证仅证明该 native 会话接受角色并在下一次真实调用保持角色。未验证 busy 投递、非空草稿保护、不同 cwd 对照、崩溃恢复或 hook/queue 后台投递。这些缺项不能因 nonce ACK 通过而升级。

## OpenCode：无登录传输与模型 ACK 分层

复用原独立 loopback serve PID `26680` 和 attach PID `44608`，没有进行登录。native session `ses_e65a4cf24ffe3wZAiM2U7og4Px`，window `CAE5B3FE-3E61-4E92-9896-775B9343687F` / workspace `158ADE67-F09E-47D5-9EB9-7B6718BE682E` / surface `FDDB002D-F985-4CB1-AE3C-FB8918AADC5D`。本轮没有操作该旧实验窗口中的其他工具。

`noReply=true` POST 返回 HTTP 200，native message `msg_19ad38460001PTDmLUPhEieguj` 包含唯一 nonce `S1-local-a128-4c90b80d-2a81-44d1-9636-29410b6ca881`；原生 GET 回读及原 surface 屏幕显示匹配。见 [提交](opencode-transport-submit.json)、[原生回读](opencode-transport-messages.json)、[可见屏幕](opencode-transport-visible.json)。此项只标记 **VERIFIED_TRANSPORT_ONLY**。

另发模型请求 nonce `S1-model-a128-b142e4bd-2c7c-47cb-a7d8-3f862fde7808`，`prompt_async` HTTP 204 只代表接受请求。[原生状态](opencode-model-status.json) 和 [屏幕](opencode-model-screen.json) 实际返回 `Free usage exceeded, subscribe to Go` / `free_tier_limit`。对应 assistant parts 空、output tokens=0，没有 ACK。保存证据后仅 abort 此实验模型请求，HTTP 200；最终 session status `{}`。见 [原生最终消息](opencode-messages-final.json)、[分层断言](opencode-verification.json)。

OpenCode reset READY、角色执行、实际身份、有效 full、模型上下文恢复继续 UNVERIFIED；不切换付费提供商、不以用户消息或 HTTP 成功冒充模型 ACK。

## 工程检查与最终保留状态

`go test ./...`、`go build ./...`、`make check` 实际退出码均为 0，完整输出和环境保存在本目录的同名 JSON。项目 Go 源码未改；临时实验记录器快照见 [harness-source.txt](harness-source.txt)。只追加本目录证据并更新 S1 报告和矩阵，未进入 S2、创建 worktree 或提交。

[协作拓扑对账](protected-topology-comparison.json) 确认 main/worker workspace 的布局、pane 和 surface 集合保持一致；没有向它们或其他项目 Codex 发送实验输入。Codex 新实验窗口、控制 shell 和恢复后的 native 客户端继续存活，见 [最终树](codex-tree-final.json)、[进程](codex-process-final.json)。用户已完成登录，因此没有待确认的登录步骤。已正式回传 DONE，含完整 S1 PARTIAL / OpenCode WAITING_DEPENDENCY；handoff ID `7e76abb6-a22a-4582-8ba0-dbc251b22c68`，退出 0，确认 `[DONE] handoff sent`。

继续 S1 的缺项见 [dependencies.json](dependencies.json)。正式回传的 argv、退出码和 `[DONE] handoff sent` 保存在 [handoff.json](handoff.json)。
