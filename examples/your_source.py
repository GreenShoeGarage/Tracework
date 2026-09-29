#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-3.0-only
"""A runnable custom-project tutorial. Data is explicitly synthetic."""
import argparse
import math
import time
from tracework import Client

p = argparse.ArgumentParser(description=__doc__)
p.add_argument('--count', type=int, default=600)
p.add_argument('--interval', type=float, default=0.1)
a = p.parse_args()
c = Client()
project = c.request('/projects', {'name': 'Python tutorial · synthetic',
    'description': 'Replace the mathematical waveform with your own observations.',
    'sources': [], 'sample': True})
source = c.define_source(project['id'], 'custom-producer', [
    {'name': 'displacement', 'type': 'float64', 'unit': 'mm'},
    {'name': 'counter', 'type': 'uint64', 'unit': 'samples'},
    {'name': 'enabled', 'type': 'bool', 'unit': ''}], clock_domain='tutorial-clock', synthetic=True)
# Marking the project synthetic is preserved in exports; source provenance may also
# be set via the UI example fixtures. This source is a documented synthetic tutorial.
session = c.start_recording(project['id'], 'Python-owned acquisition, server-owned recording')
s = c.source(project['id'], source['id'], source['events'][0]['id'], source['clock_domain'])
print('Project:', project['id'], 'Recording:', session['id'], flush=True)
print('Open the project in the workbench. Closing the browser does not stop this producer.', flush=True)
try:
    for i in range(a.count):
        s.emit({'displacement': 8 + math.sin(i / 12) * 3,
                'counter': str(9007199254740993 + i), 'enabled': True},
               message='Synthetic checkpoint' if i % 20 == 0 else '')
        time.sleep(a.interval)
finally:
    c.stop_recording(session['id'])
