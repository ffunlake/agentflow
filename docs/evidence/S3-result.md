# S3 持久消息与本机协调实施结果

状态：PARTIAL（S3.1 存储基础 PASS；S3.2 最小 NDJSON IPC 与请求校验 PASS；S3.3 最小运行锁、owner 元数据和单实例探测 PASS；协调进程唯一业务写入口及协调启动/恢复尚未实施）。

S3.1 任务：`s3-store-schema-outbox`；session_id：`b92244e3-216d-4efb-9c33-c4d097313bf0`。以下保留 S3.1 的独立临时数据库验证；S3.2 与 S3.3 最小运行锁记录见文末。

## 产物与行为

- `internal/store/store.go`：`Open(path)`、`Close`，以及 run/seat 创建和读取 API；连接级 PRAGMA 使用驱动 DSN 配置，连接替换及重新打开时仍设置 foreign_keys=ON、journal_mode=WAL、synchronous=FULL，并核对实际值。每库仅一个 run，保存配置快照和摘要、workspace、启动阶段与停止意图；seat 保存 native session/thread、tool、workspace/surface、generation、状态及当前消息引用。
- `internal/store/schema.go`：`Migrate(ctx)` 在一个事务内创建 runs、seats、messages、deliveries、journal 五张业务表、六个命名索引、唯一约束、外键及 journal 防修改触发器。使用 `user_version=1`，重复迁移保留数据，迁移中途失败整体回滚，拒绝较新 schema。
- `internal/store/outbox.go`：`SaveOutbox(ctx, Outbox)` 在单个事务内保存 message、delivery 和 journal，提交成功才返回 Message；投递目标代际、reset/投递阶段、channel、attempt、next retry 和错误字段可持久保存。正文及其他输入均参数绑定；时间为固定宽度 UTC 纳秒文本，读回为 `time.Time`。
- `(run_id, sender, request_id)` 去重：同正文逐字相同返回原 Message，保留原 envelope，不新增三类记录；异正文返回可由 `errors.Is` 判断的 `ErrIdempotencyConflict`，稳定文本为 `IDEMPOTENCY_CONFLICT`。空 request_id 存为 NULL，不参与 request 去重。回复及报告状态使用同一个消息 API。
- `AppendJournal` 与 `JournalAfter`：只追加，按 seq 游标读取，保存事件细节及证据引用。没有更新/删除 API，数据库触发器拒绝 UPDATE/DELETE。
- `internal/store/store_test.go`：全部使用 `t.TempDir()`；未复用 sqlitecheck 自检作为 store 验收。API 及后续边界见 [Store README](../../internal/store/README.md)。根 README、技术规范和实施计划已同步 S3.1 状态。

## 实际验证

2026-11-02 在 `/Users/funlake/Projects/agentflow` 执行，沿用 README 中项目内 Go 缓存、临时目录、`GOTOOLCHAIN=local`、`CGO_ENABLED=0`、`GOFLAGS=-mod=readonly` 设置；使用既有依赖。

| 命令 | 实际结果 | 退出码 |
|---|---|---|
| `go test ./internal/store -count=1 -cover` | Store 临时数据库专项测试通过 | 0 |
| `go test ./...` | 所有包测试通过，包含 Store 及既有 CLI/配置/布局/probe/集成测试 | 0 |
| `go build ./...` | 全部包构建通过 | 0 |
| `make check` | 格式、依赖校验、go vet、全部测试与 CLI 构建通过；all modules verified | 0 |
| `go mod tidy -diff` | 无输出，无依赖差异 | 0 |

验证覆盖：

- 首次与重复迁移、五表与六个命名索引、user_version，以及数据存在时重复迁移。
- PRAGMA 实际值分别为 foreign_keys=1、journal_mode=wal、synchronous=2；首次打开、重复迁移、物理连接替换、关闭重开均保持。
- 中途及接近末尾的 DDL/索引冲突使整个迁移回滚，runs 不残留，user_version 保持 0；较新版本被拒绝。
- 原子创建消息/投递/事件，提交后真正关闭 Store，通过新的 Store 读回消息正文、序号、绑定、配置快照、delivery 恢复字段及 journal 证据引用。
- message CHECK、delivery 目标代际外键、journal seat 外键分别造成失败时，三个表均无残留；相同请求可随后成功重试。
- 相同 request_id 重试、不同正文稳定冲突、不同 sender/request 的独立性、空 request、重开后模拟响应丢失重试，以及两个独立 Store 句柄的八个并发相同请求只生成一份三类记录。
- 外键拒绝孤儿 run/message/reply/seat 绑定、错误发送代际、投递接收者错配与引用记录删除；唯一约束拒绝第二个 run、重复 message ID、重复 seat generation、两个 current generation 和重复 delivery。
- 历史 seat 可保留，回复关系与 DONE 报告状态读写正确；journal 时间逆序输入仍按追加 seq 输出，证据引用及 UTC 时间正确，UPDATE/DELETE 被拒绝。
- 非法数据库路径、缺失父目录、取消上下文、关闭后的写入及不存在消息均返回错误。所有 Store 测试无网络或用户目录依赖。

