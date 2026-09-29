# TRACEWORK native data contract v1

This describes the running HTTP interface and the JSON payload contract inside native gRPC Protobuf messages. WebSocket/gRPC subscriptions send exact event envelopes in live frames.

## Identity and schemas

Project, source, event-schema, field and stored-event IDs are opaque stable strings. New server identities use 128 random bits in hexadecimal. User-supplied source/schema/field IDs must match `[a-zA-Z0-9_-]{1,80}` and have non-conflicting identities throughout a project. Imported project configurations receive a new project ID while retaining their source/schema/field IDs. Sources contain immutable schema revisions, numbered consecutively from 1. A schema ID may repeat only within its source, with a distinct revision. Unchanged field definitions can retain their field ID across revisions; changing a name, type, unit or enum requires a new field ID. The revision endpoint appends a version with an optimistic expected-version check and updates any active capture snapshot atomically. Old revisions remain valid and old recordings retain their original schemas. Limits are 64 sources/project, 32 total schema revisions/source and 64 fields/revision.

Six field types are available: `float64` (finite JSON number), `int64` and `uint64` (canonical decimal **strings** within their respective ranges), `bool`, `string`, and `enum` (one of declared strings). Any field may be null. All declared fields must be present, and undeclared fields are rejected. Units are descriptive strings; no implicit unit conversions occur. There is no inferred schema, nested struct, binary, int8/16/32, or float32 support yet.

## Ingestion

`POST /api/v1/projects/{project_id}/events` with `{"events":[...]}`. Maximum 1,000 events, 4 MiB encoded request; invalid batches are rejected atomically. An event looks like:

```json
{
  "source_id": "source_A", "schema_id": "measurement_A", "schema_version": 1,
  "boot_id": "unique_producer_lifetime", "sequence": "0",
  "timestamp_ns": "1790612345678901234", "clock_domain": "clock_A",
  "sync_quality": "unsynchronized", "uncertainty_ns": null,
  "values": {"temperature": 23.125, "counter": "9007199254740993"},
  "gap_before": false, "level": "info", "message": "optional structured message"
}
```

The server adds `id`, `received_ns`, `session_id` (if recording), acceptance `ordinal`, and diagnostic `flags`. Producer-supplied values for those server fields are ignored. A timestamp is a signed int64 decimal string in nanoseconds within the declared clock domain; it need not denote UTC if the domain is not a wall clock. Receive timestamps are server Unix epoch nanoseconds. No timestamp is coerced through JavaScript Number in storage or transport. Browser plotting converts only relative offsets and plotted values to approximate numbers; exact strings remain in inspection/export.

`sequence` is a uint64 decimal string scoped to project/source/boot. Increment it across **all** schemas of one source. Assign a new boot ID on producer restart or clock reset. The simple Python Source object owns one sequence counter and is not thread-safe: serialize emissions; for multiple event schemas use the native API with one coordinated counter or separate source IDs.

`sync_quality`: `unknown`, `unsynchronized`, or `synchronized`. Synchronized observations require a non-null uint64 uncertainty in nanoseconds. Uncertainty is producer-declared, not independently calibrated. Nanosecond representation does not establish nanosecond accuracy. Across independent domains, receive order is not evidence of causal order.

## Gaps, duplicates, and ordering

Records preserve acceptance order. The server identifies sequence skips, sequence regression, source clock regression, and new boot IDs. Such events have `gap_before: true` and explanatory flags. A producer can explicitly report a gap. Null values and stale intervals break rendered lines; no interpolation bridges known gaps. Out-of-order observations are preserved, not reordered or discarded. Sequence flags compare to the immediately preceding accepted observation; a late record can therefore be followed by another conservative discontinuity marker.

The key `(project,source,boot,sequence)` deduplicates retries, including across restarts. An identical retry is accepted as a duplicate without a second stored event. A different payload or timestamp at that key is a conflict and rejects the whole request. A duplicate's original live/recorded disposition never changes; retrying a live-only event after Record starts does not backfill it.

## Receipt guarantees

`accepted` counts new validated observations; `duplicates` counts already accepted identities; `persisted` counts new observations written to an active recording; `session_id` identifies that recording.

