"""Derive conclusions from native messages, tools, visible panes and cleanup."""
import datetime
import hashlib
import json
import pathlib
import re
import sys

OUT = pathlib.Path(__file__).resolve().parent
ROOT = OUT.parents[2]
PRIOR = OUT.parent / 'S1-codex-final-edge-fd8f8aac'


def load(name):
    return json.loads((OUT / (name+'.json')).read_text())


def save(name, data):
    with (OUT / (name+'.json')).open('x') as f:
        json.dump(data, f, ensure_ascii=False, indent=2)
        f.write('\n')


def compact(text):
    return re.sub(r'\s+', '', text)


def assistant(label, prefix):
    found = []
    for r in load('final-'+label+'-native')['records']:
        p = r.get('payload', {})
        if r['type'] == 'response_item' and p.get('type') == 'message' and p.get('role') == 'assistant':
            text = '\n'.join(c.get('text', '') for c in p.get('content', []))
            if prefix in text:
                found.append(dict(message_id=p['id'], timestamp=r['timestamp'], text=text, native_id=load('seed-'+label+'-request')['native_id']))
    assert len(found) == 1, (label, prefix, len(found))
    return found[0]


def topology(name):
    tree = json.loads(load(name)['stdout'])
    return {w['id']: {'layout': w['layout'], 'panes': {p['id']: p['surface_ids'] for p in w['panes']}} for win in tree['windows'] for w in win['workspaces']}


at = datetime.datetime.now(datetime.timezone.utc).isoformat()
if len(sys.argv) > 1 and sys.argv[1] == 'documents':
    documents = [OUT / 'README.md', OUT.parent / 'S1-result.md', OUT.parent / 'S1-capability-matrix.md']
    checked = []
    for path in documents:
        for link in re.findall(r'\]\(([^)]+)\)', path.read_text()):
            if '://' not in link and not link.startswith('#'):
                assert (path.parent / link.split('#')[0]).resolve().exists(), (path, link)
                checked.append(dict(document=str(path.relative_to(ROOT)), link=link))
    paths = list(OUT.glob('*.json'))
    for path in paths:
        json.loads(path.read_text())
    matrix = load('runtime-matrix')
    assert len(matrix['runtime_passes']) == 19 and matrix['s1_status'] == 'PARTIAL'
    save(sys.argv[2] if len(sys.argv)>2 else 'document-validation', dict(at=at, status='PASS', checked_links=checked, json_files_parsed=len(paths), runtime_passes=19, new_runtime_passes=1, manifest={p.name: hashlib.sha256(p.read_bytes()).hexdigest() for p in sorted(OUT.iterdir()) if p.is_file()}, manifest_excludes='This validation file and later handoff evidence'))
    print(json.dumps(dict(status='PASS', links=len(checked), json_files=len(paths), runtime_passes=19)))
    raise SystemExit(0)

