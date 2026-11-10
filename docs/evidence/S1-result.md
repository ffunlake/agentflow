# S1 实施结果

最新状态（3266749c）：完整 S1 仍 **PARTIAL**。本轮只读发现用户当前 OpenCode 的安全外部 queue / 消息入口，结论 **UNVERIFIED / QUEUE_ENTRY_UNAVAILABLE**，新增功能 PASS、外部消息和模型请求均为 0。当前 surface / PID / TTY / OS cwd 与旧 native assistant 文本关联已核对；没有证明当前 runtime 的入站地址或 hook 消费者绑定，未发 nonce。历史各轮 PASS 保留各自范围，以下最新节优先于旧轮次摘要。

## 最新结果：3266749c，OpenCode 安全外部 queue 只读发现

协作 session `3266749c-1804-4665-9e94-71b766be4095` / task `s1-opencode-queue-discovery`，`ralph-loop`。新增 [报告](S1-opencode-queue-discovery-3266749c/README.md)、[机器矩阵](S1-opencode-queue-discovery-3266749c/runtime-matrix.json)、[缺口](S1-opencode-queue-discovery-3266749c/dependencies.json)、[原始命令记录器](S1-opencode-queue-discovery-3266749c/collect.py)。只调查 window `73FEB4F4-BE2B-4F4D-9B48-E872BBC51EA3` / workspace `3AB7D16C-B09D-44AE-8CD5-5B6225388D45` / surface `5265DA42-9A45-4C71-BABE-155E458FCF33`。

| 发现项 | 本轮证据与验收边界 |
|---|---|
| 当前身份观察 | OpenCode PID `23097` / PPID `21983` / `opencode --auto` / `ttys003` / cwd `/Users/funlake/Projects/okra-wiki`；同 surface 旧 ACK 和恢复回复对应只读 SQLite 中 native `ses_e64c2fa39ffeI5sYf6iCWDKYpv`。这是旧内容关联，不是新消息 ACK |
| 监听与子进程 | 目标无 TCP LISTEN，FD 17 为无可绑定 pathname 的 Unix 连接，目标子进程为空；loopback `55367` listener 所有者是 `Code H`，不能当作 OpenCode server |
| CLI / SDK 候选 | `run --attach --session --dir`、`prompt_async`、TUI control queue 仅有 help/本机 SDK 声明；没有可证明归属当前 runtime 的 server URL，没有启动第二 runtime 或请求 endpoint |
| cmux 消息候选 | `receiving=true`，但 `resume_binding` / `restore_record` 都为空，标准 cmux 插件路径不存在；配置仅取非敏感 stat 元数据，未证明任意自定义接收 hook。未发送 `agent message` |
| 功能 PASS | **无**；`new_runtime_passes=[]`，nonce=null，external_messages_sent=0，model_requests=0 |
| 外部同 session ACK / busy / 草稿保护 | **UNVERIFIED / QUEUE_ENTRY_UNAVAILABLE**；停在发现阶段，不归因于模型不可用，不盲试 |

[最终屏幕](S1-opencode-queue-discovery-3266749c/final-target-screen.json) 与初始屏幕一致；原 PID/TTY/cwd、原可见布局和空输入栏保留；[保护比较](S1-opencode-queue-discovery-3266749c/protection-comparison.json) 记录 17 个既有项目文件哈希无变化，native 元数据/选定旧文本一致。没有做 native 全内容或 wiki 全目录字节快照，不将这些投影升级为更强保护保证。未向目标 TUI 发 prompt/按键，未 reset/改配置/读凭据内容/写 wiki/操作 Codex 或 Claude/改 Go/进入 S2；未运行 Go test/build/check 或 doctor。`jev-judge` 当前 PATH 缺失，无伪造判断，继续已授权只读调查。正式 handoff DONE 仅表示本轮报告交付。

## 前轮状态摘要（468c9508）

状态：**PARTIAL（完整 S1）**；Go 安全 probe 基础检查维持历史 **PASS**。2026-11-02 最新 468c9508 在授权用户窗口验证 Codex **busy 原生排队和非空草稿保护 PASS，仅受控 native queue**：真实工具运行期间入队，原 turn 完成后唯一 assistant ACK，草稿保留且未形成 user message，最后只清除自有草稿。OpenCode 缺少已验证安全外部 queue，本轮两项 **UNVERIFIED**，新增模型请求 0。上一轮两工具 reset/READY、角色、真实 pwd 与同进程手动恢复 PASS，以及 Codex 有效 full 原生策略与只读调用 PASS 保留；OpenCode 有效 full 仍 UNVERIFIED。两个原 PID/surface/TTY/cwd、原布局保留且输入栏为空。19 项旧环境受控 PASS 不继承或重复计数；无 TUI 自动唤醒仍 **UNVERIFIED / WAITING_DEPENDENCY**，Ctrl-C 单独终止工具仍 **FAIL_OBSERVED**。Claude 排除，hook/AgentFlow 适配器、自动/主机恢复、通用恰好一次与草稿门禁仍 UNVERIFIED。下方历史记录保留当时状态，以最新节为准。

## 前轮结果：468c9508，用户窗口 busy 与非空草稿保护

协作 session `468c9508-c700-42a7-9d91-81ffdeb46ff2` / task `s1-user-window-busy-draft`，`ralph-loop`。新增独占证据：[报告](S1-user-window-busy-draft-468c9508/README.md)、[机器矩阵](S1-user-window-busy-draft-468c9508/runtime-matrix.json)、[原生断言](S1-user-window-busy-draft-468c9508/evidence-validation.json)。共同 window `73FEB4F4-BE2B-4F4D-9B48-E872BBC51EA3` / workspace `3AB7D16C-B09D-44AE-8CD5-5B6225388D45` / cwd `/Users/funlake/Projects/okra-wiki`。

| 验收 | OpenCode 用户会话 | Codex 用户会话 |
|---|---|---|
| 原绑定与输入门禁 | 原 native `ses_e64c2fa39ffeI5sYf6iCWDKYpv` / surface `5265DA42-9A45-4C71-BABE-155E458FCF33` / PID `23097` / `ttys003`，空闲无草稿 | 原 native `01a19b3b-9fc2-7622-a43f-af895cfac312` / surface `94EBBAAE-5EA1-4352-B183-580E24F0F23C` / PID `23477` / `ttys004`，空闲无草稿 |
| busy 原生排队 | **UNVERIFIED**；原进程无 TCP 监听/可绑定 Unix 地址，CLI 未提供已验证 queue；未启动实验或另起 runtime | **PASS，受控 native queue**；45 秒工具实际区间 `08:38:53.283–08:39:38.211Z`，queue 于 `08:39:33.879498–34.131827Z` 入队；原 turn 完成 `08:39:39.791Z` 后，`turn_trigger=queue` 才开始并返回真实 assistant ACK |
| 非空草稿保护 | **UNVERIFIED**；未强行操作 | **PASS，仅 native queue**；唯一自有 marker 在第二条 queue assistant ACK 后逐字保留；native user message 中无 marker；仅清除本轮自有草稿 |
| 唯一交付与最终状态 | native 内容与屏幕前后相同，空输入栏 | 两条 queue 各唯一 canonical user/assistant，ID 与事件镜像/屏幕一致；原 PID/cwd/TTY、原历史前缀、空输入栏保留 |

Codex 两条队列 ACK 的 assistant IDs、queue IDs、真实 live session/工具 PID 与时间顺序均见新矩阵。仅新增 3 条 canonical user 请求、3 条最终 assistant 回复、1 次只读 CommandExecution；排队消息不调用工具。计数限于最终 native 快照，不宣称通用 exactly-once 或 AgentFlow 适配器已经实现。OpenCode 新模型请求 0。没有 reset/退出/kill/故障注入、修改 provider、Go 或 wiki、操作 Claude、向协作窗口发送实验消息；未运行 Go test/build/check 或 doctor。

[文件保护核验](S1-user-window-busy-draft-468c9508/file-protection-verification.json) 比较 wiki 原文件/Git status、agentflow 既有源码和非 scratch 历史证据；[SHA-256 清单](S1-user-window-busy-draft-468c9508/sha256-manifest.json)。一次进程命令参数采样包含无关敏感参数，交付前已替换为脱敏元数据，未用于 busy 判定；详见报告。两个目标最终保持原布局、原 PID/TTY/cwd 和空输入栏。完整 S1 仍 **PARTIAL**；历史 PASS 保留环境边界；handoff DONE 仅表示本轮调查交付。

