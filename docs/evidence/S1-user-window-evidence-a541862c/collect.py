"""Read-only observation of two existing TUIs; writes new evidence exclusively here.

No provider APIs, terminal input, process signals, or Go commands are used.
Run collect once; run verify after the two authorized summary documents are edited.
"""
import base64
import datetime
import hashlib
import json
from pathlib import Path
import subprocess
import sys

HERE = Path(__file__).resolve().parent
ROOT = HERE.parents[2]
DOCS = ROOT / "docs"
CMUX = "/Applications/cmux.app/Contents/Resources/bin/cmux"
WINDOW = "73FEB4F4-BE2B-4F4D-9B48-E872BBC51EA3"
WORKSPACE = "3AB7D16C-B09D-44AE-8CD5-5B6225388D45"
CWD = "/Users/funlake/Projects/okra-wiki"
TARGETS = {
    "opencode": ("5265DA42-9A45-4C71-BABE-155E458FCF33", "ttys003", 23097,
                 "OPENCODE_ACK_7ba139c0-0133-47da-b99d-e7549a22f735"),
    "codex": ("94EBBAAE-5EA1-4352-B183-580E24F0F23C", "ttys004", 23477,
              "CODEX_ACK_9196eeee-17ab-40ac-91ee-8f558336b201"),
}
ALLOWED = {"docs/evidence/S1-result.md", "docs/evidence/S1-capability-matrix.md"}


def now():
    return datetime.datetime.now(datetime.timezone.utc).isoformat()


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def save(name, data):
    # Exclusive creation prevents this recorder from overwriting prior evidence.
    with (HERE / name).open("x") as output:
        json.dump(data, output, ensure_ascii=False, indent=2)
        output.write("\n")


def run(name, argv):
    start = now()
    result = subprocess.run(argv, capture_output=True, timeout=30)
    record = {"started_at": start, "finished_at": now(), "argv": argv,
              "cwd": str(ROOT), "exit_code": result.returncode,
              "stdout": result.stdout.decode("utf-8", errors="replace"),
              "stderr": result.stderr.decode("utf-8", errors="replace"),
              "stdout_base64": base64.b64encode(result.stdout).decode(),
              "stderr_base64": base64.b64encode(result.stderr).decode()}
    save(name + ".json", record)
    if result.returncode:
        raise RuntimeError(f"Read-only command failed: {name}; see raw record")
    return record["stdout"]


def cmux(name, command, extra=()):
    return json.loads(run(name, [CMUX, "--json", "--id-format", "uuids", command,
                                "--window", WINDOW, "--workspace", WORKSPACE, *extra]))


def inventory():
    return {str(path.relative_to(ROOT)): digest(path)
            for path in sorted(DOCS.rglob("*"))
            if path.is_file() and not path.is_symlink()
            and "scratch" not in path.relative_to(DOCS).parts
            and not path.is_relative_to(HERE)}