before, after = load('inventory-before')['hashes'], load('inventory-after')['hashes']
assert before == after, [(p, before.get(p), after.get(p)) for p in set(before)|set(after) if before.get(p) != after.get(p)]
assert topology('initial-tree') == topology('final-tree-settled')
binding = load('both-seeded-binding')
recall_binding = load('both-recall-inflight-binding')
assert {k: {f:v for f,v in d.items() if f != 'screen'} for k,d in binding['threads'].items()} == {k: {f:v for f,v in d.items() if f != 'screen'} for k,d in recall_binding['threads'].items()}
assert binding['simultaneous_visible_panes'] and recall_binding['simultaneous_visible_panes']
assert binding['threads']['A']['pid'] == 96559
assert len({v['native_id'] for v in binding['threads'].values()}) == 2
assert len({v['cwd'] for v in binding['threads'].values()}) == 2
checks = {}
for label, other in [('A','B'), ('B','A')]:
    request = load('seed-'+label+'-request')
    recall = load('recall-'+label+'-request')
    data = load('final-'+label+'-native')
    own = binding['threads'][label]
    other_binding = binding['threads'][other]
    assert request['native_id'] == data['native_id'] == own['native_id']
    assert request['surface_id'] == own['surface_id']
    assert request['sender_cwd'] == recall['sender_cwd'] == other_binding['cwd']
    assert request['nonce'] not in recall['message'] and recall['private_nonce_omitted']
    calls = [r['payload'] for r in data['records'] if r.get('payload',{}).get('type') == 'item_completed' and r['payload'].get('item',{}).get('type') == 'CommandExecution' and request['nonce'] in r['payload']['item'].get('aggregated_output','')]
    assert len(calls) == 1
    call = calls[0]
    output = json.loads(call['item']['aggregated_output'])
    assert call['thread_id'] == own['native_id'] and call['item']['exit_code'] == 0
    assert output['cwd'] == own['cwd'] and output['surface'] == own['surface_id'] and output['workspace'] == binding['workspace_id']
    assert call['item']['cwd'] == 'file://'+own['cwd']
    seed_ack = assistant(label, 'CONCURRENT_PRIVATE_ACK '+label+' '+request['nonce'])
    recall_ack = assistant(label, 'CONCURRENT_RECALL '+recall['nonce'])
    assert request['nonce'] in recall_ack['text'] and 'UNKNOWN' in recall_ack['text']
    assert ' '+label+' '+request['nonce'] in recall_ack['text']
    assert datetime.datetime.fromisoformat(seed_ack['timestamp']) > datetime.datetime.fromisoformat(load('b-launch')['at'])
    assert datetime.datetime.fromisoformat(recall_ack['timestamp']) < datetime.datetime.fromisoformat(load('b-exit')['at'])
    screen = load('both-seeded-'+label+'-screen-decoded')['text']
    assert compact(seed_ack['text'].split('\n')[0]) in compact(screen)
    assert compact(output['cwd']) in compact(screen)
    assert compact(output['surface']) in compact(screen)
    recalled_screen = load('both-recall-inflight-'+label+'-screen-decoded')['text']
    assert compact(recall_ack['text']) in compact(recalled_screen)
    assert 'Ask Codex to do anything' in recalled_screen
    for forbidden in [load('seed-'+other+'-request')['nonce'], load('recall-'+other+'-request')['nonce']]:
        assert forbidden not in json.dumps(data['records'])
        assert compact(forbidden) not in compact(screen+recalled_screen)
    queue_ids = {}
    for kind in ['seed','recall']:
        result = load(kind+'-'+label+'-queue')
        match = re.fullmatch(r'Queued message ([\w-]+) for thread ([\w-]+)\.\n', result['stdout'])
        assert result['exit_code'] == 0 and match and match[2] == own['native_id']
        queue_ids[kind] = match[1]
    checks[label] = dict(binding=own, private_nonce=request['nonce'], route_nonce=recall['nonce'], sender_cwd=request['sender_cwd'], native_tool=call, native_seed_ack=seed_ack, native_recall_ack=recall_ack, native_queue_ids=queue_ids, cross_private_and_route_nonce_absent=True)

# Existing native history is preserved as a prefix, with new probe turns appended.
initial_a = load('initial-A-native')['records']
assert load('final-A-native')['records'][:len(initial_a)] == initial_a
prior_b = json.loads((PRIOR / 'native-b-final.json').read_text())['records']
for record in prior_b:
    if record['type'] == 'session_meta':
        record['payload'].pop('source', None)
assert load('final-B-native')['records'][:len(prior_b)] == prior_b
assert load('b-exit')['exit_code'] == 0
assert load('cleanup-b-process-absent')['exit_code'] == 1
assert load('close-owned-b-split')['exit_code'] == 0
assert '96559 96558' in load('final-A-process')['stdout']
assert 'Ask Codex to do anything' in load('final-A-screen-decoded')['text']
mutations = []
for path in OUT.glob('*.json'):
    d = json.loads(path.read_text())
    argv = d.get('argv', [])
    if argv and argv[0].endswith('/cmux') and any(v in argv for v in ['send','send-key','new-split','close-surface']):
        assert argv[argv.index('--window')+1] == binding['window_id']
        assert argv[argv.index('--workspace')+1] == binding['workspace_id']
        assert argv[argv.index('--surface')+1] in {v['surface_id'] for v in binding['threads'].values()}
        if 'close-surface' in argv:
            assert argv[argv.index('--surface')+1] == binding['threads']['B']['surface_id']
        mutations.append(path.name)

