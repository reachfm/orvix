package main

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/api"
	"github.com/orvix/orvix/internal/auth"
	"github.com/orvix/orvix/internal/config"
	"github.com/orvix/orvix/internal/dbdialect"
	"github.com/orvix/orvix/internal/license"
	"github.com/orvix/orvix/internal/models"
	"github.com/orvix/orvix/internal/modules"
	orvixruntime "github.com/orvix/orvix/internal/runtime"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestAdminBootstrapInsertsUserAndLoginSucceeds(t *testing.T) {
	testAdminBootstrapLogin(t, "admin@example.com", "AdminPassword123!", false)
}

func TestAdminBootstrapEncodedPasswordLoginSucceeds(t *testing.T) {
	testAdminBootstrapLogin(t, "admin@orvix.email", `Admin "quoted" \ slash $ dollar ! bang # hash 123`, true)
}

func TestAdminBootstrapInstallerPasswordCasesLoginSucceeds(t *testing.T) {
	passwords := []string{
		"MaghaghaMos086",
		"Password123!",
		"Password$123",
		"Password With Spaces",
		`Password\Slash123`,
		`Password"Quote123`,
		"Password'SingleQuote123",
	}
	for _, password := range passwords {
		t.Run(password, func(t *testing.T) {
			testAdminBootstrapLogin(t, "admin@orvix.email", password, true)
		})
	}
}

func TestAdminBootstrapCreatesCoreMailFoldersOnFreshDatabase(t *testing.T) {
	const (
		email    = "admin@orvix.email"
		password = "AdminPassword123!"
	)
	t.Setenv("ORVIX_ADMIN_EMAIL", email)
	t.Setenv("ORVIX_ADMIN_PASSWORD_B64", base64.StdEncoding.EncodeToString([]byte(password)))
	t.Setenv("ORVIX_ADMIN_PASSWORD", "wrong-plain-fallback")

	observed, logs := observer.New(zap.WarnLevel)
	logger := zap.New(observed)
	cfg := config.Defaults()
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = t.TempDir() + "/orvix.db?_loc=auto&_busy_timeout=5000&_txlock=immediate"

	db, err := config.NewDatabase(&cfg.Database, logger)
	if err != nil {
		t.Fatalf("database: %v", err)
	}
	if err := models.MigrateAllRaw(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	authenticator, err := auth.NewAuthenticator(&cfg.Auth, db, logger)
	if err != nil {
		t.Fatalf("authenticator: %v", err)
	}
	seedAdminUser(db, authenticator, logger, dbdialect.FromDriver("sqlite"))
	if logs.FilterMessage("failed to provision system folders for admin mailbox").Len() != 0 {
		t.Fatal("admin bootstrap logged system folder provisioning failure")
	}

	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("sql db: %v", err)
	}
	defer sqlDB.Close()

	var mailboxID int64
	if err := sqlDB.QueryRow("SELECT id FROM coremail_mailboxes WHERE email = ? AND is_admin = 1", email).Scan(&mailboxID); err != nil {
		t.Fatalf("admin mailbox not created: %v", err)
	}

	rows, err := sqlDB.Query("SELECT path FROM coremail_folders WHERE mailbox_id = ? ORDER BY path", mailboxID)
	if err != nil {
		t.Fatalf("query system folders: %v", err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			t.Fatalf("scan folder: %v", err)
		}
		got = append(got, path)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("folder rows: %v", err)
	}
	want := []string{"Archive", "Drafts", "INBOX", "Junk", "Sent", "Trash"}
	sort.Strings(got)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("admin mailbox folders = %v, want %v", got, want)
	}
}

