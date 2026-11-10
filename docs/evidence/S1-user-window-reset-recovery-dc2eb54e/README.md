# S1 用户窗口 reset、角色、权限与恢复验证

2026-11-02；协作 session `dc2eb54e-025e-40a1-8d42-76b7852761f7`，task `s1-user-window-reset-recovery`，模式 `ralph-loop`。用户已授权当前目标窗口原生 reset 和恢复。本轮完成调查交付，完整 S1 仍 **PARTIAL**。

**OpenCode/Codex 的原生 reset、nonce assistant READY、受控角色确认、真实 pwd、原会话上下文恢复均 PASS。Codex Full Access 的有效原生策略与只读执行证据 PASS；OpenCode 有效 full 权限 UNVERIFIED。** 角色结果仅证明本次角色确认与只读执行，不外推其他研发任务。恢复仅为同一存活进程内手动切回原会话，没有验证退出、崩溃或自动恢复。

## 绑定与 native ID

共同 window `73FEB4F4-BE2B-4F4D-9B48-E872BBC51EA3`，workspace `3AB7D16C-B09D-44AE-8CD5-5B6225388D45`，默认 cwd `/Users/funlake/Projects/okra-wiki`。全部终端输入显式携带这些目标及对应 surface，没有依赖当前焦点路由。

| 工具 | surface / PID / TTY | reset 前 → 后 → 恢复 |
|---|---|---|
| OpenCode 1.19.5 | `5265DA42-9A45-4C71-BABE-155E458FCF33` / `23097` / `ttys003` | `ses_e64c2fa39ffeI5sYf6iCWDKYpv` → `ses_e64aa6b77ffeP7BLeSFxdbziwS` → 原 ID |
| Codex 0.166.0 | `94EBBAAE-5EA1-4352-B183-580E24F0F23C` / `23477` / `ttys004` | `01a19b3b-9fc2-7622-a43f-af895cfac312` → `01a19b55-5f41-76c0-b91c-a09595425d28` → 原 ID |

基线见 [before-tree.json](before-tree.json)、[before-processes.json](before-processes.json)、两份 `before-*-screen.json` 与 `before-*-cwd.json`。原屏幕均空闲、无草稿。OpenCode 旧消息的独立 user/assistant 记录与屏幕 ACK 对应：[original-opencode-native.json](original-opencode-native.json)。Codex 原 ID 由进程打开的 rollout、[原生记录](before-codex-native.json) 与 [/status](codex-before-status-screen.json) 共同确认。协作 session 不充当 native ID。

## 实际交互和验收

| 项目 | OpenCode | Codex |
|---|---|---|
| 原生 reset | `/new` 原生补全明确为 New session；提交后首页，再发新请求建立新 ID | `/new` 后选择 **Use current Git worktree**，保持原目录；未创建 worktree |
| 唯一 reset nonce | `S1-OC-reset-dc2eb54e-7f02c8a1` | `S1-CX-reset-dc2eb54e-a93b50d2` |
| 真实模型响应 | 独立 assistant `READY nonce=… role=资深研发`；工具完成后独立 assistant `ROLE_ACK` | 独立 native assistant READY、ROLE_ACK；与可见屏幕一致 |
| 实际只读工具 | native bash `command=pwd`，exit=0，返回共同 cwd | native CommandExecution `exec-520129e1-6dfd-4c0c-9b7e-90eb02e39bb4`，exit=0，返回共同 cwd |
| 角色重注入 | **PASS，受控角色 ACK 与 pwd** | **PASS，受控角色 ACK 与 pwd** |
| 有效权限 | `pwd` 执行 **PASS**；session permission=null、Build auto 均不足以证明 full，**full UNVERIFIED** | **PASS，原生有效策略与只读调用**：新 thread turn_context 为 never / danger-full-access / permission_profile disabled，/status 对应新 ID 与 Full Access，真实 pwd 成功 |
| 恢复方式 | 原生 `/sessions`，从当日列表选择原 `07:50:30.086Z` 会话 | 原生 `/resume 01a19b3b-9fc2-7622-a43f-af895cfac312`；/status 再确认原 ID |
| 恢复请求 | `S1-OC-recover-dc2eb54e-b9d405f3`，未包含旧 ACK 或其 UUID | `S1-CX-recover-dc2eb54e-2a7801c6`，未包含旧 ACK 或其 UUID |
| 恢复上下文 | **PASS**，模型从原会话历史回忆完整旧 `OPENCODE_ACK_7ba139c0-0133-47da-b99d-e7549a22f735`，再实际 pwd 和 RECOVERY_ACK | **PASS**，模型从原 thread 历史回忆完整旧 `CODEX_ACK_9196eeee-17ab-40ac-91ee-8f558336b201`，再实际 pwd 和 RECOVERY_ACK |

