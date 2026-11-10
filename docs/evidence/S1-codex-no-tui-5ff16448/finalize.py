"""Derive findings from captured evidence; never upgrade a manual ACK to autowake."""
import datetime
import hashlib
import json
from pathlib import Path
import subprocess
import sys

ROOT = Path('/Users/funlake/Projects/agentflow')
OUT = Path(__file__).resolve().parent
PRIOR = ROOT/'docs/evidence/S1-codex-interrupt-0cf09894'
SESSION = '5ff16448-f0a6-44a5-a827-350891d0429c'


def now():
    return datetime.datetime.now(datetime.timezone.utc).isoformat()


def load(name):
    return json.loads((OUT/(name+'.json')).read_text())


def save(name, value):
    with (OUT/(name+'.json')).open('x') as f:
        json.dump(value, f, ensure_ascii=False, indent=2)
        f.write('\n')


def stamp(text):
    return datetime.datetime.fromisoformat(text.replace('Z','+00:00'))


def topology(name):
    data = json.loads(load(name)['stdout'])
    return [(w['id'], ws['id'], ws['layout'], [(p['id'], [(s['id'],s['tty'],s['selected']) for s in p['surfaces']]) for p in ws['panes']]) for w in data['windows'] for ws in w['workspaces']]


stage = sys.argv[1]
if stage == 'report':
    policy = load('execution-policy')
    target = policy['target']
    nonce = policy['nonce']
    initial = load('initial-native')
    baseline = load('no-tui-before-queue-native')
    queue = load('queue-result')
    boundary = load('manual-resume-boundary')
    final = load('final-native')
    delta = [r for r in final['records'] if r.get('ordinal',-1) > initial['records'][-1]['ordinal']]
    save('native-transcript-delta', dict(at=now(),native_id=target['native_id'],records=delta,system_developer_messages_filtered=True))
    acks = [r for r in delta if r['type']=='response_item' and r['payload'].get('role')=='assistant' and any('NO_TUI_ACK '+nonce in c.get('text','') for c in r['payload'].get('content',[]))]
    users = [r for r in delta if r['type']=='response_item' and r['payload'].get('role')=='user' and any(nonce in c.get('text','') for c in r['payload'].get('content',[]))]
    tools = [r for r in delta if r['type']=='event_msg' and r['payload'].get('item',{}).get('type')=='CommandExecution']
    starts = [r for r in delta if r['payload'].get('type')=='task_started']
    assert len(acks)==len(users)==len(tools)==len(starts)==1
    assert tools[0]['payload']['item']['exit_code']==0
    assert tools[0]['payload']['item']['aggregated_output'].strip()==initial['row'][1]
    assert all(stamp(r['timestamp']) > stamp(boundary['at']) for r in acks+users+tools+starts)
    observations=[]
    for label in ['no-tui-plus30','no-tui-plus60','no-tui-plus90','immediate-pre-manual-resume']:
        n = load(label+'-native')
        processes = load(label+'-tty-processes')
        text = load(label+'-screen-decoded')['text']
        assert n['raw_sha256']==baseline['raw_sha256'] and n['raw_bytes']==baseline['raw_bytes']
        assert '/opt/homebrew/bin/codex' not in processes['stdout']
        assert 'Ask Codex to do anything' not in text[-700:]
        assert nonce not in text
        assert topology(label+'-tree')==topology('initial-tree')
        observations.append(dict(evidence=label,at=n['at'],seconds_after_queue=round((stamp(n['at'])-stamp(queue['end'])).total_seconds(),3),rollout_unchanged=True,target_tty_codex_absent=True,new_visible_tui=False,native_ack=False))
    assert observations[-1]['seconds_after_queue']>=90
    assert queue['exit_code']==0 and 'could not resume thread' in queue['stderr']
    original_exit=json.loads((PRIOR/'resume-exit.json').read_text())
    assert original_exit['pid']==11697 and original_exit['exit_code']==0
    save('original-wrapper-exit',original_exit)
    history={}
    for label in ['A','B']:
        old=load('initial-'+label+'-raw-binding')
        raw=Path(old['path']).read_bytes()
        history[label]=dict(path=old['path'],original_bytes=old['byte_count'],final_bytes=len(raw),original_sha256=old['sha256'],prefix_sha256=hashlib.sha256(raw[:old['byte_count']]).hexdigest(),final_sha256=hashlib.sha256(raw).hexdigest())
        assert history[label]['original_sha256']==history[label]['prefix_sha256']
        history[label]['original_prefix_preserved']=True
        history[label]['unchanged']=history[label]['final_sha256']==old['sha256']
    assert history['B']['unchanged']
    save('native-history-preservation',dict(at=now(),status='PASS',threads=history))
    process=load('resume-process')
    assert process['argv'][:-2]==policy['original_argv'] and process['argv'][-1]==target['native_id']
    assert str(process['pid']) in load('final-tty-processes')['stdout']
    assert process['cwd'] in load('final-cwd')['stdout']
    assert target['native_id'] in load('final-screen-decoded')['text']
    assert 'Ask Codex to do anything' in load('final-screen-decoded')['text'][-400:]
    assert topology('initial-tree')==topology('final-tree')
    findings=dict(at=now(),target=target,nonce=nonce,queue_id=queue['stdout'].split()[2],queue_exit_code=queue['exit_code'],queue_warning=queue['stderr'],queue_start=queue['start'],queue_end=queue['end'],baseline_rollout_bytes=baseline['raw_bytes'],baseline_rollout_sha256=baseline['raw_sha256'],observations=observations,manual_resume_at=boundary['at'],manual_resume_process=process,first_native_task_started_at=starts[0]['timestamp'],native_tool=tools[0],native_assistant_ack=acks[0],native_user_message_count=1,automatic_native_ack=False,automatic_visible_tui_rebuild=False,manual_resume_ack=True,classification='PERSISTED_UNTIL_MANUAL_RESUME',autowake_acceptance='UNVERIFIED / WAITING_DEPENDENCY',scope='Existing authorized --no-daemon environment with no selected remote/local daemon; other environments not tested',final_state='LIVE_IDLE_EMPTY_PROMPT')
    save('codex-verification',findings)
    prior=json.loads((PRIOR/'runtime-matrix.json').read_text())
    matrix=dict(at=now(),collaboration_session_id=SESSION,task_id=policy['task_id'],s1_status='PARTIAL',runtime_acceptance='UNVERIFIED / WAITING_DEPENDENCY',this_round='PERSISTED_UNTIL_MANUAL_RESUME_NO_AUTOWAKE_OBSERVED',runtime_passes=prior['runtime_passes'],new_runtime_passes=[],historical_failures=prior['failures'],unverified=prior['unverified'],tools=[dict(tool='codex',status='AUTOWAKE_UNVERIFIED_WAITING_DEPENDENCY',target=target,findings='codex-verification.json',queue_exit_code=0,automatic_native_ack=False,automatic_visible_tui_rebuild=False,manual_resume_ack=True,final_pid=process['pid'],final_state='LIVE_IDLE_EMPTY_PROMPT'),dict(tool='opencode',status='VERIFIED_TRANSPORT_ONLY / MODEL_PROVIDER_UNAVAILABLE / WAITING_DEPENDENCY',new_calls=0),dict(tool='claude',status='EXCLUDED',new_calls=0)],checks=dict(evidence_validation='evidence-validation.json',protected_files='protected-files-verification.json',go_test='NOT_RUN_EVIDENCE_ONLY',go_build='NOT_RUN_EVIDENCE_ONLY',make_check='NOT_RUN_EVIDENCE_ONLY',doctor='NOT_RUN'),go_source_changed=False,s2=False,handoff_is_native_ack=False)
    save('runtime-matrix',matrix)
    save('dependencies',dict(at=now(),status='WAITING_DEPENDENCY_FOR_COMPLETE_S1',this_round='COMPLETE_PERSISTENCE_AND_MANUAL_RESUME_OBSERVATION',codex=dict(target=target,current_pid=process['pid'],state='LIVE_IDLE_EMPTY_PROMPT',queue_autowake_without_live_tui='UNVERIFIED / WAITING_DEPENDENCY',observed='No native progress or visible TUI before manual resume; ACK only afterwards',dependency='queue --resume reports no selected local daemon or remote server',next='Remain in S1. A separately authorized daemon/remote environment and complete native/visible evidence are needed to assess that path; do not infer it from manual resume.',remaining_scope=prior['unverified']),opencode=dict(status='VERIFIED_TRANSPORT_ONLY / MODEL_PROVIDER_UNAVAILABLE / WAITING_DEPENDENCY',new_requests=0,login_allowed=False),claude='EXCLUDED',history_cli='UNAVAILABLE; supplied prior evidence reused',risk_cli='UNAVAILABLE; no fabricated judgment; scoped reversible authorization',s2='DO_NOT_ENTER'))
    save('evidence-validation',dict(at=now(),status='PASS',assertions=dict(original_normal_quit_exit_zero=True,no_tui_observation_over_90_seconds=True,all_observed_rollout_hashes_unchanged_before_manual_resume=True,no_target_tty_codex_or_visible_tui_before_resume=True,native_task_tool_and_ack_strictly_after_manual_resume=True,unique_native_user_tool_and_ack=True,original_surface_cwd_native_thread_restored=True,final_empty_prompt=True,topology_preserved=True,A_history_prefix_preserved=True,B_unchanged=True,no_autowake_pass_awarded=True),functional_acceptance='UNVERIFIED / WAITING_DEPENDENCY'))
    print(json.dumps(dict(status='PASS',classification=findings['classification'],observation_seconds=observations[-1]['seconds_after_queue'],ack_at=acks[0]['timestamp'],final_pid=process['pid'])))
