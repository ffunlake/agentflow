#!/usr/bin/env python3
"""Opt-in real acceptance retry. Stops at the first startup/protocol failure.

Running this file makes real CLI/model requests. It never logs in, opens credential
files, changes user settings, or communicates with existing terminal windows.
"""
import hashlib
import json
import os
from pathlib import Path
import shutil
import signal
import sqlite3
import subprocess
import tempfile
import time
import traceback
import uuid

E = Path(__file__).resolve().parent
ROOT = E.parents[2]
PARENT = Path('/var/folders/wv/x8nwj9tj7xz5dhqf5f2mt3hw0000gn/T/opencode').resolve()
CLI = '/Users/funlake/.nvm/versions/node/v26.8.1/bin/claude'
RUN = 'real-claude-v1-validation-20261104-02'
state = {'session_id': RUN, 'run_id': RUN, 'native_cli': CLI, 'commands': [],
         'started_at': time.time(), 'outcome': 'UNVERIFIED', 'captures': [],
         'checks': {k: 'UNVERIFIED' for k in (
             'real_startup_two_roles', 'execute_nonce_handoff', 'review_approve',
             'workflow_completed', 'persistent_resume_second_delivery',
             'coordinator_reopen_exact_replay', 'real_nonce', 'effective_permissions')},
         'risk_judgment': {'status': 'UNAVAILABLE', 'reason': 'jev-judge absent from PATH'},
         'history_lookup': {'status': 'UNAVAILABLE', 'reason': 'ochist absent from PATH; repository evidence read'},
         'environment_presence_only': {n: bool(os.environ.get(n)) for n in (
             'ANTHROPIC_API_KEY', 'ANTHROPIC_AUTH_TOKEN', 'ANTHROPIC_BASE_URL',
             'CLAUDE_CODE_OAUTH_TOKEN', 'CLAUDE_CODE_USE_BEDROCK',
             'CLAUDE_CODE_USE_VERTEX', 'CLAUDE_CODE_USE_FOUNDRY')},
         'boundaries': {'login_attempted': False, 'credentials_read_or_copied_by_harness': False,
                        'user_configuration_edited_by_harness': False, 'user_windows_accessed': False,
                        'wiki_accessed': False, 'adapter_modified': False}}
owned = []
env = os.environ.copy()
env['AF_VALIDATION_EVIDENCE'] = str(E)

def save():
    (E / 'validation.json').write_text(json.dumps(state, indent=2) + '\n')

def phase(label):
    state['phase'] = label
    save()
    print(label, flush=True)

def hashes():
    files = [p for d in ('cmd', 'internal', 'tests', 'examples')
             for p in (ROOT / d).rglob('*') if p.is_file()]
    files += [ROOT / n for n in ('go.mod', 'go.sum', 'Makefile', 'README.md')]
    return {str(p.relative_to(ROOT)): hashlib.sha256(p.read_bytes()).hexdigest() for p in sorted(files)}

def command(label, args, timeout=540, at=None):
    rec = {'label': label, 'argv': [str(a) for a in args], 'cwd': str(at or cwd), 'started_at': time.time()}
    state['commands'].append(rec)
    save()
    p = subprocess.run(rec['argv'], cwd=rec['cwd'], env=env, capture_output=True, timeout=timeout)
    (E / (label + '.stdout.log')).write_bytes(p.stdout)
    (E / (label + '.stderr.log')).write_bytes(p.stderr)
    rec.update(exit_code=p.returncode, finished_at=time.time())
    save()
    return p

def launch(label, args):
    rec = {'label': label, 'argv': [str(a) for a in args], 'cwd': str(cwd), 'started_at': time.time()}
    state['commands'].append(rec)
    with (E / (label + '.stdout.log')).open('wb') as out, (E / (label + '.stderr.log')).open('wb') as err:
        p = subprocess.Popen(rec['argv'], cwd=cwd, env=env, stdout=out, stderr=err, start_new_session=True)
    owned.append((p, rec))
    rec['pid'] = p.pid
    save()
    deadline = time.monotonic() + 420
    last_notice = time.monotonic()
    while time.monotonic() < deadline:
        text = (E / (label + '.stdout.log')).read_text()
        if text.strip():
            data = json.loads(text)
            state[label + '_response'] = data
            save()
            if data.get('status') in ('READY', 'COORDINATOR_READY'):
                return data
            p.wait(timeout=20)
            rec['exit_code'] = p.returncode
            raise RuntimeError(label + ' returned ' + str(data.get('status')))
        if p.poll() is not None:
            rec['exit_code'] = p.returncode
            raise RuntimeError(label + ' exited before readiness')
        if time.monotonic() - last_notice >= 25:
            print(label + ': waiting for bounded native turn', flush=True)
            last_notice = time.monotonic()
        time.sleep(.25)
    raise TimeoutError(label + ' readiness deadline')

def close():
    for p, rec in owned:
        if p.poll() is None:
            p.send_signal(signal.SIGTERM)
            p.wait(timeout=20)
        rec.update(exit_code=p.returncode, finished_at=time.time())
    save()

