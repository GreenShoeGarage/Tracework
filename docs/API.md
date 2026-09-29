# Native HTTP API v1

Base: `http://127.0.0.1:8080/api/v1`. JSON by default. Optional `Authorization: Bearer TOKEN`; non-loopback bind requires a configured token. No CORS access is granted. Error body: `{"error":"explanation"}` with a non-success HTTP status. API slots are bounded; 503 indicates pressure. Mutating callers must handle ambiguous network outcomes carefully; ingestion retries must retain the same native identity.

| Method | Path | Behavior |
|---|---|---|
| GET | `/status` | Version, process counters, persisted totals, quota/disk and restart recovery notices |
| GET | `/projects` | Project configurations |
| POST | `/projects` | Create/import name, description, format, sample flag and source configuration; assigns new project identity |
| GET | `/projects/{id}` | Configuration for export |
| POST | `/projects/{id}/sources` | Append a source and event schema(s); schema IDs auto-generated if absent |
| POST | `/projects/{id}/sources/{source}/schemas/{schema}/revisions` | Append immutable revision: `expected_version`, `name`, `fields`; returns updated project or 409 |
| POST | `/projects/{id}/agents/{agent}/heartbeat` | Agent name/source/connector/state/detail/pending count/bytes; server adds last-seen and reachability |
| GET | `/projects/{id}/agents` | Agent status; offline after six seconds without heartbeat |
| GET (Upgrade) | `/projects/{id}/stream` | Same-origin WebSocket; first frame `{ "token": "..." }` within five seconds, then bounded live frames |
| POST | `/projects/{id}/events` | `{"events":[native-envelope,...],"require_recording":true}`; optional `session_id`, recording intent yields 409 if unmet; validated batch, maximum 1000 events/4 MiB/64 KiB per event |
| GET | `/projects/{id}/live` | Current bounded volatile payload buffer plus independent source states |
| GET | `/projects/{id}/sessions` | Recording metadata and snapshots |
| POST | `/projects/{id}/sessions` | `{"name":"Trial"}`; one active recording per project |
| GET | `/sessions/{id}` | Session details and original/current recording schema snapshot |
| POST | `/sessions/{id}/stop` | Seal active recording; does not stop acquisition |
| GET | `/sessions/{id}/events?after=0` | Up to 2000 original events in acceptance order; `more` and `next` pagination |
| GET | `/sessions/{id}/export/zip` | Complete native trace ZIP |
| GET | `/sessions/{id}/export/jsonl` | Complete original event envelopes |
| GET | `/sessions/{id}/export/csv` | Complete rows with `values_json`; import timestamp/integer columns as text in spreadsheets |
| POST | `/examples/motor` | Create optional synthetic motor project and complete recording |
| POST | `/examples/serial` | Create optional synthetic sensor project and complete recording |
| POST | `/examples/service` | Create optional synthetic latency project and complete recording |
| DELETE | `/samples` | Delete all sample-marked projects/recordings; reject if any is active or protected |

Identifiers accepted in configuration are restricted, while display names are arbitrary bounded text. HTML renders names/messages as text. Input files are never treated as code. Source and field schemas are described in DATA-CONTRACT.md. Session status is `recording`, `complete`, `interrupted` or `imported`. Timestamps are strings; sequence values are strings; acceptance ordinal preserves original recording order and is used for paging/ranges. Deleted sample identities are not retained in deduplication history.

The CLI is a thin client to these endpoints. There are no hidden hardware-control, notebook, AI, extension or administrator endpoints in this release.

## Live frame and gRPC

A live frame has `instance`, `snapshot`, `lost_before`, `events`, `source_states`, `agents`, `evicted_through` (discard earlier/equal live ordinals), and `next` (ordinal cursor). The first frame is a bounded snapshot; subsequent events are deltas. If the connected subscriber falls behind buffer eviction, `lost_before` is true. Reconnect always starts a new snapshot; use paginated recorded reads for historical evidence. Maximum 16 live subscriptions, WebSocket write deadline five seconds, bounded payloads as in DATA-CONTRACT.md. Source/agent state refreshes at least on periodic one-second server frames while connected; bursts are coalesced.

