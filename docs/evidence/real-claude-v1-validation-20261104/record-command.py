#!/usr/bin/env python3
"""Archive the read-only check or the user-authorized final handoff command."""
import json
from pathlib import Path
import subprocess
import sys
import time

E = Path(__file__).resolve().parent
ROOT = E.parents[2]
label = sys.argv[1]
if label == 'consistency':
    args = [sys.executable, str(E / 'check-evidence.py')]
elif label == 'handoff':
    verified = json.loads((E / 'consistency.stdout.log').read_text())
    assert verified['status'] == 'PASS', 'Read-only consistency must pass before handoff'
    args = ['/Users/funlake/.codex/skills/okra-agent-init/scripts/agent-handoff.sh',
        'send', '--window', '1', '--session-id', 'real-claude-v1-validation-20261104-01',
        '--status', 'BLOCKED_CONFIRM',
        '--summary', 'Claude 2.1.318 real acceptance BLOCKED_CONFIRM: bare adapter requires existing environment auth; no ANTHROPIC_API_KEY or enabled provider in this process. Zero model calls. READY/execute/review/resume/reopen/permissions UNVERIFIED. Isolated dry-run and source/cleanup consistency PASS.',
        '--evidence', str(ROOT / 'docs/evidence/real-claude-v1-validation-20261104.md') + '; ' + str(E / 'validation.json') + '; ' + str(E / 'consistency.stdout.log'),
        '--next', 'Main review authentication preflight evidence. Resume real acceptance only when existing supported bare-mode auth is available; no login, credential copying, config changes or authentication bypass.']
else:
    raise SystemExit('Expected consistency or handoff')
record_path = E / (label + '-command.json')
assert not record_path.exists(), 'Refuse to overwrite recorded command'
record = {'argv': args, 'cwd': str(ROOT), 'started_at': time.time()}
record_path.write_text(json.dumps(record, indent=2) + '\n')
p = subprocess.run(args, cwd=ROOT, capture_output=True, timeout=60)
(E / (label + '.stdout.log')).write_bytes(p.stdout)
(E / (label + '.stderr.log')).write_bytes(p.stderr)
record.update(exit_code=p.returncode, finished_at=time.time())
record_path.write_text(json.dumps(record, indent=2) + '\n')
sys.stdout.buffer.write(p.stdout)
sys.stderr.buffer.write(p.stderr)
raise SystemExit(p.returncode)
