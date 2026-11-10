"""Offline acceptance audit; does not invoke a CLI/model or read user configuration."""
import hashlib
import json
from pathlib import Path

E = Path(__file__).resolve().parent
ROOT = E.parents[2]
A = E / "attempt-2"
state = json.loads((A / "validation.json").read_text())
verify = json.loads((E / "verification.json").read_text())
assert state["outcome"] == "PASS" and state["effective_permissions"] == "UNVERIFIED"
assert state["cwd_entries_after"] == [] and state["owned_processes_exited"] and state["lock_removed"]
assert all(value == "PASS" for value in state["checks"].values())
assert not any(state["auth_environment_presence_only"].values())
assert verify["active"] is None and verify["source_unchanged"]
assert [job["name"] for job in verify["results"]] == ["test", "build", "check", "tidy", "race"]
assert all(job["exit_code"] == 0 for job in verify["results"])
# Historical verification fingerprints describe that acceptance run, not later
# fixes. Report the comparison separately from the archived protocol evidence.
source_matches_current = all(hashlib.sha256((ROOT / path).read_bytes()).hexdigest() == digest for path, digest in verify["source_sha256"].items())
calls = [json.loads(line) for line in (A / "native-invocations.jsonl").read_text().splitlines()]
assert len(calls) == 5 and sum("--resume" in call["argv"] for call in calls) == 3
for call in calls:
    assert call["argv"][0] == "/Users/funlake/.nvm/versions/node/v26.8.1/bin/claude"
    assert call["argv"][-2:] == ["--tools", ""] and "--safe-mode" in call["argv"]
    assert "--bare" not in call["argv"] and "--dangerously-skip-permissions" not in call["argv"]
captures = []
sessions = {}
for item in state["captures"]:
    cap = json.loads((A / item["file"]).read_text())
    assert cap["succeeded"] and not cap["truncated"]
    events = [json.loads(line) for line in cap["stdout"].splitlines()]
    assert events[0]["type"] == "system" and events[0]["subtype"] == "init"
    assert events[0]["tools"] == [] and events[0]["mcp_servers"] == []
    assert events[0]["apiKeySource"] == "none"
    final = events[-1]
    assert final["type"] == "result" and final["subtype"] == "success" and final["is_error"] is False
    assert final["terminal_reason"] == "completed" and final["stop_reason"] == "end_turn"
    session = events[0]["session_id"]
    assert all(event["session_id"] == session for event in events)
    blocks = [block for event in events for block in event.get("message", {}).get("content", [])]
    assert not any(block["type"] == "tool_use" for block in blocks)
    assert not any(event["type"] == "tool_progress" for event in events)
    actor = cap["seat"]["agent_id"]
    if item["file"].startswith("headless-start-"):
        ready = json.loads(final["result"])
        assert ready["actor"] == {"runId": state["run_id"], "agentId": actor, "generation": 1}
        assert ready["status"] == "READY" and ready["tool"] == "claude" and ready["nonce"]
        sessions[actor] = session
    else:
        assert cap["seat"]["native"]["native_session_id"] == session
    captures.append({"file": item["file"], "agent": actor, "session": session, "result": final["result"]})
assert len(captures) == 5 and set(sessions) == {"main", "builder"}
assert len([call for call in calls if "--resume" in call["argv"] and call["argv"][call["argv"].index("--resume") + 1] == sessions["builder"]]) == 2
for label in ("completed-start-replay", "completed-resume-replay", "workflow-status", "terminal-drain"):
    result = json.loads((A / (label + ".stdout.log")).read_text())
    assert not result.get("executed") and not result.get("dispatch")
current = json.loads((A / "workflow-resume.stdout.log").read_text())["workflow_current"]
assert current["phase"] == "COMPLETED" and current["revision"] == 5
print(json.dumps({"outcome": "PASS", "scope": "historical explicit non-bare OAuth artifacts; not the safe default",
    "historical_source_matches_current": source_matches_current, "historical_db_before_after_archived": False,
    "effective_permissions": "UNVERIFIED", "native_calls": len(calls),
    "sessions": sessions, "workflow_current": current, "required_checks": verify["results"], "captures": captures}, indent=2))
