# Orvix Enterprise Mail (OrvixEM)

> Stalwart Core Engine · Orvix Enterprise Layer · License-Driven Features · Enterprise Grade

**Domain:** [orvix.email](https://orvix.email)  
**Repository:** `github.com/orvixemail/orvix`  
**Binary:** `orvix`  
**License:** Commercial (3 tiers: SMB / ISP / Enterprise)

---

## Overview

Orvix Enterprise Mail is a self-hosted, white-label enterprise email platform built around **Stalwart Core Engine**. Instead of rebuilding SMTP, IMAP, POP3, and JMAP from scratch, OrvixEM focuses on the commercial enterprise layer:

- Enterprise administration
- Licensing and feature control
- Multi-tenant hosting/provider management
- Reseller and data center operations
- Migration automation
- Safe updates and rollback
- Guardian AI security
- Smart Compose AI
- Email Intelligence

## Quick Start

```bash
# Download and install (one line)
curl -sSL https://get.orvix.email | bash

# Or build from source
git clone https://github.com/orvixemail/orvix.git
cd orvix
make build
./orvix start
```

## Configuration

OrvixEM is configured via `orvix.yaml`. Default location: `/etc/orvix/orvix.yaml`

```bash
# Copy default config
cp configs/orvix.yaml /etc/orvix/orvix.yaml

# Environment variables override config values
export ORVIX_SERVER_LISTEN=:8080
export ORVIX_DATABASE_DRIVER=sqlite
```

See [configs/orvix.yaml](configs/orvix.yaml) for all options.

## Architecture

```
┌──────────────────────────────────────────────────────────────┐
│                  Orvix Enterprise Mail Platform               │
│                                                                │
│  ┌────────────────────────────────────────────────────────┐  │
│  │                  Stalwart Core Engine                    │  │
│  │  SMTP │ IMAP │ POP3 │ JMAP │ Mail Store │ Queue │ Auth │  │
│  └────────────────────────────────────────────────────────┘  │
│                            │                                   │
│                            ▼                                   │
│  ┌────────────────────────────────────────────────────────┐  │
│  │                  Orvix Enterprise Layer                  │  │
│  │  License │ Admin │ Webmail │ API │ Security │ Migration │  │
│  │  Guardian │ Auto-Heal │ Updates │ DNS │ Monitoring     │  │
│  └────────────────────────────────────────────────────────┘  │
└──────────────────────────────────────────────────────────────┘
```

## License Tiers

| Tier | Price | Mailboxes | Domains |
|------|-------|-----------|---------|
| SMB | $500/year | Up to 500 | Up to 10 |
| ISP | $1,200/year | Up to 50,000 | Unlimited |
| Enterprise | $2,500/year | Unlimited | Unlimited |

## API Endpoints

| Method | Path | Description |
|--------|------|-------------|
| GET | `/health` | Health check |
| GET | `/version` | Version info |
| GET | `/api/v1/license/status` | License status |
| GET | `/api/v1/features` | Feature flags |
| POST | `/api/v1/auth/login` | User login |
| POST | `/api/v1/auth/refresh` | Refresh token |
| GET | `/api/v1/admin/stats` | Admin statistics |
| GET | `/api/v1/stalwart/status` | Stalwart status |
| GET | `/api/v1/stalwart/health` | Stalwart health |
| GET | `/metrics` | Prometheus metrics |

## Development

```bash
# Prerequisites
go 1.23+
node 20+ (for frontend)

# Build
make build

# Run tests
make test

# Run development server
make dev

# Code formatting
make fmt
```

## Docker

```bash
# Build and run with Docker Compose
docker-compose up -d

# Or build standalone
docker build -t orvixemail/orvix .
```

## Services Map

| Hostname | Purpose |
|----------|---------|
| orvix.email | Marketing website |
| portal.orvix.email | Customer portal |
| admin.orvix.email | Admin control panel |
| license.orvix.email | License authority |
| mail.orvix.email | Webmail |
| api.orvix.email | Public API |
| docs.orvix.email | Documentation |
| updates.orvix.email | Update server |
| status.orvix.email | Status page |

## Support

- Documentation: [docs.orvix.email](https://docs.orvix.email)
- License: [license.orvix.email](https://license.orvix.email)
- Status: [status.orvix.email](https://status.orvix.email)
