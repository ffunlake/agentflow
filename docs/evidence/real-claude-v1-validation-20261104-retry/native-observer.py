#!/usr/bin/env python3
"""Observe the installed Claude CLI without changing argv, input or environment."""
import json
import os
from pathlib import Path
import subprocess
import sys
import time
import uuid

out = Path(os.environ['AF_VALIDATION_EVIDENCE'])
call = uuid.uuid4().hex
args = ['/Users/funlake/.nvm/versions/node/v26.8.1/bin/claude', *sys.argv[1:]]
record = {'id': call, 'argv': args, 'cwd': os.getcwd(), 'started_at': time.time()}
def log(event, **fields):
    with (out / 'native-invocations.jsonl').open('a') as f:
        f.write(json.dumps({**record, 'event': event, **fields}) + '\n')
log('start')
body = sys.stdin.buffer.read()
(out / (call + '.stdin.txt')).write_bytes(body)
result = subprocess.run(args, input=body, capture_output=True)
(out / (call + '.stdout.jsonl')).write_bytes(result.stdout)
(out / (call + '.stderr.log')).write_bytes(result.stderr)
log('exit', exit_code=result.returncode, finished_at=time.time())
sys.stdout.buffer.write(result.stdout)
sys.stderr.buffer.write(result.stderr)
sys.exit(result.returncode)
