#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-3.0-only
import json,os,subprocess,time,unittest,urllib.request,urllib.error
from pathlib import Path
import integration as base
from tracework_actions import validate_parameters
class Actions(base.Workflows):
 def call(self,path,body=None,method='POST',token=''):
  req=urllib.request.Request(self.url+'/api/v1'+path,json.dumps(body).encode() if body is not None else None,{'Content-Type':'application/json','Authorization':'Bearer '+token},method=method)
  return json.load(urllib.request.urlopen(req))
 def provider(self,timeout=10000):
  reg=self.call('/projects/'+self.project['id']+'/providers',{'name':'Test counter','source_id':self.source['id'],'actions':[{'id':'set','name':'Set counter','description':'Synthetic only','effect':'write','timeout_ms':timeout,'parameters':[{'field':{'name':'count','type':'uint64','unit':'cycles'},'min':'0','max':'18446744073709551615'}]}]});self.provider_id=reg['provider']['id'];self.secret=reg['provider_token'];self.prefix='/projects/'+self.project['id'];self.definition=reg['provider']['actions'][0];return reg
 def transport(self,op,body={}):return self.call('/providers/'+self.provider_id+'/'+op,body,token=self.secret)
 def own(self):
  self.transport('heartbeat');self.lease=self.call(self.prefix+'/providers/'+self.provider_id+'/lease',{})['lease_token']
 def prep(self,params=None,aid='set'):
  return self.call(self.prefix+'/actions/prepare',{'provider_id':self.provider_id,'action_id':aid,'lease_token':self.lease,'parameters':params or {'count':'9007199254740993'}})
 def execute(self,prep=None):
  p=prep or self.prep();return self.call(self.prefix+'/actions/execute',{'preparation_id':p['preparation_id'],'lease_token':self.lease,'arming_text':p['arming_text']})
 def runs(self):return self.get(self.prefix+'/actions')['runs']
 def test_v6_01_ownership_validation_arming_and_single_dispatch(self):
  self.provider()
  with self.assertRaises(urllib.error.HTTPError):self.call(self.prefix+'/providers/'+self.provider_id+'/lease',{})
  self.own()
  with self.assertRaises(urllib.error.HTTPError):self.call(self.prefix+'/providers/'+self.provider_id+'/lease',{'lease_token':'wrong'})
  for value in [None,1,'-1','18446744073709551616','01','1e99999999']:
   with self.assertRaises(urllib.error.HTTPError):self.prep({'count':value})
  with self.assertRaises(urllib.error.HTTPError):self.prep({'count':'1','extra':'2'})
  prep=self.prep()
  with self.assertRaises(urllib.error.HTTPError):self.call(self.prefix+'/actions/execute',{'preparation_id':prep['preparation_id'],'lease_token':self.lease,'arming_text':'yes'})
  run=self.execute(prep)
  self.assertEqual(self.get(self.prefix+'/actions/'+run['id'])['id'],run['id'])
  with self.assertRaises(urllib.error.HTTPError):self.get('/projects/other_project/actions/'+run['id'])
  with self.assertRaises(urllib.error.HTTPError):self.execute(prep)
  self.assertEqual(self.transport('next')['command']['id'],run['id']);self.assertIsNone(self.transport('next')['command'])
  result={'run_id':run['id'],'state':'succeeded','detail':'Target confirmed','result':{'count':'9007199254740993'}}
  self.assertEqual(self.transport('result',result)['state'],'succeeded');self.assertEqual(self.transport('result',result)['state'],'succeeded');result['state']='failed'
  with self.assertRaises(urllib.error.HTTPError):self.transport('result',result)
 def test_v6_02_cancel_and_ownership_change(self):
  self.provider();self.own();run=self.execute();self.call(self.prefix+'/actions/'+run['id']+'/cancel',{'lease_token':self.lease});self.assertIsNone(self.transport('next')['command']);self.assertEqual(self.runs()[0]['state'],'canceled')
  run=self.execute();self.transport('next');self.call(self.prefix+'/actions/'+run['id']+'/cancel',{'lease_token':self.lease});self.assertIn(run['id'],self.transport('next')['cancellation_requests']);self.assertEqual(self.runs()[0]['state'],'cancel_requested');self.transport('result',{'run_id':run['id'],'state':'canceled','detail':'Target stopped','result':None})
  run=self.execute();self.call(self.prefix+'/providers/'+self.provider_id+'/lease',{'lease_token':self.lease,'release':True});self.own();self.assertIsNone(self.transport('next')['command']);self.assertEqual(self.runs()[0]['state'],'canceled')
 def test_v6_03_unknown_blocks_retry_and_late_result(self):
  self.provider(100);self.own();run=self.execute();self.transport('next');time.sleep(.15);self.assertEqual(self.runs()[0]['state'],'unknown')
  with self.assertRaises(urllib.error.HTTPError):self.prep()
  out=self.transport('result',{'run_id':run['id'],'state':'succeeded','detail':'Late confirmation','result':None});self.assertTrue(out['late_result'])
  run=self.execute();self.transport('next');time.sleep(.15);self.runs();self.call(self.prefix+'/actions/'+run['id']+'/reconcile',{'lease_token':self.lease,'confirmation':'CHECKED '+run['id'],'detail':'Independent observation confirmed target counter state.'});self.assertEqual(self.runs()[0]['state'],'unknown_resolved');self.prep()
 def test_v6_04_restart_and_dedicated_credentials(self):
  self.provider();self.own();run=self.execute();self.transport('next');self.stop(crash=True);self.start(token='master');self.assertEqual(self.call(self.prefix+'/actions',method='GET',token='master')['runs'][0]['state'],'unknown')
  self.assertIsNone(self.transport('next')['command'])
  with self.assertRaises(urllib.error.HTTPError):self.call('/projects',method='GET',token=self.secret)
  with self.assertRaises(urllib.error.HTTPError):self.call('/providers/'+self.provider_id+'/next',{},token='master')
 def test_v6_05_independent_provider_and_local_validation(self):
  state=Path(self.tmp.name)/'target';env=dict(os.environ,PYTHONPATH=str(base.ROOT/'sdk/python'),TRACEWORK_URL=self.url)
  proc=subprocess.Popen(['python3',str(base.ROOT/'examples/action_provider.py'),'--project',self.project['id'],'--source',self.source['id'],'--state',str(state)],env=env,stdout=subprocess.DEVNULL,stderr=subprocess.PIPE)
  try:
   self.prefix='/projects/'+self.project['id']
   for _ in range(100):
    providers=self.get(self.prefix+'/actions')['providers']
    if providers and providers[0]['online']:break
    if proc.poll() is not None:raise AssertionError(proc.stderr.read().decode())
    time.sleep(.05)
   self.provider_id=providers[0]['id'];self.lease=self.call(self.prefix+'/providers/'+self.provider_id+'/lease',{})['lease_token'];run=self.execute(self.prep({'count':'123456','delay_ms':'0'},'set_counter'))
   for _ in range(100):
    if self.runs()[0]['state']=='succeeded':break
    time.sleep(.05)
   self.assertEqual(self.runs()[0]['state'],'succeeded');self.assertEqual((state/'counter.txt').read_text(),'123456')
   run=self.execute(self.prep({'count':'9','delay_ms':'3000'},'set_counter'))
   for _ in range(100):
    if self.runs()[0]['state']=='running':break
    time.sleep(.03)
   self.call(self.prefix+'/actions/'+run['id']+'/cancel',{'lease_token':self.lease})
   for _ in range(100):
    if self.runs()[0]['state']=='canceled':break
    time.sleep(.05)
   self.assertEqual(self.runs()[0]['state'],'canceled');self.assertEqual((state/'counter.txt').read_text(),'123456');self.assertEqual(len(json.loads((state/'journal/journal.json').read_text())),2)
   definition=providers[0]['actions'][0]
   with self.assertRaises(ValueError):validate_parameters(definition,{'count':'1000001','delay_ms':'0'})
   with self.assertRaises(ValueError):validate_parameters(definition,{'count':12,'delay_ms':'0'})
  finally:proc.terminate();proc.wait(timeout=5);proc.stderr.close()
if __name__=='__main__':
 result=unittest.TextTestRunner(verbosity=2).run(unittest.TestSuite(Actions(n) for n in unittest.defaultTestLoader.getTestCaseNames(Actions) if n.startswith('test_v6_')));raise SystemExit(not result.wasSuccessful())
