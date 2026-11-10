# S2 配置、布局与 CLI 实施结果

状态：PASS（S2 离线配置与布局出口）。S1 真实三工具通信仍为 PARTIAL；本报告不改变其能力结论。

本轮任务：`s2-cli-validate-plan-doctor`；session_id：`f892bc44-7818-434b-8fa1-f627b275596d`。在已有 S2.1 config、S2.2 layout 之上完成 S2.3，未进入 S3。

## 产物与行为

- `internal/cli/team.go`：validate/plan/doctor 共用 config.Load → config.Resolve → layout.Compile；保留 help/version/probe 路由。未修改 config/layout 的既有实现。
- validate 成功明确报告配置与布局有效。plan 文本输出配置和上下文绝对路径、lead、排序后的角色 tool/role/permissions/session/cwd/prompt 摘要及编译布局；JSON 为来源元数据、完整规范化快照与编译树。
- CLI `--tool` 交由 ResolveOptions 处理，仅覆盖默认工具，角色级 tool 优先。示例默认 main=opencode/persistent、builder=codex/fresh、reviewer=claude/fresh，权限均为 full。
- doctor 使用 exec.LookPath，仅检查最终角色计划使用的工具，按工具与角色名称排序。报告配置、布局、实际发现的绝对可执行路径或缺失，缺失有 EXECUTABLE_MISSING 及 tools.<tool>.executable 字段路径；其余查找失败也有结构化诊断。
- doctor 的整体 OK 仅代表配置、布局和可执行文件查找通过。runtime_communication 始终为 UNVERIFIED；既不启动工具，也不运行 help/version、cmux 或真实通信探针。有效权限、会话身份与消息接收不在本次验收内。
- 参数错误退出 2；配置、布局、工具诊断或输出失败退出 1；成功退出 0。诊断写 stderr。plan/doctor --json 在配置失败时也向 stdout 输出稳定的结构化失败报告，参数错误只写 stderr。
- `internal/cli/team_test.go`、`tests/e2e/team_cli_test.go`：覆盖输出失败、prompt 摘要、多种进程成功/失败路径、JSON 稳定性、现有 YAML/JSON 计划等价、三种 --tool 覆盖、缺文件、非法 schema/布局/重复键/未知字段/缺 prompt_file、参数错误、工具缺失、带空格路径与源配置不变。执行标记 fixture 验证三个命令均不启动工具或 cmux。

## 实际验证

2026-11-02 在 `/Users/funlake/Projects/agentflow` 执行：

| 命令 | 实际结果 | 退出码 |
|---|---|---|
| `go test ./internal/cli ./tests/e2e` | CLI 单元与实际二进制进程测试通过 | 0 |
| `go test ./...` | 所有包测试通过 | 0 |
| `go build ./...` | 全部构建通过 | 0 |
| `make check` | 格式、依赖下载/校验、vet、全部测试、CLI 构建通过 | 0 |
| `go mod tidy -diff` | 无输出、无依赖差异 | 0 |
| `./bin/agentflow validate -f examples/team.yaml` | 明确显示 Configuration valid，包含绝对来源路径 | 0 |
| `./bin/agentflow plan -f examples/team.json --tool claude` | main 改为 claude；builder 保持 codex；reviewer 保持 claude；布局为 horizontal 0.4 嵌套 vertical 0.5 | 0 |
| `./bin/agentflow doctor -f examples/team.yaml --json` | 配置与布局 VALID；三工具 FOUND；整体 OK；runtime_communication=UNVERIFIED | 0 |

实际 doctor 找到的 claude/codex 是当前 PATH 中的 cmux CLI shim，opencode 是 `/opt/homebrew/bin/opencode`。本次只确认 PATH 查找结果，不执行这些文件，也不判断 shim 的运行时接入有效性。

进程测试以独立 PATH fixture 记录真实退出码：参数错误为 2，缺文件、非法配置/布局和缺失工具为 1；空 PATH 下 validate/plan 仍成功。工具缺失 JSON 包含全部所需工具及逐条诊断，没有 panic。示例计划比较剔除唯一不同的来源元数据 source_path 后，config（含 digest）与 layout 完全一致；同一输入多次执行 JSON 字节一致。

## 剩余阶段

S2 离线出口已通过。S1 原生接收、重置、有效权限、身份与恢复证据仍需补齐；S3 store/IPC、S4 原生启动、S5 派发消息、S6 文件上下文、S7 恢复与完整验收均待后续授权任务。本轮未实现这些模块，未修改配置、未启动团队或原生工具会话，未执行提交。
