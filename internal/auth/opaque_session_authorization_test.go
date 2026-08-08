package auth

import (
	"crypto/rand"
	"crypto/rsa"
	"database/sql"
	"fmt"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/orvix/orvix/internal/config"
	"github.com/orvix/orvix/internal/models"
)

var opaqueSeedCounter int64

func newOpaqueTestAuth(t *testing.T) (*Authenticator, *sql.DB) {
	t.Helper()
	logger := testLogger(t)
	cfg := config.Defaults()
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = filepath.Join(t.TempDir(), "opaque.db") + "?_loc=auto&_busy_timeout=5000&_journal_mode=WAL"
	cfg.Auth.JWTKeyPath = filepath.Join(t.TempDir(), "jwt.pem")

	gdb, err := config.NewDatabase(&cfg.Database, logger)
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	if err := models.MigrateAllRaw(gdb); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	sqlDB, _ := gdb.DB()
	t.Cleanup(func() { sqlDB.Close() })

	privateKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	return &Authenticator{privateKey: privateKey, publicKey: &privateKey.PublicKey, db: gdb, logger: logger, accessTTL: 15 * time.Minute, refreshTTL: 30 * 24 * time.Hour}, sqlDB
}

func seedOpaqueUser(t *testing.T, db *sql.DB, role string, tenantID interface{}, active bool, deleted bool) uint {
	t.Helper()
	now := time.Now().UTC()
	del := sql.NullTime{}
	if deleted {
		del = sql.NullTime{Time: now, Valid: true}
	}
	tid := sql.NullInt64{}
	if tenantID != nil {
		tid = sql.NullInt64{Int64: int64(tenantID.(uint)), Valid: true}
	}
	atomic.AddInt64(&opaqueSeedCounter, 1)
	email := fmt.Sprintf("%s-%d@test.local", role, opaqueSeedCounter)
	res, err := db.Exec(
		`INSERT INTO users (created_at, updated_at, email, password_hash, role, tenant_id, active, email_verified, deleted_at, token_version) VALUES (?, ?, ?, ?, ?, ?, ?, 1, ?, 0)`,
		now, now, email, "h", role, tid, active, del,
	)
	if err != nil {
		t.Fatalf("seed user %s: %v", role, err)
	}
	id, _ := res.LastInsertId()
	return uint(id)
}

func TestOpaqueSessionUsesCurrentDBRole(t *testing.T) {
	a, db := newOpaqueTestAuth(t)
	uid := seedOpaqueUser(t, db, "tenant_support", uint(1), true, false)
	// Create session with stale role stored.
	tok, err := a.GenerateOpaqueSession(uid, "tenant_admin", "x@test.local")
	if err != nil {
		t.Fatalf("generate session: %v", err)
	}
	gotUID, gotRole, _, err := a.ValidateOpaqueSession(tok)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if gotUID != uid || string(gotRole) != "tenant_support" {
		t.Fatalf("want uid=%d role=tenant_support, got uid=%d role=%s", uid, gotUID, gotRole)
	}
}

func TestOpaqueSessionRoleChangeAfterCreation(t *testing.T) {
	a, db := newOpaqueTestAuth(t)
	uid := seedOpaqueUser(t, db, "tenant_admin", uint(1), true, false)
	tok, err := a.GenerateOpaqueSession(uid, "tenant_admin", "x@test.local")
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	// Change role.
	if _, err := db.Exec("UPDATE users SET role='tenant_readonly', token_version=token_version+1 WHERE id=?", uid); err != nil {
		t.Fatalf("update: %v", err)
	}
	_, gotRole, _, err := a.ValidateOpaqueSession(tok)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if string(gotRole) != "tenant_readonly" {
		t.Fatalf("want tenant_readonly, got %s", gotRole)
	}
}

func TestOpaqueSessionInactiveUserFails(t *testing.T) {
	a, db := newOpaqueTestAuth(t)
	uid := seedOpaqueUser(t, db, "tenant_admin", uint(1), true, false)
	tok, err := a.GenerateOpaqueSession(uid, "tenant_admin", "x@test.local")
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if _, err := db.Exec("UPDATE users SET active=0, token_version=token_version+1 WHERE id=?", uid); err != nil {
		t.Fatalf("update: %v", err)
	}
	gotUID, gotRole, _, err := a.ValidateOpaqueSession(tok)
	if err == nil {
		t.Fatalf("expected error, got uid=%d role=%s", gotUID, gotRole)
	}
	if gotUID != 0 || gotRole != "" {
		t.Fatalf("expected zero values, got uid=%d role=%s", gotUID, gotRole)
	}
}

func TestOpaqueSessionDeletedUserFails(t *testing.T) {
	a, db := newOpaqueTestAuth(t)
	uid := seedOpaqueUser(t, db, "tenant_admin", uint(1), true, false)
	tok, err := a.GenerateOpaqueSession(uid, "tenant_admin", "x@test.local")
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	now := time.Now().UTC()
	if _, err := db.Exec("UPDATE users SET active=0, deleted_at=?, token_version=token_version+1 WHERE id=?", now, uid); err != nil {
		t.Fatalf("update: %v", err)
	}
	gotUID, gotRole, _, err := a.ValidateOpaqueSession(tok)
	if err == nil {
		t.Fatalf("expected error, got uid=%d role=%s", gotUID, gotRole)
	}
	if gotUID != 0 || gotRole != "" {
		t.Fatalf("expected zero values, got uid=%d role=%s", gotUID, gotRole)
	}
}

