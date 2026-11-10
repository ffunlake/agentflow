# CMUX OpenCode 结果采集修复（2026-11-06）

## 问题

OpenCode TUI 已完成回复，但 AgentFlow 一直保留 `SUBMIT_REQUESTED`，没有进入 reviewer。本次长会话的 `opencode export` 输出截断在 196608 字节，返回不完整 JSON；`waitResult` 忽略了解析错误并持续轮询。另一个匹配风险是整段 prompt 在 cmux/OpenCode 输入后不再逐字匹配。

## 修复

- 用 `opencode db <只读 SELECT> --format json` 查询当前 native session、最后一条消息、带当前 dispatch ID 的 user 消息及其已完成 assistant 回复；不再导出整段会话。
- 派发文本加入短的唯一 `AgentFlow dispatch ID` 标记，只按该标记关联 user 消息。
- SQL 字符串安全转义；查询失败立即以 UNKNOWN 返回，不静默等待或重发。

## 验证

- 真实 OpenCode session 的只读定向查询取到已完成、JSON 合法的 `handoff` 结果；未打印或修改报告正文。
- `go test ./internal/runtime -run 'TestCMUXOpenCodeUsesVisibleSurfaceAndNativeSession|TestOpenCodeSQLLiteral' -count=1`：PASS。
- `go test ./internal/coordinator -run '^TestCMUXWorkflowUsesRegisteredOpenCodeTUIs$' -count=1`：PASS。
- `make check`：PASS（format、dependencies、vet、全量 tests、build）。

## 当前 live run 边界

修复后的代码不能热替换已运行的 coordinator。测试 run `wiki-review-v4` 的 native 回复已完成，但旧进程没有提交 workflow receipt；状态仍是 `SUBMIT_REQUESTED`，reviewer 未派发。没有重发该任务或伪造 workflow 回执。该 run 需要用修复后的 binary 新建，不能从 UNKNOWN 动作自动重试。
