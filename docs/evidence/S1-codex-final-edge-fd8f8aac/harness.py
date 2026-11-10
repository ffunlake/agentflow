"""Evidence-only S1 recorder. No AgentFlow application behavior is changed."""
import base64
import datetime
import hashlib
import json
import os
import pathlib
import shlex
import sqlite3
import subprocess
import sys
import time
import uuid

ROOT = pathlib.Path('/Users/funlake/Projects/agentflow')
OUT = pathlib.Path(__file__).resolve().parent
PRIOR = ROOT / 'docs/evidence/S1-codex-edge-cases-eb2e9a87'
PLAN = json.loads((ROOT / 'docs/evidence/S1-manual-login-retry-a128f1ae/codex-plan.json').read_text())
TARGET = {'window_id': '2B9E10E2-0C0A-47DC-AED7-206B833A4462', 'workspace_id': '28A52619-270D-4FC5-A692-C730028054E2', 'surface_id': '4E483942-928F-455C-B03D-75DAEDE9F38F'}
ORIGINAL = '01a19ad4-2958-75f3-82b6-f488745caec4'
CMUX = '/Applications/cmux.app/Contents/Resources/bin/cmux'
NATIVE_HOME = pathlib.Path(PLAN['environment_overrides']['CODEX_HOME'])


def now():
    return datetime.datetime.now(datetime.timezone.utc).isoformat()


def save(name, obj):
    with (OUT / (name + '.json')).open('x') as f:
        json.dump(obj, f, ensure_ascii=False, indent=2)
        f.write('\n')


def load(name):
    return json.loads((OUT / (name + '.json')).read_text())


def run(name, argv, cwd=ROOT, env=None, timeout=25):
    record = {'at': now(), 'argv': argv, 'cwd': str(cwd), 'target': TARGET}
    start = time.monotonic()
    p = subprocess.run(argv, cwd=cwd, env=env, stdin=subprocess.DEVNULL, capture_output=True, text=True, timeout=timeout)
    record.update(exit_code=p.returncode, stdout=p.stdout, stderr=p.stderr, duration_ms=round((time.monotonic() - start) * 1000))
    save(name, record)
    print(json.dumps({'evidence': name, 'exit_code': p.returncode, 'stdout': p.stdout[:1200], 'stderr': p.stderr}, ensure_ascii=False))
    return record


def cmux(name, *args):
    return run(name, [CMUX, '--json', '--id-format', 'uuids', *args])


def target():
    return ['--window', TARGET['window_id'], '--workspace', TARGET['workspace_id'], '--surface', TARGET['surface_id']]


def screen(name):
    r = cmux(name, 'read-screen', *target(), '--scrollback', '--lines', '100')
    if r['exit_code'] == 0:
        data = json.loads(r['stdout'])
        text = data.get('text')
        if text is None:
            text = base64.b64decode(data['base64']).decode(errors='replace')
        save(name + '-decoded', {'at': now(), 'source': name + '.json', 'target': TARGET, 'text': text})
        print(text)


def env():
    allowed = ['HOME', 'USER', 'LOGNAME', 'PATH', 'TERM', 'COLORTERM', 'LANG', 'LC_ALL', 'SHELL', 'CMUX_SOCKET_PATH', 'CMUX_SOCKET_CAPABILITY']
    e = {key: os.environ[key] for key in allowed if key in os.environ}
    e.update(PLAN['environment_overrides'])
    e.update(CMUX_WINDOW_ID=TARGET['window_id'], CMUX_WORKSPACE_ID=TARGET['workspace_id'], CMUX_SURFACE_ID=TARGET['surface_id'], CMUX_PANEL_ID=TARGET['surface_id'])
    return e


def paste(name, text):
    assert cmux(name + '-text', 'send', '--paste', *target(), '--', text)['exit_code'] == 0
    assert cmux(name + '-enter', 'send-key', *target(), 'enter')['exit_code'] == 0


def rows():
    with sqlite3.connect((NATIVE_HOME / 'state_5.sqlite').as_uri() + '?mode=ro', uri=True) as db:
        return db.execute('select id,cwd,model_provider,rollout_path from threads order by created_at').fetchall()


