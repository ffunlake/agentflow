"""Record one authorized native Ctrl-C experiment; never overwrite evidence."""
import atexit
import base64
import datetime
import hashlib
import json
import os
from pathlib import Path
import select
import shlex
import shutil
import signal
import sqlite3
import subprocess
import sys
import time
import uuid

ROOT = Path('/Users/funlake/Projects/agentflow')
OUT = Path(__file__).resolve().parent
PRIOR = ROOT / 'docs/evidence/S1-codex-concurrent-tui-754ba5a3'
PLAN = json.loads((ROOT / 'docs/evidence/S1-manual-login-retry-a128f1ae/codex-plan.json').read_text())
NATIVE_HOME = Path(PLAN['environment_overrides']['CODEX_HOME'])
WINDOW = '2B9E10E2-0C0A-47DC-AED7-206B833A4462'
WORKSPACE = '28A52619-270D-4FC5-A692-C730028054E2'
SURFACE = '4E483942-928F-455C-B03D-75DAEDE9F38F'
THREAD = '01a19ad4-2958-75f3-82b6-f488745caec4'
B_THREAD = '01a19b0a-7662-7582-a5f0-36ea08fb1205'
SESSION = '0cf09894-116f-4abe-b601-93b8572a86bb'
CMUX = '/Applications/cmux.app/Contents/Resources/bin/cmux'
TARGET = ['--window', WINDOW, '--workspace', WORKSPACE, '--surface', SURFACE]


def now():
    return datetime.datetime.now(datetime.timezone.utc).isoformat()


def save(name, value):
    with (OUT / (name + '.json')).open('x') as f:
        json.dump(value, f, ensure_ascii=False, indent=2)
        f.write('\n')


def load(name):
    return json.loads((OUT / (name + '.json')).read_text())


def run(name, argv, cwd=ROOT, env=None, timeout=20):
    start = now()
    p = subprocess.run(argv, cwd=cwd, env=env, stdin=subprocess.DEVNULL, capture_output=True, text=True, timeout=timeout)
    d = dict(start=start, end=now(), argv=argv, cwd=str(cwd), exit_code=p.returncode, stdout=p.stdout, stderr=p.stderr)
    save(name, d)
    print(json.dumps(dict(evidence=name, exit_code=p.returncode, stderr=p.stderr)))
    return d


def cmux(name, *args):
    return run(name, [CMUX, '--json', '--id-format', 'uuids', *args])


def paste(name, text):
    assert cmux(name+'-text', 'send', '--paste', *TARGET, '--', text)['exit_code'] == 0
    assert cmux(name+'-enter', 'send-key', *TARGET, 'enter')['exit_code'] == 0


def row(thread=THREAD):
    with sqlite3.connect((NATIVE_HOME / 'state_5.sqlite').as_uri()+'?mode=ro', uri=True) as db:
        result = db.execute('select id,cwd,model_provider,rollout_path from threads where id=?', (thread,)).fetchone()
    assert result and Path(result[3]).is_relative_to(NATIVE_HOME / 'sessions')
    return result


def native(name, thread=THREAD):
    r = row(thread)
    raw = Path(r[3]).read_bytes()
    records = []
    for line in raw.splitlines():
        item = json.loads(line)
        p = item.get('payload', {})
        if item['type'] == 'response_item' and p.get('role') in ['system', 'developer']:
            continue
        if item['type'] == 'session_meta':
            item['payload'] = {k: p.get(k) for k in ['id', 'cwd', 'originator', 'cli_version', 'model_provider']}
        records.append(item)
    save(name, dict(at=now(), native_id=thread, row=r, records=records, raw_bytes=len(raw), raw_sha256=hashlib.sha256(raw).hexdigest(), filtered_only_system_developer_messages=True, credentials_read=False))
    return records


def screen(name):
    r = cmux(name, 'read-screen', *TARGET, '--scrollback', '--lines', '130')
    assert r['exit_code'] == 0
    d = json.loads(r['stdout'])
    text = d.get('text') or base64.b64decode(d['base64']).decode(errors='replace')
    save(name+'-decoded', dict(at=now(), surface_id=SURFACE, text=text))
    print(json.dumps({'screen': name, 'suffix': text[-2400:]}, ensure_ascii=False))
    return text


