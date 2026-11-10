#!/usr/bin/env python3
"""Evidence-only preflight. Never executes a model, login, or credential reader."""
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import time
import uuid

E = Path(__file__).resolve().parent
ROOT = E.parents[2]
TEMP_PARENT = Path('/var/folders/wv/x8nwj9tj7xz5dhqf5f2mt3hw0000gn/T/opencode')
RUN = 'real-claude-v1-validation-20261104-01'
CLI = '/Users/funlake/.nvm/versions/node/v26.8.1/bin/claude'
AUTH_NAMES = ['ANTHROPIC_API_KEY', 'ANTHROPIC_AUTH_TOKEN', 'ANTHROPIC_BASE_URL',
              'CLAUDE_CODE_USE_BEDROCK', 'CLAUDE_CODE_USE_VERTEX', 'CLAUDE_CODE_USE_FOUNDRY',
              'AWS_PROFILE', 'AWS_ACCESS_KEY_ID', 'AWS_WEB_IDENTITY_TOKEN_FILE',
              'GOOGLE_APPLICATION_CREDENTIALS', 'ANTHROPIC_FOUNDRY_API_KEY',
              'CLAUDE_CODE_OAUTH_TOKEN', 'CLAUDECODE']
state = {'session_id': RUN, 'run_id': RUN, 'started_at': time.time(),
         'outcome': 'BLOCKED_CONFIRM', 'native_cli': CLI, 'commands': [],
         'native_invocations': [], 'captures': [], 'observed_sessions': [],
         'environment_presence_only': {n: bool(os.environ.get(n)) for n in AUTH_NAMES},
         'risk_judgment': {'status': 'UNAVAILABLE', 'cli': shutil.which('jev-judge'),
                           'reason': 'jev-judge not available in PATH; no judgment fabricated'},
         'checks': {name: 'UNVERIFIED' for name in [
             'real_startup_two_roles', 'execute_nonce_handoff', 'review_approve',
             'workflow_completed', 'persistent_resume_second_delivery',
             'coordinator_reopen_exact_replay', 'real_nonce', 'effective_permissions']}}

def save():
    (E / 'validation.json').write_text(json.dumps(state, indent=2) + '\n')

def hashes():
    paths = [p for d in ('cmd', 'internal', 'tests', 'examples')
             for p in (ROOT / d).rglob('*') if p.is_file()]
    paths += [ROOT / n for n in ('go.mod', 'go.sum', 'Makefile', 'README.md')]
    return {str(p.relative_to(ROOT)): hashlib.sha256(p.read_bytes()).hexdigest()
            for p in sorted(paths)}

def command(label, args, cwd, timeout=60):
    r = {'label': label, 'argv': [str(a) for a in args], 'cwd': str(cwd),
         'started_at': time.time()}
    state['commands'].append(r)
    save()
    p = subprocess.run(r['argv'], cwd=cwd, capture_output=True, timeout=timeout)
    (E / (label + '.stdout.log')).write_bytes(p.stdout)
    (E / (label + '.stderr.log')).write_bytes(p.stderr)
    r.update(exit_code=p.returncode, finished_at=time.time())
    save()
    assert p.returncode == 0, label
    return p

os.umask(0o077)
assert not (E / 'validation.json').exists(), 'Evidence already exists; refuse overwrite'
state['production_before'] = hashes()
temp = Path(tempfile.mkdtemp(prefix='af-claude-preflight-', dir=TEMP_PARENT)).resolve()
cwd, run, binary = temp / 'cwd', temp / 'run', temp / 'agentflow'
cwd.mkdir(mode=0o700)
run.mkdir(mode=0o700)
nonce = 'AF_CLAUDE_REAL_' + uuid.uuid4().hex
state.update(temp_root=str(temp), cwd=str(cwd), run_dir=str(run),
             nonce={'value': nonce, 'status': 'NOT_EXECUTED',
                    'explanation': 'Generated for planned workflow only; no native nonce response exists'},
             planned_actors=[{'run_id': RUN, 'agent_id': aid, 'generation': 1,
                              'session_id': None, 'status': 'NOT_STARTED'}
                             for aid in ('builder', 'main')])
