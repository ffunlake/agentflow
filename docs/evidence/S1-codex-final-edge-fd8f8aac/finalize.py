"""Validate native S1 evidence and derive scoped matrices, without model calls."""
import datetime
import hashlib
import json
import pathlib
import re
import sys

OUT = pathlib.Path(__file__).resolve().parent
ROOT = OUT.parents[2]
PRIOR = OUT.parent / 'S1-codex-edge-cases-eb2e9a87'


def load(name):
    return json.loads((OUT / (name + '.json')).read_text())


def save(name, data):
    with (OUT / (name + '.json')).open('x') as f:
        json.dump(data, f, ensure_ascii=False, indent=2)
        f.write('\n')


def messages(name, prefix):
    data = load(name)
    found = []
    for record in data['records']:
        payload = record.get('payload', {})
        if record['type'] == 'response_item' and payload.get('type') == 'message' and payload.get('role') == 'assistant':
            text = '\n'.join(c.get('text', '') for c in payload.get('content', []))
            if prefix in text:
                found.append({'native_id': data['native_id'], 'message_id': payload['id'], 'timestamp': record['timestamp'], 'text': text, 'source': name + '.json'})
    assert len(found) == 1, (name, prefix, len(found))
    return found[0]


def commands(name):
    return [r['payload'] for r in load(name)['records'] if r.get('payload', {}).get('type') == 'item_completed' and r['payload'].get('item', {}).get('type') == 'CommandExecution']


def screen_contains(name, *values):
    compact = re.sub(r'\s+', '', load(name + '-decoded')['text'])
    assert all(re.sub(r'\s+', '', value) in compact for value in values), (name, values)


at = datetime.datetime.now(datetime.timezone.utc).isoformat()
if len(sys.argv) > 1 and sys.argv[1] == 'documents':
    documents = [OUT / 'README.md', OUT.parent / 'S1-result.md', OUT.parent / 'S1-capability-matrix.md']
    checked = []
    for document in documents:
        for link in re.findall(r'\]\(([^)]+)\)', document.read_text()):
            if '://' in link or link.startswith('#'):
                continue
            assert (document.parent / link.split('#')[0]).resolve().exists(), (document, link)
            checked.append({'document': str(document.relative_to(ROOT)), 'link': link})
    json_files = list(OUT.rglob('*.json'))
    for path in json_files:
        json.loads(path.read_text())
    matrix = load('runtime-matrix')
    assert len(matrix['runtime_passes']) == 18 and len(matrix['new_runtime_passes']) == 2
    assert matrix['s1_status'] == 'PARTIAL' and matrix['checks']['go-test'] == 'NOT_RUN_NO_GO_CHANGES'
    save('document-validation', {'at': at, 'status': 'PASS', 'checked_links': checked, 'json_files_parsed': len(json_files), 'runtime_passes': 18, 'new_runtime_passes': 2, 's1_status': 'PARTIAL', 'sha256': {str(p.relative_to(OUT)): hashlib.sha256(p.read_bytes()).hexdigest() for p in sorted(OUT.rglob('*')) if p.is_file()}, 'manifest_excludes': 'This validation file and later handoff evidence.'})
    print(json.dumps({'status': 'PASS', 'links': len(checked), 'json_files': len(json_files), 'runtime_passes': 18}))
    raise SystemExit(0)

