"""Evidence recorder for the authorized S1 concurrent native TUI experiment."""
import base64
import datetime
import hashlib
import json
import os
import pathlib
import shlex
import shutil
import sqlite3
import subprocess
import sys
import time
import uuid

ROOT = pathlib.Path('/Users/funlake/Projects/agentflow')
OUT = pathlib.Path(__file__).resolve().parent
PRIOR = ROOT / 'docs/evidence/S1-codex-final-edge-fd8f8aac'
PLAN = json.loads((ROOT / 'docs/evidence/S1-manual-login-retry-a128f1ae/codex-plan.json').read_text())
HOME_NATIVE = pathlib.Path(PLAN['environment_overrides']['CODEX_HOME'])
WINDOW = '2B9E10E2-0C0A-47DC-AED7-206B833A4462'
WORKSPACE = '28A52619-270D-4FC5-A692-C730028054E2'
A_SURFACE = '4E483942-928F-455C-B03D-75DAEDE9F38F'
IDS = {'A': '01a19ad4-2958-75f3-82b6-f488745caec4', 'B': '01a19b0a-7662-7582-a5f0-36ea08fb1205'}
CMUX = '/Applications/cmux.app/Contents/Resources/bin/cmux'


def now():
    return datetime.datetime.now(datetime.timezone.utc).isoformat()


def save(name, data):
    with (OUT / (name + '.json')).open('x') as f:
        json.dump(data, f, ensure_ascii=False, indent=2)
        f.write('\n')


def load(name):
    return json.loads((OUT / (name + '.json')).read_text())


def run(name, argv, cwd=ROOT, env=None, timeout=25):
    start = time.monotonic()
    p = subprocess.run(argv, cwd=cwd, env=env, stdin=subprocess.DEVNULL, text=True, capture_output=True, timeout=timeout)
    data = dict(at=now(), argv=argv, cwd=str(cwd), exit_code=p.returncode, stdout=p.stdout, stderr=p.stderr, duration_ms=round(1000*(time.monotonic()-start)))
    save(name, data)
    print(json.dumps({'evidence': name, 'exit_code': p.returncode, 'stderr': p.stderr}, ensure_ascii=False))
    return data


def cmux(name, *args):
    return run(name, [CMUX, '--json', '--id-format', 'uuids', *args])


def surface(label):
    return A_SURFACE if label == 'A' else load('owned-b-surface')['surface_id']


def target(label):
    return ['--window', WINDOW, '--workspace', WORKSPACE, '--surface', surface(label)]


def paste(name, label, text):
    assert cmux(name+'-text', 'send', '--paste', *target(label), '--', text)['exit_code'] == 0
    assert cmux(name+'-enter', 'send-key', *target(label), 'enter')['exit_code'] == 0


def native_rows():
    with sqlite3.connect((HOME_NATIVE / 'state_5.sqlite').as_uri()+'?mode=ro', uri=True) as db:
        return {label: db.execute('select id,cwd,model_provider,rollout_path from threads where id=?', (native,)).fetchone() for label, native in IDS.items()}


def native(name, label):
    row = native_rows()[label]
    assert row and pathlib.Path(row[3]).is_relative_to(HOME_NATIVE / 'sessions')
    records = []
    for line in pathlib.Path(row[3]).read_text().splitlines():
        r = json.loads(line)
        p = r.get('payload', {})
        if r['type'] == 'response_item' and p.get('role') in ['developer', 'system']:
            continue
        if r['type'] == 'session_meta':
            r['payload'] = {k: p.get(k) for k in ['id', 'cwd', 'originator', 'cli_version', 'model_provider']}
        records.append(r)
    save(name, dict(at=now(), native_id=IDS[label], surface_id=surface(label), row=row, records=records, read_only=True, credentials_read=False))
    return records


def screen(name, label):
    r = cmux(name, 'read-screen', *target(label), '--scrollback', '--lines', '150')
    assert r['exit_code'] == 0
    d = json.loads(r['stdout'])
    text = d.get('text') or base64.b64decode(d['base64']).decode(errors='replace')
    save(name+'-decoded', dict(at=now(), surface_id=surface(label), text=text))
    print(json.dumps({'screen': name, 'text_suffix': text[-2600:]}, ensure_ascii=False))
    return text


