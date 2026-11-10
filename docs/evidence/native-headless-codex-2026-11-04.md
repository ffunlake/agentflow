# Headless Codex native smoke test

- Date: 2026-11-04
- Result: `PASS` for one-shot headless Codex JSONL transport
- Native result: `UNVERIFIED` for the persistent TUI adapter
- Thread: `01a1a436-c520-7b01-9af3-9b3791f3429c`
- Temporary cwd: `/tmp/agentflow-headless-codex.xP4fL9`
- Request nonce: `AGENTFLOW_HEADLESS_CODEX_6303BA60`

## Checks

The command used the installed Codex CLI with `exec --json`, an isolated temporary
cwd, and a stdin prompt forbidding tools, file inspection, authentication, retries,
and modifications. It returned exit code 0 and JSONL containing `thread.started`,
`turn.started`, one completed `agent_message` with the exact nonce, and
`turn.completed`. No tool calls were emitted and the temporary cwd remained empty.

The CLI emitted a hooks-configuration warning, but it did not affect the turn or
exit status.

## Boundary

This proves a real one-shot headless Codex request and response. It does not prove
the persistent cmux TUI path: the existing `NativeSubmitAdapter` still requires a
real exclusive `NativeSubmitGuard`, and the target TUI writer lock is not an input
ownership lease. No target cmux surface was operated during this smoke test.
