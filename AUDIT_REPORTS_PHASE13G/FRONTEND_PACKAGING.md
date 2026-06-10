# Phase 13G Frontend Packaging

Admin UI:

- Release bundle exists under `release/admin`.
- Installer copies the complete directory to `/usr/share/orvix/admin`.
- Router serves it at `/admin`.

Webmail UI:

- Webmail Vite base is `/webmail`.
- Built bundle is included under `release/webmail`.
- Installer copies the complete directory to `/usr/share/orvix/webmail`.
- Router serves it at `/webmail`.

No customer-side npm commands are required on a fresh VPS.
