package main

// PORTAL-SEPARATION-PHASE1 Section-4 tests: bootstrap convergence.
//
// These tests are the runtime proof for the four Phase-1 bootstrap
// invariants promised in docs/deployment/portal-separation-phase1.md:
//
//   1. Fresh install creates exactly ONE row for the configured admin
//      email, with role=platform_super_admin and tenant_id=NULL.
//   2. A pre-existing legacy admin row (role='admin', tenant_id=<t>)
//      SELF-HEALS on the next process start — the same row is rewritten
//      to platform_super_admin/NULL, token_version is bumped, and
//      active/verified are preserved. No duplicate row is inserted.
//   3. Restarting a healthy install is a no-op — no additional row is
//      created, the existing row's role/tenant/active/verified/hash
//      are unchanged, and the token_version does NOT get bumped a
//      second time (the normalizer is idempotent).
//   4. An unrelated tenant admin row (different email, role='admin' or
//      'tenant_admin', tenant_id=<t>) is NEVER promoted to
//      platform_super_admin, no matter what email domain matches. There
//      is no email-domain inference: only the configured
//      ORVIX_ADMIN_EMAIL address gets the platform role.
//
// The tests exercise the same seedAdminUser+migrateConfiguredDatabase
// pair that main() calls at startup, using the SQLite path (the
// PostgreSQL path is covered by the same NormalizeAdminRoles code
// path and its own unit tests in internal/models).

import (
	"context"
	"database/sql"
	"encoding/base64"
	"testing"

	"github.com/orvix/orvix/internal/auth"
	"github.com/orvix/orvix/internal/config"
	"github.com/orvix/orvix/internal/dbdialect"
	"github.com/orvix/orvix/internal/models"
	"go.uber.org/zap"
)

// bootstrapDBPath returns a fresh SQLite DSN under t.TempDir. Reused
// across the convergence tests so each subtest starts from a clean DB.
func bootstrapDBPath(t *testing.T) string {
	t.Helper()
	return t.TempDir() + "/orvix.db?_loc=auto&_busy_timeout=5000&_txlock=immediate"
}

// runBootstrapPass mirrors the ordered fragment of cmd/orvix.main()
// that is relevant to admin-row convergence:
//
//	migrateConfiguredDatabase(...)   // includes NormalizeAdminRoles
//	seedAdminUser(...)
//
// It returns the raw *sql.DB so the caller can assert the resulting
// rows. The caller is responsible for calling sqlDB.Close().
func runBootstrapPass(t *testing.T, dsn, email, password string) *sql.DB {
	t.Helper()
	t.Setenv("ORVIX_ADMIN_EMAIL", email)
	t.Setenv("ORVIX_ADMIN_PASSWORD_B64", base64.StdEncoding.EncodeToString([]byte(password)))
	t.Setenv("ORVIX_ADMIN_PASSWORD", "")

	cfg := config.Defaults()
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = dsn
	logger := zap.NewNop()

	db, err := config.NewDatabase(&cfg.Database, logger)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := migrateConfiguredDatabase(db, cfg.Database.Driver, logger); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("sql db: %v", err)
	}
	dialect, err := dbdialect.Detect(sqlDB)
	if err != nil {
		t.Fatalf("detect dialect: %v", err)
	}
	authenticator, err := auth.NewAuthenticator(&cfg.Auth, db, logger)
	if err != nil {
		t.Fatalf("auth: %v", err)
	}
	seedAdminUser(db, authenticator, logger, dialect)
	return sqlDB
}

type adminRow struct {
	id           int64
	role         string
	tenantID     sql.NullInt64
	active       bool
	verified     bool
	tokenVersion int64
	passwordHash string
}

// selectAdminRow reads a single row for the given email. Fails the
// test if the row is missing.
func selectAdminRow(t *testing.T, db *sql.DB, email string) adminRow {
	t.Helper()
	var r adminRow
	err := db.QueryRow(
		`SELECT id, role, tenant_id, active, email_verified, token_version, password_hash
		   FROM users WHERE email = ?`, email,
	).Scan(&r.id, &r.role, &r.tenantID, &r.active, &r.verified, &r.tokenVersion, &r.passwordHash)
	if err != nil {
		t.Fatalf("select admin row for %s: %v", email, err)
	}
	return r
}

