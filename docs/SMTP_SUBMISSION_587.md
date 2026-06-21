# Orvix SMTP Submission (Port 587) — Operator Guide

SUBMISSION-3D operational guide for binding Orvix port 587 to a real
TLS certificate, verifying the live STARTTLS / AUTH flow, and rolling
back to safe-default if anything goes wrong.

## What this feature does

By default, Orvix does **not** listen on port 587. The runtime reads
`coremail.submission_enabled`, `coremail.tls_cert_file`, and
`coremail.tls_key_file` from `/etc/orvix/orvix.yaml` (or the env
overrides described below) and only starts the submission listener
when **all three** are configured and the cert/key pair validates.

This feature ships two operator scripts:

| Script | Purpose |
|---|---|
| `release/scripts/setup-smtp-tls.sh` | Bind a source cert/key pair to `/etc/orvix/tls/smtp/`, validate it, edit `/etc/orvix/orvix.yaml`, restart Orvix, and probe port 587. |
| `release/scripts/check-smtp-tls.sh` | Doctor / readiness check. Reports PASS / WARN / FAIL on the runtime state without leaking cert paths or raw TLS errors. |

## Layout the runtime cares about

```
/etc/orvix/tls/smtp/
├── fullchain.pem    (0644, root:root)   — leaf + chain
└── privkey.pem      (0640, root:orvix)  — private key (NEVER world-readable)
```

The Go runtime does **not** know about Caddy, certbot, or any other
upstream certificate source. The setup script is the only place that
translates "wherever the operator got the cert" into
`/etc/orvix/tls/smtp/`. If you change the cert location later, edit
`/etc/orvix/orvix.yaml` (or restart with the env override below).

## Enable 587 with a real cert (live)

### 1. Pick the source cert/key paths

These typically come from your ACME client (Let's Encrypt via Caddy or
certbot). Examples only — the setup script never assumes these paths:

```
# If you use Caddy
/var/lib/caddy/.local/share/caddy/certificates/acme-v02.api.letsencrypt.org-directory/<your-domain>/fullchain.pem
/var/lib/caddy/.local/share/caddy/certificates/acme-v02.api.letsencrypt.org-directory/<your-domain>/privkey.pem

# If you use certbot
/etc/letsencrypt/live/<your-domain>/fullchain.pem
/etc/letsencrypt/live/<your-domain>/privkey.pem
```

The script does not look at these paths — you pass them in. The
script copies (or symlinks, with `ORVIX_SMTP_TLS_MODE=symlink`) them
into `/etc/orvix/tls/smtp/`.

### 2. Run the setup script

```bash
sudo bash release/scripts/setup-smtp-tls.sh \
    /var/lib/caddy/.local/share/caddy/certificates/acme-v02.api.letsencrypt.org-directory/your-domain/fullchain.pem \
    /var/lib/caddy/.local/share/caddy/certificates/acme-v02.api.letsencrypt.org-directory/your-domain/privkey.pem
```

The script will, in order:

1. Refuse to run if not root.
2. Refuse if the source key permissions are not 0600 or 0640 (rejects 0644, 0666, 0777, world-readable keys).
3. Validate the cert/key pair (`openssl x509 -noout -modulus` matches
   `openssl pkey -noout -modulus`). OpenSSL stderr is captured privately;
   only safe reason codes (cert_read_failed, key_read_failed, etc.)
   appear in the install log.
4. Create `/etc/orvix/tls/smtp/` with mode `0750`.
5. Copy the cert as `fullchain.pem` (0644) and the key as `privkey.pem`
   (0640 root:orvix, or 0600 root:root if the `orvix` group does not
   exist).
6. Re-validate the **copied** files (defense in depth).
7. Back up `/etc/orvix/orvix.yaml` to `orvix.yaml.bak-<timestamp>`
   with mode 0600.
8. Upsert three keys under `coremail:`
9. `systemctl reload-or-restart orvix.service`.
10. Probe `ss -lntp` for `:587` for up to 30 seconds.
11. If reload fails OR port 587 does not bind: **rolls back** to the
    previous config backup, reloads the service, exits non-zero, and
    does NOT print PASS. Port 25 inbound is never affected.
12. On success: prints PASS with safe labels (no cert/key paths in
    output).

If any step fails, the script aborts **before** editing the YAML or
reloading the service. Port 25 inbound keeps running throughout.

### 3. Verify with the doctor script

```bash
sudo bash release/scripts/check-smtp-tls.sh
# or with verbose
sudo bash release/scripts/check-smtp-tls.sh --verbose
```

Expected output:

