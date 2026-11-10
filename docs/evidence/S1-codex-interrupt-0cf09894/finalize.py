"""Validate evidence, including a negative Ctrl-C-only result, and publish snapshots."""
import datetime
import hashlib
import json
from pathlib import Path
import re
import sys

OUT = Path(__file__).resolve().parent
ROOT = OUT.parents[2]
PRIOR = ROOT/'docs/evidence/S1-codex-concurrent-tui-754ba5a3'


def load(name):
    return json.loads((OUT/(name+'.json')).read_text())


def save(name, data):
    with (OUT/(name+'.json')).open('x') as f:
        json.dump(data, f, ensure_ascii=False, indent=2)
        f.write('\n')


def now():
    return datetime.datetime.now(datetime.timezone.utc).isoformat()


def text(record):
    return '\n'.join(p.get('text', '') for p in record.get('payload', {}).get('content', []))


def timestamp(value):
    return datetime.datetime.fromisoformat(value.replace('Z', '+00:00'))


stage = sys.argv[1]
if stage == 'results':
    final = load('final-native')
    old = load('initial-native')
    delta = final['records'][len(old['records']):]
    policy = load('execution-policy')
    nonce = policy['nonce']
    recovery = load('recovery-request')
    new_nonce = recovery['nonce']
    history_nonce = json.loads((PRIOR/'seed-A-request.json').read_text())['nonce']
    thread = policy['target']['native_id']
    messages = [r for r in delta if r['type'] == 'response_item' and r['payload'].get('type') == 'message']
    calls = [r for r in delta if r['type'] == 'response_item' and r['payload'].get('type') in ['custom_tool_call', 'function_call']]
    launches = [r for r in calls if 'exec_command' in r['payload'].get('input', r['payload'].get('arguments', ''))]
    old_launch = [r for r in launches if 'recorder.py tool '+nonce in r['payload'].get('input', '')]
    new_launch = [r for r in launches if 'recorder.py recovery-tool '+new_nonce in r['payload'].get('input', '')]
    aborted = [r for r in delta if r['type'] == 'event_msg' and r['payload'].get('type') == 'turn_aborted']
    assert len(aborted) == 1
    aborted_turn = aborted[0]['payload']['turn_id']
    commands = [r for r in delta if r['payload'].get('type') == 'item_completed' and r['payload'].get('item', {}).get('type') == 'CommandExecution']
    tool_end = [r for r in commands if nonce in ' '.join(r['payload']['item']['command'])]
    recovery_end = [r for r in commands if new_nonce in ' '.join(r['payload']['item']['command'])]
    assistant = [r for r in messages if r['payload']['role'] == 'assistant']
    ack = [r for r in assistant if text(r).startswith('RESUME_ONCE_ACK '+new_nonce)]
    events = [json.loads(l) for l in (OUT/'tool-events.jsonl').read_text().splitlines()]
    attempts = [json.loads(l) for l in (OUT/'recovery-attempts.jsonl').read_text().splitlines()]
    pid = load('tool-once')['pid']
    kernel = load('tool-kernel-exit')
    target = policy['target']
    survived_seconds = (timestamp(load('pre-stop-tool-process')['start'])-timestamp(aborted[0]['timestamp'])).total_seconds()
    observation_seconds = (timestamp(load('final-screen')['end'])-timestamp(events[0]['at'])).total_seconds()
    checks = {
        'original_native_history_prefix_preserved': all(v['original_prefix_preserved'] for v in load('native-history-preservation')['threads'].values()),
        'secondary_native_history_unchanged': load('native-history-preservation')['threads']['B']['unchanged'],
        'native_ctrl_c_sent_once_to_authorized_surface': load('native-ctrl-c')['exit_code'] == 0 and load('native-interrupt-action')['attempts'] == 1 and load('native-interrupt-action')['surface_id'] == target['surface'],
        'actual_native_turn_aborted_interrupted': aborted[0]['payload']['reason'] == 'interrupted',
        'ctrl_c_alone_leaves_tool_running_observed': load('post-interrupt-tool-process')['exit_code'] == 0 and load('pre-stop-tool-process')['exit_code'] == 0 and survived_seconds > 0,
        'only_owned_background_terminal_before_stop': '1 background terminal running' in load('pre-stop-screen-decoded')['text'],
        'native_stop_sent_once': load('native-stop-enter')['exit_code'] == 0,
        'kernel_process_exit_observed_for_exact_tool_pid': kernel['pid'] == pid and len(kernel['events']) == 1 and kernel['events'][0]['ident'] == pid and kernel['events'][0]['filter'] == -5 and kernel['events'][0]['fflags'] & 0x80000000 != 0,
        'process_absent_after_stop_and_final': load('post-stop-tool-process')['exit_code'] == 1 and load('final-tool-process')['exit_code'] == 1,
        'native_failed_command_exit_record': len(tool_end) == 1 and tool_end[0]['payload']['item']['status'] == 'failed' and tool_end[0]['payload']['item']['exit_code'] == -1,
        'tool_started_only_once_and_never_completed': len(events) == 1 and events[0]['event'] == 'TOOL_BEGIN' and len(old_launch) == 1,
        'no_false_assistant_completion': not any('INTERRUPT_TOOL_DONE' in text(r) for r in assistant),
        'aborted_turn_has_no_task_complete': not any(r['payload'].get('type') == 'task_complete' and r['payload'].get('turn_id') == aborted_turn for r in delta),
        'normal_old_tui_exit_zero': load('original-wrapper-exit')['exit_code'] == 0 and load('old-native-absent')['exit_code'] == 1,
        'manual_resume_same_native_id_and_surface': load('resume-process')['native_id'] == thread and load('resume-process')['surface_id'] == target['surface'] and load('resume-process')['pid'] != 96559 and thread in load('resumed-before-request-screen-decoded')['text'],
        'new_request_delivered_once': len([r for r in messages if r['payload']['role'] == 'user' and text(r) == recovery['message']]) == 1,
        'new_tool_executed_once': len(new_launch) == 1 and len(attempts) == 1 and len(recovery_end) == 1 and recovery_end[0]['payload']['item']['exit_code'] == 0,
        'unique_native_model_ack': len(ack) == 1,
        'context_recalled_without_resending_nonces': len(ack) == 1 and nonce in text(ack[0]) and history_nonce in text(ack[0]) and nonce not in recovery['message'] and history_nonce not in recovery['message'],
        'tool_output_matches_new_nonce_cwd_and_surface': load('recovery-once')['nonce'] == new_nonce and load('recovery-once')['surface'] == target['surface'] and load('recovery-once')['workspace'] == target['workspace'] and load('recovery-once')['cwd'] == final['row'][1],
        'same_surface_native_ack_visible': new_nonce in load('recovery-observed-screen-decoded')['text'] and 'RESUME_ONCE_ACK' in load('recovery-observed-screen-decoded')['text'] and history_nonce in load('recovery-observed-screen-decoded')['text'],
        'final_native_idle_empty_no_background_tool': 'Ask Codex to do anything' in load('final-screen-decoded')['text'] and 'background terminal running' not in load('final-screen-decoded')['text'][-600:] and load('final-process')['exit_code'] == 0,
        'observation_past_original_natural_completion_deadline': observation_seconds > policy['long_seconds'],
    }
    save('native-transcript-delta', dict(at=now(), native_id=thread, source='final-native.json', records=delta))
    save('evidence-validation', dict(at=now(), status='PASS' if all(checks.values()) else 'FAIL', meaning='Evidence consistency assertions only; feature Ctrl-C-alone termination is FAIL.', checks=checks, failures=[k for k,v in checks.items() if not v]))
    assert all(checks.values()), [k for k,v in checks.items() if not v]
    before = load('inventory-before')['hashes']
    after = load('inventory-after')['hashes']
    allowed = ['docs/evidence/S1-result.md', 'docs/evidence/S1-capability-matrix.md']
    changed = [p for p,h in before.items() if after.get(p) != h]
    added = sorted(set(after)-set(before))
    expected_addition = 'docs/evidence/S1-codex-final-edge-fd8f8aac/a-final-exit.json'
    protected_ok = all(p in allowed for p in changed) and all(p == expected_addition for p in added)
    save('protected-files-verification', dict(at=now(), status='PASS' if protected_ok else 'FAIL', checked_existing_files=len(before), changed=changed, added=added, allowed_document_updates=allowed, expected_wrapper_addition=expected_addition, originals_overwritten=False, native_history='native-history-preservation.json', credentials_read=False))
    assert protected_ok
    findings = dict(at=now(), native_id=thread, target=target, codex_version='0.166.0', acceptance='NOT_PASS_CTRL_C_ALONE_DOES_NOT_TERMINATE_BACKGROUND_TOOL', native_turn_abort=dict(status='PASS', event=aborted[0], action='native-interrupt-action.json'), ctrl_c_tool_termination=dict(status='FAIL_OBSERVED', pid=pid, survived_after_native_abort_seconds=survived_seconds, evidence=['post-interrupt-tool-process.json', 'pre-stop-tool-process.json', 'pre-stop-screen.json']), explicit_native_cleanup=dict(status='PASS_WITH_ADDITIONAL_STOP', action='native-stop-action.json', kernel_exit=kernel, native_failed_tool=tool_end[0], actual_signal='UNVERIFIED; no external OS signal sent; NOTE_EXIT data=0 is not treated as success or a signal'), no_false_completion_or_duplicate=dict(status='PASS_IN_OBSERVATION_WINDOW', observation_seconds=observation_seconds, tool_start_count=len(old_launch), tool_event_count=len(events), original_task_complete_count=0), manual_resume=dict(status='PASS_CONTROLLED_MANUAL_ONLY', process=load('resume-process'), request='recovery-request.json', tool_launch_count=len(new_launch), execution_attempts=len(attempts), final_ack_count=len(ack), model_ack=ack[0], tool_result=load('recovery-once'), recovery_command=recovery_end[0], private_nonces_omitted_from_request=True), limitations=['Ctrl-C cancels the model turn; the yielded background process remained live until explicit native /stop.', 'The /stop path is native cleanup, not evidence that Ctrl-C alone terminates the tool.', 'One request executed once in this bounded experiment; no general exactly-once, transaction, automatic replay, host or automatic recovery claim.', 'Native Script completed / item_completed describe wrapper/item lifecycle; failed CommandExecution exit -1 is not a successful long-tool completion.', 'No exact terminating OS signal or POSIX wait status is inferred from native exit -1 / kernel NOTE_EXIT data=0.'])
    save('codex-verification', findings)
    prior = json.loads((PRIOR/'runtime-matrix.json').read_text())
    matrix = dict(at=now(), collaboration_session_id=policy['session_id'], task_id=policy['task_id'], s1_status='PARTIAL', runtime_acceptance='PARTIAL', this_round=findings['acceptance'], runtime_passes=prior['runtime_passes'], new_runtime_passes=[], failures=['codex.ctrl_c_alone_terminates_yielded_background_tool'], verified_observations=['native_ctrl_c_turn_aborted', 'native_stop_tool_process_exit', 'no_false_completion_or_duplicate_in_bounded_window', 'manual_same_thread_resume_new_request_once_and_native_ack'], unverified=prior['unverified'], tools=[dict(tool='codex', status='NEGATIVE_CTRL_C_TOOL_TERMINATION_RESULT_RECORDED', findings='codex-verification.json', target=target, native_id_current=thread, final_pid=load('resume-process')['pid'], final_state='LIVE_IDLE_EMPTY_PROMPT', prior_passes_source='../S1-codex-concurrent-tui-754ba5a3/runtime-matrix.json'), dict(tool='opencode', status='VERIFIED_TRANSPORT_ONLY / MODEL_PROVIDER_UNAVAILABLE / WAITING_DEPENDENCY', new_calls=0), dict(tool='claude', status='EXCLUDED', new_calls=0)], checks=dict(evidence_validation='PASS', protected_files='PASS', go_test='NOT_RUN_EVIDENCE_ONLY', go_build='NOT_RUN_EVIDENCE_ONLY', make_check='NOT_RUN_EVIDENCE_ONLY'), go_source_changed=False, s2=False, handoff_is_native_ack=False)
    save('runtime-matrix', matrix)
    save('dependencies', dict(at=now(), status='WAITING_DEPENDENCY_FOR_COMPLETE_S1', this_round='COMPLETE_WITH_NEGATIVE_CTRL_C_TERMINATION_RESULT', codex=dict(target=target, native_id=thread, current_pid=load('resume-process')['pid'], state='LIVE_IDLE_EMPTY_PROMPT', ctrl_c_only_tool_termination='FAIL_OBSERVED', safe_native_cleanup='Ctrl-C then explicit /stop verified; exact cleanup signal UNVERIFIED', remaining_scope=prior['unverified'], next='Remain in S1. Preserve distinction between turn cancellation and process termination; define explicit native /stop handling before accepting an adapter cancellation contract. Manual resume is not automatic recovery.'), opencode=dict(status='VERIFIED_TRANSPORT_ONLY / MODEL_PROVIDER_UNAVAILABLE / WAITING_DEPENDENCY', new_requests=0, login_allowed=False), claude='EXCLUDED', risk_cli='UNAVAILABLE; no fabricated judgment; scoped authorization and PID/native/surface checks retained', history_cli='UNAVAILABLE; supplied prior evidence reused', s2='DO_NOT_ENTER'))
    print(json.dumps(dict(evidence_assertions='PASS', feature_acceptance=findings['acceptance'], survived_seconds=survived_seconds, observation_seconds=observation_seconds, new_nonce=new_nonce, final_pid=load('resume-process')['pid']), ensure_ascii=False))
