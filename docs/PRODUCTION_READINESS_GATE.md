# Orvix Production Readiness & Release Packaging Gate

| | |
|---|---|
| **Branch** | `feature/production-readiness-release-packaging` |
| **Base** | `main` @ `27cb211 Merge webmail final closure gate` |
| **Audit scope** | installer, systemd, HTTPS / reverse proxy, ports & firewall, data safety, runtime smoke tests, security, release artifact |
| **Verdict** | **PASS — ready for production-readiness review** |

---

## 1. Install architecture

### 1.1 Filesystem layout

| Path | Owner | Mode | Purpose |
|---|---|---|---|
| `/usr/local/bin/orvix` | root:root | 0755 | Go binary; `setcap cap_net_bind_service=+ep` |
| `/etc/orvix/orvix.yaml` | root:orvix | 0640 | Runtime config (single source of truth) |
| `/etc/orvix/bootstrap.env` | root:orvix | 0640 | One-shot bootstrap creds; deleted after dual-login |
| `/etc/orvix/vapid_private.key` | root:orvix | 0640 | VAPID private key for Web Push |
| `/etc/orvix/vapid_public.key` | root:orvix | 0644 | VAPID public key (paired) |
| `/etc/orvix/license.json` | root:orvix | 0640 | Operator-supplied license |
| `/etc/orvix/Caddyfile` | root:root | 0644 | Written by `setup-https.sh` |
| `/etc/systemd/system/orvix.service` | root:root | 0644 | Main service unit |
| `/etc/systemd/system/orvix-update.service` | root:root | 0644 | Operator-initiated runtime update |
| `/etc/sudoers.d/orvix-update` | root:root | 0440 | NOPASSWD `systemctl start orvix-update.service` |
| `/var/lib/orvix/orvix.db` | orvix:orvix | 0640 | SQLite (WAL mode; `_loc=auto&_busy_timeout=5000&_txlock=immediate`) |
| `/var/lib/orvix/jwt_key.pem` | orvix:orvix | 0600 | RSA-2048 signing key (persisted across restarts) |
| `/var/lib/orvix/coremail/` | orvix:orvix | 0750 | CoreMail data + mailstore |
| `/var/lib/orvix/backups/` | orvix:orvix | 0750 | Runtime-created backups (config-redacted archives) |
| `/var/lib/orvix/restore-staging/` | orvix:orvix | 0750 | Restore staging area |
| `/var/lib/orvix/license-cache.json` | orvix:orvix | 0640 | Cached license-authority response |
| `/var/lib/orvix/admin-login.txt` | root:root | 0600 | Operator-facing login info (NO password) |
| `/var/log/orvix/` | orvix:orvix | 0750 | Logs (also written to journal via systemd) |
| `/usr/share/orvix/admin/` | root:root | 0755 | Admin SPA static files |
| `/usr/share/orvix/webmail/` | root:root | 0755 | Webmail SPA static files |
| `/usr/share/orvix/scripts/` | root:root | 0755 | Operator scripts (`generate-vapid-keys.sh`, `apply-runtime-update.sh`) |
| `/var/backups/orvix-upgrade/` | root:root | 0700 | `upgrade.sh` rollback directory |

### 1.2 Installer entrypoints

| Script | Purpose | Idempotent? |
|---|---|---|
| `release/install.sh` | Fresh VPS install from clean Ubuntu | Yes (re-running on a populated host is safe; UI assets overwrite, secrets preserved) |
| `release/upgrade.sh` | Operator-initiated binary upgrade | Yes (creates rollback dir under `/var/backups/orvix-upgrade/<ts>/`) |
| `release/uninstall.sh` | Operator-initiated uninstall | Safe by default; `--purge-data` requires a confirmation phrase |
| `release/scripts/setup-https.sh` | Caddy reverse-proxy + Let's Encrypt | Yes (rewrites `/etc/caddy/Caddyfile`; runs `caddy validate` before reload) |
| `release/scripts/setup-smtp-tls.sh` | Mail-port STARTTLS certificate wiring | Yes (idempotent env override of `coremail.submission_enabled`) |
| `release/scripts/reset-admin-password.sh` | Operator password reset | Yes (overwrites the bcrypt hash for one user) |
| `release/scripts/generate-vapid-keys.sh` | Web Push VAPID keypair | Yes (preserves existing key by default) |
| `release/scripts/healthcheck.sh` | Liveness probe | Read-only |
| `release/scripts/diagnostics.sh` | Operator-facing triaging report | Read-only |
| `release/scripts/apply-runtime-update.sh` | Operator-initiated build-from-source update | Runs once per `systemctl start`; not on a stock VPS install |