def inventory(name):
    files = [p for d in ['cmd', 'internal', 'probes', 'tests'] for p in (ROOT/d).rglob('*.go')]
    files += [ROOT/p for p in ['Makefile', 'go.mod', 'go.sum']]
    files += [p for p in (ROOT/'docs/evidence').rglob('*') if p.is_file() and not p.is_relative_to(OUT) and 'scratch' not in p.relative_to(ROOT/'docs/evidence').parts]
    if (NATIVE_HOME/'config.toml').exists():
        files.append(NATIVE_HOME/'config.toml')
    save(name, dict(at=now(), hashes={str(p.relative_to(ROOT)): hashlib.sha256(p.read_bytes()).hexdigest() for p in sorted(set(files))}, credentials_read=False))


def snapshot(name):
    cmux(name+'-tree', 'tree', '--window', WINDOW, '--workspace', WORKSPACE)
    screen(name+'-screen')
    native(name+'-native')
    pid = load('resume-process')['pid'] if (OUT/'resume-process.json').exists() else 96559
    run(name+'-process', ['/bin/ps', '-p', str(pid), '-o', 'pid=,ppid=,pgid=,tty=,stat=,lstart=,command='])
    if (OUT/'tool-events.jsonl').exists():
        events = [json.loads(l) for l in (OUT/'tool-events.jsonl').read_text().splitlines()]
        save(name+'-tool-events', dict(at=now(), events=events))
        tool_pid = events[0]['pid']
        run(name+'-tool-process', ['/bin/ps', '-p', str(tool_pid), '-o', 'pid=,ppid=,pgid=,tty=,stat=,lstart=,command='])


def child_env():
    env = {k: os.environ[k] for k in ['HOME', 'USER', 'LOGNAME', 'PATH', 'TERM', 'COLORTERM', 'LANG', 'LC_ALL', 'SHELL', 'CMUX_SOCKET_PATH', 'CMUX_SOCKET_CAPABILITY'] if k in os.environ}
    env.update(PLAN['environment_overrides'])
    env.update(CMUX_WINDOW_ID=WINDOW, CMUX_WORKSPACE_ID=WORKSPACE, CMUX_SURFACE_ID=SURFACE, CMUX_PANEL_ID=SURFACE)
    return env


def append_event(filename, event, nonce):
    d = dict(at=now(), event=event, nonce=nonce, pid=os.getpid(), ppid=os.getppid(), pgid=os.getpgid(0), cwd=os.getcwd(), surface=os.getenv('CMUX_SURFACE_ID'), workspace=os.getenv('CMUX_WORKSPACE_ID'))
    with (OUT/filename).open('a') as f:
        f.write(json.dumps(d)+'\n')
        f.flush()
        os.fsync(f.fileno())
    return d


stage = sys.argv[1]
if stage == 'initial':
    inventory('inventory-before')
    save('execution-policy', dict(at=now(), session_id=SESSION, task_id='s1-codex-interrupt-semantics', mode='ralph-loop', target=dict(window=WINDOW, workspace=WORKSPACE, surface=SURFACE, native_id=THREAD), nonce='S1-interrupt-'+str(uuid.uuid4()), long_seconds=240, interrupt='One native Ctrl-C via cmux send-key ctrl+c while identified tool is running; require actual native aborted event and PID exit', recovery='Normal quit only after native abort and idle; manually resume same thread and send exactly one fresh nonce request', opencode_model_calls=0, claude_operations=0, explicit_kill_signals=0, credentials_read=False, provider_config_changes=False, go_changes=False, s2=False))
    save('cli-availability', dict(at=now(), ochist=shutil.which('ochist'), jev_judge=shutil.which('jev-judge'), judgment='UNAVAILABLE; no judgment invented; explicit scoped user authorization', typesafe_key_set=bool(os.getenv('TYPESAFE_API_KEY'))))
    snapshot('initial')
    native('initial-B-native', B_THREAD)
    for label, thread in [('A', THREAD), ('B', B_THREAD)]:
        raw = Path(row(thread)[3]).read_bytes()
        save('initial-'+label+'-raw-binding', dict(path=row(thread)[3], byte_count=len(raw), sha256=hashlib.sha256(raw).hexdigest()))
    run('initial-cwd', ['/usr/sbin/lsof', '-a', '-p', '96559', '-d', 'cwd', '-Fn'])
