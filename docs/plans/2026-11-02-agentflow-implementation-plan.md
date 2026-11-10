# AgentFlow 分步实施计划 v2

状态（2026-11-03）：S0–S7.2 的本地基础实现与 S7.3 代码侧完整故障矩阵已交付；新增 30 个表驱动故障场景、全量 test/build/check/tidy 和核心 race PASS，见 [S7.3 最终报告](../evidence/S7.3-result.md)。真实 native/cmux 三工具 e2e、有效权限、现场 attach/stop 与真实恢复仍 PARTIAL/UNVERIFIED；完整 v1 出口保持未通过。S1 历史受控原生证据保留自身边界，见 [能力矩阵](../evidence/S1-capability-matrix.md)。

依据：[访谈决策](2026-11-02-agentflow-interview-decisions.md)、[技术实现文档](2026-11-02-agentflow-technical-spec.md)、[架构设计](2026-11-02-agentflow-design.md)。

S5.1 消息持久化 API/CLI、S5.2 fresh/persistent 协调、S5.3 持久对账、S6 文件上下文/报告/Bootstrap、S7.1 有限恢复和 S7.2 原会话优先恢复 API 均已完成本地验证。S7 恢复使用显式 tick 和可信注入 adapter；真实 adapter/READY IPC、后台自动调度和包装器恢复协调进程仍未接线。

## 1. 执行规则

按 S0 → S7 推进。每阶段保存产物、完成必要验证、写结果报告后进入下一阶段。阶段出口是工程检查，不增加逐阶段人工审批。

可恢复问题在当前阶段自主处理；依赖暂不可用时记录原因并等待恢复。不得把未验证项标为通过。实际执行时只在验证完成后勾选任务。

结果报告写入 docs/evidence/Sn-result.md，记录命令、实际结果、证据和下一步。新会话先读本计划、访谈决策和上一阶段报告。

## 2. 阶段总览

| 阶段 | 达成能力 | 验收出口 | 当前状态 |
|---|---|---|---|
| S0 | 工程基础 | CLI、构建和 SQLite 基础检查通过 | 已通过，见 S0-result.md |
| S1 | 三工具接入验证 | 接收、重置、权限、恢复与可见会话有证据 | PARTIAL：安全 probe 已实现，真实会话证据待补齐 |
| S2 | 配置与布局 | YAML/JSON 等价，角色及嵌套布局正确 | S2.1/S2.2/S2.3 已实现并验证，见 S2-result.md |
| S3 | 持久通信基础 | SQLite、IPC、幂等与单实例通过 | 本地 SQLite/NDJSON IPC/锁与竞争测试 PASS；产品协调进程唯一写入口和运行接线仍 PARTIAL |
| S4 | 原生团队启动 | 三角色启动并完成真实 READY | PARTIAL：S4.1 计划、S4.2 fake 注册、S4.3 查询/停止意图、S4.4 seat 校验入口/cmux argv 已实现；真实启动及 cmux attach/stop 未验证 |
| S5 | 直接派发与消息 | 一次派发自动完成必要重置与发送 | PARTIAL：S5.1 持久化 API/CLI、S5.2 会话协调/fake adapter、S5.3 持久化对账/恢复决策已实现；真实投递/重置/恢复未验证 |
| S6 | 文件上下文与持续协作 | 新会话按文件继续工作，结果回报后待命 | PARTIAL：S6.1 私有上下文、S6.2 独立报告/精简合并、S6.3 文件 Bootstrap/Rebuild API/CLI 已实现；真实注入/协作/重建未验证 |
| S7 | 自主恢复与完整验收 | 故障恢复、主动停止、三工具回归通过 | PARTIAL：S7.1/S7.2 API 与 S7.3 本地完整矩阵/工程检查 PASS；真实恢复、包装器恢复与三工具最终出口未验证 |

里程碑：S4 可启动团队；S6 可进行真实协作；S7 才可声明完整 v1 通过验收。

## 3. S0：工程基础

