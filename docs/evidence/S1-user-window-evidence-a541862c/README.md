# S1 用户新窗口已有模型 ACK 证据

Session：`a541862c-c620-47e8-95c9-e2186e359c9c`；task：`s1-user-window-evidence`；协作模式：`ralph-loop`；日期：2026-11-02。本轮仅整理用户已完成的两次只读验证，新增模型请求数 **0**。完整 S1 保持 **PARTIAL**。

## 已证明的两项观察

共同 window：`73FEB4F4-BE2B-4F4D-9B48-E872BBC51EA3`，workspace：`3AB7D16C-B09D-44AE-8CD5-5B6225388D45`，cwd：`/Users/funlake/Projects/okra-wiki`。原始 [tree](target-tree.json) 确认窗口 visible、水平分屏及两个 surface 均 selected in pane；`tty003/tty004` 在 OS/cmux 中的实际名称为 `ttys003/ttys004`。

| 工具 / 观察结论 | surface / pane | TTY / 进程 | 屏幕中的真实 assistant 结果 |
|---|---|---|---|
| OpenCode 已有真实模型 ACK **PASS** | surface `5265DA42-9A45-4C71-BABE-155E458FCF33`；pane `568DCC2D-D84E-44A2-81EF-AED6CAE8F5FB` | `ttys003`；PID `23097`，PPID `21983`；进程名 `opencode` | scrollback 第 3 行是用户请求，第 **6** 行单独返回 `OPENCODE_ACK_7ba139c0-0133-47da-b99d-e7549a22f735`，第 8 行为 Build 完成信息；页脚显示共同 cwd |
| Codex 已有真实模型 ACK 和 pwd **PASS** | surface `94EBBAAE-5EA1-4352-B183-580E24F0F23C`；pane `0EB494B8-9ADB-4013-97ED-E71C2CA2808C` | `ttys004`；PID `23477`，PPID `22535`；进程名 `/opt/homebrew/bin/codex` | scrollback 第 8–9 行是用户请求，第 **12** 行 assistant 单独返回 `CODEX_ACK_9196eeee-17ab-40ac-91ee-8f558336b201`；第 14–15 行显示 `Ran pwd` 及共同 cwd，第 17 行 assistant 回显 cwd |

每个 PASS 都通过 [bindings.json](bindings.json) 绑定同一 window/workspace/surface/TTY/PID/cwd 与该 surface 的 assistant 行；[evidence-validation.json](evidence-validation.json) 记录逐项断言。ACK 依据是用户消息之外的真实 assistant 输出，没有使用 cmux 命令成功、退出码或协作 READY/handoff 替代 ACK。

原始采集含精确 argv、UTC 起止时间、实际 stdout/stderr 及其 base64。各 `*-screen.json` 与 `*-scrollback.json` 保存 cmux 原始输出；`*-decoded.json` 保留完整逐行文本，未裁剪空行，解码结果与原始 base64 一致。scrollback 请求上限为 1000 行；本次 OpenCode、Codex 均返回 58 行，历史覆盖仅限 cmux 实际返回内容。

## 只读交叉映射

- [TTY 进程快照](tty-processes.json) 保存两条 TTY 的 PID/PPID/PGID/TTY/状态/进程名，子进程映射只记录这些元数据。
- [OpenCode 进程](opencode-process.json) 与 [OS cwd](opencode-cwd.json) 对应 PID `23097`；[Codex 进程](codex-process.json) 与 [OS cwd](codex-cwd.json) 对应 PID `23477`。cwd 来自只读 `lsof -a -p <PID> -d cwd -Fn`，与屏幕路径一致。
- [OpenCode 原始 scrollback](opencode-scrollback.json)、[逐行文本](opencode-scrollback-decoded.json)；[Codex 原始 scrollback](codex-scrollback.json)、[逐行文本](codex-scrollback-decoded.json)。初次人工 `ps` 查询曾包含无关子进程参数；上述正式进程证据仅采集元数据，未将该查询的参数输出复制入目录。

## 验收边界

两会话的 **native session/thread ID、reset/READY、角色重注入、调用身份刷新、有效 full 权限、recovery、busy 投递、草稿保护、不同 cwd 隔离、hook/queue 与自动唤醒均为 UNVERIFIED**。没有读取原生 transcript/database，没有据协作 session ID 猜测 native ID。模型名称只是屏幕标签：OpenCode 显示 `GPT-6.1 Sol / OpenAI`，Codex 显示 `GPT-5.6-Luna`；本轮未独立审计提供商侧的实际模型身份。

Codex 第 5 行 `Permission selection requested: Full Access` 仅证明出现权限选择请求，不证明已批准或实际有效 full。`pwd` 成功只能证明此次只读命令成功。OpenCode 的 `Build auto` 同样不证明 full 权限。

旧隔离 OpenCode 的额度/provider 依赖保留为该旧环境的历史事实；不能继续据此将本用户窗口描述为只有 transport 或无法获得模型回复。历史 Codex 19 项受控 PASS 保留其原环境边界，不转移到本窗口；本轮两项已有 ACK 观察单列，不据此重算或夸大完整 S1 能力。

## 文件与哈希核验

新增文件均独占创建，历史证据未覆盖。仅允许更新 `docs/evidence/S1-result.md`、`docs/evidence/S1-capability-matrix.md`；[采集前清单](inventory-before.json) 与 [保护核验](protected-documents-verification.json) 对账 `docs/` 中既有非 scratch 普通文件，排除 symlink 和本新增目录，新增目录之外仅上述两份文档变化。

[SHA-256 清单](sha256-manifest.json) 覆盖新证据及两份总文档；[哈希核验](hash-verification.json) 逐文件回读检查并记录清单自身哈希。清单排除自身与稍后生成的 hash/handoff 回执以避免循环；[handoff 哈希回执](handoff-sha256.json) 补充 handoff、hash 核验和清单哈希。正式 [handoff](handoff.json) 的 DONE 仅表示本轮证据交付完成。

本轮没有向用户两个 surface 发送输入，没有 reset/clear/new、退出、kill、切换 provider、登录或更改两个会话；只读观察原窗口之外没有拓扑操作。未操作 Claude、未修改 okra-wiki、未修改 Go、未运行 Go test/build/check 或 doctor、未进入 S2。[collect.py](collect.py) 是本次证据记录器，不是 AgentFlow 运行时实现。
