# TRACEWORK v1.0.0

A local engineering workbench from Green Shoe Garage: capture observations from your own devices and software, preserve exact evidence, compare recordings, operate explicitly registered targets, and run reproducible evidence checks. Start with a blank project and define your own sources and schemas. Three optional examples are clearly synthetic.

The complete package runs offline. The native server, agent, browser assets and help are included; no account service, CDN, telemetry or subscription is required. Original source is **GPL-3.0-only**. This is the native TRACEWORK API and trace format; Zelos `.trz`, SDK and API compatibility is not implemented or verified.

## Start locally

Unzip the complete package, open its `tracework` directory, then run:

```sh
./start.sh serve --data ./tracework-data
```

Open **http://127.0.0.1:8080**. Linux x86-64 is runtime qualified in the supplied environment. Linux ARM64 and macOS Intel/Apple Silicon binaries are included as experimental cross-builds; they have not been executed on those platforms. No Windows binary is included. No Node, Python, Go or Rust installation is needed to run the native server and agent.

1. Create a blank project, then define a source, fields, units and clock domain.
2. Open **Connect source** for a Python producer example or Serial/MQTT/stdin agent configuration. The integration guide covers Go/Rust, CAN/DBC, Modbus and local extensions.
3. Start **Record**, then run the producer. Default agents buffer until a recording exists. Capture belongs to the server and continues when the browser closes.
4. Stop capture, reopen the recording, inspect original values and timing, and export its trace ZIP.
5. Use **Investigate** for comparisons and cited findings; **Checks** for explicit evidence rules and reports.

[Quickstart](docs/QUICKSTART.md) · [Connectors](docs/CONNECTORS.md) · [Native integrations](docs/INTEGRATIONS.md)

## What's included

| Workflow | v1.0 behavior |
|---|---|
| Capture | Generic schemas, immutable revisions, exact int64/uint64 strings and nanosecond timestamps; HTTP, WebSocket and optional gRPC; durable recording and agent spools |
| Inspect | Six configurable panel types, eight saved tabs, shared cursor, loaded-page zoom/pan, keyboard equivalents, three themes and mobile layouts |
| Preserve | Hash-verified native JSONL chunks; ranged ZIP/JSONL/CSV export; read-only trace import; protection, reviewed retention, recoverable trash and explicit purge |
| Investigate | Up to four explicit recording aliases; absolute/relative/event alignment; exact two-observation measurements; authored assumptions/evidence/findings; immutable baselines; printable HTML reports |
| Operate | Registered typed actions; independent provider validation; exclusive account-bound lease; preparation and arming; at-most-once dispatch; distinct failure/cancellation/unknown outcomes |
| Check | Five bounded predicates; explicit ordinal/time windows, coverage/gap/null policies; immutable results and citations; Go/Python CLI, pytest, JUnit and HTML |
| Administer | Optional local accounts, installation-wide viewer/operator/admin roles, expiring scoped links, hash-linked audit, consistent live backup and validated new-directory restore |
| Integrate | Python/Go/Rust clients; Serial JSONL, MQTT, bounded Classical CAN/DBC and read-only Modbus TCP; trusted resource-bounded process adapters and sandboxed browser renderers |

The [capability matrix](docs/CAPABILITY-MATRIX.md) marks remaining limits and deferred work. Notebooks and optional AI are future versions, with no active controls in v1.0.

## Connect a producer

From the package directory, run your copied connection example with:

```sh
PYTHONPATH=sdk/python python3 my_source.py
# Or use the configuration downloaded from Connect source:
./start.sh agent --config tracework-agent.json
# For stdin values, append: < my-values.jsonl
```

Python examples require preinstalled Python 3.10+; the basic SDK uses the standard library. The Go agent supplies capture identity and a durable queue. Use a unique spool per source/configuration. By default it holds observations until a recording is active, and after server restart requires an explicitly started new recording. Fixed-session spools never silently move to a different recording. Native-envelope input preserves producer clocks and identities; value-only input uses the agent's timestamp and can repeat MQTT redelivery. Serial device buffers can overflow under backpressure.

