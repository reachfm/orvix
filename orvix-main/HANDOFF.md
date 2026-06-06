# Orvix Handoff Guide

## Prerequisites

| Requirement | Version | Purpose |
|-------------|---------|---------|
| Go | 1.25+ | Build the Orvix binary |
| Node.js | 20+ | Build frontend apps |
| npm | 10+ | Package management |
| PostgreSQL 16 | 16+ | Production database (optional) |
| SQLite 3 | 3.x | Development database (default) |
| Redis 7 | 7.x | Session caching, rate limiting (optional) |
| Stalwart | 0.10.x | Mail engine (REST API) - download from https://stalw.art/download |

## Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `ORVIX_DATABASE_PASSWORD` | PostgreSQL password | `""` |
| `ORVIX_REDIS_PASSWORD` | Redis password | `""` |
| `ORVIX_STALWART_API_KEY` | Stalwart REST API key | `""` |
| `ORVIX_JWT_SECRET` | JWT signing secret | Auto-generated RSA |
| `ORVIX_DEEPSEEK_API_KEY` | DeepSeek AI API key (for Guardian + Compose) | `""` |
| `ORVIX_CLOUDFLARE_API_KEY` | Cloudflare API key (for DNS automation) | `""` |
| `ORVIX_ENCRYPTION_KEY` | AES-256-GCM key (64 hex chars) | Auto-generated |
| `ORVIX_CONFIG` | Config file path | `/etc/orvix/orvix.yaml` |
| `ORVIX_ADMIN_EMAIL` | **Required**: Admin email for initial user | `""` |
| `ORVIX_ADMIN_PASSWORD` | **Required**: Admin password for initial user | `""` |

## Architecture

```
┌─────────────────────────────────────────────────────┐
│                   Orvix Binary                      │
│                                                      │
│  ┌─────────────────────────────────────────────┐     │
│  │            API Layer (Fiber v3)             │     │
│  │  60+ endpoints · JWT Auth · RBAC · CSRF    │     │
│  └─────────────────────────────────────────────┘     │
│                                                      │
│  ┌───────┐ ┌───────┐ ┌──────┐ ┌────────┐ ┌──────┐  │
│  │Auth    │ │Modules│ │Stalw.│ │Config  │ │Models│  │
│  │Security│ │ ×12   │ │Integ.│ │Metrics │ │GORM  │  │
│  └───────┘ └───────┘ └──────┘ └────────┘ └──────┘  │
│                                                      │
│  ┌─────────────────────────────────────────────┐     │
│  │            Stalwart (external)              │     │
│  │  SMTP · IMAP · POP3 · JMAP · Queue · DKIM  │     │
│  └─────────────────────────────────────────────┘     │
└─────────────────────────────────────────────────────┘
```

## Stalwart Binary

Orvix requires Stalwart Mail Server as an external binary.

- **Download**: https://stalw.art/download
- **Placement**: `stalwart-bin/stalwart`, `/usr/local/bin/stalwart`, or system PATH
- **Not embedded**: The Stalwart binary is NOT embedded in the Orvix binary.
- **Version**: 0.10.x recommended
- **Orvix manages**: Starting, stopping, health checking, and config generation

## How to Run in Development

(previous content stays)

## Release Package

The release package is at:
```
release/orvix-v1.0.2-linux-amd64.tar.gz
```

- **Git Commit:** `dd2bb64`
- **GitHub Release:** https://github.com/reachfm/orvix/releases/tag/v1.0.2
- **Archive SHA256:** `5672e7c6e8b82d59a090e2b6366cbd72d4cf9fb41079e5e81c730ff6462842e0`
- **Binary SHA256:** `f730934a46cfd6c2c6d401f325131366548c19d98f8b47f8bd835d202e9acabe`

