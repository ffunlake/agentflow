#!/usr/bin/env python3
"""Verify the archive and send the explicitly requested collaboration handoff."""
import json
from pathlib import Path
import shutil
import subprocess
import sys
import time

E = Path(__file__).resolve().parent
ROOT = E.parents[2]
def run(label, args, stdout_name):
    record = {'argv': args, 'cwd': str(ROOT), 'started_at': time.time()}
    p = subprocess.run(args, cwd=ROOT, capture_output=True, timeout=45)
    (E / stdout_name).write_bytes(p.stdout)
    (E / (label + '.stderr.log')).write_bytes(p.stderr)
    record.update(exit_code=p.returncode, finished_at=time.time())
    (E / (label + '-command.json')).write_text(json.dumps(record, indent=2) + '\n')
    sys.stdout.buffer.write(p.stdout)
    sys.stderr.buffer.write(p.stderr)
    return p

assert not (E / 'handoff-command.json').exists(), 'Do not send duplicate handoff'
for name in ('consistency.stdout.json', 'consistency.stderr.log', 'consistency-command.json'):
    path = E / name
    archived = E / name.replace('consistency', 'consistency-initial', 1)
    if path.exists() and not archived.exists():
        shutil.copy2(path, archived)
result = run('consistency', [sys.executable, str(E / 'check-evidence.py')], 'consistency.stdout.json')
assert result.returncode == 0, 'Archive check failed; handoff not sent'
args = ['/Users/funlake/.codex/skills/okra-agent-init/scripts/agent-handoff.sh', 'send',
    '--window', '1', '--session-id', 'real-claude-v1-validation-20261104-02',
    '--status', 'BLOCKED_CONFIRM',
    '--summary', '真实 Claude 2.1.318 headless retry BLOCKED_CONFIRM：现有 adapter --bare 实际返回 apiKeySource=none / authentication_failed / Not logged in；native 与 up 均 exit 1。仅失败 startup 一次，未产生 READY；execute/review/resume/reopen/权限 UNVERIFIED。隔离 cwd 空、源码未改、自有进程与临时目录已清理，归档一致性核对通过。',
    '--evidence', 'docs/evidence/real-claude-v1-validation-20261104-retry.md; docs/evidence/real-claude-v1-validation-20261104-retry/validation.json; native-invocations.jsonl; headless-start-builder.jsonl; state.db; final.db.json; consistency.stdout.json',
    '--next', '主 agent review 认证边界：官方 OAuth 登录不被现有 --bare 模式读取；本轮已按停止条件结束。另行决定支持的认证来源或产品策略，再开启新验收；不要绕过认证或操作用户登录窗口。']
result = run('handoff', args, 'handoff.stdout.log')
assert result.returncode == 0 and b'[DONE] handoff sent' in result.stdout, 'Handoff not confirmed'
