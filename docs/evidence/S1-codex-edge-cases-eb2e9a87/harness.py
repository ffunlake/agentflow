import base64, datetime, hashlib, json, os, pathlib, re, shlex, sqlite3, subprocess, sys, time, uuid

ROOT = pathlib.Path('/Users/funlake/Projects/agentflow')
OUT = pathlib.Path(__file__).resolve().parent
PRIOR = ROOT / 'docs/evidence/S1-manual-login-retry-a128f1ae'
PLAN = json.loads((PRIOR/'codex-plan.json').read_text())
TARGET = {'window_id':'2B9E10E2-0C0A-47DC-AED7-206B833A4462','workspace_id':'28A52619-270D-4FC5-A692-C730028054E2','surface_id':'4E483942-928F-455C-B03D-75DAEDE9F38F'}
NATIVE = '01a19ad4-2958-75f3-82b6-f488745caec4'
CMUX = '/Applications/cmux.app/Contents/Resources/bin/cmux'
HOME = pathlib.Path(PLAN['environment_overrides']['CODEX_HOME'])
def now(): return datetime.datetime.now(datetime.timezone.utc).isoformat()
def save(name,obj):
    with (OUT/(name+'.json')).open('x') as f: json.dump(obj,f,ensure_ascii=False,indent=2); f.write('\n')
def load(name): return json.loads((OUT/(name+'.json')).read_text())
def run(name,argv,env=None,cwd=ROOT,timeout=25):
    r={'at':now(),'argv':argv,'cwd':str(cwd),'target':TARGET,'native_id':NATIVE}; start=time.monotonic()
    try:
        p=subprocess.run(argv,cwd=cwd,env=env,stdin=subprocess.DEVNULL,capture_output=True,text=True,timeout=timeout)
        r.update(exit_code=p.returncode,stdout=p.stdout,stderr=p.stderr)
    except subprocess.TimeoutExpired as e:
        r.update(exit_code=None,timed_out=True,stdout=str(e.stdout),stderr=str(e.stderr))
    r['duration_ms']=round(1000*(time.monotonic()-start)); save(name,r)
    print(json.dumps({'evidence':name,'exit_code':r['exit_code'],'stdout':r.get('stdout','')[:1800],'stderr':r.get('stderr','')},ensure_ascii=False)); return r
def cmux(name,*args): return run(name,[CMUX,'--json','--id-format','uuids',*args])
def target(): return ['--window',TARGET['window_id'],'--workspace',TARGET['workspace_id'],'--surface',TARGET['surface_id']]
def screen(name):
    r=cmux(name,'read-screen',*target(),'--scrollback','--lines','100')
    if r['exit_code']==0:
        o=json.loads(r['stdout']); text=o.get('text')
        if text is None: text=base64.b64decode(o['base64']).decode(errors='replace')
        save(name+'-decoded',{'source':name+'.json','target':TARGET,'native_id':NATIVE,'text':text}); print(text)
def env():
    allowed=['HOME','USER','LOGNAME','PATH','TERM','COLORTERM','LANG','LC_ALL','SHELL','CMUX_SOCKET_PATH','CMUX_SOCKET_CAPABILITY']
    e={k:os.environ[k] for k in allowed if k in os.environ}
    e.update(PLAN['environment_overrides'])
    e.update(CMUX_WINDOW_ID=TARGET['window_id'],CMUX_WORKSPACE_ID=TARGET['workspace_id'],CMUX_SURFACE_ID=TARGET['surface_id'],CMUX_PANEL_ID=TARGET['surface_id'])
    return e
