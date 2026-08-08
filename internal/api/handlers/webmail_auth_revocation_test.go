package handlers

import (
	"database/sql"
	"path/filepath"
	"strings"
	"sync"
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

	// Seed existing webmail user with the ONLY role ensureWebmailUser is
	// allowed to reconcile (migration-window: legacy 'admin' → tenant_admin
	// per state machine case D). Every other role mismatch is required to
	// fail closed by the strict state machine, so this test uses the
	// single supported reconciliation path to prove the revocation
	// invariant (role change → token_version bump → old token rejected).
	const email = "webmail-role-change@example.test"
	sqlDB.Exec(`INSERT INTO users (created_at, updated_at, email, password_hash, role, tenant_id, active, email_verified, token_version) VALUES (?, ?, ?, ?, 'admin', 1, 1, 1, 300)`, now.Format("2006-01-02 15:04:05"), now.Format("2006-01-02 15:04:05"), email, "hash")

	// Get the user ID.
	var userID uint
	if err := sqlDB.QueryRow("SELECT id FROM users WHERE email = ?", email).Scan(&userID); err != nil {
		t.Fatalf("seed user not found: %v", err)
	}

	// Issue old access token and prove it validates. The stored role
	// string is legacy 'admin' — token_version validation is the property
	// under test, not the role claim shape.
	oldAccess, _, _, err := authn.GenerateAccessTokenWithJTI(userID, auth.RoleAdmin)
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}
	uid, _, valErr := authn.ValidateAccessToken(oldAccess)
	if valErr != nil || uid != userID {
		t.Fatalf("old token before mutation: err=%v uid=%d", valErr, uid)
	}

	// Count users before.
	var countBefore int
	sqlDB.QueryRow("SELECT COUNT(*) FROM users WHERE email = ?", email).Scan(&countBefore)

	// Invoke real ensureWebmailUser. isAdmin=true → desired role=tenant_admin.
	dial := dbdialect.FromDriver("sqlite")
	h := &Handler{}
	returnedID, callErr := h.ensureWebmailUser(dial, sqlDB, email, 1, true)
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
	returnedID, callErr := h.ensureWebmailUser(dial, sqlDB, email, 1, true)
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

func TestWebmailNewAdminUsesCanonicalTenantRole(t *testing.T) {
	for i := 0; i < 3; i++ {
		sqlDB, _ := newWebmailRevocationAuth(t)
		now := time.Now().UTC()
		sqlDB.Exec(`INSERT INTO tenants (id, name, slug, domain, plan, active, created_at, updated_at) VALUES (1, 'a', 'a', 'a.example', 'enterprise', 1, ?, ?)`, now, now)
		dial := dbdialect.FromDriver("sqlite")
		h := &Handler{}
		const email = "webmail-newadmin@example.test"
		returnedID, err := h.ensureWebmailUser(dial, sqlDB, email, 1, true)
		if err != nil {
			t.Fatalf("ensureWebmailUser: %v", err)
		}
		if returnedID == 0 {
			t.Fatal("returned ID is 0")
		}
		var role string
		var tid uint
		var active bool
		var deleted sql.NullTime
		var tv int64
		sqlDB.QueryRow("SELECT role, tenant_id, active, deleted_at, COALESCE(token_version,0) FROM users WHERE id=?", returnedID).Scan(&role, &tid, &active, &deleted, &tv)
		if role != "tenant_admin" || tid != 1 || !active || deleted.Valid || tv != 0 {
			t.Fatalf("new admin: role=%s tid=%d active=%v deleted=%v tv=%d", role, tid, active, deleted.Valid, tv)
		}
		var cnt int
		sqlDB.QueryRow("SELECT COUNT(*) FROM users WHERE email=?", email).Scan(&cnt)
		if cnt != 1 {
			t.Fatalf("user count: want 1 got %d", cnt)
		}
	}
}