CAN/DBC and Modbus adapters preserve raw payloads plus a SHA-256-pinned decoder definition in source snapshots. Protocol fixture success is not physical-device qualification. Process extensions are trusted host programs with resource limits, not a filesystem/network security sandbox. Browser renderers are opaque-origin iframes with networking denied and receive only an explicitly sent bounded signal selection. See [INTEGRATIONS.md](docs/INTEGRATIONS.md).

## Export and automate

```sh
./start.sh version
./start.sh api GET /api/v1/status
./start.sh api POST /api/v1/projects --body examples/blank-project.json
./start.sh api GET /api/v1/sessions/SESSION/export/zip --out capture.zip
python3 scripts/verify_trace.py capture.zip
./start.sh check --project PROJECT --session SESSION --junit result.xml --html result.html
```

`TRACEWORK_URL` selects the server, and `TRACEWORK_TOKEN` supplies a master or account session token. The CLI does not automatically retry mutations. Export includes all events in the selected range across pages. The API CLI caps buffered responses at 512 MiB; use the browser or a streaming HTTP client for larger exports/backups. Check exits distinguish passing, failing and insufficient evidence; see [CHECKS.md](docs/CHECKS.md). The optional pytest plugin needs pytest installed separately.

## Accounts and network access

With no accounts or master token configured, loopback operation grants local administrator access. Use **Access** to create accounts and roles. Viewer reads/compares/exports; operator also configures and captures, runs checks and operates registered targets; administrator manages accounts, sharing, retention/purge and backups. Roles cover every project in this installation; this is not tenant isolation. Saved project workspaces are shared, while viewer adjustments are temporary.

Binding beyond loopback requires a master token of at least 24 characters:

```sh
export TRACEWORK_TOKEN="$(python3 -c 'import secrets; print(secrets.token_urlsafe(32))')"
./start.sh serve --listen 0.0.0.0:8080 --data ./tracework-data
```

Keep the token for recovery. Configure an HTTPS reverse proxy or encrypted tunnel before sending credentials over an untrusted network. Native Go/Rust clients refuse remote plain HTTP. Optional non-loopback gRPC requires TLS. WebSocket/gRPC sessions enforce role changes and revocation during streaming. Production reverse-proxy/LAN deployments are not qualified by the loopback tests. [Access, sharing, audit and recovery](docs/ACCESS-AND-RECOVERY.md) documents the exact boundary.

## Storage, backup and upgrade

`metadata.sqlite` and its sidecars hold definitions, indexes, layouts, investigations, checks, accounts and audit. Immutable `chunks/*.jsonl` hold recorded evidence and are hash-checked on startup/read/export. `recovery/` quarantines uncommitted chunks. `server.lock` prevents concurrent servers using one directory.

A recorded acknowledgment follows chunk fsync and SQLite FULL commit. The live buffer is volatile: 2,000 events per project, with a global 32 MiB encoded-payload budget. Default trace quota is 1 GiB; hitting it rejects capture without acknowledgment and never automatically deletes recordings. Metadata, journals and agent spools have separate growth characteristics. Writes and full exports serialize; batching improves capture rate. Stopped recordings can aggregate chunks without changing original event lines.

Use **Access → Download consistent backup**, then validate a restore into a new directory:

```sh
./start.sh restore --backup backup.zip --data ./restored-data
./start.sh serve --data ./restored-data --listen 127.0.0.1:8081
```

Backups contain credential hashes and are not encrypted. Restore verifies manifests, SQLite and indexed chunk hashes, revokes browser/share sessions, and applies interrupted-capture/action recovery. It never overwrites an existing directory. Separately preserve agent spools, action-provider journals and installed extension programs. A trace export is not a full-store backup.