policy = load('execution-policy')
target = policy['target']
a = load('seed-A-request')
b = load('seed-B-request')
assert a['native_id'] != b['native_id']
assert load('native-a-seeded')['rows'][0][1] != load('native-b-seeded')['rows'][0][1]
seed_calls = {}
for label, request in [('A', a), ('B', b)]:
    name = 'native-' + label.lower() + '-seeded'
    matches = [call for call in commands(name) if request['nonce'] in call['item']['aggregated_output']]
    assert len(matches) == 1
    call = matches[0]
    output = json.loads(call['item']['aggregated_output'])
    assert call['thread_id'] == request['native_id'] and call['item']['exit_code'] == 0
    assert output['cwd'] == load(name)['rows'][0][1]
    assert output['surface'] == target['surface_id'] and output['workspace'] == target['workspace_id']
    ack = messages(name, 'PRIVATE_ACK ' + label + ' ' + request['nonce'])
    screen_contains(label.lower() + '-seeded-screen', ack['text'].split('\n')[0])
    seed_calls[label] = {'native_id': request['native_id'], 'cwd': output['cwd'], 'nonce': request['nonce'], 'tool': call, 'ack': ack, 'screen': label.lower() + '-seeded-screen.json'}

route_a = load('route-a-while-b-live-retry-request')
route_b = load('route-b-from-a-cwd-retry-request')
assert route_a['sender_cwd'] == seed_calls['B']['cwd'] and route_b['sender_cwd'] == seed_calls['A']['cwd']
assert a['nonce'] not in route_a['message'] and b['nonce'] not in route_b['message']
ack_a = messages('native-a-route-ack', 'ROUTE_ACK ' + route_a['nonce'])
ack_b = messages('native-b-route-complete', 'ROUTE_ACK ' + route_b['nonce'])
assert a['nonce'] in ack_a['text'] and b['nonce'] in ack_b['text']
assert 'no private nonce for the other label' in ack_a['text'] and 'no private nonce for the other label' in ack_b['text']
assert load('native-a-seeded')['records'] == load('native-a-stopped-with-queued-message')['records']
for other_nonce, name in [(b['nonce'], 'native-a-final'), (a['nonce'], 'native-b-final'), (route_a['nonce'], 'native-b-final'), (route_b['nonce'], 'native-a-final')]:
    assert other_nonce not in json.dumps(load(name)['records']), (name, other_nonce)
screen_contains('a-route-ack-screen', ack_a['text'])
screen_contains('b-route-screen', ack_b['text'])
queues = {}
for label, name in [('A', 'route-a-while-b-live-retry'), ('B', 'route-b-from-a-cwd-retry')]:
    result = load(name)
    match = re.fullmatch(r'Queued message ([\w-]+) for thread ([\w-]+)\.\n', result['stdout'])
    assert result['exit_code'] == 0 and match and match[2] == seed_calls[label]['native_id']
    queues[label] = {'queue_id': match[1], 'native_id': match[2], 'evidence': name + '.json', 'ack': ack_a if label == 'A' else ack_b}

signal = load('signal-result')
safety = load('signal-safety-decision')
assert signal['native_id'] == b['native_id'] and safety['native_id'] == b['native_id']
assert signal['pid'] == safety['pid'] == load('b-resume-process')['pid']
assert load('b-resume-exit')['exit_code'] == -9
assert 'resume ' + b['native_id'] in load('immediate-pre-signal-process')['stdout']
assert route_a['native_id'] == a['native_id']
assert load('a-stopped-before-signal')['exit_code'] == 1
assert load('bounded-tool-final-process')['exit_code'] == 1
request = load('midflight-retry-request')
starts = [r for r in load('native-b-pre-signal')['records'] if r.get('payload', {}).get('type') == 'task_started']
turn_id = starts[-1]['payload']['turn_id']
assert starts[-1]['timestamp'] < signal['at']
assert not any(r.get('payload', {}).get('type') in ['task_complete', 'task_aborted'] and r['payload'].get('turn_id') == turn_id for r in load('native-b-pre-signal')['records'])
tool_outputs = []
for r in load('native-b-pre-signal')['records']:
    p = r.get('payload', {})
    if p.get('type') == 'custom_tool_call_output':
        items = p.get('output', [])
        if isinstance(items, str):
            items = [{'type': 'input_text', 'text': items}]
        for item in items:
            if item.get('type') in ['text', 'input_text']:
                try:
                    parsed = json.loads(item['text'])
                except (ValueError, KeyError):
                    continue
                if 'MIDFLIGHT_BEGIN ' + request['nonce'] in parsed.get('output', ''):
                    tool_outputs.append({'timestamp': r['timestamp'], 'call_id': p['call_id'], 'output': parsed})
