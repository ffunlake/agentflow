#!/usr/bin/env python3
"""Explicit real-model acceptance; fresh evidence only, bounded processes, no login."""
import json
import os
from pathlib import Path
import signal
import sqlite3
import subprocess
import sys
import tempfile
import time
import traceback

BASE = Path(__file__).resolve().parent
ROOT = BASE.parents[2]
E = BASE / "attempt-2" if "attempt-2" in sys.argv[1:] else BASE
PARENT = Path("/var/folders/wv/x8nwj9tj7xz5dhqf5f2mt3hw0000gn/T/opencode")
RUN = "claude-oauth-headless-compat-20261104-01"
NONCE = "CLAUDE_OAUTH_HEADLESS_COMPAT_20261104_01"
env = dict(os.environ)
env["AF_CLAUDE_COMPAT_EVIDENCE"] = str(E)
env.update(GOTOOLCHAIN="local", GOFLAGS="-mod=readonly", CGO_ENABLED="0",
           GOCACHE=str(ROOT / ".cache/go-build"), GOMODCACHE=str(ROOT / ".cache/go-mod"),
           GOPATH=str(ROOT / ".cache/gopath"), GOTMPDIR=str(ROOT / ".cache/tmp"), TMPDIR=str(ROOT / ".cache/tmp"))
if len(sys.argv) == 1 or sys.argv[1:] == ["attempt-2"]:
    with open(os.devnull, "wb") as output:
        child = subprocess.Popen([sys.executable, __file__, "run", *sys.argv[1:]], cwd=ROOT, env=env,
                                 stdout=output, stderr=subprocess.STDOUT, start_new_session=True)
    print(json.dumps({"pid": child.pid, "manifest": str(E / "validation.json")}))
    sys.exit(0)

os.umask(0o077)
if E != BASE:
    E.mkdir(mode=0o700)
assert not (E / "validation.json").exists(), "Refuse to overwrite an earlier run"
temp = Path(tempfile.mkdtemp(prefix="aco-", dir=PARENT)).resolve()
cwd, run, binary = temp / "cwd", temp / "run", temp / "agentflow"
cwd.mkdir(mode=0o700)
run.mkdir(mode=0o700)
observer = BASE / "native-no-tools.py"
observer.chmod(0o700)
owned = []
state = {"run_id": RUN, "nonce": NONCE, "uid": os.getuid(), "cwd": str(cwd), "run_dir": str(run),
         "outcome": "RUNNING", "commands": [], "effective_permissions": "UNVERIFIED",
         "auth_environment_presence_only": {key: key in os.environ for key in (
             "ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "CLAUDE_CODE_OAUTH_TOKEN", "ANTHROPIC_BASE_URL",
             "CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_FOUNDRY")}}

def save():
    (E / "validation.json").write_text(json.dumps(state, indent=2) + "\n")

def command(label, args, timeout=60, at=cwd):
    record = {"label": label, "argv": list(map(str, args)), "cwd": str(at), "started": time.time()}
    state["commands"].append(record)
    save()
    process = subprocess.Popen(record["argv"], cwd=at, env=env, stdin=subprocess.DEVNULL,
                               stdout=subprocess.PIPE, stderr=subprocess.PIPE, start_new_session=True)
    try:
        stdout, stderr = process.communicate(timeout=timeout)
    except subprocess.TimeoutExpired:
        os.killpg(process.pid, signal.SIGKILL)
        stdout, stderr = process.communicate(timeout=5)
        record["timed_out"] = True
    (E / (label + ".stdout.log")).write_bytes(stdout)
    (E / (label + ".stderr.log")).write_bytes(stderr)
    record.update(exit_code=process.returncode, seconds=round(time.time() - record["started"], 3))
    save()
    assert process.returncode == 0, label + " failed"
    return json.loads(stdout) if stdout.strip().startswith(b"{") else stdout.decode()

