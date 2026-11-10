# S6.1–S6.3 私有上下文、独立报告与文件恢复包

`internal/context`（建议导入别名 `runcontext`）使用 Go 标准库，保存按 run/agent 绑定的精简上下文。`runtime.CreateContext(snapshotPath, runID)` 从已有 `RunSnapshot` 创建初始文件；它读取冻结运行快照，核对 run ID、配置摘要与角色元数据，为每个 agent 发布 `initial` 版本。`prompt_file` 不被重新读取，完整 prompt 通过冻结快照路径、字段引用和 SHA-256 定位。

## 目录与 API

```text
<context_dir>/                       0700
  <run_id>/                         0700
    run.json                        0600：版本、run/path/lead/agents/config_digest
    <agent_id>/                     0700
      agent.json                    0600：版本、run/agent/path/config_digest
      <revision>/                   0700：排他创建，一次发布
        role.json                   0600
        background.json             0600
        decisions.json              0600
        progress.json               0600
        pending.json                0600
        manifest.json               0600：最后发布，是提交点
      .evidence/g-<generation>/     0700
        <evidence_id>               0600：无覆盖发布的私有证据
      .reports/g-<generation>/      0700
        .request-<request_sha256>   0600：可选派发 request 的一次性占位
        <report_id>/               0700：排他创建的报告版本
          report.json              0600：精简独立报告
          manifest.json            0600：最后发布，记录身份、路径、SHA 和时间
      .bootstrap/g-<generation>/    0700
        <content_sha256>.json       0600：不可变的精简文件恢复包
```

低层入口：`Create(base, runID, lead, configDigest, agentIDs) (*Run, error)`、`Open(base, runID) (*Run, error)`、`Run.Agent(agentID) (*Agent, error)`、`Agent.Publish(Snapshot) (*Manifest, error)` 和 `Agent.Read(revision) (*Snapshot, *Manifest, error)`。`Run.Path()` / `Agent.Path()` 只返回定位信息。调用方应使用绑定 API 执行写入。

配置根目录必须为规范绝对路径（不含 `..`、冗余分隔或 symlink）；所有已存在的祖先必须为真实目录。base 不存在时只创建最后一级，其父目录必须已存在。已有 base 和所有受管理子目录必须恰好 0700；文件必须为恰好 0600 的普通文件。公共祖先（如 `/Users`）可以存在。macOS 的 `/var` 常为 symlink，测试/调用方应先选择真实的 `/private/var/...` 路径。API 拒绝 symlink，不能依靠它定位上下文。

run/revision ID 匹配 `[A-Za-z0-9][A-Za-z0-9_-]{0,127}`；agent ID 沿用配置的 `[a-z][a-z0-9_-]{0,47}`。agent 集合不重复、排序保存，最多 128 人，lead 必须存在。跨 run/agent/config 的 Snapshot 写入被拒绝。每次操作重新校验 run 和 agent 描述，目录被搬移到另一个 run/agent 下后会报身份错误。`os.Root` 将实际操作约束在打开的目录内。

## 更新精简摘要

主 agent 维护自己目录下的四类摘要；执行者使用自己的目录。调用方可以显式发布摘要，或使用下面的报告合并 API：

```go
run, err := runtime.CreateContext("/private/run/config.resolved.json", "run-a")
if err != nil { return err }
lead, err := run.Agent("main")
if err != nil { return err }
snapshot, _, err := lead.Read("initial")
if err != nil { return err }
snapshot.Revision = "work-1"
snapshot.Background.Text = "实现按 run 隔离的文件上下文。"
snapshot.Decisions.Text = "使用不可变 revision 和 manifest 提交点。"
snapshot.Progress.Text = "单元测试已通过，等待独立审查。"
snapshot.Pending.Text = "核对集成测试结果。"
snapshot.Progress.References = []runcontext.Reference{
    {Path: "/project/docs/evidence/S6.1-result.md", Purpose: "验证证据"},
}
_, err = lead.Publish(*snapshot)
return err
```

`Protocol` 保存角色、tool/cwd/permissions/session、冻结配置摘要、prompt 摘要与引用；嵌套 `MessageProtocol` 记录 S5 v1 的身份字段 `run_id/agent_id/generation`、终态报告 `DONE/FAILED/BLOCKED_CONFIRM`，并固定 ACK 为接收凭据、报告为 agent 声明。它是行为契约，不是当前消息、代际或验收状态；这些仍以 S5 store 为准。角色正文保留在原冻结运行快照中。

