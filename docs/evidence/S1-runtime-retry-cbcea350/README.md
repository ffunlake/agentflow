# S1 隔离运行时重试：cbcea350

结论：完整 S1 **PARTIAL**；运行时 **WAITING_DEPENDENCY**；本轮 **没有任何运行时 PASS**。协作 session `cbcea350-1e6f-4fd8-b7f6-537fc07bb1b5` / task `s1-runtime-retry` 仅用于回传。

## 旧窗口为什么消失

旧证据的窗口 `7B4184EF-AEF6-4A60-9C92-593645DFA10E` 确实被关闭，不能归因于 `tree --all` 范围差异。本轮从仍保留的 cmux 事件流读回以下事件，详见 [old-window-close-timeline.json](old-window-close-timeline.json)：

| 时间 UTC | seq | 原始事实 |
|---|---|---|
| 03:25:39.519 | 前轮记录 | 对实验 OpenCode PID 13125 发送 TERM |
| 03:25:39.619 | 前轮记录 | OpenCode 包装器记录退出 0 |
| 03:25:41.100–.111 | 323087–323089 | 三个旧 surface `surface.closed`，origin=`workspace_teardown` |
| 03:25:41.119 | 323090 | 旧 workspace `workspace.closed` |
| 03:25:41.119 | 323091 | 旧窗口 `window.closed`，origin=`appkit_close`，source=`window.lifecycle` |
| 03:25:49.446 | 前轮记录 | 定向按键返回 `TabManager not available` |

这证明 AppKit 窗口关闭伴随 workspace teardown；**关闭发起者仍 UNKNOWN**，不能声称是 OpenCode TERM 导致，也不能声称用户、自动化或 cmux 崩溃导致。原始事件命令保存在 `cmux-historical-{surface,workspace}-closed.json` 和 `cmux-window-closed-events.json`。事件流达到记录器超时后结束，`exit_code=null`；超时前输出的事件完整保留，未伪造成功退出。系统历史日志也保留于 `cmux-historical-log.json`，未提供更具体发起者。

## 新隔离环境与存活

新窗口 `BB9A5102-A080-47B6-98E4-AA2F4445E259`；workspace `BA91FC57-BF8B-4CC8-BC14-C7F138DE8EB0`；专用控制 shell surface `4F2C62EC-C662-4B09-A2A6-3405C81EB7FC`。创建返回 UUID 后立即显式 `tree --window <UUID>` 对账；没有用协作 main/worker 承载实验。

控制 shell 独立保留。每个工具由专用包装器启动，退出后记录退出码并进入 `/bin/zsh -f`，使 native 进程退出不等于该终端生命周期结束。OpenCode 两次受控 TERM 均捕获退出 0；原 surface、workspace、window 继续存在。[window-survival.json](window-survival.json) 记录创建、运行、停止 native 后及最终四次定向观察，观测跨度 **4521.119372 秒（约 75 分钟）**；最终仍 visible。此结论限于本次观测，不能保证人为或 AppKit 关闭不会发生。

所有实验输入都带新 window/workspace/surface UUID。保护协作 workspace 仅做拓扑前后只读对账，布局、pane、surface 集合一致，见 [protected-topology-comparison.json](protected-topology-comparison.json)。未读取协作终端正文、注入实验消息或重置协作会话。

## 三工具实际结果

| 工具 | 可见 surface | 实际 native ID | nonce 与接收结果 |
|---|---|---|---|
| OpenCode 1.19.5 | `79B69666-1879-4E91-99F6-AFE9F80DB3DD` | `ses_e65a4cf24ffe3wZAiM2U7og4Px` | API `prompt_async` 提交唯一 nonce，返回 204；可见客户端出现同一输入；免费额度耗尽，无模型 ACK |
| Codex 0.166.0 | `2A3A96DE-DA25-4293-8860-E21EB814A124` | null；隔离 threads 表仍空 | 选择设备登录并得到授权页；最终 token 请求错误；未提交 nonce，无 ACK |
| Claude Code 运行中 2.1.284 | `1461550B-80C4-4AE6-9C0C-6DE274017B9E` | `9d2555cf-41c7-4eb8-a957-51124de8ff5d` | 目录信任完成，`/status` 观察 native ID 后提交 nonce；JSONL 与 UI 返回未登录，无模型 ACK |

OpenCode nonce：`S1-opencode-937e0e55-b1b6-4684-b227-889eac0bb88b`。Claude nonce：`S1-claude-de161701-9677-443a-b595-c6174c5eac84`。Codex 的未提交 nonce 只保存在 `codex-plan.json`，不算接收。

OpenCode 服务仅监听 `127.0.0.1:19731`，新 XDG/cwd；实际 `POST /session` 返回 ID，原生 `--pure attach ... --session <ID>` 的 PID、argv、终端身份保存在 `opencode-{serve,session-create,launch,process}.json`。`opencode-messages-after-submit.json` 记录 native message ID、session、build/big-pickle 和 cwd；`opencode-screen-after-submit.json` 与 `opencode-status-after-submit.json` 分别记录可见输入及 `Free usage exceeded, subscribe to Go`。assistant 元数据存在但 parts 空、tokens 0，不能算 ACK。已主动 abort 额度重试。

