#!/usr/bin/env python3
"""Read-only archive verification; never executes a CLI or model."""
import hashlib
import json
from pathlib import Path

E = Path(__file__).resolve().parent
ROOT = E.parents[2]
s = json.loads((E / 'validation.json').read_text())
checks = {}
checks['blocked_auth_preflight'] = (s['outcome'] == 'BLOCKED_CONFIRM'
    and s['auth_gate']['code'] == 'BARE_AUTH_SOURCE_UNAVAILABLE'
    and not s['auth_gate']['native_auth_failure_observed'])
checks['auth_presence_only_no_supported_source'] = not any(s['environment_presence_only'][n]
    for n in ('ANTHROPIC_API_KEY', 'CLAUDE_CODE_USE_BEDROCK', 'CLAUDE_CODE_USE_VERTEX', 'CLAUDE_CODE_USE_FOUNDRY'))
checks['no_native_requests_or_fabricated_captures'] = (s['native_invocations'] == []
    and s['captures'] == [] and s['observed_sessions'] == []
    and not list(E.glob('*.jsonl')) and not (E / 'state.db').exists())
checks['actors_are_planned_only'] = all(a['session_id'] is None and a['generation'] == 1
    and a['run_id'] == s['run_id'] and a['status'] == 'NOT_STARTED' for a in s['planned_actors'])
expected = ('real_startup_two_roles', 'execute_nonce_handoff', 'review_approve',
    'workflow_completed', 'persistent_resume_second_delivery',
    'coordinator_reopen_exact_replay', 'real_nonce', 'effective_permissions')
checks['all_unperformed_acceptance_unverified'] = all(s['checks'][n] == 'UNVERIFIED' for n in expected)
checks['nonce_not_claimed_real'] = s['nonce']['status'] == 'NOT_EXECUTED'
checks['commands_are_metadata_build_dry_run_only'] = ([c['label'] for c in s['commands']]
    == ['claude-version', 'claude-help', 'build', 'up-dry-run'])
checks['all_command_exits_zero'] = all(c['exit_code'] == 0 for c in s['commands'])
checks['all_raw_stderr_empty'] = all((E / (c['label'] + '.stderr.log')).read_bytes() == b'' for c in s['commands'])
checks['raw_cli_version_matches'] = (E / 'claude-version.stdout.log').read_text().strip() == s['native_version']
checks['help_confirms_bare_auth'] = 'strictly ANTHROPIC_API_KEY' in (E / 'claude-help.stdout.log').read_text()
plan = json.loads((E / 'up-dry-run.stdout.log').read_text())
checks['plan_matches_archive'] = plan == s['dry_run'] and plan['executed'] is False
config = json.loads((E / 'team.json').read_text())
checks['two_persistent_default_claude_roles'] = (set(config['agents']) == {'builder', 'main'}
    and config['defaults'] == {'tool': 'claude', 'session': 'persistent',
        'permissions': 'default', 'cwd': s['cwd']})
checks['planned_nonce_in_config'] = s['nonce']['value'] in config['workflow']['steps'][0]['instruction']
paths = [p for d in ('cmd', 'internal', 'tests', 'examples')
         for p in (ROOT / d).rglob('*') if p.is_file()]
paths += [ROOT / n for n in ('go.mod', 'go.sum', 'Makefile', 'README.md')]
current = {str(p.relative_to(ROOT)): hashlib.sha256(p.read_bytes()).hexdigest() for p in sorted(paths)}
checks['production_hashes_unchanged'] = s['production_before'] == s['production_after'] == current
checks['no_runtime_state_created'] = s['run_state']['database'] == 'NOT_CREATED' and s['run_state']['run_directory_entries'] == []
checks['cleanup_complete'] = (s['cleanup']['temporary_root_removed'] and not Path(s['temp_root']).exists()
    and s['cleanup']['cwd_entries'] == [] and s['cleanup']['run_directory_entries'] == []
    and all(s['cleanup'][n] for n in ('db_not_created', 'lock_not_created', 'socket_not_created')))
checks['no_credentials_or_login'] = (not s['auth_gate']['credential_files_read_or_copied_by_harness']
    and not s['auth_gate']['login_attempted'] and s['auth_gate']['oauth_keychain_login_status'] == 'UNVERIFIED')
result = {'status': 'PASS' if all(checks.values()) else 'FAIL',
          'scope': 'Read-only consistency of blocked-preflight evidence; does not establish native acceptance',
          'checks': checks}
print(json.dumps(result, indent=2))
raise SystemExit(0 if all(checks.values()) else 1)
