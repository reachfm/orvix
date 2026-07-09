# Orvix RC4 Release Acceptance Gate

**Version:** 1.0.3-rc4
**Tag:** v1.0.3-rc4
**Channel:** stable
**Commit:** 277af64 (fix: run admin functional smoke with secure session cookie)
**Date:** 2026-07-09
**Release Bundle:** orvix-enterprise-mail-stable-linux-amd64.tar.gz

---

## Pre-upgrade backup

Run on the target VPS before the upgrade:

```bash
# 1. Stop the service
sudo systemctl stop orvix

# 2. Backup the full service directory
sudo cp -a /etc/orvix /etc/orvix.bak.$(date +%Y%m%d-%H%M%S)

# 3. Backup the binary
sudo cp /usr/local/bin/orvix /usr/local/bin/orvix.bak.$(date +%Y%m%d-%H%M%S)

# 4. Snapshot database (if sqlite)
sudo cp /var/lib/orvix/orvix.db /var/lib/orvix/orvix.db.bak.$(date +%Y%m%d-%H%M%S)

# 5. Snapshot any Stalwart data
if [ -d /opt/stalwart ]; then
  sudo cp -a /opt/stalwart /opt/stalwart.bak.$(date +%Y%m%d-%H%M%S)
fi

# 6. Capture current package list
dpkg -l > /root/pre-rc4-packages-$(date +%Y%m%d-%H%M%S).txt
```

---

## Current service / version capture

```bash
echo "=== ORVIX VERSION ==="
/usr/local/bin/orvix version --full 2>/dev/null || echo "orvix binary not found / not running"

echo "=== ORVIX SERVICE ==="
sudo systemctl status orvix --no-pager -l 2>/dev/null || true

echo "=== CURRENT COMMIT ==="
cat /etc/orvix/orvix.yaml 2>/dev/null | grep -i commit || true
cat /etc/orvix/bootstrap.env 2>/dev/null | grep -i commit || true
```

---

## Installer command

Copy-paste the full installer on the target VPS as root:

```bash
sudo bash -c "$(curl -fsSL https://raw.githubusercontent.com/reachfm/orvix/main/release/install-public.sh)"
```

**If you need to specify the exact bundle URL (air-gap / pinned version):**

```bash
export ORVIX_BUNDLE_URL="https://github.com/reachfm/orvix/releases/download/v1.0.3-rc4/orvix-enterprise-mail-stable-linux-amd64.tar.gz"
sudo -E bash -c "$(curl -fsSL https://raw.githubusercontent.com/reachfm/orvix/main/release/install-public.sh)"
```

**If you need a non-interactive install:**

```bash
export ORVIX_NON_INTERACTIVE=1
export ORVIX_DOMAIN="mail.example.com"
export ORVIX_PUBLIC_IPV4="203.0.113.1"
export ORVIX_ADMIN_EMAIL="admin@example.com"
export ORVIX_ADMIN_PASSWORD="YourSecurePassword123"
sudo -E bash -c "$(curl -fsSL https://raw.githubusercontent.com/reachfm/orvix/main/release/install-public.sh)"
```

---

## Health check

```bash
# 1. Wait for service to start
sleep 5

# 2. Check systemd
sudo systemctl is-active orvix

# 3. API health endpoint
curl -fsS http://localhost:8080/api/v1/health

# 4. Admin page serves
curl -fsS -o /dev/null -w '%{http_code}' http://localhost:8080/admin/

# 5. Webmail page serves
curl -fsS -o /dev/null -w '%{http_code}' http://localhost:8080/webmail/

# 6. Orvix doctor (full diagnostic)
sudo bash /etc/orvix/scripts/orvix-doctor.sh
```

---

## Admin login check

```bash
# CSRF token
CSRF=$(curl -fsS -c /tmp/orvix-cookies http://localhost:8080/api/v1/csrf-token | grep -o '"csrf_token":"[^"]*"' | cut -d'"' -f4)

# Login
curl -fsS -b /tmp/orvix-cookies -c /tmp/orvix-cookies \
  -H "X-CSRF-Token: $CSRF" \
  -H "Content-Type: application/json" \
  -d '{"email":"admin@example.com","password":"YourSecurePassword123"}' \
  http://localhost:8080/api/v1/auth/login

# Verify opaque session cookie is set
grep -c '__Host-orvix_session' /tmp/orvix-cookies

# Access protected admin endpoint
curl -fsS -b /tmp/orvix-cookies http://localhost:8080/api/v1/me

# Access admin settings
curl -fsS -b /tmp/orvix-cookies http://localhost:8080/api/v1/admin/settings
```

**Assertions:**
- [ ] Login returns 200 with `"status":"ok"`
- [ ] `__Host-orvix_session` cookie is present in cookie jar
- [ ] `/api/v1/me` returns email + roles
- [ ] `/api/v1/admin/settings` returns runtime config

---

## Webmail login check

```bash
# Webmail login
curl -fsS -b /tmp/orvix-webmail-cookies -c /tmp/orvix-webmail-cookies \
  -H "Content-Type: application/json" \
  -d '{"email":"admin@example.com","password":"YourSecurePassword123"}' \
  http://localhost:8080/api/v1/webmail/login

# Verify opaque session cookie is set
grep -c '__Host-orvix_session' /tmp/orvix-webmail-cookies

# Access webmail folder list
curl -fsS -b /tmp/orvix-webmail-cookies http://localhost:8080/api/v1/webmail/folders
```

