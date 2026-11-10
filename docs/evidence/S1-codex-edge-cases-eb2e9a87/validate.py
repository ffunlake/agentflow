"""Derive S1 findings from captured native records; never generate native ACKs."""
import datetime, hashlib, json, pathlib, re

OUT=pathlib.Path(__file__).resolve().parent
PRIOR=OUT.parent/'S1-manual-login-retry-a128f1ae'
def load(name,base=OUT): return json.loads((base/(name+'.json')).read_text())
def save(name,obj):
    with (OUT/(name+'.json')).open('x') as f: json.dump(obj,f,ensure_ascii=False,indent=2); f.write('\n')
def timestamp(text): return datetime.datetime.fromisoformat(text.replace('Z','+00:00')).timestamp()
at=datetime.datetime.now(datetime.timezone.utc).isoformat()
target=load('execution-policy')['target']; native=load('execution-policy')['native_id']
final=load('native-final'); records=final['records']
def text(p): return ''.join(c.get('text','') for c in p.get('content',[]))
def assistant_exact(expected):
    found=[r for r in records if r['type']=='response_item' and r['payload'].get('role')=='assistant' and text(r['payload'])==expected]
    assert len(found)==1,(expected,len(found))
    r=found[0]; p=r['payload']
    return {'native_id':native,'message_id':p['id'],'timestamp':r['timestamp'],'text':expected,'source':'native-final.json'}
def normalized_screen(name): return re.sub(r'\s+','',load(name+'-decoded')['text'])
def command_with_nonce(nonce):
    found=[r for r in records if r['type']=='event_msg' and r['payload'].get('type')=='item_completed' and r['payload'].get('item',{}).get('type')=='CommandExecution' and nonce in r['payload']['item'].get('aggregated_output','')]
    assert len(found)==1,(nonce,len(found))
    assert found[0]['payload']['thread_id']==native and found[0]['payload']['item']['exit_code']==0
    return found[0]['payload']

queue_results={}
for name,screen in [('draft-queue','draft-complete-screen'),('draft-resume-queue','draft-complete-screen'),('busy-queue','busy-complete-screen'),('different-sender-cwd-queue','before-abnormal-exit-screen'),('stopped-resume-queue','stopped-queue-after-manual-resume-screen')]:
    req=load(name+'-request'); cmd=load(name)
    assert cmd['exit_code']==0 and req['native_id']==native and not req['terminal_input_used']
    queue_id=re.search(r'Queued message ([0-9a-f-]+) for thread '+native,cmd['stdout']).group(1)
    ack=assistant_exact('ACK '+req['nonce'])
    assert re.sub(r'\s+','',ack['text']) in normalized_screen(screen)
    assert timestamp(ack['timestamp'])>=timestamp(cmd['at'])
    queue_results[name]={'queue_id':queue_id,'nonce':req['nonce'],'command_evidence':name+'.json','request_evidence':name+'-request.json','ack':ack,'screen_evidence':screen+'.json','sender_cwd':cmd['cwd'],'stderr':cmd['stderr']}

draft=load('draft-marker')['marker']
assert '› '+draft in load('draft-before-queue-decoded')['text']
assert '› '+draft in load('draft-complete-screen-decoded')['text']
assert not any(r['type']=='response_item' and r['payload'].get('role')=='user' and draft in text(r['payload']) for r in records)
assert draft not in load('draft-cleared-decoded')['text']

busy=command_with_nonce(load('busy-request')['nonce'])
queued_at=timestamp(load('busy-queue')['at'])*1000
assert busy['started_at_ms']<queued_at<busy['completed_at_ms']
busy_done=assistant_exact('BUSY_DONE '+load('busy-request')['nonce'])
assert timestamp(busy_done['timestamp'])<timestamp(queue_results['busy-queue']['ack']['timestamp'])
assert 'Working (' in load('busy-before-queue-decoded')['text']

cwd_calls={}
for name in ['cwd-a','cwd-b']:
    p=command_with_nonce(load(name+'-request')['nonce']); output=json.loads(p['item']['aggregated_output'])
    assert output['workspace']==target['workspace_id'] and output['surface']==target['surface_id']
    assert output['nonce']==load(name+'-request')['nonce']
    cwd_calls[name]={'thread_id':p['thread_id'],'turn_id':p['turn_id'],'item_id':p['item']['id'],'exit_code':p['item']['exit_code'],'output':output,'source':'native-final.json'}
