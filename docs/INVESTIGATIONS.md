# Investigate, compare and report

From a project or recording, choose **Investigate**. Write the question, compare explicit signal aliases, cite observations and record assumptions or findings. Investigation edits autosave separately from recording evidence. A visible save indicator and same-tab recovery copy protect work; conflicting server revisions fail instead of overwriting another editor. Export a local copy before reloading saved state after a conflict.

Comparison accepts one to four stopped recordings, including imported evidence. Choose each recording and field explicitly; identical names never silently merge identities. Absolute alignment keeps original selected timestamps. Relative alignment subtracts the first matching sample in the selected range. Event alignment subtracts a selected original observation's timestamp, which must belong to that source/schema revision. Choose server receive time or original source time. These transforms do not synchronize clocks or establish causality. Each alias retains the recording, source, schema revision and field identities.

The overlay groups numeric signals only when units and numeric representation agree; integer and float lanes remain separate. Integer graphs subtract an exact baseline before rendering. Raw values and original nanosecond timestamps remain exact in the observation tables and JSON export. Nulls, gaps, restarts, stale intervals and clock regressions break paths. No resampling or interpolation is applied to the evidence. Tables show at most 500 points per alias; comparison JSON contains every selected point. Each comparison is bounded to 10,000 events per trace and 32 MiB combined event payload; use ordinal ranges for larger captures.

Two-observation measurement computes B minus A using exact integer timestamps and rational arithmetic for numeric values. Units must match; no conversion is inferred. Rational results preserve differences that decimal display cannot represent exactly. Measurement endpoints are explicit original ordinals, and may be entered beyond the displayed table. Endpoint differences do not prove continuous coverage. Retain the measurement to add its calculation and both verified endpoint citations to the investigation.

Four registers keep interpretation distinct: annotations, assumptions, evidence and findings. Findings carry severity and confidence. Evidence requires a recorded observation; the server verifies the original bytes, records SHA-256 and retains an original envelope snapshot. Imported evidence changes local session identities, so references use the local recording ID and original ordinal. Snapshot JSON cannot be edited independently of its source. Notes and snapshots are separate from immutable recording chunks.

A baseline freezes the saved investigation revision, including evidence snapshots and comparison configuration. Baselines are immutable and persist across restart. Open a baseline to see added, changed and removed notes and changes to the question/configuration. The current UI supports up to 200 notes, 2 MiB per investigation and 100 baselines per project. Frozen snapshots are not complete recording backups. Deleting original recordings can prevent later recomputation or saving of referenced investigations; retain protected recordings for long-lived evidence.

Export/import investigation JSON preserves the original reference requirements and is validated before replacement. The standalone HTML report is designed for printing to PDF and includes question, registers, severity/confidence, cited envelopes, hashes, comparison configuration and interpretation limits. Comparison JSON is a separate complete point export. Original trace ZIP remains the complete recording interchange.

## API

- GET `/api/v1/recordings`: newest 1,000 recording descriptors for explicit selection.
- POST `/api/v1/compare`: `{alignment,time_basis,traces:[{session_id,alias,source_id,schema_id,schema_version,field_id,anchor_ordinal,range}]}`.
- POST `/api/v1/measure`: `{a:{session_id,ordinal},b:{session_id,ordinal},field_a,field_b,time_basis}`.
- GET/PUT `/api/v1/projects/{id}/investigation`: revisioned `tracework.investigation.v1` document; PUT includes `expected_revision` and `document`.
- GET/POST `/api/v1/projects/{id}/baselines`: POST `{name,expected_revision}` creates an immutable saved snapshot.
- GET `/api/v1/projects/{id}/investigation/report`: escaped standalone HTML.

Metadata version 5 adds investigations and baselines with a pre-migration SQLite snapshot. Original chunks, layouts and schema definitions are preserved. Tests use the actual original v0.4 executable for migration and verify exact event/chunk preservation.
