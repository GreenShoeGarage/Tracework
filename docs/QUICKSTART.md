# TRACEWORK v1.0.0

1. Create a blank project and define your own source, fields, units and clock domain.
2. Open **Connect source**. Download a Serial JSONL, MQTT or stdin agent configuration, or copy the Python example.
3. Start **Record**, then run the producer/agent. Agents buffer until recording is active by default. The browser can close while capture continues.
4. Stop capture and select a recording to inspect exact events, timestamps and schema versions. Export the full trace ZIP for evidence.
5. After a server restart the old capture is interrupted. Start a new recording to drain an unpinned agent buffer.

[Connector/reconnect guide](CONNECTORS.md) · [Data contract](DATA-CONTRACT.md) · [HTTP and streaming API](API.md). Complete installation, backup and platform instructions are in README.md in the package.

## Reopen, replay, and share part of a trace

Use **Import trace ZIP** to open existing native evidence, or select your own stopped recording. Choose **Select range**, set inclusive ordinals (or use the current cursor), and apply. **Play** follows receive-time intervals; select a speed or pause/step. **Export** labels whether you are exporting a selection or the complete recording and includes every matching page.

**Manage recording** protects evidence or optimizes stopped recordings. **Storage & retention** previews older recordings before moving them into recoverable trash. Nothing is automatically deleted. Restore from trash, or separately confirm permanent deletion. Read the [storage and recovery guide](STORAGE-REPLAY.md) before upgrading an existing data directory.

## Arrange an investigation view

Choose **+ Panel** for a plot, values, table, state timeline, raw events or logs. Choose **Workspace** to create a saved tab and set its columns. Pin source IDs for a fixed instrument, or use portable named paths for matching projects. Move with the panel arrows or grip; resize with the bottom edge or its arrow-key controls. Plot zoom/pan affects the loaded page; Select range controls recording exports. Check the server-save indicator. See [Panels and recovery](WORKSPACES.md).

## Compare, check, operate and administer

**Investigate** selects explicit trace aliases, aligns them without changing original clocks, measures two observations and retains cited findings. Freeze a reviewed baseline and download a printable report. Save/recovery/file controls are grouped beside the report.

**Checks** evaluates stopped recordings with explicit ordinal windows and coverage policies. Pass, fail and insufficient evidence stay distinct. Download result JSON, JUnit or HTML.

**Actions** requires an online registered provider, an ownership lease and explicit arming for each prepared command. Unknown outcomes block further commands until reconciled. Start with the software-only example in ACTIONS.md.

**Access** configures accounts/roles, scoped links and backups. **Local extensions** displays explicitly installed renderers and sends only your selected loaded observations when you click Send. Read the linked integration guide before installing host programs.
