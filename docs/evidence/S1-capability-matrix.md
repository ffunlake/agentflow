# S1 三工具能力矩阵

交付同步（2026-11-03，S7.3 / `6858c2f1-7800-4ab2-a323-d0ed6e06ae01`）：**代码侧本地故障矩阵 PASS；完整 S1 与产品整体仍 PARTIAL**。本轮仅执行 fake/临时 DB/context/测试 socket 与 AgentFlow CLI 回归，新增 30 个表驱动场景；test/build/check/tidy 与核心 race 均通过，见 [最终报告](S7.3-result.md)。`new_runtime_passes=[]`，未启动或测试真实 OpenCode/Codex/Claude/cmux 会话；完成 handoff 仅为协作回传，不是产品原生通信验收。

| 最终交付边界 | 本轮结果 | 原生验收 |
|---|---|---|
| 配置/布局、快照权限、SQLite/IPC/锁、重复 request 与代际保护 | 本地 PASS | 不证明工具接收或有效 full |
| busy/草稿、未知 reset/submit、迟到 READY/ACK/结果、stop、有限预算 | fake/store 本地 PASS | 三工具真实通道与门禁 PARTIAL/UNVERIFIED |
| context/report/bootstrap 完整性与 run/agent 隔离 | 文件与 DB 本地 PASS | native 注入/恢复、跨用户安全隔离 UNVERIFIED |
| 三工具 e2e、现场 attach/stop、真实故障恢复 | 未执行 | PARTIAL/UNVERIFIED；保留以下历史受控证据的原边界 |

最近真实调查（2026-11-02，3266749c）：完整 S1 **PARTIAL**；OpenCode 当前用户 TUI 外部 queue 入口 **UNVERIFIED / QUEUE_ENTRY_UNAVAILABLE**。仅完成只读发现，没有新 nonce、消息或模型请求，功能 PASS 为空；没有重新验收 Codex / Claude。历史 PASS 保留原边界，最新真实调查节优先于下方旧轮次摘要。

## 最新有效范围与结果（3266749c，OpenCode queue 入口发现）

协作 session `3266749c-1804-4665-9e94-71b766be4095` / task `s1-opencode-queue-discovery`。新增 [报告](S1-opencode-queue-discovery-3266749c/README.md)、[机器矩阵](S1-opencode-queue-discovery-3266749c/runtime-matrix.json)、[缺口](S1-opencode-queue-discovery-3266749c/dependencies.json)。目标 window `73FEB4F4-BE2B-4F4D-9B48-E872BBC51EA3` / workspace `3AB7D16C-B09D-44AE-8CD5-5B6225388D45` / OpenCode surface `5265DA42-9A45-4C71-BABE-155E458FCF33`。

| 能力 / 发现事实 | OpenCode 当前观察 | 验收状态 |
|---|---|---|
| 原目标与 native 关联 | PID `23097` / PPID `21983` / `ttys003` / `opencode --auto` / cwd `/Users/funlake/Projects/okra-wiki`；旧 assistant ACK/恢复文本在同屏与只读 SQLite 的 `ses_e64c2fa39ffeI5sYf6iCWDKYpv` 匹配 | 身份只读关联；不算本轮外部消息 PASS |
| 当前 runtime 监听 | 无 TCP LISTEN；一个无 pathname 的 Unix 连接；无目标子进程；loopback 55367 属于 `Code H` | 安全入站地址 **UNVERIFIED** |
| CLI / SDK server 消息 | help 声明 `run --attach --session --dir`，SDK 有 message/prompt_async/TUI control queue；当前无可绑定 server URL | **ADVERTISED_ONLY / UNVERIFIED** |
| cmux agent message | `receiving=true`，但 native resume binding/restore record=null，未证明当前加载接收 hook；标准 cmux 插件目录项不存在，配置正文未查 | **UNVERIFIED**；未发送测试消息 |
| 同 native / surface / cwd 的真实外部 assistant ACK | 本轮 nonce=null，消息=0，模型请求=0，`new_runtime_passes=[]` | **UNVERIFIED / QUEUE_ENTRY_UNAVAILABLE** |
| busy / 非空草稿 / AgentFlow 适配器 | 未执行，没有安全外部入口前不开展 | **UNVERIFIED** |
| 最终保护观察 | 原 PID/TTY/cwd/可见布局；screen 相同、空输入栏；native 元数据和选定旧文本相同；17 个既有项目文件哈希无变化 | 仅证据一致性，不是 queue 功能 PASS |

原始前后命令及 stdout/stderr 在新目录。`MODEL/QUEUE_ENTRY_UNAVAILABLE` 分类具体为 queue 入口缺失，没有证明模型不可用。未 reset/发普通 prompt/改配置/读凭据内容/写 wiki/操作 Codex 或 Claude/改 Go/进入 S2；未运行 Go test/build/check 或 doctor。`jev-judge` 当前 PATH 缺失，无判断结果；只读调查完成。正式 handoff 不算原生 ACK。

## 前轮状态摘要（468c9508）

日期：2026-11-02。安全基础检查开始：`2026-11-02T03:10:22.21866Z`；独立原生实验启动：`2026-11-02T03:21:14Z`。平台：Go 1.25.5 / darwin arm64。整个 S1：**PARTIAL**；安全基础检查：历史 **PASS**。最新 468c9508 完成授权用户窗口 Codex **busy 原生排队、非空草稿保护 PASS，仅本次 native queue**；原 turn 完成后唯一 assistant ACK、自有草稿逐字保留且未提交，最终清除自有草稿。OpenCode 缺少已验证安全外部 queue，两项 **UNVERIFIED**，新增模型请求 0。上一轮两工具 reset/READY、角色、真实 pwd、同进程手动恢复 PASS 与 Codex Full Access 原生有效策略/只读调用 PASS 保留，OpenCode full UNVERIFIED。两个原 surface/PID/TTY/cwd 和布局保留，输入栏为空。19 项历史受控 PASS 保留原环境边界，不继承或重复计数。Codex 无 TUI 自动唤醒仍 **UNVERIFIED / WAITING_DEPENDENCY**；**Ctrl-C 单独终止工具 FAIL_OBSERVED** 保留；Claude 排除。历史记录保留，以最新节为准。

基础机器矩阵：[S1-verified/local-probe/matrix.json](S1-verified/local-probe/matrix.json)。前轮主命令的实际退出码和输出：[local-probe.json](S1-verified/local-probe.json)。逐命令证据位于同一 `local-probe/` 目录；矩阵中的 evidence ID 对应 `<id>.json`。这些版本、帮助和 cmux 只读命令退出 0，**仅证明这些命令执行成功**。最初独立实验另存于 [S1-runtime-b7bf333c](S1-runtime-b7bf333c/README.md) 和 [runtime-matrix.json](S1-runtime-b7bf333c/runtime-matrix.json)，该历史轮次无运行时 PASS。

## 前轮有效范围与结果（468c9508，用户窗口 busy 与草稿）

协作 session `468c9508-c700-42a7-9d91-81ffdeb46ff2` / task `s1-user-window-busy-draft`。新增 [报告](S1-user-window-busy-draft-468c9508/README.md)、[机器矩阵](S1-user-window-busy-draft-468c9508/runtime-matrix.json)、[原生断言](S1-user-window-busy-draft-468c9508/evidence-validation.json)。目标仍是 window `73FEB4F4-BE2B-4F4D-9B48-E872BBC51EA3` / workspace `3AB7D16C-B09D-44AE-8CD5-5B6225388D45` / cwd `/Users/funlake/Projects/okra-wiki`。

