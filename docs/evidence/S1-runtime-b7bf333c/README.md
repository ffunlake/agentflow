# S1 独立原生会话实验（b7bf333c）

本目录为 2026-11-02 `s1-runtime-evidence` 的新证据目录，不覆盖 S1-verified。
协作 session `b7bf333c-53ad-492e-8196-61f0acabed60` 仅用于任务路由，不是工具原生 ID。

最终状态：**WAITING_DEPENDENCY**；完整 S1：**PARTIAL**。三工具确实进入了独立窗口里的原生界面，但没有建立 native session/thread ID，没有提交 nonce，没有 agent ACK；运行时验收未通过。

自有窗口 `7B4184EF-AEF6-4A60-9C92-593645DFA10E`，workspace `44AC3631-8A19-42C7-8E06-CD9A2AD4F182`。窗口由 `cmux --json --id-format uuids rpc window.create '{"focus":false}'` 创建，退出 0；创建前后原窗口拓扑及当时焦点一致。后续窗口从 `list-windows` 消失，定向操作真实退出 1，原因未知。没有重新创建窗口或向原 main/worker 投递实验消息。

| 工具 | surface UUID | native ID / nonce ACK | 最后观察 |
|---|---|---|---|
| OpenCode 1.19.5 | `E31395A3-3A26-4155-BF07-9AF017CA7FCB` | null / 无 | Build / Big Pickle 首页；隔离 session 表空 |
| Codex 0.166.0 | `C7B13CA5-4331-4025-BAC9-F8B20579DFDA` | null / 无 | 登录选择页；隔离 threads 表空 |
| Claude Code 2.1.284 | `B46EBE05-9EEC-4413-8DFF-73E923DED334` | null / 无 | 隔离 cwd 信任页，未确认 |

精确原生 argv 分别为 `/opt/homebrew/bin/opencode --pure`、`/opt/homebrew/bin/codex --no-daemon -c 'cli_auth_credentials_store="file"'`、`/Users/funlake/.nvm/versions/node/v26.8.1/bin/claude --safe-mode --bare`。新的 cwd 和环境覆盖见每工具的 `*-plan.json` 与 `*-launch.json`。HOME 沿用原值；没有复制凭据或设置 provider key；Codex 文件凭据模式及 Claude `--bare` 用于避免 keychain 复用。OpenCode `--pure` 仍读取 HOME 中的 skills（见日志），不意味着完全配置隔离。

| 证据 | 内容 |
|---|---|
| [runtime-matrix.json](runtime-matrix.json) | 实际启动 argv/cwd/终端身份/PID；native ID、nonce 提交与 ACK 都明确为空或 false |
| [dependencies.json](dependencies.json) | 每工具阻塞、未执行维度与下一步 |
| `*-version.json` / `*-help.json` | 实际版本和帮助原始流、真实退出码；不作为接收证据 |
| [window-create.json](window-create.json)、[owned-tree-after-create.json](owned-tree-after-create.json) | 新窗口创建返回 UUID 与该窗口下独立 workspace/surface |
| [isolation-prelaunch.json](isolation-prelaunch.json) | 创建前后原有结构及当时焦点一致 |
| `*-launch.json` / `*-process.json` | 真正启动的底层二进制 argv、cwd、环境覆盖、surface 和 PID |
| `*-screen-*.json` / `*-screen-*-decoded.json` | 仅自有 surface 的完整原始屏幕 JSON/base64 与按行解码副本 |
| `opencode-native-state-{before,after}.json`、`codex-native-state-{before,after}.json` | 对隔离 SQLite 只读查询：native 表前后均无记录 |
| [claude-native-inspection.json](claude-native-inspection.json) | 隔离目录下 JSONL 检索结果为空；不是预分配会话 ID |
| `opencode-log-{before,after}.json` | 隔离工具日志按行保留；没有 model prompt 或 session 创建 |
| [claude-trust-select.json](claude-trust-select.json)、[opencode-shell-after-stop.json](opencode-shell-after-stop.json) | 定向 cmux 调用退出 1，`TabManager not available` |
| [window-list-after-unavailable.json](window-list-after-unavailable.json)、[owned-topology-after-unavailable.json](owned-topology-after-unavailable.json) | 实验窗口不在列表；定向 tree 退出 1，`Window not found` |
| [opencode-initial-process-check.json](opencode-initial-process-check.json)、[opencode-initial-stop.json](opencode-initial-stop.json)、[opencode-exit.json](opencode-exit.json) | 核对自有 PID 后 TERM；kill 退出 0，包装器捕获工具退出 0 |
| [native-processes-after-window-loss.json](native-processes-after-window-loss.json)、[wrapper-processes-after-window-loss.json](wrapper-processes-after-window-loss.json) | 记录的 native/wrapper PID 均不存在：ps 退出 1、空输出；Codex/Claude 原生退出码未捕获，保留 null |
| [topology-comparison.json](topology-comparison.json) | 保护窗口的 workspace/split/pane/surface UUID 结构前后一致；期间焦点选择有变化，原因未定 |
| [launch-orchestrator-first.json](launch-orchestrator-first.json)、[launch-orchestrator-retry.json](launch-orchestrator-retry.json) | 首次临时记录器因 `tree --all` 仍限于 caller window 而在启动前退出 1；显式 `--window` 核对原已创建窗口后才启动，没有重复创建 |
| [harness-source.txt](harness-source.txt) | 本轮临时记录器最终源码快照；固定路径、人工分阶段调用，仅作审阅记录，不是新增 AgentFlow 功能或可直接复跑入口 |
| [go-test.json](go-test.json)、[go-build.json](go-build.json)、[make-check.json](make-check.json) | `go test ./...`、`go build ./...`、`make check` 均退出 0；每条命令有对应 `*-environment.json` |

`scratch/` 由根 `.gitignore` 排除，保留本机原生缓存、配置和 SQLite 原件；可审阅命令、屏幕、元数据与日志另存于本目录顶层。记录器使用独占创建写证据，不覆盖前轮 S1-verified；不要直接重跑固定路径的快照脚本。

没有执行 OpenCode serve/attach 或 API 创建会话：在准备切换前实验窗口已不可用。没有执行任何工具的 reset、角色重注入、调用身份刷新、有效 full 权限或原会话恢复；busy/草稿/不同 cwd/主动退出/崩溃也未验收。原生进程/包装器均已消失，但窗口消失原因不明，不能当成主动回收或崩溃恢复通过。

本轮未运行 `jev-judge`：PATH 无该可执行文件（`risk-preflight.json` 保留事实），没有伪造风险判断。没有创建 worktree、Git 提交、修改需求决策或运行 S2。

后续继续 S1：先明确独立窗口保持存活的条件及消失原因，准备隔离登录，再建立真实 native ID 和同一可见会话 ACK；原有三工具未满足维度继续保留 UNVERIFIED。

正式协作回传已完成，见 [handoff.json](handoff.json)：命令退出 0，终端确认 `[DONE] handoff sent: id=f3b24a3b-6438-49fc-a774-fcd536557e8d window=1 status=BLOCKED_CONFIRM`。该确认仅表示证据与阻塞已回传，不计入任何 native ACK。