elif stage == 'status':
    paste(sys.argv[2], '/status')
elif stage == 'snapshot':
    snapshot(sys.argv[2])
elif stage == 'request':
    text = screen('request-preflight-screen')
    assert 'Ask Codex to do anything' in text and THREAD in text
    p = run('request-preflight-process', ['/bin/ps', '-p', '96559', '-o', 'pid=,ppid=,tty=,command='])
    assert THREAD in p['stdout'] and 'ttys028' in p['stdout']
    nonce = load('execution-policy')['nonce']
    command = shlex.join(['/usr/bin/python3', str(OUT/'recorder.py'), 'tool', nonce])
    prompt = 'Controlled S1 native interruption probe. Run exactly ONCE using your terminal tool in its DEFAULT cwd: '+command+'. This harmless tool only appends its own lifecycle events under its evidence directory and sleeps for 240 seconds. Use yield_time_ms=1000; if still running, wait for this same tool, never restart/retry it or run any other command. Remember its nonce in THIS conversation. Only if the tool actually finishes naturally with TOOL_COMPLETE, reply INTERRUPT_TOOL_DONE followed by its nonce. If interrupted/cancelled, do not claim completion, do not rerun it, and await the next user request. No unrelated work or file access.'
    save('interrupt-request', dict(at=now(), native_id=THREAD, nonce=nonce, command=command, message=prompt))
    paste('interrupt-request', prompt)
elif stage == 'tool':
    nonce = sys.argv[2]
    assert nonce == load('execution-policy')['nonce']
    # Exclusive startup guard makes an accidental second invocation observable.
    with (OUT/'tool-once.json').open('x') as f:
        json.dump(dict(at=now(), nonce=nonce, pid=os.getpid()), f)
    def caught(signum, frame):
        append_event('tool-events.jsonl', signal.Signals(signum).name, nonce)
        raise SystemExit(128+signum)
    signal.signal(signal.SIGINT, caught)
    signal.signal(signal.SIGTERM, caught)
    atexit.register(append_event, 'tool-events.jsonl', 'PYTHON_EXIT', nonce)
    print('TOOL_BEGIN '+json.dumps(append_event('tool-events.jsonl', 'TOOL_BEGIN', nonce)), flush=True)
    time.sleep(240)
    print('TOOL_COMPLETE '+json.dumps(append_event('tool-events.jsonl', 'TOOL_COMPLETE', nonce)), flush=True)
elif stage == 'watch':
    tool = load('tool-once')
    pid = tool['pid']
    q = select.kqueue()
    event = select.kevent(pid, filter=select.KQ_FILTER_PROC, flags=select.KQ_EV_ADD | select.KQ_EV_ENABLE | select.KQ_EV_ONESHOT, fflags=select.KQ_NOTE_EXIT)
    q.control([event], 0, 0)
    save('tool-exit-watcher-ready', dict(at=now(), watcher_pid=os.getpid(), pid=pid, source='Darwin EVFILT_PROC / NOTE_EXIT', signal_sent=False))
    events = q.control(None, 1, 300)
    save('tool-kernel-exit', dict(at=now(), pid=pid, events=[dict(ident=e.ident, filter=e.filter, flags=e.flags, fflags=e.fflags, data=e.data, udata=e.udata) for e in events], signal_sent=False, status_data_semantics='Raw Darwin NOTE_EXIT data retained; do not infer POSIX wait status unless kernel supplies it'))
elif stage == 'arm':
    pid = load('tool-once')['pid']
    p = run('arm-tool-process', ['/bin/ps', '-p', str(pid), '-o', 'pid=,ppid=,pgid=,tty=,stat=,lstart=,command='])
    assert str(OUT/'recorder.py') in p['stdout'] and load('execution-policy')['nonce'] in p['stdout']
    p = subprocess.Popen([sys.executable, str(OUT/'recorder.py'), 'watch'], stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, start_new_session=True)
    save('tool-exit-watcher-launch', dict(at=now(), pid=p.pid, target_pid=pid))