- [x] S0.1 建立 Go module、go.mod/go.sum、cmd/agentflow、源码与测试目录，固定依赖版本和校验和。
- [x] S0.2 建立 make build、fmt、fmt-check、vet、test-unit、test-integration、test-e2e 和 check 入口；CLI 支持 help/version 与一致错误输出。
- [x] S0.3 使用纯 Go SQLite 驱动验证事务提交、真实关闭并重新打开后的读回；记录实际 Go 版本与平台。本轮按用户限定只做持久化自检；私有运行目录和 Unix socket 随 S3 的运行与 IPC 实现验证。

产物：工程文件、锁文件、最小可执行 CLI、S0 结果报告。

检查：执行 make deps、go test ./...、go build ./...、make check、./bin/agentflow --help 和 ./bin/agentflow --version。检查错误输入非零退出码。test-e2e 在 S0 仅指本地 CLI 二进制进程测试，不代表真实工具通信测试。

出口：干净安装后可构建运行；SQLite 可提交并在重启后读回；错误返回非零退出码。

## 4. S1：三工具接收、重置与权限验证

首轮 Go 安全 probe 最小基础已完成：独立工具检查、结构化矩阵、实际版本/帮助/cmux 前置证据与错误诊断已实现。后续 [独立原生启动实验](../evidence/S1-runtime-b7bf333c/README.md) 已记录三工具自有 surface；Codex 等待隔离登录，实验窗口后续丢失，native ID 与 nonce ACK 均未建立，状态为 WAITING_DEPENDENCY。下面三项仍不勾选；这些记录不等于完整 S1 出口。使用方法见 [probe 说明](../../probes/README.md)。

- [ ] S1.1 建立 OpenCode、Codex、Claude Code 的独立 probe；对三个工具分别证明消息进入对应原生可见会话，空闲时可主动接收。
- [ ] S1.2 分别验证新会话操作、重置完成信号、角色重新注入和调用身份刷新；验证最高权限的真实有效状态及原会话恢复。
- [ ] S1.3 覆盖 busy、输入草稿、不同 cwd、主动退出与崩溃；记录能力矩阵、实际版本及接入选择，同步技术文档。

产物：probes/、S1 结果报告、三工具能力矩阵。建议脚本为 probe:opencode、probe:codex、probe:claude、probe:cmux。

检查：每个工具记录 workspace、surface、native session/thread、接收证据和实际重置结果。Codex queue 的成功退出不能单独作为唤醒证据。

出口：三个工具均有接收、重置和权限实测依据。任一工具未通过，保留该适配器的未完成状态，修正方案或等待必要依赖；不能降低为延迟接收后宣布首版完成。

## 5. S2：配置与嵌套布局

- [x] S2.1 实现严格 schema/load/resolve，覆盖 lead、context_dir、角色覆盖、permissions 和 fresh/persistent。见 [配置 API](../../internal/config/README.md)；go test ./...、go build ./...、make check 通过，go mod tidy -diff 无差异。布局形态、ratio 默认值与范围留给 S2.2。
- [x] S2.2 实现布局纯函数校验与数据编译：`internal/layout.Validate/Compile` 检查二叉 split/leaf、每角色恰好一次引用及 ratio 0.1–0.9；columns/rows 映射 horizontal/vertical，缺省 ratio 0.5，叶子携带独立 resolved 配置。错误提供 layout 字段路径；重复键、未知字段继续由配置 schema 拒绝。表驱动测试覆盖现有 YAML/JSON 等价、比例边界、冲突、引用、深层嵌套及输入不变；go test ./...、go build ./...、make check 通过，go mod tidy -diff 无差异。本轮只生成数据，原条目中的包装器命令转义留到实际包装器接入时实现。
- [x] S2.3 实现 validate/plan/doctor，组合 Load/Resolve/Compile；文本与稳定 JSON 输出规范化角色属性、绝对路径和布局，核对示例 YAML/JSON 等价及 CLI --tool 优先级。doctor 只查找所需工具，缺失时结构化报告并退出 1；FOUND/OK 不表示真实通信通过，运行时始终 UNVERIFIED。进程测试覆盖成功、JSON、缺文件、非法配置/布局、未知参数、覆盖、退出码与不启动工具；go test ./...、go build ./...、make check 通过，go mod tidy -diff 无差异。见 [S2 报告](../evidence/S2-result.md)。