func testAdminBootstrapLogin(t *testing.T, email, password string, encoded bool) {
	t.Helper()
	t.Setenv("ORVIX_ADMIN_EMAIL", email)
	if encoded {
		t.Setenv("ORVIX_ADMIN_PASSWORD_B64", base64.StdEncoding.EncodeToString([]byte(password)))
		t.Setenv("ORVIX_ADMIN_PASSWORD", "wrong-plain-fallback")
	} else {
		t.Setenv("ORVIX_ADMIN_PASSWORD_B64", "")
		t.Setenv("ORVIX_ADMIN_PASSWORD", password)
	}

	logger := zap.NewNop()
	cfg := config.Defaults()
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = t.TempDir() + "/orvix.db?_loc=auto&_busy_timeout=5000&_txlock=immediate"

	db, err := config.NewDatabase(&cfg.Database, logger)
	if err != nil {
		t.Fatalf("database: %v", err)
	}
	if err := models.MigrateAllRaw(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	authenticator, err := auth.NewAuthenticator(&cfg.Auth, db, logger)
	if err != nil {
		t.Fatalf("authenticator: %v", err)
	}
	seedAdminUser(db, authenticator, logger, dbdialect.FromDriver("sqlite"))

	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("sql db: %v", err)
	}
	defer sqlDB.Close()

	var storedHash string
	if err := sqlDB.QueryRow("SELECT password_hash FROM users WHERE email = ?", email).Scan(&storedHash); err != nil {
		t.Fatalf("query seeded user: %v", err)
	}
	if storedHash == "" {
		t.Fatal("seeded user has empty password_hash")
	}
	if !authenticator.VerifyPassword(password, storedHash) {
		t.Fatalf("authenticator.VerifyPassword failed immediately after seed for user %s", email)
	}

	var count int
	if err := sqlDB.QueryRow("SELECT COUNT(*) FROM users WHERE email = ?", email).Scan(&count); err != nil {
		t.Fatalf("count users: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected one bootstrapped admin user, got %d", count)
	}

	reg := modules.NewRegistry(logger)
	ff := license.NewFeatureFlags(logger)
	router := api.NewRouter(cfg, authenticator, logger, db, reg, ff, nil)
	payload, err := json.Marshal(map[string]string{"email": email, "password": password})
	if err != nil {
		t.Fatalf("marshal login payload: %v", err)
	}
	body := strings.NewReader(string(payload))
	req := httptest.NewRequest("POST", "/admin/login", body)
	req.Header.Set("Content-Type", "application/json")
	resp, err := router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("login request: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("expected login 200, got %d", resp.StatusCode)
	}
	var loginResp struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&loginResp); err != nil {
		t.Fatalf("decode login response: %v", err)
	}
	if loginResp.AccessToken == "" {
		t.Fatal("expected access token in login response")
	}

	req = httptest.NewRequest("GET", "/api/v1/me", nil)
	req.Header.Set("Authorization", "Bearer "+loginResp.AccessToken)
	resp, err = router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("me request: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("expected me 200, got %d", resp.StatusCode)
	}
}

// TestAdminBootstrapRefusesInconsistentHash simulates the
// production failure mode "first login works, subsequent
// fail" at the unit level. We seed a valid user, then
// corrupt the stored password_hash directly in the database
// to a string that bcrypt will never accept. The runtime's
// post-insert verify guard must detect the mismatch on
// re-bootstrap (when the env is still present) and refuse
// to keep the broken row instead of silently returning.
func TestAdminBootstrapRefusesInconsistentHash(t *testing.T) {
	const (
		email    = "admin@orvix.email"
		password = "MaghaghaMos086"
	)
	t.Setenv("ORVIX_ADMIN_EMAIL", email)
	t.Setenv("ORVIX_ADMIN_PASSWORD_B64", base64.StdEncoding.EncodeToString([]byte(password)))
	t.Setenv("ORVIX_ADMIN_PASSWORD", "wrong-plain-fallback")

	logger := zap.NewNop()
	cfg := config.Defaults()
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = t.TempDir() + "/orvix.db?_loc=auto&_busy_timeout=5000&_txlock=immediate"

	db, err := config.NewDatabase(&cfg.Database, logger)
	if err != nil {
		t.Fatalf("database: %v", err)
	}
	if err := models.MigrateAllRaw(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	authenticator, err := auth.NewAuthenticator(&cfg.Auth, db, logger)
	if err != nil {
		t.Fatalf("authenticator: %v", err)
	}
	seedAdminUser(db, authenticator, logger, dbdialect.FromDriver("sqlite"))

	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("sql db: %v", err)
	}
	defer sqlDB.Close()

	// Corrupt the stored hash with a well-formed bcrypt prefix
	// but a payload that no password can match. This is what
	// would happen if, for example, the database was restored
	// from an older snapshot whose hash was generated with a
	// different cost.
	corruptHash := "$2a$10$abcdefghijklmnopqrstuuozDVAVLAmclE3j/9pjUOH6K8RuEX2Cu"
	if _, err := sqlDB.Exec("UPDATE users SET password_hash = ? WHERE email = ?", corruptHash, email); err != nil {
		t.Fatalf("corrupt hash: %v", err)
	}

	// Re-run seedAdminUser with the same env. The user count
	// is already 1, so we go down the "user exists" branch.
	// The new guard must detect that the stored hash does not
	// verify the env password and refuse to keep the row.
	seedAdminUser(db, authenticator, logger, dbdialect.FromDriver("sqlite"))

	// The hash must still be the corrupt one — we did NOT
	// silently overwrite it. If the guard were missing, a
	// future code change could decide to "helpfully" re-hash
	// the password and the user would never know.
	var after string
	if err := sqlDB.QueryRow("SELECT password_hash FROM users WHERE email = ?", email).Scan(&after); err != nil {
		t.Fatalf("read hash: %v", err)
	}
	if after != corruptHash {
		t.Fatalf("expected guard to leave corrupt hash untouched, got %q", after)
	}
}