func TestOpaqueSessionMissingUserFails(t *testing.T) {
	a, db := newOpaqueTestAuth(t)
	uid := seedOpaqueUser(t, db, "tenant_admin", uint(1), true, false)
	tok, err := a.GenerateOpaqueSession(uid, "tenant_admin", "x@test.local")
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if _, err := db.Exec("DELETE FROM users WHERE id=?", uid); err != nil {
		t.Fatalf("delete: %v", err)
	}
	gotUID, gotRole, _, err := a.ValidateOpaqueSession(tok)
	if err == nil {
		t.Fatalf("expected error, got uid=%d role=%s", gotUID, gotRole)
	}
	if gotUID != 0 || gotRole != "" {
		t.Fatalf("expected zero values, got uid=%d role=%s", gotUID, gotRole)
	}
}

func TestOpaqueSessionDeprecatedAndUnknownRolesFail(t *testing.T) {
	a, db := newOpaqueTestAuth(t)
	deprecated := []string{"admin", "operator", "readonly", "superadmin", "super_admin", "super-admin", "nonexistent_role"}
	for _, role := range deprecated {
		uid := seedOpaqueUser(t, db, role, uint(1), true, false)
		tok, err := a.GenerateOpaqueSession(uid, Role(role), "x@test.local")
		if err != nil {
			t.Fatalf("generate session for %q: %v", role, err)
		}
		gotUID, gotRole, _, err := a.ValidateOpaqueSession(tok)
		if err == nil {
			t.Fatalf("role %q: expected failure, got uid=%d role=%s", role, gotUID, gotRole)
		}
		if gotUID != 0 || gotRole != "" {
			t.Fatalf("role %q: expected zero values, got uid=%d role=%s", role, gotUID, gotRole)
		}
	}
}

func TestOpaqueSessionTenantBinding(t *testing.T) {
	a, db := newOpaqueTestAuth(t)
	// PSA NULL tenant succeeds.
	uidPSA := seedOpaqueUser(t, db, "platform_super_admin", nil, true, false)
	tokPSA, err := a.GenerateOpaqueSession(uidPSA, "platform_super_admin", "psa-null@test.local")
	if err != nil {
		t.Fatalf("generate PSA: %v", err)
	}
	gotUID, gotRole, _, err := a.ValidateOpaqueSession(tokPSA)
	if err != nil || gotUID != uidPSA || string(gotRole) != "platform_super_admin" {
		t.Fatalf("PSA NULL: want uid=%d role=platform_super_admin, got uid=%d role=%s err=%v", uidPSA, gotUID, gotRole, err)
	}

	// PSA with tenant fails.
	uidPSATenant := seedOpaqueUser(t, db, "platform_super_admin", uint(1), true, false)
	tokPSATenant, err := a.GenerateOpaqueSession(uidPSATenant, "platform_super_admin", "psa-tenant@test.local")
	if err != nil {
		t.Fatalf("generate PSA tenant: %v", err)
	}
	gotUID, gotRole, _, err = a.ValidateOpaqueSession(tokPSATenant)
	if err == nil || gotUID != 0 || gotRole != "" {
		t.Fatalf("PSA tenant: want failure, got uid=%d role=%s err=%v", gotUID, gotRole, err)
	}

	// Tenant roles with valid tenant succeed.
	for _, role := range []string{"tenant_admin", "tenant_operator", "tenant_support", "tenant_readonly", "user", "billing"} {
		uid := seedOpaqueUser(t, db, role, uint(1), true, false)
		tok, err := a.GenerateOpaqueSession(uid, Role(role), role+"@test.local")
		if err != nil {
			t.Fatalf("generate %s: %v", role, err)
		}
		gotUID, gotRole, _, err := a.ValidateOpaqueSession(tok)
		if err != nil || gotUID != uid || string(gotRole) != role {
			t.Fatalf("%s: want uid=%d role=%s, got uid=%d role=%s err=%v", role, uid, role, gotUID, gotRole, err)
		}
	}

	// Tenant roles with NULL tenant fail.
	for _, role := range []string{"tenant_admin", "tenant_operator", "tenant_support", "tenant_readonly", "user", "billing"} {
		uid := seedOpaqueUser(t, db, role, nil, true, false)
		tok, err := a.GenerateOpaqueSession(uid, Role(role), role+"null@test.local")
		if err != nil {
			t.Fatalf("generate %s null: %v", role, err)
		}
		gotUID, gotRole, _, err := a.ValidateOpaqueSession(tok)
		if err == nil || gotUID != 0 || gotRole != "" {
			t.Fatalf("%s null: want failure, got uid=%d role=%s err=%v", role, gotUID, gotRole, err)
		}
	}
}