def transcript(name, native):
    selected = [r for r in rows() if r[0] == native]
    records = []
    for row in selected:
        path = pathlib.Path(row[3])
        assert path.is_relative_to(NATIVE_HOME / 'sessions')
        for line in path.read_text().splitlines():
            rec = json.loads(line)
            payload = rec.get('payload', {})
            if rec['type'] == 'session_meta':
                rec = {**rec, 'payload': {k: payload.get(k) for k in ['id', 'cwd', 'originator', 'cli_version', 'source', 'model_provider']}}
            elif rec['type'] == 'response_item' and payload.get('role') in ['system', 'developer']:
                continue
            records.append(rec)
    result = {'at': now(), 'native_id': native, 'target': TARGET, 'read_only': True, 'rows': selected, 'records': records, 'credentials_read': False}
    save(name, result)
    print(json.dumps({'evidence': name, 'native_id': native, 'rows': selected, 'record_count': len(records), 'last_records': records[-2:]}, ensure_ascii=False))


def inventory(name):
    paths = sorted([p for directory in ['cmd', 'internal', 'probes', 'tests'] for p in (ROOT / directory).rglob('*.go')] + [ROOT / 'Makefile', ROOT / 'go.mod', ROOT / 'go.sum'])
    evidence = sorted(p for p in PRIOR.iterdir() if p.is_file())
    config = NATIVE_HOME / 'config.toml'
    save(name, {'at': now(), 'source_hashes': {str(p.relative_to(ROOT)): hashlib.sha256(p.read_bytes()).hexdigest() for p in paths}, 'prior_evidence_hashes': {str(p.relative_to(ROOT)): hashlib.sha256(p.read_bytes()).hexdigest() for p in evidence}, 'isolated_config_hash': hashlib.sha256(config.read_bytes()).hexdigest() if config.exists() else None, 'credentials_read': False})


stage = sys.argv[1]
if stage == 'initial':
    save('execution-policy', {'at': now(), 'session_id': 'fd8f8aac-71a7-4e9e-845e-8cd6b458e468', 'task_id': 's1-codex-final-edge', 'target': TARGET, 'original_native_id': ORIGINAL, 'scope': ['two-native-thread cwd/message/context isolation', 'controlled mid-execution recovery only if safely possible'], 'login_attempts': 0, 'credentials_copied': False, 'opencode_model_calls': 0, 'claude_operations': 0, 's2': False, 'new_surfaces': False, 'strategy': 'Use only the authorized surface sequentially for A and B; route queues explicitly by native ID, preserve all drafts, restore original A at end.', 'history_cli': 'ochist unavailable on PATH and ~/.local; supplied native evidence used', 'risk_cli': 'jev-judge unavailable on PATH and searched ~/.local, ~/.claude, ~/.agents, /opt/homebrew/bin; no judgment fabricated'})
    inventory('inventory-before')
    cmux('target-tree-initial', 'tree', '--window', TARGET['window_id'])
    screen('target-screen-initial')
    run('target-process-initial', ['/bin/ps', '-p', '70876,70874', '-o', 'pid=,ppid=,tty=,lstart=,command='])
    save('native-threads-initial', {'at': now(), 'read_only': True, 'rows': rows()})
    transcript('native-a-initial', ORIGINAL)
elif stage == 'screen':
    screen(sys.argv[2])
elif stage == 'native':
    transcript(sys.argv[2], sys.argv[3])
elif stage == 'rows':
    data = rows()
    save(sys.argv[2], {'at': now(), 'read_only': True, 'rows': data})
    print(json.dumps(data, ensure_ascii=False))
elif stage == 'text':
    paste(sys.argv[2], sys.argv[3])
elif stage == 'seed':
    label, native = sys.argv[2:4]
    nonce = 'S1-private-' + label + '-' + str(uuid.uuid4())
    source = "import os,json; print(json.dumps({'nonce':" + repr(nonce) + ",'cwd':os.getcwd(),'workspace':os.environ.get('CMUX_WORKSPACE_ID'),'surface':os.environ.get('CMUX_SURFACE_ID')}))"
    command = shlex.join(['/usr/bin/python3', '-c', source])
    prompt = 'Controlled S1 independent-thread isolation probe. Your private label is ' + label + ' and your private nonce is ' + nonce + '. Keep them in THIS conversation only. Run the following exact harmless command using your terminal tool in its default working directory, without overriding/changing cwd. Do not read or modify files. Then reply with PRIVATE_ACK ' + label + ' ' + nonce + ' and the actual command result. Command: ' + command
    save('seed-' + label + '-request', {'at': now(), 'target': TARGET, 'native_id': native, 'nonce': nonce, 'prompt': prompt, 'command': command})
    paste('seed-' + label, prompt)