def collect():
    save("execution-policy.json", {
        "session_id": "a541862c-c620-47e8-95c9-e2186e359c9c",
        "task_id": "s1-user-window-evidence", "mode": "ralph-loop",
        "window": WINDOW, "workspace": WORKSPACE, "captured_at": now(),
        "scope": "Existing user-completed ACKs only; no new model request",
        "allowed_observation": ["cmux tree", "cmux read-screen", "ps", "lsof cwd"],
        "terminal_input_commands": [], "go_commands": [],
        "native_transcripts_or_credential_files_read": False,
        "protected_hash_scope": "docs excluding scratch, symlinks and this new directory",
        "process_capture": "TTY metadata without arguments; native target PID metadata only",
        "fresh_session": "READY already sent; user target sessions must not be reset",
    })
    save("inventory-before.json", {"at": now(), "files": inventory()})
    tree = cmux("target-tree", "tree")
    window = next(w for w in tree["windows"] if w["id"] == WINDOW)
    workspace = next(w for w in window["workspaces"] if w["id"] == WORKSPACE)
    surfaces = {s["id"]: s for p in workspace["panes"] for s in p["surfaces"]}
    run("tty-processes", ["/bin/ps", "-ww", "-t", "ttys003,ttys004", "-o",
                          "pid=,ppid=,pgid=,tty=,stat=,comm="])
    bindings = {}
    assertions = []
    for provider, (surface, tty, pid, nonce) in TARGETS.items():
        captures = {}
        for suffix, extra in [("screen", []), ("scrollback", ["--scrollback", "--lines", "1000"])]:
            capture = cmux(provider + "-" + suffix, "read-screen", ["--surface", surface, *extra])
            assert (capture["window_id"], capture["workspace_id"], capture["surface_id"]) == (WINDOW, WORKSPACE, surface)
            assert base64.b64decode(capture["base64"]).decode() == capture["text"]
            captures[suffix] = capture["text"]
            save(provider + "-" + suffix + "-decoded.json", {
                "window": WINDOW, "workspace": WORKSPACE, "surface": surface,
                "lines": [{"line": i, "text": line} for i, line in enumerate(capture["text"].splitlines(), 1)]})
        process = run(provider + "-process", ["/bin/ps", "-ww", "-p", str(pid), "-o",
                                             "pid=,ppid=,pgid=,tty=,stat=,comm="])
        fields = process.strip().split(None, 5)
        assert fields[0] == str(pid) and fields[3] == tty
        assert Path(fields[5]).name == provider
        cwd = run(provider + "-cwd", ["/usr/sbin/lsof", "-a", "-p", str(pid), "-d", "cwd", "-Fn"])
        assert "p" + str(pid) in cwd.splitlines() and "n" + CWD in cwd.splitlines()
        assert surfaces[surface]["tty"] == tty and surfaces[surface]["selected_in_pane"]
        assert window["visible"]
        screen_lines = captures["scrollback"].splitlines()
        # ACK must be a standalone assistant line, separate from the quoted user request.
        assistant_lines = [i for i, line in enumerate(screen_lines, 1)
                           if line.strip() in (nonce, "• " + nonce)]
        assert len(assistant_lines) == 1
        assert sum(nonce in line for line in screen_lines) == 2
        assert CWD in captures["scrollback"]
        if provider == "codex":
            assert "• Ran pwd" in screen_lines and "  └ " + CWD in screen_lines
            assert "• " + CWD in screen_lines
        bindings[provider] = {
            "window": WINDOW, "workspace": WORKSPACE, "surface": surface,
            "pane": surfaces[surface]["pane_id"], "tty": tty, "pid": pid,
            "ppid": int(fields[1]), "process_name": fields[5], "cwd": CWD,
            "nonce": nonce, "assistant_ack_line": assistant_lines[0],
            "native_session_id": None, "native_session_id_status": "UNVERIFIED",
            "model_label_only": "GPT-6.1 Sol / OpenAI" if provider == "opencode" else "GPT-5.6-Luna",
            "evidence": ["target-tree.json", "tty-processes.json", provider + "-process.json",
                         provider + "-cwd.json", provider + "-scrollback.json",
                         provider + "-scrollback-decoded.json"],
            "status": "PASS", "scope": "Existing visible assistant ACK + OS cwd binding",
        }
        assertions.append({"id": provider + ".user_window_existing_model_ack", "status": "PASS",
                           "binding": "bindings.json#" + provider,
                           "assistant_line": assistant_lines[0]})
    save("bindings.json", bindings)
    save("evidence-validation.json", {"at": now(), "status": "PASS", "assertions": assertions,
        "boundaries": ["Visible assistant result evidence only; no provider-side inference audit",
                       "No native session/thread ID proven", "No new model request sent",
                       "Permission selection requested is not proof of effective full access"]})
    save("runtime-matrix.json", {"at": now(), "status": "PARTIAL", "bindings": bindings,
        "new_observation_passes": [a["id"] for a in assertions],
        "historical_19_controlled_passes": "Preserved in prior evidence; not recounted or retested",
        "new_model_requests": 0,
        "user_window_unverified_both_providers": ["reset", "reset READY", "role reinjection",
            "native session ID", "identity refresh", "effective full access", "recovery", "busy delivery",
            "draft protection", "different cwd isolation", "hook/queue delivery", "automatic wakeup"],
        "opencode_old_isolated_provider_dependency": "Historical environment only; does not block the observed user-window ACK",
        "codex_historical_passes": "Prior environment and native thread only; not inherited by this window",
        "go_checks": "NOT_RUN_AS_REQUESTED"})
    print(json.dumps({"status": "PASS", "bindings": bindings}, ensure_ascii=False, indent=2))


