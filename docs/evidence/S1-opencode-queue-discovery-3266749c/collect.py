#!/usr/bin/env python3
"""Bounded read-only discovery; writes only this evidence directory.

Never opens auth/config contents, process environments, generic logs, or other
agents' transcripts. No message, TUI input, network request, or runtime launch.
The only OpenCode invocations are the installed binary's help paths.
"""
import concurrent.futures
import datetime
import hashlib
import json
import os
from pathlib import Path
import shutil
import sqlite3
import subprocess
import sys

OUT = Path(__file__).resolve().parent
ROOT = OUT.parents[2]
WINDOW = "73FEB4F4-BE2B-4F4D-9B48-E872BBC51EA3"
WORKSPACE = "3AB7D16C-B09D-44AE-8CD5-5B6225388D45"
SURFACE = "5265DA42-9A45-4C71-BABE-155E458FCF33"
NATIVE = "ses_e64c2fa39ffeI5sYf6iCWDKYpv"
CWD = "/Users/funlake/Projects/okra-wiki"
BIN = "/opt/homebrew/Cellar/opencode/1.19.5/bin/opencode"
TARGET = ["--window", WINDOW, "--workspace", WORKSPACE]
PHASE = sys.argv[1] if len(sys.argv) == 2 else "initial"
assert PHASE in {"initial", "final"}


def stamp():
    return datetime.datetime.now(datetime.timezone.utc).isoformat()


def save(name, value):
    # Exclusive creation keeps previous evidence intact.
    with (OUT / name).open("x") as stream:
        json.dump(value, stream, ensure_ascii=False, indent=2)
        stream.write("\n")


def run(argv):
    start = stamp()
    try:
        p = subprocess.run(argv, cwd=ROOT, capture_output=True, text=True, timeout=12)
        return dict(argv=argv, cwd=str(ROOT), started_at=start, ended_at=stamp(),
                    exit_code=p.returncode, stdout=p.stdout, stderr=p.stderr)
    except subprocess.TimeoutExpired:
        return dict(argv=argv, cwd=str(ROOT), started_at=start, ended_at=stamp(),
                    exit_code=None, error="bounded discovery timeout; no retry")


def native_snapshot():
    db = sqlite3.connect("file:/Users/funlake/.local/share/opencode/opencode.db?mode=ro", uri=True)
    db.execute("PRAGMA query_only=ON")
    db.execute("BEGIN")
    session = db.execute("SELECT id,directory,version,time_created,time_updated FROM session WHERE id=?", (NATIVE,)).fetchone()
    messages = db.execute("SELECT id,json_extract(data,'$.role'),time_created,time_updated,length(data) FROM message WHERE session_id=? ORDER BY id", (NATIVE,)).fetchall()
    # Project only metadata, never tool input/output or injected context.
    parts = db.execute("SELECT id,message_id,json_extract(data,'$.type'),time_created,time_updated,length(data) FROM part WHERE session_id=? ORDER BY id", (NATIVE,)).fetchall()
    selected = db.execute("""SELECT m.id,p.id,json_extract(m.data,'$.role'),json_extract(p.data,'$.text')
        FROM part p JOIN message m ON m.id=p.message_id
        WHERE p.session_id=? AND json_extract(m.data,'$.role')='assistant'
        AND json_extract(p.data,'$.type')='text'
        AND (json_extract(p.data,'$.text')=? OR json_extract(p.data,'$.text')=?) ORDER BY p.id""",
        (NATIVE, "OPENCODE_ACK_7ba139c0-0133-47da-b99d-e7549a22f735",
         "RECOVERY_ACK request=S1-OC-recover-dc2eb54e-b9d405f3 cwd=" + CWD)).fetchall()
    db.rollback()
    db.close()
    return dict(captured_at=stamp(), database_mode="ro; PRAGMA query_only=ON; read transaction",
                native_session=session, messages_metadata=messages, parts_metadata=parts,
                selected_existing_assistant_text=selected,
                scope="Existing assistant excerpts are identity evidence, not a new external ACK")


def metadata():
    paths = [Path("/Users/funlake/.config/opencode/opencode.json"),
             Path("/Users/funlake/.config/opencode/tui.json"),
             Path(CWD) / ".opencode", Path(CWD) / "opencode.json", Path(CWD) / "opencode.jsonc"]
    configs = []
    for p in paths:
        value = dict(path=str(p), exists=p.exists(), content_read=False)
        if p.exists():
            s = p.stat()
            value.update(size=s.st_size, mtime_ns=s.st_mtime_ns, mode=oct(s.st_mode & 0o777))
            if p.is_dir():
                value["entry_names"] = sorted(x.name for x in p.iterdir())
        configs.append(value)
    plugins = Path("/Users/funlake/.config/opencode/plugins")
    return dict(captured_at=stamp(), config_metadata=configs,
                global_plugin_entries=sorted(p.name for p in plugins.iterdir()),
                cmux_expected_plugin_exists={name:(plugins / name).exists() for name in ("cmux-session.js", "cmux-feed.js", "cmux")},
                jev_judge_available=shutil.which("jev-judge") is not None,
                typesafe_api_key_set=bool(os.getenv("TYPESAFE_API_KEY")),
                credential_files_opened=[], process_environments_read=False)


