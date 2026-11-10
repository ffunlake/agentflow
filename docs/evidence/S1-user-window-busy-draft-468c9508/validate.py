"""Assert recorded native evidence; does not interact with any terminal/model."""
import base64
import datetime
import hashlib
import importlib.util
import json
from pathlib import Path
import re
import sys

sys.dont_write_bytecode = True
HERE = Path(__file__).resolve().parent
spec = importlib.util.spec_from_file_location('recorder', HERE/'record.py')
h = importlib.util.module_from_spec(spec)
spec.loader.exec_module(h)
load, save = h.load, h.r.save


def text(event):
    return ''.join(c.get('text', '') for c in event['payload'].get('content', []))


def ms(value):
    return datetime.datetime.fromisoformat(value.replace('Z', '+00:00')).timestamp()*1000


def main():
    before, final = load('before-codex-native'), load('final-codex-native')
    events = final['events']
    delta = [e for e in events if e['line'] > max(e['line'] for e in before['events'])]
    messages = [e for e in delta if e['type'] == 'response_item'
                and e['payload'].get('type') == 'message']
    users = [e for e in messages if e['payload'].get('role') == 'user']
    assistants = [e for e in messages if e['payload'].get('role') == 'assistant']
    assert len(users) == 3 and len(assistants) == 3
    commands = [e for e in delta if e['payload'].get('item', {}).get('type') == 'CommandExecution']
    assert len(commands) == 1
    execution = commands[0]['payload']
    assert execution['thread_id'] == h.NATIVE['codex']
    assert execution['item']['status'] == 'completed' and execution['item']['exit_code'] == 0
    assert 'time.sleep(45)' in ' '.join(execution['item']['command'])
    assert execution['item']['cwd'] == 'file://'+str(h.r.WIKI)
    busy_nonce = load('busy-request')['nonce']
    assert 'BUSY_BEGIN '+busy_nonce in execution['item']['aggregated_output']
    assert 'BUSY_END '+busy_nonce in execution['item']['aggregated_output']
    assert 'pid=60102 cwd='+str(h.r.WIKI) in execution['item']['aggregated_output']
    busy_ack = [e for e in assistants if text(e) == 'BUSY_DONE '+busy_nonce]
    assert len(busy_ack) == 1
    completed = [e for e in delta if e['payload'].get('type') == 'task_complete'
                 and e['payload'].get('turn_id') == execution['turn_id']]
    assert len(completed) == 1
    busy_queue = load('busy-queue')
    assert execution['started_at_ms'] < ms(busy_queue['started_at'])
    assert ms(busy_queue['finished_at']) < execution['completed_at_ms']
    # Native first exec yielded a live tool session after 30 seconds, before enqueue.
    partial = [e for e in delta if e['payload'].get('type') == 'custom_tool_call_output'
               and '"session_id":46150' in json.dumps(e['payload'], ensure_ascii=False).replace('\\"', '"')]
    assert len(partial) == 1 and ms(partial[0]['timestamp']) < ms(busy_queue['started_at'])
    assert 'BUSY_BEGIN '+busy_nonce in json.dumps(partial[0]) and 'BUSY_END' not in json.dumps(partial[0])

    queues = {}
    for name in ('busy-queue', 'draft-queue'):
        request, result = load(name+'-request'), load(name)
        assert result['exit_code'] == 0 and not result['stderr']
        assert result['argv'][result['argv'].index('--thread')+1] == h.NATIVE['codex']
        assert not request['terminal_input_used'] and not request['resume_requested']
        nonce = request['nonce']
        received = [e for e in users if nonce in text(e)]
        replies = [e for e in assistants if text(e) == 'QUEUE_ACK '+nonce]
        assert len(received) == len(replies) == 1
        reply = replies[0]
        turn_id = reply['payload']['internal_chat_message_metadata_passthrough']['turn_id']
        started = [e for e in delta if e['payload'].get('type') == 'task_started'
                   and e['payload']['turn_id'] == turn_id]
        assert len(started) == 1 and started[0]['payload']['turn_attribution']['turn_trigger'] == 'queue'
        mirror = [e for e in delta if e['payload'].get('item', {}).get('id') == reply['payload']['id']]
        assert len(mirror) == 1 and mirror[0]['payload']['thread_id'] == h.NATIVE['codex']
        queue_id = re.search(r'Queued message ([\w-]+) for thread', result['stdout']).group(1)
        queues[name] = {'status': 'PASS', 'nonce': nonce, 'queue_id': queue_id,
                        'enqueued_between': [result['started_at'], result['finished_at']],
                        'native_user_id': received[0]['payload']['id'],
                        'native_assistant_id': reply['payload']['id'],
                        'native_turn_id': turn_id, 'turn_trigger': 'queue',
                        'queue_turn_started_at': started[0]['timestamp'],
                        'assistant_at': reply['timestamp'],
                        'canonical_user_count': len(received), 'canonical_assistant_count': len(replies)}
        if name == 'busy-queue':
            assert ms(completed[0]['timestamp']) < ms(started[0]['timestamp']) < ms(reply['timestamp'])

    marker = load('draft-marker')['marker']
    draft_inputs = []
    for name in ('draft-before-queue', 'draft-after-queue'):
        raw = json.loads(load(name)['stdout'])
        assert base64.b64decode(raw['base64']).decode() == raw['text']
        prompt = [line for line in raw['text'].splitlines() if line.startswith('› ')][-1]
        assert prompt == '› '+marker
        draft_inputs.append(prompt)
    assert draft_inputs[0] == draft_inputs[1]
    assert marker not in json.dumps(final)
    assert marker not in json.dumps(load('draft-after-queue-codex-native'))
    assert ms(load('draft-after-queue')['started_at']) > ms(queues['draft-queue']['assistant_at'])
    final_prompt = [line for line in load('final-codex-decoded')['text'].splitlines() if line.startswith('› ')][-1]
    assert final_prompt == '› Ask Codex to do anything'
    assert load('draft-clear-owned')['argv'][-1] == 'ctrl+u'
    assert ms(load('draft-clear-owned')['started_at']) > ms(load('draft-after-queue')['finished_at'])

    op_before, op_final = load('before-opencode-native'), load('final-opencode-native')
    for field in ('session', 'messages', 'parts', 'session_messages'):
        assert op_before[field] == op_final[field]
    assert load('before-opencode-decoded')['text'] == load('final-opencode-decoded')['text']
    assert load('opencode-listening-tcp')['exit_code'] == 1
    assert not load('opencode-listening-tcp')['stdout']
    raw = Path(final['path']).read_bytes()
    assert hashlib.sha256(raw[:before['bytes']]).hexdigest() == before['sha256']
    assert hashlib.sha256(raw).hexdigest() == final['sha256']
    for tool, (_, pid, tty) in h.r.TARGETS.items():
        assert any(str(pid) in line.split() and tty in line.split()
                   for line in load('final-processes')['stdout'].splitlines())
        assert 'n'+str(h.r.WIKI) in load('final-'+tool+'-cwd')['stdout']
    old_tree = json.loads(load('before-tree')['stdout'])['windows'][0]['workspaces'][0]
    new_tree = json.loads(load('final-tree')['stdout'])['windows'][0]['workspaces'][0]
    assert old_tree['layout'] == new_tree['layout']
    assert [(p['id'], p['surface_ids']) for p in old_tree['panes']] == [(p['id'], p['surface_ids']) for p in new_tree['panes']]
    for path in HERE.glob('*.json'):
        assert not re.search(r'mg_[0-9a-f]{20,}|sk-[A-Za-z0-9_-]{20,}', path.read_text()), path.name

    result = {'at': h.r.now(), 'status': 'PASS', 's1_status': 'PARTIAL',
              'scope': 'This exact original live Codex TUI and native queue; bounded once-only observations',
              'queues': queues,
              'busy': {'native_command_execution_id': execution['item']['id'],
                       'native_tool_session_id': execution['item']['process_id'],
                       'os_pid_from_actual_tool_output': 60102,
                       'started_at_ms': execution['started_at_ms'],
                       'completed_at_ms': execution['completed_at_ms'],
                       'busy_turn_id': execution['turn_id'],
                       'busy_done_assistant_id': busy_ack[0]['payload']['id'],
                       'busy_turn_completed_at': completed[0]['timestamp'],
                       'partial_live_tool_output_at': partial[0]['timestamp'],
                       'queue_inside_real_command_interval': True,
                       'queue_turn_after_busy_turn_complete': True,
                       'ps_sample_not_used_as_busy_proof': True},
              'draft': {'status': 'PASS', 'marker': marker, 'before_equals_after': True,
                        'canonical_native_user_occurrences': 0,
                        'present_after_real_assistant_ack': True,
                        'cleared_only_owned_draft': True, 'final_empty_prompt': True},
              'new_canonical_users': len(users), 'new_canonical_assistants': len(assistants),
              'new_command_executions': len(commands),
              'opencode': {'busy_delivery': 'UNVERIFIED', 'nonempty_draft_protection': 'UNVERIFIED',
                           'reason': 'No verified safe addressable external queue for this running original TUI',
                           'new_model_requests': 0, 'native_state_unchanged': True},
              'native_history_prefix_preserved': True, 'original_pids_ttys_cwd_layout_retained': True,
              'final_native_observed_at': final['at'],
              'no_general_exactly_once_or_agentflow_adapter_claim': True}
    save('evidence-validation', result)
    save('runtime-matrix', {'at': h.r.now(), 'collaboration_session_id': load('execution-policy')['session_id'],
                           'task_id': 's1-user-window-busy-draft', 's1_status': 'PARTIAL',
                           'window': h.r.WINDOW, 'workspace': h.r.WORKSPACE, 'cwd': str(h.r.WIKI),
                           'targets': h.r.TARGETS, 'native_ids': h.NATIVE,
                           'new_runtime_passes': ['codex.user_window_busy_native_queue',
                                                  'codex.user_window_nonempty_draft_native_queue'],
                           'codex': {'queues': queues, 'busy': result['busy'], 'draft': result['draft']},
                           'opencode': result['opencode'],
                           'unverified': ['OpenCode safe external queue/busy/draft', 'AgentFlow adapter',
                                          'universal draft gate', 'general exactly-once', 'automatic recovery'],
                           'historical_passes': 'Retain original environment boundaries; no duplicated cumulative count'})
    print(json.dumps(result, ensure_ascii=False, indent=2))


if __name__ == '__main__':
    main()
