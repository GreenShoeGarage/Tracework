# Reference capability matrix · TRACEWORK v1.0.0

Reviewed 2026-09-28 against [Zelos documentation](https://docs.zeloscloud.io/latest/). This is a capability inventory and staged implementation plan, **not a compatibility declaration**. “Implemented” means the bounded behavior described here; “partial” identifies narrower functionality. Future acceptance criteria are release gates, not current features.

Reference keys: [Concepts](https://docs.zeloscloud.io/latest/sdk/core-concepts/), [Schemas](https://docs.zeloscloud.io/latest/sdk/how-to/define-schemas/), [Recording](https://docs.zeloscloud.io/latest/sdk/how-to/record-files/), [Workspace](https://docs.zeloscloud.io/latest/app/workspace/), [Plot](https://docs.zeloscloud.io/latest/app/panels/plot-panel/), [Timeline](https://docs.zeloscloud.io/latest/app/timeline/), [Actions](https://docs.zeloscloud.io/latest/app/panels/action-panel/), [Checks](https://docs.zeloscloud.io/latest/sdk/testing/checker/), [Extensions](https://docs.zeloscloud.io/latest/app/extensions/), [Notebooks](https://docs.zeloscloud.io/latest/notebooks/format/), [AI](https://docs.zeloscloud.io/latest/app/zelos-ai/). These linked sections and the reference navigation were reviewed before implementation. Requirements below also incorporate the supplied TRACEWORK brief.

| Reference capability | Release | Acceptance criterion | v1.0 status |
|---|---|---|---|
| App installation and local start | 0.1 / 1.0 | Extract package; launch without frontend compile or vendor account | Implemented; Linux x86-64 runtime tested |
| Agent installation / service lifecycle | 0.1–0.2 | Separate producer host; documented start, stop and reconnect | CLI agent implemented; service installer later |
| CLI installation and headless use | 0.1 / 0.7 | Script project creation, acquisition and evidence export | Native API CLI plus Go/Python checks, pytest fixtures, JUnit/HTML reporting |
| Python SDK | 0.1 | Define schema and emit actual producer data with no pip dependencies | Implemented native API |
| Rust / Go SDKs | 0.9 | Same typed conformance fixture through all SDKs | Native clients implemented; actual exact-value producer/readback fixtures pass; Rust dependencies vendored |
| Source / event / field hierarchy | 0.1 | User-defined fields bind to plots and exact event inspector | Implemented |
| Segment / producer lifetimes | 0.1–0.2 | Distinguish reboots and source lifecycle without sequence collision | Boot ID and restart flags implemented; discovery later |
| Rich typed fields | 0.1–0.2 | Validate widths, nulls, enums, units and nested/binary payloads | Partial: float64, int64, uint64, bool, string, enum |
| Explicit schemas | 0.1 | Reject unknown fields and incompatible values atomically | Implemented |
| Inferred schemas | Later | Explicit review of inference before capture; stable IDs | Deferred; explicit user-approved schemas remain the implemented contract |
| Schema evolution | 0.2 | Old recording interpretation survives new schema revisions | Implemented immutable revisions; changed field metadata gets new ID; migration fixture passes |
| Well-known logs / spans / annotations | 0.1 / 0.5 | Semantic metadata does not depend on arbitrary field names | Logs and cited investigation annotations implemented; generic span model deferred |
| Publish service and batching | 0.1–0.3 | Atomic typed batches with bounded buffers and receipts | HTTP JSON batches up to 1,000 events implemented |
| gRPC/Protobuf SDK transport | 0.2 | Generated clients pass native v1 conformance tests | Implemented generated native Publish/Subscribe; TLS/auth/conformance fixtures |
| Subscribe service / filtering / backfill | 0.1–0.3 | Reconnect, filter and resume without silent loss | WebSocket/gRPC snapshots+deltas with explicit buffer loss; recorded pagination; historical reconnect backfill later |
| Source/receive time and clock quality | 0.1 | Preserve 1 ns differences beyond JS safe integer range | Implemented; accuracy never inferred |
| Agent memory cache / retention | 0.1–0.3 | Explicit capacity, predictable eviction and pressure signal | Last 2,000/project with global encoded-payload cap; recorded retention/protection/trash implemented |
| Agent disk buffering | 0.1–0.2 | Disconnect/restart agent, replay identical observations once | Spool, reconnect, health and recording-intent fixtures tested; actual remote topology qualification open |
| Backpressure | 0.1–0.3 | Queue fills without unbounded memory or silent dropping | Request bounds / spool backpressure implemented |
| Multiple concurrent sources | 0.1–0.2 | Observe distinct IDs without schema leakage | Distinct source/schema/clock fixture passes; physical device qualification later |
| Multiple agent connections | 0.2 | Agent identity, health, reconnection and source association | Persistent agent IDs and source association, heartbeat/offline status and queues implemented |
| Recording ownership | 0.1 | Browser closes while external producer continues capture | Implemented and acceptance tested |
| Trace writer / recorded reads | 0.1 | Persist, restart, reopen exact same event | Implemented native JSONL; sealed-recording aggregation and indexed ranges |
| Conditional/ring/multi-file recording | 0.3 | Triggered capture and retention preserve known coverage | Reviewed retention/protection/trash implemented; triggered/ring capture still open |
| Interrupted writes / recovery | 0.1–0.3 | Quarantine orphans, reject corruption, never acknowledge failed storage | ENOSPC/SQLite-full/pre-commit failure and restart tested; physical power loss unqualified |
| Trace cache API | 0.3 | Cache lifetime/ownership and explicit persisted coverage | Planned |
| Workspace home / recent projects | 0.1 | Create genuinely blank project; restore chosen project | Implemented |
| Guided tour / sample workspaces | 0.1 | Three optional examples; remove without touching user projects | Implemented synthetic recordings and local help |
| Signal sidebar / search | 0.1 | Find, select and inspect user-defined fields | Implemented; eight selected signals, with independent panel bindings |
| Action discovery sidebar | 0.6 | Show provider actions only while connected | Online provider catalogs and typed actions shown in Actions; offline execution refused |
| Command palette | 0.1 / 0.4 | Keyboard reaches core commands and all panel operations | Core commands, Add panel and Workspace tabs; all panel controls reachable by Tab |
| Workspace autosave | 0.1 / 0.4 | Restore project, selection, theme, mode, sizes and layouts | Server-saved project layouts with revision conflict detection and browser draft recovery; global preferences remain local |
| Rearrangeable/resizable panels | 0.4 | Drag and equivalent keyboard operation; reload layout | Pointer drag/resize and keyboard equivalents; dimensions/order restore on reload |
| Multiple tabs / tab restore | 0.4 | Reusable user-created tabs persist across reload | Implemented; up to eight saved tabs per project |
| Portable / source-scoped bindings | 0.4 | Rebind named paths or pin provider/source explicitly | Exact IDs or unique named paths with version/type/unit/enum agreement; visible unresolved states |
| Multi-trace aliases and scope | 0.5 | Distinct traces never silently merge same-named signals | Up to four explicit aliases, 10,000 events per trace and 32 MiB combined; identities retained |
| Plot live and recorded signals | 0.1 | Show custom numeric fields and units, exact value inspection | Implemented basic independent lanes |
| Plot axes and multi-scale settings | 0.1 / 0.4 | Useful axes, explicit units and saved scale configuration | Independent lanes, exact integer delta baseline, saved automatic/fixed Y bounds per panel |
| Plot zoom, pan and history | 0.4 | Range selection, pan, reset and keyboard equivalents | Loaded-page pointer/button zoom, pan and Fit page implemented; zoom history stack open |
| Plot colors, line styles, area, interpolation | 0.4 | Per-signal settings persist and interpolation is labeled | Panel palettes and labeled linear/step/points implemented; per-signal styles and area fill open |
| Downsampling / M4-like preservation | 0.3–0.4 | Bound display points without losing original evidence | Planned; page-limited original samples now |
| Plot gaps and unavailable states | 0.1 | Break paths at flags/nulls/stale intervals; no invented continuity | Implemented |
| Shared cursor across plots/readouts/raw/logs | 0.1 / 0.4 | Same selected event drives all views | Implemented across six panel types; lane inspection selects its source/schema revision |
| Two-cursor measurements | 0.5 | Reversible Δt/Δvalue with cited observations | Exact two-observation time/value difference with original endpoint citations |
| Table panel | 0.4 | Source, unit and stale state at shared cursor | Configurable cursor snapshot, filters, exact integer sorting, unit/source/state metadata |
| Raw panel | 0.1 / 0.4 | Complete event identity, sequence and timestamps | Configurable scope/filter; latest 100 matching page events and complete selected envelope |
| State panel | 0.4 | Labeled discrete spans, gaps and transition inspection | Labeled bool/enum/string intervals and event inspection; gaps, nulls, restarts and stale breaks preserved |
| Log panel | 0.1 | Search level/message/source; jump to linked event | Configurable scopes and message/level filters; inspect linked event |
| Value panel | 0.1 / 0.4 | Large exact readouts, units and stale state | Configurable bindings and float decimals; exact integers, units and stale state |
| Actions panel | 0.6 | Schema-generated forms, validation and execution outcomes | Typed forms, prepare/arm, independent provider validation and distinct outcomes; software-target qualified |
| Timeline pause, step, seek | 0.1 | Inspect without stopping capture; resume latest data | Implemented event cursor |
| Real-time replay and speed | 0.3–0.4 | Playback honors time intervals and gaps | Receive-time browser replay at selectable speed, pause/step and cross-page continuation; browser scheduling limits apply |
| Absolute / relative / event alignment | 0.5 | Original time preserved; transforms reversible | Implemented in Investigate, with explicit receive/source clock basis and reversible offsets |
| Selected-range export | 0.3–0.5 | Export exact boundaries and manifest coverage | Inclusive ordinal/receive-time/source-selected export with explicit manifest coverage implemented |
| Complete trace export | 0.1 | ZIP hashes and JSONL/CSV independently inspectable | Implemented native format |
| Trace import / merge | 0.3 / 0.5 | Reject corruption; preserve source identity and coverage | Read-only native import preserves evidence; explicit comparison aligns separate traces without merging original identities |
| Settings / themes / shortcuts | 0.1 / 0.4 | Dark/light/high contrast and keyboard workflow | Core implemented; richer settings later |
| Local projects / organization workspaces | 0.1 / 0.8 | Ownership/roles enforced server-side | Installation-wide roles and shared project layouts; no tenant isolation or private per-account project scope |
| Trace sharing by authenticated link | 0.8 | Expiring scoped link requires correct permission | Admin-created expiring/revocable scoped bearer links; recording ZIP or check HTML |
| Cloud storage / billing replacement | 0.3 / 0.8 | Local quotas/backup; no paid service required | Local storage/quota implemented; billing not applicable |
| Accounts, viewer/operator/admin | 0.8 | Deny unauthorized query and action at server/provider | Server-side roles with account-bound leases and live WebSocket/gRPC revocation |
| Audit / full backup and restore | 0.8 | Restore permissions, projects and trace integrity | Hash-linked audit and consistent ZIP backup; validated new-directory restore with credential/capture/action recovery |
| Dynamic action registration / schemas | 0.6 | Target/provider validates every parameter | Typed immutable provider catalog, server and provider validation; max 64 providers/project |
| Action result / timeout / cancellation | 0.6 | Success, failure, canceled, unknown kept distinct | Queued/running/cancel_requested/succeeded/failed/canceled/expired/unknown/resolved kept distinct |
| Operator ownership / consequential arming | 0.6–0.8 | Conflicting operators cannot issue simultaneous control | Exclusive account-bound lease, short-lived preparation, explicit arming; unknown blocks further commands |
| Checks / readable rule suites | 0.7 | Reach target, remain within, settle, transitions, sequence | all_between, any_between, equals, transition and settles; general sequence language deferred |
| Coverage, tolerance, deadline rules | 0.7 | Insufficient/stale evidence cannot pass | Explicit ordinal window, min samples/span, max gap, null/gap policy and hold interval; inadequate evidence cannot pass |
| Pytest fixture/plugin / trace-on-failure | 0.7 | UI, CLI and pytest give same verdict for same trace | Native engine via Python fixture and independent pass/fail/insufficient outcomes; existing recording evidence retained |
| Headless CI checks / JUnit / HTML | 0.7 | Evidence-linked deterministic machine reports | Go/Python CLI and standalone immutable result reports; original evidence citations |
| Agent extensions | 0.9 | Failure isolated from recording and scoped device access | Trusted process adapters with pinned files and resource/failure isolation; no OS filesystem/network sandbox |
| Browser custom panels | 0.9 | Isolated extension cannot compromise data/control boundary | Explicit local renderers in opaque CSP sandbox; bounded selected observations only; no action API |
| Marketplace / private catalog | 0.9 / 1.3 | Offline signed local install, private distribution and updates | Local hash-pinned installation implemented; signatures, catalog distribution and update service deferred to v1.3 |
| Serial JSONL / MQTT | 0.2 | Real device/connection fixtures and honest support labels | Implemented Serial JSONL and MQTT; PTY/TCP protocol fixtures pass; physical hardware/broker-product qualification open |
| CAN / DBC and Modbus | 0.9 | Decode published fixtures and verify selected adapters | Bounded Classical CAN/DBC and read-only Modbus TCP; decoder/independent-peer fixtures pass, physical devices unqualified |
| OPC UA / Ethernet-Protobuf / instruments | 1.3 | Protocol-specific fixtures and hardware support matrix | Planned |
| Markdown/Python notebook format | 1.1 | Parameters, stable cells, retained outputs, pinned environment | Planned |
| Notebook interruption / restart / CLI | 1.1 | Scoped resources and repeatable offline execution | Planned |
| Notebook publish / .ipynb interchange | 1.1–1.3 | Preserve outputs/provenance without implicit execution | Planned; no format compatibility claimed |
| Optional AI signal discovery / queries | 1.2 | Evidence shows actual signal IDs and ranges | Planned; core works without AI |
| AI layout/notebook assistance | 1.2 | Edits respect user scope and execution permissions | Planned |
| AI action confirmations | 1.2 | Same leases, arming and outcome handling as manual action | Planned |
| Reference `.trz` compatibility | 1.3 | Real writer/reader round trips against pinned SDK fixtures | Not implemented or verified |
| Reference SDK/API compatibility | 1.3 | Version-pinned public conformance suite | Not implemented or verified |

## Compatibility investigation

The reviewed reference documents gRPC/HTTP2 with Protocol Buffers for SDK transport, a richer type system than this batch, producer segments, and `.trz` trace-file APIs. Its Python API and names are not the native TRACEWORK API. No reference-format binary fixture was supplied or generated during this batch. Documentation-level functional similarity does not establish wire or file compatibility.

No Zelos source, application assets, SDK, trademarks-as-branding, or licensed desktop binaries are included. The UI and native data format are original. A future adapter must identify a pinned upstream format/version, license obligations, coverage, unknown fields, and round-trip failures before its status can change to verified.
