"""S1 evidence recorder: GET-only discovery, no model/auth/config mutations."""
import concurrent.futures
import datetime
import hashlib
import json
import os
from pathlib import Path
import subprocess
import sys
import time
import urllib.error
import urllib.request

ROOT = Path('/Users/funlake/Projects/agentflow')
OUT = Path(__file__).resolve().parent
BASE = 'http://127.0.0.1:19731'
SID = 'ses_e65a4cf24ffe3wZAiM2U7og4Px'
TARGET = {
    'window_id': 'CAE5B3FE-3E61-4E92-9896-775B9343687F',
    'workspace_id': '158ADE67-F09E-47D5-9EB9-7B6718BE682E',
    'surface_id': 'FDDB002D-F985-4CB1-AE3C-FB8918AADC5D',
}
CONFIG = ROOT / 'docs/evidence/S1-runtime-retry-cbcea350/scratch/opencode/config/opencode/opencode.jsonc'
CMUX = '/Applications/cmux.app/Contents/Resources/bin/cmux'
# Disable proxies and redirects: every HTTP request stays on the named loopback endpoint.
class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None

HTTP = urllib.request.build_opener(urllib.request.ProxyHandler({}), NoRedirect())


def now():
    return datetime.datetime.now(datetime.timezone.utc).isoformat()


def save(name, value):
    with (OUT / (name + '.json')).open('x') as f:
        json.dump(value, f, ensure_ascii=False, indent=2)
        f.write('\n')


def run(name, argv):
    record = {'at': now(), 'argv': argv, 'cwd': str(ROOT)}
    try:
        p = subprocess.run(argv, cwd=ROOT, stdin=subprocess.DEVNULL,
                           capture_output=True, text=True, timeout=45)
        record.update(exit_code=p.returncode, stdout=p.stdout, stderr=p.stderr)
    except subprocess.TimeoutExpired:
        record.update(exit_code=None, timed_out=True)
    save(name, record)
    print(json.dumps({'evidence': name, 'exit_code': record['exit_code']}))
    return record


def get(url, project, limit=4 * 1024 * 1024):
    record = {'at': now(), 'method': 'GET', 'url': url,
              'credential_headers': False, 'proxy': False, 'redirects': False}
    start = time.monotonic()
    try:
        with HTTP.open(urllib.request.Request(url, method='GET'), timeout=4) as r:
            record['http_status'] = r.status
            body = r.read(limit + 1)
            record['response_bytes_read'] = len(body)
            if len(body) > limit:
                record['error'] = 'RESPONSE_SIZE_LIMIT'
            else:
                try:
                    record['projection'] = project(json.loads(body))
                except (ValueError, TypeError, AttributeError):
                    record['error'] = 'NOT_EXPECTED_JSON_RESPONSE'
    except urllib.error.HTTPError as e:
        record['http_status'] = e.code
        record['error'] = 'HTTP_ERROR_BODY_NOT_SAVED'
    except (urllib.error.URLError, TimeoutError, OSError) as e:
        record['error'] = str(e)
    record['duration_ms'] = round((time.monotonic() - start) * 1000)
    return record


def models(provider):
    return [{'id': k, 'name': v.get('name'), 'cost': v.get('cost'),
             'status': v.get('status')}
            for k, v in provider.get('models', {}).items()]


def providers(x):
    connected = x.get('connected', [])
    return {
        'connected': connected,
        'catalogue_provider_count': len(x.get('all', [])),
        'catalogue_provider_ids': [p['id'] for p in x.get('all', [])],
        'local_catalogue_candidates': [
            {'id': p['id'], 'connected': p['id'] in connected,
             'model_count': len(p.get('models', {}))}
            for p in x.get('all', []) if p['id'] in ['lmstudio', 'ollama', 'localai', 'llamacpp']],
        'connected_providers': [
            {'id': p['id'], 'name': p.get('name'), 'source': p.get('source'),
             'option_keys_only': sorted(p.get('options', {})), 'models': models(p)}
            for p in x.get('all', []) if p['id'] in connected],
        'connected_defaults': {k: v for k, v in x.get('default', {}).items() if k in connected},
        'projection_policy': 'No provider option values, API keys, headers or auth data saved. Catalogue entries do not establish availability.',
    }