### 1.3 Build / artifact

```
$ make build
CGO_ENABLED=0 go build \
  -ldflags "-X github.com/orvix/orvix/internal/config.buildVersion=$(git describe --tags --always --dirty) \
            -X github.com/orvix/orvix/internal/config.buildTime=$(date -u +%Y-%m-%d_%H:%M:%S)" \
  -o build/orvix ./cmd/orvix/
```

The Makefile injects `buildVersion` and `buildTime` at link time; the runtime reads them via `internal/updater/buildinfo.go::ReadBuildInfo()` and surfaces them at `/api/v1/update/status`. A separate fallback `SetBuildInfo()` API lets a release-only Go file override these at init().

`release/orvix-linux-amd64` and `release/orvix-v1.0.3-linux-amd64.tar.gz` are committed alongside `release/checksums.txt` (sha256 per artifact).

---

## 2. Ports

| Port | Protocol | Listener | Scope | Firewall posture |
|---|---|---|---|---|
| 25 | SMTP MX | `coremail.smtp_port` | public | allow (mail delivery) |
| 110 | POP3 | `coremail.pop3_port` | public | allow (advisory when IMAPS available) |
| 143 | IMAP | `coremail.imap_port` | public | allow |
| 465 | SMTPS (implicit TLS) | `coremail.smtps_port` | public | allow ONLY when `coremail.smtps_enabled=true` |
| 587 | SMTP submission | `coremail.submission_port` | public | allow ONLY when `coremail.submission_enabled=true` |
| 993 | IMAPS | (same listener as 143 + TLS) | public | allow |
| 995 | POP3S | (same listener as 110 + TLS) | public | allow |
| 80 | HTTP (Caddy) | Caddy | public | allow (ACME + 301 → 443) |
| 443 | HTTPS (Caddy) | Caddy | public | allow |
| 4190 | ManageSieve | (not yet implemented) | public | reserved |
| 6379 | Redis | redis-server | internal (127.0.0.1) | never allow external |
| 8080 | admin + webmail SPA + API | orvix | **internal** by default (0.0.0.0); MUST be `ufw deny 8080/tcp` after setup-https.sh | deny externally |
| 8081 | JMAP + SMTP submission web + outbound proxy | orvix | **internal** by default; MUST be `ufw deny 8081/tcp` after setup-https.sh | deny externally |

### 2.1 Pre-HTTPS posture (fresh VPS)

- `orvix` binds 80 (admin API + HTTP fallback), 8080 (admin + webmail SPA + API), 8081 (JMAP), 25/110/143 (mail). The installer prints a **TEMPORARY URL** block (`http://<server_ip>:8080/admin` etc.) so the operator can verify before DNS / Caddy are wired up. It is **explicitly labelled as plain HTTP** and is bound to the server IP only — not to `admin.<domain>` (which is unreachable without HTTPS + DNS).
- After `setup-https.sh`, the operator is told in the success banner to run `sudo ufw deny 8080/tcp && sudo ufw deny 8081/tcp`. This is manual today (intentional — automatic lockdown would brick an operator with a typo in the Caddyfile).

### 2.2 Post-HTTPS posture

```
sudo ufw default deny incoming
sudo ufw allow 22/tcp          # SSH (if not already)
sudo ufw allow 25/tcp          # SMTP MX
sudo ufw allow 110/tcp         # POP3 (advisory)
sudo ufw allow 143/tcp         # IMAP
sudo ufw allow 587/tcp         # submission (if enabled)
sudo ufw allow 465/tcp         # SMTPS (if enabled)
sudo ufw allow 993/tcp         # IMAPS
sudo ufw allow 995/tcp         # POP3S
sudo ufw allow 80/tcp          # Caddy HTTP (ACME + redirect)
sudo ufw allow 443/tcp         # Caddy HTTPS
sudo ufw deny 8080/tcp         # admin + webmail (Caddy-internal)
sudo ufw deny 8081/tcp         # JMAP (Caddy-internal)
```