| 能力 / 当前事实 | OpenCode | Codex |
|---|---|---|
| 原 native / PID / TTY | `ses_e64c2fa39ffeI5sYf6iCWDKYpv` / `23097` / `ttys003` | `01a19b3b-9fc2-7622-a43f-af895cfac312` / `23477` / `ttys004` |
| 可见绑定与输入门禁 | 原 surface `5265DA42-9A45-4C71-BABE-155E458FCF33`，原 cwd，空闲无草稿 | 原 surface `94EBBAAE-5EA1-4352-B183-580E24F0F23C`，原 cwd，空闲无草稿 |
| 安全外部原生 queue | **UNVERIFIED**；当前原进程无 TCP listener / 可绑定 Unix 地址，CLI 无已验证 queue；不另启 runtime 或直接插库 | **PASS，仅存活原 TUI**；显式 `queue --thread <原nativeID>`，两条均有真实 assistant nonce ACK |
| busy 排队与顺序交付 | **UNVERIFIED**，未强行实验 | **PASS，受控场景**；45 秒真实 CommandExecution 内入队，原 turn `task_complete` 后才启动 queue turn，再唯一 ACK；[native](S1-user-window-busy-draft-468c9508/final-codex-native.json) |
| 非空草稿保护 | **UNVERIFIED**，未强行放草稿 | **PASS，仅 native queue**；[ACK 前草稿](S1-user-window-busy-draft-468c9508/draft-before-queue.json) 与 [ACK 后草稿](S1-user-window-busy-draft-468c9508/draft-after-queue.json) 逐字相同、native user 无 marker；只清除自有草稿 |
| 最终可用性与历史 | native 全部内容和屏幕不变，原 PID/cwd/TTY、空输入栏 | 原 rollout 前缀哈希保留，两条 queue 各唯一 canonical user/assistant；原 PID/cwd/TTY、空输入栏 |
| 通用 exactly-once、草稿门禁、AgentFlow hook/queue 适配器、自动恢复 | **UNVERIFIED** | **UNVERIFIED**；本轮仅原生 TUI 有界观察 |

两项 Codex 新受控 PASS 单列，不重复累计旧 19 项；上一轮 reset/角色/同进程手动恢复仍保留原边界。OpenCode 本轮模型请求 0。Codex 3 条新增 canonical user、3 条最终 assistant、1 次只读命令执行。文件保护见 [核验](S1-user-window-busy-draft-468c9508/file-protection-verification.json) 和 [清单](S1-user-window-busy-draft-468c9508/sha256-manifest.json)；一次进程参数采样已脱敏，不用于 busy 结论。无 reset/退出/kill/provider 修改/wiki 写入/Go 修改/Claude 操作/协作窗口实验消息；未运行 Go test/build/check 或 doctor。完整 S1 **PARTIAL**，handoff 不作为 native ACK。

## 前轮有效范围与结果（dc2eb54e，用户窗口 reset 与手动恢复）

协作 session `dc2eb54e-025e-40a1-8d42-76b7852761f7` / task `s1-user-window-reset-recovery`。新增 [报告](S1-user-window-reset-recovery-dc2eb54e/README.md)、[机器矩阵](S1-user-window-reset-recovery-dc2eb54e/runtime-matrix.json)、[证据断言](S1-user-window-reset-recovery-dc2eb54e/evidence-validation.json)。共同 window `73FEB4F4-BE2B-4F4D-9B48-E872BBC51EA3` / workspace `3AB7D16C-B09D-44AE-8CD5-5B6225388D45` / cwd `/Users/funlake/Projects/okra-wiki`；下列 PASS 均绑定此目标和相应原 surface。

| 能力 / 当前事实 | OpenCode | Codex |
|---|---|---|
| 原 surface / PID / TTY | `5265DA42-9A45-4C71-BABE-155E458FCF33` / `23097` / `ttys003` | `94EBBAAE-5EA1-4352-B183-580E24F0F23C` / `23477` / `ttys004` |
| native reset 前 → 后 → 恢复 | `ses_e64c2fa39ffeI5sYf6iCWDKYpv` → `ses_e64aa6b77ffeP7BLeSFxdbziwS` → 原 ID | `01a19b3b-9fc2-7622-a43f-af895cfac312` → `01a19b55-5f41-76c0-b91c-a09595425d28` → 原 ID |
| 原生 reset / nonce READY | **PASS**：`/new`，新 ID 下独立模型 READY；[native](S1-user-window-reset-recovery-dc2eb54e/reset-ready-opencode-native.json)、[screen](S1-user-window-reset-recovery-dc2eb54e/opencode-ready-result-screen.json) | **PASS**：`/new` + 当前 worktree，新 ID /status 与独立模型 READY；[native](S1-user-window-reset-recovery-dc2eb54e/reset-ready-explicit-codex-native.json)、[screen](S1-user-window-reset-recovery-dc2eb54e/codex-ready-result-screen.json) |
| 角色 / 实际工具 cwd | **PASS，受控角色 ACK 与 pwd**：READY、ROLE_ACK role=资深研发；真实 bash pwd exit=0 返回共同 cwd | **PASS，受控角色 ACK 与 pwd**：READY、ROLE_ACK 与真实 CommandExecution pwd 返回共同 cwd |
| 有效 full | **UNVERIFIED**：pwd 可执行，但 auto/null 权限不是 full 证明 | **PASS，仅原生有效策略与只读调用**：新 thread never / danger-full-access / permission_profile disabled，实际 pwd；不外推写入或网络权限 |
| 原会话上下文恢复 | **PASS，同进程手动**：`/sessions` 选择原会话，原 ID 的真实模型回忆未重发的旧完整 ACK；[native](S1-user-window-reset-recovery-dc2eb54e/restored-opencode-native.json)、[screen](S1-user-window-reset-recovery-dc2eb54e/final-opencode-screen.json) | **PASS，同进程手动**：`/resume <原ID>`，/status、原 ID 真实模型回忆未重发旧 ACK；[native](S1-user-window-reset-recovery-dc2eb54e/restored-codex-native.json)、[screen](S1-user-window-reset-recovery-dc2eb54e/final-codex-screen.json) |
| 原历史 / 最终可用性 | 原非注入 parts 内容保留，原 PID/cwd/TTY、布局和空输入栏 | 原 rollout 字节前缀哈希保留，原 PID/cwd/TTY、布局和空输入栏 |
| 当前窗口 busy、草稿保护、hook/queue、自动或故障恢复 | **UNVERIFIED** | **UNVERIFIED** |

本轮恢复通过仅为原存活 native 进程内切回，不证明退出/重启或崩溃恢复。Codex 进程 reset 后一度仍同时打开旧、新 rollout，未用 lsof 单独判定活跃 ID；/status、native nonce ACK、同 surface 屏幕一起构成证据。每工具新增 reset/recovery 各 1 条 user 请求和各 1 次 pwd，共 4 请求/4 次只读工具调用。

[文件保护核验](S1-user-window-reset-recovery-dc2eb54e/file-protection-verification.json) 比较 wiki 原文件与 Git status 基线、agentflow 既有源码和非 scratch 历史证据，仅两份授权总文档允许变化；新证据独立保存。[哈希清单](S1-user-window-reset-recovery-dc2eb54e/sha256-manifest.json)。未操作 Claude/协作会话、写 wiki、执行 git 修改/kill/登录/provider 变更；未运行 Go test/build/check 或 doctor，不进入 S2。完整 S1 仍 PARTIAL，旧 19 项受控通过保留边界；handoff 不作为 ACK。

## 前轮有效范围与结果（a541862c，用户新窗口已有两次模型验证）

