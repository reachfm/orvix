# fresh-install-sqlite idempotent-rerun failure — attribution analysis

## Failing symptom

CI job: **`fresh-install-sqlite`** in `.github/workflows/phase5-rc-fresh-install.yml`
Failing step: **`Restart persistence + idempotent rerun`** (workflow line 214)
Error text (log excerpt from PR#58 CI):

```
admin password is required; input ended before a password was provided
```

This error is emitted from `release/install.sh:556` and `:560`.

## Environment reproduction constraints

The full end-to-end reproduction requires:
- A Linux host with `systemd`, `sudo`, PID-1 init
- The workflow uses `ubuntu-24.04` runners with a real `install.sh` invocation via systemd

This investigation's host is Windows Git-Bash, which has no `systemd` and no way
to install/start the Orvix systemd unit. End-to-end replay is therefore not
possible in this environment. What is possible — and what this doc provides —
is a **code-level differential attribution** using `bash -n` syntax checks and
line-anchored comparisons, cross-referenced with the actual CI-run failures
already observed on both branches.

## CI execution evidence (authoritative, already observed)

| Branch | Head SHA | `fresh-install-sqlite` result |
|---|---|---|
| `origin/main` @ merge of PR #60 | `dc9bc91cc2ee9b944e7ba0c505a8c7cc5c11e3c1` | **PASS** (verified in PR #60 post-merge run) |
| PR #58 head | `251e172c0cfe78b52bdc81d04022c82cd3adc418` | **FAIL** with the exact quoted symptom (verified during PR #58 CI history) |
| PR #59 intermediate head | `e708926c8d26dab61a025078f10cf0c92db08f41` | **PASS** (verified in the earlier CI poll after this PR pushed the intermediate head) |

CI is authoritative for the execution behavior; the code-level analysis below
explains why.

## Root cause (code-level differential attribution)

The Go bootstrap flow (`cmd/orvix/main.go`) and the shell installer
(`release/install.sh`) must agree on the string value stored in `users.role`
for the initial admin. If they disagree, the idempotent-rerun path calls
`active_admin_count()` and it returns 0, so the installer thinks no admin
exists, tries to create one, and — under non-TTY CI stdin — fails at the
password prompt with the observed error.

### `origin/main` @ `dc9bc91` (baseline — currently passes)

- Bootstrap writes `role='admin'`
- `release/install.sh:1701` `active_admin_count()` checks `role IN ('admin','superadmin','super_admin')`
- `release/install.sh:1713` `admin_user_exists()` checks same triple
- `release/install.sh:2732` `users_count` scan checks `role='admin'`
- All queries agree with the bootstrap-planted role → rerun sees the admin exists → skips prompt → PASS

### PR #58 head @ `251e172c` (fails)

- Bootstrap writes `role='platform_super_admin'` (the canonical role)
- `release/install.sh:2732` `users_count` scan **was updated** to `role IN ('admin','platform_super_admin')` ✓
- `release/install.sh:1701` `active_admin_count()` **was NOT updated** — still `role IN ('admin','superadmin','super_admin')` ✗
- `release/install.sh:1713` `admin_user_exists()` **was NOT updated** — same ✗
- The idempotent-rerun code path at `release/install.sh:2954` invokes
  `active_admin_count()` (line 2954: `existing_admins="$(active_admin_count)"`)
- Because bootstrap planted `platform_super_admin` and `active_admin_count()`
  only counts `admin|superadmin|super_admin`, `existing_admins` returns 0
- The installer treats this as "no admin exists" → falls into the "create a new
  admin" branch → prompts for password → non-TTY CI stdin ends → error at
  `install.sh:556` or `:560`

### PR #59 head (this branch — expected to pass)

- Does not modify `release/install.sh` (production installer) — scope guard enforces this
- Does not modify `cmd/orvix/main.go` (bootstrap) — scope guard enforces this
- Bootstrap plants `role='admin'` (unchanged from main); `active_admin_count()` recognizes it → PASS

Verified via the intermediate-head CI (`e708926`) which passed
`fresh-install-sqlite`.

## Verdict

**The defect is genuinely PR #58's**, not PR #59's, and not `origin/main`'s.
It is a **partial fix** on PR #58: only one of three admin-lookup queries in
`release/install.sh` was updated to include the new canonical role. The
remaining two (`active_admin_count` at L1701 and `admin_user_exists` at L1713)
still use the legacy triple, and the idempotent-rerun code path uses
`active_admin_count`.

## Recommended remediation (for PR #58's fix cycle — NOT this PR)

Update `release/install.sh:1701` and `release/install.sh:1713` to include
`platform_super_admin` in their role allow-lists:

```bash
# release/install.sh:1701 (active_admin_count)
n="$(_orvix_db_scalar "SELECT COUNT(*) FROM users WHERE role IN ('admin','superadmin','super_admin','platform_super_admin') AND active = $(_orvix_db_true);")" || true

# release/install.sh:1713 (admin_user_exists)
count="$(_orvix_db_scalar "SELECT COUNT(*) FROM users WHERE email = '$sql_email' AND role IN ('admin','superadmin','super_admin','platform_super_admin') AND active = $(_orvix_db_true);")" || true
```

This is a two-line change scoped entirely to `release/install.sh` on PR #58's
branch. It restores agreement between the Go bootstrap and the shell
installer's rerun logic without any schema/migration/API changes.

**This PR (#59) does not implement that fix** — production installer code is
strictly out of PR #59's scope. The fix belongs on PR #58.
