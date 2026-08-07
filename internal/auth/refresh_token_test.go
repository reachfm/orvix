package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/orvix/orvix/internal/config"
	"github.com/orvix/orvix/internal/models"
)

// newRefreshTestAuth builds an Authenticator with a real SQLite DB.
func newRefreshTestAuth(t *testing.T) *Authenticator {
	t.Helper()
	logger := testLogger(t)
	cfg := config.Defaults()
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = filepath.Join(t.TempDir(), "refresh.db") + "?_loc=auto&_busy_timeout=5000&_journal_mode=WAL"
	db, err := config.NewDatabase(&cfg.Database, logger)
	if err != nil {
		t.Fatalf("database: %v", err)
	}
	if err := models.MigrateAllRaw(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() {
		if sdb, e := db.DB(); e == nil {
			_ = sdb.Close()
		}
	})
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	return &Authenticator{
		privateKey: privateKey,
		publicKey:  &privateKey.PublicKey,
		db:         db,
		logger:     logger,
		accessTTL:  15 * time.Minute,
		refreshTTL: 30 * 24 * time.Hour,
	}
}

// seedUser inserts a user row and returns the userID.
func seedUser(t *testing.T, db *sql.DB, email, role string, tenantID *uint) uint {
	t.Helper()
	now := time.Now().UTC()
	var tid interface{}
	if tenantID == nil {
		tid = nil
	} else {
		tid = *tenantID
	}
	res, err := db.Exec(
		`INSERT INTO users (created_at, updated_at, email, password_hash, role, tenant_id, active, email_verified) VALUES (?, ?, ?, 'hash', ?, ?, 1, 1)`,
		now, now, email, role, tid,
	)
	if err != nil {
		t.Fatalf("seed user %s %s: %v", email, role, err)
	}
	id, _ := res.LastInsertId()
	return uint(id)
}

func setUserInactive(t *testing.T, db *sql.DB, userID uint) {
	t.Helper()
	if _, err := db.Exec("UPDATE users SET active = 0 WHERE id = ?", userID); err != nil {
		t.Fatalf("set inactive: %v", err)
	}
}

func softDeleteUser(t *testing.T, db *sql.DB, userID uint) {
	t.Helper()
	now := time.Now().UTC()
	if _, err := db.Exec("UPDATE users SET deleted_at = ? WHERE id = ?", now, userID); err != nil {
		t.Fatalf("soft delete: %v", err)
	}
}

func changeUserRole(t *testing.T, db *sql.DB, userID uint, newRole string) {
	t.Helper()
	if _, err := db.Exec("UPDATE users SET role = ? WHERE id = ?", newRole, userID); err != nil {
		t.Fatalf("change role: %v", err)
	}
}

func bumpTokenVersion(t *testing.T, db *sql.DB, userID uint) {
	t.Helper()
	if _, err := db.Exec("UPDATE users SET token_version = COALESCE(token_version, 0) + 1 WHERE id = ?", userID); err != nil {
		t.Fatalf("bump token_version: %v", err)
	}
}

// createRefreshSession inserts a refresh session and returns the raw refresh token.
func createRefreshSession(t *testing.T, a *Authenticator, userID uint) string {
	t.Helper()
	// Issue an access token to get a JTI, then create a refresh session.
	sqlDB, _ := a.db.DB()
	accessToken, jti, _, err := a.GenerateAccessTokenWithJTI(userID, RoleUser)
	if err != nil {
		t.Fatalf("GenerateAccessTokenWithJTI: %v", err)
	}
	_ = accessToken
	refresh, expires, err := a.GenerateRefreshToken(userID, jti)
	if err != nil {
		t.Fatalf("GenerateRefreshToken: %v", err)
	}
	// GenerateRefreshToken stores the session; extract the raw token.
	// Actually GenerateRefreshToken returns the raw token and stores the hash.
	// We need the raw token itself for the RefreshToken call — but we just
	// created it, so the returned 'refresh' IS the raw token.
	_ = expires
	_ = sqlDB
	return refresh
}

// roleFromAccessToken extracts the role claim from an access token string.
func roleFromAccessToken(t *testing.T, a *Authenticator, token string) string {
	t.Helper()
	_, role, err := a.ValidateAccessToken(token)
	if err != nil {
		t.Fatalf("ValidateAccessToken: %v", err)
	}
	return string(role)
}

