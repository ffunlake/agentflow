"""S1 user-authorized native reset evidence recorder; exclusive evidence writes.

All terminal mutations are explicitly bound to the two authorized user surfaces.
No credential contents, process argv, provider configuration or wiki writes.
"""
import base64
import datetime
import hashlib
import json
import os
from pathlib import Path
import sqlite3
import subprocess
import sys

HERE = Path(__file__).resolve().parent
ROOT = HERE.parents[2]
WIKI = Path('/Users/funlake/Projects/okra-wiki')
CMUX = '/Applications/cmux.app/Contents/Resources/bin/cmux'
WINDOW = '73FEB4F4-BE2B-4F4D-9B48-E872BBC51EA3'
WORKSPACE = '3AB7D16C-B09D-44AE-8CD5-5B6225388D45'
TARGETS = {'opencode': ('5265DA42-9A45-4C71-BABE-155E458FCF33', 23097, 'ttys003'),
           'codex': ('94EBBAAE-5EA1-4352-B183-580E24F0F23C', 23477, 'ttys004')}
ALLOWED = {'docs/evidence/S1-result.md', 'docs/evidence/S1-capability-matrix.md'}


def now():
    return datetime.datetime.now(datetime.timezone.utc).isoformat()


def save(name, value):
    with (HERE / (name + '.json')).open('x') as out:
        json.dump(value, out, ensure_ascii=False, indent=2)
        out.write('\n')


def run(name, argv):
    started = now()
    p = subprocess.run(argv, cwd=ROOT, capture_output=True, timeout=45,
                       env={**os.environ, 'GIT_OPTIONAL_LOCKS': '0'})
    data = {'started_at': started, 'finished_at': now(), 'argv': argv,
            'cwd': str(ROOT), 'exit_code': p.returncode,
            'stdout': p.stdout.decode(errors='replace'), 'stderr': p.stderr.decode(errors='replace')}
    save(name, data)
    return data


def cmux(name, command, provider=None, args=()):
    argv = [CMUX, '--json', '--id-format', 'uuids', command,
            '--window', WINDOW, '--workspace', WORKSPACE]
    if provider:
        argv += ['--surface', TARGETS[provider][0]]
    return run(name, argv + list(args))


def snapshot(stage):
    cmux(stage + '-tree', 'tree')
    run(stage + '-processes', ['/bin/ps', '-t', 'ttys003,ttys004', '-o',
                             'pid=,ppid=,pgid=,tty=,stat=,ucomm='])
    for provider, (_, pid, _) in TARGETS.items():
        r = cmux(stage + '-' + provider + '-screen', 'read-screen', provider,
                 ['--scrollback', '--lines', '2000'])
        parsed = json.loads(r['stdout'])
        assert parsed['surface_id'] == TARGETS[provider][0]
        assert parsed['window_id'] == WINDOW and parsed['workspace_id'] == WORKSPACE
        assert base64.b64decode(parsed['base64']).decode() == parsed['text']
        save(stage + '-' + provider + '-decoded', {'at': now(), 'text': parsed['text']})
        run(stage + '-' + provider + '-cwd', ['/usr/sbin/lsof', '-a', '-p', str(pid), '-d', 'cwd', '-Fn'])
        print(provider, parsed['text'].rstrip())


def db(path):
    c = sqlite3.connect('file:' + str(path) + '?mode=ro', uri=True)
    c.row_factory = sqlite3.Row
    c.execute('pragma query_only=on')
    return c


def schema():
    result = {}
    for name, path in [('opencode', Path.home()/'.local/share/opencode/opencode.db'),
                       ('codex', Path.home()/'.codex/state_5.sqlite')]:
        c = db(path)
        result[name] = [dict(r) for r in c.execute("select name,sql from sqlite_master where type='table'")]
        c.close()
    save('native-schema', result)
    print(json.dumps(result, ensure_ascii=False))


def opencode(stage, session=None):
    c = db(Path.home()/'.local/share/opencode/opencode.db')
    if not session:
        rows = [dict(r) for r in c.execute('select * from session where directory=? order by time_updated desc limit 8', (str(WIKI),))]
        save(stage + '-opencode-candidates', rows)
        print(json.dumps(rows, ensure_ascii=False))
        return
    row = dict(c.execute('select * from session where id=?', (session,)).fetchone())
    messages = [dict(r) for r in c.execute('select * from message where session_id=? order by time_created', (session,))]
    parts = [dict(r) for r in c.execute('select * from part where session_id=? order by time_created', (session,))]
    for part in parts:
        data = json.loads(part['data'])
        if data.get('synthetic') or data.get('metadata', {}).get('kind') == 'editor_context':
            part['data'] = json.dumps({'type': data.get('type'), 'synthetic': True,
                                      'redacted': 'Injected editor context omitted'})
    entries = [dict(r) for r in c.execute('select * from session_message where session_id=? order by seq', (session,))]
    save(stage + '-opencode-native', {'at': now(), 'session': row, 'messages': messages, 'parts': parts, 'session_messages': entries})
    print(json.dumps({'session': row, 'messages': len(messages), 'parts': len(parts), 'session_messages': len(entries)}, ensure_ascii=False))