func TestBootstrapAdminPostgresIntegration(t *testing.T) {
	if strings.ToLower(strings.TrimSpace(os.Getenv("ORVIX_DB_DRIVER"))) != "postgres" {
		t.Skip("ORVIX_DB_DRIVER must be postgres")
	}
	dsn := strings.TrimSpace(os.Getenv("ORVIX_DB_DSN"))
	if dsn == "" {
		t.Skip("ORVIX_DB_DSN is empty")
	}

	const (
		adminEmail    = "admin@orvix.email"
		adminPassword = "AdminPassword123!"
		tenantDomain  = "orvix.email"
	)
	t.Setenv("ORVIX_ADMIN_EMAIL", adminEmail)
	t.Setenv("ORVIX_ADMIN_PASSWORD_B64", base64.StdEncoding.EncodeToString([]byte(adminPassword)))

	cfg := config.Defaults()
	cfg.Database.Driver = "postgres"
	cfg.Database.DSN = dsn
	cfg.Database.MaxOpen = 5

	logger := zap.NewNop()
	db, err := config.NewDatabase(&cfg.Database, logger)
	if err != nil {
		t.Fatalf("connect to postgres: %v", err)
	}

	// Clean any data from previous test runs.
	sqlDB, _ := db.DB()
	sqlDB.Exec("DELETE FROM coremail_folders; DELETE FROM coremail_mailboxes; DELETE FROM users; DELETE FROM tenants; DELETE FROM coremail_domains;")

	if err := models.MigrateAllPostgres(db); err != nil {
		t.Fatalf("MigrateAllPostgres: %v", err)
	}

	authenticator, err := auth.NewAuthenticator(&cfg.Auth, db, logger)
	if err != nil {
		t.Fatalf("authenticator: %v", err)
	}

	hashedPassword, err := authenticator.HashPassword(adminPassword)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}

	dial := dbdialect.FromDriver("postgres")
	err = insertBootstrapAdmin(sqlDB, dial, adminEmail, hashedPassword, tenantDomain, adminPassword, logger)
	if err != nil {
		t.Fatalf("insertBootstrapAdmin: %v", err)
	}

	var userCount, tenantCount, mailboxCount, folderCount int
	sqlDB.QueryRow("SELECT COUNT(*) FROM users").Scan(&userCount)
	sqlDB.QueryRow("SELECT COUNT(*) FROM tenants").Scan(&tenantCount)
	sqlDB.QueryRow("SELECT COUNT(*) FROM coremail_mailboxes").Scan(&mailboxCount)
	sqlDB.QueryRow("SELECT COUNT(*) FROM coremail_folders").Scan(&folderCount)
	t.Logf("users=%d tenants=%d mailboxes=%d folders=%d", userCount, tenantCount, mailboxCount, folderCount)

	if userCount != 1 {
		t.Fatalf("expected 1 user, got %d", userCount)
	}
	if tenantCount != 1 {
		t.Fatalf("expected 1 tenant, got %d", tenantCount)
	}
	if mailboxCount != 1 {
		t.Fatalf("expected 1 mailbox, got %d", mailboxCount)
	}
	if folderCount < 6 {
		t.Fatalf("expected at least 6 system folders, got %d", folderCount)
	}

	var storedHash string
	var active, emailVerified, isAdmin bool
	var mfaEnabled sql.NullBool
	sqlDB.QueryRow("SELECT password_hash, active, email_verified, mfa_enabled FROM users WHERE email = $1", adminEmail).Scan(&storedHash, &active, &emailVerified, &mfaEnabled)
	if storedHash == "" {
		t.Fatal("stored password_hash is empty")
	}
	if !active {
		t.Fatal("active should be true")
	}
	if !emailVerified {
		t.Fatal("email_verified should be true")
	}
	if !authenticator.VerifyPassword(adminPassword, storedHash) {
		t.Fatal("password verification failed")
	}

	sqlDB.QueryRow("SELECT is_admin FROM coremail_mailboxes WHERE email = $1", adminEmail).Scan(&isAdmin)
	if !isAdmin {
		t.Fatal("is_admin should be true")
	}

	selectors := []string{"INBOX", "Sent", "Drafts", "Trash", "Junk", "Archive"}
	for _, path := range selectors {
		var fid int
		err := sqlDB.QueryRow("SELECT id FROM coremail_folders WHERE path = $1 AND mailbox_id = (SELECT id FROM coremail_mailboxes WHERE email = $2)", path, adminEmail).Scan(&fid)
		if err != nil {
			t.Errorf("system folder %q not found: %v", path, err)
		}
	}
}

