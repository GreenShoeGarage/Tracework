# Architecture · TRACEWORK v1.0.0

Decision date: 2026-09-28. Original code: GNU GPL v3 only.

## Three cooperating parts

- **Workbench:** TypeScript compiled once to bundled browser JavaScript. Native HTML/SVG/CSS; zero remote fonts, CDN dependencies, telemetry, or required internet calls. It can be closed without affecting acquisition.
- **Server:** Go, one process, embedded browser assets/documentation; SQLite metadata, deduplication index, session snapshots, and chunk indexes. Receives typed envelopes over HTTP JSON v1 and optional gRPC; pushes bounded WebSocket/gRPC live frames. Owns capture, checks integrity, serves original observations and exports.
- **Agent:** Go subcommand of the same binary, running beside the producer. Reads generic field-value JSONL, supplies stream identity and timing metadata, fsyncs observations to a local spool, drains in order, and retries identical identities after disconnection. Pipe any acquisition program into it. It includes serial port JSONL and MQTT 3.1.1 subscribers; downloaded config selects source/revision/connector. Required-recording policy protects buffered evidence.

Native Python, Go and Rust clients speak the same HTTP contract. Optional action providers independently validate typed commands and journal dispatch. Explicitly installed trusted process extensions use resource limits; browser renderers use an opaque sandbox and restricted selection messages. Imported projects never install or execute code. Notebooks remain deferred.

## Consequential decisions

| Decision | Evidence / reason | Revisit |
|---|---|---|
| Retain Go server/agent and TypeScript browser | Both compiled successfully; a pure Go SQLite implementation permits static Linux builds and cross-compilation | v0.3 measurements completed; see codec/storage reports |
| HTTP JSON plus WebSocket/gRPC in v0.2 | Shared native validation and persistence; exact JSON decimal strings inside Protobuf bytes | Automatic historical stream backfill remains open; recorded ranges available |
| Running generated gRPC service | Bidirectional Publish and streaming Subscribe; authentication, local TLS and agent fixtures pass | Wider network/load qualification |
| Indexed immutable JSONL per accepted recording batch | Human-readable original envelopes; can hash/verify each batch; reference values survive process death and restart | v0.3 sealed aggregation/indexes delivered; live group commit open |
| SQLite FULL commits plus file and directory fsync | A receipt must identify a real persistence boundary; SQLite holds commit disposition and chunk hashes | ENOSPC/SQLite-full fixtures delivered; physical power loss open |
| Crash/restart seals active sessions as interrupted | A restarted process cannot know uninterrupted acquisition coverage; require explicit new capture | Continued-session semantics remain open |
| Receive-time plot axis | Independently clocked producers cannot be assumed synchronized; source timestamps remain exact in the ledger/inspector | Separate investigation view supports explicit receive/source time and reversible alignment |
| Immutable schema revisions in v0.2 | Append versions, preserve prior definitions, give changed field metadata a new identity | Broader types and tooling later |
| Loopback default, optional shared token | Optional installation-wide accounts enforce viewer/operator/admin roles; master token remains recovery access | Role/revocation fixtures pass; production TLS/LAN deployment remains unqualified |
| No automatic trace deletion | Data loss is a worse default than an explicit quota rejection | v0.3 reviewed policies, recoverable trash and protection delivered |

## Arrow IPC evaluation

Apache Arrow's columnar format provides typed contiguous arrays, null validity buffers, dictionary encoding, and IPC stream/file variants. These fit high-volume homogeneous event batches and cross-language analysis. See [Arrow columnar specification](https://arrow.apache.org/docs/format/Columnar.html), reviewed 2026-09-28.

For v0.1 the native envelopes are heterogeneous and small. Arrow would also need an explicit mapping for source/receive timestamps, boot/sequence identities, provenance, gap boundaries, changing schemas, and schema-to-batch association. It does not supply that application contract by itself. JSONL keeps the first acceptance path independently inspectable and avoids making Arrow an unmeasured throughput claim.

**v0.3 decision:** the optional Python Arrow prototype now passes exact semantic round trips and has measured size/encode/decode results. Retain JSONL production storage; see [ARROW-DECISION.md](ARROW-DECISION.md). No runtime Arrow codec is implemented.

## Limits and failure boundaries

The server serializes ingest and exports. Each request is at most 4 MiB / 1,000 events; the live buffer is bounded. Each successful recording request becomes an immutable chunk, so per-event requests have significant file/fsync overhead. Export locks ingestion while constructing a consistent snapshot; clients may block/retry. This is a correctness-first bench release, not a high-throughput industrial recorder.

SQLite identities/indexes include live-only events to make retries unambiguous, but live payloads are not persisted. Metadata grows until data is retired; the trace quota does not cap SQLite size. Disk-space feedback reports filesystem free bytes. Chunk ENOSPC injection, real SQLite FULL under a bounded page quota, pre-commit failure, quarantine and backup/restart are tested. Physical disk power loss and actual host-filesystem exhaustion remain unqualified.

Only a stopped-server filesystem copy is a supported full backup. A trace ZIP is an evidence export, not a full project-store backup. v0.3 supports bounded validated native ZIP import into isolated read-only evidence. It does not merge imported data into live captures.

Metadata v1→v2 migration adds persistent agent health without rewriting recorded chunks. A pre-migration SQLite snapshot is created; full stopped-server backups remain the rollback boundary. See CONNECTORS.md for capture intent, session pinning, spool limits and reconnect behavior.

## v0.3 storage evolution

Metadata v3 adds per-session original ordinal and receive-time indexes, provenance, protection, reviewed retention, recoverable trash, purge tombstones and maintenance history. Sealed recordings can aggregate into approximately 4 MiB chunks with atomic index replacement after durable new-file writes. Capture retains per-request durability. Ranged retrieval/export and browser receive-time replay provide deliberate historical inspection; automatic historical stream backfill remains open. Transfer staging stays inside the data volume. See [STORAGE-REPLAY.md](STORAGE-REPLAY.md) and [third-batch review](V03-WORKFLOW-REVIEW.md).

## v0.4 view architecture

The browser separates pure workspace/binding models, panel renderers and workspace interaction/persistence from acquisition/replay controls. Six native HTML/SVG panel types share the existing event cursor. The server stores bounded project views in a separate metadata table with revision preconditions; concurrent writes cannot silently replace newer saves. Browser drafts and explicit conflict recovery protect unsaved view edits. Portable references resolve unique named paths with schema contract agreement, while exact-ID references preserve identity. No event chunk is rewritten by a layout operation. UI workspaces do not imply multi-user roles or collaborative synchronization.

## v1.0 state and boundaries

Metadata version 9 stores workspace revisions, investigation citations/baselines, action catalogs/leases/runs, check rules/results, accounts/sessions/shares/audit and pinned decoder definitions. Original chunks retain the native v1 contract. Restore validates a staged copy, revokes browser/share credentials and applies capture/action recovery before publishing a new data directory. Complete histories remain stored; API lists have explicit count/byte limits. One writer lock bounds consistency but can delay acquisition during large exports, backups or checks.
