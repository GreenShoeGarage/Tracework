#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-3.0-only
"""Qualify each historical executable, never substitute a newer binary."""
import hashlib,json,os,sqlite3,subprocess,time,urllib.request
from pathlib import Path
import integration as base
from tracework import Client
from workspaces import workspace
TOKEN='migration-fixture-master-token-123456'
current=base.BINARY
results=[]
for minor in range(1,10):
 old=Path(os.getenv('TRACEWORK_V%02d_BINARY'%minor,'/tmp/tracework-v%02d-baseline'%minor))
 if not old.is_file():raise RuntimeError('Missing actual historical executable: '+str(old))
 w=base.Workflows();w.setUp()
 try:
  w.stop();w.data=Path(w.tmp.name)/'historical';base.BINARY=old;w.start(token=TOKEN);w.client=Client(w.url,token=TOKEN)
  def req(route,body=None,method=None):
   r=urllib.request.Request(w.url+'/api/v1'+route,data=None if body is None else json.dumps(body).encode(),headers={'Content-Type':'application/json','Authorization':'Bearer '+TOKEN},method=method or ('GET' if body is None else 'POST'))
   return json.load(urllib.request.urlopen(r))
  assert req('/status')['version']=='0.%d.0'%minor
  p=w.client.create_project('Historical v0.%d'%minor);pid=p['id'];src=w.client.define_source(pid,'Exact counter',[{'name':'n','type':'uint64','unit':'cycles'}]);sc=src['events'][0]
  ss=w.client.start_recording(pid,'Preserve evidence');w.client.source(pid,src['id'],sc['id'],'producer-clock').emit({'n':'18446744073709551615'},timestamp_ns=1790612345678901234);w.client.stop_recording(ss['id'])
  paths=['/projects/'+pid,'/sessions/'+ss['id']+'/events'];features=['exact evidence']
  if minor>=3:req('/sessions/'+ss['id']+'/protect',{'protected':True});features.append('protection')
  prefix='/projects/'+pid
  if minor>=4:
   ws=req(prefix+'/workspace');req(prefix+'/workspace',{'expected_revision':ws['revision'],'workspace':workspace()},'PUT');paths.append(prefix+'/workspace');features.append('workspace')
  if minor>=5:
   doc={'format':'tracework.investigation.v1','title':'Retained investigation','question':'Does evidence survive?','notes':[{'id':'proof','kind':'evidence','text':'Exact original','severity':'info','confidence':'high','ref':{'session_id':ss['id'],'ordinal':1}}]}
   inv=req(prefix+'/investigation',{'expected_revision':0,'document':doc},'PUT');req(prefix+'/baselines',{'name':'Frozen','expected_revision':inv['revision']});paths += [prefix+'/investigation',prefix+'/baselines'];features.append('investigation and frozen baseline')
  if minor>=6:
   reg=req(prefix+'/providers',{'name':'Offline software target','source_id':src['id'],'actions':[{'id':'set','name':'Set','description':'Fixture','effect':'write','timeout_ms':1000,'parameters':[]}]});features.append('provider catalog')
  if minor>=7:
   rule={'id':'exact','name':'Exact uint64','trace':{'source_id':src['id'],'schema_id':sc['id'],'schema_version':1,'field_id':sc['fields'][0]['id'],'range':{'from':1,'to':1}},'time_basis':'source','kind':'equals','expected':'18446744073709551615','min_samples':1,'min_span_ns':'0','max_gap_ns':'100','hold_ns':'1','gap_policy':'insufficient','null_policy':'insufficient'}
   req(prefix+'/checks',{'expected_revision':0,'document':{'format':'tracework.checks.v1','rules':[rule]}},'PUT');run=req(prefix+'/check-runs',{'session_id':ss['id']});assert run['status']=='pass';paths += [prefix+'/checks','/check-runs/'+run['id']];features.append('check rules and cited result')
  if minor>=8:req('/admin/accounts',{'name':'historical_admin','password':'historical-fixture-password','role':'admin'});paths.append('/admin/accounts');features.append('account password hash and role')
  if minor>=9:
   out=subprocess.check_output([str(old),'dbc-schema','--dbc',str(base.ROOT/'examples/protocols/bench.dbc'),'--can-id','291']);req(prefix+'/sources',json.loads(out));features.append('pinned decoder definition')
  before={path:req(path) for path in paths};w.stop();chunks={f.name:hashlib.sha256(f.read_bytes()).hexdigest() for f in (w.data/'chunks').glob('*.jsonl')}
  with sqlite3.connect(w.data/'metadata.sqlite') as db:
   retained={table:db.execute('SELECT * FROM '+table).fetchall() for table in ['providers','accounts'] if db.execute("SELECT name FROM sqlite_master WHERE type='table' AND name=?",(table,)).fetchone()}
  base.BINARY=current;w.start(token=TOKEN)
  for path,body in before.items():
   after=req(path)
   assert (after['events']==body['events'] if path.endswith('/events') else after==body),(minor,path)
  assert chunks=={f.name:hashlib.sha256(f.read_bytes()).hexdigest() for f in (w.data/'chunks').glob('*.jsonl')}
  if minor>=3:assert req('/sessions/'+ss['id'])['protected']
  with sqlite3.connect(w.data/'metadata.sqlite') as db:
   assert db.execute('SELECT version FROM meta').fetchone()[0]==9
   for table,rows in retained.items():assert db.execute('SELECT * FROM '+table).fetchall()==rows,(minor,table)
  results.append({'from':'0.%d.0'%minor,'to':'1.0.0','preserved':features,'event_and_chunk_identity':True});print('PASS v0.%d migration'%minor,flush=True)
 finally:base.BINARY=current;w.tearDown()
(base.ROOT/'docs/MIGRATION-RESULTS.json').write_text(json.dumps(results,indent=2)+'\n')