def transcript(name):
    dbpath=HOME/'state_5.sqlite'
    with sqlite3.connect(dbpath.as_uri()+'?mode=ro',uri=True) as db:
        rows=db.execute('select id,cwd,model_provider,rollout_path from threads where id=?',(NATIVE,)).fetchall()
    records=[]
    for row in rows:
        path=pathlib.Path(row[3]); assert path.is_relative_to(HOME/'sessions')
        for line in path.read_text().splitlines():
            rec=json.loads(line); p=rec.get('payload',{})
            if rec['type']=='session_meta': rec={**rec,'payload':{k:p.get(k) for k in ['id','cwd','originator','cli_version','source','model_provider']}}
            elif rec['type']=='response_item' and p.get('role') in ['system','developer']: continue
            records.append(rec)
    save(name,{'at':now(),'native_id':NATIVE,'target':TARGET,'database':str(dbpath),'read_only':True,'rows':rows,'records':records,'credentials_read':False})
    print(json.dumps({'evidence':name,'rows':rows,'records':len(records),'last_events':records[-2:]},ensure_ascii=False))
def paste(name,text,submit=False):
    r=cmux(name+'-text','send','--paste',*target(),'--',text); assert r['exit_code']==0
    if submit: assert cmux(name+'-enter','send-key',*target(),'enter')['exit_code']==0

stage=sys.argv[1]
if stage=='initial':
    save('execution-policy',{'at':now(),'collaboration_session_id':'eb2e9a87-d909-47c1-8015-ae54615ace35','task_id':'s1-codex-edge-cases','target':TARGET,'native_id':NATIVE,'new_login':False,'copy_credentials':False,'global_config_edits':False,'other_sessions_touched':False,'opencode':'PRESERVE_PRIOR_TRANSPORT_ONLY_NO_CALLS','claude':'EXCLUDED','s2':False,'ochist':'unavailable on PATH; supplied evidence used','jev_judge':'unavailable on PATH; no judgment fabricated','authorization':'User explicitly authorizes controlled busy/draft/cwd/abnormal-exit recovery and native queue experiments on this target only.'})
    cmux('target-tree-initial','tree','--window',TARGET['window_id'])
    screen('target-screen-initial')
    run('target-process-initial',['/bin/ps','-p','59794','-o','pid=,ppid=,tty=,command='])
    transcript('native-initial')
    run('codex-queue-help',['/opt/homebrew/bin/codex','queue','--help'],env=env())
elif stage=='screen': screen(sys.argv[2])
elif stage=='transcript': transcript(sys.argv[2])
elif stage=='queue-state':
    with sqlite3.connect((HOME/'state_5.sqlite').as_uri()+'?mode=ro',uri=True) as db:
        schemas=db.execute("select name,sql from sqlite_master where type='table' and (name like '%queue%' or name like '%message%')").fetchall()
        tables={}
        for name,sql in schemas:
            columns=[r[1] for r in db.execute('pragma table_info("'+name+'")')]
            if 'thread_id' in columns:
                tables[name]={'columns':columns,'rows':db.execute('select * from "'+name+'" where thread_id=?',(NATIVE,)).fetchall()}
    save(sys.argv[2],{'at':now(),'target':TARGET,'native_id':NATIVE,'read_only':True,'schemas':schemas,'tables':tables})
    print(json.dumps({'evidence':sys.argv[2],'schemas':schemas,'tables':tables},ensure_ascii=False))
elif stage=='key': cmux(sys.argv[2],'send-key',*target(),sys.argv[3])
elif stage=='text': paste(sys.argv[2],sys.argv[3],len(sys.argv)>4 and sys.argv[4]=='submit')
elif stage=='queue':
    name=sys.argv[2]; nonce='S1-'+name+'-'+str(uuid.uuid4())
    message='Controlled S1 background queue probe; do not use tools or modify files. Reply exactly: ACK '+nonce
    save(name+'-request',{'at':now(),'target':TARGET,'native_id':NATIVE,'nonce':nonce,'message':message,'terminal_input_used':False,'resume_requested':'--resume' in sys.argv})
    argv=['/opt/homebrew/bin/codex','-c','cli_auth_credentials_store="file"','-c','check_for_update_on_startup=false','queue','--thread',NATIVE,'--message',message]
    if '--resume' in sys.argv: argv+=['--resume']
    cwd=pathlib.Path(sys.argv[3]) if len(sys.argv)>3 and sys.argv[3]!='--resume' else pathlib.Path(PLAN['cwd'])
    run(name,argv,env=env(),cwd=cwd)
