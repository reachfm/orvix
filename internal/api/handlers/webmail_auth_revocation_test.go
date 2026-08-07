package handlers

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/orvix/orvix/internal/auth"
	"github.com/orvix/orvix/internal/config"
	"github.com/orvix/orvix/internal/dbdialect"
	"github.com/orvix/orvix/internal/models"
)

func newWebmailRevocationAuth(t *testing.T) (*sql.DB, *auth.Authenticator) {
	t.Helper()
	logger, _ := zap.NewDevelopment()
	cfg := config.Defaults()
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = filepath.Join(t.TempDir(), "webmailrev.db") + "?_loc=auto&_busy_timeout=5000&_journal_mode=WAL"
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

	authn, err := auth.NewAuthenticator(&cfg.Auth, gdb, logger)
	if err != nil {
		t.Fatalf("authn: %v", err)
	}
	return sqlDB, authn
}

func TestWebmailGetOrCreateUserRoleChangeBumpsVersionAndRevokesToken(t *testing.T) {
	sqlDB, authn := newWebmailRevocationAuth(t)

	now := time.Now().UTC()
	// Create tenant.
	sqlDB.Exec(`INSERT INTO tenants (name, slug, domain, plan, active, created_at, updated_at) VALUES ('a', 'a', 'a.example', 'enterprise', 1, ?, ?)`, now, now)

	// Seed existing webmail user: role=tenant_support, token_version=300.
	const email = "webmail-role-change@example.test"
	sqlDB.Exec(`INSERT INTO users (created_at, updated_at, email, password_hash, role, tenant_id, active, email_verified, token_version) VALUES (?, ?, ?, ?, 'tenant_support', 1, 1, 1, 300)`, now.Format("2006-01-02 15:04:05"), now.Format("2006-01-02 15:04:05"), email, "hash")

	// Get the user ID.
	var userID uint
	if err := sqlDB.QueryRow("SELECT id FROM users WHERE email = ?", email).Scan(&userID); err != nil {
		t.Fatalf("seed user not found: %v", err)
	}

	// Issue old access token and prove it validates.
	oldAccess, _, _, err := authn.GenerateAccessTokenWithJTI(userID, auth.RoleTenantSupport)
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}
	uid, role, valErr := authn.ValidateAccessToken(oldAccess)
	if valErr != nil || uid != userID || string(role) != "tenant_support" {
		t.Fatalf("old token before mutation: err=%v uid=%d role=%s", valErr, uid, role)
	}

	// Count users before.
	var countBefore int
	sqlDB.QueryRow("SELECT COUNT(*) FROM users WHERE email = ?", email).Scan(&countBefore)

	// Invoke real ensureWebmailUser. isAdmin=true → desired role=tenant_admin.
	dial := dbdialect.FromDriver("sqlite")
	h := &Handler{}
	returnedID, callErr := h.ensureWebmailUser(dial, sqlDB, email, true)
	if callErr != nil {
		t.Fatalf("ensureWebmailUser: %v", callErr)
	}
	if returnedID != userID {
		t.Fatalf("returned ID: want %d, got %d", userID, returnedID)
	}

	// Count users after.
	var countAfter int
	sqlDB.QueryRow("SELECT COUNT(*) FROM users WHERE email = ?", email).Scan(&countAfter)
	if countAfter != countBefore || countAfter != 1 {
		t.Fatalf("user count: before=%d after=%d", countBefore, countAfter)
	}

	// Query user state.
	var storedRole string
	var storedTV int64
	var storedActive bool
	var storedDeleted sql.NullTime
	var storedTenant uint
	sqlDB.QueryRow("SELECT role, COALESCE(token_version,0), active, deleted_at, tenant_id FROM users WHERE id = ?", userID).Scan(&storedRole, &storedTV, &storedActive, &storedDeleted, &storedTenant)
	if storedRole != "tenant_admin" {
		t.Fatalf("stored role: want tenant_admin, got %s", storedRole)
	}
	if storedTV != 301 {
		t.Fatalf("token_version: want 301, got %d", storedTV)
	}
	if !storedActive {
		t.Fatal("active must be true")
	}
	if storedDeleted.Valid {
		t.Fatal("deleted_at must be NULL")
	}
	if storedTenant != 1 {
		t.Fatalf("tenant_id: want 1, got %d", storedTenant)
	}

	// Old token must be rejected.
	uid2, role2, err2 := authn.ValidateAccessToken(oldAccess)
	if err2 == nil || uid2 != 0 || role2 != "" {
		t.Fatalf("old token after mutation: err=%v uid=%d role=%s", err2, uid2, role2)
	}

	// Error must not leak secrets.
	msg := err2.Error()
	for _, banned := range []string{email, "eyJ", "sqlite", "DSN", "sha256"} {
		if strings.Contains(strings.ToLower(msg), strings.ToLower(banned)) {
			t.Errorf("error leaked %q: %s", banned, msg)
		}
	}
}