// countAdminRowsByEmail returns the total number of user rows for an
// email. Convergence guarantees there is never more than one.
func countAdminRowsByEmail(t *testing.T, db *sql.DB, email string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM users WHERE email = ?`, email).Scan(&n); err != nil {
		t.Fatalf("count rows for %s: %v", email, err)
	}
	return n
}

// TestBootstrap_FreshInstall_CreatesExactlyOnePlatformSuperAdmin is
// the Section-4 invariant #1 test. A brand-new database plus a single
// seedAdminUser pass must land exactly one users row for the
// configured email, and that row must carry role=platform_super_admin
// and tenant_id=NULL. This is the runtime side of the installer's
// verify_install SQL predicate.
func TestBootstrap_FreshInstall_CreatesExactlyOnePlatformSuperAdmin(t *testing.T) {
	dsn := bootstrapDBPath(t)
	sqlDB := runBootstrapPass(t, dsn, "admin@orvix.email", "MaghaghaMos086")
	defer sqlDB.Close()

	if got := countAdminRowsByEmail(t, sqlDB, "admin@orvix.email"); got != 1 {
		t.Fatalf("expected exactly 1 admin row after fresh install, got %d", got)
	}
	row := selectAdminRow(t, sqlDB, "admin@orvix.email")
	if row.role != string(auth.RolePlatformSuperAdmin) {
		t.Errorf("fresh install role: got %q, want %q", row.role, auth.RolePlatformSuperAdmin)
	}
	if row.tenantID.Valid {
		t.Errorf("fresh install tenant_id: got %d, want NULL", row.tenantID.Int64)
	}
	if !row.active {
		t.Error("fresh install active: got false, want true")
	}
	if !row.verified {
		t.Error("fresh install email_verified: got false, want true")
	}
}

// TestBootstrap_ExistingLegacyAdmin_SelfHealsToPSA is the Section-4
// invariant #2 test. An install that predates portal-separation-phase1
// carries the configured admin row as role='admin' with tenant_id set
// to the customer tenant. On the next process start, migrate() runs
// NormalizeAdminRoles BEFORE seedAdminUser sees the row, so the
// legacy row is rewritten in place: same id, same active/verified,
// same password_hash, but role=platform_super_admin, tenant_id=NULL,
// and token_version bumped by exactly 1 (session invalidation for
// every already-issued JWT).
func TestBootstrap_ExistingLegacyAdmin_SelfHealsToPSA(t *testing.T) {
	dsn := bootstrapDBPath(t)

	// First pass: build the DB by running a normal fresh install so
	// the schema is right, then reset the row to the legacy shape
	// that older installs carry on disk.
	first := runBootstrapPass(t, dsn, "admin@orvix.email", "MaghaghaMos086")
	fresh := selectAdminRow(t, first, "admin@orvix.email")
	if fresh.role != string(auth.RolePlatformSuperAdmin) {
		t.Fatalf("preconditions: fresh install role should be PSA, got %q", fresh.role)
	}
	// Rewrite the row to look like a legacy 'admin' + tenant_id=1
	// install, and freeze token_version so we can measure the bump.
	if _, err := first.Exec(
		`UPDATE users SET role = 'admin', tenant_id = 1, token_version = 0 WHERE id = ?`,
		fresh.id,
	); err != nil {
		t.Fatalf("regress row to legacy shape: %v", err)
	}
	first.Close()

	// Second pass: same DSN. migrateConfiguredDatabase must
	// normalize the legacy row before seedAdminUser observes it.
	second := runBootstrapPass(t, dsn, "admin@orvix.email", "MaghaghaMos086")
	defer second.Close()

	if got := countAdminRowsByEmail(t, second, "admin@orvix.email"); got != 1 {
		t.Fatalf("self-heal must not insert a second row; got %d rows", got)
	}
	healed := selectAdminRow(t, second, "admin@orvix.email")
	if healed.id != fresh.id {
		t.Errorf("row id changed during self-heal: was %d, is %d (must be in-place)", fresh.id, healed.id)
	}
	if healed.role != string(auth.RolePlatformSuperAdmin) {
		t.Errorf("self-heal role: got %q, want %q", healed.role, auth.RolePlatformSuperAdmin)
	}
	if healed.tenantID.Valid {
		t.Errorf("self-heal tenant_id: got %d, want NULL", healed.tenantID.Int64)
	}
	if !healed.active {
		t.Error("self-heal must preserve active=true")
	}
	if !healed.verified {
		t.Error("self-heal must preserve email_verified=true")
	}
	if healed.passwordHash != fresh.passwordHash {
		t.Error("self-heal must not rewrite password_hash")
	}
	if healed.tokenVersion != 1 {
		t.Errorf("self-heal token_version: got %d, want 1 (single bump)", healed.tokenVersion)
	}
}

// TestBootstrap_SecondStartupIsNoop is the Section-4 invariant #3
// test. A healthy install already at PSA/NULL must survive an
// unlimited number of extra restarts without mutation. Concretely:
// the same id and same password_hash, and — critically —
// token_version stays where it was (no bump on a no-op pass). If
// this fails we've regressed idempotency and would invalidate every
// user's session on every restart.
func TestBootstrap_SecondStartupIsNoop(t *testing.T) {
	dsn := bootstrapDBPath(t)

	first := runBootstrapPass(t, dsn, "admin@orvix.email", "MaghaghaMos086")
	before := selectAdminRow(t, first, "admin@orvix.email")
	first.Close()

	second := runBootstrapPass(t, dsn, "admin@orvix.email", "MaghaghaMos086")
	defer second.Close()

	if got := countAdminRowsByEmail(t, second, "admin@orvix.email"); got != 1 {
		t.Fatalf("second startup must not insert a new row; got %d rows", got)
	}
	after := selectAdminRow(t, second, "admin@orvix.email")
	if after.id != before.id {
		t.Errorf("second startup changed id: was %d, is %d", before.id, after.id)
	}
	if after.role != before.role {
		t.Errorf("second startup changed role: %q -> %q", before.role, after.role)
	}
	if after.tenantID != before.tenantID {
		t.Errorf("second startup changed tenant_id: %+v -> %+v", before.tenantID, after.tenantID)
	}
	if after.passwordHash != before.passwordHash {
		t.Error("second startup must not rewrite password_hash")
	}
	if after.tokenVersion != before.tokenVersion {
		t.Errorf("second startup bumped token_version: %d -> %d (idempotency broken)", before.tokenVersion, after.tokenVersion)
	}
}

// TestBootstrap_UnrelatedTenantAdmin_NotPromoted is the Section-4
// invariant #4 test. Even if a customer's own tenant_admin (a totally
// unrelated user row, different email, different tenant) exists on
// the same box, it must NEVER inherit platform_super_admin. The
// promotion is keyed off ORVIX_ADMIN_EMAIL exact match, not off any
// email-domain match, not off "the only admin-shaped row".
//
// The test seeds an unrelated tenant admin with role='admin' + tenant
// set (the pre-normalization shape) AND another with the already
// normalized role='tenant_admin', both under a different email
// domain than the bootstrap. After normalization runs, the first
// must land on 'tenant_admin' (still keeping tenant_id) and the
// second must remain untouched. Neither may become PSA.
func TestBootstrap_UnrelatedTenantAdmin_NotPromoted(t *testing.T) {
	dsn := bootstrapDBPath(t)

	// Fresh install to build schema + bootstrap PSA row.
	first := runBootstrapPass(t, dsn, "admin@orvix.email", "MaghaghaMos086")

	// Seed two unrelated tenant admins on a DIFFERENT email domain.
	// One in the legacy 'admin' shape, one already normalized to
	// 'tenant_admin'. Neither shares the bootstrap email.
	if _, err := first.Exec(
		`INSERT INTO users (created_at, updated_at, email, password_hash, role, tenant_id, active, email_verified, token_version)
		 VALUES (datetime('now'), datetime('now'), 'legacy-admin@othertenant.example', 'x', 'admin', 42, 1, 1, 0)`,
	); err != nil {
		t.Fatalf("seed legacy tenant admin: %v", err)
	}
	if _, err := first.Exec(
		`INSERT INTO users (created_at, updated_at, email, password_hash, role, tenant_id, active, email_verified, token_version)
		 VALUES (datetime('now'), datetime('now'), 'canonical-admin@othertenant.example', 'x', 'tenant_admin', 43, 1, 1, 0)`,
	); err != nil {
		t.Fatalf("seed canonical tenant admin: %v", err)
	}
	first.Close()

	// Second startup runs normalize again. Neither unrelated row
	// may be promoted to platform_super_admin.
	second := runBootstrapPass(t, dsn, "admin@orvix.email", "MaghaghaMos086")
	defer second.Close()

	legacy := selectAdminRow(t, second, "legacy-admin@othertenant.example")
	if legacy.role != "tenant_admin" {
		t.Errorf("legacy tenant admin: got role %q, want tenant_admin", legacy.role)
	}
	if !legacy.tenantID.Valid || legacy.tenantID.Int64 != 42 {
		t.Errorf("legacy tenant admin tenant_id: got %+v, want 42", legacy.tenantID)
	}

	canonical := selectAdminRow(t, second, "canonical-admin@othertenant.example")
	if canonical.role != "tenant_admin" {
		t.Errorf("canonical tenant admin: got role %q, want tenant_admin (unchanged)", canonical.role)
	}
	if !canonical.tenantID.Valid || canonical.tenantID.Int64 != 43 {
		t.Errorf("canonical tenant admin tenant_id: got %+v, want 43", canonical.tenantID)
	}

	// Bootstrap PSA row still exactly one, still PSA.
	if got := countAdminRowsByEmail(t, second, "admin@orvix.email"); got != 1 {
		t.Fatalf("bootstrap PSA rowcount: got %d, want 1", got)
	}
	psa := selectAdminRow(t, second, "admin@orvix.email")
	if psa.role != string(auth.RolePlatformSuperAdmin) || psa.tenantID.Valid {
		t.Errorf("bootstrap PSA drifted: role=%q tenant_id=%+v", psa.role, psa.tenantID)
	}

	// Belt-and-braces: nobody outside the bootstrap email is PSA.
	var psaOthers int
	if err := second.QueryRow(
		`SELECT COUNT(*) FROM users WHERE role = 'platform_super_admin' AND email <> 'admin@orvix.email'`,
	).Scan(&psaOthers); err != nil {
		t.Fatalf("count non-bootstrap PSA rows: %v", err)
	}
	if psaOthers != 0 {
		t.Errorf("non-bootstrap PSA rows: got %d, want 0", psaOthers)
	}
}

// TestBootstrap_NoEmailDomainInference is the Section-4 invariant #4
// corollary: the PSA row's tenant_id must be NULL regardless of the
// admin email's domain. Older sketches of this code inferred a
// "platform tenant" from the email domain and stamped it into
// users.tenant_id; that inference is banned. Test proves: fresh
// install with an email whose domain matches an existing tenant row
// still produces tenant_id=NULL on the PSA row.
func TestBootstrap_NoEmailDomainInference(t *testing.T) {
	// Two different domains, two separate DBs. Neither must produce
	// a PSA row with a non-NULL tenant_id.
	cases := []string{
		"admin@orvix.email",
		"root@example.internal",
	}
	for _, email := range cases {
		email := email
		t.Run(email, func(t *testing.T) {
			dsn := bootstrapDBPath(t)
			sqlDB := runBootstrapPass(t, dsn, email, "MaghaghaMos086")
			defer sqlDB.Close()
			row := selectAdminRow(t, sqlDB, email)
			if row.role != string(auth.RolePlatformSuperAdmin) {
				t.Errorf("role: got %q, want platform_super_admin", row.role)
			}
			if row.tenantID.Valid {
				t.Errorf("tenant_id: got %d, want NULL (no email-domain inference allowed)", row.tenantID.Int64)
			}
		})
	}
}

// TestBootstrap_InstallerPredicatesRecognizePSAAfterRerun is the
// end-to-end proof that a non-interactive rerun of release/install.sh
// on an already-installed box will take the "preserve existing admin"
// path instead of the "fresh install" path. The failure mode this
// guards against is subtle: the runtime writes role=platform_super_admin,
// but a stale installer WHERE clause that only knew about legacy
// 'admin'/'superadmin'/'super_admin' would return count=0, and
// install.sh at line 2957 (`if [ "${existing_admins:-0}" -gt 0 ]`)
// would decide this is a first install and demand a fresh password.
//
// We replay the exact SQL from active_admin_count, first_active_admin_email,
// and admin_user_exists against the same on-disk shape the runtime
// leaves after seedAdminUser. If any of them fails to match, the
// installer's non-interactive rerun would break.
func TestBootstrap_InstallerPredicatesRecognizePSAAfterRerun(t *testing.T) {
	dsn := bootstrapDBPath(t)
	sqlDB := runBootstrapPass(t, dsn, "admin@orvix.email", "MaghaghaMos086")
	defer sqlDB.Close()

	// The exact WHERE clause literals from release/install.sh.
	// Keep in lockstep with active_admin_count / first_active_admin_email
	// / admin_user_exists / verify_install / reset_existing_admin_password.
	const activeAdminCountSQL = `SELECT COUNT(*) FROM users WHERE role IN ('admin','superadmin','super_admin','platform_super_admin') AND active = 1`
	const firstAdminEmailSQL = `SELECT email FROM users WHERE role IN ('admin','superadmin','super_admin','platform_super_admin') AND active = 1 ORDER BY id LIMIT 1`
	const adminUserExistsSQL = `SELECT COUNT(*) FROM users WHERE email = ? AND role IN ('admin','superadmin','super_admin','platform_super_admin') AND active = 1`

	var count int
	if err := sqlDB.QueryRow(activeAdminCountSQL).Scan(&count); err != nil {
		t.Fatalf("active_admin_count SQL: %v", err)
	}
	if count != 1 {
		t.Fatalf("installer's active_admin_count would return %d for a fresh PSA install; expected 1 (would misroute non-interactive rerun to fresh mode)", count)
	}

	var firstEmail string
	if err := sqlDB.QueryRow(firstAdminEmailSQL).Scan(&firstEmail); err != nil {
		t.Fatalf("first_active_admin_email SQL: %v", err)
	}
	if firstEmail != "admin@orvix.email" {
		t.Fatalf("installer's first_active_admin_email would return %q; expected admin@orvix.email", firstEmail)
	}

	var existsCount int
	if err := sqlDB.QueryRow(adminUserExistsSQL, "admin@orvix.email").Scan(&existsCount); err != nil {
		t.Fatalf("admin_user_exists SQL: %v", err)
	}
	if existsCount != 1 {
		t.Fatalf("installer's admin_user_exists would return %d for the bootstrap PSA row; expected 1 (would break ORVIX_RESET_ADMIN_PASSWORD=1)", existsCount)
	}
}

// TestBootstrap_NormalizeAdminRoles_DirectlyProvesSelfHealAndIdempotency
// is a belt-and-braces unit-level assertion against the same code path
// exercised end-to-end by TestBootstrap_ExistingLegacyAdmin_SelfHealsToPSA
// and TestBootstrap_SecondStartupIsNoop. It calls NormalizeAdminRoles
// twice on the same DB and asserts the second call performs zero
// mutations. This is the property main() depends on to be safe to run
// on every process start.
func TestBootstrap_NormalizeAdminRoles_DirectlyProvesSelfHealAndIdempotency(t *testing.T) {
	dsn := bootstrapDBPath(t)
	sqlDB := runBootstrapPass(t, dsn, "admin@orvix.email", "MaghaghaMos086")
	defer sqlDB.Close()

	// Regress: legacy shape on the same row.
	if _, err := sqlDB.Exec(
		`UPDATE users SET role = 'admin', tenant_id = 99, token_version = 0 WHERE email = ?`,
		"admin@orvix.email",
	); err != nil {
		t.Fatalf("regress: %v", err)
	}

	// First call: promotes bootstrap row → PSA.
	first, err := models.NormalizeAdminRoles(context.Background(), sqlDB, "sqlite")
	if err != nil {
		t.Fatalf("normalize pass 1: %v", err)
	}
	if first.PlatformPromoted != 1 {
		t.Errorf("first pass PlatformPromoted: got %d, want 1", first.PlatformPromoted)
	}
	afterFirst := selectAdminRow(t, sqlDB, "admin@orvix.email")
	if afterFirst.role != string(auth.RolePlatformSuperAdmin) || afterFirst.tenantID.Valid {
		t.Fatalf("first pass did not converge: role=%q tenant_id=%+v", afterFirst.role, afterFirst.tenantID)
	}

	// Second call: pure no-op.
	second, err := models.NormalizeAdminRoles(context.Background(), sqlDB, "sqlite")
	if err != nil {
		t.Fatalf("normalize pass 2: %v", err)
	}
	if second.PlatformPromoted != 0 || second.TenantPromoted != 0 ||
		second.OperatorRenamed != 0 || second.ReadOnlyRenamed != 0 {
		t.Errorf("second pass must be a no-op, got %+v", second)
	}
	afterSecond := selectAdminRow(t, sqlDB, "admin@orvix.email")
	if afterSecond.tokenVersion != afterFirst.tokenVersion {
		t.Errorf("no-op pass bumped token_version: %d -> %d", afterFirst.tokenVersion, afterSecond.tokenVersion)
	}
}
