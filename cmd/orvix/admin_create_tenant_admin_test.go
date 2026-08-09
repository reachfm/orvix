package main

import (
	"bytes"
	"database/sql"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/orvix/orvix/internal/auth"
	"github.com/orvix/orvix/internal/dbdialect"
)

func seedTenant(t *testing.T, db *sql.DB, id int64, active bool, deleted bool) {
	t.Helper()
	now := time.Now().UTC()
	activeInt := 0
	if active {
		activeInt = 1
	}
	_, err := db.Exec(
		`INSERT INTO tenants (id, created_at, updated_at, name, slug, domain, plan, active, max_domains, max_mailboxes)
		 VALUES (?, ?, ?, ?, ?, ?, 'smb', ?, 10, 500)`,
		id, now, now, "Tenant", "tenant-slug", "tenant.test", activeInt,
	)
	if err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	if deleted {
		if _, err := db.Exec("UPDATE tenants SET deleted_at = ? WHERE id = ?", now, id); err != nil {
			t.Fatalf("mark tenant deleted: %v", err)
		}
	}
}

func ctaDeps(db *sql.DB, dial *dbdialect.Info, isRoot bool, password string, stdout, stderr *bytes.Buffer) adminCLIDeps {
	return adminCLIDeps{
		isRoot: func() bool { return isRoot },
		readPassword: func(w io.Writer) (string, error) {
			if password == "" {
				return "", errAdminRecoveryNotTTY
			}
			return password, nil
		},
		readConfirmation: func(w io.Writer, prompt string) (string, error) { return "", errAdminRecoveryNotTTY },
		openDB: func() (*sql.DB, *dbdialect.Info, func() error, error) {
			return db, dial, func() error { return nil }, nil
		},
		now:    func() time.Time { return time.Date(2026, 8, 9, 0, 0, 0, 0, time.UTC) },
		stdout: stdout,
		stderr: stderr,
	}
}

// ---------------------------------------------------------------------------
// non-root denial before DB access
// ---------------------------------------------------------------------------

func TestCreateTenantAdmin_NonRootDeniedBeforeDBAccess(t *testing.T) {
	var stdout, stderr bytes.Buffer
	dbOpened := false
	deps := adminCLIDeps{
		isRoot: func() bool { return false },
		readPassword: func(w io.Writer) (string, error) {
			t.Fatal("password must never be prompted for a non-root invocation")
			return "", nil
		},
		readConfirmation: func(w io.Writer, p string) (string, error) { return "", nil },
		openDB: func() (*sql.DB, *dbdialect.Info, func() error, error) {
			dbOpened = true
			return nil, nil, nil, errors.New("must not be called")
		},
		now:    time.Now,
		stdout: &stdout,
		stderr: &stderr,
	}
	if code := runAdminCreateTenantAdmin(1, "new-admin@example.com", deps); code == 0 {
		t.Fatal("expected non-zero exit for non-root invocation")
	}
	if dbOpened {
		t.Fatal("database must never be opened for a non-root invocation")
	}
}

// ---------------------------------------------------------------------------
// invalid/zero/negative tenant ID denied
// ---------------------------------------------------------------------------

func TestCreateTenantAdmin_InvalidTenantIDDenied(t *testing.T) {
	db, dial := adminRecoveryTestDB(t)
	var stdout, stderr bytes.Buffer
	deps := ctaDeps(db, dial, true, "a-strong-password-1", &stdout, &stderr)

	for _, id := range []int64{0, -1, -100} {
		if code := runAdminCreateTenantAdmin(id, "new-admin@example.com", deps); code == 0 {
			t.Fatalf("expected failure for tenant id %d", id)
		}
	}
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM users WHERE email = 'new-admin@example.com'").Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Fatalf("invalid tenant id must cause zero mutation, got %d rows", n)
	}
}

// ---------------------------------------------------------------------------
// missing/deleted/inactive tenant denied
// ---------------------------------------------------------------------------

func TestCreateTenantAdmin_MissingTenantDenied(t *testing.T) {
	db, dial := adminRecoveryTestDB(t)
	var stdout, stderr bytes.Buffer
	deps := ctaDeps(db, dial, true, "a-strong-password-1", &stdout, &stderr)
	if code := runAdminCreateTenantAdmin(999, "new-admin@example.com", deps); code == 0 {
		t.Fatal("expected failure for a nonexistent tenant")
	}
}

func TestCreateTenantAdmin_DeletedTenantDenied(t *testing.T) {
	db, dial := adminRecoveryTestDB(t)
	seedTenant(t, db, 5, true, true)
	var stdout, stderr bytes.Buffer
	deps := ctaDeps(db, dial, true, "a-strong-password-1", &stdout, &stderr)
	if code := runAdminCreateTenantAdmin(5, "new-admin@example.com", deps); code == 0 {
		t.Fatal("expected failure for a soft-deleted tenant")
	}
}

