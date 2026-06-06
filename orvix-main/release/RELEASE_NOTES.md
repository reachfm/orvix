# Orvix v1.0.0 Release Notes

## Overview
Orvix is a self-hosted email server platform built on top of Stalwart Mail Server.
This is the first release candidate.

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