## 前轮结果：dc2eb54e，用户窗口 reset、角色、权限与手动恢复

协作 session `dc2eb54e-025e-40a1-8d42-76b7852761f7` / task `s1-user-window-reset-recovery`，`ralph-loop`。新增独占证据：[报告](S1-user-window-reset-recovery-dc2eb54e/README.md)、[运行矩阵](S1-user-window-reset-recovery-dc2eb54e/runtime-matrix.json)、[证据断言](S1-user-window-reset-recovery-dc2eb54e/evidence-validation.json)。共同 window `73FEB4F4-BE2B-4F4D-9B48-E872BBC51EA3` / workspace `3AB7D16C-B09D-44AE-8CD5-5B6225388D45` / cwd `/Users/funlake/Projects/okra-wiki`。

| 验收 | OpenCode 用户会话 | Codex 用户会话 |
|---|---|---|
| 目标绑定 | 原 surface `5265DA42-9A45-4C71-BABE-155E458FCF33` / PID `23097` / `ttys003` | 原 surface `94EBBAAE-5EA1-4352-B183-580E24F0F23C` / PID `23477` / `ttys004` |
| native reset 前 → 后 | `ses_e64c2fa39ffeI5sYf6iCWDKYpv` → `ses_e64aa6b77ffeP7BLeSFxdbziwS`，原生 `/new` | `01a19b3b-9fc2-7622-a43f-af895cfac312` → `01a19b55-5f41-76c0-b91c-a09595425d28`，原生 `/new` 选择当前 worktree |
| READY / 角色 / 工具 cwd | **PASS，受控场景**：真实 assistant nonce READY、ROLE_ACK role=资深研发；native bash pwd exit=0 与共同 cwd 一致 | **PASS，受控场景**：真实 native assistant nonce READY、ROLE_ACK；CommandExecution pwd exit=0 与共同 cwd 一致 |
| 有效 full | **UNVERIFIED**；pwd 成功、Build auto 和 session permission=null 不证明 full | **PASS，仅有效原生策略与只读调用**；新 thread 原生 never / danger-full-access / permission_profile disabled，/status 同 ID，实际 pwd；写入和网络行为未测 |
| 原会话恢复 | **PASS，同进程手动 `/sessions`**：恢复原 ID，模型正确回忆未在恢复请求重发的旧 ACK，再实际 pwd | **PASS，同进程手动 `/resume <原ID>`**：/status 回到原 ID，模型正确回忆未重发旧 ACK，再实际 pwd |

原始前后屏幕、native 消息/工具及 PID/TTY/cwd 关联均在新目录；每项 PASS 绑定相同可见 surface。Codex `/new` 后进程仍持有旧 rollout 描述符，lsof 一度列出两个文件，不能独立推断当前 thread；最终以 /status、新 ID native nonce ACK 与屏幕共同确认。OpenCode 原非注入 parts、Codex 原 rollout 字节前缀保留。恢复仅为存活进程内切回，不宣称退出/崩溃/自动恢复。Full Access 不由提示文本单独判定。

本轮新增 user 模型请求 4 条、工具调用 4 次且均为 pwd。原窗口布局保留、两个原 PID 仍存活、输入栏为空；[文件保护核验](S1-user-window-reset-recovery-dc2eb54e/file-protection-verification.json) 比较 wiki 基线和既有源码/非 scratch 证据，仅允许两份总文档变化，[清单](S1-user-window-reset-recovery-dc2eb54e/sha256-manifest.json) 保留哈希。凭据内容不作为证据，编辑器注入内容脱敏。未操作 Claude/协作会话、未写 okra-wiki、无 git 修改/kill/登录/provider 变更，不进入 S2；未运行 Go test/build/check 或 doctor。完整 S1 仍 PARTIAL，旧受控 PASS 保留原边界；正式 handoff 仅表示本轮交付完成。

## 前轮结果：a541862c，用户新窗口已有 OpenCode/Codex 模型 ACK

协作 session `a541862c-c620-47e8-95c9-e2186e359c9c` / task `s1-user-window-evidence`，`ralph-loop`。新增独占证据：[报告](S1-user-window-evidence-a541862c/README.md)、[绑定](S1-user-window-evidence-a541862c/bindings.json)、[原始拓扑](S1-user-window-evidence-a541862c/target-tree.json)、[运行矩阵](S1-user-window-evidence-a541862c/runtime-matrix.json)、[证据断言](S1-user-window-evidence-a541862c/evidence-validation.json)。本轮仅整理用户已完成的验证，没有发新 prompt 或按键。

共同 window `73FEB4F4-BE2B-4F4D-9B48-E872BBC51EA3` / workspace `3AB7D16C-B09D-44AE-8CD5-5B6225388D45` / cwd `/Users/funlake/Projects/okra-wiki`。cmux tree 确认两个 surface 位于可见窗口，TTY 由 `ps` 与各 native 进程交叉关联，OS cwd 由只读 `lsof` 核对。

| 已有验证 | 绑定与真实结果 | 状态 |
|---|---|---|
| OpenCode 模型 ACK | surface `5265DA42-9A45-4C71-BABE-155E458FCF33` / `ttys003` / PID `23097` / 上述共同 window/workspace/cwd；[scrollback](S1-user-window-evidence-a541862c/opencode-scrollback.json) 第 6 行独立 assistant 返回 `OPENCODE_ACK_7ba139c0-0133-47da-b99d-e7549a22f735`，与第 3 行用户请求区分 | **PASS，仅已有真实 assistant 回复观察** |
| Codex 模型 ACK 与只读 pwd | surface `94EBBAAE-5EA1-4352-B183-580E24F0F23C` / `ttys004` / PID `23477` / 上述共同 window/workspace/cwd；[scrollback](S1-user-window-evidence-a541862c/codex-scrollback.json) 第 12 行独立 assistant 返回 `CODEX_ACK_9196eeee-17ab-40ac-91ee-8f558336b201`，随后 `Ran pwd` 及 assistant 回显真实 cwd | **PASS，仅已有真实 assistant 回复与 pwd 观察** |

两会话 native session/thread ID 未从现有屏幕证明，保留 **UNVERIFIED**；协作 session 不替代 native ID。reset/READY、角色重注入、身份刷新、有效 full、recovery、busy、草稿/不同 cwd 隔离、hook/queue 与自动唤醒均未在本窗口验证。屏幕中的 `Permission selection requested: Full Access` 仅为请求提示，`pwd` 成功不证明 full。屏幕模型标签不是提供商侧实际模型身份审计。

旧 OpenCode 隔离 provider/额度阻塞不再概括本用户窗口：本窗口已有真实模型 ACK，旧环境失败原样保留。历史 Codex 19 项受控 PASS 仅属于原实验边界；本轮两项观察单列。完整 S1 仍 PARTIAL。

新增原始 screen/scrollback、逐行解码、TTY 进程及 cwd 映射；只更新两份总文档。[既有文档保护](S1-user-window-evidence-a541862c/protected-documents-verification.json)、[SHA-256 清单](S1-user-window-evidence-a541862c/sha256-manifest.json)、[哈希核验](S1-user-window-evidence-a541862c/hash-verification.json) 仅核验证据与文档。历史证据未覆盖；未 reset/退出/kill、未修改 okra-wiki/Go、未运行 Go 检查或 doctor，Claude 无操作，不进入 S2。正式 handoff 另存本目录，不计作模型 ACK。

## 前轮结果：5ff16448，Codex 无 TUI queue 与手动恢复边界

协作 session `5ff16448-f0a6-44a5-a827-350891d0429c` / task `s1-codex-no-tui-wakeup`，`ralph-loop`。独立证据：[报告](S1-codex-no-tui-5ff16448/README.md)、[runtime-matrix.json](S1-codex-no-tui-5ff16448/runtime-matrix.json)、[dependencies.json](S1-codex-no-tui-5ff16448/dependencies.json)、[原生关联](S1-codex-no-tui-5ff16448/codex-verification.json)、[证据断言](S1-codex-no-tui-5ff16448/evidence-validation.json)。证据断言 PASS 不等于自动唤醒 PASS。