产物：配置模块、布局编译器、计划输出、S2 结果报告。

检查：单元测试；现有示例解析后 main=OpenCode/persistent、builder=Codex/fresh、reviewer=Claude/fresh，全部默认 full。CLI --tool 只覆盖默认工具。

出口：非法配置在创建 workspace 前失败；合法配置可离线展示最终计划。

## 6. S3：持久消息与本机协调

- [x] S3.1 实现五张表、事务迁移、WAL/FULL/foreign_keys、事务 outbox 和追加 journal；保存配置快照及 native/generation 绑定。同 sender/request_id、同正文返回原消息，异正文返回稳定 IDEMPOTENCY_CONFLICT。临时数据库测试覆盖重开读回、每个写入阶段回滚、连接替换、并发重试、外键/唯一约束和追加顺序。见 [Store API](../../internal/store/README.md) 与 [S3 报告](../evidence/S3-result.md)。
- [ ] S3.2 实现 NDJSON IPC、消息幂等、actor/代际校验及稳定错误码；协调进程作为唯一业务写入口。
  - 本地 IPC/actor 校验、断线、分帧、跨连接幂等及 S7.3 提交后响应丢失矩阵已通过；可信传输身份与运行中协调进程唯一写入口未接线。
- [ ] S3.3 实现运行锁、协调启动与恢复竞争；测试并发启动、连接断线、事务回滚和重复 request_id。
  - 锁 API 的并发/跨进程竞争、损坏锁不抢占、SQLite 多句柄竞争和重开已通过本地回归；真实协调进程启动/恢复未验收。

产物：store、IPC、协调进程基础、S3 结果报告。

检查：使用临时数据库和假通道执行 integration tests，覆盖已提交但响应丢失、同 key 不同正文冲突、单实例竞争。

出口：消息提交后不因协调进程退出丢失；重复请求不创建第二条消息；不产生双协调进程。

## 7. S4：三角色原生团队启动

- [ ] S4.1 实现 up、cmux 嵌套 layout、启动日志和 _seat 包装器，启动示例中的三个不同工具。
  - 已实现的最小部分：`internal/runtime` 纯 StartupPlan、seat wrapper binary/argv 与 `up --dry-run`；S4.4 补充隐藏 `_seat` 校验入口与 cmux argv 纯函数。真实 cmux launcher、启动日志、工具执行仍未实现，S4.1 出口仍未通过。
- [ ] S4.2 注入角色与文件路径，注册 UUID/session/generation，等待真实会话执行 READY；验证默认 full。
  - 最小注册 API 已通过 fake launcher 验证，真实 READY 与 full 验收未通过，见 S4.2-result.md。
- [ ] S4.3 实现 status/attach 与停止意图记录，验证布局变化不改变角色绑定、创建响应丢失不重复建团队。
  - [x] S4.3 最小范围：实现只读 runtime 状态与 attach 定位结果、幂等 stop intent 和文本/JSON CLI；覆盖空库/缺库、run 身份、当前 generation/native、快照缺失/损坏/不匹配、锁缺失/存在/损坏/不匹配、事务回滚、并发及跨进程重复 stop。gofmt、go test ./...、go build ./...、make check 通过，go mod tidy -diff 无差异。见 [S4.3 结果](../evidence/S4.3-result.md)。
  - 真实 cmux attach/stop、布局变化与创建响应丢失现场仍未验证；原 S4.3 出口保持未勾选。stop 只记录意图，不关闭进程/workspace；不进入 S5。
