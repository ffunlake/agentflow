"""One authorized installed-OpenCode up attempt; exclusive action receipts."""
import hashlib
import json
import os
from pathlib import Path
import queue
import re
import shlex
import signal
import sqlite3
import subprocess
import sys
import threading
import time

ROOT = Path(__file__).resolve().parent
PROJECT = Path('/Users/funlake/Projects/agentflow')
CMUX = '/Applications/cmux.app/Contents/Resources/bin/cmux'
WINDOW = 'window:1000060000'
CONTROL = 'workspace:1000060006'
RUN = 'real-up-3117cc8e'
BINARY = str(PROJECT / 'bin/agentflow')


def write(name, value):
    fd = os.open(ROOT / name, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(fd, 'w') as f:
        json.dump(value, f, ensure_ascii=False, indent=2)
        f.write('\n')
        f.flush()
        os.fsync(f.fileno())


def record(name, argv, accepted=(0,), timeout=15):
    path = ROOT / (name + '.json')
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(fd, 'w') as f:
        json.dump({'state': 'REQUESTED', 'argv': argv}, f)
        f.flush()
        os.fsync(f.fileno())
        p = subprocess.run(argv, cwd=PROJECT, capture_output=True, text=True,
                           timeout=timeout)
        value = {'argv': argv, 'exit_code': p.returncode,
                 'stdout': p.stdout, 'stderr': p.stderr}
        f.seek(0)
        f.truncate()
        json.dump(value, f, ensure_ascii=False, indent=2)
        f.flush()
        os.fsync(f.fileno())
    if p.returncode not in accepted:
        raise RuntimeError(name + ' failed; inspect its exclusive receipt')
    return value


def created_workspace():
    receipt = json.loads((ROOT / 'create-workspace.json').read_text())
    try:
        workspace = json.loads(receipt['stdout']).get('workspace_ref')
    except json.JSONDecodeError:
        match = re.fullmatch(r'OK (workspace:\d+)\s*', receipt['stdout'])
        assert match, 'unrecognized workspace creation receipt; do not recreate'
        workspace = match.group(1)
    assert workspace and workspace not in (CONTROL, 'workspace:1000060011')
    return workspace


def inspect_created():
    workspace = created_workspace()
    record('temporary-tree', [CMUX, '--json', '--id-format', 'both', 'tree', '--window', WINDOW,
                             '--workspace', workspace])
    print(json.dumps({'target': target()[:2]}))


def target():
    workspace = created_workspace()
    tree = json.loads((ROOT / 'temporary-tree.json').read_text())['stdout']
    tree = json.loads(tree)
    workspaces = [w for v in tree['windows'] for w in v['workspaces']
                  if w['ref'] == workspace]
    assert len(workspaces) == 1
    panes = workspaces[0]['panes']
    assert len(panes) == 1 and len(panes[0]['surfaces']) == 1
    surface = panes[0]['surfaces'][0]
    assert surface['ref'] not in ('surface:1000060034', 'surface:1000060035')
    return workspace, surface['ref'], workspaces[0]['id'], surface['id']


def hashes():
    base = ROOT / 'run'
    paths = [base / name for name in ('config.resolved.json', 'native.jsonl',
                                      'up.requested.json')]
    paths += sorted((ROOT / 'context').rglob('*'))
    return {str(p.relative_to(ROOT)): hashlib.sha256(p.read_bytes()).hexdigest()
            for p in paths if p.is_file()}


def verify(result):
    workspace, surface, wid, sid = target()
    records = [json.loads(s) for s in (ROOT / 'run/native.jsonl').read_text().splitlines()]
    assert len(records) == 2
    challenge, finished = records
    assert challenge['type'] == 'challenge' and finished['type'] == 'finished'
    assert finished['executed'] and finished['process_succeeded'] and not finished['truncated']
    expected = challenge['challenge']
    assert expected['actor'] == {'runId': RUN, 'agentId': 'solo', 'generation': 1}
    assert expected['tool'] == 'opencode' and expected['status'] == 'READY'
    assert len(expected['nonce']) == 48
    assert challenge['location']['workspace_id'] == wid
    assert challenge['location']['surface_id'] == sid
    events = [json.loads(line) for line in finished['stdout'].splitlines()]
    assert not any(e.get('type') == 'error' for e in events)
    text = ''.join(e['part']['text'] for e in events
                   if e.get('type') == 'text' and e.get('part', {}).get('type') == 'text')
    assert json.loads(text) == expected
    assert any(e.get('type') == 'step_finish' and e.get('part', {}).get('reason') == 'stop'
               for e in events)
    native_ids = {identity for e in events for identity in
                  (e.get('sessionID'), e.get('part', {}).get('sessionID')) if identity}
    assert len(native_ids) == 1
    db = sqlite3.connect('file:' + str(ROOT / 'run/state.db') + '?mode=ro', uri=True)
    db.row_factory = sqlite3.Row
    seat = dict(db.execute('SELECT * FROM seats WHERE is_current=1').fetchone())
    journal = [dict(row) for row in db.execute('SELECT * FROM journal ORDER BY seq')]
    db.close()
    assert seat['run_id'] == RUN and seat['agent_id'] == 'solo' and seat['generation'] == 1
    assert seat['status'] == 'READY' and seat['tool'] == 'opencode'
    assert native_ids == {seat['native_session_id']}
    assert seat['workspace_id'] == wid and seat['surface_id'] == sid
    assert [e['event'] for e in journal] == ['LAUNCH_REQUESTED', 'LAUNCH_BOUND', 'STARTUP_READY_VERIFIED']
    proof = json.loads(journal[-1]['details'])
    assert proof['nonce'] == expected['nonce'] == result['ready_proof']['nonce']
    for source, dest in [('RunID', 'run_id'), ('AgentID', 'agent_id'),
                         ('Generation', 'generation'), ('Tool', 'tool'),
                         ('NativeSessionID', 'native_session_id'),
                         ('WorkspaceID', 'workspace_id'), ('SurfaceID', 'surface_id')]:
        assert proof['seat'][source] == seat[dest]
    assert result['executed'] and result['coordinator_ready'] and result['status'] == 'READY'
    assert result['native_process'] == 'EXITED_AFTER_HANDSHAKE'
    for name in ('config.resolved.json', 'native.jsonl', 'up.requested.json'):
        assert (ROOT / 'run' / name).stat().st_mode & 0o777 == 0o600
    assert (ROOT / 'run').stat().st_mode & 0o777 == 0o700
    assert Path(result['context_path']).stat().st_mode & 0o777 == 0o700
    return {'status': 'PASS', 'seat': seat, 'ready_proof': proof,
            'journal_events': [e['event'] for e in journal],
            'native_invocations': 1, 'workspace': workspace, 'surface': surface}


def execute():
    workspace, surface, _, _ = target()
    # Reserve the whole field attempt before starting any provider process.
    write('native-attempt.json', {'provider': 'opencode', 'limit': 1,
                                 'workspace': workspace, 'surface': surface})
    argv = [BINARY, 'up', '-f', str(ROOT / 'team.json'), '--run', RUN,
            '--run-dir', str(ROOT / 'run'), '--window', WINDOW,
            '--workspace', workspace, '--surface', surface, '--json']
    write('up-command.json', {'argv': argv, 'cwd': str(PROJECT)})
    result = {'status': 'PARTIAL', 'scope': 'one installed OpenCode up; no other provider'}
    fd = os.open(ROOT / 'up-stderr.txt', os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(fd, 'w') as errfile:
        p = subprocess.Popen(argv, cwd=PROJECT, stdin=subprocess.DEVNULL,
                             stdout=subprocess.PIPE, stderr=errfile, text=True)
        write('up-process.json', {'pid': p.pid, 'parent_pid': os.getpid()})
        try:
            ready = queue.Queue()
            def read_result():
                body = ''
                for line in p.stdout:
                    body += line
                    try:
                        value = json.loads(body)
                    except json.JSONDecodeError:
                        continue
                    ready.put(value)
                    return
                ready.put({'status': 'NO_STRUCTURED_RESULT'})
            threading.Thread(target=read_result, daemon=True).start()
            value = ready.get(timeout=95)
            write('up-result.json', value)
            assert value['status'] == 'READY', 'native startup did not establish READY'
            inbox = record('ipc-inbox', [BINARY, 'inbox', '--run', RUN, '--run-dir',
                           str(ROOT / 'run'), '--agent', 'solo', '--generation', '1', '--json'])
            assert json.loads(inbox['stdout'])['status'] == 'QUERIED'
            duplicate = record('duplicate-active', argv, accepted=(1,))
            assert 'ALREADY_RUNNING' in duplicate['stdout']
            before = hashes()
            p.send_signal(signal.SIGINT)
            assert p.wait(timeout=12) == 0
            write('up-exit.json', {'exit_code': p.returncode, 'signal': 'SIGINT',
                                  'purpose': 'stop only the owned foreground coordinator'})
            result.update(verify(value))
            duplicate = record('duplicate-reopened', argv, accepted=(1,))
            assert 'UP_ALREADY_ATTEMPTED' in duplicate['stdout']
            assert before == hashes(), 'duplicate up overwrote evidence or context'
            assert not (ROOT / 'run/coordinator.lock').exists()
            assert not (ROOT / 'run/ipc.sock').exists()
            write('immutable-artifacts.json', {'status': 'PASS', 'sha256': before})
        except Exception as error:
            result['error'] = str(error)
        finally:
            if p.poll() is None:
                p.send_signal(signal.SIGINT)
                try:
                    p.wait(timeout=12)
                except subprocess.TimeoutExpired:
                    result['cleanup'] = 'coordinator did not stop; manual inspection required'
            result['up_exit_code'] = p.poll()
            write('field-result.json', result)
            print(json.dumps(result, ensure_ascii=False, indent=2))


action = sys.argv[1]
if action == 'preflight':
    record('control-before', [CMUX, '--json', '--id-format', 'both', 'tree', '--window', WINDOW,
                             '--workspace', CONTROL])
elif action == 'create':
    value = record('create-workspace', [CMUX, '--json', '--id-format', 'both', 'new-workspace',
                   '--window', WINDOW, '--cwd', str(PROJECT), '--focus', 'false'])
    inspect_created()
elif action == 'inspect-created':
    inspect_created()
elif action == 'prepare':
    workspace, surface, _, _ = target()
    write('scope.json', {'window': WINDOW, 'workspace': workspace, 'surface': surface,
                        'protected_surfaces': ['surface:1000060034', 'surface:1000060035'],
                        'providers': {'opencode': 1, 'codex': 0, 'claude': 0},
                        'action': 'one up in a newly created workspace; originals never addressed'})
    write('team.json', {'version': 1, 'name': 'single-real-up', 'terminal': 'cmux',
                       'lead': 'solo', 'context_dir': str(ROOT / 'context'),
                       'agents': {'solo': {'role': 'Senior developer validation', 'tool': 'opencode',
                                  'cwd': str(PROJECT), 'permissions': 'default', 'session': 'fresh',
                                  'prompt': 'Only acknowledge the exact startup challenge. Do not use tools, inspect files, authenticate, retry or change anything.'}},
                       'layout': {'agent': 'solo'}})
elif action == 'submit':
    workspace, surface, _, _ = target()
    scope = ['--window', WINDOW, '--workspace', workspace, '--surface', surface]
    text = shlex.join([sys.executable, str(ROOT / 'field.py'), 'execute'])
    record('send-once', [CMUX, 'send', '--paste'] + scope + ['--', text])
    record('enter-once', [CMUX, 'send-key'] + scope + ['enter'])
elif action == 'execute':
    execute()
elif action == 'verify':
    value = verify(json.loads((ROOT / 'up-result.json').read_text()))
    expected = json.loads((ROOT / 'immutable-artifacts.json').read_text())['sha256']
    assert expected == hashes()
    assert json.loads((ROOT / 'cleanup-verification.json').read_text())['status'] == 'PASS'
    for path in ROOT.glob('*.json'):
        data = json.loads(path.read_text())
        argv = data.get('argv', [])
        assert 'surface:1000060034' not in argv and 'surface:1000060035' not in argv
        assert 'window:1000060005' not in argv and 'workspace:1000060011' not in argv
    value['immutable_artifacts'] = 'PASS'
    value['cleanup'] = 'PASS'
    value['protected_targets_addressed'] = False
    write('offline-verification.json', value)
    print(json.dumps({'status': 'PASS', 'native_invocations': 1,
                      'immutable_artifacts': 'PASS', 'cleanup': 'PASS'}))
elif action == 'close':
    workspace, surface, _, _ = target()
    assert (ROOT / 'field-result.json').exists(), 'do not close an unconfirmed running attempt'
    record('close-temporary-workspace', [CMUX, '--json', 'close-workspace', '--window', WINDOW,
                                       '--workspace', workspace])
    after = record('control-after', [CMUX, '--json', '--id-format', 'both', 'tree', '--window', WINDOW,
                                    '--workspace', CONTROL])
    before = json.loads(json.loads((ROOT / 'control-before.json').read_text())['stdout'])
    current = json.loads(after['stdout'])
    old = [w for v in before['windows'] for w in v['workspaces'] if w['ref'] == CONTROL][0]
    new = [w for v in current['windows'] for w in v['workspaces'] if w['ref'] == CONTROL][0]
    assert old['layout'] == new['layout']
    assert [(p['id'], p['surface_ids']) for p in old['panes']] == [(p['id'], p['surface_ids']) for p in new['panes']]
    write('cleanup-verification.json', {'status': 'PASS', 'control_layout_unchanged': True,
                                       'temporary_workspace_closed': workspace,
                                       'protected_surfaces_addressed': False})
else:
    raise SystemExit('preflight | create | inspect-created | prepare | submit | execute | close | verify')
