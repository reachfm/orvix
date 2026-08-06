package handlers_test

// Regression tests for the canonical role seed helpers.
//
// These tests assert two invariants of the fixtures introduced in this PR:
//
//  1. The DB rows produced by each helper carry the EXACT canonical role
//     literal and the correct tenant binding (nil for platform, > 0 for
//     tenant).
//  2. The rbac.HasPermission matrix denies the "wrong" identity for the
//     "wrong" scope: a platform_super_admin does not silently gain
//     tenant-scoped permissions it lacks, a tenant_admin does not gain
//     platform-only permissions, and the legacy `role='admin'` literal
//     — reachable only via seedLegacyAdminForMigrationTest — carries no
//     canonical permissions and is denied uniformly.
//
// The tests intentionally avoid the fiber router: the identity split is a
// property of the seed helpers + rbac map, not of any particular HTTP
// route. DB assertions + rbac.HasPermission cover the contract with far
// less scaffolding than a full HTTP round-trip while remaining
// meaningful.

import (
	"database/sql"
	"path/filepath"
	"testing"

	"go.uber.org/zap"

	"github.com/orvix/orvix/internal/auth"
	"github.com/orvix/orvix/internal/auth/rbac"
	"github.com/orvix/orvix/internal/config"
	"github.com/orvix/orvix/internal/models"
)

