# S1 安全能力检查

另有手动执行的 **S4 `real-seat`** 验证器，源码位于 `probes/real-seat`。它仅接受 OpenCode/Codex、显式绝对 cwd/wrapper、window/workspace/surface 和已有 0700 证据目录；须从已授权的独立临时终端启动。它独占创建 provider 子目录，持有 run lock，调用现有 `Start → ForegroundLauncher → _seat`，每个 provider 最多执行一次，保存真实 JSONL 与数据库登记，验证跨进程锁竞争、重复 Start 和重开后的精确 READY。不会登录、复制凭据、改用户配置或自动重试。`go test`/`make check` 不执行这个现场入口。具体命令回执、结果和离线复核见 [S4 真实启动证据](../docs/evidence/s4-real-start-ready-6c5bcfcb-2f9c-45c4-acd1-2409bafa82f7/README.md)。下文的固定只读命令范围只适用于 S1 `probe`。

实现使用 Go 标准库 `os/exec`、`context` 和 `encoding/json`，不增加依赖。入口为 `agentflow probe`；每种工具可独立检查，共享 cmux 只读前置检查。本目录的 `verify` 是固定验收命令的 Go 证据记录器。

## 运行

从项目根目录执行，证据目录的父目录必须存在；每次使用新目录，拒绝覆盖旧证据：

```sh
make build
./bin/agentflow probe --tool opencode --evidence-dir docs/evidence/S1-opencode-new
./bin/agentflow probe --tool codex --evidence-dir docs/evidence/S1-codex-new
./bin/agentflow probe --tool claude --evidence-dir docs/evidence/S1-claude-new
./bin/agentflow probe --tool cmux --evidence-dir docs/evidence/S1-cmux-new
./bin/agentflow probe --tool all --evidence-dir docs/evidence/S1-all-new --timeout 10s
```

每条命令的 stdout 是 JSON，`matrix.json` 保存同一报告，每条底层命令另有 `<id>.json`。证据记录可执行路径、跳过的 cmux shim、argv、cwd、开始时间、耗时、真实退出码、stdout、stderr 和诊断码。未能启动的进程记录 `exit_code: null`；信号终止可记录 `-1`。每条命令的 stdout/stderr 各限制 1 MiB，截断会使检查失败。超时范围为大于 0、不超过 1 分钟，按单条命令计算。

退出码：0 表示本次版本/帮助/cmux 基础检查通过，整个 S1 状态仍为 `PARTIAL`，`runtime_acceptance` 始终为 `UNVERIFIED`；1 表示依赖、执行、证据输出或运行时验收未满足；2 表示参数无效。工具缺失、执行权限拒绝、命令失败、超时、帮助未声明候选能力等都有独立诊断。`--require-runtime` 在当前实现中明确返回 1 和 `RUNTIME_UNVERIFIED`，同时保留真实的 `baseline_status`；没有任何方式仅靠帮助输出将运行时能力标成通过。

`doctor -f ...` 属于 S2，当前仍返回 2。probe 不解析 YAML/JSON 团队配置。

## 执行边界

固定命令集合：Go `version`；工具 `--version`、`--help`；OpenCode `serve/attach/run --help`；Codex `queue/app-server/resume --help`；cmux `version`、`--help`、`ping`、JSON `capabilities/identify/tree`。不会执行原生 `queue`、模型 prompt、重置、hook 安装、分屏创建或恢复操作。

通过 argv 直接启动底层可执行文件，无 shell 插值、无继承终端输入；跳过 PATH 中的 `cmux-cli-shims`，避免 surface shim 注册 hook 或将检查绑定到别的会话。使用每次证据目录内的 `scratch/` 作为子进程 XDG 状态、Codex/Claude 配置和临时目录；覆盖路径记录在 JSON。父进程环境与用户全局配置不被修改，不复制登录凭据。实际第三方可执行文件自身的行为仍受其实现约束；这些检查不是沙箱执行或恶意程序隔离。

cmux 使用显式环境 workspace 查询树，并保存环境身份、caller/focused 的原始观察；不把焦点、标题、短序号或 caller 自动当作 worker 或 native session 绑定。环境没有 workspace 时明确失败，不猜测当前活动工作区。查询拓扑也不证明原生会话收到了消息。