def child_env(label):
    e = {k: os.environ[k] for k in ['HOME', 'USER', 'LOGNAME', 'PATH', 'TERM', 'COLORTERM', 'LANG', 'LC_ALL', 'SHELL', 'CMUX_SOCKET_PATH', 'CMUX_SOCKET_CAPABILITY'] if k in os.environ}
    e.update(PLAN['environment_overrides'])
    e.update(CMUX_WINDOW_ID=WINDOW, CMUX_WORKSPACE_ID=WORKSPACE, CMUX_SURFACE_ID=surface(label), CMUX_PANEL_ID=surface(label))
    return e


def inventory(name):
    files = [p for d in ['cmd', 'internal', 'probes', 'tests'] for p in (ROOT/d).rglob('*.go')]
    files += [ROOT / p for p in ['Makefile', 'go.mod', 'go.sum']]
    evidence = [p for p in PRIOR.rglob('*') if p.is_file() and 'scratch' not in p.relative_to(PRIOR).parts]
    files += evidence
    config = HOME_NATIVE / 'config.toml'
    if config.exists():
        files.append(config)
    save(name, dict(at=now(), hashes={str(p.relative_to(ROOT)): hashlib.sha256(p.read_bytes()).hexdigest() for p in sorted(files)}, credentials_read=False))


def snapshot(name):
    tree = cmux(name+'-tree', 'tree', '--window', WINDOW)
    assert tree['exit_code'] == 0
    data = json.loads(tree['stdout'])
    ws = [w for win in data['windows'] if win['id'] == WINDOW for w in win['workspaces'] if w['id'] == WORKSPACE]
    assert len(ws) == 1
    surfaces = {s['id']: dict(s, pane_id=p['id']) for p in ws[0]['panes'] for s in p['surfaces']}
    pids = {'A': 96559, 'B': load('b-process')['pid']}
    result = {}
    for label, pid in pids.items():
        s = surfaces[surface(label)]
        assert s['selected_in_pane']
        ps = run(name+'-'+label+'-process', ['/bin/ps', '-p', str(pid), '-o', 'pid=,ppid=,tty=,lstart=,command='])
        assert ps['exit_code'] == 0 and IDS[label] in ps['stdout'] and ps['stdout'].split()[2] == s['tty']
        cwd = run(name+'-'+label+'-cwd', ['/usr/sbin/lsof', '-a', '-p', str(pid), '-d', 'cwd', '-Fn'])
        assert '\nn'+native_rows()[label][1]+'\n' in cwd['stdout']
        text = screen(name+'-'+label+'-screen', label)
        native(name+'-'+label+'-native', label)
        result[label] = dict(pid=pid, tty=s['tty'], surface_id=s['id'], pane_id=s['pane_id'], native_id=IDS[label], cwd=native_rows()[label][1], screen=name+'-'+label+'-screen.json')
    assert result['A']['pid'] != result['B']['pid'] and result['A']['tty'] != result['B']['tty'] and result['A']['pane_id'] != result['B']['pane_id']
    again = run(name+'-both-live', ['/bin/ps', '-p', ','.join(map(str,pids.values())), '-o', 'pid=,ppid=,tty=,stat=,command='])
    assert len(again['stdout'].strip().splitlines()) == 2
    save(name+'-binding', dict(at=now(), window_id=WINDOW, workspace_id=WORKSPACE, threads=result, simultaneous_visible_panes=True))


stage = sys.argv[1]
if stage == 'initial':
    inventory('inventory-before')
    save('execution-policy', dict(at=now(), session_id='754ba5a3-ca1d-43a7-ba19-b14301cc33f1', task_id='s1-codex-concurrent-tui', authorized_window=WINDOW, authorized_workspace=WORKSPACE, original_surface=A_SURFACE, native_ids=IDS, strategy='Keep A alive; create one owned split in authorized workspace and resume stopped B in its original cwd. Route by explicit native ID; normal quit only B and close only owned split.', model_calls_opencode=0, claude_operations=0, credentials_copied=False, provider_config_changes=False, s2=False, go_checks='NOT_RUN_EVIDENCE_ONLY'))
    save('cli-availability', dict(at=now(), ochist=shutil.which('ochist'), jev_judge=shutil.which('jev-judge'), typesafe_api_key_set=bool(os.getenv('TYPESAFE_API_KEY')), history_fallback='Supplied immutable prior evidence', judgment='UNAVAILABLE; no judgment fabricated; reversible authorized experiment only'))
    cmux('initial-tree', 'tree', '--window', WINDOW)
    screen('initial-A-screen', 'A')
    native('initial-A-native', 'A')
    # B surface does not exist yet, so capture its rollout without suggesting a binding.
    save('initial-native-rows', dict(at=now(), rows=native_rows()))
    run('initial-A-process', ['/bin/ps', '-p', '96559,96558', '-o', 'pid=,ppid=,tty=,lstart=,command='])
