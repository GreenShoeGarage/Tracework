#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-3.0-only
import json,statistics,time
from integration import Workflows
w=Workflows();w.setUp()
try:
    s=w.record();count=1000;t=time.perf_counter()
    for i in range(count):w.ingest(w.event(i))
    ingest=time.perf_counter()-t;w.client.stop_recording(s['id']);sid=s['id']
    def measure():
        full=[];selected=[]
        for _ in range(5):
            t=time.perf_counter();es=list(w.client.events(sid));full.append(time.perf_counter()-t);assert len(es)==count
            t=time.perf_counter();subset=w.get('/sessions/'+sid+'/events?from=451&to=550')['events'];selected.append(time.perf_counter()-t);assert subset==es[450:550]
        return {'full_read_median_s':statistics.median(full),'range_100_read_median_s':statistics.median(selected),'stats':w.get('/sessions/'+sid+'/range')}
    before=measure();t=time.perf_counter();result=w.client.request('/sessions/'+sid+'/compact',{});duration=time.perf_counter()-t;after=measure()
    print(json.dumps({'events':count,'events_per_request':1,'repetitions':5,'initial_ingest_seconds':ingest,'before':before,'after':after,'optimize_seconds':duration,'optimize_result':result,'interpretation':'Sealed-recording read/file-count fixture. Optimization does not reduce capture acknowledgment fsyncs or establish ingestion throughput improvement.'},indent=2))
finally:w.tearDown()
