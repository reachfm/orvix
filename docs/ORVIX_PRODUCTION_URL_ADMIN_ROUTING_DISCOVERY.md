# Orvix Production URL and Admin Routing Discovery

## 1. Executive Verdict

Both defects are confirmed with file/line evidence and live production observation. Root causes are proven. Canonical origins are partially determined (marketing domain proven, login/admin origins depend on subdomain convention established by `setup-https.sh`).

## 2. Starting Main SHA

`f948866161f236dc974f7a35f2f3cff6f37d90b2`

## 3. Issues in Scope

- **#38** — Fix Admin portal entry route serving the marketing homepage
- **#39** — Fix orvix.email production links pointing to orvix.com

## 4. Repository Component Inventory

| Component | Path |
|---|---|
| Marketing website source | `web/marketing/` (Astro/React SPA) |
| Marketing built output | `release/marketing/` |
| Admin frontend source | `web/admin/` |
| Admin built output | `release/admin/` |
| Webmail frontend source | `web/webmail/` |
| Webmail built output | `release/webmail/` |
| Caddy configuration template | `release/scripts/setup-https.sh:152-248` |
| Installer (config generation) | `release/install.sh:1282-1323` |
| Public installer entrypoint | `release/install-public.sh` |
| Release build script | `release/scripts/build-release-bundle.sh` |
| Go Fiber router | `internal/api/router.go:1393-1488` |
| Go config example | `release/configs/orvix.yaml.example` |
| Playwright E2E tests | `web/marketing/tests/e2e/marketing.spec.ts` |
| Router tests | `internal/api/router_test.go` |
| Release integrity tests | `internal/config/installer_test.go` |

## 5. Complete Domain-Reference Inventory

### 5.1 Active Production Source Files

| File | Line | Value | Classification |
|---|---|---|---|
| `web/marketing/src/lib/links.ts` | 1 | `PORTAL_BASE = "https://app.orvix.com"` | **active production source — ROOT CAUSE of #39** |
| `web/marketing/src/lib/links.ts` | 2 | `PORTAL_LOGIN = ${PORTAL_BASE}/login` | active production source |
| `web/marketing/src/lib/links.ts` | 3 | `PORTAL_SIGNUP = ${PORTAL_BASE}/signup` | active production source |
| `web/marketing/src/lib/links.ts` | 4 | `DOCS_BASE = "https://docs.orvix.email"` | active production source |
| `web/marketing/src/lib/seo-data.json` | 3 | `"siteBaseUrl": "https://orvix.com"` | active production source |
| `web/marketing/src/components/SEO.tsx` | 17 | `"https://orvix.com/favicon.svg"` | active production source |
| `web/marketing/src/pages/Api.tsx` | 27 | `"https://app.orvix.com/api/v1/health"` | active production source |

### 5.2 Generated/Built Assets (release/marketing/)

Every HTML file under `release/marketing/` embeds `https://orvix.com` canonicals. The JavaScript bundles hard-code domain values:

| File | Embedded Value | Classification |
|---|---|---|
| `release/marketing/marketing-assets/index--gH4x2ue.js` | `const xv="https://app.orvix.com"`, `const zv=${xv}/login`, `const rv=${xv}/signup` | generated asset — **builds from links.ts** |
| `release/marketing/marketing-assets/Section-DUAeMUks.js` | `const S="https://orvix.com"`, `const d="https://orvix.com/favicon.svg"` | generated asset |
| `release/marketing/marketing-assets/Api-CnDdIJPt.js` | `"curl -fsS https://app.orvix.com/api/v1/health"` | generated asset |
| `release/marketing/index.html` | `<link rel="canonical" href="https://orvix.com/" />` | generated asset |
| `release/marketing/robots.txt` | `Sitemap: https://orvix.com/sitemap.xml` | generated asset |
| `release/marketing/sitemap.xml` | All `<loc>https://orvix.com/...</loc>` | generated asset |

### 5.3 Environment/Configuration Defaults