commands = {
    "target-tree": ["cmux", "--id-format", "uuids", "--json", "tree", *TARGET],
    "target-screen": ["cmux", "--id-format", "uuids", "--json", "read-screen", *TARGET, "--surface", SURFACE, "--lines", "100"],
    "target-resume-binding": ["cmux", "--id-format", "uuids", "--json", "surface", "resume", "show", *TARGET, "--surface", SURFACE],
    "cmux-receiving": ["cmux", "agent", "messages", "status", SURFACE, "--json"],
    "target-process": ["ps", "-p", "23097,21983,23095", "-o", "pid=,ppid=,tty=,lstart=,args="],
    "target-cwd-tty-binary": ["lsof", "-nP", "-a", "-p", "23097", "-d", "cwd,txt,0,1,2"],
    "target-network": ["lsof", "-nP", "-a", "-p", "23097", "-i"],
    "target-unix": ["lsof", "-nP", "-a", "-p", "23097", "-U"],
    "loopback-peer-listener": ["lsof", "-nP", "-iTCP:55367", "-sTCP:LISTEN"],
}
if PHASE == "initial":
    commands.update({
        "opencode-help": [BIN, "--help"],
        "opencode-run-help": [BIN, "run", "--help"],
        "opencode-attach-help": [BIN, "attach", "--help"],
        "opencode-serve-help": [BIN, "serve", "--help"],
        "opencode-session-help": [BIN, "session", "--help"],
        "opencode-acp-help": [BIN, "acp", "--help"],
        "cmux-message-help": ["cmux", "agent", "message", "--help"],
        "cmux-hooks-help": ["cmux", "hooks", "opencode", "--help"],
    })
with concurrent.futures.ThreadPoolExecutor(max_workers=5) as executor:
    futures = {executor.submit(run, argv): name for name, argv in commands.items()}
    for future in concurrent.futures.as_completed(futures):
        save(f"{PHASE}-{futures[future]}.json", future.result())
save(f"{PHASE}-native-projection.json", native_snapshot())
save(f"{PHASE}-metadata.json", metadata())

# Process ancestry is read as command names only; unrelated argv never enter output.
process_result = run(["ps", "-axo", "pid=,ppid=,tty=,comm="])
rows = [line.split(None, 3) for line in process_result["stdout"].splitlines() if line.strip()]
owned = {23097}
descendants = []
while True:
    added = [r for r in rows if int(r[1]) in owned and int(r[0]) not in owned]
    if not added:
        break
    descendants.extend(added)
    owned.update(int(r[0]) for r in added)
process_result["stdout"] = None
process_result["target_descendants_only"] = descendants
process_result["projection"] = "PID/PPID/TTY/comm only; all unrelated processes discarded"
save(f"{PHASE}-children.json", process_result)

if PHASE == "initial":
    # Installed SDK declarations and app integration code are documentation,
    # not user configuration. Preserve only bounded relevant snippets and hashes.
    sources = [
        (Path("/Users/funlake/.config/opencode/node_modules/@opencode-ai/sdk/dist/gen/sdk.gen.js"),
         ("promptAsync(", "url: \"/session/{id}/prompt_async\"", "url: \"/session/{id}/message\"")),
        (Path("/Applications/cmux.app/Contents/Resources/opencode-plugin.js"),
         ("pushTelemetry", "session.inbox.enqueued", "export default", "isTUIProcessServerContext")),
        (Path("/Users/funlake/.config/opencode/plugins/orca-opencode-status.js"),
         ("async function post(", "const url =", "client.session.get", "ctx.session.hook(\"prompt\"")),
    ]
    docs = []
    for path, needles in sources:
        content = path.read_bytes()
        lines = content.decode().splitlines()
        selected = set()
        for index, line in enumerate(lines):
            if any(needle in line for needle in needles):
                selected.update(range(max(0, index - 2), min(len(lines), index + 5)))
        docs.append(dict(path=str(path), sha256=hashlib.sha256(content).hexdigest(),
                         excerpts=[dict(line=i+1, text=lines[i]) for i in sorted(selected)]))
    save("local-documentation.json", docs)
    protected = {}
    for folder in ("cmd", "internal", "examples", "tests"):
        for p in (ROOT / folder).rglob("*"):
            if p.is_file():
                protected[str(p.relative_to(ROOT))] = hashlib.sha256(p.read_bytes()).hexdigest()
    for name in ("go.mod", "go.sum", "Makefile", "README.md"):
        p = ROOT / name
        protected[name] = hashlib.sha256(p.read_bytes()).hexdigest()
    save("protected-project-baseline.json", protected)
else:
    baseline = json.loads((OUT / "protected-project-baseline.json").read_text())
    changed = [name for name, digest in baseline.items()
               if not (ROOT / name).is_file() or hashlib.sha256((ROOT / name).read_bytes()).hexdigest() != digest]
    initial = json.loads((OUT / "initial-native-projection.json").read_text())
    final = json.loads((OUT / "final-native-projection.json").read_text())
    initial.pop("captured_at")
    final.pop("captured_at")
    save("protection-comparison.json", dict(project_files_compared=len(baseline), changed_project_files=changed,
         native_metadata_and_selected_text_unchanged=initial == final,
         screen_text_unchanged=json.loads((OUT / "initial-target-screen.json").read_text())["stdout"] == json.loads((OUT / "final-target-screen.json").read_text())["stdout"],
         limitation="Baseline begins after preliminary read-only discovery; no full native content hash or wiki content snapshot"))
print(json.dumps(dict(phase=PHASE, evidence=str(OUT), command_count=len(commands), completed_at=stamp())))
