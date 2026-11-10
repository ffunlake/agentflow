# AgentFlow 使用说明

## 安装与第一次启动

当前通过 Go 源码构建，没有预编译安装包。CMUX 分屏模式需要 macOS、cmux 和配置中使用的 agent CLI；默认 `team.yaml` 使用 Codex 与 OpenCode。先在各 CLI 中自行完成登录。Go 版本要求见 `go.mod`（当前为 Go 1.25+）。

```sh
git clone https://github.com/ffunlake/agentflow.git
cd agentflow
make build
./bin/agentflow --help
```

如果已经克隆过项目，更新后重新构建：

```sh
git pull --ff-only
make build
```

运行前，把 `examples/team.yaml`（或 `team.json`）中的 `defaults.cwd` 改成你要让 agent 操作的、确实存在的项目目录；不要照搬示例里的本机路径。检查依赖：

```sh
command -v cmux
command -v codex
command -v opencode
```

在 cmux 的终端中启动三分屏 build-review 团队：

```sh
./bin/agentflow up -f examples/team.yaml --run my-first-run --json
```

`up` 会在当前 cmux window 创建一个新 workspace，启动协调器并保持前台运行。进入新 workspace 的 main 分屏后，直接用自然语言输入任务；完成后同一 run 可继续接收下一项任务。也可在启动命令中加 `--task "要做的任务"`，立即派发首项任务。每个 run ID 只能启动一次；重新创建一组分屏时请换一个新的 ID。

按 `Ctrl-C` 停止启动命令运行的协调器；这不会代替你关闭 cmux workspace。若要重开已创建的 run，不要再次执行 `up`，使用首次输出中的 `run_dir`：

```sh
./bin/agentflow coordinator --run my-first-run --run-dir "/实际/run_dir" --cmux-transport --json
```

默认 `run_dir` 位于系统临时目录，适合试用；需要长期保留时，启动时用 `--run-dir` 指定私有目录。确认协调器已停止后，可在 cmux 中手动关闭 workspace。

AgentFlow 默认 real `up` 按 YAML/JSON 布局创建新的 cmux workspace，并为每个 agent 启动对应的 Codex、OpenCode 或 Claude Code TUI，一个布局可混用三种工具。窗口创建后，主 agent 只复用这组已注册分屏，不得再次执行 `up`、`cmux new-workspace` 或 `cmux new-split`。

`examples/team.yaml` 和 `team.json` 的当前最小路径是 main/Codex、builder/OpenCode、reviewer/OpenCode，两个 worker 使用 persistent 会话。协调器从已登记的 cmux surface 绑定原生 OpenCode session，通过精确 surface 的 `cmux send --paste`、`send-key enter` 投递，并用 OpenCode 自带的定向只读 DB 查询读取该消息对应的最终结构化结果；它不使用 `cmux agent message` inbox 投递，但需要 cmux 的 OpenCode session/resume 注册。投递前检查 surface/session 身份一致且 OpenCode 上一轮已结束。cmux 尚无独占输入租约，因此用户同时输入仍可能与派发竞争。启动只准备席位，不自动把角色说明当任务；主席位仅在用户启动后明确提出工作任务时调用 `workflow start --task`，状态询问直接回答。start 成功后，主 agent 调用一次 `workflow drain`；它推进并派发各阶段，直到完成、暂停、耗尽、超时，或等待 main 自己审批再返回。如果 main 是审批人，main 根据派发内容完成审批后继续调用 drain。不要手工 sleep/轮询，也不要在中间阶段停下。当前任务未结束时不能启动另一任务，完成后可在同一 run 再次启动。`up` 本身作为前台 coordinator 持续运行。

workflow 支持两条显式前台路径：已有 cmux run 可通过 agent message/inbox transport 派发与收集结构化回传；headless run 使用同一工具和 `persistent` 会话，支持 Codex、OpenCode、Claude Code，并同步采集原生结果。headless 不创建或操作 cmux 分屏。

