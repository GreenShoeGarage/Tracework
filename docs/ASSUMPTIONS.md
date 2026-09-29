# Consequential assumptions and open qualification

| ID | Assumption / decision | Effect / next verification |
|---|---|---|
| A01 | First release is a personal bench instrument with a self-hosted server | Local accounts and installation-wide roles are implemented; no tenant isolation or qualified remote production deployment |
| A02 | User definitions must be sufficient without modifying code | Form/API supports generic source/event/field definitions; no fixed device model |
| A03 | A producer identifies its clock and boot lifetime honestly | Data preserves declared quality; independent time calibration remains a device concern |
| A04 | Local filesystem honors fsync and SQLite FULL commit behavior | Tested process-failure recovery; not hardware power-loss qualification |
| A05 | Capture starts at server acceptance, not an earlier edge-device timestamp | Reconnected spool observations belong to the session active at acceptance; required-recording checks and optional fixed-session binding in v0.2; unpinned agents explicitly drain into a new active capture |
| A06 | Original JSONL is a better first evidence interchange than an unmeasured columnar codec | Arrow prototype measured in v0.3; JSONL retained; see ARROW-DECISION.md |
| A07 | Native compatibility must be proven, not inferred from similar features | No `.trz`, SDK or wire-format compatibility claim |
| A08 | Platform cross-compilation is useful but does not establish runtime support | Linux x86-64 tested; ARM/macOS builds labeled experimental/untested |
| A09 | Closed browser cannot own capture | Python/agent and Go server run independently; directly acceptance tested |
| A10 | No physical devices are available for this batch | Synthetic and process fixtures labeled; hardware qualification remains open |

Observations are raw data. Interpretation, severity/confidence findings, authored registers and baselines are available in Investigate. They retain original citations without automatically inferring diagnostic conclusions.