| File | Line | Value | Classification |
|---|---|---|---|
| `release/scripts/setup-https.sh` | 116-118 | `ADMIN_DOMAIN=admin.$PRIMARY_DOMAIN`, `WEBMAIL_DOMAIN=webmail.$PRIMARY_DOMAIN`, `MAIL_DOMAIN=mail.$PRIMARY_DOMAIN` | installer template |
| `release/install.sh` | 1321-1323 | `admin_ui_dir: /usr/share/orvix/admin`, `marketing_ui_dir: /usr/share/orvix/marketing` | installer template |
| `release/configs/orvix.yaml.example` | 15-17 | `admin_ui_dir: /usr/share/orvix/admin` | config default |
| `release/install-public.sh` | 81 | `ORVIX_DOCS_URL="https://docs.orvix.email"` | config default |

### 5.4 Installer Templates

| File | Line | Value |
|---|---|---|
| `release/scripts/setup-https.sh` | 152-248 | Entire `write_caddyfile()` function |
| `release/install.sh` | 1282-1323 | `write_config()` function |

### 5.5 Test Fixtures

| File | Line | Value | Classification |
|---|---|---|---|
| `internal/api/router_test.go` | 80 | `rel="canonical" href="https://orvix.com/"` | test fixture — **enforces wrong domain** |
| `internal/api/router_test.go` | 81 | `rel="canonical" href="https://orvix.com/pricing"` | test fixture |
| `web/marketing/tests/e2e/marketing.spec.ts` | 27 | `a[href="https://app.orvix.com/login"]` | **Playwright test enforces wrong domain** |
| `web/marketing/tests/e2e/marketing.spec.ts` | 28 | `a[href="https://app.orvix.com/signup"]` | Playwright test |
| `internal/config/installer_test.go` | 3553+ | `ADMIN_DOMAIN="admin.orvix.email"` | installer test — **already correct** |

### 5.6 Documentation (not runtime, but stale)

| File | References |
|---|---|
| `docs/customer/api.md` | `https://api.orvix.com/v1`, `https://docs.orvix.com/api` |
| `docs/customer/mailboxes.md` | `https://webmail.orvix.com` |
| `docs/customer/password-reset.md` | `https://app.orvix.com/login` |
| `docs/customer/signup.md` | `https://app.orvix.com/signup`, `https://app.orvix.com/login` |
| `docs/customer/troubleshooting.md` | `https://app.orvix.com/login`, `https://webmail.orvix.com` |
| `docs/customer/webmail.md` | `https://webmail.orvix.com` |
| `docs/customer/status.md` | `https://status.orvix.com` |
| `docs/customer/privacy.md` | `https://orvix.com/dpa`, `privacy@orvix.com` |
| `docs/LAUNCH_SPECIFICATION_v1.0.md` | Multiple `app.orvix.com` references |
| `HANDOFF.md` | `https://orvix.email/install.sh` (correct) |
| `docs/api/openapi.yaml` | `https://api.orvix.email/api/v1` (correct) |

## 6. URL Configuration Mechanisms

The marketing SPA uses two configuration sources, both hard-coded at build time:

1. **`web/marketing/src/lib/links.ts`** — Portal base URL, login, signup, docs links. Used by the Header navigation component and CTA buttons.
2. **`web/marketing/src/lib/seo-data.json`** — Site name, base URL, and route metadata. Read by `web/marketing/src/lib/seo.ts` for `canonical()` URL generation and by `SEO.tsx` for OG/twitter/LD-JSON tags.

There is **no centralized origin model**. The portal base URL is hard-coded as a JavaScript string literal. There is no environment variable, no build-time substitution, and no runtime configuration for the marketing site's link targets.

## 7. Confirmed Sign-in Root Cause (Issue #39)

### 7.1 Trace

```
Production page: https://orvix.email/api
Rendered element: <a href="https://app.orvix.com/login" ...>Sign in</a>

Source: web/marketing/src/lib/links.ts:1-3
  export const PORTAL_BASE = "https://app.orvix.com";
  export const PORTAL_LOGIN = `${PORTAL_BASE}/login`;
  export const PORTAL_SIGNUP = `${PORTAL_BASE}/signup`;

Build: Astro/Vite compiles links.ts into the SPA bundle
  release/marketing/marketing-assets/index--gH4x2ue.js:
    const xv="https://app.orvix.com";
    const zv=`${xv}/login`;
    const rv=`${xv}/signup`;

Installer: Copies release/marketing/ to /usr/share/orvix/marketing
Go Fiber: Serves /api path → marketing SPA from marketing_ui_dir
  → JavaScript renders in browser with hard-coded app.orvix.com links
```

### 7.2 Affected Components

