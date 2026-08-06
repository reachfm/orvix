# Canonical role fixtures — remaining legacy-string allowlist

This document enumerates every remaining occurrence of a bare
`admin`/`superadmin`/`operator` string literal in `internal/api/handlers/`
test code. Each occurrence is classified. **No unexplained legacy fixture
remains.**

## Class A — helper implementation internals

Legitimate: this is the seed helper that intentionally plants the legacy
role for compatibility/migration tests.

| File | Line | Occurrence |
|---|---|---|
| `internal/api/handlers/testhelpers_role_test.go` | ~156 | `insertUser(t, db, email, "admin", tenantID)` inside `seedLegacyAdminForMigrationTest` |

## Class B — static scanner regex

Legitimate: the scanner itself must contain the literal strings it detects.

| File | Line | Occurrence |
|---|---|---|
| `internal/api/handlers/no_direct_role_gates_test.go` | 48-49 | regex source strings `"admin"`, `"superadmin"` |

## Class C — canonical-role denial tests (PR #60 scope)

Legitimate: these tests intentionally plant legacy strings and prove denial.

| File | Line | Occurrence |
|---|---|---|
| `internal/api/handlers/canonical_role_gates_test.go` | 90 | `runQueueGate(t, "superadmin")` — denial-path test |
| `internal/api/handlers/canonical_role_gates_test.go` | 123 | `if tc.role == "admin"` — matrix classifier |
| `internal/api/handlers/canonical_role_gates_test.go` | 168 | `[]string{"platform_super_admin", "superadmin"}` — accept-list check |
| `internal/api/handlers/enterprise_parity_test.go` | 365 | `c.Locals("role", "superadmin")` — normalization coverage |

## Class D — coremail_mailboxes.local_part (NOT the users.role column)

Legitimate: these INSERTs target `coremail_mailboxes`, where the value
`'admin'` is the mailbox **local part** (as in `admin@domain`), not a
user's role.

| File | Line | Occurrence |
|---|---|---|
| `internal/api/handlers/enterprise_mutation_smoke_test.go` | 98 | `INSERT INTO coremail_mailboxes (... local_part ...) VALUES (..., 'admin', ...)` |
| `internal/api/handlers/tenant_isolation_test.go` | 93 | same shape |
| `internal/api/handlers/tenant_isolation_aliases_groups_test.go` | 74 | same shape |
| `internal/api/handlers/webmail_routing_test.go` | 194 | same shape |
| `internal/api/handlers/webmail_user_test.go` | 260 | same shape |
| `internal/api/handlers/rehash_on_login_test.go` | 73 | same shape |

## Class E — filesystem-path segments (NOT roles)

Legitimate: `admin` is the on-disk directory name for the admin SPA assets
under a test scratch dir. No auth semantics.

| Files | Pattern |
|---|---|
| `admin_domain_advanced_test.go`, `admin_mailing_public_folder_patch_test.go`, `admin_mfa_test.go`, `admin_queue_test.go`, `admin_settings_test.go`, `domain_provisioning_api_test.go`, `enterprise_mutation_smoke_test.go`, `queue_csrf_test.go`, `tenant_isolation_aliases_groups_test.go`, `tenant_isolation_test.go`, `webmail_auth_gate_test.go`, `webmail_auth_login_test.go`, `webmail_push_integration_test.go`, `webmail_routing_test.go`, `webmail_rules_test.go`, `webmail_user_test.go` | `adminDir := filepath.Join(scratchDir, "admin")` |

## Class F — DSL / JSON-payload `operator` field (NOT the role)

Legitimate: `"operator": "contains"` describes a rule-DSL comparison
operator, not a canonical role.

| File | Line | Occurrence |
|---|---|---|
| `internal/api/handlers/enterprise_admin_hardening_test.go` | 225, 227, 253, 276 | rule DSL |
| `internal/api/handlers/enterprise_admin_v3_test.go` | 224 | rule DSL |

## Class G — response-body substring detection

Legitimate: `bytes.Contains(resp.bodyBytes, []byte("admin"))` is a leak
detector, not an auth fixture.

| File | Line | Occurrence |
|---|---|---|
| `internal/api/handlers/enterprise_admin_test.go` | 397 | leak detection |

## Class H — documented compat fixtures pending PR #58 router split

These tests hit routes still gated on the top-level `admin` group at
`internal/api/router.go:1123` — `RequireAnyRole(RoleAdmin, RoleSuperAdmin,
RolePlatformSuperAdmin)`, which notably **does not include
`RoleTenantAdmin`**. Until PR #58 splits that group into strict
platform-only and tenant-admin sub-groups, these tests must plant a
legacy `admin` role (via `seedLegacyAdminForMigrationTestWithPassword`)
or a bare-SQL equivalent to satisfy the router allow-list. Each call site
carries a `// COMPAT:` code comment referencing this note.

| File | Migration helper / bare-SQL |
|---|---|
| `internal/api/handlers/admin_domain_advanced_test.go:82` | `seedLegacyAdminForMigrationTestWithPassword` |
| `internal/api/handlers/tenant_isolation_test.go:89` | `seedLegacyAdminForMigrationTestWithPassword` |
| `internal/api/handlers/tenant_isolation_aliases_groups_test.go:71` | `seedLegacyAdminForMigrationTestWithPassword` |
| `internal/api/handlers/tenant_isolation_matrix_test.go:67` | `seedLegacyAdminForMigrationTestWithPassword` |
| `internal/api/handlers/domain_list_isolation_test.go:88, 89` | `seedLegacyAdminForMigrationTestWithPassword` |
| `internal/api/handlers/webmail_user_test.go:242` | bare SQL with `// COMPAT:` comment (authentication test, not authorization) |
| `internal/api/handlers/rehash_on_login_test.go:62` | bare SQL with `// COMPAT:` comment (authentication test, not authorization) |

**Post-PR#58 follow-up**: after the router split lands, migrate every
Class H call site to `seedTenantAdminWithPassword` / `seedTenantAdmin`
per its test's actual semantic intent. The `seedLegacyAdminForMigrationTest*`
helpers should then only be used by Class A/C tests (helper internals
and PR#60-style denial regressions).
