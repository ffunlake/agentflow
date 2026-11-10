# AgentFlow 技术实现文档 v2

状态：S0 工程基础、S1 安全能力检查基础、S2 配置/布局/validate/plan/doctor CLI 及 S3.1 持久消息存储已实现，S1 真实通信验收为 PARTIAL；S4.1 partial 已提供纯启动计划、wrapper argv 数据与 `up --dry-run`，真实启动待实现。实际验证见 [S0 报告](../evidence/S0-result.md)、[S1 报告](../evidence/S1-result.md)、[S2 报告](../evidence/S2-result.md)、[S3 报告](../evidence/S3-result.md) 和 [能力矩阵](../evidence/S1-capability-matrix.md)；后续运行时接口仍是实施约定。

需求依据：[访谈决策](2026-11-02-agentflow-interview-decisions.md)。上游：[架构设计](2026-11-02-agentflow-design.md)。执行入口：[分步计划](2026-11-02-agentflow-implementation-plan.md)。

## 1. 实现范围

首版同时支持 OpenCode、Codex、Claude Code，终端后端为 cmux。用户使用原生界面；后台投递必须对应同一可见会话。

AgentFlow 管理角色、会话、消息、确认与恢复。主 agent 判断工作可行性、直接分配工作并检查结果。默认最高权限；执行者默认新工作新会话，允许配置持续会话。首版不建立独立任务表、任务领取凭证或强制验收流程。

## 2. 技术栈与模块

按用户指定改用 Go 1.25+、Go modules 和 Make；本机验证版本为 Go 1.25.5。SQLite 使用纯 Go 驱动 modernc.org/sqlite v1.39.1，通过 database/sql 调用，无需 CGO。go.mod 与 go.sum 固定版本和校验和；测试使用 Go 标准 testing。make check 组合格式检查、依赖校验、go vet、测试与构建，详见根目录 [README](../../README.md)。后续本机网络和子进程使用标准库 net、os/exec；配置解析库在 S2 按原契约选型，不在 S0 引入。

```text
cmd/agentflow/  main.go，可注入版本的 CLI 入口
internal/
  cli/          help/version、S1 probe、S2 validate/plan/doctor、错误输出与退出码
  probe/        S1：三工具版本/帮助、cmux 前置检查和 JSON 证据
  sqlitecheck/  S0：诊断库事务提交、关闭并重开验证
  config/       schema、load、resolve
  layout/       配置树到 cmux layout 的纯函数
  store/        S3.1：SQLite 五表、事务迁移、消息 outbox 与追加 journal
  runtime/      S4.1 partial：纯 StartupPlan、seat wrapper binary/argv
  # 以下模块待后续阶段实现：
  cmux/         socket/CLI 客户端、能力和事件
  tools/        opencode、codex、claude 适配器
  # runtime 后续：协调进程、IPC、包装器执行、重置与恢复
  context/      角色协议、文件清单、报告路径
probes/         安全 probe 使用说明和 Go 验收证据记录器；真实会话验证待补齐
tests/         integration、e2e；单元测试与 Go 源码同目录
docs/evidence/ 阶段结果；S0-result.md 记录本轮实际验证
```

配置与布局模块不启动进程；store 不执行外部操作；runtime 组合事务与工具调用。每个 run 一份 SQLite 和一个协调进程，包装器维护原生工具生命周期。

## 3. 配置契约

配置字段与现有 [YAML](../../examples/team.yaml)、[JSON](../../examples/team.json) 一致。

| 字段 | 规则 |
|---|---|
| version | 固定 1 |
| name / terminal | 非空名称；terminal 仅 cmux |
| lead | 必须引用存在的主 agent |
| context_dir | 文件上下文根目录；相对配置文件解析 |
| defaults.tool | opencode、codex、claude；缺省 opencode |
| defaults.cwd | 相对配置文件目录；缺省配置文件目录 |
| defaults.permissions | full 或 default；缺省 full |
| defaults.session | fresh 或 persistent；缺省 fresh |
| agents.ID | role、prompt 或 prompt_file；可覆盖 tool、cwd、permissions、session，可选 model、args |
| layout | 二叉 split 树或 agent 叶子；每个角色恰好引用一次 |

prompt 与 prompt_file 恰选其一。拒绝未知字段与重复对象键。角色 ID 使用小写字母开头的字母、数字、下划线和短横线，最长 48 字符。

