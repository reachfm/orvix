# Orvix RC2 VPS Retest Commands

**Version:** 1.0.1 (RC2)
**Date:** 2026-06-06
**Purpose:** Exact commands to verify RC2 fixes RC1 failures

---

## Prerequisites

- Clean Ubuntu 22.04 or 24.04 VPS (minimal install)
- Root SSH access
- GitHub release access OR local RC2 files

---

## Option A: Download from GitHub (After RC2 Release)

```bash
# 1. Connect to VPS
ssh root@YOUR_VPS_IP

# 2. Download RC2 release
wget https://github.com/reachfm/orvix/releases/v1.0.1/orvix-v1.0.1-linux-amd64.tar.gz

# 3. Extract
tar -xzf orvix-v1.0.1-linux-amd64.tar.gz
cd release/
```

---

## Option B: Use Local RC2 Files (Before GitHub Release)

```bash
# On your local machine, download RC2 files from /workspace/RC2/
# Upload to VPS using scp:
scp -r /workspace/RC2/* root@YOUR_VPS_IP:/root/orvix-rc2/

# On VPS:
ssh root@YOUR_VPS_IP
cd /root/orvix-rc2/release/
```

---

## Step 1: Fresh VPS Baseline

```bash
# Verify clean state
uname -a
# Expected: Linux version 5.15.x or 6.x (Ubuntu 22.04 or 24.04)

cat /etc/os-release
# Expected: NAME="Ubuntu", VERSION="22.04" or "24.04"

df -h
free -m

# Check for existing orvix
which orvix
systemctl status orvix.service 2>/dev/null || echo "No existing orvix - clean VPS confirmed"
```

**Expected Output:**
```
No existing orvix - clean VPS confirmed
```

---

## Step 2: Run Installer

```bash
# Run as root
bash install.sh
```

**Expected Prompts:**
```
Orvix v1.0.1 RC2 Installer
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
Primary email domain (e.g., example.com): [USER ENTERS VALID DOMAIN]
Admin email address: [USER ENTERS VALID EMAIL]
Admin password (min 12 chars): [USER ENTERS PASSWORD]
License key (optional, press Enter to skip): [USER PRESSES ENTER]
```

**Validation Test (try invalid input):**
- Try entering invalid domain → should be rejected with error
- Try entering invalid email → should be rejected with error
- Try entering short password → should be rejected with error

---

## Step 3: Verify No CGO Error

```bash
# Check service status
systemctl status orvix.service
```

**Expected Output (no CGO error):**
```
● orvix.service - Orvix Email Server Platform
     Loaded: loaded (/etc/systemd/system/orvix.service; enabled; vendor preset: enabled)
     Active: active (running) since ...
```

**Verify No Error:**
```bash
journalctl -u orvix.service -n 100 | grep -i "cgo"
# Expected: NO OUTPUT (no CGO error)

journalctl -u orvix.service -n 100 | grep -i "requires cgo"
# Expected: NO OUTPUT (no CGO error)
```

---

## Step 4: Verify No systemd Warning

```bash
# Check for systemd warnings
systemctl status orvix.service 2>&1 | grep -i "unknown"
# Expected: NO OUTPUT (no warnings)

# Check service file directives
grep -E "StartLimit(Interval|Burst)" /etc/systemd/system/orvix.service
# Expected: NO OUTPUT (directives removed)
```

---

## Step 5: Verify Health Endpoint

```bash
# Test health endpoint
curl -s http://localhost:8080/api/v1/health
```

**Expected Output:**
```json
{"status":"ok"}
```

---

## Step 6: Verify No 404 Errors

```bash
# Check for 404 errors in logs
journalctl -u orvix.service -n 500 | grep -i "404"
# Expected: NO OUTPUT (no 404 errors)

# Check for Stalwart download
journalctl -u orvix.service -n 500 | grep -i "downloading stalwart"
# Expected: Stalwart download message (or already installed)
```

---

## Step 7: Login Test

```bash
# Get CSRF token
curl -s -c cookies.txt http://localhost:8080/api/v1/csrf-token

# Login (use credentials from installation)
curl -s -X POST http://localhost:8080/api/v1/auth/login \
  -H "Content-Type: application/json" \
  -d '{"email":"admin@YOUR_DOMAIN","password":"YOUR_PASSWORD"}' \
  -c cookies.txt
```

**Expected Output:**
```json
{
  "access_token": "eyJ...",
  "refresh_token": "eyJ...",
  "access_expires_in": 900,
  "refresh_expires_in": 2592000
}
```

---

## Step 8: Domain Creation

```bash
# Get CSRF token
CSRF=$(curl -s -b cookies.txt http://localhost:8080/api/v1/csrf-token | grep -o '"csrf_token":"[^"]*"' | cut -d'"' -f4)

# Create domain
curl -s -X POST http://localhost:8080/api/v1/domains \
  -H "Content-Type: application/json" \
  -H "X-CSRF-Token: $CSRF" \
  -b cookies.txt \
  -d '{"name":"YOUR_DOMAIN"}'
```

