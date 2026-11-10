"""Scope-bound, exclusive S4 action receipts. Never retries terminal actions."""
import datetime
import hashlib
import json
import os
from pathlib import Path
import shlex
import subprocess
import sys

ROOT = Path(__file__).resolve().parent
CWD = "/Users/funlake/Projects/agentflow"
CMUX = "/Applications/cmux.app/Contents/Resources/bin/cmux"
WINDOW = "window:1000060005"
WORKSPACE = "workspace:1000060011"
ORIGINAL = ["surface:1000060034", "surface:1000060035"]
SCOPE = ["--window", WINDOW, "--workspace", WORKSPACE]


def record(name, argv, timeout=20, env=None):
    # Reserve before external actions; failures and unknown outcomes cannot replay.
    with (ROOT / (name + ".json")).open("x") as f:
        at = datetime.datetime.now(datetime.timezone.utc).isoformat()
        json.dump(dict(at=at, argv=argv, state="REQUESTED"), f)
        f.flush()
        os.fsync(f.fileno())
        try:
            p = subprocess.run(argv, cwd=CWD, capture_output=True, text=True,
                               timeout=timeout, env=env)
            result = dict(at=at, argv=argv, cwd=CWD, exit_code=p.returncode,
                          stdout=p.stdout, stderr=p.stderr)
        except subprocess.TimeoutExpired:
            result = dict(at=at, argv=argv, cwd=CWD, exit_code=None,
                          stderr="TIMEOUT: outcome unknown; do not replay")
        f.seek(0)
        f.truncate()
        json.dump(result, f, ensure_ascii=False, indent=2)
        f.write("\n")
        f.flush()
        os.fsync(f.fileno())
    print(json.dumps(result, ensure_ascii=False, indent=2))
    if result["exit_code"] != 0:
        raise SystemExit(1)
    return result


def temporary():
    receipt = json.loads((ROOT / "create-pane.json").read_text())
    value = json.loads(receipt["stdout"])
    ref = value.get("surface_ref")
    if not ref or ref in ORIGINAL or value.get("workspace_ref") != WORKSPACE:
        raise SystemExit("temporary target not uniquely verified")
    return ref


action = sys.argv[1]
if action == "preflight":
    record("tree-before", [CMUX, "--json", "--id-format", "both", "tree"] + SCOPE)
    for i, surface in enumerate(ORIGINAL):
        record("original-before-" + str(i), [CMUX, "read-screen"] + SCOPE + ["--surface", surface, "--lines", "35"])
elif action == "create":
    record("create-pane", [CMUX, "--json", "--id-format", "both", "new-split", "down"] + SCOPE + ["--surface", ORIGINAL[0], "--focus", "false"])
elif action == "inspect":
    label = sys.argv[2]
    surface = temporary()
    record("temp-screen-" + label, [CMUX, "read-screen"] + SCOPE + ["--surface", surface, "--lines", "35"])
    record("temp-tree-" + label, [CMUX, "--json", "--id-format", "both", "tree"] + SCOPE)
elif action == "run":
    tool = sys.argv[2]
    if tool not in ("opencode", "codex"):
        raise SystemExit("unsupported tool")
    surface = temporary()
    argv = [CWD + "/bin/real-seat", "--tool", tool, "--evidence-dir", str(ROOT),
            "--cwd", CWD, "--wrapper", CWD + "/bin/agentflow",
            "--window", WINDOW, "--workspace", WORKSPACE, "--surface", surface]
    record(tool + "-send", [CMUX, "send", "--paste"] + SCOPE + ["--surface", surface, "--", shlex.join(argv)])
    record(tool + "-submit", [CMUX, "send-key"] + SCOPE + ["--surface", surface, "enter"])
elif action == "status":
    tool = sys.argv[2]
    result = json.loads((ROOT / tool / "result.json").read_text())
    print(json.dumps({k: result.get(k) for k in ["tool", "status", "error", "terminal_cwd", "launch_calls", "verified_claim", "nonce_exact_match", "lock_contention", "lock_released", "duplicate_start_rejected", "reopened_duplicate_rejected", "stored_seat"]}, indent=2))
elif action == "final-tree":
    record("tree-final", [CMUX, "--json", "--id-format", "both", "tree"] + SCOPE)
elif action == "close":
    record("close-temp", [CMUX, "--json", "close-surface"] + SCOPE + ["--surface", temporary()])
    record("tree-after", [CMUX, "--json", "--id-format", "both", "tree"] + SCOPE)
    for i, surface in enumerate(ORIGINAL):
        record("original-after-" + str(i), [CMUX, "read-screen"] + SCOPE + ["--surface", surface, "--lines", "35"])
elif action == "verify":
    label = sys.argv[2]
    commands = {"go-test": ["go", "test", "./..."], "go-build": ["go", "build", "./..."],
                "make-check": ["make", "check"], "go-tidy": ["go", "mod", "tidy", "-diff"],
                "focused-test": ["go", "test", "./internal/runtime", "./internal/cli", "./tests/e2e", "./probes/real-seat"]}
    env = dict(os.environ)
    env.update(GOTOOLCHAIN="local", CGO_ENABLED="0", GOFLAGS="-mod=readonly",
               GOCACHE=CWD+"/.cache/go-build", GOMODCACHE=CWD+"/.cache/go-mod",
               GOPATH=CWD+"/.cache/gopath", GOTMPDIR=CWD+"/.cache/tmp", TMPDIR=CWD+"/.cache/tmp")
    record(label, commands[label], timeout=55, env=env)
elif action == "handoff":
    payload = json.loads((ROOT / "agent_handoff.json").read_text())
    record("handoff-receipt", [CMUX, "agent", "message", "surface:1000060018", "--from", "agentflow/资深研发", "--thread", payload["session_id"], "--json", "--", "[HANDOFF] " + json.dumps(payload, ensure_ascii=False)])
elif action == "seal":
    sources = ["internal/runtime/native.go", "internal/runtime/start.go", "internal/cli/seat.go",
               "probes/real-seat/main.go", "tests/e2e/native_launcher_test.go", "README.md",
               "internal/runtime/README.md", "probes/README.md", "bin/agentflow", "bin/real-seat"]
    files = [Path(CWD) / p for p in sources] + sorted(p for p in ROOT.rglob("*") if p.is_file() and p.name != "sha256.json")
    hashes = {str(p.relative_to(CWD)): hashlib.sha256(p.read_bytes()).hexdigest() for p in files}
    with (ROOT / "sha256.json").open("x") as f:
        json.dump(hashes, f, indent=2)
        f.write("\n")
    print(json.dumps(dict(files=len(hashes), status="SEALED")))
elif action == "check-seal":
    hashes = json.loads((ROOT / "sha256.json").read_text())
    for name, expected in hashes.items():
        assert hashlib.sha256((Path(CWD) / name).read_bytes()).hexdigest() == expected, name
    print(json.dumps(dict(files=len(hashes), status="PASS")))
else:
    raise SystemExit("preflight | create | inspect LABEL | run TOOL | close | verify CHECK | handoff")
