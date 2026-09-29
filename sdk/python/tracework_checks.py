"""Native TRACEWORK evidence checks and CI entry point. GPL-3.0-only.
The server is the single rule engine used by the browser, CLI and Python.
"""
import argparse,json,os,urllib.request
from pathlib import Path
from tracework import Client
class InsufficientEvidence(AssertionError):pass
class CheckFailure(AssertionError):pass
class Checks:
 def __init__(self,client=None):self.client=client or Client()
 def run(self,project,session,document=None):
  request={'session_id':session}
  if document is not None:request['document']=document
  return self.client.request('/projects/'+project+'/check-runs',request)
 def report(self,run_id,format):
  if format not in ('junit','html'):raise ValueError('junit or html required')
  req=urllib.request.Request(self.client.url+'/api/v1/check-runs/'+run_id+'?format='+format,headers={'Authorization':'Bearer '+self.client.token})
  with urllib.request.urlopen(req,timeout=self.client.timeout) as r:return r.read()
 @staticmethod
 def assert_pass(run):
  detail='; '.join(r['rule']['name']+': '+r['status']+' — '+r['detail'] for r in run['results'] if r['status']!='pass')
  if run['status']=='insufficient':raise InsufficientEvidence(detail)
  if run['status']!='pass':raise CheckFailure(detail)
  return run

def main():
 p=argparse.ArgumentParser(description='Evaluate retained evidence with the native rule engine');p.add_argument('--project',required=True);p.add_argument('--session',required=True);p.add_argument('--rules');p.add_argument('--junit');p.add_argument('--html');args=p.parse_args();checks=Checks();doc=json.loads(Path(args.rules).read_text()) if args.rules else None;run=checks.run(args.project,args.session,doc)
 for format in ('junit','html'):
  dest=getattr(args,format)
  if dest:
   fd=os.open(dest,os.O_WRONLY|os.O_CREAT|os.O_TRUNC,0o600)
   with os.fdopen(fd,'wb') as f:f.write(checks.report(run['id'],format))
 print(json.dumps(run,indent=2));return {'pass':0,'fail':1,'insufficient':2}[run['status']]
if __name__=='__main__':raise SystemExit(main())