def launch(label, args):
    record = {"label": label, "argv": list(map(str, args)), "cwd": str(cwd), "started": time.time()}
    state["commands"].append(record)
    with (E / (label + ".stdout.log")).open("wb") as out, (E / (label + ".stderr.log")).open("wb") as err:
        process = subprocess.Popen(record["argv"], cwd=cwd, env=env, stdin=subprocess.DEVNULL,
                                   stdout=out, stderr=err, start_new_session=True)
    owned.append((process, record))
    record["pid"] = process.pid
    state["phase"] = label
    save()
    deadline = time.monotonic() + 110
    while time.monotonic() < deadline:
        text = (E / (label + ".stdout.log")).read_text()
        if text.strip():
            response = json.loads(text)
            state[label] = response
            save()
            assert response.get("status") in ("READY", "COORDINATOR_READY"), label + " not ready"
            return response
        assert process.poll() is None, label + " exited"
        time.sleep(.2)
    raise TimeoutError(label + " readiness timeout")

def close():
    for process, record in owned:
        if process.poll() is None:
            process.send_signal(signal.SIGTERM)
            try:
                process.wait(timeout=5)
            except subprocess.TimeoutExpired:
                os.killpg(process.pid, signal.SIGKILL)
                process.wait(timeout=5)
        record["exit_code"] = process.returncode
    save()

def workflow(label, kind, revision, request, reason=None):
    args = [binary, "workflow", kind, "--run", RUN, "--run-dir", run, "--agent", "main",
            "--generation", "1", "--revision", str(revision), "--request-id", request, "--timeout", "110s", "--json"]
    if reason:
        args += ["--reason", reason]
    return command(label, args, timeout=115)

def snapshot():
    db = sqlite3.connect("file:" + str(run / "state.db") + "?mode=ro", uri=True)
    tables = {}
    for (table,) in db.execute("SELECT name FROM sqlite_master WHERE type='table' ORDER BY name"):
        cur = db.execute('SELECT * FROM "' + table.replace('"', '""') + '" ORDER BY rowid')
        tables[table] = [dict(zip([d[0] for d in cur.description], row)) for row in cur]
    db.close()
    return tables