def snapshot(label):
    db = sqlite3.connect('file:' + str(run / 'state.db') + '?mode=ro', uri=True)
    data = {}
    for (name,) in db.execute("SELECT name FROM sqlite_master WHERE type='table' ORDER BY name"):
        cur = db.execute('SELECT * FROM "' + name.replace('"', '""') + '" ORDER BY rowid')
        data[name] = [dict(zip([d[0] for d in cur.description], row)) for row in cur]
    state['checks'][label + '_db_integrity'] = list(db.execute('PRAGMA integrity_check')) == [('ok',)] and not list(db.execute('PRAGMA foreign_key_check'))
    db.close()
    (E / (label + '.db.json')).write_text(json.dumps(data, indent=2) + '\n')
    return data

def workflow(label, kind, revision, request, reason=None):
    args = [binary, 'workflow', kind, '--run', RUN, '--run-dir', run, '--agent', 'main',
            '--generation', '1', '--revision', str(revision), '--request-id', request, '--timeout', '8m', '--json']
    if reason:
        args += ['--reason', reason]
    p = command(label, args)
    assert p.returncode == 0, label
    return json.loads(p.stdout)

os.umask(0o077)
assert not (E / 'validation.json').exists(), 'Refuse to overwrite an earlier run'
assert PARENT.is_dir()
temp = Path(tempfile.mkdtemp(prefix='ac-', dir=PARENT)).resolve()
cwd, run, binary = temp / 'cwd', temp / 'run', temp / 'agentflow'
cwd.mkdir(mode=0o700)
run.mkdir(mode=0o700)
observer = E / 'native-observer.py'
observer.chmod(0o700)
nonce = 'AF_CLAUDE_REAL_' + uuid.uuid4().hex
state.update(temp_root=str(temp), cwd=str(cwd), run_dir=str(run), nonce=nonce,
             production_before=hashes(), cwd_before=[])
try:
    phase('metadata_and_build')
    version = command('claude-version', [CLI, '--version'], 20)
    assert version.returncode == 0
    state['native_version'] = version.stdout.decode().strip()
    assert command('claude-help', [CLI, '--help'], 20).returncode == 0
    assert command('build', ['go', 'build', '-o', binary, './cmd/agentflow'], 180, ROOT).returncode == 0
    instruction = ('Do not use tools, inspect files, authenticate, or modify anything. '
        'For purpose=execute with revision=1, return exactly {"kind":"blocked","reason":"' + nonce + ' explicit lead resume requested for acceptance"}. '
        'For purpose=execute with revision greater than 1, return kind=handoff with summary and evidence each containing the exact nonce ' + nonce + ', next=review. '
        'For purpose=review, check the supplied handoff summary and evidence for that exact nonce; return kind=approve and accepted=[true] only if both contain it. '
        'No external facts or tests are required: this is a no-tools nonce protocol check.')
    config = {'version': 1, 'name': RUN, 'terminal': 'cmux', 'lead': 'main', 'context_dir': str(temp / 'context'),
        'defaults': {'tool': 'claude', 'session': 'persistent', 'permissions': 'default', 'cwd': str(cwd)},
        'agents': {a: {'role': role, 'prompt': 'Follow scoped nonce instructions. Do not use tools, inspect files, authenticate or modify files.'}
                   for a, role in [('main', 'Reviewer'), ('builder', 'Developer')]},
        'layout': {'split': 'columns', 'children': [{'agent': 'main'}, {'agent': 'builder'}]},
        'workflow': {'entry': 'nonce', 'completion': 'done', 'steps': [{'id': 'nonce', 'execute': {'agent': 'builder'},
            'reviewer': 'main', 'instruction': instruction, 'acceptance': ['The handoff summary and evidence both contain ' + nonce + '.'],
            'handoff_required': True, 'max_attempts': 2, 'transitions': {'approve': 'next', 'changes': 'self', 'retry': 'self', 'blocked': 'pause'}, 'next': 'done'}]}}
    (E / 'team.json').write_text(json.dumps(config, indent=2) + '\n')
    native_flags = ['--headless-claude', '--native-binary', observer, '--turn-timeout', '3m']
    phase('real_up')
    up = launch('up', [binary, 'up', '-f', E / 'team.json', *native_flags, '--startup-timeout', '7m', '--run', RUN, '--run-dir', run, '--json'])
    assert len(up['seats']) == 2
    state['checks']['real_startup_two_roles'] = 'PASS'
    snapshot('after-up')
    phase('workflow_start_pause')
    start = workflow('workflow-start', 'start', 0, 'start-real-1')
    state['start'] = start
    assert start['workflow_current']['phase'] == 'PAUSED' and start['workflow_current']['revision'] == 2
    paused = snapshot('paused')
    close()
    phase('coordinator_reopen_paused')
    launch('coordinator-reopen-paused', [binary, 'coordinator', *native_flags, '--run', RUN, '--run-dir', run, '--json'])
    replay = workflow('start-exact-replay', 'start', 0, 'start-real-1')
    assert replay['workflow'] == start['workflow'] and not replay.get('executed') and not replay.get('dispatch')
    assert snapshot('paused-after-replay') == paused
    phase('persistent_resume_second_delivery')
    reason = 'No-tools nonce protocol approved; continue ' + nonce
    resumed = workflow('workflow-resume', 'resume', 2, 'resume-real-1', reason)
    state['resume'] = resumed
    assert resumed['workflow_current']['phase'] == 'COMPLETED' and resumed['workflow_current']['revision'] == 5
    completed = snapshot('completed')
    state['checks'].update(execute_nonce_handoff='PASS', review_approve='PASS', workflow_completed='PASS')
    close()
    phase('coordinator_reopen_completed')
    launch('coordinator-reopen-completed', [binary, 'coordinator', *native_flags, '--run', RUN, '--run-dir', run, '--json'])
    for label, kind, revision, request, why, original in [
        ('completed-start-replay', 'start', 0, 'start-real-1', None, start),
        ('completed-resume-replay', 'resume', 2, 'resume-real-1', reason, resumed),
        ('workflow-status', 'status', 0, 'status-real-1', None, None),
        ('terminal-drain', 'drain', 5, 'drain-real-1', None, None)]:
        reply = workflow(label, kind, revision, request, why)
        assert not reply.get('executed') and not reply.get('dispatch')
        if original:
            assert reply['workflow'] == original['workflow']
    assert snapshot('completed-after-replays') == completed
    state['checks']['coordinator_reopen_exact_replay'] = 'PASS'
    close()
    calls = [json.loads(l) for l in (E / 'native-invocations.jsonl').read_text().splitlines()]
    exits = [c for c in calls if c['event'] == 'exit']
    assert len(exits) == 5 and all(c['exit_code'] == 0 for c in exits)
    assert len([c for c in exits if '--resume' in c['argv']]) == 3
    seats = {s['agent_id']: s for s in completed['seats']}
    builder_resumes = [c for c in exits if '--resume' in c['argv'] and c['argv'][c['argv'].index('--resume') + 1] == seats['builder']['native_session_id']]
    assert len(builder_resumes) == 2
    assert nonce in ''.join(p.read_text() for p in run.glob('headless-result-*.json'))
    state['checks'].update(persistent_resume_second_delivery='PASS', real_nonce='PASS')
    state['outcome'] = 'PASS'