func TestWebmailNewUserUsesCanonicalTenantRole(t *testing.T) {
	sqlDB, _ := newWebmailRevocationAuth(t)
	now := time.Now().UTC()
	sqlDB.Exec(`INSERT INTO tenants (id, name, slug, domain, plan, active, created_at, updated_at) VALUES (1, 'a', 'a', 'a.example', 'enterprise', 1, ?, ?)`, now, now)
	dial := dbdialect.FromDriver("sqlite")
	h := &Handler{}
	const email = "webmail-newuser@example.test"
	returnedID, err := h.ensureWebmailUser(dial, sqlDB, email, 1, false)
	if err != nil {
		t.Fatalf("ensureWebmailUser: %v", err)
	}
	var role string
	var tid uint
	sqlDB.QueryRow("SELECT role, tenant_id FROM users WHERE id=?", returnedID).Scan(&role, &tid)
	if role != "user" || tid != 1 {
		t.Fatalf("new user: role=%s tid=%d", role, tid)
	}
	var cnt int
	sqlDB.QueryRow("SELECT COUNT(*) FROM users WHERE email=?", email).Scan(&cnt)
	if cnt != 1 {
		t.Fatalf("user count: want 1 got %d", cnt)
	}
}

func TestWebmailTenantResolutionFailureCreatesNothing(t *testing.T) {
	sqlDB, _ := newWebmailRevocationAuth(t)
	now := time.Now().UTC()
	sqlDB.Exec(`INSERT INTO tenants (id, name, slug, domain, plan, active, created_at, updated_at) VALUES (1, 'a', 'a', 'a.example', 'enterprise', 1, ?, ?)`, now, now)
	dial := dbdialect.FromDriver("sqlite")
	h := &Handler{}
	for _, tid := range []uint{0, 99} {
		email := "webmail-tenfail@example.test"
		_, err := h.ensureWebmailUser(dial, sqlDB, email, tid, true)
		if err == nil {
			t.Fatalf("tenantID=%d: expected error", tid)
		}
		var cnt int
		sqlDB.QueryRow("SELECT COUNT(*) FROM users WHERE email=?", email).Scan(&cnt)
		if cnt != 0 {
			t.Fatalf("tenantID=%d: user created: %d", tid, cnt)
		}
	}
}

func TestWebmailCrossTenantExistingUserFailsClosed(t *testing.T) {
	sqlDB, _ := newWebmailRevocationAuth(t)
	now := time.Now().UTC()
	sqlDB.Exec(`INSERT INTO tenants (id, name, slug, domain, plan, active, created_at, updated_at) VALUES (1, 'a', 'a', 'a.example', 'enterprise', 1, ?, ?)`, now, now)
	sqlDB.Exec(`INSERT INTO tenants (id, name, slug, domain, plan, active, created_at, updated_at) VALUES (2, 'b', 'b', 'b.example', 'enterprise', 1, ?, ?)`, now, now)
	sqlDB.Exec(`INSERT INTO users (created_at, updated_at, email, password_hash, role, tenant_id, active, email_verified, token_version) VALUES (?, ?, 'webmail-cross@example.test', 'h', 'tenant_support', 2, 1, 1, 50)`, now, now)
	dial := dbdialect.FromDriver("sqlite")
	h := &Handler{}
	_, err := h.ensureWebmailUser(dial, sqlDB, "webmail-cross@example.test", 1, true)
	if err == nil {
		t.Fatal("cross-tenant: expected error")
	}
	var role string
	var tid uint
	var tv int64
	sqlDB.QueryRow("SELECT role, tenant_id, COALESCE(token_version,0) FROM users WHERE email='webmail-cross@example.test'").Scan(&role, &tid, &tv)
	if role != "tenant_support" || tid != 2 || tv != 50 {
		t.Fatalf("user mutated: role=%s tid=%d tv=%d", role, tid, tv)
	}
}

