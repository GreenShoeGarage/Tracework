#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-3.0-only
"""v0.3 end-to-end evidence, retention, migration and backup acceptance."""
import copy, hashlib, io, json, os, shutil, sqlite3, unittest, urllib.error, urllib.request, zipfile
from pathlib import Path
import integration as base

class Storage(base.Workflows):
    def call(self,path,method='POST',body=None):
        data=None if body is None else json.dumps(body).encode()
        req=urllib.request.Request(self.url+'/api/v1'+path,data=data,method=method,headers={'Content-Type':'application/json'})
        return json.load(urllib.request.urlopen(req))
    def export(self,sid,query=''):
        return urllib.request.urlopen(self.url+'/api/v1/sessions/'+sid+'/export/zip'+query).read()
    def upload(self,blob):
        return json.load(urllib.request.urlopen(urllib.request.Request(self.url+'/api/v1/import/trace',data=blob,headers={'Content-Type':'application/zip'})))
    def sealed(self,start=0,count=4):
        s=self.record()
        for i in range(start,start+count): self.ingest(self.event(i))
        self.client.stop_recording(s['id']);return s
    def rewrite(self,blob,mutate):
        with zipfile.ZipFile(io.BytesIO(blob)) as z: parts={n:z.read(n) for n in z.namelist()}
        m=json.loads(parts['manifest.json']);mutate(m,parts);parts['manifest.json']=json.dumps(m).encode()
        out=io.BytesIO()
        with zipfile.ZipFile(out,'w',zipfile.ZIP_DEFLATED) as z:
            for k,v in parts.items(): z.writestr(k,v)
        return out.getvalue()
    def test_v3_01_compact_range_exact_restart(self):
        s=self.sealed(count=21);sid=s['id'];original=list(self.client.events(sid));stats=self.get('/sessions/'+sid+'/range');self.assertEqual(stats['chunks'],21)
        answer=self.call('/sessions/'+sid+'/compact',body={});self.assertEqual(answer['chunks_after'],1)
        self.assertEqual(list(self.client.events(sid)),original)
        query=f"?from={original[5]['ordinal']}&to={original[10]['ordinal']}&from_ns={original[5]['received_ns']}&to_ns={original[10]['received_ns']}&source={self.source['id']}"
        self.assertEqual(self.get('/sessions/'+sid+'/events'+query)['events'],original[5:11])
        with zipfile.ZipFile(io.BytesIO(self.export(sid,query))) as z:
            m=json.loads(z.read('manifest.json'));self.assertEqual(m['session']['count'],6);self.assertEqual(m['original_session_count'],21)
            es=[json.loads(line) for c in m['chunks'] for line in z.read('chunks/'+c['id']+'.jsonl').splitlines()];self.assertEqual(es,original[5:11])
        for q in ['?from=9&to=2','?from_ns=9223372036854775808','?after=-1','?from=9007199254740992']:
            with self.assertRaises(urllib.error.HTTPError):self.get('/sessions/'+sid+'/events'+q)
        self.stop(crash=True);self.start();self.assertEqual(list(self.client.events(sid)),original)
        self.assertEqual(len(list((self.data/'chunks').glob('*.jsonl'))),1)
    def test_v3_02_import_range_provenance_readonly_idempotency(self):
        sid=self.sealed(count=8)['id'];original=list(self.client.events(sid));blob=self.export(sid,'?from=3&to=6');result=self.upload(blob)
        self.assertNotEqual(result['project_id'],self.project['id']);self.assertEqual(self.upload(blob)['session_id'],result['session_id'])
        imported=self.get('/sessions/'+result['session_id']);self.assertTrue(imported['project']['read_only']);self.assertEqual(imported['origin']['original_session_id'],sid)
        self.assertEqual(list(self.client.events(result['session_id'])),original[2:6])
        for path,body in [('/projects/'+result['project_id']+'/sessions',{'name':'bad'}),('/projects/'+result['project_id']+'/events',{'events':[self.event(50)]}),('/projects/'+result['project_id']+'/sources',self.source)]:
            with self.assertRaises(urllib.error.HTTPError):self.call(path,body=body)
        second=self.upload(self.export(result['session_id']));self.assertEqual(list(self.client.events(second['session_id'])),original[2:6])
        self.stop();self.start();self.assertEqual(list(self.client.events(result['session_id'])),original[2:6])
    def test_v3_03_import_validation_atomic(self):
        blob=self.export(self.sealed()['id']);before=self.get('/projects');files=set((self.data/'chunks').iterdir())
        def alter_event(m,parts,change):
            c=m['chunks'][0];key='chunks/'+c['id']+'.jsonl';es=[json.loads(x) for x in parts[key].splitlines()];change(es);parts[key]=b''.join(json.dumps(x).encode()+b'\n' for x in es);c['sha256']=hashlib.sha256(parts[key]).hexdigest();c['bytes']=len(parts[key])
        mutations=[lambda m,p:m['chunks'][0].update(sha256='0'*64),lambda m,p:p.update({'../outside':b'bad'}),lambda m,p:m['session'].update(count=999),lambda m,p:alter_event(m,p,lambda es:es[0].update(unrecognized=True)),lambda m,p:alter_event(m,p,lambda es:es[1].update(id=es[0]['id'])),lambda m,p:alter_event(m,p,lambda es:es[1].update(ordinal=es[0]['ordinal'])),lambda m,p:alter_event(m,p,lambda es:es[0]['values'].update(cycles=9007199254740993)),lambda m,p:m['session'].update(project_id='wrong')]
        for mutate in mutations:
            with self.assertRaises(urllib.error.HTTPError) as exc:self.upload(self.rewrite(blob,mutate))
            self.assertEqual(exc.exception.code,422);self.assertEqual(self.get('/projects'),before);self.assertEqual(set((self.data/'chunks').iterdir()),files)
    def test_v3_04_retention_protect_trash_restore_purge(self):
        old=self.sealed(0);middle=self.sealed(10);new=self.sealed(20);pid=self.project['id'];root='/projects/'+pid
        used=self.get('/storage')['trace_bytes'];self.call('/sessions/'+middle['id']+'/protect',body={'protected':True});self.call(root+'/retention','PUT',{'keep_last':1,'older_than_days':0})
        plan=self.get(root+'/retention/preview');self.assertEqual([x['id'] for x in plan['candidates']],[old['id']]);self.call('/sessions/'+old['id']+'/protect',body={'protected':True})
        with self.assertRaises(urllib.error.HTTPError):self.call(root+'/retention/apply',body={'plan_token':plan['plan_token']})
        self.call('/sessions/'+old['id']+'/protect',body={'protected':False});plan=self.get(root+'/retention/preview');self.call(root+'/retention/apply',body={'plan_token':plan['plan_token']})
        self.assertEqual(self.get('/storage')['trace_bytes'],used);self.assertEqual(len(self.get(root+'/trash')),1)
        self.stop();self.start();self.call('/sessions/'+old['id']+'/restore',body={});self.assertEqual(len(self.get(root+'/sessions')),3)
        self.call('/sessions/'+old['id']+'/trash',body={})
        with self.assertRaises(urllib.error.HTTPError):self.call('/sessions/'+old['id'],'DELETE',{'confirmation':'wrong'})
        self.call('/sessions/'+old['id'],'DELETE',{'confirmation':old['id']});self.assertLess(self.get('/storage')['trace_bytes'],used);self.assertEqual(self.get('/storage')['purged_identity_count'],4)
        self.record()
        with self.assertRaises(urllib.error.HTTPError):self.ingest(self.event(0))
        self.assertEqual(len(list(self.client.events(middle['id']))),4)
    def test_v3_05_pagination_empty_range_and_active_guards(self):
        s=self.record();sid=s['id']
        for first in range(0,2400,600):self.ingest(*[self.event(i) for i in range(first,first+600)])
        for suffix in ['compact','trash']:
            with self.assertRaises(urllib.error.HTTPError):self.call('/sessions/'+sid+'/'+suffix,body={})
        first=self.get('/sessions/'+sid+'/events?from=201&to=2350');self.assertEqual(len(first['events']),2000);self.assertTrue(first['more']);second=self.get('/sessions/'+sid+'/events?from=201&to=2350&after='+str(first['next']));self.assertEqual(len(second['events']),150);self.assertFalse(second['more'])
        empty=self.upload(self.export(sid,'?from=99999'));self.assertEqual(list(self.client.events(empty['session_id'])),[])
    def test_v3_06_legacy_bundles_and_backup(self):
        for path in (base.ROOT/'examples').rglob('*.trace.zip'):
            result=self.upload(path.read_bytes());self.assertEqual(self.get('/sessions/'+result['session_id'])['count'],240)
        self.assertEqual(len(self.get('/projects')),4);self.stop();backup=Path(self.tmp.name)/'backup';shutil.copytree(self.data,backup);self.data=backup;self.start();self.assertEqual(len(self.get('/projects')),4)
    @unittest.skipUnless(os.getenv('TRACEWORK_V02_BINARY'),'Set TRACEWORK_V02_BINARY to original v0.2 executable')
    def test_v3_07_actual_v02_migration(self):
        self.stop();current=base.BINARY;base.BINARY=Path(os.environ['TRACEWORK_V02_BINARY']);self.data=Path(self.tmp.name)/'old'
        try:
            self.start();self.assertEqual(self.get('/status')['version'],'0.2.0');p=self.client.create_project('Legacy v0.2');src=self.client.define_source(p['id'],'sensor',[{'name':'big','type':'uint64','unit':'count'}]);ss=self.client.start_recording(p['id'],'Old capture');self.client.source(p['id'],src['id'],src['events'][0]['id'],'producer-clock').emit({'big':'9007199254740993'},timestamp_ns=1790612345678901234);self.client.stop_recording(ss['id']);events=list(self.client.events(ss['id']));self.stop();hashes={f.name:hashlib.sha256(f.read_bytes()).hexdigest() for f in (self.data/'chunks').glob('*.jsonl')}
            base.BINARY=current;self.start();self.assertEqual(list(self.client.events(ss['id'])),events);self.assertEqual(self.get('/sessions/'+ss['id']+'/range')['first_received_ns'],events[0]['received_ns']);self.assertEqual({f.name:hashlib.sha256(f.read_bytes()).hexdigest() for f in (self.data/'chunks').glob('*.jsonl')},hashes);self.assertEqual(len(list(self.data.glob('metadata.pre-v0.3-*.sqlite'))),1)
            with sqlite3.connect(self.data/'metadata.sqlite') as db:self.assertEqual(db.execute('SELECT version FROM meta').fetchone()[0],9)
        finally:base.BINARY=current
    def test_v3_08_import_quota_atomic(self):
        sid=self.sealed(count=4)['id'];blob=self.export(sid);before=self.get('/projects');self.stop();self.start('--max-trace-mb','1')
        s=self.record()
        for i in range(100,160):self.ingest(self.event(i,message='x'*15000))
        blob=self.export(s['id']);count=len(self.get('/projects'))
        with self.assertRaises(urllib.error.HTTPError):self.upload(blob)
        self.assertEqual(len(self.get('/projects')),count)

    def test_v3_09_sample_protection_and_auxiliary_cleanup(self):
        result=self.call('/examples/motor',body={});sid=result['session_id'];pid=result['project']['id']
        self.call('/sessions/'+sid+'/protect',body={'protected':True})
        with self.assertRaises(urllib.error.HTTPError):self.call('/samples','DELETE')
        self.assertEqual(self.get('/sessions/'+sid)['count'],240)
        self.call('/sessions/'+sid+'/protect',body={'protected':False});self.call('/sessions/'+sid+'/trash',body={});self.call('/projects/'+pid+'/retention','PUT',{'keep_last':2,'older_than_days':0});self.call('/samples','DELETE')
        with sqlite3.connect(self.data/'metadata.sqlite') as db:
            self.assertEqual(db.execute('SELECT count(*) FROM trash WHERE session=?',(sid,)).fetchone()[0],0)
            self.assertEqual(db.execute('SELECT count(*) FROM retention WHERE project=?',(pid,)).fetchone()[0],0)
        self.assertEqual(len(self.get('/projects')),1)

if __name__=='__main__':
    suite=unittest.TestSuite(Storage(n) for n in unittest.defaultTestLoader.getTestCaseNames(Storage) if n.startswith('test_v3_'))
    result=unittest.TextTestRunner(verbosity=2).run(suite);raise SystemExit(not result.wasSuccessful())
