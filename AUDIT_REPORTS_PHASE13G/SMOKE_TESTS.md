# Phase 13G Smoke Tests

Installer verification now checks:

- `systemctl is-active redis-server`
- `systemctl is-active orvix`
- `GET /api/v1/health`
- `GET /admin`
- `GET /webmail`
- `GET http://127.0.0.1:8081/.well-known/jmap`
- listening ports `25`, `110`, `143`, `8080`, `8081`, `6379`
- `POST /api/v1/auth/login` using installer-provided admin credentials

If any check fails, installer prints a failure screen and the last 80 log lines.
