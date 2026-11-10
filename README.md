# AgentFlow

**CMUX workflow transport v1（2026-11-06）：** 默认 real `up` 使用一次 `cmux new-workspace --layout` 创建新 workspace，支持 columns、rows、nested 二叉布局及比例，按 agent 的 `tool` 启动 Codex/OpenCode/Claude Code TUI。当前 `examples/team.yaml` / `team.json` 的 builder 和 reviewer 为 OpenCode persistent 会话；协调器绑定已登记 surface 的原生 session，用 `cmux send --paste` 与 `send-key enter` 投递，再用 OpenCode 自带的定向只读 DB 查询收取结构化结果，避免导出完整长会话，不调用旧的 cmux agent inbox。主席位可在同一 run 的一次工作流完成后启动下一任务。本地 fake cmux/原生会话测试已完成；真实完整任务流仍需现场验收。其它工具组合沿用旧的 cmux agent message/inbox 路径，其 hooks 未验收。详见[安装与使用说明](how-to-use.md)和[旧通道证据](docs/evidence/cmux-workflow-transport-v1-result.md)。

Workflow 提供三种类型：`build_review`（执行后独立审查）、`approval`（按步骤指定审批人，支持驳回到前置步骤）和 `pipeline`（按 `main -> role1 -> ... -> main` 顺序交接）。示例与边界见 [使用说明](how-to-use.md#工作流类型)。

```sh
make build
./bin/agentflow up -f examples/team.yaml --run cmux-demo --json
# JSON 配置同样适用；每次使用新的 run ID/目录。
./bin/agentflow up -f examples/team.json --run cmux-json-demo --window window:N --json
```

real `up` 未指定 `--run-dir` 时，在 `os.TempDir()` 下按 run ID 稳定定位私有目录，cmux 与三个 headless 模式均适用；重复执行同一 run ID 会命中原目录并拒绝重复启动，不会创建第二个 workspace。临时根目录由 Go 按 Windows/macOS/Linux 的系统规则选择，不使用 XDG/HOME 拼接路径；目录名包含安全 run ID 前缀和稳定摘要。JSON 的 `run_dir`（文本的 `Run directory`）返回实际绝对规范路径。第二个终端及重开已有 run 时仍建议显式传入同一 `--run-dir`。显式 `--run-dir` 保持原行为，已有目录仍须满足 runtime 的私有目录约束（Unix 0700、无 symlink）。分配失败返回 `UP_DIRECTORY_INVALID`，不回退到其他位置。交给 runtime 前失败只清理本次新建的空目录；交接后成功、失败或正常退出均保留目录和证据，不自动删除运行中目录。系统可能清理临时目录，需长期保留时请显式选择持久目录。

可用 `--cmux-binary /absolute/cmux` 和 `--codex-binary` / `--opencode-binary` / `--claude-binary` 分别覆盖可执行文件；单工具布局还支持 `--native-binary`。所有 cwd 必须存在。layout 的 shell command 只包含经过引用的 `_tui-seat` 入口、快照路径、agent ID 和 binary 引用；角色/prompt/model 从私有冻结快照读取，以 argv 传给工具。TUI args 仅支持 Codex 的 `--profile NAME` 和 Claude 的精确 `--bare` / `--safe-mode`，OpenCode TUI 不接受额外 args。session policy 保留为元数据，尚未实现 resume/reset。cmux 失败或结果不明会保留 attempt，重复 up 拒绝重放；`--workspace` / `--surface` 已有目标参数会被拒绝。以下显式 headless 模式继续使用原有路径。

```sh
# up 退出后，为其已有 run 启动前台 transport coordinator。
RUN_DIR="<复制 up 输出的 run_dir 绝对路径>"
./bin/agentflow coordinator --run cmux-demo --run-dir "$RUN_DIR" --cmux-transport --json
# 在另一个终端投递；body-file 必须已存在，recipient 使用配置中的 logical agent。
./bin/agentflow cmux-send builder --run cmux-demo --run-dir "$RUN_DIR" --agent main --generation 1 \
  --recipient-generation 1 --request-id message-1 --body-file next.md --json
./bin/agentflow cmux-inbox --run cmux-demo --run-dir "$RUN_DIR" --agent builder --generation 1 --json
./bin/agentflow status --run cmux-demo --run-dir "$RUN_DIR" --json
```

`CMUX_QUEUED` 仅确认 native 消息存储回执匹配目标和正文；接收、执行、READY 与 workflow 完成仍为 UNVERIFIED。发送使用参数数组 `cmux agent message <surface-UUID> --from agentflow --json -- <text>`；正文中的引号、换行与 `--help` 均作为文本。复用现有 actor/run/agent/generation、stop、binding recovery 和 sender/request 门禁；发送失败/超时/错目标保留 raw stdout/stderr/exit，持久 intent 禁止重放。相同 request ID 只返回原凭据，改变正文或 recipient 拒绝。Inbox 只读调用 `agent inbox --surface <UUID> --limit 100 --json`，通过显式事件入口保留 ID、来源/目标、正文和 raw evidence。历史已接受 ID 的 source（名称与 surface/workspace）、target、body 及接收 agent/generation 一致时幂等跳过，仅在同一事务内追加新 ID；重开后混合 `[old,new]` 与 `[new,old]` 语义相同。历史内容/身份冲突、同批重复 ID、未知新来源及错 surface 均拒绝整批；写入失败整体回滚。native state/raw 元数据变化不产生新事件，首次凭据保留；返回的 `messages` 仍包含本次读取的历史与新消息。查询不 mark-read、不自动 ACK、不转换 READY/DONE；cmux 窗口中的显示名不作为逻辑身份。正文最多 32 KiB，单独 `-` 及不可表示的控制字符拒绝；进程 stdout/stderr 分别最多 64 KiB，超出视为 UNVERIFIED。见 [批处理幂等修复证据](docs/evidence/cmux-agent-message-transport-v1-fix-result.md)。

已有 cmux run 的冻结配置含 workflow 时，可在上述前台 coordinator 运行期间执行：

```sh
./bin/agentflow workflow start --run cmux-demo --run-dir "$RUN_DIR" --agent main --generation 1 \
  --revision 0 --request-id workflow-start-1 --json
# 使用 start 返回的 workflow_current.revision；drain 在前台等待结构化回传。
./bin/agentflow workflow drain --run cmux-demo --run-dir "$RUN_DIR" --agent main --generation 1 \
  --revision CURRENT_REVISION --request-id workflow-drain-1 --json
```

首次启动需选含 workflow 的配置，例如 `examples/workflow.yaml` 或 `examples/approval.yaml`；`up` 创建 workspace 并启动前台 coordinator，只有显式提供 `--task` 才发出首个 workflow start。无 `--task` 时等待主席位收到用户明确任务。编辑源文件不会改变已创建 run 的冻结配置。无 workflow 的配置仍只创建 workspace；需要临时 transport 的旧 run 可用 `--cmux-transport`。`start/resume` 只投递新 outbox，历史请求不重发；cmux 模式的一次 `workflow drain` 在 CLI 内每秒检查 lead 的登记 surface inbox，收到回传后推进并派发下一阶段，直到完成、暂停、耗尽、超时或 main 需要处理自己的审批才返回。`awaiting_actor=true` 时，main 回复后用最新 revision 继续 drain。消息正文为 `workflow_reply` JSON，必须带 actor/run/generation、request_id、message_id、workflow revision/kind 和对应终态 status；native source 名称必须为 logical agent ID，source/target UUID 必须匹配登记绑定。普通消息及 `cmux-inbox` 不推进 workflow，直接 IPC workflow reply 在 cmux 模式下拒绝。完整格式、暂停恢复及未知发送处理见 [使用说明](how-to-use.md#cmux-workflow-前台协作)。

**Headless Claude Code v1（2026-11-04）：** 显式 `up --headless-claude` 与 `coordinator --headless-claude` 复用共享 Headless/coordinator/outbox/workflow 内核。Claude Code 2.1.318 默认使用严格的前台 `--print --bare --verbose --output-format stream-json` 与 `--resume UUID`；只有 agent 的 `args: ["--safe-mode"]` 明确 opt-in 才使用 non-bare 模式复用官方 CLI 已有 OAuth。选择随 resolved config/startup snapshot 冻结。完整 init、主 assistant 和终结 result 共同验证 session/READY/结构化结果。仅支持同工具 persistent、generation 1；fixture 已覆盖成功、失败、未知、严格 JSONL、重开、幂等、stop 和代际。此前 attempt-2 的真实验收是显式 non-bare OAuth argv，不代表安全默认；见 [认证兼容证据](docs/evidence/claude-oauth-headless-compat-20261104.md) 和 [默认值修复与 DB 复算证据](docs/evidence/claude-oauth-headless-default-fix-20261104.md)。有效权限仍 UNVERIFIED。三个 headless 工具标志互斥。

**Headless OpenCode v1（2026-11-04）：** 显式 `up --headless-opencode` 与 `coordinator --headless-opencode` 复用相同 headless 内核、coordinator、outbox 与 workflow 门禁。OpenCode 使用 `run --format json` / `--session ID`；READY、session identity 和最终 step 文本来自完整 JSONL。仅支持同工具 persistent、generation 1，fixture 全程不启动模型。本轮真实模型验证未执行，详见 [OpenCode 交付证据](docs/evidence/headless-opencode-v1-result.md)。

**Headless Codex v1（2026-11-04）：** 显式 `up --headless-codex` 在前台初始化同工具的 persistent 逻辑角色，使用真实 Codex `exec --json` READY 与 `exec resume` 投递；`workflow start|resume|drain` 复用既有 outbox、动作领取和 workflow journal，同步采集结构化结果。重开用 `coordinator --headless-codex`，随后显式 drain；未知提交不重发。此模式不调用 cmux，配置的 `terminal/layout` 仅保留为既有 schema 的逻辑角色声明。fixture 与真实验证边界、限制见 [交付证据](docs/evidence/headless-codex-v1-result.md)。默认不带该标志的 coordinator 保持原行为。

**v1 门禁修复（2026-11-04）：** 原生提交领取与 seat 占用同事务保存；缺少匹配的持久 SENT 时，ACK/结果不能覆盖未知提交，后续 assignment 保持排队。drain 每次保存结果后刷新 cursor，第 128 次进入完成、暂停或耗尽时直接返回最新状态。定向回归与本轮验证见 [修复证据](docs/evidence/headless-codex-v1-fix-result.md)。

```sh
make build
# 前台运行；示例只做无工具 nonce 执行与审查。使用已登录的真实 Codex。
./bin/agentflow up -f examples/headless-codex.yaml --headless-codex \
  --run codex-demo --run-dir /absolute/private/new-run --json
# 另一个终端显式开始 workflow；其余事件由该 headless adapter 采集。
./bin/agentflow workflow start --run codex-demo --run-dir /absolute/private/new-run \
  --agent main --generation 1 --revision 0 --request-id start-1 --json
# 上述 coordinator 正常退出后，重开已有 run（不重新启动 native/不自动 drain）。
./bin/agentflow coordinator --headless-codex --run codex-demo \
  --run-dir /absolute/private/new-run --json
# 查询当前 revision；该操作不触发工具调用。
./bin/agentflow workflow status --run codex-demo --run-dir /absolute/private/new-run \
  --agent main --generation 1 --request-id status-1 --json
# 使用当前 workflow revision；示例完成时为 3。重放 start-1 返回原始凭据。
./bin/agentflow workflow drain --run codex-demo --run-dir /absolute/private/new-run \
  --agent main --generation 1 --revision 3 --request-id drain-1 --json
```

OpenCode 使用相同 workflow 命令，将上述配置换成 `examples/headless-opencode.yaml`，启动与重开标志换成 `--headless-opencode`，并使用独立的新 run ID/目录。暂停后用 `workflow resume --revision <当前 revision> --reason <决策> --request-id <稳定 ID>`，其余身份参数与 start 相同。

Claude 使用 `examples/headless-claude.yaml` 和 `--headless-claude`，同样使用独立的新 run ID/目录。示例：

```sh
./bin/agentflow up -f examples/headless-claude.yaml --headless-claude \
  --run claude-demo --run-dir /absolute/private/new-claude-run --json
# coordinator 正常关闭、释放锁后，显式重开；随后按上例查询 status/drain。
./bin/agentflow coordinator --headless-claude --run claude-demo \
  --run-dir /absolute/private/new-claude-run --json
```

Claude v1 的 headless/native 默认严格使用 `--bare`，不读取 OAuth/keychain，只支持已有环境中的 API key/helper 或云 provider 认证。需要复用官方 CLI 管理的同一 OS 用户已有 OAuth 时，须在启动前对每个相应 Claude agent 设置 `args: ["--safe-mode"]`；例如 `agents.main.args: ["--safe-mode"]` 和 `agents.builder.args: ["--safe-mode"]`。AgentFlow 不登录、读取或复制凭据。safe mode 禁用自定义 hooks/plugins/CLAUDE.md 等扩展，CLI 内置插件仍可能出现在 init 中。两种模式都显式加入 `--strict-mcp-config --setting-sources "" --disable-slash-commands --no-chrome`。空 args 或精确的 `["--bare"]` 均选择 bare；只有精确的 `["--safe-mode"]` 选择 non-bare。args 已包含在 resolved config/Digest、startup snapshot 和 DB config 中，resume/coordinator 正常重开使用冻结选择，编辑原配置不会切换已有 run，不自动回退。其余 raw args、provider 参数或在其他工具上使用此 opt-in 均拒绝。`--permission-prompts none` 让需提示的调用直接被拒绝；`permissions=full` 才添加 `--dangerously-skip-permissions`，实际权限效果仍 UNVERIFIED。未知 native 事件或终止原因保留 UNKNOWN。`apiKeySource=none` 也可能出现在成功 OAuth 请求中，不能单独据此判断认证失败或选择认证模式。

这里的重开指 coordinator **正常关闭并释放 run lock 后**重新打开已有 run；它与强制退出、崩溃后遗留锁的恢复不等价。当前不会自动删除、替换或接管 stale lock；已有锁（含过期或损坏锁）仍阻止启动并返回 `ALREADY_RUNNING`，需要人工核对。正常重开 fixture 通过不代表已验证强制退出恢复。OpenCode 严格字段解析与本轮验收见 [修复证据](docs/evidence/headless-opencode-v1-fix-result.md)。

Headless 支持 `--native-binary /absolute/codex`、`/absolute/opencode` 或 `/absolute/claude`（自动查找跳过 cmux shims）、`--turn-timeout`（默认 85s，最多 30m）和 up 的 `--startup-timeout`（headless 默认 5m，最多 30m）。workflow CLI 的 `--timeout` 默认 30m；IPC 超时可能隐藏已提交结果，应复用原 request ID 查询原始凭据，再用 `workflow status` 查询当前 revision 并显式推进。`workflow.cursor` 是该事件的原始凭据，`workflow_current` 是本次执行后游标；审查批准仅代表 agent 对每条 criterion 的显式判断，证据内容不自动验证。未知/格式错误结果返回诊断，不能解释为完成。一般开发任务可修改示例 instruction/acceptance/cwd；真正文件修改权限由原生工具配置与 `permissions` 决定。

**Workflow：** YAML/JSON 可选 `type: build_review`、`approval` 或 `pipeline`。Approval 可逐步指定不同审批席位，并把驳回退回到当前或更早的处理步骤；pipeline 用动态长度 `main -> role1 -> ... -> main` 串行交接。角色从 `agents`/`layout` 引用，三类工作流共用持久 cursor、outbox 和幂等门禁。前台 runner/CLI 与 native adapter 的验证范围见[使用说明](how-to-use.md#工作流类型)和 [loop-core 契约](internal/loopcore/README.md)。

**历史单 seat 真实 up（2026-11-03）：** 单 OpenCode foreground 握手保留为显式 `coordinator.Up` Native API 与 `ForegroundLauncher`；默认 CLI 已由上方 cmux launcher 替代。它初始化私有 snapshot/context，校验完整 native JSONL 和 exact actor/generation/native ID/nonce READY，保存 seat 与 journal 凭据，再持锁提供 IPC。历史现场证据见 [真实 up 证据](docs/evidence/s4-real-up-single-seat-3117cc8e/README.md)。READY 是一次已退出的握手，能力仍为 PARTIAL。

**新增 S5 thin coordinator（2026-11-03）：** `agentflow coordinator --run ID [--run-dir DIR] [--json]` 为已有 run 提供单实例前台协调进程。assign/send/inbox/ack/reply/stop 与 reconcile/resume 统一经本地 IPC，CLI 不再直接写数据库；stop 现在要求 `--agent/--generation/--request-id`。本地单实例、IPC、幂等和重开验证 PASS，见 [结果](docs/evidence/s5-thin-coordinator-result.md)。原生 reset/submit、完整团队 up 和进程级 attach/stop 仍为 PARTIAL/UNIMPLEMENTED。使用与身份/重试边界见 [coordinator 契约](internal/coordinator/README.md)。

**交付状态（2026-11-03）：S7.3 代码侧本地故障矩阵 PASS；产品整体 PARTIAL，尚未完成 v1 真实三工具验收。** 新增 30 个表驱动场景，结合 S0–S7.2 回归，验证错误诊断、正文持久化、事务回滚、幂等/代际保护、未知动作不重放与 stop 不复活；全量 test/build/check/tidy 和核心包 race 通过。真实 native/cmux 三工具 e2e、权限生效、现场 attach/stop 与真实恢复保持 PARTIAL/UNVERIFIED。交付能力、风险、未完成项和复现命令见 [S7.3 最终报告](docs/evidence/S7.3-result.md)。

AgentFlow 计划通过 cmux 中的原生终端会话组织 OpenCode、Codex 和 Claude Code 协作。目前已实现 S0 工程基础、S1 安全能力检查基础、S2 配置/布局/静态 CLI、S3 存储/IPC/运行锁基础，以及 **S4.1 启动计划、S4.2 最小启动注册 API（fake launcher 验证）、S4.3 status/attach 查询与 stop intent、S4.4 隐藏 seat 校验入口与 cmux 命令纯函数、S5.1 消息保存/查询/确认 API 与 CLI、S5.2 fresh/persistent 重置协调（fake adapter 验证）、S5.3 持久化对账与恢复决策、S6.1 按 run/agent 隔离的版本化文件上下文、S6.2 独立报告与主 agent 精简合并 API、S6.3 文件会话 Bootstrap/Rebuild API 与 CLI**，使用用户指定的 Go；S1 真实通信、S4 真实启动/cmux attach/stop、S5 真实投递和 S6 真实会话注入仍为 PARTIAL/UNVERIFIED，阶段与需求见 [文档入口](docs/README.md)。

## 环境与构建

- Go 1.25 或更高版本；本轮实际验证 Go 1.25.5、macOS arm64。
- Make（用于统一工程入口）。首次下载依赖需要访问 Go module proxy。
- SQLite 使用固定版本的 `modernc.org/sqlite`，纯 Go 实现；构建默认 `CGO_ENABLED=0`，无需 Node、npm 或系统 SQLite 开发库。
- `go.mod` 声明 module `agentflow`（尚未指定远程仓库地址），`go.sum` 固定直接和间接依赖的校验和。

在项目根目录执行：

```sh
make deps
make check
./bin/agentflow --help
./bin/agentflow --version
```

`make build VERSION=0.1.0` 可注入版本；默认版本为 `0.0.0-dev`。构建产物为 `bin/agentflow`。`make check` 检查格式、依赖校验、`go vet`、全部测试和 CLI 构建。另有 `make fmt`、`make fmt-check`、`make test`、`make test-unit`、`make test-integration`、`make test-e2e`。

Make 将 Go 缓存、依赖下载和临时文件保存到项目内 `.cache/`，禁用自动下载 Go 工具链，并以 `-mod=readonly` 检测未更新的依赖声明。直接使用 Go 时可沿用同样的环境：

```sh
export GOTOOLCHAIN=local CGO_ENABLED=0 GOFLAGS=-mod=readonly
export GOCACHE="$PWD/.cache/go-build" GOMODCACHE="$PWD/.cache/go-mod"
export GOPATH="$PWD/.cache/gopath" GOTMPDIR="$PWD/.cache/tmp" TMPDIR="$PWD/.cache/tmp"
make prepare
go test ./...
go build ./...
```

仅使用标准 Go 默认环境时，项目根目录的 `go test ./...`、`go build ./...` 同样适用；Go 将按本机配置选择缓存目录。

## 当前行为与验证范围

CLI 支持无参数帮助、`-h`、`--help`、`--version`、`probe`、`validate/plan/doctor`、S4.1 partial 的 `up --dry-run`、S4.3 最小 `status/attach/stop`、S5.1 `assign/send/inbox/ack/reply`（含 `message ack` 别名）、S5.3 `reconcile/resume` 决策查询和 S6.3 `rebuild/bootstrap` 文件恢复包。成功输出到 stdout；诊断以 `agentflow: error:` 写入 stderr。参数错误退出 2，配置、布局、工具诊断或输出失败退出 1，成功退出 0。全局帮助参数必须单独使用。例如 `./bin/agentflow --unknown` 返回 2。

配置命令共用严格加载、默认值解析和布局编译，不创建团队或启动进程：

```sh
./bin/agentflow validate -f examples/team.yaml
./bin/agentflow plan -f examples/team.json --json
./bin/agentflow plan -f examples/team.yaml --tool claude
./bin/agentflow doctor -f examples/team.yaml --json
./bin/agentflow up -f examples/team.yaml --dry-run --json
```

`validate` 成功时明确输出配置与布局有效。`plan` 默认文本展示绝对配置路径、context_dir、lead、每个角色的 tool/role/permissions/session/cwd/prompt 摘要和编译布局；JSON 的 `source_path` 是来源元数据，`config` 包含完整规范化快照及摘要，`layout` 包含编译树。等价 YAML/JSON 的 `config` 和 `layout` 相同。`--tool` 仅覆盖默认工具，优先级为 agent.tool → CLI --tool → defaults.tool → opencode。

`doctor` 只通过 PATH 查找最终计划所需工具的可执行文件，不运行版本、帮助或运行时探针。文本和 JSON 分别报告配置、布局及工具查找状态；JSON 字段包括 `source_path/status/configuration/layout/tools/runtime_communication/diagnostics`，工具与角色列表按名称排序。工具缺失返回 1，包含 `EXECUTABLE_MISSING` 与 `tools.<tool>.executable` 字段路径；配置错误包含源文件和字段路径。`plan --json` 和 `doctor --json` 在配置错误时也向 stdout 输出该结构的失败报告，同时向 stderr 写诊断；参数错误仅写 stderr。`FOUND` 和整体 `OK` 仅表示静态检查成功，运行时通信始终为 `UNVERIFIED`，不证明权限实际生效、工具接收消息或会话重置。

`up --dry-run [--tool ...] [--json]` 输出 `StartupPlan`，状态为 `PLANNED`、`executed=false`，包含冻结配置、按 agent ID 排序的 seat、布局路径和独立 wrapper binary/argv。默认 real `up -f <config> --run ID [--window REF] [--run-dir DIR] [--json]` 使用本文开头的 cmux launcher，以视觉叶子顺序返回每个 agent 的 surface，能力为 PARTIAL/UNVERIFIED。`_seat` 保留一次性握手契约；显式 headless 路径提供原有 coordinator/workflow 行为。详见 [coordinator 契约](internal/coordinator/README.md) 和 [runtime README](internal/runtime/README.md)。

S4.2 的 `runtime.Start` 接受现有启动计划、run ID、私有目录中的绝对快照路径、已迁移的 store 和最小 `Launcher` 接口。它写入 0600 运行快照，按计划稳定顺序传递 wrapper/cwd/seat 元数据，等待匹配 READY 后才登记当前 seat；run/agent/generation、工具和 native session/workspace/surface 不匹配、重复 READY 或重复绑定均拒绝。配置正文不进入 wrapper argv；冻结配置保存在快照与 runs 中，接受的 native 身份和 READY 状态保存到 seats。部分失败保留已确认结果并停止后续启动，不自动重试。快照模型、注册拒绝路径、并发重复、取消、部分失败和 SQLite 重开已通过 fake launcher 测试；OpenCode/Codex 真实单 seat adapter 与 READY 登记现已通过下文的现场验证，完整团队 launcher、READY IPC 接线及三工具通信仍待实现或验证。S4.2 局部验证为 PASS，S4 整体为 PARTIAL，见 [S4.2 结果](docs/evidence/S4.2-result.md)。

S4.4 的隐藏入口 `_seat --snapshot <absolute> --agent <id>` 保持严格参数与私有初始快照校验。`ForegroundLauncher` 复用 `Launcher`、`BuildCMUXCommand` 和注册 API，在单个所属终端中以独立 argv/cwd 启动 wrapper。wrapper 需要 `AGENTFLOW_NATIVE_OPTIONS` 中的显式 window/workspace/surface、绝对 native/cmux binary，并核对 cmux tree 的真实 caller；目标错误时不启动工具。OpenCode 使用 `run --format json`，Codex 使用 `exec --json`，角色正文和一次性 READY challenge 经 stdin 输入。native JSONL 提供 session/thread ID，只有完成的 assistant 响应匹配 run/agent/generation/tool/nonce 后才输出 READY。无显式目标返回 `SEAT_NATIVE_TARGET_REQUIRED`；Claude 保持 `SEAT_TOOL_ADAPTER_UNIMPLEMENTED`。**OpenCode/Codex 各一次真实单 seat JSONL → `_seat` → `Start` → 持久 READY、run lock 竞争及重复启动保护已 PASS**，见 [真实启动证据](docs/evidence/s4-real-start-ready-6c5bcfcb-2f9c-45c4-acd1-2409bafa82f7/README.md)。完整团队启动仍为 PARTIAL；此前指定 TUI 的 [nonce ACK 证据](docs/evidence/s4-real-opencode-codex-83a87513-a6ab-47fe-b23d-1d5e466569a6/README.md) 独立保留。

`runtime.BuildCMUXCommand(plan, agentID, target)` 保留为 foreground adapter 使用的旧 topology 纯函数。新的 `BuildCMUXWorkspaceCommand` / `LaunchCMUXWorkspace` 用一次完整 layout 请求创建拓扑，`ShellCommand` 逐字引用所有入口参数；只读 `tree --workspace <返回目标>` 核对拓扑与 UUID/ref，位置证据不会转为 native session/READY。见 [layout launcher 结果](docs/evidence/cmux-layout-launcher-v1-result.md)。

S4.3 对已有运行目录提供以下入口；使用新默认临时目录的 run 必须传 `--run-dir <up 输出的 run_dir>`。为兼容历史 run，省略目录时仍只查找 `$XDG_STATE_HOME/agentflow/runs/<run-id>`，未设置 XDG 时查找 `~/.local/state/agentflow/runs/<run-id>`；不会创建、扫描或猜测临时目录。文件约定为 `state.db`、`config.resolved.json` 和 `coordinator.lock`；socket 路径只取匹配 run 的锁中已记录值。

```sh
./bin/agentflow status --run <run-id> --json
./bin/agentflow attach --run <run-id> --run-dir /absolute/existing/run --json
./bin/agentflow coordinator --run <run-id> --json
# 在另一终端中请求停止意图；coordinator 保持运行。
./bin/agentflow stop --run <run-id> --agent main --generation 1 --request-id stop-1 --json
```

`status` 用只读数据库连接读取同一事务内的 run 和当前 seats，组合冻结配置、不可变启动快照、锁、PID 与最多 250ms socket 连通性诊断。输出 `run.status/startup_phase/startup_progress/stop_requested/workspace_id`、按 agent ID 排序的 `seats`（含当前 generation/native）、`snapshot`、`lock`、`diagnostics`。`startup_phase` 保留数据库原值；进度 STARTING/PARTIAL/REGISTERED 仅表示注册记录，`run.status=STOP_REQUESTED` 仅表示停止意图。整体 `status=PARTIAL` 和 `runtime_communication=UNVERIFIED` 始终保留，不证明进程存活、有效权限或真实通信。快照损坏/缺失时保留数据库可读内容并退出 1；没有锁时报告 MISSING，读取完整的数据库状态仍可退出 0。查询不创建/迁移数据库或启动工具，也不重读源配置/提示文件。

`attach` 是定位查询：返回 `run_id/workspace_id/socket_path`，workspace 可由唯一一致的持久 seat 身份推导；冲突时不选取。真实 cmux workspace 存在性与 attach **尚未验证**，始终 `status=PARTIAL`、`attached=false`、`runtime_communication=UNVERIFIED`、退出 1。没有 workspace 时提供 `WORKSPACE_UNVERIFIED` 与 `run.workspace_id`。`stop` 经 coordinator IPC 在事务内设置 `runs.stop_requested` 并追加一次 STOP_REQUESTED journal，同时保存共享幂等命名空间中的请求凭据；重复请求返回相同 `journal_seq/recorded_at`，`executed=false`，成功退出 0。进程关闭和 cmux stop 返回结构化 `PROCESS_STOP_UNIMPLEMENTED`，能力为 PARTIAL。损坏快照不妨碍记录意图；缺失或错误的锁会阻断 IPC，不回退直写。历史只读状态诊断见 [S4.3 结果](docs/evidence/S4.3-result.md)，当前命令契约见 [coordinator](internal/coordinator/README.md)。

S5 消息命令经持锁的 coordinator 操作已有 `state.db`，临时 run 传入 up 返回的 `--run-dir`，省略时仅沿用历史 XDG 目录查找；所有调用显式提供已注册 caller 的 `--run/--agent/--generation`，写入还须提供稳定 `--request-id`。当前不自动推断身份或接受无 seat 的外部 sender。示例要求库中已有匹配 run 和当前代际 seats，并已启动前台 coordinator；`up --dry-run` 不会创建它们：

```sh
./bin/agentflow assign builder --run <run-id> --agent main --generation 1 --request-id work-1 --body-file next.md --json
./bin/agentflow assign builder --run <run-id> --agent main --generation 1 --request-id work-2 --session persistent --body-file next.md --json
./bin/agentflow send builder --run <run-id> --agent main --generation 1 --request-id supplement-1 --body-file supplement.md --json
./bin/agentflow inbox --run <run-id> --agent builder --generation 1 --pending --after 0 --json
./bin/agentflow message ack <message-id> --run <run-id> --agent builder --generation 1 --request-id receipt-1 --json
./bin/agentflow reply <message-id> --run <run-id> --agent builder --generation 1 --request-id result-1 --status DONE --body-file result.md --json
```

`assign` 保存新工作派发意图，`send` 保存当前工作补充，`reply` 保存关联原消息的结果，recipient 自动取原发送者并限定原发送代际。`send/assign` 可加 `--recipient-generation N` 核对目标，缺省在事务内选择当前代际。正文逐字保存在消息中；同 sender/request_id、同正文返回原消息和原路由/报告字段，异正文返回 `IDEMPOTENCY_CONFLICT`。ACK 与消息共用 request 命名空间；相同 ACK 请求重试无额外写入，不同 request_id 再次确认同一消息只保存请求凭据，始终返回首次确认的 `journal_seq/recorded_at`。一个 ACK request_id 不能确认另一条消息，也不能与同 sender 的 send/reply request_id 混用。

`send/reply` 新消息输出 `status=STORED`、`delivery.phase=PENDING`、`delivery.channel=UNVERIFIED`、`attempt=0`、`reset_phase=NONE`，成功仅表示事务已提交。`inbox` 输出 `status=QUERIED` 与按 seq 排序的当前目标代际记录；空列表为 `[]`，`--pending` 排除已确认消息。显式 `ack`/`message ack` 将 message 与 delivery 原子标为 `ACKED` 并追加一次 `MESSAGE_ACKED`，只确认接收；reply 的 `--status` 是原样保存的报告，不代表系统验收或自动确认原消息。未知 run/agent、旧或未来 generation、越权 ACK/reply 均拒绝；写入失败完整回滚。消息 JSON 失败报告含固定 code/path 诊断，同时写 stderr；退出码遵循上述约定。API 与证据见 [runtime README](internal/runtime/README.md)、[S5.1 结果](docs/evidence/S5.1-result.md)。

S5.2 的 `assign` 默认使用运行库中冻结的接收者 `session`，可用 `--session fresh|persistent` 覆盖，`--mode` 为同义参数；同时提供时必须一致。首次保存的 mode、正文和路由决定幂等重试的行为，重试改变 mode 不会再重置。persistent 沿用当前 native 绑定；fresh 先检查 seat 工作状态、原生 busy、非空草稿（含空白）和未完成输入，未知检查结果也排队。未执行原生动作的排队项可通过同一 assign 请求或 runtime `Advance` 重新检查，并按保存顺序推进。

`SessionCoordinator` 接受可注入的 `ResetAdapter` 和 `SubmitAdapter`。fresh 在事务提交 `RESET_REQUESTED` 后调用 reset，adapter 重新注入冻结角色正文、prompt_file 来源路径、cwd 和按 run 隔离的 context 路径，再等待独立 READY。新 native session/thread 必须不同于旧值及历史绑定，workspace/surface 保持原目标；READY 的 run/agent/generation/tool/native 全部匹配后，才原子推进 generation、切换 current seat 和 delivery 目标。正文投递另以 `SUBMIT_REQUESTED`、`attempt=1` 开始，同时将 seat 标记 BUSY 并绑定当前消息；adapter 确认后为 `SENT`，ACK 仍需显式回传。原生提交开始后的 ACK/结果要求匹配的持久 SENT；ACK 只确认接收，与该工作关联的 DONE/FAILED/BLOCKED_CONFIRM 报告才释放 seat，报告本身仍只是 agent 声明。旧代际 READY/ACK/结果和未 READY 的 fresh ACK/结果被拒绝。

当前 coordinator 没有安装原生 adapter：assign 保存后返回 `STORED/QUEUED`，`record.session` 展示模式，`record.coordination` 为 `RESET_ADAPTER_UNIMPLEMENTED` 或 `SUBMIT_ADAPTER_UNIMPLEMENTED`，保持 `reset_phase=NONE`、`attempt=0`；退出 0 表示已保存并排队。消息结果仍为 `runtime_communication=UNVERIFIED`、`capability_status=PARTIAL`。没有可用 coordinator 时返回结构化失败，不直接写库。reset 或 submit 中断、响应未知、READY/投递证据不符时保留请求阶段；重开数据库和重复请求均不会再次 reset 或重发正文。普通 send 仅保存补充消息，不触发会话 adapter。会话协调见 [S5.2 结果](docs/evidence/S5.2-result.md)。

S5.3 提供最小的持久化对账与恢复决策查询，显式使用原派发者的当前身份：

```sh
./bin/agentflow reconcile <message-id> --run <run-id> --agent main --generation 1 --json
./bin/agentflow resume <message-id> --run <run-id> --run-dir /absolute/existing/run --agent main --generation 1 --json
```

两条命令语义相同，不需要 CLI request ID；经 coordinator IPC 在已有数据库事务内读取 delivery/reset/submit、journal、当前 seat generation/native 及 stop intent，保存 `SESSION_RECONCILED` 决策。输出 `status=RECONCILED`、`executed=false`，成功退出 0 只表示决策查询和持久化完成。`decision` 包含当前绑定、delivery、`state/reason/next_action`、`stop_requested` 与证据/决策 journal 序号及时间；相同证据重查、跨进程与数据库重开返回相同决策凭据。

| decision.state | 含义 |
|---|---|
| `SAFE_TO_CONTINUE` | 持久账本允许继续；`next_action=RESET|SUBMIT` 是建议，仍需 runtime 重新检查输入并独占领取动作门禁 |
| `QUEUED` | 停止意图、忙碌、队列前项、草稿或 adapter 等阻塞原因保留，`next_action=NONE` |
| `UNKNOWN_NEEDS_RECONCILE` | 原生动作请求已保存但结果未知，或 delivery/journal 不一致；必须补齐可信证据，禁止自动重发 |
| `REJECTED_OLD_GENERATION` | 已开始动作或未排队的 delivery 指向非当前代际，禁止重新定位和执行 |
| `SENT` / `ACKED` | 有匹配的持久提交/接收凭据，保留已确认状态，禁止重复执行 |

`Advance` 和重复 assign 在原生动作前先对账。只有 `QUEUED/reset_phase=NONE/attempt=0` 且 journal 没有原生动作证据的协调派发可以重新定位当前 generation；绑定移动和 `SESSION_RELOCATED`/对账决策原子提交。已确认 READY 的 fresh 只允许后续提交正文，不能再次 reset；`RESET_REQUESTED/SUBMIT_REQUESTED` 即使跨进程也保持未知，不重复 `/new` 或提交正文。迟到的历史代际 ACK/reply/reset/submit 确认返回身份错误并幂等追加 `SESSION_RESULT_REJECTED`，不保存报告正文。`resume` 本身不调用 adapter，也不自动补证或调度重试；S7.1 已增加独立的有限退避 API，真实接线与自动调度仍待实施。见 [S5.3 结果](docs/evidence/S5.3-result.md)。

S6.1 提供 `runtime.CreateContext(absoluteSnapshotPath, runID)` 与独立的 `internal/context` API，在 `<context_dir>/<run_id>/<agent_id>/<revision>/` 保存角色协议、必要背景、决策、进度和未完成事项。主 agent 使用自己的绑定 API 更新精简摘要；每次使用新 revision，五个 0600 文件经临时文件和无覆盖原子发布后，最后提交包含路径、用途、SHA-256 与更新时间的 manifest。所有受管理目录为 0700；读写拒绝相对/穿越路径、symlink、权限不符、身份错配、未知字段、损坏和重复发布。角色正文引用冻结快照，S5 ACK/报告语义保存在角色协议中，实时消息与代际仍由 store 管理。双 run/双 agent 集成测试验证独立更新和重新打开读取，原始 prompt_file 删除后仍可创建上下文。详细契约、限制和示例见 [context README](internal/context/README.md)、[S6.1 结果](docs/evidence/S6.1-result.md) 与 [实施计划](docs/plans/2026-11-03-s6.1-context-plan.md)。真实工具注入仍待后续接入。

S6.2 增加 `runtime.NewReportService(db, contextRun)` 的 PublishEvidence/Publish/Read/Review/Merge API。执行者把 DONE/FAILED/BLOCKED_CONFIRM 报告写入私有 `.reports/g-<generation>/<report_id>/`，正文只含精简 summary、next 与已校验证据引用；证据保存为同 agent/generation 下的不可变 0600 文件。报告绑定 run/agent/generation，可选 message_id/request_id 必须对应原始 lead 派发；request_id 指原派发请求。SQLite 事务在文件读写期间固定当前身份，拒绝旧代际、错误 author/消息关联、越界路径和缺失/损坏证据；重复报告或 request 明确 ErrExists。冻结 lead 才能 Review/Merge，显式选取最多四份报告合并为自己的新 revision，保留状态、摘要、next、证据路径/SHA 和源报告摘要校验，不复制证据、派发或提示正文。`INTEGRITY_VERIFIED/agent_claim` 仅表示完整性校验通过，DONE 不转成 PASS；S5 状态仍由现有消息 API 管理。双 run/独立数据库及新测试进程发布/合并已覆盖，无新增 CLI 或依赖。契约与边界见 [context README](internal/context/README.md)、[S6.2 结果](docs/evidence/S6.2-result.md) 和 [S6.2 实施计划](docs/plans/2026-11-03-s6.2-report-plan.md)。

S6.3 的 `runtime.NewBootstrapService(db, contextRun)` 提供语义相同的 `Bootstrap(ctx, ipc.Actor)` 与 `Rebuild(ctx, ipc.Actor)`。同一 store 事务覆盖当前 run/agent/generation、完整冻结配置、context 路径/成员/lead、seat 工具、角色协议和报告派发绑定核对。读取所有可见 revision 并按 manifest 更新时间选择最新，时间相同时按 revision 名字排序；任何损坏/未提交 revision 都拒绝，不回退。执行者恢复自己的上下文及当前代际最新报告，主 agent 恢复自己的最新精简摘要并重新验证 Merge 引用的报告和证据。源 prompt、聊天及证据正文不复制。bootstrap 返回 role/cwd/permissions/session policy、progress/pending、报告/证据文件引用、持久 seat 身份、恢复建议及 next；fresh 建议新会话，persistent 建议核实旧会话、不可用时从文件继续，均须真实适配器独立 READY。

```sh
./bin/agentflow rebuild --run <run-id> --agent main --generation <current> --run-dir /absolute/existing/run --json
./bin/agentflow bootstrap --run <run-id> --agent builder --generation <current> --json
```

CLI 使用已有 `state.db` 中冻结的 context_dir，默认运行目录沿用 XDG 规则，不要求旧 native session 可用。成功退出 0 只表示生成或读回 `.bootstrap/g-N/<content-sha256>.json`，状态 `BOOTSTRAP_READY`、`executed=false`、`runtime_communication=UNVERIFIED`；重复/跨进程请求复用相同文件，缓存损坏拒绝。不会变更 S5 seat/generation/message/ACK 或旧报告/revision。角色正文仍通过冻结快照字段引用，实际注入方须确保引用可读；本 API 可用 store 内冻结配置验证协议，不读取原始 prompt_file。上下文外的普通证据引用保持不打开，受管理上下文的跨 run/agent 引用拒绝，只有主 agent 的已校验 Merge 报告引用例外。测试和边界见 [S6.3 结果](docs/evidence/S6.3-result.md)、[实施计划](docs/plans/2026-11-03-s6.3-bootstrap-plan.md)。S6 两轮真实协作及 S7 自动恢复保持待实现/未验证。

S7.1 增加最小 `runtime.RecoveryRunner` / `RecoveryPolicy` API，使用已有 journal 和 delivery.next_retry_at 保存恢复计划。`NewRecoveryRunner(coordinator, policy, clock, dependency)` 注入有限退避表、时钟和依赖探测；`Recover(ctx, actor, messageID)` 每次显式调用只计划等待或领取一次到期尝试，首个调用不执行动作。默认最多 3 次尝试，依次等待 1 秒、5 秒、30 秒；首次策略跨数据库重开保持不变。`RetryDecision` 返回分类、原因、已用次数、下一次尝试时间、当前对账证据和 journal 凭据。等待期内重复调用复用凭据，次数耗尽为 `EXHAUSTED`，保留原派发供后续核对。

只有依赖探测明确 `CanContinue=true`，并重新通过当前绑定、输入检查、stop intent、恢复领取记录与 S5 原生动作事务门禁，才最多执行一次 reset 或正文提交；fresh 已确认 READY 后须另一个到期调用提交正文。未知 native 结果进入 `MANUAL_RECONCILE`，旧代际、权限/配置错误、主动 stop 与 context 取消停止推进；取消后的日志使用最多 5 秒的独立 context 落盘。崩溃遗留的 `ATTEMPT_RESERVED` 标为需对账，后续调用不会夺取或执行。不自动 ACK，不改变工具/模型/账号，不创建后台服务、任务表或依赖。缺少真实 adapter/依赖探测仍为 `UNIMPLEMENTED/UNVERIFIED`；现有 `reconcile/resume` CLI 继续仅查询对账决策，Bootstrap/Rebuild 继续仅生成文件恢复包。局部实现与 fake 测试见 [S7.1 结果](docs/evidence/S7.1-result.md)、[实施计划](docs/plans/2026-11-03-s7.1-recovery-plan.md)。

S7.2 增加 `NewSessionRecovery(coordinator, adapter, policy, clock, bootstrap)`，以 `Recover(ctx, caller, target, requestID)` 显式恢复逻辑 run/agent/generation；caller 为当前 lead 或本人，尚无任何 lead seat 的部分启动允许冻结 lead 的 generation 1 协调身份。复用 S7.1 的有限策略和 S5.3 的账本对账，首个 tick 保存 WAITING，到期后独占检查原 native session/thread；原会话可用且位置未变时保持绑定及 generation，即使角色配置为 fresh 也不创建新会话。workspace/surface/cmux ID 改变时，须同 native session、同 generation 以及匹配 run/agent/tool/权限/冻结配置摘要的独立 READY 才重绑定；历史位置追加保存在 journal。原会话明确不可用时，先验证 S6.3 检查点并提交 CREATE_REQUESTED，再由 adapter 创建和等待新 READY，最后原子推进下一 generation，历史 generation 保留。

`SessionRecovery.NoteExit` 校验完整当前绑定；用户关闭/已确认终态为持久门禁，未知原因记录 `EXIT_UNVERIFIED` 并进入人工对账。过期事件、丢失启动响应、未知 reset/submit/create/READY 结果均不推断为可重建。`Start` 在 Launch 前保存 LAUNCH_REQUESTED，返回 native 后保存 LAUNCH_BOUND；因此 READY 前中断可以检查原会话并补齐原 generation 注册，丢失 Launch 响应则不重复创建。恢复凭据、首次预算、退避、历史绑定跨数据库重开保持；新 request ID 不能增加同 generation 预算或抢占未决领取。S5/S7.1 动作及迟到启动 READY 也检查 stop/关闭/恢复门禁。原生事件接入方须先认证，再调用 `ValidateBinding` 核对完整位置身份；仅凭 actor 无法区分同代际的旧位置。

S7.2 提供注入式 API 和 fake adapter/clock 验证，未启动真实 opencode/codex/claude/cmux、未新增 CLI/依赖。恢复 API 不投递正文、不 reset、不 ACK，沿用冻结工具、模型及账号参数。S7.3 已完成本地故障矩阵与最终工程检查；真实恢复出口仍未通过。详见 [S7.2 结果](docs/evidence/S7.2-result.md)、[实施计划](docs/plans/2026-11-03-s7.2-session-recovery-plan.md)、[S7.3 最终报告](docs/evidence/S7.3-result.md) 和 [runtime 契约](internal/runtime/README.md)。S7 整体保持 PARTIAL。

本地故障矩阵可单独复现，全部使用 fake adapter、临时 SQLite、临时 context 与测试 Unix socket：

```sh
go test -v ./internal/runtime ./internal/store ./internal/ipc -run TestFaultMatrix -count=1
CGO_ENABLED=1 go test -race ./internal/runtime ./internal/store ./internal/context ./internal/ipc ./internal/lock -count=1
go mod tidy -diff
```

race 检查需要本机可用的 C 编译器；产品构建仍可 `CGO_ENABLED=0`。`tests/e2e` 验证 AgentFlow CLI 子进程及工具替身，不能当作原生三工具 e2e。未知 reset/submit/create/READY 保留人工对账门禁；预算耗尽保留原工作，恢复由显式 API tick 驱动。文件权限与 SHA 校验验证同一 OS 用户下的存储契约，不提供跨用户沙箱或原生 full 权限证明。

S1 能力检查可以分别选择 `opencode`、`codex`、`claude`、`cmux`，或一次选择 `all`。使用新证据目录：

```sh
./bin/agentflow probe --tool all --evidence-dir docs/evidence/S1-my-probe
```

stdout 与证据目录中的 `matrix.json` 输出结构化矩阵，逐条命令记录真实退出码和原始输出。0 仅表示版本/帮助及 cmux 前置检查通过；实际消息接收、重置、full 权限和恢复仍为 `UNVERIFIED`。使用 `--require-runtime` 会明确返回 1。工具缺失、权限拒绝或 cmux 不可达返回非零和结构化诊断。独立 probe、会话实验计划与证据记录器用法见 [probes/README.md](probes/README.md)。

SQLite 自检仅作为工程测试：在测试临时目录创建诊断表 `s0_selfcheck`，在显式事务中提交随机标记，关闭所有连接后通过新的数据库句柄读回。集成测试执行两轮，并另开独立连接核对磁盘文件，避免旧标记掩盖写入失败。运行：

```sh
make test-integration
```

`tests/e2e` 是本地 CLI 进程测试，验证实际二进制的输出、版本注入和退出码，并使用独立 fixture 验证 probe 的缺失/权限/失败诊断，以及 validate/plan/doctor 的成功、JSON 稳定性、非法参数与配置、工具覆盖和缺失工具报告。执行标记验证配置命令、`up --dry-run`、非 dry-run 错误路径及 S5.1 消息命令不会启动工具或 cmux；消息进程测试还核对关闭数据库后的跨进程保存/确认/回复重试。S2.1 的 `internal/config` 已支持严格 YAML/JSON 加载、角色引用校验、默认值与覆盖、绝对路径及提示文本快照，API 与校验边界见 [配置说明](internal/config/README.md)。S2.2 的 `internal/layout` 已支持二叉布局语义校验与纯数据编译；S2.3 的 CLI 已组合这两个模块。实际验证见 [S2 结果报告](docs/evidence/S2-result.md)。S3 已提供 IPC 与运行锁基础；真实三工具通信、`up` 启动、分屏、完整适配器、运行时 IPC/锁接线、真实派发和恢复仍待后续实现。

S3.1 的 `internal/store` 已实现 runs、seats、messages、deliveries、journal 五张业务表，事务迁移与 WAL/FULL/foreign_keys 配置。`SaveOutbox` 在同一事务中提交消息、目标代际 delivery 和追加事件；同 sender/request_id、同正文复用原消息，不同正文返回 `IDEMPOTENCY_CONFLICT`。临时数据库测试覆盖关闭重开、连接替换、各写入阶段回滚、并发幂等、外键和 journal 追加顺序；API 说明见 [Store README](internal/store/README.md)，阶段证据见 [S3 结果报告](docs/evidence/S3-result.md)。

布局 API 为 `layout.Validate(tree *config.Layout, agentIDs []string) error` 和 `layout.Compile(snapshot *config.Resolved) (*layout.Node, error)`；先 Load/Resolve，再 Compile。编译树的 split 包含 `direction`、`ratio` 和两个有序 `children`，叶子包含 `agent.id` 与独立复制的 `agent.config`（resolved 角色配置）。columns/rows 分别编译为 horizontal/vertical；缺省 ratio 为 0.5，合法范围为闭区间 0.1–0.9。形态冲突、未知/重复/遗漏角色通过 `config.FieldError` 返回 `layout` 字段路径。编译过程不读文件或启动进程，不生成终端命令，也不修改输入；配置模块本身仍保留源布局，完整布局语义须通过布局 API 检查。

目录：`cmd/agentflow` 为入口，`internal/cli` 为 CLI 逻辑及单元测试，`internal/probe` 为只读能力检查，`internal/sqlitecheck` 为 SQLite 自检，`probes/verify` 为 Go 验收证据记录器，`tests/integration` 和 `tests/e2e` 为集成与 CLI 进程测试。实际证据见 [S0 结果报告](docs/evidence/S0-result.md)、[S1 结果报告](docs/evidence/S1-result.md) 和 [能力矩阵](docs/evidence/S1-capability-matrix.md)。