The optional gRPC listener uses `tracework.v1.Acquisition` from the checked-in Protobuf definition. Publish request: `project_id`, `batch_json` bytes; reply: `acknowledgment_json` bytes. Subscribe request: `project_id`; reply stream: `frame_json` bytes. Decimal strings inside JSON stay exact. Native event validation/storage are shared with HTTP. gRPC errors: UNAUTHENTICATED, INVALID_ARGUMENT, FAILED_PRECONDITION (recording intent), RESOURCE_EXHAUSTED (pressure), NOT_FOUND (subscription project). Token metadata is `authorization: Bearer TOKEN`; remote binds require TLS and a 24-character installation token. See CONNECTORS.md for startup and agent flags.

## v0.3 recording storage, ranges and imports

All routes below use the same installation token and origin/host boundary. No per-project role boundary exists. Errors are JSON and never imply a durable acknowledgment.

| Method and route | Request / result |
|---|---|
| `GET /api/v1/storage` | Referenced trace bytes, quota, chunk count, free disk, trash bytes, tombstone count, quarantine inventory; `automatic_deletion:false` |
| `POST /api/v1/import/trace` | Raw ZIP body (`application/zip`), not multipart. 201 returns `project_id`, `session_id`, `already_imported`; invalid import 422, oversized body 413 |
| `GET /api/v1/sessions/{sid}/range` | Matching count, first/last original ordinal, min/max received_ns as exact strings, full-recording bytes and chunk count |
| `GET /api/v1/sessions/{sid}/events` | Original event envelopes; cursor pagination, `more`, `next`, `selection` |
| `GET /api/v1/sessions/{sid}/export/{zip,jsonl,csv}` | Matching events across all pages; complete verified output prepared before success headers |
| `POST /api/v1/sessions/{sid}/compact` | `{}`; sealed nontrashed recording only; before/after chunk counts and any cleanup warning |
| `POST /api/v1/sessions/{sid}/protect` | `{"protected":true}` or false |
| `POST /api/v1/sessions/{sid}/trash` | `{}`; active or protected recordings rejected |
| `POST /api/v1/sessions/{sid}/restore` | `{}`; remove recoverable-trash marker |
| `DELETE /api/v1/sessions/{sid}` | `{"confirmation":"exact-session-id"}`; only stopped, unprotected, trashed recordings; identity tombstones retained |
| `GET /api/v1/projects/{pid}/trash` | Trashed sessions with count/protection/import provenance |
| `GET` / `PUT /api/v1/projects/{pid}/retention` | Policy `{"keep_last":10,"older_than_days":0}`; keep_last 1..100000, days 0..36500; no automatic action |
| `GET /api/v1/projects/{pid}/retention/preview` | Policy, candidate IDs/names/counts/bytes, SHA-256 `plan_token` |
| `POST /api/v1/projects/{pid}/retention/apply` | `{"plan_token":"reviewed-token"}`; recalculates under writer lock; stale set rejects with 409; matching candidates move to trash |

Range query parameters on `/range`, `/events` and `/export/*`: optional inclusive `from`/`to` original ordinals, exact signed-int64 `from_ns`/`to_ns` receive-time strings, and `source` ID. Ordinals/cursors are restricted to 0..9007199254740991; zero means unbounded for range endpoints. Reversed/invalid bounds reject with 400. `/events` additionally accepts exclusive `after` in original ordinal space. Pages cap at 2,000 events / 16 MiB raw lines; continue while `more` is true. Empty ranges are valid. Counts/byte statistics are not a promise of clock synchronization.

Imported sessions use new local session/project IDs, `status:"imported"`, a read-only project and `origin` metadata. Event `session_id` and `ordinal` remain those in the evidence, so do not use an imported event's session_id as its local lookup ID. An identical archive hash resolves the existing copy, even if it is in trash. Session listings exclude trash; direct session reads/exports remain possible. Project configuration import creates a writable copy with new project identity.

Bundle manifests retain `format:"tracework.trace.v1"` and add `selection`, `original_session_count`, `event_session_id` and import `origin` when applicable. The embedded session count describes only exported events. `event_session_id` describes the preserved event envelopes. Chunk grouping/IDs may change on export or optimization; original event content does not. See [STORAGE-REPLAY.md](STORAGE-REPLAY.md) for limits and recovery semantics.

Raw ZIP import example (token optional for localhost):

```sh
curl --fail-with-body -H 'Content-Type: application/zip' \
  -H "Authorization: Bearer $TRACEWORK_TOKEN" --data-binary @capture.zip \
  http://127.0.0.1:8080/api/v1/import/trace
```

## v0.4 project workspaces