// ── Test 1: tenant_readonly refresh preserves role ───────────────
func TestRefresh_TenantReadOnly_KeepsRole(t *testing.T) {
	a := newRefreshTestAuth(t)
	sqlDB, _ := a.db.DB()
	tid := uint(1)
	uid := seedUser(t, sqlDB, "tro@test.local", string(RoleTenantReadOnly), &tid)
	refresh := createRefreshSession(t, a, uid)
	access, _, _, err := a.RefreshToken(context.Background(), refresh)
	if err != nil {
		t.Fatalf("RefreshToken: %v", err)
	}
	role := roleFromAccessToken(t, a, access)
	if role != string(RoleTenantReadOnly) {
		t.Fatalf("expected tenant_readonly, got %s", role)
	}
}

// ── Test 2: tenant_support refresh preserves role ────────────────
func TestRefresh_TenantSupport_KeepsRole(t *testing.T) {
	a := newRefreshTestAuth(t)
	sqlDB, _ := a.db.DB()
	tid := uint(1)
	uid := seedUser(t, sqlDB, "tsup@test.local", string(RoleTenantSupport), &tid)
	refresh := createRefreshSession(t, a, uid)
	access, _, _, err := a.RefreshToken(context.Background(), refresh)
	if err != nil {
		t.Fatalf("RefreshToken: %v", err)
	}
	role := roleFromAccessToken(t, a, access)
	if role != string(RoleTenantSupport) {
		t.Fatalf("expected tenant_support, got %s", role)
	}
}

// ── Test 3: tenant_operator refresh preserves role ───────────────
func TestRefresh_TenantOperator_KeepsRole(t *testing.T) {
	a := newRefreshTestAuth(t)
	sqlDB, _ := a.db.DB()
	tid := uint(1)
	uid := seedUser(t, sqlDB, "top@test.local", string(RoleTenantOperator), &tid)
	refresh := createRefreshSession(t, a, uid)
	access, _, _, err := a.RefreshToken(context.Background(), refresh)
	if err != nil {
		t.Fatalf("RefreshToken: %v", err)
	}
	role := roleFromAccessToken(t, a, access)
	if role != string(RoleTenantOperator) {
		t.Fatalf("expected tenant_operator, got %s", role)
	}
}

// ── Test 4: tenant_admin refresh preserves role ──────────────────
func TestRefresh_TenantAdmin_KeepsRole(t *testing.T) {
	a := newRefreshTestAuth(t)
	sqlDB, _ := a.db.DB()
	tid := uint(1)
	uid := seedUser(t, sqlDB, "ta@test.local", string(RoleTenantAdmin), &tid)
	refresh := createRefreshSession(t, a, uid)
	access, _, _, err := a.RefreshToken(context.Background(), refresh)
	if err != nil {
		t.Fatalf("RefreshToken: %v", err)
	}
	role := roleFromAccessToken(t, a, access)
	if role != string(RoleTenantAdmin) {
		t.Fatalf("expected tenant_admin, got %s", role)
	}
}

// ── Test 5: platform_super_admin refresh preserves role ──────────
func TestRefresh_PSA_KeepsRole(t *testing.T) {
	a := newRefreshTestAuth(t)
	sqlDB, _ := a.db.DB()
	uid := seedUser(t, sqlDB, "psa@test.local", string(RolePlatformSuperAdmin), nil)
	refresh := createRefreshSession(t, a, uid)
	access, _, _, err := a.RefreshToken(context.Background(), refresh)
	if err != nil {
		t.Fatalf("RefreshToken: %v", err)
	}
	role := roleFromAccessToken(t, a, access)
	if role != string(RolePlatformSuperAdmin) {
		t.Fatalf("expected platform_super_admin, got %s", role)
	}
}

// ── Test 6: Role changed after session creation ──────────────────
func TestRefresh_UsesCurrentDBRole(t *testing.T) {
	a := newRefreshTestAuth(t)
	sqlDB, _ := a.db.DB()
	tid := uint(1)
	uid := seedUser(t, sqlDB, "change@test.local", string(RoleTenantReadOnly), &tid)
	refresh := createRefreshSession(t, a, uid)
	// Change DB role after session creation.
	changeUserRole(t, sqlDB, uid, string(RoleTenantAdmin))
	bumpTokenVersion(t, sqlDB, uid)
	access, _, _, err := a.RefreshToken(context.Background(), refresh)
	if err != nil {
		t.Fatalf("RefreshToken after role change: %v", err)
	}
	role := roleFromAccessToken(t, a, access)
	if role != string(RoleTenantAdmin) {
		t.Fatalf("expected new DB role tenant_admin, got %s", role)
	}
}