复用原授权 window `2B9E10E2-0C0A-47DC-AED7-206B833A4462` / workspace `28A52619-270D-4FC5-A692-C730028054E2` / surface `4E483942-928F-455C-B03D-75DAEDE9F38F` / thread A `01a19ad4-2958-75f3-82b6-f488745caec4`。基线确认 visible、idle、空输入栏、PID `11697` / `ttys028` 与原 cwd。正常 `/quit` exit=0 后，在原隔离授权环境从项目根目录执行唯一 nonce `S1-no-tui-13bf49bb-1e33-4b8e-b57e-8237a9888a03` 的原生 queue，queue ID `01a19b33-c278-7161-ae58-a1b342e3568e`。命令退出 0，但 stderr 明确 `could not resume thread ... start a local daemon or specify a server with --remote`，见 [完整命令结果](S1-codex-no-tui-5ff16448/queue-result.json)。未启动 daemon、选择 remote 或改配置。

三个无 TUI 观察点及手动恢复前最终快照均确认：rollout 417908 字节、SHA-256 完全不变，目标 TTY 无 Codex、同 surface 为 shell，未自动重建可见 TUI。queue 返回至恢复前最后快照约 **119.963 秒**。`07:42:23.133548Z` 才显式手动恢复原 ID，未重发消息；`07:42:24.409Z` 才新增原生 queue turn，随后唯一 user message、一次 `/bin/pwd` 成功结果，`07:42:34.001Z` 唯一 native assistant 返回 `NO_TUI_ACK <nonce> <原 cwd>`，同 surface 可见。结论为 **PERSISTED_UNTIL_MANUAL_RESUME**；无 TUI 自动唤醒 **UNVERIFIED / WAITING_DEPENDENCY**，`new_runtime_passes=[]`。结论限于此次 `--no-daemon`、未选择可用 daemon/remote 的原环境，不泛化到其他部署。

最终原 A/thread/surface/cwd/`ttys028` 已恢复，PID `19007` / wrapper `19006` 存活、空输入栏，原布局保留；A 历史前缀保留、B 全文不变。Go 未改，未运行 Go test/build/check 或 doctor，OpenCode 模型调用 0，Claude 无操作，无登录/购买/凭据复制/provider 配置变更，不进入 S2。已有证据哈希对账仅两份授权总文档变化；前轮 wrapper 正常退出自动新增此前不存在的 `resume-exit.json`，无旧证据覆盖，见 [文件保护](S1-codex-no-tui-5ff16448/protected-files-verification.json)、[哈希核验](S1-codex-no-tui-5ff16448/hash-verification.json)。正式回传另存本轮 `handoff.json`，DONE 仅表示调查交付。

## 前轮结果：0cf09894，Codex 原生中断工具语义与手动恢复

协作 session `0cf09894-116f-4abe-b601-93b8572a86bb` / task `s1-codex-interrupt-semantics`，`ralph-loop`。独立证据：[报告](S1-codex-interrupt-0cf09894/README.md)、[runtime-matrix.json](S1-codex-interrupt-0cf09894/runtime-matrix.json)、[dependencies.json](S1-codex-interrupt-0cf09894/dependencies.json)、[原生结果关联](S1-codex-interrupt-0cf09894/codex-verification.json)、[证据断言](S1-codex-interrupt-0cf09894/evidence-validation.json)。证据断言 PASS 不等于功能验收 PASS。

复用授权 window `2B9E10E2-0C0A-47DC-AED7-206B833A4462` / workspace `28A52619-270D-4FC5-A692-C730028054E2` / surface `4E483942-928F-455C-B03D-75DAEDE9F38F`，始终为原 A thread `01a19ad4-2958-75f3-82b6-f488745caec4`。原 Codex PID `96559` / TTY `ttys028`；长时工具 PID / PGID `10800`、PPID `96559`，nonce `S1-interrupt-e70530c3-84ed-4de3-a697-26ad09251dfe`，自然睡眠 240 秒。

一次定向 `ctrl+c` 对应 `07:29:12.658Z` 的真实 native `turn_aborted` / reason=`interrupted`。中断后两次 `ps` 仍查到工具，屏幕明确显示一个后台终端仍在运行：**不能把对话中断视为工具进程已终止**。使用 TUI 提示的原生 `/stop` 后，native CommandExecution 在 `07:30:18.919Z` 记录 failed / exit=-1，独立内核 EVFILT_PROC / NOTE_EXIT 在下一毫秒记录 PID `10800` 退出，多次 `ps` 随后查无进程。见 [按键](S1-codex-interrupt-0cf09894/native-ctrl-c.json)、[清理前仍存活](S1-codex-interrupt-0cf09894/pre-stop-tool-process.json)、[退出事件](S1-codex-interrupt-0cf09894/tool-kernel-exit.json)。未发送外部 OS signal；具体终止信号未证明，内核 data=0 不作为成功退出码。

长工具只启动一次、只有 TOOL_BEGIN，没有 TOOL_COMPLETE、没有原 turn 的 task_complete 或 assistant 的 INTERRUPT_TOOL_DONE。原生包装器 `Script completed` 与工具 `item_completed` 不作为业务完成消息。最终观察越过原 240 秒自然完成期限，没有原工具重放或迟到交付。正常 `/quit` 捕获原 Codex exit=0，再同 surface 手动 resume 原 thread，新 Codex PID `11697` / wrapper `11696` / 原 cwd / `ttys028`。新请求 `S1-resume-once-d0bb72df-547a-4abd-b811-3358e9154b6c` 只有一条原生 user message、一次工具启动/尝试、一个成功结果和唯一 native assistant `RESUME_ONCE_ACK`，同屏可见。模型同时取回恢复请求未重发的中断 nonce 与前轮 A 私有 nonce。见 [恢复屏幕](S1-codex-interrupt-0cf09894/recovery-observed-screen.json)、[原生增量](S1-codex-interrupt-0cf09894/native-transcript-delta.json)。仅证明手动恢复和本次请求一次执行，不泛化为自动恢复或通用 exactly-once。

本轮 `new_runtime_passes=[]`，Ctrl-C 单独终止 yielded 后台工具为明确负向结果；完整 S1 保持 PARTIAL。Go 未修改，未运行 Go test/build/check 或 doctor；OpenCode 模型调用 0，Claude 无操作，无登录/购买/凭据读取复制/provider 配置变更，不进入 S2。A/B 原历史前缀保留，B 全文不变，既有证据与源码/构建定义/原配置哈希核验见 [文件保护](S1-codex-interrupt-0cf09894/protected-files-verification.json)。旧 wrapper 因正常退出仅自动新增此前不存在的 `a-final-exit.json`，未覆盖旧证据。最终 A 保持存活、空输入栏，原布局保留；正式回传另存本轮 `handoff.json`，DONE 表示调查交付完成，不作为 native ACK。

## 前轮结果：754ba5a3，Codex 并发双存活 native TUI 隔离

协作 session `754ba5a3-ca1d-43a7-ba19-b14301cc33f1` / task `s1-codex-concurrent-tui`。独立证据：[报告](S1-codex-concurrent-tui-754ba5a3/README.md)、[runtime-matrix.json](S1-codex-concurrent-tui-754ba5a3/runtime-matrix.json)、[原生交互关联](S1-codex-concurrent-tui-754ba5a3/codex-verification.json)、[证据断言](S1-codex-concurrent-tui-754ba5a3/evidence-validation.json)、[dependencies.json](S1-codex-concurrent-tui-754ba5a3/dependencies.json)。

只在既有授权 Codex window `2B9E10E2-0C0A-47DC-AED7-206B833A4462` / workspace `28A52619-270D-4FC5-A692-C730028054E2` 内新增本轮 B 分屏。A 原 native ID `01a19ad4-2958-75f3-82b6-f488745caec4` / surface `4E483942-928F-455C-B03D-75DAEDE9F38F` / PID `96559` / TTY `ttys028` 全程保留；B 原 native ID `01a19b0a-7662-7582-a5f0-36ea08fb1205` 恢复于新 surface `2C84977B-4E1E-4790-BC80-2065AFF9D323` / PID `4776` / TTY `ttys003`。两个 pane 同时 selected，窗口 visible，同一 `ps` 快照确认双进程存活：[绑定](S1-codex-concurrent-tui-754ba5a3/both-seeded-binding.json)。

两边从对方 cwd 用显式 native ID 入队各自私有 nonce，原生工具默认 cwd 输出分别匹配 A/B 的历史独立路径和当前 surface，native assistant 分别 ACK。再发送不含私有 nonce 的回忆请求，各自只回忆本 thread 的值；完整 native 记录和屏幕均无对方本轮 private/route nonce。新增 `codex.concurrent_live_two_tui_isolation` **PASS，受控并发存活场景**；共享 CODEX_HOME/文件系统不是安全隔离，未宣称并行工具执行或产品适配器已实现。

