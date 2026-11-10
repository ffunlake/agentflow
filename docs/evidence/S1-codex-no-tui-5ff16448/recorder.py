"""Scoped no-TUI queue observation; all evidence writes are exclusive."""
import base64
import datetime
import hashlib
import json
import os
from pathlib import Path
import shlex
import shutil
import sqlite3
import subprocess
import sys
import time
import uuid

ROOT = Path('/Users/funlake/Projects/agentflow')
OUT = Path(__file__).resolve().parent
PRIOR = ROOT / 'docs/evidence/S1-codex-interrupt-0cf09894'
PLAN = json.loads((ROOT/'docs/evidence/S1-manual-login-retry-a128f1ae/codex-plan.json').read_text())
HOME_NATIVE = Path(PLAN['environment_overrides']['CODEX_HOME'])
WINDOW = '2B9E10E2-0C0A-47DC-AED7-206B833A4462'
WORKSPACE = '28A52619-270D-4FC5-A692-C730028054E2'
SURFACE = '4E483942-928F-455C-B03D-75DAEDE9F38F'
THREAD = '01a19ad4-2958-75f3-82b6-f488745caec4'
B_THREAD = '01a19b0a-7662-7582-a5f0-36ea08fb1205'
SESSION = '5ff16448-f0a6-44a5-a827-350891d0429c'
CMUX = '/Applications/cmux.app/Contents/Resources/bin/cmux'
TARGET = ['--window', WINDOW, '--workspace', WORKSPACE, '--surface', SURFACE]


def now():
    return datetime.datetime.now(datetime.timezone.utc).isoformat()


def save(name, value):
    with (OUT/(name+'.json')).open('x') as f:
        json.dump(value, f, ensure_ascii=False, indent=2)
        f.write('\n')


def load(name):
    return json.loads((OUT/(name+'.json')).read_text())


def run(name, argv, cwd=ROOT, env=None, timeout=25):
    start = now()
    p = subprocess.run(argv, cwd=cwd, env=env, stdin=subprocess.DEVNULL,
                       capture_output=True, text=True, timeout=timeout)
    result = dict(start=start, end=now(), argv=argv, cwd=str(cwd),
                  exit_code=p.returncode, stdout=p.stdout, stderr=p.stderr)
    save(name, result)
    print(json.dumps(dict(evidence=name, exit_code=p.returncode, stderr=p.stderr)))
    return result


def cmux(name, *args):
    return run(name, [CMUX, '--json', '--id-format', 'uuids', *args])


def screen(name):
    result = cmux(name, 'read-screen', *TARGET, '--scrollback', '--lines', '100')
    assert result['exit_code'] == 0
    data = json.loads(result['stdout'])
    text = data.get('text') or base64.b64decode(data['base64']).decode(errors='replace')
    save(name+'-decoded', dict(at=now(), surface=SURFACE, text=text))
    print(json.dumps(dict(screen=name, suffix=text[-3500:]), ensure_ascii=False))
    return text


def paste(name, text):
    assert cmux(name+'-text', 'send', '--paste', *TARGET, '--', text)['exit_code'] == 0
    assert cmux(name+'-enter', 'send-key', *TARGET, 'enter')['exit_code'] == 0


def row(thread=THREAD):
    with sqlite3.connect((HOME_NATIVE/'state_5.sqlite').as_uri()+'?mode=ro', uri=True) as db:
        result = db.execute('select id,cwd,model_provider,rollout_path from threads where id=?', (thread,)).fetchone()
    assert result and Path(result[3]).is_relative_to(HOME_NATIVE/'sessions')
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
            item['payload'] = {k:p.get(k) for k in ['id','cwd','originator','cli_version','model_provider']}
        records.append(item)
    value = dict(at=now(), row=r, native_id=thread, records=records,
                 raw_bytes=len(raw), raw_sha256=hashlib.sha256(raw).hexdigest(), credentials_read=False)
    save(name, value)
    print(json.dumps(dict(native=name, bytes=len(raw), sha256=value['raw_sha256'])))
    return value


def child_env():
    env = {k:os.environ[k] for k in ['HOME','USER','LOGNAME','PATH','TERM','COLORTERM','LANG','LC_ALL','SHELL','CMUX_SOCKET_PATH','CMUX_SOCKET_CAPABILITY'] if k in os.environ}
    env.update(PLAN['environment_overrides'])
    env.update(CMUX_WINDOW_ID=WINDOW, CMUX_WORKSPACE_ID=WORKSPACE, CMUX_SURFACE_ID=SURFACE, CMUX_PANEL_ID=SURFACE)
    return env


def inventory(name):
    files = [p for d in ['cmd','internal','probes','tests'] for p in (ROOT/d).rglob('*.go')]
    files += [ROOT/p for p in ['Makefile','go.mod','go.sum']]
    files += [p for p in (ROOT/'docs/evidence').rglob('*') if p.is_file() and not p.is_relative_to(OUT) and 'scratch' not in p.relative_to(ROOT/'docs/evidence').parts]
    if (HOME_NATIVE/'config.toml').exists():
        files.append(HOME_NATIVE/'config.toml')
    save(name, dict(at=now(), hashes={str(p.relative_to(ROOT)):hashlib.sha256(p.read_bytes()).hexdigest() for p in sorted(set(files))}, credentials_read=False))


def snapshot(name):
    cmux(name+'-tree', 'tree', '--window', WINDOW, '--workspace', WORKSPACE)
    screen(name+'-screen')
    native(name+'-native')
    run(name+'-tty-processes', ['/bin/ps','-t','ttys028','-o','pid=,ppid=,pgid=,tty=,stat=,lstart=,command='])
    # No process environment is read; this also detects a daemon/background resume.
    p = subprocess.run(['/bin/ps','-axo','pid=,ppid=,tty=,stat=,command='], capture_output=True, text=True, check=True)
    save(name+'-codex-processes', dict(at=now(), command=['/bin/ps','-axo','pid=,ppid=,tty=,stat=,command='], matches=[line for line in p.stdout.splitlines() if '/codex' in line or THREAD in line], process_environments_read=False))


