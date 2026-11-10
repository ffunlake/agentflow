# S4 OpenCode/Codex 真实接线与最小现场验收

日期：2026-11-03。Session：`83a87513-a6ab-47fe-b23d-1d5e466569a6`。Task：`s4-real-opencode-codex`。角色：资深研发。

**整体 PARTIAL。前台 launcher/adapter 的代码与进程集成验证 PASS；指定两个真实 TUI 的唯一 nonce → 可见 assistant ACK 为 PASS。原生 JSONL adapter 尚未连接真实模型验证，完整团队启动未完成。** 没有把进程启动、native ID、用户输入回显或 ACK 写成角色 READY 注册通过。

## 代码与最小方案

- `internal/runtime/native.go`：`ForegroundLauncher` 实现现有 `Launcher`，调用现有 `BuildCMUXCommand` 校验规范启动数据，复用私有 snapshot、`Start` 和 `Registry.Register`。当前仅支持单 seat 的所属前台终端；拓扑命令继续独立保存，不执行 shell 文本。
- 隐藏 `_seat` 保持原有严格参数与初始快照校验。通过 `AGENTFLOW_NATIVE_OPTIONS` 接收显式 window/workspace/surface 和绝对 tool/cmux binary。核对 `cmux tree` 的真实 caller 后，使用 `exec.CommandContext(binary, argv...)` 和 `cmd.Dir=Seat.Cwd` 执行工具，不使用 `sh -c`、`--command` 或 cwd 拼接。
- OpenCode：`run --format json --dir CWD`；Codex：`exec --json --color never --skip-git-repo-check --cd CWD -`。冻结角色正文和密码学随机 nonce 经 stdin 输入。model 是独立 argv；额外 args 只支持 OpenCode 的一对 `--variant VALUE` 或 Codex 的一对 `--profile VALUE`，拒绝其他覆盖参数。
- READY 必须来自完成的 native assistant 事件，JSON 必须精确匹配 run/agent/generation/tool/status/nonce。session/thread ID 来自同一 native 事件流，workspace/surface UUID 来自独立核对的 cmux caller，不允许模型指定。错误身份、旧代际、native ID 改变、错误事件、缺少完成、截断和进程失败均不注册 READY。已获得的 native ID 在 READY 失败时保留于结果。
- wrapper/native context、90 秒 CLI 上限、有限输出和取消处理均有界。原生 stderr 和私有配置不会进入公开错误。`Start` 保留现有 launch journal、注册校验和事务；单个 launcher 不重放。直接 `_seat` 是底层执行入口，没有 store 级重放门禁，正式编排须用现有 run lock 与 `Start`。
- `full` 映射到 OpenCode `--auto`、Codex `--dangerously-bypass-approvals-and-sandbox`，有效权限仍 UNVERIFIED。fresh/persistent 在初始启动时均创建首次 session；后续 reset/resume 没有接入。Claude 返回未实现错误，不查找或启动 Claude。

## 现场边界与证据

被测范围固定为 `window:1000060005 / workspace:1000060011`。OpenCode `surface:1000060034`（UUID `5265DA42-9A45-4C71-BABE-155E458FCF33`），Codex `surface:1000060035`（UUID `94EBBAAE-5EA1-4352-B183-580E24F0F23C`）。窗口 UUID 为 `73FEB4F4-BE2B-4F4D-9B48-E872BBC51EA3`，workspace UUID 为 `3AB7D16C-B09D-44AE-8CD5-5B6225388D45`。

`preflight-tree.json` 保存作用域内拓扑；`*-before.json`、`*-after-presend.json` 保存发送前的空闲空输入框。原生 provider 与 cwd 同时有 TUI、进程身份和 `lsof cwd` 证据：OpenCode PID 54924 / ttys003，Codex PID 55194 / ttys004，均为 `/Users/funlake/Projects/agentflow`。进程证据只保存这两个已定位 PID 的内核程序名（`ucomm`），不保存完整子进程命令行或环境。

发现一个必须保留的差异：Codex 发送前的 cmux `resume_binding` 是旧 checkpoint `01a19b3b-9fc2-7622-a43f-af895cfac312`，其中 cwd 指向另一个项目；当前 TUI 和真实进程 cwd 已是 agentflow。没有执行该恢复记录中的任何命令，也没有访问其项目。先核对当前进程身份与 cwd，再发送一次 nonce。ACK 后 cmux 自己更新了当前绑定，thread ID 为 **`01a19f58-8923-7731-88f5-e99cf15fe4bd`**，cwd 为 agentflow（`codex-resume-after-final.json`）。这证明不能用旧 restore record 代替当前绑定。