B 正常 `/quit`，wrapper 捕获退出 0、PID 随后不存在，只关闭本轮新增 surface。关闭后的即时拓扑短暂保留旧布局，后续 [稳定拓扑](S1-codex-concurrent-tui-754ba5a3/final-tree-settled.json) 确认恢复原布局。A 原 PID 仍存活、空输入栏；两个 native 历史均保留。实际证据断言、既有源码/构建定义/原配置/输入非 scratch 证据哈希核对 PASS。Go 未修改，本轮未运行 Go test/build/check；没有登录、购买、输入/复制凭据或 provider 配置变更，OpenCode 调用 0，Claude 无操作，没有向协作 main/worker 发送实验输入。完整 S1 保持 PARTIAL，不进入 S2。正式回传另存本目录 `handoff.json`，不作为 native ACK。

## 前轮结果：fd8f8aac，Codex 双 thread 隔离与执行中恢复

协作 session `fd8f8aac-71a7-4e9e-845e-8cd6b458e468` / task `s1-codex-final-edge`。独立证据：[报告](S1-codex-final-edge-fd8f8aac/README.md)、[runtime-matrix.json](S1-codex-final-edge-fd8f8aac/runtime-matrix.json)、[原始交互索引](S1-codex-final-edge-fd8f8aac/codex-verification.json)、[断言](S1-codex-final-edge-fd8f8aac/evidence-validation.json)、[dependencies.json](S1-codex-final-edge-fd8f8aac/dependencies.json)。

仅使用既有授权 window `2B9E10E2-0C0A-47DC-AED7-206B833A4462` / workspace `28A52619-270D-4FC5-A692-C730028054E2` / surface `4E483942-928F-455C-B03D-75DAEDE9F38F`。原 A thread `01a19ad4-2958-75f3-82b6-f488745caec4`，本轮新 B thread `01a19b0a-7662-7582-a5f0-36ea08fb1205`；在同一可见 surface **顺序运行**。各自真实默认 cwd、私有 nonce、native assistant message 与屏幕吻合。

| 本轮验收 | 实际结果与边界 |
|---|---|
| 双 native thread cwd / 消息 / 上下文隔离 | **PASS，受控顺序运行**：A/B 默认 cwd 各自正确；B cwd 发给暂停 A 的 queue 不进入 B，恢复 A 后才 ACK；A cwd 发给活跃 B 正确 ACK。原生完整记录不含对方本轮私有 nonce 和路由 nonce；私有 nonce 未在恢复请求中重发。并发两个存活 TUI、文件系统安全隔离未证明 |
| 执行中异常退出后继续交付 | **PASS，手动恢复**：B PID `93342` / wrapper `93341` / TTY `ttys028` / resume native ID / cwd 核对后，在有 nonce 的打印和 120 秒睡眠工具仍运行时只向 B PID 发一次 SIGKILL，wrapper 实际 -9。新 PID `95478` resume 同 B，真实 `/bin/pwd`，回忆恢复请求未包含的 B 私有 nonce，并交付 RECOVERY_DELIVERED |

旧睡眠 PID `95169` 一度 PPID 1，之后记录 PID 不存在：[观察](S1-codex-final-edge-fd8f8aac/post-signal-process.json)、[最终核对](S1-codex-final-edge-fd8f8aac/bounded-tool-final-process.json)。被中断工具未交付 MIDFLIGHT_DONE，不据此宣称自动恢复、原工具完成、恰好一次、事务或主机故障恢复。原 A 未收到终止信号。B 正常退出 0 后已恢复 A 原 cwd，真实 `/bin/pwd` 与 ORIGINAL_RESTORED 通过；最终 PID `96559` / wrapper `96558` / TTY `ttys028` / 空输入栏：[最终屏幕](S1-codex-final-edge-fd8f8aac/a-final-status-screen.json)。

新增 `codex.different_cwd_two_native_threads` 和 `codex.sigkill_mid_tool_manual_context_recovery`，保留 16 项历史通过，总计 18 项；完整 S1 仍 PARTIAL，不进入 S2。Go 源码、构建定义、原配置未改，按要求未运行 Go test/build/check；实际证据断言和文件哈希核验 PASS。前轮 wrapper 因 A 正常退出自动新增此前不存在的 `resume-final-exit.json`，无既有证据覆盖：[核验](S1-codex-final-edge-fd8f8aac/protected-files-verification.json)。无登录、购买、输入/复制凭据或 provider 配置变更。OpenCode 新调用 0，Claude 排除，main/worker 未作为实验目标。正式回传在本目录 `handoff.json` 保存，协作 DONE 不作为 native ACK。

## 前轮结果：458ce2e5，OpenCode 本地 provider 只读检查

协作 session `458ce2e5-e679-4fb0-a769-897ce24d33ed` / task `s1-opencode-local-provider`。新增不覆盖证据：[报告](S1-opencode-local-provider-458ce2e5/README.md)、[provider 投影](S1-opencode-local-provider-458ce2e5/providers-retry.json)、[运行矩阵](S1-opencode-local-provider-458ce2e5/runtime-matrix.json)、[依赖](S1-opencode-local-provider-458ce2e5/dependencies.json)。

原隔离 `127.0.0.1:19731` 服务 PID `26680` 与 native session `ses_e65a4cf24ffe3wZAiM2U7og4Px` 仍存在，CLI/server 版本均为 `1.19.5`。`/provider` 的 connected 与 `/config/providers` 只有 `opencode`，列出 8 个免费模型；有效配置没有自定义 provider，内置 `lmstudio` 条目未连接。Ollama/LM Studio/常见本地模型端口 `11434`、`1234`、`8080`、`8000` 拒绝连接；已有 Python 服务 `5000`、`8787` 的 `/v1/models` 均 404。见 [本地端点](S1-opencode-local-provider-458ce2e5/local-endpoints.json)。结论限于原隔离环境和已查端点，不断言其他配置或整台机器没有模型。

原实验 window `CAE5B3FE-3E61-4E92-9896-775B9343687F` 定向查询返回 `Window not found`，原 attach PID `44608` 不存在，见 [目标查询](S1-opencode-local-provider-458ce2e5/target-tree.json)。没有重新创建可见目标、发 nonce 或 reset。本轮模型请求 **0**，没有重试免费额度；`FREE_TIER_LIMIT` 仅沿用前轮事实，未重新验证当前额度。OpenCode 模型 ACK、reset READY、角色执行、实际调用身份、有效 full、模型恢复全部 **UNVERIFIED / WAITING_DEPENDENCY**；HTTP 200、元数据和协作 handoff 均不作为模型 ACK，见 [分层验证](S1-opencode-local-provider-458ce2e5/opencode-verification.json)。

未登录、购买、输入 key、复制凭据或修改配置。Go 源码、构建定义、原隔离配置与输入顶层证据哈希前后对账见 [核验](S1-opencode-local-provider-458ce2e5/protected-files-verification.json)。实际 `go test ./...`、`go build ./...`、`make check` 均退出 0。新增仅为证据记录器和报告；本轮不操作 Codex/Claude，不进入 S2、不创建 worktree/提交。正式回传见 [handoff.json](S1-opencode-local-provider-458ce2e5/handoff.json)，DONE 只表示本轮调查交付完成。

## 前轮结果：eb2e9a87，Codex 边界实测

协作 session `eb2e9a87-d909-47c1-8015-ae54615ace35` / task `s1-codex-edge-cases`。新证据不覆盖目录：[报告](S1-codex-edge-cases-eb2e9a87/README.md)、[runtime-matrix.json](S1-codex-edge-cases-eb2e9a87/runtime-matrix.json)、[逐项断言](S1-codex-edge-cases-eb2e9a87/evidence-validation.json)、[dependencies.json](S1-codex-edge-cases-eb2e9a87/dependencies.json)。复用原登录态，未重新登录、复制凭据或改全局配置。

全程只操作 window `2B9E10E2-0C0A-47DC-AED7-206B833A4462` / workspace `28A52619-270D-4FC5-A692-C730028054E2` / surface `4E483942-928F-455C-B03D-75DAEDE9F38F`，native thread 始终为 `01a19ad4-2958-75f3-82b6-f488745caec4`。原始命令、queue ID、assistant message ID、真实工具输出与同 surface 屏幕由 [Codex 验证](S1-codex-edge-cases-eb2e9a87/codex-verification.json) 关联。

