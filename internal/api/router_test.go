package api

import (
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/auth"
	"github.com/orvix/orvix/internal/config"
	"github.com/orvix/orvix/internal/license"
	"github.com/orvix/orvix/internal/models"
	"github.com/orvix/orvix/internal/modules"
	"go.uber.org/zap"
)

func TestCSPHeader(t *testing.T) {
	app := fiber.New()
	app.Use(securityHeaders())
	app.Get("/test", func(c fiber.Ctx) error {
		return c.SendString("ok")
	})

	req := httptest.NewRequest("GET", "/test", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	csp := resp.Header.Get("Content-Security-Policy")
	if csp == "" {
		t.Fatal("expected CSP header")
	}
	if strings.Contains(csp, "unsafe-inline") {
		t.Fatalf("CSP must not allow unsafe-inline: %s", csp)
	}
	for _, directive := range []string{"script-src 'self'", "style-src 'self'", "base-uri 'self'", "form-action 'self'"} {
		if !strings.Contains(csp, directive) {
			t.Fatalf("expected directive %q in CSP %q", directive, csp)
		}
	}
}

func TestLoginHandlerDoesNotLogPasswordHashMaterial(t *testing.T) {
	sourcePath := filepath.Join("handlers", "handlers.go")
	raw, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatalf("read handlers source: %v", err)
	}
	source := string(raw)
	for _, forbidden := range []string{
		"hash_first_20",
		"hash_prefix",
		"password_hash_prefix",
		"password_hash_len",
		"hash_len",
		"truncateHash",
	} {
		if strings.Contains(source, forbidden) {
			t.Fatalf("login handler must not log password hash material: found %q", forbidden)
		}
	}
}

func TestLoginAcceptsUsernameField(t *testing.T) {
	logger := zap.NewNop()
	cfg := config.Defaults()
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = filepath.Join(t.TempDir(), "orvix.db") + "?_loc=auto&_busy_timeout=5000&_txlock=immediate"
	db, err := config.NewDatabase(&cfg.Database, logger)
	if err != nil {
		t.Fatalf("database: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("sql db: %v", err)
	}
	defer sqlDB.Close()
	if err := models.MigrateAllRaw(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	authenticator, err := auth.NewAuthenticator(&cfg.Auth, db, logger)
	if err != nil {
		t.Fatalf("authenticator: %v", err)
	}
	router := NewRouter(cfg, authenticator, logger, db, modules.NewRegistry(logger), license.NewFeatureFlags(logger), nil)
	defer router.App().Shutdown()

	hashedPw, err := authenticator.HashPassword("TestPassword123!")
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	now := time.Now().UTC().Format("2006-01-02 15:04:05")
	_, err = sqlDB.Exec(
		`INSERT INTO users (created_at, updated_at, email, password_hash, role, tenant_id, active, email_verified)
		 VALUES (?, ?, 'admin@test.local', ?, 'admin', 1, 1, 1)`,
		now, now, hashedPw,
	)
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}

	tests := []struct {
		name string
		body string
		want int
	}{
		{"email field", `{"email":"admin@test.local","password":"TestPassword123!"}`, 200},
		{"username field", `{"username":"admin@test.local","password":"TestPassword123!"}`, 200},
		{"both fields username priority", `{"email":"wrong@test.local","username":"admin@test.local","password":"TestPassword123!"}`, 200},
		{"empty both", `{"email":"","password":""}`, 400},
		{"wrong password", `{"username":"admin@test.local","password":"wrong"}`, 401},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/admin/login", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			resp, err := router.App().Test(req)
			if err != nil {
				t.Fatalf("request: %v", err)
			}
			if resp.StatusCode != tc.want {
				body, _ := io.ReadAll(resp.Body)
				t.Fatalf("expected %d, got %d; body: %s", tc.want, resp.StatusCode, body)
			}
		})
	}
}

func TestAdminUIStaticRoutes(t *testing.T) {
	adminDir := filepath.Join("..", "..", "release", "admin")
	webmailDir := filepath.Join("..", "..", "release", "webmail")

	logger := zap.NewNop()
	cfg := config.Defaults()
	cfg.Server.AdminUIDir = adminDir
	cfg.Server.WebmailUIDir = webmailDir
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = filepath.Join(t.TempDir(), "orvix.db") + "?_loc=auto&_busy_timeout=5000&_txlock=immediate"
	db, err := config.NewDatabase(&cfg.Database, logger)
	if err != nil {
		t.Fatalf("database: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("sql db: %v", err)
	}
	defer sqlDB.Close()
	if err := models.MigrateAllRaw(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	authenticator, err := auth.NewAuthenticator(&cfg.Auth, db, logger)
	if err != nil {
		t.Fatalf("authenticator: %v", err)
	}
	router := NewRouter(cfg, authenticator, logger, db, modules.NewRegistry(logger), license.NewFeatureFlags(logger), nil)
	defer router.App().Shutdown()

	resp, err := router.App().Test(httptest.NewRequest("POST", "/admin/login", strings.NewReader(`{}`)))
	if err != nil {
		t.Fatalf("POST /admin/login request: %v", err)
	}
	if resp.StatusCode == 405 {
		t.Fatal("POST /admin/login must reach API handler, not SPA static route")
	}
	if resp.StatusCode != 400 {
		t.Fatalf("POST /admin/login expected API validation 400, got %d", resp.StatusCode)
	}

	for _, path := range []string{"/admin", "/admin/login"} {
		resp, err = router.App().Test(httptest.NewRequest("GET", path, nil))
		if err != nil {
			t.Fatalf("%s request: %v", path, err)
		}
		if resp.StatusCode != 200 {
			t.Fatalf("%s expected 200, got %d", path, resp.StatusCode)
		}
	}
	for _, tc := range []struct {
		path string
		want string
	}{
		{"/admin/styles.css", ".app-shell"},
		{"/admin/app.js", "/api/v1/auth/login"},
	} {
		resp, err := router.App().Test(httptest.NewRequest("GET", tc.path, nil))
		if err != nil {
			t.Fatalf("%s request: %v", tc.path, err)
		}
		if resp.StatusCode != 200 {
			t.Fatalf("%s expected 200, got %d", tc.path, resp.StatusCode)
		}
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("%s body: %v", tc.path, err)
		}
		if !strings.Contains(string(body), tc.want) {
			t.Fatalf("%s missing %q", tc.path, tc.want)
		}
	}
	resp, err = router.App().Test(httptest.NewRequest("HEAD", "/admin", nil))
	if err != nil {
		t.Fatalf("HEAD /admin request: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("HEAD /admin expected 200, got %d", resp.StatusCode)
	}

	for _, path := range []string{"/webmail", "/webmail/inbox"} {
		resp, err = router.App().Test(httptest.NewRequest("GET", path, nil))
		if err != nil {
			t.Fatalf("%s request: %v", path, err)
		}
		if resp.StatusCode != 200 {
			t.Fatalf("%s expected 200, got %d", path, resp.StatusCode)
		}
	}
}