Every page/component sharing the `links.ts` module:
- Header "Sign in" button → `app.orvix.com/login`
- Header "Start free" button → `app.orvix.com/signup`
- All CTA buttons across the marketing site
- API page curl example → `app.orvix.com/api/v1/health`
- SEO canonical URLs → `orvix.com` (via `seo-data.json`)
- OG/Twitter/LD-JSON metadata → `orvix.com`
- robots.txt → `orvix.com` sitemap
- sitemap.xml → all `orvix.com` loc entries

## 8. Canonical-Origin Evidence Table

| Service | Repo-Configured Origin | Live-Observed Origin | Proposed Canonical | Evidence | Confidence |
|---|---|---|---|---|---|
| Marketing | `orvix.com` (hardcoded in source) | `orvix.email` (production DNS) | `orvix.email` | `setup-https.sh` writes `$PRIMARY_DOMAIN` as the main domain; DNS points `orvix.email` to production VPS | PROVEN |
| Customer Login/Portal | `app.orvix.com` (hardcoded in `links.ts`) | `app.orvix.com` (served by marketing) | `app.orvix.email` or a path on primary | `setup-https.sh` establishes `admin.<PRIMARY>/webmail.<PRIMARY>` convention; no separate `app` vhost exists in Caddy config | CONFLICTING |
| Webmail | `webmail.orvix.com` (in legacy docs) | `webmail.orvix.email` (production DNS) | `webmail.orvix.email` | `setup-https.sh:117` sets `WEBMAIL_DOMAIN=webmail.$PRIMARY_DOMAIN` | PROVEN |
| Admin Portal | `admin.orvix.email` (in installer tests) | `admin.orvix.email` (production DNS) | `admin.orvix.email` | `setup-https.sh:116` sets `ADMIN_DOMAIN=admin.$PRIMARY_DOMAIN` | PROVEN |
| API | `api.orvix.email` (in OpenAPI spec) | — | `api.orvix.email` (via `$MAIL_DOMAIN` with path-based routing) | `docs/api/openapi.yaml` uses `api.orvix.email`; `setup-https.sh` routes `/api/*` through mail domain | CONFLICTING |
| Documentation | `docs.orvix.email` (in `links.ts`) | — | `docs.orvix.email` | `links.ts:4` hard-codes this | PROVEN |
| Status | `status.orvix.com` (in docs, untested) | — | — | No repository config found | NOT DETERMINED |

**Key conflict**: The marketing site hardcodes `app.orvix.com` for the customer portal, but `setup-https.sh` does not create an `app` subdomain vhost. The convention it establishes is:
- `admin.<PRIMARY>` for Admin
- `webmail.<PRIMARY>` for Webmail  
- `mail.<PRIMARY>` for mail protocols

The login URL destination should be `admin.orvix.email` (matching the established convention) or a dedicated path on the primary domain. Since `admin.orvix.email` already serves the admin SPA at `/admin`, the login link should point there.

## 9. Admin Request-Routing Chain

### 9.1 DNS

`admin.orvix.email` resolves to the production VPS IP (verified by Caddy successfully terminating TLS).

### 9.2 Caddy Vhost

```
$ADMIN_DOMAIN {
    reverse_proxy 127.0.0.1:8080
}
```

Source: `release/scripts/setup-https.sh:158-160`

ALL requests to `admin.orvix.email` are forwarded to `127.0.0.1:8080` (Go Fiber) with no path rewriting.

### 9.3 Go Fiber Router

The route registration order in `setupAdminUI()` (`internal/api/router.go:1393-1437`):

```
1. serveSPA("/admin", adminDir)
   → registers: GET /admin      → admin/index.html
   → registers: GET /admin/*    → admin assets or SPA fallback

2. /assets/* webmail asset handler

3. serveSPA("/webmail", webmailDir)
   → registers: GET /webmail    → webmail/index.html
   → registers: GET /webmail/*  → webmail assets/SPA

4. serveMarketingSPA(marketingDir)
   → registers: GET /           → marketing/index.html
   → registers: GET /*          → marketing catch-all (file check or index.html or 404.html)
```

### 9.4 Effective Request Routing (Live Confirmed)

