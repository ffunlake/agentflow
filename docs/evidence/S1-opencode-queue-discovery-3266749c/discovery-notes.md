# 补充只读发现命令

正式采集命令及输出见 `initial-*.json` / `final-*.json`；此处记录正式采集前的辅助查询。只读预查未逐条录制到文件，以下是实际工具输出的整理，不冒充另一次执行。

- `ps -t ttys003 -o pid=,ppid=,tty=,stat=,comm=`：`login` PID 21982、shell 21983/23095、`opencode` 23097；后续 argv 采样只针对这三个明确 PID。
- `cmux identify --window 73FEB4F4-BE2B-4F4D-9B48-E872BBC51EA3 --workspace 3AB7D16C-B09D-44AE-8CD5-5B6225388D45 --surface 5265DA42-9A45-4C71-BABE-155E458FCF33 --json`：返回目标 terminal short ref `surface:1000060034`。正式证据改为 UUID tree/screen 以避免 short ref 被当成 native 身份。
- `cmux --id-format uuids --json list-pane-surfaces --window 73FEB4F4-BE2B-4F4D-9B48-E872BBC51EA3 --workspace 3AB7D16C-B09D-44AE-8CD5-5B6225388D45 --pane 568DCC2D-D84E-44A2-81EF-AED6CAE8F5FB`：仅一个 selected OpenCode surface，ID 与目标一致。
- `lsof -nP -U -Fpcfn` 经内存投影，只保留 name 包含 `0xd081154b03baf9e3` 或 `0x89ecea4b5a1212d4` 的行及对应 PID/comm/FD：唯一返回 `{pid:23097, command:opencode, fd:17, name:->0xd081154b03baf9e3}`；没有查得 peer 的可连接 pathname，不把它认作 queue。
- 只读 SQLite `sqlite_master` / `PRAGMA table_info`：确认 `session`、`message`、`part` 表及可投影字段。随后只针对目标 cwd 与屏幕上的两段确切旧 assistant 文本查找，匹配同一 `ses_e64c2fa39ffeI5sYf6iCWDKYpv`。正式快照使用该 ID，查询源码完整保存在 `collect.py`。
- 文件工具列出 `.config/opencode/plugins`、应用 Resources 与项目 `.opencode/*`，读取随包 bridge 和现有 Orca 状态上报代码，没有执行插件或读取其认证 endpoint。
- 读取 `.local/state/opencode/kv.json` 的非敏感 UI 偏好：sidebar、thinking/timestamps/tool-details、动画及 upsell 状态；没有 native session 路由字段，未将其作为身份依据。

本机 SDK `~/.config/opencode/node_modules/@opencode-ai/sdk/dist/gen/sdk.gen.js` 的只读补充摘录（本机安装 SDK 不等同于运行映像版本保证）：

```text
703 class Control extends _HeyApiClient
705   Get the next TUI request from the queue
707   next(options)
709     url: "/tui/control/next"
714   Submit a response to the TUI request queue
716   response(options)
718     url: "/tui/control/response"
727 class Tui extends _HeyApiClient
729   Append prompt to the TUI
731   appendPrompt(options)
733     url: "/tui/append-prompt"
```

这些名称只描述 API，未建立当前 TUI 的 server URL；没有调用任何上述 endpoint。`append-prompt` 还会触及输入草稿，不作为本轮安全外部消息替代方案。