def verify():
    before = json.loads((HERE / "inventory-before.json").read_text())["files"]
    after = inventory()
    changed = sorted(p for p in before if p in after and before[p] != after[p])
    deleted = sorted(set(before) - set(after))
    added = sorted(set(after) - set(before))
    assert set(changed) == ALLOWED and not deleted and not added, (changed, deleted, added)
    save("protected-documents-verification.json", {"at": now(), "status": "PASS",
        "existing_files_checked": len(before), "changed": changed, "deleted": deleted, "added_outside_new_evidence": added,
        "unchanged_files": len(before) - len(changed), "files_after": after})
    manifest = {str(p.relative_to(ROOT)): digest(p) for p in sorted(HERE.iterdir()) if p.is_file()}
    manifest.update({p: digest(ROOT / p) for p in sorted(ALLOWED)})
    save("sha256-manifest.json", {"at": now(), "algorithm": "SHA-256", "files": manifest,
         "excluded_later_receipts": ["hash-verification.json", "handoff.json", "handoff-sha256.json"],
         "self_excluded": "sha256-manifest.json"})
    assert all(digest(ROOT / path) == expected for path, expected in manifest.items())
    save("hash-verification.json", {"at": now(), "status": "PASS", "files_checked": len(manifest),
         "manifest": "sha256-manifest.json", "manifest_sha256": digest(HERE / "sha256-manifest.json")})
    print(json.dumps({"status": "PASS", "files_hashed": len(manifest), "protected_documents": len(before),
                      "changed": changed}, ensure_ascii=False))


def handoff():
    verification = json.loads((HERE / "hash-verification.json").read_text())
    assert verification["status"] == "PASS"
    manifest = json.loads((HERE / "sha256-manifest.json").read_text())["files"]
    assert all(digest(ROOT / path) == expected for path, expected in manifest.items())
    result = run("handoff", [
        "/Users/funlake/.codex/skills/okra-agent-init/scripts/agent-handoff.sh", "send",
        "--window", "1", "--session-id", "a541862c-c620-47e8-95c9-e2186e359c9c",
        "--status", "DONE", "--summary",
        "仅只读整理用户新窗口已有验证：OpenCode/Codex assistant nonce ACK 均 PASS，绑定指定 window/workspace/surface/TTY/PID/cwd；Codex 已有 pwd 结果一致。完整 S1 仍 PARTIAL；两会话 reset/full/recovery/busy/native ID 等 UNVERIFIED。",
        "--evidence", str(HERE) + "/README.md; bindings.json; evidence-validation.json; "
        "protected-documents-verification.json; hash-verification.json; 两份 S1 总文档已更新；无新 prompt/reset/退出/kill，无 Go 检查。",
        "--next", "本轮已完成；保持用户两会话现状，等待下一轮明确范围；不进入 S2。",
    ])
    assert "[DONE] handoff sent" in result
    save("handoff-sha256.json", {"at": now(), "algorithm": "SHA-256", "status": "PASS",
        "files": {name: digest(HERE / name) for name in
                  ["handoff.json", "hash-verification.json", "sha256-manifest.json"]},
        "self_excluded": "handoff-sha256.json"})
    print(result.strip())


if __name__ == "__main__":
    {"collect": collect, "verify": verify, "handoff": handoff}[sys.argv[1]]()