| 受控场景 | 最新实际结果 |
|---|---|
| busy 投递 | **PASS**：40 秒工具运行期间原生 queue 入队；原任务 BUSY_DONE 后，同 thread 才处理消息并 ACK；原生 CommandExecution 区间包含入队时间 |
| 非空草稿 | **PASS，仅 native queue**：两条队列消息 ACK 后，未提交唯一草稿完整保留且不在 native user 消息中；最后只清除自有草稿 |
| 不同发送 cwd | **PASS**：queue 从 B 执行，TUI 仍在 A，显式 thread 路由返回真实 ACK |
| 同 thread cwd A/B | **PASS**：恢复菜单选择一次性 current directory 后，真实工具默认 cwd 从 A 变 B；最终恢复 A。两个独立 native thread 的隔离未测试 |
| 异常退出后恢复 | **PASS，仅空闲 SIGKILL**：PID/TTY/argv 核对后仅 kill 实验 PID 59794，包装器真实退出 -9；同 surface resume 原 ID，模型回忆原 nonce，恢复提示未重发完整 nonce |
| 原生 queue 后台接收 | **PASS，仅存活 TUI**：正文由 Codex queue 投递，原生 assistant 与可见屏幕吻合；没有把 cmux send 或协作 handoff 当作 queue ACK |
| 停止期间 queue 持久化 | **PASS，仅手动恢复后接收**：`queue --resume` 退出 0 同时警告不能 resume；停止期间 rollout 不变，手动 resume 后才出现真实 ACK |

新增 `runtime_passes` 为 `busy_delivery`、`draft_protection_with_nonempty_draft`、`different_sender_cwd_queue_delivery`、`same_thread_cwd_switch_comparison`、`sigkill_idle_context_recovery`、`native_queue_live_tui_background_delivery`、`native_queue_persistence_until_manual_resume`，与前轮 9 项合并保存。通用终端草稿门禁、hook、AgentFlow hook/queue 适配器、无 TUI 自动唤醒、双 thread cwd 隔离、执行中/机器故障恢复继续 **UNVERIFIED**；完整 S1 未通过，不进入 S2。

最终原 thread 已回到原 cwd A，PID `70876` / wrapper `70874` / TTY `ttys028` 存活，输入栏为空，见 [最终 /status](S1-codex-edge-cases-eb2e9a87/final-status-screen.json)、[进程](S1-codex-edge-cases-eb2e9a87/target-process-final.json)、[native 记录](S1-codex-edge-cases-eb2e9a87/native-final.json)。原有包装器因 SIGKILL 自动新增前轮目录的 `codex-resume-exit.json`（-9），复制其结果到新目录，旧证据没有覆盖。

`go test ./...`、`go build ./...`、`make check` 均退出 0；新增的 Python 文件只用于记录/核验实验，没有修改 Go 源码。所有实验 cmux 终端变更命令已逐条核对仅针对授权目标；未向当前 main/worker 或其他项目发实验输入。OpenCode 本轮请求数 0，保留既有 **VERIFIED_TRANSPORT_ONLY / WAITING_DEPENDENCY**；Claude 排除。正式回传 DONE 退出 0，确认 `[DONE] handoff sent`，ID `c97e64db-f03f-486f-b108-ac9488cca97f`，见 [handoff.json](S1-codex-edge-cases-eb2e9a87/handoff.json)；不作为 native ACK。

## 前轮结果：a128f1ae，Codex 手动登录后真实 ACK 与恢复

协作 session `a128f1ae-db2d-4cf5-98c1-ff3a995fdb51` / task `s1-manual-login-retry`。证据：[报告](S1-manual-login-retry-a128f1ae/README.md)、[机器矩阵](S1-manual-login-retry-a128f1ae/runtime-matrix.json)、[逐项断言](S1-manual-login-retry-a128f1ae/evidence-validation.json)。本次授权只为新隔离 Codex 环境启动手动登录界面，OpenCode 无登录；未尝试 Claude。

新建 Codex window `2B9E10E2-0C0A-47DC-AED7-206B833A4462` / workspace `28A52619-270D-4FC5-A692-C730028054E2` / surface `4E483942-928F-455C-B03D-75DAEDE9F38F`。独立 `CODEX_HOME` 位于新证据目录 `scratch/codex/codex`，使用文件凭据存储和最小子进程环境，未复制或读取已有凭据、keychain、cookies，未自动输入凭据。用户确认在本轮目标手动登录完成后，停止认证界面操作，只使用该 surface。

| Codex 受控验收 | 实际结果与证据 |
|---|---|
| 同一可见 native thread / 空闲无草稿 | `/status` 原 thread `01a19ad4-2958-75f3-82b6-f488745caec4`；空输入栏、TTY `ttys028`、PID `56438` 与 surface 绑定；先检查再发送唯一 nonce，见 [门禁](S1-manual-login-retry-a128f1ae/codex-pre-nonce-gate.json) |
| 真实 nonce ACK | 原生 assistant message 返回 `ACK S1-codex-a128-5633e5b0-be0f-4c85-ab21-cc361ec03a35`，同 surface 可见，原生 output tokens=35；见 [rollout](S1-manual-login-retry-a128f1ae/codex-original-native-transcript.json)、[屏幕](S1-manual-login-retry-a128f1ae/codex-after-nonce.json) |
| reset READY / 角色重注入 | `/new` 后 ID 为 `01a19ad6-edd3-7e10-bb5e-0a04a699da32`，模型返回带唯一 nonce 的 `READY role=资深研发`；见 [新 ID](S1-manual-login-retry-a128f1ae/codex-reset-status.json)、[READY](S1-manual-login-retry-a128f1ae/codex-reset-ready-transcript.json) |
| 调用身份 / 有效 full | 新 thread 的实际终端调用返回相同 workspace/surface 与独立 cwd；native `approval_policy=never` / `sandbox_policy=danger-full-access` / profile disabled，并在 cwd 外自有 scratch 实际写入回读 nonce 标记，exit=0；见 [原始调用](S1-manual-login-retry-a128f1ae/codex-identity-transcript.json) |
| 主动退出 / 原 thread 上下文恢复 | `/quit` 退出 0；同 surface 用原独立 CODEX_HOME resume 原 ID，提示中不重发原 nonce，模型正确返回 `RESTORED` + 原 nonce；见 [恢复](S1-manual-login-retry-a128f1ae/codex-restored-native-transcript.json)、[最终屏幕](S1-manual-login-retry-a128f1ae/codex-restored-final-screen.json) |

这些 PASS 限于上述受控场景；full 权限不是全部系统操作的穷举保证，身份由原生记录绑定，不依赖模型自述 ID。busy 投递、非空草稿保护、不同 cwd 对照、崩溃恢复、hook/queue 后台投递继续 UNVERIFIED。实验窗口和恢复原 thread 的 PID `59794` 保持存活；当前没有待用户完成的登录步骤。

OpenCode 保留原独立 native `ses_e65a4cf24ffe3wZAiM2U7og4Px` / surface `FDDB002D-F985-4CB1-AE3C-FB8918AADC5D`。新的 noReply nonce 已获 HTTP 200、native message 回读和可见显示；新模型请求实际返回 `free_tier_limit`，assistant 无文本、output tokens=0。已 abort 此实验重试，最终 session idle；没有登录或切换付费提供商。详见 [OpenCode 分层验证](S1-manual-login-retry-a128f1ae/opencode-verification.json)。OpenCode 模型 ACK、reset READY、角色执行、实际身份、full、模型恢复仍未通过。

本轮 `go test ./...`、`go build ./...`、`make check` 全部退出 0。未修改 Go 源码；协作 main/worker 拓扑前后集合一致，未向其或其他项目 Codex 发实验输入。未进入 S2、创建 worktree 或提交。正式回传 DONE，handoff ID `7e76abb6-a22a-4582-8ba0-dbc251b22c68`，退出 0 且确认 `[DONE] handoff sent`，见 [handoff.json](S1-manual-login-retry-a128f1ae/handoff.json)，不作为 native ACK。

## 前轮结果：871686a3，无登录可见投递与已有 Codex 目标核对

协作 session `871686a3-507a-4757-9a00-f130216ee893` / task `s1-auth-retry`。当轮证据：[报告](S1-auth-retry-871686a3/README.md)、[机器矩阵](S1-auth-retry-871686a3/runtime-matrix.json)、[依赖](S1-auth-retry-871686a3/dependencies.json)。以下为历史范围，已被上方 a128f1ae 的明确手动登录授权与实测结果更新。

