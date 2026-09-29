# TRACEWORK v1.0.0 release qualification

Release date: 2026-09-28. **60 API workflow tests, 15 native Go tests with the race detector, 53 browser workflow groups, eight view-model groups, one Rust unit test and nine historical-version migration scenarios pass.** Strict TypeScript compilation, Go vet, offline locked Rust build/test and four-target static compilation pass. Raw release logs use `docs/V1-*`; screenshots are under `docs/images/` (filenames identify the feature's introduction batch).

## Qualification evidence

| Suite | Passing tests / groups | Release log |
|---|---:|---|
| Core HTTP/storage/Python SDK/agent | 15 | V1-API-INTEGRATION.txt |
| Serial/MQTT/gRPC/revisions/spools | 7 | V1-API-CONNECTORS.txt |
| Import/ranges/retention/storage | 9 | V1-API-STORAGE.txt |
| Workspace persistence/isolation | 6 | V1-API-WORKSPACES.txt |
| Investigations and exact citations | 5 | V1-API-INVESTIGATIONS.txt |
| Action ownership/arming/lifecycle | 5 | V1-API-ACTIONS.txt |
| Native checks, CLI and pytest | 4 | V1-API-CHECKS.txt |
| Accounts, sharing, audit, backup/restore | 5 | V1-API-ACCESS.txt |
| Go/Rust SDKs, CAN, Modbus, process adapters | 4 | V1-API-INTEGRATIONS.txt |
| Historical v0.1–v0.9 stores | 9 scenarios | MIGRATION-RESULTS.json |
| Go native tests / race | 15 | V1-NATIVE-TEST.txt |
| Go vet | Clean | V1-NATIVE-VET.txt |
| Browser: core / storage / workspaces | 11 / 7 / 14 | V1-BROWSER-* |
| Browser: investigations / actions / checks | 7 / 3 / 4 | V1-BROWSER-* |
| Browser: access / extensions | 4 / 3 | V1-BROWSER-* |
| Pure workspace model | 8 | V1-BROWSER-WORKSPACE-MODEL.txt |
| Rust unit / native conformance | 1 / API fixture above | V1-RUST-RESULTS.txt |

Python pytest integration used pytest 8.4.2 and executed a real fixture. Native SDK conformance invokes separately compiled Go and Rust examples against the server, recording `18446744073709551615` with timestamp `1790612345678901234`. Both values return unchanged. Browser suites used Chromium 138.0.7204.0 on Linux, desktop and 390-pixel mobile viewports. They report zero page errors; network-intercepted suites report zero application requests outside localhost. The renderer test additionally attempts parent DOM/storage access, network exfiltration and action messages; these are blocked/ignored while exact selected observations are delivered.

Visual review inspected the grouped investigation controls and high-contrast comparison paths, plus the bounded extension evidence view. Existing browser fixtures exercise dark/light/contrast layouts, mobile overflow, keyboard panel operations, file exports and recovery dialogs.

## Evidence, migrations and recovery

Actual original release executables from v0.1 through v0.9 generate independent historical stores. v1.0 preserves original JSONL hashes and exact event envelopes, recording protection, saved workspaces, cited investigations/baselines, provider catalogs, check rules/results, account password hashes/roles and pinned decoder definitions where available. Metadata remains version 9. The oldest `/events` response gained additional pagination metadata in later versions; migration comparisons deliberately compare the preserved original events, not an obsolete response wrapper. No fixture substitutes a newly built executable for a historical binary.

Core acceptance closes the browser while an independent producer continues capture, then kills/restarts the server: 4 observations before browser closure and 24 afterward remain available. Active captures become interrupted; earlier acknowledgments survive. Deduplication, sequence identity and exact clocks remain intact.

Fault tests include ENOSPC injection, actual SQLite page exhaustion, after-chunk/before-commit failure, orphan quarantine, corruption detection, transactional import/optimization, trash/protection/purge and spool restart/reconnect. Live backup produces a consistent snapshot while the original server can continue afterward; restore rejects tampered archives and existing destinations, verifies original evidence and revokes browser/share sessions. Action recovery distinguishes undispatched cancellation from dispatched unknown outcomes. Physical power loss and physical target safety remain unqualified.

## Measured performance envelope

One disclosed local fixture used AMD EPYC 9V74 80-Core Processor, 9 exposed logical CPUs, Linux 6.18.44 x86-64 and an ephemeral container filesystem. `/proc` reports approximately 9.7 GiB total memory; this is not a portable allocation guarantee. Server RSS could not be read because its PID was outside the exposed `/proc` namespace.

| Workload / metric | Measured |
|---|---:|
| 10,000 events, batches of 500, one source, four fields, one client | 1.031 s |
| Aggregate ingest throughput | 9,697 events/s |
| Batch acknowledgment median / p95 | 47.84 / 64.62 ms |
| Read all 10,000 observations | 0.165 s |
| Compare two aliases of 10,000 original points each | 0.289 s |
| One check across 10,000 original observations | 0.143 s |
| Trace bytes for that capture | 5,847,784 |
| Separate 1,000 single-event requests | 1.464 s |
| Sealed-recording chunk count before / after aggregation | 1000 / 1 |
| Full-read median before / after aggregation (five repetitions) | 0.0351 / 0.0157 s |

No browser was concurrently active in the throughput fixture. Measurements are not guaranteed capacity, latency or acquisition-rate claims. Full exports/backups/checks share the writer lock and can delay ingestion. Sealed aggregation reduces file count and read overhead, not capture fsyncs. Raw values and workload definitions are in BENCHMARK.json, STORAGE-BENCHMARK.json and the corresponding scripts. Larger sustained multi-device workloads need deployment-specific qualification.

## Reproduce with prepared tools

Tested build tools: Go 1.27.1 (minimum source requirement 1.24), TypeScript 5.9.3, Rust 1.98.1; Go and Rust runtime dependencies are vendored. The native binary and browser runtime need no compiler or package-manager installation. Cargo must run from `sdk/rust` so its local vendoring configuration is discovered.

For a complete historical run, set `TRACEWORK_BASELINE_BINARY` to original v0.1 for the older connector test and `TRACEWORK_V02_BINARY` through `TRACEWORK_V09_BINARY` to their original executables. `tests/migrations.py` also accepts `TRACEWORK_V01_BINARY`; defaults point to `/tmp/tracework-vNN-baseline`. Those historical binaries are qualification inputs, not bundled runtime dependencies. Set `TRACEWORK_PYTEST_PATH`, `TRACEWORK_GO_SDK_EXAMPLE`, and `TRACEWORK_RUST_SDK_EXAMPLE` to prepared test installations/builds. Run `python3 scripts/qualify.py api`. All release gates ran with those dependencies present; historical tests were not skipped.

For browser tests set `PLAYWRIGHT_MODULE` and `CHROMIUM_PATH`, then run `python3 scripts/qualify.py browser`. `python3 scripts/qualify.py native` records race/vet results. Optional names select a targeted rerun while retaining other recorded outcomes. CI checks source-based gates; no remote CI run or repository publication is claimed.

## Release boundary

Supported runtime qualification is Linux x86-64 plus Chromium on Linux. Linux ARM64 and both macOS architectures are cross-compiled experimental binaries. Windows, Docker runtime, Firefox/Safari, physical mobile devices, physical serial/CAN/Modbus hardware, broker products, production reverse-proxy/LAN deployments and physical disk power cuts are not qualified. SocketCAN is compiled and shares the tested decoder, but was not executed against a CAN interface. Rust client TLS uses its bundled library backend; a separate remote TLS interoperability run was not performed.

The package retains GPL-3.0-only original source and all dependency notices, including 75 vendored Rust crates. Final packaging verifies ZIP integrity and every SHA256SUMS entry. Core assets/docs are embedded in each binary, and standalone files accompany them. Feature limits and deferred work are documented in README, CAPABILITY-MATRIX and SUPPORT-MATRIX; v1.0 does not imply full reference-product compatibility.