## 后续边界

S3.1 只证明消息已保存。存储层核对引用绑定的存在性，保留历史代际；本轮 S3.2 已补齐 NDJSON IPC、集中校验回调和稳定错误码映射，见下文。真实运行时身份来源与协调入口仍需后续接入。绑定切换、delivery 状态推进等更新 API 在实际运行时接入时继续补齐。

S3.3 最小运行锁与探测已实现，见文末；单实例协调启动和竞争恢复仍待实施。Store 多句柄幂等测试不能作为单实例验收，关闭重开测试也不代表已完成进程崩溃、掉电或真实通道恢复验证。F10 的完整运行时恢复与 S3 阶段出口仍未通过。

S3.1 轮次无 IPC、CLI、cmux 或工具适配器实现，未执行 Git 提交。

## S3.2 最小 NDJSON IPC 与请求校验

任务：`s3-ipc-ndjson-idempotency`；session_id：`8ca6032a-5bf4-4e46-9f8b-1bfc62ec3b43`。2026-11-02 在本项目执行，标准构建沿用上述项目内 Go 缓存和 CGO_ENABLED=0；专项竞态检查使用 CGO_ENABLED=1。未增加依赖。

- `internal/ipc` 新增 `Listen(path, Handler, ServerOptions)`、`Server.Serve(ctx)` / `Close()`、`Client{Path}.Call(ctx, Request)`，以及 `Actor`、`Request`、`Response`、结构化 `Error`。调用方提供私有目录内的短 socket 路径，监听文件权限为 0600；路径已存在时绑定失败，不删除重试。API、线格式与错误契约见 [IPC README](../../internal/ipc/README.md)。
- 请求为 `v/requestId/actor/method/params`，actor 为 `runId/agentId/generation`；全部字段必需，params 可以显式为 null。精确校验字段名，拒绝未知/缺失/null 必填字段、重复 envelope/actor 键、非法 JSON、非法 UTF-8 和空行；method/params 交给 handler。
- `ServerOptions.ValidateActor(ctx, Actor)` 必填，每次合法请求在 handler 前调用，统一检查 actor 与当前 generation，持续连接和相同 requestId 重试也重新校验。可信身份来源与真实运行时绑定由后续协调入口提供，本轮不声称已完成原生会话身份认证。
- 不同连接并发，同连接按 NDJSON 行顺序单请求单响应；处理拆包/粘包、断线、半关闭和取消。帧上限为 1 MiB UTF-8 字节，不含 LF；超限即报错并关闭该连接，无 LF 也能在第 1 MiB+1 字节拒绝，不继续无界排空。缺 LF 的截断请求不会进入 handler。
- response 固定为 `requestId/ok/result` 或 `requestId/ok/error{code,message}`，成功 nil result 为显式 null。可识别且唯一的字符串 requestId 原样回传；非法 JSON/UTF-8、缺失或重复 ID、超限及截断帧使用空 ID。客户端验证响应 ID 与成功/错误字段互斥，不自动重试。未知内部错误、编码失败及 validator/handler panic 映射为不暴露诊断详情的 INTERNAL。
- 稳定错误码：`INVALID_REQUEST`、`FRAME_TOO_LARGE`、`ACTOR_MISMATCH`、`GENERATION_MISMATCH`、`METHOD_NOT_FOUND`、`INTERNAL`、`IDEMPOTENCY_CONFLICT`。`errors.Is(err, store.ErrIdempotencyConflict)` 在 IPC 统一映射；其他显式 `*ipc.Error` 保留 code/message。无持久响应缓存，幂等仍归 store；真实临时数据库测试验证重复请求返回原 STORED 消息，异正文返回结构化冲突且不多写 journal。

| 命令 | 实际结果 | 退出码 |
|---|---|---|
| `go test ./internal/ipc -count=1 -cover` | 临时 Unix socket 专项测试通过，语句覆盖率 90.9% | 0 |
| `go test ./...` | 全部包测试通过，含新 IPC 和既有 Store/CLI/集成测试 | 0 |
| `go build ./...` | 全部包构建通过 | 0 |
| `make check` | 格式、依赖校验、go vet、全部测试与 CLI 构建通过；all modules verified | 0 |
| `go mod tidy -diff` | 无输出，无依赖差异 | 0 |
| `CGO_ENABLED=1 go test -race ./internal/ipc -count=1` | IPC 专项竞态检查通过 | 0 |

