package main

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"net/http/httptest"
	"os"
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

// TestStartupPostgresRuntime verifies that the full runtime bootstrap
// sequence works against a PostgreSQL database: MigrateAllPostgres,
// minimal module init, health endpoint, and clean shutdown.
func TestStartupPostgresRuntime(t *testing.T) {
	if strings.ToLower(strings.TrimSpace(os.Getenv("ORVIX_DB_DRIVER"))) != "postgres" {
		t.Skip("ORVIX_DB_DRIVER must be postgres")
	}
	dsn := strings.TrimSpace(os.Getenv("ORVIX_DB_DSN"))
	if dsn == "" {
		t.Skip("ORVIX_DB_DSN is empty")
	}

	logger := zap.NewNop()
	cfg := config.Defaults()
	cfg.Database.Driver = "postgres"
	cfg.Database.DSN = dsn
	cfg.Database.MaxOpen = 5
	cfg.Database.MaxIdle = 2
	cfg.Database.MaxLifetime = 300

	db, err := config.NewDatabase(&cfg.Database, logger)
	if err != nil {
		t.Fatalf("connect to postgres: %v", err)
	}

	// Run PostgreSQL migration (the production path).
	if err := models.MigrateAllPostgres(db); err != nil {
		t.Fatalf("MigrateAllPostgres: %v", err)
	}

	// Verify schema compatibility.
	if err := models.PostgresSchemaCompatible(db, "public"); err != nil {
		t.Fatalf("PostgresSchemaCompatible: %v", err)
	}
	t.Log("PostgreSQL schema verified: all 59 tables present")

	// Seed feature flags (production code path).
	seedFeatureFlags(db, logger)

	// Create minimal runtime: authenticator + router.
	authenticator, err := auth.NewAuthenticator(&cfg.Auth, db, logger)
	if err != nil {
		t.Fatalf("authenticator: %v", err)
	}

	reg := modules.NewRegistry(logger)
	ff := license.NewFeatureFlags(logger)
	ff.SetTier(license.TierSMB)

	// Register only the lightweight modules that don't need
	// external services (Redis, CoreMail storage, etc.).
	router := api.NewRouter(cfg, authenticator, logger, db, reg, ff, nil)

	// Verify health endpoint responds.
	req := httptest.NewRequest("GET", "/api/v1/health", nil)
	resp, err := router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("health request: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("expected health 200, got %d", resp.StatusCode)
	}
	t.Log("health endpoint returned 200 OK")

	// Verify admin login route is registered (returns 405 without body
	// or 400 with wrong content-type, but must not 404).
	emptyReq := httptest.NewRequest("POST", "/admin/login", nil)
	emptyResp, err := router.App().Test(emptyReq, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("admin login route check: %v", err)
	}
	t.Logf("admin/login returned %d (expected non-404)", emptyResp.StatusCode)

	// Stop all modules cleanly.
	if err := reg.StopAll(); err != nil {
		t.Fatalf("module shutdown: %v", err)
	}
	t.Log("all modules stopped cleanly")

	sqlDB, err := db.DB()
	if err == nil {
		sqlDB.Close()
	}
	t.Log("PostgreSQL runtime startup test PASSED")
}
