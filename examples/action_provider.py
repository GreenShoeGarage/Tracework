#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-3.0-only
"""A deliberately harmless software counter action provider; no hardware control.
PYTHONPATH=sdk/python python3 examples/action_provider.py --project ID --source ID
"""
import argparse,json,os,time
from pathlib import Path
from tracework import Client
from tracework_actions import Provider
p=argparse.ArgumentParser();p.add_argument('--project',required=True);p.add_argument('--source',required=True);p.add_argument('--state',default='./counter-provider');args=p.parse_args();directory=Path(args.state);directory.mkdir(parents=True,exist_ok=True,mode=0o700);registration=directory/'registration.json'
if registration.exists():config=json.loads(registration.read_text())
else:
 config=Client().request('/projects/'+args.project+'/providers',{'name':'Software counter · synthetic','source_id':args.source,'actions':[{'id':'set_counter','name':'Set software counter','description':'Updates a local text file. This example controls no physical equipment.','effect':'write','timeout_ms':15000,'parameters':[{'field':{'name':'count','type':'uint64','unit':'cycles'},'min':'0','max':'1000000'},{'field':{'name':'delay_ms','type':'uint64','unit':'ms'},'min':'0','max':'10000'}]}]})
 fd=os.open(registration,os.O_WRONLY|os.O_CREAT|os.O_EXCL,0o600)
 with os.fdopen(fd,'w') as f:json.dump(config,f);f.flush();os.fsync(f.fileno())
if config['provider']['project_id']!=args.project or config['provider']['source_id']!=args.source:raise SystemExit('Provider state belongs to another project/source')
def set_counter(values,context):
 end=time.monotonic()+int(values['delay_ms'])/1000
 while time.monotonic()<end:context.check_canceled();time.sleep(.05)
 context.check_canceled();target=directory/'counter.txt';temp=directory/'counter.tmp'
 with temp.open('w') as f:f.write(values['count']);f.flush();os.fsync(f.fileno())
 os.replace(temp,target)
 return {'count':values['count'],'scope':'local software file only'}
client=Client(token=config['provider_token']);agent=Provider(client,config['provider'],{'set_counter':set_counter},directory/'journal');print('Provider ready:',config['provider']['id'],flush=True);agent.serve()
