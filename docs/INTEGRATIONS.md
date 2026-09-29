# Native SDKs and local integrations

TRACEWORK v0.9 introduces native Go and Rust HTTP clients, bounded Classical CAN/DBC and read-only Modbus TCP adapters, and explicitly installed local extensions. No Zelos SDK/API/file compatibility is claimed. All adapters retain the normal source/schema/clock contract and durable agent spool. Start a recording before acquisition; use a distinct spool for each adapter configuration.

## Go and Rust

The Go package is `tracework/sdk/go/tracework` within this module. See `sdk/go/example/main.go`. It uses the standard library. Run `go test -mod=vendor ./sdk/go/tracework` and `go run -mod=vendor ./sdk/go/example` against a local server. The example creates its own project and recording.

The Rust crate is in `sdk/rust`; run Cargo from that directory so `.cargo/config.toml` resolves its vendored dependencies: `cargo test --offline --locked` and `cargo run --offline --locked --example conformance`. All 75 resolved crates and their licenses are included. Rust 1.98.1 was used for qualification; Rust is a development dependency only.

Both clients refuse remote plain HTTP, credential-bearing URLs and redirects, apply 30-second timeouts and cap each response at 34 MiB. `TRACEWORK_URL` and `TRACEWORK_TOKEN` configure the examples. Production calls take the token explicitly. Source helpers preserve pending envelopes after ambiguous acknowledgments: retry that same envelope before emitting the next observation. These SDK helpers hold pending data in memory; use the Go agent for a durable disk spool. Rust callers supply a unique boot ID per producer lifetime. Encode int64/uint64 as canonical decimal strings; do not cast them through floating point. Generic request methods expose native endpoints, while convenience APIs cover projects, schemas, capture and paginated reads.

## CAN and DBC

Create the source definition from the exact DBC that the agent will use:

```sh
./start.sh dbc-schema --dbc examples/protocols/bench.dbc --can-id 291 --clock can-gateway > can-source.json
./start.sh api POST /api/v1/projects/PROJECT/sources --body can-source.json
# Substitute the returned source/schema IDs below.
./start.sh agent --project PROJECT --source SOURCE --schema SCHEMA --clock can-gateway --connector can-jsonl --dbc examples/protocols/bench.dbc --can-id 291 --spool ./can-spool < examples/protocols/can-frames.jsonl
# Linux read-only SocketCAN: replace connector/input with:
# --connector socketcan --can-interface can0
```

The supported DBC subset is Classical CAN, 1–8 payload bytes, Intel and Motorola integer signals, signed/unsigned widths 1–64, rational factor/offset, and one unsigned unscaled multiplexer with simple mN alternatives. Each selected message has at most 59 signals, a 64 KiB retained definition and units at most 32 bytes. Entire input is capped at 4 MiB and 2,048 messages. Extended frame IDs use the DBC high-bit convention and the CLI `--can-extended` flag. CAN FD, floating DBC signal encodings, extended multiplexing, multiline comments and unknown syntax are rejected. Recognized comments/attributes/value labels do not affect decoding. Disjoint multiplexer alternatives may overlap; simultaneously active signals may not.

JSONL input is `{ "id":291, "extended":false, "data":"hex bytes", "timestamp_ns":"optional exact signed integer" }`. Other identifiers are ignored. Supplied gateway timestamps retain their original precision but are not declared synchronized; otherwise agent receive time is used. SocketCAN applies an ID filter, detects kernel queue overflow and rejects remote/error frames with an explicit gap. SocketCAN acquisition is compiled, not hardware qualified.

Recorded fields include `can_id`, `can_extended`, original `can_data`, `dbc_range_ok` and `decoder_sha256`. Unscaled integers stay exact decimal strings. Scaled signals become float64, inactive alternatives are null, and declared range violations remain visible. The source snapshot retains normalized selected decoding lines and SHA-256. Native ingestion requires every decoded event's hash to match that snapshot. Raw payloads remain available to investigate decoding errors.

## Read-only Modbus TCP