Full Access 结论限于原生有效策略与本次只读执行；没有写入或网络权限探针，不宣称全部 OS 权限已穷举验证。屏幕提示或 `pwd` 单独成功均不足以证明 full。本轮不更改任何权限或 provider 设置。

reset 原始交互：`*-new-send.json`、`*-new-enter.json`、[OpenCode 命令补全](opencode-new-command-screen.json)、[Codex worktree 选择](codex-after-new-screen.json)、`*-ready-send.json` / `*-ready-enter.json`。每次正文均单独发送 Enter。

reset 结果：[OpenCode native](reset-ready-opencode-native.json)、[OpenCode screen](opencode-ready-result-screen.json)、[Codex native](reset-ready-explicit-codex-native.json)、[Codex screen](codex-ready-result-screen.json)、[新 thread /status](codex-reset-status-screen.json)。OpenCode 工具 part `prt_19b55a52b001WXkxfxcspb0ZE5` 的调用、状态和输出与可见 pwd 一致。

恢复原始交互：[OpenCode Sessions 菜单](opencode-sessions-menu.json)、`opencode-original-select-*.json`、[恢复后旧屏幕](opencode-restored-before-prompt.json)、`codex-resume-*.json`、[Codex 恢复 /status](codex-restored-status-screen.json)，以及两份 `*-recovery-send.json` 和 `*-recovery-enter.json`。恢复结果：[OpenCode native](restored-opencode-native.json)、[Codex native](restored-codex-native.json)、[OpenCode 最终 screen](final-opencode-screen.json)、[Codex 最终 screen](final-codex-screen.json)。

## 证据关联中的边界

Codex `/new` 首页出现后，进程仍短暂保留旧 rollout 的打开描述符；新请求完成时，lsof 同时列出旧、新两个 rollout。因此记录器停止了“一个文件即活跃 thread”的断言，保留 [原始 open-files](reset-ready-codex-open-files.json)，再用新 ID 的原生记录、唯一 nonce 和 /status 交叉确认。该采集断言失败不是 reset 失败，也未触发额外模型请求。

最终证据断言见 [evidence-validation.json](evidence-validation.json) 和 [机器矩阵](runtime-matrix.json)。验证读取实际 native user/assistant 身份、独立 nonce 响应、工具调用/结果、native ID 变化、原历史保留以及恢复请求未重发旧 nonce；未将 cmux 返回、工具包装器或 handoff 当作模型 ACK。OpenCode 原历史非注入 parts 内容保留；编辑器注入上下文在证据中脱敏。Codex 原 rollout 字节前缀 SHA-256 保持一致。证据是相关 native 记录的投影，不复制系统提示、auth/key 文件或编辑器选中内容。

`jev-judge` 不在 PATH，未取得 Jev 判断；没有伪造 risk 输出。用户明确授权的本地会话操作照常执行。

## 最终状态与文件保护

[final-tree.json](final-tree.json) 确认原 window 仍 visible，两个原 surface 仍 selected，布局一致；[final-processes.json](final-processes.json) 确认原 PID/TTY 存活，两份 `final-*-cwd.json` 与共同 cwd 一致。最终两个输入栏为空，native 请求已完成，没有确认框或待处理草稿。用户可继续使用原会话。

本轮新增 user 模型请求 4 条（每工具 reset 与恢复各 1 条），新增终端工具调用 4 次，均为 `pwd`。本轮记录器只向此新证据目录写入，授权文档通过补丁更新。未操作 Claude、agentflow 协作会话、其他窗口、登录、购买、provider 变更、git 修改命令、OS kill 或故障注入；未进入 S2，未运行 Go test/build/check 或 doctor。

[before-inventory.json](before-inventory.json) / [after-inventory.json](after-inventory.json) 与 [file-protection-verification.json](file-protection-verification.json) 核对 wiki 文件内容、符号链接、敏感文件元数据和 Git status；凭据文件只核对元数据。`.git`、node_modules 未哈希，既有用户改动以 before 为基线保留。agentflow 核对既有源码、构建定义及历史非 scratch 证据，仅允许两份 S1 总文档变化，新证据目录自身排除。清单见 [sha256-manifest.json](sha256-manifest.json)，handoff 回执生成在其后。

OpenCode full、当前窗口 busy/草稿保护、hook/queue 产品适配、自动/崩溃/主机恢复仍 **UNVERIFIED**；这些均不妨碍本轮已完成的 reset 和手动恢复验收，但完整 S1 继续 **PARTIAL**。正式协作回传保存在 `handoff.json`，其 DONE 仅表示本轮调查交付完成。