elif stage == 'interrupt':
    assert load('tool-exit-watcher-ready')['pid'] == load('tool-once')['pid']
    snapshot('immediate-pre-interrupt')
    assert load('immediate-pre-interrupt-tool-process')['exit_code'] == 0
    text = load('immediate-pre-interrupt-screen-decoded')['text']
    assert 'esc to interrupt' in text.lower() or 'Running' in text
    save('native-interrupt-action', dict(at=now(), method='cmux send-key', key='ctrl+c', native_id=THREAD, surface_id=SURFACE, tool_pid=load('tool-once')['pid'], explicit_os_signal=False, attempts=1))
    assert cmux('native-ctrl-c', 'send-key', *TARGET, 'ctrl+c')['exit_code'] == 0
elif stage == 'quit':
    text = screen('pre-quit-screen')
    assert 'Ask Codex to do anything' in text
    assert load('post-stop-tool-process')['exit_code'] == 1
    paste('normal-quit', '/quit')
elif stage == 'stop':
    snapshot('pre-stop')
    assert load('pre-stop-tool-process')['exit_code'] == 0
    assert '1 background terminal running' in load('pre-stop-screen-decoded')['text']
    save('native-stop-action', dict(at=now(), native_id=THREAD, surface_id=SURFACE, command='/stop', reason='Native Ctrl-C aborted conversation but left the single owned background tool running; use TUI advertised native cleanup', explicit_os_signal=False))
    paste('native-stop', '/stop')
elif stage == 'resume':
    assert run('old-native-absent', ['/bin/ps', '-p', '96559', '-o', 'pid=,command='])['exit_code'] == 1
    text = screen('pre-resume-shell-screen')
    assert 'Ask Codex to do anything' not in text[-600:]
    paste('manual-resume', shlex.join([sys.executable, str(OUT/'recorder.py'), 'child']))
elif stage == 'child':
    assert os.getenv('CMUX_SURFACE_ID') == SURFACE and os.getenv('CMUX_WORKSPACE_ID') == WORKSPACE
    argv = PLAN['argv'] + ['resume', THREAD]
    p = subprocess.Popen(argv, cwd=row()[1], env=child_env())
    save('resume-process', dict(at=now(), pid=p.pid, wrapper_pid=os.getpid(), argv=argv, cwd=row()[1], native_id=THREAD, surface_id=SURFACE, environment_overrides=PLAN['environment_overrides']))
    code = p.wait()
    save('resume-exit', dict(at=now(), pid=p.pid, exit_code=code, native_id=THREAD))
elif stage == 'recovery-request':
    text = screen('pre-recovery-screen')
    assert THREAD in text and 'Ask Codex to do anything' in text
    nonce = 'S1-resume-once-'+str(uuid.uuid4())
    command = shlex.join(['/usr/bin/python3', str(OUT/'recorder.py'), 'recovery-tool', nonce])
    prompt = 'Controlled S1 post-interrupt MANUAL resume check. Use only THIS conversation to recall the nonce of the interrupted long tool and the earlier concurrent private label A nonce; neither value is supplied in this request. Do not repeat or complete the interrupted tool. Run this new harmless command exactly ONCE in DEFAULT cwd, no retries and no other commands: '+command+'. Then reply RESUME_ONCE_ACK '+nonce+' followed by both recalled full nonces, whether the long tool actually completed or was interrupted, and the actual new tool JSON result. Do not read other files or sessions.'
    assert load('execution-policy')['nonce'] not in prompt
    save('recovery-request', dict(at=now(), nonce=nonce, native_id=THREAD, command=command, message=prompt, interrupted_nonce_omitted=True, historical_private_nonce_omitted=True))
    paste('recovery-request', prompt)
elif stage == 'recovery-tool':
    nonce = sys.argv[2]
    assert nonce == load('recovery-request')['nonce']
    append_event('recovery-attempts.jsonl', 'EXECUTION_ATTEMPT', nonce)
    result = dict(at=now(), nonce=nonce, pid=os.getpid(), cwd=os.getcwd(), surface=os.getenv('CMUX_SURFACE_ID'), workspace=os.getenv('CMUX_WORKSPACE_ID'))
    with (OUT/'recovery-once.json').open('x') as f:
        json.dump(result, f, indent=2)
    print(json.dumps(result), flush=True)
elif stage == 'inventory':
    inventory(sys.argv[2])
