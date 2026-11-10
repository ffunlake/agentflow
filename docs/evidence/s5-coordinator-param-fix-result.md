# S5 coordinator parameter fix — DONE

Task: `s5-coordinator-param-fix`  
Session: `3117cc8e-a3c2-4eaf-828e-971ca29181f8`  
Date: 2026-11-03

## Root cause and fix

`decodeParams` used substring membership over a space-delimited list of field names. A send request containing valid recipient/body plus `"recipient recipient_generation":123` passed that check; `json.Unmarshal` then ignored the composite field and the request persisted a message. This was reproduced through the real coordinator Unix IPC path before changing production code: the regression returned `ok=true` / STORED and added one row each to messages, deliveries and journal.

`internal/coordinator/coordinator.go` now defines an exact field-name map for every method and checks the decoded key with map lookup. Stop has an empty field set. Existing duplicate-key, null-value and JSON type validation remains in place. Unsupported-method behavior is unchanged. No schema library, table, dependency or native operation was added.

## Regression evidence

`internal/coordinator/params_test.go` adds 18 IPC scenarios: send/assign/reply/inbox composite fields, duplicate keys, null values, wrong string/integer/bool types, case mismatches, empty keys and unknown fields for ACK/reconciliation/stop. Each scenario seeds a valid persisted message/delivery/journal first, then asserts typed `INVALID_REQUEST`, `ok=false` and the wire error code. A read-only SQL snapshot compares **all columns and rows** of messages, deliveries and journal before and after, detecting both insertions and updates.

Red reproduction command (before fix):

```sh
go test ./internal/coordinator -run '^TestInvalidParamsLeaveMessageDeliveryJournalUnchanged/send-compound$' -count=1
```

Observed exit 1: expected INVALID_REQUEST but got STORED; all three table snapshots gained a second row. After the exact-map fix, all 18 scenarios pass and complete table snapshots are identical.

## Verification

All requested checks completed with exit **0** using project-local Go caches/temp directories, GOTOOLCHAIN=local, CGO_ENABLED=0 and GOFLAGS=-mod=readonly:

- `go test ./internal/coordinator ./internal/cli ./internal/store ./internal/ipc -count=1`
- `go test ./...`
- `go build ./...`
- `make check` — format, module verification, vet, all tests and binary build passed.
- `go mod tidy -diff` — empty output; dependencies unchanged.

Only production parameter validation, its new regression test and this evidence were changed for the fix. The tested cmux window/workspace/surfaces were not accessed. Native capability remains PARTIAL; this fix verifies rejection before persistence.

## Formal handoff

Payload ID: `s5-coordinator-param-fix-3117cc8e`; status DONE; scope result PASS. Destination is the supplied control route `workspace:1000060006` / `surface:1000060016`. Formal queue submission and main review ACK are tracked separately.

Actual `cmux agent message` submission exited 0; receipt `dbc5cf70-d4c7-483a-82ac-422652392ba1`, `state=queued`, `recipient_has_agent=false`, `delivered_at=null`, `read_at=null`. The formal handoff was accepted by the queue; main receipt/review ACK is pending. No alternate surface or terminal input was used.
