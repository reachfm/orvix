# Fresh-install idempotent-rerun defect analysis

## Summary

**Verdict:** the defect is **introduced by PR #58** (pinned head `251e172c`). It is **not present** on `origin/main` at `dc9bc91`. It is a bootstrap-vs-installer mismatch: PR #58 changes the Go bootstrap to write the canonical role literal for the initial admin, but does not update the shell installer's admin-detection queries, so the idempotent rerun step no longer recognises the just-provisioned admin and re-enters the fresh-install prompt path.

## Failing step

Workflow file: `.github/workflows/phase5-rc-fresh-install.yml`
Step name (line 214): **`Restart persistence + idempotent rerun`**

Failing command (lines 230-232 of the workflow):

```bash
sudo env ORVIX_SOURCE_DIR=/tmp/stage/orvix ORVIX_USE_PREBUILT=1 ORVIX_PUBLIC_IPV4=100.0.0.10 \
  ORVIX_PRIMARY_DOMAIN="$DOMAIN" ORVIX_ADMIN_EMAIL="$ADMIN_EMAIL" \
  bash /tmp/stage/orvix/release/install.sh
```

Emitted failure (from `release/install.sh:556` or `:560`):

```
admin password is required; input ended before a password was provided
```

## Root cause

Two functions must agree on which role literal identifies an "active admin":

1. The Go bootstrap in `cmd/orvix/main.go` — writes the initial admin row.
2. The shell installer helpers in `release/install.sh` — `active_admin_count()`, `first_active_admin_email()`, `admin_user_exists()` — decide, on rerun, whether an admin already exists (and therefore whether to skip password prompting).

### origin/main (`dc9bc91`) — CONSISTENT, works

- `cmd/orvix/main.go:524` inserts `role='admin'` (legacy literal).
- `release/install.sh:1701` `active_admin_count` queries `role IN ('admin','superadmin','super_admin')`.

Rerun path:

1. `active_admin_count` returns 1 (bootstrap row was `role='admin'`).
2. `admin_mode="preserve"`, `prompt_password` is NOT called.
3. Installer completes without touching stdin. Green.

### PR #58 (`251e172`) — MISMATCHED, fails

Diff observed via `git fetch origin refs/pull/58/head` and `git diff dc9bc91..251e172`:

- `cmd/orvix/main.go:559` (new `seedAdminUser`) inserts `role=string(auth.RolePlatformSuperAdmin)` (i.e. `platform_super_admin`) with `tenant_id=NULL`.
- `release/install.sh:1701` `active_admin_count` is **unchanged** — still queries only the legacy triple `('admin','superadmin','super_admin')`.

Rerun path under PR #58:

1. Bootstrap wrote `role='platform_super_admin'` on first install.
2. On rerun, `active_admin_count` scans for legacy roles only, gets `0`.
3. Code at `install.sh:2977-2980` takes the fresh-install branch and calls `admin_password="$(prompt_password)"`.
4. CI workflow provides no `ORVIX_ADMIN_PASSWORD` and no TTY; the read at `install.sh:558` fails; `install.sh:560` fires the fatal message.

PR #58 *does* update ONE other query — `install.sh:2732` (post-install verification) now accepts both `'admin'` and `'platform_super_admin'`. But the three admin-detection helpers used by the idempotency-preserve branch were not updated:

- `active_admin_count` (line 1701)
- `first_active_admin_email` (line 1707)
- `admin_user_exists` (line 1713)

That partial migration is the defect.

## Reproduction (local)

```bash
git checkout 251e172c   # PR #58 head
# 1. Do a normal install:
sudo env ORVIX_ADMIN_EMAIL=admin@example.test \
         ORVIX_ADMIN_PASSWORD='SomeStrongPass!23' \
         ORVIX_PRIMARY_DOMAIN=example.test \
         ORVIX_PUBLIC_IPV4=100.0.0.10 \
         bash release/install.sh          # succeeds
sudo sqlite3 /var/lib/orvix/orvix.db "SELECT role FROM users;"
# platform_super_admin
# 2. Rerun WITHOUT the password (simulates the workflow step):
sudo env ORVIX_ADMIN_EMAIL=admin@example.test \
         ORVIX_PRIMARY_DOMAIN=example.test \
         ORVIX_PUBLIC_IPV4=100.0.0.10 \
         bash release/install.sh          # fails: admin password is required
```

## Recommendation for PR #58's fix cycle

Update the three admin-detection queries in `release/install.sh` to include the canonical literal alongside the legacy triple. Concretely, in `active_admin_count`, `first_active_admin_email`, and `admin_user_exists`, replace

```
role IN ('admin','superadmin','super_admin')
```

with

```
role IN ('admin','superadmin','super_admin','platform_super_admin')
```

This keeps backwards compatibility with legacy databases (which still exist during the migration window — see `seedLegacyAdminForMigrationTest` in the test suite) while recognising the new canonical bootstrap row. A follow-up canonicalisation pass can drop the legacy literals when the deprecated roles are removed from the rbac map.

## Scope

This investigation is **documentation only**. No production installer or bootstrap code was modified in this PR (PR #59). The fix belongs in PR #58's own review cycle, where the bootstrap literal was introduced.
