# Repeatable evidence checks

Choose a stopped recording, open **Checks**, and add rules for explicit source/schema/field identities and inclusive original ordinal windows. The native Go engine also serves the Python SDK, pytest plugin and headless CLI. It never executes code from a rule file. Saved rules use revisions; stale saves are refused. Every run retains its own rule snapshot and endpoint/witness citations with original envelopes and SHA-256 hashes.

Five predicates are available: `all_between` (every valid sample within inclusive exact limits), `any_between` (at least one), `equals` (every valid sample equals a typed JSON value), `transition` (adjacent uninterrupted valid samples move from one typed value to another), and `settles` (the final continuous in-range interval lasts at least `hold_ns`). This proves conditions on observed samples, not on unsampled physical behavior.

Every rule declares `time_basis` (`receive` or `source`), `min_samples`, `min_span_ns`, positive `max_gap_ns`, and separate `gap_policy`/`null_policy` (`insufficient`, `fail`, or `allow`). A discontinuity includes recorded gaps, reboots, receive-time staleness, regressing selected time, or a selected-time interval beyond max_gap_ns. Transition adjacency and settling intervals always break at gaps or nulls, including under allow policies. Source-time rules retain original synchronization uncertainty; source clocks are not silently aligned.

Insufficient valid samples, missing window endpoints, or an inadequate observed span cannot pass. Missing coverage takes precedence over a predicate result; an explicit fail coverage policy yields failure. A run fails if any rule fails; otherwise it is insufficient if any rule lacks evidence. Empty evidence never passes. Invalid rules are request errors, not test passes. Limits: 20 rules, at most 10,000 original events per rule window and 32 MiB per evaluated trace. Reports include the applied policies and interpretation limits.

CLI: `TRACEWORK_URL=http://127.0.0.1:8080 bin/tracework-linux-amd64 check --project ID --session ID --rules checks.json --junit result.xml --html result.html`. Exit zero means every rule passed; nonzero indicates a failed/insufficient run or request error. The optional rules file evaluates that snapshot without changing saved rules.

Python: `PYTHONPATH=sdk/python python3 -m tracework_checks --project ID --session ID --junit result.xml --html result.html` (exit 0 pass, 1 fail, 2 insufficient). In code use `Checks().run(project, session, document)` and `Checks.assert_pass(run)`; distinct CheckFailure and InsufficientEvidence exceptions prevent missing coverage from masquerading as success.

Pytest: load `-p tracework_pytest`, supply `--tracework-project` and `--tracework-session`, and use the `tracework_checks` and `tracework_recording` fixtures. The plugin requires your existing pytest installation; the core client has no third-party dependencies. Example:

```python
def test_recorded_contract(tracework_checks, tracework_recording):
    project, session = tracework_recording
    tracework_checks.assert_pass(tracework_checks.run(project, session))
```

JUnit reports use failure for an observed/policy failure and error for insufficient evidence; both make CI fail. HTML is a standalone printable document with typed rule snapshots and exact cited observations. JSON exports preserve every result field. Trashing/purging a source recording never edits an earlier retained result; citations explicitly represent the earlier evaluation snapshot.

History lists return the most recent 100 runs within a 16 MiB aggregate body budget; retained older runs remain accessible by ID through `/api/v1/check-runs/{id}`. Each complete single result remains available even if it exceeds the history budget.
