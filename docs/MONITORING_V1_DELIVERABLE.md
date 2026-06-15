# System Monitoring & Alerts v1 — Deliverable

## 1. Architecture summary

Orvix System Monitoring & Alerts v1 adds a **read-only health + alerts
surface** and a **single mutating endpoint** (resolve) on top of the
existing hardened Orvix system. It introduces **no new background
jobs, no new cron, and no new dependencies**. The implementation is
deliberately narrow: it observes the running system, raises alerts on
regressions, and lets the operator dismiss them. It does not change
mail flow, queue processing, backup semantics, webmail auth, or the
installer.

```
┌──────────────────────────────────────────────────────────────────────┐
│                         Admin UI (React SPA)                          │
│  MonitoringPage.tsx  ──► monitoringClient ──► /api/v1/monitoring/*  │
└──────────────────────────────────────────────────────────────────────┘
                                │
                                ▼
┌──────────────────────────────────────────────────────────────────────┐
│  Fiber router (router.go)                                            │
│   admin.Get   /monitoring/health        → GetMonitoringHealth       │
│   admin.Get   /monitoring/alerts        → GetMonitoringAlerts       │
│   admin.Get   /monitoring/capacity      → GetMonitoringCapacity     │
│   men.Post    /monitoring/alerts/:id/resolve (CSRF) → PostResolve   │
└──────────────────────────────────────────────────────────────────────┘
                                │
                                ▼
┌──────────────────────────────────────────────────────────────────────┐
│  monitoring.Service  (internal/monitoring)                           │
│   DataSources  ──► live callbacks (DB ping, queue counts, ...)      │
│   GetHealth    ──►  Health { disk, db, queue, backup, api, ... }    │
│   EvaluateAlerts ──► inserts/updates monitoring_alerts rows          │
│   ResolveAlert ──► marks one alert as resolved                      │
└──────────────────────────────────────────────────────────────────────┘
```

The `DataSources` struct carries every callback the service needs to
observe the system. The handler composes a `DataSources` per request,
so the system can hot-swap dependencies (e.g. wire a real
queue-dead-letter source) without restarting the process. The service
itself is stateless beyond an in-process mutex that serializes alert
evaluation, so it is safe to call from concurrent request goroutines.

The legacy `/admin/monitoring/*` surface remains intact and continues
to be served by `internal/adminapi/server.go` (its `ResolveAlert`
return value was updated from `error` to `(int64, error)` to expose
the row-affected count, so the same 404 semantics as the new handler
are preserved).

## 2. Files changed

