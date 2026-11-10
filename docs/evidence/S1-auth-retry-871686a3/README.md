# S1 隔离认证重试：871686a3

协作 session：`871686a3-507a-4757-9a00-f130216ee893`；task：`s1-auth-retry`。

本目录独占写入新证据，不覆盖此前报告的原始命令记录。运行时验收仍需同一 native ID、可见 surface、唯一 nonce 和真实模型 ACK 同时成立。

最终结果：完整 S1 **PARTIAL**，运行时 **WAITING_DEPENDENCY**，`runtime_passes=[]`。完成了无需登录的 OpenCode 原生消息写入、回读和指定 surface 上的唯一 nonce 显示；这是 **VERIFIED_TRANSPORT_ONLY**，没有真实模型 ACK。机器结果见 [runtime-matrix.json](runtime-matrix.json) 和 [dependencies.json](dependencies.json)。

## 当前生效的用户范围

本轮起初获授权继续独立认证；用户随后要求停止一切登录、OAuth、设备授权、浏览器认证与 API key 尝试，再澄清 Codex 已登录，只能核对已有可见 Codex 身份。两次更新见 [scope-update.json](scope-update.json)、[codex-scope-clarification.json](codex-scope-clarification.json)。最新依赖不再要求用户登录或输入 key。

已对核对过 argv 的本轮设备授权、OpenCode auth、Claude 隐藏 key 输入和独立浏览器进程发送 TERM，实际命令/结果保留于 `*-before-stop.json`、`*-stop.json`；[auth-processes-after-stop.json](auth-processes-after-stop.json) 对这些 PID 及下载器/浏览器包装器查询退出 1、空输出，确认无匹配。此前未向认证输入框提供任何凭据。未复制 auth.json、keychain、cookies 或 API secret；未编辑用户全局配置。保留初始登录页、失败记录和源码作为历史，不再运行这些流程。

## 窗口与身份

用户指定的旧 window `BB9A5102-A080-47B6-98E4-AA2F4445E259` 已关闭。定向 tree 真实退出 1，窗口列表不包含它；回读到 `05:35:01.674–.690Z` 的 surface `workspace_teardown` → workspace.closed → window.closed(origin=`appkit_close`) 事件。发起者仍 UNKNOWN。原始记录：[old-window-query.json](old-window-query.json)、[previous-target-close.json](previous-target-close.json)。事件流在 4 秒记录超时后结束，exit_code=null，没有伪造退出 0。

捕获关闭事件后才创建替代独立 window `CAE5B3FE-3E61-4E92-9896-775B9343687F` / workspace `158ADE67-F09E-47D5-9EB9-7B6718BE682E`，控制 shell `E4B5CDF9-8E25-4F08-BB4C-FBF8D837B1CD`。OpenCode 原隔离服务 PID 26680 仍存活，复用其 XDG/cwd 和 native session，不复制凭据。新 OpenCode native surface 是 `FDDB002D-F985-4CB1-AE3C-FB8918AADC5D`；启动 argv、PID、环境覆盖和身份见 [opencode-native-launch.json](opencode-native-launch.json)。所有新 helper/child 显式 `DISABLE_AUTOUPDATER=1`；未回滚前轮 Claude 自动更新。

## 无登录 native nonce 投递

原 native ID：`ses_e65a4cf24ffe3wZAiM2U7og4Px`。新 nonce：`S1-noauth-visible-ed7b0e7c-ac94-42d3-969e-99bc1835c7f5`。

1. 对本地 `http://127.0.0.1:19731/session/<ID>/message` 发起 `POST`，`noReply=true`，明确不请求模型执行；实际 HTTP 200。
2. 返回 native message `msg_19acb5303001aoptO3swe72CNE`；随后 GET 回读匹配 session/message/nonce。
3. 对当前 window/workspace/surface 定向 `cmux read-screen --scrollback --lines 120`，原始屏幕真实显示唯一 nonce。
4. `GET /session/status` 返回 `{}`；该 message 没有对应 assistant 子消息。