工具优先级：agent.tool → CLI --tool → defaults.tool → opencode。其他角色属性优先使用角色覆盖，再使用 defaults。主 agent 的示例 session 为 persistent，这是配置选择；其可恢复上下文以文件为准。

columns 编译为 cmux horizontal；rows 编译为 vertical；ratio 默认 0.5，范围 0.1–0.9。每个 split 恰好两个 children。比例是目标值，受终端最小尺寸限制。

args 是原始字符串数组，不做 shell 插值，也不推测参数内部路径。工具适配器应使用 exec.CommandContext(ctx, binary, argv...) 直接启动。S4.4 按本轮无 shell 拼接约束，仅构造独立 cmux 拓扑 argv 与包装器 argv；现有 cmux `--command` 接收文本，包装器传输尚未实现，不生成该文本参数。正文与凭据保留在私有快照中。

resolve 保存绝对路径、角色文本、工具与权限模式的规范化快照及摘要。恢复使用快照；编辑配置不改变当前运行。

## 4. 本机运行、身份与 IPC

运行目录：$XDG_STATE_HOME/agentflow/runs/<run-id>，默认 ~/.local/state/agentflow/runs/<run-id>。目录私有；socket 使用短路径以满足 macOS 限制。

```text
state.db                 消息、绑定与恢复日志
config.resolved.json     配置快照
coordinator.log          脱敏诊断信息
prompts/                 角色和通信协议
```

逻辑地址为 run ID 与 agent ID；实际绑定包含 native session/thread ID、workspace UUID、surface UUID 和 generation。重置或替换会话时增加 generation，同会话重连不增加。短序号与屏幕位置只用于展示。

CLI 与协调进程采用 Unix socket、UTF-8 NDJSON。请求字段为 v、requestId、actor、method、params；响应为 requestId、ok、result 或 error。处理拆包、粘包、断线和 1 MiB 帧上限。正文过大返回明确错误，详细证据通过文件引用。

actor 由启动环境或生成的会话身份文件提供。工具重置后必须更新真实调用身份；仅修改数据库不足以更新常驻服务环境。不能把迟到旧会话的命令自动认成当前 generation。身份刷新方式列入 S1 验证。

单实例由独占运行锁和 socket 探测保证。记录 owner PID、随机 token 和启动信息；恢复前核对 owner，不能因一次连接超时就删除锁。竞争恢复只能有一个成功者，不能重复创建团队。

## 5. SQLite：五张表

使用 WAL、foreign_keys=ON、synchronous=FULL，迁移在事务内执行。每库只有一个 run。

| 表 | 核心内容 | 约束 |
|---|---|---|
| runs | 配置摘要、workspace、启动阶段、停止意图 | 唯一 run；保存创建现场 |
| seats | agent、generation、原生 session、surface、状态、当前派发消息 | 保留历史绑定；每角色只有一个当前代际 |
| messages | seq、id、sender、recipient、kind、正文、reply_to、request_id、状态报告 | id 唯一；发送方与 request_id 幂等；正文不可静默覆盖 |
| deliveries | message、目标代际、投递阶段、重置阶段、通道 ID、尝试与下次恢复时间 | 唯一 message 与目标代际组合；兼作 outbox |
| journal | 启动、重置、绑定、确认、故障、恢复和主动停止事件 | 追加记录，保留证据引用 |

没有独立 tasks 表。派发是一种消息，结果是回复消息；当前派发引用仅帮助路由和恢复，不产生用户要操作的工作 ID。

发送先在事务中保存 message、delivery 和 journal，提交后才返回已保存。投递发生在事务外。相同 request_id 与相同正文返回原消息，不同正文报 IDEMPOTENCY_CONFLICT。ACK 与结果消息写入也必须幂等。

## 6. 用户与 agent 命令

S0 实现 agentflow --help（含 -h 和无参数帮助）及 --version；S1 提供独立安全检查 `agentflow probe --tool all --evidence-dir <新目录>`。S2.3 已实现以下 validate/plan/doctor 接口，S4.1 partial 已实现 `up --dry-run`；`up` 不带 `--dry-run` 明确返回尚未执行、需要 cmux launcher 的错误（退出码 1）。S4.3 已实现最小 status、attach 定位查询与 stop intent，支持 `--run ID [--run-dir DIR] [--json]`。status 只读既有库/快照/锁，attach 始终 attached=false、UNVERIFIED、退出 1；stop 仅原子记录一次停止标志和 journal，重复返回原事件且 executed=false。真实 cmux attach/stop 未验证，详见 [S4.3 结果](../evidence/S4.3-result.md)。其余命令仍待后续实现，当前返回用法错误（退出码 2）。