def effective_config(x):
    return {'top_level_keys': sorted(x),
            'selected': {k: x[k] for k in ['model', 'small_model', 'default_agent',
                                           'enabled_providers', 'disabled_providers'] if k in x},
            'configured_providers': [
                {'id': k, 'option_keys_only': sorted(v.get('options', {})),
                 'model_ids': sorted(v.get('models', {}))}
                for k, v in x.get('provider', {}).items()]}


def snapshot():
    paths = [CONFIG, ROOT/'go.mod', ROOT/'go.sum', ROOT/'Makefile', ROOT/'README.md']
    for folder in ['cmd', 'internal', 'tests', 'probes']:
        paths.extend((ROOT/folder).rglob('*.go'))
    for folder in ['S1-manual-login-retry-a128f1ae', 'S1-codex-edge-cases-eb2e9a87']:
        # Only top-level evidence, never recurse into credential-bearing scratch directories.
        paths.extend((ROOT/'docs/evidence'/folder).glob('*.json'))
        paths.append(ROOT/'docs/evidence'/folder/'README.md')
    return {str(p): hashlib.sha256(p.read_bytes()).hexdigest() for p in sorted(set(paths)) if p.is_file()}


stage = sys.argv[1]
if stage == 'discover':
    save('execution-policy', {
        'at': now(), 'collaboration_session_id': '458ce2e5-e679-4fb0-a769-897ce24d33ed',
        'task_id': 's1-opencode-local-provider', 'target': TARGET, 'native_id': SID,
        'http_methods': ['GET'], 'model_requests': 0, 'free_tier_model_requests': 0,
        'auth_attempts': 0, 'credential_files_read_or_copied': False,
        'config_edits': False, 'codex_claude_operations': False, 's2': False,
        'history_cli': 'ochist unavailable; supplied S1 evidence used',
        'fresh_worker_session': 'Existing formal READY only establishes collaboration routing; not a native model ACK.',
    })
    save('snapshot-before', snapshot())
    run('opencode-processes', ['/bin/ps', '-p', '26680,44608', '-o', 'pid=,ppid=,tty=,command='])
    run('opencode-version', ['/opt/homebrew/bin/opencode', '--version'])
    run('target-tree', [CMUX, '--json', '--id-format', 'uuids', 'tree', '--window', TARGET['window_id']])
    run('local-listeners', ['/usr/sbin/lsof', '-nP', '-iTCP', '-sTCP:LISTEN', '-Fpcn'])
    specs = [
        ('server-health', '/global/health', lambda x: {k: x.get(k) for k in ['healthy', 'version']}),
        ('providers', '/provider', providers),
        ('config-providers', '/config/providers', lambda x: {
            'default': x.get('default'), 'providers': [
                {'id': p['id'], 'source': p.get('source'), 'models': models(p)}
                for p in x.get('providers', [])]}),
        ('effective-config', '/config', effective_config),
        ('native-session', '/session/'+SID, lambda x: {
            k: x.get(k) for k in ['id', 'directory', 'version', 'model', 'time']}),
        ('session-status', '/session/status', lambda x: {
            'target_status': x.get(SID, 'idle'), 'target_present_in_busy_status': SID in x}),
    ]
    with concurrent.futures.ThreadPoolExecutor(max_workers=6) as pool:
        futures = [(name, pool.submit(get, BASE+path, project)) for name, path, project in specs]
        for name, f in futures:
            record = f.result()
            save(name, record)
            print(json.dumps({'evidence': name, 'http_status': record.get('http_status'), 'error': record.get('error')}))
    # Metadata endpoints only. No model completions, downloads, starts, logins or keys.
    urls = [
        'http://127.0.0.1:11434/api/version', 'http://127.0.0.1:11434/api/tags',
        'http://127.0.0.1:1234/v1/models', 'http://127.0.0.1:8080/v1/models',
        'http://127.0.0.1:8000/v1/models', 'http://127.0.0.1:5000/v1/models',
        'http://127.0.0.1:8787/v1/models',
    ]
    def local_models(x):
        return {'version': x.get('version'),
                'model_ids': [m.get('id', m.get('name', m.get('model')))
                              for m in x.get('data', x.get('models', []))],
                'models_field_present': 'data' in x or 'models' in x}
    with concurrent.futures.ThreadPoolExecutor(max_workers=7) as pool:
        endpoints = list(pool.map(lambda url: get(url, local_models), urls))
    save('local-endpoints', {'at': now(), 'scope': 'Common local model ports and existing Python listeners 5000/8787 only; no exhaustive host/network scan.', 'results': endpoints})
    for r in endpoints:
        print(json.dumps(r, ensure_ascii=False))