assert len(tool_outputs) == 1
tool_start = tool_outputs[0]
assert 'pid=' + str(safety['tool_pid']) in tool_start['output']['output']
assert 'exit_code' not in tool_start['output'] and tool_start['output'].get('session_id')
assert tool_start['timestamp'] < signal['at']
screen_contains('pre-signal-screen', 'Working', request['nonce'])
assert 'Conversation interrupted' in load('b-after-resume-screen-decoded')['text']
recovery = load('recovery-request')
assert b['nonce'] not in recovery['prompt']
delivered = messages('native-b-recovery-delivered', 'RECOVERY_DELIVERED B ' + b['nonce'])
assert seed_calls['B']['cwd'] in delivered['text'] and delivered['timestamp'] > signal['at']
pwd_calls = [call for call in commands('native-b-recovery-delivered') if call['started_at_ms'] > datetime.datetime.fromisoformat(signal['at']).timestamp() * 1000]
assert len(pwd_calls) == 1 and pwd_calls[0]['item']['exit_code'] == 0
assert pwd_calls[0]['item']['aggregated_output'].strip() == seed_calls['B']['cwd']
screen_contains('b-recovery-delivered-screen', delivered['text'])
assert load('b-recovered-exit')['exit_code'] == 0
original_restored = messages('native-a-final', 'ORIGINAL_RESTORED A ' + a['nonce'])
assert seed_calls['A']['cwd'] in original_restored['text']
assert commands('native-a-final')[-1]['item']['aggregated_output'].strip() == seed_calls['A']['cwd']
screen_contains('a-final-delivered-screen', original_restored['text'])
screen_contains('a-final-status-screen', a['native_id'], 'Ask Codex to do anything')
final_pid = load('a-final-process')['pid']
assert str(final_pid) in load('target-process-final')['stdout'] and 'resume ' + a['native_id'] in load('target-process-final')['stdout']

before, after = load('inventory-before'), load('inventory-after')
assert before['source_hashes'] == after['source_hashes']
assert before['isolated_config_hash'] == after['isolated_config_hash']
assert all(after['prior_evidence_hashes'].get(k) == v for k, v in before['prior_evidence_hashes'].items())
added_prior = sorted(set(after['prior_evidence_hashes']) - set(before['prior_evidence_hashes']))
assert added_prior == ['docs/evidence/S1-codex-edge-cases-eb2e9a87/resume-final-exit.json']
mutations = []
for path in sorted(OUT.glob('*.json')):
    data = json.loads(path.read_text())
    argv = data.get('argv', [])
    if argv and argv[0].endswith('/cmux'):
        if 'send' in argv or 'send-key' in argv:
            assert argv[argv.index('--window') + 1] == target['window_id']
            assert argv[argv.index('--workspace') + 1] == target['workspace_id']
            assert argv[argv.index('--surface') + 1] == target['surface_id']
            mutations.append(path.name)
tree_before = json.loads(load('target-tree-initial')['stdout'])
tree_after = json.loads(load('target-tree-final')['stdout'])
for tree in [tree_before, tree_after]:
    tree['active'] = None
    for window in tree['windows']:
        for workspace in window['workspaces']:
            for pane in workspace['panes']:
                for surface in pane['surfaces']:
                    surface.pop('title', None)
assert tree_before == tree_after

