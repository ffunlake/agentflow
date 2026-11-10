# Native SubmitAdapter fixture verification

Task: `native-submit-adapter`. Result: **implementation and fixture checks PASS**, pending main agent review. Native runtime acceptance remains **UNVERIFIED**. Target: `surface:1000060018`.

## Implementation

- `internal/runtime/native_submit.go`: explicitly constructed OpenCode/Codex `SubmitAdapter`, bound to one READY actor/generation/native session, frozen agent configuration and absolute tool executable. Reuses `NativeCommand` and `captureNative`; adds only explicit session resume arguments. No default CLI/coordinator wiring.
- Mandatory injected `NativeSubmitGuard`: validates actor/native input state and holds ownership until the foreground child has exited and output has been parsed. Unknown ownership, busy state, draft and incomplete input fail closed. No permissive production guard is implemented.
- Frozen body goes byte-for-byte through stdin, with no shell, prompt-file publication, argv/environment transport or appended model challenge. Native stderr and model content are excluded from public errors.
- Accepted Submission requires the bound session, native assistant output and a completed native turn. Explicit failure events produce a typed FAILED diagnostic; process failure, missing/malformed/truncated output, cancellation and identity drift produce UNKNOWN. Both remain non-replayable under the existing coordinator gate. A successful CLI turn does not create ACK/reply/workflow acceptance.

## Executable verification

`internal/runtime/testdata/native-submit/main.go` builds into a real temporary executable. It records argv/cwd/stdin and emits protocol fixture events without launching installed model CLIs, connecting to a network or operating any user test window.

`internal/runtime/native_submit_test.go` verifies:

- Both tools with default/full permission flags, model/allowlisted extension arguments, explicit session resume, cwd with spaces/shell characters/Unicode, and byte-exact stdin including CRLF, quotes, shell text and NUL.
- Lost stdout, malformed output, explicit native failure, nonzero exit after successful-looking events, missing session/assistant/completion, wrong native session, stdout/stderr bounds and cancellation after stdin consumption.
- Unsafe constructor options; run/agent/generation/tool/native/workspace/surface/cwd/permission mismatch; busy/draft/incomplete/unverified input, guard error and missing ownership release; rejection before execution.
- User/reasoning echoes, plain “native PASS” text, malformed trailing data, failure following completion, foreign identity fields and changed native identity cannot establish success.
- Actual SessionCoordinator and SQLite integration: persisted `SUBMIT_REQUESTED`/attempt 1 precedes the executable boundary; successful fixture output reaches SENT; failed/lost output remains `SUBMIT_REQUESTED` with `UNVERIFIED`; draft remains QUEUED without execution; duplicate assign and Advance do not replay; changed-body request retry conflicts and original frozen body is preserved.

## Checks

```text
go test ./internal/runtime -run '^TestNativeSubmit' -count=1
PASS (2.576s, repository-local Go cache environment)

go test ./internal/runtime -timeout 45s -count=1
PASS (18.773s)

go test ./tests/e2e -timeout 45s
PASS (17.591s)

go test ./tests/integration -timeout 45s
PASS (4.262s)

make check GOFLAGS='-mod=readonly -p=1'
PASS: gofmt check, dependency download/verification, go vet ./...,
      every unit/integration/e2e package, CLI build
```

Default parallel `make check` twice exceeded the tool's 120-second execution limit without returning a test assertion failure. The final serial `make check` above completed all required checks successfully. Additional standalone package checks also passed.

Installed `opencode run --help`, `codex exec resume --help`, and a help-only Codex invocation with the generated parent option placement exited 0. This establishes advertised argument syntax only; no installed model submission was run.

## Remaining boundaries

Continued TUI operation, multiple windows with persistent agent sessions, native input ownership, ResetAdapter, automatic generation rebinding and background services remain UNIMPLEMENTED. CLI defaults remain without native adapters. An embedding process must supply real exclusive input protection and authorize each bound generation/session. This checkout has no Git metadata. Fixture success supplies no native PASS claim.

Formal handoff payload: `docs/evidence/native-submit-adapter-handoff.json`. Transport receipt and main review/ACK are separate evidence.

`cmux agent message surface:1000060018 --from '资深研发' --json - < docs/evidence/native-submit-adapter-handoff.json` exited 0. Message ID: `2cf2b240-3088-4221-a53a-0147c771e9d9`; `state=queued`, `recipient_has_agent=true`, `delivered_at=null`, `read_at=null`. Receipt: `docs/evidence/native-submit-adapter-handoff-receipt.json`. Main ACK/review remains pending.