### RC3 Security Changes
- **REMOVED**: Hardcoded default credentials (admin@orvix.local / admin123)
- Admin credentials now provided via environment variables
- Installer shows "Orvix v1.0.2 RC3 Installer" banner
- Installer downloads from GitHub releases (https://github.com/reachfm/orvix/releases/download/v1.0.2/)

## How to Install on a Server (One-Command)

```bash
# On a clean Ubuntu 22.04+ / Debian 12+ VPS as root:
curl -fsSL https://orvix.email/install.sh | bash
```

The installer will:
1. Detect OS and install dependencies
2. Create system user `orvix`
3. Create directories: /etc/orvix, /var/lib/orvix, /var/log/orvix
4. Download and install Orvix binary
5. Download and install Stalwart mail server
6. Generate configuration (prompts for domain, admin email/password)
7. Install systemd service
8. Start services and verify health

## Release Package Structure

```
release/
├── install.sh              # Production installer
├── uninstall.sh            # Clean uninstall (with backup)
├── upgrade.sh              # Version upgrade (with rollback)
├── checksums.txt           # SHA256 checksums
├── RELEASE_NOTES.md        # Version changelog
├── VERSION                 # Version file
├── orvix-linux-amd64       # Go binary (Linux AMD64)
├── systemd/
│   └── orvix.service       # systemd service unit
├── configs/
│   └── orvix.yaml.example  # Example configuration
└── scripts/
    ├── healthcheck.sh      # Health check script
    └── diagnostics.sh      # Diagnostics script
```

## Upgrade Instructions

```bash
# From release directory as root:
bash release/upgrade.sh [version]

# Or from anywhere:
ORVIX_VERSION=1.0.0 bash upgrade.sh
```

The upgrade script:
1. Backs up current binary and config
2. Downloads new version
3. Restarts service
4. Verifies health
5. Rolls back on failure

## Uninstall Instructions

```bash
# From release directory as root:
bash release/uninstall.sh
```

The uninstall script:
1. Backs up configuration and data
2. Stops and disables services
3. Removes binaries and service files
4. Removes system user and logs

```bash
# 1. Install Go dependencies
cd D:\orvix
go mod tidy

# 2. Install frontend dependencies
cd web/webmail && npm install && cd ../..
cd web/admin && npm install && cd ../..

# 3. Download Stalwart (https://stalw.art/download) and place in stalwart-bin/

# 4. Create config (optional — uses defaults if missing)
cp orvix.yaml /etc/orvix/orvix.yaml  # Linux
# Or run from project root (uses ./orvix.yaml)

# 5. Run the Orvix server
go run ./cmd/orvix/

# 6. Run frontend dev servers (separate terminals)
cd web/webmail && npm run dev   # http://localhost:3000
cd web/admin && npm run dev     # http://localhost:3001
```

## How to Build Production Binary

```bash
cd D:\orvix
go build -ldflags="-s -w" -o build/orvix ./cmd/orvix/

# Build frontends
cd web/webmail && npm run build && cd ../..
cd web/admin && npm run build && cd ../..
```

## How to Run Tests

```bash
cd D:\orvix
go test ./... -v         # 51 tests across 6 packages
go vet ./...             # Run Go vet
cd web/webmail && npm run build  # Verify webmail builds
cd web/admin && npm run build    # Verify admin builds
```

## Module Status

### Registered Modules (12 functional, 5 new infrastructure)

| Module | Status | Description | APIs |
|--------|--------|-------------|------|
| Firewall | ✅ Functional | Pipeline, rules CRUD, IP reputation, webhook-triggered | 3 |
| Auto-Heal | ✅ Functional | DB/disk health checks, action history | 2 |
| Guardian | ✅ Functional | DeepSeek AI + offline threat analysis, threat logs | 2 |
| Smart Compose AI | ✅ Functional | Compose/summarize/rewrite via DeepSeek, SSE streaming | 2 |
| DNS Automation | ✅ Functional | MX/SPF/DKIM/DMARC generation, Cloudflare provider | 2 |
| Smart Migration | ✅ Functional | IMAP engine, connection test, job tracking | 3 |
| Auto-Update | ✅ Functional | Version check, changelog, update application | 3 |
| Provision API | ✅ Functional | Instant domain + mailbox provision, rollback | 1 |
| Calendar | ✅ Functional | Events CRUD, user-scoped | 4 |
| Collaboration | ✅ Functional | Shared mailboxes via Stalwart | 2 |
| Compliance | ✅ Functional | Legal holds, retention policies, ZKE encryption | 8 |
| Intelligence | ✅ Functional | Email analytics, delivery reports | 2 |

### Infrastructure Packages
| Package | Status | Description |
|---------|--------|-------------|
| Multi-Tenancy | ✅ Models + Middleware | Tenant model, tenant resolve, scope enforcement |
| Reseller | ✅ Model | Reseller model, customer limits, commission |
| White Label | ✅ Model + Middleware | Logo URL, colors, tenant branding |
| Security Alerts | ✅ Full | SMTP + webhook delivery via AlertSender |
| LDAP | ✅ Sync Engine | LDAP config, connection test, user sync job |
| ClamAV | ✅ Scanner | TCP/socket client, INSTREAM protocol, ping |
| Backup | ✅ Manager | Database backup, config backup, history |

## New Models (recent sprint)
- **Tenant** — Multi-tenant organization (slug, domain, plan, limits, branding)
- **Reseller** — Reseller managing customer tenants (limits, commission)
- **LDAPConfig** — LDAP sync settings (host, port, bind DN, user filter)
- **SSOConfig** — SSO/OAuth provider config (provider, client ID, issuer URL)
- **AlertConfig** — Security alert delivery (SMTP server, webhook URL, toggles)
- **BackupHistory** — Backup records (path, size, type, status)

### Features Not Yet Implemented
- Reseller panel (model + dashboard)
- White label (tenant branding)
- SSO/LDAP integration
- ActiveSync protocol support
- ClamAV virus scanning
- SMTP alert delivery (alerts are log-only)
- Multi-tenancy (database scoping)
- Full frontend UI (SPA pages)

## API Inventory

Total: 60+ API endpoints under `/api/v1/`

### Core
- `GET /health` - Server health
- `POST /auth/login` - Login (rate limited)
- `POST /auth/refresh` - Refresh token
- `POST /auth/logout` - Logout (CSRF protected)
- `POST /auth/logout-all` - Logout all sessions (CSRF protected)
- `POST /auth/change-password` - Change password (CSRF protected)
- `GET /me` - Current user profile
- `GET /csrf-token` - Get CSRF token

### Administration
- `GET/POST /domains` - Domain CRUD
- `DELETE /domains/:name` - Delete domain
- `GET/POST /users` - User/mailbox CRUD
- `DELETE /users/:id` - Delete user
- `GET /queue` - Mail queue
- `DELETE /queue/:id` - Delete queue message
- `POST /queue/:id/retry` - Retry queue message

### Firewall
- `GET/POST /firewall/rules` - Rule CRUD
- `GET /firewall/logs` - Block logs

### Modules
- `GET /modules` - Registered modules
- `GET /license` - License info
- `POST /license/validate` - Validate license key
- `GET /audit/logs` - Audit logs
- `GET/PUT /feature-flags` - Feature flags
- `GET/POST/DELETE /api-keys` - API key management

### Auto-Heal
- `GET /heal/history` - Health check history
- `POST /heal/check/:name` - Run health check

### Guardian
- `POST /guardian/analyze` - Analyze email threat
- `GET /guardian/logs` - Analysis logs

### Smart Compose AI
- `POST /compose/complete` - AI completion
- `POST /compose/stream` - AI streaming completion (SSE)

### DNS
- `POST /dns/check/:domain` - DNS validation
- `POST /dns/wizard/:domain` - DNS wizard

### Migration
- `POST /migration/test` - Test IMAP connection
- `POST /migration/start` - Start migration
- `GET /migration/jobs` - Migration jobs

### Provision
- `POST /provision/domain` - Instant provision

### Calendar/Contacts/Tasks
- `GET/POST /calendar/events` - Events CRUD
- `PUT/DELETE /calendar/events/:id` - Event update/delete
- `GET/POST /contacts` - Contacts CRUD
- `PUT/DELETE /contacts/:id` - Contact update/delete
- `GET/POST /tasks` - Tasks CRUD
- `PUT/PATCH/DELETE /tasks/:id` - Task update/complete/delete

### Updates
- `GET /updates/check` - Check for updates
- `GET /updates/changelog` - Changelog
- `POST /updates/apply/:module` - Apply update

### Intelligence
- `GET /intelligence/stats` - Email statistics
- `GET /intelligence/delivery` - Delivery reports

### Compliance
- `GET/POST /compliance/legal-holds` - Legal hold CRUD
- `PUT/DELETE /compliance/legal-holds/:id` - Legal hold update/delete
- `GET/POST /compliance/policies` - Retention policies CRUD
- `PUT/DELETE /compliance/policies/:id` - Policy update/delete

### Collaboration
- `GET/POST /collaboration/mailboxes` - Shared mailbox CRUD

## Project Structure

```
D:\orvix\
├── cmd/orvix/main.go                # Entry point — starts all modules
├── embed.go                         # Embedded Stalwart binary (removed — external)
├── internal/
│   ├── api/                         # HTTP router + 17 handler files
│   ├── auth/                        # JWT, Argon2id, RBAC, CSRF, API keys, rate limit, security
│   ├── autoheal/                    # Health checks, action history
│   ├── calendar/                    # Events, contacts, tasks models + CRUD
│   ├── collaboration/               # Shared mailboxes
│   ├── compliance/                  # Legal holds, retention, ZKE encryption
│   ├── compose/                     # Smart Compose AI (DeepSeek streaming)
│   ├── config/                      # Viper, GORM, Zap, AES-256-GCM
│   ├── dns/                         # DNS automation (Cloudflare provider)
│   ├── firewall/                    # Pipeline, rules engine, IP reputation
│   ├── guardian/                    # Threat analysis (DeepSeek + offline)
│   ├── intelligence/                # Email analytics, delivery reports
│   ├── license/                     # RS256 validation, feature flags, fingerprint
│   ├── metrics/                     # Prometheus metrics
│   ├── migration/                   # IMAP sync engine
│   ├── models/                      # GORM models
│   ├── modules/                     # Module registry
│   ├── provision/                   # Instant deployment API
│   ├── stalwart/                    # REST client, webhooks, process manager, config
│   └── updater/                     # Update manager, changelog, rollback
├── web/
│   ├── webmail/                     # React 19 webmail (Vite + Tailwind)
│   └── admin/                       # React 19 admin console
├── migrations/                      # SQL migrations (additive only)
├── scripts/                         # Build + install scripts
├── stalwart-bin/                    # Stalwart binary location
├── go.mod
├── Makefile
└── orvix.yaml                       # Default config
```

## Test Coverage

| Package | Tests | What's tested |
|---------|-------|--------------|
| `internal/auth` | 15 | JWT persistence, token validation, password hash/verify, key load/generate, token expiry, RBAC |
| `internal/config` | 8 | AES-256-GCM encrypt/decrypt, watermark, canary tokens |
| `internal/firewall` | 8 | Pipeline scoring, verdict thresholds, cancellation, multi-layer |
| `internal/license` | 5 | Feature flags for SMB/ISP/Enterprise tiers |
| `internal/modules` | 7 | Module registry, registration, duplicates, lifecycle |
| `internal/stalwart` | 8 | Client config, structs, event handlers, process manager |
| **Total** | **51** | All passing |

## Known Issues

1. **Go 1.25 requirement**: Fiber v3 requires Go 1.25+. Older Go versions will not compile.
2. **SQLite CGO dependency**: SQLite driver requires CGO. For fully static builds, use PostgreSQL.
3. **Stalwart binary**: Must be downloaded separately from https://stalw.art/download.
4. **Redis not required**: System works without Redis (falls back to in-memory rate limiting + DB sessions).
5. **Coverage tooling**: Go 1.25 coverage tooling may differ. `go test -cover` may need specific flags.
6. **Windows/Unix paths**: Config file paths default to `/etc/orvix/`. On Windows, use `orvix.yaml` in project root.
7. **Security alerts**: Alert detection works but no SMTP delivery — log-only.
8. **Frontend mock data removed**: React apps show loading/empty/error states when backend unavailable.
9. **No multi-tenancy**: Database is single-tenant. Cross-tenant isolation not implemented.
10. **Frontend SPA routing**: Admin served at `/admin`, webmail at `/`. Proper fallback routing needed.
11. **Admin credentials**: Must provide `ORVIX_ADMIN_EMAIL` and `ORVIX_ADMIN_PASSWORD` env vars. No default credentials.