func TestCreateTenantAdmin_InactiveTenantDenied(t *testing.T) {
	db, dial := adminRecoveryTestDB(t)
	seedTenant(t, db, 6, false, false)
	var stdout, stderr bytes.Buffer
	deps := ctaDeps(db, dial, true, "a-strong-password-1", &stdout, &stderr)
	if code := runAdminCreateTenantAdmin(6, "new-admin@example.com", deps); code == 0 {
		t.Fatal("expected failure for an inactive tenant")
	}
}

// ---------------------------------------------------------------------------
// invalid/duplicate email denied with zero mutation
// ---------------------------------------------------------------------------

func TestCreateTenantAdmin_InvalidEmailDenied(t *testing.T) {
	db, dial := adminRecoveryTestDB(t)
	seedTenant(t, db, 1, true, false)
	var stdout, stderr bytes.Buffer
	deps := ctaDeps(db, dial, true, "a-strong-password-1", &stdout, &stderr)
	if code := runAdminCreateTenantAdmin(1, "not-an-email", deps); code == 0 {
		t.Fatal("expected failure for an invalid email")
	}
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM users").Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Fatalf("invalid email must cause zero mutation, got %d users", n)
	}
}

func TestCreateTenantAdmin_DuplicateEmailDenied(t *testing.T) {
	db, dial := adminRecoveryTestDB(t)
	seedTenant(t, db, 1, true, false)
	seedAdminRecoveryUser(t, db, seededUser{Email: "existing@example.com", Role: "tenant_admin", TenantID: int64Ptr(1), Active: true})

	var stdout, stderr bytes.Buffer
	deps := ctaDeps(db, dial, true, "a-strong-password-1", &stdout, &stderr)
	if code := runAdminCreateTenantAdmin(1, "existing@example.com", deps); code == 0 {
		t.Fatal("expected failure for a duplicate email")
	}
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM users").Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Fatalf("duplicate email must cause zero additional mutation, got %d users", n)
	}
}

// ---------------------------------------------------------------------------
// password mismatch/weak password/non-TTY denied with zero mutation
// ---------------------------------------------------------------------------

func TestCreateTenantAdmin_NonInteractivePasswordDenied(t *testing.T) {
	db, dial := adminRecoveryTestDB(t)
	seedTenant(t, db, 1, true, false)
	var stdout, stderr bytes.Buffer
	deps := ctaDeps(db, dial, true, "", &stdout, &stderr)
	if code := runAdminCreateTenantAdmin(1, "new-admin@example.com", deps); code == 0 {
		t.Fatal("expected failure when password cannot be read interactively")
	}
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM users").Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Fatalf("expected zero mutation, got %d users", n)
	}
}

func TestCreateTenantAdmin_WeakPasswordZeroMutation(t *testing.T) {
	db, dial := adminRecoveryTestDB(t)
	seedTenant(t, db, 1, true, false)
	var stdout, stderr bytes.Buffer
	deps := ctaDeps(db, dial, true, "short", &stdout, &stderr)
	if code := runAdminCreateTenantAdmin(1, "new-admin@example.com", deps); code == 0 {
		t.Fatal("expected failure for a too-short password")
	}
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM users").Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Fatalf("weak password must cause zero mutation, got %d users", n)
	}
}

func TestCreateTenantAdmin_PasswordMismatchZeroMutation(t *testing.T) {
	db, dial := adminRecoveryTestDB(t)
	seedTenant(t, db, 1, true, false)
	var stdout, stderr bytes.Buffer
	deps := ctaDeps(db, dial, true, "", &stdout, &stderr)
	deps.readPassword = func(w io.Writer) (string, error) { return "", errAdminRecoveryMismatch }
	if code := runAdminCreateTenantAdmin(1, "new-admin@example.com", deps); code == 0 {
		t.Fatal("expected failure on password mismatch")
	}
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM users").Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Fatalf("mismatch must cause zero mutation, got %d users", n)
	}
}

// ---------------------------------------------------------------------------
// successful creation: full contract
// ---------------------------------------------------------------------------