协作 session `a541862c-c620-47e8-95c9-e2186e359c9c` / task `s1-user-window-evidence`。新证据：[报告](S1-user-window-evidence-a541862c/README.md)、[绑定](S1-user-window-evidence-a541862c/bindings.json)、[运行矩阵](S1-user-window-evidence-a541862c/runtime-matrix.json)、[证据断言](S1-user-window-evidence-a541862c/evidence-validation.json)。共同 window `73FEB4F4-BE2B-4F4D-9B48-E872BBC51EA3` / workspace `3AB7D16C-B09D-44AE-8CD5-5B6225388D45` / cwd `/Users/funlake/Projects/okra-wiki`；每个 PASS 都同时绑定这些值。

| 能力 / 当前事实 | OpenCode 用户会话 | Codex 用户会话 |
|---|---|---|
| surface / TTY / native 进程 | `5265DA42-9A45-4C71-BABE-155E458FCF33` / `ttys003` / PID `23097`，PPID `21983` | `94EBBAAE-5EA1-4352-B183-580E24F0F23C` / `ttys004` / PID `23477`，PPID `22535` |
| 拓扑及 OS cwd 绑定 | tree 确认可见窗口的左侧所选 surface；ps 与 lsof cwd 匹配共同路径 | tree 确认可见窗口的右侧所选 surface；ps 与 lsof cwd 匹配共同路径 |
| 已有真实模型 ACK | **PASS**：[原始 scrollback](S1-user-window-evidence-a541862c/opencode-scrollback.json) 第 6 行独立 assistant 返回 `OPENCODE_ACK_7ba139c0-0133-47da-b99d-e7549a22f735`，用户请求在第 3 行 | **PASS**：[原始 scrollback](S1-user-window-evidence-a541862c/codex-scrollback.json) 第 12 行独立 assistant 返回 `CODEX_ACK_9196eeee-17ab-40ac-91ee-8f558336b201`，用户请求在第 8–9 行 |
| 已有只读工具结果 | 未要求 OpenCode 运行工具；屏幕 cwd 与 OS cwd 绑定 | `Ran pwd` → `/Users/funlake/Projects/okra-wiki`，随后 assistant 回显；并入上方同一次观察 PASS |
| native session/thread ID | **UNVERIFIED**，现有屏幕未证明 | **UNVERIFIED**，现有屏幕未证明 |
| reset/READY、角色重注入、身份刷新 | **UNVERIFIED** | **UNVERIFIED** |
| 实际有效 full | **UNVERIFIED**；`Build auto` 不足以证明 | **UNVERIFIED**；`Permission selection requested: Full Access` 仅为请求，pwd 不能证明 full |
| recovery、busy、草稿保护、不同 cwd 隔离 | **UNVERIFIED** | **UNVERIFIED** |
| hook/queue、自动唤醒、AgentFlow 适配器 | **UNVERIFIED** | **UNVERIFIED** |
| 屏幕模型标签 | `GPT-6.1 Sol / OpenAI`；实际提供商模型身份未独立核验 | `GPT-5.6-Luna`；实际提供商模型身份未独立核验 |
| 历史证据边界 | 旧隔离环境 `MODEL_PROVIDER_UNAVAILABLE` 不概括本窗口已有 ACK；旧文件保留 | 19 项历史受控 PASS 不继承到此窗口；保留旧环境中的原结论 |

只整理已有结果，没有追加用户输入或模型请求、reset/退出/kill，没有修改 okra-wiki/Go、操作 Claude 或进入 S2。新增两项观察单列，完整 S1 仍 PARTIAL。仅做 [文档保护哈希对账](S1-user-window-evidence-a541862c/protected-documents-verification.json) 与 [证据/文档 SHA-256 核验](S1-user-window-evidence-a541862c/hash-verification.json)，未运行 Go test/build/check 或 doctor。历史证据未覆盖；正式 handoff 与 cmux 命令退出码均不充当模型 ACK。

## 前轮有效范围与结果（5ff16448，仅 Codex 无 TUI 自动唤醒）

新证据：[报告](S1-codex-no-tui-5ff16448/README.md)、[机器矩阵](S1-codex-no-tui-5ff16448/runtime-matrix.json)、[原生关联](S1-codex-no-tui-5ff16448/codex-verification.json)、[断言](S1-codex-no-tui-5ff16448/evidence-validation.json)、[依赖](S1-codex-no-tui-5ff16448/dependencies.json)。仅复用原授权 window/workspace/surface 和 thread A `01a19ad4-2958-75f3-82b6-f488745caec4`。

| 能力 / 当前事实 | 实际观察 | 验收状态与边界 |
|---|---|---|
| 可见、空闲、无草稿基线 | 原 PID `11697` / `ttys028`，`/status`、cwd、native ID 和可见 surface 一致，空输入栏 | 基线已确认；正常 `/quit` exit=0，不终止其他 PID |
| 外部原生 queue | 唯一 nonce `S1-no-tui-13bf49bb-1e33-4b8e-b57e-8237a9888a03`，queue `01a19b33-c278-7161-ae58-a1b342e3568e`，`--resume` exit=0，同时明确警告缺少可选 local daemon/remote | **只证明入队**，退出码不作为原生 ACK |
| 无 TUI 自动执行 | queue 返回至手动恢复前最终快照 119.963 秒；rollout 字节数/哈希完全不变，目标 TTY 无 Codex | **UNVERIFIED / WAITING_DEPENDENCY**；本次无自动原生 ACK |
| 自动重建可见 TUI | 三个观察点和恢复前快照中原 surface 均为 shell，布局与 surface 集合未变 | **UNVERIFIED**；本次未观察到重建 |
| 消息持久化后手动接收 | 手动恢复边界 `07:42:23.133548Z`；其后才出现 queue turn、唯一 user message、一次 `/bin/pwd` 成功和 `07:42:34.001Z` native `NO_TUI_ACK`，同屏可见 | **PERSISTED_UNTIL_MANUAL_RESUME**；沿用既有手动恢复能力，不计自动唤醒 PASS |
| 原会话恢复 | 原 native/surface/cwd/`ttys028`，新 PID `19007` / wrapper `19006` 存活、空输入栏；A 历史前缀保留、B 全文不变 | 恢复完成，原布局保留 |
| 整体与后续依赖 | 原环境为 `--no-daemon`，queue 未选到可用 daemon/remote；本轮没有更改该环境 | S1 **PARTIAL**，`new_runtime_passes=[]`；其他 daemon/remote 路径需另行授权并取得原生证据 |
| OpenCode / Claude | OpenCode 模型调用 0，Claude 无操作 | 无新增验收 |

Go 未改，未运行 Go test/build/check 或 doctor，不进入 S2。源码/构建定义/原配置/原有非 scratch 证据核验见 [文件保护](S1-codex-no-tui-5ff16448/protected-files-verification.json)；只有两份授权总文档变化。前轮 wrapper 正常退出自动新增此前不存在的 `resume-exit.json`，没有覆盖旧证据。无登录、购买、凭据复制、切换模型或 provider 配置变更。正式 handoff 另存本轮目录；DONE 只表示调查交付，不构成自动唤醒 ACK。

## 前轮有效范围与结果（0cf09894，仅 Codex 中断工具执行语义）

新证据：[报告](S1-codex-interrupt-0cf09894/README.md)、[机器矩阵](S1-codex-interrupt-0cf09894/runtime-matrix.json)、[原生关联](S1-codex-interrupt-0cf09894/codex-verification.json)、[证据断言](S1-codex-interrupt-0cf09894/evidence-validation.json)、[依赖](S1-codex-interrupt-0cf09894/dependencies.json)。仅复用原授权 window/workspace/surface 与 A thread `01a19ad4-2958-75f3-82b6-f488745caec4`；原 TTY `ttys028`，原 Codex PID `96559`，手动恢复后 PID `11697`。

