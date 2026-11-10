# Store（S3.1）

CMUX workflow transport adds no schema: `BeginCMUXDispatch` claims the existing
current workflow outbox under sender/recipient launch binding, generation, cursor,
stop and recovery gates. It saves `CMUX_SEND_REQUESTED` before native work and never
reclaims a prior attempt. `ReplyCMUXWorkflow` validates the accepted inbox evidence,
exact native text, source name, source/target UUIDs, actor/current generation,
dispatch queue receipt and terminal status, then reuses the workflow transaction
for reply, next outbox, cursor and `CMUX_WORKFLOW_APPLIED`. A failure rolls back
all workflow writes. Plain inbox receipts remain transport evidence only. See
[workflow transport result](../../docs/evidence/cmux-workflow-transport-v1-result.md).

`store` 使用现有 `modernc.org/sqlite`，只读写调用者指定的数据库；不创建运行目录、不执行工具或通信操作。

```go
s, err := store.Open(path) // 父目录必须已存在；只接受文件路径
if err != nil {
    return err
}
defer s.Close()
if err := s.Migrate(ctx); err != nil {
    return err
}
```

`Open` 对每个物理连接设置 `foreign_keys=ON`、`journal_mode=WAL`、`synchronous=FULL`、5 秒 busy timeout，并检查实际持久化设置。池最多保留一个连接；连接替换及重新打开均重新设置 PRAGMA。事务使用 `BEGIN IMMEDIATE`，多个 Store 句柄写同库时，幂等检查与写入串行化。这属于 SQLite 事务保护；运行锁仍由后续运行时实现。

`Migrate(ctx)` 显式执行；DDL、索引、触发器和 `user_version=1` 在一个事务内提交。重复迁移保留现有数据，未知较新版本被拒绝。数据库只有五张业务表；SQLite 为递增序号维护的 `sqlite_sequence` 是内部表。

- `CreateRun` / `GetRun`：每库一个 run；保存完整配置快照文本、调用者提供的摘要、workspace、启动阶段及停止意图。摘要沿用 config.Resolve 的契约，store 不重新计算或解释配置。
- `CreateSeat` / `GetSeat`：保存 tool、native session/thread ID、workspace、surface、generation、状态和当前派发引用。每个角色最多一个 current generation，可以保存多个历史 generation；S5.2 增加下述事务绑定切换 API。
- `SaveOutbox(ctx, Outbox) (Message, error)`：一个事务创建 message、初始 delivery 与 journal，提交成功后才返回消息。空消息 ID 自动生成；seq 为数据库递增序号。默认 message status=`STORED`、delivery phase=`PENDING`、reset phase=`NONE`、journal event=`MESSAGE_STORED`。delivery/journal 的 run、message 链接从 Message 派生，delivery recipient 也从 Message 派生；创建时间统一使用 Message.CreatedAt（零值为当前时间）。
- `GetMessage` / `GetDelivery`：读回持久记录；不存在时返回 `sql.ErrNoRows`。
- `AppendJournal` / `JournalAfter`：追加及按 seq 游标读取事件；包含事件细节和证据引用。没有 journal 更新或删除 API，数据库触发器也拒绝 UPDATE/DELETE。

非空 `RequestID` 的幂等范围为 `(run_id, sender, request_id)`，同一正文逐字相同则返回原始 Message，不新增 delivery/journal，也不改变原 ID、接收者、kind、reply 或报告状态。不同正文返回 `ErrIdempotencyConflict`，可用 `errors.Is` 识别，稳定错误文本为 `IDEMPOTENCY_CONFLICT`。空 RequestID 保存为 NULL，不参与 request 去重；消息 ID 仍唯一。S5.1 的 reply 使用同一 outbox；ACK 请求凭据保存在 journal，并与消息共享非空 sender/request_id 命名空间。

外键核对 run、消息回复、发送方绑定（提供 SenderGeneration 时）、delivery 的消息/接收者/目标 generation，以及 journal 的消息/seat 引用。外部用户可使用无 seat 绑定的 sender，`SenderGeneration=0` 表示未提供；actor 身份认证、当前代际校验与状态转换由后续 S3.2 协调入口负责。存储层允许引用历史绑定，不将存在性检查误当成当前身份认证。

所有业务数据通过 SQL 参数绑定；时间写为固定宽度 UTC RFC3339 纳秒文本，读取为 `time.Time`。`NextRetryAt` 零值保存为 NULL，其余错误、channel、reset 阶段字段为后续恢复保留。成功只表示已持久保存，不能据此声称已经投递或执行。

验证：`go test ./internal/store` 使用 `t.TempDir()`，覆盖迁移/索引/约束、PRAGMA 及连接替换、落盘重开、各写入阶段回滚、request 幂等/并发重试、回复、外键与 journal 追加顺序；不依赖用户目录、网络或 sqlitecheck。

## S5.1 validated messaging

