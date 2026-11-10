#!/usr/bin/env python3
"""One opt-in real CLI acceptance run; writes only evidence and its own temp tree."""
import hashlib
import json
import os
from pathlib import Path
import shutil
import signal
import sqlite3
import subprocess
import tempfile
import time
import traceback
import uuid

E = Path(__file__).resolve().parent
ROOT = E.parents[2]
RUN = "real-opencode-v1-validation-20261104-01"
state = {"session_id": RUN, "checks": {}, "commands": []}
owner = None
owned = []

def save():
    (E / "validation.json").write_text(json.dumps(state, indent=2) + "\n")

def checkpoint(phase):
    state["phase"] = phase
    save()
    print(phase, flush=True)

def source_hashes():
    paths = [p for folder in ("cmd", "internal", "tests", "examples")
             for p in (ROOT / folder).rglob("*") if p.is_file()]
    paths += [ROOT / n for n in ("go.mod", "go.sum", "Makefile", "README.md")]
    return {str(p.relative_to(ROOT)): hashlib.sha256(p.read_bytes()).hexdigest() for p in sorted(paths)}

def command(label, args, timeout=600):
    record = {"label": label, "argv": [str(x) for x in args], "cwd": str(ROOT)}
    state["commands"].append(record)
    save()
    result = subprocess.run(record["argv"], cwd=ROOT, env=env, capture_output=True, timeout=timeout)
    (E / (label + ".stdout.log")).write_bytes(result.stdout)
    (E / (label + ".stderr.log")).write_bytes(result.stderr)
    record["exit_code"] = result.returncode
    save()
    return result

def launch(label, args):
    global owner
    record = {"label": label, "argv": [str(x) for x in args], "cwd": str(ROOT)}
    state["commands"].append(record)
    stdout = (E / (label + ".stdout.log")).open("wb")
    stderr = (E / (label + ".stderr.log")).open("wb")
    p = subprocess.Popen(record["argv"], cwd=ROOT, env=env, stdout=stdout, stderr=stderr, start_new_session=True)
    stdout.close()
    stderr.close()
    record["pid"] = p.pid
    owned.append((p, record))
    owner = p
    save()
    deadline = time.monotonic() + 420
    while time.monotonic() < deadline:
        text = (E / (label + ".stdout.log")).read_text()
        if text.strip():
            result = json.loads(text)
            if result.get("status") in ("READY", "COORDINATOR_READY"):
                return result
            raise RuntimeError(label + " failed: " + text)
        if p.poll() is not None:
            raise RuntimeError(label + " exited before readiness")
        time.sleep(.25)
    raise TimeoutError(label + " readiness deadline")

def close_owner():
    global owner
    if owner and owner.poll() is None:
        owner.send_signal(signal.SIGTERM)
        owner.wait(timeout=20)
    for p, r in owned:
        if p.poll() is not None:
            r["exit_code"] = p.returncode
    owner = None
    save()

def snapshot(label):
    db = sqlite3.connect("file:" + str(run / "state.db") + "?mode=ro", uri=True)
    tables = [r[0] for r in db.execute("SELECT name FROM sqlite_master WHERE type='table' ORDER BY name")]
    data = {}
    for table in tables:
        cur = db.execute('SELECT * FROM "' + table.replace('"', '""') + '" ORDER BY rowid')
        cols = [d[0] for d in cur.description]
        data[table] = [dict(zip(cols, row)) for row in cur]
    integrity = list(db.execute("PRAGMA integrity_check"))
    foreign = list(db.execute("PRAGMA foreign_key_check"))
    db.close()
    (E / (label + ".db.json")).write_text(json.dumps(data, indent=2) + "\n")
    state["checks"][label + "_db_integrity"] = integrity == [("ok",)] and not foreign
    save()
    return data

def workflow(label, kind, revision, request_id, reason=None, want=0):
    args = [binary, "workflow", kind, "--run", RUN, "--run-dir", run,
            "--agent", "main", "--generation", "1", "--revision", str(revision),
            "--request-id", request_id, "--timeout", "8m", "--json"]
    if reason:
        args += ["--reason", reason]
    result = command(label, args)
    assert result.returncode == want, (label, result.stdout.decode(), result.stderr.decode())
    return json.loads(result.stdout)

