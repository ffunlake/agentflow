# AgentFlow

用 YAML/JSON 配置 cmux 分屏和 agent 角色，让主 agent 按工作流把任务交给 worker、审查并继续流转。

## 下载与启动

下载对应架构的 macOS 二进制：[Apple Silicon](https://github.com/ffunlake/agentflow/releases/latest/download/agentflow_darwin_arm64) · [Intel Mac](https://github.com/ffunlake/agentflow/releases/latest/download/agentflow_darwin_amd64) · [SHA-256 校验文件](https://github.com/ffunlake/agentflow/releases/latest/download/checksums.txt)。

```sh
chmod +x agentflow_darwin_arm64
./agentflow_darwin_arm64 --help
```

需要从源码构建时，安装 Go 1.25+ 后运行：

```sh
git clone https://github.com/ffunlake/agentflow.git
cd agentflow
make build
```

默认团队使用 Codex 和 OpenCode。运行前，把 `examples/team.yaml` 中的 `defaults.cwd` 改成目标项目的绝对路径，并先在相应 agent CLI 中完成登录。`up` 会在当前 cmux window 创建一个 workspace 并保持前台运行；进入新 workspace 的 main 分屏后直接输入任务。也可在启动命令中加 `--task "任务描述"` 立即派发。

停止时，在运行 `up` 的终端按 `Ctrl-C`；之后可在 cmux 中手动关闭 workspace。要重开同一个 run，使用 `up` 输出的 `run_dir` 启动 `coordinator --cmux-transport`，不要再次运行 `up`。每个 run ID 只创建一次；新的 workspace 使用新的 run ID。

```sh
./bin/agentflow coordinator --run first-run --run-dir "/实际/run_dir" --cmux-transport --json
```

完整安装步骤、JSON 配置、运行目录和故障处理见[安装与使用说明](how-to-use.md)。新版本推送 `v*` 标签后会自动发布 macOS 两种架构的二进制和 `checksums.txt`。

## 工作流

| 类型 | 流转方式 | 示例 |
| --- | --- | --- |
| `build_review` | 执行者完成后由 reviewer 审查；可退回修改 | [`examples/team.yaml`](examples/team.yaml) |
| `approval` | 每步指定审批人；可驳回到当前或之前步骤 | [`examples/approval.yaml`](examples/approval.yaml) |
| `pipeline` | 按配置中的角色顺序逐个交接 | [`examples/pipeline.yaml`](examples/pipeline.yaml) |

同样的示例也提供 JSON 格式。工作流字段和边界见[使用说明中的工作流章节](how-to-use.md#工作流类型)。

## 运行模式与限制

- **cmux：** 按布局创建分屏，支持混合配置 Codex、OpenCode 和 Claude Code。真实收发与权限效果依具体工具和配置而异，详见使用说明中的验收边界。
- **Headless：** 可用 `--headless-codex`、`--headless-opencode` 或 `--headless-claude`，不创建 cmux 分屏，配置示例见 [`examples/`](examples/)。
- `up` 默认把运行数据放在系统临时目录；需要长期保留时指定 `--run-dir`。
- 每个 run ID 只能启动一次。重复运行同一 `up` 不会另开 workspace。
- 本项目的 cmux 分屏路径以 macOS + cmux 为目标；Windows 完整运行尚未支持。native 权限强制效果和崩溃后自动恢复也未全部验证。

## 开发

```sh
make build   # 构建 bin/agentflow
make check   # 格式、依赖、静态检查、测试与构建
```

更多资料：[架构说明](docs/architecture.md) · [文档索引](docs/README.md) · [CMUX workflow 验证范围](docs/evidence/cmux-workflow-transport-v1-result.md)
