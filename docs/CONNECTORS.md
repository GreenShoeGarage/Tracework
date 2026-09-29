# Acquisition and reconnect guide · v0.3.0

Run agents on the computer attached to the device or broker. Run the server where recordings should live. The browser can close independently of either process. One agent process/spool serves one configured source/schema revision; run multiple processes with separate spools for multiple sources. This release observes data only; it has no device commands.

## Configure without code changes

Create your project and source in the workbench. **Connect source** lets you choose any source and schema revision, select Serial JSONL, MQTT, or stdin, and download `tracework-agent.json`. Run from the package directory:

```sh
export TRACEWORK_URL=http://127.0.0.1:8080
# If the server requires one: export TRACEWORK_TOKEN='your configured token'
./start.sh agent --config tracework-agent.json
```

For stdin, append `< my-values.jsonl` or pipe a producer into that command. Configuration contains source/destination settings, not passwords. Explicit command flags override config fields. `./start.sh agent -h` lists all flags.

## Input and clock provenance

- `input: "values"`: each input is one JSON object matching the selected schema's fields. The agent assigns a new boot lifetime and increasing sequence, timestamps receipt using the acquisition computer's Unix clock, and declares it unsynchronized. Configure that source's clock domain accordingly; this is not device time.
- `input: "envelope"`: each input is a full native producer envelope as described in DATA-CONTRACT.md. Source, schema ID, revision and clock must match config. Producer timestamp, boot, sequence, gap and synchronization metadata survive unchanged. Omit server-assigned IDs, receive timestamps, ordinal, flags and session ID.
- Input is capped at 64 KiB; wrapped spool envelopes must also fit 64 KiB. Blank Serial/stdin lines are ignored. Invalid input stops acquisition with stderr detail; it is not acknowledged as a successful observation. Pending valid envelopes remain on disk.
- A producer must coordinate sequences across all event schemas of a source, or use separate source IDs. Native envelope producers own their coverage/gap declarations. Value adapters mark gaps after detected reconnect or spool pressure, but cannot count unknown missing hardware samples.

## Recording intent and restart

`recording: "required"` is the default for agents. A buffered event is deleted only after a matching server receipt. When no session is active, the agent holds observations, reports **waiting recording**, and retries. Start a recording in the workbench to drain it. Buffered source timestamps remain original; receive time and recording assignment occur when accepted.

A server restart seals the old active recording as **interrupted**. Start a **new recording** explicitly; the default agent drains its pending observations into it. It never silently turns them into volatile live-only events. This is an acceptance-time capture policy, not a promise that every observation originated within the session's start/end times.

For a buffer that must belong to one recording, set `session_id` or `--session SESSION_ID`. A different active recording cannot drain that buffer. A stopped/interrupted session cannot resume in v0.3; preserve the pinned spool for explicit later recovery. Do not rewrite its binding or silently reassign its pending observations. Choose the default unpinned policy if continued capture into a new session is intended.

Use `recording: "live"` / `--recording live` only for deliberate live monitoring. Server acknowledgment then permits volatile payloads when no capture is active. A retry that was originally accepted live-only cannot later be upgraded to recorded evidence.

## Durable spool and backpressure

Each spool has an exclusive process lock, persistent agent ID, and immutable destination/capture binding. Use a new spool when changing server, project, source, schema revision, input mode, connector endpoint, transport or recording policy. Retain the old spool until its pending evidence has been handled. Upgrading a v0.1 spool preserves its original destination binding and pending envelopes; v0.2 adds a policy binding.

An observation is durable locally after its pending file is written, fsynced, renamed and directory-fsynced. The sender retries the same identity and removes the pending file only after server acknowledgment and another directory fsync. Process death after server commit but before removal is resolved by native deduplication. `.tmp` files are never treated as acknowledged observations or automatically promoted. Preserve them when investigating a crash.

The default spool payload limit is 64 MiB (`max_spool_mb` / `--max-spool-mb`), with a hard 10,000-pending-file limit. At either limit, input blocks until space becomes available. Serial device/driver buffers may overflow; TRACEWORK cannot guarantee lossless acquisition when a device cannot stop. MQTT delivery depends on publisher QoS, broker persistence, queue limits and session retention. Use native envelopes to recognize redelivery. This is a bench recorder; chunk/spool aggregation and broader throughput qualification are v0.3 work.

Agent health reports a persistent ID, associated source, connector, state, pending count/bytes and explanation. Heartbeats run about every two seconds; the workbench marks an agent offline after six seconds without one. Transport timeouts may lengthen this cadence. Source **receiving/disconnected** is based on actual accepted observations and its configured stale interval, independently of agent reachability. Maximum 128 agent identities per project; agent deletion/retirement UI is not yet implemented.

## Serial JSONL

```sh
./start.sh agent --project PROJECT --source SOURCE --schema SCHEMA --clock acquisition-host-clock \
  --connector serial --serial-device /dev/ttyUSB0 --serial-baud 115200 --spool ./serial-spool
```

