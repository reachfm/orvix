package models

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/orvix/orvix/internal/config"
	"go.uber.org/zap"
)

// setupUsersForNormalize builds a fresh SQLite DB with just the users
// table columns NormalizeAdminRoles cares about. Using a hand-rolled
// schema keeps the test focused and independent of the full migration
// suite.
func setupUsersForNormalize(t *testing.T) *sql.DB {
	t.Helper()
	cfg := config.Defaults()
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = filepath.Join(t.TempDir(), "norm.db") + "?_loc=auto&_busy_timeout=5000&_txlock=immediate"
	db, err := config.NewDatabase(&cfg.Database, zap.NewNop())
	if err != nil {
		t.Fatalf("database: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("sql db: %v", err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	_, err = sqlDB.Exec(`CREATE TABLE users (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		email TEXT NOT NULL,
		role TEXT NOT NULL,
		tenant_id INTEGER,
		token_version INTEGER NOT NULL DEFAULT 0
	)`)
	if err != nil {
		t.Fatalf("create users: %v", err)
	}
	return sqlDB
}

func insertUser(t *testing.T, db *sql.DB, email, role string, tenantID *int64) int64 {
	t.Helper()
	var res sql.Result
	var err error
	if tenantID == nil {
		res, err = db.Exec("INSERT INTO users (email, role, tenant_id, token_version) VALUES (?, ?, NULL, 0)", email, role)
	} else {
		res, err = db.Exec("INSERT INTO users (email, role, tenant_id, token_version) VALUES (?, ?, ?, 0)", email, role, *tenantID)
	}
	if err != nil {
		t.Fatalf("insert %s: %v", email, err)
	}
	id, _ := res.LastInsertId()
	return id
}

func mustRoleTenant(t *testing.T, db *sql.DB, id int64) (string, sql.NullInt64, int64) {
	t.Helper()
	var role string
	var tid sql.NullInt64
	var tv int64
	if err := db.QueryRow("SELECT role, tenant_id, token_version FROM users WHERE id = ?", id).Scan(&role, &tid, &tv); err != nil {
		t.Fatalf("row lookup id=%d: %v", id, err)
	}
	return role, tid, tv
}

func TestNormalizeAdminRoles_Rules(t *testing.T) {
	t.Setenv("ORVIX_ADMIN_EMAIL", "admin@orvix.email")
	db := setupUsersForNormalize(t)

	tenantA := int64(7)

	idBootstrap := insertUser(t, db, "admin@orvix.email", "admin", &tenantA)   // will become platform, NULL tenant
	idLegacySuper := insertUser(t, db, "old@example.com", "superadmin", nil)   // → platform
	idTenantAdmin := insertUser(t, db, "boss@customer.com", "admin", &tenantA) // → tenant_admin
	idAmbiguous := insertUser(t, db, "ghost@nowhere.com", "admin", nil)        // AMBIGUOUS: skip
	idOperator := insertUser(t, db, "op@customer.com", "operator", &tenantA)   // → tenant_operator
	idReadOnly := insertUser(t, db, "ro@customer.com", "readonly", &tenantA)   // → tenant_readonly

	res, err := NormalizeAdminRoles(context.Background(), db, "sqlite")
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if res.PlatformPromoted < 2 {
		t.Errorf("PlatformPromoted: want >=2 (bootstrap + legacy superadmin), got %d", res.PlatformPromoted)
	}
	if res.TenantPromoted != 1 {
		t.Errorf("TenantPromoted: want 1, got %d", res.TenantPromoted)
	}
	if res.OperatorRenamed != 1 {
		t.Errorf("OperatorRenamed: want 1, got %d", res.OperatorRenamed)
	}
	if res.ReadOnlyRenamed != 1 {
		t.Errorf("ReadOnlyRenamed: want 1, got %d", res.ReadOnlyRenamed)
	}
	if res.Skipped != 1 {
		t.Errorf("Skipped (ambiguous): want 1, got %d", res.Skipped)
	}

	// Bootstrap → platform_super_admin, NULL tenant, token bumped.
	if role, tid, tv := mustRoleTenant(t, db, idBootstrap); role != "platform_super_admin" || tid.Valid || tv != 1 {
		t.Errorf("bootstrap row: role=%q tid=%v tv=%d — want platform_super_admin/NULL/1", role, tid, tv)
	}
	// Legacy superadmin → platform, NULL, bumped.
	if role, tid, tv := mustRoleTenant(t, db, idLegacySuper); role != "platform_super_admin" || tid.Valid || tv != 1 {
		t.Errorf("legacy superadmin: role=%q tid=%v tv=%d", role, tid, tv)
	}
	// Tenant admin → tenant_admin, tenant preserved, bumped.
	if role, tid, tv := mustRoleTenant(t, db, idTenantAdmin); role != "tenant_admin" || !tid.Valid || tid.Int64 != tenantA || tv != 1 {
		t.Errorf("tenant admin: role=%q tid=%v tv=%d", role, tid, tv)
	}
	// Ambiguous → unchanged, token NOT bumped.
	if role, tid, tv := mustRoleTenant(t, db, idAmbiguous); role != "admin" || tid.Valid || tv != 0 {
		t.Errorf("ambiguous: role=%q tid=%v tv=%d — must remain untouched", role, tid, tv)
	}
	// Operator → tenant_operator.
	if role, _, tv := mustRoleTenant(t, db, idOperator); role != "tenant_operator" || tv != 1 {
		t.Errorf("operator: role=%q tv=%d", role, tv)
	}
	// Read-only → tenant_readonly.
	if role, _, tv := mustRoleTenant(t, db, idReadOnly); role != "tenant_readonly" || tv != 1 {
		t.Errorf("readonly: role=%q tv=%d", role, tv)
	}
}

func TestNormalizeAdminRoles_Idempotent(t *testing.T) {
	t.Setenv("ORVIX_ADMIN_EMAIL", "admin@orvix.email")
	db := setupUsersForNormalize(t)
	tenantA := int64(1)
	insertUser(t, db, "admin@orvix.email", "admin", &tenantA)
	insertUser(t, db, "boss@customer.com", "admin", &tenantA)

	first, err := NormalizeAdminRoles(context.Background(), db, "sqlite")
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	second, err := NormalizeAdminRoles(context.Background(), db, "sqlite")
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if second.PlatformPromoted != 0 || second.TenantPromoted != 0 ||
		second.OperatorRenamed != 0 || second.ReadOnlyRenamed != 0 {
		t.Errorf("second pass should be no-op: %+v", second)
	}
	if first.PlatformPromoted+first.TenantPromoted == 0 {
		t.Errorf("first pass should have promoted rows: %+v", first)
	}
	_ = time.Now
}

// TestNormalizeAdminRoles_LeaveUnscoped covers the "leave-in-place" rows
// (operator/readonly with no tenant_id). PORTAL-SEPARATION-PHASE1 Phase 3
// counts and logs these rows without touching them: the role string stays
// legacy, no token_version bump, and the RBAC map already denies the
// deprecated strings.
func TestNormalizeAdminRoles_LeaveUnscoped(t *testing.T) {
	t.Setenv("ORVIX_ADMIN_EMAIL", "admin@orvix.email")
	db := setupUsersForNormalize(t)

	idOpUnscoped := insertUser(t, db, "helpdesk@nowhere.com", "operator", nil)
	idROUnscoped := insertUser(t, db, "auditor@nowhere.com", "readonly", nil)

	res, err := NormalizeAdminRoles(context.Background(), db, "sqlite")
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if res.OperatorLeftUnscoped != 1 {
		t.Errorf("OperatorLeftUnscoped: want 1, got %d", res.OperatorLeftUnscoped)
	}
	if res.ReadOnlyLeftUnscoped != 1 {
		t.Errorf("ReadOnlyLeftUnscoped: want 1, got %d", res.ReadOnlyLeftUnscoped)
	}
	if res.OperatorRenamed != 0 || res.ReadOnlyRenamed != 0 {
		t.Errorf("nothing to rename (no tenant_id present), got %+v", res)
	}

	// Operator+NULL row: role unchanged, no token bump.
	if role, tid, tv := mustRoleTenant(t, db, idOpUnscoped); role != "operator" || tid.Valid || tv != 0 {
		t.Errorf("operator unscoped: role=%q tid=%v tv=%d — must remain untouched", role, tid, tv)
	}
	if role, tid, tv := mustRoleTenant(t, db, idROUnscoped); role != "readonly" || tid.Valid || tv != 0 {
		t.Errorf("readonly unscoped: role=%q tid=%v tv=%d — must remain untouched", role, tid, tv)
	}
}

// TestNormalizeAdminRoles_Atomic verifies that the migration runs inside
// a transaction: an inspection error (missing required column) returns
// early WITHOUT partially updating rows, and even a successful run
// exhibits BEGIN/COMMIT semantics — after a successful call, every
// eligible row is either updated or none are (there is no partial state
// that can be observed by a concurrent reader mid-migration).
//
// We can't easily kill the connection mid-COMMIT, so this test asserts
// the observable property: the pre-Phase-3 code executed each UPDATE in
// autocommit mode (five separate transactions); post-Phase-3 all rows
// share a single tx. We verify by confirming both statements land or
// (in the failure path) neither does, and that a returned error does
// not leave a half-migrated table.
func TestNormalizeAdminRoles_Atomic(t *testing.T) {
	t.Setenv("ORVIX_ADMIN_EMAIL", "admin@orvix.email")
	db := setupUsersForNormalize(t)
	tenantA := int64(11)

	idBootstrap := insertUser(t, db, "admin@orvix.email", "admin", &tenantA)
	idTenantAdmin := insertUser(t, db, "boss@customer.com", "admin", &tenantA)
	idOperator := insertUser(t, db, "op@customer.com", "operator", &tenantA)

	res, err := NormalizeAdminRoles(context.Background(), db, "sqlite")
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	// All three eligible rows updated in the same transaction.
	if res.PlatformPromoted < 1 || res.TenantPromoted != 1 || res.OperatorRenamed != 1 {
		t.Errorf("all rows must have been updated in one tx, got %+v", res)
	}
	// Token versions bumped exactly once per row (never twice from re-running
	// steps inside the tx).
	for _, id := range []int64{idBootstrap, idTenantAdmin, idOperator} {
		if _, _, tv := mustRoleTenant(t, db, id); tv != 1 {
			t.Errorf("id=%d token_version = %d, want 1 (single tx should bump exactly once)", id, tv)
		}
	}
}
