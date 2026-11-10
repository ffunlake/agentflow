#!/usr/bin/env python3
"""Recompute archived fixture DB/call/config hashes offline. No models or auth."""
import hashlib
import json
from pathlib import Path
import sys

E = Path(__file__).resolve().parent
ROOT = E.parents[2]

def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()

def load(path):
    return json.loads(path.read_text())

def check():
    verify = load(E / "verification.json")
    assert verify["active"] is None and verify["source_unchanged"]
    assert verify["real_models"] == "NOT_RUN"
    assert verify["effective_permissions"] == verify["crash_recovery"] == "UNVERIFIED"
    assert [job["name"] for job in verify["results"]] == ["fixtures", "focused", "test", "build", "check", "tidy", "race"]
    assert all(job["exit_code"] == 0 for job in verify["results"])
    assert all(digest(ROOT / path) == sha for path, sha in verify["source_sha256"].items())
    artifacts = {}
    modes = ["success", "blocked-resume", "bare", "oauth", "oauth-blocked-resume"]
    for mode in modes:
        directory = E / "fixtures" / mode
        safe = mode.startswith("oauth")
        expected_args = ["--safe-mode"] if safe else (["--bare"] if mode == "bare" else [])
        source = load(directory / "source-at-start.json")
        changed = load(directory / "source-after-start.json")
        frozen = load(directory / "config.resolved.json")["config"]
        for agent in ("main", "builder"):
            assert source["agents"][agent].get("args", []) == expected_args
            assert frozen["agents"][agent]["args"] == expected_args
            assert changed["agents"][agent].get("args", []) == ([] if safe else ["--safe-mode"])
        for phase in (["paused", "completed"] if "blocked-resume" in mode else ["completed"]):
            labels = [phase + suffix for suffix in ("-before-reopen", "-after-reopen", "-after-replay")]
            dbpaths = [directory / (label + ".db.json") for label in labels]
            callpaths = [directory / (label + ".calls.jsonl") for label in labels]
            assert len({digest(path) for path in dbpaths}) == 1
            assert len({digest(path) for path in callpaths}) == 1
            db = load(dbpaths[0])
            assert db["runs"][0]["config_digest"] == frozen["digest"]
            assert json.loads(db["runs"][0]["config_snapshot"]) == frozen
            assert {seat["generation"] for seat in db["seats"]} == {1}
            calls = [json.loads(line) for line in callpaths[0].read_text().splitlines()]
            assert len(calls) == (3 if phase == "paused" else (5 if "blocked-resume" in mode else 4))
            for i, argv in enumerate(calls):
                assert argv[:2] == ["--print", "--safe-mode" if safe else "--bare"]
                assert argv.count("--safe-mode") == int(safe) and argv.count("--bare") == int(not safe)
                assert "--dangerously-skip-permissions" not in argv
                assert ("--resume" in argv) == (i >= 2)
                if i >= 2:
                    expected = "00000000-0000-4000-8000-000000000001"
                    if phase == "completed" and i == len(calls) - 1:
                        expected = "00000000-0000-4000-8000-000000000002"
                    assert argv[-2:] == ["--resume", expected]
            artifacts[mode + "/" + phase] = {
                "db_sha256": digest(dbpaths[0]), "calls_sha256": digest(callpaths[0]),
                "native_fixture_calls": len(calls), "equal_before_after_reopen_replay": True,
                "tables": {table: len(rows) for table, rows in db.items()},
            }
    result = {"outcome": "PASS", "scope": "offline executable fixtures; strict bare default and explicit non-bare opt-in",
              "real_models": "NOT_RUN", "effective_permissions": "UNVERIFIED", "full_permissions": "UNVERIFIED",
              "crash_recovery": "UNVERIFIED", "artifacts": artifacts,
              "artifact_sha256": {str(path.relative_to(E)): digest(path)
                                  for path in sorted((E / "fixtures").rglob("*")) if path.is_file()}}
    if sys.argv[1:] == ["--record"]:
        with (E / "audit.json").open("x") as output:
            json.dump(result, output, indent=2)
            output.write("\n")
    else:
        assert not sys.argv[1:]
        assert load(E / "audit.json") == result
    print(json.dumps(result, indent=2))

check()
