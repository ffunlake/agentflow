# Claude OAuth headless compatibility — 2026-11-04

Session: `claude-oauth-headless-compat-20261104-01`。角色：资深研发（调查/最小修复），`ralph-loop`。

**显式 non-bare OAuth 认证兼容与真实无工具 workflow 验收 PASS；不代表严格 bare 安全默认已获真实验收。** 同一 OS 用户（UID 501）下，已安装的 Claude Code **2.1.318** 在明确含 `--safe-mode`、不含 `--bare` 的 print/stream-json argv 下成功复用 CLI 已有认证，返回固定 nonce。全新隔离 run 完成两个角色 READY、blocked→显式 resume、nonce handoff→review approve→`COMPLETED/revision=5`，以及暂停/完成后的正常关闭重开和幂等重放。**所有真实权限效果仍为 UNVERIFIED；产品整体保持 PARTIAL。**

2026-11-04 P1 更正：此前 adapter 错把空 args 默认选为 non-bare。本次 [默认值修复](claude-oauth-headless-default-fix-20261104.md) 恢复所有 Claude headless/native 的严格 `--bare` 默认，只允许冻结的 `args: ["--safe-mode"]` 明确 opt-in。attempt-2 原始 team.json 的 args 为空，它由当时 adapter 生成显式 non-bare argv；**不能把历史配置说成已包含新的配置 opt-in**。原始 artifacts 保留，不重写历史源配置或伪造新真实请求。

## 认证边界与最小选择

本机 `claude --help` 明确声明：

- `--bare` 跳过 keychain；Anthropic 认证只接受 `ANTHROPIC_API_KEY` 或显式 settings 中的 `apiKeyHelper`，不读取 OAuth。第三方 provider 使用自己的认证。
- `--safe-mode` 禁用自定义 CLAUDE.md、skills、已安装插件、hooks、MCP 等扩展，认证、模型选择、内置工具/插件和权限保持正常。
- `--tools ""` 禁用工具；`--no-session-persistence` 仅用于 print 模式，不能与本产品的 persistent resume 验收同时使用。

OAuth 兼容路径使用显式 non-bare `--safe-mode`，由官方 CLI 决定现有认证优先级，不强制覆盖 CLI 的其他认证来源。当前严格默认已恢复为 `--bare`，空 args 不再启用该兼容路径。两种模式均明确加入 `--strict-mcp-config --setting-sources "" --disable-slash-commands --no-chrome`，并继续使用 `--permission-prompts none`。safe mode 的 init 仍列出四个 `source=builtin` 插件，不能描述为没有任何内置插件或没有 CLI 内部运行时功能。

当前精确配置选择：Claude 空 args 或 `args: ["--bare"]` 使用严格 API-key/provider 模式；只有 `args: ["--safe-mode"]` 明确 opt-in 才使用 non-bare OAuth 兼容路径。该选择通过现有 resolved config 的 args/Digest 保存；startup、resume 和 coordinator 正常重开均复用它。不新增配置字段、认证代理、凭据复制或服务，不自动登录或认证回退。其余 raw args 继续拒绝。默认 permissions 不增加 bypass；`permissions=full` 的既有 flag 语义不变。

生产代码仅修改 [native.go](../../internal/runtime/native.go)、[headless_claude.go](../../internal/runtime/headless_claude.go) 和 [headless_claude_events.go](../../internal/runtime/headless_claude_events.go)。Codex/OpenCode 的命令与行为不变。

## 直接 nonce probe

真实入口：`/Users/funlake/.nvm/versions/node/v26.8.1/bin/claude`。stdin 只要求返回 `CLAUDE_OAUTH_HEADLESS_COMPAT_20261104_01`，禁止工具和文件操作。参数为：

```text
--print --safe-mode --verbose --output-format stream-json --input-format text
--permission-prompts none --prompt-suggestions false --tools ""
--strict-mcp-config --setting-sources "" --disable-slash-commands --no-chrome
--no-session-persistence
```

每次使用全新 0700 临时 cwd，45 秒 deadline；超时终止该 probe 自有 process group 并等待回收。

| 调用 | 结果 | 时间 | cwd 变化 |
| --- | --- | --- | --- |
| 首次 PATH 入口 | 经 cmux shim 成功返回 nonce；从 adapter 验收中排除 | 6.226s | 空目录保持为空 |
| 首次真实二进制 | 只有 init；45 秒超时后 SIGKILL，exit -9；不计成功 | 45.023s | 空目录保持为空 |
| 真实二进制复测 | assistant 与 result 精确匹配 nonce，exit 0、`is_error=false`、`end_turn`、`terminal_reason=completed` | 5.136s | 空目录保持为空 |

