# Default system temporary run directory

Session: `default-temp-rundir-20261105-01`  
Validated: 2026-11-05, Go 1.25.5, darwin/arm64.

## Result

Real CLI `up` without `--run-dir` now calls `os.MkdirTemp("", pattern)`, using
Go's platform-specific `os.TempDir()` selection. The pattern contains `agentflow-`,
the first 12 characters of the validated run ID and a random suffix. The bounded
prefix avoids making the Unix socket path grow with a maximum-length run ID.
No new default path is constructed from `/tmp`, XDG or HOME.

The allocated directory is resolved with `filepath.Abs` and `filepath.EvalSymlinks`
before compiling snapshot/wrapper paths. This supports system temporary roots
containing symlinks on macOS. MkdirTemp creates mode 0700 before umask on Unix;
the existing runtime private-directory checks remain in force. Explicit
`--run-dir` still uses the previous absolute-path resolution and runtime checks.

Both cmux and all three headless real-up modes use this entry point. JSON
`run_dir` and text `Run directory` carry the actual absolute canonical path,
including runtime startup failures before the runtime populates its own result.
Existing-run commands never call the allocation helper. They retain deterministic
legacy XDG lookup for old runs; temporary runs require the output path passed as
`--run-dir` for status, workflow, messages, bootstrap and coordinator reopening.

On CLI failure before runtime handoff, cleanup attempts only `os.Remove` of the
new empty allocation. Explicit directories are never removed. Once handed to the
runtime, the directory is retained on failure, success and normal shutdown; there
is no recursive runtime cleanup or automatic deletion of a running directory.
Creation failure returns `UP_DIRECTORY_INVALID` without another fallback root.

## Coverage

- `internal/cli/up_directory_path_test.go`: host `os.TempDir()` selection with
  TMPDIR/TMP/TEMP overrides; spaces/Unicode; Windows-style backslashes (literal
  filename characters on Unix, native drive/separators on Windows); symlink root;
  long safe run ID prefix; unique allocations; Unix mode 0700; explicit absolute
  and relative directories resolved without creation.
- `internal/cli/up_directory_test.go`: structured creation failure, pre-handoff
  empty-directory cleanup, explicit-directory retention, post-handoff retention,
  unsafe ID rejection, and no fresh allocation from coordinator/workflow/status.
- `internal/cli/up_cmux_test.go`: executable fixture startup without `--run-dir`,
  output path used by a separate status process, persisted snapshot/registrations,
  and explicit repeat at that returned directory refused without replay.
- `tests/e2e/headless_codex_test.go`: real CLI and IPC with native fixture,
  no-dir headless up returns its path; independent workflow CLI processes use it;
  normal shutdown preserves state; coordinator reopens that same path; workflow
  status/replay/drain preserve native-call idempotency. Existing explicit-directory
  cases also assert that their output directory is unchanged.

README, how-to-use and coordinator documentation explain path capture/reuse,
legacy lookup, cleanup/retention and temporary storage lifetime. Up/coordinator/
workflow/status flag help identifies the relevant directory behavior.

## Verification

All requested local checks passed:

```text
go test ./...                                             PASS
go build ./...                                            PASS
make check                                                PASS
go mod tidy -diff                                         PASS (no diff)
go test -race ./internal/cli ./internal/coordinator ./tests/e2e
                                                          PASS
```

`make check` includes gofmt, module download/verify, vet, all tests and the CLI
build. The race run reported cli 19.169s, coordinator 103.102s and e2e 59.463s.
Built `./bin/agentflow up --help` was also checked for the new default/reopen text.

Additional platform checks:

```sh
GOOS=linux GOARCH=amd64 go build ./...
# PASS

GOOS=windows GOARCH=amd64 go test -c -o <external-temp>/agentflow-default-temp-path-windows.test.exe \
  internal/cli/up_directory.go internal/cli/up_directory_path_test.go
# PASS: directory helper and standalone path tests compile for Windows.

GOOS=linux GOARCH=amd64 go test -c -o <external-temp>/agentflow-default-temp-path-linux.test \
  internal/cli/up_directory.go internal/cli/up_directory_path_test.go
# PASS: directory helper and standalone path tests compile for Linux.

go test internal/cli/up_directory.go internal/cli/up_directory_path_test.go
# PASS: standalone path tests execute on macOS.
```

Windows/Linux target test binaries were compiled, not executed on those operating
systems. A full Windows cross-build was additionally attempted and remains
blocked by existing code outside the directory change:

```text
GOOS=windows GOARCH=amd64 go build ./...
internal/context/files.go:88:49: undefined: syscall.O_NOFOLLOW
internal/lock/probe.go:100:17: undefined: syscall.Kill
```

This verifies portable directory selection without claiming Windows runtime,
private ACL or complete application support. No real cmux workspace was created
and no model was requested; native execution in checks used local fixtures.
