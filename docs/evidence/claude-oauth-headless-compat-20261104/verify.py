#!/usr/bin/env python3
"""Required local checks. No real model opt-ins are enabled by this script."""
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
for key in ("AGENTFLOW_REAL_CODEX", "AGENTFLOW_REAL_EVIDENCE_DIR"):
    env.pop(key, None)
env.update(GOTOOLCHAIN="local", GOFLAGS="-mod=readonly", CGO_ENABLED="0",
           GOCACHE=str(ROOT / ".cache/go-build"), GOMODCACHE=str(ROOT / ".cache/go-mod"),
           GOPATH=str(ROOT / ".cache/gopath"), GOTMPDIR=str(ROOT / ".cache/tmp"), TMPDIR=str(ROOT / ".cache/tmp"))
if len(sys.argv) == 1:
    with open(os.devnull, "wb") as output:
        child = subprocess.Popen([sys.executable, __file__, "run"], cwd=ROOT, env=env,
                                 stdout=output, stderr=subprocess.STDOUT, start_new_session=True)
    print(json.dumps({"pid": child.pid, "manifest": str(E / "verification.json")}))
    sys.exit(0)

assert not (E / "verification.json").exists(), "Refuse to overwrite earlier checks"
paths = sorted(path for base in ("cmd", "internal", "tests", "examples") for path in (ROOT / base).rglob("*") if path.is_file())
paths += [ROOT / name for name in ("go.mod", "go.sum", "Makefile", "README.md")]
def hashes():
    return {str(path.relative_to(ROOT)): hashlib.sha256(path.read_bytes()).hexdigest() for path in paths}
before = hashes()
state = {"active": None, "results": []}
def save():
    (E / "verification.json").write_text(json.dumps(state, indent=2) + "\n")

for name, args in [
    ("test", ["go", "test", "./..."]),
    ("build", ["go", "build", "./..."]),
    ("check", ["make", "check"]),
    ("tidy", ["go", "mod", "tidy", "-diff"]),
    ("race", ["go", "test", "-race", "./internal/runtime", "./internal/coordinator", "./internal/cli", "./internal/store", "./internal/loopcore", "./internal/lock", "./tests/e2e"]),
]:
    state["active"] = name
    save()
    jobenv = dict(env)
    if name == "race":
        jobenv["CGO_ENABLED"] = "1"
    started = time.monotonic()
    with (E / (name + ".log")).open("wb") as output:
        process = subprocess.Popen(args, cwd=ROOT, env=jobenv, stdout=output, stderr=subprocess.STDOUT, start_new_session=True)
        try:
            code = process.wait(timeout=240)
        except subprocess.TimeoutExpired:
            os.killpg(process.pid, signal.SIGKILL)
            process.wait(timeout=5)
            code = -9
    state["results"].append({"name": name, "command": args, "exit_code": code, "seconds": round(time.monotonic() - started, 3)})
    save()
    if code:
        break
after = hashes()
state.update(active=None, source_unchanged=before == after, source_sha256=after,
             changed_sources=[key for key in before if before[key] != after[key]])
save()