## 工作流类型

现有 `workflow` 支持三种类型。省略 `type` 等同 `build_review`，兼容旧配置。

- `build_review`：每步固定“执行者 → reviewer”。通过后进入 `next`；`changes` / `retry` 回到本步执行者；`blocked` 暂停。沿用 `entry`、`completion`、`steps` 配置。
- `approval`：每步指定 `execute.agent` 和 `approver`。审批人可以是任一不同于本步执行者的分屏，也可以在其它步骤承担实际工作。通过后进入 `next`；`changes` 写明原因，并把任务退回本步（`self`）或某个更早步骤；修正后仍沿 `next` 顺序重新经过后续审批。每步尝试数在退回后累计，不会因返回旧步骤而重置。`retry` 回本步，`blocked` 暂停。它是线性办理链加有界退回，不是任意 DAG。
- `pipeline`：按角色顺序逐个交接，不插入 reviewer。角色数量和顺序完全由 `flow` 决定；下面以三个执行角色为例：

```yaml
lead: main
workflow:
  type: pipeline
  flow: main -> analyst -> builder -> tester -> main
  max_attempts: 3 # 可省略，默认 3；范围 1–10
```

`flow` 的首尾必须是顶层 `lead`；中间每个 ID 必须同时存在于 `agents` 和 `layout`。首尾的 `main` 表示主 agent 发起任务、并在最后收到 drain 结果，不是额外执行阶段。中间角色依次收到原始任务和上一角色的 handoff；每个角色的 `agents.<id>.prompt` 定义它负责的部分。正常 handoff 自动派给下一个角色，最后一个角色完成后 workflow 返回 `COMPLETED`，最终 handoff 随结果返回主 agent。`retry` 重做当前角色并消耗尝试次数；`blocked` 暂停，由 lead 显式 resume。pipeline 不支持 `approve` 或 `changes`，因为它没有独立审查阶段。

可直接使用的三角色配置见 [examples/pipeline.yaml](examples/pipeline.yaml) 或 [examples/pipeline.json](examples/pipeline.json)。增减角色时，同步调整 `agents`、`layout` 和 `workflow.flow`；无需改代码。
示例中 main seat 使用 full 权限，以便调用 AgentFlow 本地协调器；worker seats 保持默认权限。

启动 pipeline 示例：

```sh
./bin/agentflow up -f examples/pipeline.yaml --run pipeline-demo --json
```

该命令会在当前 cmux window 新建一个 workspace 并保持协调器运行；之后在新 workspace 的 main 分屏输入任务即可启动这条 pipeline。

`approval` 示例见 [examples/approval.yaml](examples/approval.yaml) 或 [examples/approval.json](examples/approval.json)。同一个角色可以在某一步执行、另一步审批；同一步禁止自己审批自己的工作。审批人写 `kind=approve` 和逐项通过结果；驳回写 `kind=changes` 与原因。退回目标只能是当前或更早的步骤，超过该步骤累计 `max_attempts` 后进入 `EXHAUSTED`。

如果 lead/main 被配置为审批人，`workflow drain` 会在审批派给 main 时返回 `awaiting_actor=true`，让 main 执行 `workflow decide --kind approve --accepted true,...` 或 `workflow decide --kind changes --reason ...`，再以最新 revision 继续 drain。OpenCode worker 的结构化最终回复由绑定的 TUI adapter 收集；不需要另发普通 cmux 消息。三种类型复用持久 cursor、outbox、generation 和幂等门禁；配置可表达的流程不代表每种工具组合都已通过真实 cmux 收发验收。

## 默认启动 cmux 布局

在 cmux 终端中执行，先确认配置中的各个 cwd 已存在、相应工具已安装并完成认证：

```sh
make build
./bin/agentflow up -f examples/team.yaml --dry-run --json
./bin/agentflow up -f examples/team.yaml --run cmux-demo --json
```

