#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-3.0-only
"""One disclosed local fixture workload; not a production capacity claim."""
import json,os
from pathlib import Path
import platform
import statistics
import time
from integration import Workflows

w=Workflows();w.setUp()
try:
    s=w.record();durations=[];size=500;count=10000
    t=time.perf_counter()
    for begin in range(0,count,size):
        batch=[w.event(i) for i in range(begin,begin+size)]
        t0=time.perf_counter();w.ingest(*batch);durations.append(time.perf_counter()-t0)
    elapsed=time.perf_counter()-t
    t0=time.perf_counter();events=list(w.client.events(s['id']));read=time.perf_counter()-t0
    assert len(events)==count
    status=w.get('/status')
    rss='Unavailable: process PID is outside exposed /proc namespace'
    proc=Path('/proc')/str(w.process.pid)/'status'
    if proc.exists():rss=next((line for line in proc.read_text().splitlines() if line.startswith('VmHWM:')),'Unavailable')
    cpu=next((line.split(':',1)[1].strip() for line in Path('/proc/cpuinfo').read_text().splitlines() if line.startswith('model name')),'unknown')
    memory=next((line for line in Path('/proc/meminfo').read_text().splitlines() if line.startswith('MemTotal:')),'unknown')
    w.client.stop_recording(s['id']);sc=w.source['events'][0]
    trace={'session_id':s['id'],'alias':'A','source_id':w.source['id'],'schema_id':sc['id'],'schema_version':1,'field_id':sc['fields'][1]['id'],'range':{'from':1,'to':count}}
    t0=time.perf_counter();comparison=w.client.request('/compare',{'alignment':'relative','time_basis':'source','traces':[trace,dict(trace,alias='B')]});compare_seconds=time.perf_counter()-t0
    assert all(len(t['points'])==count for t in comparison['traces'])
    rule={'id':'fixture','name':'Exact integer range','trace':trace,'time_basis':'source','kind':'all_between','min':'18446744073709551615','max':'18446744073709551615','min_samples':count,'min_span_ns':str(count-1),'max_gap_ns':'1','hold_ns':'1','gap_policy':'insufficient','null_policy':'insufficient'}
    t0=time.perf_counter();check=w.client.request('/projects/'+w.project['id']+'/check-runs',{'session_id':s['id'],'document':{'format':'tracework.checks.v1','rules':[rule]}});check_seconds=time.perf_counter()-t0
    assert check['status']=='pass',check
    result={'platform':platform.platform(),'processor':cpu,'logical_cpus':os.cpu_count(),
        'memory_reported_by_proc':memory,'storage':'ephemeral container filesystem; durability tests use OS fsync, physical power-loss not tested',
        'workload':{'events':count,'batch_size':size,'sources':1,'fields':4,'clients':1,'concurrent_browser':False},
        'ingest_seconds':elapsed,'events_per_second':count/elapsed,
        'batch_ack_median_ms':statistics.median(durations)*1000,'batch_ack_p95_ms':sorted(durations)[18]*1000,
        'compare_two_10000_point_traces_seconds':compare_seconds,'check_10000_observations_seconds':check_seconds,'full_read_seconds':read,'trace_bytes':status['trace_bytes'],'server_rss':rss,
        'interpretation':'Measured single local fixture only. Not guaranteed throughput, latency, or hardware acquisition capacity.'}
    print(json.dumps(result,indent=2))
finally:w.tearDown()