try:
    command("build", ["go", "build", "-o", binary, "./cmd/agentflow"], timeout=60, at=ROOT)
    instruction = ("Do not use tools, inspect files, authenticate or modify anything. "
        'For purpose=execute with revision=1, return exactly {"kind":"blocked","reason":"explicit lead resume required"}. '
        "For purpose=execute with revision greater than 1, return kind=handoff with summary and evidence each containing the exact nonce " + NONCE + ", next=review. "
        "For purpose=review, check the supplied handoff summary and evidence for that exact nonce; return kind=approve and accepted=[true] only if both contain it. "
        "No external facts or tests are required: this is a no-tools nonce protocol check.")
    config = {"version": 1, "name": RUN, "terminal": "cmux", "lead": "main", "context_dir": str(temp / "context"),
        "defaults": {"tool": "claude", "session": "persistent", "permissions": "default", "cwd": str(cwd)},
        "agents": {agent: {"role": role, "args": ["--safe-mode"], "prompt": "Follow scoped nonce instructions. Do not use tools, inspect files, authenticate or modify files."}
                   for agent, role in [("main", "Reviewer"), ("builder", "Developer")]},
        "layout": {"split": "columns", "children": [{"agent": "main"}, {"agent": "builder"}]},
        "workflow": {"entry": "nonce", "completion": "done", "steps": [{"id": "nonce", "execute": {"agent": "builder"},
            "reviewer": "main", "instruction": instruction, "acceptance": ["The handoff summary and evidence both contain " + NONCE + "."],
            "handoff_required": True, "max_attempts": 2, "transitions": {"approve": "next", "changes": "self", "retry": "self", "blocked": "pause"}, "next": "done"}]}}
    (E / "team.json").write_text(json.dumps(config, indent=2) + "\n")
    native_flags = ["--headless-claude", "--native-binary", observer, "--turn-timeout", "50s"]
    up = launch("up", [binary, "up", "-f", E / "team.json", *native_flags, "--startup-timeout", "105s", "--run", RUN, "--run-dir", run, "--json"])
    assert len(up["seats"]) == 2
    start = workflow("workflow-start", "start", 0, "start-1")
    assert start["workflow_current"]["phase"] == "PAUSED" and start["workflow_current"]["revision"] == 2
    paused = snapshot()
    (E / "paused-before-reopen.db.json").write_text(json.dumps(paused, indent=2) + "\n")
    close()
    launch("reopen-paused", [binary, "coordinator", *native_flags, "--run", RUN, "--run-dir", run, "--json"])
    (E / "paused-after-reopen.db.json").write_text(json.dumps(snapshot(), indent=2) + "\n")
    replay = workflow("paused-start-replay", "start", 0, "start-1")
    assert replay["workflow"] == start["workflow"] and not replay.get("executed") and not replay.get("dispatch")
    assert snapshot() == paused
    (E / "paused-after-replay.db.json").write_text(json.dumps(snapshot(), indent=2) + "\n")
    reason = "Continue the no-tools nonce protocol " + NONCE
    resumed = workflow("workflow-resume", "resume", 2, "resume-1", reason)
    assert resumed["workflow_current"]["phase"] == "COMPLETED" and resumed["workflow_current"]["revision"] == 5
    completed = snapshot()
    (E / "completed-before-reopen.db.json").write_text(json.dumps(completed, indent=2) + "\n")
    close()
    launch("reopen-completed", [binary, "coordinator", *native_flags, "--run", RUN, "--run-dir", run, "--json"])
    (E / "completed-after-reopen.db.json").write_text(json.dumps(snapshot(), indent=2) + "\n")
    for label, kind, revision, request, why, original in [
        ("completed-start-replay", "start", 0, "start-1", None, start),
        ("completed-resume-replay", "resume", 2, "resume-1", reason, resumed),
        ("workflow-status", "status", 0, "status-1", None, None),
        ("terminal-drain", "drain", 5, "drain-1", None, None)]:
        response = workflow(label, kind, revision, request, why)
        assert not response.get("executed") and not response.get("dispatch")
        if original:
            assert response["workflow"] == original["workflow"]
    assert snapshot() == completed
    (E / "completed-after-replay.db.json").write_text(json.dumps(snapshot(), indent=2) + "\n")
    calls = [json.loads(line) for line in (E / "native-invocations.jsonl").read_text().splitlines()]
    assert len(calls) == 5 and sum("--resume" in call["argv"] for call in calls) == 3
    state["checks"] = {key: "PASS" for key in ("two_ready_roles", "pause", "persistent_resume", "nonce_handoff", "review_approve", "completed_revision_5", "reopen_paused", "reopen_completed", "exact_replay_without_native_calls")}
    state["outcome"] = "PASS"
except Exception as error:
    state.update(outcome="BLOCKED_CONFIRM", error=str(error))
    (E / "failure.log").write_text(traceback.format_exc())
finally:
    close()
    captures = []
    for path in sorted(run.glob("headless-*.json")):
        if not path.name.startswith(("headless-start-", "headless-result-")):
            continue
        capture = json.loads(path.read_text())
        events = [json.loads(line) for line in capture["stdout"].splitlines()]
        tool_blocks = [block for event in events for block in event.get("message", {}).get("content", []) if block.get("type") == "tool_use"]
        assert not tool_blocks and not any(event["type"] == "tool_progress" for event in events)
        assert all(event.get("tools") == [] for event in events if event["type"] == "system" and event.get("subtype") == "init")
        (E / path.name).write_text(path.read_text())
        captures.append({"file": path.name, "seat": capture["seat"], "message_id": capture["message_id"], "succeeded": capture["succeeded"], "tool_uses": len(tool_blocks)})
    state["captures"] = captures
    state["cwd_entries_after"] = sorted(str(path.relative_to(cwd)) for path in cwd.rglob("*"))
    state["owned_processes_exited"] = all(process.poll() is not None for process, _ in owned)
    state["lock_removed"] = not (run / "coordinator.lock").exists()
    if (run / "state.db").exists():
        (E / "final.db.json").write_text(json.dumps(snapshot(), indent=2) + "\n")
    if state["outcome"] == "PASS":
        assert len(captures) == 5 and all(capture["succeeded"] for capture in captures)
        assert not state["cwd_entries_after"] and state["owned_processes_exited"] and state["lock_removed"]
    state["finished"] = time.time()
    save()