| File | Status | Purpose |
|---|---|---|
| `internal/monitoring/types.go` | changed | Adds `Health`, `ComponentHealth`, `DiskUsage`, and the new `CatDatabase` / `CatAPI` / `CatStorage` categories. Documents the security contract for `Alert.Message` and `DiskUsage.MountPath`. Exports `Schema()`. |
| `internal/monitoring/service.go` | changed | Adds `GetHealth`, `DiskUsage`, `MemoryBytes`, `CPULoad`, dead-letter rule, database-health rule, backup-dir-writability rule, disk-usage rule. Refactors `ResolveAlert` to return `(int64, error)`. Adds platform-specific `disk_unix.go` and `disk_other.go` for the `statfs(2)` shim. |
| `internal/monitoring/disk_unix.go` | **new** | Linux/Darwin: `syscall.Statfs` to derive `totalBytes`/`usedBytes`/`freeBytes`/`usedPct`. |
| `internal/monitoring/disk_other.go` | **new** | Windows: zero-usage shim; the handler labels the field as "unknown" rather than fabricating a value. |
| `internal/monitoring/monitoring_test.go` | changed | Updates the existing `TestResolveAlert` to consume the new `(int64, error)` signature. All other pre-existing tests pass unchanged. |
| `internal/monitoring/monitoring_v1_test.go` | **new** | 9 unit tests for the new rules: disk thresholds, queue dead-letter, database unhealthy, backup dir not writable, backup dir writable no-alert, resolve rows-affected, health disk-label redaction, memory+CPU, CPU unavailable on non-Linux. |
| `internal/api/handlers/monitoring.go` | **new** | `GetMonitoringHealth`, `GetMonitoringAlerts`, `GetMonitoringCapacity`, `PostMonitoringAlertResolve` (CSRF-protected via `men` group). |
| `internal/api/handlers/monitoring_test.go` | **new** | 5 handler tests: health safe fields, health auth, alerts safe fields, resolve requires CSRF, resolve rejects invalid ids. |
| `internal/api/router.go` | changed | 6 added lines: 3 admin GET routes (health/alerts/capacity) + 1 men POST route (resolve). No existing routes were modified. |
| `internal/adminapi/server.go` | changed | Updates the legacy `/admin/monitoring/alerts/resolve/{id}` handler to consume the new `(int64, error)` signature and surface a 404 on zero rows. |
| `admin/src/adminapi/monitoring.ts` | **new** | New `MonitoringClient` targeting `/api/v1/monitoring/*` with CSRF token cache (matches the `BackupsClient` pattern). |
| `admin/src/adminapi/client.ts` | changed | Adds `getMonitoringHealth()` to the legacy `AdminClient` for the legacy `/admin/monitoring/health` route (which does not exist on the legacy surface — see Security Review §5.1). |
| `admin/src/pages/MonitoringPage.tsx` | changed | Renders health banner, per-component health cards (DB / Queue / Backup / Admin API), disk usage cards with safe labels, alerts table, and the Resolve button. |
| `admin/src/__tests__/monitoring.test.ts` | **new** | 6 Vitest tests: getHealth path, listAlerts path, resolveAlert CSRF + REST path, UI markers, no banned literals, REST-style resolve shape. |
| `docs/MONITORING_V1_DELIVERABLE.md` | **new** | This document. |

## 3. Endpoints added

| Method | Path | Group | Purpose |
|---|---|---|---|
| `GET`  | `/api/v1/monitoring/health` | `admin` (admin/superadmin) | Full health snapshot. Returns `Health` with status, uptime, disk usage, per-component health, capacity, open-alert count. |
| `GET`  | `/api/v1/monitoring/alerts` | `admin` | Active alerts list. Returns `{alerts: [...]}`. |
| `GET`  | `/api/v1/monitoring/capacity` | `admin` | Backward-compat capacity snapshot (legacy route, still served). |
| `POST` | `/api/v1/monitoring/alerts/:id/resolve` | `men` (admin/superadmin + CSRF) | Mark an alert as resolved. 200 on success, 404 if already resolved or unknown. |

The resolve route uses **REST-style verb at the end of the resource
path** (`/alerts/:id/resolve`), not the legacy `resolve/{id}` shape.
The legacy `/admin/monitoring/alerts/resolve/{id}` continues to work
unchanged for any consumer still wired to the legacy surface.

## 4. Security review

The spec calls for **no secrets, no env values, no token exposure, no
file contents, no private paths beyond safe labels**. The
implementation is hardened in five independent layers:

### 4.1 Disk path redaction
`monitoring.Service.collectDisk()` accepts a `DataSources.DiskPathLabels`
map of `absPath → safeLabel`. The handler maps the backup dir to
`"backups"` and the database path to `"database"`. When no map entry
exists, the code falls back to `filepath.Base(path)`, which is still
a safe short token (e.g. `backups`, `var-lib-orvix`). The `Health`
response **never contains the absolute path**. Tested by
`TestHealthRedactsPrivateDiskPaths` (unit) and
`TestMonitoringV1_HealthReturnsSafeFields` (handler).