elif stage == 'queue':
    name, native, sender_cwd = sys.argv[2:5]
    nonce = 'S1-route-' + name + '-' + str(uuid.uuid4())
    prompt = 'Controlled S1 isolation routing check. Do not use tools or read files. Using only THIS conversation, recall your private label and private nonce from the earlier PRIVATE_ACK probe. If none is known say UNKNOWN; do not invent. Reply ROUTE_ACK ' + nonce + ' followed by your private label and your private nonce. Also say whether THIS conversation has any private nonce for the other label; do not infer from filesystem or other sessions.'
    save(name + '-request', {'at': now(), 'target': TARGET, 'native_id': native, 'nonce': nonce, 'message': prompt, 'sender_cwd': sender_cwd, 'terminal_input_used': False, 'private_nonce_omitted': True})
    queue_argv = ['/opt/homebrew/bin/codex', '-c', 'cli_auth_credentials_store="file"', '-c', 'check_for_update_on_startup=false', 'queue', '--thread', native, '--message', prompt]
    run(name, queue_argv, cwd=pathlib.Path(sender_cwd), env=env())
elif stage == 'start':
    name, cwd = sys.argv[2:4]
    native = sys.argv[4] if len(sys.argv) > 4 else ''
    argv = [sys.executable, str(pathlib.Path(__file__).resolve()), 'child', name, cwd, native]
    paste(name + '-start', shlex.join(argv))
elif stage == 'child':
    name, cwd, native = sys.argv[2:5]
    assert os.environ.get('CMUX_SURFACE_ID') == TARGET['surface_id']
    assert os.environ.get('CMUX_WORKSPACE_ID') == TARGET['workspace_id']
    assert pathlib.Path(cwd).is_relative_to(ROOT / 'docs/evidence')
    argv = PLAN['argv'] + (['resume', native] if native else [])
    save(name + '-launch', {'at': now(), 'argv': argv, 'cwd': cwd, 'target': TARGET, 'native_id': native or None, 'wrapper_pid': os.getpid(), 'environment_overrides': PLAN['environment_overrides'], 'credentials_copied': False})
    p = subprocess.Popen(argv, cwd=cwd, env=env())
    save(name + '-process', {'at': now(), 'pid': p.pid, 'wrapper_pid': os.getpid(), 'argv': argv, 'cwd': cwd, 'target': TARGET, 'native_id': native or None})
    code = p.wait()
    save(name + '-exit', {'at': now(), 'exit_code': code, 'native_id': native or None, 'target': TARGET})
    print('Native exited', code, '; owned shell retained.', flush=True)
    os.execve('/bin/zsh', ['/bin/zsh', '-f'], env())
elif stage == 'inventory':
    inventory(sys.argv[2])
elif stage == 'busy':
    native = sys.argv[2]
    attempt = sys.argv[3] if len(sys.argv) > 3 else 'midflight'
    duration = 120 if attempt == 'midflight-retry' else 45
    nonce = 'S1-midflight-' + str(uuid.uuid4())
    source = "import os,time; print('MIDFLIGHT_BEGIN " + nonce + " pid='+str(os.getpid())+' cwd='+os.getcwd(),flush=True); time.sleep(" + str(duration) + "); print('MIDFLIGHT_END " + nonce + "',flush=True)"
    command = shlex.join(['/usr/bin/python3', '-c', source])
    prompt = 'Controlled S1 process-recovery probe in this disposable thread. Run this exact harmless command using your terminal tool in the default cwd. It only prints and sleeps for ' + str(duration) + ' seconds; do not read, write or modify any files or launch anything else. When it finishes reply MIDFLIGHT_DONE ' + nonce + '. Command: ' + command
    save(attempt + '-request', {'at': now(), 'target': TARGET, 'native_id': native, 'nonce': nonce, 'prompt': prompt, 'command': command, 'duration_seconds': duration, 'only_side_effect': 'stdout and bounded sleep'})
    paste(attempt + '-submit', prompt)
