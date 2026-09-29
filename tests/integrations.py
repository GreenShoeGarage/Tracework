#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-3.0-only
import copy,hashlib,json,os,socket,struct,subprocess,sys,threading,time,unittest
from pathlib import Path
import integration as base
class Integrations(base.Workflows):
 def source_from(self,command):
  output=subprocess.check_output([str(base.BINARY),*command]);src=json.loads(output);p=self.client.request('/projects/'+self.project['id']+'/sources',src);self.source=p['sources'][-1];return self.source
 def agent(self,connector,*args,input=None):
  spool=Path(self.tmp.name)/('spool-'+connector+str(time.time_ns()));src=self.source;command=[str(base.BINARY),'agent','--project',self.project['id'],'--source',src['id'],'--schema',src['events'][0]['id'],'--clock',src['clock_domain'],'--connector',connector,'--spool',str(spool),*args];env=dict(os.environ,TRACEWORK_URL=self.url,TRACEWORK_TOKEN='');proc=subprocess.Popen(command,stdin=subprocess.PIPE if input is not None else subprocess.DEVNULL,stdout=subprocess.PIPE,stderr=subprocess.PIPE,env=env)
  if input is not None:proc.stdin.write(input);proc.stdin.close();proc.stdin=None
  return proc,spool
 def test_v9_01_can_jsonl_to_exact_recording_and_decoder_guard(self):
  dbc=base.ROOT/'examples/protocols/bench.dbc';self.source_from(['dbc-schema','--dbc',str(dbc),'--can-id','291']);ss=self.record();proc,spool=self.agent('can-jsonl','--dbc',str(dbc),'--can-id','291',input=(base.ROOT/'examples/protocols/can-frames.jsonl').read_bytes());stdout,stderr=proc.communicate(timeout=15);self.assertEqual(proc.returncode,0,stderr);self.client.stop_recording(ss['id']);events=list(self.client.events(ss['id']));self.assertEqual(len(events),2);self.assertEqual(events[0]['timestamp_ns'],'1790612345678901234');self.assertEqual(events[0]['values']['can_data'],'fcff000000800200');self.assertEqual(events[0]['values']['count'],'2147483648');self.assertEqual(events[0]['values']['temperature'],-40.4);self.assertEqual(events[0]['values']['decoder_sha256'],self.source['decoder']['sha256']);self.assertEqual(len(list(spool.glob('*.pending'))),0)
  ev={k:v for k,v in events[0].items() if k not in ('id','session_id','ordinal','received_ns','flags')};ev['boot_id']='wrong_decoder';ev['values']['decoder_sha256']='f'*64
  with self.assertRaises(Exception):self.client.request('/projects/'+self.project['id']+'/events',{'events':[ev]})
 def test_v9_02_modbus_independent_tcp_peer_rejects_bad_transaction(self):
  listener=socket.socket();listener.bind(('127.0.0.1',0));listener.listen();listener.settimeout(.2);stop=threading.Event();requests=[];thread_errors=[]
  def exact(conn,n):
   b=b''
   while len(b)<n:
    x=conn.recv(n-len(b))
    if not x:raise EOFError()
    b+=x
   return b
  def peer():
   bad=True
   while not stop.is_set():
    try:conn,_=listener.accept()
    except socket.timeout:continue
    except OSError:break
    try:
     conn.settimeout(.5)
     while not stop.is_set():
      req=exact(conn,12);requests.append(req);tid,proto,length,unit,fc,start,count=struct.unpack('>HHHBBHH',req)
      if (proto,length,unit,fc,start,count)!=(0,6,1,3,0,7):raise AssertionError(req)
      raw=bytes.fromhex('fffffffffffffffffffe41c80000');body=bytes([fc,len(raw)])+raw;conn.sendall(struct.pack('>HHHB',(tid+1)&65535 if bad else tid,0,len(body)+1,unit)+body)
      if bad:bad=False;break
    except (EOFError,ConnectionError,socket.timeout):pass
    except Exception as e:thread_errors.append(str(e))
    finally:conn.close()
  thread=threading.Thread(target=peer);thread.start();config=json.loads((base.ROOT/'examples/protocols/modbus.json').read_text());config['address']='127.0.0.1:'+str(listener.getsockname()[1]);file=Path(self.tmp.name)/'modbus.json';file.write_text(json.dumps(config));self.source_from(['modbus-schema','--config',str(file)]);ss=self.record();proc,spool=self.agent('modbus','--modbus-config',str(file))
  try:
   for _ in range(150):
    events=list(self.client.events(ss['id']))
    if events:break
    if proc.poll() is not None:raise AssertionError(proc.stderr.read())
    time.sleep(.03)
   self.assertTrue(events);self.assertTrue(events[0]['gap_before']);self.assertEqual(events[0]['values']['count'],'18446744073709551615');self.assertEqual(events[0]['values']['signed_value'],'-2');self.assertEqual(events[0]['values']['temperature'],25);self.assertEqual(events[0]['values']['modbus_registers'],'fffffffffffffffffffe41c80000');self.assertGreaterEqual(len(requests),2);self.assertEqual(thread_errors,[])
  finally:proc.terminate();proc.communicate(timeout=5);stop.set();listener.close();thread.join(timeout=2)
 def manifest(self,script,seconds=5):
  code=Path(self.tmp.name)/('adapter-'+str(time.time_ns())+'.py');code.write_text(script);manifest=Path(str(code)+'.json');manifest.write_text(json.dumps({'format':'tracework.agent-extension.v1','name':'Independent fixture','command':[sys.executable,str(code)],'files':[{'path':str(code),'sha256':hashlib.sha256(code.read_bytes()).hexdigest()}],'timeout_seconds':seconds,'max_memory_mb':128}));return manifest,code
 def test_v9_03_process_extension_validation_limits_and_no_credentials(self):
  self.source=self.client.define_source(self.project['id'],'Plugin counter',[{'name':'count','type':'uint64','unit':'cycles'}]);ss=self.record();manifest,code=self.manifest("import json,os,sys\nc=json.loads(sys.stdin.readline())\nassert c['format']=='tracework.agent-extension.context.v1'\nassert 'TRACEWORK_TOKEN' not in os.environ\nprint(json.dumps({'count':'9007199254740993'}),flush=True)\n");proc,spool=self.agent('extension','--extension-manifest',str(manifest));stdout,stderr=proc.communicate(timeout=12);self.assertEqual(proc.returncode,0,stderr);self.assertEqual(list(self.client.events(ss['id']))[0]['values']['count'],'9007199254740993');code.write_text('raise Exception("unreviewed change")');proc,_=self.agent('extension','--extension-manifest',str(manifest));_,stderr=proc.communicate(timeout=5);self.assertNotEqual(proc.returncode,0);self.assertIn(b'hash',stderr)
  manifest,_=self.manifest('import time\ntime.sleep(30)\n',seconds=1);start=time.monotonic();proc,_=self.agent('extension','--extension-manifest',str(manifest));_,stderr=proc.communicate(timeout=5);self.assertNotEqual(proc.returncode,0);self.assertLess(time.monotonic()-start,4)
  manifest,_=self.manifest('print("x"*70000,flush=True)\n');proc,_=self.agent('extension','--extension-manifest',str(manifest));_,stderr=proc.communicate(timeout=5);self.assertNotEqual(proc.returncode,0);self.assertEqual(len(list(self.client.events(ss['id']))),1)
 def test_v9_04_go_rust_native_sdk_conformance(self):
  env=dict(os.environ,TRACEWORK_URL=self.url,TRACEWORK_TOKEN='');binaries=[Path(os.environ['TRACEWORK_GO_SDK_EXAMPLE']),Path(os.environ['TRACEWORK_RUST_SDK_EXAMPLE'])]
  for binary in binaries:
   out=subprocess.run([str(binary)],env=env,capture_output=True,timeout=20);self.assertEqual(out.returncode,0,out.stderr);identity=json.loads(out.stdout);events=list(self.client.events(identity['session_id']));self.assertEqual(len(events),1);self.assertEqual(events[0]['values']['count'],'18446744073709551615');self.assertEqual(events[0]['timestamp_ns'],'1790612345678901234');self.assertEqual(events[0]['schema_version'],1)
if __name__=='__main__':
 result=unittest.TextTestRunner(verbosity=2).run(unittest.TestSuite(Integrations(n) for n in unittest.defaultTestLoader.getTestCaseNames(Integrations) if n.startswith('test_v9_')));raise SystemExit(not result.wasSuccessful())
