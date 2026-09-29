#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-3.0-only
import copy,hashlib,json,os,sqlite3,unittest,urllib.request,urllib.error
from pathlib import Path
import integration as base
class Investigations(base.Workflows):
 def call(self,path,body=None,method='POST'):
  req=urllib.request.Request(self.url+'/api/v1'+path,json.dumps(body).encode() if body is not None else None,{'Content-Type':'application/json'},method=method)
  return json.load(urllib.request.urlopen(req))
 def captures(self):
  out=[]
  for j in range(2):
   ss=self.record();events=[]
   for i in range(4):
    v=self.event(i,boot_id='boot'+str(j),timestamp_ns=str(1790612345678901234+j*100+i),gap_before=i==2);v['values']['cycles']=str(9007199254740993+j*10+i);events.append(v)
   self.ingest(*events);self.client.stop_recording(ss['id']);out.append((ss,list(self.client.events(ss['id']))))
  return out
 def request(self,captures,mode='relative'):
  return {'alignment':mode,'time_basis':'source','traces':[{'session_id':ss['id'],'alias':chr(65+j),'source_id':self.source['id'],'schema_id':self.source['events'][0]['id'],'schema_version':1,'field_id':self.source['events'][0]['fields'][1]['id'],'anchor_ordinal':es[1]['ordinal'],'range':{}} for j,(ss,es) in enumerate(captures)]}
 def document(self,ref=None):
  return {'format':'tracework.investigation.v1','title':'Cure comparison','question':'What changed?','notes':[] if not ref else [{'id':'evidence_a','kind':'evidence','text':'Original observation','severity':'info','confidence':'high','ref':ref}]}
 def test_v5_01_alignment_precision_and_gaps(self):
  cs=self.captures();req=self.request(cs);out=self.call('/compare',req)
  for t in out['traces']:
   self.assertEqual([p['aligned_ns'] for p in t['points']],['0','1','2','3']);self.assertTrue(t['points'][2]['gap_before']);self.assertEqual(t['points'][0]['value'],str(9007199254740993+(10 if t['request']['alias']=='B' else 0)))
  req['alignment']='event';out=self.call('/compare',req);self.assertEqual([p['aligned_ns'] for p in out['traces'][0]['points']],['-1','0','1','2'])
  req['alignment']='absolute';out=self.call('/compare',req);self.assertEqual(out['traces'][0]['points'][0]['aligned_ns'],'1790612345678901234')
  for ss,es in cs:self.assertEqual(list(self.client.events(ss['id'])),es)
 def test_v5_02_exact_measurements_and_unit_refusal(self):
  cs=self.captures();a={'session_id':cs[0][0]['id'],'ordinal':cs[0][1][0]['ordinal']};b={'session_id':cs[1][0]['id'],'ordinal':cs[1][1][3]['ordinal']};f=self.source['events'][0]['fields'][1]['id'];req={'a':a,'b':b,'field_a':f,'field_b':f,'time_basis':'source'};out=self.call('/measure',req);self.assertEqual(out['delta_value_exact'],'13');self.assertEqual(out['delta_time_ns'],'103');req['field_b']=self.source['events'][0]['fields'][0]['id']
  with self.assertRaises(urllib.error.HTTPError):self.call('/measure',req)
 def test_v5_03_saved_evidence_baseline_report_and_conflict(self):
  cs=self.captures();ref={'session_id':cs[0][0]['id'],'ordinal':cs[0][1][0]['ordinal']};doc=self.document(ref);doc['comparison']=self.request(cs);path='/projects/'+self.project['id']+'/investigation';out=self.call(path,{'expected_revision':0,'document':doc},'PUT');self.assertEqual(out['revision'],1);note=out['document']['notes'][0];self.assertEqual(note['evidence'],cs[0][1][0]);self.assertEqual(len(note['event_sha256']),64)
  baseline=self.call('/projects/'+self.project['id']+'/baselines',{'name':'Reviewed','expected_revision':1});doc=out['document'];doc['question']='A different question';self.call(path,{'expected_revision':1,'document':doc},'PUT');self.assertEqual(self.get('/projects/'+self.project['id']+'/baselines')[0]['snapshot']['document']['question'],'What changed?')
  with self.assertRaises(urllib.error.HTTPError) as exc:self.call(path,{'expected_revision':1,'document':doc},'PUT')
  self.assertEqual(exc.exception.code,409);report=urllib.request.urlopen(self.url+'/api/v1'+path+'/report').read();self.assertIn(b'9007199254740993',report);self.stop(crash=True);self.start();self.assertEqual(self.get(path)['revision'],2);self.assertEqual(len(self.get('/projects/'+self.project['id']+'/baselines')),1)
 def test_v5_04_forged_evidence_invalid_aliases_and_samples(self):
  cs=self.captures();doc=self.document({'session_id':cs[0][0]['id'],'ordinal':cs[0][1][0]['ordinal']});doc['notes'][0]['event_sha256']='f'*64;path='/projects/'+self.project['id']+'/investigation'
  with self.assertRaises(urllib.error.HTTPError):self.call(path,{'expected_revision':0,'document':doc},'PUT')
  self.assertEqual(self.get(path)['revision'],0);req=self.request(cs);req['traces'][1]['alias']='A'
  with self.assertRaises(urllib.error.HTTPError):self.call('/compare',req)
  self.call(path,{'expected_revision':0,'document':self.document()},'PUT');sample=self.call('/examples/motor',{});pid=sample['project']['id'];self.call('/projects/'+pid+'/investigation',{'expected_revision':0,'document':self.document()},'PUT');self.call('/projects/'+pid+'/baselines',{'name':'Sample','expected_revision':1});self.call('/samples',method='DELETE');self.assertEqual(self.get(path)['revision'],1)
  with sqlite3.connect(self.data/'metadata.sqlite') as db:self.assertEqual(db.execute('SELECT count(*) FROM baselines').fetchone()[0],0)
 @unittest.skipUnless(os.getenv('TRACEWORK_V04_BINARY'),'Set original v0.4 executable for migration fixture')
 def test_v5_05_v04_migration(self):
  self.stop();new=base.BINARY;base.BINARY=Path(os.environ['TRACEWORK_V04_BINARY']);self.data=Path(self.tmp.name)/'old'
  try:
   self.start();self.project=self.client.create_project('Original v0.4');self.source=self.client.define_source(self.project['id'],'legacy',[{'name':'n','type':'uint64','unit':'n'}]);ss=self.record();self.client.source(self.project['id'],self.source['id'],self.source['events'][0]['id'],'producer-clock').emit({'n':'9007199254740993'},timestamp_ns=1790612345678901234);self.client.stop_recording(ss['id']);before=list(self.client.events(ss['id']));self.stop();hashes={p.name:hashlib.sha256(p.read_bytes()).hexdigest() for p in (self.data/'chunks').glob('*.jsonl')};base.BINARY=new;self.start();self.assertEqual(list(self.client.events(ss['id'])),before);self.assertEqual({p.name:hashlib.sha256(p.read_bytes()).hexdigest() for p in (self.data/'chunks').glob('*.jsonl')},hashes);self.assertEqual(self.get('/projects/'+self.project['id']+'/investigation')['revision'],0)
  finally:base.BINARY=new
if __name__=='__main__':
 result=unittest.TextTestRunner(verbosity=2).run(unittest.TestSuite(Investigations(n) for n in unittest.defaultTestLoader.getTestCaseNames(Investigations) if n.startswith('test_v5_')));raise SystemExit(not result.wasSuccessful())
