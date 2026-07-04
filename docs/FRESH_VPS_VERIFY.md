# Fresh VPS Verification — One-Command Acceptance

This document is the BLOCKER 9 acceptance gate. It defines the
single test an operator (or a CI runner) executes on a clean
Ubuntu 22.04 VPS to prove the one-command install actually
delivers a working Orvix Enterprise Mail instance.

## The command

```bash
sudo bash release/scripts/verify-fresh-vps-one-command.sh
```

(Or, on the operator's own machine, after SSHing into the VPS.)

## What the script proves

The script runs the BLOCKER 1 / 6 / 7 / 8 / 9 acceptance list
end-to-end. It exits non-zero on the first failure, with a
precise diagnostic; full output is also written to
`/var/log/orvix/fresh-vps-verify.log`.

### 1. Service status

- `systemctl is-active --quiet orvix`     — runtime is up
- `systemctl is-active --quiet caddy`     — HTTPS reverse proxy is up
  (only after `setup-https.sh` has been run; the script warns
  rather than fails if Caddy is not yet active)

### 2. Port binding posture

The BLOCKER 1 + 2 contract (port table is fixed in
`docs/PRODUCTION_READINESS_GATE.md` §2):

| Port | Required bind          | Reason                                      |
|------|------------------------|---------------------------------------------|
| 25   | public (`0.0.0.0`)     | SMTP MX — must be reachable on public IP    |
| 110  | public                 | POP3 (advisory; TLS-wrapped recommended)    |
| 143  | public                 | IMAP                                         |
| 8080 | `127.0.0.1` only       | admin + webmail SPA backend                  |
| 8081 | `127.0.0.1` only       | JMAP backend                                 |
| 80   | public                 | Caddy HTTP (ACME challenge + redirect)       |
| 443  | public                 | Caddy HTTPS                                  |

The script asserts 8080/8081 are bound to loopback only, and
25/110/143 are bound on a public address. Any non-loopback bind
on 8080/8081 fails the script — that is the BLOCKER 1
loopback-only posture check.

### 3. Local endpoints (unauthenticated)

- `http://127.0.0.1:8080/api/v1/health`  → 200 + `{"status":"ok"}`
- `http://127.0.0.1:8080/admin`         → 200 (admin SPA HTML)
- `http://127.0.0.1:8080/webmail`       → 200 (webmail SPA HTML)
- `http://127.0.0.1:8081/.well-known/jmap` → 200 + a valid JMAP session document

### 4. Public HTTPS endpoints (after `setup-https.sh`)

- `https://admin.<domain>/admin`         → 200
- `https://webmail.<domain>/`            → 200
- `https://mail.<domain>/.well-known/jmap` → 200

### 5. Security — BLOCKER 6 / 7 contract

- `/etc/orvix/bootstrap.env`              → does NOT exist (deleted by `verify_install`)
- `/var/lib/orvix/admin-login.txt`        → root:root, mode 0600, no password/hash/JWT
- `/proc/$ORVIX_PID/environ`              → no `ORVIX_ADMIN_PASSWORD(B64)` after the post-install restart
- `journalctl -u orvix`                   → no plain-text admin password material

### 6. Admin login form hydration — BLOCKER 1 contract

- `/usr/share/orvix/admin/index.html`     → loads `app.js` as `<script type="module">`
- `/usr/share/orvix/admin/modules/auth.js` → exports `renderLogin` + `hasValidSession`

A failed assertion here is the regression guard for the static
HTML / no-form symptom the previous CTO review caught. The
shipped `smoke-admin-browser.sh` runs the same checks at the
release tree level; this final assertion proves the live,
installed bundle still hydrates.

## What it does NOT prove (documented constraints)

The script does not:

- Try to actually log in as admin (that would require a
  browser-real test). The smoke proves the form is hydrated;
  a successful sign-in is left to the operator's first
  browser visit.
- Run on Windows. The script is bash + `systemctl` + `ss` +
  `curl` + `jq` + `sqlite3` + `openssl`; it is targeted at
  the same Ubuntu 22.04 LTS host the install supports.
- Override existing state. The script is read-only — it
  inspects the live system, never writes.

## Running the gate

A typical fresh-VPS run after install + setup-https:

```bash
# 1. The one-command install (BLOCKER 3/4/5 contract).
ORVIX_PRIMARY_DOMAIN=orvix.email \
ORVIX_ADMIN_EMAIL=admin@orvix.email \
ORVIX_ADMIN_PASSWORD='STRONG_PASSWORD' \
curl -fsSL https://raw.githubusercontent.com/reachfm/orvix/main/release/install-public.sh | sudo -E bash

# 2. The HTTPS setup (BLOCKER 6 path).
sudo /usr/share/orvix/scripts/setup-https.sh orvix.email 65.75.203.74

# 3. The fresh-VPS acceptance gate.
sudo ORVIX_PRIMARY_DOMAIN=orvix.email ORVIX_PUBLIC_IPV4=65.75.203.74 \
    bash release/scripts/verify-fresh-vps-one-command.sh
```

If step 3 prints `PASS — fresh VPS one-command GitHub install + HTTPS + admin login form verified`,
the install is ready for production. If it prints `NEEDS FIX`,
the report at `/var/log/orvix/fresh-vps-verify.log` lists every
failing check.