```
Orvix SMTP TLS readiness check (/etc/orvix/orvix.yaml)
  PASS  service orvix.service is active
  PASS  port 25 is listening (inbound MX)
  PASS  cert file exists
  PASS  key file exists
  PASS  key file mode 0640 (not world-readable)
  PASS  cert/key pair validates (modulus match)
  PASS  cert is valid for at least 30 more days
  PASS  port 587 is listening (TLS active)
  PASS  port 465 is not listening (SMTPS honest-disabled)

587 status: READY
```

## Live swaks playbook (VPS smoke tests)

These are the exact commands used to verify port 587 against a live
host. Do **not** run them locally — they exercise the public path.

### 1. Deploy and verify default state

```bash
cd /opt/orvix
git fetch origin main
git pull --ff-only origin main
COMMIT="$(git rev-parse HEAD)"
systemctl stop orvix
/usr/local/go/bin/go build -ldflags "-X github.com/orvix/orvix/internal/config.buildCommit=$COMMIT" -o /tmp/orvix-new ./cmd/orvix
install -o root -g root -m 0755 /tmp/orvix-new /usr/local/bin/orvix
setcap cap_net_bind_service=+ep /usr/local/bin/orvix
systemctl start orvix

ss -lntp | egrep ':25|:465|:587|:8080|:8081' || true
curl -s http://127.0.0.1:8080/api/v1/health; echo
```

Expected default:

```
LISTEN ... 0.0.0.0:25    users:(("orvix",...))     # port 25 only
LISTEN ... 0.0.0.0:8080  ...                        # admin
LISTEN ... 0.0.0.0:8081  ...                        # jmap
# no :587 and no :465
```

### 2. Enable SMTP TLS via the setup script

```bash
sudo bash /opt/orvix/release/scripts/setup-smtp-tls.sh \
    /var/lib/caddy/.local/share/caddy/certificates/acme-v02.api.letsencrypt.org-directory/<your-domain>/fullchain.pem \
    /var/lib/caddy/.local/share/caddy/certificates/acme-v02.api.letsencrypt.org-directory/<your-domain>/privkey.pem
```

### 3. Restart and verify port 587

```bash
systemctl restart orvix
ss -lntp | egrep ':25|:465|:587|:8080|:8081' || true
```

Expected after enable:

```
LISTEN ... 0.0.0.0:25    ...                       # still up
LISTEN ... 0.0.0.0:587   ...                       # NEW: STARTTLS submission
LISTEN ... 0.0.0.0:8080  ...
LISTEN ... 0.0.0.1:8081  ...
# no :465
```

### 4. EHLO inspection

```bash
printf 'EHLO test.local\r\nQUIT\r\n' | nc -w 10 127.0.0.1 587
```

Expected: response includes `STARTTLS`. `AUTH PLAIN LOGIN` may be
advertised. AUTH before STARTTLS must return `454 Must issue STARTTLS
first`.

### 5. AUTH before STARTTLS must fail

```bash
swaks --server 127.0.0.1 --port 587 \
    --auth LOGIN --auth-user admin@your-domain --auth-password '<PASSWORD>' \
    --from admin@your-domain --to admin@your-domain \
    --data "Subject: auth before tls should fail

fail"
```

Expected: `454 Must issue STARTTLS first` (or equivalent 5xx response).

### 6. STARTTLS + AUTH local test

```bash
swaks --server 127.0.0.1 --port 587 --tls \
    --auth LOGIN --auth-user admin@your-domain --auth-password '<PASSWORD>' \
    --from admin@your-domain --to admin@your-domain \
    --data "Subject: 587 live local test

hello"
```

Expected: `AUTH` succeeds (235), `MAIL FROM`/`RCPT TO`/`DATA` all
return 250. The message lands in the recipient's `INBOX`.

### 7. STARTTLS + AUTH external queue test

```bash
swaks --server 127.0.0.1 --port 587 --tls \
    --auth LOGIN --auth-user admin@your-domain --auth-password '<PASSWORD>' \
    --from admin@your-domain --to someone@gmail.com \
    --data "Subject: 587 live external queue test

hello"
```

Expected: AUTH 235, DATA 250. The queue shows an outbound
`remote_smtp` pending entry:

```sql
sqlite3 /var/lib/orvix/orvix.db \
  "select to_address, recipient_domain, from_address, direction, delivery_mode, status, message_id
   from queue_entries
   where direction='outbound' and status='pending'
   order by id desc limit 5;"
# → to_address=someone@gmail.com  recipient_domain=gmail.com
#   from_address=admin@your-domain  direction=outbound
#   delivery_mode=remote_smtp  status=pending  message_id=<non-empty>
```

### 8. Spoofing must be rejected

```bash
swaks --server 127.0.0.1 --port 587 --tls \
    --auth LOGIN --auth-user admin@your-domain --auth-password '<PASSWORD>' \
    --from other@your-domain --to someone@gmail.com \
    --data "Subject: spoof should fail

fail"
```