| 工具 | 唯一 nonce | 实际动作 | 现场结果 |
|---|---|---|---|
| OpenCode | `AF-S4-83a87513-opencode-4b7c62e9` | 一次显式 `send --paste`，一次独立 `send-key enter` | **PASS**：独立 assistant 文本 `ACK AF-S4-83a87513-opencode-4b7c62e9`，9.8 秒完成 |
| Codex | `AF-S4-83a87513-codex-d8351fa6` | 一次显式 `send --paste`，一次独立 `send-key enter` | **PASS**：`• ACK AF-S4-83a87513-codex-d8351fa6`，6 秒完成 |

原始证据为 `*-send-intent.json`、`*-send.json`、`*-submit.json`、`*-after-final.json`。每条 cmux 现场动作均有显式 window/workspace/surface；tree 无 surface 参数，显式指定 window/workspace。发送意图先独占落盘，失败/未知不重发。本轮各工具恰好一次测试输入，无 `/new`、草稿清除、权限切换或认证动作。Codex 原有 “Permission selection requested” 是已有记录，发送前没有弹窗；空输入框可见。

OpenCode 的 cmux resume record 在 ACK 前后均为空，当前 OpenCode native session ID **UNVERIFIED**。本轮未用临近 session 或最新 session 猜测其 ID。现场 ACK 只说明可见 TUI 接收并回应，不进入任何产品数据库 READY/ACK 状态。

## 测试与检查

`internal/runtime/native_test.go` 验证独立 assistant 来源、完整 actor/generation/nonce 校验、native ID 改变、错误/未完成/非 assistant 事件、严格 JSON、字面 argv 与覆盖参数拒绝。`tests/e2e/native_launcher_test.go` 构建实际 AgentFlow wrapper 和原生 JSONL fixture 子进程，通过真实 SQLite 验证完整启动注册，并覆盖缺失 READY、过期 generation、子进程错误、目标不匹配和取消。cwd 含空格、引号及 shell 元字符，正文不进入 argv。fixture 不是已安装工具的现场运行。

原有 CLI/e2e 离线保护测试继续确认：没有显式 native options 时不启动 OpenCode/Codex/cmux；Claude 始终未实现。未新增依赖、schema 或后台服务。

| 实际检查 | 结果 | 原始证据 |
|---|---|---|
| `go test ./internal/runtime ./internal/cli ./tests/e2e` | PASS，退出 0 | 首轮工具输出；随后包含于全量检查 |
| `make check` | PASS，退出 0；格式、依赖校验、vet、全量测试、CLI 构建 | `make-check.json` |
| `go build ./...` | PASS，退出 0 | `go-build.json` |
| `go mod tidy -diff` | PASS，退出 0，无差异 | `go-tidy.json` |

## 保留项与下一步

1. **UNVERIFIED**：已安装 OpenCode/Codex 的真实 JSONL → `_seat` → `Start` → READY 注册链路。本轮现场授权只允许每个已有 TUI 一条 nonce，已全部使用；未额外启动模型进程冒充现场验收。下一轮应在独立所属前台终端中用单 seat 私有快照各验收一次完整链路。
2. **PARTIAL**：完整团队 `up`、cmux 拓扑创建与 shell-free wrapper argv 传输。已验证 cmux `--command` 只接受文本；现实现不伪造 argv/cwd/ratio API，也不向旧/控制终端输入启动命令。需要明确选定可验证的 native argv 传输能力后接入团队编排。
3. **UNVERIFIED / 未实现**：有效 full 权限、OpenCode TUI session 身份、真实 attach/stop、S5/S7 原生 reset/submit/recovery。当前错误与启动状态不推断存活。
4. **未实现**：Claude adapter，按本轮范围保留。代码目录没有 Git 元数据，交付以文件与测试证据为准。

`collect.py` 保存本轮固定范围命令的真实结果；`verify_evidence.py` 可离线核对 scope、一次发送和可见 assistant ACK，不会访问 cmux。正式 handoff 的 payload 与传输回执保存在同目录；它是协作回传，不是产品 READY 验收。

## 正式 handoff

已在 2026-11-03T03:43:05Z 通过 `cmux agent message` 向现有 main 路由 `surface:1000060018` 提交 `[HANDOFF]` / `type=agent_handoff`，payload ID `f40b5152-baa7-45b1-8bb1-23b337a348fc`。命令退出 0，cmux 回执 ID **`505a2ddc-76e4-42ca-b969-0391b551ea68`**，`recipient_has_agent=true`，状态 **queued**；`delivered_at/read_at` 为空，主 agent 读取与验收 ACK 尚未确认。

使用原生消息队列是为了遵守本轮控制终端的操作边界：原有 `agent-handoff.sh` 会向 main 输入框发送文本和提交键，本轮仅发送同格式正式 payload 到 agent hooks/inbox。没有向主控/worker 控制终端注入输入，也没有为检查投递而读取其屏幕。回执详见 `handoff-receipt.json`；queued 只证明 cmux 接受投递，不表示主控已验收。