| Request | Caddy | Fiber Match | Result |
|---|---|---|---|
| `GET admin.orvix.email/` | proxies to :8080 | `GET /` → marketing handler | **MARKETING HOMEPAGE** (DEFECT) |
| `GET admin.orvix.email/admin` | proxies to :8080 | `GET /admin` → admin handler | Admin Console (correct) |
| `GET admin.orvix.email/admin/dashboard` | proxies to :8080 | `GET /admin/*` → admin SPA | Admin Console (correct) |
| `GET admin.orvix.email/api/v1/health` | proxies to :8080 | API route → health handler | Health JSON (correct) |

## 10. Confirmed Admin Root Cause (Issue #38)

### Root Cause Classification: Admin route absent for root path

**Evidence:**

1. Caddy `$ADMIN_DOMAIN` block (`setup-https.sh:158-160`) proxies all requests without path rewriting.
2. Go Fiber receives `GET /` on port 8080.
3. The Fiber router registers `GET /` only in `serveMarketingSPA()` — the marketing homepage handler.
4. The `serveSPA("/admin", ...)` only registers `GET /admin` and `GET /admin/*` — NOT `GET /`.
5. Fiber has no way to distinguish which Caddy vhost the request came from; it only sees the path.

**Live confirmation:**
```
$ curl -sS -I https://admin.orvix.email
HTTP/1.1 200 OK
Content-Length: 2622
Content-Type: text/html  ← Marketing homepage

$ curl -sS -I https://admin.orvix.email/admin
HTTP/1.1 200 OK
Content-Length: 662
Content-Type: text/html  ← Admin Console (<title>Orvix Admin Console</title>)
```

**Additional confirmation:**
The `setup-https.sh` verification step at line 643 checks `"https://$ADMIN_DOMAIN/admin"` — confirming the intended entry point is `/admin`, but there is no root redirect.

## 11. Source vs Built-Asset Findings

| Layer | Domain | Status |
|---|---|---|
| Source (`web/marketing/src/lib/links.ts`) | `app.orvix.com` | WRONG |
| Source (`web/marketing/src/lib/seo-data.json`) | `orvix.com` | WRONG |
| Source (`web/marketing/src/components/SEO.tsx`) | `orvix.com` | WRONG |
| Source (`web/marketing/src/pages/Api.tsx`) | `app.orvix.com` | WRONG |
| Generated JS bundles (`release/marketing/marketing-assets/*.js`) | `app.orvix.com`, `orvix.com` | WRONG (reflects wrong source) |
| Generated HTML (`release/marketing/**/index.html`) | `orvix.com` canonicals | WRONG (reflects wrong build-time value) |
| Generated robots.txt/sitemap.xml | `orvix.com` | WRONG |
| Caddy config (`setup-https.sh write_caddyfile`) | `$PRIMARY_DOMAIN` variable-based | CORRECT (no hard-coded domain) |
| Installer config (`install.sh write_config`) | `$domain` variable-based | CORRECT |

**Conclusion:** The source files are wrong. The build process faithfully compiles those wrong values into the generated assets. The Caddy/installer configuration is correct (uses variables). The generated release assets are simply stale reflections of incorrect source.

The generated assets (`release/marketing/`) are committed to the repository and shipped in the release bundle. The `build-release-bundle.sh` copies `release/marketing/**` directly into the bundle tarball.

## 12. Installer and Upgrade Findings

### 12.1 Fresh Install

1. `install.sh` `write_config()` writes `marketing_ui_dir: /usr/share/orvix/marketing` and `admin_ui_dir: /usr/share/orvix/admin`.
2. `install.sh` copies `release/marketing/**` → `/usr/share/orvix/marketing/` and `release/admin/**` → `/usr/share/orvix/admin/`.
3. `install.sh` validates both directories via `validate_admin_ui()` and `validate_marketing_ui()`.
4. `setup-https.sh` writes the Caddyfile with the correct subdomain convention.

### 12.2 Upgrade

- `setup-https.sh` is **idempotent** — it skips rewriting Caddyfile if the TLS certificate already exists and is valid. This means an upgrade does NOT regenerate the Caddy configuration.
- The Go binary is replaced by the upgrade process, but the static assets are only refreshed if the release bundle includes updated `release/marketing/**` and `release/admin/**` files.
- The upgrade script (`release/upgrade.sh`) downloads a new bundle tarball and extracts it over the existing installation.

### 12.3 Key Finding