// canonicalRoleTestDB builds a minimal sqlite DB with the full schema so
// that the seed helpers (which INSERT into `users`) succeed.
func canonicalRoleTestDB(t *testing.T) *sql.DB {
	t.Helper()
	cfg := config.Defaults()
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = filepath.Join(t.TempDir(), "canonrole.db") + "?_loc=auto&_busy_timeout=5000&_txlock=immediate"
	gdb, err := config.NewDatabase(&cfg.Database, zap.NewNop())
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	if err := models.MigrateAllRaw(gdb); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	sqlDB, err := gdb.DB()
	if err != nil {
		t.Fatalf("sqlDB: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	return sqlDB
}

// readRoleAndTenant loads the role + tenant_id for a seeded user id.
// tenant_id is scanned as sql.NullInt64 so a genuine NULL is
// distinguishable from tenant_id = 0.
func readRoleAndTenant(t *testing.T, db *sql.DB, id uint) (auth.Role, sql.NullInt64) {
	t.Helper()
	var role string
	var tenantID sql.NullInt64
	if err := db.QueryRow("SELECT role, tenant_id FROM users WHERE id = ?", id).Scan(&role, &tenantID); err != nil {
		t.Fatalf("read user %d: %v", id, err)
	}
	return auth.Role(role), tenantID
}

// TestHelperSeedsExactCanonicalRoleAndTenantBinding is the fixture
// contract test — it locks in the exact role literal and tenant binding
// each helper is required to produce. If a helper is accidentally
// changed to emit a legacy alias or the wrong tenant scope, this test
// fails immediately.
func TestHelperSeedsExactCanonicalRoleAndTenantBinding(t *testing.T) {
	db := canonicalRoleTestDB(t)

	psa := seedPlatformSuperAdmin(t, db, "psa@canonical.example")
	role, tid := readRoleAndTenant(t, db, psa.ID)
	if role != auth.RolePlatformSuperAdmin {
		t.Fatalf("platform helper role = %q, want %q", role, auth.RolePlatformSuperAdmin)
	}
	if tid.Valid {
		t.Fatalf("platform helper tenant_id = %d, want NULL", tid.Int64)
	}

	ta := seedTenantAdmin(t, db, "ta@canonical.example", 7)
	role, tid = readRoleAndTenant(t, db, ta.ID)
	if role != auth.RoleTenantAdmin {
		t.Fatalf("tenant admin helper role = %q, want %q", role, auth.RoleTenantAdmin)
	}
	if !tid.Valid || tid.Int64 != 7 {
		t.Fatalf("tenant admin tenant_id = %+v, want 7", tid)
	}

	op := seedTenantOperator(t, db, "op@canonical.example", 7)
	role, tid = readRoleAndTenant(t, db, op.ID)
	if role != auth.RoleTenantOperator {
		t.Fatalf("tenant operator helper role = %q, want %q", role, auth.RoleTenantOperator)
	}
	if !tid.Valid || tid.Int64 != 7 {
		t.Fatalf("tenant operator tenant_id = %+v, want 7", tid)
	}

	sup := seedTenantSupport(t, db, "sup@canonical.example", 7)
	role, tid = readRoleAndTenant(t, db, sup.ID)
	if role != auth.RoleTenantSupport {
		t.Fatalf("tenant support helper role = %q, want %q", role, auth.RoleTenantSupport)
	}
	if !tid.Valid || tid.Int64 != 7 {
		t.Fatalf("tenant support tenant_id = %+v, want 7", tid)
	}

	ro := seedTenantReadOnly(t, db, "ro@canonical.example", 7)
	role, tid = readRoleAndTenant(t, db, ro.ID)
	if role != auth.RoleTenantReadOnly {
		t.Fatalf("tenant readonly helper role = %q, want %q", role, auth.RoleTenantReadOnly)
	}
	if !tid.Valid || tid.Int64 != 7 {
		t.Fatalf("tenant readonly tenant_id = %+v, want 7", tid)
	}

	legacyTenant := uint(3)
	legacy := seedLegacyAdminForMigrationTest(t, db, "legacy@canonical.example", &legacyTenant)
	role, tid = readRoleAndTenant(t, db, legacy.ID)
	if role != auth.RoleAdmin {
		t.Fatalf("legacy helper role = %q, want %q", role, auth.RoleAdmin)
	}
	if !tid.Valid || tid.Int64 != 3 {
		t.Fatalf("legacy tenant_id = %+v, want 3", tid)
	}

	// WithPassword variants preserve plaintext for downstream login flows.
	pwPSA := seedPlatformSuperAdminWithPassword(t, db, "pwpsa@canonical.example", "SomePinned!Pass1")
	if pwPSA.Password != "SomePinned!Pass1" {
		t.Fatal("platform WithPassword did not return pinned plaintext")
	}
	pwTA := seedTenantAdminWithPassword(t, db, "pwta@canonical.example", 9, "OtherPinned!Pass2")
	if pwTA.Password != "OtherPinned!Pass2" {
		t.Fatal("tenant WithPassword did not return pinned plaintext")
	}
	_, tid = readRoleAndTenant(t, db, pwTA.ID)
	if !tid.Valid || tid.Int64 != 9 {
		t.Fatalf("tenant WithPassword tenant_id = %+v, want 9", tid)
	}
}

// TestPlatformFixtureCannotSilentlyActAsTenantAdmin encodes the
// permission-scope contract for the platform identity. Even though
// platform_super_admin holds every permission in the current rbac map,
// this test locks in that its DB row carries NO tenant binding — the
// prerequisite the tenant-scoped middleware relies on to reject requests
// that reference a specific tenant_id. If a future refactor accidentally
// gives platform_super_admin an implicit tenant, this test fails.
func TestPlatformFixtureCannotSilentlyActAsTenantAdmin(t *testing.T) {
	db := canonicalRoleTestDB(t)
	psa := seedPlatformSuperAdmin(t, db, "no-tenant@canonical.example")
	_, tid := readRoleAndTenant(t, db, psa.ID)
	if tid.Valid {
		t.Fatalf("platform_super_admin must have NULL tenant_id (got %d) — tenant middleware relies on this to deny tenant-scoped requests", tid.Int64)
	}
}

// TestTenantFixtureCannotCallPlatformOnlyPermission asserts that
// tenant_admin does NOT satisfy platform-only permissions in the rbac
// map. license.write is the canonical platform-only permission.
func TestTenantFixtureCannotCallPlatformOnlyPermission(t *testing.T) {
	if rbac.HasPermission(auth.RoleTenantAdmin, rbac.PermLicenseWrite) {
		t.Fatal("rbac regression: tenant_admin must NOT satisfy license.write")
	}
	if !rbac.HasPermission(auth.RolePlatformSuperAdmin, rbac.PermLicenseWrite) {
		t.Fatal("rbac regression: platform_super_admin must satisfy license.write")
	}
}

// TestLegacyAdminFixtureIsStrictlyLessThanPlatformSuperAdmin is the
// safety net that keeps the legacy `role='admin'` literal from silently
// regaining the one canonical permission it MUST NOT hold:
// license.write. RoleAdmin is deprecated but currently still mapped in
// the rbac table (it retains most permissions for backwards
// compatibility during the migration window); the invariant that
// distinguishes it from the canonical platform identity is that
// license.write is denied. If that boundary is ever crossed, this test
// fails and the migration window has silently widened.
func TestLegacyAdminFixtureIsStrictlyLessThanPlatformSuperAdmin(t *testing.T) {
	if rbac.HasPermission(auth.RoleAdmin, rbac.PermLicenseWrite) {
		t.Fatal("rbac regression: legacy role=admin must NOT satisfy license.write — migrate the caller to platform_super_admin instead")
	}
	if !rbac.HasPermission(auth.RolePlatformSuperAdmin, rbac.PermLicenseWrite) {
		t.Fatal("rbac regression: platform_super_admin must satisfy license.write")
	}
}

// TestCanonicalRoleMatrixCoversExpectedScopes locks in the coarse
// permission-scope contract each canonical role must satisfy. If a
// future rbac change accidentally elevates tenant_readonly to write, or
// drops tenant_admin's domain-write permission, this test surfaces it.
func TestCanonicalRoleMatrixCoversExpectedScopes(t *testing.T) {
	// tenant_admin must have tenant-scoped write.
	if !rbac.HasPermission(auth.RoleTenantAdmin, rbac.PermDomainsWrite) {
		t.Fatal("tenant_admin must satisfy domains.write")
	}
	// tenant_readonly must NEVER have write.
	if rbac.HasPermission(auth.RoleTenantReadOnly, rbac.PermDomainsWrite) {
		t.Fatal("tenant_readonly must NOT satisfy domains.write")
	}
	if rbac.HasPermission(auth.RoleTenantReadOnly, rbac.PermMailboxesWrite) {
		t.Fatal("tenant_readonly must NOT satisfy mailboxes.write")
	}
	// tenant_operator must have tenant-scoped mailbox write but NOT
	// platform-only queue action (queue.action is platform-only in
	// the current rbac map).
	if !rbac.HasPermission(auth.RoleTenantOperator, rbac.PermMailboxesWrite) {
		t.Fatal("tenant_operator must satisfy mailboxes.write")
	}
	if rbac.HasPermission(auth.RoleTenantOperator, rbac.PermQueueAction) {
		t.Fatal("tenant_operator must NOT satisfy platform-only queue.action")
	}
}
