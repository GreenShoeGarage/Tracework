"""TRACEWORK native HTTP v1 client; standard library only. GPL-3.0-only."""
import json
import os
import time
import urllib.request
import uuid


class Client:
    def __init__(self, url=None, token=None, timeout=30):
        self.url = (url or os.getenv('TRACEWORK_URL', 'http://127.0.0.1:8080')).rstrip('/')
        self.token = token if token is not None else os.getenv('TRACEWORK_TOKEN', '')
        self.timeout = timeout

    def request(self, path, data=None, method=None):
        headers = {'Content-Type': 'application/json'}
        if self.token:
            headers['Authorization'] = 'Bearer ' + self.token
        body = None if data is None else json.dumps(data, allow_nan=False).encode()
        req = urllib.request.Request(self.url + '/api/v1' + path, data=body,
                                     headers=headers, method=method or ('GET' if data is None else 'POST'))
        with urllib.request.urlopen(req, timeout=self.timeout) as response:
            return json.load(response)

    def create_project(self, name, description=''):
        return self.request('/projects', {'name': name, 'description': description, 'sources': []})

    def define_source(self, project_id, name, fields, event='measurement',
                      clock_domain='producer-clock', stale_ms=3000, synthetic=False):
        result = self.request('/projects/' + project_id + '/sources', {
            'name': name, 'clock_domain': clock_domain, 'stale_ms': stale_ms, 'synthetic': synthetic,
            'events': [{'name': event, 'version': 1, 'fields': fields}]})
        return result['sources'][-1]

    def source(self, project_id, source_id, schema_id, clock_domain, boot_id=None, schema_version=1, require_recording=False, session_id=""):
        return Source(self, project_id, source_id, schema_id, clock_domain, boot_id, schema_version, require_recording, session_id)

    def revise_schema(self, project_id, source_id, schema_id, expected_version, name, fields):
        """Unchanged fields may retain IDs; changed metadata requires a new/omitted ID."""
        return self.request('/projects/' + project_id + '/sources/' + source_id +
                            '/schemas/' + schema_id + '/revisions', {
            'expected_version': expected_version, 'name': name, 'fields': fields})

    def start_recording(self, project_id, name):
        return self.request('/projects/' + project_id + '/sessions', {'name': name})

    def stop_recording(self, session_id):
        return self.request('/sessions/' + session_id + '/stop', {})

    def events(self, session_id):
        after = 0
        while True:
            page = self.request('/sessions/' + session_id + '/events?after=' + str(after))
            yield from page['events']
            if not page['more']:
                return
            after = page['next']


class Source:
    """One instance per producer stream. Not thread-safe; serialize emit calls.

    A failed send retains its envelope. Call retry() before sending another event.
    This client does not spool to disk. Use the Go agent for durable buffering.
    """
    def __init__(self, client, project_id, source_id, schema_id, clock_domain, boot_id=None, schema_version=1, require_recording=False, session_id=""):
        self.client, self.project_id = client, project_id
        self.schema_version, self.require_recording, self.session_id = schema_version, require_recording, session_id
        self.source_id, self.schema_id, self.clock_domain = source_id, schema_id, clock_domain
        self.boot_id, self.sequence, self.pending = boot_id or uuid.uuid4().hex, 0, None

    def emit(self, values, timestamp_ns=None, sync_quality='unsynchronized',
             uncertainty_ns=None, gap_before=False, message='', level='info'):
        if self.pending is not None:
            raise RuntimeError('A send is unresolved; retry() the same observation before emitting another.')
        self.pending = {'source_id': self.source_id, 'schema_id': self.schema_id,
                        'schema_version': self.schema_version, 'boot_id': self.boot_id, 'sequence': str(self.sequence),
                        'timestamp_ns': str(time.time_ns() if timestamp_ns is None else timestamp_ns),
                        'clock_domain': self.clock_domain, 'sync_quality': sync_quality,
                        'uncertainty_ns': None if uncertainty_ns is None else str(uncertainty_ns),
                        'gap_before': gap_before, 'values': values,
                        'message': message, 'level': level}
        return self.retry()

    def retry(self):
        if self.pending is None:
            raise RuntimeError('No unresolved event')
        batch = {'events': [self.pending]}
        if self.require_recording:
            batch['require_recording'] = True
        if self.session_id:
            batch['session_id'] = self.session_id
        result = self.client.request('/projects/' + self.project_id + '/events', batch)
        self.pending = None
        self.sequence += 1
        return result