四类 `Section` 仅保存必要文字和 `Reference{Path, Purpose, Fragment}`。每段最多 8192 字节、16 个引用，引用路径最多 4096 字节、用途 512 字节、字段引用 256 字节；限制用来控制上下文体积，调用方仍负责提炼内容。普通 Section 引用必须为规范绝对路径，其目标不会被自动打开。S6.2 的 Evidence 有更严格的目录和内容校验，见下节。

## 独立报告与主 agent 合并

文件层提供 `Agent.PublishEvidence(generation, name, purpose, body)`、`PublishReport(Report)`、`ReadReport(generation, reportID)`、`Review([]ReportRef)` 与 `Merge(baseRevision, newRevision, []ReportRef)`。`Run.ID/Lead/ConfigDigest` 返回冻结绑定。需要当前 seat/message 权限的调用应使用 `runtime.ReportService`；文件层只验证绑定身份、目录和产物完整性，无法自行判断哪个 generation 当前有效。

`Report` 必填 version/run_id/agent_id/generation/report_id、status、summary、evidence 和 next；status 仅支持 `DONE/FAILED/BLOCKED_CONFIRM`。summary 与 next 各最多 2048 字节、有效 UTF-8、非空；没有完整聊天或任意正文载荷字段。证据必须 1–8 项，每项为 `Evidence{Path, Purpose, SHA256}`。证据通过 PublishEvidence 在同 agent 的 `.evidence/g-N/` 发布，单份非空且最多 128 KiB；报告只接受该目录下的规范绝对路径和安全单段文件名。发布、读回和合并都逐项检查文件存在、0600、无 symlink、SHA-256 一致；跨 run/agent/generation、目录穿越、缺失和损坏均拒绝。

`message_id` 和 `request_id` 都可省略。request_id 定义为**原始 S5 派发消息的 request ID**，不是新建 reply/ACK 请求；若单独提供，runtime 在冻结 lead → 当前 author 的 assign/send 中查找；两者同时提供必须指向同一派发。关联消息必须属于同 run、目标 agent 和当前 generation，原派发者 generation 也仍有效；fresh 必须已确认 READY。报告 request 命名空间独立于 S5 写请求，不修改消息。report/request ID 使用上述安全 ID 格式，每个 author/generation 内重复标识明确返回 `ErrExists`，即使正文相同也不覆盖。不同 generation 可以沿用相同 report ID，但 runtime 拒绝历史代际读入或合并；文件层可显式读取历史产物用于审计。

Review/Merge 仅允许冻结 lead，显式选择 1–4 个不同执行者报告引用（同一执行者可选不同报告，但引用不能重复），全量验证后才返回或发布。合并从显式 baseRevision 读取原主上下文，保留角色、背景和决策，将本批精简状态、摘要、证据路径/SHA、next 和源报告 SHA 写入 progress，把 next 写入 pending，再发布 newRevision。合并后的段落仍受 8192 字节/16 引用限制，超限整体拒绝，调用方应缩小批次；旧主 revision 与报告不可变。重复 newRevision 返回 ErrExists。

验证结果固定为 `verification=INTEGRITY_VERIFIED`、`meaning=agent_claim`，status 原样保留；它证明文件完整性和来源绑定，不能代替功能验收，没有 PASS 字段或自动验收状态。证据正文、派发正文与冻结 prompt 不进入新 revision。runtime 的 SQLite immediate 事务覆盖身份核对、报告读回和文件发布，阻止跨数据库句柄/进程在其中切换代际。成功 Merge 为每份报告追加 `REPORT_MERGED` journal 凭据，绑定 lead revision、发布时的 run/agent/generation、report ID/SHA 和原始派发引用；S5 ACK、delivery 和 seat 不变。需要 S5 结果回复时继续显式使用 MessageService.Reply，并只传精简结果与报告引用。

报告也采用文件临时写入/fsync/hard-link 无覆盖发布、manifest 最后提交。request 占位在报告 manifest 前保留，竞争中只有一个报告能提交；中断或重复 request 的失败可能留下缺 manifest 的报告目录和一次性占位。调用方应显式读回已知报告再决定下一步；没有自动删除、覆盖或事务回滚文件。SQLite commit 失败后文件也可能已经发布，重复调用仍返回 ErrExists。

## 原子发布与校验

