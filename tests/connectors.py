#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-3.0-only
"""Linux PTY + MQTT 3.1.1 protocol fixture tests. No physical hardware claimed."""
import copy
import hashlib
import json
import os
from pathlib import Path
import pty
import socket
import struct
import subprocess
import threading
import time
import unittest
import urllib.error

import integration as base


def until(fn, timeout=12):
    end=time.monotonic()+timeout
    while time.monotonic()<end:
        try:
            value=fn()
            if value:return value
        except (OSError, ValueError): pass
        time.sleep(.05)
    raise AssertionError('condition timed out')


def packet(kind, payload=b''):
    n=len(payload); size=bytearray()
    while True:
        v=n%128;n//=128;size.append(v|(128 if n else 0))
        if not n:break
    return bytes([kind])+size+payload


def read_packet(conn):
    def exact(n):
        out=b''
        while len(out)<n:
            b=conn.recv(n-len(out))
            if not b:raise EOFError()
            out+=b
        return out
    kind=exact(1)[0]; n=0;shift=0
    while True:
        b=exact(1)[0];n+=(b&127)<<shift
        if not b&128:break
        shift+=7
    return kind,exact(n)


class Broker:
    """Small test peer: CONNECT/SUBSCRIBE/PUBLISH QoS1/PUBACK/PING/DISCONNECT.

    It deliberately sends the same native observation with another packet ID to
    exercise application dedup, and can cut the TCP connection. Not a production
    broker and not a broker compatibility certification.
    """
    def __init__(self):
        self.listener=socket.socket();self.listener.bind(('127.0.0.1',0));self.listener.listen()
        self.listener.settimeout(.1);self.port=self.listener.getsockname()[1]
        self.lock=threading.Lock();self.connection=None;self.subscriptions=0;self.acks=[];self.stopped=False
        self.thread=threading.Thread(target=self.run,daemon=True);self.thread.start()
    def run(self):
        while not self.stopped:
            try:conn,_=self.listener.accept()
            except socket.timeout:continue
            except OSError:return
            conn.settimeout(.2)
            try:
                while not self.stopped:
                    try:kind,payload=read_packet(conn)
                    except socket.timeout:continue
                    if kind>>4==1:conn.sendall(packet(0x20,b'\x00\x00'))
                    elif kind>>4==8:
                        conn.sendall(packet(0x90,payload[:2]+b'\x01'))
                        with self.lock:self.connection=conn;self.subscriptions+=1
                    elif kind>>4==4:self.acks.append(struct.unpack('!H',payload)[0])
                    elif kind>>4==12:conn.sendall(packet(0xd0))
                    elif kind>>4==14:break
            except (EOFError,OSError):pass
            finally:
                with self.lock:
                    if self.connection is conn:self.connection=None
                conn.close()
    def publish(self,payload,pid,retained=False,duplicate=False):
        topic=b'lab/observations';body=struct.pack('!H',len(topic))+topic+struct.pack('!H',pid)+json.dumps(payload).encode()
        with self.lock:
            self.connection.sendall(packet(0x32|int(retained)|(8 if duplicate else 0),body))
    def disconnect(self):
        with self.lock:
            if self.connection:self.connection.shutdown(socket.SHUT_RDWR);self.connection.close();self.connection=None
    def close(self):
        self.stopped=True;self.disconnect();self.listener.close();self.thread.join(2)