| 能力 / 当前事实 | 最新实际结果 | 验收边界 |
|---|---|---|
| 原生 Ctrl-C 中断动作与事件 | 一次定向 `ctrl+c`，native `turn_aborted` / `reason=interrupted`，原 turn `01a19b29-0827-7202-8ebf-59971d59c316` | **PASS，仅 turn 中断事件** |
| Ctrl-C 单独终止工具 | 工具 OS PID `10800` / PPID `96559` / PGID `10800`；中断后及 `/stop` 前 `ps` 均存活，屏幕仍有一个后台终端 | **FAIL_OBSERVED**；原生工具 session `38086` 不是 OS PID，不能以 turn aborted 推断进程结束 |
| 追加原生 `/stop` 清理 | TUI 提示的 `/stop` 后 native CommandExecution failed / exit=-1，独立内核 NOTE_EXIT 对应 PID `10800`，随后多次 `ps` 不存在 | **PASS，仅显式追加 `/stop` 路径**；具体终止 OS signal UNVERIFIED，无外部 kill/SIGKILL |
| 无伪造原工具完成或重放 | 一次原工具启动、只有 TOOL_BEGIN、无 TOOL_COMPLETE/INTERRUPT_TOOL_DONE/原 turn task_complete；最终观察越过原 240 秒期限 | **PASS，本次有界观察**；`Script completed`、`item_completed`、屏幕 `Ran` 不是长工具成功或业务交付 |
| 同 native thread 手动恢复 | 正常 `/quit` exit=0，再同 surface/cwd/TTY 显式 resume 原 A；新 nonce 一条 user message、一次 exec/尝试、一个成功结果和唯一模型 ACK | **PASS，手动恢复**；退出码/handoff 均不替代 ACK |
| 上下文与结果可见 | 新 nonce `S1-resume-once-d0bb72df-547a-4abd-b811-3358e9154b6c` 的 RESUME_ONCE_ACK 与结果同屏；模型回忆未重发的中断 nonce 和历史 A 私有 nonce | **PASS，受控回忆和一次执行**；不泛化为自动恢复、事务或通用 exactly-once |
| 完整中断工具验收 | Ctrl-C 单动作不满足工具终止要求，原生 `/stop` 为额外动作 | **本轮整体不记 PASS**，`new_runtime_passes=[]`；累计 19 项保留，完整 S1 PARTIAL |
| 其他剩余项 | hook/AgentFlow 适配器、无 TUI 自动唤醒、自动/主机恢复、通用草稿门禁与中断工具完成/恰好一次保证 | **UNVERIFIED** |
| OpenCode / Claude | OpenCode 模型调用 0，沿用 provider 依赖；Claude 无操作 | 无新增验收 |

证据断言 PASS 只表示记录符合上述正向和负向事实。Go 未改，未运行 Go test/build/check 或 doctor；原配置、Go/构建定义、输入非 scratch 证据与 native A/B 原历史哈希核验通过。原 wrapper 正常退出仅自动新增此前不存在的 `a-final-exit.json`，旧文件未覆盖。最终 A 空闲且无草稿，原布局保留；无登录、购买、凭据读取复制或 provider 配置变更，不进入 S2。详见 [文件保护](S1-codex-interrupt-0cf09894/protected-files-verification.json) 和 [最终屏幕](S1-codex-interrupt-0cf09894/final-screen.json)。正式 handoff 仅表示调查报告已交付。

## 前轮有效范围与结果（754ba5a3，仅 Codex 并发双存活 native TUI）

新证据：[报告](S1-codex-concurrent-tui-754ba5a3/README.md)、[机器矩阵](S1-codex-concurrent-tui-754ba5a3/runtime-matrix.json)、[原生结果关联](S1-codex-concurrent-tui-754ba5a3/codex-verification.json)、[断言](S1-codex-concurrent-tui-754ba5a3/evidence-validation.json)、[依赖](S1-codex-concurrent-tui-754ba5a3/dependencies.json)。

| 能力 / 当前事实 | 最新实际结果 | 验收边界 |
|---|---|---|
| 双 native TUI 同时存活且可见 | 原 A PID `96559` / TTY `ttys028` / surface `4E483942-928F-455C-B03D-75DAEDE9F38F`；新分屏恢复 B PID `4776` / TTY `ttys003` / surface `2C84977B-4E1E-4790-BC80-2065AFF9D323`；两次同一 `ps` 各记录两条 Codex 进程，两个 pane 同时 selected | **PASS，受控并发存活**；原生 ID、argv、TTY、cwd、SQLite/rollout 与可见屏幕绑定；没有顺序复用 surface |
| 消息、输出、默认 cwd | 从对方 cwd 分别按显式 native ID 入队；各自原生工具打印私有 nonce、实际 cwd、surface/workspace；原生 assistant 与同 surface 屏幕吻合 | **PASS**；不是根据 queue/cmux 退出码判断 ACK |
| 对话上下文隔离 | 回忆请求不含私有 nonce，各自正确回忆本 thread 的值，对另一边回答 UNKNOWN；完整 native 记录和屏幕无对方 private/route nonce | **PASS，受控对话**；共享文件系统与 CODEX_HOME 不构成安全隔离 |
| 清理与原会话保护 | B 正常退出 0，PID 不存在，新增 surface 关闭；稳定拓扑恢复原布局，A 原 PID、native ID、cwd 保留且空输入栏 | **PASS**；A/B 旧历史保留为前缀，旧证据哈希相同；未发送信号 |
| hook / AgentFlow 适配器 / 无 TUI 自动唤醒 / 自动和主机恢复 / 中断工具语义 / 通用草稿门禁 | 本轮范围外 | **UNVERIFIED**；完整 S1 仍 PARTIAL |
| OpenCode / Claude | OpenCode 模型调用 0，沿用 provider 依赖；Claude 无操作 | 无新增验收 |

新增 `codex.concurrent_live_two_tui_isolation`，18 项历史通过保留，累计 19 项。Go 源码未改，本轮未运行 Go test/build/check；实际证据断言与源码/构建定义/原配置/输入非 scratch 证据哈希核对 PASS。关闭 RPC 后即时拓扑仍有旧分屏，后续 [稳定快照](S1-codex-concurrent-tui-754ba5a3/final-tree-settled.json) 才确认恢复；两者均保留。无登录、购买、输入/复制凭据或 provider 配置变更，没有向协作 main/worker 发送实验输入，不进入 S2。正式 handoff 保存在本轮目录，不充当 native ACK。

## 前轮有效范围与结果（fd8f8aac，仅 Codex 最后两项边界）

新证据：[报告](S1-codex-final-edge-fd8f8aac/README.md)、[机器矩阵](S1-codex-final-edge-fd8f8aac/runtime-matrix.json)、[原始交互索引](S1-codex-final-edge-fd8f8aac/codex-verification.json)、[断言](S1-codex-final-edge-fd8f8aac/evidence-validation.json)、[依赖](S1-codex-final-edge-fd8f8aac/dependencies.json)。原 A `01a19ad4-2958-75f3-82b6-f488745caec4`，新 B `01a19b0a-7662-7582-a5f0-36ea08fb1205`；只复用原授权 surface `4E483942-928F-455C-B03D-75DAEDE9F38F`，顺序切换，没有新分屏。