elif stage == 'documents':
    documents = [OUT/'README.md', ROOT/'docs/evidence/S1-result.md', ROOT/'docs/evidence/S1-capability-matrix.md']
    links = []
    for p in documents:
        for target in re.findall(r'\]\(([^)]+)\)', p.read_text()):
            if '://' in target or target.startswith('#'):
                continue
            dest = p.parent / target.split('#')[0]
            links.append(dict(document=str(p.relative_to(ROOT)), target=target, exists=dest.exists()))
    assert all(l['exists'] for l in links)
    save('document-validation', dict(at=now(), status='PASS', links=links, hashes={str(p.relative_to(ROOT)):hashlib.sha256(p.read_bytes()).hexdigest() for p in documents}))
    print('Document links and hashes PASS')
elif stage == 'manifest':
    prefix = sys.argv[2] if len(sys.argv) > 2 else ''
    hashes = {str(p.relative_to(OUT)):hashlib.sha256(p.read_bytes()).hexdigest() for p in sorted(OUT.rglob('*')) if p.is_file() and p.name not in ['sha256-manifest.json', 'hash-verification.json']}
    save(prefix+'sha256-manifest', dict(at=now(), algorithm='sha256', hashes=hashes))
    failures = [p for p,h in hashes.items() if hashlib.sha256((OUT/p).read_bytes()).hexdigest() != h]
    save(prefix+'hash-verification', dict(at=now(), status='PASS' if not failures else 'FAIL', checked_files=len(hashes), failures=failures, manifest_sha256=hashlib.sha256((OUT/(prefix+'sha256-manifest.json')).read_bytes()).hexdigest()))
    assert not failures
    print(json.dumps(dict(hash_verification='PASS', files=len(hashes))))