`examples/team.json` 是同一布局的 JSON 写法。`--task` 会把本轮任务直接写入首个 workflow 派发；不需要在主 agent 中再运行初始化或普通 assign。`layout.split: columns` 表示左右、`rows` 表示上下；每个 split 恰好两个 children，可嵌套。ratio 是第一个 child 的比例，默认 0.5、范围 0.1–0.9；每个 agent 恰好出现一次。工具优先级为 agent.tool → `--tool` → defaults.tool → opencode。相对 cwd 和 prompt_file 相对配置文件解析，role/prompt 冻结到私有快照。

默认在调用者所在 window 新建 workspace 并自动切换焦点到它；可加 `--window window:N` 或 window UUID。每个 run ID 只能启动一次；`--run-dir /absolute/private/new-run` 可指定目录，已有目录须为 0700 且为无 symlink 的规范路径。省略 `--run-dir` 时，cmux/headless real `up` 均在 `os.TempDir()` 下按 run ID 稳定定位私有目录，重复执行同一命令会命中原目录并返回 `UP_ALREADY_ATTEMPTED`，不会再创建 workspace。Go 按 Windows/macOS/Linux 规则选择系统临时根目录，不拼接 XDG/HOME 或写死某个临时路径；路径分隔符使用本机规则，临时根中的 symlink 会解析为规范路径。

保存 JSON 输出的实际绝对 `run_dir`（文本输出为 `Run directory`）。后续所有 status、coordinator、workflow、消息及 bootstrap 命令都应显式传同一 `--run-dir`，包括在第二个终端重开；已有 run 不会扫描或猜测其它目录。创建失败返回 `UP_DIRECTORY_INVALID`，不会静默回退。交给 runtime 前失败只移除本次新建的空目录；一旦交接，即使启动失败或进程正常退出也保留目录与证据。不会自动删除运行中的目录；确认所有相关进程/workspace 已停止并保存所需证据后再人工清理。需长期保留时显式指定持久目录，避免系统清理临时文件。

可选工具路径覆盖：

```sh
./bin/agentflow up -f examples/team.yaml --run cmux-custom \
  --cmux-binary "/absolute/path/cmux" \
  --codex-binary "/absolute/path/codex" \
  --opencode-binary "/absolute/path/opencode" \
  --claude-binary "/absolute/path/claude" --json
```

默认 PATH 查找跳过 cmux routing shims。单工具布局也支持 `--native-binary`，不能与该工具的专用 override 同时使用。TUI 扩展 args 仅支持 Codex 的 `--profile NAME` 和 Claude 精确的 `--bare` / `--safe-mode`；OpenCode TUI 不接受额外 args。Claude 默认 bare，OAuth 的 safe-mode 选择见后文。model 通过 argv 传递。session policy 保留在快照，启动不执行原会话 resume/reset。

没有 workflow 的配置成功后 `up` 输出 `status=LAUNCHED` 并退出；包含 workflow 的配置会在创建 workspace 后自动启动前台 coordinator，输出 `coordinator_ready=true` 并保持运行。只有提供 `--task` 才自动启动第一项任务；否则等待主席位调用 `workflow start --task`。`cmux.workspace_id/workspace_ref` 和 `cmux.surfaces[]` 返回真实坐标及 agent/tool 映射。run 目录的 `up.requested.json`、`config.resolved.json`、`cmux.launch.json` 保存请求、冻结角色和启动结果。`state.db` 的 seats 与 journal 持久保存每个 logical agent 的 generation、launch identity、workspace/surface UUID 与 refs。`executed=true` 表示 workspace 创建已确认；`native_process=LAUNCH_REQUESTED` 不证明工具仍存活或模型已就绪。

此路径返回 `runtime_communication=UNVERIFIED`、`registration=SURFACE_REGISTERED`；含 workflow 时 `coordinator_ready=true`，否则为 false。launcher 初始将 surface 记为 `LAUNCHED`、native session ID 留空；当前团队配置的 OpenCode worker 由协调器校验并绑定原生 session 后才升为 `READY`。含 workflow 时不需要另开 coordinator 命令，工具可能展示自身的登录/信任/权限提示。创建失败、超时或返回不可解析会保留 attempt，重复 up 不重放；先检查 retained evidence 和实际状态。`--workspace` / `--surface` 不接受已有目标。

