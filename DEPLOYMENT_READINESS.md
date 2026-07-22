# OrvixEM Deployment Readiness

**Date:** 2026-06-05

---

## Linux Deployment (Ubuntu 24.04)

| Requirement | Status | Evidence |
|---|---|---|
| Binary builds | ✅ | `go build -o orvix ./cmd/orvix` passes |
| Config file | ✅ | `configs/orvix.yaml` with all sections |
| Data directories | ✅ | Created by install script |
| Log directories | ✅ | `/var/log/orvix` |

## Docker Deployment

| Requirement | Status | Evidence |
|---|---|---|
| Dockerfile | ✅ | Multi-stage (Node → Go → Alpine) |
| docker-compose.yml | ✅ | Orvix + optional PostgreSQL/Redis/Stalwart |
| Health check | ✅ | `curl -sf http://localhost:8080/healthz` |
| Volume mounts | ✅ | `orvix_data`, `orvix_config` |
| Port mapping | ✅ | 8080:8080 |

## Standalone Binary Deployment

| Requirement | Status | Evidence |
|---|---|---|
| Single binary | ✅ | All frontends embedded via `embed.FS` |
| No external runtime | ✅ | SQLite via pure Go (no CGO) |
| Embedded frontend | ✅ | `web/embed.go` - admin/webmail/portal |
| Config discovery | ✅ | Searches current dir, ./configs/, /etc/orvix/, $HOME/.orvix |
| Env var overrides | ✅ | All ORVIX_* vars supported |

## systemd Deployment

| Requirement | Status | Evidence |
|---|---|---|
| Unit file | ✅ | `packaging/systemd/orvix.service` |
| User creation | ✅ | Created by install script |
| Service management | ✅ | `systemctl start|stop|restart orvix` |

## PostgreSQL Mode

| Requirement | Status | Evidence |
|---|---|---|
| PostgreSQL driver | ✅ | gorm.io/driver/postgres |
| Config support | ✅ | `database.driver: postgres` |
| DSN support | ✅ | Full connection string in config |

## SQLite Mode

| Requirement | Status | Evidence |
|---|---|---|
| SQLite driver | ✅ | github.com/glebarez/sqlite (pure Go) |
| No CGO | ✅ | CGO_ENABLED=0 build works |
| WAL mode | ✅ | `_journal_mode=WAL` in default DSN |
| Busy timeout | ✅ | `_busy_timeout=5000` |

## Redis (Optional)

| Requirement | Status | Evidence |
|---|---|---|
| Config | ✅ | `redis.address`, `redis.password`, `redis.db` |
| Optional | ✅ | Only used when configured |
| Feature flags | ✅ | Present in config |

## Stalwart Integration Mode

| Requirement | Status | Evidence |
|---|---|---|
| Binary detection | ✅ | Config, env var, PATH, common paths |
| Config generation | ✅ | TOML template in stalwart.go |
| Config validation | ✅ | `orvix stalwart validate` |
| Lifecycle | ✅ | systemd + direct binary fallback |
| CLI commands | ✅ | 6 stalwart subcommands |

## Missing Files Check

| File | Status |
|---|---|
| orvix binary | ❌ Must be built (`go build ./cmd/orvix`) |
| configs/orvix.yaml | ✅ Present |
| scripts/install.sh | ✅ Present |
| scripts/build.sh | ✅ Present |
| scripts/release.sh | ✅ Present |
| packaging/systemd/orvix.service | ✅ Present |
| Dockerfile | ✅ Present |
| docker-compose.yml | ✅ Present |
| Makefile | ✅ Present |
| docs/STALWART_INTEGRATION.md | ✅ Present |
| docs/API.md | ✅ Present |
| .github/workflows/ci.yml | ✅ Present |

## Missing Environment Variables

| Variable | Required | Default | Notes |
|---|---|---|---|
| ORVIX_SECURITY_JWT_SECRET | ✅ YES | none | Must be set in production |
| ORVIX_DATABASE_DSN | Optional | SQLite file | PostgreSQL DSN for production |
| ORVIX_STALWART_BINARY | Optional | detected | Override binary path |
| ORVIX_GUARDIAN_API_KEY | Optional | none | For DeepSeek AI |
| ORVIX_COMPOSE_API_KEY | Optional | none | For Smart Compose AI |
| ORVIX_SERVER_LISTEN | Optional | :8080 | Listen address |

## Deployment Requirements

| Requirement | Minimum | Recommended |
|---|---|---|
| CPU | 1 core | 2 cores |
| RAM | 512 MB | 2 GB |
| Disk | 1 GB | 10 GB |
| OS | Linux x86_64 | Ubuntu 24.04 LTS |
| Go version | 1.23+ | 1.23 |
| Node.js (build only) | 20+ | 20 LTS |

## Deployment Order

1. Build binary (`make build` or `scripts/build.sh`)
2. Create directories (`/etc/orvix`, `/var/lib/orvix/{data,rollback,snapshots}`)
3. Copy binary to `/usr/local/bin/orvix`
4. Copy `configs/orvix.yaml` to `/etc/orvix/orvix.yaml`
5. Edit config with production values (JWT secret, DB, etc.)
6. Install systemd service (optional)
7. Start server (`orvix start` or `systemctl start orvix`)
8. Bootstrap admin (`POST /api/v1/admin/bootstrap`)
9. Verify health (`curl /healthz`)

## Verdict

**READY FOR VPS DEPLOYMENT** with the following caveats:
- Set `ORVIX_SECURITY_JWT_SECRET` to a secure random value
- Configure PostgreSQL for production (SQLite for testing/dev)
- Install Stalwart separately for full mail functionality
- Configure DNS records for orvix.email domain
