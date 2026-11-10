#!/usr/bin/env python3
"""Acceptance-only CLI observer. Adds only --tools ''; never changes auth/output."""
import json
import os
from pathlib import Path
import sys
import time

cli = "/Users/funlake/.nvm/versions/node/v26.8.1/bin/claude"
argv = [cli, *sys.argv[1:], "--tools", ""]
with (Path(os.environ["AF_CLAUDE_COMPAT_EVIDENCE"]) / "native-invocations.jsonl").open("a") as output:
    output.write(json.dumps({"timestamp": time.time(), "pid": os.getpid(), "cwd": os.getcwd(), "argv": argv}) + "\n")
os.execv(cli, argv)