func TestWebmailPlatformSuperAdminNotRebound(t *testing.T) {
	// PSA must NEVER authenticate through a tenant mailbox — for either
	// isAdmin value. ensureWebmailUser must return a non-nil error, a
	// zero user id, and leave every field of the PSA row untouched.
	for _, isAdmin := range []bool{true, false} {
		isAdmin := isAdmin
		name := "isAdmin=false"
		if isAdmin {
			name = "isAdmin=true"
		}
		t.Run(name, func(t *testing.T) {
			sqlDB, _ := newWebmailRevocationAuth(t)
			now := time.Now().UTC()
			sqlDB.Exec(`INSERT INTO tenants (id, name, slug, domain, plan, active, created_at, updated_at) VALUES (1, 'a', 'a', 'a.example', 'enterprise', 1, ?, ?)`, now, now)
			sqlDB.Exec(`INSERT INTO users (created_at, updated_at, email, password_hash, role, tenant_id, active, email_verified, token_version) VALUES (?, ?, 'webmail-psa@example.test', 'h', 'platform_super_admin', NULL, 1, 1, 10)`, now, now)

			var beforeCnt int
			sqlDB.QueryRow("SELECT COUNT(*) FROM users WHERE email='webmail-psa@example.test'").Scan(&beforeCnt)

			dial := dbdialect.FromDriver("sqlite")
			h := &Handler{}
			returnedID, err := h.ensureWebmailUser(dial, sqlDB, "webmail-psa@example.test", 1, isAdmin)
			if err == nil {
				t.Fatalf("PSA %s: expected error, got success returnedID=%d", name, returnedID)
			}
			if returnedID != 0 {
				t.Fatalf("PSA %s: expected returned id 0, got %d", name, returnedID)
			}
			var afterCnt int
			sqlDB.QueryRow("SELECT COUNT(*) FROM users WHERE email='webmail-psa@example.test'").Scan(&afterCnt)
			if afterCnt != beforeCnt || afterCnt != 1 {
				t.Fatalf("PSA %s: user row count mutated: before=%d after=%d", name, beforeCnt, afterCnt)
			}
			var role string
			var tid sql.NullInt64
			var tv int64
			var active bool
			var deletedAt sql.NullTime
			sqlDB.QueryRow("SELECT role, tenant_id, COALESCE(token_version,0), active, deleted_at FROM users WHERE email='webmail-psa@example.test'").
				Scan(&role, &tid, &tv, &active, &deletedAt)
			if role != "platform_super_admin" || tid.Valid || tv != 10 || !active || deletedAt.Valid {
				t.Fatalf("PSA %s mutated: role=%s tid_valid=%v tv=%d active=%v deleted_valid=%v",
					name, role, tid.Valid, tv, active, deletedAt.Valid)
			}
		})
	}
}

func TestWebmailUnsupportedLegacyRolesFailClosed(t *testing.T) {
	// Every role is exercised as its own subtest with a DISTINCT email
	// so no seed collides with another. Each subtest asserts complete
	// state invariance (role, tenant_id, token_version, active,
	// deleted_at, users-row count for this email).
	roles := []struct{ role, slug string }{
		{"operator", "operator"},
		{"readonly", "readonly"},
		{"superadmin", "superadmin"},
		{"super_admin", "superunder"},
		{"super-admin", "superdash"},
		{"platform_super_admin", "psa-tenantbound"},
		{"user", "user-isadmintrue"},
		{"tenant_operator", "tenopwithisadmin"},
		{"tenant_support", "tensupwithisadmin"},
		{"tenant_readonly", "tenrowithisadmin"},
		{"billing", "billing"},
		{"nonexistent_role", "nonexistent"},
		{"", "empty"},
	}
	for _, r := range roles {
		r := r
		t.Run(r.role, func(t *testing.T) {
			sqlDB, _ := newWebmailRevocationAuth(t)
			now := time.Now().UTC()
			sqlDB.Exec(`INSERT INTO tenants (id, name, slug, domain, plan, active, created_at, updated_at) VALUES (1, 'a', 'a', 'a.example', 'enterprise', 1, ?, ?)`, now, now)
			email := "webmail-unsup-" + r.slug + "@example.test"
			sqlDB.Exec(`INSERT INTO users (created_at, updated_at, email, password_hash, role, tenant_id, active, email_verified, token_version) VALUES (?, ?, ?, 'h', ?, 1, 1, 1, 5)`, now, now, email, r.role)
			dial := dbdialect.FromDriver("sqlite")
			h := &Handler{}
			if _, err := h.ensureWebmailUser(dial, sqlDB, email, 1, true); err == nil {
				t.Fatalf("role %q: expected error (fail closed)", r.role)
			}
			var storedRole string
			var tid uint
			var tv int64
			var active bool
			var deletedAt sql.NullTime
			sqlDB.QueryRow("SELECT role, tenant_id, COALESCE(token_version,0), active, deleted_at FROM users WHERE email=?", email).Scan(&storedRole, &tid, &tv, &active, &deletedAt)
			if storedRole != r.role || tid != 1 || tv != 5 || !active || deletedAt.Valid {
				t.Fatalf("role %q mutated: role=%s tid=%d tv=%d active=%v deleted_valid=%v", r.role, storedRole, tid, tv, active, deletedAt.Valid)
			}
			var cnt int
			sqlDB.QueryRow("SELECT COUNT(*) FROM users WHERE email=?", email).Scan(&cnt)
			if cnt != 1 {
				t.Fatalf("role %q: user row count mutated (want 1, got %d)", r.role, cnt)
			}
		})
	}
}