- [x] S4.4 最小范围：增加隐藏 `_seat --snapshot <absolute> --agent <id>`，严格拒绝缺参/重复/未知/位置参数/相对路径；复用私有快照校验，检查 agent 身份、工具/cwd 等元数据，并限定全体 seat 为 STARTING、generation 1、空 native。合法输入稳定输出 `SEAT_TOOL_ADAPTER_UNIMPLEMENTED` 和退出码 1，不启动工具或伪造 READY/ACK。增加 `BuildCMUXCommand` 纯函数，校验完整计划、显式目标与 PanePath，返回分开的 cmux 和 wrapper binary/argv，正文、模型及凭据不进入启动命令。runtime/CLI 单元与进程 e2e 覆盖拒绝路径、不启动工具/cmux、冻结文件提示、敏感值不泄露、YAML/JSON 等价及输出稳定。gofmt、go test ./...、go build ./...、make check 均通过，go mod tidy -diff 无差异。见 [S4.4 结果](../evidence/S4.4-result.md)。
  - 当前 cmux `--command` 只接收命令文本；按本轮无 shell 拼接约束保留独立 wrapper argv。真实 cmux 创建、split cwd/比例应用、wrapper 传输、原生工具启动与 READY IPC 仍为 PARTIAL/UNIMPLEMENTED；S4 原生团队出口不勾选，后续继续 S4，不进入 S5。

产物：可启动的三角色团队、角色协议、状态输出、S4 结果报告。

检查：真实 e2e；核对每个 pane 的工具、会话、角色、目录与有效权限。包装器注册不能代替 agent READY。

出口：一个命令启动整个示例；CLI 退出后团队仍服务；attach 定位原 workspace。

## 8. S5：直接派发、会话模式与可靠消息

- [ ] S5.1 实现 assign/send/inbox/message ack/reply；普通 send 不重置，assign 按配置一次完成新工作派发。
  - [x] S5.1 本轮最小持久化范围：runtime MessageService 的 assign/send/inbox/ack/reply 与文本/JSON CLI（含 message ack 别名）；事务内校验 run/current sender/recipient generation，reply 关联原消息并核对双方代际。共用现有 outbox，sender/request_id 同正文返回原消息，异正文稳定冲突；ACK journal 凭据复用同一请求命名空间，重复确认无重复状态转换。保存 message/delivery/journal，channel=UNVERIFIED、attempt=0；不新增表、依赖或领取状态机。验证与边界见 [S5.1 结果](../evidence/S5.1-result.md)。
   - S5.1 按明确授权提前实现独立存储基础；S4 原生团队出口仍为 PARTIAL。上层 S5.1 真实派发出口保留未勾选；S5.2/S5.3 已补最小协调与持久化对账，真实 reset/READY/投递继续待实施。
- [ ] S5.2 实现 fresh 重置、角色重新注入和新绑定验证；persistent 保留会话；busy 或存在草稿时正确排队。
  - [x] S5.2 最小 API 范围：SessionCoordinator 与可注入 ResetAdapter/SubmitAdapter；先保存消息和冻结模式，再检查 busy/草稿/未完成输入；RESET_REQUESTED 提交后才 reset，新 native 与独立 READY 同时匹配后原子推进 generation/current 绑定和 delivery。persistent 沿用绑定，send 不调用 adapter。重开、重复 request_id、旧代际和未知结果均有测试；未 reset 的队列项按顺序显式推进，requested/unknown 不重放。assign 增加兼容的 --session/--mode，默认冻结配置；无 adapter 明确 QUEUED/UNIMPLEMENTED。runtime/store/CLI/e2e 与指定工程检查见 [S5.2 结果](../evidence/S5.2-result.md)。
   - 原生 adapter、三工具实际重置与 READY/正文接收尚未实现或验证；原 S5.2 阶段出口保持未勾选。S5.3 增加下面的对账决策，自动恢复留给 S7。
- [ ] S5.3 覆盖发送重复、响应丢失、重置中断和旧代际迟到；恢复时先对账，不能重复清空或盲目重放。
   - [x] S5.3 最小持久化范围：Store.ReconcileSession、SessionCoordinator/MessageService.Reconcile 与决策查询 Resume；Advance 在原生动作前先对账。五表/journal 读取 delivery/reset/submit/current seat/stop intent，持久化稳定状态与幂等凭据；未开始动作的 QUEUED 项原子重定位，READY/请求阶段/已确认投递不重放，迟到历史代际 ACK/结果追加拒绝审计。最小 reconcile/resume CLI 明确 executed=false，退出 0 仅代表决策已持久化。store/runtime/CLI/e2e 覆盖响应丢失、请求后中断、重开、并发、回滚、代际拒绝与不启动工具，见 [S5.3 结果](../evidence/S5.3-result.md)。
   - 原生证据采集、真实工具重置/提交恢复和 S7 自动重试/退避未实现；S5.3 上层真实故障恢复出口保持未勾选。