| 能力 / 当前事实 | 最新实际结果 | 验收边界 |
|---|---|---|
| 双 native thread 默认 cwd | A 为原 `S1-manual-login-retry-a128f1ae/scratch/codex/cwd`；B 为新 `S1-codex-final-edge-fd8f8aac/scratch/cwd-b`；真实工具输出路径、nonce、workspace/surface | **PASS，单 surface 顺序运行的两个独立 thread**；并发存活双 TUI 未验证 |
| 跨 cwd 消息隔离 | B cwd 发给停止 A 的 queue 未进入 B，A 恢复后才 native ACK；A cwd 发给活跃 B 正确 ACK | **PASS，显式 native ID queue 路由**；退出码不作为 ACK |
| 私有上下文隔离 | 各自从历史回忆私有 nonce，完整 native 记录不含对方本轮私有 nonce / route nonce | **PASS，受控对话上下文**；不证明文件系统安全隔离 |
| 执行中异常退出 | B PID `93342` / TTY `ttys028` / argv / cwd / native ID 与仍运行的 nonce 工具核对；单 PID SIGKILL，wrapper 实际 -9 | **PASS，受控工具执行期间终止**；原 A 未被 signal |
| 恢复后继续交付 | 同 B 手动 resume，新 PID `95478`，实际 `/bin/pwd`，RECOVERY_DELIVERED + 提示未重发的 B 私有 nonce | **PASS，手动恢复后新请求交付**；自动/主机故障/原工具完成/恰好一次未验证 |
| 中断工具遗留 | 睡眠 PID `95169` 一度 PPID 1，最终记录 PID 不存在；原 120 秒工具未交付 MIDFLIGHT_DONE | 不声称自动清理或重放保证 |
| 最终原会话 | A / 原 cwd，PID `96559` / wrapper `96558` / TTY `ttys028`，实际 pwd 和 ORIGINAL_RESTORED，空输入栏 | **LIVE_IDLE_EMPTY_PROMPT**；B 正常退出 0 |
| hook / AgentFlow 适配器 / 无 TUI 自动唤醒 / 通用草稿门禁 | 本轮没有实现或新增验证 | **UNVERIFIED / NOT_IMPLEMENTED** |
| OpenCode / Claude | OpenCode 新模型调用 0，沿用 transport / provider 依赖；Claude 无操作 | 无新增验收 |

机器矩阵保留 16 项历史通过，新增 `different_cwd_two_native_threads`、`sigkill_mid_tool_manual_context_recovery`，共 18 项受控通过。Go 源码未改，按本轮要求未运行 Go test/build/check；证据断言和源码/构建定义/原配置/既有证据哈希对账 PASS。前轮 wrapper 只自动新增此前不存在的退出文件：[核验](S1-codex-final-edge-fd8f8aac/protected-files-verification.json)。无登录、购买、输入凭据或 provider 配置变更；完整 S1 仍 PARTIAL，不进入 S2。正式协作回传保存在本轮目录 `handoff.json`，不充当 native ACK。

## 前轮有效范围与结果（458ce2e5，仅 OpenCode provider 只读检查）

新证据：[报告](S1-opencode-local-provider-458ce2e5/README.md)、[机器矩阵](S1-opencode-local-provider-458ce2e5/runtime-matrix.json)、[分层验证](S1-opencode-local-provider-458ce2e5/opencode-verification.json)、[依赖](S1-opencode-local-provider-458ce2e5/dependencies.json)。本轮新增 `runtime_passes=[]`，不重试免费额度模型。

| 能力 / 当前事实 | 最新实际结果 | 验收边界 |
|---|---|---|
| OpenCode CLI / 隔离 server | 均 `1.19.5`，原 server PID `26680` 存活 | 版本/健康只证明本地服务可访问 |
| 原 native ID | `ses_e65a4cf24ffe3wZAiM2U7og4Px` GET 200、idle，模型仍 `opencode/big-pickle` | session 元数据不等于模型 ACK 或恢复 |
| 已有 provider/model | connected 仅 `opencode`，8 个免费模型；无自定义 provider，目录内 `lmstudio` 未连接 | **MODEL_PROVIDER_UNAVAILABLE**，没有已接入可用的本地替代 |
| 本地端点 | `11434/1234/8080/8000` 拒绝连接；已有 `5000/8787` 的模型列表均 404 | 仅覆盖记录的端点；不推断其他配置 |
| 原可见目标 | 指定 window 不存在，原 attach PID 不存在 | 追加 **EXISTING_VISIBLE_TARGET_UNAVAILABLE**；未重建/改投 |
| transport | 沿用前轮 **VERIFIED_TRANSPORT_ONLY**，本轮未重测 | 保留历史等级，未升级为模型通过 |
| 模型 nonce / ACK | 本轮没有发送，模型请求 0；前轮 **FREE_TIER_LIMIT** 未重试 | **WAITING_DEPENDENCY**，无新增 runtime pass |
| reset READY / 角色 / 实际身份 / full / 模型恢复 | 本轮均未执行 | 全部 **UNVERIFIED** |
| Codex / Claude | 本轮无运行时操作，Codex 16 项历史通过保留 | 当前存活未重核，不新增验收 |

三项工程检查 `go test ./...`、`go build ./...`、`make check` 退出 0。源码、原隔离配置和输入顶层证据哈希对账见 [核验](S1-opencode-local-provider-458ce2e5/protected-files-verification.json)。没有登录、购买、复制凭据或配置变更；完整 S1 保持 PARTIAL，不进入 S2。协作 handoff 不算模型 ACK。

## 前轮有效范围与结果（eb2e9a87，仅 Codex 边界补证）

新证据：[报告](S1-codex-edge-cases-eb2e9a87/README.md)、[runtime-matrix.json](S1-codex-edge-cases-eb2e9a87/runtime-matrix.json)、[Codex 逐项原始索引](S1-codex-edge-cases-eb2e9a87/codex-verification.json)、[断言](S1-codex-edge-cases-eb2e9a87/evidence-validation.json)、[dependencies.json](S1-codex-edge-cases-eb2e9a87/dependencies.json)。Codex 的前轮 9 项通过保留，追加以下 7 项受控通过。native thread 全程 `01a19ad4-2958-75f3-82b6-f488745caec4`，window/workspace/surface 与前轮一致；没有新登录或凭据复制。

| 能力 | Codex 最新实测 | 状态与边界 |
|---|---|---|
| busy queue 投递 | 实际 40 秒 CommandExecution 内入队；BUSY_DONE 后同 thread ACK | **PASS，受控忙碌任务后顺序接收** |
| 非空草稿保护 | 两条 queue 消息原生 ACK 后草稿仍完整，未被提交为 user 消息 | **PASS，仅 native queue 路径**；通用终端草稿门禁 UNVERIFIED |
| 不同发送者 cwd 路由 | B 中执行 queue，A 中同一原生 TUI 收到并 ACK | **PASS，显式 native ID 路由** |
| 同 thread cwd 对照 | 原生恢复菜单一次性选择当前目录，真实工具 cwd A→B；最后回 A | **PASS，单 thread A/B**；双 thread 隔离 UNVERIFIED |
| SIGKILL 后上下文恢复 | 实验 PID 59794 真实退出 -9，手动 resume 原 ID，模型取回提示中未重发的完整原 nonce | **PASS，仅空闲强制终止后的手动恢复**；执行中/主机故障 UNVERIFIED |
| native queue 后台接收 | 存活 `--no-daemon` TUI 接收，5 个 queue ID 分别对应真实 assistant ACK 和同 surface 屏幕 | **PASS，仅原生命令与存活 TUI** |
| 停止时 queue 持久化 | `--resume` 返回 0 但警告不能恢复，rollout 暂无变化；手动 resume 后才 ACK | **PASS，仅持久化及手动恢复接收**；自动唤醒 UNVERIFIED |
| hook / AgentFlow hook/queue 适配器 | 本轮未运行 hook，Go 只有 probe 候选检查 | **UNVERIFIED / NOT_IMPLEMENTED**，不据原生 queue 实测宣称产品已实现 |
| 最终运行状态 | 原 native ID、原 cwd A、PID 70876 / TTY ttys028 / 空输入栏 | **LIVE_IDLE**；见最终 status/process/native |
| OpenCode / Claude | OpenCode 本轮 0 请求，沿用原无登录 transport 与额度依赖；Claude 排除 | OpenCode **VERIFIED_TRANSPORT_ONLY / WAITING_DEPENDENCY**；Claude 无新增验收 |

