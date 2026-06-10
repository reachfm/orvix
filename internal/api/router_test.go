package api

import (
	"io"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

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
