#!/usr/bin/env python3
"""Check recorded evidence consistency; never queries agents or runs Go."""
import ast
import datetime
import hashlib
import json
from pathlib import Path
import re

HERE = Path(__file__).resolve().parent
ROOT = HERE.parents[2]


def read(name):
    return json.loads((HERE / name).read_text())


def result(name):
    return json.loads(read(name)["stdout"])


matrix = read("runtime-matrix.json")
checks = {}
for phase in ("initial", "final"):
    screen = result(f"{phase}-target-screen.json")
    checks[f"{phase}_screen_target"] = all(screen[k] == matrix[k] for k in ("surface_id", "window_id", "workspace_id"))
    tree = result(f"{phase}-target-tree.json")
    windows = tree["windows"]
    checks[f"{phase}_visible_target"] = any(
        w["id"] == matrix["window_id"] and w["visible"] and
        any(ws["id"] == matrix["workspace_id"] and any(
            s["id"] == matrix["surface_id"] and s.get("tty") == matrix["tty"]
            for pane in ws["panes"] for s in pane["surfaces"])
            for ws in w["workspaces"])
        for w in windows)
    native = read(f"{phase}-native-projection.json")
    checks[f"{phase}_native_session_cwd"] = native["native_session"][:2] == [matrix["native_session_id"], matrix["cwd"]]
    existing = native["selected_existing_assistant_text"]
    checks[f"{phase}_old_assistant_text_visible"] = len(existing) == 2 and all(
        r[2] == "assistant" and r[3].replace(" ", "") in re.sub(r"\s+", "", screen["text"])
        for r in existing)
    network = read(f"{phase}-target-network.json")
    checks[f"{phase}_no_listener_observed"] = network["exit_code"] == 0 and "LISTEN" not in network["stdout"]
    checks[f"{phase}_no_children"] = read(f"{phase}-children.json")["target_descendants_only"] == []
    binding = result(f"{phase}-target-resume-binding.json")
    checks[f"{phase}_no_cmux_native_binding"] = binding["resume_binding"] is None and binding["restore_record"] is None
    process = read(f"{phase}-target-process.json")
    checks[f"{phase}_target_pid_argv_tty"] = any(
        line.split()[0:3] == ["23097", "21983", "ttys003"] and line.endswith("opencode --auto")
        for line in process["stdout"].splitlines())
    cwd = read(f"{phase}-target-cwd-tty-binary.json")["stdout"]
    checks[f"{phase}_os_cwd_tty"] = matrix["cwd"] in cwd and "/dev/ttys003" in cwd

records = [read(p.name) for p in HERE.glob("initial-*.json") if "argv" in read(p.name)]
records += [read(p.name) for p in HERE.glob("final-*.json") if "argv" in read(p.name)]
checks["recorded_commands_exit_zero"] = all(r["exit_code"] == 0 for r in records)
protection = read("protection-comparison.json")
checks["protected_project_hashes_unchanged"] = protection["changed_project_files"] == []
checks["native_projection_unchanged"] = protection["native_metadata_and_selected_text_unchanged"]
checks["screen_unchanged"] = protection["screen_text_unchanged"]
checks["no_runtime_pass_or_nonce_claim"] = matrix["new_runtime_passes"] == [] and matrix["nonce"] is None and matrix["native_assistant_ack"] is None
checks["no_message_or_model_request_claim"] = matrix["external_messages_sent"] == matrix["new_model_requests"] == 0
for p in HERE.glob("*.py"):
    ast.parse(p.read_text())
checks["evidence_scripts_parse"] = True
native = read("final-native-projection.json")
report = dict(checked_at=datetime.datetime.now(datetime.timezone.utc).isoformat(),
              scope="Recorded evidence consistency; not functional queue acceptance or Go checks",
              status="PASS" if all(checks.values()) else "FAIL", checks=checks,
              native_message_count=len(native["messages_metadata"]),
              native_part_count=len(native["parts_metadata"]),
              new_runtime_passes=[])
with (HERE / "evidence-validation.json").open("x") as f:
    json.dump(report, f, indent=2)
    f.write("\n")
paths = sorted(p for p in HERE.iterdir() if p.is_file())
paths.extend(ROOT / "docs/evidence" / name for name in ("S1-result.md", "S1-capability-matrix.md"))
manifest = {str(p.relative_to(ROOT)): hashlib.sha256(p.read_bytes()).hexdigest() for p in paths}
with (HERE / "sha256-manifest.json").open("x") as f:
    json.dump(dict(scope="Evidence and authorized summary documents before handoff; excludes this manifest and later handoff.json", files=manifest), f, indent=2)
    f.write("\n")
print(json.dumps(report, indent=2))
raise SystemExit(0 if all(checks.values()) else 1)