自动断言与逐项文件索引：[opencode-transport-verification.json](opencode-transport-verification.json)。原始命令/屏幕/结果全部独立保留。没有把存储成功、API 200、屏幕上的 user 文本或正式 handoff 当模型 ACK；没有增加 runtime_pass。

原 native ID attach 后也实际显示旧 nonce，证明历史消息在本轮可见；reset READY、角色执行、调用身份、实际 full、认证后上下文恢复仍为 UNVERIFIED。

## 已有 Codex 可见目标

用户确认 Codex 已登录。以前两次 `codex-login-status*.json` 的 Not logged in 仅来自独立 disposable CODEX_HOME，不能推断用户默认账号状态。范围更新后没有读取默认 auth store、运行新的登录、创建登录环境或复制凭据。

对两个现存 cmux window 分别执行只读 tree，并将 native `codex` 进程的 PID/TTY/可执行路径对账；不读取进程 argv/环境。找到 5 个 surface 匹配：[codex-existing-targets.json](codex-existing-targets.json)。一个位于受保护的当前 main/worker workspace；其余在其他项目工作区，未确认 native thread、空闲/草稿状态和实验归属。没有读取其聊天屏幕、发按键、发 nonce 或 reset。当前依赖是 **NO_CONFIRMED_SAFE_EXISTING_CODEX_TARGET**，不是用户未登录；继续工作需明确可安全使用的已有 target/native thread。

## 范围更新前的历史认证尝试

OpenCode 对原 ID 提交新 nonce `S1-opencode-auth-e2aa7e17-b573-4270-9ea2-571042845e42`，仍返回 `Free usage exceeded, subscribe to Go`；原生 assistant parts 空、output tokens=0。已 abort 该重试。Claude 2.1.316 以原 native ID `9d2555cf-41c7-4eb8-a957-51124de8ff5d` resume 并提交 nonce，JSONL 与 /status 核对身份，结果仍为 `<synthetic>` / authentication_failed / 零 tokens。均不是 ACK。其后准备的隐藏 key 输入在用户取消后停止，未输入 key。

浏览器引擎安装曾超时且实际启动失败，下载 100% 未作为成功。随后使用系统 Chrome 可执行文件和本目录新的 `scratch/browser-profile-system` 成功访问初始登录页，未导入 cookies。`browser-started-system.json.profile` 的显示字段曾误写为未带后缀的路径；保留原证据，以 [browser-owned-process.json](browser-owned-process.json) 的真实 `--user-data-dir` 和 [browser-profile-correction.json](browser-profile-correction.json) 为准。可选下载器在可用独立浏览器建立后停止；认证浏览器在用户收窄范围后关闭。上述流程只保留历史，不是下一步指令。

## 验证与交付

`go test ./...`、`go build ./...`、`make check` 全部退出 0，见对应 JSON/environment 文件。没有修改项目 Go 源码。临时记录器快照为 `harness-source.txt` 与 `browser-source.txt`；认证取消后记录器拒绝再次执行认证入口。原协作 workspace 的布局、pane、surface 集合前后相同，见 [protected-topology-comparison.json](protected-topology-comparison.json)，焦点前后原始观察仍保留。

最新能力/依赖保留 UNVERIFIED 的项目包括模型 ACK、reset READY、角色执行、实际调用身份、有效 full 和完整恢复。没有进入 S2、创建 worktree/提交、实现 up/assign/send、五张表、IPC 或运行锁。正式回传使用原协作 session，原始命令与 `[DONE] handoff sent` 见本目录 `handoff.json`。

正式回传已成功：`status=DONE`，handoff ID `b00f5eb6-1ff5-4cae-9ef4-9c7df4fb137d`，命令退出 0，确认 `[DONE] handoff sent`。DONE 表示最新收窄范围内的检查与报告交付完成；完整 S1 仍 PARTIAL/WAITING_DEPENDENCY。总报告和能力矩阵位于上一级 `../S1-result.md`、`../S1-capability-matrix.md`。这是协作回传，不是原生模型 ACK。
