"""Offline evidence assertions; never invokes a native CLI."""
import json
import pathlib
import re

root = pathlib.Path(__file__).resolve().parent
window, workspace = "window:1000060005", "workspace:1000060011"
for tool, surface, nonce in [
    ("opencode", "surface:1000060034", "AF-S4-83a87513-opencode-4b7c62e9"),
    ("codex", "surface:1000060035", "AF-S4-83a87513-codex-d8351fa6"),
]:
    for suffix in ["send", "submit", "before", "after-presend", "after-final"]:
        data = json.loads((root / f"{tool}-{suffix}.json").read_text())
        assert data["exit_code"] == 0
        argv = data["argv"]
        for flag, value in [("--window", window), ("--workspace", workspace), ("--surface", surface)]:
            assert argv[argv.index(flag) + 1] == value, (tool, suffix, flag)
    final = json.loads((root / f"{tool}-after-final.json").read_text())["stdout"]
    marker = r"\s+" if tool == "opencode" else r"\s*•\s+"
    assert re.search(r"^" + marker + "ACK " + re.escape(nonce) + r"\s*$", final, re.M), tool
    sends = [json.loads(p.read_text()) for p in root.glob(f"{tool}-*.json")
             if p.name not in (f"{tool}-send-intent.json",)]
    assert sum("send" in d.get("argv", []) for d in sends) == 1
    assert sum("send-key" in d.get("argv", []) for d in sends) == 1
for label in ["make-check", "go-build", "go-tidy"]:
    assert json.loads((root / f"{label}.json").read_text())["exit_code"] == 0
print("PASS: two scoped one-shot submissions, two assistant ACKs, required checks")
