package api

import (
	"database/sql"
	"encoding/json"
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

func TestAdminListEndpointsReturnArraysAndBootstrapRows(t *testing.T) {
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
	_, err = sqlDB.Exec(
		`INSERT INTO coremail_domains (name, tenant_id, status, plan, created_at, updated_at)
		 VALUES ('test.local', 1, 'active', 'enterprise', ?, ?)`,
		now, now,
	)
	if err != nil {
		t.Fatalf("insert coremail domain: %v", err)
	}
	_, err = sqlDB.Exec(
		`INSERT INTO coremail_mailboxes (domain_id, tenant_id, local_part, email, name, password_hash, auth_scheme, status, quota_mb, is_admin, created_at, updated_at)
		 VALUES (1, 1, 'admin', 'admin@test.local', 'Admin', ?, 'argon2id', 'active', 1024, 1, ?, ?)`,
		"$argon2id$v=19$m=1024,t=1,p=1$c2FsdA$aGFzaA", now, now,
	)
	if err != nil {
		t.Fatalf("insert coremail mailbox: %v", err)
	}
	createAdminQueueFixture(t, sqlDB, now)

	router := NewRouter(cfg, authenticator, logger, db, modules.NewRegistry(logger), license.NewFeatureFlags(logger), nil)
	defer router.App().Shutdown()
	token := loginForTest(t, router, "admin@test.local", "TestPassword123!")

	domainsBody := getAdminJSON(t, router, token, "/api/v1/domains")
	var domains []map[string]any
	if err := json.Unmarshal(domainsBody, &domains); err != nil {
		t.Fatalf("domains must be JSON array: %v: %s", err, domainsBody)
	}
	if len(domains) != 1 || domains[0]["domain"] != "test.local" {
		t.Fatalf("expected bootstrap CoreMail domain, got %s", domainsBody)
	}

	usersBody := getAdminJSON(t, router, token, "/api/v1/users")
	var users []map[string]any
	if err := json.Unmarshal(usersBody, &users); err != nil {
		t.Fatalf("users must be JSON array: %v: %s", err, usersBody)
	}
	if len(users) != 1 || users[0]["email"] != "admin@test.local" || users[0]["role"] != "admin" {
		t.Fatalf("expected deduped bootstrap admin user, got %s", usersBody)
	}
	if strings.Contains(string(usersBody), "password") || strings.Contains(string(usersBody), "argon2") {
		t.Fatalf("users response must not expose password material: %s", usersBody)
	}

	queueBody := getAdminJSON(t, router, token, "/api/v1/queue")
	var queue []map[string]any
	if err := json.Unmarshal(queueBody, &queue); err != nil {
		t.Fatalf("queue must be JSON array: %v: %s", err, queueBody)
	}
	if len(queue) != 1 || queue[0]["from"] != "sender@test.local" || queue[0]["to"] != "admin@test.local" || queue[0]["status"] != "pending" {
		t.Fatalf("expected queue entry with sender/recipient/status, got %s", queueBody)
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

func createAdminQueueFixture(t *testing.T, sqlDB *sql.DB, now string) {
	t.Helper()
	_, err := sqlDB.Exec(`CREATE TABLE IF NOT EXISTS coremail_queue (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		from_address TEXT NOT NULL DEFAULT '',
		to_address TEXT NOT NULL DEFAULT '',
		status TEXT NOT NULL DEFAULT 'pending',
		deleted_at DATETIME
	)`)
	if err != nil {
		t.Fatalf("create queue fixture: %v", err)
	}
	_, err = sqlDB.Exec(
		`INSERT INTO coremail_queue (from_address, to_address, status, deleted_at)
		 VALUES ('sender@test.local', 'admin@test.local', 'pending', NULL)`,
	)
	if err != nil {
		t.Fatalf("insert queue fixture: %v", err)
	}
	_ = now
}

func loginForTest(t *testing.T, router *Router, email, password string) string {
	t.Helper()
	payload := `{"username":"` + email + `","password":"` + password + `"}`
	req := httptest.NewRequest("POST", "/admin/login", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	resp, err := router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("login request: %v", err)
	}
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("login expected 200, got %d: %s", resp.StatusCode, body)
	}
	var parsed struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		t.Fatalf("decode login: %v", err)
	}
	if parsed.AccessToken == "" {
		t.Fatal("login did not return access token")
	}
	return parsed.AccessToken
}

func getAdminJSON(t *testing.T, router *Router, token, path string) []byte {
	t.Helper()
	req := httptest.NewRequest("GET", path, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("%s request: %v", path, err)
	}
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		t.Fatalf("%s expected 200, got %d: %s", path, resp.StatusCode, body)
	}
	if string(body) == "null" {
		t.Fatalf("%s must return JSON array, got null", path)
	}
	return body
}