assert cwd_calls['cwd-a']['output']['cwd']==load('codex-plan',PRIOR)['cwd']
assert cwd_calls['cwd-b']['output']['cwd']==str(OUT/'scratch/cwd-b')
assert load('different-sender-cwd-queue')['cwd']==cwd_calls['cwd-b']['output']['cwd']
assert 'Working directory · resume' in load('resume-cwd-b-screen-decoded')['text']

assert load('abnormal-exit-wrapper-result')['result']['exit_code']==-9
assert load('post-abnormal-exit-process')['exit_code']==1 and not load('post-abnormal-exit-process')['stdout']
original=load('codex-nonce',PRIOR)['nonce']
assert original not in load('crash-recovery-request')['prompt']
restored=assistant_exact('CRASH_RESTORED '+original)
assert restored['text'] in load('crash-recovered-screen-decoded')['text']
assert 'could not resume thread '+native in load('stopped-resume-queue')['stderr']
assert load('native-stopped-after-queue')['records']==load('native-different-sender-cwd')['records']
assert timestamp(queue_results['stopped-resume-queue']['ack']['timestamp'])>timestamp(load('resume-cwd-b-launch')['at'])

assert final['rows'][0][0]==native and final['rows'][0][1]==load('codex-plan',PRIOR)['cwd']
assert native in load('final-status-screen-decoded')['text'] and '› Ask Codex to do anything' in load('final-status-screen-decoded')['text']
assert load('target-process-final')['exit_code']==0 and 'ttys028' in load('target-process-final')['stdout']
assert load('resume-cwd-b-exit')['exit_code']==0
assert load('source-before-checks')['files']==load('source-after-checks')['files']
checks={n:load(n)['exit_code'] for n in ['go-test','go-build','make-check']}; assert all(v==0 for v in checks.values())

# Every terminal mutation in this evidence set must use the explicitly authorized target.
mutations=[]
for path in OUT.glob('*.json'):
    obj=json.loads(path.read_text()); argv=obj.get('argv',[])
    if argv and argv[0].endswith('/cmux') and any(x in argv for x in ['send','send-key']):
        for flag,key in [('--window','window_id'),('--workspace','workspace_id'),('--surface','surface_id')]: assert argv[argv.index(flag)+1]==target[key]
        mutations.append(path.name)

