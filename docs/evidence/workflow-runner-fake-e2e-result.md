# Workflow runner fake e2e

日期：2026-11-03。任务：`workflow-runner-fake-e2e`。

局部结果：可注入 workflow runner 和 fake IPC 集成验证 PASS。真实 native 通信保持 UNVERIFIED，产品能力保持 PARTIAL。

## 实现与验证边界

`runtime.NewWorkflowRunner(SessionCoordinator)` 复用现有 workflow reducer、reply/outbox/cursor 原子事务、输入检查及 reset/submit 持久门禁。`coordinator.OpenWithAdapters` 为同一前台 coordinator 的显式注入入口。新 dispatch 的 frozen session mode 保存到原 journal；无 schema 迁移、新表或依赖。

每次新事件提交后只同步消费它产生的一个 outbox 项。事件失败不调用 adapter；提交动作失败保留已提交的 workflow receipt，供明确对账。普通 DONE/ACK 不推进验收，reviewer 必须显式给出逐项 accepted。reviewer approve 是 DECIDE 语义，不添加额外阶段或 agent。

完整测试用临时 SQLite、真实本地 Unix IPC 与 fake Inspect/Submit，三个 registered persistent seats。fake 角色仅消费 Submit 实际收到的消息，再通过原 IPC reply 发送 handoff / approve。测试不扫描 inbox、不补调用 assign/SessionCoordinator.Advance，不启动 native 工具。

| 次序 | 派发 | 角色 | 成功事件 / 决策 |
|---|---|---|---|
| 1 | implement execute | builder | 正式 handoff，包含 summary/evidence/next |
| 2 | implement review | reviewer | 两项 accepted=true，进入下一步 |
| 3 | verify execute | reviewer | 正式 handoff |
| 4 | verify review | main | accepted=true，COMPLETED |

start 加四条结构化 reply 产生 revision 5。四条 delivery 均为 `SENT/attempt=1/channel=fake-workflow`；未自动 ACK。reviewer 在相邻两条派发之间由终态 reply 释放 BUSY，随后接收下一步执行工作。

## 故障与幂等

- 完成后重启 coordinator，重放 start 及全部 reply：保持原 receipt，adapter 仍共调用四次，消息/delivery/journal 无新增写入。
- 丢失 submit 响应、拒绝提交、错 generation 证据：保留 `SUBMIT_REQUESTED`，重启及同事件重试不重复提交。
- 草稿、缺少 adapter：保留 QUEUED；重启时安装可用 adapter 也不扫描或重试历史 outbox。
- 配置错误导致 dispatch 失败：workflow receipt 保留，返回 `WORKFLOW_DISPATCH_FAILED`；重试事件不隐式恢复。
- 错角色、旧 revision、缺失 handoff：原子拒绝，不新增 reply、cursor 或 adapter 调用；普通 DONE 不推进。
- stop 后重启、重放 start 和提交新 handoff：只允许历史 receipt，新事件拒绝；adapter 无额外调用。
- Inspect 期间另一个数据库连接写 stop：事件已提交，但动作门禁保持 QUEUED/STOP_REQUESTED，无 Submit。
- 八条并发相同 start：一个 outbox，一次 adapter 提交。
- 已排队 workflow 更换目标 generation 后对账：保持原 target generation，REJECTED_OLD_GENERATION；动作领取拒绝，不迁移。
- cursor 暂停后尝试领取旧 dispatch：事务门禁拒绝。
- outbox message / delivery / dispatch journal / cursor journal 注入失败：整笔新事件回滚。

## 复现

本轮实际执行结果（全部退出 0）：

| 检查 | 结果 |
|---|---|
| Workflow 定向测试 | PASS，coordinator/store |
| coordinator/runtime/store 全包测试 | PASS |
| `make check` | PASS，格式、依赖 verify、vet、全部测试及 CLI build |
| coordinator/runtime/store race | PASS，无竞态报告 |

```sh
go test ./internal/coordinator ./internal/store -run Workflow -count=1
go test ./internal/coordinator ./internal/runtime ./internal/store -count=1
make check
CGO_ENABLED=1 go test -race ./internal/coordinator ./internal/runtime ./internal/store -count=1
```

## 能力限制

默认 `Open`、CLI 与单 seat real up 未安装 runner/adapters。开启注入入口也必须显式 start；打开或重启不会自动 drain 历史 outbox。缺少后台服务、任务表、MQ、DAG、native adapter、workflow CLI 与 native 事件采集。事件认证、原生输入保护及实际工具实现仍由未来接入方负责。

本测试证明 persistent fake 会话的两步自动派发。fresh 仍依赖已有 reset/独立 READY 门禁；旧代际不会接管旧工作，lead 拥有未完成派发时不支持自动重置 lead 后继续。adapter 必须先返回提交凭据，再报告事件；同步重入的 workflow 回调不受支持。未知结果、失败、主动 stop、代际变化均不授权自动重放。文件证据引用和 reviewer 判断保留 agent claim 边界，测试不把它们升级为真实内容验收或三工具现场通过。