Expected: `550 5.7.1 Sender not authorized for other@your-domain`.

### 9. Rollback to safe-disabled

```bash
# Restore YAML from backup.
LATEST_BAK="$(ls -1t /etc/orvix/orvix.yaml.bak-* | head -n1)"
sudo cp -p "$LATEST_BAK" /etc/orvix/orvix.yaml
# Belt and suspenders: force submission_enabled false.
sudo sed -i 's/^  submission_enabled: true/  submission_enabled: false/' /etc/orvix/orvix.yaml
sudo systemctl reload-or-restart orvix.service
ss -lntp | grep -E ':25|:587' || true
```

Expected after rollback:

```
LISTEN ... 0.0.0.0:25    ...   # port 25 unaffected, still listening
# no :587 and no :465
```

The doctor script will then report `587 status: DISABLED (safe default)`.

## Config and env reference

### YAML fields (`/etc/orvix/orvix.yaml`)

```yaml
coremail:
  submission_enabled: false   # flip to true to bind 587
  submission_host: 0.0.0.0
  submission_port: 587
  tls_cert_file: /etc/orvix/tls/smtp/fullchain.pem
  tls_key_file:  /etc/orvix/tls/smtp/privkey.pem
  smtps_enabled: false         # keep false — SMTPS not implemented yet
```

### Environment overrides (Operator convenience)

These take precedence over the YAML when set, but empty values leave
the YAML untouched. They are intended for containerized installs and
one-shot debugging.

| Env var | Effect |
|---|---|
| `ORVIX_COREMAIL_SUBMISSION_ENABLED` | true / false — overrides `coremail.submission_enabled` |
| `ORVIX_COREMAIL_TLS_CERT_FILE` | path — overrides `coremail.tls_cert_file` |
| `ORVIX_COREMAIL_TLS_KEY_FILE`  | path — overrides `coremail.tls_key_file` |
| `ORVIX_COREMAIL_SMTPS_ENABLED` | true / false — overrides `coremail.smtps_enabled` |
| `ORVIX_COREMAIL_ENABLED` | (pre-existing) overrides `coremail.enabled` |

Defaults are safe: `submission_enabled=false`, `tls_cert_file=""`,
`tls_key_file=""`, `smtps_enabled=false`.

## Safety guarantees

This feature inherits and preserves every guarantee from
SUBMISSION-3B/3C:

- **No plaintext AUTH on 587.** The runtime refuses to start the
  submission listener when `tls_cert_file` / `tls_key_file` are
  missing or fail to parse. The setup script refuses to write
  `submission_enabled: true` if the cert/key pair does not validate.
- **Cert/key paths are not leaked.** The setup script's install log
  records only byte sizes, never paths or PEM contents. The doctor
  script reports PASS/FAIL with reason codes only.
- **Private key is never world-readable.** Setup script enforces 0600/0640
  source mode before copying (rejects 0644, 0666, 0777). The doctor
  script fails loudly if the installed key has more permissive bits.
- **Port 25 unaffected.** Any failure in the setup script triggers an
  automatic rollback (restores previous config, reloads service) before
  exit. Port 25 inbound keeps running throughout.
- **587 bind failure is FAIL, not a warning.** If port 587 does not listen
  after reload, the script rolls back and exits non-zero.
- **Output avoids printing cert/key paths.** Success output uses safe
  labels ("key: installed") instead of full filesystem paths.
- **OpenSSL stderr is never logged raw.** Only safe reason codes like
  cert_read_failed appear in logs.
- **Check script uses mktemp** for runtime telemetry temp files and
  cleans them up on exit.
- **Port 465 stays honest-disabled.** No listener is started on 465
  regardless of cert state.
- **No hardcoded Caddy paths in the Go runtime.** Only the operator's
  setup script ever mentions upstream cert locations.

## Quick troubleshooting

| Symptom | Likely cause | Fix |
|---|---|---|
| `port 587 is NOT listening` after restart | `coremail.tls_cert_file` empty / wrong path | Run `bash release/scripts/check-smtp-tls.sh --verbose` and inspect `/var/log/orvix/orvix.log` (path sanitization in logs — look for `submission disabled:` markers) |
| `key file mode 0644` (FAIL) | Symlink inherited source mode | Re-run `setup-smtp-tls.sh` in copy mode (`ORVIX_SMTP_TLS_MODE=copy`, default) |
| `cert/key pair modulus does not match` | Cert and key from different domains/renewals | Re-fetch both files from the same ACME source |
| `cert expires within 30 days` (WARN) | Renewal due | Trigger ACME renewal upstream, then re-run `setup-smtp-tls.sh` |
| `550 Sender not authorized` on local test | Authenticated mailbox different from `MAIL FROM` | Use the same address for `--auth-user` and `--from` |