`StoreMessage(ctx, MessageCommand)` 与 `SaveOutbox` 共用私有事务写入函数。在同一事务中检查 run、current sender/recipient generation、请求幂等和 reply 权限，再提交 message/delivery/journal。`MessageActor` 必须是当前 seat；`TargetGeneration=0` 原子解析当前接收者，非零必须匹配。重试保留原路由并核对原双方代际；已过期的请求不能换代际继续发送。reply 仅允许原 recipient/target generation，回复原 sender/sender generation；缺失原消息和越权分别返回 `MESSAGE_NOT_FOUND`、`ACTOR_MISMATCH`，过期代际返回 `GENERATION_MISMATCH`。新记录保持 STORED/PENDING、channel=UNVERIFIED、attempt=0、reset_phase=NONE。

`Inbox(ctx, InboxQuery)` 只读事务校验身份并按 seq 返回当前 recipient generation 的记录；支持 After 游标与 Pending（排除 ACKED），空结果为非 nil 切片。读取不触发确认、投递或尝试次数更新。

`Ack(ctx, actor, requestID, messageID)` 在事务中核对 caller 是消息的目标 recipient generation，将 message.status/delivery.phase 标为 ACKED，并追加 `MESSAGE_ACKED`。journal.details 用 JSON 保存 request_id；不同 key 对同一消息的重复确认追加 `MESSAGE_ACK_RECORDED` 请求凭据，避免该 key 被用于别的操作。相同 key 重试不追加任何事件，所有重复确认都返回首次 MESSAGE_ACKED 的序号/时间。ACK 与消息写入都在 BEGIN IMMEDIATE 下核对消息索引和 journal 凭据，无新表或迁移。ACK 不修改 attempt、channel、seat 或工作状态。

`MessagingError{Code,Path,Message}` 为固定公共诊断；原始 SQL 错误由 CLI 输出固定失败诊断。低层 SaveOutbox/CreateSeat 仍保留原始存储能力，服务调用必须使用以上校验入口；本轮本地 CLI 直接打开已有库，不提供已认证 IPC coordinator。服务只核对持久身份，不能证明原生工具通信。

## S5.2 session transactions

`MessageCommand.Session` 为 fresh/persistent/config 时启用协调派发；config 在同一事务内读取 runs 中冻结的 recipient session。选择保存在 MESSAGE_STORED.details JSON，复用五张表与 schema v1。`MessageRecord.Session/Coordination` 返回模式及 delivery.last_error_code。空 Session 保留底层 S5.1 持久化契约；MessageService 的 Assign 始终使用协调入口。协调派发重试直接读回已保存 delivery，允许接收者已换代，但 caller 必须仍为原 current sender；旧代际 ACK/reply 仍拒绝。

`SessionWork` 在同一视图读取消息、delivery 和当前 seat。`BeginSessionAction` 在 BEGIN IMMEDIATE 事务中检查已保存阶段、观察到的 native/generation、busy/current_message_id、先前排队派发和停止意图；只有返回 claimed=true 的调用者可以执行原生动作。排队保存 QUEUED 和固定原因码，不增加 attempt；完全相同的重试不写入。RESET_REQUESTED 先于 reset，SUBMIT_REQUESTED 先于正文调用；attempt 只统计 submit 调用次数。尚未 reset 的队列项可重新定位当前 generation，并追加队列事件。

`ConfirmSessionReset` 由 runtime 在新 native 与 READY 双重核对后调用：拒绝无 intent、旧代际、重复 READY、相同/历史 native、其他 workspace/surface；原子保留旧 seat、创建 generation+1 的 current READY seat，并把原 delivery 目标改为新代际、reset_phase=READY。任何 seat/delivery/journal 失败全部回滚。`NoteSessionUnknown` 只记录 UNVERIFIED 与追加事件，保留原请求阶段。未 READY 的 fresh 派发不能 ACK 或 reply。

`ConfirmSessionSubmit` 原子设置 SENT/channel、seat BUSY/current_message_id 和 MESSAGE_SENT；消息仍保留 STORED 直到显式 ACK。精确关联当前工作的 DONE/FAILED/BLOCKED_CONFIRM reply 清除 current_message_id 并设 READY；ACK 和非终态报告不释放 seat，幂等 reply 使用首次保存的报告状态。所有 requested/unknown 阶段通过 S5.3 输出下述对账决策，当前没有重置重放、正文重发或自动恢复。

## S5.3 reconciliation receipts

`ReconcileSession(ctx, originalSender MessageActor, messageID) (RecoveryDecision, error)` 在 BEGIN IMMEDIATE 事务中核对原 sender 当前身份，读取消息、delivery、当前 seat、stop intent 与原生动作 journal。决策状态包括 SAFE_TO_CONTINUE、QUEUED、UNKNOWN_NEEDS_RECONCILE、REJECTED_OLD_GENERATION、SENT、ACKED。next_action 仅为建议，不授予动作执行权；未知阶段保留原 delivery，不推测原生动作失败。已完成 SENT/ACKED 有匹配 journal 时直接返回已确认状态，即使当前 seat 已换代也不重复执行。

