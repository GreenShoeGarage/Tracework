# TRACEWORK development roadmap

Current release: **v1.0.0 — qualified offline package**. Working name: TRACEWORK.

| Batch | Outcome and release gate | State |
|---|---|---|
| v0.0 | Reviewed reference matrix, architecture decisions, native contract, compatibility boundaries; Arrow evaluation | Complete; native gRPC delivered in v0.2; Arrow experiment delivered in v0.3 |
| v0.1 | Blank user projects; Go server/agent; Python ingestion/API CLI; plot/readout/log/ledger; durable recording; restart/reopen | Delivered, scoped limits in README and test report |
| v0.2 | Serial JSONL/MQTT connectors; remote-agent identity/status; multiple-source qualification; schema versioning; gRPC and WebSocket transport | Delivered; PTY/MQTT protocol/TLS fixtures, hardware and broker qualification still open |
| v0.3 | Aggregated/indexed chunks; optional Arrow codec benchmark; retention and full-disk/recovery qualification; offline trace import; ranged replay/export | Delivered within explicit limits: sealed-recording aggregation, ENOSPC/SQLite-full fixtures; physical power-loss testing remains open |
| v0.4 | Configurable plot/table/raw/state/log/value panels; layout tabs; portable/source bindings; zoom/pan, resizable/reorderable keyboard workflow | Delivered; loaded-page plots, revision-checked saves and explicit portable binding resolution |
| v0.5 | Compare recordings; absolute/relative/event alignment; measurements; annotations, baselines, assumptions/evidence/findings; investigation reports | Implemented; bounded explicit trace aliases and verified evidence snapshots |
| v0.6 | Discovered actions; server/target validation; arming and ownership; outcome history; cancellation and unknown-timeout handling | Verified; software target, separate provider validation and durable dispatch journal |
| v0.7 | Rules with explicit windows/coverage/stale policies; native Python checks, pytest, headless CI; JUnit and HTML evidence reports | Verified; one native engine, five predicates, separate insufficient evidence |
| v0.8 | Local accounts/workspaces; viewer/operator/admin; authenticated sharing; audit history; live backup and restore | Verified; installation-wide roles, scoped bearer shares, hash-linked audit and new-directory restore |
| v0.9 | Rust/Go SDKs and conformance; isolated agent/browser extensions; CAN/DBC and Modbus | Verified within documented protocol and isolation boundaries |
| v1.0 | Qualified supported platforms, full offline packaging, migration/recovery, polished and reviewed UX, documented performance envelope | Complete for Linux x86-64/Chromium with explicit protocol/platform limits |
| v1.1 | Reproducible Markdown/Python notebooks with isolated execution, parameters, retained outputs and CI | Planned |
| v1.2 | Optional local AI with signal/time evidence and existing permissions/control constraints | Planned |
| v1.3 | Specialist integrations, private extension catalog, qualified compatibility adapters | Planned |

Reassess scope after every three batches. The v0.3 workflow/visual review is complete in `docs/V03-WORKFLOW-REVIEW.md`; dedicated v0.6 and v0.9 reviews are also complete. Keep JSONL following the measured Arrow prototype. Sealed-recording optimization reduces small-file overhead without weakening acknowledgment durability. Cross-compilation is not runtime qualification.

The user has authorized all remaining batches through v1.0 without further approval. The v0.5 batch implements: compare recordings; absolute, relative and event alignment; measurements; annotations, baselines, assumptions/evidence/findings; and investigation reports. Preserve imported evidence, exact values, existing capture and replay/retention paths.

Open qualification and scaling work: automatic historical streaming backfill (recorded range retrieval is available), ongoing capture group commit, triggered/ring recording, trace-cache lifetime APIs, hardware/broker/LAN/platform qualification and physical power-loss testing. These are explicitly deferred beyond v1.0. Plot decimation, per-signal styling, area fill and zoom history also remain open; their absence is explicit in the capability matrix. Prioritize these by measured workflow need in subsequent versions.