elif stage == 'provider-retry':
    record = get(BASE+'/provider', providers, limit=32 * 1024 * 1024)
    save('providers-retry', record)
    print(json.dumps({'evidence': 'providers-retry', 'http_status': record.get('http_status'),
                      'error': record.get('error'), 'response_bytes_read': record.get('response_bytes_read'),
                      'connected': record.get('projection', {}).get('connected')}))
elif stage == 'checks':
    # Use the same checkout-local Go caches and readonly module flags as Makefile.
    for directory in ['.cache/go-build', '.cache/go-mod', '.cache/gopath', '.cache/tmp', 'bin']:
        assert (ROOT/directory).is_dir(), directory
    os.environ.update(GOTOOLCHAIN='local', GOCACHE=str(ROOT/'.cache/go-build'),
                      GOMODCACHE=str(ROOT/'.cache/go-mod'), GOPATH=str(ROOT/'.cache/gopath'),
                      GOTMPDIR=str(ROOT/'.cache/tmp'), TMPDIR=str(ROOT/'.cache/tmp'),
                      GOFLAGS='-mod=readonly', CGO_ENABLED='0')
    for name, argv in [('go-test', ['go', 'test', './...']),
                       ('go-build', ['go', 'build', './...']),
                       ('make-check', ['make', 'check'])]:
        r = run(name, argv)
        assert r['exit_code'] == 0, name