// freePort returns an ephemeral TCP port for test listeners.
func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("free port: %v", err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// TestStartupPostgresRuntime exercises the exact production bootstrap
// sequence from main() against a live PostgreSQL database.
//
// Architecture note: CoreMail operational storage remains SQLite-only.
// config.Defaults() leaves CoreMail.Enabled=false, so the production
// bootstrap on a PostgreSQL metadata database is the supported hybrid
// configuration: metadata (tenants, users, domains, mailboxes, etc.) in
// PostgreSQL, CoreMail message/queue storage in SQLite. Enabling CoreMail
// on PostgreSQL would fail because storage.Tables()/Indexes() use SQLite
// AUTOINCREMENT syntax. This test documents that the hybrid path starts,
// serves health, and admin login succeeds.
func TestStartupPostgresRuntime(t *testing.T) {
	if strings.ToLower(strings.TrimSpace(os.Getenv("ORVIX_DB_DRIVER"))) != "postgres" {
		t.Skip("ORVIX_DB_DRIVER must be postgres")
	}
	dsn := strings.TrimSpace(os.Getenv("ORVIX_DB_DSN"))
	if dsn == "" {
		t.Skip("ORVIX_DB_DSN is empty")
	}

	const (
		adminEmail    = "admin@runtime.test"
		adminPassword = "RuntimePassword123!"
	)
	t.Setenv("ORVIX_ADMIN_EMAIL", adminEmail)
	t.Setenv("ORVIX_ADMIN_PASSWORD_B64", base64.StdEncoding.EncodeToString([]byte(adminPassword)))

	logger := zap.NewNop()
	cfg := config.Defaults()
	cfg.SetLogger(logger)
	cfg.Database.Driver = "postgres"
	cfg.Database.DSN = dsn
	cfg.Database.MaxOpen = 5
	cfg.Database.MaxIdle = 2
	cfg.Database.MaxLifetime = 300

	// Isolate runtime: use an isolated admin port and a temp mailstore path.
	// CoreMail is left disabled (default). The metadata runtime starts on
	// PostgreSQL; CoreMail operational storage would require a separate
	// SQLite database and is therefore not exercised here.
	cfg.Server.Host = "127.0.0.1"
	cfg.Server.AdminPort = freePort(t)
	cfg.CoreMail.DataPath = t.TempDir()
	cfg.CoreMail.MailStorePath = cfg.CoreMail.DataPath

	db, err := config.NewDatabase(&cfg.Database, logger)
	if err != nil {
		t.Fatalf("connect to postgres: %v", err)
	}
	defer func() {
		if sqlDB, err := db.DB(); err == nil {
			sqlDB.Close()
		}
	}()

	// 1. migrateConfiguredDatabase (production path).
	if err := migrateConfiguredDatabase(db, cfg.Database.Driver, logger); err != nil {
		t.Fatalf("migrateConfiguredDatabase: %v", err)
	}
	t.Log("database migrations completed")

	// 2. seedFeatureFlags.
	seedFeatureFlags(db, logger)
	t.Log("feature flags seeded")

	// 3. Build registry and listener registry.
	reg := modules.NewRegistry(logger)
	listenerReg := orvixruntime.NewListenerRegistry()

	// 4. Create authenticator.
	authenticator, err := auth.NewAuthenticator(&cfg.Auth, db, logger)
	if err != nil {
		t.Fatalf("authenticator: %v", err)
	}

	// 5. seedAdminUser.
	seedAdminUser(db, authenticator, logger, dbdialect.FromDriver(cfg.Database.Driver))
	t.Log("admin user seeded")

	// 6. registerModules.
	featureFlags := license.NewFeatureFlags(logger)
	featureFlags.SetTier(license.TierSMB)
	registerModules(reg, cfg, db, logger, featureFlags, listenerReg)
	t.Log("modules registered")

	// 7. InitAll.
	if err := reg.InitAll(cfg, db); err != nil {
		t.Fatalf("InitAll: %v", err)
	}
	t.Log("modules initialized")

	// 8. StartAll.
	if err := reg.StartAll(); err != nil {
		t.Fatalf("StartAll: %v", err)
	}
	t.Log("modules started")

	// 9. Create router.
	router := api.NewRouter(cfg, authenticator, logger, db, reg, featureFlags, nil)

	// 10. Health endpoint.
	req := httptest.NewRequest("GET", "/api/v1/health", nil)
	resp, err := router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("health request: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("expected health 200, got %d", resp.StatusCode)
	}
	t.Log("health endpoint returned 200 OK")

	// 11. Admin login succeeds.
	payload, err := json.Marshal(map[string]string{"email": adminEmail, "password": adminPassword})
	if err != nil {
		t.Fatalf("marshal login payload: %v", err)
	}
	loginReq := httptest.NewRequest("POST", "/admin/login", strings.NewReader(string(payload)))
	loginReq.Header.Set("Content-Type", "application/json")
	loginResp, err := router.App().Test(loginReq, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("login request: %v", err)
	}
	if loginResp.StatusCode != 200 {
		t.Fatalf("expected admin login 200, got %d", loginResp.StatusCode)
	}
	var loginBody struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(loginResp.Body).Decode(&loginBody); err != nil {
		t.Fatalf("decode login response: %v", err)
	}
	if loginBody.AccessToken == "" {
		t.Fatal("expected access token in login response")
	}
	t.Log("admin login succeeded")

	// 12. StopAll cleanly.
	if err := reg.StopAll(); err != nil {
		t.Fatalf("StopAll: %v", err)
	}
	t.Log("modules stopped cleanly")
	t.Log("PostgreSQL runtime startup test PASSED (hybrid architecture: PostgreSQL metadata, CoreMail disabled)")
}

// TestPostgresBackupRestoreAndRuntime validates that a full 10-table
// migration can be dumped with pg_dump, restored into a fresh PostgreSQL
// database with pg_restore, and that the restored database passes the
// same row-count, semantic, sequence, and runtime-startup checks as the
// source.
//
// This test requires:
//   - ORVIX_DB_DRIVER=postgres
//   - ORVIX_DB_DSN set to a PostgreSQL server the test can create/drop databases on
//   - ORVIX_RUN_POSTGRES_BACKUP_TEST=1
//   - pg_dump and pg_restore in PATH
func TestPostgresBackupRestoreAndRuntime(t *testing.T) {
	if strings.ToLower(strings.TrimSpace(os.Getenv("ORVIX_DB_DRIVER"))) != "postgres" {
		t.Skip("ORVIX_DB_DRIVER must be postgres")
	}
	if os.Getenv("ORVIX_RUN_POSTGRES_BACKUP_TEST") != "1" {
		t.Skip("set ORVIX_RUN_POSTGRES_BACKUP_TEST=1 to run backup/restore integration")
	}
	baseDSN := strings.TrimSpace(os.Getenv("ORVIX_DB_DSN"))
	if baseDSN == "" {
		t.Fatal("ORVIX_DB_DSN is empty")
	}

	const adminEmail = "admin@restored.test"
	const adminPassword = "AdminBackupRestore123!"

	pgDump := findPGTool(t, "pg_dump")
	pgRestore := findPGTool(t, "pg_restore")

	// Build isolated source/destination database names.
	srcDB := generateTestDBName("orvix_backup_src")
	dstDB := generateTestDBName("orvix_backup_dst")

	createTestDatabase(t, baseDSN, srcDB)
	defer dropTestDatabase(t, baseDSN, srcDB)
	createTestDatabase(t, baseDSN, dstDB)
	defer dropTestDatabase(t, baseDSN, dstDB)

	srcDSN, err := replaceDSNDatabase(baseDSN, srcDB)
	if err != nil {
		t.Fatalf("build source dsn: %v", err)
	}
	dstDSN, err := replaceDSNDatabase(baseDSN, dstDB)
	if err != nil {
		t.Fatalf("build destination dsn: %v", err)
	}
	t.Logf("backup test source db=%s destination db=%s", srcDB, dstDB)

	// 1. Create and seed SQLite source.
	srcDir := t.TempDir()
	srcPath := filepath.Join(srcDir, "orvix.db")
	cfg := config.Defaults()
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = srcPath + "?_loc=auto&_busy_timeout=5000"
	sqliteGorm, err := config.NewDatabase(&cfg.Database, zap.NewNop())
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := models.MigrateAllRaw(sqliteGorm); err != nil {
		t.Fatalf("migrate sqlite: %v", err)
	}
	sqliteDB, _ := sqliteGorm.DB()
	seed10Tables(t, sqliteDB)
	sqliteDB.Close()

	// 2. Migrate SQLite → source PostgreSQL database.
	code := migrateCommand([]string{
		"--from", "sqlite",
		"--to", "postgres",
		"--sqlite-path", srcPath,
		"--postgres-dsn", srcDSN,
		"--target-schema", "public",
		"--dry-run=false",
		"--skip-confirm",
	})
	if code != 0 {
		t.Fatalf("migration to source db returned %d", code)
	}

	// 3. pg_dump source database to a temporary file.
	dumpFile := filepath.Join(t.TempDir(), "orvix.dump")
	dumpCmd := exec.Command(pgDump,
		"--format=custom",
		"--no-owner",
		"--no-privileges",
		"--file="+dumpFile,
		"--dbname="+srcDSN,
	)
	if out, err := dumpCmd.CombinedOutput(); err != nil {
		t.Fatalf("pg_dump failed: %v\n%s", err, sanitizeOutput(string(out), baseDSN))
	}
	dumpInfo, err := os.Stat(dumpFile)
	if err != nil || dumpInfo.Size() == 0 {
		t.Fatalf("pg_dump produced no output at %s", dumpFile)
	}
	t.Logf("pg_dump wrote %d bytes", dumpInfo.Size())

	// 4. Verify the dump is readable with pg_restore --list.
	listCmd := exec.Command(pgRestore, "--list", dumpFile)
	if out, err := listCmd.CombinedOutput(); err != nil {
		t.Fatalf("pg_restore --list failed: %v\n%s", err, string(out))
	}

	// 5. pg_restore into destination database.
	restoreCmd := exec.Command(pgRestore,
		"--exit-on-error",
		"--no-owner",
		"--no-privileges",
		"--dbname="+dstDSN,
		dumpFile,
	)
	if out, err := restoreCmd.CombinedOutput(); err != nil {
		t.Fatalf("pg_restore failed: %v\n%s", err, sanitizeOutput(string(out), baseDSN))
	}

	// 6. Verify restored counts and semantics.
	dstCfg := config.Defaults()
	dstCfg.Database.Driver = "postgres"
	dstCfg.Database.DSN = dstDSN
	dstCfg.Database.MaxOpen = 1
	dstGorm, err := config.NewDatabase(&dstCfg.Database, zap.NewNop())
	if err != nil {
		t.Fatalf("connect to restored db: %v", err)
	}
	dstSQL, _ := dstGorm.DB()
	defer dstSQL.Close()

	plan := defaultMigrationPlan()
	counts, err := plan.rowCounts(context.Background(), dstSQL)
	if err != nil {
		t.Fatalf("row counts on restored db: %v", err)
	}
	expected := expectedRowCounts()
	for table, want := range expected {
		if got := counts[table]; got != want {
			t.Errorf("restored table %s: expected %d rows, got %d", table, want, got)
		}
	}
	verifyMailboxSemantics(t, dstSQL)

	// 7. Verify sequence synchronization by inserting a row with DEFAULT id.
	var nextID int64
	err = dstSQL.QueryRow(`INSERT INTO tenants (created_at, updated_at, name, slug, domain, plan, active)
		VALUES (NOW(), NOW(), 'Sequence Test', 'sequence-test', 'sequence.test', 'smb', true) RETURNING id`).Scan(&nextID)
	if err != nil {
		t.Fatalf("sequence sync insert failed: %v", err)
	}
	if nextID <= 0 {
		t.Fatalf("sequence sync insert returned invalid id: %d", nextID)
	}
	t.Logf("backup/restore row counts, mailbox semantics, and sequence sync verified (new tenant id=%d)", nextID)

	// 8. Runtime startup against the restored database.
	t.Setenv("ORVIX_ADMIN_EMAIL", adminEmail)
	t.Setenv("ORVIX_ADMIN_PASSWORD_B64", base64.StdEncoding.EncodeToString([]byte(adminPassword)))

	logger := zap.NewNop()
	rtcfg := config.Defaults()
	rtcfg.SetLogger(logger)
	rtcfg.Database.Driver = "postgres"
	rtcfg.Database.DSN = dstDSN
	rtcfg.Server.Host = "127.0.0.1"
	rtcfg.Server.AdminPort = freePort(t)
	rtcfg.CoreMail.DataPath = t.TempDir()
	rtcfg.CoreMail.MailStorePath = rtcfg.CoreMail.DataPath

	db, err := config.NewDatabase(&rtcfg.Database, logger)
	if err != nil {
		t.Fatalf("connect runtime to restored db: %v", err)
	}
	defer func() {
		if sqlDB, err := db.DB(); err == nil {
			sqlDB.Close()
		}
	}()

	if err := migrateConfiguredDatabase(db, rtcfg.Database.Driver, logger); err != nil {
		t.Fatalf("migrateConfiguredDatabase on restored db: %v", err)
	}
	seedFeatureFlags(db, logger)
	reg := modules.NewRegistry(logger)
	listenerReg := orvixruntime.NewListenerRegistry()
	authenticator, err := auth.NewAuthenticator(&rtcfg.Auth, db, logger)
	if err != nil {
		t.Fatalf("authenticator: %v", err)
	}
	seedAdminUser(db, authenticator, logger, dbdialect.FromDriver(rtcfg.Database.Driver))
	ff := license.NewFeatureFlags(logger)
	ff.SetTier(license.TierSMB)
	registerModules(reg, rtcfg, db, logger, ff, listenerReg)
	if err := reg.InitAll(rtcfg, db); err != nil {
		t.Fatalf("InitAll on restored db: %v", err)
	}
	if err := reg.StartAll(); err != nil {
		t.Fatalf("StartAll on restored db: %v", err)
	}
	router := api.NewRouter(rtcfg, authenticator, logger, db, reg, ff, nil)

	req := httptest.NewRequest("GET", "/api/v1/health", nil)
	resp, err := router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("health request on restored db: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("expected health 200 on restored db, got %d", resp.StatusCode)
	}

	payload, _ := json.Marshal(map[string]string{"email": adminEmail, "password": adminPassword})
	loginReq := httptest.NewRequest("POST", "/admin/login", strings.NewReader(string(payload)))
	loginReq.Header.Set("Content-Type", "application/json")
	loginResp, err := router.App().Test(loginReq, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("login request on restored db: %v", err)
	}
	if loginResp.StatusCode != 200 {
		t.Fatalf("expected login 200 on restored db, got %d", loginResp.StatusCode)
	}

	if err := reg.StopAll(); err != nil {
		t.Fatalf("StopAll on restored db: %v", err)
	}
	t.Log("runtime startup against restored database succeeded")
}

// sanitizeOutput removes credential-like substrings from command output so it
// is safe to include in test failure messages.
func sanitizeOutput(out, dsn string) string {
	redacted := redactDSN(dsn)
	// Also strip any line that contains a password= fragment.
	lines := strings.Split(out, "\n")
	filtered := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.Contains(strings.ToLower(line), "password=") {
			filtered = append(filtered, "[line redacted: contains password=]")
			continue
		}
		filtered = append(filtered, line)
	}
	return fmt.Sprintf("dsn=%s\n%s", redacted, strings.Join(filtered, "\n"))
}
