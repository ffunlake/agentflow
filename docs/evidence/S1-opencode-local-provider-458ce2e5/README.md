# S1 OpenCode 既有 provider / 本地端点只读检查

协作 session：`458ce2e5-e679-4fb0-a769-897ce24d33ed`；task：`s1-opencode-local-provider`。检查时间：2026-11-02 06:30–06:31 UTC。本轮检查完成，模型依赖仍阻塞：**MODEL_PROVIDER_UNAVAILABLE / WAITING_DEPENDENCY**。完整 S1 保持 **PARTIAL**；OpenCode 保留历史 **VERIFIED_TRANSPORT_ONLY**；新增 `runtime_passes=[]`。

只读检查原有隔离服务及其配置视图，没有重新登录、购买、输入 key、复制凭据、修改配置或调用模型。Codex/Claude 本轮无运行时操作，既有 Codex 16 项受控通过沿用前轮证据，不重新验收。没有进入 S2、创建 worktree 或提交。

## 实际观察

| 检查 | 实际结果 | 原始证据 |
|---|---|---|
| OpenCode 版本 | CLI `--version` 与已有 server health 均为 `1.19.5` | [CLI](opencode-version.json)、[服务](server-health.json) |
| 原隔离 server | PID `26680`，`--pure serve --hostname 127.0.0.1 --port 19731` 仍存活 | [进程](opencode-processes.json) |
| 原 native session | `ses_e65a4cf24ffe3wZAiM2U7og4Px` GET 200，仍绑定隔离 cwd 与 `opencode/big-pickle`；状态 idle | [session](native-session.json)、[status](session-status.json) |
| 已有 provider | `/provider` 的 `connected=["opencode"]`；`/config/providers` 也只有该 provider；默认 `big-pickle` | [provider 投影](providers-retry.json)、[有效 provider/model 列表](config-providers.json) |
| 可用列表中的模型 | `flint-40b-free`、`nemotron-3.5-super-vl-free`、`beam-free`、`mimo-v2.6-flash-free`、`nemotron-3.5-lightning-free`、`big-pickle`、`glm-5.3-flash-free`、`deepseek-v4.1-flash-free`；均为零价免费模型，未调用 | [模型 ID、状态和价格](config-providers.json) |
| 本地 provider 配置 | 有效配置未声明任何自定义 provider；内置目录中的 `lmstudio` 未连接，目录条目不代表本机模型可用 | [配置投影](effective-config.json)、[目录与 connected 分离](providers-retry.json) |
| Ollama / LM Studio / 常见本地模型端点 | `11434/api/version`、`11434/api/tags`、`1234/v1/models`、`8080/v1/models`、`8000/v1/models` 均连接拒绝 | [本地 GET 结果](local-endpoints.json) |
| 已有 Python HTTP 服务 | `5000/v1/models`、`8787/v1/models` 均 HTTP 404，没有发现模型列表 | [监听信息](local-listeners.json)、[本地 GET 结果](local-endpoints.json) |
| 原可见 surface | window `CAE5B3FE-3E61-4E92-9896-775B9343687F` 定向查询退出 1 / `Window not found`；原 attach PID `44608` 不存在；未重建窗口或改投其他 surface | [目标查询](target-tree.json)、[进程](opencode-processes.json) |

结论只覆盖原隔离 OpenCode 环境、记录的监听信息与上述本机端点，不据此断言整台机器或其他配置不存在模型。`/provider` 的首次记录达到 4 MiB 上限，保留 [原失败记录](providers.json)；随后仅重试同一只读 GET，将读取上限设为 32 MiB，实际返回 7,213,738 bytes，保存非敏感字段投影。没有保存 provider option 值、API key、认证文件或原始配置内容。`option_keys_only` 中的字段名不能证明可用凭据。

## 模型验收门槛

未发现无需配置变更即可在原隔离 session 使用的本地 provider；同时原可见目标已不可用，所以没有发送 nonce、重置会话或重启客户端。本轮模型请求与免费额度模型请求均为 **0**。前轮 `free_tier_limit` 仅作为 [历史事实](../S1-manual-login-retry-a128f1ae/opencode-verification.json) 保留，本轮没有重试额度，也不推断当前额度是否已恢复。

| 验收项 | 本轮实际完成情况 |
|---|---|
| 既有 transport | 沿用历史 `VERIFIED_TRANSPORT_ONLY`，本轮未重新投递 |
| 同 native session + 可见 surface + 唯一 nonce + 真实模型 ACK | **WAITING_DEPENDENCY**，没有发送、没有 ACK、新增 runtime pass 为 0 |
| reset / READY | **UNVERIFIED**，本轮未执行 |
| 角色重注入与执行 | **UNVERIFIED**，本轮未执行 |
| 实际工具调用身份 / 刷新 | **UNVERIFIED**，API 元数据不作为工具调用身份通过 |
| 有效 full 权限 | **UNVERIFIED**，本轮没有模型工具操作或权限修改 |
| 模型上下文恢复 | **UNVERIFIED**，native session GET 成功不等于模型恢复 |

机器结果见 [opencode-verification.json](opencode-verification.json)、[runtime-matrix.json](runtime-matrix.json)、[dependencies.json](dependencies.json)。正式协作 READY/handoff、HTTP 200、健康端点或 session 元数据均不计入模型 ACK。

## 工程与证据核验

仅新增本目录的记录器、证据和报告，并更新两份 S1 汇总。Go 源码、构建定义、原隔离配置及两轮输入的顶层证据以哈希前后对照，结果见 [protected-files-verification.json](protected-files-verification.json)。哈希范围由 snapshot 列明，不递归读取任何旧 scratch 凭据。

实际 `go test ./...`、`go build ./...`、`make check` 均退出 **0**，原始输出分别见 [test](go-test.json)、[build](go-build.json)、[check](make-check.json)。使用与 Makefile 一致的 checkout 内缓存和 `GOFLAGS=-mod=readonly`。本轮记录器为 [collect.py](collect.py)，结构化验收见 [evidence-validation.json](evidence-validation.json)。

下一步仍限 S1：只有已接入且允许使用的非免费额度本地 provider 与同 native session 的安全可见目标都具备时，才能进行唯一 nonce 的真实模型 ACK 检查。当前授权没有恢复这些依赖；不启动登录、不购买、不修改 provider 配置。正式回传结果单独记录在 [handoff.json](handoff.json)，其 DONE 只表示本轮检查与汇总完成。
