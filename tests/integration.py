#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-3.0-only
import contextlib
import hashlib
import io
import json
import os
from pathlib import Path
import shutil
import socket
import subprocess
import sys
import tempfile
import time
import unittest
import urllib.error
import urllib.request
import zipfile

ROOT = Path(__file__).resolve().parents[1]
BINARY = Path(os.getenv('TRACEWORK_BINARY', ROOT / 'bin/tracework-linux-amd64'))
sys.path.insert(0, str(ROOT / 'sdk/python'))
from tracework import Client

class Workflows(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory(prefix='tracework-test-')
        self.data = Path(self.tmp.name)/'data'
        with socket.socket() as sock:
            sock.bind(('127.0.0.1', 0)); self.port = sock.getsockname()[1]
        self.url = f'http://127.0.0.1:{self.port}'
        self.process = None
        self.start()
        self.client = Client(self.url)
        self.project = self.client.create_project('Unscripted materials trial', 'No sample fields')
        self.assertEqual(self.project['sources'], [])
        self.source = self.client.define_source(self.project['id'], 'strain-reader', [
            {'name':'strain','type':'float64','unit':'µε'},
            {'name':'cycles','type':'uint64','unit':'cycles'},
            {'name':'state','type':'enum','unit':'','enum':['ready','loaded']},
            {'name':'valid','type':'bool','unit':''}],clock_domain='fixture-unsynced',stale_ms=100)
        self.path = '/projects/'+self.project['id']+'/events'

    def start(self, *extra, token=''):
        env = dict(os.environ, TRACEWORK_TOKEN=token)
        self.log = open(Path(self.tmp.name)/'server.log', 'ab')
        self.process = subprocess.Popen([str(BINARY),'serve','--data',str(self.data),
            '--listen',f'127.0.0.1:{self.port}',*extra], stdout=self.log, stderr=self.log,env=env)
        for _ in range(100):
            if self.process.poll() is not None:
                raise AssertionError((Path(self.tmp.name)/'server.log').read_text())
            try:
                request = urllib.request.Request(self.url+'/api/v1/status',headers={'Authorization':'Bearer '+token})
                urllib.request.urlopen(request,timeout=.2).close(); return
            except (OSError,urllib.error.URLError): time.sleep(.03)
        raise AssertionError('Server did not start')

    def stop(self, crash=False):
        if self.process and self.process.poll() is None:
            self.process.kill() if crash else self.process.terminate()
            self.process.wait(timeout=15)
        self.log.close()

    def tearDown(self):
        self.stop(); self.tmp.cleanup()

    def event(self, seq=0, **changes):
        e={'source_id':self.source['id'],'schema_id':self.source['events'][0]['id'],
           'schema_version':1,'boot_id':'boot_fixture','sequence':str(seq),
           'timestamp_ns':str(1790612345678901234+seq),'clock_domain':'fixture-unsynced',
           'sync_quality':'unsynchronized','uncertainty_ns':None,'gap_before':False,
           'values':{'strain':1.2345678901234567,'cycles':'18446744073709551615','state':'loaded','valid':True},
           'level':'info','message':'Material loaded'}
        e.update(changes); return e

    def ingest(self, *events): return self.client.request(self.path,{'events':list(events)})
    def record(self): return self.client.start_recording(self.project['id'],'Independent acceptance run')
    def get(self, path): return self.client.request(path)

    def test_01_exact_restart_and_independent_producer(self):
        session=self.record(); original=self.event()
        ack=self.ingest(original); self.assertEqual(ack['persisted'],1)
        first=list(self.client.events(session['id']))[0]
        self.stop(crash=True); self.start()
        saved=list(self.client.events(session['id']))[0]
        self.assertEqual(saved, first)
        self.assertEqual(saved['timestamp_ns'],original['timestamp_ns'])
        self.assertEqual(saved['values'],original['values'])
        self.assertEqual(self.get('/sessions/'+session['id'])['status'],'interrupted')
        self.assertEqual(self.ingest(original)['duplicates'],1)
        self.assertEqual(len(list(self.client.events(session['id']))),1)
        self.assertEqual(self.get('/projects/'+self.project['id']+'/live')['source_states'][self.source['id']],'disconnected')

    def test_02_atomic_validation_duplicates_conflicts(self):
        session=self.record(); e=self.event()
        self.assertEqual(self.ingest(e,e)['accepted'],1)
        self.assertEqual(self.ingest(e)['duplicates'],1)
        bad=self.event(1);bad['values']['cycles']=9007199254740993
        with self.assertRaises(urllib.error.HTTPError): self.ingest(self.event(2),bad)
        self.assertEqual(len(list(self.client.events(session['id']))),1)
        conflict=self.event();conflict['values']['strain']=999
        with self.assertRaises(urllib.error.HTTPError): self.ingest(conflict)
        self.assertEqual(len(list(self.client.events(session['id']))),1)

    def test_03_gaps_clock_reset_reboots_null(self):
        session=self.record()
        self.ingest(self.event(0),self.event(3),self.event(2,timestamp_ns='1'),self.event(0,boot_id='new_boot'))
        es=list(self.client.events(session['id']))
        self.assertIn('sequence_gap',es[1]['flags'])
        self.assertIn('clock_regression',es[2]['flags'])
        self.assertIn('out_of_order_sequence',es[2]['flags'])
        self.assertIn('producer_restart',es[3]['flags'])
        e=self.event(1,boot_id='new_boot');e['values']['strain']=None;self.ingest(e)
        self.assertIsNone(list(self.client.events(session['id']))[-1]['values']['strain'])

    def test_04_project_roundtrip_and_recording_export(self):
        exported=self.get('/projects/'+self.project['id'])
        imported=self.client.request('/projects',exported)
        self.assertNotEqual(imported['id'],exported['id'])
        self.assertEqual(imported['sources'],exported['sources'])
        session=self.record();self.ingest(self.event());self.client.stop_recording(session['id'])
        blob=urllib.request.urlopen(self.url+'/api/v1/sessions/'+session['id']+'/export/zip').read()
        with zipfile.ZipFile(io.BytesIO(blob)) as z:
            m=json.loads(z.read('manifest.json'))
            self.assertEqual(m['format'],'tracework.trace.v1')
            self.assertEqual(m['source_time_bounds_by_clock']['fixture-unsynced']['min_ns'],'1790612345678901234')
            self.assertEqual(m['gap_before_count'],0)
            for c in m['chunks']:
                chunk=z.read('chunks/'+c['id']+'.jsonl')
                self.assertEqual(hashlib.sha256(chunk).hexdigest(),c['sha256'])
                self.assertEqual(json.loads(chunk)['values']['cycles'],'18446744073709551615')
        for fmt in ['jsonl','csv']:
            data=urllib.request.urlopen(self.url+'/api/v1/sessions/'+session['id']+'/export/'+fmt).read()
            self.assertIn(b'1790612345678901234',data)

    def test_05_live_not_durable_and_stale(self):
        ack=self.ingest(self.event());self.assertEqual(ack['persisted'],0)
        self.assertIn('not durable',ack['guarantee'])
        path='/projects/'+self.project['id']+'/live'
        self.assertEqual(self.get(path)['source_states'][self.source['id']],'receiving')
        time.sleep(.15)
        self.assertEqual(self.get(path)['source_states'][self.source['id']],'disconnected')
        self.stop();self.start();self.assertEqual(self.get(path)['events'],[])

    def test_06_quota_rejection_does_not_ack_or_commit(self):
        self.stop();self.start('--max-trace-mb','1');session=self.record()
        oversized=[self.event(i,message='x'*16000) for i in range(100)]
        with self.assertRaises(urllib.error.HTTPError): self.ingest(*oversized)
        self.assertEqual(list(self.client.events(session['id'])),[])
        self.assertEqual(self.ingest(self.event())['persisted'],1)

    def test_07_write_failure_is_not_success(self):
        session=self.record();chunks=self.data/'chunks';chunks.rmdir();chunks.write_text('blocks directory')
        with self.assertRaises(urllib.error.HTTPError): self.ingest(self.event())
        self.assertEqual(self.get('/sessions/'+session['id'])['count'],0)
        chunks.unlink();chunks.mkdir();self.assertEqual(self.ingest(self.event())['persisted'],1)

    def test_08_uncommitted_file_quarantine(self):
        self.record();self.ingest(self.event());self.stop(crash=True)
        (self.data/'chunks'/'orphan.jsonl.tmp').write_text('{partial')
        self.start();self.assertTrue((self.data/'recovery'/'orphan.jsonl.tmp').exists())
        self.assertTrue(any('Quarantined' in s for s in self.get('/status')['recovery']))

    def test_09_corruption_fails_closed(self):
        self.record();self.ingest(self.event());self.stop()
        chunk=next((self.data/'chunks').glob('*.jsonl'));chunk.write_text('tampered')
        proc=subprocess.run([str(BINARY),'serve','--data',str(self.data),'--listen',f'127.0.0.1:{self.port}'],capture_output=True,timeout=10)
        self.assertNotEqual(proc.returncode,0);self.assertIn(b'integrity failure',proc.stderr)

    def test_10_backup_restore(self):
        s=self.record();self.ingest(self.event());self.client.stop_recording(s['id']);original=list(self.client.events(s['id']))
        self.stop();backup=Path(self.tmp.name)/'backup';shutil.copytree(self.data,backup);shutil.rmtree(self.data);shutil.copytree(backup,self.data);self.start()
        self.assertEqual(list(self.client.events(s['id'])),original)

    def test_11_origin_host_auth_and_directory_lock(self):
        for headers in [{'Origin':'https://untrusted.example'},{'Host':'untrusted.example'}]:
            with self.assertRaises(urllib.error.HTTPError):
                urllib.request.urlopen(urllib.request.Request(self.url+'/api/v1/projects',headers=headers))
        proc=subprocess.run([str(BINARY),'serve','--data',str(self.data),'--listen','127.0.0.1:0'],capture_output=True,timeout=10)
        self.assertNotEqual(proc.returncode,0);self.assertIn(b'in use',proc.stderr)
        self.stop();self.start(token='a-test-token-at-least-24-characters')
        with self.assertRaises(urllib.error.HTTPError): self.get('/status')
        authenticated=Client(self.url,token='a-test-token-at-least-24-characters')
        self.assertEqual(authenticated.request('/status')['version'],json.loads((ROOT/'package.json').read_text())['version'])

    def test_12_pagination_and_live_limit(self):
        s=self.record()
        for begin in range(0,2500,500):self.ingest(*[self.event(i) for i in range(begin,begin+500)])
        self.assertEqual(len(list(self.client.events(s['id']))),2500)
        live=self.get('/projects/'+self.project['id']+'/live')['events']
        self.assertEqual(len(live),2000);self.assertEqual(live[0]['sequence'],'500')

    def test_13_clear_samples_preserves_user_projects(self):
        r=self.client.request('/examples/motor',{})
        self.assertTrue(r['project']['sample'])
        self.assertEqual(len(list(self.client.events(r['session_id']))),240)
        self.client.request('/samples',method='DELETE')
        ids=[p['id'] for p in self.get('/projects')]
        self.assertIn(self.project['id'],ids);self.assertNotIn(r['project']['id'],ids)

    def test_14_agent_buffer_retry_and_restart(self):
        session=self.record();spool=Path(self.tmp.name)/'spool'
        args=[str(BINARY),'agent','--project',self.project['id'],'--source',self.source['id'],
              '--schema',self.source['events'][0]['id'],'--clock','fixture-unsynced','--spool',str(spool)]
        env=dict(os.environ,TRACEWORK_URL=self.url)
        self.stop(crash=True)
        agent=subprocess.Popen(args,stdin=subprocess.PIPE,stdout=subprocess.PIPE,stderr=subprocess.PIPE,env=env)
        agent.stdin.write((json.dumps(self.event()['values'])+'\n').encode());agent.stdin.flush()
        for _ in range(100):
            if list(spool.glob('*.pending')):break
            time.sleep(.02)
        self.assertEqual(len(list(spool.glob('*.pending'))),1)
        agent.kill();agent.wait(timeout=10);agent.stdin.close();agent.stdout.close();agent.stderr.close()
        self.start();newsession=self.record()
        resumed=subprocess.run(args,input=b'',capture_output=True,env=env,timeout=10)
        self.assertEqual(resumed.returncode,0,resumed.stderr)
        self.assertEqual(len(list(self.client.events(newsession['id']))),1)
        self.assertEqual(len(list(spool.glob('*.pending'))),0)
        self.assertEqual(len(list(self.client.events(session['id']))),0)

    def test_15_python_sdk_and_cli(self):
        s=self.record();source=self.client.source(self.project['id'],self.source['id'],self.source['events'][0]['id'],'fixture-unsynced')
        source.emit(self.event()['values'],timestamp_ns=1790612345678901234)
        self.assertEqual(next(self.client.events(s['id']))['timestamp_ns'],'1790612345678901234')
        cli=subprocess.run([str(BINARY),'api','GET','/api/v1/status'],env=dict(os.environ,TRACEWORK_URL=self.url),capture_output=True,timeout=10)
        self.assertEqual(cli.returncode,0);self.assertEqual(json.loads(cli.stdout)['version'],json.loads((ROOT/'package.json').read_text())['version'])

if __name__=='__main__':unittest.main(verbosity=2)
