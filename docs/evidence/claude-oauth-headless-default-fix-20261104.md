# Claude headless/native 严格默认值修复 — 2026-11-04

Session：`claude-oauth-headless-default-fix-20261104-01`。固定角色：资深研发（写代码），协作模式：`ralph-loop`。

修复 P1：Claude headless/native 的空 args 恢复为严格 `--bare`，只有冻结配置中精确的 `args: ["--safe-mode"]` 才启用 non-bare 官方 OAuth 兼容路径。本轮仅本地工程检查与可执行 fixture，**没有启动真实模型、登录、读取/复制凭据或操作用户窗口**。有效权限、full/bypass、真实文件读写拒绝和 crash/stale-lock 恢复均保持 **UNVERIFIED**；产品整体 **PARTIAL**。

## 配置及冻结契约

| Claude resolved args | native/headless argv | 认证边界 |
| --- | --- | --- |
| 缺省、null 或空数组 | `--print --bare ...` | 严格默认，不读取 OAuth/keychain |
| `["--bare"]` | `--print --bare ...` | 显式选择同一严格模式 |
| `["--safe-mode"]` | `--print --safe-mode ...` | 明确 non-bare opt-in，由官方 CLI 复用已有认证，包括 OAuth |
| 其他 flags、组合、重复、provider 参数 | 执行前拒绝 | 不允许覆盖认证、协议或 session transport |

non-bare 选择按 agent 设置，进入现有 `config.Resolve` 的独立 args 副本及 Digest。`runtime.Compile` 再复制完整配置；`Start` 保存不可变 `config.resolved.json` 和 DB `runs.config_snapshot/config_digest`。headless startup 和 persistent submit 均从冻结配置调用同一 `NativeCommand`；coordinator 正常重开读取 startup snapshot，校验 DB digest，再建立 adapter。编辑原配置不改变已有 run。没有认证结果驱动的模式切换、自动回退、新环境 opt-in 或凭据处理代码。

保留既有 `--strict-mcp-config --setting-sources "" --disable-slash-commands --no-chrome` 和 `--permission-prompts none`。`permissions=full` 的既有 bypass flag 语义保留，实际效果未验证。safe mode 自定义扩展禁用不等于不存在 CLI 内置插件。

生产修改仅为 [native.go](../../internal/runtime/native.go) 的 Claude 分支及 [probe.go](../../internal/probe/probe.go) 的 Claude 候选 argv。Codex/OpenCode 的生产 argv 与行为不变。未扩展认证代理、TUI、后台服务或其他工具。

## 反向回归与归档

[命令回归](../../internal/runtime/headless_claude_test.go) 将旧的默认 non-bare 断言反向为默认 bare，覆盖显式 bare/opt-in、resume、重复/冲突 flags、未知 provider、NUL、协议/session/raw flags 与其他工具滥用此 opt-in。真实派生 [OAuth fixture](../../internal/runtime/testdata/claude-oauth-nonce.jsonl) 仍离线验证成功流、thinking 排除、歧义字段和失败门禁；它不改变认证选择，也不证明 bare 默认可用 OAuth。

[CLI fixture 回归](../../tests/e2e/headless_codex_test.go) 使用真实 AgentFlow 子进程、IPC 和本地编译的 native 替身；替身从不启动模型。五个场景：`success`（默认 bare）、`blocked-resume`（默认 bare）、`bare`（显式 bare）、`oauth`（显式 opt-in）、`oauth-blocked-resume`（显式 opt-in）。所有场景在 startup 后把源配置改为相反认证模式，验证随后的 resume argv 仍保持冻结选择。

[快照辅助函数](../../tests/e2e/headless_evidence_test.go) 在只读事务中按表名/rowid 导出所有 SQLite 表的完整列与行，包括空表和 sqlite_sequence；按 JSON 字节归档内容。哈希比较的是完整表内容，**不是 SQLite 数据库文件的物理字节**。每个场景归档 completed 的 before-reopen、after-reopen、after-replay；两个 blocked 场景还归档 paused 的三个时间点。每点同时归档 `calls.jsonl`，验证正常重开与 exact replay 不增加原生替身调用、不改变 DB 内容。

归档目录：[fixtures/](claude-oauth-headless-default-fix-20261104/fixtures/)。每个场景含源配置前后副本、冻结 snapshot，以及上述 DB/calls 前后 artifacts。`blocked-resume` 和 `oauth-blocked-resume` 分别有 3 次调用时的暂停快照、5 次调用后的完成快照；其余场景完成时为 4 次调用。保留 generation 1 及两个独立 native session，resume 指向原 UUID。

独立复算脚本：[check-evidence.py](claude-oauth-headless-default-fix-20261104/check-evidence.py)。它只读归档，不访问认证、模型、TUI、网络或原临时 DB；检查源码指纹、全部检查退出码、每组 DB/calls SHA-256 相等、DB 与 snapshot 的 config/digest 一致、源配置修改和实际 argv 的冻结对应关系。复算命令：

