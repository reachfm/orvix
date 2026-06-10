# Phase 13G Route Fix

Problem: `POST /admin/login` could be intercepted by the SPA route and return 405.

Fix:

- `internal/api/router.go` registers `POST /admin/login` before static Admin UI routes.
- `GET /admin` and `GET /admin/*` continue serving the Admin SPA.
- `GET /webmail` and `GET /webmail/*` serve the Webmail SPA.

Regression test:

- `internal/api/router_test.go` verifies `POST /admin/login` reaches API validation and does not return 405.
- The same test verifies `/admin`, `/admin/login`, `/admin/styles.css`, `/admin/app.js`, `/webmail`, and `/webmail/inbox`.
