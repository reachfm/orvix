# Break-Glass Recovery (Design Doc — not yet implemented)

This document describes the intended shape of a future root-only CLI for
recovering a locked-out platform. It is **not** implemented in Phase 1.
Anyone doing recovery today does it with `psql` under the ops runbook.

## Goals

- Recover an install whose only Platform Super Admin has lost MFA or password.
- Never require raw SQL as the normal recovery path.
- Never accept a password on `argv` or in the environment.
- Always emit an audit event and revoke existing sessions.

## Proposed CLI

```
orvix admin recover --email <email>
```

Behaviour:

1. Refuses to run unless `EUID == 0` on Linux (or an equivalent local-root
   check). No sudo bypass.
2. Reads the config file used by the service, opens the same database
   connection.
3. Locates the user by email (case-insensitive).
4. Prompts interactively for:
   - New password (twice, hidden).
   - MFA recovery / disable confirmation.
   - Confirmation prompt with the exact email in ALL CAPS.
5. Rewrites the row:
   ```
   role = 'platform_super_admin'
   tenant_id = NULL
   password_hash = <argon2id of new password>
   mfa_enabled = 0
   mfa_secret = ''
   token_version = token_version + 1
   ```
6. Revokes every session for this user.
7. Writes an audit row: `action='admin.recover', actor='local-root',
   target=<email>, result='ok'`.
8. Prints the new user id and role and exits.

## Anti-goals

- No API endpoint. Ever.
- No remote invocation.
- No password on the command line.
- No "reset every admin" mode. One user per invocation.

## Operational recommendation

Keep at least **two** Platform Super Admin accounts on every deployment so a
lockout of one still allows normal recovery via the other's console without
touching this CLI.