工程 `go test ./...`、`go build ./...`、`make check` 全部退出 0。未改 Go 源码，未进入 S2。完整 S1 仍 PARTIAL，协作 handoff 不计为 native ACK。

## 前轮有效范围与结果（a128f1ae，仅 OpenCode / Codex）

新证据：[报告](S1-manual-login-retry-a128f1ae/README.md)、[runtime-matrix.json](S1-manual-login-retry-a128f1ae/runtime-matrix.json)、[evidence-validation.json](S1-manual-login-retry-a128f1ae/evidence-validation.json)。用户已确认在本轮独立窗口手动完成 Codex 登录；不再进行登录界面操作。下表覆盖历史记录中的旧阻塞，完整 S1 仍 PARTIAL。

| 能力 / 当前事实 | OpenCode | Codex |
|---|---|---|
| 范围 / 认证 | 无登录，复用独立 local serve + attach | 新 window、独立 CODEX_HOME，用户手动登录已完成；未复制旧凭据 |
| window / workspace | `CAE5B3FE-3E61-4E92-9896-775B9343687F` / `158ADE67-F09E-47D5-9EB9-7B6718BE682E` | `2B9E10E2-0C0A-47DC-AED7-206B833A4462` / `28A52619-270D-4FC5-A692-C730028054E2` |
| surface | `FDDB002D-F985-4CB1-AE3C-FB8918AADC5D` | `4E483942-928F-455C-B03D-75DAEDE9F38F`，全程同一 surface |
| 原 native ID / 当前 native ID | `ses_e65a4cf24ffe3wZAiM2U7og4Px` | `01a19ad4-2958-75f3-82b6-f488745caec4`，已恢复 |
| `/new` 后 native ID | 本轮未重置 | `01a19ad6-edd3-7e10-bb5e-0a04a699da32` |
| native 可见绑定 | session API、attach argv / TTY、可见消息匹配 | **PASS**：原生 `/status`、SQLite、rollout、进程↔TTY↔surface 一致 |
| 空闲 / 无草稿后唯一 nonce | **VERIFIED_TRANSPORT_ONLY**：noReply HTTP 200 + native message + 可见显示 | **PASS**：先检查原生 ID、空输入栏和空闲，再粘贴并单独 Enter |
| 同会话真实模型 ACK | **WAITING_DEPENDENCY**：`free_tier_limit`，assistant parts 空 / output tokens=0 | **PASS**：native assistant 与同 surface 返回 `ACK S1-codex-a128-5633e5b0-be0f-4c85-ab21-cc361ec03a35` |
| reset READY | UNVERIFIED | **PASS**：ID 改变，新 thread 返回带 nonce 的模型 READY |
| 角色重注入 | UNVERIFIED | **PASS（受控会话）**：READY role=资深研发，下一次真实工具调用继续保持角色 |
| 实际调用身份 / 刷新 | UNVERIFIED | **PASS**：新 thread 原生 CommandExecution、cwd、workspace/surface 输出匹配 |
| 有效 full | UNVERIFIED | **PASS（受控探针）**：native never / danger-full-access / disabled；实际 cwd 外自有 scratch 写入回读 nonce，无审批 |
| 原会话恢复 | UNVERIFIED | **PASS**：同 surface resume 原 ID，模型从历史回忆原 nonce；恢复请求未包含 nonce |
| 主动退出 | 本轮未执行退出 | **PASS**：`/quit` exit=0，窗口/shell 保留，随后恢复 |
| busy / 非空草稿保护 / 不同 cwd 对照 / 崩溃恢复 / 后台 hook/queue | UNVERIFIED | UNVERIFIED |
| 当前状态 / 下一步 | WAITING_DEPENDENCY；等待可用的无登录模型容量，不启动登录 | 已列场景 PASS；窗口/native 客户端继续存活，剩余场景待后续 S1 |

逐项原始索引：[OpenCode 验证](S1-manual-login-retry-a128f1ae/opencode-verification.json)、[Codex 验证](S1-manual-login-retry-a128f1ae/codex-verification.json)。full 只证明配置与实际受控操作，不代表穷尽 OS/网络权限；角色 PASS 只证明本 native 会话行为。协作 handoff 不计入任何 runtime PASS。工程 test/build/check 均为 0，未修改 Go 源码、未进入 S2。Claude 本轮未启动、认证、重置或发消息。

## 前轮范围与结果（871686a3，历史）

当轮证据：[报告](S1-auth-retry-871686a3/README.md)、[runtime-matrix.json](S1-auth-retry-871686a3/runtime-matrix.json)、[dependencies.json](S1-auth-retry-871686a3/dependencies.json)。当轮完整 S1 **PARTIAL**；运行时 **WAITING_DEPENDENCY**；`runtime_passes=[]`。用户当轮停止全部认证；此限制已被上方本轮明确隔离 Codex 手动登录授权更新，OpenCode 仍禁止登录。

| 能力 / 当前事实 | OpenCode | Codex | Claude |
|---|---|---|---|
| 当前范围 | 无登录 native session / 可见 noReply 投递 | 仅核对已有已登录可见会话/原生身份 | 认证和 key 输入已停止，保留历史证据 |
| native ID | `ses_e65a4cf24ffe3wZAiM2U7og4Px` | 尚无可安全绑定的实验 thread；不能拿协作 ID 替代 | 历史 `9d2555cf-41c7-4eb8-a957-51124de8ff5d` |
| 实际 surface | `FDDB002D-F985-4CB1-AE3C-FB8918AADC5D` | 已发现 5 个进程↔TTY↔surface 匹配，见目标映射；均未获确认可用于实验 | 历史 `A7B86D73-4FB8-4D8A-9D50-BFC2C684A3AB`，原 client 已停止 |
| 无登录 nonce 投递 | **VERIFIED_TRANSPORT_ONLY**：`noReply=true` HTTP 200、原生消息回读、可见屏幕匹配、session idle | 未发送实验 nonce | 未追加请求 |
| 模型 ACK | UNVERIFIED；noReply 标记不请求模型 | UNVERIFIED | UNVERIFIED；此前响应 synthetic 认证错误 |
| reset READY / 角色执行 / 调用身份 / 有效 full / 完整恢复 | 全部 UNVERIFIED | 全部 UNVERIFIED | 全部 UNVERIFIED |
| 当前依赖 | 真实模型 ACK 缺失；本轮只授权本地投递，不重试认证 | **NO_CONFIRMED_SAFE_EXISTING_CODEX_TARGET**；用户确认已登录，不再归因 LOGIN_REQUIRED | 用户取消认证；不再尝试登录或 key 输入 |

OpenCode native message 为 `msg_19acb5303001aoptO3swe72CNE`，nonce 为 `S1-noauth-visible-ed7b0e7c-ac94-42d3-969e-99bc1835c7f5`，详见 [传输验证](S1-auth-retry-871686a3/opencode-transport-verification.json)。5 个 Codex 匹配中，一个属于受保护协作 workspace，其余属于其他项目；没有确认 thread、空闲/草稿状态及实验归属，未向已有会话注入输入，见 [已有目标](S1-auth-retry-871686a3/codex-existing-targets.json)。此前隔离 CODEX_HOME 的登录失败仅为该隔离状态的历史事实。