**Assertions:**
- [ ] Login returns 200
- [ ] `__Host-orvix_session` cookie is present
- [ ] Folder list returns Inbox, Sent, Drafts, Trash, Archive

---

## `/api/v1/health` check

```bash
curl -fsS http://localhost:8080/api/v1/health | python3 -m json.tool
```

**Assertions:**
- [ ] HTTP 200
- [ ] Response contains `"status":"ok"`

---

## Ports check

```bash
sudo ss -tlnp | grep -E ':(25|143|465|587|993|8080|8443)'
```

Expected ports (varies by install):
- [ ] 25   — SMTP (CoreMail)
- [ ] 143  — IMAP (CoreMail)
- [ ] 587  — SMTP Submission (if TLS configured)
- [ ] 993  — IMAPS (CoreMail TLS)
- [ ] 8080 — Orvix Admin API
- [ ] 8443 — Webmail HTTPS

---

## MTA-STS HTTPS check

```bash
# Check MTA-STS policy is served over HTTPS
curl -fsS -o /dev/null -w '%{http_code}' https://mail.example.com/.well-known/mta-sts.txt 2>/dev/null || echo "MTA-STS not configured (expected on fresh install)"

# If TLS is set up:
# curl -fsS https://mail.example.com/.well-known/mta-sts.txt
```

---

## Service logs check

```bash
# Last 50 lines of service log
sudo journalctl -u orvix --no-pager -n 50

# Errors only (last 5 minutes)
sudo journalctl -u orvix --since "5 min ago" --no-pager -p err

# Verify no FATAL or PANIC messages
sudo journalctl -u orvix --since "5 min ago" --no-pager | grep -iE 'fatal|panic|segfault|corrupt' || echo "No fatal/panic messages"
```

---

## Two-console admin SPA check

```bash
# Customer console renders
curl -fsS -o /dev/null -w '%{http_code}' http://localhost:8080/admin/

# Internal console modules (loaded by app.js)
for mod in \
  customer/dashboard.js customer/domains.js customer/users.js customer/groups.js \
  customer/security.js customer/mail-flow.js customer/reports.js customer/settings.js \
  internal/overview.js internal/tenants.js internal/domain-intelligence.js \
  internal/security-ops.js internal/mail-flow-ops.js internal/runtime.js \
  internal/observability.js internal/branding.js saas-shared.js; do
  code=$(curl -fsS -o /dev/null -w '%{http_code}' "http://localhost:8080/admin/modules/pages/$mod")
  echo "$mod: $code"
done

# Legacy route aliases accessible
for route in dashboard domains mailboxes accounts queue dns backups updates monitoring logs settings; do
  code=$(curl -fsS -o /dev/null -w '%{http_code}' "http://localhost:8080/admin/#/$route" 2>/dev/null || echo "302")
  echo "/$route: $code"
done
```

**Assertions:**
- [ ] All 16 two-console modules return HTTP 200
- [ ] `saas-shared.js` returns HTTP 200
- [ ] Admin index.html is served
- [ ] Legacy route aliases resolve (302 or 200)

---

## Rollback plan

If the RC4 upgrade fails:

```bash
# 1. Stop the service
sudo systemctl stop orvix

# 2. Restore backup config
sudo rm -rf /etc/orvix
sudo cp -a /etc/orvix.bak.YYYYMMDD-HHMMSS /etc/orvix

# 3. Restore binary
sudo cp /usr/local/bin/orvix.bak.YYYYMMDD-HHMMSS /usr/local/bin/orvix

# 4. Restore database
sudo cp /var/lib/orvix/orvix.db.bak.YYYYMMDD-HHMMSS /var/lib/orvix/orvix.db

# 5. Start service
sudo systemctl start orvix

# 6. Verify health
curl -fsS http://localhost:8080/api/v1/health
sudo systemctl status orvix --no-pager
```

Replace `YYYYMMDD-HHMMSS` with the timestamp captured during backup.

---

## PASS / FAIL checklist

| Check | Status |
|---|---|
| Pre-upgrade backup completed | [ ] |
| Service version captured | [ ] |
| Installer completed without error | [ ] |
| `systemctl is-active orvix` returns active | [ ] |
| `/api/v1/health` returns 200 `{"status":"ok"}` | [ ] |
| Admin page serves (HTTP 200) | [ ] |
| Webmail page serves (HTTP 200) | [ ] |
| Admin login sets `__Host-orvix_session` cookie | [ ] |
| `/api/v1/me` returns profile | [ ] |
| `/api/v1/admin/settings` returns config | [ ] |
| Webmail login works | [ ] |
| Webmail folder list returns folders | [ ] |
| All 16 two-console modules return 200 | [ ] |
| `saas-shared.js` returns 200 | [ ] |
| Legacy routes resolve | [ ] |
| Expected ports listening | [ ] |
| No fatal/panic in service logs | [ ] |
| MTA-STS policy served (if TLS configured) | [ ] |
| Rollback plan documented | [ ] |

---

**VPS:** _______________
**IP:** _______________
**Operator:** _______________
**Date:** _______________
**Result:** PASS / FAIL / NEEDS FIX
**Notes:** _______________
