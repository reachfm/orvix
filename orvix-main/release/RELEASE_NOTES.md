# Orvix v1.0.1 Release Notes

## Overview
Orvix is a self-hosted email server platform built on top of Stalwart Mail Server.
**RC2 - Pure Go SQLite Build** - No CGO required.

## RC2 Changes (2026-06-06)
- ✅ Replaced `mattn/go-sqlite3` with `modernc.org/sqlite` (pure Go, no CGO)
- ✅ Custom GORM dialector for SQLite compatibility
- ✅ Raw SQL migrations (bypasses PostgreSQL-specific AutoMigrate)
- ✅ Fixed CORS configuration (invalid email URL in allowed_origins)
- ✅ Build: `CGO_ENABLED=0 go build` - static binary ready
- ✅ VPS deployment verified (65.75.203.74)

## What's Included
- Single binary: `orvix-linux-amd64`
- Admin console (React SPA)
- Webmail (React SPA)
- One-command installer: `bash install.sh`
- systemd service

## Features
- Domain/mailbox/queue management via Stalwart API
- JWT authentication with refresh token rotation
- Argon2id password hashing
- RBAC (superadmin, admin, user)
- CSRF protection, CORS, rate limiting
- Mail firewall with pipeline engine and rules engine
- Guardian AI threat analysis (DeepSeek, optional)
- Smart Compose AI (DeepSeek, optional)
- Auto-heal health checks
- DNS automation
- Smart IMAP migration
- Instant domain/mailbox provisioning
- Calendar, Contacts, Tasks CRUD APIs
- Zero-knowledge encryption (client-side)
- Multi-tenant support
- LDAP sync engine
- Security alert delivery (SMTP/webhook)
- Backup management
- Prometheus metrics
- Comprehensive audit logging

## System Requirements
- Ubuntu 22.04+ or Debian 12+
- 2GB RAM minimum
- 20GB disk minimum
- Stalwart Mail Server (downloaded automatically)

## Quick Start
```bash
curl -fsSL https://orvix.email/install.sh | bash
```

## Upgrade
```bash
bash upgrade.sh [version]
```

## Uninstall
```bash
bash uninstall.sh
```

## Known Limitations
- Stalwart is an external dependency (downloaded by installer)
- Redis is optional (falls back to in-memory rate limiting)
- ActiveSync protocol not included
- SSO/OAuth full redirect flow not included
- Multi-cloud storage not included