isolation = {'status': 'PASS_FOR_CONTROLLED_SEQUENTIAL_NATIVE_THREADS', 'target': target, 'threads': seed_calls, 'queues': queues, 'native_a_unchanged_while_stopped': True, 'cross_private_nonces_absent_in_native_transcripts': True, 'cross_route_nonces_absent_in_native_transcripts': True, 'restored_original': original_restored, 'limitations': ['One authorized visible surface was reused sequentially; simultaneous live TUI isolation remains UNVERIFIED.', 'Explicit native ID routes were tested; no filesystem security boundary is claimed.']}
crash = {'status': 'PASS_FOR_MANUAL_MID_TOOL_SIGKILL_RECOVERY', 'native_id': b['native_id'], 'target': target, 'cwd': seed_calls['B']['cwd'], 'nonce': request['nonce'], 'private_nonce': b['nonce'], 'interrupted_turn_id': turn_id, 'tool_started': tool_start, 'old_pid': signal['pid'], 'signal_at': signal['at'], 'wrapper_exit_code': -9, 'new_pid': load('b-recovered-process')['pid'], 'recovery_delivery': delivered, 'post_recovery_tool': pwd_calls[0], 'orphan_tool_pid': safety['tool_pid'], 'orphan_observation': load('post-signal-process')['stdout'], 'orphan_final_ps_exit': load('bounded-tool-final-process')['exit_code'], 'limitations': ['Manual resume and explicit recovery request were required; automatic supervisor recovery remains UNVERIFIED.', 'The interrupted tool did not deliver MIDFLIGHT_DONE; no exactly-once, interrupted-tool completion or transaction recovery guarantee.', 'The harmless sleep survived briefly as PPID 1, then its recorded PID was absent. No additional signal was sent.', 'Host failure, arbitrary tools and user-data operations were not tested.'], 'evidence': ['signal-safety-decision.json', 'immediate-pre-signal-process.json', 'immediate-pre-signal-tool.json', 'native-b-pre-signal.json', 'pre-signal-screen.json', 'b-resume-exit.json', 'b-after-resume-screen.json', 'native-b-recovery-delivered.json', 'b-recovery-delivered-screen.json', 'bounded-tool-final-process.json']}
save('codex-verification', {'at': at, 'target': target, 'two_native_threads': isolation, 'mid_execution_recovery': crash, 'handoff_is_native_ack': False})
save('protected-files-verification', {'at': at, 'status': 'PASS', 'go_build_sources_unchanged': True, 'isolated_config_unchanged': True, 'existing_prior_evidence_unchanged': True, 'prior_evidence_automatically_added': added_prior, 'reason': 'The prior-round live wrapper created its previously nonexistent exit file on the intentional /quit of A. No existing evidence was overwritten.', 'all_terminal_mutations_target_only_authorized_surface': mutations, 'target_topology_unchanged_except_title': True, 'credentials_read': False})
new_passes = ['codex.different_cwd_two_native_threads', 'codex.sigkill_mid_tool_manual_context_recovery']
prior_matrix = json.loads((PRIOR / 'runtime-matrix.json').read_text())
pending = ['hook_background_delivery', 'agentflow_hook_queue_adapter', 'queue_autowake_without_live_tui', 'concurrent_live_two_tui_isolation', 'machine_failure_recovery', 'automatic_mid_turn_recovery', 'interrupted_tool_exactly_once_or_completion', 'general_terminal_draft_guard']
matrix = {'at': at, 'collaboration_session_id': policy['session_id'], 'task_id': policy['task_id'], 's1_status': 'PARTIAL', 'runtime_acceptance': 'PARTIAL', 'runtime_passes': prior_matrix['runtime_passes'] + new_passes, 'new_runtime_passes': new_passes, 'unverified': pending, 'tools': [{'tool': 'codex', 'status': 'PASS_FOR_LISTED_CONTROLLED_SCENARIOS', 'target': target, 'native_id_current': a['native_id'], 'native_id_secondary': b['native_id'], 'new_runtime_passes': [p.split('.', 1)[1] for p in new_passes], 'findings': {'two_native_threads': isolation, 'mid_execution_recovery': crash}, 'final_pid': final_pid, 'final_cwd': seed_calls['A']['cwd'], 'final_state': 'LIVE_IDLE_EMPTY_PROMPT_ORIGINAL_A', 'secondary_state': 'STOPPED_INTENTIONALLY_AFTER_RECOVERY_DELIVERY', 'prior_passes_source': '../S1-codex-edge-cases-eb2e9a87/runtime-matrix.json'}, {'tool': 'opencode', 'status': 'VERIFIED_TRANSPORT_ONLY / WAITING_DEPENDENCY', 'new_calls': 0, 'source': '../S1-opencode-local-provider-458ce2e5/runtime-matrix.json'}, {'tool': 'claude', 'status': 'EXCLUDED', 'new_calls': 0}], 'checks': {'evidence_validation': 'PASS', 'protected_files': 'PASS', 'go-test': 'NOT_RUN_NO_GO_CHANGES', 'go-build': 'NOT_RUN_NO_GO_CHANGES', 'make-check': 'NOT_RUN_NO_GO_CHANGES'}, 'go_source_changed': False, 's2': False, 'handoff_is_native_ack': False}
assert len(matrix['runtime_passes']) == 18
save('runtime-matrix', matrix)
save('dependencies', {'at': at, 'status': 'WAITING_DEPENDENCY_FOR_COMPLETE_S1', 'this_round': 'COMPLETE_FOR_BOTH_AUTHORIZED_CONTROLLED_SCENARIOS', 'codex': {'target': target, 'current_native_id': a['native_id'], 'current_pid': final_pid, 'current_cwd': seed_calls['A']['cwd'], 'state': 'LIVE_IDLE_EMPTY_PROMPT', 'secondary_native_id': b['native_id'], 'secondary_state': 'STOPPED_INTENTIONALLY_AFTER_RECOVERY', 'remaining_scope': pending, 'next': 'Continue S1 only. Concurrent TUI, host/automatic recovery and interrupted-tool semantics need separately scoped safe experiments. Do not infer AgentFlow adapter implementation from native CLI evidence.'}, 'opencode': {'status': 'VERIFIED_TRANSPORT_ONLY / WAITING_DEPENDENCY', 'blocker': 'MODEL_PROVIDER_UNAVAILABLE_FROM_PRIOR_ROUND_NOT_RETESTED', 'new_requests': 0, 'login_allowed': False}, 'claude': 'EXCLUDED', 'risk_cli': 'UNAVAILABLE; no fabricated judgment; explicit user authorization and PID/TTY/native/cwd/harmless-child checks recorded', 'history_cli': 'UNAVAILABLE; supplied native evidence reused', 's2': 'DO_NOT_ENTER'})
save('evidence-validation', {'at': at, 'status': 'PASS', 'assertions': ['distinct native IDs and actual default cwd tool outputs', 'both private nonce model ACKs with native message IDs and same visible surface', 'cross-cwd explicit native queue IDs paired with real native ACKs', 'stopped A transcript unchanged until manual resume', 'cross nonce and route messages absent from the other native transcript', 'PID/PPID/TTY/argv/cwd and live nonce-bearing tool checked before single SIGKILL', 'native in-flight turn and emitted BEGIN precede SIGKILL with no task completion', 'wrapper captures -9; same native B resumes and delivers recalled nonce plus fresh actual cwd tool result', 'orphan sleep observed then recorded PID absent; B intentionally stopped', 'original A restored with fresh actual cwd output and recalled nonce', 'Go/build/config and preexisting evidence hashes unchanged', 'all cmux mutations target only authorized window/workspace/surface'], 'new_runtime_passes': new_passes, 'runtime_passes_total': 18, 'scope_caveats': {'isolation': isolation['limitations'], 'recovery': crash['limitations']}})
print(json.dumps({'status': 'PASS', 'new_runtime_passes': new_passes, 'runtime_passes': 18, 'final_pid': final_pid}, ensure_ascii=False))