`GET /api/v1/projects/{id}/workspace` returns `{ "revision": 0, "workspace": null }` until a layout is saved. `PUT` at the same path accepts `{ "expected_revision": 0, "workspace": { ... } }` and returns the validated workspace with the next revision. Stale revision returns 409; invalid/unknown fields return 400 with no replacement. The master token has administrator scope. Account session tokens enforce viewer/operator/admin roles; workspace PUT requires operator. Imported read-only projects permit these view-only writes.

Workspace shape: `{format:"tracework.workspace.v1", active_tab:"tab_id", tabs:[{id,name,columns,panels:[]}]}`. Panel fields: `id`, `kind` (plot/value/table/raw/state/log), `title`, `height`, `span`, `binding_mode` (selection/pinned/portable), `bindings` (array), `scope` (all/bindings), `filter`, `level` (empty/debug/info/warn/error), `sort` (signal/source/value/time), `descending`, `decimals` (-1..12), `palette` (instrument/blue/warm), `line` (linear/step/points), `y_min`, `y_max` (both empty or finite numeric strings, minimum < maximum). Arrays must be present, even when empty. Default examples are in `tests/workspaces.py` and `frontend/workspace-model.ts`.

Each binding carries `source_id`, `schema_id`, `field_id`, `version`, `source_name`, `schema_name`, `field_name`, `type`, `unit`, `enum`. The server validates structure/bounds and preserves unresolved references; the browser resolves them against the selected project's immutable schema metadata. It never rewrites the source definition to make a binding resolve. Saved revisions are JavaScript-safe integers. See [workspace limits and semantics](WORKSPACES.md).

## v1.0 investigations, control, checks and access

| Native endpoint | Contract / guide |
|---|---|
| `GET /api/v1/recordings` | Available recording inventory |
| `POST /api/v1/compare`, `POST /api/v1/measure` | Read-only analysis; viewer permitted; INVESTIGATIONS.md |
| `GET/PUT /api/v1/projects/{pid}/investigation` | Revision-checked investigation document |
| `GET/POST /api/v1/projects/{pid}/baselines` | Immutable reviewed snapshots |
| `GET /api/v1/projects/{pid}/investigation/report` | Standalone HTML |
| `POST /api/v1/projects/{pid}/providers` | Typed provider catalog registration; ACTIONS.md |
| `GET /api/v1/projects/{pid}/actions` | Catalog, leases and recent runs (500 / 16 MiB) |
| `GET /api/v1/projects/{pid}/actions/{run}` | Complete retained run by ID; project identity checked |
| `POST /api/v1/projects/{pid}/providers/{provider}/lease` | Acquire/renew/release account-bound ownership |
| `POST /api/v1/projects/{pid}/actions/prepare`, `/execute` | Validate, then consume one prepared arming request |
| `POST /api/v1/projects/{pid}/actions/{run}/cancel`, `/reconcile` | Explicit lifecycle operations; unknown remains distinct |
| `POST /api/v1/providers/{provider}/heartbeat`, `/next`, `/result` | Dedicated provider credential only |
| `GET/PUT /api/v1/projects/{pid}/checks` | Revision-checked native rules; CHECKS.md |
| `GET/POST /api/v1/projects/{pid}/check-runs` | Recent history (100 / 16 MiB), or evaluate and retain |
| `GET /api/v1/check-runs/{run}?format=html` | Complete JSON by default; html/junit reports |
| `GET /api/v1/auth/info`, `/auth/me` | Sign-in mode / current role |
| `POST /api/v1/auth/login`, `/auth/logout` | Password session / revocation |
| `GET/POST /api/v1/admin/accounts` | Admin account management |
| `PUT /api/v1/admin/accounts/{account}` | Role, password, disabled state; revokes sessions |
| `GET/POST /api/v1/admin/shares` | Expiring recording ZIP / check HTML scopes |
| `DELETE /api/v1/admin/shares/{share}` | Revoke a share |
| `GET /api/v1/shared/{share}` | Requires that share's bearer credential |
| `GET /api/v1/admin/audit` | Latest 1,000 entries with returned chain verification |
| `POST /api/v1/admin/backup` | Consistent ZIP; ACCESS-AND-RECOVERY.md |
| `GET /api/v1/extensions` | Authenticated inventory of explicitly installed local renderers |

History lists stop before exceeding the aggregate budget after at least one result. Stored runs are not deleted by list limits. Authentication and role checks apply centrally; share/provider credentials grant only their exact scoped endpoints. Public extension asset paths expose installed code only. The host CLI installs extensions; no project API executes or installs host code.
