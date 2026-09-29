#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-3.0-only
import copy,json,os,subprocess,unittest,urllib.error,xml.etree.ElementTree as ET
from pathlib import Path
import actions as actionbase
import integration as base
from tracework_checks import Checks,CheckFailure,InsufficientEvidence
class EvidenceChecks(actionbase.Actions):
 def fixture(self,gap=False,null=False):
  ss=self.record();events=[]
  for i in range(5):
   ev=self.event(i,timestamp_ns=str(1790612345678901234+i*100),gap_before=gap and i==2);ev['values']['cycles']=None if null and i==2 else str(9007199254740993+i);ev['values']['state']='ready' if i<2 else 'loaded';events.append(ev)
  self.ingest(*events);self.client.stop_recording(ss['id']);self.ss=ss;self.original=list(self.client.events(ss['id']));self.engine=Checks(self.client);self.path='/projects/'+self.project['id']+'/checks'
 def rule(self,kind='all_between',field=1):
  sc=self.source['events'][0];return {'id':'rule_a','name':'Exact observation contract','trace':{'session_id':'','alias':'','source_id':self.source['id'],'schema_id':sc['id'],'schema_version':1,'field_id':sc['fields'][field]['id'],'anchor_ordinal':0,'range':{'from':self.original[0]['ordinal'],'to':self.original[-1]['ordinal']}},'time_basis':'source','kind':kind,'min':'9007199254740993','max':'9007199254740997','expected':None,'from_value':None,'min_samples':5,'min_span_ns':'400','max_gap_ns':'100','hold_ns':'200','gap_policy':'insufficient','null_policy':'insufficient'}
 def run_rule(self,rule):return self.engine.run(self.project['id'],self.ss['id'],{'format':'tracework.checks.v1','rules':[rule]})
 def test_v7_01_exact_predicates_citations_reports(self):
  self.fixture();r=self.rule();out=self.run_rule(r);self.assertEqual(out['status'],'pass');self.assertEqual(out['results'][0]['span_ns'],'400');self.assertEqual(out['results'][0]['evidence'][0]['event']['values']['cycles'],'9007199254740993');self.assertEqual(len(out['results'][0]['evidence'][0]['event_sha256']),64)
  r['max']='9007199254740996';out=self.run_rule(r);self.assertEqual(out['status'],'fail');self.assertEqual(ET.fromstring(self.engine.report(out['id'],'junit')).attrib['failures'],'1');self.assertIn(b'9007199254740993',self.engine.report(out['id'],'html'))
  with self.assertRaises(CheckFailure):self.engine.assert_pass(out)
  r['kind']='any_between';r['min']='9007199254740994';r['max']=r['min'];self.assertEqual(self.run_rule(r)['status'],'pass');r['kind']='settles';r['min']='9007199254740995';r['max']='9007199254740997';self.assertEqual(self.run_rule(r)['status'],'pass');r['hold_ns']='300';self.assertEqual(self.run_rule(r)['status'],'fail')
  r=self.rule('transition',2);r['from_value']='ready';r['expected']='loaded';self.assertEqual(self.run_rule(r)['status'],'pass');r=self.rule('equals',3);r['expected']=True;self.assertEqual(self.run_rule(r)['status'],'pass');self.assertEqual(list(self.client.events(self.ss['id'])),self.original)
 def test_v7_02_coverage_is_not_a_pass(self):
  self.fixture(gap=True,null=True);r=self.rule();out=self.run_rule(r);self.assertEqual(out['status'],'insufficient');self.assertEqual(ET.fromstring(self.engine.report(out['id'],'junit')).attrib['errors'],'1')
  with self.assertRaises(InsufficientEvidence):self.engine.assert_pass(out)
  r['min_samples']=4;r['gap_policy']='allow';r['null_policy']='allow';self.assertEqual(self.run_rule(r)['status'],'pass');r['gap_policy']='fail';self.assertEqual(self.run_rule(r)['status'],'fail');r['gap_policy']='allow';r['trace']['range']['to']+=1;self.assertEqual(self.run_rule(r)['status'],'insufficient');r['trace']['range']['from']+=50;r['trace']['range']['to']+=50;self.assertEqual(self.run_rule(r)['status'],'insufficient')
 def test_v7_03_revisions_validation_and_retained_history(self):
  self.fixture();doc={'format':'tracework.checks.v1','rules':[self.rule()]};saved=self.call(self.path,{'expected_revision':0,'document':doc},'PUT');self.assertEqual(saved['revision'],1)
  with self.assertRaises(urllib.error.HTTPError):self.call(self.path,{'expected_revision':0,'document':doc},'PUT')
  for key,val in [('min_samples',0),('max_gap_ns','0'),('gap_policy','skip'),('kind','execute_shell')]:
   bad=copy.deepcopy(doc);bad['rules'][0][key]=val
   with self.assertRaises(urllib.error.HTTPError):self.engine.run(self.project['id'],self.ss['id'],bad)
  run=self.engine.run(self.project['id'],self.ss['id']);self.assertEqual(run['rules_revision'],1);self.stop(crash=True);self.start();self.assertEqual(self.get('/check-runs/'+run['id']),run);self.assertEqual(self.get(self.path)['revision'],1)
 def test_v7_04_python_go_cli_and_pytest(self):
  self.fixture();doc={'format':'tracework.checks.v1','rules':[self.rule()]};self.call(self.path,{'expected_revision':0,'document':doc},'PUT');env=dict(os.environ,TRACEWORK_URL=self.url,PYTHONPATH=str(base.ROOT/'sdk/python')+os.pathsep+os.getenv('TRACEWORK_PYTEST_PATH',''));folder=Path(self.tmp.name);rules=folder/'checks.json';rules.write_text(json.dumps(doc))
  commands=[['python3','-m','tracework_checks'],[str(base.BINARY),'check']]
  for i,command in enumerate(commands):
   out=subprocess.run(command+['--project',self.project['id'],'--session',self.ss['id'],'--rules',str(rules),'--junit',str(folder/f'{i}.xml'),'--html',str(folder/f'{i}.html')],env=env,capture_output=True);self.assertEqual(out.returncode,0,out.stderr);self.assertEqual(json.loads(out.stdout)['status'],'pass');self.assertEqual(ET.parse(folder/f'{i}.xml').getroot().attrib['errors'],'0')
  test=folder/'test_native.py';test.write_text('def test_contract(tracework_checks, tracework_recording):\n p,s=tracework_recording\n tracework_checks.assert_pass(tracework_checks.run(p,s))\n');out=subprocess.run(['python3','-m','pytest','-q','-p','tracework_pytest',str(test),'--tracework-project',self.project['id'],'--tracework-session',self.ss['id']],env=env,capture_output=True);self.assertEqual(out.returncode,0,out.stdout+out.stderr)
if __name__=='__main__':
 result=unittest.TextTestRunner(verbosity=2).run(unittest.TestSuite(EvidenceChecks(n) for n in unittest.defaultTestLoader.getTestCaseNames(EvidenceChecks) if n.startswith('test_v7_')));raise SystemExit(not result.wasSuccessful())