原指定 window/workspace 已关闭，事件 origin=`appkit_close`，发起者未知；先保存事件，再在独立替代 window `CAE5B3FE-3E61-4E92-9896-775B9343687F` / workspace `158ADE67-F09E-47D5-9EB9-7B6718BE682E` 复用原 native 状态。认证进程已停止并用 `ps` 确认无匹配。工程 test/build/check 全部退出 0；三工具 runtime PASS 数仍为 0。

## 前轮隔离重试（cbcea350，历史记录）

完整证据：[S1-runtime-retry-cbcea350](S1-runtime-retry-cbcea350/README.md)、[runtime-matrix.json](S1-runtime-retry-cbcea350/runtime-matrix.json)、[dependencies.json](S1-runtime-retry-cbcea350/dependencies.json)。本节覆盖下方前轮“无 native ID”等历史状态，完整 S1 继续 PARTIAL，运行时 WAITING_DEPENDENCY，runtime_passes=[]。

| 能力 / 实际观察 | OpenCode | Codex | Claude Code |
|---|---|---|---|
| 运行版本 | 1.19.5 | 0.166.0 | 运行中 2.1.284；工具自动更新后独立 --version 为 2.1.316 |
| 独立 window / workspace | 三工具共用新窗口 `BB9A5102-A080-47B6-98E4-AA2F4445E259` / workspace `BA91FC57-BF8B-4CC8-BC14-C7F138DE8EB0` | 同左 | 同左 |
| 独立可见 surface | `79B69666-1879-4E91-99F6-AFE9F80DB3DD` | `2A3A96DE-DA25-4293-8860-E21EB814A124` | `1461550B-80C4-4AE6-9C0C-6DE274017B9E` |
| native ID 实际建立 | `ses_e65a4cf24ffe3wZAiM2U7og4Px`：POST /session + SQLite + attach argv | null；隔离 threads 表空 | `9d2555cf-41c7-4eb8-a957-51124de8ff5d`：原生 /status + JSONL |
| 唯一 nonce 实际提交 | API 204 + native message + 可见输入；额度耗尽 | 未提交；设备登录失败 | 提交 Enter + native JSONL；synthetic authentication_failed |
| 同可见 native session 模型 ACK | WAITING_DEPENDENCY；无文本/零 tokens | WAITING_DEPENDENCY；未登录 | WAITING_DEPENDENCY；无模型响应 |
| reset / 完成信号 | UNVERIFIED；第二 ID 创建并同 surface attach，无 agent READY | UNVERIFIED | UNVERIFIED |
| 角色重注入 | UNVERIFIED；noReply 角色消息已存储，无执行 | UNVERIFIED | UNVERIFIED |
| 实际调用身份 / 刷新 | UNVERIFIED；仅元数据 build/big-pickle/cwd，无工具调用 | UNVERIFIED | UNVERIFIED；认证失败响应为 synthetic |
| 有效 full | UNVERIFIED；session wildcard allow 被接受，agent 仍有 deny，无工具实验 | UNVERIFIED | UNVERIFIED；实际记录 auto |
| 原会话恢复 | UNVERIFIED；原 ID 重启 + API 原消息保留，最终 UI 未展示原上下文 | UNVERIFIED | UNVERIFIED |
| busy / 草稿 / 不同 cwd 对照 / 主动退出 / 崩溃 | UNVERIFIED | UNVERIFIED | UNVERIFIED |
| 实际下一步依赖 | 独立恢复可用额度/认证；同 ID 新 nonce ACK | 独立设备授权、token 请求恢复、可用隔离浏览器 | 与 --bare 兼容的独立认证；目录信任已完成 |

旧窗口事件已回读：surface workspace_teardown → workspace.closed → window.closed(origin=appkit_close)，关闭发起者未知。新窗口保留控制 shell 和 native 退出后的 zsh，约 75 分钟定向观察存活；不是永久存活保证。见 [关闭时间线](S1-runtime-retry-cbcea350/old-window-close-timeline.json) 与 [存活记录](S1-runtime-retry-cbcea350/window-survival.json)。工程 test/build/check 全退出 0。

Claude 首次启动发生自动更新旁效应，后续启动才显式禁用 updater；没有主动改全局配置，不宣称第三方全局文件无变化。所有阻塞、命令和退出状态均保留。下文为前轮基础能力与历史实验记录。

## 版本与接入候选

| 项目 | OpenCode | Codex | Claude Code |
|---|---|---|---|
| 实际版本（PASS） | 1.19.5 | codex-cli 0.166.0 | 2.1.284 |
| 实际可执行路径 | `/opt/homebrew/bin/opencode` | `/opt/homebrew/bin/codex` | `/Users/funlake/.nvm/versions/node/v26.8.1/bin/claude` |
| 版本证据（退出码 0） | [opencode-version.json](S1-verified/local-probe/opencode-version.json) | [codex-version.json](S1-verified/local-probe/codex-version.json) | [claude-version.json](S1-verified/local-probe/claude-version.json) |
| 已执行帮助（退出码均 0） | `--help`、`serve --help`、`attach --help`、`run --help` | `--help`、`queue --help`、`app-server --help`、`resume --help` | `--help` |
| 启动/绑定候选（ADVERTISED_ONLY） | serve 的 hostname/port；attach 的 session/dir | app-server；原生 TUI 的 `--remote` | `--session-id` |
| 接收候选 | run 的 attach/session 只在帮助中声明；session API / prompt_async 未调用 | queue 的 thread/message/resume 只在帮助中声明；未排队任何消息 | cmux hook 未检查安装/配置/运行；UNVERIFIED |
| 权限候选（ADVERTISED_ONLY） | `--auto` 可见，但明确保留显式 deny，不能据此证明 full | `--dangerously-bypass-approvals-and-sandbox` 可见 | `--permission-mode bypassPermissions`、`--dangerously-skip-permissions` 可见 |
| 恢复候选 | attach/session 只作为候选 | resume 只在帮助中声明 | resume 只在帮助中声明 |
| 后续独立会话计划 | `--pure` | `--no-daemon` | `--safe-mode` |

前轮启动计划包含独立 cwd、底层 executable argv 和环境覆盖，见基础机器矩阵每个工具的 `isolated_session_plan`。本轮使用新证据目录执行了这些候选启动，并额外给 Codex 指定文件凭据存储、Claude 指定 `--bare`，防止 keychain 凭据复用。实际 native ID、ACK、权限和恢复未建立，状态仍为 `WAITING_DEPENDENCY`；旧机器矩阵是前轮快照，不覆盖改写。

## 本轮独立可见启动与阻塞

自有窗口：`7B4184EF-AEF6-4A60-9C92-593645DFA10E`；自有 workspace：`44AC3631-8A19-42C7-8E06-CD9A2AD4F182`。协作 session `b7bf333c-53ad-492e-8196-61f0acabed60` 不作为 native ID。