产物：消息 CLI、投递与重置协调、三工具通信验证、S5 结果报告。

检查：两个连续工作在 fresh 下使用不同干净会话；同一工作补充仍在当前会话；persistent 下会话保持。结果回复能进入主 agent 的可见会话。

出口：无用户侧工作 ID、领取凭证或 task accept 操作；已保存、已投递、已确认的状态准确可追踪。

## 9. S6：文件上下文与实际协作

- [x] S6.1 建立按 run/agent 隔离的上下文目录与版本化角色协议；主 agent 通过绑定 API 发布精简背景、决策、进度及未完成事项。不可变 revision、manifest 提交点、0600/0700、SHA-256 与严格读回校验；双 run/双 agent 和故障测试见 [S6.1 结果](../evidence/S6.1-result.md)、[局部实施计划](2026-11-03-s6.1-context-plan.md) 与 [context API](../../internal/context/README.md)。真实工具注入保持未验证。
- [ ] S6.2 执行者读取必要工作包并写独立报告；主 agent 核实、提炼合并，详细证据通过路径引用。
  - [x] S6.2 最小 API 范围：版本化 Report、私有证据/报告的 0600/0700 无覆盖原子发布、manifest/SHA 校验、runtime 当前身份/可选派发关联事务校验、主 agent Review/Merge 新 revision。保留 agent_claim，不生成 PASS，不复制证据/消息正文；重复 report/request/revision 返回 ErrExists。context/runtime/store 与双 run/多 agent/独立数据库/新测试进程测试见 [S6.2 结果](../evidence/S6.2-result.md)、[局部实施计划](2026-11-03-s6.2-report-plan.md) 和 [context API](../../internal/context/README.md)。
  - 真实执行者工作包注入、功能验收与可见协作仍未验证；S6.2 上层真实协作出口保持未勾选。
- [ ] S6.3 完成至少两轮真实分工；比较 fresh/persistent 模式；证明主 agent 或执行者重建会话后可通过文件继续。
  - [x] S6.3 最小基础：当前 run/agent/generation 的 Bootstrap/Rebuild API 和 CLI，冻结角色/context/store 严格绑定，最新 revision/报告/证据验证，私有内容寻址恢复包与 stable retry；fresh/persistent 仅输出建议，executed=false、runtime communication UNVERIFIED。关闭重开、新进程、双 run/agent、旧代际、报告合并后继续和损坏 manifest 测试见 [S6.3 结果](../evidence/S6.3-result.md)、[局部实施计划](2026-11-03-s6.3-bootstrap-plan.md)。
  - 原生会话创建/注入/独立 READY、两轮真实协作与 S7 自动恢复未实施；本项真实出口保持未勾选。

产物：上下文模板、独立报告、演示产物及证据、S6 结果报告。

检查：工作前后无需复制全部聊天；执行者互不覆盖共享摘要；完成验证并回报后等待下一次分配；主 agent 自行检查并继续。

出口：三工具可以实际协作，文件足以恢复关键上下文；没有强制任务验收流程。

## 10. S7：自主恢复、主动停止与最终验收

- [ ] S7.1 实现可恢复错误的持续退避、等待依赖与自动继续；包装器可恢复协调进程；默认不切换工具、模型或账号。
  - [x] 本轮授权的最小基础：RecoveryPolicy/RecoveryRunner 显式 tick、默认 3 次有限退避（1s/5s/30s）、分类/原因/next_attempt_at、journal 幂等凭据与跨重开冻结策略；到期独占领取，依赖明确允许后复用 Reconcile/输入/动作门禁，单次最多一个 reset 或正文动作。
  - [x] 注入时钟/退避的单元测试覆盖连续失败、依赖恢复、取消、stop、权限/配置拒绝、最大尝试、未知结果、旧代际、独立数据库句柄并发、崩溃遗留领取、事务回滚与数据库重开。详见 [S7.1 结果](../evidence/S7.1-result.md)、[局部实施计划](2026-11-03-s7.1-recovery-plan.md)。
  - 本轮按明确授权采用有限策略，次数耗尽只停止自动推进并保留派发，不终结业务工作流；原持续等待/自动调度、包装器恢复协调进程、真实 adapter 接线和工具恢复均未实施，S7.1 原阶段出口保持未勾选。S6.3 Bootstrap 仍仅生成文件恢复包。
