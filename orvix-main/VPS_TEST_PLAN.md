# Orvix VPS Test Plan

## Prerequisites
- Clean Ubuntu 22.04 or 24.04 VPS (minimal install)
- Root SSH access
- Domain name with DNS access (optional but recommended)

## RC5 Release Information
- **Version:** v1.0.4-rc5
- **Git Commit:** (pending push)
- **GitHub Release:** https://github.com/reachfm/orvix/releases/tag/v1.0.4
- **Archive SHA256:** `48be25d12c7d9eb257680088f2d74bb6aa24250b7ac6aee5b9b305f11bd3f955`
- **Binary SHA256:** `e7ad824523dea77858b11dfcc06793bb868a1141bf1f95dd9f511b4317b1138b`

## Download Release
```bash
# Download from GitHub
curl -fsSL https://github.com/reachfm/orvix/releases/download/v1.0.4/orvix-v1.0.4-linux-amd64.tar.gz -o orvix.tar.gz

# Verify checksum
sha256sum orvix.tar.gz
# Expected: 48be25d12c7d9eb257680088f2d74bb6aa24250b7ac6aee5b9b305f11bd3f955

# Extract
tar -xzf orvix.tar.gz
```

## Test Steps

### 1. Fresh VPS Setup
```bash
# Connect to VPS
ssh root@YOUR_VPS_IP

# Verify clean state
uname -a
cat /etc/os-release
df -h
free -m
```

### 2. Install Orvix
```bash
# Run installer from extracted directory
sudo ./install.sh
```

Expected:
- Script prompts for domain, admin email, admin password
- **NO default credentials** - must provide admin email and password
- Redis server installed and enabled
- Directories created: /etc/orvix, /etc/orvix/stalwart, /var/lib/orvix, /var/log/orvix
- User 'orvix' created
- Binary installed at /usr/local/bin/orvix
- systemd service installed with ReadWritePaths
- Service starts successfully

### 3. Verify Installation
```bash
# Check Orvix service
systemctl status orvix --no-pager -l

# Check Redis service
systemctl status redis-server --no-pager -l

# Check health endpoint
curl -fsS http://127.0.0.1:8080/api/v1/health

# Check logs
journalctl -u orvix --no-pager -n 100

# Check Stalwart process
pgrep -a stalwart || true
```

Expected:
- Health endpoint returns `{"status":"ok"}`
- Redis server is running
- No hardcoded credentials in logs

### 4. Verify Systemd Hardening
```bash
# Check ReadWritePaths are set
grep -i ReadWritePath /etc/systemd/system/orvix.service

# Verify environment variables are set
cat /etc/systemd/system/orvix.service.d/override.conf
```

Expected:
- ReadWritePaths include: /etc/orvix, /var/lib/orvix, /var/log/orvix
- ORVIX_ADMIN_EMAIL and ORVIX_ADMIN_PASSWORD in override

### 5. Login Test
```bash
# Login with provided credentials
curl -s -X POST http://127.0.0.1:8080/api/v1/auth/login \
  -H "Content-Type: application/json" \
  -d '{"email":"YOUR_ADMIN_EMAIL","password":"YOUR_ADMIN_PASSWORD"}'
```

Expected:
- Returns access_token and refresh_token

### 6. Service Restart Test
```bash
systemctl restart orvix.service
sleep 5
curl -s http://127.0.0.1:8080/api/v1/health
```

Expected:
- Service restarts cleanly
- Data persists
- Login still works

## Validation Checklist
- [ ] 1. Fresh VPS baseline verified
- [ ] 2. Checksum verified (48be25d1...)
- [ ] 3. Installer runs without errors
- [ ] 4. Redis server installed and running
- [ ] 5. Stalwart download succeeds
- [ ] 6. No default credentials prompted
- [ ] 7. Health endpoint responds
- [ ] 8. Login works with provided credentials
- [ ] 9. Service restart preserves data
- [ ] 10. ReadWritePaths configured in systemd
- [ ] 11. Environment variables in systemd override
- [ ] 12. Password confirmation works

## Troubleshooting
```bash
# Check service logs
journalctl -u orvix.service -f

# Check Redis status
systemctl status redis-server

# Check environment variables
systemctl show orvix.service | grep ORVIX

# Verify binary checksum
sha256sum /usr/local/bin/orvix
# Expected: e7ad824523dea77858b11dfcc06793bb868a1141bf1f95dd9f511b4317b1138b
```