## 显式 cmux 消息通道

本节是旧的通用 `cmux agent message` transport，当前 `examples/team.yaml` / `team.json` 的 OpenCode worker workflow 不使用它。

含 `workflow` 的配置不需要手动执行本节：`up` 会自动启动 coordinator；有 `--task` 时才发出首个 workflow start。以下命令只适用于没有 workflow 的旧 run，或需要手动控制 transport 的场景。将实际输出的路径复制到终端的 `RUN_DIR`，启动已有 run 的前台 coordinator：

```sh
RUN_DIR="<复制 up 输出的 run_dir 绝对路径>"
./bin/agentflow status --run cmux-demo --run-dir "$RUN_DIR" --json
./bin/agentflow coordinator --run cmux-demo --run-dir "$RUN_DIR" --cmux-transport --json
```

可加 `--cmux-binary /absolute/cmux` 指定可执行文件。`--cmux-transport` 与三个 headless 标志互斥。在另一个终端准备 UTF-8 正文文件 `next.md`，显式发送到配置中的 logical agent：

```sh
./bin/agentflow cmux-send builder --run cmux-demo --run-dir "$RUN_DIR" \
  --agent main --generation 1 --recipient-generation 1 \
  --request-id message-1 --body-file next.md --json
./bin/agentflow cmux-inbox --run cmux-demo --run-dir "$RUN_DIR" --agent builder --generation 1 --json
```

`cmux-send` 只使用持久绑定的 surface UUID，通过 `cmux agent message ... --from agentflow --json -- <text>` 交给 cmux hooks；不输入终端、不检查 provider TUI 屏幕。`CMUX_QUEUED` 要求 native message ID、目标、正文匹配且 cmux 报告目标存在活跃 agent；仍不证明接收、READY、执行或 workflow 完成。无活跃 agent 或 cmux 未提供该字段时为 `UNVERIFIED`，且不会重发。JSON 的 `cmux_send` 包含本地 message/delivery、launch binding 与 native ID/source/target/body/raw stdout/stderr/exit。失败、超时、回执异常返回 UNVERIFIED 和非零退出码；请保留原 request ID 查询原凭据，不换 ID 重发未知动作。相同 sender/request 的正文或 recipient 改变会拒绝，stop 或旧 generation 在调用 cmux 前拒绝。

`cmux-inbox` 按 caller 的绑定 surface 只读查询 native inbox，保留 native message ID、source/target/body 与原始 JSON；可接纳已登记来源 surface 的消息，或与已确认 outbound receipt 完全匹配的 agentflow 回显。跨调用读到历史 ID 时，source（名称与 surface/workspace）、target、body 及接收 agent/generation 一致则幂等跳过，原始凭据保留；同批新 ID 正常接纳，重开后的 `[old,new]` 和 `[new,old]` 语义相同。历史 ID 内容/身份冲突返回 `CMUX_EVENT_DUPLICATE`；同批重复 ID、未知新 source、错 surface 仍拒绝整批，事务失败整体回滚。native state/raw 元数据变化不会生成新事件，返回的 `messages` 包含本次观察到的历史与新消息，不能把每次返回都当作新工作。当前是最小、有限批次入口，每次最多读取最新 100 条、stdout/stderr 各最多 64 KiB；没有增量游标或自动 drain。不会 mark-read、自动 ACK、解释 READY/DONE 或推进 workflow。cmux hooks 是否支持当前工具、是否接收到消息仍需独立真实验收；本轮只用 fake cmux 验证产品接线。

普通 `send/inbox` 保持现有 store 消息接口。以上两个 cmux 消息命令本身只操作 transport 凭据；结构化 workflow 回传由下面的显式 drain 处理。