```bash
agentflow validate -f examples/team.yaml [--tool <opencode|codex|claude>]
agentflow plan -f examples/team.yaml [--tool <opencode|codex|claude>] [--json]
agentflow doctor -f examples/team.yaml [--tool <opencode|codex|claude>] [--json]
agentflow up -f examples/team.yaml --dry-run [--tool <opencode|codex|claude>] [--json]
agentflow status --run <run-id>
agentflow assign builder --body-file next.md
agentflow send builder --body-file supplement.md
agentflow inbox --pending
agentflow message ack <message-id>
agentflow reply <message-id> --status DONE --body-file result.md
agentflow attach --run <run-id>
agentflow resume --run <run-id>
agentflow stop --run <run-id>
```

三个配置命令组合 config.Load/Resolve 与 layout.Compile；`--tool` 遵循第 3 节优先级。validate 成功明确报告配置与布局有效；plan 文本展示规范化绝对路径、lead、角色属性与 prompt 摘要、编译布局，JSON 为 `source_path/config/layout`，完整提示文本包含在快照内。YAML/JSON 来源路径不同，但规范化配置与编译树相同。

`up --dry-run` 在相同配置解析后调用 `runtime.Compile`，输出 `StartupPlan`：run 摘要、配置快照、编译树，以及按 agent ID 排序的 seat；每个 seat 包含 tool/cwd/permissions/session/role、prompt 引用、从根到叶子的方向/ratio/child 路径及 wrapper binary + argv。JSON 为 `status=PLANNED`、`executed=false`；不分配真实 run/session ID，不写文件，不查找或启动工具/cmux。默认 wrapper 数据为 `agentflow` 和 `["_seat","--snapshot","config.resolved.json","--agent",ID]`，相对快照引用须由未来 launcher 替换成运行目录内的绝对路径；正文和工具 args 仅存在快照内。S4.4 隐藏 `_seat` 入口仅校验私有初始快照及 agent 身份，校验通过后返回 `SEAT_TOOL_ADAPTER_UNIMPLEMENTED`、退出 1；纯函数 `BuildCMUXCommand` 只生成拓扑与独立 wrapper argv。真实 launcher、工具启动、有效权限与 READY IPC 仍为 PARTIAL/未实现，详见 [runtime API](../../internal/runtime/README.md)。

doctor 仅查找最终角色配置要求的工具可执行文件，不调用工具或 cmux，不执行版本/帮助探针。其 JSON 报告 `source_path/status/configuration/layout/tools/runtime_communication/diagnostics`；configuration/layout 为 VALID、INVALID 或 NOT_CHECKED，工具为 FOUND、MISSING 或 UNAVAILABLE，整体为 OK 或 FAILED，运行时通信始终 UNVERIFIED。工具和关联角色按名称排序；工具缺失包含 EXECUTABLE_MISSING 与 tools.<tool>.executable 路径。配置/布局错误包含字段路径与文件上下文。plan/doctor 的 JSON 配置失败报告采用同一诊断结构并输出到 stdout，错误说明同时写 stderr；参数错误只写 stderr。成功退出 0，配置/布局/工具诊断失败退出 1，参数错误退出 2。

assign 表示分配下一项工作，按接收者 session 配置决定是否新建会话。send 是普通消息或当前工作的补充，不重置。reply 关联消息并报告结果；不另行领取或强制验收。

目标运行中的 agent 自动定位 run 与身份；人从外部终端显式提供 --run。S5.1 最小实现暂要求显式提供 `--run/--agent/--generation`，写入另需 `--request-id`，支持已有库中的 assign/send/inbox/ack（含 message ack）/reply；自动身份、已认证 IPC coordinator 与真实投递后续接线。命令支持 --json，诊断写 stderr。新消息仅报告 STORED/PENDING、channel=UNVERIFIED、attempt=0；显式 ACK 仅表示 receipt。完整约定与实际验证见 [S5.1 结果](../evidence/S5.1-result.md)。成功表示具体操作已完成，不能把已保存描述为已执行。

