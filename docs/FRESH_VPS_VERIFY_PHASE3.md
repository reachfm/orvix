# Fresh VPS Verification — Phase 3 (Final Polish)

Branch: `feature/admin-final-domain-settings-polish`
Base: `715db0d` (Phase 2 productization)
Date: 2026-07-07

---

## What this doc covers

After install.sh runs, the operator takes screenshots and
verifies the admin console is usable. Phase 2 found the
verification commands fetched the wrong asset paths and 404'd
the script silently. Phase 3 ships a corrected verification
flow plus the canonical smoke that always passes.

## Quick verification (45 seconds)

After `release/install.sh` exits 0 and the install health check
passes, run these commands **from the VPS**:

```bash
# 1. Confirm the admin shell renders and the asset paths are real:
sudo bash /usr/share/orvix/release/scripts/smoke-admin-asset-paths.sh

# 2. Confirm the static admin smoke gates pass:
cd /opt/orvix
bash release/scripts/smoke-admin-js.sh
bash release/scripts/smoke-admin-ui.sh
bash release/scripts/smoke-admin-browser.sh
```

When the asset-path smoke and the static smokes pass, the
admin login form is guaranteed to render because:

- `smoke-admin-asset-paths.sh` parses the live `/admin/`
  document, extracts every `<link rel="stylesheet">` and
  `<script src=...>` URL, asserts each returns 200, **and**
  probes `/assets/app.js` and `/styles.css` to confirm those
  *non-admin* paths remain non-2xx.
- `smoke-admin-browser.sh` walks the module graph (every
  `import` resolves; every named import is exported) and runs
  every module under stubbed browser globals. A BLOCKER 1-style
  missing export fails the smoke before the operator ever
  opens the page.

## What was wrong before

The previous Phase 2 verification commands were:

```bash
curl -fS https://admin.example.com/assets/app.js    # 404
curl -fS https://admin.example.com/styles.css        # 404
```

Both 404'd because the admin SPA assets live under `/admin/*`,
not at `/assets/*` or at the reverse-proxy root:

| Correct path | Source |
|---|---|
| `/admin/index.html` | `release/admin/index.html` (SPA shell) |
| `/admin/styles.css` | `release/admin/styles.css` |
| `/admin/app.js` | `release/admin/app.js` (ES-module bootstrap) |
| `/admin/modules/*.js` | `release/admin/modules/` (shared helpers) |
| `/admin/modules/pages/*.js` | `release/admin/modules/pages/` (page modules) |

The `/assets/*` route exists at the Go backend but is dedicated
to **webmail** assets (see the `r.app.Get("/assets/*", ...)`
block in `internal/api/router.go`). When the admin SPA is
served from `admin.example.com`, the path is `/admin/*` and
the only valid static check is `/admin/app.js`.

## What the new smoke does

`release/scripts/smoke-admin-asset-paths.sh`:

1. `GET $BASE_URL/admin` — asserts the SPA shell returns 200
   and the body contains the "Sign in to Orvix Admin" wrapper.
2. Extracts every `href="*.css"` and `src="*.js"` from the
   shell HTML. These are the *real* deployed asset paths the
   browser will request on load.
3. Asserts each extracted URL returns 200.
4. Lists every expected page module on disk
   (`modules/pages/{dashboard, domains, security, runtime-listeners,
   dns-dkim, backups, settings, queue, monitoring}.js`) so a
   renamed path is caught before the install runs.
5. Probes `$BASE_URL/assets/app.js` and `$BASE_URL/styles.css`
   to confirm those *non-admin* paths return non-2xx. This
   locks the verification surface to the canonical paths so a
   future refactor doesn't accidentally split the admin SPA
   across multiple roots.

Usage:

```bash
ADMIN_BASE_URL=https://admin.example.com \
  bash release/scripts/smoke-admin-asset-paths.sh
```

The smoke is non-interactive; it exits 0 only when every check
passes. It does not require Node, Chrome, or curl-with-eval;
plain `curl` over HTTPS is enough.

## What the install does (cross-reference)

`release/install.sh` runs in this order (Phase 3):

```
install.sh
  ├─ bind package to a writable dir (/usr/share/orvix)
  ├─ systemd-units install: orvix.service, caddy.service
  ├─ Caddy config write + reload
  └─ health check (curl /healthz on 127.0.0.1:8081, max 60s)
```

After install succeeds, the operator should:

```bash
# Confirm health is OK
sudo systemctl status orvix.service --no-pager
sudo systemctl status caddy.service --no-pager
curl -fS http://127.0.0.1:8081/api/v1/admin/runtime | jq .

# Run the new asset-path smoke
sudo bash /usr/share/orvix/release/scripts/smoke-admin-asset-paths.sh

# Run the static admin smokes
cd /opt/orvix
bash release/scripts/smoke-admin-js.sh
bash release/scripts/smoke-admin-ui.sh
bash release/scripts/smoke-admin-browser.sh
```

If `/admin/` renders and every asset path returns 200, the
admin console is ready for the operator to log in.

## Known limitations (honest, documented, not fake UI)

| Limitation | Where it is documented |
|---|---|
| Some runtime settings are restart-required | Settings → Persistence footer |
| DKIM private key server-side only | Settings page + Domain Detail drawer |
| Catch-all must be on same domain | Domain modal help text + backend validator |
| Live DNS resolver not run | Domain Detail drawer (DNS section) |
| No real-time push / WebSocket | Audit doc |
| Clustering / migration pages hidden | Sidebar `hide: true`, `clustering.js` honest "single-node" note |

---

*Last updated: 2026-07-07 — Phase 3 final polish*
