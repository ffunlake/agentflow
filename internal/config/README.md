# S2.1 配置 API

```go
source, err := config.Load("examples/team.yaml")
// 检查 err 后：
snapshot, err := config.Resolve(source, config.ResolveOptions{Tool: "codex"})
```

- `Load(path) (*Config, error)` 支持 `.yaml`、`.yml`、`.json`，只接受单个文档；完成严格 schema 和值校验，记录绝对 `SourcePath`，保留源配置中的相对路径。
- `Validate(*Config) error` 也可用于程序构造或修改后的配置；不读取文件。`version=1`，`name`、`terminal`、`lead`、`context_dir` 必填；至少一个 agent；role 非空，prompt/prompt_file 恰选一个且非空。
- `Resolve(*Config, ResolveOptions) (*Resolved, error)` 再次校验，读取 prompt 文件并复制角色、参数及布局，返回独立快照。手动构造 Config 时需要设置 `SourcePath`。
- `FieldError` 提供 `Path`、`Message`，文件错误可通过 `errors.Is/As` 获取原因，例如 `agents.main.prompt_file`、`layout.children[1].agent`。

缺省 tool/permissions/session 为 `opencode`/`full`/`fresh`，缺省 cwd 为配置文件目录。工具优先级为 agent → `ResolveOptions.Tool` → defaults → opencode；其他选项为 agent → defaults → 文档缺省值。model/args 仅属于 agent。

context_dir、defaults.cwd、agent.cwd 和 prompt_file 都相对配置文件目录解析，角色 cwd 覆盖不以 defaults.cwd 为基准。路径仅作绝对化与清理，不展开环境变量、`~` 或符号链接。args 原样复制，不推测其中的路径或执行 shell 插值；cwd、context_dir 不要求已经存在。

拒绝未知字段、重复对象键、隐式字符串类型转换、null、YAML alias/merge、自定义容器标签、非法枚举及非法角色 ID。ID 为 `[a-z][a-z0-9_-]{0,47}`。布局 agent/split 字段出现时不得为空字符串，避免空值掩盖字段冲突。布局使用强类型树，检查每个 agent 恰好引用一次；S2.2 已实现的 `internal/layout.Validate/Compile` 负责叶子/split 形态、方向、二叉 children、ratio 的默认值与 0.1–0.9 范围及纯数据编译。因此 S2.1 的成功不能表示布局合法，有限数值的非法 ratio 会原样保留供布局 API 拒绝。

快照保存绝对路径、内联提示文本、工具、权限、session、model、args 和源布局。`Digest` 为快照 JSON 去掉 digest 字段后的 SHA-256；`SourcePath` 是诊断元数据，不参与 JSON/摘要，使同目录内等价 YAML/JSON 产生相同摘要。编辑源文件或输入对象不改变已有快照；重新 Resolve 才会读取新内容。当前包不保存快照到磁盘。

Claude headless/native 的认证选择由冻结 args 决定：缺省/空 args 或 `["--bare"]` 为严格 bare，不读取 OAuth/keychain；只有精确的 `["--safe-mode"]` 显式选择 non-bare 并允许官方 CLI 复用已有 OAuth。runtime 在启动前拒绝其他 Claude args；其他工具不能使用这个 opt-in。该选择参与 Digest，随 startup snapshot 与 DB config 保存，resume 和 coordinator 正常重开复用原选择，不重读源配置或根据认证结果自动回退。

本包不启动进程。布局编译属于 S2.2，validate/plan/doctor CLI 和工具可用性检查属于 S2.3。

## 可选 workflow 配置与冻结

顶层 `workflow` 可省略，旧配置的 JSON 和 Digest 计算保持原约定；显式 `null`、`{}` 均拒绝。build_review 等价示例见 [workflow.yaml](../../examples/workflow.yaml) 和 [workflow.json](../../examples/workflow.json)。`validate` / `up --dry-run` 可检查并显示冻结配置；真实执行由 `up`、前台 coordinator、workflow runner 与所选 native adapter 驱动。工具组合的 live 验收状态以[使用说明](../../how-to-use.md#工作流类型)为准。

`type` 可省略（默认 `build_review`），也可显式写 `build_review`、`approval` 或 `pipeline`。`build_review` 保留原有执行者→reviewer 模型。`approval` 每步独立配置执行人和审批人，允许退回当前或更早的步骤；每个 agent 可在不同步骤分别工作和审批，同一步执行人与审批人必须不同。`pipeline` 使用 `flow: main -> role1 -> ... -> main`，首尾必须是配置的 lead，中间每个角色必须是已有 agent；flow 节点数由配置决定。pipeline 的 `max_attempts` 可省略（默认 3），显式范围 1–10。三种类型不能混用字段。

build_review 的 `steps` 是非空数组，每步必填 `id`、`execute.agent`、`reviewer`、`instruction`、非空字符串数组 `acceptance`、`handoff_required`、`max_attempts`、`transitions`、`next`。执行者、审查者须为已有且不同的 agent。`handoff_required` 必须为布尔值 `true`，不接受字符串、数字或 YAML `yes/on`；每步 `max_attempts` 必须是 1–10 的整数。

approval 使用相同的有序 `entry`→`next` 步骤链，但每步以必填 `approver` 代替 `reviewer`。`transitions.approve` 固定为 `next`；`transitions.changes` 可为 `self` 或任意更早的 step ID；`retry` 固定 `self`，`blocked` 固定 `pause`。`changes` 的结构化事件必须带原因，退回目标按本任务累计访问次数消费该目标的 `max_attempts`，超限进入 `EXHAUSTED`。显式 lead/main 审批时，CMUX drain 在 dispatch 到 lead 后返回 `awaiting_actor`；审批响应后用最新 revision 继续 drain。

v1 的 transitions 为固定枚举：`approve: next`、`changes: self`、`retry: self`、`blocked: pause`。`next/self/pause` 是固定动作标记，不能用步骤 ID 代替；只有独立字段 `next` 指定推进目标。`completion` 是与全部 step ID 区分的终点字符串。`entry` 必须指向一个 step；从 entry 沿 next 必须恰好经过所有 steps，再到 completion。重复 ID、未知 agent/目标、断链、孤立步骤、多个 completion 入边及前进环均拒绝。

`ValidateWorkflow(workflow, agentIDs, lead)` 是源配置与 `layout.Compile` / `runtime.Compile` / `RunSnapshot.Validate` 共用的纯校验器；程序构造配置也必须满足同样规则。严格 loader 递归处理 workflow 的未知字段、重复键、null、alias、merge 与类型错误。恢复 resolved JSON 时，顶层字段按 JSON 标签严格区分大小写并拒绝重复键；workflow 的原始节点也递归检查，防止大小写变体绕过检查，或重复键、null 在解码时丢失。省略 workflow 的旧快照仍可恢复。

`Resolve` 深复制 workflow 及 acceptance 数组；build_review 按 entry→next 排列 steps，pipeline 规范化 `flow` 中的空格，然后一并纳入 Digest。`StartupPlan.Config`、私有 `RunSnapshot.Config` 和既有 `runs.config_snapshot` 保留同一 workflow；它们独立复制，后续修改源文件、输入对象或上游 plan 不改变既有 run。快照读取校验 Digest 与 workflow 规则，无须重读源配置或提示文件。配置包本身只负责冻结；loop-core/store 与 coordinator IPC 负责显式事件推进和 journal 进度。
