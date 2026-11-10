# AgentFlow

用 YAML/JSON 配置 cmux 分屏和 agent 角色，让主 agent 按工作流把任务交给 worker、审查并继续流转。

## 快速启动

需要 macOS、cmux，以及已安装并登录的 Codex 和 OpenCode。下面会下载 Apple Silicon 版本；Intel Mac 请把链接末尾改为 `agentflow_darwin_amd64`。

也可以在 [GitHub Releases](https://github.com/ffunlake/agentflow/releases/latest) 选择 Intel/Apple Silicon 二进制并下载校验文件。

```sh
git clone https://github.com/ffunlake/agentflow.git
cd agentflow
mkdir -p bin
curl -fL https://github.com/ffunlake/agentflow/releases/latest/download/agentflow_darwin_arm64 -o bin/agentflow
chmod +x bin/agentflow
```

先把 `examples/team.yaml` 中的 `defaults.cwd` 改成你要操作的项目绝对路径，然后在 cmux 终端运行：

```sh
./bin/agentflow up -f examples/team.yaml --run cmux-demo-v6 --json
```

`up` 会新建一个 cmux workspace 并保持前台运行。进入新 workspace 的 main 分屏后直接输入任务；也可加 `--task "任务描述"` 立即派发。每个 run ID 只能启动一次；创建新 workspace 时换一个 ID。

想从源码构建而不是下载二进制：安装 Go 1.25+，在仓库目录运行 `make build`。

停止时，在运行 `up` 的终端按 `Ctrl-C`；之后可在 cmux 中手动关闭 workspace。要重开同一个 run，使用 `up` 输出的 `run_dir` 启动 `coordinator --cmux-transport`，不要再次运行 `up`。

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
