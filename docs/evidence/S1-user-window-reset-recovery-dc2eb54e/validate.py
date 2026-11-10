"""Assertions over real native and screen evidence; no runtime feature simulation."""
import hashlib
import json
from pathlib import Path
from record import HERE, ROOT, WINDOW, WORKSPACE, WIKI, TARGETS, now, save


def load(name):
    return json.loads((HERE/(name + '.json')).read_text())


def screen(name):
    r = load(name)
    assert r['exit_code'] == 0
    p = json.loads(r['stdout'])
    assert p['window_id'] == WINDOW and p['workspace_id'] == WORKSPACE
    return p


def oc_parts(record):
    roles = {r['id']: json.loads(r['data'])['role'] for r in record['messages']}
    return [(p, json.loads(p['data']), roles[p['message_id']]) for p in record['parts']]


def cx_messages(record, role):
    return [e['payload'] for e in record['events'] if e['type'] == 'response_item'
            and e['payload'].get('type') == 'message' and e['payload'].get('role') == role]


def text(p):
    return '\n'.join(x.get('text','') for x in p.get('content',[]))


def main():
    oc_before, oc_new, oc_after = (load(x) for x in
        ['original-opencode-native', 'reset-ready-opencode-native', 'restored-opencode-native'])
    cx_before, cx_new, cx_after = (load(x) for x in
        ['before-codex-native', 'reset-ready-explicit-codex-native', 'restored-codex-native'])
    results = {}
    for provider, before, new, after, nonce, old_ack, request, ready_screen in [
        ('opencode', oc_before, oc_new, oc_after, 'S1-OC-reset-dc2eb54e-7f02c8a1',
         'OPENCODE_ACK_7ba139c0-0133-47da-b99d-e7549a22f735', 'S1-OC-recover-dc2eb54e-b9d405f3',
         'opencode-ready-result-screen'),
        ('codex', cx_before, cx_new, cx_after, 'S1-CX-reset-dc2eb54e-a93b50d2',
         'CODEX_ACK_9196eeee-17ab-40ac-91ee-8f558336b201', 'S1-CX-recover-dc2eb54e-2a7801c6',
         'codex-ready-result-screen')]:
        if provider == 'opencode':
            a, b, final = (r['session']['id'] for r in (before,new,after))
            new_assistant = [d['text'] for _,d,role in oc_parts(new) if role=='assistant' and d['type']=='text']
            restored_assistant = [d['text'] for _,d,role in oc_parts(after) if role=='assistant' and d['type']=='text']
            recovery_users = [d['text'] for _,d,role in oc_parts(after) if role=='user'
                              and d['type']=='text' and request in d.get('text','')]
            tools = [(p,d) for p,d,role in oc_parts(new) if d['type']=='tool']
            assert len(tools)==1
            tool_id = tools[0][0]['id']
            assert tools[0][1]['state']['input']=={'command':'pwd'}
            assert tools[0][1]['state']['metadata']['exit']==0
            assert tools[0][1]['state']['output'].strip()==str(WIKI)
            recovery_tools = [d for _,d,_ in oc_parts(after) if d['type']=='tool']
            assert len(recovery_tools)==1 and recovery_tools[0]['state']['output'].strip()==str(WIKI)
            old_parts = {p['id']: json.loads(p['data']) for p in before['parts']}
            final_parts = {p['id']: json.loads(p['data']) for p in after['parts']}
            for part_id, data in old_parts.items():
                assert part_id in final_parts
                if data.get('redacted'):
                    assert final_parts[part_id].get('redacted')
                else:
                    assert data == final_parts[part_id]
            native_evidence = ['original-opencode-native.json','reset-ready-opencode-native.json','restored-opencode-native.json']
        else:
            def sid(r): return next(e['payload']['id'] for e in r['events'] if e['type']=='session_meta')
            a,b,final = (sid(r) for r in (before,new,after))
            new_assistant = [text(p) for p in cx_messages(new,'assistant')]
            restored_assistant = [text(p) for p in cx_messages(after,'assistant')]
            recovery_users = [text(p) for p in cx_messages(after,'user') if request in text(p)]
            tools = [e['payload']['item'] for e in new['events'] if e['payload'].get('type')=='item_completed'
                     and e['payload'].get('item',{}).get('type')=='CommandExecution']
            assert len(tools)==1, tools
            tool_id = tools[0]['id']
            assert tools[0]['exit_code']==0 and tools[0]['aggregated_output'].strip()==str(WIKI)
            contexts = [e['payload'] for e in new['events'] if e['type']=='turn_context']
            assert contexts and all(c['cwd']==str(WIKI) and c['approval_policy']=='never'
               and c['sandbox_policy']['type']=='danger-full-access' and c['permission_profile']['type']=='disabled'
               for c in contexts)
            raw = Path(before['path']).read_bytes()
            assert hashlib.sha256(raw[:before['bytes']]).hexdigest()==before['sha256']
            native_evidence = ['before-codex-native.json','reset-ready-explicit-codex-native.json','restored-codex-native.json']
        assert a!=b and final==a
        assert any('READY nonce='+nonce+' role=资深研发' in t for t in new_assistant)
        assert any('ROLE_ACK nonce='+nonce in t and 'role=资深研发' in t and str(WIKI) in t for t in new_assistant)
        assert len(recovery_users)==1 and old_ack not in recovery_users[0]
        assert old_ack.split('_ACK_')[1] not in recovery_users[0]
        assert any('RESTORED request='+request in t and old_ack in t for t in restored_assistant)
        assert any('RECOVERY_ACK request='+request in t and str(WIKI) in t for t in restored_assistant)
        s = screen(ready_screen)
        final_screen = screen('final-'+provider+'-screen')
        assert s['surface_id']==final_screen['surface_id']==TARGETS[provider][0]
        assert nonce in s['text'] and str(WIKI) in s['text']
        assert request in final_screen['text'] and str(WIKI) in final_screen['text']
        results[provider] = {'window': WINDOW, 'workspace': WORKSPACE, 'surface': TARGETS[provider][0],
            'pid': TARGETS[provider][1], 'tty': TARGETS[provider][2], 'cwd': str(WIKI),
            'original_native_id': a, 'reset_native_id': b, 'restored_native_id': final,
            'reset_nonce': nonce, 'recovery_request': request, 'recalled_original_ack': old_ack,
            'reset_ready': 'PASS', 'role_reinjection': 'PASS_CONTROLLED_ROLE_ACK_AND_PWD',
            'tool_cwd': 'PASS', 'tool_native_item_id': tool_id, 'context_recovery': 'PASS_MANUAL_IN_PROCESS',
            'original_history_prefix': 'PASS', 'same_visible_surface': 'PASS',
            'effective_full_access': 'PASS_NATIVE_POLICY_AND_READ_ONLY_CALL' if provider=='codex' else 'UNVERIFIED',
            'permission_boundary': 'No write or network access attempted; no exhaustive OS permission guarantee',
            'evidence': native_evidence+[ready_screen+'.json','final-'+provider+'-screen.json']}
    tree = json.loads(load('final-tree')['stdout'])
    win = next(w for w in tree['windows'] if w['id']==WINDOW)
    assert win['visible']
    ws = next(w for w in win['workspaces'] if w['id']==WORKSPACE)
    before_tree = json.loads(load('before-tree')['stdout'])
    before_ws = before_tree['windows'][0]['workspaces'][0]
    assert ws['layout']==before_ws['layout']
    surfaces = {s['id']:s for p in ws['panes'] for s in p['surfaces']}
    assert set(surfaces)=={t[0] for t in TARGETS.values()}
    for provider,(surface,pid,tty) in TARGETS.items():
        assert surfaces[surface]['selected_in_pane'] and surfaces[surface]['tty']==tty
        assert 'n'+str(WIKI) in load('final-'+provider+'-cwd')['stdout'].splitlines()
    save('runtime-matrix', {'at':now(), 'overall_s1':'PARTIAL', 'session_id':'dc2eb54e-025e-40a1-8d42-76b7852761f7',
         'task_id':'s1-user-window-reset-recovery', 'mode':'ralph-loop', 'bindings': results,
         'new_user_model_requests': 4, 'new_terminal_read_only_tool_calls': 4,
         'remaining_unverified': ['OpenCode effective full access', 'busy and draft protection in these sessions',
          'hook/queue product adapters','automatic or crash/host recovery','unrestricted write/network behavior'],
         'recovery_boundary':'Native in-process switch back to original session; no exit/restart/kill/crash',
         'historical_pass_count':'Preserved, not inherited or recounted'})
    save('evidence-validation', {'at':now(), 'status':'PASS', 'bindings':results,
         'file_protection':'Separate file-protection-verification.json',
         'final_idle_empty_inputs':'Screen manually inspected after native task completion',
         'boundary':'Evidence assertions do not mean complete S1 PASS; handoff and transport exit codes are excluded'})
    print(json.dumps(results,ensure_ascii=False,indent=2))


if __name__=='__main__': main()
