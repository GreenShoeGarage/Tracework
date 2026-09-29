# Panels, saved tabs and portable layouts · v0.4

A workspace organizes views of a project's observations. It is stored separately from schemas and evidence. Editing a workspace never changes a recorded value, timestamp, source identity, export range or recording. Imported read-only projects can have editable layouts while their evidence remains read-only.

## Build a view

Choose **+ Panel**, select a type, give it a title and choose its signals. Use **Workspace** to rename, create, duplicate, remove or reorder tabs and choose one, two or three desktop columns. All panels in a tab share the existing event cursor. The default Signals & plot, Event ledger and Logs tabs preserve the earlier workflow.

| Panel | What it shows | Controls and boundaries |
|---|---|---|
| Values | Latest observation for each signal at or before the cursor, unit and availability | Optional decimal formatting affects floats only; integers remain exact decimal strings |
| Signal table | Signal/source, exact value, unit, original source time and state at the cursor | Independent text filter; signal/source/value/time sort and reverse; integer pairs use BigInt, mixed numeric types sort as text |
| Numeric plot | Numeric signals on separate scales over server receive time | Linear, step or points; three palettes; automatic or fixed Y limits; zoom/pan on the loaded page |
| State timeline | bool, enum and string observations, labels and observed intervals | Gaps, nulls, restart boundaries, stale intervals and receive-clock regressions break coverage; no extension beyond the last sample |
| Raw events | Up to 100 matching events at or before the cursor, plus the full selected envelope | All sources or bound source/schema versions; text filter; select an event for exact inspection |
| Logs | Up to 200 matching messages at or before the cursor | All sources or bound source/schema versions; text/level filters; Inspect jumps to the original event |

Filters, sorting, sizes, bindings, line styles, axis bounds, tab names and selected tab are saved on the server. Sidebar selection, theme and global view preferences remain browser settings. Plot windows are temporary per-tab inspection state and reset on project/recording/page changes or reload. Use Select range for persisted-recording retrieval and export boundaries.

## Binding modes

| Mode | Resolution | Reuse behavior |
|---|---|---|
| Follow signal browser | Current selected fields, up to eight | Changing the sidebar changes this panel |
| Pin exact IDs | Source ID, schema ID, revision and field ID, with type/unit/enum agreement | Stable identity; a different project's IDs remain unresolved |
| Portable named paths | Exact source name / event name / revision / field name, then type/unit/enum agreement | Resolves matching definitions even when generated IDs differ |

Portable names are case-sensitive and require exactly one candidate. Duplicate paths, missing fields, changed versions, types, units or enum labels produce visible unresolved-binding messages. No source is chosen arbitrarily and no unit conversion is implied. Rename/revise and explicitly configure bindings when required. A panel with raw/log scope **All sources** intentionally includes the entire loaded page, irrespective of its signal references; select the bound-sources scope to restrict it.

Each panel supports at most eight bindings; different panels can use different fields. A workspace supports 1–8 tabs, 0–12 panels per tab, 1–3 columns, panel heights 180–900 px and a total serialized layout of 256 KiB. Panel titles/tab names share the project's bounded name rules. Filters allow 512 UTF-8 bytes. Large enum definitions can reach the total layout limit before the panel count limit.

## Reorder, resize and navigate

- Drag a panel's grip onto another panel, or use its **↑ / ↓** buttons to move earlier/later. The underlying reading order follows the same order.
- Drag the bottom edge, or focus that edge and press **↑ / ↓** for 20 px steps. **Home / End** choose minimum/maximum height. Width is set in Configure panel and capped by the tab's column count. Panels stack on phones.
- Use **Undo layout** to recover up to 15 structural edits during this project/browser session, including removal or import. Filters, sort changes and tab selection do not create undo entries. Export a layout for a lasting backup.
- Focus a tab and use **← / →**, **Home / End** to switch tabs. Ctrl/Cmd K includes Add panel and Workspace tabs. Standard Tab navigation reaches configuration and movement controls.
- Plot **+ / −**, **← / →** and **Fit page** provide keyboard-accessible zoom, pan and reset. Choose Inspect, Zoom selection or Pan before pointer gestures. Inspect seeks the nearest observation from the clicked lane's source/schema revision.

Zoom/pan uses the loaded event page, not the complete recording. It does not alter Select range or exports, and is not a source-clock alignment operation. Page changes reset the view. Receive times and source times remain exact strings; rendering uses relative numeric coordinates. Integer plots subtract an exact BigInt baseline before converting deltas to display coordinates. Fixed Y bounds apply to these deltas for integer lanes and to values for float lanes. Clipped values remain available in the inspector, raw panel and exports. Float display rounding never rewrites the payload.

No plot decimation, area fill, per-signal style overrides, two-cursor measurements or zoom history stack is implemented. Fit page resets the window. State colors repeat after four labels; the key shows up to twelve distinct labels, and event inspection retains the complete value.

## Save, conflict recovery and portability

Changes autosave after a short debounce. Check **Layout saved on server** before closing. The server uses an expected revision; a stale browser cannot silently overwrite another browser's newer layout. There is no automatic merge or real-time collaborative editing.

If a save fails or another client has edited the layout, a persistent warning offers **Review local / saved layout**. Export the local recovery copy, load the last saved layout, or explicitly keep the local layout on the server. Keeping local reads a fresh revision and still refuses a conflicting intervening write. Loading saved discards the unsaved view only.

The browser retains a draft before sending changes. Recovery is isolated by browser instance and survives same-tab reloads when browser storage is available. Storage restrictions, browser cleanup, tab closure or loss of the browser profile can prevent recovery; export the local copy while it is available. A server save confirmation is the durable copy. Browser drafts are not an evidence backup.

**Export exact workspace JSON** preserves binding modes. **Export portable workspace JSON** converts all panels to named paths and freezes the current sidebar selection for panels that follow it. Import into another project from Workspace; review unresolved bindings. Import validates the complete JSON before replacing the saved workspace and offers Undo during the current session. It imports views only, with no executable code, sources or observations.

Workspace JSON format is `tracework.workspace.v1`. This is separate from project-configuration JSON and native trace ZIP. Those exports do not include layouts. A stopped-server copy of the entire data directory includes saved workspaces. UI “workspace” here means a project's saved views, not accounts, roles or an organization permission boundary.

## Upgrade

Back up the complete stopped data directory before upgrading. Metadata versions 1–3 migrate to version 4, including a pre-v0.4 SQLite snapshot before the new workspace table is created. Recording chunks remain untouched. Original v0.1, v0.2 and v0.3 executable-generated fixtures are included in release qualification. Older binaries cannot open version-4 metadata; rollback requires the complete old backup.