revision 目录通过排他 mkdir 领取；每个文件先写同目录随机临时文件（0600），fsync 后 hard-link 原子发布到固定文件名，已有文件或 symlink 永不覆盖。临时文件被清理，目录被 fsync。manifest 最后发布，固定列出五个文件的相对路径、用途、SHA-256 与同一更新时间。跨文件读取只有在完整 manifest 和五份文件全部通过校验后才返回结果。显式 revision 消除了 latest 指针和原地更新带来的版本竞争。

读回严格拒绝未知字段、尾随 JSON、版本/身份/时间不符、文件缺失、摘要不符、任意或重复 manifest 路径、错误载荷、公开权限和 symlink。读取文件有 128 KiB 硬上限；发布前检查 JSON 转义后的实际文档大小，超限在创建 revision 前返回 ErrInvalid。读取时采用 no-follow 打开并比对 inode，禁止非普通文件。时间戳记录逻辑发布时间；SHA-256 校验内容，不要求文件系统 mtime 相等。

错误可通过 `errors.Is` 分类：`ErrInvalid`（输入）、`ErrIdentity`（绑定/描述）、`ErrUnsafe`（权限/链接/类型）、`ErrExists`（重复 run/revision/文件）、`ErrCorrupt`（损坏/未提交版本）；底层 I/O 错误按原因返回或包裹。首次发布失败可能留下未提交的 run/revision；缺少 manifest 的 revision 明确报错。重复发布同一 revision 即使正文相同也返回 `ErrExists`，不覆盖、不自动清理或重试；调用方应先读回确认，再选新 revision。

这是同一 OS 用户下的协作 API 边界。0700/0600 防止其他用户访问；它不隔离同一用户的恶意进程，SHA-256 也不提供认证签名。调用方负责提炼摘要、选择适合保存的证据内容与传输身份认证。真实工具注入和 cmux 调用仍待后续实现。

单元测试见 `context_test.go` 和 `report_test.go`；runtime 身份/消息关联测试见 `../runtime/report_test.go`；事务代际门禁见 `../store/report_test.go`；双 run/多 agent 的独立测试进程发布与合并见 `../../tests/integration/report_test.go`。结果见 [S6.2 报告](../../docs/evidence/S6.2-result.md)。

## S6.3 文件继续包

`Agent.Latest()` 验证所有可见 revision 的 manifest/五份文档，按 manifest 时间和 revision 名字选择最新，未提交/损坏一律拒绝。`.reports/.evidence/.bootstrap` 属独立命名空间，不参与 revision 选择。

`PrepareBootstrap(generation)` 返回 `Bootstrap` 和待 runtime 核对的报告绑定。恢复包含 protocol、背景/决策/progress/pending、当前代际最新独立报告，或 lead 已合并的报告摘要与文件引用。引用上下文内其他 run/agent、未提交 revision 或非法 prompt 字段时拒绝；lead 的 Merge 例外必须重新核验原报告/证据/SHA/精简摘要及完整引用列表。普通外部引用保持不打开，冻结 prompt 保留字段引用和 SHA；不读取源 prompt 或聊天。

lead 恢复已合并报告时，匹配 `REPORT_MERGED` 凭据即可使用发布时的稳定身份，执行者后续正常 fresh 换代不影响已合并 revision；调用者本身仍须是当前 lead。该例外仅用于 Bootstrap/Rebuild 已合并摘要，不放宽报告发布、Read/Review/首次 Merge 或本人最新报告的当前代际校验。直接通过文件层合并、复制到另一 revision 或篡改报告 SHA 均不能获得历史授权。旧版本未记录凭据的合并仍按原规则核对当前代际，不自动补造历史授权；文件发布后数据库提交失败留下的 revision 也没有历史授权。

`PublishBootstrap(*Bootstrap)` 以序列化内容 SHA-256 命名，原子无覆盖写入 `.bootstrap/g-N/<sha>.json`；同一内容重试核验已有字节并返回同路径，损坏缓存返回 ErrCorrupt。沿用 128 KiB、0700/0600、no-follow 与 fsync 契约，不修改旧报告/revision。该文件层不授权当前 generation；产品调用应使用 `runtime.NewBootstrapService(db, run).Bootstrap/Rebuild(ctx, actor)`，让 store 事务覆盖身份核对、读取和发布。输出始终 executed=false / UNVERIFIED，成功仅表示文件准备完成。测试见 `bootstrap_test.go` 和 [S6.3 结果](../../docs/evidence/S6.3-result.md)。
