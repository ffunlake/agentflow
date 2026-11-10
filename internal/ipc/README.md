# S3.2 NDJSON Unix socket IPC

`Listen(path, handler, ServerOptions{ValidateActor: ...})` binds a caller-provided
Unix socket in an existing private directory and sets its mode to `0600` before
accepting clients. Call `server.Serve(ctx)` once; `Close()` or cancellation closes
the listener and active connections and cancels handler contexts. `Serve` waits
for handlers, which must honor cancellation. Existing socket paths/files cause a
bind error and are never removed to retry the bind. Run directories, stale-owner
recovery and single-instance locking belong to S3.3.

`Client{Path: path}.Call(ctx, request)` opens one connection per call and supports
concurrent callers. Set a context deadline to bound dialing, writing and reading.
On a remote rejection it returns both the decoded `Response` and a typed `*Error`;
on a transport/protocol failure the application outcome is unknown. It never
automatically retries.

## Wire format

Each UTF-8 JSON object is terminated by LF. The maximum frame is exactly 1 MiB
(1,048,576 bytes), excluding LF; CR in CRLF counts toward the limit. A connection
can carry multiple ordered request/response pairs. Different connections execute
concurrently, so validators and handlers must be safe for concurrent use.

```json
{"v":1,"requestId":"request-1","actor":{"runId":"run-1","agentId":"builder","generation":2},"method":"example","params":{"body":"hello"}}
{"requestId":"request-1","ok":true,"result":{"status":"STORED"}}
{"requestId":"request-1","ok":false,"error":{"code":"GENERATION_MISMATCH","message":"stale generation"}}
```

All five request fields and all three actor fields are required. Version must be
`1`; requestId, method, runId and agentId must be nonblank strings. Generation is a
nonnegative int64; zero denotes an external actor without a seat binding, whose
acceptance is the validator's decision. `params` may be any JSON value, including
explicit `null`, and remains `json.RawMessage` for the handler. Request IDs are
not trimmed or regenerated. Missing/unknown fields, duplicate envelope/actor
keys, field names with different casing, malformed JSON, invalid UTF-8 and empty
lines are rejected. Params field names and contents belong to the handler.

`Response` includes requestId and ok, and exactly one of result/error. A successful
nil handler result is encoded as `"result":null`. Errors contain code and message.
An unambiguous string requestId is echoed even on envelope validation errors;
invalid JSON, invalid UTF-8, missing/duplicate IDs, incomplete and oversized frames
use `"requestId":""`. Recoverable invalid lines receive one error and processing
continues; overflow receives one error and the connection closes without draining
arbitrary input. EOF before LF never dispatches an incomplete request.

## Identity and handler contract

`ServerOptions.ValidateActor(context.Context, Actor) error` is mandatory, runs
before the handler on **every** well-formed request, and must verify claimed
run/agent identity and the current generation against trusted runtime state.
Return typed ACTOR_MISMATCH or GENERATION_MISMATCH errors for rejections. The
socket's private directory and 0600 mode restrict local access; claimed actor
fields alone are not authentication. The caller supplies the trusted identity
source when the coordinator is integrated. No identity or response cache exists.

`Handler(context.Context, Request) (any, error)` receives method, opaque params,
actor and the original request ID. It returns a JSON-encodable result or an error.
For unsupported methods return `&ipc.Error{Code: ipc.MethodNotFound, Message: ...}`.
Typed `*Error` values (including wrapped errors) are public and preserve their
code/message. Unexpected errors, result encoding failures and validator/handler
panics return generic INTERNAL errors without internal diagnostics.

| Code | Meaning |
|---|---|
| `INVALID_REQUEST` | Invalid envelope, actor shape, JSON, UTF-8 or incomplete line |
| `FRAME_TOO_LARGE` | Request or response exceeds 1 MiB; put large evidence in a file |
| `ACTOR_MISMATCH` | Validator rejects claimed run/agent identity |
| `GENERATION_MISMATCH` | Validator rejects an old or invalid current generation |
| `METHOD_NOT_FOUND` | Handler does not support the method |
| `INTERNAL` | Unexpected validator/handler/encoding failure |
| `IDEMPOTENCY_CONFLICT` | `errors.Is(err, store.ErrIdempotencyConflict)`; same store request ID, different body |

IPC performs no persistence, deduplication or delivery. A handler that calls
`store.SaveOutbox` passes `Request.RequestID` as `Message.RequestID`; store owns
the transaction and idempotency semantics. Its conflict sentinel, including a
wrapped sentinel, is mapped centrally to a structured error. Repeating an IPC
request still runs validation and the handler. A disconnect may occur after a
commit; reuse the same store request ID to reconcile/retry. Successful IPC means
the handler completed its operation, and STORED means persisted; neither proves
delivery, ACK or execution. Disconnect does not roll back handler side effects.
