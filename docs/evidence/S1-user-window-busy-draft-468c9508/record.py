"""Exclusive evidence recorder for the authorized S1 busy/draft experiment.

Reuses the previous recorder read-only, with all output redirected here.
Only the two explicitly authorized user surfaces can receive terminal input.
"""
import importlib.util
import json
import os
from pathlib import Path
import shlex
import sys
import uuid

sys.dont_write_bytecode = True
HERE = Path(__file__).resolve().parent
spec = importlib.util.spec_from_file_location(
    'prior_recorder', HERE.parent/'S1-user-window-reset-recovery-dc2eb54e/record.py')
r = importlib.util.module_from_spec(spec)
spec.loader.exec_module(r)
r.HERE = HERE
NATIVE = {'opencode': 'ses_e64c2fa39ffeI5sYf6iCWDKYpv',
          'codex': '01a19b3b-9fc2-7622-a43f-af895cfac312'}


def load(name):
    return json.loads((HERE/(name+'.json')).read_text())


def screen(name, provider):
    result = r.cmux(name, 'read-screen', provider, ['--scrollback', '--lines', '2000'])
    parsed = json.loads(result['stdout'])
    assert parsed['surface_id'] == r.TARGETS[provider][0]
    assert parsed['window_id'] == r.WINDOW and parsed['workspace_id'] == r.WORKSPACE
    r.save(name+'-decoded', {'at': r.now(), 'text': parsed['text']})
    print(parsed['text'])
    return parsed['text']


def native(stage):
    r.opencode(stage, NATIVE['opencode'])
    r.codex(stage, NATIVE['codex'])


def paste(name, provider, text, submit=False):
    assert provider in NATIVE
    result = r.cmux(name+'-text', 'send', provider, ['--paste', '--', text])
    assert result['exit_code'] == 0
    if submit:
        assert r.cmux(name+'-enter', 'send-key', provider, ['enter'])['exit_code'] == 0


def queue(name):
    nonce = 'S1-'+name+'-468c9508-'+str(uuid.uuid4())
    message = ('只读 S1 原生队列验证。不要调用工具、读取或修改文件、发送协作消息。'
               '请仅逐字回复：QUEUE_ACK '+nonce)
    r.save(name+'-request', {'at': r.now(), 'native_id': NATIVE['codex'],
           'nonce': nonce, 'message': message, 'terminal_input_used': False,
           'resume_requested': False})
    result = r.run(name, ['/opt/homebrew/bin/codex', '-c',
                  'check_for_update_on_startup=false', 'queue', '--thread',
                  NATIVE['codex'], '--message', message])
    print(json.dumps(result, ensure_ascii=False))


def discovery():
    r.save('execution-policy', {
        'at': r.now(), 'session_id': '468c9508-c700-42a7-9d91-81ffdeb46ff2',
        'task_id': 's1-user-window-busy-draft', 'mode': 'ralph-loop',
        'window': r.WINDOW, 'workspace': r.WORKSPACE,
        'targets': r.TARGETS, 'native_ids': NATIVE,
        'writes': 'New evidence directory and two authorized S1 summary documents only',
        'forbidden': ['reset', 'clear', 'new', 'exit', 'kill', 'fault injection',
                      'provider edits', 'wiki writes', 'Go edits', 'Claude interaction',
                      'experimental messages to collaboration windows'],
        'history_cli': 'ochist unavailable on PATH; previous supplied evidence read directly',
        'authorization': 'Explicit user authorization for busy and owned nonempty draft probes; unsafe interfaces remain UNVERIFIED'})
    r.protect('before')
    r.snapshot('before')
    native('before')
    r.run('opencode-listening-tcp', ['/usr/sbin/lsof','-a','-p','23097','-iTCP','-sTCP:LISTEN','-Pn'])
    r.run('opencode-unix-sockets', ['/usr/sbin/lsof','-a','-p','23097','-U','-Fn'])
    r.run('opencode-help', ['/opt/homebrew/bin/opencode','--help'])
    r.run('codex-queue-help', ['/opt/homebrew/bin/codex','queue','--help'])
    r.run('codex-open-files', ['/usr/sbin/lsof','-a','-p','23477','-Fn'])


def busy():
    nonce = 'S1-busy-468c9508-'+str(uuid.uuid4())
    source = ("import os,time; print('BUSY_BEGIN "+nonce+" pid='+str(os.getpid())+"
              "' cwd='+os.getcwd(),flush=True); time.sleep(45); "
              "print('BUSY_END "+nonce+"',flush=True)")
    command = shlex.join(['/usr/bin/python3', '-c', source])
    prompt = ('本轮是用户已授权的短时只读 busy 验证。只调用一次终端工具执行以下原样命令，'
              '等待其自然完成（若返回后台 session 则仅等待该 session 完成）；不要读写文件、'
              '运行 git、调用其他工具或发送协作消息。命令完成后仅回复 BUSY_DONE '+nonce+
              '。命令：'+command)
    r.save('busy-request', {'at': r.now(), 'nonce': nonce, 'command': command,
           'prompt': prompt, 'duration_seconds': 45, 'native_id': NATIVE['codex']})
    paste('busy-start', 'codex', prompt, True)


def draft():
    marker = 'S1_OWN_DRAFT_468c9508_'+uuid.uuid4().hex[:12]
    r.save('draft-marker', {'at': r.now(), 'marker': marker,
           'native_id': NATIVE['codex'], 'submitted': False})
    paste('draft-place', 'codex', marker)


if __name__ == '__main__':
    action, *args = sys.argv[1:]
    if action == 'discovery': discovery()
    elif action == 'native': native(*args)
    elif action == 'snapshot': r.snapshot(*args)
    elif action == 'screen': screen(*args)
    elif action == 'busy': busy()
    elif action == 'queue': queue(*args)
    elif action == 'draft': draft()
    elif action == 'key':
        name, provider, key = args
        assert key in ('enter', 'ctrl+u')
        print(r.cmux(name, 'send-key', provider, [key])['stdout'])
    elif action == 'text': paste(*args)
    elif action == 'command':
        name, *argv = args
        print(json.dumps(r.run(name, argv), ensure_ascii=False))
    elif action == 'protect': r.protect(*args)
    elif action == 'verify': r.verify()
    else: raise ValueError(action)