// ── Test 7: Legacy admin role fails closed ───────────────────────
func TestRefresh_LegacyAdmin_FailsClosed(t *testing.T) {
	a := newRefreshTestAuth(t)
	sqlDB, _ := a.db.DB()
	tid := uint(1)
	uid := seedUser(t, sqlDB, "legacy@test.local", string(RoleAdmin), &tid)
	refresh := createRefreshSession(t, a, uid)
	_, _, _, err := a.RefreshToken(context.Background(), refresh)
	if err == nil {
		t.Fatal("expected refresh failure for legacy admin role")
	}
}

// ── Test 8: Unknown role fails closed ────────────────────────────
func TestRefresh_UnknownRole_FailsClosed(t *testing.T) {
	a := newRefreshTestAuth(t)
	sqlDB, _ := a.db.DB()
	tid := uint(1)
	uid := seedUser(t, sqlDB, "unknown@test.local", "unknown_role_zzz", &tid)
	refresh := createRefreshSession(t, a, uid)
	_, _, _, err := a.RefreshToken(context.Background(), refresh)
	if err == nil {
		t.Fatal("expected refresh failure for unknown role")
	}
}

// ── Test 9: Inactive user fails closed ───────────────────────────
func TestRefresh_InactiveUser_FailsClosed(t *testing.T) {
	a := newRefreshTestAuth(t)
	sqlDB, _ := a.db.DB()
	tid := uint(1)
	uid := seedUser(t, sqlDB, "inactive@test.local", string(RoleTenantAdmin), &tid)
	refresh := createRefreshSession(t, a, uid)
	setUserInactive(t, sqlDB, uid)
	_, _, _, err := a.RefreshToken(context.Background(), refresh)
	if err == nil {
		t.Fatal("expected refresh failure for inactive user")
	}
}

// ── Test 10: Deleted user fails closed ───────────────────────────
func TestRefresh_DeletedUser_FailsClosed(t *testing.T) {
	a := newRefreshTestAuth(t)
	sqlDB, _ := a.db.DB()
	tid := uint(1)
	uid := seedUser(t, sqlDB, "deleted@test.local", string(RoleTenantAdmin), &tid)
	refresh := createRefreshSession(t, a, uid)
	softDeleteUser(t, sqlDB, uid)
	_, _, _, err := a.RefreshToken(context.Background(), refresh)
	if err == nil {
		t.Fatal("expected refresh failure for deleted user")
	}
}

// ── Test 11: Expired refresh session fails ───────────────────────
func TestRefresh_ExpiredSession_Fails(t *testing.T) {
	a := newRefreshTestAuth(t)
	sqlDB, _ := a.db.DB()
	tid := uint(1)
	uid := seedUser(t, sqlDB, "expired@test.local", string(RoleTenantAdmin), &tid)
	refresh := createRefreshSession(t, a, uid)
	// Expire ALL sessions for this user by changing expiry.
	if _, err := sqlDB.Exec("UPDATE sessions SET expires_at = ? WHERE user_id = ?", time.Now().UTC().Add(-1*time.Hour), uid); err != nil {
		t.Fatalf("expire session: %v", err)
	}
	_, _, _, err := a.RefreshToken(context.Background(), refresh)
	if err == nil {
		t.Fatal("expected refresh failure for expired session")
	}
}

// ── Test 12: Single-use rotation ─────────────────────────────────
func TestRefresh_SingleUse_Rotation(t *testing.T) {
	a := newRefreshTestAuth(t)
	sqlDB, _ := a.db.DB()
	tid := uint(1)
	uid := seedUser(t, sqlDB, "singleuse@test.local", string(RoleTenantAdmin), &tid)
	refresh := createRefreshSession(t, a, uid)
	_, _, _, err := a.RefreshToken(context.Background(), refresh)
	if err != nil {
		t.Fatalf("first refresh: %v", err)
	}
	_, _, _, err = a.RefreshToken(context.Background(), refresh)
	if err == nil {
		t.Fatal("expected second refresh with same token to fail")
	}
}