测试包含正常请求、requestId 特殊字符原样回传、result/null/error 稳定结构、拆包/粘包、非法/未知/缺失字段与空行后继续处理、恰好 1 MiB 接受、超过上限有/无 LF 拒绝、actor/generation mismatch、未知方法、客户端半关闭及处理中断线、服务关闭/上下文取消、0600 权限、已有文件保留、16 个客户端同时进入 handler 且响应不串线、持续连接中的代际刷新拒绝旧请求，以及真实 Store 的跨连接重试幂等与冲突。

S3.3 锁与探测已由下文独立轮次补齐；剩余协调启动与恢复竞争、完整协调进程唯一业务写入口及真实会话身份源尚未接入，S3 整体阶段出口仍为 PARTIAL。IPC 成功仅表示 handler 操作完成，STORED 仅表示保存，不证明投递/ACK/执行；断线也不证明事务未提交。

本轮变更限定为 `internal/ipc/*` 与本报告；未修改 store schema、CLI、cmux、工具适配器或 okra-wiki，未执行 Git 提交。工作目录当前无 `.git`，因此以文件和实际构建/测试结果复核。

## S3.3 最小运行锁、owner 与单实例探测

任务：`s3-run-lock-single-instance`；session_id：`4de915bd-b5ce-472b-877e-e1a77c5868ab`。2026-11-02 在本项目执行，沿用项目内缓存与临时目录，标准检查使用 CGO_ENABLED=0，全量 race 使用 CGO_ENABLED=1；仅新增标准库代码与测试。API 和边界见 [Lock README](../../internal/lock/README.md)。

- `internal/lock` 提供 `Acquire(path, Owner)`、`(*Lock).Release()`、`Inspect(path)`。完整 JSON owner 先写入同目录 0600 临时文件并同步关闭，再用原子硬链接独占发布；已有路径不覆盖，竞争者返回稳定 `ErrAlreadyRunning`，可从 `*AlreadyRunningError.Owner` 读取 owner，坏记录则保留 `InspectErr`。
- owner 保存本进程 PID、32 字节加密随机 token、当前 UTC startedAt、runID、socketPath 和可选 command argv/metadata；读取限制为 64 KiB。释放同时核对 token 与文件身份，错 token/替换文件拒绝删除；重复或并发释放安全，旧 handle 不影响后续获锁者。调用方须使用私有目录并遵守锁协议，独立强行替换活动锁不在原子 compare-and-delete 保证内。
- `ProbePID` 只作存活诊断；PID 可复用。`ProbeSocket(ctx, path, timeout)` 返回 MISSING/CONNECTABLE/REFUSED/TIMEOUT/CANCELED/ERROR，限时连接后发送零字节并关闭。任何诊断都不自动删除 stale lock/socket，CONNECTABLE 仅证明传输可连接，不代表业务运行。
- 测试覆盖 32 个 goroutine 与 8 个独立进程竞争仅一个获锁、竞争失败者完整读回 owner、0600、随机身份与 metadata、顺序竞争、释放后重获锁、重复/并发释放、错 token/同 token 换 inode/损坏内容拒绝释放、坏记录/目录/符号链接保留，以及 owner 进程退出后仍拒绝自动接管。真实 Unix listener 验证不存在、可连接、零字节关闭、关闭后路径消失和保留路径时连接拒绝；已过期 context 确定性验证超时，取消与非 socket 路径返回独立结果。

| 命令 | 实际结果 | 退出码 |
|---|---|---|
| `go test ./internal/lock -count=1 -cover` | 专项测试通过，语句覆盖率 87.5% | 0 |
| `go test ./...` | 所有包测试通过 | 0 |
| `go build ./...` | 所有包构建通过 | 0 |
| `make check` | 格式、依赖校验、go vet、全量测试与 CLI 构建通过 | 0 |
| `go mod tidy -diff` | 无输出，无依赖差异 | 0 |
| `CGO_ENABLED=1 go test -race ./... -count=1` | 全量竞态检查通过 | 0 |

本轮产物为 `internal/lock/*` 与本报告。协调进程、IPC handler、CLI 启动/派发、cmux/native 与工具恢复留待后续；未修改 S1 证据或 okra-wiki，未执行 Git 提交。上述竞争测试证明锁的单实例能力，未证明团队创建、协调进程恢复或 F10 完整运行时恢复。