elif stage == 'target-safety':
    target = load('execution-policy')['target']
    def topology(name):
        tree = json.loads(load(name)['stdout'])
        windows = [w for w in tree['windows'] if w['id'] == target['window']]
        assert len(windows) == 1
        workspaces = [w for w in windows[0]['workspaces'] if w['id'] == target['workspace']]
        assert len(workspaces) == 1
        ws = workspaces[0]
        return dict(layout=ws['layout'], panes=[dict(id=p['id'], selected_surface_id=p['selected_surface_id'], surfaces=[dict(id=s['id'], tty=s['tty'], type=s['type']) for s in p['surfaces']]) for p in ws['panes']])
    initial, final = topology('initial-tree'), topology('final-tree')
    assert initial == final
    changes = []
    for p in sorted(OUT.glob('*.json')):
        d = json.loads(p.read_text())
        argv = d.get('argv', []) if isinstance(d, dict) else []
        if argv and argv[0].endswith('/cmux') and any(a in argv for a in ['send', 'send-key']):
            assert argv[argv.index('--window')+1] == target['window']
            assert argv[argv.index('--workspace')+1] == target['workspace']
            assert argv[argv.index('--surface')+1] == target['surface']
            changes.append(p.name)
    assert target['native_id'] in load('final-process')['stdout']
    assert 'ttys028' in load('final-process')['stdout']
    assert '\nn'+load('resume-process')['cwd']+'\n' in load('final-cwd')['stdout']
    save('target-safety-verification', dict(at=now(), status='PASS', topology_preserved=True, initial=initial, final=final, all_recorded_terminal_mutations_target_authorized_surface=True, mutation_records=changes, final_pid_tty_cwd_native_binding=True, no_main_worker_experimental_input=True))
    print('Authorized surface routing, final PID/cwd/TTY binding and unchanged topology PASS')
else:
    raise SystemExit('unknown stage')