try:
    print('Collecting installed CLI metadata only', flush=True)
    version = command('claude-version', [CLI, '--version'], cwd)
    state['native_version'] = version.stdout.decode().strip()
    help_result = command('claude-help', [CLI, '--help'], cwd)
    help_text = help_result.stdout.decode()
    assert '--bare' in help_text and 'strictly ANTHROPIC_API_KEY' in help_text
    assert 'OAuth and' in help_text and 'keychain are never read' in help_text
    supported_auth = any(state['environment_presence_only'][n] for n in (
        'ANTHROPIC_API_KEY', 'CLAUDE_CODE_USE_BEDROCK',
        'CLAUDE_CODE_USE_VERTEX', 'CLAUDE_CODE_USE_FOUNDRY'))
    assert not supported_auth, 'Environment changed: this preflight records only the blocked case'
    state['auth_gate'] = {
        'status': 'BLOCKED_CONFIRM', 'code': 'BARE_AUTH_SOURCE_UNAVAILABLE',
        'basis': 'Installed help requires ANTHROPIC_API_KEY, explicit --settings apiKeyHelper, or enabled third-party provider. Key/provider env absent; adapter supplies no --settings.',
        'credential_files_read_or_copied_by_harness': False,
        'oauth_keychain_login_status': 'UNVERIFIED', 'login_attempted': False,
        'native_auth_failure_observed': False,
        'explanation': 'Preflight stop, not a server-side authentication rejection. Subscription login was not inspected.'}
    print('BLOCKED_CONFIRM: no supported bare-mode auth source; model requests stopped', flush=True)
    config = {'version': 1, 'name': RUN, 'terminal': 'cmux', 'lead': 'main',
              'context_dir': str(temp / 'context'),
              'defaults': {'tool': 'claude', 'session': 'persistent',
                           'permissions': 'default', 'cwd': str(cwd)},
              'agents': {'main': {'role': 'Reviewer', 'prompt': 'Do not use tools, inspect files, authenticate, or modify files. Follow the nonce protocol.'},
                         'builder': {'role': 'Developer', 'prompt': 'Do not use tools, inspect files, authenticate, or modify files. Follow the nonce protocol.'}},
              'layout': {'split': 'columns', 'children': [{'agent': 'main'}, {'agent': 'builder'}]},
              'workflow': {'entry': 'nonce', 'completion': 'done', 'steps': [{
                  'id': 'nonce', 'execute': {'agent': 'builder'}, 'reviewer': 'main',
                  'instruction': 'Do not use tools, inspect files, authenticate, or modify anything. Execute: return a handoff with summary and evidence each containing ' + nonce + '. Review: check that exact nonce, then approve with accepted=[true].',
                  'acceptance': ['The handoff summary and evidence each contain exactly ' + nonce + '.'],
                  'handoff_required': True, 'max_attempts': 2,
                  'transitions': {'approve': 'next', 'changes': 'self', 'retry': 'self', 'blocked': 'pause'},
                  'next': 'done'}]}}
    (E / 'team.json').write_text(json.dumps(config, indent=2) + '\n')
    command('build', ['go', 'build', '-o', binary, './cmd/agentflow'], ROOT, 180)
    plan = command('up-dry-run', [binary, 'up', '-f', E / 'team.json',
        '--headless-claude', '--run', RUN, '--run-dir', run, '--dry-run', '--json'], cwd)
    state['dry_run'] = json.loads(plan.stdout)
    assert state['dry_run']['executed'] is False
    assert len(state['dry_run']['seats']) == 2
    state['checks']['isolated_config_dry_run'] = 'PASS'
    state['run_state'] = {'status': 'NOT_STARTED', 'database': 'NOT_CREATED',
                          'coordinator': 'NOT_STARTED', 'messages': 'NOT_CREATED',
                          'captures': 'NOT_CREATED', 'workflow': 'NOT_STARTED',
                          'run_directory_entries': [p.name for p in run.iterdir()]}
    assert not list(run.iterdir())
finally:
    state['production_after'] = hashes()
    state['checks']['production_files_unchanged'] = state['production_before'] == state['production_after']
    state['cleanup'] = {'cwd_entries': [p.name for p in cwd.iterdir()],
                        'run_directory_entries': [p.name for p in run.iterdir()],
                        'db_not_created': not (run / 'state.db').exists(),
                        'lock_not_created': not (run / 'coordinator.lock').exists(),
                        'socket_not_created': not (run / 'ipc.sock').exists(),
                        'owned_coordinators': [], 'native_model_processes': []}
    assert temp.parent == TEMP_PARENT.resolve() and temp.name.startswith('af-claude-preflight-')
    shutil.rmtree(temp)
    state['cleanup']['temporary_root_removed'] = not temp.exists()
    state['finished_at'] = time.time()
    save()
    print('Preflight evidence saved; isolated temporary root removed', flush=True)