## CMUX workflow 前台协作

以下结构化 inbox 协议适用于未走 OpenCode TUI 适配器的旧通用路径；`examples/team.yaml` / `team.json` 的 builder 与 reviewer 由协调器直接投递到已登记的原生 TUI 并读取对应 session 结果。

run 的冻结配置必须包含 `workflow`，例如默认 real `up -f examples/workflow.yaml --run cmux-workflow-demo --json` 创建的 run。可混用工具，session policy 在这条路径只作为启动元数据，不执行 fresh/reset。已有 run 无法通过编辑源配置补入或更换 workflow。

含 `workflow` 的 `up` 会自动执行以下 `start`；只有手动控制旧 run 时才需要在第二个终端执行。`RUN_DIR` 使用该 workflow run 的 up 输出路径：

```sh
./bin/agentflow workflow start --run cmux-workflow-demo --run-dir "$RUN_DIR" \
  --agent main --generation 1 --revision 0 --request-id start-1 --json
./bin/agentflow workflow status --run cmux-workflow-demo --run-dir "$RUN_DIR" \
  --agent main --generation 1 --request-id status-1 --json
./bin/agentflow workflow drain --run cmux-workflow-demo --run-dir "$RUN_DIR" \
  --agent main --generation 1 --revision CURRENT_REVISION --request-id drain-1 --json
```

`start` 原子保存 cursor 和第一条 outbox 后，向 recipient 的登记 surface 投递一次 `workflow_dispatch` JSON。正文包含版本、原 outbox request_id/message_id、完整 sender/recipient launch registration，以及 `workflow` 中的 instruction/acceptance/purpose/step/attempt/revision。回传目标是正文 `sender.surface_id`（lead inbox）；接收者使用 `recipient.agent_id/generation/run_id` 作为身份，保留 `message_id` 与 workflow revision。

`workflow drain` 在 CLI 端循环执行短 IPC 请求；服务端不会持有长轮询 handler。它处理当前 worker 的结构化结果并继续派发，直到完成、暂停、耗尽、`--timeout`，或当前 dispatch 的 recipient 是 lead。若返回 `awaiting_actor=true`，lead 检查结果后运行 `workflow decide --message-id <dispatch-id> --revision <revision> --request-id decision-<dispatch-id> --kind approve --accepted true,...`；退回时用 `--kind changes --reason <具体修改意见>`。之后以最新 revision 再次 drain。该命令通过同一 workflow reply IPC 原子记录决定与下一派发；不需要向 pane 发普通消息。`workflow status` 只读 cursor，不调用 cmux。历史 start/resume 请求只返回原始凭据；重复决定使用相同 request ID，不生成新 ID 重试未知结果。

若不维持常驻前台 coordinator，为每条 workflow 命令加 `--cmux-transport`，可选 `--cmux-binary /absolute/cmux`。命令会在同一个前台进程中取得既有 run lock、启动 IPC owner、完成一次请求并关闭。已有 coordinator/遗留锁时拒绝启动；此时使用已有前台 IPC 或先正常关闭它。未加此选项的 workflow CLI 只通过已有 IPC 调用，不直接写 store。

结构化回传正文示例（替换 run、message_id、revision 和证据引用）：

```json
{
  "v": 1,
  "type": "workflow_reply",
  "request_id": "builder-handoff-1",
  "actor": {"runId": "cmux-workflow-demo", "agentId": "builder", "generation": 1},
  "message_id": "ORIGINAL_DISPATCH_ID",
  "body": "实现已保存，等待审查",
  "status": "DONE",
  "workflow": {
    "revision": 1,
    "kind": "handoff",
    "handoff": {"summary": "完成实现", "evidence": ["test.log"], "next": "请审查"}
  }
}
```

