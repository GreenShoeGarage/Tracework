#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-3.0-only
"""Optional homogeneous native-event codec experiment. Not a TRACEWORK runtime codec.
Install pyarrow==25.0.0 in an isolated environment; run with no other workload.
"""
import gzip,json,platform,statistics,time
import pyarrow as pa
import pyarrow.ipc as ipc

names=['id','source_id','schema_id','schema_version','boot_id','sequence','timestamp_ns','received_ns','clock_domain','sync_quality','uncertainty_ns','values','level','message','gap_before','flags','ordinal','session_id']
values=pa.struct([('reading',pa.float64()),('count',pa.uint64()),('offset',pa.int64()),('valid',pa.bool_()),('state',pa.string())])
schema=pa.schema([(n, t) for n,t in zip(names,[pa.string(),pa.string(),pa.string(),pa.int32(),pa.string(),pa.uint64(),pa.int64(),pa.int64(),pa.string(),pa.string(),pa.uint64(),values,pa.string(),pa.string(),pa.bool_(),pa.list_(pa.string()),pa.int64(),pa.string()])],metadata={b'contract':b'tracework homogeneous prototype v1; integer strings restored on decode; original JSON lexical form not retained',b'field_units':b'{"reading":"V","count":"samples","offset":"ns"}',b'schema_id':b'measurement',b'schema_version':b'1'})
def fixture(n):
    return [dict(zip(names,[f'event_{i}','sensor_a','measurement',1,'boot_a',str(i),str(1790612345678901234+i),str(1790612345679901234+i),'independent-clock','unsynchronized',None,{'reading':None if i%101==0 else i/7,'count':None if i%103==0 else str(18446744073709551615-i),'offset':str(-9223372036854775808+i),'valid':i%2==0,'state':'ready' if i%2 else 'loaded'},'info','Observation',i%100==0,['producer_reported_gap'] if i%100==0 else [],i+1,'recording_a'])) for i in range(n)]
def arrow_encode(events):
    rows=[]
    for e in events:
        row={**e,'values':dict(e['values'])}
        for k in ['sequence','timestamp_ns','received_ns','uncertainty_ns']:
            if row[k] is not None:row[k]=int(row[k])
        for k in ['count','offset']:
            if row['values'][k] is not None:row['values'][k]=int(row['values'][k])
        rows.append(row)
    table=pa.Table.from_pylist(rows,schema=schema);sink=pa.BufferOutputStream()
    with ipc.new_file(sink,schema) as writer:writer.write_table(table)
    return sink.getvalue().to_pybytes()
def arrow_decode(blob):
    with ipc.open_file(pa.BufferReader(blob)) as reader: rows=reader.read_all().to_pylist()
    for e in rows:
        for k in ['sequence','timestamp_ns','received_ns','uncertainty_ns']:
            if e[k] is not None:e[k]=str(e[k])
        for k in ['count','offset']:
            if e['values'][k] is not None:e['values'][k]=str(e['values'][k])
    return rows
encode_json=lambda es: b''.join(json.dumps(e,separators=(',',':'),ensure_ascii=False).encode()+b'\n' for e in es)
decode_json=lambda b:[json.loads(line) for line in b.splitlines()]
codecs={'jsonl':(encode_json,decode_json),'jsonl_gzip_6':(lambda es:gzip.compress(encode_json(es),compresslevel=6,mtime=0),lambda b:decode_json(gzip.decompress(b))),'arrow_ipc_uncompressed':(arrow_encode,arrow_decode)}
results=[]
# Warm import/allocator paths outside timed measurements.
arrow_decode(arrow_encode(fixture(2)))
for count in [10000,100000]:
    events=fixture(count)
    for name,(encode,decode) in codecs.items():
        writes=[];reads=[]
        for _ in range(3):
            t=time.perf_counter();blob=encode(events);writes.append(time.perf_counter()-t)
            t=time.perf_counter();roundtrip=decode(blob);reads.append(time.perf_counter()-t)
            assert roundtrip==events, name+' changed original event content'
        results.append({'events':count,'codec':name,'bytes':len(blob),'encode_median_s':statistics.median(writes),'decode_median_s':statistics.median(reads),'semantic_exact_roundtrip':True})
print(json.dumps({'pyarrow':pa.__version__,'python':platform.python_version(),'platform':platform.platform(),'repetitions':3,'fixture':'one schema, one source, five value fields, complete envelope, nulls/gaps, uint64 max and int64 min, nanosecond timestamps','timing_scope':'Python object conversion and encoding/decoding; in-memory buffers, no fsync, SQLite, server, or network','results':results,'decision':'Keep immutable JSONL as production codec. Arrow prototype is optional analysis, not a runtime format or acquisition performance claim.'},indent=2))