成功流包含 `system/init → assistant → rate_limit_event → result/success`；`tools=[]`、`mcp_servers=[]`。`apiKeySource=none` 在成功流仍然出现，**它只说明没有 API key 来源，不能单独解释为未登录或 OAuth 失败**。成功以真实 assistant/result、终止原因和退出码共同证明。

完整探测摘要见 [probe-summary.json](claude-oauth-headless-compat-20261104/probe-summary.json)。成功流转为 [回归 fixture](../../internal/runtime/testdata/claude-oauth-nonce.jsonl)：路径、session/request ID 与 expiry 时间已规范化，保留实际协议字段；usage 金额有舍入。fixture 是脱敏派生样本，不冒充原始逐字节流。

## Parser 兼容与失败保留

non-bare 真实输出需要补齐以下 exact allowlist 元数据：init 的 analytics、original cwd、内置消息 socket、effort/view 信息；assistant 的 request ID、thinking 展示/计时；message 的 `stop_details/input_transformations/diagnostics`；订阅 `rate_limit_info` 的窗口/overage 字段；result 的 `subagent_stats/safety_stops/model_usage_since_last_result/credential_expires_at`。

这些字段仅作为元数据，不决定 READY、final text、workflow 完成或权限。未知事件/字段、重复键、身份不符、缺失 assistant/result、结果文本不匹配和原有失败门禁继续拒绝。thinking 不参与 final text。

第一轮真实 workflow 的两个 READY、pause、重开、builder resume/handoff 均成功；reviewer 已生成 approve，但额外 thinking 元数据被旧 allowlist 拒绝。保留其 [失败响应](claude-oauth-headless-compat-20261104/workflow-resume.stdout.log)、[manifest](claude-oauth-headless-compat-20261104/validation.json) 和原始 captures，状态仍为 `BLOCKED_CONFIRM`。未知 `SUBMIT_REQUESTED` 没有被删除、重发或伪造为 SENT。修复 thinking 字段并通过离线回归后，第二轮在**全新 run 目录**验收；两个目录使用相同逻辑 run ID，不共享 DB/session。

## 最终真实全流程

最终证据为 [attempt-2](claude-oauth-headless-compat-20261104/attempt-2/)，不是前一轮失败目录。入口 [native-no-tools.py](claude-oauth-headless-compat-20261104/native-no-tools.py) 记录调用并 `exec` 真实安装的 CLI，仅在 adapter argv 后追加 `--tools ""`。不替换认证、stdin 或输出。工具限制是本轮验收约束；产品默认工具集合与权限不能据此算作已验证。

每个原生 turn 超时 50s，startup 105s；workflow IPC 110s、外层命令 115s。所有调用均在 `/private/var/folders/wv/x8nwj9tj7xz5dhqf5f2mt3hw0000gn/T/opencode/aco-alqe6pj0/cwd`，run/context 与 cwd 分开。

| 验收项 | 结果 | 证据 |
| --- | --- | --- |
| builder/main READY | PASS，generation 1、独立 session、随机 challenge 回显并登记 | [up](claude-oauth-headless-compat-20261104/attempt-2/up.stdout.log) |
| 初次执行与显式决策 | PASS，blocked→`PAUSED/revision=2` | [start](claude-oauth-headless-compat-20261104/attempt-2/workflow-start.stdout.log) |
| 暂停后正常关闭重开 | 脚本当时断言 PASS，start exact replay 无新调用；历史前后 DB 未归档，不能独立复算 DB 相等 | [replay](claude-oauth-headless-compat-20261104/attempt-2/paused-start-replay.stdout.log) |
| Persistent builder 再次投递 | PASS，沿用同一 UUID，返回 nonce handoff | [native invocations](claude-oauth-headless-compat-20261104/attempt-2/native-invocations.jsonl) |
| Reviewer approve 与完成 | PASS，显式 accepted=[true]，`COMPLETED/revision=5` | [resume](claude-oauth-headless-compat-20261104/attempt-2/workflow-resume.stdout.log) |
| 完成后重开、start/resume 重放、status/drain | 脚本当时断言 PASS，无新 native 调用；历史前后 DB 未归档，不能独立复算 DB 相等 | [terminal drain](claude-oauth-headless-compat-20261104/attempt-2/terminal-drain.stdout.log) |
| 真实原生调用 | 5 次完成，2 startup + 3 resume | manifest 与 5 份原始 captures |
| 工具/文件行为 | tools/MCP 为空，无 tool_use/tool_progress；cwd 仍为空 | [manifest](claude-oauth-headless-compat-20261104/attempt-2/validation.json) |
| 清理 | 自有 coordinator 均正常退出，run lock 已移除；私有临时 run/context 保留供复核 | 同上 |
| 有效权限、full/bypass、文件读写拒绝、崩溃/stale-lock 恢复 | **UNVERIFIED** | 未执行这些能力探针 |