Edit `examples/protocols/modbus.json` for the device's published register map. Addresses and field offsets are zero-based. Only function 3 (holding registers) and 4 (input registers) are accepted. No write operation exists.

```sh
./start.sh modbus-schema --config examples/protocols/modbus.json --clock modbus-receive > modbus-source.json
./start.sh api POST /api/v1/projects/PROJECT/sources --body modbus-source.json
./start.sh agent --project PROJECT --source SOURCE --schema SCHEMA --clock modbus-receive --connector modbus --modbus-config examples/protocols/modbus.json --spool ./modbus-spool
```

One request reads 1–125 registers. Choose explicit big/little word and byte order per field; supported types are signed/unsigned 16/32/64-bit integers and IEEE float32/float64. Fields use decimal-string scale/bias. Unscaled integer outputs are exact strings, derived values are float64. Nonfinite IEEE values become null with `modbus_valid=false`. Every event retains network-order raw register hex, function, unit, start address and decoder hash. Source snapshots retain the mapping, excluding connection address/timing. Transaction IDs, MBAP protocol/unit/length, function and byte count must match; invalid replies are discarded, then reconnection marks a gap. Polling is 50 ms–1 hour, timeout 50 ms–60 seconds. Timestamps describe agent receive time, not device acquisition time. Modbus TCP has no built-in authentication/encryption here; configure the adapter on a trusted device network.

## Trusted process adapters

Use `--connector extension --extension-manifest /absolute/manifest.json`. A manifest has format `tracework.agent-extension.v1`, name, absolute `command` array, `files` array of `{path,sha256}`, `timeout_seconds` and `max_memory_mb`. Pin every locally supplied script/executable (up to 16 files of 16 MiB each). The process receives one JSON context line on stdin, emits values objects on stdout, and gets only PATH/LANG in its environment. Server credentials are not forwarded. Each stdout line is capped at 64 KiB and stderr at 64 KiB. Runtime limits bound address space, CPU, descriptors and elapsed time; the process group is terminated on timeout. Capture continues through the usual agent durability boundary.

This isolates process failure and resource use; it is **not an OS filesystem/network security sandbox**. Only install trusted host code. Use a separate OS account/container for a stronger host boundary. Pinned entry files do not pin shared libraries or the entire host environment. Project files cannot install or execute commands.

## Browser renderers

```sh
./start.sh extension-install --directory ./local-extensions --manifest examples/extensions/summary.json --script examples/extensions/summary.js
./start.sh serve --data ./tracework-data --extensions ./local-extensions
```

Installation calculates a SHA-256 pin; existing IDs require explicit `--replace`, preserving the prior directory. Scripts are bounded to 512 KiB and manifests to 8 KiB. In **Local extensions**, choose a renderer and click **Send selected signals**. The opaque-origin iframe receives at most 200 loaded observations, eight selected fields and 1 MiB per transfer. It receives original identities/timestamps/quality plus selected values, never the account token or unselected field values. CSP blocks networking/forms; sandboxing blocks parent DOM/storage. Messages requesting server actions are ignored. This is an inspection surface, not an action API or automatic project plug-in. Renderer code is publicly fetchable from the server; do not embed secrets. Browser CPU availability is governed by the browser, not agent resource limits.

Store backups include recorded decoder metadata, not host extension directories, external executables, agent spools or action-provider journals. Back those up separately. Reference renderer uses text nodes and has no external dependencies.

## Qualification and primary references

Native Go and Rust SDK examples, decoder fixtures, an independent Python Modbus peer, external-process isolation fixtures and adversarial Chromium renderer tests pass. SocketCAN hardware, actual Modbus devices, CAN FD and vendor-specific maps are unqualified. See TEST-REPORT.md for release-wide evidence.

Protocol references: [Linux SocketCAN](https://docs.kernel.org/networking/can.html), [Modbus specification](https://www.modbus.org/file/secure/modbusprotocolspecification.pdf), [CSS Electronics DBC explanation](https://www.csselectronics.com/pages/can-dbc-file-database-intro). No protocol reference code is copied into the original adapters.