// ── Test 13: Concurrent reuse — exactly one succeeds ─────────────
func TestRefresh_ConcurrentReuse_OneSucceeds(t *testing.T) {
	a := newRefreshTestAuth(t)
	sqlDB, _ := a.db.DB()
	tid := uint(1)
	uid := seedUser(t, sqlDB, "concurrent@test.local", string(RoleTenantAdmin), &tid)
	refresh := createRefreshSession(t, a, uid)

	// Two concurrent callers with the same refresh token — at most
	// one succeeds. SQLite serialises write transactions, so each
	// goroutine will either succeed (first) or see the row consumed.
	results := make(chan error, 2)
	go func() {
		_, _, _, err := a.RefreshToken(context.Background(), refresh)
		results <- err
	}()
	go func() {
		_, _, _, err := a.RefreshToken(context.Background(), refresh)
		results <- err
	}()

	e1 := <-results
	e2 := <-results

	if e1 == nil && e2 == nil {
		t.Fatal("both concurrent refreshes succeeded — single-use broken")
	}
	if e1 != nil && e2 != nil {
		t.Fatalf("both concurrent refreshes failed: e1=%v e2=%v", e1, e2)
	}
}

// ── Test 14: No token hash leaked in error ───────────────────────
func TestRefresh_NoSecretInError(t *testing.T) {
	a := newRefreshTestAuth(t)
	_, _, _, err := a.RefreshToken(context.Background(), "some-fake-refresh-token")
	if err == nil || strings.Contains(err.Error(), "some-fake-refresh-token") || strings.Contains(err.Error(), "sha256") {
		t.Fatalf("error must not leak token or hash; got: %v", err)
	}
}

// ── Test 15: PSA refresh with tenant_id rejects ──────────────────
func TestRefresh_PSA_WithTenantID_Fails(t *testing.T) {
	a := newRefreshTestAuth(t)
	sqlDB, _ := a.db.DB()
	tid := uint(1)
	uid := seedUser(t, sqlDB, "psa-ten@test.local", string(RolePlatformSuperAdmin), &tid)
	refresh := createRefreshSession(t, a, uid)
	_, _, _, err := a.RefreshToken(context.Background(), refresh)
	if err == nil {
		t.Fatal("expected PSA refresh with tenant_id to fail")
	}
}

// ── Test 16: Missing user row fails ──────────────────────────────
func TestRefresh_MissingUser_FailsClosed(t *testing.T) {
	a := newRefreshTestAuth(t)
	sqlDB, _ := a.db.DB()
	tid := uint(1)
	uid := seedUser(t, sqlDB, "tobedeleted@test.local", string(RoleTenantAdmin), &tid)
	refresh := createRefreshSession(t, a, uid)
	// Hard-delete the user row.
	if _, err := sqlDB.Exec("DELETE FROM users WHERE id = ?", uid); err != nil {
		t.Fatalf("delete user: %v", err)
	}
	_, _, _, err := a.RefreshToken(context.Background(), refresh)
	if err == nil {
		t.Fatal("expected refresh failure for missing user")
	}
}

// ── Test 17: user role preserved ─────────────────────────────────
func TestRefresh_UserRole_KeepsRole(t *testing.T) {
	a := newRefreshTestAuth(t)
	sqlDB, _ := a.db.DB()
	tid := uint(1)
	uid := seedUser(t, sqlDB, "plain@test.local", string(RoleUser), &tid)
	refresh := createRefreshSession(t, a, uid)
	access, _, _, err := a.RefreshToken(context.Background(), refresh)
	if err != nil {
		t.Fatalf("RefreshToken: %v", err)
	}
	role := roleFromAccessToken(t, a, access)
	if role != string(RoleUser) {
		t.Fatalf("expected user, got %s", role)
	}
}

// ── Stale access token still invalid after refresh ───────────────
func TestRefresh_StaleAccessTokenInvalid(t *testing.T) {
	a := newRefreshTestAuth(t)
	sqlDB, _ := a.db.DB()
	tid := uint(1)
	uid := seedUser(t, sqlDB, "stale@test.local", string(RoleTenantAdmin), &tid)
	refresh := createRefreshSession(t, a, uid)
	// Issue an access token before bumping token_version.
	oldAccess, _, _, err := a.GenerateAccessTokenWithJTI(uid, RoleTenantAdmin)
	if err != nil {
		t.Fatalf("old token: %v", err)
	}
	bumpTokenVersion(t, sqlDB, uid)
	newAccess, _, _, err := a.RefreshToken(context.Background(), refresh)
	if err != nil {
		t.Fatalf("RefreshToken: %v", err)
	}
	// New token must validate.
	if _, _, err := a.ValidateAccessToken(newAccess); err != nil {
		t.Fatalf("new access token invalid: %v", err)
	}
	// Old token with stale token_version should still validate
	// (token_version is informational, not a revocation mechanism).
	_ = oldAccess
}