func TestWebmailLegacyAdminReconciliation(t *testing.T) {
	sqlDB, _ := newWebmailRevocationAuth(t)
	now := time.Now().UTC()
	sqlDB.Exec(`INSERT INTO tenants (id, name, slug, domain, plan, active, created_at, updated_at) VALUES (1, 'a', 'a', 'a.example', 'enterprise', 1, ?, ?)`, now, now)
	sqlDB.Exec(`INSERT INTO users (created_at, updated_at, email, password_hash, role, tenant_id, active, email_verified, token_version) VALUES (?, ?, 'webmail-legacy@example.test', 'h', 'admin', 1, 1, 1, 5)`, now, now)
	dial := dbdialect.FromDriver("sqlite")
	h := &Handler{}
	returnedID, err := h.ensureWebmailUser(dial, sqlDB, "webmail-legacy@example.test", 1, true)
	if err != nil {
		t.Fatalf("legacy admin: %v", err)
	}
	var role string
	var tid uint
	var tv int64
	sqlDB.QueryRow("SELECT role, tenant_id, COALESCE(token_version,0) FROM users WHERE id=?", returnedID).Scan(&role, &tid, &tv)
	if role != "tenant_admin" || tid != 1 || tv != 6 {
		t.Fatalf("legacy admin: role=%s tid=%d tv=%d", role, tid, tv)
	}
	returnedID2, err := h.ensureWebmailUser(dial, sqlDB, "webmail-legacy@example.test", 1, true)
	if err != nil || returnedID2 != returnedID {
		t.Fatalf("legacy admin 2nd call: id=%d err=%v", returnedID2, err)
	}
	sqlDB.QueryRow("SELECT COALESCE(token_version,0) FROM users WHERE id=?", returnedID).Scan(&tv)
	if tv != 6 {
		t.Fatalf("legacy admin 2nd call bumped version: tv=%d", tv)
	}
}

func TestWebmailConcurrentCreateProducesOneCanonicalUser(t *testing.T) {
	for rep := 0; rep < 10; rep++ {
		sqlDB, _ := newWebmailRevocationAuth(t)
		now := time.Now().UTC()
		sqlDB.Exec(`INSERT INTO tenants (id, name, slug, domain, plan, active, created_at, updated_at) VALUES (1, 'a', 'a', 'a.example', 'enterprise', 1, ?, ?)`, now, now)
		dial := dbdialect.FromDriver("sqlite")
		h := &Handler{}
		email := "webmail-concurrent@example.test"
		var wg sync.WaitGroup
		ids := make(chan uint, 20)
		for i := 0; i < 20; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if uid, err := h.ensureWebmailUser(dial, sqlDB, email, 1, true); err == nil {
					ids <- uid
				}
			}()
		}
		wg.Wait()
		close(ids)
		var firstID uint
		n := 0
		for uid := range ids {
			if firstID == 0 {
				firstID = uid
			} else if uid != firstID {
				t.Fatalf("rep %d: inconsistent IDs %d vs %d", rep, firstID, uid)
			}
			n++
		}
		var cnt int
		sqlDB.QueryRow("SELECT COUNT(*) FROM users WHERE email=?", email).Scan(&cnt)
		if cnt != 1 {
			t.Fatalf("rep %d: want 1 user, got %d", rep, cnt)
		}
		if firstID == 0 {
			t.Fatalf("rep %d: no successful ID", rep)
		}
		var role string
		var tid uint
		sqlDB.QueryRow("SELECT role, tenant_id FROM users WHERE id=?", firstID).Scan(&role, &tid)
		if role != "tenant_admin" || tid != 1 {
			t.Fatalf("rep %d: role=%s tid=%d", rep, role, tid)
		}
	}
}
