# OrvixEM Deployment Validation Checklist

**Server:** 
**Date:** 
**Verified by:** 

---

## Pre-Deployment

- [ ] Ubuntu 24.04 LTS installed and updated
- [ ] SSH key authentication configured (password auth disabled)
- [ ] Firewall enabled (UFW) with correct rules
- [ ] DNS records propagated (mail/admin/portal/api point to server IP)
- [ ] Required software installed (curl, wget, git, postgresql, nginx)
- [ ] Ports 22, 80, 443, 8080 open in firewall

## Binary Installation

- [ ] Binary installed at `/usr/local/bin/orvix`
- [ ] Binary is executable (`sudo chmod +x`)
- [ ] Version matches expected (`orvix version`)
- [ ] Frontend assets embedded (`orvix status` shows all 3 frontends available)
- [ ] Config directory created (`/etc/orvix`)
- [ ] Data directories created (`/var/lib/orvix/data`, `/var/lib/orvix/rollback`, `/var/lib/orvix/snapshots`)
- [ ] Log directory created (`/var/log/orvix`)
- [ ] Config file permissions 0600 (`sudo chmod 600 /etc/orvix/orvix.yaml`)
- [ ] `ORVIX_SECURITY_JWT_SECRET` set to secure random value
- [ ] Environment variables configured in `/etc/profile.d/orvix.sh`

## Database (PostgreSQL)

- [ ] PostgreSQL 16 installed and running
- [ ] Database `orvix` created
- [ ] User `orvix` created with password
- [ ] Connection works from localhost
- [ ] Connection string configured in `orvix.yaml` or `ORVIX_DATABASE_DSN`
- [ ] Migrations run successfully on first start

## systemd Service

- [ ] Service file installed at `/etc/systemd/system/orvix.service`
- [ ] Service enabled (`sudo systemctl enable orvix`)
- [ ] Service started (`sudo systemctl start orvix`)
- [ ] Service status shows active (running)
- [ ] Service restarts on failure
- [ ] Logs visible (`sudo journalctl -u orvix -n 50`)

## Health Endpoint

- [ ] `GET /healthz` returns HTTP 200
- [ ] Response includes `database: true`
- [ ] Response includes `frontend.admin: true`
- [ ] Response includes `frontend.webmail: true`
- [ ] Response includes `frontend.portal: true`
- [ ] Response includes `status: "ok"`

## Login

- [ ] `POST /api/v1/admin/bootstrap` creates admin account
- [ ] `POST /api/v1/auth/login` returns JWT access token
- [ ] Login with wrong password returns 401
- [ ] Login with correct password returns 200 with token
- [ ] Access token can be used for authenticated requests

## Admin Panel

- [ ] Dashboard loads (stats, health, license)
- [ ] Tenants page loads and shows tenants
- [ ] Create tenant works
- [ ] Domains page loads
- [ ] Create domain works (generates DKIM/SPF/DMARC)
- [ ] Users page loads
- [ ] Create user works
- [ ] License page loads (shows status or purchase link)
- [ ] Feature flags page loads (shows all 44 flags)
- [ ] DNS wizard page loads and can check domains
- [ ] Mail queue page loads with stats
- [ ] Firewall rules CRUD works
- [ ] Geo-blocking CRUD works

## Webmail

- [ ] Login page loads at `/mail`
- [ ] Login works with JWT credentials
- [ ] Inbox layout displays (sidebar, message list, reading pane)
- [ ] Folder navigation works (Inbox/Sent/Drafts/Spam/Trash/Archive)
- [ ] Compose button opens modal
- [ ] Compose modal has TipTap editor with toolbar (B/I/U)
- [ ] Send creates mail queue entry
- [ ] Settings page loads (profile, 2FA, sessions)
- [ ] Contacts page loads (create/list)
- [ ] Calendar page loads
- [ ] Tasks page loads (add/complete/delete)

## Customer Portal

- [ ] Login page loads at `/portal`
- [ ] Dashboard loads with stats
- [ ] Domains list loads
- [ ] License overview loads
- [ ] Support page loads with contact info
- [ ] Downloads page loads with installer info
- [ ] Changelog page loads

## Backups

- [ ] `POST /api/v1/backups` creates backup successfully
- [ ] `GET /api/v1/backups` lists created backups
- [ ] Backup file exists on disk with correct size
- [ ] Backup can be listed by ID

## Updates

- [ ] `orvix update-check` runs without error
- [ ] `orvix update-apply` handles missing updates gracefully
- [ ] `orvix update-rollback` handles missing rollback gracefully
- [ ] Update channel can be changed in config

## Provisioning

- [ ] Provisioning jobs are created when domains are added
- [ ] Provisioning jobs are visible at `/admin/provisioning-jobs`
- [ ] Job status progresses through pending → running → completed

## Mail Queue

- [ ] Mail queue stats endpoint returns data
- [ ] Queued mail can be listed
- [ ] Failed mail can be retried
- [ ] Mail can be deleted from queue

## DNS Wizard

- [ ] DNS check works for real domains (MX, SPF, DKIM, DMARC)
- [ ] Managed domains list shows DNS records (DKIM selector, SPF, DMARC)

## AI Providers

- [ ] Guardian status page loads
- [ ] Guardian analyze endpoint works (local mode fallback)
- [ ] Smart Compose suggest endpoint works
- [ ] Smart Compose summarize endpoint works
- [ ] AI providers show as disabled when not configured

## License System

- [ ] License status endpoint returns status
- [ ] License page shows pricing when no license active
- [ ] Feature flags show correct enabled/disabled state per tier
- [ ] License gate middleware blocks disabled features

## Reverse Proxy (Nginx)

- [ ] Nginx installed and running
- [ ] HTTPS works with valid certificate
- [ ] HTTP redirects to HTTPS
- [ ] `/admin` routes to admin console
- [ ] `/mail` routes to webmail
- [ ] `/portal` routes to customer portal
- [ ] `/api/v1/*` routes to API
- [ ] WebSocket (if applicable) works through proxy

## Post-Deployment

- [ ] First admin bootstrap completed
- [ ] Admin can log in
- [ ] Admin panel fully functional
- [ ] Webmail accessible and login works
- [ ] Customer portal accessible and login works
- [ ] Cron backup job configured (if desired)
- [ ] Monitoring/alerting configured (if desired)
- [ ] Security headers verified (CSP, HSTS, X-Frame-Options)

---

## Sign-Off

**Deployment verified by:** _________________________

**Date:** _________________________

**Issues found:** ___________________________________

**Signed off:** ____________________________________