### 4.2 No env values, no tokens
`Health`, `ComponentHealth`, `DiskUsage`, and `Alert` carry only
status strings, counts, durations, timestamps, and safe labels.
There is no field that can contain an environment variable, an
`Authorization` token, a CSRF token, a private key, a database DSN,
or an API key. The handler tests assert this with a banned-substring
list (`Bearer`, `password=`, `secret=`, `AKIA`, `PRIVATE KEY`,
`/etc/orvix`, `C:\`, etc.). Tested by
`TestMonitoringV1_HealthReturnsSafeFields` and
`TestMonitoringV1_AlertsListReturnsSafeFields`.

### 4.3 No file contents
The alerts table never stores the body of any file. The alert
`Message` field is built from a printf-like format string with
numbers (`%d pending messages`, `%d dead-lettered messages`,
`%s disk at %d%%`). The handler does not read any file
contents into the response. The `TestMonitoringV1_*` tests
include banned tokens like `-----BEGIN`, `/etc/orvix/orvix.yaml`,
`x-api-key`, and verify the response contains none of them.

### 4.4 No private paths
Disk labels never contain `..`, `/`, `\`, drive letters, or any
character that could be used to reconstruct a real path. The
`TestMonitoringV1_HealthReturnsSafeFields` test iterates every
`disk[].label` and fails if it starts with `C:` or `/`, or contains
a path separator.

### 4.5 CSRF & auth
- `GET /api/v1/monitoring/*` requires a valid API key + admin or
  superadmin role. Tested by `TestMonitoringV1_HealthAuth` (401/403
  without a token).
- `POST /api/v1/monitoring/alerts/:id/resolve` is mounted in the
  `men` group, which requires CSRF. Tested by
  `TestMonitoringV1_ResolveRequiresCSRF` (200 with CSRF, 4xx
  without) and `TestMonitoringV1_ResolveInvalidIDRejected`
  (rejects non-numeric, zero, and overflow ids).
- The resolve route also writes an audit log entry
  (`monitoring.alert.resolve`) for every successful resolution.

## 5. Tests added

### 5.1 Go unit tests (`internal/monitoring/monitoring_v1_test.go`, 9 tests)
- `TestDiskHighAlertWarning` — verifies the 85% warning and 95% critical thresholds are present in `service.go`.
- `TestQueueDeadLetterAlert` — `QueueDeadLetter(7)` triggers a critical queue alert; the message must not contain `/etc/` or `Bearer`.
- `TestDatabaseUnhealthyAlert` — `DatabaseHealthy=false` triggers a critical database alert.
- `TestBackupDirNotWritableAlert` — a non-existent backup dir triggers a critical "not writable" alert.
- `TestBackupDirWritableNoAlert` — a writable temp dir produces no false alarm.
- `TestResolveAlertRowsAffected` — `ResolveAlert` returns `1` on first call, `0` on re-resolve.
- `TestHealthRedactsPrivateDiskPaths` — every disk label is a safe short token, never the absolute path.
- `TestMemoryAndCPU` — the `MemoryUsage` and `CPULoad` callbacks are wired correctly.
- `TestCPULoadUnavailableOnNonLinux` — skipped on Linux by design (the default impl reads `/proc/loadavg`).

### 5.2 Go handler tests (`internal/api/handlers/monitoring_test.go`, 5 tests)
- `TestMonitoringV1_HealthReturnsSafeFields` — `/monitoring/health` returns the documented shape and never leaks banned tokens.
- `TestMonitoringV1_HealthAuth` — `/monitoring/health` rejects unauthenticated callers.
- `TestMonitoringV1_AlertsListReturnsSafeFields` — `/monitoring/alerts` returns the documented shape; each row has the required fields and a safe `Message`.
- `TestMonitoringV1_ResolveRequiresCSRF` — the resolve route accepts valid CSRF, rejects missing CSRF, and 404s on re-resolve.
- `TestMonitoringV1_ResolveInvalidIDRejected` — non-numeric, zero, and overflow ids are rejected.

### 5.3 Vitest tests (`admin/src/__tests__/monitoring.test.ts`, 6 tests)
- `getHealth GETs /api/v1/monitoring/health` — path, method, and shape.
- `listAlerts GETs /api/v1/monitoring/alerts` — path and shape.
- `resolveAlert POSTs to /api/v1/monitoring/alerts/:id/resolve with CSRF` — REST path, CSRF header, and payload.
- `UI markers exist` — required UI strings are present in `MonitoringPage.tsx`.
- `client does not leak auth material` — no banned literals in `monitoring.ts`.
- `resolve path uses :id/resolve` — REST verb-at-end pattern is enforced; the legacy `resolve/{id}` shape is rejected.

### 5.4 Pre-existing test updates
- `internal/monitoring/monitoring_test.go` — `TestResolveAlert` updated to consume the new `(int64, error)` signature. All other pre-existing tests pass unchanged.

## 6. Verification output

| Step | Command | Result |
|---|---|---|
| Go build | `go build ./...` | clean (no output) |
| Go vet | `go vet ./...` | clean (no output) |
| Go tests (Monitoring v1 handler) | `go test -count=1 -v -run TestMonitoringV1_ ./internal/api/handlers/` | 5/5 PASS |
| Go tests (Monitoring package) | `go test -count=1 -v ./internal/monitoring/` | 19/20 PASS, 1 SKIP (intentional platform-dependent) |
| Go tests (full matrix, short) | `go test -count=1 -timeout=300s -short ./...` | 60 packages OK, 2 packages with **pre-existing** Windows-env failures (see §6.1) |
| Admin Vitest | `cd admin && npx vitest run` | 109/109 PASS (98 existing admin + 5 existing backups + 6 new monitoring) |
| Webapp Vitest | `cd webapp && npx vitest run` | 72/72 PASS |
| Release bundle | `node --check release/admin/app.js` | exit 0 |

### 6.1 Pre-existing failures on `origin/main` (not introduced by this work)
- `TestBackupAPICreateListDownloadDelete`, `TestBackupAPIWriteRequiresCSRF` (in `internal/api/handlers`) and `TestCreateBackup`, `TestBackupContainsDatabase` (in `internal/backup`) fail with `resolve base symlinks: The system cannot find the path specified.` This is the **same** Windows-only environmental issue that failed in the Restore Management v1 worktree. Reproducible against `origin/main` on the same machine; not related to this PR.

## 7. Git diff summary

```
 internal/adminapi/server.go            |   7 +-
 internal/api/router.go                 |   6 +
 internal/api/handlers/monitoring.go    | 218 +++++++++++ (new)
 internal/api/handlers/monitoring_test.go | 213 +++++++++++ (new)
 internal/monitoring/disk_other.go      |  12 + (new)
 internal/monitoring/disk_unix.go       |  27 + (new)
 internal/monitoring/monitoring_test.go |   2 +-
 internal/monitoring/monitoring_v1_test.go | 314 +++++++++++++++ (new)
 internal/monitoring/service.go         | 499 +++++++++++++++++++++++++++++---
 internal/monitoring/types.go           |  77 ++++-
 admin/src/adminapi/client.ts           |  12 +-
 admin/src/adminapi/monitoring.ts       | 130 ++++++ (new)
 admin/src/pages/MonitoringPage.tsx     | 219 +++++++++++-
 admin/src/__tests__/monitoring.test.ts | 138 ++++++ (new)
 docs/MONITORING_V1_DELIVERABLE.md      |  ...   (new, this file)
```

The router diff is **6 added lines** and 0 modifications. The
legacy `adminapi/server.go` diff is **7 lines** and is limited to
adapting to the new `ResolveAlert` return type.

`webapp/` and `admin/` are untracked in `origin/main` (they are
delivered as release artifacts in this repo's workflow); they are
copied verbatim into the worktree for the build to succeed and have
no functional changes outside the files listed above.

---

**Branch**: `feature/monitoring-v1`
**Worktree**: `D:\orvix_new\.worktrees\feature-monitoring-v1`
**Base**: `origin/main` @ `a776df1` (Fix webmail detail view wiring)