except Exception as exc:
    state.update(outcome='BLOCKED_CONFIRM', error=str(exc))
    (E / 'failure.log').write_text(traceback.format_exc())
    print('BLOCKED_CONFIRM: ' + str(exc) + '; stopping native requests', flush=True)
finally:
    phase('archive_and_cleanup')
    try:
        close()
        state['cleanup'] = {'owned_processes_exited': all(p.poll() is not None for p, _ in owned),
            'lock_removed': not (run / 'coordinator.lock').exists(), 'socket_removed': not (run / 'ipc.sock').exists(),
            'cwd_entries': sorted(p.name for p in cwd.iterdir())}
        if (run / 'state.db').exists():
            final = snapshot('final')
            db = sqlite3.connect('file:' + str(run / 'state.db') + '?mode=ro', uri=True)
            dest = sqlite3.connect(E / 'state.db')
            db.backup(dest)
            dest.close()
            db.close()
            state['db_table_counts'] = {k: len(v) for k, v in final.items()}
        no_tools = True
        for path in sorted(run.glob('*.json')):
            shutil.copy2(path, E / path.name)
            if not path.name.startswith(('headless-start-', 'headless-result-')):
                continue
            cap = json.loads(path.read_text())
            raw = cap['stdout']
            (E / (path.stem + '.jsonl')).write_text(raw)
            events = [json.loads(l) for l in raw.splitlines()]
            sessions = sorted({e['session_id'] for e in events if e.get('session_id')})
            tool_events = [e for e in events if e.get('type') == 'tool_progress' or any(
                b.get('type') == 'tool_use' for b in e.get('message', {}).get('content', []) if isinstance(b, dict))]
            no_tools &= not tool_events
            state['captures'].append({'capture': path.name, 'seat': cap['seat'], 'message_id': cap['message_id'],
                'native_session_ids': sessions, 'event_count': len(events), 'succeeded': cap['succeeded'],
                'tool_event_count': len(tool_events)})
        state['checks']['no_tool_events_observed'] = 'PASS' if state['captures'] and no_tools else 'UNVERIFIED'
        state['checks']['cwd_unchanged_empty'] = not list(cwd.iterdir())
        state['production_after'] = hashes()
        state['checks']['production_files_unchanged'] = state['production_before'] == state['production_after']
        assert temp.parent == PARENT and temp.name.startswith('ac-')
        shutil.rmtree(temp)
        state['cleanup']['temporary_root_removed'] = not temp.exists()
    except Exception:
        state['cleanup_error'] = traceback.format_exc()
    state['finished_at'] = time.time()
    phase('finished')