该 JSON 作为 cmux agent message 的文本正文发送到 lead 的绑定 surface。消息必须由执行者/审查者的登记 surface 发出；native `sender_name` 必须精确等于 `actor.agentId`，native source surface/workspace 必须匹配其当前 generation 的登记 UUID，native target 必须匹配原 dispatch sender 的绑定 inbox。只有设置显示名称或 `--from` 而没有匹配的 source surface/workspace 不够。普通 `agentflow cmux-send` 固定使用 `--from agentflow`，不能充当 worker workflow 回传；它的 agentflow 回显不会被当作审查结果。实际工具 hooks 如何消费 dispatch、如何携带 source identity 回传，仍需单独真实验收。

| workflow kind | 必须匹配的 status | 结构化内容 |
| --- | --- | --- |
| handoff | DONE | 非空 summary、evidence 数组和 next |
| approve | DONE | accepted 数组逐项为 true，长度等于当前 acceptance 条数 |
| changes | DONE | 非空 reason，受原 max_attempts 限制 |
| retry | FAILED | 非空 reason，受原 max_attempts 限制 |
| blocked | BLOCKED_CONFIRM | 非空 reason，进入 PAUSED |

字段区分大小写；重复、未知、null 字段和缺少必需字段拒绝。普通文字 DONE、普通 reply、ACK、缺少 workflow 或终态 status 都不能验收。回传只能通过 cmux inbox evidence 推进；cmux 模式下直接 IPC 的 workflow reply 拒绝。Inbox 的原有整批 source/target/duplicate 门禁保留，结构化事件进一步核对 actor、generation、当前 dispatch/revision、工作角色和 status。成功事件的 reply、cursor、下一 outbox 与 `CMUX_WORKFLOW_APPLIED` 同事务提交，重复历史不会重复推进；历史冲突或无效结果需检查保留的 inbox evidence，不能解释为完成。

PAUSED 后由 lead 显式决策恢复：

```sh
./bin/agentflow workflow resume --run cmux-workflow-demo --run-dir "$RUN_DIR" \
  --agent main --generation 1 --revision CURRENT_REVISION \
  --reason "决策已保存，继续执行" --request-id resume-1 --json
```

发送先提交 `CMUX_SEND_REQUESTED` 再调用 cmux。失败、超时、响应未知、错目标或截断后保留证据；重开、重放 start/resume 和显式 drain 均不重发已领取动作。`drain` 仅可发送 attempt=0 的当前 outbox。旧代际、错目标、stop 或绑定变化不能覆盖门禁，stop 后不调用 transport。`CMUX_QUEUED` 只证明 cmux 队列回执；`LAUNCHED` 只证明 launcher 绑定，均不等于 native `READY`。workflow `COMPLETED` 来自受门禁约束的结构化审查声明，证据文件内容不自动验证；当前真实 hooks、工具接收与有效权限仍为 UNVERIFIED。

本轮 fake cmux/IPC 的三角色两步及回归验收见 [结果证据](docs/evidence/cmux-workflow-transport-v1-result.md)。后台服务/调度、队列、任务表、DAG、fresh/reset、屏幕解析和 ownership guard 均不属于该前台路径。

以下章节介绍显式 headless 自动协作。

## 1. 准备

安装并确认你要使用的 CLI 已认证：

```sh
codex --version
opencode --version
claude --version
make build
```

如果使用 Claude OAuth，把配置中每个 Claude agent 都加上：

```yaml
args: ["--safe-mode"]
```

默认 Claude 使用严格 `--bare`，不会读取 OAuth/keychain；`--safe-mode` 是显式 OAuth opt-in。

## 2. 启动一个 run

先选择一个配置和对应工具。下面以 Codex 为例。请使用一个新的、绝对路径的空目录；在两个终端中设置同一个 `RUN_DIR`：

```sh
RUN_DIR="/absolute/private/agentflow-codex-demo"
mkdir -p "$RUN_DIR"

./bin/agentflow up \
  -f examples/headless-codex.yaml \
  --headless-codex \
  --run codex-demo \
  --run-dir "$RUN_DIR" \
  --json
```

