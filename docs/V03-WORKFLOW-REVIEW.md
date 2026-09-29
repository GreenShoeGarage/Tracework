# Third-batch product and visual review · v0.3

Reviewed against the Green Shoe Application Standard after v0.1–v0.3. This is a scoped bench release, not v1.0 acceptance.

## Primary workflow and first minute

The useful loop remains **define/connect → observe/record → reopen or import → inspect/replay → export evidence**. A blank project stays blank. The separate import entry now lets a reader begin with existing evidence without configuring a producer. Read-only provenance is visible, recording actions are contextual, and maintenance lives under the existing recording/timeline and project navigation rather than becoming a new dashboard.

Existing browser acceptance creates a custom materials fixture, starts capture, closes the browser and reopens preserved evidence. New acceptance imports a thermal trace, inspects exact large integers, selects a range, pauses/replays measured intervals, exports that selection and restores a trashed recording. Native recording, inspection and recovery are complete flows within the stated limits.

## Findings and resolutions

| Severity / confidence | Finding | Resolution / remaining scope |
|---|---|---|
| High / high | Imported evidence must not become a live producer destination | Separate read-only project; server rejects capture/source mutation; source definitions and provenance remain inspectable |
| High / high | A failed maintenance transaction could misrepresent preserved evidence | Full validation before import; durable new chunks before atomic index commit; ENOSPC/SQLite-full/pre-commit rollback and restart tests |
| High / high | Clear Sample Data could bypass a recording protection | Sample clearing now rejects active or protected captures; auxiliary retention/trash state is cleaned transactionally |
| Medium / high | Replay finished before rendering the final cursor | Browser test reproduced it; final render now precedes end status; interval and cross-page tests pass |
| Medium / high | Mobile navigation inherited hidden import/storage actions | Navigation footer now wraps visibly when the rail is opened; import/storage remain reachable at 390 px |
| Medium / high | Long integer readouts split into misleading digit fragments on phones; narrow inspector split floating values | One readout column on small screens, numeric value spans, stacked inspector label/value; exact payload remains available |
| Medium / high | A disabled green Record button dominated imported evidence | Disabled control now reads “Read-only” without primary-action styling; recorded views hide unrelated agent-health cards |
| Medium / high | Dialog errors could appear behind the dialog | Errors in a modal now appear in its error region without dismissing entered state |
| Medium / high | Read-only-root containers have no writable system temp directory | Import/export staging now lives in the data volume and cleans abandoned staging on restart; Docker runtime itself remains unqualified |
| Low / high | Replay status could repeatedly rewrite a live announcement | Text updates only when content changes; long-gap countdown uses whole seconds |

## Screenshot and accessibility review

Reviewed desktop replay, light/high-contrast variants, retention preview, 390 px replay and range dialog. Final screenshots are in `docs/images/v03-*`. The instrument's typography/colors and exact-evidence inspector remain consistent. Selection scope appears beside playback and in export action labels. Long labels wrap, the range form becomes one column, and no page overflow occurred in tested views. Retention preview names each candidate; purge requires a separate typed confirmation. Reversible trash does not require a redundant confirmation.

Controls use native buttons, labeled fields/selects, keyboard-operable dialogs, visible focus, and text states. Space, arrow keys and the command palette retain their meanings. Reduced-motion CSS is preserved. This is functional/screenshot review in headless Chromium, not a screen-reader audit or a WCAG certification. Browser zoom, Safari/Firefox and physical mobile devices remain qualification work.

## Architectural and roadmap reassessment

The dominant small-write issue was excessive sealed-recording files, not an absence of a columnar library. Optimize storage reduced 1,000 files to one in the disclosed fixture without changing the acknowledgment contract. A Python Arrow prototype preserved semantic content and reduced uncompressed size, but did not demonstrate faster full-event decoding. Keep JSONL; do not add a production codec without a Go/storage qualification case.

Writer serialization still blocks producers during exports/maintenance; this is a documented scaling limit. Live reconnect provides a bounded snapshot plus explicit loss; ranged recorded APIs provide deliberate historical retrieval. Automatic historical stream backfill, ongoing ingestion group commit, trigger/ring capture and a trace-cache lifetime API remain open. They are not implied by the completed v0.3 features.

Proceed to v0.4's configurable views, layout/keyboard workflow and zoom/pan. Keep v0.5 comparison/annotations separate: current plots are observation views, not findings. The new controls extend existing workflow rather than adding analytical claims. Avoid more top-level navigation. Revisit the single dense frontend module during v0.4 layout work; splitting it is worthwhile when panel state becomes configurable.