elif stage == 'create':
    r = cmux('create-b-split', 'new-split', 'right', '--focus', 'false', *target('A'))
    assert r['exit_code'] == 0
    d = json.loads(r['stdout'])
    print(json.dumps(d))
    sid = d.get('surface_id') or d.get('surface', {}).get('id')
    assert sid and sid != A_SURFACE
    save('owned-b-surface', dict(at=now(), surface_id=sid, window_id=WINDOW, workspace_id=WORKSPACE, ownership='created by this recorder for this task'))
elif stage == 'start':
    paste('b-start', 'B', shlex.join([sys.executable, str(pathlib.Path(__file__).resolve()), 'child']))
elif stage == 'child':
    assert os.getenv('CMUX_SURFACE_ID') == surface('B') and os.getenv('CMUX_WORKSPACE_ID') == WORKSPACE
    cwd = native_rows()['B'][1]
    argv = PLAN['argv'] + ['resume', IDS['B']]
    save('b-launch', dict(at=now(), argv=argv, cwd=cwd, surface_id=surface('B'), native_id=IDS['B'], environment_overrides=PLAN['environment_overrides'], wrapper_pid=os.getpid(), credentials_copied=False))
    p = subprocess.Popen(argv, cwd=cwd, env=child_env('B'))
    save('b-process', dict(at=now(), pid=p.pid, wrapper_pid=os.getpid(), argv=argv, cwd=cwd, surface_id=surface('B'), native_id=IDS['B']))
    code = p.wait()
    save('b-exit', dict(at=now(), exit_code=code, pid=p.pid, native_id=IDS['B'], surface_id=surface('B')))
    print('Owned B native exited', code, '; owned shell retained.', flush=True)
elif stage == 'screen':
    screen(sys.argv[2], sys.argv[3])
elif stage == 'status':
    paste(sys.argv[2], sys.argv[3], '/status')
elif stage == 'snapshot':
    snapshot(sys.argv[2])
elif stage == 'seed':
    label = sys.argv[2]
    nonce = 'S1-concurrent-private-'+label+'-'+str(uuid.uuid4())
    source = "import os,json; print(json.dumps({'nonce':"+repr(nonce)+",'cwd':os.getcwd(),'workspace':os.environ.get('CMUX_WORKSPACE_ID'),'surface':os.environ.get('CMUX_SURFACE_ID'),'tool_pid':os.getpid()}))"
    command = shlex.join(['/usr/bin/python3', '-c', source])
    prompt = 'Controlled S1 concurrent two-native-TUI isolation probe. Your private label is '+label+' and private nonce is '+nonce+'. Keep it in THIS conversation only. Use your terminal tool to run exactly this harmless command in the DEFAULT cwd, without overriding/changing cwd. Do not read or modify files, inspect other sessions, or start any other work. Reply CONCURRENT_PRIVATE_ACK '+label+' '+nonce+' and the actual tool result. Command: '+command
    sender = native_rows()['B' if label == 'A' else 'A'][1]
    save('seed-'+label+'-request', dict(at=now(), label=label, native_id=IDS[label], surface_id=surface(label), nonce=nonce, message=prompt, command=command, sender_cwd=sender))
    run('seed-'+label+'-queue', ['/opt/homebrew/bin/codex', '-c', 'cli_auth_credentials_store="file"', '-c', 'check_for_update_on_startup=false', 'queue', '--thread', IDS[label], '--message', prompt], cwd=pathlib.Path(sender), env=child_env(label))