def codex(stage, session=None):
    if not session:
        r = run(stage + '-codex-open-files', ['/usr/sbin/lsof', '-a', '-p', '23477', '-Fn'])
        paths = [Path(x[1:]) for x in r['stdout'].splitlines() if x.startswith('n') and '/rollout-' in x]
        assert len(paths) == 1, paths
        path = paths[0]
    else:
        paths = list((Path.home()/'.codex/sessions').rglob('*' + session + '.jsonl'))
        assert len(paths) == 1, paths
        path = paths[0]
    raw = path.read_bytes()
    events = [json.loads(line) for line in raw.splitlines()]
    # Native logs only for the authorized thread; avoid copying injected system/developer context.
    selected = []
    for i, event in enumerate(events):
        p = event.get('payload', {})
        t = event.get('type')
        if t == 'session_meta':
            p = {k: v for k, v in p.items() if k in ('id','timestamp','cwd','originator','cli_version','source','model_provider')}
        elif t == 'turn_context':
            p = {k: v for k, v in p.items() if k in ('turn_id','cwd','approval_policy','sandbox_policy','model','effort','permission_profile','permissions')}
        elif t == 'response_item' and p.get('role') in ('system','developer'):
            continue
        elif t == 'response_item' and p.get('type') == 'reasoning':
            continue
        elif t not in ('event_msg', 'response_item'):
            continue
        selected.append({'line': i+1, 'timestamp': event.get('timestamp'), 'type': t, 'payload': p})
    save(stage + '-codex-native', {'at': now(), 'path': str(path), 'bytes': len(raw),
         'sha256': hashlib.sha256(raw).hexdigest(), 'events': selected,
         'projection': 'System/developer content omitted; turn context limited to identity and permission fields'})
    print(json.dumps({'path': str(path), 'events': len(selected), 'tail': selected[-6:]}, ensure_ascii=False))


def inventory(root, exclude):
    result = {}
    skipped = []
    def walk(directory):
        for p in sorted(directory.iterdir()):
            rel = str(p.relative_to(root))
            if p.is_symlink():
                result[rel] = {'symlink': os.readlink(p)}
            elif p.name in exclude or p == HERE:
                skipped.append(rel)
            elif p.is_dir():
                walk(p)
            elif p.is_file():
                if p.name.lower() in ('auth.json', 'credentials.json', '.env') or p.name.startswith('.env.'):
                    s = p.stat()
                    result[rel] = {'sensitive_metadata_only': True, 'size': s.st_size, 'mtime_ns': s.st_mtime_ns}
                else:
                    result[rel] = {'sha256': hashlib.sha256(p.read_bytes()).hexdigest(), 'size': p.stat().st_size}
    walk(root)
    return {'files': result, 'excluded': skipped}


def protect(stage):
    result = {'at': now(), 'wiki': inventory(WIKI, {'.git', 'node_modules'}),
              'agentflow': inventory(ROOT, {'scratch', '.git', '.cache'})}
    save(stage + '-inventory', result)
    run(stage + '-wiki-status', ['/usr/bin/git', '-C', str(WIKI), 'status', '--porcelain=v1', '--untracked-files=all'])
    print(json.dumps({'at': result['at'], 'wiki_files': len(result['wiki']['files']),
                      'agentflow_files': len(result['agentflow']['files'])}))


def verify():
    b = json.loads((HERE/'before-inventory.json').read_text())
    a = json.loads((HERE/'after-inventory.json').read_text())
    result = {'at': now()}
    for root in ('wiki', 'agentflow'):
        old, new = b[root]['files'], a[root]['files']
        changed = sorted(k for k in old.keys() & new.keys() if old[k] != new[k])
        added, deleted = sorted(new.keys()-old.keys()), sorted(old.keys()-new.keys())
        ok = not added and not deleted and (not changed if root == 'wiki' else set(changed) == ALLOWED)
        result[root] = {'status': 'PASS' if ok else 'FAIL', 'files_checked': len(old),
                        'changed': changed, 'added': added, 'deleted': deleted,
                        'excluded': b[root]['excluded'], 'sensitive_files': 'metadata only, no credential reads'}
    old_git = json.loads((HERE/'before-wiki-status.json').read_text())
    new_git = json.loads((HERE/'after-wiki-status.json').read_text())
    git_ok = old_git['exit_code'] == new_git['exit_code'] == 0 and old_git['stdout'] == new_git['stdout']
    result['wiki_git_status'] = {'status': 'PASS' if git_ok else 'FAIL',
                                 'before': 'before-wiki-status.json', 'after': 'after-wiki-status.json'}
    save('file-protection-verification', result)
    manifest = {p.name: hashlib.sha256(p.read_bytes()).hexdigest() for p in HERE.iterdir() if p.is_file()}
    save('sha256-manifest', {'at': now(), 'files': manifest, 'self_excluded': True,
                             'later_receipts_excluded': ['handoff.json']})
    assert all(hashlib.sha256((HERE / name).read_bytes()).hexdigest() == digest for name,digest in manifest.items())
    assert all(result[k]['status'] == 'PASS' for k in ('wiki','agentflow','wiki_git_status'))
    print(json.dumps(result, ensure_ascii=False))


if __name__ == '__main__':
    action, *args = sys.argv[1:]
    if action == 'snapshot': snapshot(*args)
    elif action == 'schema': schema()
    elif action == 'opencode': opencode(*args)
    elif action == 'codex': codex(*args)
    elif action == 'protect': protect(*args)
    elif action == 'verify': verify()
    elif action in ('send', 'key'):
        name, provider, text = args
        assert provider in TARGETS
        print(cmux(name, 'send' if action == 'send' else 'send-key', provider, [text])['stdout'])
    elif action == 'capture':
        name, provider = args
        r = cmux(name, 'read-screen', provider, ['--scrollback','--lines','2000'])
        print(json.loads(r['stdout'])['text'])
    elif action == 'command':
        name, *argv = args
        print(json.dumps(run(name, argv), ensure_ascii=False))
    else: raise ValueError(action)