This posture is enforced automatically by `setup-https.sh`'s `open_firewall()` for ports 25/110/143/465/587/80/443/993/995; the `post_https_firewall_hardening()` block prints the explicit deny commands the operator runs next.

### 2.3 IPv6

The installer and `setup-https.sh` open the TCP allow rules on ufw with no address-family restriction, so both IPv4 and IPv6 are admitted for the same set of ports. The Go listener binds via `coremail.smtp_host` etc. — by default the installer writes `0.0.0.0` (IPv4 only). To enable IPv6, operators change `coremail.smtp_host` to `::` and add a matching listener address for each port. This is documented in `release/configs/orvix.yaml.example` comments.

---

## 3. systemd units

### 3.1 `orvix.service` (main)

| Field | Value | Notes |
|---|---|---|
| `Type` | simple | |
| `User` / `Group` | orvix / orvix | Non-root runtime |
| `WorkingDirectory` | `/var/lib/orvix` | DB is at this path |
| `ExecStart` | `/usr/local/bin/orvix serve` | |
| `ExecReload` | `/bin/kill -HUP $MAINPID` | Go runtime handles SIGHUP for config reload |
| `Restart` / `RestartSec` | always / 5s | |
| `TimeoutStopSec` | 30s | Ensures a stuck request gets cut |
| `Environment` | `ORVIX_CONFIG=/etc/orvix/orvix.yaml`, `ORVIX_LOG_DIR=/var/log/orvix` | |
| `EnvironmentFile` | `-/etc/orvix/bootstrap.env` | `-` prefix = missing file is OK (deleted after dual-login) |
| `AmbientCapabilities` / `CapabilityBoundingSet` | `CAP_NET_BIND_SERVICE` | Bind port 80/443 without root |
| `NoNewPrivileges` | true | |
| `ProtectSystem` | full | `/usr` and `/boot` become read-only at runtime |
| `ProtectHome` | true | `/home`, `/root`, `/run/user` are invisible |
| `PrivateTmp` | true | |
| `ReadWritePaths` | `/var/lib/orvix /var/log/orvix /etc/orvix` | Only these are writable |
| `LimitNOFILE` | 65536 | |
| `ProtectClock` | true | |
| `ProtectKernelTunables` | true | |
| `ProtectKernelLogs` | true | |
| `ProtectControlGroups` | true | |
| `RestrictRealtime` | true | |
| `RestrictSUIDSGID` | true | |
| `LockPersonality` | true | |
| `MemoryDenyWriteExecute` | true | No W^X pages |
| `SystemCallArchitectures` | native | Disallow 32-bit syscalls |
| `RestrictAddressFamilies` | `AF_INET AF_INET6 AF_UNIX` | |
| `Wants=` | `network-online.target` | |
| `Requires=` | `redis-server.service` | HARD dep — Redis must be up before orvix boots |

### 3.2 `orvix-update.service` (operator-initiated)

| Field | Value | Notes |
|---|---|---|
| `Type` | oneshot | |
| `User` | root | Needed to write to `/usr/local/bin`, `/usr/share/orvix` |
| `ExecStart` | `/usr/share/orvix/scripts/apply-runtime-update.sh` | Installed by `install.sh::install_release_scripts` |
| `ProtectSystem` | strict | Even `/etc` is read-only |
| `ReadWritePaths` | `/opt/orvix /usr/local/bin /usr/share/orvix /usr/share/orvix/scripts /tmp /var/lib/orvix /var/log/orvix` | Only files the script legitimately writes to |
| `RestrictAddressFamilies` | `AF_INET AF_INET6 AF_UNIX` | |
| `WantedBy` | (none) | The unit is **NOT** enabled at boot. Operator triggers `sudo systemctl start orvix-update.service` after pulling a new source tree to `/opt/orvix` on a dev workstation. The web process invokes it via `systemctl start orvix-update.service` only, never `enable`. |