Codex 使用新的 `CODEX_HOME` 和 `cli_auth_credentials_store="file"`，没有复制 auth.json 或已有凭据。`codex-login-status.json` 实际退出 1：`Not logged in`；`codex-screen-final.json` 显示 deviceauth/token 请求错误。设备码仅为本次独立授权流程，未登录成功。独立 `browse` 只使用 `scratch/browser`；没有导入 cookies。浏览器首次缺父目录、随后缺引擎；尝试将引擎安装至 scratch，55 秒超时，最后仍缺可执行文件，完整错误已保留。没有把安装下载 100% 当安装通过。

Claude 使用新 `CLAUDE_CONFIG_DIR`、`--safe-mode --bare`，在授权范围内完成主题、安全说明、隔离 cwd 信任，并选择禁用外部 imports。`/status` 显示 `Auth token: none`；`claude-auth-status.json` 退出 1 且 loggedIn=false。nonce 对应 JSONL 保存在 [claude-native-transcript.json](claude-native-transcript.json)：native ID/cwd 明确，assistant 的 model 为 `<synthetic>`、error 为 `authentication_failed`、tokens 0。`/login` 进入登录方式选择，但未完成认证。`--bare` 帮助明确仅支持独立 API key 或 apiKeyHelper，不读取 OAuth/keychain；后续 OAuth 方案需先验证隔离存储，不能复制已有凭据。

## reset、角色、身份、权限、恢复

| 维度 | 本轮实际观察 | 验收 |
|---|---|---|
| OpenCode reset | API 创建第二个 ID `ses_e659ec8c6ffeilgo49Sj1TeoqW`；同 surface 重新启动 attach；退出 banner 明确显示第二 ID | UNVERIFIED：没有 agent reset READY；不能等价于已验证 `/new` |
| OpenCode 角色重注入 | 第二 session 使用 `noReply=true` 保存独立角色文本；native message ID 已返回 | UNVERIFIED：没有模型执行或 READY |
| OpenCode 实际调用身份 | 第一条请求记录 build、big-pickle、隔离 cwd；服务端未注入或执行 AgentFlow generation 身份 | UNVERIFIED：未刷新工具调用身份 |
| OpenCode full | 第二 session API 接受 wildcard allow；`/agent` 返回的规则仍包含 deny | UNVERIFIED：没有实际工具权限实验 |
| OpenCode 原会话恢复 | 同 surface 再次以原 native ID 启动；API 原消息仍在；最终 UI 未显示原消息 | UNVERIFIED：不能只凭 argv 宣称可见上下文恢复 |
| Claude 身份与权限 | native JSONL 为 permissionMode=auto；模型答复是 synthetic 认证错误 | UNVERIFIED：没有有效 full 或实际模型工具身份 |
| Codex/Claude reset、角色、恢复 | 登录阻塞，未执行 | UNVERIFIED |
| 三工具 busy、草稿、不同 cwd 对照、主动退出与崩溃恢复 | 未完成相关原生验收；TERM 仅为本次隔离客户端切换 | UNVERIFIED |

## 边界、旁效应与下一步

子进程只继承最小环境，不复制 provider credentials；HOME 保持原值，配置/状态重定向不代表文件访问沙箱。OpenCode `--pure` 与 Claude `--safe-mode --bare` 不作为生产 hook 接收验收。没有实现 S2 或 up/assign/send、五张表、IPC、运行锁，没有 worktree 或提交。

**第三方旁效应：**初次 Claude 启动未禁用其自动更新，UI 实际提示 `Update installed · Restart to apply`；随后隔离 `--version` 返回 **2.1.316**，运行中会话仍是 **2.1.284**。没有主动编辑全局配置，但不能宣称第三方运行完全没有影响全局文件；更新落点未盘点。发现后记录器为后续 helpers/child 设置 `DISABLE_AUTOUPDATER=1`。保留版本与屏幕证据，不擅自回滚安装。

`jev-judge` 与 `ochist` 不在 PATH；未伪造风险判断或历史结果。临时人工分阶段记录器快照见 `harness-source.txt`，用于审计，固定路径不可直接重跑。所有证据使用独占创建，未覆盖前轮目录；原生 scratch 由既有 .gitignore 排除。

工程验证 `go test ./...`、`go build ./...`、`make check` 均退出 0，见对应 JSON 与 environment 文件。未修改项目 Go 源码。

下一步继续 S1：恢复 OpenCode 可用额度或在隔离环境独立认证；完成 Codex 独立登录并建立 thread；为 Claude 提供与隔离认证策略兼容的独立登录。依赖恢复后必须提交新 nonce，并取得同 native ID、同可见 surface 的实际模型 ACK，再验证 reset READY、角色/调用身份刷新、有效 full 与原会话恢复。详情见 [runtime-matrix.json](runtime-matrix.json)、[dependencies.json](dependencies.json)。当前实验窗口和三个 surface 保留，以 UUID 查询；原生客户端仍可能在等待输入。
