# ADR-0001: Secure self-update from the Admin Console

Status: **DRAFT / PARTIALLY IMPLEMENTED**. See the "Implementation status" section
at the bottom — this document specifies the target architecture; only the
pieces marked DONE exist in this branch.

## Problem

The current Modules page (`web/admin/src/components/Modules.tsx`) shows
per-module "Version" and "Update" affordances that do not correspond to
anything real: Orvix ships as a single signed bundle
(`orvix-enterprise-mail-<version>-linux-amd64.tar.gz`), not independently
versioned modules. There is no code path today that lets an admin apply an
update from the browser — `internal/updater/updater.go` is a per-module stub
(`CheckForUpdates(moduleID, ...)`) that was never wired to anything real and
must not be built on for this feature.

We need a real, safe path from "admin clicks Install Update in the browser"
to "the box is running the new signed release, or it rolled back
automatically" — without the web/API process ever gaining the ability to run
arbitrary shell as root.

## Architecture

```
 Browser (admin SPA)
   │  HTTPS, session cookie, CSRF token, recent-MFA gate
   ▼
 orvix (existing process, unprivileged service user)
   │  internal/api/handlers/updates.go — validates request, RBAC, CSRF,
   │  writes/reads job rows via internal/selfupdate (job store)
   │  NEVER execs a shell string built from request input
   │
   │  narrow structured protocol over a Unix domain socket
   ▼
 /run/orvix/updater.sock (root:orvix-update, mode 0660)
   │
   ▼
 orvix-updater.service (root, separate binary, separate systemd unit)
   - only this process may exec release/upgrade.sh / release/install-public.sh
   - only accepts a closed set of operations (Check, Preflight, Install,
     Rollback, Status) with typed, validated parameters — no raw strings
   - resolves versions only against the official GitHub repo
     (github.com/reachfm/orvix releases API), never an admin-supplied URL
   - downloads only the asset name it derives itself from the resolved tag
     (orvix-enterprise-mail-<version>-linux-amd64.tar.gz + sidecars)
   - verifies SHA256, Ed25519 (release/trust/orvix-release-signing.pub.pem),
     manifest.json, and cross-checks version across tag/filename/
     BUILDINFO/manifest before anything is executed
   - drives the existing, already-tested release/upgrade.sh for the actual
     file replacement + rollback (this ADR does not reimplement that logic,
     it gates and orchestrates it)
```

The web process and the updater process communicate only via a fixed set of
Go structs (`internal/selfupdate/protocol.go`) serialized as JSON over the
socket — there is no shell string, no free-form path, and no free-form URL
anywhere on that wire.

### Why a separate daemon, not an in-process privileged helper

`orvix` (the main server) runs as an unprivileged service user and must stay
that way — it terminates untrusted SMTP/IMAP/JMAP/webmail traffic. Giving
that process root, even briefly, to run an upgrade would put every unrelated
vulnerability in the mail-handling code on a path to root. A separate,
minimal, root-owned daemon with a narrow protocol and no network-facing
surface of its own (only a local socket) keeps the privilege boundary where
it belongs.

### Job model

One active job at a time (`internal/selfupdate.Store`, `SQLite`/`Postgres`
via the existing `dbdialect` abstraction, same pattern as
`internal/billing`). Phases per the spec: `queued → checking → downloading →
verifying → preflight → backing_up → stopping_service → migrating →
replacing_runtime → restarting → health_check → completed | failed →
rolling_back → rolled_back`. Idempotency key required on `POST
/install`/`/rollback` so a retried browser request cannot start a second
job. Job rows are the source of truth the UI polls, so a browser refresh or
even an updater-daemon restart mid-job does not lose progress — the daemon
re-attaches to the persisted job on start and resumes from the last durable
checkpoint.

## Threat model

| Threat | Mitigation |
|---|---|
| Command injection via version/channel string | Version strings are validated against `^\d+\.\d+\.\d+(-[a-z0-9.]+)?$` before touching anything; the updater never builds a shell string from user input — it calls `exec.Command` with a fixed argv and passes the version as a single argv element, never interpolated into a shell |
| Arbitrary file/path access | Updater never accepts a path from the API. All paths (backup dir, install dir, bundle cache) are compiled-in constants |
| SSRF via attacker-supplied URL | Updater never accepts a URL. It always calls the GitHub Releases API for `reachfm/orvix` (hardcoded owner/repo) and only ever downloads the asset name it derived itself |
| Malicious/unsigned release | SHA256 + Ed25519 signature (against the committed `release/trust/orvix-release-signing.pub.pem`, never a key supplied over the wire) + manifest.json cross-check are all mandatory before extraction; any failure aborts before any file is touched |
| Downgrade attack | `Install` rejects any target version `<=` currently installed version (semver compare). Downgrade is only reachable via `Rollback` to a previously-verified local snapshot that was itself created from a real prior install |
| Replay / duplicate submission | Idempotency key on `POST /install` and `/rollback`; a repeated key with an in-flight or completed job returns the existing job instead of starting a new one |
| Concurrent update jobs | Store enforces a single non-terminal job row via a unique partial index / application-level check-then-insert under a transaction, mirroring the single-subscription-per-tenant invariant pattern already used in `internal/billing` |
| Compromised browser session | Install/Rollback require CSRF token + a recent re-authentication (password or MFA) timestamp, not just an active session cookie, before the updater will accept the request |
| Secret leakage in logs | The signing private key never leaves GitHub Actions Secrets and is never present on the host at all; job event logs are sanitized (no env dump, no raw HTTP bodies) before being shown in "View Sanitised Logs" |
| Updater crash/reboot mid-update | Job state and the last durable checkpoint are persisted to disk/DB before each irreversible step; on restart the updater daemon inspects the persisted job and either resumes health-checking or triggers rollback rather than silently dropping the job |

## Implementation status (this branch, `feature/admin-console-secure-self-update`)

DONE:
- This ADR
- `internal/selfupdate` package: job/phase types, protocol types, and the
  security-critical bundle verification core (SHA256 + Ed25519 + manifest +
  version-consistency cross-check), with unit tests covering the malicious
  inputs in the threat model above (bad checksum, bad signature, tampered
  manifest, version mismatch, downgrade rejection, malformed version string)

NOT DONE in this pass (tracked, not implemented — see final report to user
for the authoritative list):
- `orvix-updater` root daemon binary + Unix socket server + systemd
  unit/socket + hardening directives
- Job persistence store (SQLite/Postgres) and single-active-job enforcement
- `internal/api/handlers/updates.go` HTTP endpoints, RBAC/CSRF/re-auth wiring
- Admin SPA Updates page (replacement for Modules.tsx)
- Preflight checks, automatic rollback orchestration, backup snapshotting
- Installer/upgrade.sh integration for the updater's own assets
- Full test matrix (API security tests, browser E2E, install/upgrade
  smokes for the updater service)
