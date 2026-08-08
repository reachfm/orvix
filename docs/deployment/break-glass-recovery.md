# Break-Glass Recovery

`orvix admin recover` and `orvix admin reset-password` are **implemented**
(see `cmd/orvix/admin_recovery.go`). Anyone doing recovery today MUST use
these commands — raw SQL against the `users` table is not a supported
recovery path, since it bypasses session revocation, `token_version`
invalidation, and the audit trail these commands guarantee.

## Goals

- Recover an install whose only Platform Super Admin has lost MFA or password.
- Never require raw SQL as the normal recovery path.
- Never accept a password on `argv` or in the environment.
- Always emit an audit event and revoke existing sessions.

## CLI

```
orvix admin recover --email <email>
orvix admin reset-password --email <email>
```

`recover` behaviour:

1. Refuses to run unless the process is root (`EUID == 0`). Checked before
   the database is ever opened.
2. Opens the same config/database connection the running service uses
   (`internal/config.Load` + `internal/config.NewDatabase`) — never a second
   or alternate connection.
3. Locates the user by case-insensitive email match. Zero or more-than-one
   matches fail closed.
4. Requires `active = true` and `deleted_at IS NULL`.
5. Requires the source role to be an already-canonical `platform_super_admin`
   (idempotent recovery) or `tenant_admin`, or a legacy role that
   `auth.NormalizeRole` maps to one of those two (e.g. `superadmin`). All
   other roles — including `tenant_operator`, `tenant_support`,
   `tenant_readonly`, `user`, `billing`, an ambiguous bare `admin` with no
   tenant context, or anything unknown/empty — are rejected before mutation.
6. Prompts interactively for:
   - Confirmation: the operator re-types the exact normalized email.
   - New password (twice, hidden, TTY-only — never argv/env/config).
7. In one transaction, rewrites the row:
   ```
   role = 'platform_super_admin'
   tenant_id = NULL
   password_hash = <the same Argon2id hash format auth.HashPassword produces>
   mfa_enabled = 0
   mfa_secret = ''
   token_version = token_version + 1
   ```
   (plus the newer pending-MFA columns, cleared the same way), revokes every
   session for the user, and writes one `coremail_audit` row
   (`action='admin.recover', actor='local-root', target=<email>,
   result='success'`). Any failure at any step rolls back the whole
   transaction — no partial mutation is ever committed.
8. Prints only `OK: <email> recovered as platform_super_admin` — never the
   password, its hash, a session token, or the database DSN.

`reset-password` behaviour is the same shared contract, restricted to
rotating the password (and bumping `token_version` / revoking sessions /
writing an `admin.password_reset` audit row) of an EXISTING
`platform_super_admin` or `tenant_admin` — it never changes `role` or
`tenant_id`.

## Anti-goals

- No API endpoint. Ever.
- No remote invocation.
- No password on the command line.
- No "reset every admin" mode. One user per invocation.

## Operational recommendation

Keep at least **two** Platform Super Admin accounts on every deployment so a
lockout of one still allows normal recovery via the other's console without
touching this CLI.
