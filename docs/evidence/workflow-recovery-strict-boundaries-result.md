# Workflow 恢复严格大小写与重复键修复

- task_id：`workflow-recovery-strict-boundaries`
- session_id：`b1155005-2134-4cee-a509-97c4f5f87618`
- role：资深研发
- 日期：2026-11-03
- 状态：本地修复与验证完成，待主控独立复核。

## 根因与修改

`Resolved.UnmarshalJSON` 原先只检查原始 JSON 中精确等于 `workflow` 的键，然后交给 `encoding/json` 解码。后者会忽略 struct 字段名大小写，并覆盖重复键。因此 `Workflow`、`WORKFLOW`、混合大小写以及与规范键混用的正文能绕过 workflow 原始节点检查；重复 `digest` 等 resolved 顶层键也会被覆盖。

现在先从 `Resolved` 的 JSON 标签建立精确字段集合，拒绝 resolved 顶层未知字段（包括大小写变体）和重复键，再对规范 `workflow` 继续复用既有递归 schema 检查。成功解码后才替换接收对象。没有改变 workflow 的语义规则、序列化格式、Digest 算法或数据库结构。

修改文件：

- `internal/config/workflow_json.go`：恢复入口顶层精确字段名与重复键检查。
- `internal/config/workflow_json_test.go`：新增恢复回归与兼容性测试。
- `internal/runtime/workflow_test.go`：扩充磁盘快照的大小写与重复摘要键负例。
- `internal/config/README.md`：明确 resolved 顶层大小写、重复键及旧快照恢复约定。
- 本证据文件和同名前缀 handoff 回执；`make check` 按现有目标重新构建 `bin/agentflow`。

## 验证证据

修复前执行新增 `TestResolvedWorkflowStrictJSONRecovery`，确认 `Workflow` / `WORKFLOW` / 混合大小写、大小写与规范键混用、重复 `digest` 及 `Digest` 均被错误接受，测试报 `got <nil>` 并退出 1。随后完成修复及测试夹具核对。

| 命令 | 实际结果 |
|---|---|
| `go test ./internal/config ./internal/layout ./internal/runtime` | PASS，exit 0；config 0.249s，runtime 16.684s |
| `make check` | PASS，exit 0；格式检查、依赖验证、`go vet ./...`、`go test ./...` 与 CLI 构建完成 |

Go 测试使用 Makefile 同等环境：`GOTOOLCHAIN=local`、`CGO_ENABLED=0`、`GOFLAGS=-mod=readonly`，所有 Go 缓存与临时目录都位于本工程 `.cache`。

新增用例检查：顶层大小写变体；大小写变体承载 null、未知字段和重复嵌套键；规范键与变体在两个顺序下混用；重复 workflow（包括同值）与 digest；JSON 转义后相同键的重复；workflow、step、execute、transitions 各层的大小写与重复键；恢复失败保留原接收对象。

兼容性用例确认合法 workflow、无 workflow 的旧配置和合法 JSON 转义键可恢复，正文与 Digest 保持不变。全量测试还通过既有的冻结独立性、磁盘读取和 SQLite 关闭重开后的 workflow 恢复验证。

## 交接范围

只修改 agentflow 工程，没有进入 loop-core，也没有对被测窗口执行任何操作。测试使用工程既有单元、集成与 e2e 路径；未启动真实被测 agent。

工作目录没有 `.git`，无法提供 Git diff 或提交号；以上为本轮具体文件清单。交接仅向授权主控 `surface:1000060018` 发送。

本机 `agent-handoff` wrapper 指向不存在的 `/Users/funlake/.codex/skills/agent-init/scripts/agent-handoff.sh`，`--help` 即失败。改用项目上一轮证据已经采用的正式 `cmux agent message surface:1000060018 --json` 通道，回执另存。传输排队成功不代表主控已 ACK 或验收通过。

下一步：主控独立复核恢复入口边界与测试结果；本轮不推进 loop-core。
