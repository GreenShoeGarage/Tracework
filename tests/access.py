#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-3.0-only
import hashlib,io,json,os,sqlite3,subprocess,unittest,urllib.request,urllib.error,zipfile
from pathlib import Path
import actions as actionbase
import integration as base
from tracework import Client
MASTER='fixture-bootstrap-token-not-for-production'
PASSWORD='fixture password only 123!'
class Access(actionbase.Actions):
 def setUp(self):
  super().setUp();self.stop();self.start(token=MASTER);self.client.token=MASTER
 def call(self,path,body=None,method='POST',token=None):return super().call(path,body,method,MASTER if token is None else token)
 def account(self,name,role):return self.call('/admin/accounts',{'name':name,'role':role,'password':PASSWORD})
 def login(self,name):return self.call('/auth/login',{'name':name,'password':PASSWORD},token='')['token']
 def forbidden(self,path,body=None,method='POST',token='',code=403):
  with self.assertRaises(urllib.error.HTTPError) as err:self.call(path,body,method,token)
  self.assertEqual(err.exception.code,code)
 def test_v8_01_role_matrix_and_credential_hashing(self):
  admin=self.account('owner','admin');viewer=self.account('reader','viewer');self.account('writer','operator');vt=self.login('reader');ot=self.login('writer');self.assertEqual(self.call('/auth/me',method='GET',token=vt)['role'],'viewer');self.call('/projects',method='GET',token=vt);self.forbidden('/projects',{'name':'not allowed'},token=vt);self.call('/projects',{'name':'Operator project','sources':[]},token=ot)
  for route,method,body in [('/admin/accounts','GET',None),('/admin/audit','GET',None),('/admin/backup','POST',{}),('/admin/shares','POST',{}),('/samples','DELETE',None)]:self.forbidden(route,body,method,ot)
  self.forbidden('/projects',method='GET',token='',code=401);self.forbidden('/projects',method='GET',token='fake',code=401)
  self.call('/admin/accounts',{'name':'short_probe','role':'viewer','password':'xxxxxxxxxxxx'});self.forbidden('/auth/login',{'name':'short_probe','password':'x'},token='',code=401)
  with sqlite3.connect(self.data/'metadata.sqlite') as db:
   salt,want=db.execute('SELECT salt,password_hash FROM accounts WHERE id=?',(viewer['id'],)).fetchone();self.assertEqual(hashlib.pbkdf2_hmac('sha256',PASSWORD.encode(),bytes.fromhex(salt),600000).hex(),want);self.assertNotEqual(want,PASSWORD);self.assertEqual(db.execute('SELECT secret_hash FROM auth_sessions WHERE account=?',(viewer['id'],)).fetchone()[0],hashlib.sha256(vt.encode()).hexdigest())
  self.forbidden('/admin/accounts/'+admin['id'],{'role':'viewer','disabled':False},'PUT',MASTER,409)
 def test_v8_02_revocation_logout_reset_and_audit(self):
  self.account('owner','admin');reader=self.account('reader','viewer');token=self.login('reader');self.call('/auth/logout',{},token=token);self.forbidden('/projects',method='GET',token=token,code=401);token=self.login('reader');self.call('/admin/accounts/'+reader['id'],{'role':'operator','disabled':False},'PUT');self.forbidden('/projects',method='GET',token=token,code=401);token=self.login('reader');self.assertEqual(self.call('/auth/me',method='GET',token=token)['role'],'operator');self.call('/admin/accounts/'+reader['id'],{'role':'operator','disabled':True},'PUT');self.forbidden('/auth/login',{'name':'reader','password':PASSWORD},token='',code=401)
  audit=self.get('/admin/audit');self.assertTrue(audit['verified_returned_chain']);self.assertGreater(len(audit['entries']),8);self.assertNotIn(PASSWORD,json.dumps(audit));self.assertNotIn(token,json.dumps(audit))
  with sqlite3.connect(self.data/'metadata.sqlite') as db:
   with self.assertRaises(sqlite3.IntegrityError):db.execute('DELETE FROM audit')
 def test_v8_03_scoped_shares_expiry_revocation_and_readonly(self):
  self.account('owner','admin');ss=self.record();self.ingest(self.event());self.client.stop_recording(ss['id']);share=self.call('/admin/shares',{'kind':'recording','resource_id':ss['id'],'expires_hours':1});path='/shared/'+share['id'];self.forbidden(path,method='GET',token='bad',code=401);self.assertEqual(self.call(path+'?view=info',method='GET',token=share['share_token'])['resource_id'],ss['id']);self.forbidden('/projects',method='GET',token=share['share_token'],code=401);self.forbidden(path,{},token=share['share_token'],code=401)
  req=urllib.request.Request(self.url+'/api/v1'+path,headers={'Authorization':'Bearer '+share['share_token']});data=urllib.request.urlopen(req).read()
  with zipfile.ZipFile(io.BytesIO(data)) as z:self.assertIsNone(z.testzip());self.assertTrue(any(n.endswith('.jsonl') for n in z.namelist()))
  self.call('/admin/shares/'+share['id'],method='DELETE');self.forbidden(path,method='GET',token=share['share_token'],code=401)
  share=self.call('/admin/shares',{'kind':'recording','resource_id':ss['id'],'expires_hours':1})
  with sqlite3.connect(self.data/'metadata.sqlite') as db:db.execute('UPDATE shares SET expires=0 WHERE id=?',(share['id'],))
  self.forbidden('/shared/'+share['id'],method='GET',token=share['share_token'],code=401)
 def test_v8_04_live_backup_restore_and_tamper_refusal(self):
  self.account('owner','admin');token=self.login('owner');ss=self.record();self.ingest(self.event());before=list(self.client.events(ss['id']));req=urllib.request.Request(self.url+'/api/v1/admin/backup',data=b'{}',headers={'Authorization':'Bearer '+MASTER},method='POST');data=urllib.request.urlopen(req).read();archive=Path(self.tmp.name)/'backup.zip';archive.write_bytes(data);restored=Path(self.tmp.name)/'restored';out=subprocess.run([str(base.BINARY),'restore','--backup',str(archive),'--data',str(restored)],capture_output=True);self.assertEqual(out.returncode,0,out.stderr)
  self.ingest(self.event(1));self.assertEqual(len(list(self.client.events(ss['id']))),2);self.stop();self.data=restored;self.start(token=MASTER);self.assertEqual(list(self.client.events(ss['id'])),before);self.assertEqual(self.get('/sessions/'+ss['id'])['status'],'interrupted');self.forbidden('/projects',method='GET',token=token,code=401);self.login('owner')
  out=subprocess.run([str(base.BINARY),'restore','--backup',str(archive),'--data',str(restored)],capture_output=True);self.assertNotEqual(out.returncode,0)
  bad=Path(self.tmp.name)/'bad.zip'
  with zipfile.ZipFile(io.BytesIO(data)) as original,zipfile.ZipFile(bad,'w') as tampered:
   for f in original.infolist():tampered.writestr(f,original.read(f.filename)+(b'broken' if f.filename.endswith('.jsonl') else b''))
  dest=Path(self.tmp.name)/'must-not-appear';out=subprocess.run([str(base.BINARY),'restore','--backup',str(bad),'--data',str(dest)],capture_output=True);self.assertNotEqual(out.returncode,0);self.assertFalse(dest.exists())
 def test_v8_05_action_lease_bound_to_account(self):
  self.account('owner','admin');self.account('operator_a','operator');self.account('operator_b','operator');a=self.login('operator_a');b=self.login('operator_b');self.provider();self.transport('heartbeat');leasePath=self.prefix+'/providers/'+self.provider_id+'/lease';lease=self.call(leasePath,{},token=a)['lease_token'];self.forbidden(leasePath,{'lease_token':lease},token=b,code=409);self.forbidden(self.prefix+'/actions/prepare',{'provider_id':self.provider_id,'action_id':'set','lease_token':lease,'parameters':{'count':'1'}},token=b,code=409);self.call(self.prefix+'/actions/prepare',{'provider_id':self.provider_id,'action_id':'set','lease_token':lease,'parameters':{'count':'1'}},token=a)
if __name__=='__main__':
 result=unittest.TextTestRunner(verbosity=2).run(unittest.TestSuite(Access(n) for n in unittest.defaultTestLoader.getTestCaseNames(Access) if n.startswith('test_v8_')));raise SystemExit(not result.wasSuccessful())