Before upgrading, preserve a complete stopped-server data-directory copy and the previous executable. v1.0 opens metadata versions 1–9 and creates consistent metadata snapshots before required migrations. Actual v0.1–v0.9 executable fixtures preserve event envelopes/chunk hashes and the feature metadata available in each version. Metadata version 9 remains current; older binaries cannot open it safely. Rollback requires the complete earlier backup, not just the automatic SQLite snapshot.

A server crash seals active capture as **interrupted**; start a fresh recording explicitly. Corrupt/missing committed chunks block startup. Keep the failed directory and restore a known complete backup. Process-failure/fsync fixtures do not establish physical power-loss guarantees.

## Offline and Docker

Transfer the entire release ZIP to the offline machine. `SHA256SUMS` covers included source, assets, examples and binaries. Core runtime has no required external requests. Go dependencies and all Rust client dependencies are vendored for builds with prepared compilers; TypeScript, test browsers and pytest are development tools that must be prepared separately.

Docker Engine/Compose must already be installed. The supplied scratch image uses an included static binary, with no registry base image:

```sh
export TRACEWORK_TOKEN="$(python3 -c 'import secrets; print(secrets.token_urlsafe(32))')"
# ARM64 only: export TRACEWORK_ARCH=arm64
# Keep the token for future sign-in and recovery.
docker compose build --pull=false
docker compose up -d
```

The container runs as UID/GID 10001, with dropped capabilities, read-only root and a named data volume. `docker compose down` retains data unless `-v` is added. Docker runtime was unavailable during qualification. To transfer a prepared image, use `docker save tracework:1.0.0 -o tracework-image.tar`, then `docker load -i tracework-image.tar` offline.

## Support limits and measured performance

Linux x86-64 and Chromium 138 on Linux are tested; other platforms and browsers remain explicitly unqualified. Physical serial/CAN/Modbus hardware, actuator safety, broker products, physical power cuts and Docker runtime were not tested. The software target example changes a counter file; cancellation is cooperative and is not an emergency stop.

Historical view pages are bounded; automatic stream backfill, live group commit, triggered/ring capture, plot decimation, zoom history, per-signal styles, area fill and a trace-cache lifecycle API remain deferred. Evidence exports retain complete selected observations. Comparisons/checks evaluate bounded recorded windows without inventing missing samples or proving unobserved physical behavior. Unsynchronized source timestamps cannot establish causality.

[TEST-REPORT.md](TEST-REPORT.md) gives complete suite results and the measured local workload. Measurements are not advertised capacity guarantees. [Support matrix](docs/SUPPORT-MATRIX.md) lists each tested and untested boundary.

## Build and verification

Original GPL-3.0-only source, licenses, vendored dependencies, compiled browser assets, four platform binaries, examples, tests and CI definition are included. Nothing has been pushed to an external repository or deployed.

```sh
# Requires Go 1.24+; supplied builds used Go 1.27.1.
CGO_ENABLED=0 go build -mod=vendor -buildvcs=false -o bin/tracework-linux-amd64 .
go test -race -mod=vendor -buildvcs=false ./...
go vet -mod=vendor ./...
python3 scripts/qualify.py api
node tests/workspace-model.cjs
./scripts/build.sh
# Rebuild edited TypeScript only; prebuilt web files already ship:
npm ci
npm run build
# Offline Rust client development (tested Rust 1.98.1):
(cd sdk/rust && cargo test --offline --locked)
```

The release runner's API group needs historical executables and prepared SDK example binaries for its complete gates; details are in TEST-REPORT.md. Browser verification uses Playwright and Chromium via `PLAYWRIGHT_MODULE` and `CHROMIUM_PATH`. `scripts/qualify.py browser` records real browser exits; no external application requests are allowed by those fixtures. The supplied CI definition tests available source gates; historical executables and hardware are not embedded as CI tools.

[Architecture](docs/ARCHITECTURE.md) · [Data contract](docs/DATA-CONTRACT.md) · [API](docs/API.md) · [Roadmap](ROADMAP.md) · [Changelog](CHANGELOG.md) · [License](LICENSE)