只有协调派发处于 QUEUED、reset NONE、attempt=0 且 journal 没有 reset/submit/sent/acked 证据时，才可在无 stop intent 时更新目标到当前代际，并追加 SESSION_RELOCATED；已 READY 或开始动作的目标不移动。移动和 SESSION_RECONCILED 在同一事务内，journal 失败会一起回滚。决策内容保存在 SESSION_RECONCILED.details，返回 journal_seq/recorded_at；只比较上一次决策内容，证据相同复用原凭据，状态 A→B→A 会产生新凭据。数据库重开和多句柄并发具有相同幂等契约。schema 仍为 v1，业务表仍为五张。

BeginSessionAction 同时检查历史原生请求 journal 和 READY 确认，避免仅改 delivery 阶段而重新授权已开始的动作。历史 generation 的 ACK/reply/reset/submit 确认失败时，在同一事务中追加去重的 SESSION_RESULT_REJECTED 后返回原身份错误；拒绝日志不包含正文或 request ID，也不创建结果消息。底层 AppendJournal 等存储原语仍要求可信调用者，真实 IPC 身份认证和原生证据采集不在该 API 内。

## S6 report/bootstrap transaction fence

`WithReportContext(ctx, ReportContext, callback)` 和 `WithBootstrapContext(ctx, ReportContext, callback)` 共用 immediate 事务内的当前 actor、冻结 lead/digest 和可选报告派发关联验证。后者还把同一事务读到的 `Run/Seat` 传给回调，供 runtime 验证完整冻结配置、context 目录/角色协议并生成恢复包。回调中的 `check(ReportBinding)` 核对当前 author、可选 message/request 的 lead 派发、目标代际和 fresh READY；只有 lead 的 Review 模式可以核对其他作者。

文件校验和发布期间，跨数据库连接的 generation 切换被事务阻塞。回调不得再调用 Store 方法或原生工具。无 native 身份、非 READY seat 或 stop intent 不妨碍读取恢复建议，实际执行仍由独立 runtime 门禁负责。两个入口均不写 seat/message/delivery/ACK/journal；文件发布可能先于 SQLite commit 失败，调用方应验证读回已有内容。`bootstrap_test.go` 覆盖 unavailable seat/stop intent 暴露、跨句柄代际互斥和失效身份拒绝。

## Workflow session dispatch

Workflow runners can use `AdvanceWorkflowWithSessions` / `ReplyWorkflowWithSessions` to select the frozen recipient session policy atomically with the existing workflow transaction. The policy is stored in `WORKFLOW_DISPATCH_STORED` details and read by `SessionWork`; persistence-only workflow APIs retain their original behavior. Workflow assignments never use ordinary untouched-queue generation relocation. Their reset/submit claim also checks the current cursor/active phase and exact target generation inside the same transaction. Native submission and event replay behavior are owned by the [runtime runner](../runtime/README.md#workflow-runner), with no new schema or table.

## S7.2 durable binding recovery

`ReserveBindingRecovery` / `FinishBindingRecovery` 使用现有 journal 保存 `BindingRecovery`：逻辑 agent/generation/request、前后绑定、冻结配置摘要、首次有限策略、已用次数、下一时刻、状态/原因及 journal 序号/时间。领取和 CAS 均使用 immediate 事务；同代际首次策略与已用预算跨 request 保留，活动 WAITING/ATTEMPT_RESERVED/CREATE_REQUESTED/人工对账不能被另一 request 抢占。中断后的领取与创建意图不能自动重放。

恢复领取及结果提交复用 S5.3 全部目标消息的账本对账，读取 S7.1 的耗尽、终止、人工对账和在途领取门禁，并重新核对 stop/退出/当前完整绑定。同 generation 只能移动 workspace/surface，native session 必须保持；generation+1 必须有 CREATE_REQUESTED 且新 native 未被其他代际使用。当前 active work 需要人工核对后才允许替换。位置/代际切换和 BINDING_CHANGED（完整前后绑定）/完成凭据原子提交；同代际旧位置保留在 append-only 历史中。

`RecordStartupLaunch` 在无注册 seat 时以 run-level LAUNCH_REQUESTED/LAUNCH_BOUND JSON 保留逻辑身份，`RecoverySeat` 优先读取 current seat，缺失时读取部分启动记录。`RegisterStartupReady` 拒绝 stop、关闭或未决恢复；未 READY 不创建 seat。`RecordBindingExit` 要求完整当前 native 绑定，用户关闭与已确认终态保持黏性，未知原因保存 EXIT_UNVERIFIED。旧 native 位置/代际事件写一次 BINDING_EVENT_REJECTED 并返回错误。S5 动作门禁/确认与 S7.1 对账共享退出/恢复门禁。schema 仍为 v1，无新增表或依赖；native 证据/READY 和 transport 认证由 runtime/接入方负责。见 [S7.2 结果](../../docs/evidence/S7.2-result.md)。
