package handlers

// Canonical role seeding helpers for handler tests.
//
// These helpers exist to eliminate the ambiguous `role='admin'` fixture that
// hides the platform-vs-tenant identity split. Every helper writes an exact
// canonical role literal and the correct tenant binding so the surrounding
// test's intent is unambiguous at the call site.
//
// Contract:
//   - Never returns, logs, or prints passwords, cookies, tokens, or auth headers.
//   - Callers own the generated password only via the returned struct field;
//     nothing is written to test logs.
//   - Each helper is idempotent per (email) within a single test's DB: callers
//     are expected to use unique emails per test (e.g. via t.Name()).
//
// Canonical roles (see internal/auth/auth.go):
//   platform_super_admin  — tenant_id IS NULL
//   tenant_admin          — tenant_id > 0
//   tenant_operator       — tenant_id > 0
//   tenant_support        — tenant_id > 0
//   tenant_readonly       — tenant_id > 0
//
// Legacy `role='admin'` MUST NOT be seeded via a generic helper; use
// seedLegacyAdminForMigrationTest so intent is obvious.

import (
	"database/sql"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// SeededUser is returned by every canonical-role helper. It exposes only
// non-sensitive fields to test code; the ephemeral password is available
// for the caller's own login flow but is never logged by the helpers.
type SeededUser struct {
	ID       uint
	Email    string
	Password string // ephemeral; do not log
}

func mustHash(t *testing.T, pw string) string {
	t.Helper()
	h, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("bcrypt: %v", err)
	}
	return string(h)
}

func insertUser(t *testing.T, db *sql.DB, email, role string, tenantID *uint) SeededUser {
	t.Helper()
	pw := "CanonRolePass!2026-" + role
	hash := mustHash(t, pw)
	now := time.Now().UTC()
	var (
		res sql.Result
		err error
	)
	if tenantID == nil {
		res, err = db.Exec(
			"INSERT INTO users (created_at, updated_at, email, password_hash, role, tenant_id, active, email_verified) VALUES (?, ?, ?, ?, ?, NULL, 1, 1)",
			now, now, email, hash, role,
		)
	} else {
		res, err = db.Exec(
			"INSERT INTO users (created_at, updated_at, email, password_hash, role, tenant_id, active, email_verified) VALUES (?, ?, ?, ?, ?, ?, 1, 1)",
			now, now, email, hash, role, *tenantID,
		)
	}
	if err != nil {
		t.Fatalf("insert %s user: %v", role, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("lastInsertId: %v", err)
	}
	return SeededUser{ID: uint(id), Email: email, Password: pw}
}

// seedPlatformSuperAdmin inserts a user with role=platform_super_admin and no
// tenant binding. Use for tests that exercise routes under the `platform` or
// `internalOps` router groups, or top-level `admin` routes that are genuinely
// platform-only (backups, firewall, cluster, license, ...).
func seedPlatformSuperAdmin(t *testing.T, db *sql.DB, email string) SeededUser {
	t.Helper()
	return insertUser(t, db, email, "platform_super_admin", nil)
}

// seedTenantAdmin inserts a canonical tenant admin bound to tenantID. Use for
// tests that exercise tenant-scoped admin routes (dns, webmail admin,
// provision/domain, calendar, contacts, tasks, compose, intelligence,
// collaboration, compliance, tenant branding, enterprise mutations).
func seedTenantAdmin(t *testing.T, db *sql.DB, email string, tenantID uint) SeededUser {
	t.Helper()
	return insertUser(t, db, email, "tenant_admin", &tenantID)
}

// seedTenantOperator inserts a tenant_operator. Use where the test's
// assertion is specifically about the operator role.
func seedTenantOperator(t *testing.T, db *sql.DB, email string, tenantID uint) SeededUser {
	t.Helper()
	return insertUser(t, db, email, "tenant_operator", &tenantID)
}

// seedTenantSupport inserts a tenant_support user.
func seedTenantSupport(t *testing.T, db *sql.DB, email string, tenantID uint) SeededUser {
	t.Helper()
	return insertUser(t, db, email, "tenant_support", &tenantID)
}

// seedTenantReadOnly inserts a tenant_readonly user.
func seedTenantReadOnly(t *testing.T, db *sql.DB, email string, tenantID uint) SeededUser {
	t.Helper()
	return insertUser(t, db, email, "tenant_readonly", &tenantID)
}

// seedLegacyAdminForMigrationTest inserts the DEPRECATED `role='admin'`
// literal. Use ONLY in tests that intentionally exercise the legacy-role
// migration/normalization path. The verbose name is deliberate so that every
// call site advertises the migration-only intent at review time.
func seedLegacyAdminForMigrationTest(t *testing.T, db *sql.DB, email string, tenantID *uint) SeededUser {
	t.Helper()
	return insertUser(t, db, email, "admin", tenantID)
}
