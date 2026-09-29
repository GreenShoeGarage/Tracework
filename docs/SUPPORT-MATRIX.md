# Support and qualification matrix · v1.0.0

| Component / integration | Status | Evidence / limitation |
|---|---|---|
| Linux x86-64 native server/agent | Runtime tested | Static executable; API, recording, restart, failure and browser workflows passed in supplied Linux container |
| Linux ARM64 | Cross-compiled | ELF architecture inspected; no ARM64 host was available |
| macOS Intel | Cross-compiled, experimental | Mach-O inspected; unsigned, no macOS execution available |
| macOS Apple Silicon | Cross-compiled, experimental | Mach-O inspected; unsigned, no Apple Silicon execution available |
| Windows | Not packaged | Future platform decision |
| Docker Compose | Definition supplied, runtime unverified | Scratch image from included static binary; Docker Engine unavailable in build environment |
| Chromium 138.0.7204.0 on Linux | Browser tested | Desktop 1440×1000 and 1500×1100 and mobile viewport 390×844; no external requests; no page errors |
| Safari / Firefox / mobile physical devices | Not tested | Standards-based implementation does not imply qualification |
| Native HTTP JSON v1 | Fixture tested | Custom schemas, multiple types, precision, duplicates/conflicts, logs and chunk export |
| Python client | Fixture tested | Independent Python process emits during browser closure; preserved timestamps after restart |
| Go stdin JSONL agent | Fixture tested | Durable pending envelope, agent kill/restart and server reconnection; bounded spool |
| Serial JSONL | Implemented, Linux PTY tested | 8N1, baud/device config, partial-line disconnect, reopen and gap flags; no physical serial hardware tested |
| MQTT 3.1.1 | Implemented, TCP protocol fixture tested | QoS 1 subscribe, local durable acknowledgment, retained filter, redelivery, reconnect; broker-product certification and broker TLS interoperability unqualified |
| WebSocket | Browser + Go fixture tested | Authentication, exact values, bounded deltas, explicit eviction and browser reconnection |
| gRPC/Protobuf | Go + agent + TLS fixture tested | Bidirectional publish, subscribe, auth and custom CA; non-loopback plaintext refused |
| v0.1–v0.9 migration | Actual original executable fixtures tested | Original envelopes/chunk hashes and applicable feature metadata retained; current metadata v9 |
| Native ZIP import, ranges, retention | API + browser tested | Exact provenance, malformed/atomic import, protection/trash/purge, cross-page receive-time replay |
| Configurable workspaces | API/model/browser tested | Six panels, exact/portable bindings, pointer/keyboard controls, save conflicts and offline draft recovery; see WORKSPACES.md |
| ENOSPC / SQLite FULL | Disposable fault fixtures | Write injection and actual bounded SQLite page exhaustion; no physical power-cut claim |
| Simulated motor | Synthetic fixture | 240 observations, enum status, gap and log markers; no motor connected |
| Software service | Example supplied | Generic HTTP probe; native ingestion fixtures verified; no external service used for release qualification |
| CAN/DBC | File/decoder fixtures tested | Classical integer signals, both endiannesses, simple multiplexing; SocketCAN compiled, physical bus unqualified |
| Modbus TCP | Independent Python peer tested | Read-only FC3/4, wire validation, exact integer/IEEE decoding; physical devices unqualified |
| OPC UA / Ethernet-Protobuf / selected instruments | Planned | v1.3 |
| Zelos `.trz`, SDK, API | Not compatible/verified | Only documented capability reference used |
| Physical sensor/actuator/instrument hardware | Not verified | No hardware was available |
| Physical disk power-loss behavior | Not verified | Process crash, quota and write-path failure fixtures are not power-cut testing |
| Multi-user permissions | API/browser/live-transport fixtures tested | Installation-wide viewer/operator/admin roles, account-bound leases, revocation; no tenant isolation |
| Go/Rust SDKs | Real native producer fixtures tested | Exact uint64/timestamp through server recording/readback; Rust builds offline |
| Actions | Software target/API/browser fixtures tested | At-most-once dispatch and durable provider journal; no actuator-safety qualification |
| Evidence checks | Native API/CLI/pytest/browser fixtures tested | Five predicates, explicit coverage and immutable cited reports |
| Backup / restore / sharing | API/browser fixtures tested | New-directory restore, corruption rejection, scoped credential expiry/revocation |
| Process extensions | External Python-process fixtures tested | Credential stripping, pin verification, timeout, stdout bounds; trusted host code only |
| Browser extensions | Adversarial Chromium fixtures tested | Parent DOM/storage/network blocked, selected exact data only, no accepted action messages |

No rate/latency budget is advertised as a guarantee. The measured local fixture workload is in BENCHMARK.json and the release test report.
