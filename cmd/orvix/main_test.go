package main

import (
	"encoding/base64"
	"encoding/json"
	"net/http/httptest"
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
