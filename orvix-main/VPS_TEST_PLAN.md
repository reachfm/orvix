# Orvix VPS Test Plan

## Prerequisites
- Clean Ubuntu 22.04 or 24.04 VPS (minimal install)
- Root SSH access
- Domain name with DNS access (optional but recommended)

## RC6 Release Information
- **Version:** v1.0.5-rc6
- **Git Commit:** (pending push)
- **GitHub Release:** https://github.com/reachfm/orvix/releases/tag/v1.0.5
- **Archive SHA256:** `f93d159fb27631220ef03814da910d649e72edb6b60623d344fbf192d0d80d48`
- **Binary SHA256:** `8d5d728188ab724f5e878b43f812071b15fa969e0f0faa8b83ab12effe447556`

## Download Release
```bash
# Download from GitHub
curl -fsSL https://github.com/reachfm/orvix/releases/download/v1.0.5/orvix-v1.0.5-linux-amd64.tar.gz -o orvix.tar.gz

# Verify checksum
sha256sum orvix.tar.gz
# Expected: f93d159fb27631220ef03814da910d649e72edb6b60623d344fbf192d0d80d48

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

### 5. RC6: Verify Systemd Override Syntax
```bash
# Check override.conf content
cat /etc/systemd/system/orvix.service.d/override.conf

# Verify no warnings
systemd-analyze verify /etc/systemd/system/orvix.service
```

Expected:
- [Service] section present
- Environment="ORVIX_ADMIN_EMAIL=..." line
- Environment="ORVIX_ADMIN_PASSWORD=..." line
- No "Missing '='" warnings

### 6. RC6: Verify Stalwart Health
```bash
# Check Stalwart process
ps -fp $(pgrep stalwart)

# Verify config file exists
ls -la /etc/orvix/stalwart/config.json
```

Expected:
- Process running with: stalwart --config /etc/orvix/stalwart/config.json
- config.json exists at correct path
- No crash loop

### 7. Login Test
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
- [ ] 2. Checksum verified (8d5d7281...)
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
- [ ] 13. RC6: Systemd override has no "Missing '='" warnings
- [ ] 14. RC6: Stalwart process verified with correct command
- [ ] 15. RC6: Config.json exists at /etc/orvix/stalwart/

## Troubleshooting
```bash
# Check service logs
journalctl -u orvix.service -f

# Check Redis status
systemctl status redis-server

# Check environment variables
systemctl show orvix.service | grep ORVIX

# Verify systemd override syntax
systemd-analyze verify /etc/systemd/system/orvix.service

# Verify Stalwart process
ps -fp $(pgrep stalwart)

# Verify binary checksum
sha256sum /usr/local/bin/orvix
# Expected: 8d5d728188ab724f5e878b43f812071b15fa969e0f0faa8b83ab12effe447556
```