elif stage == 'kill-controlled-b':
    import signal
    process = load('b-resume-process')
    pid = process['pid']
    native = process['native_id']
    assert native and native != ORIGINAL
    request = load('midflight-retry-request')
    assert native == request['native_id']
    tree = cmux('pre-signal-tree', 'tree', '--window', TARGET['window_id'])
    tree_data = json.loads(tree['stdout'])
    surfaces = [s for w in tree_data['windows'] for ws in w['workspaces'] if ws['id'] == TARGET['workspace_id'] for pane in ws['panes'] for s in pane['surfaces'] if s['id'] == TARGET['surface_id']]
    assert len(surfaces) == 1 and surfaces[0]['tty'] == 'ttys028'
    expected = ' '.join(process['argv'])
    check = run('pre-signal-process', ['/bin/ps', '-p', str(pid), '-o', 'pid=,ppid=,tty=,lstart=,command='])
    assert check['exit_code'] == 0
    tokens = check['stdout'].split()
    assert tokens[0:3] == [str(pid), str(process['wrapper_pid']), 'ttys028']
    assert expected in check['stdout'] and 'resume ' + native in check['stdout']
    cwd_result = run('pre-signal-cwd', ['/usr/sbin/lsof', '-a', '-p', str(pid), '-d', 'cwd', '-Fn'])
    assert '\nn' + process['cwd'] + '\n' in cwd_result['stdout']
    tty = run('pre-signal-tty-processes', ['/bin/ps', '-t', 'ttys028', '-o', 'pid=,ppid=,tty=,stat=,command='])
    nonce = request['nonce']
    children = run('pre-signal-child-ids', ['/usr/bin/pgrep', '-P', str(pid)])
    child_ids = children['stdout'].split()
    assert child_ids and all(v.isdecimal() for v in child_ids)
    child_processes = run('pre-signal-child-processes', ['/bin/ps', '-p', ','.join(child_ids), '-o', 'pid=,ppid=,tty=,stat=,command='])
    matches = [line for line in child_processes['stdout'].splitlines() if nonce in line and 'time.sleep(120)' in line]
    assert len(matches) == 1
    assert matches[0].split()[1] == str(pid)
    tool_pid = int(matches[0].split()[0])
    screen('pre-signal-screen')
    transcript('native-b-pre-signal', native)
    records = load('native-b-pre-signal')['records']
    starts = [r for r in records if r.get('payload', {}).get('type') == 'task_started']
    completions = [r for r in records if r.get('payload', {}).get('type') in ['task_complete', 'task_aborted']]
    assert starts and (not completions or starts[-1]['timestamp'] > completions[-1]['timestamp'])
    # Recheck exact PID/PPID/TTY/argv after all slower evidence capture, immediately before signal.
    again = run('immediate-pre-signal-process', ['/bin/ps', '-p', str(pid), '-o', 'pid=,ppid=,tty=,lstart=,command='])
    assert again['stdout'] == check['stdout']
    tool_again = run('immediate-pre-signal-tool', ['/bin/ps', '-p', str(tool_pid), '-o', 'pid=,ppid=,tty=,stat=,command='])
    assert nonce in tool_again['stdout'] and tool_again['stdout'].split()[1] == str(pid)
    save('signal-safety-decision', {'at': now(), 'native_id': native, 'target': TARGET, 'pid': pid, 'tool_pid': tool_pid, 'wrapper_pid': process['wrapper_pid'], 'cwd': process['cwd'], 'tty': 'ttys028', 'argv_verified': True, 'native_turn_active': True, 'bounded_print_sleep_process_observed': True, 'original_thread_stopped_and_not_targeted': ORIGINAL, 'authorization': 'User explicitly authorized this bounded experiment when PID/TTY/native identity and no user-data impact are established.', 'risk_cli': 'Unavailable; no Jev judgment claimed. This is a disposable print/sleep process, no data operation or user draft is in flight.', 'signal': 'SIGKILL', 'signal_scope': 'single verified Codex B PID only; no process-group, shell, wrapper, original thread or cmux signal'})
    os.kill(pid, signal.SIGKILL)
    save('signal-result', {'at': now(), 'pid': pid, 'native_id': native, 'signal': 'SIGKILL', 'syscall_returned': True, 'is_ack': False})
elif stage == 'recovery':
    native = sys.argv[2]
    prompt = 'Controlled S1 recovery after forced termination during the harmless print/sleep tool command. Do not repeat the sleep. Recover your private label and private nonce from THIS conversation history; the nonce is deliberately omitted here. Run one harmless terminal command in the default cwd: /bin/pwd . Do not read or change files. Then deliver exactly RECOVERY_DELIVERED followed by your private label, full private nonce and the actual cwd. If history is unavailable say UNVERIFIED rather than inventing it.'
    assert load('seed-B-request')['nonce'] not in prompt
    save('recovery-request', {'at': now(), 'target': TARGET, 'native_id': native, 'prompt': prompt, 'private_nonce_omitted': True, 'requires_actual_tool_and_final_delivery': True})
    paste('recovery-submit', prompt)
elif stage == 'record':
    run(sys.argv[2], sys.argv[3:])
else:
    raise SystemExit('Unknown stage')
