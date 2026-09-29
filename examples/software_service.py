#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-3.0-only
"""Observe a URL you control, using a generic source. Standard library only."""
import argparse
import time
import urllib.request
from tracework import Client

p = argparse.ArgumentParser(description=__doc__)
p.add_argument('url')
p.add_argument('--count', type=int, default=20)
a = p.parse_args()
c = Client()
project = c.create_project('Software service observations', a.url)
source = c.define_source(project['id'], 'http-probe', [
    {'name': 'latency', 'type': 'float64', 'unit': 'ms'},
    {'name': 'available', 'type': 'bool', 'unit': ''}], clock_domain='probe-monotonic-plus-wall')
s = c.source(project['id'], source['id'], source['events'][0]['id'], source['clock_domain'])
session = c.start_recording(project['id'], 'HTTP observation run')
print('Project:', project['id'], 'Recording:', session['id'], flush=True)
try:
    for i in range(a.count):
        start = time.perf_counter_ns()
        try:
            with urllib.request.urlopen(a.url, timeout=5) as response:
                response.read(1024)
            ok, message = True, 'Response received'
        except Exception as error:
            ok, message = False, str(error)
        elapsed = (time.perf_counter_ns() - start) / 1e6
        s.emit({'latency': elapsed, 'available': ok}, message=message,
               level='info' if ok else 'warn')
        time.sleep(1)
finally:
    c.stop_recording(session['id'])