```sh
python3 docs/evidence/claude-oauth-headless-default-fix-20261104/check-evidence.py
```

## 历史 attempt-2 的准确边界

[历史认证兼容报告](claude-oauth-headless-compat-20261104.md) 已同步更正：attempt-2 的五次真实调用明确带 `--safe-mode` 且不带 `--bare`，是 **显式 non-bare OAuth argv 的真实无工具验收**，不能据此宣称严格安全默认已通过真实验收。历史 team.json args 为空，由当时存在 P1 的 adapter 生成该 argv；不把历史配置伪装成含有本轮新增的配置 opt-in。

历史脚本只在内存断言重开前后 DB 相等，未归档这些 before/after。保留原始 attempt-2 artifacts，仅更正其“可独立复算”结论；本轮新增的归档是明确标注的**离线 fixture**，不冒充历史真实运行快照。[历史校验脚本](claude-oauth-headless-compat-20261104/check-evidence.py) 现分别报告历史 artifacts PASS、`historical_source_matches_current=false` 和 `historical_db_before_after_archived=false`。已离线执行该脚本，exit 0。历史真实验收脚本已补精确配置 opt-in 和未来 before/after 导出，**本轮没有执行该真实请求脚本**。

## 工程验证

结果清单：[verification.json](claude-oauth-headless-default-fix-20261104/verification.json)。执行器 [verify.py](claude-oauth-headless-default-fix-20261104/verify.py) 显式移除真实 Codex integration opt-in 和真实 evidence 环境变量；仅在单独 fixture 归档任务中设置 `AGENTFLOW_CLAUDE_FIX_EVIDENCE`。后续全量/race 检查不覆盖首次归档。

全部通过，退出码均为 0；验证前后 `source_unchanged=true`。没有 Git 元数据，未提交或推送。

| 检查 | 结果 | 耗时 |
| --- | --- | --- |
| 5 个 Claude CLI fixture 场景及归档 | PASS | 10.435s |
| Claude runtime/coordinator 与 probe focused | PASS | 10.978s |
| `go test ./...` | PASS | 46.743s |
| `go build ./...` | PASS | 0.395s |
| `make check`（fmt、依赖校验、vet、test、build） | PASS | 48.939s |
| `go mod tidy -diff` | PASS，无 diff | 0.176s |
| `CGO_ENABLED=1 go test -race ./internal/runtime ./internal/coordinator ./internal/cli ./internal/store ./internal/loopcore ./internal/lock ./internal/probe ./tests/e2e` | PASS | 113.517s |
| 独立离线归档复算 | PASS，7 组、57 份 artifacts | exit 0 |

首次 `check-evidence.py --record` 生成 [audit.json](claude-oauth-headless-default-fix-20261104/audit.json) 后，又执行不带参数的校验，重新计算并逐项核对记录，exit 0。每组三个 DB 导出字节哈希相同、三个 calls 导出字节哈希相同。下面两种模式的 paused/completed DB SHA-256 可直接用 `shasum -a 256` 与归档文件复算；完整 57 个文件的指纹均保存在 audit.json。

| 场景/阶段 | DB SHA-256 |
| --- | --- |
| 默认 bare / paused | `762d5dbddcca83482bcd58f23a1f29a9e6ec60d38bba241057b7e173749a580c` |
| 默认 bare / completed | `7504bf4dad30cfc228285d16a4333dd81a60e91b5b700eb3a3bf9b62d4487093` |
| 显式 OAuth opt-in / paused | `ab42f80af9d89568ee91d3dfe4ac3a9380ef859f4d51c47356843e41d422f96d` |
| 显式 OAuth opt-in / completed | `fe51e47e3e05aff12b089a418d6c9efdc0880bc5ea9799d27f7b2fbdde261b6a` |

## Handoff

已实际执行 `/Users/funlake/.codex/skills/okra-agent-init/scripts/agent-handoff.sh send --window 1 --session-id claude-oauth-headless-default-fix-20261104-01 --status DONE`，并提供 summary/evidence/next。命令 exit 0，transport 确认：

```text
[DONE] handoff sent: id=6b18c849-1117-4c0b-b32e-1db3ffc84631 window=1 status=DONE
```

本轮使用 [okra-agent-init 协作协议](/Users/funlake/.codex/skills/okra-agent-init/SKILL.md) 的用户指定正式回传通道。该发送确认不替代主 agent ACK、2 号独立复核或扩大任何权限/恢复验收结论。

已收到相同 ID 的正式 ACK：`主 agent 已收到 P1 修复，开始 2 号独立 review。` 独立复核结果由主 agent/2 号回传，本报告不提前宣称 review 通过。
