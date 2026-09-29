# Local access, sharing, audit and recovery

A new loopback installation has no required account. **Access → Create first administrator** enables account sign-in. The original `TRACEWORK_TOKEN` remains an optional installation-wide administrator credential and the recovery route when an administrator loses a password. A non-loopback listener still requires a token of at least 24 characters. Deploy HTTP behind an HTTPS reverse proxy on a trusted network; the embedded HTTP listener does not itself terminate TLS. Remote gRPC requires its own TLS certificate/key. Do not forward raw HTTP over an untrusted network.

| Role | Allowed scope |
|---|---|
| Viewer | Read all installation projects, recordings, investigations, check results and action history; compare/measure; export evidence. Temporary browser layouts/filters do not save to shared state. |
| Operator | Viewer capabilities plus define/ingest/capture, save layouts and investigations, run checks, register providers, acquire control and arm/cancel/reconcile actions. |
| Admin | Operator capabilities plus accounts, shares, audit, consistent backup, retention and permanent deletion. |

Roles cover the entire installation. Project workspaces are saved views, not tenant isolation or private per-user projects. Use separate installations when collaborators must not see each other's projects. Enforcement is on HTTP endpoints, each gRPC publish batch and ongoing WebSocket/gRPC subscriptions. Streams recheck credentials as they emit; disabling an account, changing its role/password or signing out revokes its sessions. In-flight requests may finish. Provider credentials are separate, restricted to their own heartbeat/command/outcome transport. Ownership leases also bind to the authenticated account; knowing another account's lease token does not transfer control.

Account names use 1..64 letters/digits/underscore/hyphen and are case-insensitive. Passwords require 12..1024 bytes. Password derivation uses Go's standard PBKDF2-HMAC-SHA-256 at 600,000 iterations with random 32-byte salts; derived keys and bearer credentials are compared in constant time. Only bearer-token hashes are stored. Browser credentials remain in sessionStorage for that tab, not URLs or cookies. At most 10 sessions per account, each 12 hours. Login is limited to 30 attempts per directly connected IP in 15 minutes and two concurrent derivations. Behind a reverse proxy this IP limit is shared by its clients; forwarded headers are not trusted. Account administration resets passwords; the last enabled administrator cannot be disabled or demoted. These are local access controls, not an independently audited identity platform or a certification claim.

Implementation references: [Go crypto/pbkdf2](https://pkg.go.dev/crypto/pbkdf2), [OWASP password storage guidance](https://cheatsheetseries.owasp.org/cheatsheets/Password_Storage_Cheat_Sheet.html), checked 2026-09-28. Building now requires Go 1.24 or later; tested compiler versions appear in the release test report.

## Scoped evidence links

An administrator can create a 1..168 hour bearer link to one stopped recording (native ZIP) or one retained check run (HTML evidence report). The token is shown once and stored only as a hash. The share page takes the credential from its URL fragment, removes that fragment from the visible URL and sends the credential only in an Authorization header to the scoped endpoint. It cannot access project lists, live streams, unrelated evidence or writes. The administrator can revoke it immediately for future downloads; downloaded copies remain outside the server's control. No link is sent automatically. Source recording trash/purge makes its share unavailable. Share links require access to the same local server; localhost links do not become internet-hosted sites.

## Audit history

Administrative and ordinary authenticated HTTP mutations record a durable intent before the handler runs and an outcome afterward. Request IDs pair the two; status 0 is an intent. If outcome logging fails after a completed operation, the intent remains and server logs identify the missing completion. Verify actual state rather than inferring a result from an unmatched intent. gRPC publish batches record intents and successful acknowledgments. Login and scoped-download access are also recorded. Provider outcome history separately retains full action state; polling heartbeats do not flood the audit log.

Audit entries exclude request bodies, passwords and bearer tokens. Hash links and SQLite update/delete-blocking triggers detect entry changes or broken links in the returned latest 1,000 entries. This is not an externally anchored tamper-proof ledger: a database owner can replace the whole store. Audit/metadata uses disk in addition to the trace-chunk quota; monitor free space. A full backup retains all audit rows.

## Consistent backup and restore

Use **Access → Download consistent backup**. The server pauses its mutation/capture lock while it takes a SQLite snapshot and copies all referenced immutable chunks. The ZIP includes a versioned manifest with per-file SHA-256 hashes. It includes all projects, configuration, exact evidence, layouts, investigations, rules/results, account credential hashes, provider credential hashes and audit history. Keep it private; it is not encrypted. Limits are 4 GiB expanded and 100,000 files. Compact stopped recordings before exceeding that file limit. A snapshot is a point in time; later accepted events are not in it.

Test restoration with:

```
tracework restore --backup TRACEWORK-backup.zip --data NEW_DIRECTORY
tracework serve --data NEW_DIRECTORY
```

The destination must not exist. Restore rejects duplicate/unsafe paths, unexpected files, oversized content, manifest/hash mismatches, unsupported metadata versions, bad SQLite integrity and missing or altered recorded chunks. It validates in a sibling staging directory and publishes by rename only after the ordinary migration/recovery path succeeds. It never overwrites a running or existing store. Active recordings become interrupted; dispatched actions become unknown, queued actions are canceled, and leases/preparations are cleared. All restored browser sessions and scoped shares are revoked. Account passwords and provider credentials remain valid. Sign in again, review actual targets and rotate credentials if the backup was exposed. Review any restored provider state against its separate on-host dispatch journal.

Restoring an old snapshot restores old account roles/passwords and evidence. Stop and isolate the old installation when replacing it so producers cannot accidentally address two servers. Keep the original directory until the restored evidence and integrations have been checked. Backups from untrusted parties are not executable extension packages and should not be used as a general SQLite import mechanism.