用户中途明确停止全部登录、OAuth、设备授权、浏览器认证和 API key 尝试。本轮相关进程已按记录 PID 停止，随后 `ps` 核对全部无匹配；没有输入凭据。见 [停止记录](S1-auth-retry-871686a3/authentication-cancelled.json)、[进程核对](S1-auth-retry-871686a3/auth-processes-after-stop.json)。后续只执行本地 noReply 投递及只读身份检查，不再打开认证入口。

用户确认 **Codex 已登录**。此前 `Not logged in` 仅适用于前轮新建的隔离 CODEX_HOME，不能解释用户已有登录态。只读进程与 cmux TTY 对账发现 **5 个已有 Codex surface**；一个位于受保护 main/worker 协作区，其余属于其他项目，尚未确认原生 thread、空闲/草稿状态和实验归属。当前阻塞是 **NO_CONFIRMED_SAFE_EXISTING_CODEX_TARGET**，不是用户未登录。没有向这些已有会话发文字、按键或 reset，也未复制凭据或另建登录环境。详见 [已有目标映射](S1-auth-retry-871686a3/codex-existing-targets.json)。

指定旧窗口已在 `05:35:01.690Z` 关闭，origin=`appkit_close`，发起者未知；原 surface 不能复用。先保存 [关闭事件](S1-auth-retry-871686a3/previous-target-close.json)，再创建独立替代 window `CAE5B3FE-3E61-4E92-9896-775B9343687F` / workspace `158ADE67-F09E-47D5-9EB9-7B6718BE682E`。保留原隔离 XDG 状态和 OpenCode native ID，没有复制 auth 文件。所有新 helper/child 显式设置 `DISABLE_AUTOUPDATER=1`。

| OpenCode 无登录投递要素 | 实际证据 |
|---|---|
| 原 native session | `ses_e65a4cf24ffe3wZAiM2U7og4Px`；原服务仍存活，客户端以原 ID attach |
| 当前可见 surface | `FDDB002D-F985-4CB1-AE3C-FB8918AADC5D`，位于上述替代 window/workspace |
| 新唯一 nonce | `S1-noauth-visible-ed7b0e7c-ac94-42d3-969e-99bc1835c7f5` |
| 原生命令/结果 | `POST /session/<ID>/message`，`noReply=true`，HTTP 200；native message `msg_19acb5303001aoptO3swe72CNE` |
| 接收证据 | GET 原生消息回读的 session/message/nonce 一致；原始 read-screen 显示该 nonce；随后 session status 为 `{}` |
| 验收界限 | **VERIFIED_TRANSPORT_ONLY**；本地用户消息显示，不是模型 ACK；`runtime_passes=[]` |

逐项断言与原始文件索引：[opencode-transport-verification.json](S1-auth-retry-871686a3/opencode-transport-verification.json)。同 surface 也确实显示恢复前的旧 nonce，但未完成认证后模型上下文恢复、reset READY、角色执行、调用身份或实际 full 权限，因此这些项全部保留 UNVERIFIED。范围更新前的 OpenCode 模型请求仍免费额度耗尽，Claude 同原 native ID 的新 nonce 仍为 synthetic `authentication_failed`；这些历史失败未被计作 ACK。

本轮 `go test ./...`、`go build ./...`、`make check` 全部退出 0，原始输出保存在新目录。项目 Go 源码没有修改；协作 workspace 的布局/pane/surface 集合前后对账一致。没有进入 S2、创建 worktree 或提交。

已按原协作 session 正式回传最新收窄范围的交付结果：`status=DONE`，handoff ID `b00f5eb6-1ff5-4cae-9ef4-9c7df4fb137d`，命令退出 0 并确认 `[DONE] handoff sent`，见 [handoff.json](S1-auth-retry-871686a3/handoff.json)。该状态不代表完整 S1 通过，也不构成 native 模型 ACK。

## 前轮重试：窗口关闭事件与 native session（cbcea350）

协作 session `cbcea350-1e6f-4fd8-b7f6-537fc07bb1b5` / task `s1-runtime-retry`。新增不覆盖证据 [S1-runtime-retry-cbcea350](S1-runtime-retry-cbcea350/README.md)、[机器矩阵](S1-runtime-retry-cbcea350/runtime-matrix.json) 与 [依赖](S1-runtime-retry-cbcea350/dependencies.json)。以下前轮记录保留历史含义，以本节为最新状态。

旧窗口消失已有实际事件链：`03:25:41.100–.111Z` 三个 surface 的 origin=`workspace_teardown`，`03:25:41.119Z` workspace.closed 后紧接 window.closed，窗口 origin=`appkit_close`。见 [关闭时间线](S1-runtime-retry-cbcea350/old-window-close-timeline.json)。**关闭发起者仍未知**，不能断言 TERM 引起，也不再仅描述为查询范围问题。

新实验窗口 `BB9A5102-A080-47B6-98E4-AA2F4445E259`、workspace `BA91FC57-BF8B-4CC8-BC14-C7F138DE8EB0`，保留独立控制 shell，native 退出后包装器落回 `/bin/zsh -f`。两次 OpenCode 客户端停止、重启后，同 UUID 仍可定向查询；[存活证据](S1-runtime-retry-cbcea350/window-survival.json) 跨度 4521.119372 秒（约 75 分钟）。协作 workspace 只读拓扑前后集合一致。

| 工具 | 新 surface UUID | 实际 native ID | 最新接收事实 / 状态 |
|---|---|---|---|
| OpenCode 1.19.5 | `79B69666-1879-4E91-99F6-AFE9F80DB3DD` | `ses_e65a4cf24ffe3wZAiM2U7og4Px` | API 创建 + 原生 attach；唯一 nonce 进入 native 消息并显示，免费额度耗尽，assistant 无文本/零 tokens；WAITING_DEPENDENCY |
| Codex 0.166.0 | `2A3A96DE-DA25-4293-8860-E21EB814A124` | null | 独立 CODEX_HOME 设备登录已尝试，最终 token 请求错误；threads 仍空，nonce 未提交；WAITING_DEPENDENCY |
| Claude 运行中 2.1.284 | `1461550B-80C4-4AE6-9C0C-6DE274017B9E` | `9d2555cf-41c7-4eb8-a957-51124de8ff5d` | 隔离 cwd 信任完成；/status 与 JSONL 核对 ID；nonce 后 synthetic authentication_failed，无模型 ACK；WAITING_DEPENDENCY |

OpenCode 另建 reset ID `ses_e659ec8c6ffeilgo49Sj1TeoqW`，同 surface 重新 attach，保存 noReply 角色消息与 wildcard allow；无模型 READY/实际工具调用。随后以原 ID 重启，API 原消息保留，但最终 UI 未显示原上下文。因此 reset 完成、角色生效、调用身份刷新、有效 full 和恢复仍 UNVERIFIED。Codex/Claude 的这些维度受登录阻塞未执行；Claude 当前记录的 permissionMode=auto 不等于 full。

第三方旁效应必须保留：初次 Claude 显示自动更新完成，后续 `--version` 实测 2.1.316，运行中仍 2.1.284。未主动修改全局配置或复制凭据，但不能声称第三方执行完全未改变全局文件；更新落点未盘点。后续 helpers/child 设置 `DISABLE_AUTOUPDATER=1`，未回滚安装。独立浏览器仅 scratch 状态，无 cookie 导入，安装引擎超时后仍不可用。

工程 `go test ./...`、`go build ./...`、`make check` 均退出 0。项目 Go 源码未改，未创建 worktree/提交，未进入 S2。继续 S1 的依赖与实际命令见新报告；全部 runtime_passes 仍为空。

## 前轮补充：独立可丢弃原生会话实验

协作 session：`b7bf333c-53ad-492e-8196-61f0acabed60`；task：`s1-runtime-evidence`。实验启动时间 `2026-11-02T03:21:14Z`；新证据目录 [S1-runtime-b7bf333c](S1-runtime-b7bf333c/README.md)，机器记录见 [runtime-matrix.json](S1-runtime-b7bf333c/runtime-matrix.json)。协作 session 不是任何工具的 native ID。

按既有 `isolated_session_plan` 使用新的 cwd、XDG、Codex/Claude 配置与临时目录。仅子进程使用这些覆盖，未复制登录凭据。使用底层二进制；Codex 额外指定文件凭据存储，Claude 额外使用 `--bare` 避免 keychain 读取。确切 argv、cwd、环境覆盖、启动时间与 PID 见各工具的 `*-plan.json`、`*-launch.json`、`*-process.json`，实际版本仍为 OpenCode 1.19.5 / Codex 0.166.0 / Claude Code 2.1.284 / cmux 0.65.2。