最终 native session：builder=`96aace09-818c-4c8c-98e2-a0d8109c3ca8`，main=`e8981944-99c9-48fd-8009-d28fa18206c9`。builder 两次 resume，main 一次 resume。最终 [DB 导出](claude-oauth-headless-compat-20261104/attempt-2/final.db.json) 与 captures 保存 run/agent/generation/message/session 关联。

历史脚本仅在内存比较 paused/completed 的前后 DB，只归档 final.db.json，缺少可独立复算的历史 before/after。当前修复已补充两种认证模式的**离线 fixture** paused/completed reopen/replay 前后完整 DB 导出、calls 与 SHA-256，并提供独立校验脚本，见 [新证据](claude-oauth-headless-default-fix-20261104.md)。不将离线样本冒充 attempt-2 的历史快照，也不补跑真实请求。

本轮只检查 API key/token/provider 环境变量的**存在性**，列出的七个变量均不存在；没有读取其值、凭据文件或 keychain 内容。官方 CLI 自己管理认证。没有重新登录、读取/操作用户 TUI、修改用户配置或访问 wiki。`jev-judge` 未安装，没有编造外部风险判断。

## 工程验证与回传

[Command manifest](claude-oauth-headless-compat-20261104/verification.json) 保存命令、退出码、耗时及源文件 SHA-256。测试移除了真实 Codex opt-in，仅 fixture/本地工程测试会运行。

| 检查 | 结果 |
| --- | --- |
| Claude focused parser/command/CLI 回归 | PASS |
| `go test ./...` | PASS，exit 0，47.285s |
| `go build ./...` | PASS，exit 0，0.812s |
| `make check` | PASS，exit 0，56.565s，含格式、modules、vet、全量测试及构建 |
| `go mod tidy -diff` | PASS，exit 0，无 diff |
| `CGO_ENABLED=1 go test -race ./internal/runtime ./internal/coordinator ./internal/cli ./internal/store ./internal/loopcore ./internal/lock ./tests/e2e` | PASS，exit 0，118.563s |

上表是历史检查，曾包含默认 non-bare、显式 bare，反映了 P1 默认值问题；本轮已反向覆盖默认 bare、显式 non-bare opt-in、拒绝额外 args/provider、startup/resume/reopen 冻结以及两种模式的 DB 前后相等。真实派生 fixture、thinking 隔离与歧义字段拒绝仍保留。当前验证结果见 [默认值修复](claude-oauth-headless-default-fix-20261104.md)；历史 verification.json 源码哈希不代表当前源码。

真实重放脚本 [validate.py](claude-oauth-headless-compat-20261104/validate.py) 会发起真实模型请求，已有证据目录拒绝覆盖。首次脚本诊断工具曾因命名 `inspect.py` 遮蔽 Python 标准库，导致重验启动前报错；已改名为 `inspect-captures.py`，该失败启动没有真实模型调用。离线 [check-evidence.py](claude-oauth-headless-compat-20261104/check-evidence.py) 只核对本轮 artifacts 和源文件指纹，不访问认证或模型。

所有指定检查完成，`source_unchanged=true`。离线 `python3 docs/evidence/claude-oauth-headless-compat-20261104/check-evidence.py` 已实际运行，exit 0，返回 `outcome=PASS`；核对 source SHA、5 次调用、两个 session、READY actor/challenge、无工具、exact replay 和 `COMPLETED/revision=5`。权限仍 `UNVERIFIED`。

按用户指定的 window 1/session 回传 DONE；transport 确认另记于下方。当前 workspace 无 git 元数据，没有提交或推送。

已实际执行 `/Users/funlake/.codex/skills/okra-agent-init/scripts/agent-handoff.sh send --window 1 --session-id claude-oauth-headless-compat-20261104-01 --status DONE`，提供 summary/evidence/next，exit 0，确认：

```text
[DONE] handoff sent: id=aded0f25-9b63-46e0-b238-8a7974848d02 window=1 status=DONE
```

这是主 agent 通知的发送确认；不替代主 agent 复核或扩大权限验收结论。

已收到相同 handoff ID 的正式 ACK：主 agent 已收到 non-bare OAuth PASS，并交 2 号做最终只读核对后收口。
