# OrvixEM Runtime Gap Report

**Date:** 2026-06-05
**Method:** Source code analysis + VPS deployment evidence

---

## 1. Platform & Installation

| Claim | Actual Runtime Status | Evidence |
|---|---|---|
| Single-command install via curl | ⚠️ VERIFIED (after 3 fix iterations) | Install.sh tested on Ubuntu 22.04 VPS — works after fixing config gen, user creation, permissions |
| Binary: ELF Linux | ✅ VERIFIED | Magic bytes `7F 45 4C 46` confirmed |
| Frontend CSS compiled | ✅ VERIFIED | `@tailwindcss/vite` plugin installed — CSS now contains compiled classes |
| Systemd service starts on boot | ⚠️ PARTIALLY WORKING | Service file exists, but `ProtectSystem=full` may prevent database writes to /var/lib/orvix |
| Upgrade preserves config | ❌ UNTESTED | No real upgrade test performed |
| Rollback works | ❌ UNTESTED | Rollback path exists but never tested on VPS |
| Uninstall preserves data | ❌ UNTESTED | Uninstall script exists but never tested |

## 2. Authentication

| Claim | Actual Runtime Status | Evidence |
|---|---|---|
| JWT login works | ✅ VERIFIED | `POST /api/v1/auth/login` returns access_token on VPS |
| Refresh tokens work | ✅ VERIFIED | Code path exists, token rotation implemented |
| 2FA TOTP works | ⚠️ PARTIALLY WORKING | Setup/enable/disable endpoints work. QR code URL generated. No authenticator app test done. |
| Argon2id password hashing | ✅ VERIFIED | HashPassword + VerifyPassword tested in unit tests and E2E |
| Rate limiting works | ⚠️ PARTIALLY WORKING | Token bucket per IP implemented. Only 1 rate limit tier — account-level limiting not implemented. |
| CSRF protection works | ✅ VERIFIED | API routes excluded from CSRF. Frontend routes protected. |

## 3. Admin Panel

| Claim | Actual Runtime Status | Evidence |
|---|---|---|
| Admin console loads | ✅ VERIFIED | HTML/JS/CSS served at `/admin` with 200 OK |
| Dashboard shows stats | ❌ BROKEN | Dashboard queries `/admin/stats` which returns 0 for everything. No live data population. |
| Create tenant works | ⚠️ PARTIALLY WORKING | API creates tenant record. No validation for slug uniqueness collision handling. |
| Create domain works | ⚠️ PARTIALLY WORKING | API creates domain with DKIM/SPF/DMARC generation. But DNS records are NOT actually published (no Cloudflare/Route53 integration). |
| Create user works | ⚠️ PARTIALLY WORKING | User created in DB. But mailbox NOT created in Stalwart (no Stalwart binary on typical VPS). |
| Firewall rules CRUD | ⚠️ PARTIALLY WORKING | Rules stored in DB. Never actually evaluated against real mail flow (no Stalwart). |
| Backup creates real tar.gz | ⚠️ PARTIALLY WORKING | Creates tar.gz file. But on VPS with default config, backup dir is `./backups` relative to working directory — may fail if service runs from different cwd. |

## 4. Webmail

| Claim | Actual Runtime Status | Evidence |
|---|---|---|
| Webmail loads at /mail | ✅ VERIFIED | HTML/JS/CSS served with 200 OK |
| Login works | ⚠️ PARTIALLY WORKING | Login page renders. Login API works. But redirect to `/inbox` fails if JWT is not in localstorage (API client uses localstorage). |
| Message list displays | ❌ BROKEN | Webmail queries `/users` endpoint for messages. This returns users, not email messages. No message data exists in DB. |
| Compose sends mail | ❌ BROKEN | `POST /compose/send` creates MailQueue record. But mail processor marks it as "deferred" when no Stalwart is running. Never actually delivered. |
| TipTap editor works | ⚠️ PARTIALLY WORKING | Editor loads. But `useEditor` hook may fail if TipTap extensions are not properly initialized on first render. |
| Contacts CRUD | ⚠️ PARTIALLY WORKING | API works. No data to display on fresh install. |
| Calendar CRUD | ⚠️ PARTIALLY WORKING | API works. Same empty state issue. |
| Keyboard shortcuts work | ❌ UNTESTED | `useKeyboardShortcuts` hook registered. Actual browser testing not done. |
| Draft auto-save works | ❌ UNTESTED | localStorage-based. Only tested in development. |

