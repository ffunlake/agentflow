#!/usr/bin/env python3
"""Read-only consistency check over archived real evidence; no native requests."""
import hashlib
import json
import os
from pathlib import Path
import sqlite3

E = Path(__file__).resolve().parent
ROOT = E.parents[2]
load = lambda name: json.loads((E / name).read_text())
v = load("validation.json")
assert v["outcome"] == "PASS" and v["phase"] == "finished"
assert "error" not in v and "cleanup_error" not in v
assert all(c["exit_code"] == 0 for c in v["commands"])
assert v["production_before"] == v["production_after"]
for rel, digest in v["production_before"].items():
    assert hashlib.sha256((ROOT / rel).read_bytes()).hexdigest() == digest, rel
assert load("paused.db.json") == load("paused-after-replay.db.json")
assert load("completed.db.json") == load("completed-after-replays.db.json") == load("final.db.json")
original_start = load("workflow-start.stdout.log")
original_resume = load("workflow-resume.stdout.log")
for name, original in [("start-exact-replay", original_start), ("completed-start-replay", original_start),
                       ("completed-resume-replay", original_resume)]:
    result = load(name + ".stdout.log")
    assert result["workflow"] == original["workflow"]
    assert result["executed"] is False and "dispatch" not in result and not result["diagnostics"]
data = load("final.db.json")
db = sqlite3.connect("file:" + str(E / "state.db") + "?mode=ro", uri=True)
assert list(db.execute("PRAGMA integrity_check")) == [("ok",)]
assert not list(db.execute("PRAGMA foreign_key_check"))
for table, rows in data.items():
    cursor = db.execute('SELECT * FROM "' + table.replace('"', '""') + '" ORDER BY rowid')
    cols = [d[0] for d in cursor.description]
    assert rows == [dict(zip(cols, row)) for row in cursor], table
db.close()
up = load("up.stdout.log")
seats = {s["agent_id"]: s for s in data["seats"]}
proofs = {p["seat"]["AgentID"]: p for p in up["ready_proofs"]}
calls = [json.loads(line) for line in (E / "native-invocations.jsonl").read_text().splitlines()]
starts = [c for c in calls if c["event"] == "start"]
exits = [c for c in calls if c["event"] == "exit"]
assert len(starts) == len(exits) == 5
for start, end in zip(starts, exits):
    assert start["id"] == end["id"] and start["argv"] == end["argv"] and end["exit_code"] == 0
    assert start["argv"][0] == "/opt/homebrew/bin/opencode"
    assert (E / (start["id"] + ".stderr.log")).read_bytes() == b""
captures = sorted(E.glob("headless-*.json"))
assert len(captures) == 5
chain = []
event_types = {}
messages = {m["id"]: m for m in data["messages"]}
for capture in captures:
    c = load(capture.name)
    assert c["succeeded"] is True and c["truncated"] is False
    seat = c["seat"]
    aid = seat["agent_id"]
    sid = seats[aid]["native_session_id"]
    assert seat["run_id"] == v["session_id"] and seat["generation"] == 1
    # Startup capture reserves the pre-handshake seat; the parsed READY proof
    # supplies its new session. Submission captures carry the established ID.
    assert seat["native"]["native_session_id"] == (sid if c["message_id"] else "")
    events = [json.loads(line) for line in c["stdout"].splitlines()]
    assert [e["type"] for e in events] == ["step_start", "text", "step_finish"]
    mid = events[0]["part"]["messageID"]
    for e in events:
        assert e["sessionID"] == e["part"]["sessionID"] == sid
        assert e["part"]["messageID"] == mid
        event_types[e["type"]] = event_types.get(e["type"], 0) + 1
    assert events[-1]["part"]["reason"] == "stop"
    final = json.loads(events[1]["part"]["text"])
    matches = [x for x in exits if (E / (x["id"] + ".stdout.jsonl")).read_text() == c["stdout"]]
    assert len(matches) == 1
    native = matches[0]
    stdin = (E / (native["id"] + ".stdin.txt")).read_text()
    if not c["message_id"]:
        assert final == {"actor": {"runId": v["session_id"], "agentId": aid, "generation": 1},
                         "tool": "opencode", "status": "READY", "nonce": proofs[aid]["nonce"]}
        assert stdin.rstrip().endswith(json.dumps(final, separators=(",", ":")))
        assert "--session" not in native["argv"]
    else:
        message = messages[c["message_id"]]
        assert message["recipient"] == aid and message["run_id"] == v["session_id"]
        assert stdin.startswith(message["body"] + "\n")
        delivery = next(d for d in data["deliveries"] if d["message_id"] == c["message_id"])
        assert delivery["phase"] == "SENT" and delivery["attempt"] == 1 and delivery["target_generation"] == 1
        assert native["argv"][-2:] == ["--session", sid]
        replies = [m for m in messages.values() if m["reply_to"] == message["id"]]
        assert len(replies) == 1 and json.loads(replies[0]["body"]) == final
        assert replies[0]["sender"] == aid and replies[0]["sender_generation"] == 1
        intent = json.loads(message["body"])
        chain.append({"revision": intent["revision"], "agent": aid, "session_id": sid,
                      "dispatch_id": message["id"], "native_message_id": mid, "kind": final["kind"]})
        if final["kind"] == "handoff":
            assert v["nonce"] in final["handoff"]["summary"]
            assert all(v["nonce"] in s for s in final["handoff"]["evidence"])
        if final["kind"] == "approve":
            assert final["accepted"] == [True]
chain.sort(key=lambda x: x["revision"])
assert [(x["revision"], x["kind"]) for x in chain] == [(1, "blocked"), (3, "handoff"), (4, "approve")]
assert chain[0]["session_id"] == chain[1]["session_id"]
assert not Path(v["temp_root"]).exists()
assert all(v["cleanup"][k] for k in ("owned_processes_exited", "lock_removed", "socket_removed", "temporary_root_removed"))
for command in v["commands"]:
    if "pid" in command:
        try:
            os.kill(command["pid"], 0)
        except ProcessLookupError:
            pass
        else:
            raise AssertionError("owned coordinator PID still present")
print(json.dumps({"status": "PASS", "checks": "archive integrity, exact receipts, all-table replay equality, READY actor/nonce/session, native stdin/stdout correlation, durable SENT/reply binding, real second persistent delivery, no tools, source hashes, cleanup",
    "event_types": event_types, "captures": len(captures), "native_calls": len(exits),
    "native_exit_codes": [c["exit_code"] for c in exits], "table_counts": {t: len(r) for t, r in data.items()},
    "runs": data["runs"], "seats": data["seats"], "chain": chain,
    "final_cursor": original_resume["workflow_current"], "effective_permissions": "UNVERIFIED"}, indent=2))