limits = ['Controlled native CLI message/context/cwd isolation; shared CODEX_HOME and OS filesystem are not a security boundary.', 'Both TUI processes and selected panes were simultaneously live; no claim of parallel tool execution is needed or made.', 'The checkpoint named both-recall-inflight captured completed recall replies, not active inference.', 'Native CLI evidence does not implement AgentFlow adapters, hooks or automatic recovery.']
cleanup = dict(secondary_native_id=checks['B']['binding']['native_id'], secondary_exit_code=0, secondary_pid_absent=True, owned_b_surface_closed=True, original_a_pid=96559, original_a_state='LIVE_IDLE_EMPTY_PROMPT', original_topology_restored=True, existing_native_histories_preserved=True, signals_sent=0)
save('codex-verification', dict(at=at, status='PASS_FOR_CONTROLLED_CONCURRENT_LIVE_TWO_TUI_ISOLATION', threads=checks, checkpoints=['both-seeded-binding.json','both-recall-inflight-binding.json'], cleanup=cleanup, caveats=limits, handoff_is_native_ack=False))
save('protected-files-verification', dict(at=at, status='PASS', existing_source_build_config_and_prior_evidence_hashes_unchanged=True, existing_native_histories_preserved_as_prefix=True, original_experiment_topology_restored=True, terminal_mutations=mutations, all_mutations_target_authorized_experiment=True, credentials_read=False))
prior = json.loads((PRIOR / 'runtime-matrix.json').read_text())
new_pass = 'codex.concurrent_live_two_tui_isolation'
pending = [x for x in prior['unverified'] if x != 'concurrent_live_two_tui_isolation']
save('runtime-matrix', dict(at=at, collaboration_session_id='754ba5a3-ca1d-43a7-ba19-b14301cc33f1', task_id='s1-codex-concurrent-tui', s1_status='PARTIAL', runtime_acceptance='PARTIAL', this_round='PASS_FOR_CONTROLLED_CONCURRENT_LIVE_TWO_TUI_ISOLATION', runtime_passes=prior['runtime_passes']+[new_pass], new_runtime_passes=[new_pass], unverified=pending, tools=[dict(tool='codex', status='PASS_FOR_CONTROLLED_CONCURRENT_LIVE_TWO_TUI_ISOLATION', findings='codex-verification.json', concurrent_binding=binding, cleanup=cleanup, prior_passes_source='../S1-codex-final-edge-fd8f8aac/runtime-matrix.json'),dict(tool='opencode', status='VERIFIED_TRANSPORT_ONLY / WAITING_DEPENDENCY', new_calls=0),dict(tool='claude', status='EXCLUDED', new_calls=0)], checks=dict(evidence_validation='PASS', protected_files='PASS', go_test='NOT_RUN_NO_GO_CHANGES', go_build='NOT_RUN_NO_GO_CHANGES', make_check='NOT_RUN_NO_GO_CHANGES'), go_source_changed=False, s2=False, handoff_is_native_ack=False))
save('dependencies', dict(at=at, status='WAITING_DEPENDENCY_FOR_COMPLETE_S1', this_round='COMPLETE_CONTROLLED_CONCURRENT_LIVE_TWO_TUI_ISOLATION', this_round_blockers=[], codex=dict(concurrent_live_two_tui_isolation='PASS', remaining_scope=pending, final_state=cleanup, next='Continue S1 only; remaining capabilities require separate scoped implementation or experiments.'), opencode=dict(status='VERIFIED_TRANSPORT_ONLY / WAITING_DEPENDENCY', blocker='MODEL_PROVIDER_UNAVAILABLE_FROM_PRIOR_ROUND_NOT_RETESTED', new_requests=0, login_allowed=False), claude='EXCLUDED', history_cli='UNAVAILABLE; supplied native evidence reused', risk_cli='UNAVAILABLE; no fabricated judgment; reversible explicitly authorized experiment', s2='DO_NOT_ENTER'))
save('evidence-validation', dict(at=at, status='PASS', new_runtime_passes=[new_pass], runtime_passes_total=19, assertions=['two distinct selected visible panes, PID/TTY/native IDs and actual cwd, both processes live in same ps snapshot', 'real native command execution outputs bind nonce/cwd/workspace/surface to each thread', 'native assistant seed ACK and same-screen evidence for both private nonces', 'cross-cwd explicit native queue routing and recall ACK without resending private nonce', 'other private/route nonce absent from entire native record and visible screens', 'original A PID remains live; B normal quit exit 0, PID absent, owned split removed', 'original native histories preserved as prefix; source/config/prior evidence hashes unchanged', 'all terminal mutations restricted to authorized experiment'], limits=limits))
print(json.dumps(dict(status='PASS', new_runtime_passes=[new_pass], runtime_passes_total=19, cleanup=cleanup), ensure_ascii=False))