elif stage == 'final':
    snapshot('final')
    native('final-B-native', B_THREAD)
    pid = load('resume-process')['pid']
    run('final-cwd', ['/usr/sbin/lsof', '-a', '-p', str(pid), '-d', 'cwd', '-Fn'])
    run('final-watcher-process', ['/bin/ps', '-p', str(load('tool-exit-watcher-launch')['pid']), '-o', 'pid=,ppid=,stat=,command='])
    save('original-wrapper-exit', json.loads((ROOT/'docs/evidence/S1-codex-final-edge-fd8f8aac/a-final-exit.json').read_text()))
    result = {}
    for label, thread in [('A', THREAD), ('B', B_THREAD)]:
        old = load('initial-'+label+'-raw-binding')
        raw = Path(row(thread)[3]).read_bytes()
        result[label] = dict(native_id=thread, original_byte_count=old['byte_count'], final_byte_count=len(raw), original_sha256=old['sha256'], prefix_sha256=hashlib.sha256(raw[:old['byte_count']]).hexdigest(), final_sha256=hashlib.sha256(raw).hexdigest())
        result[label]['original_prefix_preserved'] = result[label]['prefix_sha256'] == old['sha256']
        result[label]['unchanged'] = result[label]['final_sha256'] == old['sha256']
    save('native-history-preservation', dict(at=now(), threads=result))
    inventory('inventory-after')
elif stage == 'inspect':
    records = load(sys.argv[2])['records']
    baseline = len(load('initial-native')['records'])
    for r in records[baseline:]:
        p = r.get('payload', {})
        if r['type'] in ['world_state', 'turn_context', 'token_usage_record'] or p.get('type') in ['token_count', 'thread_settings_applied']:
            continue
        print(json.dumps(r, ensure_ascii=False)[:5000])
elif stage == 'handoff':
    for name in ['evidence-validation', 'protected-files-verification', 'document-validation', 'target-safety-verification', 'pre-handoff-hash-verification']:
        assert load(name)['status'] == 'PASS'
    result = run('handoff', [
        '/Users/funlake/.codex/skills/okra-agent-init/scripts/agent-handoff.sh',
        'send', '--window', '1', '--session-id', SESSION, '--status', 'DONE',
        '--summary', '本轮调查交付完成，功能不记整体 PASS：Codex 原生 Ctrl-C 已触发 turn_aborted/interrupted，但 yielded 后台工具 PID 10800 在中断后至少 66 秒仍存活（FAIL_OBSERVED）。追加 TUI 原生 /stop 后 kernel NOTE_EXIT + native failed/exit=-1 + ps 不存在证实工具结束；未发送外部 kill/SIGKILL。完整 S1 PARTIAL，保留历史 19 项，新 runtime pass=0。',
        '--evidence', str(OUT)+'/README.md；runtime-matrix.json、dependencies.json、codex-verification.json、native-transcript-delta.json、evidence-validation.json、protected-files-verification.json、target-safety-verification.json、document-validation.json。实际中断动作、原始屏幕、PID/PPID/PGID、内核退出事件与同 thread 手动恢复保留。新 nonce S1-resume-once-d0bb72df-547a-4abd-b811-3358e9154b6c 仅一条 user message/一次 exec/一次尝试/唯一 native RESUME_ONCE_ACK；上下文 nonce 未重发，结果同屏。观察 415 秒超原 240 秒期限，无原工具完成/重放；1061 个既有文件对账通过，仅两份授权 S1 文档改变、旧 wrapper 新增 a-final-exit.json。',
        '--next', '仅继续 S1；取消契约必须区分 Ctrl-C turn 中断与显式 /stop 工具终止。具体终止 OS signal、通用 exactly-once、自动/主机恢复仍 UNVERIFIED。最终原 A/native/surface/cwd/TTY 保留，Codex PID 11697 存活且空输入栏，拓扑恢复。OpenCode 模型调用 0、Claude 无操作、未改 Go/配置、未运行 Go 检查或 doctor、不进入 S2。哈希清单与核验随本轮证据归档；DONE 是调查交付状态，不是 native ACK 或功能整体通过。'
    ])
    print(result['stdout'], end='')
    assert result['exit_code'] == 0 and '[DONE] handoff sent' in result['stdout']
else:
    raise SystemExit('Unknown stage')