## 独立临时会话后续入口

每个工具的 JSON 包含 `isolated_session_plan`：底层工具的 `launch_argv`、独立工作目录、子进程环境覆盖以及待满足条件。这是可审阅的启动计划，**当前不执行启动，也不是会话验收证据**。新可见终端必须独立于正在工作的 main/worker；实际 cmux workspace/surface UUID 和 native session/thread 必须重新观察，不能沿用本轮协作 session id。

候选交互启动参数为 OpenCode `--pure`、Codex `--no-daemon`、Claude `--safe-mode`。这些参数用于后续隔离实验，不能证明生产 hook、默认 full 权限或后台投递路径有效。使用前创建计划所列 cwd，并核对当前工具版本、独立登录和 hook 需求；不要将这些参数发送为当前 worker 的普通消息。

后续证据至少分别记录：启动前后可见 surface 与 native ID、带唯一 nonce 的空闲接收及 agent ACK、新会话 ID 变化、重置完成信号、角色重新注入、实际调用身份、有效 full 权限、原会话恢复、busy/草稿保护、不同 cwd、主动退出和崩溃。缺项保留 `UNVERIFIED` 或 `WAITING_DEPENDENCY`。OpenCode `--auto` 保留显式 deny，不能单独证明 full；Codex `queue --resume` 帮助明确说明恢复失败仍可能退出 0，必须核对接收端。

### 2026-11-02 独立启动实测补充

本次人工受控实验使用新的 [S1-runtime-b7bf333c](../docs/evidence/S1-runtime-b7bf333c/README.md) 目录和独立 cmux 窗口；三工具确实启动在各自新的原生 terminal surface。`agentflow probe` 自身仍只执行上面的固定安全检查，未新增自动创建窗口、发送或重置功能。

实际启动保持计划中的 `--pure` / `--no-daemon` / `--safe-mode`，额外以 Codex `-c 'cli_auth_credentials_store="file"'` 和 Claude `--bare` 防止复用 OS keychain。子进程只继承最小终端环境，覆盖新的 XDG/Codex/Claude 状态路径；HOME 保持原值，未复制提供商凭据。OpenCode `--pure` 仍实际发现 HOME 中的 skills，不能把它描述成完整配置或文件访问沙箱。

Codex 停在登录选择页；OpenCode 停在无 session 记录的首页；Claude 在独立目录信任确认前实验窗口已消失。定向命令真实返回 `TabManager not available` / `Window not found`（均退出 1），停止后续操作且未回退到协作窗口。三工具 native ID 均未建立，nonce 均未发送，无 ACK；本轮运行时保持 WAITING_DEPENDENCY，重置、角色、身份、full 权限与恢复保持 UNVERIFIED。

`tree --all` 在本机实际仍限于调用者窗口；跨窗口核对需使用 `list-windows` 后对已知 UUID 显式 `tree --window <UUID>`。创建返回 ID 后先对账，不因查询看不到就重复创建。本轮第一次实验记录器因此提前退出 1，修正为显式窗口后仅使用原已创建窗口；失败原文同样保留。

新目录保存完整命令/退出码、启动前后状态、原始屏幕与解码副本、隔离数据库 native 元数据快照、进程退出核对和临时记录器源码。窗口消失原因未确认，不能当作主动关闭或崩溃恢复通过；后续须先确认独立窗口可持续访问，再建立 native ID 并进行同会话 nonce ACK。

## 自动记录验收

```sh
make fmt
make verify-probes PROBE_RUN_ID=another-run
```

Go 记录器在 `docs/evidence/S1-another-run/` 创建新目录，依次运行 `go test ./...`、`go build ./...`、`make check`、未缓存 probe/CLI 测试和本机能力检查，并检查运行时门禁、工具缺失与不可达 cmux 的真实退出码和诊断。保留完整 stdout/stderr；错误场景返回预期非零才通过记录器验收。重复 run id 失败，避免覆盖证据。测试中的 shell 可执行文件仅为错误注入 fixture，不代表真实三工具通信。实际本轮结果见 [S1 报告](../docs/evidence/S1-result.md)。
