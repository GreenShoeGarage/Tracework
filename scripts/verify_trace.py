#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-3.0-only
"""Verify a native trace ZIP and optionally print original events. Never executes code."""
import argparse
import hashlib
import json
import re
import sys
import zipfile

p=argparse.ArgumentParser(description=__doc__)
p.add_argument('trace')
p.add_argument('--events',action='store_true',help='emit original JSONL envelopes to stdout')
a=p.parse_args()
try:
    with zipfile.ZipFile(a.trace) as z:
        infos=z.infolist()
        if len({i.filename for i in infos}) != len(infos):
            raise ValueError('Duplicate ZIP members')
        if sum(i.file_size for i in infos)>1024**3:
            raise ValueError('Verifier limit: 1 GiB expanded bundle')
        if z.getinfo('manifest.json').file_size>4*1024**2:
            raise ValueError('Oversized manifest')
        m=json.loads(z.read('manifest.json'))
        if m['format']!='tracework.trace.v1':raise ValueError('Unsupported format')
        count=0;first=None;last=None;seen=set()
        for c in m['chunks']:
            if not re.fullmatch(r'[a-zA-Z0-9_-]{1,80}',c['id']):raise ValueError('Invalid chunk ID')
            name='chunks/'+c['id']+'.jsonl'
            if z.getinfo(name).file_size>8*1024**2:raise ValueError('Oversized chunk')
            blob=z.read(name)
            if hashlib.sha256(blob).hexdigest()!=c['sha256']:raise ValueError('Hash mismatch: '+c['id'])
            lines=blob.splitlines()
            if len(lines)!=c['event_count']:raise ValueError('Event-count mismatch')
            for line in lines:
                e=json.loads(line)
                for field in ['timestamp_ns','received_ns','sequence']:
                    if not isinstance(e[field],str):raise ValueError('Precision contract violated: '+field)
                    int(e[field])
                if e['id'] in seen:raise ValueError('Duplicate event ID')
                seen.add(e['id']);count+=1
                ts=int(e['timestamp_ns']);first=ts if first is None else min(first,ts);last=ts if last is None else max(last,ts)
                if a.events:sys.stdout.buffer.write(line+b'\n')
        if count!=m['session']['count']:raise ValueError('Session count mismatch')
        print(json.dumps({'verified':True,'session_id':m['session']['id'],'status':m['session']['status'],
                          'events':count,'source_time_min_ns':str(first) if first is not None else None,
                          'source_time_max_ns':str(last) if last is not None else None}),file=sys.stderr)
except (ValueError,KeyError,OSError,zipfile.BadZipFile) as e:
    print('Trace verification failed: '+str(e),file=sys.stderr);sys.exit(1)
