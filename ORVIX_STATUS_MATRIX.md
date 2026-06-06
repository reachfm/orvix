# Orvix Status Matrix

## Legend
- ✅ Functional — works end-to-end
- 🔶 Partial — some functionality works
- ❌ Broken — does not compile or crashes

---

## Core Infrastructure

| Component | Status | Files | Evidence |
|-----------|--------|-------|----------|
| Config System | ✅ Functional | `internal/config/config.go` | Viper-based, env overrides, yaml file |
| Database Layer | ✅ Functional | `internal/config/database.go` | GORM, SQLite + PostgreSQL |
| Logger | ✅ Functional | `internal/config/logger.go` | Zap structured, JSON/console output |
| Models/Migrations | ✅ Functional | `internal/models/models.go` | GORM AutoMigrate, 11 tables |
| Metrics | ✅ Functional | `internal/metrics/metrics.go` | Prometheus, 12 counters/gauges |
| Module Registry | ✅ Functional | `internal/modules/registry.go` | Module interface, init/start/stop lifecycle |
| Code Watermark | ✅ Functional | `internal/config/watermark.go` | Build info, canary tokens |

## Auth & Security

| Component | Status | Files | Evidence |
|-----------|--------|-------|----------|
| JWT Auth | ✅ Functional | `internal/auth/auth.go` | RS256, 15min access, 30d refresh, key persisted |
| Password Hashing | ✅ Functional | `internal/auth/auth.go` | Argon2id + constant-time compare |
| RBAC | ✅ Functional | `internal/auth/auth.go` | 3 roles, middleware enforcement |
| Session Management | ✅ Functional | `internal/auth/auth.go` | SHA-256 hashed, invalidation, rotation |
| CSRF | ✅ Functional | `internal/auth/csrf.go` | Double-submit cookie, DB-verified |
| CORS | ✅ Functional | `internal/api/router.go` | Configurable origins from config |
| Rate Limiting (Redis) | ✅ Functional | `internal/auth/ratelimit.go` | Redis INCR/EXPIRE pipeline |
| Rate Limiting (fallback) | ✅ Functional | `internal/api/router.go` | Fiber in-memory limiter |
| Security Headers | ✅ Functional | `internal/api/router.go` | HSTS, CSP, X-Frame-Options, etc. |
| API Keys | ✅ Functional | `internal/auth/apikey.go` | CRUD, SHA-256 hashed, rotation, revocation |
| Security Monitor | ✅ Functional | `internal/auth/security.go` | DB-backed, 5-failure threshold |

## License System

| Component | Status | Files | Evidence |
|-----------|--------|-------|----------|
| License Validation | ✅ Functional | `internal/license/validator.go` | RSA public key + offline fallback |
| Feature Flags | ✅ Functional | `internal/license/features.go` | Tier-based, current-scope features |
| Hardware Fingerprint | ✅ Functional | `internal/license/fingerprint.go` | Hostname, MAC, machine-id |

## Stalwart Integration

| Component | Status | Files | Evidence |
|-----------|--------|-------|----------|
| REST Client | ✅ Functional | `internal/stalwart/client.go` | All endpoints, retry, timeouts |
| Webhook Receiver | ✅ Functional | `internal/stalwart/webhook.go` | Event dispatch, registered handlers |
| Process Manager | ✅ Functional | `internal/stalwart/process.go` | Start/stop/restart/monitor |
| Config Generator | ✅ Functional | `internal/stalwart/config.go` | Generates stalwart.yaml + webhooks.yaml |
| Binary Embedding | ❌ Removed | N/A | Stalwart is external. embed.go deleted. Installer downloads binary separately. |

## API Layer

| Component | Status | Files | Evidence |
|-----------|--------|-------|----------|
| Router | ✅ Functional | `internal/api/router.go` | All routes + middleware |
| Health | ✅ Functional | `internal/api/handlers/handlers.go` | Returns status + version |
| Auth Endpoints | ✅ Functional | `internal/api/handlers/handlers.go` | Login/refresh/logout/logout-all/change-pw |
| Domain API | ✅ Functional | `internal/api/handlers/handlers.go` | CRUD via Stalwart |
| User API | ✅ Functional | `internal/api/handlers/handlers.go` | CRUD via Stalwart |
| Queue API | ✅ Functional | `internal/api/handlers/handlers.go` | List/delete/retry via Stalwart |
| Firewall Rules API | ✅ Functional | `internal/api/handlers/handlers.go` | CRUD via DB |
| Audit Log Endpoints | ✅ Functional | `internal/api/handlers/handlers.go` | List |
| API Key Endpoints | ✅ Functional | `internal/api/handlers/handlers.go` | Create/list/delete |
| License Endpoints | ✅ Functional | `internal/api/handlers/handlers.go` | Get/validate |
| Feature Flag Endpoints | ✅ Functional | `internal/api/handlers/handlers.go` | List/update |

## Registered Modules

| Module | Status | Files | Evidence |
|--------|--------|-------|----------|
| Firewall | 🔶 Partial | `internal/firewall/` | Pipeline + rules engine + IP reputation exist. Not wired to webhooks. |
| Compliance | ✅ Functional | `internal/compliance/` | ZKE encryption fully implemented. No API endpoints exposed. |

## Removed Modules
The following modules were stubs with no real functionality and have been removed from the product surface (unregistered from registry, no API routes, no feature flags):
- Auto-Heal, Smart Migration, DNS Automation, Calendar, Collaboration, Email Intelligence, Auto-Update, Guardian Agent, Smart Compose AI, ActiveSync, SSO, LDAP Sync, Provision API, Intelligence

## Frontend

| App | Status | Files | Evidence |
|-----|--------|-------|----------|
| Admin UI | ✅ Functional | `web/admin/src/` | Dashboard, domains, users, firewall, audit log. Connected to real API. |
| Webmail UI | 🔶 Partial | `web/webmail/src/` | Layout with API-connected email list. Shows loading/empty/error states. |

## Installer & Deployment

| Component | Status | Files | Evidence |
|-----------|--------|-------|----------|
| Install Script | 🔶 Partial | `scripts/install.sh` | Downloads binary, creates dirs/services. Untested on Linux. |
| Build Script | ✅ Functional | `scripts/build.sh` | Builds binary + frontends |
| Systemd Service | ✅ Functional | built into install.sh | Standard systemd unit |
| Makefile | ✅ Functional | `Makefile` | Build/clean/test/run targets |
| Config File | ✅ Functional | `orvix.yaml` | Default config with all sections |

## Test Coverage

| Package | Tests | Coverage Area |
|---------|-------|---------------|
| `internal/auth` | 15 | JWT gen/validate, key persistence, password hash/verify, token expiry, access control |
| `internal/config` | 8 | AES-256-GCM encrypt/decrypt, watermark, canary, key management |
| `internal/firewall` | 8 | Pipeline scoring, verdict thresholds, cancellation, multi-layer |
| `internal/license` | 5 | Feature flags for SMB/ISP/Enterprise tiers |
| `internal/modules` | 7 | Registration, duplicate prevention, sorting, lifecycle |
| `internal/stalwart` | 8 | Client config, structs, event handler, process manager |