实际执行 `cmux --json --id-format uuids rpc window.create '{"focus":false}'`，退出 0，得到新窗口 `7B4184EF-AEF6-4A60-9C92-593645DFA10E`；自有 workspace 为 `44AC3631-8A19-42C7-8E06-CD9A2AD4F182`。只在这个新窗口定向创建和操作实验 surface。创建时原焦点不变，创建前后核对见 [isolation-prelaunch.json](S1-runtime-b7bf333c/isolation-prelaunch.json)。

| 工具与实际原生启动命令 | 自有可见 surface UUID | 实际观察与阻塞 | native ID / nonce ACK |
|---|---|---|---|
| `/opt/homebrew/bin/opencode --pure` | `E31395A3-3A26-4155-BF07-9AF017CA7FCB` | 原生首页显示 Build / Big Pickle，隔离 session 表前后均为空；计划转用 serve + attach 前实验窗口消失 | 未建立 / 未发送、无 ACK |
| `/opt/homebrew/bin/codex --no-daemon -c 'cli_auth_credentials_store="file"'` | `C7B13CA5-4331-4025-BAC9-F8B20579DFDA` | 原生登录选择页，隔离 threads 表前后均为空；立即记录 [LOGIN_REQUIRED](S1-runtime-b7bf333c/codex-dependency.json) | 未建立 / 未发送、无 ACK |
| `/Users/funlake/.nvm/versions/node/v26.8.1/bin/claude --safe-mode --bare` | `B46EBE05-9EEC-4413-8DFF-73E923DED334` | 经过主题与安全说明，停在隔离 cwd 的信任页；尚未确认信任，窗口已不可用。没有发现原生会话 JSONL | 未建立 / 未发送、无 ACK |

每个工具均预生成了不同 nonce，具体值保存在 `*-plan.json`；未在 native ID 建立前发送正文，所以 nonce 不构成接收证据。原始屏幕 JSON 保留 cmux 的完整 stdout/base64；对应 `*-decoded.json` 按行解码便于复核。没有执行 queue、原生消息 hook、reset、角色重注入、身份刷新、full 权限或恢复实验；登录与模型额度只对 Codex 观察到登录缺口，不能推断其他工具账号或额度状态。

窗口丢失的精确证据为 [claude-trust-select.json](S1-runtime-b7bf333c/claude-trust-select.json) 与 [opencode-shell-after-stop.json](S1-runtime-b7bf333c/opencode-shell-after-stop.json)：cmux 均退出 **1**、stderr 为 `Error: unavailable: TabManager not available`。随后 `list-windows` 退出 0 且只剩原窗口；指定实验窗口 `tree --window ...` 退出 **1**，返回 `Window not found`，见 [owned-topology-after-unavailable.json](S1-runtime-b7bf333c/owned-topology-after-unavailable.json)。没有将后续操作改投当前协作窗口，也没有重建实验窗口。消失原因不明，不能记作主动退出、崩溃恢复或成功回收测试。

此前为准备切换 OpenCode 启动路径，仅对已核对 argv/PPID 的实验 PID 13125 执行 `/bin/kill -TERM 13125`，命令退出 0；包装器捕获原生退出 **0**，见 [opencode-exit.json](S1-runtime-b7bf333c/opencode-exit.json)。这不是模型 ACK 或故障恢复证据。Codex/Claude 的包装器随窗口消失，真实退出码未捕获，保持 `null`。随后 `ps` 核对全部记录的原生 PID/包装器 PID 均无匹配（退出 1、空输出），见 `native-processes-after-window-loss.json` 与 `wrapper-processes-after-window-loss.json`。

[topology-comparison.json](S1-runtime-b7bf333c/topology-comparison.json) 核对原窗口的全部 workspace、split 布局、pane/surface UUID 集合保持一致；期间原窗口的选中 workspace/焦点发生变化，原始前后值均保留，不能描述成焦点全程不变。本实验没有向原窗口发送实验正文或 focus/select/reset 命令。未运行 S2、创建 worktree、提交或改写需求决策。

当前依赖：独立窗口需保持存活且可定向查询；Codex 需在隔离环境有独立授权登录；OpenCode 需完成实际 API session 创建与可见 attach 绑定；Claude 需继续独立目录信任与认证检查。后续必须先建立 native ID，再发送 nonce 并核对同一可见会话的 agent ACK。全部缺项继续保留 WAITING_DEPENDENCY / UNVERIFIED，S1.1–S1.3 不勾选。

本轮 `go test ./...`、`go build ./...`、`make check` 均退出 **0**，工程回归证据保存在新目录的 `go-test.json`、`go-build.json`、`make-check.json`。没有修改项目 Go 源码；实验记录器只在临时目录执行，最终源码快照保留为 `harness-source.txt`，不是 AgentFlow 新的运行时功能。

本轮已实际执行正式 `agent-handoff.sh send --window 1 --session-id b7bf333c-53ad-492e-8196-61f0acabed60 --status BLOCKED_CONFIRM`，携带上述阻塞、工程验证结果、证据路径与继续 S1 的下一步。命令退出 **0**，确认 `[DONE] handoff sent: id=f3b24a3b-6438-49fc-a774-fcd536557e8d window=1 status=BLOCKED_CONFIRM`。完整 argv/stdout/stderr 见 [handoff.json](S1-runtime-b7bf333c/handoff.json)。这是协作回传，不是三工具 native ACK。

## 前轮安全 probe 交付记录

以下记录属于前轮 `s1-tool-probes`，其版本/帮助/工程检查证据保留，不能替代上面的本轮运行时实验。

日期：2026-11-02。最终本机 probe 时间：`2026-11-02T03:10:22.21866Z`。工作目录：`/Users/funlake/Projects/agentflow`。协作 session：`a1d0a4bf-72aa-4c3f-b532-2e3f8a4c160f`，角色“资深研发”，task `s1-tool-probes`。协作 session id 是本轮路由标识，不作为任何新原生工具 session/thread 的证据。

## 交付与边界

- `internal/probe/command.go`：Go 标准库直接执行固定 argv，逐命令超时、输出上限与实际退出状态；诊断工具缺失、权限拒绝、命令失败、超时及截断。跳过 cmux per-surface shim，避免 probe 触发其会话/配置行为。
- `internal/probe/probe.go`：OpenCode、Codex、Claude 的独立版本/帮助检查；cmux 只读 version/help/ping/capabilities/identify/tree；输出 JSON 能力矩阵和逐命令 JSON 证据。新证据目录限制在当前项目内、拒绝覆盖和已解析父目录的 symlink 越界。
- `internal/cli/probe.go` 与 CLI：新增 `agentflow probe --tool all|opencode|codex|claude|cmux --evidence-dir <新目录>`。0 仅表示基础检查通过，整体始终保留 PARTIAL/UNVERIFIED；`--require-runtime` 返回 1 和 `RUNTIME_UNVERIFIED`。`doctor -f` 仍为 S2 接口，返回 2。
- 子进程使用证据目录内的独立 XDG、Codex/Claude 配置与临时路径，具体覆盖保存在矩阵中；不复制凭据，不写用户全局配置。默认检查不会启动模型、发送消息、重置会话或安装 hook。
- JSON 中每个工具的 `isolated_session_plan` 提供底层工具路径、argv、独立 cwd、环境覆盖与待满足条件。本轮只生成计划，未实际创建或启动独立原生会话。
- `probes/verify/main.go` 和 `make verify-probes PROBE_RUN_ID=<新名称>`：Go 编写的固定验收记录器，捕获真实 argv、环境故障注入、时间、退出码和完整 stdout/stderr，并核对预期诊断。`Makefile` 的格式、vet、测试和构建包含新增 Go 源码。
- 测试覆盖真实子进程失败/权限/超时、argv 无 shell 插值、绕过 surface shim、输出上限、帮助写 stderr、证据不覆盖/路径越界，以及帮助不能升级成运行时通过；CLI 进程测试覆盖缺失、权限拒绝及不可用返回 1 和结构化诊断。
- 同步根 README、文档入口、技术实现文档、实施计划和 [能力矩阵](S1-capability-matrix.md)。S1.1–S1.3 继续不勾选；需求访谈决策文件未修改。