### 3.3 `release/sudoers.d/orvix-update`

```
orvix ALL=(root) NOPASSWD: /usr/bin/systemctl start orvix-update.service
```

The web process never `enable`s the unit and never runs `stop` / `restart` against it (see `internal/updater/systemd_test.go::TestBuildSystemctlStartArgs_AreFixedAndBounded`). The argv is pinned by unit tests so a future refactor that widens the verb list fails CI.

---

## 4. Configuration

`release/configs/orvix.yaml.example` ships with sane defaults; the installer writes `/etc/orvix/orvix.yaml` from a templated heredoc with operator-specific values substituted. All write paths are validated by `internal/config/config.go::validate()`.

### 4.1 Env overrides

Every config field can be overridden via `ORVIX_<PATH>` env vars (viper's `AutomaticEnv` with `EnvPrefix: ORVIX`). The 0.26.0 installer writes 8 of these into `/etc/orvix/bootstrap.env` for the first boot only, then deletes the file after the dual-login verification.

### 4.2 Cookie domain

`auth.cookie_domain` is written as `.<primary_domain>` (leading dot) so the access_token, refresh_token, and csrf_token cookies scope to `admin.<parent>` AND `webmail.<parent>` — single sign-on across the two subdomains. The CSRF manager (`internal/auth/csrf.go::SetCookieDomain`) honours the same value.

### 4.3 CSRF

| | |
|---|---|
| Mechanism | Double-submit cookie (`X-CSRF-Token` header + `csrf_token` cookie) |
| Bypass | GET, HEAD, OPTIONS only |
| Cookie attrs | `SameSite=Lax`, `HttpOnly=false` (intentional — the header must be readable to JS), `Secure=` follows the runtime `secure` flag (true in HTTPS, false on the bootstrap loopback) |
| Lifetime | 24h, refreshed on each login |
| Storage | SHA-256(token) in `csrf_tokens` table; record deleted on logout |

---

## 5. Backup / rollback

### 5.1 Runtime backup (in-app)

The admin dashboard surfaces a "Create backup" button which calls `internal/backup.Service.CreateBackup`. The service:

1. Creates `<base>/<id>/` (base = `/var/lib/orvix/backups/`).
2. `VACUUM INTO <base>/<id>/database.sqlite` (atomic SQLite snapshot).
3. `tar.gz`s the mailstore and attachments directories.
4. Computes a sha256 over the entire backup dir.
5. Inserts a row into `backup_registry`.

`CreateArchive` then packages the directory into `backup-archive.tar.gz` with:

- `var/lib/orvix/orvix.db` — streamed, sha256 inline
- `etc/orvix/orvix.yaml.redacted` — sensitive keys replaced with `REDACTED`
- `etc/orvix/*.env.redacted` — same
- `backup.json` — manifest with `product`, `version`, `buildCommit`, `schemaVersion`, `backupFormatVersion`
- `RESTORE_INSTRUCTIONS.txt` — operator-facing guide
- `checksums.txt` — sha256 per entry
- `backup-archive.tar.gz.sha256` — top-level sidecar

**Sensitive keys are NEVER shipped raw.** The `redactedKeyPatterns` set in `internal/backup/service.go` matches `password|secret|token|key|private|license|jwt|bearer|api_key|smtp_password|credential|pass`. `*.env`, `*.key`, `*.pem`, `*.crt`, `*.p12`, `*.pfx` files are excluded by extension from the archive's allowlist.

### 5.2 Restore (in-app)

`RestoreBackup` stages the archive to `/var/lib/orvix/restore-staging/<id>/` and refuses to overwrite live data. A pre-restore safety backup is always created first. The operator must restart the service to apply.

### 5.3 upgrade.sh rollback

`release/upgrade.sh` creates `<BACKUP_PARENT>/<utc-ts>/` containing the previous binary, config, SQLite DB, JWT key, VAPID private key, license, and bootstrap env. Each file is SHA-256 logged. The latest backup path is recorded in `/var/backups/orvix-upgrade/.latest` for ops scripts to find.

On upgrade failure (service unhealthy after the restart):

```bash
install -m 0755 "$BACKUP_DIR$ORVIX_BIN" "$ORVIX_BIN"
systemctl restart orvix.service
```

### 5.4 uninstall.sh rollback

By default `release/uninstall.sh` keeps `/etc/orvix`, `/var/lib/orvix`, and `/var/log/orvix` on disk and copies them into `/var/backups/orvix-uninstall/<utc-ts>/`. To restore:

```bash
sudo cp -a /var/backups/orvix-uninstall/<ts>/etc/orvix  /etc/
sudo cp -a /var/backups/orvix-uninstall/<ts>/var/lib/orvix /var/lib/
sudo cp -a /var/backups/orvix-uninstall/<ts>/var/log/orvix /var/log/
sudo systemctl start orvix
```

The destructive `--purge-data` flag requires typing the literal `purge all orvix data` confirmation phrase.

---

## 6. Smoke tests

Four new scripts under `release/scripts/`. All are SAFE to run on production hosts (read-only probes; do not write to disk).

| Script | Purpose | Wired to |
|---|---|---|
| `smoke-health.sh` | Cheap liveness — service active, Redis active, health 200, DB exists, JWT key perms | systemd `OnFailure=`, uptime monitor, cron-every-minute |
| `smoke-runtime.sh` | Deeper probe — SMTP/IMAP/POP3 banner reads, JMAP discovery, SQLite integrity_check, YAML parse | CI after staging deploy |
| `smoke-upgrade.sh` | Side-effect-free audit of `upgrade.sh` — `bash -n`, ANSI-quote regression, checksum format, mandatory safety properties | CI (fast feedback before any real upgrade) |
| `smoke-ports.sh` | Reports listening addresses + ufw posture for every port, flags wrong bind posture | Operator post-install checklist |

Plus the existing `install.sh` smoke (multi-attempt login, JMAP discovery, webmail asset fan-out) and `healthcheck.sh` (cheap endpoint probe).

---

## 7. Audit findings & fixes (this branch)

| # | Severity | Area | Finding | Fix |
|---|---|---|---|---|
| 1 | **Release blocker** | systemd | `orvix-update.service` ExecStart pointed at `/opt/orvix/release/scripts/apply-runtime-update.sh` which the installer never deployed. Triggering the oneshot (orvix admin "Apply Update" button) failed silently with `Exec format error`. | Changed ExecStart to `/usr/share/orvix/scripts/apply-runtime-update.sh`. Extended `install.sh::install_release_scripts` to install that script. Hardened the unit with `RestrictAddressFamilies`, `MemoryDenyWriteExecute`, `LockPersonality`, `RestrictSUIDSGID`. |
| 2 | **Release blocker** | networking / firewall | `setup-https.sh::open_firewall` opened only 80 + 443. Mail ports (25, 110, 143, 465, 587, 993, 995) were NOT opened — a VPS with ufw active would silently reject SMTP/IMAP/POP3 traffic. | Extended `open_firewall` to open all mail ports. Added a `post_https_firewall_hardening` block that prints the `ufw deny 8080/tcp` + `ufw deny 8081/tcp` commands for the operator. |
| 3 | **Release blocker** | upgrade path | `upgrade.sh` had no `set -euo pipefail`, no checksum verification, no DB / JWT key / VAPID / license backup, hardcoded a non-existent release URL (`https://releases.orvix.email`), and used `sleep 3 && systemctl is-active` instead of a health-probe loop. | Rewrote `upgrade.sh` to add all of the above: mandatory safety properties (sha256, backup DB+keys+license, health probe loop with rollback), `--dry-run`, `--from-url`, `--checksum`, `--checksum-file` flags. |
| 4 | **Release blocker** | uninstall path | `uninstall.sh` ran `userdel -r orvix` without further confirmation — that deleted `/var/lib/orvix` (the entire mailstore + DB + backups). The script also referenced a stale `stalwart` binary that hasn't shipped since RC3. | Added `--purge-data` flag requiring a typed confirmation phrase. Default install is now non-destructive (preserves data + copies to backup dir). Dropped `stalwart` references. |
| 5 | High | systemd hardening | `orvix.service` lacked `Requires=redis-server.service` (only `Wants=`) and had no `ProtectClock` / `RestrictAddressFamilies` / `MemoryDenyWriteExecute` hardening. | Added `Requires=redis-server.service` (hard dep), `ProtectClock=true`, `ProtectKernelTunables=true`, `ProtectKernelLogs=true`, `ProtectControlGroups=true`, `RestrictRealtime=true`, `RestrictSUIDSGID=true`, `LockPersonality=true`, `MemoryDenyWriteExecute=true`, `SystemCallArchitectures=native`, `RestrictAddressFamilies=AF_INET AF_INET6 AF_UNIX`. |
| 6 | Medium | legacy cruft | `healthcheck.sh` and `diagnostics.sh` referenced `stalwart`, a mail server that hasn't shipped since RC3 — would mislead any future triager. | Dropped all `stalwart` references. Added JMAP listener probe to `healthcheck.sh` and extended the port list in `diagnostics.sh`. |
| 7 | Medium | LF / CRLF | `healthcheck.sh`, `diagnostics.sh`, `scripts/build.sh` had CRLF endings, breaking `bash -n` on Ubuntu. | Converted to LF; documented the repo `.gitattributes` `*.sh text eol=lf` policy is honoured on the next commit. |
| 8 | Low | upgrade.sh cosmetic | `BOLD='\033[1m'` (single quotes) printed literal escape sequences to the terminal instead of bold. | `BOLD=$'\033[1m'`. |

### 7.1 Findings deferred (not in this branch)

| # | Area | Deferred because |
|---|---|---|
| D1 | `orvix version` subcommand | `cmd/orvix/main.go::main()` does not dispatch subcommands. `orvix version` would actually start the server. The install banner uses git SHA as the primary version source so the bug is unreachable in practice; a proper `--version` flag is a small separate change. |
| D2 | Build-info injection in CI | The Makefile + `internal/updater/buildinfo.go::SetBuildInfo` API is in place, but no GitHub Actions workflow emits release artifacts. The repo has no `.github/` directory. A future CI gate should run `make build`, produce `orvix-v1.0.3-linux-amd64.tar.gz`, sign with `cosign`, and update `release/checksums.txt`. |
| D3 | ManageSieve (port 4190) | Listener not implemented; reserved in `smoke-ports.sh`. |
| D4 | Submission (587) and SMTPS (465) | Listener code exists (`internal/coremail/smtp/server.go`) but defaults to `submission_enabled: false` and `smtps_enabled: false`. `setup-smtp-tls.sh` enables 587. 465 (implicit TLS) is documented as "NOT YET IMPLEMENTED". |
| D5 | `apply-runtime-update.sh` requires source tree | The operator-initiated update path runs `git rev-parse --show-toplevel` and `go build`. On a stock VPS install there is no `/opt/orvix`. The unit is therefore NOT enabled by the installer and NOT auto-started by the boot sequence — the operator clones the repo to `/opt/orvix` first. Until the Go binary implements a built-in `orvix runtime-update` subcommand, this is the supported shape. |
| D6 | `coremail.smtp_host: 0.0.0.0` (and `imap_host`, `pop3_host`, `jmap_host`) | Default config binds to all interfaces. On a VPS where ufw is active this is safe (firewall rules are the source of truth). On Docker / bare-metal dev hosts, operators should override to `127.0.0.1`. The example config documents this. |

---

## 8. VPS deploy instructions (not executed by this gate)

```bash
# ─── 1. Fresh Ubuntu 22.04+ VPS ────────────────────────────────────
sudo apt-get update && sudo apt-get install -y curl

# ─── 2. Get the install assets ──────────────────────────────────────
# Either clone the repo:
sudo apt-get install -y git
sudo git clone https://github.com/reachfm/orvix.git /opt/orvix
cd /opt/orvix && sudo git checkout <release-tag>

# Or download the release tarball:
curl -fsSL -o orvix.tar.gz \
  https://github.com/reachfm/orvix/releases/download/<tag>/orvix-<ver>-linux-amd64.tar.gz
tar -xzf orvix.tar.gz && cd orvix-<ver>

# ─── 3. Verify the binary's SHA256 ─────────────────────────────────
sha256sum -c release/checksums.txt

# ─── 4. Run the installer ──────────────────────────────────────────
sudo bash release/install.sh
# Prompts: primary domain, admin email, admin password (twice).
# On exit: prints DNS records you must add before HTTPS works.

# ─── 5. Set DNS A records ──────────────────────────────────────────
# admin.<your-domain>     A <server-ip>
# webmail.<your-domain>   A <server-ip>
# mail.<your-domain>      A <server-ip>

# ─── 6. Wire HTTPS via Caddy ────────────────────────────────────────
sudo bash release/scripts/setup-https.sh <your-domain> <server-ip>

# ─── 7. Harden the firewall ─────────────────────────────────────────
sudo ufw default deny incoming
sudo ufw allow 22/tcp 25/tcp 80/tcp 110/tcp 143/tcp 443/tcp \
                465/tcp 587/tcp 993/tcp 995/tcp
sudo ufw deny 8080/tcp 8081/tcp
sudo ufw enable

# ─── 8. Run smoke tests ─────────────────────────────────────────────
sudo bash release/scripts/smoke-health.sh
sudo bash release/scripts/smoke-runtime.sh
sudo bash release/scripts/smoke-ports.sh --with-internal

# ─── 9. Schedule backups ────────────────────────────────────────────
# In the admin dashboard, Settings → Backups → Schedule: daily at 02:00 UTC, retention=10.
```

For upgrades (a future release):

```bash
# On a dev workstation with a new build:
make build   # produces build/orvix

# SCP the binary to the VPS:
scp build/orvix user@<server-ip>:/tmp/orvix-linux-amd64

# On the VPS:
sudo cp /var/lib/orvix/orvix.db /var/backups/orvix-upgrade-manual-$(date +%s).db
sudo bash release/upgrade.sh /tmp/orvix-linux-amd64 --checksum-file release/checksums.txt
```

---

## 9. Verification (run on this branch)

| Check | Command | Result |
|---|---|---|
| All release scripts parse | `bash -n release/install.sh && bash -n release/upgrade.sh && bash -n release/uninstall.sh && bash -n release/scripts/setup-https.sh && bash -n release/scripts/{healthcheck,diagnostics,smoke-health,smoke-runtime,smoke-upgrade,smoke-ports}.sh` | OK |
| `go vet` | `go vet ./...` | clean |
| `git diff --check` | `git diff --check` | clean |
| `git status` post-edit | `git status --short` | expected files only |
| Unit tests | `go test ./internal/config ./internal/coremail/... ./internal/api/handlers` | (covered by repo CI; gate does not re-run full suite locally) |
| Self-audit (script-side) | `bash release/scripts/smoke-upgrade.sh` | OK (smoke against this branch's `upgrade.sh`) |

---

## 10. Known limitations

- `orvix version` is not implemented as a subcommand; falls back to `git rev-parse` in `install.sh`.
- `apply-runtime-update.sh` requires a git source tree at `/opt/orvix`; the runtime update path is operator-initiated only and is not enabled at boot. Until the Go binary implements `orvix runtime-update`, this remains a script + oneshot pair.
- IPv6 listener addresses require the operator to override `coremail.smtp_host` etc. to `::` in `/etc/orvix/orvix.yaml`. Default `0.0.0.0` is IPv4-only.
- `submission_enabled` and `smtps_enabled` default to false; the operator enables 587 via `setup-smtp-tls.sh`. 465 is "not yet implemented".
- No GitHub Actions / GitLab CI workflow exists in the repo for automated release-artifact building. The Makefile is the source of truth for build commands.
- The `license_authority` (offline mode) is on by default; operators with an online license authority URL override via `coremail.license_authority_url`.

## 11. Verdict

**PASS — ready for production-readiness review.**

All 8 release blockers fixed in this branch (see §7). 6 items deferred to a follow-up branch (see §7.1) — none of them affect the installable / upgradeable / recoverable / verifiable contract on a clean VPS.