elif stage=='draft':
    marker='S1-UNSUBMITTED-DRAFT-'+str(uuid.uuid4())
    save('draft-marker',{'at':now(),'marker':marker,'target':TARGET,'native_id':NATIVE,'submitted':False})
    paste('draft-place',marker)
elif stage=='busy':
    nonce='S1-busy-'+str(uuid.uuid4())
    source="import time; print('BUSY_BEGIN "+nonce+"',flush=True); time.sleep(40); print('BUSY_END "+nonce+"',flush=True)"
    command=shlex.join(['/usr/bin/python3','-c',source])
    prompt='Controlled S1 busy experiment in this isolated session. Use your terminal tool to run this exact harmless 40-second command. Do not read or edit files. After it completes reply exactly BUSY_DONE '+nonce+'. Command: '+command
    save('busy-request',{'at':now(),'nonce':nonce,'command':command,'prompt':prompt,'native_id':NATIVE,'target':TARGET})
    paste('busy-submit',prompt,True)
elif stage=='cwd-prompt':
    name=sys.argv[2]; nonce='S1-'+name+'-'+str(uuid.uuid4())
    source="import os,json; print(json.dumps({'nonce':"+repr(nonce)+",'cwd':os.getcwd(),'workspace':os.environ.get('CMUX_WORKSPACE_ID'),'surface':os.environ.get('CMUX_SURFACE_ID')}))"
    command=shlex.join(['/usr/bin/python3','-c',source])
    prompt='Controlled S1 cwd comparison. Run this exact command using your terminal tool in its current default working directory, without setting/changing cwd. Do not read or modify files. Then report its result. Command: '+command
    save(name+'-request',{'at':now(),'nonce':nonce,'command':command,'prompt':prompt,'target':TARGET,'native_id':NATIVE})
    paste(name,prompt,True)
elif stage=='recovery-prompt':
    prompt='Controlled S1 recovery after forced process termination. Do not use tools or modify files. Retrieve from THIS conversation history the original S1-codex-a128 nonce you acknowledged before any edge experiments. Reply exactly CRASH_RESTORED followed by that full original nonce. Do not invent it.'
    original=json.loads((PRIOR/'codex-nonce.json').read_text())['nonce']
    assert original not in prompt
    save('crash-recovery-request',{'at':now(),'native_id':NATIVE,'target':TARGET,'prompt':prompt,'expected_original_nonce_omitted':True})
    paste('crash-recovery',prompt,True)
elif stage=='capture-wrapper-exit':
    p=PRIOR/'codex-resume-exit.json'
    save('abnormal-exit-wrapper-result',{'at':now(),'target':TARGET,'native_id':NATIVE,'source':str(p),'result':json.loads(p.read_text()),'note':'Existing prior-round wrapper automatically created this new exit file when its child was killed; previous evidence files were not overwritten.'})
elif stage=='kill':
    pid=int(sys.argv[2]); r=run('pre-abnormal-exit-process',['/bin/ps','-p',str(pid),'-o','pid=,ppid=,tty=,command='])
    assert r['exit_code']==0 and 'ttys028' in r['stdout'] and 'resume '+NATIVE in r['stdout'] and '/opt/homebrew/bin/codex' in r['stdout']
    run('abnormal-exit-signal',['/bin/kill','-KILL',str(pid)])
elif stage=='resume-child':
    name=sys.argv[2]; cwd=pathlib.Path(sys.argv[3]); assert cwd.is_relative_to(ROOT/'docs/evidence')
    assert os.environ.get('CMUX_SURFACE_ID')==TARGET['surface_id'] and os.environ.get('CMUX_WORKSPACE_ID')==TARGET['workspace_id']
    argv=PLAN['argv']+['resume',NATIVE]
    save(name+'-launch',{'at':now(),'argv':argv,'cwd':str(cwd),'target':TARGET,'native_id':NATIVE,'environment_overrides':PLAN['environment_overrides'],'wrapper_pid':os.getpid(),'credentials_copied':False})
    p=subprocess.Popen(argv,env=env(),cwd=cwd); save(name+'-process',{'at':now(),'pid':p.pid,'argv':argv,'cwd':str(cwd),'target':TARGET,'native_id':NATIVE})
    code=p.wait(); save(name+'-exit',{'at':now(),'exit_code':code,'target':TARGET,'native_id':NATIVE})
    print('Native exited',code,'; owned shell retained.',flush=True); os.execve('/bin/zsh',['/bin/zsh','-f'],env())