elif stage=='verify':
    old=load('inventory-before')['hashes']
    changed=[]
    missing=[]
    for path,digest in old.items():
        p=ROOT/path
        if not p.exists():
            missing.append(path)
        elif hashlib.sha256(p.read_bytes()).hexdigest()!=digest:
            changed.append(path)
    allowed=['docs/evidence/S1-result.md','docs/evidence/S1-capability-matrix.md']
    assert not missing and set(changed)==set(allowed), (changed,missing)
    current={str(p.relative_to(ROOT)) for p in (ROOT/'docs/evidence').rglob('*') if p.is_file() and not p.is_relative_to(OUT) and 'scratch' not in p.relative_to(ROOT/'docs/evidence').parts}
    additions=sorted(current-set(old))
    assert additions==['docs/evidence/S1-codex-interrupt-0cf09894/resume-exit.json'], additions
    save('protected-files-verification',dict(at=now(),status='PASS',existing_files_checked=len(old),changed_authorized_documents=changed,missing=missing,unexpected_changes=[],prior_evidence_additions=additions,addition_reason='Already-running prior wrapper normally wrote its previously absent resume-exit.json after /quit; no existing evidence overwritten',go_and_build_definitions_unchanged=True,config_unchanged=True,credentials_read=False))
    for filename in allowed:
        text=(ROOT/filename).read_text()
        assert 'S1-codex-no-tui-5ff16448' in text and 'UNVERIFIED / WAITING_DEPENDENCY' in text
    for p in OUT.glob('*.json'):
        json.loads(p.read_text())
    save('document-validation',dict(at=now(),status='PASS',updated=allowed,json_parse='PASS'))
    mutations=[]
    for p in OUT.glob('*.json'):
        d=json.loads(p.read_text())
        argv=d.get('argv',[])
        if argv and argv[0].endswith('/cmux') and ('send' in argv or 'send-key' in argv):
            for option,key in [('--window','window'),('--workspace','workspace'),('--surface','surface')]:
                assert argv[argv.index(option)+1]==load('execution-policy')['target'][key]
            mutations.append(p.name)
    save('target-safety-verification',dict(at=now(),status='PASS',cmux_mutations=mutations,all_explicitly_target_authorized_surface=True,opencode_model_calls=0,claude_operations=0,external_os_signals=0,go_changes=False,s2=False))
    hashes={p.name:hashlib.sha256(p.read_bytes()).hexdigest() for p in sorted(OUT.iterdir()) if p.is_file()}
    hashes.update({str(ROOT/f):hashlib.sha256((ROOT/f).read_bytes()).hexdigest() for f in allowed})
    save('sha256-manifest',dict(at=now(),hashes=hashes,exclusions=['sha256-manifest.json','hash-verification.json','handoff.json'],reason='No self-reference; handoff added after final verification'))
    for path,digest in hashes.items():
        p=Path(path) if Path(path).is_absolute() else OUT/path
        assert hashlib.sha256(p.read_bytes()).hexdigest()==digest
    save('hash-verification',dict(at=now(),status='PASS',files_checked=len(hashes),manifest='sha256-manifest.json'))
    print(json.dumps(dict(status='PASS',existing_files_checked=len(old),manifest_files=len(hashes),old_evidence_additions=additions)))