func TestCreateTenantAdmin_FullContract(t *testing.T) {
	db, dial := adminRecoveryTestDB(t)
	seedTenant(t, db, 1, true, false)
	psaID := seedAdminRecoveryUser(t, db, seededUser{Email: "psa@example.com", Role: "platform_super_admin", TenantID: nil, Active: true})

	var stdout, stderr bytes.Buffer
	deps := ctaDeps(db, dial, true, "a-strong-new-password-1", &stdout, &stderr)
	if code := runAdminCreateTenantAdmin(1, "New-Admin@Example.com", deps); code != 0 {
		t.Fatalf("expected success, stderr=%s", stderr.String())
	}

	role, tenantID, active, deleted, tv, mfa := rowState(t, db, mustUserID(t, db, "new-admin@example.com"))
	if role != "tenant_admin" {
		t.Fatalf("expected role tenant_admin, got %s", role)
	}
	if !tenantID.Valid || tenantID.Int64 != 1 {
		t.Fatalf("expected tenant_id=1, got %+v", tenantID)
	}
	if !active || deleted {
		t.Fatalf("expected active=true deleted=false, got active=%v deleted=%v", active, deleted)
	}
	if tv != 0 {
		t.Fatalf("expected token_version=0 for a brand-new user, got %d", tv)
	}
	if mfa {
		t.Fatal("expected mfa_enabled=false")
	}

	// email is normalized (case-insensitive storage lookup succeeds).
	var storedEmail string
	if err := db.QueryRow("SELECT email FROM users WHERE LOWER(email) = 'new-admin@example.com'").Scan(&storedEmail); err != nil {
		t.Fatalf("lookup normalized email: %v", err)
	}

	if n := sessionCount(t, db, mustUserID(t, db, "new-admin@example.com")); n != 0 {
		t.Fatalf("provisioning must create no session, got %d", n)
	}
	if n := auditCount(t, db, "admin.tenant_admin_create", "new-admin@example.com"); n != 1 {
		t.Fatalf("expected exactly one audit row, got %d", n)
	}

	// PSA and other users remain unchanged.
	psaRole, psaTenant, _, _, psaTV, _ := rowState(t, db, psaID)
	if psaRole != "platform_super_admin" || psaTenant.Valid || psaTV != 0 {
		t.Fatalf("PSA must remain unchanged, got role=%s tenant=%+v tv=%d", psaRole, psaTenant, psaTV)
	}

	// password authenticates through production auth, and a token can be
	// issued carrying role=tenant_admin.
	authn := mustNewTestAuthenticator(t, db, &authTestConfig{})
	var storedHash string
	if err := db.QueryRow("SELECT password_hash FROM users WHERE LOWER(email) = 'new-admin@example.com'").Scan(&storedHash); err != nil {
		t.Fatalf("read stored hash: %v", err)
	}
	if !authn.VerifyPassword("a-strong-new-password-1", storedHash) {
		t.Fatal("new password must authenticate against the stored hash")
	}
	newID := mustUserID(t, db, "new-admin@example.com")
	token, _, _, err := authn.GenerateAccessTokenWithJTI(uint(newID), auth.RoleTenantAdmin)
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}
	if _, gotRole, err := authn.ValidateAccessToken(token); err != nil || gotRole != auth.RoleTenantAdmin {
		t.Fatalf("token must validate with role tenant_admin, role=%v err=%v", gotRole, err)
	}

	output := stdout.String() + stderr.String()
	for _, forbidden := range []string{"a-strong-new-password-1", storedHash, token} {
		if strings.Contains(output, forbidden) {
			t.Fatalf("output must never contain the password, hash, or token, found %q", forbidden)
		}
	}
	if !strings.Contains(stdout.String(), "new-admin@example.com") {
		t.Fatal("success message must include the created user's email")
	}
}

// ---------------------------------------------------------------------------
// rollback occurs if audit insertion fails
// ---------------------------------------------------------------------------

func TestCreateTenantAdmin_AuditInsertFailureRollsBackEverything(t *testing.T) {
	db, dial := adminRecoveryTestDB(t)
	seedTenant(t, db, 1, true, false)

	if _, err := db.Exec("DROP TABLE coremail_audit"); err != nil {
		t.Fatalf("drop audit table: %v", err)
	}

	var stdout, stderr bytes.Buffer
	deps := ctaDeps(db, dial, true, "a-strong-new-password-1", &stdout, &stderr)
	if code := runAdminCreateTenantAdmin(1, "new-admin@example.com", deps); code == 0 {
		t.Fatal("expected failure when the audit insert fails")
	}

	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM users WHERE email = 'new-admin@example.com'").Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Fatalf("user creation must be rolled back when the audit insert fails, got %d rows", n)
	}
}

// ---------------------------------------------------------------------------
// existing reset/recover behavior remains unaffected (spot check;
// admin_recovery_test.go carries the full suite)
// ---------------------------------------------------------------------------

func TestCreateTenantAdmin_DoesNotAffectResetPasswordOrRecover(t *testing.T) {
	db, dial := adminRecoveryTestDB(t)
	seedTenant(t, db, 1, true, false)
	targetID := seedAdminRecoveryUser(t, db, seededUser{Email: "ta@example.com", Role: "tenant_admin", TenantID: int64Ptr(1), Active: true})

	var stdout, stderr bytes.Buffer
	deps := ctaDeps(db, dial, true, "a-strong-new-password-1", &stdout, &stderr)
	if code := runAdminCreateTenantAdmin(1, "brand-new@example.com", deps); code != 0 {
		t.Fatalf("provisioning should succeed, stderr=%s", stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	deps2 := ctaDeps(db, dial, true, "another-strong-password-2", &stdout, &stderr)
	if code := runAdminResetPassword("ta@example.com", deps2); code != 0 {
		t.Fatalf("reset-password should still work unaffected, stderr=%s", stderr.String())
	}
	role, _, _, _, tv, _ := rowState(t, db, targetID)
	if role != "tenant_admin" || tv != 1 {
		t.Fatalf("unexpected post-reset state: role=%s tv=%d", role, tv)
	}
}