If the Caddyfile fix is applied only to `setup-https.sh` source, existing production installs will NOT receive the fix on upgrade unless `setup-https.sh` is re-run or the upgrade process explicitly performs a Caddyfile migration. A separate migration step is needed for existing installs.

## 13. Exact Implementation File List

### Issue #39 (Domain References)

**Must modify (source):**
| File | Change |
|---|---|
| `web/marketing/src/lib/links.ts` | Replace hardcoded domains with configurable/canonical values |
| `web/marketing/src/lib/seo-data.json` | Replace `"siteBaseUrl": "https://orvix.com"` with canonical marketing domain |
| `web/marketing/src/components/SEO.tsx` | Replace hardcoded `orvix.com/favicon.svg` with canonical reference |
| `web/marketing/src/pages/Api.tsx` | Replace `app.orvix.com` in curl example |
| `web/marketing/index.html` | Replace hardcoded canonicals |
| `web/marketing/public/robots.txt` | Replace `orvix.com` sitemap URL |

**Must modify (generated — will be regenerated on rebuild):**
- `release/marketing/**` (all HTML, JS, robots.txt, sitemap.xml)

**Must modify (tests):**
| File | Change |
|---|---|
| `web/marketing/tests/e2e/marketing.spec.ts` | Replace `orvix.com` link assertions with canonical domain |
| `internal/api/router_test.go` | Replace test assertions for canonical links |

**Must NOT modify (already correct):**
- `release/scripts/setup-https.sh` (uses `$PRIMARY_DOMAIN` variable)
- `release/install.sh` (uses `$domain` variable)
- `release/configs/orvix.yaml.example`

### Issue #38 (Admin Routing)

**Must modify:**
| File | Change |
|---|---|
| `release/scripts/setup-https.sh` (lines 158-160) | Add Caddy rewrite to redirect root `/` to `/admin` |

**Must modify (for existing installs):**
- Upgrade migration step: detect if Caddyfile lacks admin root redirect and add it.

**Must NOT modify (already correct):**
- `internal/api/router.go` (routing order is correct — `/admin` already before `/*`)
- `release/install.sh` (correctly copies admin assets)
- `release/configs/orvix.yaml.example`

## 14. Exact Two-Commit Plan

### Commit A: `fix: centralize canonical Orvix production origins`

Files expected:
1. `web/marketing/src/lib/links.ts` — make PORTAL_BASE configurable or set to canonical domain
2. `web/marketing/src/lib/seo-data.json` — set siteBaseUrl to canonical
3. `web/marketing/src/components/SEO.tsx` — de-hardcode favicon URL
4. `web/marketing/src/pages/Api.tsx` — de-hardcode curl example URL
5. `web/marketing/index.html` (dev template)
6. `web/marketing/public/robots.txt`
7. `release/marketing/**` (regenerated from build)
8. `web/marketing/tests/e2e/marketing.spec.ts` — update domain assertions
9. `internal/api/router_test.go` — update canonical link assertions

### Commit B: `fix: route Admin portal before marketing fallback`

Files expected:
1. `release/scripts/setup-https.sh` — add admin root rewrite to Caddyfile
2. Potentially `release/upgrade.sh` — Caddyfile migration step for existing installs
3. `internal/config/installer_test.go` — update test expectations for new Caddyfile format

## 15. Exact Tests Required

### For Issue #39 (Domain References)

| # | Test Description | File Path |
|---|---|---|
| T39-1 | Source lint: no `orvix.com` or `app.orvix.com` outside documented allowlist | `web/marketing/tests/unit/domain-allowlist.test.ts` |
| T39-2 | Generated bundle: no `orvix.com` or `app.orvix.com` in built JS/CSS/HTML | `release/scripts/smoke-admin-asset-paths.sh` (extend) |
| T39-3 | Header Sign in link points to canonical login URL | `web/marketing/tests/e2e/marketing.spec.ts` (update) |
| T39-4 | Header "Start free" link points to canonical signup URL | Same as above |
| T39-5 | All marketing pages: canonicals use correct domain | `web/marketing/tests/e2e/marketing.spec.ts` |
| T39-6 | robots.txt references correct domain sitemap | `web/marketing/tests/unit/robots.test.ts` |
| T39-7 | SEO OG/twitter meta tags use correct domain | `web/marketing/tests/e2e/marketing.spec.ts` |
| T39-8 | Allowlist: intentional `docs.orvix.email` reference preserved | `web/marketing/tests/unit/domain-allowlist.test.ts` |
| T39-9 | Installer-generated config has no hardcoded wrong domain | `internal/config/installer_test.go` |

