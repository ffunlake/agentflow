# 最小运行锁与诊断

本包仅使用标准库，供后续协调启动组合；不启动协调进程、不处理 IPC 业务请求、不恢复或接管 stale lock。

```go
held, err := lock.Acquire(path, lock.Owner{
    RunID: "run-id", SocketPath: socketPath,
    Command: []string{"agentflow", "up"},
    Metadata: map[string]string{"version": "dev"},
})
// 成功后由调用方在正常退出时 held.Release()。
// errors.Is(err, lock.ErrAlreadyRunning) 表示已有锁，禁止自动删除。
```

`Acquire` 在已有私有父目录中创建临时 owner 文件，写完 JSON、同步并关闭，再以 `os.Link` 独占发布到目标路径；硬链接不会替换已有目录项，竞争失败时清理自己的临时文件。这样成功者和竞争失败者都能读取完整记录。文件权限为 0600；依赖同目录文件系统支持硬链接，不支持时直接返回错误。正常调用结束会清理临时文件；进程在发布前崩溃可能留下不具有运行锁效力的临时文件，本包不扫描清理。

JSON 包含 `pid/token/startedAt/runID/socketPath` 和可选 `command/metadata`。PID 使用当前进程，token 使用 32 字节加密随机数的 hex 字符串，startedAt 为当前 UTC RFC3339 时间；这些身份字段由包生成，覆盖输入中的同名值。command 是调用方提供的 argv，仅作诊断，不执行，不自动采集进程参数。owner 记录上限为 64 KiB。

`Inspect(path) (Owner, error)` 只读元数据。已有目标无论是否 stale、内容损坏、目录或符号链接，`Acquire` 都返回可用 `errors.Is` 判断的 `ErrAlreadyRunning`，文本为 `ALREADY_RUNNING`；`errors.As` 为 `*AlreadyRunningError` 后可读取 `Owner`，读取失败则查看 `InspectErr`。路径不存在与其他文件错误不会伪装成成功。

`(*Lock).Release()` 只删除原 token 与原文件身份均匹配的目标。token 不符或同 token 的替换文件返回 `ErrNotOwner`；内容损坏返回读取错误，均保留目标。成功释放或发现目标不存在后，重复释放安全，不会触及后续获得的锁。同一 handle 的并发释放受互斥保护，不要复制 handle。私有目录内的调用方必须遵守协议，不能独立替换活动锁；检查 token 与 unlink 不是文件系统原子 compare-and-delete，无法保护任意外部进程在这两步之间强行修改路径。

`ProbePID(pid)` 使用 signal 0 返回 `ALIVE/DEAD/UNKNOWN`，不会发送业务信号。PID 可重用；结果只用于诊断，不证明 owner 身份，DEAD 不授权自动接管。

`ProbeSocket(ctx, path, timeout)` 返回 `SocketProbe{Path, Status, Error}`：`MISSING/CONNECTABLE/REFUSED/TIMEOUT/CANCELED/ERROR`。只连接真正的 Unix socket，随后关闭，发送零字节；非 socket 与符号链接返回 ERROR。timeout 非正时默认一秒，更早的 context deadline 优先。结果是瞬时的传输层诊断：CONNECTABLE 不证明业务运行或 owner 身份，TIMEOUT/REFUSED/MISSING 不授权删除锁或 socket。恢复策略与 IPC 协议检查留待调用方后续实现。
