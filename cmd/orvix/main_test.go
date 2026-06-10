package main

import (
	"encoding/base64"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/api"
	"github.com/orvix/orvix/internal/auth"
	"github.com/orvix/orvix/internal/config"
	"github.com/orvix/orvix/internal/license"
	"github.com/orvix/orvix/internal/models"
	"github.com/orvix/orvix/internal/modules"
	"go.uber.org/zap"
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
	seedAdminUser(db, authenticator, logger)

	var count int
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("sql db: %v", err)
	}
	defer sqlDB.Close()
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
