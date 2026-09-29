#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-3.0-only
"""Run the packaged executable after independently checking every archived hash."""
import hashlib,json,os,subprocess,sys,tempfile,zipfile
from pathlib import Path
import integration as base
package=Path(sys.argv[1]).resolve();original=base.BINARY
with tempfile.TemporaryDirectory(prefix='tracework-release-unpack-') as tmp:
 root=Path(tmp)
 with zipfile.ZipFile(package) as z:
  assert z.testzip() is None
  for line in z.read('tracework/SHA256SUMS').decode().splitlines():
   want,name=line.split('  ',1);assert hashlib.sha256(z.read('tracework/'+name)).hexdigest()==want,name
  z.extractall(root)
  for member in z.infolist():
   if member.external_attr>>16&0o111:(root/member.filename).chmod(0o755)
 extracted=root/'tracework';base.BINARY=extracted/'bin/tracework-linux-amd64'
 assert subprocess.check_output([str(extracted/'start.sh'),'version'],cwd=extracted,text=True).strip()=='TRACEWORK 1.0.0'
 w=base.Workflows();w.setUp()
 try:
  s=w.record();w.ingest(w.event());before=list(w.client.events(s['id']));w.client.stop_recording(s['id'])
  import urllib.request
  for doc in (extracted/'docs').glob('*.md'):
   assert urllib.request.urlopen(w.url+'/docs/'+doc.name).read()==doc.read_bytes(),doc.name
  exported=urllib.request.urlopen(w.url+'/api/v1/sessions/'+s['id']+'/export/zip').read();trace=root/'capture.zip';trace.write_bytes(exported)
  verification=subprocess.run(['python3',str(extracted/'scripts/verify_trace.py'),str(trace)],capture_output=True,text=True);assert verification.returncode==0,verification.stderr
  w.stop();w.start();assert list(w.client.events(s['id']))==before
  env=dict(os.environ,TRACEWORK_BINARY=str(base.BINARY))
  browser=subprocess.run(['node',str(base.ROOT/'tests/extensions-browser.cjs')],env=env,capture_output=True,text=True,timeout=90);assert browser.returncode==0,browser.stdout+browser.stderr
  result={'version':'1.0.0','qualification_archive_sha256':hashlib.sha256(package.read_bytes()).hexdigest(),'executables_sha256':{f.name:hashlib.sha256(f.read_bytes()).hexdigest() for f in (extracted/'bin').iterdir() if f.is_file()},'every_archived_file_hash_verified':True,'extracted_start_script':True,'capture_export_verify_restart':True,'embedded_documents_match_packaged_source':True,'adversarial_browser_against_extracted_binary':True}
  (base.ROOT/'docs/V1-PACKAGE-SMOKE.json').write_text(json.dumps(result,indent=2)+'\n');print(json.dumps(result,indent=2))
 finally:w.tearDown();base.BINARY=original