**Expected Output:**
```json
{"id":1,"name":"YOUR_DOMAIN","...}
```

---

## Step 9: User Creation

```bash
# Create user
curl -s -X POST http://localhost:8080/api/v1/users \
  -H "Content-Type: application/json" \
  -H "X-CSRF-Token: $CSRF" \
  -b cookies.txt \
  -d '{"name":"Test User","email":"test@YOUR_DOMAIN","password":"TestPass123!"}'
```

**Expected Output:**
```json
{"id":1,"name":"Test User","email":"test@YOUR_DOMAIN","...}
```

---

## Step 10: Service Restart Test

```bash
# Restart service
systemctl restart orvix.service
sleep 5

# Verify running
systemctl is-active orvix.service
# Expected: active

# Verify health
curl -s http://localhost:8080/api/v1/health
# Expected: {"status":"ok"}
```

---

## Step 11: Collect Failure Evidence

If any test fails, collect evidence:

```bash
# Full logs
journalctl -u orvix.service > /tmp/orvix-logs.txt

# Service status
systemctl status orvix.service > /tmp/orvix-status.txt

# Binary info
file /usr/local/bin/orvix > /tmp/orvix-binary.txt

# Database check
ls -la /var/lib/orvix/
file /var/lib/orvix/orvix.db

# Download logs
grep -i "download" /tmp/orvix-logs.txt

# Error logs only
grep -i "error\|fail\|panic" /tmp/orvix-logs.txt
```

---

## Success Criteria

| Test | Pass Criteria | Verified |
|------|--------------|----------|
| No CGO errors | `grep -i "cgo"` returns empty | ⬜ |
| No 404 errors | `grep -i "404"` returns empty | ⬜ |
| No systemd warnings | `grep -i "unknown"` returns empty | ⬜ |
| Health endpoint | Returns `{"status":"ok"}` | ⬜ |
| Login works | Returns valid tokens | ⬜ |
| Domain creation | Returns domain object | ⬜ |
| User creation | Returns user object | ⬜ |
| Service restart | Service stays active | ⬜ |

---

## Expected Summary (After All Tests)

```
╔══════════════════════════════════════════════════════════════╗
║                    ORVIX RC2 VERIFICATION                    ║
╠══════════════════════════════════════════════════════════════╣
║  SQLite CGO Error      │  ✅ FIXED (no error in logs)        ║
║  Stalwart 404          │  ✅ FIXED (download successful)     ║
║  systemd Warning       │  ✅ FIXED (no unknown keys)         ║
║  Installer Validation  │  ✅ FIXED (prompts work correctly)  ║
║  Health Endpoint       │  ✅ PASS (`{"status":"ok"}`)        ║
║  Login                 │  ✅ PASS (tokens returned)          ║
║  Domain Create         │  ✅ PASS (domain created)           ║
║  User Create           │  ✅ PASS (user created)             ║
║  Service Restart       │  ✅ PASS (stays active)             ║
╠══════════════════════════════════════════════════════════════╣
║  STATUS: ORVIX 100% COMPLETE AND VPS-VERIFIED              ║
╚══════════════════════════════════════════════════════════════╝
```

---

## Failure Collection Script

Run this if ANY test fails:

```bash
cat > /tmp/orvix-collect-evidence.sh << 'EOF'
#!/bin/bash
echo "=== Orvix RC2 Failure Evidence Collection ==="
echo ""

echo "1. Service status:"
systemctl status orvix.service 2>&1 | head -50
echo ""

echo "2. Recent logs (last 100 lines):"
journalctl -u orvix.service -n 100
echo ""

echo "3. Check for CGO errors:"
journalctl -u orvix.service | grep -i "cgo" || echo "No CGO errors found"
echo ""

echo "4. Check for 404 errors:"
journalctl -u orvix.service | grep -i "404" || echo "No 404 errors found"
echo ""

echo "5. Check for systemd warnings:"
journalctl -u orvix.service | grep -i "unknown" || echo "No unknown warnings found"
echo ""

echo "6. Binary info:"
file /usr/local/bin/orvix
ls -la /usr/local/bin/orvix
echo ""

echo "7. Database info:"
ls -la /var/lib/orvix/
file /var/lib/orvix/orvix.db 2>/dev/null || echo "Database not found"
echo ""

echo "8. Health check:"
curl -s http://localhost:8080/api/v1/health
echo ""
echo ""

echo "Evidence collection complete. Check output above."
EOF

chmod +x /tmp/orvix-collect-evidence.sh
bash /tmp/orvix-collect-evidence.sh > /tmp/orvix-evidence.txt
cat /tmp/orvix-evidence.txt
```

---

*Orvix RC2 VPS Retest Commands*
*Run these exact commands to verify all RC1 issues are fixed*