`up` 是前台进程。保持它运行，在另一个终端继续执行下面的命令。

也可以省略 up 的 `--run-dir`，使用同样的系统临时目录默认值；在第二个终端设置 `RUN_DIR` 为 up 首条 JSON 输出中的实际 `run_dir`。随后查询与重开仍必须传入该路径。若手工预建显式目录，Unix 上请使用 `mkdir -m 700` 或确保其权限为 0700。

OpenCode 使用：

```sh
RUN_DIR="/absolute/private/agentflow-opencode-demo"
mkdir -p "$RUN_DIR"
./bin/agentflow up -f examples/headless-opencode.yaml \
  --headless-opencode --run opencode-demo --run-dir "$RUN_DIR" --json
```

Claude Code 使用：

```sh
RUN_DIR="/absolute/private/agentflow-claude-demo"
mkdir -p "$RUN_DIR"
./bin/agentflow up -f examples/headless-claude.yaml \
  --headless-claude --run claude-demo --run-dir "$RUN_DIR" --json
```

三个 `--headless-*` 标志互斥。不同 run 使用不同的 run ID 和目录；不要复用已有 run 目录。

## 3. 开始自动协作

在第二个终端执行：

```sh
./bin/agentflow workflow start \
  --run codex-demo \
  --run-dir "$RUN_DIR" \
  --agent main \
  --generation 1 \
  --revision 0 \
  --request-id start-1 \
  --json
```

它会按配置执行：

```text
执行者 → handoff → 审查者 → approve/changes/retry → 下一步
```

正常完成时，结果会显示 `COMPLETED`。`request-id` 必须稳定；同一个请求重试不会重复调用工具。

## 4. 查看和继续

查询当前游标，不会调用工具：

```sh
./bin/agentflow workflow status \
  --run codex-demo --run-dir "$RUN_DIR" \
  --agent main --generation 1 \
  --request-id status-1 --json
```

如果 workflow 尚未结束，使用查询结果中的当前 `revision` 显式继续：

```sh
./bin/agentflow workflow drain \
  --run codex-demo --run-dir "$RUN_DIR" \
  --agent main --generation 1 \
  --revision CURRENT_REVISION \
  --request-id drain-1 --json
```

如果 workflow 处于暂停状态，使用：

```sh
./bin/agentflow workflow resume \
  --run codex-demo --run-dir "$RUN_DIR" \
  --agent main --generation 1 \
  --revision CURRENT_REVISION \
  --reason "继续执行" \
  --request-id resume-1 --json
```

## 5. 正常重开

先正常停止前台 `up/coordinator`，确保 run lock 已释放，再重开：

```sh
./bin/agentflow coordinator --headless-codex \
  --run codex-demo --run-dir "$RUN_DIR" --json
```

重开不会自动调用工具或自动 drain；需要再次显式执行 `workflow status` 和 `workflow drain`。

OpenCode 和 Claude 只需把上面的配置文件、headless 标志和 run ID 换成对应版本。

## 6. Headless 重要限制

- 当前只支持同一工具、`persistent`、generation 1 的 headless run。
- headless 不支持混合工具、fresh/reset、后台常驻调度；cmux 支持混合工具启动与显式前台 workflow transport，真实 hooks 和 native READY 仍未验收。
- 强制杀进程后的 stale lock 不会自动接管；先人工核对。
- 超时或结果未知时不要换新的 `request-id` 重发，先用原 request 查询并检查 `status`。
- `permissions: full` 只表示请求更高权限，不等于权限实际生效。

更多阶段证据见：

- `docs/evidence/cmux-layout-launcher-v1-result.md`
- `docs/evidence/cmux-agent-message-transport-v1-result.md`
- `docs/evidence/cmux-workflow-transport-v1-result.md`
- `docs/evidence/real-opencode-v1-validation-20261104.md`
- `docs/evidence/claude-oauth-headless-compat-20261104.md`
- `docs/evidence/headless-codex-v1-result.md`
