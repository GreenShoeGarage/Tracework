# Codec evaluation · v0.3

**Keep JSONL as the production recording codec.** The optional Arrow IPC prototype is included in `benchmarks/arrow_codec.py`; it is not selected by a server flag or accepted as a trace chunk. No new runtime dependency was added.

The prototype uses PyArrow 25.0.0 and one homogeneous native schema. It carries the complete event envelope: source/schema/version, clock domain and synchronization, original timestamps, boot/sequence, event/session identity, ordinal, uncertainty, null validity, flags, log fields and typed values. uint64/int64 extremes are typed numeric columns and reconstruct decimal strings exactly. Field units and mapping information are schema metadata. All measured round trips compare the reconstructed full event objects with the originals, including nulls and gap flags. Original JSON lexical spacing is not an Arrow guarantee.

Python 3.12.14 on the same Linux host as the storage benchmark; medians of three runs. Timings include conversion between Python event objects and codec buffers. They exclude filesystem, fsync, SQLite, HTTP and Go implementation costs. This is a highly repetitive single-source fixture; it is not representative of every trace.

| 100,000 events | Size | Encode | Decode to native objects |
|---|---:|---:|---:|
| JSONL | 53,133,797 B | 0.535 s | 1.131 s |
| JSONL + gzip level 6 | 2,411,851 B | 0.821 s | 1.128 s |
| Arrow IPC, uncompressed | 21,131,210 B | 0.654 s | 1.416 s |

Arrow used about 40% of uncompressed JSONL space here, while decoding full Python envelopes was slower. At 10,000 events its measured encoding was faster; results are size/workload dependent. gzip is shown as a storage comparator, not a runtime chunk codec. No dictionary encoding, compressed Arrow, selective column scan or heterogeneous schema dispatch was benchmarked. ZIP exports already compress their members.

A production Arrow proposal still needs a versioned chunk mapping, unknown-field/presence rules, heterogeneous schema partitions, Go reader/writer conformance, indexed selection behavior, migration/recovery and realistic workloads. The current results justify retaining independently inspectable JSONL while using chunk aggregation for the demonstrated small-file problem. Revisit a production columnar codec only with evidence that analytical reads or acquisition workloads need it.

Reproduce in an isolated environment with `pip install pyarrow==25.0.0`, then `python3 benchmarks/arrow_codec.py`. This optional experiment requires that dependency in advance on offline development hosts. Machine results include both 10,000 and 100,000 events in `ARROW-BENCHMARK.json`.

Format references reviewed for the experiment: [Arrow columnar format](https://arrow.apache.org/docs/format/Columnar.html) and [PyArrow IPC documentation](https://arrow.apache.org/docs/python/ipc.html). The benchmark, not those documents, supplies the measurements above.
