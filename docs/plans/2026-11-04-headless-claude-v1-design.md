# Headless Claude Code v1

Session: `headless-claude-v1-20261104-01`. The supplied INIT defines the approved
scope: homogeneous persistent agents, generation 1, bounded foreground children.

The installed real CLI (outside cmux shims) is Claude Code 2.1.318. Read-only
`--version`/`--help` and the official headless/SDK references expose print mode,
JSONL messages, a UUID session, explicit resume and a terminal result. These are
sufficient to implement the protocol adapter; a real authenticated model turn is
not part of this fixture validation and remains UNVERIFIED.

Extend the existing `Headless` selector with Claude constructors and a native
parser. Keep coordinator, WorkflowRunner, SessionCoordinator, outbox, captures,
action claims, no-replay gates and stop/generation checks shared. No scheduler or
second execution kernel. The default selector remains Codex.

Claude runs `--print --bare --verbose --output-format stream-json --input-format
text --permission-prompts none --prompt-suggestions false`. Prompt and READY nonce
travel through stdin; cwd is a process property. Resume appends only `--resume
<frozen UUID>`. Bare mode prevents implicit customizations and background work and
uses environment/provider auth rather than subscription OAuth/keychain. Existing
`permissions=full` explicitly adds `--dangerously-skip-permissions`; default does
not. v1 accepts no raw Claude argument extensions.

Require one init and one terminal result, exact keys, duplicate rejection, matching
UUID sessions, main assistant content and matching result text. Same-message text
blocks concatenate; a new assistant message replaces an intermediate candidate.
Thinking and tool/user text cannot establish READY. Only an end_turn success can
confirm submission; known native errors are FAILED, incomplete or ambiguous
streams are UNKNOWN, and neither is replayable. The shared exact READY and workflow
JSON decoders enforce actor/generation/nonce and structured handoff/review events.

Explicit mutually exclusive `up --headless-claude` and `coordinator
--headless-claude` use the private tool marker and frozen configuration. Reopen is
read-only with respect to native work until an explicit workflow action/drain.
Forced-exit stale locks remain outside v1 recovery.

Reuse the executable fixture and integration/e2e matrices for startup failure,
native success/failure/unknown, malformed JSONL, workflow outcomes, clean reopen,
queued/SENT/SUBMIT_REQUESTED drain cuts, idempotency, concurrent start, stop and
generation fences. Validate all Go tests/build, make check, tidy diff and relevant
package race without starting a model.