elif stage == 'recall':
    label = sys.argv[2]
    nonce = 'S1-concurrent-route-'+label+'-'+str(uuid.uuid4())
    prompt = 'Controlled S1 concurrent routing/context check. Use only THIS conversation history. Do not read files, inspect other sessions or use any tools. Recall the private label and nonce from your earlier CONCURRENT_PRIVATE_ACK. Reply CONCURRENT_RECALL '+nonce+' followed by your own label and full private nonce. State whether THIS conversation contains a concurrent private nonce for the other label; say UNKNOWN if not available. The private nonce is intentionally omitted from this request.'
    assert load('seed-'+label+'-request')['nonce'] not in prompt
    sender = native_rows()['B' if label == 'A' else 'A'][1]
    save('recall-'+label+'-request', dict(at=now(), native_id=IDS[label], surface_id=surface(label), nonce=nonce, message=prompt, sender_cwd=sender, private_nonce_omitted=True))
    run('recall-'+label+'-queue', ['/opt/homebrew/bin/codex', '-c', 'cli_auth_credentials_store="file"', '-c', 'check_for_update_on_startup=false', 'queue', '--thread', IDS[label], '--message', prompt], cwd=pathlib.Path(sender), env=child_env(label))
elif stage == 'native':
    native(sys.argv[2], sys.argv[3])
elif stage == 'quit-b':
    p = load('b-process')
    check = run('pre-cleanup-b-process', ['/bin/ps', '-p', str(p['pid']), '-o', 'pid=,ppid=,tty=,command='])
    assert check['exit_code'] == 0 and IDS['B'] in check['stdout'] and check['stdout'].split()[1] == str(p['wrapper_pid'])
    text = screen('pre-cleanup-B-screen', 'B')
    assert 'CONCURRENT_RECALL' in text and 'Ask Codex' in text
    paste('b-quit', 'B', '/quit')
elif stage == 'close-b':
    assert load('b-exit')['exit_code'] == 0
    p = load('b-process')
    check = run('cleanup-b-process-absent', ['/bin/ps', '-p', str(p['pid']), '-o', 'pid=,ppid=,tty=,command='])
    assert check['exit_code'] == 1 and not check['stdout'].strip()
    screen('cleanup-B-shell', 'B')
    assert cmux('close-owned-b-split', 'close-surface', *target('B'))['exit_code'] == 0
    cmux('final-tree', 'tree', '--window', WINDOW)
    screen('final-A-screen', 'A')
    native('final-A-native', 'A')
    native('final-B-native', 'B')
    run('final-A-process', ['/bin/ps', '-p', '96559,96558', '-o', 'pid=,ppid=,tty=,lstart=,command='])
    inventory('inventory-after')
elif stage == 'cleanup-settled':
    cmux('final-tree-settled', 'tree', '--window', WINDOW)
    screen('final-A-screen-settled', 'A')
elif stage == 'handoff':
    assert load('evidence-validation')['status'] == 'PASS'
    assert load('document-validation-final')['status'] == 'PASS'
    result = run('handoff', [
        '/Users/funlake/.codex/skills/okra-agent-init/scripts/agent-handoff.sh',
        'send', '--window', '1', '--session-id', '754ba5a3-ca1d-43a7-ba19-b14301cc33f1',
        '--status', 'DONE',
        '--summary', 'PASS：Codex 并发双存活 native TUI 消息/输出/cwd/上下文隔离；新增 1 项累计 19 项受控 PASS，完整 S1 PARTIAL。A 原 PID 96559 保留；B PID 4776 正常退出 0，新分屏关闭，原布局恢复。',
        '--evidence', str(OUT) + '/README.md；runtime-matrix.json、dependencies.json、codex-verification.json、evidence-validation.json、document-validation-final.json；两次双存活 PID/TTY/surface/native ID 绑定、真实工具和 assistant nonce ACK、无私有 nonce 重发的回忆及完整记录交叉检查 PASS；S1-result.md 与 S1-capability-matrix.md 已更新。',
        '--next', '仅继续 S1 剩余项；hook/AgentFlow 适配器、无 TUI 自动唤醒、自动/主机故障恢复、中断工具语义和通用草稿门禁仍 UNVERIFIED；OpenCode provider 依赖保留。本轮 OpenCode 模型调用 0、Claude 无操作、未改 Go/配置、未运行 Go 检查、不进入 S2。'
    ])
    print(result['stdout'], end='')
    assert result['exit_code'] == 0 and '[DONE] handoff sent' in result['stdout']
else:
    raise SystemExit('unknown stage')
