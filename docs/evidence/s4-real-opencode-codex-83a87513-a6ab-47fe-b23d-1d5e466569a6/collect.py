"""Bounded S4 evidence recorder. Only the user-authorized test surfaces are used.

Each send requires an explicit tool argument and an exclusive on-disk intent;
failed/unknown sends are never automatically replayed. Review preflight screens
for an empty idle input before invoking send. No login/config/session reset.
"""
import datetime
import json
import pathlib
import os
import subprocess
import sys

ROOT = pathlib.Path(__file__).resolve().parent
WINDOW = "window:1000060005"
WORKSPACE = "workspace:1000060011"
SURFACES = {"opencode": "surface:1000060034", "codex": "surface:1000060035"}
CMUX = "/Applications/cmux.app/Contents/Resources/bin/cmux"
CWD = "/Users/funlake/Projects/agentflow"


def record(name, args, binary=CMUX, env=None, timeout=20):
    argv = [binary] + args
    at = datetime.datetime.now(datetime.timezone.utc).isoformat()
    try:
        proc = subprocess.run(argv, cwd=CWD, capture_output=True, text=True, timeout=timeout, env=env)
        result = dict(argv=argv, cwd=CWD, at=at, exit_code=proc.returncode,
                      stdout=proc.stdout, stderr=proc.stderr)
    except subprocess.TimeoutExpired:
        result = dict(argv=argv, cwd=CWD, at=at, exit_code=None,
                      stdout="", stderr="TIMEOUT: outcome unknown; do not replay")
    with (ROOT / (name + ".json")).open("x") as f:
        json.dump(result, f, ensure_ascii=False, indent=2)
        f.write("\n")
    print(json.dumps(result, ensure_ascii=False, indent=2))
    if result["exit_code"] != 0:
        raise SystemExit(1)
    return result


def target(tool):
    return ["--window", WINDOW, "--workspace", WORKSPACE, "--surface", SURFACES[tool]]


action = sys.argv[1]
if action == "preflight":
    record("preflight-tree", ["--json", "--id-format", "both", "tree",
                              "--window", WINDOW, "--workspace", WORKSPACE])
    for tool in SURFACES:
        record(tool + "-before", ["read-screen"] + target(tool) + ["--lines", "45"])
        record(tool + "-resume-before", ["--json", "surface", "resume", "show"] + target(tool))
elif action == "native-cwd":
    record("native-processes", ["-p", "54924,55194", "-o", "pid=,ppid=,tty=,ucomm="], "/bin/ps")
    record("native-cwd", ["-a", "-p", "54924,55194", "-d", "cwd", "-Fn"], "/usr/sbin/lsof")
elif action == "send":
    tool = sys.argv[2]
    nonce = "AF-S4-83a87513-" + tool + "-" + ("4b7c62e9" if tool == "opencode" else "d8351fa6")
    message = ("AgentFlow S4 one-time connectivity check. Do not use tools, read files, "
               "or change anything. Reply with exactly: ACK " + nonce)
    # Write-ahead intent also prevents duplicate submissions if recording fails.
    with (ROOT / (tool + "-send-intent.json")).open("x") as f:
        json.dump(dict(nonce=nonce, message=message, target=target(tool)), f, indent=2)
    record(tool + "-send", ["send", "--paste"] + target(tool) + ["--", message])
    record(tool + "-submit", ["send-key"] + target(tool) + ["enter"])
elif action == "handoff":
    # Existing route ca2ec2ab2c563fe0577e53b7.json maps this caller to
    # logical worker 1 and main surface:1000060018. Native messaging uses
    # agent hooks/inbox, never send/send-key into a protected control terminal.
    payload = json.loads((ROOT / "agent_handoff.json").read_text())
    with (ROOT / "handoff-send-intent.json").open("x") as f:
        json.dump(dict(target="surface:1000060018", payload_id=payload["id"]), f)
    record("handoff-receipt", ["agent", "message", "surface:1000060018", "--from", "agentflow/资深研发",
                               "--thread", payload["session_id"], "--json", "--",
                               "[HANDOFF] " + json.dumps(payload, ensure_ascii=False)])
elif action == "verify":
    label = sys.argv[2]
    commands = {"make-check": ["make", "check"], "go-build": ["go", "build", "./..."],
                "go-tidy": ["go", "mod", "tidy", "-diff"]}
    argv = commands[label]
    env = dict(os.environ)
    env.update(GOTOOLCHAIN="local", GOCACHE=CWD+"/.cache/go-build", GOMODCACHE=CWD+"/.cache/go-mod",
               GOPATH=CWD+"/.cache/gopath", GOTMPDIR=CWD+"/.cache/tmp", TMPDIR=CWD+"/.cache/tmp",
               GOFLAGS="-mod=readonly", CGO_ENABLED="0")
    record(label, argv[1:], argv[0], env=env, timeout=120)
elif action == "observe":
    tool = sys.argv[2]
    suffix = sys.argv[3]
    record(tool + "-after-" + suffix, ["read-screen"] + target(tool) + ["--lines", "65"])
    record(tool + "-resume-after-" + suffix, ["--json", "surface", "resume", "show"] + target(tool))
else:
    raise SystemExit("preflight | send TOOL | observe TOOL LABEL")
