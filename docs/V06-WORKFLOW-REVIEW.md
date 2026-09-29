# v0.6 workflow review

Reviewed the cumulative Observe → Record → Investigate and Discover → Review → Arm → Verify workflows after the third batch since v0.3. Investigation retains source evidence and separates authored findings. Actions uses a dedicated view, shows provider availability and ownership, and separates parameter entry from arming. A canceled target and a successful target have separate persisted history entries. Unknown outcomes block another action until a target result or explicit reconciliation.

Corrections made during review: mobile forms shrink within a 390 px viewport; opening a recording or returning to Live exits secondary workspaces; guide text describes delivered investigation/action features; result retries tolerate JSON whitespace changes; dispatch verifies the original lease, even if another operator subsequently obtains control. Busy detection fails closed on database read errors.

Verification: five action API workflows, three Chromium action workflow checks and the earlier seven investigation browser checks. The action browser used a separate Python provider process and verified the actual software counter file after success and cancellation. No browser errors were observed. Desktop/mobile screenshots are under `docs/images/v06-*`.

Scope decision: retain one explicit arming gate for every action; do not add a general scripting terminal to the workbench. No hardware qualification is implied by the software-counter fixture. Continue with deterministic evidence checks, accounts/recovery, then bounded integrations. Review again after v0.9.
