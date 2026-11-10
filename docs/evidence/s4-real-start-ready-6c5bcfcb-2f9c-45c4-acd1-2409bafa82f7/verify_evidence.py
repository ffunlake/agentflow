"""Offline verification only: never invokes cmux or a native provider."""
import hashlib
import json
from pathlib import Path
import sqlite3
import sys

ROOT = Path(__file__).resolve().parent
WINDOW = "window:1000060005"
WORKSPACE = "workspace:1000060011"
CWD = "/Users/funlake/Projects/agentflow"


def load(path):
    return json.loads((ROOT / path).read_text())


def receipt(path):
    data = load(path)
    assert data["exit_code"] == 0, path
    return json.loads(data["stdout"])


created = receipt("create-pane.json")
temp = created["surface_ref"]
assert temp not in ["surface:1000060034", "surface:1000060035"]
assert created["window_ref"] == WINDOW and created["workspace_ref"] == WORKSPACE
before = receipt("tree-before.json")["windows"][0]["workspaces"][0]
after = receipt("tree-final.json")["windows"][0]["workspaces"][0]
assert before["layout"] == after["layout"], "original layout not restored"
assert [(p["ref"], p["surface_refs"]) for p in before["panes"]] == [(p["ref"], p["surface_refs"]) for p in after["panes"]]
summary = {}
for tool in ["opencode", "codex"]:
    run = load(tool + "/result.json")
    snapshot = load(tool + "/snapshot.json")
    assert run["status"] == "PASS" and run["launch_calls"] == 1
    assert run["terminal_cwd"] == run["cwd"] == CWD
    assert run["nonce_exact_match"] and run["duplicate_start_rejected"] and run["reopened_duplicate_rejected"]
    assert run["lock_contention"] == "ALREADY_RUNNING\n" and run["lock_released"]
    assert not (ROOT / tool / "coordinator.lock").exists()
    for filename in ["native.jsonl", "snapshot.json"]:
        assert (ROOT / tool / filename).stat().st_mode & 0o077 == 0
    assert (ROOT / tool).stat().st_mode & 0o077 == 0
    challenge, finished = [json.loads(line) for line in (ROOT / tool / "native.jsonl").read_text().splitlines()]
    assert finished["executed"] and finished["process_succeeded"] and not finished["truncated"]
    expected = challenge["challenge"]
    assert expected["actor"] == {"runId": run["run_id"], "agentId": "solo", "generation": 1}
    assert expected["tool"] == tool and expected["status"] == "READY" and len(expected["nonce"]) == 48
    assert challenge["command"]["binary"] == "/opt/homebrew/bin/" + tool
    events = [json.loads(line) for line in finished["stdout"].splitlines()]
    assert not any(e["type"] in ["error", "turn.failed"] for e in events)
    diagnostics = []
    if tool == "opencode":
        ids = {e["sessionID"] for e in events}
        text = "".join(e["part"]["text"] for e in events if e["type"] == "text" and e["part"]["type"] == "text")
        assert events[-1]["type"] == "step_finish" and events[-1]["part"]["reason"] == "stop"
        assert {e["type"] for e in events} <= {"step_start", "text", "step_finish"}
    else:
        ids = {e["thread_id"] for e in events if e["type"] == "thread.started"}
        text = "".join(e["item"]["text"] for e in events if e["type"] == "item.completed" and e["item"]["type"] == "agent_message")
        assert events[-1]["type"] == "turn.completed"
        assert all(e["item"]["type"] in ["agent_message", "error"] for e in events if e["type"] == "item.completed")
        diagnostics = [e["item"]["message"] for e in events if e["type"] == "item.completed" and e["item"]["type"] == "error"]
    assert json.loads(text) == expected
    claim = run["verified_claim"]
    assert ids == {claim["native"]["native_session_id"]}
    assert claim["actor"] == expected["actor"] and claim["status"] == "READY"
    assert claim["native"]["workspace_id"] == created["workspace_id"]
    assert claim["native"]["surface_id"] == created["surface_id"]
    assert snapshot["seats"][0]["status"] == "STARTING"  # immutable initial artifact
    assert snapshot["config"] == json.loads(run["stored_run"]["ConfigSnapshot"])
    with sqlite3.connect((ROOT / tool / "state.db").as_uri() + "?mode=ro", uri=True) as db:
        seat = db.execute("SELECT run_id,agent_id,generation,tool,native_session_id,workspace_id,surface_id,status,is_current FROM seats").fetchall()
        assert seat == [(run["run_id"], "solo", 1, tool, next(iter(ids)), created["workspace_id"], created["surface_id"], "READY", 1)]
        assert db.execute("SELECT event FROM journal ORDER BY seq").fetchall() == [("LAUNCH_REQUESTED",), ("LAUNCH_BOUND",)]
    for action in ["send", "submit"]:
        data = load(tool + "-" + action + ".json")
        assert data["exit_code"] == 0
        argv = data["argv"]
        assert argv[argv.index("--window") + 1] == WINDOW
        assert argv[argv.index("--workspace") + 1] == WORKSPACE
        assert argv[argv.index("--surface") + 1] == temp
    raw = ROOT / tool / "native-stdout.jsonl"
    if "--record" in sys.argv:
        with raw.open("x") as f:
            f.write(finished["stdout"])
        raw.chmod(0o600)
    assert raw.read_text() == finished["stdout"]
    summary[tool] = dict(status="PASS", native_id=next(iter(ids)), generation=1,
                         nonce=expected["nonce"], event_types=[e["type"] for e in events],
                         diagnostics=diagnostics, stdout_sha256=hashlib.sha256(raw.read_bytes()).hexdigest())
for name in ["go-test", "go-build", "make-check", "go-tidy"]:
    assert load(name + ".json")["exit_code"] == 0
result = dict(status="PASS", providers=summary, original_layout_restored=True,
              temporary_surface=temp, checks="test/build/check/tidy PASS")
if "--record" in sys.argv:
    with (ROOT / "verification.json").open("x") as f:
        json.dump(result, f, ensure_ascii=False, indent=2)
        f.write("\n")
print(json.dumps(result, ensure_ascii=False, indent=2))