| 观察 | OpenCode | Codex | Claude Code |
|---|---|---|---|
| 实际 surface UUID | `E31395A3-3A26-4155-BF07-9AF017CA7FCB` | `C7B13CA5-4331-4025-BAC9-F8B20579DFDA` | `B46EBE05-9EEC-4413-8DFF-73E923DED334` |
| 原生界面实际状态 | 首页 Build / Big Pickle | 登录方式选择页 | 独立 cwd 信任页，未确认 |
| native session/thread ID | null；隔离 session 表前后 0 行 | null；隔离 threads 表前后 0 行 | null；无原生 JSONL |
| 唯一 nonce | 已生成，未提交 | 已生成，未提交 | 已生成，未提交 |
| agent ACK | 无 | 无 | 无 |
| 本轮依赖 | 建立 session + 可见 attach 前窗口丢失 | 隔离登录必需，且窗口丢失 | onboarding 未完成，且窗口丢失 |
| 原生退出码 | 0，曾对已核对实验 PID 发送 TERM | null，未捕获 | null，未捕获 |
| 原始屏幕 | [首页](S1-runtime-b7bf333c/opencode-screen-start.json) | [登录页](S1-runtime-b7bf333c/codex-screen-start.json) | [信任页](S1-runtime-b7bf333c/claude-screen-after-security.json) |

nonce 的具体值、精确 argv、cwd、环境覆盖和 PID 见新目录各工具的 `*-plan.json`、`*-launch.json`、`*-process.json`。原始屏幕含完整 base64/text，`*-decoded.json` 是逐行解码副本。窗口丢失时定向命令真实退出 1：`TabManager not available`，随后指定窗口 tree 退出 1：`Window not found`。具体请求与错误见 [owned-topology-after-unavailable.json](S1-runtime-b7bf333c/owned-topology-after-unavailable.json)。未重新创建窗口、安装 hook、登录或换投协作 surface。

记录的原生 PID 和包装器 PID 随后均不再存在；原协作窗口的 workspace/布局/pane/surface 集合保持一致，见 [topology-comparison.json](S1-runtime-b7bf333c/topology-comparison.json)。焦点选择前后有变化，原因不确定，保留原始观察。窗口消失不证明主动退出或崩溃恢复。

OpenCode 帮助证据主要位于 stderr，原始两条流均保留。[Codex queue 帮助](S1-verified/local-probe/codex-queue-help.json) 明确说明恢复失败仍可成功退出；不能将退出 0 当作唤醒、投递或 ACK。

## 运行时验收状态

| 能力 | OpenCode | Codex | Claude Code | 缺少的证据/依赖 |
|---|---|---|---|---|
| 原生可见会话与后台路由绑定 | WAITING_DEPENDENCY | WAITING_DEPENDENCY | WAITING_DEPENDENCY | 已有实际 surface；无 native ID；实验窗口消失，Codex 另需隔离登录 |
| 空闲主动接收、接收端 ACK | WAITING_DEPENDENCY | WAITING_DEPENDENCY | WAITING_DEPENDENCY | nonce 未提交，无 ACK；先建立 native ID 与稳定独立窗口 |
| 新会话操作与重置完成事件 | UNVERIFIED | UNVERIFIED | UNVERIFIED | 重置前后 native ID 与完成信号 |
| 角色重新注入、调用身份刷新 | UNVERIFIED | UNVERIFIED | UNVERIFIED | 实际 agent 执行身份与角色证据 |
| 实际有效 full 权限 | UNVERIFIED | UNVERIFIED | UNVERIFIED | 独立启动的有效权限与覆盖规则验证 |
| 原会话恢复 | UNVERIFIED | UNVERIFIED | UNVERIFIED | 实际恢复相同 native ID 和上下文 |
| busy / 输入草稿保护 | UNVERIFIED | UNVERIFIED | UNVERIFIED | 独立受控输入与忙碌实验 |
| 不同 cwd 隔离 | UNVERIFIED | UNVERIFIED | UNVERIFIED | 两个真实原生会话与路径证据 |
| 主动退出 / 崩溃区分及恢复 | UNVERIFIED | UNVERIFIED | UNVERIFIED | 退出意图、实际进程事件与恢复证据 |

上述项目的后续执行依赖稳定的独立原生可见终端、明确 native 身份、登录/模型可用性与针对各工具的接收/事件方案。Codex 已实测停在登录选择页；OpenCode/Claude 的登录和三工具模型额度没有验证，不能断言缺失或可用。当前 worker/main 不是这些实验的目标。

## cmux 前置条件与身份观察

| 项目 | 状态 | 实际命令/结果 | 证据 |
|---|---|---|---|
| 版本 | PASS | `cmux version`，`cmux 0.65.2 (113) [12023d89f]`，退出 0 | [cmux-version.json](S1-verified/local-probe/cmux-version.json) |
| Socket 可达 | PASS | `cmux ping`，PONG，退出 0 | [cmux-ping.json](S1-verified/local-probe/cmux-ping.json) |
| 能力声明 | PASS（读取） | `cmux --json capabilities`，退出 0；声明不等于调用通过 | [cmux-capabilities.json](S1-verified/local-probe/cmux-capabilities.json) |
| 调用者/焦点观察 | PASS（读取） | `cmux --json --id-format uuids identify`，退出 0 | [cmux-identify.json](S1-verified/local-probe/cmux-identify.json) |
| 指定工作区拓扑 | PASS（读取） | `cmux --json --id-format uuids tree --workspace D9F41024-5C40-4E98-8B34-BD39785B0C81`，退出 0 | [cmux-tree.json](S1-verified/local-probe/cmux-tree.json) |
| surface → 固定 worker / native 会话绑定 | UNVERIFIED | 环境/标题/caller/focused 不构成实际 agent 身份 | [matrix.json](S1-verified/local-probe/matrix.json) |

最终 tree 列出指定工作区下三个终端 surface：`CC1C9123-FA9B-42CD-98C0-4974FD9BC3CF`、`5939D158-188A-4095-BF09-1D4B8C3FAA58`、`3637E165-9BD7-4EF5-B8B6-C4B46DEB2867`。这不是三个不同工具的接收会话证据；本轮没有读取这些终端的聊天正文，也没有在其上做接收/重置实验。

最终环境 surface/panel 与 cmux caller 都为 `CC1C9123-FA9B-42CD-98C0-4974FD9BC3CF`；focused surface 为另一个工作区中的 `3D8E1E72-FC05-4BC8-AB10-BB66CB89E703`，focused workspace 为 `0895C9BA-94AE-444D-A5DB-8FC1930BE65A`。被查询的工作区当时不是前台工作区。这些原始差异被保存，没有据此改绑、切换焦点或发送测试消息。

## 错误路径

| 场景 | 状态 | 真实退出/诊断 | 证据 |
|---|---|---|---|
| 要求真实运行时验收 | WAITING_DEPENDENCY | probe 退出 1；基础检查仍 PASS；`RUNTIME_UNVERIFIED` | [runtime-guard.json](S1-verified/runtime-guard.json) |
| PATH 中工具缺失 | WAITING_DEPENDENCY | probe 退出 1；子进程未启动，exit_code=null；`EXECUTABLE_MISSING` | [missing-tool.json](S1-verified/missing-tool.json) |
| 指向不存在的 cmux socket | WAITING_DEPENDENCY | probe 退出 1；真实 cmux ping 退出 1，Socket not found；`COMMAND_FAILED` | [unavailable-cmux.json](S1-verified/unavailable-cmux.json) |
| fixture 执行权限拒绝 | PASS（错误处理测试） | 实际 CLI 退出 1；`PERMISSION_DENIED`；子进程未启动 | [probe-tests.json](S1-verified/probe-tests.json) |
| fixture 命令失败 | PASS（错误处理测试） | fixture 退出 23；实际 CLI 退出 1；`COMMAND_FAILED` | [probe-tests.json](S1-verified/probe-tests.json) |
| fixture 超时 | PASS（错误处理测试） | 被终止子进程退出 -1；`COMMAND_TIMEOUT_OR_CANCELLED` | [probe-tests.json](S1-verified/probe-tests.json) |

fixture 行仅验证错误处理，实现测试不替代任何原生工具通信验收。整体结论见 [S1-result.md](S1-result.md)。
