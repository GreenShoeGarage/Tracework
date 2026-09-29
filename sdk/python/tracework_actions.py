"""TRACEWORK action provider transport. GPL-3.0-only; Python standard library.

Handlers are code installed deliberately by the provider operator, never imported
project data. Durable dispatch journal prevents automatic execution after a retry.
Hardware interlocks and target-specific validation belong in each handler.
"""
import decimal
import fcntl
import json
import math
import os
from pathlib import Path
import re
import threading
import time

class Canceled(Exception):
    """Raise only after the target has actually stopped the operation."""

class ActionContext:
    def __init__(self, run):
        self.run_id = run['id']
        self.cancel_requested = threading.Event()
        self.deadline = time.monotonic() + run['action']['timeout_ms']/1000
    def check_canceled(self):
        if self.cancel_requested.is_set():
            raise Canceled('Target confirmed cooperative cancellation')
    @property
    def deadline_passed(self):
        return time.monotonic() >= self.deadline


def validate_parameters(definition, values):
    params = definition['parameters']
    if set(values) != {p['field']['name'] for p in params}:
        raise ValueError('Parameter set does not match the locally installed action')
    for p in params:
        f=p['field'];v=values[f['name']];kind=f['type']
        if kind == 'float64':
            if type(v) not in (int,float) or not math.isfinite(v): raise ValueError('Finite numeric parameter required')
        elif kind in ('int64','uint64'):
            pattern=r'(0|-?[1-9][0-9]*)' if kind=='int64' else r'(0|[1-9][0-9]*)'
            if not isinstance(v,str) or not re.fullmatch(pattern,v): raise ValueError('Exact canonical integer string required')
            number=int(v);minimum=-(2**63) if kind=='int64' else 0;maximum=2**63-1 if kind=='int64' else 2**64-1
            if not minimum<=number<=maximum: raise ValueError('Integer out of range')
        elif kind == 'bool':
            if type(v) is not bool: raise ValueError('Boolean parameter required')
        elif kind in ('string','enum'):
            if not isinstance(v,str) or len(v.encode())>16384: raise ValueError('Bounded string required')
            if kind=='enum' and v not in f['enum']: raise ValueError('Unknown enum choice')
        else: raise ValueError('Unsupported locally installed parameter type')
        if p.get('min','')!='':
            number=decimal.Decimal(str(v))
            if not decimal.Decimal(p['min'])<=number<=decimal.Decimal(p['max']): raise ValueError('Parameter outside target limits')
    return values


class Provider:
    def __init__(self, client, descriptor, handlers, journal_dir):
        self.client=client;self.descriptor=descriptor;self.handlers=handlers
        self.path=Path(journal_dir);self.path.mkdir(parents=True,exist_ok=True,mode=0o700)
        self.lock=open(self.path/'provider.lock','a+b');fcntl.flock(self.lock,fcntl.LOCK_EX|fcntl.LOCK_NB)
        self.journal_path=self.path/'journal.json';self.journal=json.loads(self.journal_path.read_text()) if self.journal_path.exists() else {}
        self.mutex=threading.Lock();self.active=None;self.context=None
        self.definitions={a['id']:a for a in descriptor['actions']}
    def _persist(self):
        tmp=self.path/'journal.tmp';data=json.dumps(self.journal,allow_nan=False,separators=(',',':')).encode()
        fd=os.open(tmp,os.O_WRONLY|os.O_CREAT|os.O_TRUNC,0o600)
        with os.fdopen(fd,'wb') as stream:stream.write(data);stream.flush();os.fsync(stream.fileno())
        os.replace(tmp,self.journal_path);directory=os.open(self.path,os.O_RDONLY)
        try:os.fsync(directory)
        finally:os.close(directory)
    def _result(self,run):
        state='succeeded';detail='Target reported completion';result=None
        try:
            definition=self.definitions.get(run['action']['id'])
            if definition != run['action']: raise ValueError('Command differs from locally installed action definition')
            values=validate_parameters(definition,run['parameters'])
            if definition['id'] not in self.handlers:raise ValueError('No locally installed handler')
            result=self.handlers[definition['id']](values,self.context)
            encoded=json.dumps(result,allow_nan=False).encode()
            if len(encoded)>65536:raise ValueError('Target result exceeds 64 KiB')
        except Canceled as err:state='canceled';detail=str(err)[:4096]
        except Exception as err:state='failed';detail=(type(err).__name__+': '+str(err))[:4096]
        with self.mutex:
            self.journal[run['id']]={'state':'result','result':{'run_id':run['id'],'state':state,'detail':detail,'result':result if state=='succeeded' else None}}
            self._persist()
    def tick(self):
        # Outcomes are retried; commands are never automatically rerun.
        with self.mutex:pending=[(k,v['result']) for k,v in self.journal.items() if v['state']=='result']
        for key,result in pending:
            self.client.request('/providers/'+self.descriptor['id']+'/result',result)
            with self.mutex:self.journal[key]={'state':'acknowledged'};self._persist()
        response=self.client.request('/providers/'+self.descriptor['id']+'/next',{})
        if self.context and self.context.run_id in response['cancellation_requests']:self.context.cancel_requested.set()
        run=response['command']
        if not run:return
        with self.mutex:
            if run['id'] in self.journal:raise RuntimeError('Previously dispatched command refused; reconcile its actual target outcome')
            if self.active and self.active.is_alive():raise RuntimeError('Target already busy; new command left unknown')
            if len(self.journal)>=10000:raise RuntimeError('Dispatch journal limit reached; archive it with operator review before further control')
            self.journal[run['id']]={'state':'started'};self._persist()
        self.context=ActionContext(run);self.active=threading.Thread(target=self._result,args=(run,),daemon=True);self.active.start()
    def serve(self, stop=None):
        stop=stop or threading.Event()
        while not stop.is_set():
            try:self.tick()
            except OSError as err:print('Provider transport unavailable:',err,flush=True)
            stop.wait(.5)