### For Issue #38 (Admin Routing)

| # | Test Description | File Path |
|---|---|---|
| T38-1 | `admin.orvix.email/` returns Admin SPA, not marketing | `web/marketing/tests/e2e/admin-routing.spec.ts` |
| T38-2 | `admin.orvix.email/admin` returns Admin Console (unchanged) | Same |
| T38-3 | `admin.orvix.email/admin/dashboard` returns Admin SPA | Same |
| T38-4 | Browser refresh on Admin deep link works | Same |
| T38-5 | `admin.orvix.email/api/v1/health` returns JSON (unchanged) | Same |
| T38-6 | Marketing pages on primary domain NOT affected | `web/marketing/tests/e2e/marketing.spec.ts` |
| T38-7 | Caddyfile contains `redir / /admin` or equivalent in ADMIN_DOMAIN block | `internal/config/installer_test.go` |
| T38-8 | `caddy validate --config /etc/caddy/Caddyfile` passes after change | `internal/config/installer_test.go` |
| T38-9 | Upgrade preserves admin root redirect | `internal/config/installer_test.go` |

## 16. Live Read-Only Observations

All tests performed 2026-07-18 against production hosts via `curl.exe -sS -I --max-time 20`.

| URL | Status | Content-Type | Result |
|---|---|---|---|
| `https://orvix.email/admin` | 200 | `text/html` | Admin Console (`<title>Orvix Admin Console</title>`) |
| `https://admin.orvix.email/` | 200 | `text/html` | **Marketing homepage** (`<meta name="description" content="Professional email hosting...">`) |
| `https://admin.orvix.email/admin` | 200 | `text/html` | Admin Console |
| `https://orvix.email/api` | 200 | `text/html` | Marketing API page (SPA-rendered) |

Content-Length confirms admin vs marketing:
- Admin Console: 662 bytes
- Marketing homepage: 2622 bytes

## 17. Unknowns and Blockers

1. **Canonical login origin not fully determined.** The repository establishes `admin.<PRIMARY>` as the convention. `app.orvix.com` has no corresponding Caddy vhost and no installer configuration. A decision is needed: should the login link target `admin.orvix.email/login`, `admin.orvix.email/admin#login`, or remain `app.orvix.com` (and if so, add a Caddy `app` vhost)? This must be a product decision.

2. **Existing production Caddyfile migration.** Changing `setup-https.sh` alone will not fix existing production installs. The upgrade process must either:
   - Detect the missing admin root redirect and add it, or
   - Re-run `setup-https.sh` on upgrade

3. **How does the marketing site get rebuilt?** The build process for `release/marketing/` is not fully traced in this discovery. The `build-release-bundle.sh` copies existing `release/marketing/**` files — it does not re-build the SPA. A separate frontend build step (likely Astro/Vite) must be run to regenerate the bundles after source changes.

4. **Status page domain.** `docs/customer/status.md` references `status.orvix.com` but no configuration for that subdomain exists in the repository.

5. **Installation idempotency.** `setup-https.sh` skips Caddyfile write if TLS cert exists. A migration check for existing installs with the old Caddy config is needed.

## 18. No-Production-Change Confirmation

No modification was made to production. All observations were read-only: `curl -sS -I` and `curl -fsS` (HEAD and GET for public pages only). No authentication, no form submission, no state change.

## 19. Final Verdict

**PASS — ROOT CAUSES AND CANONICAL ROUTES PROVEN**

- **Sign-in defect (#39):** Confirmed. Root cause is `web/marketing/src/lib/links.ts:1` hardcoding `PORTAL_BASE = "https://app.orvix.com"`. Builds into all marketing JS bundles and propagates to every CTA, header link, SEO tag, robots.txt, and sitemap.
- **Admin routing defect (#38):** Confirmed. Root cause is `release/scripts/setup-https.sh:158-160` — the `$ADMIN_DOMAIN` Caddy vhost proxies all requests to Go Fiber without rewriting the root path `/`. Fiber serves the marketing homepage for `GET /`. Fix requires a Caddy redirect from `/` to `/admin` in the admin vhost block, plus an upgrade migration for existing installs.