stage = sys.argv[1]
if stage == 'initial':
    inventory('inventory-before')
    save('execution-policy', dict(at=now(), session_id=SESSION, task_id='s1-codex-no-tui-wakeup', mode='ralph-loop', target=dict(window=WINDOW,workspace=WORKSPACE,surface=SURFACE,native_id=THREAD), nonce='S1-no-tui-'+str(uuid.uuid4()), observation_seconds=90, queue_resume=True, original_argv=PLAN['argv'], opencode_model_calls=0, claude_operations=0, os_signals=0, credentials_read=False, config_changes=False, go_changes=False, s2=False))
    save('cli-availability', dict(at=now(), ochist=shutil.which('ochist'), jev_judge=shutil.which('jev-judge'), typesafe_key_set=bool(os.getenv('TYPESAFE_API_KEY')), judgment='UNAVAILABLE; no fabricated judgment; explicit scoped reversible user authorization'))
    snapshot('initial')
    native('initial-B-native', B_THREAD)
    for label, thread in [('A',THREAD),('B',B_THREAD)]:
        raw = Path(row(thread)[3]).read_bytes()
        save('initial-'+label+'-raw-binding', dict(path=row(thread)[3],byte_count=len(raw),sha256=hashlib.sha256(raw).hexdigest()))
    run('initial-process', ['/bin/ps','-p','11697','-o','pid=,ppid=,pgid=,tty=,stat=,lstart=,command='])
    run('initial-cwd', ['/usr/sbin/lsof','-a','-p','11697','-d','cwd','-Fn'])
elif stage == 'status':
    paste(sys.argv[2], '/status')
elif stage == 'snapshot':
    snapshot(sys.argv[2])
elif stage == 'quit':
    text = screen('pre-quit-screen')
    assert THREAD in text and 'Ask Codex to do anything' in text
    p = run('pre-quit-process', ['/bin/ps','-p','11697','-o','pid=,ppid=,tty=,command='])
    assert THREAD in p['stdout'] and 'ttys028' in p['stdout']
    save('baseline-gate', dict(at=now(),native_id=THREAD,surface=SURFACE,visible=True,idle=True,empty_prompt=True,checked='screen, /status, ps, cwd, native rollout',manual_screen_review=True))
    paste('normal-quit', '/quit')
elif stage == 'queue':
    assert run('old-native-absent', ['/bin/ps','-p','11697','-o','pid=,command='])['exit_code'] == 1
    snapshot('no-tui-before-queue')
    text = load('no-tui-before-queue-screen-decoded')['text']
    assert 'Ask Codex to do anything' not in text[-700:]
    assert 'codex' not in load('no-tui-before-queue-tty-processes')['stdout'].lower().replace(str(OUT/'recorder.py'), '')
    nonce = load('execution-policy')['nonce']
    prompt = 'Controlled S1 no-TUI wakeup probe. Run exactly one harmless terminal command in your DEFAULT cwd: /bin/pwd . Do not read files, change configuration, or perform other work. Then reply exactly NO_TUI_ACK '+nonce+' followed by the actual cwd from the tool. If this message is only processed after manual resume, still answer the same way; timing is measured externally.'
    save('queue-request', dict(at=now(),native_id=THREAD,nonce=nonce,message=prompt,external_sender_cwd=str(ROOT),resume=True))
    run('queue-result', ['/opt/homebrew/bin/codex','queue','--thread',THREAD,'--message',prompt,'--resume'], env=child_env(), timeout=40)
elif stage == 'observe':
    # Fixed bounded observation, split into <=30-second calls for responsiveness.
    label = sys.argv[2]
    delay = int(sys.argv[3])
    assert 0 <= delay <= 30
    time.sleep(delay)
    snapshot(label)
elif stage == 'resume':
    snapshot('immediate-pre-manual-resume')
    text = load('immediate-pre-manual-resume-screen-decoded')['text']
    assert 'Ask Codex to do anything' not in text[-700:]
    assert run('pre-resume-old-native-absent', ['/bin/ps','-p','11697','-o','pid=,command='])['exit_code'] == 1
    save('manual-resume-boundary', dict(at=now(),native_id=THREAD,method='explicit original surface shell command',automatic=False))
    paste('manual-resume', shlex.join(['/usr/bin/python3',str(OUT/'recorder.py'),'child']))
elif stage == 'child':
    assert os.getenv('CMUX_SURFACE_ID') == SURFACE and os.getenv('CMUX_WORKSPACE_ID') == WORKSPACE
    argv = PLAN['argv'] + ['resume', THREAD]
    p = subprocess.Popen(argv,cwd=row()[1],env=child_env())
    save('resume-process',dict(at=now(),pid=p.pid,wrapper_pid=os.getpid(),argv=argv,cwd=row()[1],surface=SURFACE,native_id=THREAD,environment_overrides=PLAN['environment_overrides']))
    code = p.wait()
    save('resume-exit',dict(at=now(),exit_code=code,pid=p.pid))
elif stage == 'final':
    snapshot('final')
    native('final-B-native',B_THREAD)
    run('final-cwd', ['/usr/sbin/lsof','-a','-p',str(load('resume-process')['pid']),'-d','cwd','-Fn'])
    inventory('inventory-after')
elif stage == 'delta':
    before = load('initial-native')['raw_bytes']
    raw = Path(row()[3]).read_bytes()
    print(raw[before:].decode())
else:
    raise SystemExit('Unknown stage')