Serial framing is newline-delimited UTF-8 JSON, 8 data bits, no parity, one stop bit (8N1), without configured flow control. Use a stable device path such as a user-managed symlink when available. The connector retries the configured path about once per second. A partial line at disconnect is discarded with visible status; the next value-only event reports a gap. Native producers retain their own gap metadata. Device permissions and drivers must already be configured on the host.

Linux pseudo-terminal tests cover line parsing, disconnect, a replacement device at the same path, discarded partial input and gap flags. **No physical adapter, sensor or macOS serial port was available for qualification.**

## MQTT 3.1.1

```sh
export TRACEWORK_MQTT_USERNAME='optional user'
export TRACEWORK_MQTT_PASSWORD='optional password'
./start.sh agent --project PROJECT --source SOURCE --schema SCHEMA --clock device-clock \
  --connector mqtt --mqtt-broker tcp://127.0.0.1:1883 --mqtt-topic lab/observations \
  --input envelope --spool ./mqtt-spool
```

The connector uses a persistent client ID derived from the spool's agent ID, clean-session false, a QoS 1 subscription and manual acknowledgments. A delivered QoS 1 message is acknowledged **after local spool fsync**, before server recording acceptance. A broker redelivery with the same native source/boot/sequence/payload is stored at most once by the server. A conflicting payload at that identity is rejected. Value-only MQTT mode assigns a new agent identity per delivered message; broker redelivery can therefore produce duplicate observations, especially across crashes. Native envelopes are recommended.

A publisher's QoS 0 remains best effort even with a QoS 1 subscription. This connector does not promise end-to-end exactly-once acquisition. Retained messages are ignored and acknowledged by default; opt in with `mqtt_retained: true` / `--mqtt-retained`. Retained values do not prove a current measurement. Native timing metadata is especially useful when accepting them.

Use `ssl://broker:8883` for TLS with system trust roots, or `mqtt_ca` / `--mqtt-ca` for your PEM trust root. Passwords belong in the two environment variables above, never the broker URL/config. MQTT v5, WebSocket broker connections, client-certificate authentication and device discovery are not implemented. Incoming MQTT packets are bounded to 128 KiB before payload allocation; observation limits remain 64 KiB. The ordered handler propagates spool pressure into the transport; a broker can disconnect a stalled subscriber and must retain queued QoS 1 messages according to its own policy.

The release tests use a real TCP MQTT 3.1.1 protocol fixture for subscribe, durable PUBACK, redelivery, reconnect and retained-message handling. Broker-product interoperability and production broker persistence behavior remain unqualified.

## gRPC acquisition

HTTP ingestion remains available. Enable native gRPC separately:

```sh
./start.sh serve --data "$PWD/tracework-data" --grpc-listen 127.0.0.1:8082
# Agent flags, in addition to its usual source/spool configuration:
./start.sh agent --config tracework-agent.json --grpc-address 127.0.0.1:8082 --grpc-plaintext
```

For a non-loopback gRPC bind, the server requires a token of at least 24 characters plus `--grpc-cert CERT.pem --grpc-key KEY.pem`. Clients use TLS by default and system trust, with `--grpc-ca CA.pem` for a private trust root. `--grpc-plaintext` is restricted to numeric loopback addresses. A TLS/CA fixture passes; this does not qualify a particular LAN deployment or certificate management system.

`protocol/tracework-v1.proto` and checked-in generated Go bindings define running `Acquisition.Publish` (bidirectional stream) and `Acquisition.Subscribe` (server stream). Publish accepts the same strict JSON batch contract inside Protobuf bytes, preserving decimal integer/timestamp strings and shared validation/durability guarantees. It supports multiple request/receipt pairs per stream. Authorization uses gRPC metadata `authorization: Bearer TOKEN`. Maximum request size is 4 MiB. The agent still sends health heartbeats to `TRACEWORK_URL` over HTTP/HTTPS; configure that endpoint alongside gRPC.

The Python standard-library SDK remains HTTP based. It accepts `schema_version`, `require_recording` and optional `session_id` arguments; no grpcio dependency is required. Default options keep compatibility with v0.1 servers. Generated workbench snippets explicitly require recording.

## Browser streaming and display gaps

The workbench authenticates a same-origin WebSocket with the token in its first frame, not in a URL. It receives a bounded snapshot, event deltas, source states and agent health. Frames are coalesced and carry a server instance ID, cursor and `lost_before` flag if a connected subscriber fell behind an evicted buffer. Slow writes time out; reconnection begins from a fresh bounded snapshot. Historical gaps across reconnect are not automatically backfilled into the live view: use the recording for complete persisted evidence. There are at most 16 live subscriptions shared across WebSocket/gRPC. Project/session/status metadata refreshes every two seconds; observation delivery is streamed. Pause freezes the view while acquisition continues.

## Upgrading from v0.1

Stop the old server and all agents. Back up the entire server data directory and every agent spool. v0.3 preserves native project/trace v1 event files and migrates metadata v1/v2 to v3. Automatic SQLite snapshots do not replace a full backup. Existing pending agent envelopes and spool policy bindings remain unchanged. See [Storage, replay and recovery](STORAGE-REPLAY.md) for tested migration/rollback behavior and staging/retention semantics. Actual v0.1 and v0.2 executable fixtures preserve original events and chunk hashes through migration.
