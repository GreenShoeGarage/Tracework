#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-3.0-only
"""Saved-view isolation, optimistic concurrency and upgrade acceptance."""
import copy,hashlib,json,os,sqlite3,unittest,urllib.error
from pathlib import Path
import integration as base
from storage import Storage

def panel():
    return {'id':'panel_a','kind':'plot','title':'Custom plot','height':400,'span':2,'binding_mode':'selection','bindings':[],'scope':'bindings','filter':'','level':'','sort':'signal','descending':False,'decimals':-1,'palette':'instrument','line':'linear','y_min':'','y_max':''}
def workspace():
    return {'format':'tracework.workspace.v1','active_tab':'view_a','tabs':[{'id':'view_a','name':'Investigation','columns':2,'panels':[panel()]}]}
class Workspaces(Storage):
    def put(self,doc,revision=0,pid=None):return self.call('/projects/'+(pid or self.project['id'])+'/workspace','PUT',{'expected_revision':revision,'workspace':doc})
    def test_v4_01_persistence_conflict_and_evidence_isolation(self):
        sid=self.sealed()['id'];events=list(self.client.events(sid));before=self.get('/projects/'+self.project['id']);path='/projects/'+self.project['id']+'/workspace';self.assertEqual(self.get(path),{'revision':0,'workspace':None})
        doc=workspace();result=self.put(doc);self.assertEqual(result['revision'],1)
        other=copy.deepcopy(doc);other['tabs'][0]['name']='Another view';self.put(other,1)
        with self.assertRaises(urllib.error.HTTPError) as exc:self.put(doc,1)
        self.assertEqual(exc.exception.code,409);self.assertEqual(self.get(path)['workspace'],other)
        self.stop(crash=True);self.start();self.assertEqual(self.get(path)['revision'],2);self.assertEqual(list(self.client.events(sid)),events);self.assertEqual(self.get('/projects/'+self.project['id']),before)
    def test_v4_02_invalid_layouts_are_atomic(self):
        good=workspace();self.put(good)
        mutations=[lambda d:d.update(format='wrong'),lambda d:d.update(active_tab='missing'),lambda d:d['tabs'].append(copy.deepcopy(d['tabs'][0])),lambda d:d['tabs'][0].update(columns=4),lambda d:d['tabs'][0]['panels'][0].update(kind='execute'),lambda d:d['tabs'][0]['panels'][0].update(height=99999),lambda d:d['tabs'][0]['panels'][0].update(y_min='NaN',y_max='1'),lambda d:d['tabs'][0]['panels'][0].update(y_min='5',y_max='2'),lambda d:d['tabs'][0]['panels'][0].update(unknown='discard me'),lambda d:d['tabs'][0]['panels'][0].update(filter='x'*513),lambda d:d['tabs'][0].update(panels=[panel() for _ in range(13)]),lambda d:d['tabs'][0].update(panels=None),lambda d:d['tabs'][0]['panels'][0].update(bindings=None)]
        for mutate in mutations:
            doc=copy.deepcopy(good);mutate(doc)
            with self.assertRaises(urllib.error.HTTPError) as exc:self.put(doc,1)
            self.assertEqual(exc.exception.code,400);self.assertEqual(self.get('/projects/'+self.project['id']+'/workspace')['revision'],1)
        self.assertEqual(self.get('/projects/'+self.project['id']+'/workspace')['workspace'],good)
    def test_v4_03_readonly_import_allows_views_not_data_mutation(self):
        sid=self.sealed()['id'];imported=self.upload(self.export(sid));pid=imported['project_id'];events=list(self.client.events(imported['session_id']));doc=workspace();doc['tabs'][0]['name']='Imported review';self.put(doc,pid=pid)
        self.assertEqual(self.get('/projects/'+pid+'/workspace')['workspace'],doc)
        with self.assertRaises(urllib.error.HTTPError):self.call('/projects/'+pid+'/sessions',body={'name':'not allowed'})
        self.assertEqual(list(self.client.events(imported['session_id'])),events)
    def test_v4_04_pinned_and_portable_metadata_roundtrip(self):
        src=self.source;sc=src['events'][0];f=sc['fields'][2];ref={'source_id':src['id'],'schema_id':sc['id'],'field_id':f['id'],'version':1,'source_name':src['name'],'schema_name':sc['name'],'field_name':f['name'],'type':f['type'],'unit':f['unit'],'enum':f['enum']}
        doc=workspace();p=doc['tabs'][0]['panels'][0];p.update(kind='state',binding_mode='pinned',bindings=[ref]);self.put(doc);p.update(binding_mode='portable');self.put(doc,1);self.assertEqual(self.get('/projects/'+self.project['id']+'/workspace')['workspace'],doc)
        p['bindings']=[ref,ref]
        with self.assertRaises(urllib.error.HTTPError):self.put(doc,2)
    def test_v4_05_sample_cleanup_and_foreign_project_isolation(self):
        self.put(workspace());sample=self.call('/examples/motor',body={});self.put(workspace(),pid=sample['project']['id']);self.call('/samples','DELETE');self.assertEqual(self.get('/projects/'+self.project['id']+'/workspace')['revision'],1)
        with sqlite3.connect(self.data/'metadata.sqlite') as db:self.assertEqual(db.execute('SELECT count(*) FROM workspaces').fetchone()[0],1)
    @unittest.skipUnless(os.getenv('TRACEWORK_V03_BINARY'),'Set TRACEWORK_V03_BINARY to original v0.3 executable')
    def test_v4_06_actual_v03_migration(self):
        self.stop();new=base.BINARY;base.BINARY=Path(os.environ['TRACEWORK_V03_BINARY']);self.data=Path(self.tmp.name)/'v03'
        try:
            self.start();self.assertEqual(self.get('/status')['version'],'0.3.0');p=self.client.create_project('v0.3 retained data');src=self.client.define_source(p['id'],'legacy',[{'name':'n','type':'uint64','unit':'cycles'}]);s=self.client.start_recording(p['id'],'Protected capture');self.client.source(p['id'],src['id'],src['events'][0]['id'],'producer-clock').emit({'n':'9007199254740993'},timestamp_ns=1790612345678901234);self.client.stop_recording(s['id']);self.call('/sessions/'+s['id']+'/protect',body={'protected':True});before=list(self.client.events(s['id']));self.stop();hashes={f.name:hashlib.sha256(f.read_bytes()).hexdigest() for f in (self.data/'chunks').glob('*.jsonl')}
            base.BINARY=new;self.start();self.assertEqual(list(self.client.events(s['id'])),before);self.assertTrue(self.get('/sessions/'+s['id'])['protected']);self.assertEqual(self.get('/projects/'+p['id']+'/workspace')['revision'],0);self.assertEqual({f.name:hashlib.sha256(f.read_bytes()).hexdigest() for f in (self.data/'chunks').glob('*.jsonl')},hashes);self.assertEqual(len(list(self.data.glob('metadata.pre-v0.4-*.sqlite'))),1)
            with sqlite3.connect(self.data/'metadata.sqlite') as db:self.assertEqual(db.execute('SELECT version FROM meta').fetchone()[0],9)
        finally:base.BINARY=new
if __name__=='__main__':
    result=unittest.TextTestRunner(verbosity=2).run(unittest.TestSuite(Workspaces(n) for n in unittest.defaultTestLoader.getTestCaseNames(Workspaces) if n.startswith('test_v4_')));raise SystemExit(not result.wasSuccessful())