elif stage=='handoff':
    for name in ['evidence-validation','protected-files-verification','document-validation','target-safety-verification','hash-verification']:
        assert load(name)['status']=='PASS'
    for path,digest in load('sha256-manifest')['hashes'].items():
        p=Path(path) if Path(path).is_absolute() else OUT/path
        assert hashlib.sha256(p.read_bytes()).hexdigest()==digest
    argv=['/Users/funlake/.codex/skills/okra-agent-init/scripts/agent-handoff.sh','send','--window','1','--session-id',SESSION,'--status','DONE','--summary','本轮 S1 无 TUI 唤醒调查完成：原生 queue --resume exit=0 但警告没有可恢复的 local daemon/remote；无 TUI 观察超过90秒 rollout完全不变，无 native ACK、无自动重建 TUI。仅手动 resume 原 thread 后执行 /bin/pwd 并真实 NO_TUI_ACK。自动唤醒 UNVERIFIED / WAITING_DEPENDENCY，完整 S1 PARTIAL，新增 runtime PASS=0。','--evidence',str(OUT)+'/README.md；queue-result.json、codex-verification.json、native-transcript-delta.json、manual-resume-boundary.json、runtime-matrix.json、dependencies.json、evidence-validation.json、protected-files-verification.json、sha256-manifest.json、hash-verification.json。两份 S1 总文档已更新。','--next','保持 S1：当前 --no-daemon 环境缺少选定 local daemon/remote，后续评估需另行授权该路径。最终原 thread A/surface/cwd/ttys028 已恢复，PID 19007 空闲且输入栏为空；原布局与 A 历史前缀/B 全文保留。无 Go 修改或 Go/doctor 检查，OpenCode 模型调用0，Claude无操作，无登录/凭据复制/配置变更，不进入 S2。DONE 仅为调查交付，不是自动唤醒 PASS。']
    start=now()
    p=subprocess.run(argv,cwd=ROOT,capture_output=True,text=True,timeout=30)
    save('handoff',dict(start=start,end=now(),argv=argv,exit_code=p.returncode,stdout=p.stdout,stderr=p.stderr))
    print(p.stdout,end='')
    assert p.returncode==0 and '[DONE] handoff sent' in p.stdout
else:
    raise SystemExit('Unknown stage')