new_passes=['busy_delivery','draft_protection_with_nonempty_draft','different_sender_cwd_queue_delivery','same_thread_cwd_switch_comparison','sigkill_idle_context_recovery','native_queue_live_tui_background_delivery','native_queue_persistence_until_manual_resume']
old=load('runtime-matrix',PRIOR)
pending=['hook_background_delivery','agentflow_hook_queue_adapter','queue_autowake_without_live_tui','different_cwd_two_native_threads','crash_mid_turn_or_machine_failure_recovery','general_terminal_draft_guard']
findings={
    'busy_delivery':{'status':'PASS_FOR_CONTROLLED_SCENARIO','command':busy,'queue_at_ms':queued_at,'busy_done':busy_done,'queue':queue_results['busy-queue'],'evidence':['busy-before-queue.json','busy-after-queue.json','native-busy-complete.json','busy-complete-screen.json']},
    'nonempty_draft':{'status':'PASS_FOR_NATIVE_QUEUE_ONLY','marker':draft,'preserved_before_and_after':True,'submitted_to_native_model':False,'cleared_own_marker_with':'ctrl+u','evidence':['draft-before-queue.json','draft-complete-screen.json','native-draft-complete.json','draft-cleared.json'],'limitation':'No generic AgentFlow guard for terminal injection exists; arbitrary user drafts and terminal send paths not validated.'},
    'different_cwd':{'status':'PASS_FOR_SAME_THREAD_A_B_AND_SENDER_CWD','calls':cwd_calls,'queue':queue_results['different-sender-cwd-queue'],'evidence':['resume-cwd-b-screen.json','resume-cwd-select-current.json','resume-cwd-confirm.json','native-cwd-a.json','cwd-a-screen.json','native-cwd-b.json','cwd-b-screen.json'],'limitation':'One authorized native thread; two independent native-thread isolation remains UNVERIFIED.'},
    'abnormal_exit_recovery':{'status':'PASS_FOR_SIGKILL_WHILE_IDLE','signal':'SIGKILL','old_pid':59794,'wrapper_exit_code':-9,'restored':restored,'new_pid':load('resume-cwd-b-process')['pid'],'evidence':['pre-abnormal-exit-process.json','abnormal-exit-signal.json','abnormal-exit-wrapper-result.json','post-abnormal-exit-process.json','shell-after-abnormal-exit.json','resume-cwd-b-launch.json','native-crash-recovered.json','crash-recovered-screen.json'],'limitation':'Explicit forced idle-process termination; no real accidental crash, in-flight tool crash, host crash or automatic supervisor recovery tested.'},
    'native_queue':{'status':'PASS_FOR_LIVE_TUI_RECEPTION_AND_MANUAL_RESUME_PERSISTENCE','results':queue_results,'autowake_status':'UNVERIFIED','autowake_observation':'--resume returned 0 with could-not-resume warning; stopped native rollout unchanged until manual TUI resume, then persisted message ACKed.','hook_status':'UNVERIFIED','agentflow_adapter':'NOT_IMPLEMENTED','implementation_evidence':'source-before-checks.json'},
}
codex={'tool':'codex','status':'PASS_FOR_LISTED_SCENARIOS','target':target,'native_id_current':native,'runtime_passes':load('codex-verification',PRIOR)['runtime_passes']+new_passes,'new_runtime_passes':new_passes,'unverified':pending,'findings':findings,'final_pid':load('resume-final-process')['pid'],'final_cwd':final['rows'][0][1],'final_state':'LIVE_IDLE_EMPTY_PROMPT','credentials_copied':False,'login_attempted':False}
save('codex-verification',codex)
save('runtime-matrix',{'at':at,'collaboration_session_id':load('execution-policy')['collaboration_session_id'],'task_id':'s1-codex-edge-cases','s1_status':'PARTIAL','runtime_acceptance':'PARTIAL','runtime_passes':old['runtime_passes']+['codex.'+p for p in new_passes],'new_runtime_passes':['codex.'+p for p in new_passes],'unverified':pending,'tools':[codex,{'tool':'opencode','status':'VERIFIED_TRANSPORT_ONLY / WAITING_DEPENDENCY','native_id':'ses_e65a4cf24ffe3wZAiM2U7og4Px','source':'../S1-manual-login-retry-a128f1ae/opencode-verification.json','runtime_passes':[],'new_calls':0},{'tool':'claude','status':'EXCLUDED','new_calls':0}],'checks':checks,'go_source_changed':False,'s2':False,'handoff_is_native_ack':False})
save('dependencies',{'at':at,'status':'WAITING_DEPENDENCY_FOR_COMPLETE_S1','codex':{'target':target,'current_native_id':native,'current_pid':load('resume-final-process')['pid'],'current_cwd':final['rows'][0][1],'state':'LIVE_IDLE_EMPTY_PROMPT','pending':pending,'next':'Continue S1 only. Preserve this existing login and native identity; implement/authorize no broader architecture in this evidence-only round. Native queue receiver is the live --no-daemon TUI; auto-wake needs a separately authorized existing server/daemon path and native evidence.'},'opencode':{'native_id':'ses_e65a4cf24ffe3wZAiM2U7og4Px','status':'VERIFIED_TRANSPORT_ONLY / WAITING_DEPENDENCY','blocker':'FREE_TIER_LIMIT_FROM_PRIOR_EVIDENCE','new_requests':0,'login_allowed':False,'next':'Preserve historical transport evidence; do not call models, login or purchase capacity in this round.'},'claude':'EXCLUDED','s2':'DO_NOT_ENTER','history_cli':'ochist unavailable; supplied evidence used','risk_cli':'jev-judge unavailable; no judgment fabricated'})
save('evidence-validation',{'at':at,'status':'PASS','assertions':['five queue message IDs parsed from real stdout and exact native assistant ACKs matched same-surface screens','draft present before/after two queue ACKs, absent from native user messages, cleared only own marker','native CommandExecution interval contains busy queue timestamp; original BUSY_DONE precedes queued ACK','actual native tool output proves cwd A/B and same surface/workspace','different sender cwd queue ACK proves explicit thread routing in controlled case','SIGKILL preflight PID/TTY/argv, actual -9 exit and absence, native history nonce recalled after resume without full nonce in prompt','stopped --resume exit 0 warning distinguished from unchanged rollout and later manual-resume ACK','same original thread and cwd restored; final PID/TTY and empty native prompt verified','every cmux terminal mutation explicitly targets authorized window/workspace/surface','Go source inventory equal across checks; test/build/check all exit 0'],'new_runtime_passes':new_passes,'terminal_mutation_files':mutations,'source_inventory_timing':'Inventory begins during experiments, not a pre-task snapshot. No Go code edits performed.','limitations':pending})
print(json.dumps({'status':'PASS','new_runtime_passes':new_passes,'checks':checks,'final_pid':codex['final_pid'],'s1_status':'PARTIAL'},ensure_ascii=False))