elif stage == 'verify':
    before = json.loads((OUT/'snapshot-before.json').read_text())
    after = snapshot()
    save('snapshot-after', after)
    assert before == after, 'Protected source/config/prior evidence changed'
    save('protected-files-verification', {'at': now(), 'status': 'PASS',
         'hashed_file_count': len(before), 'isolated_config_unchanged': True,
         'go_source_and_build_definitions_unchanged': True,
         'prior_top_level_evidence_unchanged': True,
         'scope': 'Exact paths listed in snapshots; no auth file read, no claim about unrelated runtime files.'})
    def load(name):
        return json.loads((OUT/(name+'.json')).read_text())
    prior = json.loads((ROOT/'docs/evidence/S1-codex-edge-cases-eb2e9a87/runtime-matrix.json').read_text())
    matrix = load('runtime-matrix')
    verification = load('opencode-verification')
    policy = load('execution-policy')
    assert matrix['runtime_passes'] == prior['runtime_passes']
    assert matrix['new_runtime_passes'] == verification['runtime_passes'] == []
    assert matrix['s1_status'] == 'PARTIAL'
    assert verification['model_requests'] == policy['model_requests'] == 0
    assert verification['free_tier_model_requests'] == policy['free_tier_model_requests'] == 0
    assert not verification['nonce_sent'] and not verification['model_ack_verified']
    assert verification['transport_status'] == 'VERIFIED_TRANSPORT_ONLY'
    assert load('providers-retry')['projection']['connected'] == ['opencode']
    configured = load('config-providers')['projection']['providers']
    assert [p['id'] for p in configured] == ['opencode']
    assert len(configured[0]['models']) == 8
    assert all(m['cost']['input'] == m['cost']['output'] == 0 for m in configured[0]['models'])
    assert load('effective-config')['projection']['configured_providers'] == []
    assert load('target-tree')['exit_code'] == 1
    assert 'Window not found' in load('target-tree')['stderr']
    assert '44608' not in load('opencode-processes')['stdout']
    assert load('native-session')['projection']['id'] == SID
    assert load('session-status')['projection']['target_status'] == 'idle'
    endpoints = load('local-endpoints')['results']
    assert len(endpoints) == 7
    assert all('Connection refused' in r.get('error', '') for r in endpoints[:5])
    assert all(r.get('http_status') == 404 for r in endpoints[5:])
    for name in ['go-test', 'go-build', 'make-check']:
        assert load(name)['exit_code'] == 0, name
    import re
    documents = [OUT/'README.md', ROOT/'docs/evidence/S1-result.md',
                 ROOT/'docs/evidence/S1-capability-matrix.md']
    links_checked = 0
    for doc in documents:
        for target in re.findall(r'\]\(([^)]+)\)', doc.read_text()):
            if target.startswith(('http:', 'https:', '#')):
                continue
            linked = doc.parent/target.split('#')[0]
            # This record is written below; handoff is performed after validation.
            if linked in [OUT/'handoff.json', OUT/'evidence-validation.json']:
                continue
            assert linked.exists(), str(linked)
            links_checked += 1
    save('evidence-validation', {
        'at': now(), 'status': 'PASS', 'inherited_codex_runtime_passes': len(matrix['runtime_passes']),
        'new_runtime_passes': 0, 'opencode_model_requests': 0,
        'configured_provider_ids': ['opencode'], 'free_model_count': 8,
        'local_get_probes': 7, 'local_model_endpoint_found': False,
        'visible_target_missing_confirmed': True, 'source_config_and_prior_evidence_hashes_match': True,
        'engineering_exit_codes': {name: load(name)['exit_code'] for name in ['go-test', 'go-build', 'make-check']},
        'existing_document_links_checked': links_checked,
        'handoff': 'FINAL_ACTION_PENDING_NOT_MODEL_ACK',
    })
    print(json.dumps({'evidence': 'evidence-validation', 'status': 'PASS', 'new_runtime_passes': 0}))
elif stage == 'handoff':
    assert json.loads((OUT/'evidence-validation.json').read_text())['status'] == 'PASS'
    argv = [
        '/Users/funlake/Projects/okra-wiki/.claude/skills/okra-agent-init/scripts/agent-handoff.sh',
        'send', '--window', '1', '--session-id', '458ce2e5-e679-4fb0-a769-897ce24d33ed',
        '--status', 'DONE',
        '--summary', 'OpenCode 只读检查完成：仅 connected=opencode 的8个免费模型，无已接入可用本地provider；MODEL_PROVIDER_UNAVAILABLE。常见本地端点拒绝连接或模型列表404；原可见窗口已不存在。模型请求0，保留 VERIFIED_TRANSPORT_ONLY，新增runtime_pass=0，S1 PARTIAL/WAITING_DEPENDENCY。test/build/make check均通过。',
        '--evidence', str(OUT),
        '--next', '继续停在S1。等待已接入且允许使用的本地provider及绑定原native session的安全可见目标；不重试免费模型、不登录、不购买、不改配置。本轮不操作Codex/Claude，不进入S2。',
    ]
    result = run('handoff', argv)
    assert result['exit_code'] == 0 and '[DONE] handoff sent' in result['stdout']
    save('handoff-verification', {'at': now(), 'status': 'PASS', 'command_exit_code': 0,
         'done_handoff_sent_confirmed': True, 'model_ack': False})
    print(result['stdout'])
else:
    raise SystemExit('Unknown stage')
