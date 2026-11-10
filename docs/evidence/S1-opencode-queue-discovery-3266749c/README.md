# S1 OpenCode 外部 queue / 消息入口发现

协作 session：`3266749c-1804-4665-9e94-71b766be4095`；task：`s1-opencode-queue-discovery`；角色：资深研发；模式：`ralph-loop`。日期：2026-11-02。正式快照区间：`08:51:50–08:52:48Z`，此前有同范围只读预查。

**结论：UNVERIFIED / QUEUE_ENTRY_UNAVAILABLE。** 未找到可证明绑定当前 native session、当前可见 surface/cwd，并能安全取得 assistant ACK 的外部入口。停止在发现阶段；本轮外部消息、nonce 提交和模型请求均为 **0**，`new_runtime_passes=[]`。调查交付 DONE 不代表功能 PASS。

## 当前目标与只读身份关联

| 字段 | 本轮观察 |
|---|---|
| window | `73FEB4F4-BE2B-4F4D-9B48-E872BBC51EA3` |
| workspace | `3AB7D16C-B09D-44AE-8CD5-5B6225388D45` |
| surface | `5265DA42-9A45-4C71-BABE-155E458FCF33`；selected terminal，窗口 visible |
| PID / PPID / argv | `23097` / `21983` / `opencode --auto` |
| TTY / OS cwd | `/dev/ttys003` / `/Users/funlake/Projects/okra-wiki` |
| 运行映像 | `/opt/homebrew/Cellar/opencode/1.19.5/bin/opencode` |
| 当前 native 关联 | `ses_e64c2fa39ffeI5sYf6iCWDKYpv`；只读 SQLite 的 directory/version 与进程一致 |
| 关联方法 | 同 surface 可见的旧 `OPENCODE_ACK_7ba139c0-0133-47da-b99d-e7549a22f735`、`RECOVERY_ACK request=S1-OC-recover-dc2eb54e-b9d405f3 cwd=...`，与该 session 的 assistant 文本、message/part IDs 对上 |

[原始屏幕](initial-target-screen.json)、[UUID 拓扑](initial-target-tree.json)、[进程](initial-target-process.json)、[OS cwd/TTY/映像](initial-target-cwd-tty-binary.json)、[只读 native 投影](initial-native-projection.json)。SQLite 使用 `mode=ro`、`PRAGMA query_only=ON` 和读事务；只选择 session/message/part 元数据及两段既有 assistant 文本，不导出注入上下文或工具内容。

这是一条屏幕→旧 assistant 文本→native ID 的观察链；没有用普通 prompt、`/status`、`/sessions` 或新消息强行刷新身份。它能关联当前显示的历史，但没有证明外部消息路由。旧 ACK 不算本轮新 ACK。

## 候选入口与停止依据

| 候选 | 证据与判断 | 验收 |
|---|---|---|
| 当前 OpenCode 的 HTTP server | [网络](initial-target-network.json) 没有 LISTEN，最终也没有。初始有公网出站连接与 `127.0.0.1:60722 → 127.0.0.1:55367`；后者的 [listener 所有者](initial-loopback-peer-listener.json) 是 `Code H` PID `79692`，不能当作 OpenCode server | UNVERIFIED；无可归属于目标 runtime 的服务地址 |
| Unix socket / 子进程 | [Unix socket](initial-target-unix.json) FD `17` 只有 `->0xd081154b03baf9e3`，没有可连接 pathname；[子进程](initial-children.json) 为空。预查的 Unix 地址匹配没有找到可绑定路径 | UNVERIFIED；不向未命名 socket 或未知对端写入 |
| `opencode run --attach URL --session ID --dir DIR` | [help](initial-opencode-run-help.json) 声明这些参数；需要现有可验证 server URL。单独 `run --session` 不能证明使用当前存活 TUI runtime | UNVERIFIED / ADVERTISED_ONLY；未执行 run |
| `POST /session/{id}/message` / `prompt_async` | [本机 SDK 摘录](local-documentation.json) 声明按 ID 发消息。SDK 是本机文档，不证明与运行映像同版本或目标 runtime 可达。当前无可绑定入口 | UNVERIFIED / ADVERTISED_ONLY；没有 HTTP 请求 |
| `/tui/control/next`、`/tui/control/response` | SDK 描述为 TUI 请求/响应队列，见 [补充说明](discovery-notes.md)。需要 server，不能据 queue 名称当作当前会话的模型消息入口 | UNVERIFIED；没有请求，也未使用 append/submit prompt |
| `serve` / `web` / `acp` / `attach` | [serve help](initial-opencode-serve-help.json)、[ACP help](initial-opencode-acp-help.json)、[attach help](initial-opencode-attach-help.json)；启动或附加路径不提供当前 TUI 的稳定入口证据 | UNVERIFIED；未启动第二 runtime、未附加客户端 |
| cmux `agent message <surface>` | [help](initial-cmux-message-help.json) 声明通过 hooks 交付；[status](initial-cmux-receiving.json) 为 `receiving=true`，但 [resume binding](initial-target-resume-binding.json) 的 `restore_record`、`resume_binding` 都为 null | UNVERIFIED；声明不能证明 provider/native session 绑定，未发送消息 |
| cmux / Orca 插件 | [目录与配置元数据](initial-metadata.json)：标准 cmux 插件路径不存在，项目 `.opencode` 不存在；现有全局插件为 Orca 状态上报。应用随包 cmux bridge 和 Orca 状态代码见 [本机文档](local-documentation.json)；没有已证实的当前会话入站通道 | UNVERIFIED；配置只取 stat，未安装插件或调用 Orca |