env = os.environ.copy()
env["AF_VALIDATION_EVIDENCE"] = str(E)
temp = Path(tempfile.mkdtemp(prefix="af-oc-v1-", dir="/tmp")).resolve()
run, cwd, binary = temp / "run", temp / "cwd", temp / "agentflow"
run.mkdir(mode=0o700)
cwd.mkdir(mode=0o700)
observer = E / "native-observer.py"
observer.chmod(0o700)
state.update({"temp_root": str(temp), "run_dir": str(run), "cwd": str(cwd),
              "nonce": "AF_OC_REAL_" + uuid.uuid4().hex,
              "native_cli": "/opt/homebrew/bin/opencode", "observer": str(observer),
              "started_at": time.time(), "production_before": source_hashes()})
try:
    checkpoint("build_and_prepare")
    assert command("build", ["go", "build", "-o", binary, "./cmd/agentflow"]).returncode == 0
    version = command("opencode-version", ["/opt/homebrew/bin/opencode", "--version"], 20)
    state["native_version"] = version.stdout.decode().strip()
    assert command("opencode-run-help", ["/opt/homebrew/bin/opencode", "run", "--help"], 20).returncode == 0
    # JSON is an accepted config format; begin from the same two-role example contract.
    nonce = state["nonce"]
    instruction = ("Do not use tools, inspect files, authenticate, or modify anything. "
        "For purpose=execute with revision=1, return exactly {\"kind\":\"blocked\",\"reason\":\""
        + nonce + " explicit lead resume requested for acceptance\"}. "
        "For purpose=execute with revision greater than 1, return kind=handoff with summary and evidence "
        "each containing the exact nonce " + nonce + ", next=review. "
        "For purpose=review, check the supplied handoff summary and evidence for that exact nonce; "
        "return kind=approve and accepted=[true] only if both contain it. "
        "No external facts or tests are required: this is a no-tools nonce protocol check.")
    config = {"version": 1, "name": RUN, "terminal": "cmux", "lead": "main",
        "context_dir": str(temp / "context"),
        "defaults": {"tool": "opencode", "session": "persistent", "permissions": "default", "cwd": str(cwd)},
        "agents": {"main": {"role": "Reviewer", "prompt": "Follow scoped nonce instructions. Do not use tools or inspect or modify files."},
                   "builder": {"role": "Developer", "prompt": "Follow scoped nonce instructions. Do not use tools or inspect or modify files."}},
        "layout": {"split": "columns", "children": [{"agent": "main"}, {"agent": "builder"}]},
        "workflow": {"entry": "nonce", "completion": "done", "steps": [{"id": "nonce", "execute": {"agent": "builder"},
            "reviewer": "main", "instruction": instruction, "acceptance": ["The handoff summary and evidence both contain " + nonce + "."],
            "handoff_required": True, "max_attempts": 2, "transitions": {"approve": "next", "changes": "self", "retry": "self", "blocked": "pause"}, "next": "done"}]}}
    config_path = E / "team.json"
    config_path.write_text(json.dumps(config, indent=2) + "\n")
    checkpoint("real_up")
    up = launch("up", [binary, "up", "-f", config_path, "--headless-opencode", "--native-binary", observer,
        "--turn-timeout", "3m", "--startup-timeout", "7m", "--run", RUN, "--run-dir", run, "--json"])
    state["up"] = up
    assert len(up["seats"]) == 2
    snapshot("after-up")
    state["checks"]["real_startup"] = "PASS"
    checkpoint("workflow_start_expected_pause")
    start = workflow("workflow-start", "start", 0, "start-real-1")
    state["start"] = start
    assert start["workflow_current"]["phase"] == "PAUSED" and start["workflow_current"]["revision"] == 2
    paused = snapshot("paused")
    close_owner()
    assert not (run / "coordinator.lock").exists() and not (run / "ipc.sock").exists()
    checkpoint("reopen_paused_and_exact_start_replay")
    launch("coordinator-reopen-paused", [binary, "coordinator", "--headless-opencode", "--native-binary", observer,
        "--turn-timeout", "3m", "--run", RUN, "--run-dir", run, "--json"])
    replay = workflow("start-exact-replay", "start", 0, "start-real-1")
    assert not replay.get("executed") and not replay.get("dispatch")
    assert snapshot("paused-after-replay") == paused
    state["checks"]["paused_restart_exact_replay"] = "PASS"
    checkpoint("explicit_resume_second_persistent_delivery")
    resumed = workflow("workflow-resume", "resume", 2, "resume-real-1", "No-tools nonce protocol approved; continue " + nonce)
    state["resume"] = resumed
    assert resumed["workflow_current"]["phase"] == "COMPLETED" and resumed["workflow_current"]["revision"] == 5
    completed = snapshot("completed")
    state["checks"]["workflow_execute_review_complete"] = "PASS"
    close_owner()
    checkpoint("reopen_completed_and_replay")
    launch("coordinator-reopen-completed", [binary, "coordinator", "--headless-opencode", "--native-binary", observer,
        "--turn-timeout", "3m", "--run", RUN, "--run-dir", run, "--json"])
    for label, kind, rev, ident, reason in [
        ("completed-start-replay", "start", 0, "start-real-1", None),
        ("completed-resume-replay", "resume", 2, "resume-real-1", "No-tools nonce protocol approved; continue " + nonce),
        ("workflow-status", "status", 0, "status-real-1", None),
        ("terminal-drain", "drain", 5, "drain-real-1", None)]:
        result = workflow(label, kind, rev, ident, reason)
        assert not result.get("executed") and not result.get("dispatch")
    assert snapshot("completed-after-replays") == completed
    state["checks"]["completed_restart_exact_replay"] = "PASS"
    close_owner()
    checkpoint("native_and_identity_consistency")
    seats = {s["agent_id"]: s for s in completed["seats"]}
    captures = sorted(run.glob("headless-*.json"))
    assert len(captures) == 5, len(captures)
    observed = []
    nonce_seen = False
    for path in captures:
        cap = json.loads(path.read_text())
        events = [json.loads(line) for line in cap["stdout"].splitlines()]
        ids = {e.get("sessionID") for e in events if e.get("sessionID")}
        seat = cap["seat"]
        aid = seat["agent_id"]
        assert seat["run_id"] == RUN and seat["generation"] == 1
        assert ids == {seats[aid]["native_session_id"]}
        assert all(e.get("type") in ("step_start", "text", "step_finish", "reasoning") for e in events)
        assert all(e.get("part", {}).get("type") not in ("tool", "tool_use") for e in events)
        assert any(e["type"] == "step_finish" and e["part"]["reason"] == "stop" for e in events)
        nonce_seen |= nonce in cap["stdout"]
        (E / path.name).write_bytes(path.read_bytes())
        (E / (path.stem + ".jsonl")).write_text(cap["stdout"])
        observed.append({"capture": path.name, "agent": aid, "generation": 1,
            "run_id": RUN, "session_id": next(iter(ids)), "message_id": cap["message_id"]})
    calls = [json.loads(line) for line in (E / "native-invocations.jsonl").read_text().splitlines()]
    exits = [c for c in calls if c["event"] == "exit"]
    assert len(exits) == 5 and all(c["exit_code"] == 0 for c in exits)
    resumes = [c for c in exits if "--session" in c["argv"]]
    assert len(resumes) == 3
    builder_id = seats["builder"]["native_session_id"]
    assert sum(c["argv"][c["argv"].index("--session") + 1] == builder_id for c in resumes) == 2
    assert nonce_seen and not list(cwd.iterdir())
    state["captures"] = observed
    state["checks"].update({"persistent_resume_second_delivery": "PASS", "nonce": "PASS",
        "no_tool_events": "PASS", "cwd_unchanged_empty": "PASS", "native_exit_codes": "PASS",
        "effective_permissions": "UNVERIFIED"})
    state["outcome"] = "PASS"
