#!/usr/bin/env python3
"""Local fixture/check runner. Never enables a real model integration."""
import hashlib
import json
import os
from pathlib import Path
import signal
import subprocess
import sys
import time

E = Path(__file__).resolve().parent
ROOT = E.parents[2]
env = dict(os.environ)
for key in ("AGENTFLOW_REAL_CODEX", "AGENTFLOW_REAL_EVIDENCE_DIR", "AGENTFLOW_CLAUDE_FIX_EVIDENCE"):
    env.pop(key, None)
env.update(GOTOOLCHAIN="local", GOFLAGS="-mod=readonly", CGO_ENABLED="0",
           GOCACHE=str(ROOT / ".cache/go-build"), GOMODCACHE=str(ROOT / ".cache/go-mod"),
           GOPATH=str(ROOT / ".cache/gopath"), GOTMPDIR=str(ROOT / ".cache/tmp"), TMPDIR=str(ROOT / ".cache/tmp"))
if len(sys.argv) == 1:
    with open(os.devnull, "wb") as out:
        child = subprocess.Popen([sys.executable, __file__, "run"], cwd=ROOT, env=env,
                                 stdout=out, stderr=subprocess.STDOUT, start_new_session=True)
    print(json.dumps({"pid": child.pid, "manifest": str(E / "verification.json")}))
    sys.exit(0)
assert sys.argv[1:] == ["run"]
assert not (E / "verification.json").exists(), "Refuse to overwrite earlier verification"
(E / "fixtures").mkdir(mode=0o700)
paths = sorted(path for base in ("cmd", "internal", "tests", "examples")
               for path in (ROOT / base).rglob("*") if path.is_file())
paths += [ROOT / name for name in ("go.mod", "go.sum", "Makefile", "README.md")]
paths += [E / "verify.py", E / "check-evidence.py"]

def hashes():
    return {str(path.relative_to(ROOT)): hashlib.sha256(path.read_bytes()).hexdigest() for path in paths}

before = hashes()
state = {"session_id": "claude-oauth-headless-default-fix-20261104-01", "active": None,
         "real_models": "NOT_RUN", "effective_permissions": "UNVERIFIED", "crash_recovery": "UNVERIFIED",
         "results": []}

def save():
    (E / "verification.json").write_text(json.dumps(state, indent=2) + "\n")

for name, args in [
    ("fixtures", ["go", "test", "-v", "./tests/e2e", "-run", "^TestHeadlessClaudeCLIWorkflowAndRestart$", "-count=1"]),
    ("focused", ["go", "test", "./internal/runtime", "./internal/coordinator", "./internal/probe", "-run", "TestHeadlessClaude|TestHelpCannotProveRuntime", "-count=1"]),
    ("test", ["go", "test", "./..."]),
    ("build", ["go", "build", "./..."]),
    ("check", ["make", "check"]),
    ("tidy", ["go", "mod", "tidy", "-diff"]),
    ("race", ["go", "test", "-race", "./internal/runtime", "./internal/coordinator", "./internal/cli",
              "./internal/store", "./internal/loopcore", "./internal/lock", "./internal/probe", "./tests/e2e"]),
]:
    state["active"] = name
    save()
    jobenv = dict(env)
    if name == "fixtures":
        jobenv["AGENTFLOW_CLAUDE_FIX_EVIDENCE"] = str(E / "fixtures")
    if name == "race":
        jobenv["CGO_ENABLED"] = "1"
    started = time.monotonic()
    with (E / (name + ".log")).open("xb") as output:
        process = subprocess.Popen(args, cwd=ROOT, env=jobenv, stdout=output, stderr=subprocess.STDOUT, start_new_session=True)
        try:
            code = process.wait(timeout=300)
        except subprocess.TimeoutExpired:
            os.killpg(process.pid, signal.SIGKILL)
            process.wait(timeout=5)
            code = -9
    state["results"].append({"name": name, "command": args, "exit_code": code,
                             "seconds": round(time.monotonic() - started, 3)})
    save()
    if code:
        break
after = hashes()
state.update(active=None, source_unchanged=before == after, source_sha256=after,
             changed_sources=[key for key in before if before[key] != after[key]])
save()
