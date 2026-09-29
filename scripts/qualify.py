#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-3.0-only
"""Release runner. Requires prepared tools; captures each actual exit status."""
import json,os,subprocess,sys,time
from pathlib import Path
root=Path(__file__).resolve().parents[1]
group=sys.argv[1]
suites={
 'api':[['python3','tests/'+n+'.py'] for n in ['integration','connectors','storage','workspaces','investigations','actions','checks','access','integrations','migrations']],
 'browser':[['node','tests/'+n+'.cjs'] for n in ['workspace-model','browser','storage-browser','workspace-browser','investigation-browser','actions-browser','checks-browser','access-browser','extensions-browser']],
 'native':[['go','test','-race','-mod=vendor','-buildvcs=false','-v','./...'],['go','vet','-mod=vendor','./...']]}
report=root/'docs'/('V1-'+group.upper()+'-RESULTS.json')
selected=set(sys.argv[2:])
results=json.loads(report.read_text()) if selected and report.exists() else []
for command in suites[group]:
 name=Path(command[-1]).stem if group!='native' else command[1]
 if selected and name not in selected:continue
 start=time.monotonic();p=subprocess.run(command,cwd=root,stdout=subprocess.PIPE,stderr=subprocess.STDOUT,text=True)
 log='V1-'+group.upper()+'-'+name.upper()+'.txt';(root/'docs'/log).write_text(p.stdout)
 row={'command':command,'exit_code':p.returncode,'seconds':round(time.monotonic()-start,3),'log':log};results=[old for old in results if old['command']!=command];results.append(row)
 (root/'docs'/('V1-'+group.upper()+'-RESULTS.json')).write_text(json.dumps(results,indent=2)+'\n')
 print(group,name,'PASS' if p.returncode==0 else 'FAIL',flush=True)
 if p.returncode:print(p.stdout[-4000:]);sys.exit(p.returncode)