- `persisted > 0`: complete chunk written and fsynced, renamed, directory fsynced, and SQLite FULL-synchronous transaction committed before the receipt.
- Live only: the identity/index is committed, but the observation payload is volatile. The receipt explicitly says **payload not durable**. Press Record before acquisition that must be retained.
- Duplicate-only: no new persistence occurs. Original disposition is unchanged. Query the recording to verify its original evidence.
- Error/no receipt: success is not established. Retry the **same envelope**; never invent a new sequence just because a response timed out. No hardware command retry semantics exist in v0.2.

These guarantees rely on the operating system, filesystem, storage device and its cache honoring fsync. They are not a claim of hardware power-loss qualification.

## Recording intent

An ingestion batch may add `require_recording: true` and/or `session_id: "specific_id"`. New events are accepted only into an active matching recording; otherwise HTTP returns 409 or gRPC FAILED_PRECONDITION without recording them. The check is atomic with ingest. The default native API remains live-capable for compatibility; agents default to requiring recording. Durable duplicate retries are acknowledged even after a capture ends. Required-recording retries of originally live-only events are rejected; they cannot silently become retained evidence.

CSV exports include `schema_id` and `schema_version`; retain project JSON or the complete trace ZIP for schema/units/provenance. JSONL and ZIP retain the full native envelopes without spreadsheet type inference.

## Native trace ZIP

`manifest.json` contains format `tracework.trace.v1`, application version, session ID/status/time bounds, project/schema snapshot, source provenance/synthetic flags, chunk list, hashes, and interpretation notes. Each `chunks/{id}.jsonl` contains complete immutable original event envelopes, one JSON value per line. Chunk first/last source timestamps follow acceptance order, **not necessarily numeric minima/maxima**. The original `timestamp_ns`, `received_ns`, values, identity, flags and sequence are retained.

SHA-256 is checked on startup, recorded reads and before export. A missing/corrupt committed chunk prevents startup; restore a complete backup. Uncommitted files are moved to `recovery/`, never silently promoted into a recording. Restart marks previously active sessions `interrupted` to expose the acquisition boundary. Known gaps are recorded in event flags and session status; complete physical acquisition coverage cannot be inferred from accepted observations alone.

Configuration import never executes code. The standalone verifier parses JSON/ZIP data only. Zelos `.trz`, SDK and wire formats are not supported.

## v0.3 storage and imported evidence

Original event ordinals are now indexed separately from local SQLite record identities. Existing v1/v2 stores migrate without changing chunk bytes. This allows an isolated imported recording to preserve original ordinals/event IDs without colliding with a local recording. Imported event.session_id identifies original evidence, while the new local session ID is used for retrieval. `origin` and the trace manifest's `event_session_id` make that distinction explicit.

Inclusive ordinal, receive-time and source filters select original events. ZIP `session.count` reflects selected coverage, while `original_session_count` and `selection` describe the extraction. No events are inferred between boundaries. Timestamps/int64/uint64 values remain decimal strings in native JSON. CSV is a projection; use native ZIP/JSONL for original envelopes.

Trash is recoverable, uses quota, and is not an authorization boundary. Purge requires explicit confirmation and keeps producer identity tombstones. Optimization changes physical chunks only. Current format/migration/import limits are in [STORAGE-REPLAY.md](STORAGE-REPLAY.md). Arrow IPC is an optional conformance/benchmark prototype, not a native runtime chunk encoding.

## v0.4 view configuration

Metadata version 4 adds a project-keyed `workspaces` table containing a revision and bounded JSON document. `tracework.workspace.v1` is a view format, independent of the native observation/trace version. It never changes captured evidence, source definitions or export selections. Exact-ID and named-path references carry revision/type/unit/enum metadata and can remain explicitly unresolved. Native trace ZIP and project JSON do not embed workspaces; use separate workspace export, or back up the complete stopped store. Metadata migrations preserve original chunk bytes; older executables require a complete pre-upgrade backup for rollback.

## Decoder identity and v1.0 metadata

Optional source `decoder` is `{kind,sha256,definition}`. Kind is `dbc-classical-v1` or `modbus-registers-v1`; definition is nonempty, at most 64 KiB, and its UTF-8 SHA-256 must match. Every schema on that source requires a string field named `decoder_sha256`. Every ingested event must carry the matching value. Raw CAN bytes/Modbus registers are ordinary typed fields retained with the derived signals. Recorded source snapshots preserve the interpretation contract.

Metadata version 9 prevents older binaries silently dropping decoder identity. Investigation and check results retain hash-verified original envelopes. Actions are separately persisted operational history, not inferred telemetry. Account/share tokens never belong in observation envelopes or exported project definitions.
