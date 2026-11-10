#!/usr/bin/env python3
"""Read-only check of this blocked archive; never invokes a provider."""
import hashlib
import json
import os
from pathlib import Path
import sqlite3

E = Path(__file__).resolve().parent
ROOT = E.parents[2]
v = json.loads((E / 'validation.json').read_text())
checks = {}
def check(label, ok):
    checks[label] = bool(ok)
    assert ok, label

check('blocked_outcome', v['outcome'] == 'BLOCKED_CONFIRM')
check('all_acceptance_gates_unverified', all(v['checks'][k] == 'UNVERIFIED' for k in (
    'real_startup_two_roles', 'execute_nonce_handoff', 'review_approve', 'workflow_completed',
    'persistent_resume_second_delivery', 'coordinator_reopen_exact_replay', 'real_nonce', 'effective_permissions')))
calls = [json.loads(l) for l in (E / 'native-invocations.jsonl').read_text().splitlines()]
check('one_real_native_attempt', len(calls) == 2 and [c['event'] for c in calls] == ['start', 'exit'] and calls[0]['id'] == calls[1]['id'])
call = calls[1]
check('native_exit_1', call['exit_code'] == 1)
check('unchanged_native_argv', call['argv'] == [v['native_cli'], '--print', '--bare', '--verbose', '--output-format', 'stream-json', '--input-format', 'text', '--permission-prompts', 'none', '--prompt-suggestions', 'false'])
check('isolated_native_cwd', call['cwd'] == v['cwd'])
cap = json.loads((E / 'headless-start-builder.json').read_text())
raw = (E / (call['id'] + '.stdout.jsonl')).read_bytes()
check('raw_capture_byte_equality', raw == cap['stdout'].encode() == (E / 'headless-start-builder.jsonl').read_bytes())
check('failed_capture', cap['succeeded'] is False and cap['truncated'] is False and cap['message_id'] == '')
seat = cap['seat']
check('capture_actor_binding', seat['run_id'] == v['run_id'] and seat['agent_id'] == 'builder' and seat['generation'] == 1 and seat['session'] == 'persistent')
body = (E / (call['id'] + '.stdin.txt')).read_text()
challenge = json.loads(body.splitlines()[-1])
check('stdin_actor_nonce_binding', challenge['actor'] == {'runId': v['run_id'], 'agentId': 'builder', 'generation': 1} and challenge['nonce'] == v['runtime_scope']['startup_challenge_nonce'])
events = [json.loads(l) for l in raw.splitlines()]
check('three_native_events', [e['type'] for e in events] == ['system', 'assistant', 'result'])
check('failed_session_identity', {e['session_id'] for e in events} == {v['runtime_scope']['native_session_id']})
check('native_auth_failure', events[0]['apiKeySource'] == 'none' and events[1]['error'] == 'authentication_failed' and events[2]['is_error'] is True and events[2]['terminal_reason'] == 'api_error' and events[2]['result'] == v['auth_gate']['result'])
check('no_successful_model_evidence', events[1]['message']['model'] == '<synthetic>' and events[2]['duration_api_ms'] == 0 and events[2]['total_cost_usd'] == 0)
check('nonce_not_returned', challenge['nonce'] not in raw.decode() and v['nonce'] not in raw.decode())
check('no_tool_calls_observed', all(e.get('type') != 'tool_progress' and all(b.get('type') != 'tool_use' for b in e.get('message', {}).get('content', [])) for e in events))
check('version_and_help', (E / 'claude-version.stdout.log').read_text().strip() == '2.1.318 (Claude Code)' and 'keychain are never read' in (E / 'claude-help.stdout.log').read_text())
check('command_exits', {c['label']: c['exit_code'] for c in v['commands']} == {'claude-version': 0, 'claude-help': 0, 'build': 0, 'up': 1})
check('metadata_build_native_stderr_empty', all((E / name).stat().st_size == 0 for name in ('claude-version.stderr.log', 'claude-help.stderr.log', 'build.stderr.log', call['id'] + '.stderr.log')))
check('up_stderr_reports_failure', 'UP_START_FAILED' in (E / 'up.stderr.log').read_text())
db = sqlite3.connect('file:' + str(E / 'state.db') + '?mode=ro', uri=True)
check('db_integrity', list(db.execute('PRAGMA integrity_check')) == [('ok',)] and not list(db.execute('PRAGMA foreign_key_check')))
actual = {}
for (name,) in db.execute("SELECT name FROM sqlite_master WHERE type='table' ORDER BY name"):
    cur = db.execute('SELECT * FROM "' + name.replace('"', '""') + '" ORDER BY rowid')
    actual[name] = [dict(zip([d[0] for d in cur.description], row)) for row in cur]
db.close()
check('db_archive_equality', actual == json.loads((E / 'final.db.json').read_text()))
check('no_ready_dispatch_or_workflow', not actual['seats'] and not actual['messages'] and not actual['deliveries'] and [r['event'] for r in actual['journal']] == ['LAUNCH_REQUESTED'])
check('launch_requested_binding', json.loads(actual['journal'][0]['details'])['AgentID'] == 'builder' and actual['runs'][0]['run_id'] == v['run_id'])
check('two_persistent_roles_configured', len(json.loads((E / 'team.json').read_text())['agents']) == 2 and json.loads((E / 'team.json').read_text())['defaults']['session'] == 'persistent')
check('source_hashes_equal', v['production_before'] == v['production_after'] and all(hashlib.sha256((ROOT / p).read_bytes()).hexdigest() == digest for p, digest in v['production_before'].items()))
check('temporary_root_removed', not Path(v['temp_root']).exists() and all(v['cleanup'][k] for k in ('owned_processes_exited', 'lock_removed', 'socket_removed', 'temporary_root_removed')) and v['cleanup']['cwd_entries'] == [])
for c in v['commands']:
    if 'pid' in c:
        try:
            os.kill(c['pid'], 0)
        except ProcessLookupError:
            pass
        else:
            raise AssertionError('owned PID still present')
check('owned_process_absent', True)
check('boundaries_preserved', all(value is False for value in v['boundaries'].values()))
print(json.dumps({'status': 'PASS', 'meaning': 'Blocked evidence consistency only; real acceptance remains BLOCKED_CONFIRM', 'checks': checks}, indent=2))