except Exception as exc:
    state["outcome"] = "UNVERIFIED"
    state["error"] = str(exc)
    (E / "failure.log").write_text(traceback.format_exc())
finally:
    try:
        close_owner()
        state["cleanup"] = {"owned_processes_exited": all(p.poll() is not None for p, r in owned),
            "lock_removed": not (run / "coordinator.lock").exists(), "socket_removed": not (run / "ipc.sock").exists(),
            "cwd_entries": [p.name for p in cwd.iterdir()]}
        if (run / "state.db").exists():
            snapshot("final")
            db = sqlite3.connect("file:" + str(run / "state.db") + "?mode=ro", uri=True)
            dest = sqlite3.connect(E / "state.db")
            db.backup(dest)
            dest.close()
            db.close()
        for path in run.iterdir():
            if path.is_file() and path.suffix == ".json":
                shutil.copy2(path, E / path.name)
        state["production_after"] = source_hashes()
        state["checks"]["production_files_unchanged"] = state["production_before"] == state["production_after"]
        # Remove only this harness's unique temp root after preserving the private run evidence.
        shutil.rmtree(temp)
        state["cleanup"]["temporary_root_removed"] = not temp.exists()
    except Exception:
        state["cleanup_error"] = traceback.format_exc()
    state["finished_at"] = time.time()
    checkpoint("finished")
