# AgentFlow 文档入口

当前状态（2026-11-03）：S0–S7.2 的本地基础实现与 S7.3 代码侧完整故障矩阵已验证，新增 30 个表驱动场景、全量 test/build/check/tidy 及核心 race 均 PASS。产品整体保持 PARTIAL；真实 native/cmux 三工具 e2e、有效权限、现场 attach/stop、真实上下文注入与恢复仍 PARTIAL/UNVERIFIED。S1 历史受控原生 PASS 保留各自环境与动作边界，本轮没有新增原生通过项。

| 文档 | 用途 |
|---|---|
| [访谈决策](plans/2026-11-02-agentflow-interview-decisions.md) | 已确认需求和取舍，是后续文档的需求依据 |
| [架构设计](plans/2026-11-02-agentflow-design.md) | 产品边界、通信模式、会话策略和文件上下文 |
| [技术实现文档](plans/2026-11-02-agentflow-technical-spec.md) | 模块、配置、五张表、IPC、适配器及恢复规则 |
| [分步实施计划](plans/2026-11-02-agentflow-implementation-plan.md) | S0–S7 分步实施、局部交付与真实阶段出口、22 项验收场景 |
| [Go 工程使用说明](../README.md) | 构建、格式、测试、check、CLI 范围和退出码 |
| [S5 thin coordinator](../internal/coordinator/README.md) / [设计](plans/2026-11-03-s5-thin-coordinator-design.md) / [结果](evidence/s5-thin-coordinator-result.md) | 已有 run 的单实例前台 IPC、消息/stop 转发、代际与共享幂等；本地验证 PASS，原生能力 PARTIAL |
| [单 OpenCode 真实 up](evidence/s4-real-up-single-seat-3117cc8e/README.md) / [设计](plans/2026-11-03-s4-real-up-single-seat-design.md) | 先持锁、私有 snapshot/context、exact JSONL/nonce READY、同锁 coordinator IPC；一次现场验证 PASS，整体能力 PARTIAL |
| [Workflow 配置与冻结](../internal/config/README.md#可选-workflow-配置与冻结) / [loop-core](../internal/loopcore/README.md) / [IPC 接线](../internal/coordinator/README.md#workflow-event-flow) / [使用示例](../examples/approval.yaml) | `build_review`、逐步指定审批人并允许有限前置退回的 `approval`、动态长度 `pipeline`；共用纯 reducer 与原子 reply/派发/cursor journal |
| [可注入 Workflow runner](../internal/runtime/README.md#workflow-runner) / [fake e2e 结果](evidence/workflow-runner-fake-e2e-result.md) | 显式注入 adapter 后同步消费新事件 outbox，三角色两步 fake IPC 完成；未知/失败/stop/旧代际不自动重放，默认 CLI 与真实 native 能力仍 PARTIAL |
| [CMUX OpenCode 结果采集修复](evidence/cmux-opencode-result-collection-fix-20261106.md) | 长 session export 截断导致工作流死等；改为定向只读 session DB 查询与唯一 dispatch ID 关联；本地全量检查通过，已运行旧 run 不自动重放 |
| [cmux 输入 ownership 调查](evidence/cmux-input-ownership-guard-2026-11-04.md) | cmux 0.65.2 实时 API 与同版本源码核对；exactly-once/屏幕检查不构成独占 lease，真实 NativeSubmitGuard 保持 UNIMPLEMENTED |
| [S0 结果报告](evidence/S0-result.md) | 实际执行的命令、结果、证据与未解决项 |
| [S1 结果报告](evidence/S1-result.md) / [能力矩阵](evidence/S1-capability-matrix.md) | 三工具真实版本、只读检查与运行时未验证项 |
| [S2 结果报告](evidence/S2-result.md) | 配置、布局与 validate/plan/doctor CLI 的验证及后续阶段 |
| [S6.1 结果报告](evidence/S6.1-result.md) / [上下文 API](../internal/context/README.md) / [局部实施计划](plans/2026-11-03-s6.1-context-plan.md) | run/agent 私有上下文、角色协议、manifest 与隔离/故障测试；S6 整体保持 PARTIAL |
| [S6.2 结果报告](evidence/S6.2-result.md) / [报告实施计划](plans/2026-11-03-s6.2-report-plan.md) | 独立报告、证据校验、当前代际门禁与主 agent 精简合并；跨进程/独立数据库验证，真实协作仍为 PARTIAL |
| [S6.3 结果报告](evidence/S6.3-result.md) / [恢复包实施计划](plans/2026-11-03-s6.3-bootstrap-plan.md) | 当前代际 Bootstrap/Rebuild API 与 CLI、幂等文件恢复包、fresh/persistent 和跨进程继续；真实 native 会话仍为 UNVERIFIED |
| [S7.1 结果报告](evidence/S7.1-result.md) / [有限恢复实施计划](plans/2026-11-03-s7.1-recovery-plan.md) | 有限退避、依赖门禁、持久恢复凭据与取消/stop/未知结果测试；S7 整体 PARTIAL |
| [S7.2 结果报告](evidence/S7.2-result.md) / [会话恢复实施计划](plans/2026-11-03-s7.2-session-recovery-plan.md) | 原会话优先、READY 重绑定、检查点新代际、部分启动及主动关闭门禁；fake 验证，S7 整体 PARTIAL |
| [S7.3 最终报告](evidence/S7.3-result.md) / [本地矩阵实施计划](plans/2026-11-03-s7.3-final-matrix-plan.md) | 本地故障矩阵 PASS、复现命令、最终能力/风险/未完成项；真实三工具最终出口仍 PARTIAL/UNVERIFIED |
| [probe 使用说明](../probes/README.md) | 独立检查、结构化证据、退出码和临时会话实验计划 |
| [YAML 示例](../examples/team.yaml) / [JSON 示例](../examples/team.json) | 等价的三工具嵌套分屏配置 |
| [架构说明草稿](agentflow-explainer.md) | 可视化页面的 Markdown 内容 |
| [实施路线草稿](agentflow-implementation-explainer.md) | 可视化实施页面的 Markdown 内容 |

先读 S7.3 最终报告了解交付范围，再按访谈决策与实施计划核对需求。后续工作是补齐 S1 真实工具能力、S4 launcher/适配器/READY IPC 接线及 S5–S7 现场证据；本地测试通过不关闭这些阶段出口。