class Connectors(base.Workflows):
    # Run only the v0.2 names below; baseline suite is invoked separately.
    def agent(self, *extra, connector='stdin', config=False):
        spool=Path(self.tmp.name)/('spool-'+connector)
        args=[str(base.BINARY),'agent','--project',self.project['id'],'--source',self.source['id'],
              '--schema',self.source['events'][0]['id'],'--clock','fixture-unsynced','--spool',str(spool),
              '--connector',connector,*extra]
        if config:
            path=Path(self.tmp.name)/'agent.json';path.write_text(json.dumps({
                'project_id':self.project['id'],'source_id':self.source['id'],
                'schema_id':self.source['events'][0]['id'],'schema_version':1,'clock_domain':'fixture-unsynced',
                'spool':str(spool),'connector':'stdin','recording':'required'}))
            args=[str(base.BINARY),'agent','--config='+str(path),*extra]
        log=open(Path(self.tmp.name)/('agent-'+connector+'.log'),'ab')
        proc=subprocess.Popen(args,stdin=subprocess.PIPE,stdout=log,stderr=log,env=dict(os.environ,TRACEWORK_URL=self.url))
        def cleanup():
            if proc.poll() is None:proc.kill();proc.wait(timeout=5)
            proc.stdin.close();log.close()
        self.addCleanup(cleanup)
        return proc,spool

    def test_v2_01_recording_intent_and_pinning(self):
        with self.assertRaises(urllib.error.HTTPError) as cm:self.client.request(self.path,{'events':[self.event()],'require_recording':True})
        self.assertEqual(cm.exception.code,409);self.assertEqual(self.get('/projects/'+self.project['id']+'/live')['events'],[])
        proc,spool=self.agent(config=True);proc.stdin.write((json.dumps(self.event()['values'])+'\n').encode());proc.stdin.flush()
        until(lambda:list(spool.glob('*.pending')))
        until(lambda:any(h['state']=='waiting_recording' for h in self.get('/projects/'+self.project['id']+'/agents')))
        self.assertIsNone(proc.poll());session=self.record();until(lambda:len(list(self.client.events(session['id'])))==1)
        self.assertEqual(list(spool.glob('*.pending')),[])
        with self.assertRaises(urllib.error.HTTPError) as cm:self.client.request(self.path,{'events':[self.event(2)],'session_id':'another-session'})
        self.assertEqual(cm.exception.code,409)
        self.client.request(self.path,{'events':[self.event(2)],'session_id':session['id']})
        self.client.stop_recording(session['id'])
        # Retrying a durable acknowledged event is safe even after capture is closed.
        self.assertEqual(self.client.request(self.path,{'events':[self.event(2)],'require_recording':True})['duplicates'],1)
        self.assertEqual(len(list(self.client.events(session['id']))),2)

    def test_v2_02_immutable_revisions_and_multiple_sources(self):
        session=self.record();sc=self.source['events'][0];before=self.ingest(self.event())
        fields=copy.deepcopy(sc['fields']);fields[0]['unit']='mm';fields[0].pop('id')
        p=self.client.revise_schema(self.project['id'],self.source['id'],sc['id'],1,sc['name'],fields)
        self.assertEqual(p['sources'][0]['events'][0],sc)
        v2=p['sources'][0]['events'][1];self.assertEqual(v2['version'],2);self.assertNotEqual(v2['fields'][0]['id'],sc['fields'][0]['id'])
        self.ingest(self.event(1,schema_version=2),self.event(2))
        with self.assertRaises(urllib.error.HTTPError):self.client.revise_schema(self.project['id'],self.source['id'],sc['id'],1,sc['name'],fields)
        other=self.client.define_source(self.project['id'],'power',[{'name':'watts','type':'float64','unit':'W'}],clock_domain='independent-clock')
        self.client.source(self.project['id'],other['id'],other['events'][0]['id'],'independent-clock').emit({'watts':12.5})
        recorded=list(self.client.events(session['id']));self.assertEqual([e['schema_version'] for e in recorded[:3]],[1,2,1])
        self.assertEqual(len(self.get('/sessions/'+session['id'])['project']['sources']),2)
        self.assertEqual(recorded[0]['values'],self.event()['values'])

    def test_v2_03_serial_disconnect_reopen_and_partial_line(self):
        session=self.record();master,slave=pty.openpty();device=Path(self.tmp.name)/'serial-device';device.symlink_to(os.ttyname(slave))
        handles=[master,slave]
        self.addCleanup(lambda:[os.close(fd) for fd in handles])
        proc,spool=self.agent('--serial-device',str(device),'--serial-baud','115200',connector='serial')
        until(lambda:'Serial JSONL connected' in (Path(self.tmp.name)/'agent-serial.log').read_text())
        os.write(master,(json.dumps(self.event()['values'])+'\n').encode())
        until(lambda:len(list(self.client.events(session['id'])))==1)
        os.write(master,b'{"incomplete":');os.close(master);handles.remove(master)
        until(lambda:'Serial disconnected' in (Path(self.tmp.name)/'agent-serial.log').read_text())
        m2,s2=pty.openpty();handles.extend([m2,s2]);device.unlink();device.symlink_to(os.ttyname(s2))
        until(lambda:(Path(self.tmp.name)/'agent-serial.log').read_text().count('Serial JSONL connected')>=2)
        vals=self.event()['values'];vals['strain']=42.125;os.write(m2,(json.dumps(vals)+'\n').encode())
        until(lambda:len(list(self.client.events(session['id'])))==2)
        events=list(self.client.events(session['id']));self.assertEqual(events[1]['values'],vals);self.assertTrue(events[1]['gap_before']);self.assertIn('producer_reported_gap',events[1]['flags'])
        self.assertEqual(events[1]['sequence'],'1');self.assertEqual(events[1]['boot_id'],events[0]['boot_id']);self.assertIsNone(proc.poll())

    def test_v2_04_mqtt_durable_ack_redelivery_reconnect_retained(self):
        broker=Broker();self.addCleanup(broker.close)
        proc,spool=self.agent('--mqtt-broker',f'tcp://127.0.0.1:{broker.port}','--mqtt-topic','lab/observations','--input','envelope',connector='mqtt')
        until(lambda:broker.subscriptions==1)
        broker.publish(self.event(),1);until(lambda:1 in broker.acks)
        # MQTT acknowledgment occurs after local durable spool, before any server recording exists.
        pending=list(spool.glob('*.pending'));self.assertEqual(len(pending),1)
        saved=json.loads(pending[0].read_text())['events'][0];self.assertEqual(saved['timestamp_ns'],self.event()['timestamp_ns'])
        session=self.record();until(lambda:len(list(self.client.events(session['id'])))==1)
        broker.publish(self.event(),2,duplicate=True);until(lambda:2 in broker.acks);until(lambda:self.get('/status')['duplicates']>=1)
        self.assertEqual(len(list(self.client.events(session['id']))),1)
        broker.publish(self.event(50),3,retained=True);until(lambda:3 in broker.acks);time.sleep(.2);self.assertEqual(len(list(self.client.events(session['id']))),1)
        broker.disconnect();until(lambda:broker.subscriptions>=2)
        broker.publish(self.event(1),4);until(lambda:4 in broker.acks);until(lambda:len(list(self.client.events(session['id'])))==2)
        self.assertEqual(list(self.client.events(session['id']))[-1]['sequence'],'1');self.assertIsNone(proc.poll())

    def test_v2_05_agent_grpc_transport(self):
        self.stop()
        with socket.socket() as sock:sock.bind(('127.0.0.1',0));port=sock.getsockname()[1]
        self.start('--grpc-listen',f'127.0.0.1:{port}');session=self.record()
        proc,spool=self.agent('--grpc-address',f'127.0.0.1:{port}','--grpc-plaintext','--input','envelope')
        for i in range(4):proc.stdin.write((json.dumps(self.event(i))+'\n').encode())
        proc.stdin.close();until(lambda:proc.poll() is not None)
        self.assertEqual(proc.returncode,0,(Path(self.tmp.name)/'agent-stdin.log').read_text());self.assertEqual(len(list(self.client.events(session['id']))),4)

    def test_v2_07_spool_quota_blocks_and_drains_without_loss(self):
        self.source=self.client.define_source(self.project['id'],'buffer-test',[{'name':'blob','type':'string','unit':''}],clock_domain='fixture-unsynced')
        proc,spool=self.agent('--max-spool-mb','1')
        value={'blob':'x'*16000};line=(json.dumps(value)+'\n').encode()
        def produce():
            try:
                for _ in range(100):proc.stdin.write(line);proc.stdin.flush()
                proc.stdin.close()
            except (BrokenPipeError,ValueError):pass
        producer=threading.Thread(target=produce,daemon=True);producer.start()
        until(lambda:'Spool full' in (Path(self.tmp.name)/'agent-stdin.log').read_text())
        self.assertLessEqual(sum(f.stat().st_size for f in spool.glob('*.pending')),1024*1024)
        self.assertIsNone(proc.poll());session=self.record()
        until(lambda:proc.poll() is not None,timeout=25)
        self.assertEqual(proc.returncode,0,(Path(self.tmp.name)/'agent-stdin.log').read_text())
        producer.join(1);events=list(self.client.events(session['id']))
        self.assertEqual(len(events),100);self.assertEqual([e['sequence'] for e in events],[str(i) for i in range(100)])
        self.assertTrue(any(e['gap_before'] for e in events));self.assertEqual(list(spool.glob('*.pending')),[])

    @unittest.skipUnless(os.getenv('TRACEWORK_BASELINE_BINARY'),'Set TRACEWORK_BASELINE_BINARY to the original v0.1.0 binary')
    def test_v2_06_actual_v01_migration(self):
        self.stop();new_binary=base.BINARY;old_data=self.data
        base.BINARY=Path(os.environ['TRACEWORK_BASELINE_BINARY']);self.data=Path(self.tmp.name)/'v01-data'
        try:
            self.start();self.assertEqual(self.get('/status')['version'],'0.1.0')
            project=self.client.create_project('Existing v0.1 project');source=self.client.define_source(project['id'],'legacy',[{'name':'large','type':'uint64','unit':'count'}])
            session=self.client.start_recording(project['id'],'Legacy recording');self.client.source(project['id'],source['id'],source['events'][0]['id'],'producer-clock').emit({'large':'9007199254740993'},timestamp_ns=1790612345678901234)
            original=list(self.client.events(session['id']));self.client.stop_recording(session['id']);self.stop()
            hashes={f.name:hashlib.sha256(f.read_bytes()).hexdigest() for f in (self.data/'chunks').glob('*.jsonl')}
            base.BINARY=new_binary;self.start();self.assertEqual(list(self.client.events(session['id'])),original)
            self.assertEqual({f.name:hashlib.sha256(f.read_bytes()).hexdigest() for f in (self.data/'chunks').glob('*.jsonl')},hashes)
            self.assertEqual(len(list(self.data.glob('metadata.pre-v0.2-*.sqlite'))),1)
            self.assertEqual(self.get('/projects/'+project['id'])['sources'][0],source)
        finally:base.BINARY=new_binary

if __name__=='__main__':
    suite=unittest.TestSuite(Connectors(n) for n in unittest.defaultTestLoader.getTestCaseNames(Connectors) if n.startswith('test_v2_'))
    result=unittest.TextTestRunner(verbosity=2).run(suite)
    raise SystemExit(not result.wasSuccessful())