- [ ] S7.2 实现原会话优先恢复、检查点继续及主动关闭不复活；覆盖 cmux ID 变化、部分启动、事件历史过期和未知投递。
  - [x] 本轮最小 API：SessionRecovery / BindingRecoveryAdapter / store.BindingRecovery；持久领取后先检查原 native session/thread，可用时保持 generation，位置变化经独立 READY 后重绑定并保留历史；明确不可用时组合 S5.3 对账、S6.3 文件检查点、CREATE_REQUESTED 和新 READY 原子推进下一 generation。
  - [x] LAUNCH_REQUESTED/LAUNCH_BOUND 跨重开保留部分启动；主动关闭/终态不复活，未知退出/历史缺失/未知动作转人工对账；旧位置与代际事件拒绝，S5/S7.1 与迟到启动 READY 共享门禁。
  - [x] fake adapter/clock、SQLite 重开/独立句柄并发、预算冻结/不抢占、未知创建/READY、历史/事务回滚与 stop/close 竞争测试，README 和局部计划/结果文档。见 [S7.2 结果](../evidence/S7.2-result.md)、[局部计划](2026-11-03-s7.2-session-recovery-plan.md)。
  - 原阶段出口继续待验：真实 native/cmux 接线、事件游标协议与真实检查点注入未验证；S7.3 本地完整故障矩阵已通过，三工具最终验收仍未通过。
- [ ] S7.3 执行完整故障矩阵、三工具 e2e 和 check，补齐使用文档、能力矩阵与最终结果报告。
  - [x] S7.3 本轮本地范围：新增 30 个表驱动故障场景，复用 S0–S7.2 深入回归，覆盖配置/布局、快照/权限、SQLite/IPC/锁、重复 request、旧代际与迟到事件、busy/草稿、未知 reset/submit、stop/恢复预算、context/report/bootstrap 损坏及跨 run/agent 隔离；核对错误/决策、正文保留、事务/journal、幂等及无重复动作。
  - [x] 全量 go test/build/make check/tidy 与 runtime/store/context/IPC/lock 完整 race PASS；README、docs/README、S1 能力矩阵与 [最终报告](../evidence/S7.3-result.md) 已同步；见 [局部计划](2026-11-03-s7.3-final-matrix-plan.md)。未发现需要修改生产代码的缺陷，无新增依赖/服务。
  - 三工具真实 native/cmux e2e、权限生效、现场 attach/stop 与真实恢复保留 PARTIAL/UNVERIFIED；本地 PASS 不勾选原 S7.3 整体出口。

产物：首版 CLI、使用说明、S7 结果报告和证据。

检查：可恢复故障不会因为固定重试次数结束工作流；依赖恢复后自动推进；同一工作不会因故障从头重做；主动停止被尊重。

出口：三个工具均通过完整标准，才能宣布 v1 完成。未覆盖或失败项明确保留，不能用文档检查替代实际通信验收。

## 11. 故障与验收矩阵

