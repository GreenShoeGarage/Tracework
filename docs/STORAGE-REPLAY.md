# Storage, trace import, replay and recovery · v0.3

The workflow is **open or import → inspect a range → replay → export evidence → retain or retire**. Capture remains owned by the Go server, independently of the browser.

## Open and import evidence

Use **Import trace ZIP** in the navigation. Only native `tracework.trace.v1` ZIPs are supported; the three bundled examples and v0.1/v0.2 exports remain readable. This does not import Zelos `.trz` files or run producer code.

Import validates the complete bundle before installation: SHA-256, counts, schema and field types, stable identities, unique producer observations, increasing original ordinals, and original session association. Unknown event-envelope fields are rejected rather than discarded. Duplicate or unlisted ZIP members, nonregular files, malformed schemas and integrity failures reject the entire import. ZIP members are read in place; paths are never extracted. SHA-256 verifies consistency, not the trustworthiness of a sender.

A successful import creates a separate **read-only project and recording**. Source/schema/field IDs and every original event envelope remain unchanged, including original event ID, session ID, ordinal, source/receive timestamps, nulls, exact integers and gap flags. The new local project/session IDs locate the imported copy. **Manage recording → Import provenance** shows their relationship and the archive hash. Imported data does not join live acquisition. Export project JSON and import that configuration if you need a new writable capture project.

Reimporting the identical ZIP opens its existing imported copy. If that recording is in trash, restore it through Storage. Exporting a copy again preserves the original event session association. A ranged import retains the selected original ordinals; it does not renumber them or claim to contain omitted observations. Partial exports from an active recording retain its original status in provenance and are frozen, read-only imports.

Limits per import: 256 MiB compressed, 1 GiB expanded, 200,000 events, 20,000 chunks, 8 MiB per chunk, 4 MiB manifest, 128 KiB per stored event line. Recording quota and available disk space are checked before installation. Export a smaller range if necessary. Limits bound a single operation, not all concurrent installation traffic. A valid full export can exceed the import limit.

## Select and replay a range

Open a recording and choose **Select range**. First/last ordinal are inclusive; blanks mean unbounded. Use the current cursor for either bound, filter by source, or expand exact receive-time bounds and enter signed int64 nanosecond strings. A selection applies to pages, replay, and ZIP/JSONL/CSV export. **Use whole recording** clears it. Selection is temporary view state and resets when another recording opens.

**Play** advances in original acceptance order using receive-time intervals, at 0.25×, 1×, 4× or 16×. Space plays/pauses when focus is outside a control. Pause, seek and manual page navigation stop playback. Replay fetches the next page automatically. **Step / skip wait** advances once and pauses; it is the explicit way to skip a long original interval. No synthetic observations are inserted.

Replay is an inspection aid, not a real-time acquisition clock. Browser scheduling, rendering and page fetches can add delay; background tabs may throttle. Negative receive-clock steps use zero delay while retaining the original timestamps and acceptance order. Source clocks can be independent; receive order and playback do not establish causality. At high rates intermediate observations can advance within one display frame; all accepted events remain in the recording.

Pages contain at most 2,000 events and at most 16 MiB of raw event lines. The UI holds one recorded page at a time; plots/readouts are scoped to that page. ZIP exports preserve all matching events beyond the visible page. JSONL preserves event lines; CSV is a tabular projection, with values JSON retaining precision. Source and receive-time bounds in ZIP manifests are calculated from the actual exported events. Every export is fully prepared and verified before successful response headers are sent.

## Optimize storage

Each successful capture request still uses chunk fsync, directory fsync and SQLite FULL commit before acknowledgment. Small requests therefore create small files. Stop a recording, open **Manage recording**, and choose **Optimize storage** to aggregate its event lines into approximately 4 MiB chunks.

Optimization preserves original event lines, identities, ordinals, timestamps, values and coverage. Chunk IDs/hashes and physical grouping can change. The new files are durably written before the index transaction commits; old files are removed after commit. A failure before commit leaves the old index/evidence intact. A crash around commit can leave unreferenced files; startup quarantines them, without promoting them to evidence.

This is sealed-recording maintenance, not ingestion group commit or a throughput claim. It can require roughly one extra copy of the trace plus headroom. Quota measures referenced chunks, not temporary duplicate files. Optimization, imports, range reads and exports serialize with the writer for consistency. Long maintenance/export operations can block producers; buffered agents retry/backpressure according to their connector limits.

## Protection, retention and trash

**Protect recording** excludes a recording from retention, trash and permanent deletion. Unprotect it explicitly before retiring it. Active recordings cannot be trashed, optimized or purged.

**Storage & retention** shows installation-wide trace/quota/free-space usage and project-specific policy/trash. The default policy keeps the newest 10 closed recordings. Saving a policy does not delete anything. **Save policy & preview** shows eligible recordings; **Move these recordings to trash** applies that exact reviewed set. The server recalculates the plan under its writer lock and rejects a stale preview if candidates changed. A protected recording still counts among the newest closed recordings. “Newest” means local session insertion order, including imports; age uses preserved end time. Blank/unknown end time is excluded from age-restricted deletion.

Trash is recoverable and still consumes quota/disk. **Restore** returns the unchanged recording. Trash entries remain readable through direct APIs and can be exported; trash is not an access boundary. **Delete permanently…** requires a separate confirmation in the UI and the exact session ID through the API. It removes event payloads/indexes only for an unprotected, stopped, trashed recording. Identity tombstones remain so a retry cannot silently recreate intentionally deleted observations. Metadata is not automatically vacuumed, and tombstones/audit history still consume space. Physical free space can differ from the payload byte count.

Permanent deletion clears that project's volatile live display and advances its eviction floor; other recorded sessions remain intact. If physical cleanup fails after a committed deletion/optimization, the response contains a warning and a restart moves leftover unreferenced chunks into recovery. Quarantined files still consume disk until deliberately handled outside the application.

## Recovery, backup and upgrade

Before an upgrade, stop the server and copy the **entire data directory**, plus agent spools. Native ZIP export is evidence interchange, not a full-store backup. Metadata v1/v2 migrates to v3 with consistent pre-migration SQLite snapshots. Migration adds original-session ordinal and receive-time indexes, import provenance, retention/protection/trash and identity tombstones. Existing chunk files are verified and not rewritten during migration. Actual v0.1 and v0.2 executables generated the tested migration fixtures.

Current metadata is v4; versions 1–3 migrate with pre-migration snapshots. Old binaries cannot open metadata v4. See WORKSPACES.md for the added view table. Rollback requires the complete old backup, not only an automatic SQLite snapshot. After optimization, the old index snapshot can reference removed chunk files; do not combine it with a later chunks directory.

The writable `transfers/` directory stages imports and verified exports. Only application-named `import-*` and `export-*` staging directories are removed on startup. They are not evidence. This keeps transfers within the data volume for read-only-root containers. Do not place personal files in this application-owned directory.

Recovery inventory lists quarantined files without executing, promoting or deleting them. On suspected corruption, stop the server, preserve the whole directory, and restore a known complete backup to a separate path. A corrupt or missing committed chunk blocks startup. Do not remove indexes to force startup. No undelete or file-repair guarantee is made.

Qualification includes forced chunk-write ENOSPC, real SQLite `SQLITE_FULL` using a bounded page quota, injected failures before commit, SIGKILL/restart and stopped-directory backup/restore. These tests use disposable stores; they do not fill the host filesystem or simulate physical disk power loss. See TEST-REPORT.md for platform and workload limits.
