# workflow-config-freeze 交付证据

- task_id：`workflow-config-freeze`
- session_id：`712bf5e6-5867-4913-bd06-eb68b466c336`
- 日期：2026-11-03
- 结果：第①②步配置层与冻结本地验证 PASS；loop-core 未实现。

## 实现与契约

`Config.Workflow` 和 `Resolved.Workflow` 为可选指针，JSON `omitempty` 保持旧配置不增加 workflow 字段。源文件显式 null/空对象均拒绝。`workflow` 含 entry、completion、steps；completion 是与 step ID 区分的独立终点字符串。每步包含 execute.agent、reviewer、instruction、acceptance、handoff_required、max_attempts、transitions、next，steps 为数组。

复用既有反射节点 strict loader，补充 bool 类型分支；workflow 递归拒绝未知字段、重复键、null、alias、merge 与隐式类型转换。必填字符串非空白且无 NUL；acceptance 至少一条；执行者/审查者均已存在且不同；handoff_required 必须为布尔 true；max_attempts 为整数 1–10。transitions 固定 approve=next、changes=self、retry=self、blocked=pause。next 目标只能是现有 step 或 completion，且必须恰好一个 completion 入边；entry 链路覆盖全部步骤，拒绝断链、孤立、多终点与前进环。

`config.ValidateWorkflow` 由源 Validate 和 layout Compile 共用，runtime Compile / NewSnapshot / RunSnapshot.Validate 经过同一入口。Resolve 深复制 acceptance 并按 entry→next 顺序复制 steps，纳入既有 Digest。runtime 继续通过已有 JSON 克隆机制冻结独立 StartupPlan.Config 和 RunSnapshot.Config；已有 runs.config_snapshot 保存同一正文，无迁移。`Resolved.UnmarshalJSON` 对 workflow 原始节点复用 strict schema，拒绝解码可能丢失的显式 null、重复 workflow/嵌套键及类型问题，并保留 unknown-field 检查。

## 修改文件

- `internal/config/config.go`、`load.go`、`resolve.go`：可选字段、strict bool 与深复制规范化入口。
- `internal/config/workflow.go`、`workflow_json.go`：类型、共享语义校验、规范化复制、冻结 JSON 恢复检查。
- `internal/layout/layout.go`：编译入口调用共享 workflow 校验器。
- `internal/config/workflow_test.go`、`internal/runtime/workflow_test.go`：正负例、等价、冻结与恢复验证。
- `examples/workflow.yaml`、`examples/workflow.json`：等价双步骤示例，YAML 故意倒序声明步骤。
- `README.md`、`docs/README.md`、`internal/config/README.md`、`internal/runtime/README.md`：契约、示例与实现边界。

## 测试与实际结果

Go 命令使用 `GOTOOLCHAIN=local CGO_ENABLED=0 GOFLAGS=-mod=readonly`，GOCACHE/GOMODCACHE/GOPATH/GOTMPDIR/TMPDIR 均指向本工程 `.cache`；Makefile 使用同样约定。

| 命令 | 结果 |
|---|---|
| `go test ./internal/config ./internal/layout ./internal/runtime` | PASS，exit 0 |
| `go test ./...` | PASS，exit 0；全部 internal、CLI e2e、integration 包通过 |
| `go build ./...` | PASS，exit 0 |
| `make check` | PASS，exit 0；格式检查、依赖验证、go vet、全量测试和 CLI 构建完成 |
| `go mod tidy -diff` | PASS，exit 0；stdout/stderr 无差异 |
| `./bin/agentflow validate -f examples/workflow.yaml` | PASS，exit 0；configuration and layout valid |
| `./bin/agentflow validate -f examples/workflow.json` | PASS，exit 0；configuration and layout valid |

主要新增测试：

- `TestWorkflowStrictSchema`：YAML/JSON 类型、重复键、未知字段、null、bool/整数类型与嵌套结构。
- `TestWorkflowSemanticValidation`：程序构造、Resolve 和 loader 对必填字段、角色、attempts、handoff、固定 transitions、图结构的负例。
- `TestWorkflowYAMLOnlyStrictness`：原生 YAML yes/on/字符串 bool、alias/merge 和 timestamp 类型拒绝；先检查原始 YAML 夹具有效。
- `TestWorkflowNormalizationDigestAndIsolation` / `TestWorkflowExamplesAndSingleStep`：YAML/JSON、声明顺序、合法单步骤与 1/10 边界；源对象/文件修改不影响已 Resolve 快照；workflow 修改改变新 Digest；缺省 workflow 不出现在旧 JSON。
- `TestWorkflowFrozenPlanSnapshotAndDatabaseRecovery`：StartupPlan 与 RunSnapshot 同值且独立；修改 Config/Resolved/Plan/源文件后读回快照不变；SQLite 关闭重开后恢复 workflow 与 Digest，无须源文件有效。
- `TestWorkflowSharedValidationAtCompileAndSnapshot`：layout/runtime 编译及已重算正确 Digest 的非法快照仍拒绝。
- `TestWorkflowSnapshotRejectsTamperingAndEncodingAmbiguity`：私有快照的 workflow null、重复字段、错误 bool、未知字段与未更新 Digest 的合法值篡改均拒绝。

开发中 YAML 测试夹具直接 marshal Config 会产生旧 defaults 的 null，已修正为经 source JSON 契约生成 YAML；最终相关包和全部指定检查均通过。首次测试启动使用 1 秒工具 timeout 被终止，后续使用 60 秒上限完整执行通过，无遗留测试进程。

## 边界与交接

没有新增 loop-core、任务表、DSL、依赖或外部服务；未执行原生工具模型调用。没有向被测 `window:1000060005` / `workspace:1000060011` / `surface:1000060034/35` 发出任何命令。协作消息仅发往获授权的主控 `surface:1000060018`。本地配置/冻结 PASS 不代表 workflow 自动执行、真实审查或 loop 生命周期验收。

下一步：主控独立 review 本切片；确认第①②步后，另行派发 loop-core 工作。正式 handoff 使用 `cmux agent message surface:1000060018`，包含 task_id/session_id/status/evidence/tests/next；传输回执另存于同目录 `workflow-config-freeze-handoff-receipt.json`。
