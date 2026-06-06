# Orvix RC2 VPS Test Plan

## Test Date: 2026-06-06

## Version: 1.0.1 (RC2)

---

## Purpose

This test plan validates that RC2 fixes all RC1 deployment failures on a clean Ubuntu VPS.

---

## Prerequisites

- Clean Ubuntu 22.04 or 24.04 VPS (minimal install)
- Root SSH access
- Domain name with DNS access (optional but recommended)

---

## RC1 Issues Being Tested

| Issue | RC1 Error | RC2 Fix |
|-------|-----------|---------|
| SQLite CGO | "Binary was compiled with CGO_ENABLED=0" | Pure Go SQLite |
| Stalwart 404 | "404 Not Found" | Corrected URL |
| systemd warning | "Unknown key StartLimitIntervalSec" | Removed directive |
| Prompts | No validation | Added validation |

---

## Test Steps

### Step 1: Fresh VPS Setup

```bash
# Connect to VPS
ssh root@YOUR_VPS_IP

# Verify clean state
uname -a
cat /etc/os-release
df -h
free -m

# Check for any existing orvix installations
which orvix
systemctl status orvix.service 2>/dev/null || echo "No existing orvix"
```

**Expected:** Clean Ubuntu 22.04/24.04 with no Orvix installed

---

### Step 2: Download RC2

```bash
# Download release
wget https://github.com/reachfm/orvix/releases/v1.0.1/orvix-v1.0.1-linux-amd64.tar.gz

# Extract
tar -xzf orvix-v1.0.1-linux-amd64.tar.gz
cd release/

# Verify files
ls -la
```

**Expected:** Archive extracts cleanly with all files present

---

### Step 3: Run Installer

```bash
# Run installer as root
bash install.sh
```

**Expected Prompts:**
```
Primary email domain (e.g., example.com): [user input with validation]
Admin email address: [user input with validation]
Admin password (min 12 chars): [masked input]
License key (optional, press Enter to skip): [optional]
```

**No Expected Errors:**
- ✅ No CGO errors
- ✅ No 404 errors
- ✅ No systemd warnings

---

### Step 4: Verify Installation

```bash
# Check service status
systemctl status orvix.service

# Expected output:
# ● orvix.service - Orvix Email Server Platform
#    Loaded: loaded (/etc/systemd/system/orvix.service; enabled)
#    Active: active (running) since ...
```

**No Expected Warnings:**
- ✅ No "Unknown key" warnings
- ✅ No CGO errors in logs

---

### Step 5: Health Check

```bash
# Test health endpoint
curl -s http://localhost:8080/api/v1/health

# Expected:
{"status":"ok"}
```

---

### Step 6: Login Test

```bash
# Get CSRF token first
curl -s -c cookies.txt http://localhost:8080/api/v1/csrf-token

# Login (use credentials from installation)
curl -s -X POST http://localhost:8080/api/v1/auth/login \
  -H "Content-Type: application/json" \
  -d '{"email":"admin@YOUR_DOMAIN","password":"YOUR_PASSWORD"}' \
  -c cookies.txt

# Expected:
{
  "access_token": "...",
  "refresh_token": "...",
  "access_expires_in": 900,
  "refresh_expires_in": 2592000
}
```

---

### Step 7: Domain Creation

```bash
# Get CSRF token
CSRF=$(curl -s -b cookies.txt http://localhost:8080/api/v1/csrf-token | grep -o '"csrf_token":"[^"]*"' | cut -d'"' -f4)

# Create domain
curl -s -X POST http://localhost:8080/api/v1/domains \
  -H "Content-Type: application/json" \
  -H "X-CSRF-Token: $CSRF" \
  -b cookies.txt \
  -d '{"name":"YOUR_DOMAIN"}'

# Expected: Domain created successfully
```

---

### Step 8: Mailbox Creation

```bash
# Create mailbox
curl -s -X POST http://localhost:8080/api/v1/users \
  -H "Content-Type: application/json" \
  -H "X-CSRF-Token: $CSRF" \
  -b cookies.txt \
  -d '{"name":"Test User","email":"test@YOUR_DOMAIN","password":"TestPass123!"}'

# Expected: User created successfully
```

---

### Step 9: Service Restart

```bash
# Restart service
systemctl restart orvix.service
sleep 5
systemctl status orvix.service
curl -s http://localhost:8080/api/v1/health
```

**Expected:** Service restarts cleanly, health check passes

---

### Step 10: Logs Verification

```bash
# Check for any errors
journalctl -u orvix.service --since "5 minutes ago" | grep -i error

# Expected: No CGO-related errors
# Expected: No 404 errors
# Expected: No systemd warnings
```

---

## Validation Checklist

| Test | Status | Notes |
|------|--------|-------|
| Fresh VPS baseline | ⬜ | |
| RC2 download | ⬜ | |
| RC2 extract | ⬜ | |
| Installer runs without CGO error | ⬜ | |
| Installer runs without 404 error | ⬜ | |
| Installer prompts with validation | ⬜ | |
| systemd service installs without warnings | ⬜ | |
| Service starts successfully | ⬜ | |
| Health endpoint responds | ⬜ | |
| Login works | ⬜ | |
| Domain creation works | ⬜ | |
| Mailbox creation works | ⬜ | |
| Service restart survives | ⬜ | |
| No errors in logs | ⬜ | |

---

## Troubleshooting

```bash
# Full logs
journalctl -u orvix.service -f

# Check for CGO errors
journalctl -u orvix.service | grep -i "cgo"

# Check for 404 errors
journalctl -u orvix.service | grep -i "404"

# Check for systemd warnings
journalctl -u orvix.service | grep -i "unknown"

# Check database
ls -la /var/lib/orvix/
file /var/lib/orvix/orvix.db
```

---

## Success Criteria

**RC2 is ready for production if:**

1. ✅ Installer runs without CGO errors
2. ✅ Stalwart downloads without 404 errors
3. ✅ systemd service installs without warnings
4. ✅ All prompts have validation
5. ✅ Health endpoint responds correctly
6. ✅ Basic operations (login, domain, user) work
7. ✅ No errors in journal logs

---

## RC1 → RC2 Comparison

| Metric | RC1 | RC2 Target |
|--------|-----|------------|
| CGO Error | ❌ Yes | ✅ No |
| 404 Error | ❌ Yes | ✅ No |
| systemd Warning | ❌ Yes | ✅ No |
| Validation | ❌ None | ✅ Yes |
| Install Success | ❌ No | ✅ Yes |

---

*Orvix RC2 - Ready for VPS validation*