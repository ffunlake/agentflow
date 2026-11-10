# CMUX workflow transport v1 交付证据

Session：`cmux-workflow-transport-v1-20261105-01`  
日期：2026-11-05  
角色：资深研发（写代码）  
范围：将已有 workflow runner 接入已登记的 cmux agent message/inbox，提供显式前台 dispatch → structured reply → 原子 workflow 推进。

## 交付行为

`coordinator.OpenCMUXTransport` / `coordinator --cmux-transport` 现在安装 `WorkflowTransportAdapter`。`workflow start/resume` 提交新事件后只投递该事件新生成的 outbox；历史请求返回原有 receipt，正常重开不投递、不 drain。`workflow drain` 核对 lead/current generation/revision，领取尚未发送的当前 outbox，然后读取原 dispatch sender（lead）的登记 surface inbox。没有结构化结果时返回当前状态，不等待未来消息。

`workflow --cmux-transport [--cmux-binary /absolute/cmux]` 是单次前台分支：同进程持有原 run lock、提供本地 IPC、完成一次请求并关闭。已有 coordinator 或遗留锁时拒绝竞争；未指定选项的 workflow CLI 仍只调用已有 IPC。两种路径使用同一 coordinator、store 和 runner。

出站 JSON 为 `workflow_dispatch`，携带原 outbox `request_id/message_id`、sender/recipient 的 run/agent/generation/launch/workspace/surface 绑定及原 `loopcore.Intent`。原 workflow outbox 的消息 ID、请求 ID、收件者和代际保持原值，没有额外消息或任务。

入站严格 JSON 为 `workflow_reply`，包含版本、request_id、actor、message_id、body、status 和 workflow revision/kind。actor 使用已有 IPC 字段 `runId/agentId/generation`。native `sender_name` 必须精确等于 logical agent ID，source surface/workspace 必须匹配该 actor 的当前登记绑定；target 必须匹配原 dispatch sender 的登记 inbox。缺少来源坐标、名称别名、未知来源、错目标或旧 generation 均不能推进。

`handoff/approve/changes` 必须配 `DONE`，`retry` 配 `FAILED`，`blocked` 配 `BLOCKED_CONFIRM`；handoff、验收数组、reason、revision、角色、尝试上限继续由既有 loopcore/store 校验。普通 message/DONE/ACK/reply 不自动验收。cmux 模式拒绝不带 native inbox evidence 的直接 IPC workflow reply。

`ReplyCMUXWorkflow` 在既有 workflow 事务内重新核对已接受 native inbox evidence、精确正文和当前 source/target 绑定，再原子提交 reply、cursor、下一 outbox 与 `CMUX_WORKFLOW_APPLIED`。重复历史使用原 workflow receipt。COMMIT 报错不能单凭错误推断整笔事务尚未提交，恢复须核对 durable receipt；提交失败后的连接清理及“已提交后报错”的回归见 [P1 修复证据](cmux-workflow-transport-v1-commit-fix-result.md)。原 `AcceptCMUXInbox` 的整批 duplicate/source/target 门禁和首次 evidence 保留语义继续生效。

发送使用原 durable intent 模式：先保存 `CMUX_SEND_REQUESTED`、attempt=1，再执行 `agent message`；`CMUX_SEND_RESULT` 保留 bounded stdout/stderr、exit、native ID/source/target/body。结果未知、错目标、失败、超时或截断后不重发；显式 drain 也只能领取 attempt=0 的当前 outbox。store 原 generation/stop/binding recovery 门禁继续生效。

## 代码与文档

| 文件 | 实现内容 |
| --- | --- |
| `internal/runtime/workflow.go` | `WorkflowTransportAdapter`、transport runner 和有界前台 drain |
| `internal/runtime/cmux_workflow.go` | cmux Dispatch/Collect、严格 workflow JSON 解析 |
| `internal/runtime/cmux_transport.go` | 复用现有 native 命令执行、证据解析和 inbox 接受入口 |
| `internal/store/cmux_workflow.go` | 已有 outbox claim、入站身份/正文门禁、原子 workflow 事件 |
| `internal/store/workflow.go` | 复用 workflow 事务并原子追加 cmux workflow receipt |
| `internal/store/cmux_transport.go`、`messaging.go` | envelope queue receipt 与普通 ACK/reply 的确认门禁 |
| `internal/coordinator/cmux_transport.go`、`workflow.go` | 生产 runner 安装及稳定 gate 诊断 |
| `internal/cli/workflow.go` | 单次 cmux 前台分支、IPC 调用与关闭 |
| `README.md`、`how-to-use.md`、包 README | 启动、单次/常驻前台模式、回传协议、恢复及能力边界 |

继续使用现有五张业务表、append-only journal、IPC 和 run lock；没有迁移、新依赖、队列、任务表或 DAG。

## 测试覆盖

`internal/coordinator/cmux_workflow_test.go` 使用真实本地 Unix IPC 和编译出的 fake cmux 可执行文件，逐步构造 native inbox：

```text
revision 1  implement execute -> builder
revision 2  builder handoff -> implement review -> reviewer
revision 3  reviewer approve -> verify execute -> reviewer
revision 4  reviewer handoff -> verify review -> main
revision 5  main approve -> COMPLETED
```

四次 native agent-message 调用，每条 dispatch 的队列回执均为 `CMUX_QUEUED` / attempt=1。每个角色结果前正常关闭并重开 coordinator；重放 start、重复旧结果和逆序混合历史均不重复投递或推进。全部 seat 保持 LAUNCHED，native session ID 为空，没有合成 READY。

回归包括：