未推断机器上所有 OpenCode 服务均不存在；没有扫描其他服务、访问历史隔离 server、启动新 runtime、直接插库或盲试多个端点。全局/项目配置正文未读，因此也不宣称已穷尽任意自定义插件。停止依据是：**在已授权的只读证据中，没有可安全绑定当前 TUI runtime 的入口**。

## PASS 与 UNVERIFIED

本轮功能 **PASS：无**。进程/TTY/cwd、现有 native 文本关联、命令退出 0 和证据一致性仅是观察事实。

以下均为 **UNVERIFIED**：外部 queue 的同 session ACK、busy 排队、非空草稿保护、通用恰好一次、AgentFlow hook/queue 适配器。没有本轮 nonce，因此没有新原生 ACK transcript；交付的 screen/native 摘录明确标记为旧内容。`MODEL/QUEUE_ENTRY_UNAVAILABLE` 分类本轮具体落在 `QUEUE_ENTRY_UNAVAILABLE`，**没有验证模型不可用**，不沿用其他环境的 provider/额度错误。

## 最终状态与记录边界

[最终进程](final-target-process.json)、[最终网络](final-target-network.json)、[最终屏幕](final-target-screen.json)、[最终 native 投影](final-native-projection.json)、[最终拓扑](final-target-tree.json) 保留 PID/TTY/cwd 与原布局；两次 screen 返回相同，输入栏视觉为空。原生投影共 5 个 message、13 个 part，前后元数据及选定文本一致；未做完整 native 内容哈希，不据元数据相同宣称全部内容字节不变。

[保护比较](protection-comparison.json) 确认快照期间 17 个既有项目源码/构建/样例等文件哈希无变化。基线在预查后建立；没有保存 wiki 全目录内容快照，也没有重新验收 Codex/Claude。所有本轮写入限于本目录和授权两份 S1 总文档。

没有向目标 TUI 发送文字/按键、reset/clear/new、退出/kill、改 provider/config、认证/购买/复制凭据、写 okra-wiki、操作 Codex/Claude、修改 Go 或进入 S2。只运行本机帮助与只读元数据查询，未运行 Go test/build/check 或 doctor。

`risky-ask` 的已安装 CLI `jev-judge` 不在当前 PATH，未产生或伪造 Jev 判断；只读发现按技能允许继续。仅检查环境变量是否设置，没有输出值或读取任何 token/key/cookie/auth 文件、目标进程环境或认证 endpoint 文件。实际命令、cwd、时间、退出码及两条输出流保存于 `initial-*.json` / `final-*.json`；记录器为 [collect.py](collect.py)，不属于 AgentFlow runtime。

后续缺口是：由当前 OpenCode runtime 明确公布可绑定的入站地址/协议，或取得当前已加载接收 hook 的 provider/native ID 绑定证据，再在同 surface/cwd 用单个 nonce 验证真实 assistant ACK。当前授权下无需用户动作，调查到此结束。