## 7. 派发与会话重置

fresh 模式的一次 assign 自动完成：

1. 持久保存本次派发，检查目标身份、上一项工作状态与用户输入。
2. 成员仍忙碌或存在草稿时排队；不擅自中断正在运行的命令。
3. 记录 RESET_REQUESTED，调用已验证的新会话操作。
4. 观察并核对新的 native session/thread，暂不推进 generation。
5. 注入角色、文件上下文路径和通信说明，等待匹配新 native 与预期下一代际的 READY；确认后原子推进 generation、current 绑定及 delivery 目标。
6. 投递本次正文并等待接收确认。

重置阶段保存在原派发的 delivery 中。恢复先对账，已确认重置时不重复清空；结果不明时不能再盲目发送 /new。persistent 模式跳过重置步骤。

S5.2 最小实现增加可注入 SessionCoordinator/ResetAdapter/SubmitAdapter，使用 fake 验证上述状态转换。assign 支持 `--session fresh|persistent`（`--mode` 同义），默认读取运行库中的冻结配置；普通 send 不触发 adapter。CLI 未接真实 adapter，保存后明确报告 STORED/QUEUED、UNIMPLEMENTED 与 runtime_communication=UNVERIFIED；requested/unknown 阶段保持原状态且不自动重放。S5.3 对账恢复和真实通信未完成，见 [S5.2 结果](../evidence/S5.2-result.md)。

/new、/clear 是候选原生控制命令，不是发给模型的普通正文。优先使用原生控制接口；必须经终端输入时，确认空闲与无草稿，并验证实际效果。若原生快捷操作无法保持界面与会话一致，可重新启动同一 pane 的原生客户端连接到新会话，但必须验证。

角色 READY 必须由实际 agent 会话执行通信命令回传，包装器注册不能代替。generation 或 session 不匹配的 READY、ACK、结果均拒绝并记录。

## 8. 三种工具适配器

公共能力包括 probe、launch、resetSession、submit、inspect、reconcile、resume、stop。公共返回值携带原生会话身份及证据，不仅是命令退出码。

| 工具 | 接入候选 | 首轮必须验证 |
|---|---|---|
| OpenCode | 每角色 serve、创建确定 session、原生 attach、prompt_async | 空闲接收、新会话可见切换、API 与终端同 session |
| Codex | queue --resume；必要时 app-server 配合原生 TUI --remote | 实际唤醒、同 thread 展示、重置后身份与路由 |
| Claude Code | cmux wrapper、原生消息 hook、/clear 或等价控制 | 主动接收、重置完成事件、角色重新注入 |

本机 Codex 0.166.0 的帮助信息确认存在 queue --resume 与 --remote，但 queue 的恢复失败可能只给 warning，退出码不能证明唤醒。必须通过接收端确认与可见会话核对验收。

OpenCode 的服务继承对应角色身份和 cwd；包装器保管进程与凭据，协调进程重启后重新注册。不能只给 attach 客户端设置身份而忽略实际执行工具的服务端。

Claude 的 hook 负责消息接收，不等于会话重置接口。缺少必要 hook 时预检显示具体缺口；安装方式需确保不覆盖已有配置。三种适配器均未实际验收，不能标记为已可靠接入。

### S1 本轮已实现边界

Go `internal/probe` 为三个工具建立独立选择入口；固定执行版本和帮助检查，以及 cmux 的只读 ping/capabilities/identify/tree。跳过 cmux per-surface shim，使用证据目录中的独立子进程配置/状态目录，逐命令记录实际路径、argv、cwd、版本输出、退出码和原始流。无模型请求、会话发送、重置或 hook 安装。OpenCode 帮助在本机写入 stderr，能力检查同时检查两个流。

结构化矩阵区分 `PASS`（具体基础检查）、`ADVERTISED_ONLY`（帮助中的候选能力）、`UNVERIFIED`（无运行时证据）和 `WAITING_DEPENDENCY`。基础检查全通过时整个 S1 仍为 `PARTIAL`。退出 0 不代表接收或重置成功；`--require-runtime` 明确返回非零。JSON 内的独立临时会话启动计划不会自动执行。有效最高权限、原生会话身份刷新、hook 接收与恢复仍未验证；本轮不将候选 CLI 参数升级为接入结论。