- 五种 workflow kind 的正确终态与缺失/不匹配终态；blocked 后显式 lead resume。
- 普通 DONE、缺 workflow、缺 status、重复/大小写/未知/null JSON、缺 handoff、错误 kind/revision/dispatch/actor/run。
- source 名称、source surface/workspace、target surface/workspace、未知 source、native failed state、同批重复 inbox ID。
- 重开后的未发送 outbox 可显式领取；已有 intent、失败、无效回执、错目标和截断不重发；stop 后不调用 transport。
- 并发 start 只领取一次；旧 source/target generation、读 inbox 后绑定变化及 stop 在原子提交前重新拒绝。
- 注入 `CMUX_WORKFLOW_APPLIED` 写失败，确认 reply/cursor/next outbox/evidence 整体回滚；去掉故障后同 request 成功，重复调用不写入。
- 第 128 个结果恰好 COMPLETED/PAUSED/EXHAUSTED 正常返回；仍有后续步骤时提示再次显式 drain。
- CLI 单次前台 start/drain/resume/status、正常释放 lock/socket、历史请求和显式 cmux 选项门禁。

## 验收记录

最终源码的全部验收通过：

| 检查 | 结果 | 日志 |
| --- | --- | --- |
| `go test -count=1 -v ./internal/runtime ./internal/coordinator ./internal/cli ./internal/store -run TestCMUXWorkflow -timeout 180s` | PASS（11.684s） | [focused](../../.cache/cmux-workflow-transport-v1-focused.log) |
| `go test ./...` | PASS（56.822s） | [test](../../.cache/cmux-workflow-transport-v1-test.log) |
| `go build ./...` | PASS（0.916s） | [build](../../.cache/cmux-workflow-transport-v1-build.log) |
| `make check` | PASS（74.629s，含 fmt/deps/verify/vet/test/build） | [check](../../.cache/cmux-workflow-transport-v1-check.log) |
| `go mod tidy -diff` | PASS（无差异） | [tidy](../../.cache/cmux-workflow-transport-v1-tidy.log) |
| `go test -count=1 -v ./internal/coordinator -run '^TestHeadless(Codex\|OpenCode\|Claude)IntegratedFixtures$' -timeout 180s` | PASS（23.863s，三工具完整 fixture 矩阵） | [headless](../../.cache/cmux-workflow-transport-v1-headless.log) |
| `CGO_ENABLED=1 go test -race -count=1 ./internal/runtime ./internal/coordinator ./internal/cli ./internal/store ./internal/loopcore -timeout 300s` | PASS（192.376s，五个包全部通过） | [race](../../.cache/cmux-workflow-transport-v1-race.log) |

[基础验收 JSON](../../.cache/cmux-workflow-transport-v1-checks-verification.json) 与 [race JSON](../../.cache/cmux-workflow-transport-v1-race-verification.json) 均记录 `passed=true`、`source_unchanged=true`、所有命令 exit_code=0，以及相关源码和 go.mod/go.sum 的前后 SHA-256。

脚本：[复现检查](../../.cache/cmux-workflow-transport-v1-verify.py)。检查日志和带前后源码 SHA-256 的结果位于 `.cache/cmux-workflow-transport-v1-*-verification.json`。真实模型测试的 opt-in 环境变量在检查脚本中清除，所有新增 cmux 测试仅执行 fake 可执行文件。

## 能力与真实验收边界

| 状态 | 本轮含义 |
| --- | --- |
| LAUNCHED / SURFACE_REGISTERED | 沿用现有 up 保存的 launcher/坐标凭据；不证明 native agent 存活或登录完成 |
| CMUX_QUEUED | native 消息存储回执与绑定目标及 envelope 正文匹配；不证明 hooks 消费或模型执行 |
| COORDINATOR_READY | 前台 owner/IPC 已监听；不等于 native READY |
| native READY | 本轮未建立；`native_ready=UNVERIFIED`，native session ID 为空 |
| workflow COMPLETED | 绑定结构化事件通过原 workflow 门禁后的状态；不自动核验证据文件内容 |
| runtime_communication / capability | UNVERIFIED / PARTIAL；fixture PASS 不等于真实 hooks 验收 |

本轮实现与测试没有创建或操作真实用户 workspace，没有请求模型，没有读取 TUI 屏幕，也没有生产 terminal input 操作。任务要求的协作完成回传另按指定脚本执行，不作为产品真实 hooks 验收。真实 Codex/OpenCode/Claude Code 的 hooks 是否消费 dispatch、是否以所需 source identity 生成回传、真实有效权限与端到端执行均未验收。后台自动调度、fresh/reset、屏幕解析、ownership guard 和 stale-lock 接管均在范围外。

正常重开指前台 coordinator 已关闭并释放 lock；它不等于崩溃遗留锁自动恢复。Inbox 仍是最多 100 条、stdout/stderr 各 64 KiB 的有限批次；没有新增 native 增量游标或后台拉取。无效历史保留且继续经过门禁，不能作为成功结果跳过后宣称验收完成。

## 回传

全部验收完成后，已实际执行任务指定的 `/Users/funlake/.codex/skills/okra-agent-init/scripts/agent-handoff.sh send --window 1 --session-id cmux-workflow-transport-v1-20261105-01 --status DONE`，携带 summary/evidence/next。正式发送的实际输出为：

```text
[DONE] handoff sent: id=77b1c8e7-035a-49db-a623-279e591cab92 window=1 status=DONE
```

该 ID 对应正式发送；之前的路由 dry-run 不作为完成凭据。

主 agent 已对同一 ID 回传 ACK：`主 agent 已收到 cmux workflow transport 交付，开始 2 号独立 review。` ACK 表示收到交付，独立 review 结果尚未回传。