完整 up/assign/send、五张业务表、IPC、运行锁、S2 配置解析与 S3+ 恢复不在本轮实现中。实际独立工具通信仍需后续 S1 工作补齐。

## 实际版本与命令证据

| 工具 | 实际版本 | 实际底层路径 | 版本命令退出码 | 证据 |
|---|---|---|---|---|
| Go | go1.25.5 darwin/arm64 | `/opt/homebrew/Cellar/go/1.25.5/libexec/bin/go` | 0 | [go-version.json](S1-verified/local-probe/go-version.json) |
| OpenCode | 1.19.5 | `/opt/homebrew/bin/opencode` | 0 | [opencode-version.json](S1-verified/local-probe/opencode-version.json) |
| Codex | codex-cli 0.166.0 | `/opt/homebrew/bin/codex` | 0 | [codex-version.json](S1-verified/local-probe/codex-version.json) |
| Claude Code | 2.1.284 | `/Users/funlake/.nvm/versions/node/v26.8.1/bin/claude` | 0 | [claude-version.json](S1-verified/local-probe/claude-version.json) |
| cmux | 0.65.2 (113), 12023d89f | `/Applications/cmux.app/Contents/Resources/bin/cmux` | 0 | [cmux-version.json](S1-verified/local-probe/cmux-version.json) |

OpenCode/Codex/Claude 的命令为 `<tool> --version`，Go 为 `go version`，cmux 为 `cmux version`。完整 help argv 和输出分别记录于 [机器矩阵](S1-verified/local-probe/matrix.json) 与同目录 `<id>.json`；每个实际命令均有独立退出码，未将三个工具的输出混成一个成功结论。

## 最终工程验收

实际执行 `make fmt && make verify-probes PROBE_RUN_ID=verified`，整体退出 **0**。记录器内部逐项结果如下。Go 环境沿用 Make 的 `GOTOOLCHAIN=local`、`CGO_ENABLED=0`、`GOFLAGS=-mod=readonly`，缓存、模块和临时文件位于项目 `.cache/`；与 S0 保持一致。

| 实际命令 | 实际结果 | 退出码 | 完整证据 |
|---|---|---|---|
| `go test ./...` | 全部包测试通过 | 0 | [go-test.json](S1-verified/go-test.json) |
| `go build ./...` | 全部包构建成功 | 0 | [go-build.json](S1-verified/go-build.json) |
| `make check` | 格式、依赖下载/校验、vet、测试与 CLI 构建通过 | 0 | [make-check.json](S1-verified/make-check.json) |
| `go test -count=1 -v ./internal/probe/... ./tests/e2e/...` | 未缓存的 probe 单元/子进程及 CLI 进程测试通过 | 0 | [probe-tests.json](S1-verified/probe-tests.json) |
| `./bin/agentflow probe --tool all --evidence-dir docs/evidence/S1-verified/local-probe --timeout 10s` | baseline_status=PASS，status=PARTIAL，runtime_acceptance=UNVERIFIED | 0 | [local-probe.json](S1-verified/local-probe.json) |
| `./bin/agentflow probe --tool cmux --evidence-dir docs/evidence/S1-verified/runtime-guard --require-runtime` | 基础检查仍 PASS；运行时未验证，明确拒绝验收 | 1（预期） | [runtime-guard.json](S1-verified/runtime-guard.json) |
| `PATH='' ./bin/agentflow probe --tool opencode --evidence-dir docs/evidence/S1-verified/missing-tool` | EXECUTABLE_MISSING，子进程 exit_code=null，结构化诊断 | 1（预期） | [missing-tool.json](S1-verified/missing-tool.json) |
| `CMUX_SOCKET_PATH="$PWD/.cache/s1-no-such-socket.sock" ./bin/agentflow probe --tool cmux --evidence-dir docs/evidence/S1-verified/unavailable-cmux` | 真实 cmux 报 Socket not found；COMMAND_FAILED | 1（预期） | [unavailable-cmux.json](S1-verified/unavailable-cmux.json) |

最后两项的环境覆盖由 Go 记录器直接设置，表格使用等价 shell 表达。CLI 权限拒绝场景通过独立 fixture 的实际二进制进程验证：probe 退出 1、诊断 PERMISSION_DENIED；fixture 失败场景子进程退出 23、probe 退出 1；超时 fixture 被信号终止，记录 -1。具体测试名、退出和诊断见 probe-tests 证据。fixture 仅用于错误处理，不是原生工具真实接收证据。

第一轮实际 probe 发现 OpenCode help 写入 stderr，早期实现只检查 stdout，误报候选能力缺失；已修正、加入回归测试并重新实测。原始初轮证据保留于 [S1-probe-local/matrix.json](S1-probe-local/matrix.json)，中间验收位于 `S1-validation/`，最终以 `S1-verified/` 为准，旧证据没有覆盖。

## 真实会话、接收、重置、权限与恢复

cmux 原始观察见 [cmux-identify.json](S1-verified/local-probe/cmux-identify.json)、[cmux-tree.json](S1-verified/local-probe/cmux-tree.json)；两条实际命令均退出 0。环境 workspace 为 `D9F41024-5C40-4E98-8B34-BD39785B0C81`，tree 列出三个终端 surface，详细 UUID 和前后台差异见能力矩阵。本轮记录的是原始终端拓扑，未为任一工具建立 native session/thread 绑定。

最初终端环境没有 surface/panel；最终通过 Make 启动的命令记录到 environment surface/panel 与 caller 为 `CC1C9123-FA9B-42CD-98C0-4974FD9BC3CF`。最终 focused surface 位于另一工作区。probe 保留观察差异，不把 caller/focused 自动绑定为固定 worker，不变更焦点。这也说明环境身份需要在实际适配器验证时单独确认。

| 验收维度 | 本轮真实证据 | 状态 |
|---|---|---|
| cmux 终端拓扑与 socket | 真实只读查询、明确 UUID、PONG、原始 JSON | PASS（仅前置条件） |
| 三工具原生可见会话与后台一致 | 无独立 native session/thread | UNVERIFIED |
| 空闲接收及 agent ACK | 未向当前 worker/main 发送测试工作消息；无独立接收回执 | UNVERIFIED |
| 新会话、重置完成事件 | 未执行 `/new`、`/clear` 或其他重置 | UNVERIFIED |
| 角色重新注入、实际调用身份刷新 | 无运行时证据 | UNVERIFIED |
| full 权限实际生效 | 仅检查候选参数，未执行权限实验；OpenCode `--auto` 保留显式 deny | UNVERIFIED |
| 原会话恢复、故障恢复 | 仅帮助声明候选恢复路径；未执行恢复 | UNVERIFIED |
| busy、用户草稿、不同 cwd、主动退出与崩溃 | 未在原生会话中运行这些场景 | UNVERIFIED |
| 独立临时会话实验 | 已提供结构化计划；独立终端、native ID、登录/模型/事件通道尚需准备并实测 | WAITING_DEPENDENCY |

Claude hook 未检查安装状态，不能称为已缺失或已可用；本轮未读取登录凭据或调用模型，不能称账号、额度和实际执行权限已通过。Codex queue 帮助本身明确警告 resume 失败仍可能退出 0，所以任何 queue 命令退出码都不能替代接收确认。

## 剩余工作与下一步

本轮最小基础范围交付完成；完整 S1 出口未通过。继续 S1 时使用独立可丢弃的可见会话，记录三工具 native ID、带 nonce 的同会话 ACK、重置身份切换、有效权限和原会话恢复，再覆盖 busy/草稿/不同 cwd/主动退出/崩溃。不得用本轮帮助/版本证据勾选 S1.1–S1.3 或宣称三工具通信通过。

本轮未创建 Git worktree 或提交，未修改需求决策、用户全局工具配置，未重置当前 main/worker；正式协作 READY 与最终 handoff 只用于本轮路由，不充当三工具通信实验。

## 前轮正式回传

已实际执行 `/Users/funlake/Projects/okra-wiki/.claude/skills/okra-agent-init/scripts/agent-handoff.sh send`，参数为 `--window 1 --session-id a1d0a4bf-72aa-4c3f-b532-2e3f8a4c160f --status DONE`，并携带本轮基础 PASS / 完整 S1 PARTIAL 的结果、验证命令与上述证据路径及继续 S1 的下一步。命令退出 **0**，终端确认：

```text
[DONE] handoff sent: id=3e1806d1-cdd0-4e63-bff5-7753bb41969f window=1 status=DONE
```