func TestWebmailGetOrCreateUserNoOpDoesNotBumpVersion(t *testing.T) {
	sqlDB, authn := newWebmailRevocationAuth(t)

	now := time.Now().UTC()
	sqlDB.Exec(`INSERT INTO tenants (name, slug, domain, plan, active, created_at, updated_at) VALUES ('a', 'a', 'a.example', 'enterprise', 1, ?, ?)`, now, now)

	// Seed user with the exact role production will apply: tenant_admin, token_version=400.
	const email = "webmail-role-noop@example.test"
	sqlDB.Exec(`INSERT INTO users (created_at, updated_at, email, password_hash, role, tenant_id, active, email_verified, token_version) VALUES (?, ?, ?, ?, 'tenant_admin', 1, 1, 1, 400)`, now.Format("2006-01-02 15:04:05"), now.Format("2006-01-02 15:04:05"), email, "hash")

	var userID uint
	sqlDB.QueryRow("SELECT id FROM users WHERE email = ?", email).Scan(&userID)

	// Issue token and prove valid.
	oldAccess, _, _, err := authn.GenerateAccessTokenWithJTI(userID, auth.RoleTenantAdmin)
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}
	uid, role, valErr := authn.ValidateAccessToken(oldAccess)
	if valErr != nil || uid != userID || string(role) != "tenant_admin" {
		t.Fatalf("token before no-op: err=%v uid=%d role=%s", valErr, uid, role)
	}

	var countBefore int
	sqlDB.QueryRow("SELECT COUNT(*) FROM users WHERE email = ?", email).Scan(&countBefore)

	// Invoke ensureWebmailUser with isAdmin=true (same role the user already has).
	dial := dbdialect.FromDriver("sqlite")
	h := &Handler{}
	returnedID, callErr := h.ensureWebmailUser(dial, sqlDB, email, true)
	if callErr != nil {
		t.Fatalf("ensureWebmailUser: %v", callErr)
	}
	if returnedID != userID {
		t.Fatalf("returned ID: want %d, got %d", userID, returnedID)
	}

	var countAfter int
	sqlDB.QueryRow("SELECT COUNT(*) FROM users WHERE email = ?", email).Scan(&countAfter)
	if countAfter != 1 {
		t.Fatalf("user count after: want 1, got %d", countAfter)
	}

	// State unchanged.
	var storedRole string
	var storedTV int64
	sqlDB.QueryRow("SELECT role, COALESCE(token_version,0) FROM users WHERE id = ?", userID).Scan(&storedRole, &storedTV)
	if storedRole != "tenant_admin" || storedTV != 400 {
		t.Fatalf("mutated: role=%s tv=%d", storedRole, storedTV)
	}

	// Old token still valid.
	uid2, role2, err2 := authn.ValidateAccessToken(oldAccess)
	if err2 != nil || uid2 != userID || string(role2) != "tenant_admin" {
		t.Fatalf("token after no-op: err=%v uid=%d role=%s", err2, uid2, role2)
	}
}