| 用例 | 预期行为 | 阶段 | 结果 |
|---|---|---|---|
| F01 配置重复键或布局非法 | 创建前失败，定位字段 | S2 | 已通过配置/布局单元与 CLI 进程测试 |
| F02 YAML/JSON 与覆盖顺序 | 规范化等价且工具解析正确 | S2 | 已通过示例计划等价与 CLI 覆盖测试 |
| F03 三工具空闲接收 | 原生可见会话实际收到并确认 | S1/S5 | PARTIAL：S1 部分历史受控 ACK，完整三工具与产品适配器未验证 |
| F04 三工具最高权限 | 报告真实有效权限 | S1/S4 | PARTIAL：Codex 历史受控权限证据保留，OpenCode/Claude 与产品默认权限未验证 |
| F05 fresh 连续分派 | 每项新工作使用干净会话 | S5 | fake 连续工作/新代际 PASS；真实工具未验证 |
| F06 persistent 与补充消息 | 会话保留，补充不重置 | S5 | 本地保留原绑定、send 不 reset PASS；真实产品投递未验证 |
| F07 用户输入草稿或忙碌 | 保留输入，排队后继续 | S1/S5 | fake 本地门禁 PASS；S1 Codex 原生 queue 历史受控 PASS；通用三工具门禁未验证 |
| F08 重置后绑定与身份 | 角色重新注入，旧代际被拒绝 | S5 | 本地冻结角色/READY/代际保护 PASS；S1 受控 reset 证据保留，产品接线未验证 |
| F09 重置命令响应丢失 | 先核对，不重复清空 | S5/S7 | S5.3 fake/store/CLI 通过：请求阶段保留 UNKNOWN，不重放；真实工具未验证 |
| F10 落盘后投递前崩溃 | 恢复原消息继续投递 | S3/S7 | S5.3 本地重开决策、READY 后只提交一次通过；真实恢复未验证 |
| F11 投递后记录响应前崩溃 | 按通道对账，不盲目重做 | S5/S7 | S5.3 请求中断/响应丢失重开通过：不重发正文；真实通道对账未验证 |
| F12 重复 request_id | 同正文复用，冲突报错 | S3 | Store/IPC 重开、多句柄并发、提交后响应丢失本地 PASS；真实传输接线未验证 |
| F13 协调进程并发与退出 | 唯一实例，异常后自动恢复 | S3/S7 | 锁/数据库/恢复领取竞争本地 PASS；运行中协调进程与包装器自动恢复未验证 |
| F14 网络、限流或依赖失联 | 退避等待，恢复后继续 | S7 | fake 依赖/有限预算 PASS；真实限流/网络/自动调度未验证 |
| F15 同一工作会话崩溃 | 优先恢复原会话和进度 | S7 | fake 原会话优先/重开 PASS；S1 部分历史手动恢复保留，产品真实恢复未验证 |
| F16 原会话不可恢复 | 核对文件产物后继续剩余部分 | S6/S7 | 本地检查点/新代际 READY PASS；真实注入/继续交付未验证 |
| F17 主动退出或关闭 pane | 停止成员、通知主 agent、不复活 | S1/S7 | 本地 stop/关闭/终态门禁 PASS；现场事件/通知/关闭未接线 |
| F18 cmux 重启或事件游标过期 | 核对稳定身份和账本恢复 | S7 | fake ID 重绑定/历史缺失转人工对账 PASS；真实 cmux 重启/游标协议未验证 |
| F19 完成后待命 | 报告含验证证据，等待下一次分派 | S6 | 本地独立报告/合并及工作释放 PASS；三工具真实两轮协作未验证 |
| F20 配置编辑及账号来源 | 当前用快照，恢复不擅自换来源 | S2/S7 | 本地冻结配置/角色/摘要校验 PASS；真实提供商/账号有效状态未验证 |
| F21 多 run 与不同 cwd | 会话、上下文、消息不串线 | S1/S7 | 本地双 run/agent/独立数据库及跨进程 PASS；S1 历史受控隔离保留，产品现场未验证 |
| F22 部分创建或创建响应丢失 | 查证原现场，不重复建团队 | S4/S7 | fake LAUNCH/CREATE 请求持久化、原绑定核对与未知不重放 PASS；真实团队未验证 |

## 12. 阶段报告模板

```markdown
# Sn 实施结果
状态：PASS / PARTIAL / WAITING_DEPENDENCY / BLOCKED
完成项：Sn.1、Sn.2、Sn.3 中实际验证完成的项目
产物：文件路径
验证：命令、预期、实际结果、退出码
证据：run、原生 session、消息 ID 与必要日志路径
未解决事项：原因、自动恢复方案或所需外部输入
约定变化：依据及同步修改的文档
下一步：阶段出口通过后进入下一阶段，否则继续本阶段
```

各阶段以对应结果报告的实际验收为准。S7.3 本地交付已收口；后续补齐 S1 原生能力与 S4–S7 产品运行接线及现场证据。安全 probe、fake 和本地测试不证明完整三工具接入、权限、通信或恢复，也不能据此声明首版完成。
