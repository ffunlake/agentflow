# Headless OpenCode v1

Session: `headless-opencode-v1-20261104-01`. Scope and acceptance are the supplied
INIT task: persistent homogeneous roles, generation 1, bounded foreground children.

Reuse the existing headless execution kernel, coordinator, SessionCoordinator,
WorkflowRunner, outbox, journal and result protocol. The former Codex implementation
becomes `Headless`; its constructors and option names remain available as aliases.
An explicit tool selector defaults to Codex for existing callers. OpenCode adds a
constructor and strict JSONL parser. No second scheduler or execution architecture.

OpenCode startup uses `run --format json --dir CWD`; resume appends only the frozen
`--session ID`. Role/nonce and work/protocol text travel through stdin. Startup
requires exact assistant READY JSON plus a complete native session/step stream.
Only final step text completed with `reason=stop` is used; intermediate tool-call
steps are discarded. Session and message identity changes, malformed streams,
missing completion and nonzero exits cannot confirm submission.

CLI `up --headless-opencode` selects this adapter; headless tool flags are mutually
exclusive. A private tool-specific version marker and matching frozen config gate
`coordinator --headless-opencode`. Reopen never starts native work. Existing explicit
workflow start/resume/drain/status paths and all stop/generation/no-replay gates
remain authoritative. Captures are reserved exclusively before process launch and
collected only after durable SENT.

Validation reuses one executable fixture and the same integration matrix for both
tools, with OpenCode-specific startup/step/identity failures and CLI resume checks.
Required checks: all Go tests/build, make check, tidy diff and relevant package race.
Fixture runs start no model. A real OpenCode model run is optional and separately
reported; historical transport evidence cannot establish this delivery's success.