elif stage=='start-resume':
    name=sys.argv[2]; cwd=sys.argv[3]
    paste(name+'-start',shlex.join([sys.executable,str(pathlib.Path(__file__).resolve()),'resume-child',name,cwd]),True)
elif stage=='regression':
    name=sys.argv[2]; e=os.environ.copy()
    overrides={'GOTOOLCHAIN':'local','CGO_ENABLED':'0','GOFLAGS':'-mod=readonly','GOCACHE':str(ROOT/'.cache/go-build'),'GOMODCACHE':str(ROOT/'.cache/go-mod'),'GOPATH':str(ROOT/'.cache/gopath'),'GOTMPDIR':str(ROOT/'.cache/tmp'),'TMPDIR':str(ROOT/'.cache/tmp'),'DISABLE_AUTOUPDATER':'1'}
    e.update(overrides); save(name+'-environment',overrides)
    argv={'go-test':['go','test','./...'],'go-build':['go','build','./...'],'make-check':['make','check']}[name]
    r=run(name,argv,env=e,timeout=55); raise SystemExit(0 if r['exit_code']==0 else 1)
elif stage=='source-inventory':
    files=sorted([p for d in ['cmd','internal','probes','tests'] for p in (ROOT/d).rglob('*.go')]+[ROOT/'Makefile',ROOT/'go.mod',ROOT/'go.sum'])
    save(sys.argv[2],{'at':now(),'files':{str(p.relative_to(ROOT)):hashlib.sha256(p.read_bytes()).hexdigest() for p in files},'implementation_evidence':{'file':'internal/probe/probe.go','selected_lines':[{'line':i,'text':line} for i,line in enumerate((ROOT/'internal/probe/probe.go').read_text().splitlines(),1) if 139<=i<=159]},'claim':'Inventory taken during experiment, before final checks; no Go source edits performed by this task.'})
elif stage=='final-target':
    cmux('target-tree-final','tree','--window',TARGET['window_id'])
    screen('final-status-screen'); transcript('native-final')
    run('target-process-final',['/bin/ps','-p',str(load('resume-final-process')['pid']),'-o','pid=,ppid=,tty=,command='])
elif stage=='document-validation':
    docs=[OUT/'README.md',OUT.parent/'S1-result.md',OUT.parent/'S1-capability-matrix.md']; checked=[]
    for doc in docs:
        for link in re.findall(r'\]\(([^)]+)\)',doc.read_text()):
            if '://' in link or link.startswith('#'): continue
            path=(doc.parent/link.split('#')[0]).resolve(); assert path.exists(),(doc,link)
            checked.append({'document':str(doc.relative_to(ROOT)),'link':link})
    assert len(load('runtime-matrix')['runtime_passes'])==16
    assert load('runtime-matrix')['s1_status']=='PARTIAL'
    assert load('dependencies')['codex']['current_native_id']==NATIVE
    manifest={str(p.relative_to(OUT)):hashlib.sha256(p.read_bytes()).hexdigest() for p in sorted(OUT.rglob('*')) if p.is_file()}
    save('document-validation',{'at':now(),'status':'PASS','checked_links':checked,'runtime_passes':16,'new_runtime_passes':7,'s1_status':'PARTIAL','snapshot_sha256':manifest,'manifest_excludes':'This file and subsequent handoff evidence; native runtime files outside this directory are not read.'})
    print(json.dumps({'status':'PASS','links':len(checked),'evidence_files':len(manifest),'runtime_passes':16}))
elif stage=='record': run(sys.argv[2],sys.argv[3:])
else: raise SystemExit('Unknown stage')