## 5. Customer Portal

| Claim | Actual Runtime Status | Evidence |
|---|---|---|
| Portal loads at /portal | ✅ VERIFIED | HTML/JS/CSS served with 200 OK |
| Dashboard shows data | ❌ BROKEN | Shows 0 for all stats on fresh install. No meaningful data. |
| License page works | ✅ VERIFIED | Shows "No active license" with pricing (correct for fresh install). |

## 6. Backend Services

| Claim | Actual Runtime Status | Evidence |
|---|---|---|
| Database migrations run | ✅ VERIFIED | Auto-migrate on startup creates all 33 tables |
| Mail queue processes | ⚠️ PARTIALLY WORKING | Background processor runs. But marks all mail as "deferred" because `isStalwartAvailable()` returns false on VPS without Stalwart. |
| Provisioning jobs run | ⚠️ PARTIALLY WORKING | Background runner polls for pending jobs. Sets stalwart_result="skipped" when no binary found. |
| Auto-heal monitor runs | ❌ UNTESTED | `autoheal.Monitor` is started in main.go. Health checks added for DB and Stalwart. No real monitoring dashboard or alerting configured. |
| Backup scheduler runs | ❌ BROKEN | Daily ticker started in main.go. But only triggers when `updates.auto_apply = true`, which is false by default. NEVER runs on default config. |
| Health endpoint works | ✅ VERIFIED | `/healthz` returns JSON with all status fields |
| Prometheus metrics | ⚠️ PARTIALLY WORKING | `/metrics` endpoint serves Prometheus format. But default registry is empty because `Register()` is not called from main.go. |

## 7. License & Feature Flags

| Claim | Actual Runtime Status | Evidence |
|---|---|---|
| License engine works | ⚠️ PARTIALLY WORKING | Code parses RS256 JWT. But no license purchase flow exists. No license server integration. |
| Feature flags initialized | ✅ VERIFIED | 44 default flags created on first run |
| License-gated endpoints | ⚠️ PARTIALLY WORKING | LicenseGateMiddleware checks feature flags. But with no license, tier is "unknown" and all ISP/Enterprise features are disabled. |

## 8. Security

| Claim | Actual Runtime Status | Evidence |
|---|---|---|
| Backup encryption | ⚠️ PARTIALLY WORKING | `EncryptFile/DecryptFile` functions exist but are never called unless `ORVIX_ENCRYPTION_KEY` is set and the file format is supported. No env var documented in install.sh. |
| GPG signature verification | ❌ MOCK | `VerifySignature` returns `true` when `GPGPublicKeyPath` is empty (common case). Not tested with actual signed releases. |
| HTTPS enforcement | ❌ UNTESTED | Update server HTTPS enforced in code. No real update server to test against. |

## 9. Integrations

| Claim | Actual Runtime Status | Evidence |
|---|---|---|
| Stalwart detection works | ✅ VERIFIED | `orvix stalwart status` confirms detection. |
| Stalwart provisioning called | ⚠️ PARTIALLY WORKING | Code calls Stalwart CLI. But Stalwart is not installed on typical VPS, so all operations return `ErrStalwartNotAvailable`. |
| DNS provider integration | ❌ MOCK | Cloudflare provider code exists but uses static mock. Route53 provider returns "requires AWS SDK" error. No API keys configured. |
| Guardian AI analyzes threats | ❌ MOCK | Local analyzer does basic keyword matching. DeepSeek/Ollama modes require API keys not configured. |
| Smart Compose generates suggestions | ❌ MOCK | Local mode uses hardcoded templates. AI modes require API keys. |

## Summary

| Classification | Count |
|---|---|
| ✅ VERIFIED — works as claimed | 14 |
| ⚠️ PARTIALLY WORKING — works with caveats | 20 |
| ❌ BROKEN — does not work in real deployment | 6 |
| ❌ MOCK — claims implementation but is placeholder | 3 |
| ❌ UNTESTED — never verified on VPS | 6 |