后续独立人工实测已在新 cmux 窗口启动三工具原生界面，见 [S1-runtime-b7bf333c](../evidence/S1-runtime-b7bf333c/README.md)。Codex 明确等待隔离登录，三工具均未建立 native ID 或 nonce ACK，且实验窗口后续不可用；实际错误与退出码保留，状态为 WAITING_DEPENDENCY。OpenCode serve/attach/API、Claude hook、重置/身份/full 权限/恢复均未实测通过。该记录不扩展安全 probe 的固定执行集合，不改变适配器契约或阶段出口。

## 9. 默认最高权限

full 是用户已确认的默认模式。适配器为本次启动构造正确配置，并报告实际有效权限；不会仅设置“不询问”而保留不符合预期的执行限制。

候选映射：Codex 的本次启动绕过审批与沙箱选项；Claude 的 bypassPermissions 或对应启动选项；OpenCode 的本次运行 permission allow 配置。精确行为和配置优先级由 S1 验证，尤其检查角色规则或受管策略是否覆盖。

不静默改写其他会话的全局权限。环境不能提供要求的权限时，在可行性预检阶段准确报告；运行中出现新的限制则进入自主诊断或等待依赖。

## 10. 消息状态与恢复

分别记录 STORED、SUBMITTED、DELIVERED、ACKED；没有独立 delivered 事件时，显式 ACK 可同时证明投递与接收。ACK 不证明工作完成。

投递结果丢失时先按原生 message ID、session 事件及日志对账。已在原生队列中的消息不重复提交。通道允许至少一次通知，内部 ID 与投递记录用于去重；取消强制领取系统后，不声称任意外部副作用恰好执行一次。无法确定是否执行过时由主 agent 核对文件和证据。

可恢复错误使用退避和抖动持续重试，建议等待从 1 秒增长至 60 秒；具体数值属于可调整实现默认。重试次数不决定工作流终止。等待期间不用模型轮询；状态展示最近原因和下一次检查时间。

协调进程异常退出后，仍存活的包装器可竞争恢复唯一协调进程。停止意图先落盘，正常 stop 或主动关闭成员不能触发自动复活。启动创建响应丢失时先根据注册和启动日志查证，不能直接再建 workspace。

同一工作优先恢复原会话。需要替代会话时，主 agent 根据检查点和实际产物生成继续说明；不从头重放旧指令。工具、模型、账号沿用快照，未配置备用项就不自动换来源。

## 11. 文件上下文与成员状态

建议在 context_dir/<run-id>/ 下建立 project.md、decisions.md、progress.md、pending.md，以及 inputs/、reports/、evidence/。主 agent 独占更新精简摘要；执行者写独立报告。更新摘要采用同目录临时文件后原子替换。

角色提示只引用必要文件，不复制全部聊天。完成报告包含产物、验证命令与实际结果、证据路径及未解决事项。主 agent 检查后决定下一步，无强制 accept 命令。

成员状态包括 STARTING、READY、WORKING、WAITING_DEPENDENCY、RECOVERING、STOPPED。DONE、FAILED、BLOCKED_CONFIRM 可作消息报告；失败或待确认消息触发主 agent 处理，不自动终止整个团队。native idle 与 DONE 报告分开记录。

主动退出或关闭分屏写停止意图并通知主 agent。意外崩溃自动恢复；原因不明时先核对 cmux 事件与包装器退出记录。工作完成后待命，stop 才结束对应运行。

## 12. 验证边界

必须验证：三工具同会话接收与重置、有效最高权限、fresh/persistent 两种模式、补充消息不重置、用户草稿保护、旧代际拒绝、重置中断恢复、投递响应丢失、协调进程恢复、主动关闭不复活及文件上下文重建。

S0 已建立 Go 工程与本地 CLI、SQLite 基础测试，具体执行结果以 S0 报告为准；其诊断表 s0_selfcheck 仅用于自检。S3.1 的五张业务表、迁移、事务 outbox 与追加 journal 已在独立临时数据库中验证，详见 [Store API](../../internal/store/README.md) 和 [S3 报告](../evidence/S3-result.md)。运行目录、IPC、运行锁、真实启动、权限生效、消息接收、会话重置和故障恢复均属于后续实施验收。
