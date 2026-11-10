#!/usr/bin/env python3
"""Transparent observer: real installed OpenCode, unchanged argv/stdin/env."""
import json
import os
import pathlib
import subprocess
import sys
import time
import uuid

out = pathlib.Path(os.environ["AF_VALIDATION_EVIDENCE"])
call = uuid.uuid4().hex
args = ["/opt/homebrew/bin/opencode", *sys.argv[1:]]
record = {"id": call, "argv": args, "cwd": os.getcwd(), "started": time.time()}
with (out / "native-invocations.jsonl").open("a") as f:
    f.write(json.dumps({**record, "event": "start"}) + "\n")
body = sys.stdin.buffer.read()
(out / (call + ".stdin.txt")).write_bytes(body)
result = subprocess.run(args, input=body, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
(out / (call + ".stdout.jsonl")).write_bytes(result.stdout)
(out / (call + ".stderr.log")).write_bytes(result.stderr)
with (out / "native-invocations.jsonl").open("a") as f:
    f.write(json.dumps({**record, "event": "exit", "exit_code": result.returncode,
                        "finished": time.time()}) + "\n")
sys.stdout.buffer.write(result.stdout)
sys.stderr.buffer.write(result.stderr)
sys.exit(result.returncode)
