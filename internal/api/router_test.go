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
	if _, ok := domains[0]["mailbox_count"]; !ok {
		t.Fatalf("domains response must include mailbox_count field: %s", domainsBody)
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
	if _, ok := users[0]["is_admin"]; !ok {
		t.Fatalf("users response must include is_admin field: %s", usersBody)
	}
	if _, ok := users[0]["status"]; !ok {
		t.Fatalf("users response must include status field: %s", usersBody)
	}
	if _, ok := users[0]["mailbox_id"]; !ok {
		t.Fatalf("users response must include mailbox_id field: %s", usersBody)
	}
	if users[0]["mailbox_id"] == nil {
		t.Fatalf("admin user must have non-null mailbox_id since coremail_mailboxes row exists: %s", usersBody)
	}
	if _, ok := users[0]["user_id"]; !ok {
		t.Fatalf("users response must include user_id field: %s", usersBody)
	}
	if users[0]["user_id"] == nil {
		t.Fatalf("admin user must have non-null user_id since users row exists: %s", usersBody)
	}
	if _, ok := users[0]["id"]; ok {
		t.Fatalf("users response must not include ambiguous id field: %s", usersBody)
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
	adminJSBody := func() []byte {
		req := httptest.NewRequest("GET", "/admin/app.js", nil)
		resp, err := router.App().Test(req)
		if err != nil {
			t.Fatalf("admin/app.js request: %v", err)
		}
		if resp.StatusCode != 200 {
			t.Fatalf("admin/app.js expected 200, got %d", resp.StatusCode)
		}
		body, _ := io.ReadAll(resp.Body)
		return body
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
	jsBody := adminJSBody()
	if !strings.Contains(string(jsBody), "mailbox_id") {
		t.Fatalf("admin app.js must contain mailbox_id")
	}
	if strings.Contains(string(jsBody), `data-id="`) {
		t.Fatalf("admin app.js must not build action URLs from row.id: found data-id")
	}
	if !strings.Contains(string(jsBody), "data-mailbox-id") {
		t.Fatalf("admin app.js must use data-mailbox-id for action URLs")
	}
	if !strings.Contains(string(jsBody), "No mailbox record") {
		t.Fatalf("admin app.js must show non-action state for user-only rows")
	}
	if !strings.Contains(string(jsBody), "mb-action") {
		t.Fatalf("admin app.js must have mailbox action controls")
	}
	if strings.Contains(string(jsBody), "RC1") || strings.Contains(string(jsBody), "Clean Path") {
		t.Fatalf("admin app.js must not contain RC1 or Clean Path branding")
	}
	if !strings.Contains(string(jsBody), "dm-action") {
		t.Fatalf("admin app.js must have domain action controls")
	}
	if !strings.Contains(string(jsBody), "add-domain-btn") {
		t.Fatalf("admin app.js must have add domain button")
	}
	if !strings.Contains(string(jsBody), "mailbox_count") {
		t.Fatalf("admin app.js must reference mailbox_count for domain rows")
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

func TestDomainManagement(t *testing.T) {
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

	now := time.Now().UTC().Format("2006-01-02 15:04:05")
	hashedPw, _ := authenticator.HashPassword("TestPassword123!")
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
		t.Fatalf("insert domain: %v", err)
	}
	// Insert a mailbox for test.local so delete-with-mailboxes test works
	testHash := "$argon2id$v=19$m=1024,t=1,p=1$c2FsdA$aGFzaA"
	_, err = sqlDB.Exec(
		`INSERT INTO coremail_mailboxes (domain_id, tenant_id, local_part, email, name, password_hash, auth_scheme, status, quota_mb, is_admin, created_at, updated_at)
		 VALUES (1, 1, 'admin', 'admin@test.local', 'Admin', ?, 'argon2id', 'active', 1024, 1, ?, ?)`,
		testHash, now, now,
	)
	if err != nil {
		t.Fatalf("insert mailbox: %v", err)
	}

	token := loginForTest(t, router, "admin@test.local", "TestPassword123!")
	csrfToken := getCSRFTokenForTest(t, router, token)

	// CREATE DOMAIN
	t.Run("create domain success", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/api/v1/domains", strings.NewReader(`{"name":"example.com"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Cookie", "csrf_token="+csrfToken)
		req.Header.Set("X-CSRF-Token", csrfToken)
		resp, err := router.App().Test(req)
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		if resp.StatusCode != 201 {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("expected 201, got %d: %s", resp.StatusCode, body)
		}
		body, _ := io.ReadAll(resp.Body)
		if !strings.Contains(string(body), `"domain":"example.com"`) {
			t.Fatalf("response must include domain: %s", body)
		}
		if !strings.Contains(string(body), `"status":"active"`) {
			t.Fatalf("response must include status: %s", body)
		}
	})

	t.Run("duplicate domain rejected", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/api/v1/domains", strings.NewReader(`{"name":"example.com"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Cookie", "csrf_token="+csrfToken)
		req.Header.Set("X-CSRF-Token", csrfToken)
		resp, err := router.App().Test(req)
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		if resp.StatusCode != 409 {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("expected 409, got %d: %s", resp.StatusCode, body)
		}
	})

	t.Run("invalid domain rejected", func(t *testing.T) {
		tests := []struct {
			name    string
			payload string
		}{
			{"empty", `{"name":""}`},
			{"single label", `{"name":"localhost"}`},
			{"spaces", `{"name":"has spaces.com"}`},
			{"wildcard", `{"name":"*.example.org"}`},
			{"http protocol", `{"name":"http://example.com"}`},
			{"https protocol", `{"name":"https://example.com"}`},
			{"scheme separator", `{"name":"example.com://foo"}`},
			{"trailing slash", `{"name":"example.com/"}`},
			{"path segment", `{"name":"example.com/path"}`},
		}
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				req := httptest.NewRequest("POST", "/api/v1/domains", strings.NewReader(tc.payload))
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Authorization", "Bearer "+token)
				req.Header.Set("Cookie", "csrf_token="+csrfToken)
				req.Header.Set("X-CSRF-Token", csrfToken)
				resp, err := router.App().Test(req)
				if err != nil {
					t.Fatalf("request: %v", err)
				}
				if resp.StatusCode != 400 {
					body, _ := io.ReadAll(resp.Body)
					t.Fatalf("expected 400 for %s, got %d: %s", tc.name, resp.StatusCode, body)
				}
			})
		}
	})

	t.Run("trailing slash domain rejected as invalid", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/api/v1/domains", strings.NewReader(`{"name":"example.com/"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Cookie", "csrf_token="+csrfToken)
		req.Header.Set("X-CSRF-Token", csrfToken)
		resp, err := router.App().Test(req)
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		if resp.StatusCode != 400 {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("expected 400 (trailing slash rejected), got %d: %s", resp.StatusCode, body)
		}
	})

	// DISABLE DOMAIN
	t.Run("disable domain success", func(t *testing.T) {
		req := httptest.NewRequest("PATCH", "/api/v1/domains/example.com/status", strings.NewReader(`{"status":"suspended"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Cookie", "csrf_token="+csrfToken)
		req.Header.Set("X-CSRF-Token", csrfToken)
		resp, err := router.App().Test(req)
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		if resp.StatusCode != 200 {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
		}
		body, _ := io.ReadAll(resp.Body)
		if !strings.Contains(string(body), `"status":"suspended"`) {
			t.Fatalf("response must include new status: %s", body)
		}
	})

	// ENABLE DOMAIN
	t.Run("enable domain success", func(t *testing.T) {
		req := httptest.NewRequest("PATCH", "/api/v1/domains/example.com/status", strings.NewReader(`{"status":"active"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Cookie", "csrf_token="+csrfToken)
		req.Header.Set("X-CSRF-Token", csrfToken)
		resp, err := router.App().Test(req)
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		if resp.StatusCode != 200 {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
		}
	})

	// DOMAIN NOT FOUND
	t.Run("missing domain returns 404", func(t *testing.T) {
		req := httptest.NewRequest("PATCH", "/api/v1/domains/nonexistent.com/status", strings.NewReader(`{"status":"suspended"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Cookie", "csrf_token="+csrfToken)
		req.Header.Set("X-CSRF-Token", csrfToken)
		resp, err := router.App().Test(req)
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		if resp.StatusCode != 404 {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("expected 404, got %d: %s", resp.StatusCode, body)
		}
	})

	// DELETE EMPTY DOMAIN
	t.Run("delete empty domain success", func(t *testing.T) {
		req := httptest.NewRequest("DELETE", "/api/v1/domains/example.com", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Cookie", "csrf_token="+csrfToken)
		req.Header.Set("X-CSRF-Token", csrfToken)
		resp, err := router.App().Test(req)
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		if resp.StatusCode != 200 {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
		}
	})

	// DELETE DOMAIN WITH MAILBOXES REJECTED
	t.Run("delete domain with mailboxes rejected", func(t *testing.T) {
		req := httptest.NewRequest("DELETE", "/api/v1/domains/test.local", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Cookie", "csrf_token="+csrfToken)
		req.Header.Set("X-CSRF-Token", csrfToken)
		resp, err := router.App().Test(req)
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		if resp.StatusCode != 409 {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("expected 409, got %d: %s", resp.StatusCode, body)
		}
		body, _ := io.ReadAll(resp.Body)
		if !strings.Contains(string(body), "mailboxes") {
			t.Fatalf("expected mailboxes error, got %s", body)
		}
	})

	// CSRF REJECTION
	t.Run("create domain missing CSRF rejected", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/api/v1/domains", strings.NewReader(`{"name":"nocsrfdomain.com"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := router.App().Test(req)
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		if resp.StatusCode != 403 {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("expected 403, got %d: %s", resp.StatusCode, body)
		}
	})

	t.Run("domain status missing CSRF rejected", func(t *testing.T) {
		req := httptest.NewRequest("PATCH", "/api/v1/domains/test.local/status", strings.NewReader(`{"status":"suspended"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := router.App().Test(req)
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		if resp.StatusCode != 403 {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("expected 403, got %d: %s", resp.StatusCode, body)
		}
	})

	t.Run("delete domain invalid CSRF rejected", func(t *testing.T) {
		req := httptest.NewRequest("DELETE", "/api/v1/domains/test.local", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Cookie", "csrf_token=invalidtoken")
		req.Header.Set("X-CSRF-Token", "differenttoken")
		resp, err := router.App().Test(req)
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		if resp.StatusCode != 403 {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("expected 403, got %d: %s", resp.StatusCode, body)
		}
	})

	// LIVE MAILBOX COUNT (Blocker 3)
	t.Run("list domains returns live mailbox count", func(t *testing.T) {
		body := getAdminJSON(t, router, token, "/api/v1/domains")
		var domains []map[string]any
		if err := json.Unmarshal(body, &domains); err != nil {
			t.Fatalf("domains must be JSON array: %v: %s", err, body)
		}
		found := false
		for _, d := range domains {
			if d["domain"] == "test.local" {
				found = true
				mc, _ := d["mailbox_count"].(float64)
				if mc < 1 {
					t.Fatalf("test.local must have live mailbox_count >= 1, got %v: %s", d["mailbox_count"], body)
				}
			}
		}
		if !found {
			t.Fatalf("test.local not found in domains list: %s", body)
		}
	})

	t.Run("soft-deleted mailbox not counted in mailbox_count", func(t *testing.T) {
		_, err = sqlDB.Exec("UPDATE coremail_mailboxes SET deleted_at = ? WHERE email = 'admin@test.local'", time.Now().UTC().Format("2006-01-02 15:04:05"))
		if err != nil {
			t.Fatalf("soft-delete mailbox: %v", err)
		}
		body := getAdminJSON(t, router, token, "/api/v1/domains")
		var domains []map[string]any
		if err := json.Unmarshal(body, &domains); err != nil {
			t.Fatalf("domains must be JSON array: %v: %s", err, body)
		}
		for _, d := range domains {
			if d["domain"] == "test.local" {
				mc, _ := d["mailbox_count"].(float64)
				if mc != 0 {
					t.Fatalf("test.local must have mailbox_count 0 after soft-delete, got %v: %s", d["mailbox_count"], body)
				}
			}
		}
		_, err = sqlDB.Exec("UPDATE coremail_mailboxes SET deleted_at = NULL WHERE email = 'admin@test.local'")
		if err != nil {
			t.Fatalf("restore mailbox: %v", err)
		}
	})

	t.Run("delete domain succeeds when only soft-deleted mailboxes remain", func(t *testing.T) {
		_, err = sqlDB.Exec("UPDATE coremail_mailboxes SET deleted_at = ? WHERE email = 'admin@test.local'", time.Now().UTC().Format("2006-01-02 15:04:05"))
		if err != nil {
			t.Fatalf("soft-delete mailbox: %v", err)
		}
		delReq := httptest.NewRequest("DELETE", "/api/v1/domains/test.local", nil)
		delReq.Header.Set("Authorization", "Bearer "+token)
		delReq.Header.Set("Cookie", "csrf_token="+csrfToken)
		delReq.Header.Set("X-CSRF-Token", csrfToken)
		resp, err := router.App().Test(delReq)
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		if resp.StatusCode != 200 {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("expected 200 (delete allowed when only soft-deleted mailboxes), got %d: %s", resp.StatusCode, body)
		}
	})

	// NO PROVISIONED_DOMAINS FALLBACK (Blocker 1)
	t.Run("list domains does not include provisioned_domains-only rows", func(t *testing.T) {
		_, _ = sqlDB.Exec("INSERT INTO provisioned_domains (domain, tenant_id, plan, status, created_at, updated_at) VALUES ('provisioned-only.local', 1, 'smb', 'active', ?, ?)", time.Now().UTC().Format("2006-01-02 15:04:05"), time.Now().UTC().Format("2006-01-02 15:04:05"))
		body := getAdminJSON(t, router, token, "/api/v1/domains")
		if strings.Contains(string(body), "provisioned-only.local") {
			t.Fatalf("provisioned_domains fallback must not appear in ListDomains: %s", body)
		}
	})
}

func TestMailboxManagement(t *testing.T) {
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

	now := time.Now().UTC().Format("2006-01-02 15:04:05")
	hashedPw, _ := authenticator.HashPassword("TestPassword123!")
	_, err = sqlDB.Exec(
		`INSERT INTO users (created_at, updated_at, email, password_hash, role, tenant_id, active, email_verified)
		 VALUES (?, ?, 'admin@test.local', ?, 'admin', 1, 1, 1)`,
		now, now, hashedPw,
	)
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	_, err = sqlDB.Exec(
		`INSERT INTO coremail_domains (name, tenant_id, reseller_id, status, plan, description, max_mailboxes, max_aliases, max_quota_mb, dkim_enabled, dkim_selector, dmarc_enabled, mtasts_enabled, catchall_address, abuse_contact, labels, mailbox_count, created_at, updated_at)
		 VALUES ('test.local', 1, 0, 'active', 'enterprise', '', 100, 50, 1024, 0, '', 0, 0, '', '', '', 0, ?, ?)`,
		now, now,
	)
	if err != nil {
		t.Fatalf("insert domain: %v", err)
	}
	testHash := "$argon2id$v=19$m=1024,t=1,p=1$c2FsdA$aGFzaA"
	_, err = sqlDB.Exec(
		`INSERT INTO coremail_mailboxes (domain_id, tenant_id, local_part, email, name, password_hash, auth_scheme, status, quota_mb, is_admin, created_at, updated_at)
		 VALUES (1, 1, 'admin', 'admin@test.local', 'Admin', ?, 'argon2id', 'active', 1024, 1, ?, ?)`,
		testHash, now, now,
	)
	if err != nil {
		t.Fatalf("insert admin mailbox: %v", err)
	}

	_, err = sqlDB.Exec(
		`INSERT INTO coremail_mailboxes (domain_id, tenant_id, local_part, email, name, password_hash, auth_scheme, status, quota_mb, is_admin, created_at, updated_at)
		 VALUES (1, 1, 'user1', 'user1@test.local', 'User One', ?, 'argon2id', 'active', 1024, 0, ?, ?)`,
		testHash, now, now,
	)
	if err != nil {
		t.Fatalf("insert user mailbox: %v", err)
	}

	// User-only row (no coremail_mailboxes entry) — use high id to avoid collision
	_, err = sqlDB.Exec(
		`INSERT INTO users (id, created_at, updated_at, email, password_hash, role, tenant_id, active, email_verified)
		 VALUES (999, ?, ?, 'legacy@test.local', ?, 'user', 1, 1, 1)`,
		now, now, hashedPw,
	)
	if err != nil {
		t.Fatalf("insert user-only row: %v", err)
	}

	token := loginForTest(t, router, "admin@test.local", "TestPassword123!")
	csrfToken := getCSRFTokenForTest(t, router, token)

	// Verify ListUsers includes identity contract fields
	t.Run("list includes mailbox_id user_id is_admin status", func(t *testing.T) {
		body := getAdminJSON(t, router, token, "/api/v1/users")
		var users []map[string]any
		if err := json.Unmarshal(body, &users); err != nil {
			t.Fatalf("users must be JSON array: %v: %s", err, body)
		}
		if len(users) < 2 {
			t.Fatalf("expected at least 2 users, got %d: %s", len(users), body)
		}
		for _, u := range users {
			if _, ok := u["mailbox_id"]; !ok {
				t.Fatalf("user missing mailbox_id: %s", body)
			}
			if _, ok := u["user_id"]; !ok {
				t.Fatalf("user missing user_id: %s", body)
			}
			if _, ok := u["is_admin"]; !ok {
				t.Fatalf("user missing is_admin: %s", body)
			}
			if _, ok := u["status"]; !ok {
				t.Fatalf("user missing status: %s", body)
			}
			if _, ok := u["id"]; ok {
				t.Fatalf("user response must not include ambiguous id field: %s", body)
			}
		}
		var adminFound bool
		var userFound bool
		for _, u := range users {
			if u["email"] == "admin@test.local" {
				adminFound = true
				if u["is_admin"] != true {
					t.Fatalf("expected admin is_admin=true, got %v", u["is_admin"])
				}
				if u["mailbox_id"] == nil {
					t.Fatalf("admin must have non-null mailbox_id: %s", body)
				}
				if u["user_id"] == nil {
					t.Fatalf("admin must have non-null user_id: %s", body)
				}
			}
			if u["email"] == "user1@test.local" {
				userFound = true
				if u["mailbox_id"] == nil {
					t.Fatalf("user1 must have non-null mailbox_id: %s", body)
				}
				if u["user_id"] != nil {
					t.Fatalf("user1 must have null user_id (no users row): %s", body)
				}
			}
		}
		if !adminFound {
			t.Fatalf("admin user not found in list: %s", body)
		}
		if !userFound {
			t.Fatalf("user1 not found in list: %s", body)
		}
		if strings.Contains(string(body), "password_hash") || strings.Contains(string(body), "argon2id") || strings.Contains(string(body), "$argon2") {
			t.Fatalf("response must not expose password material: %s", body)
		}
	})

	// Verify user-only row identity contract
	t.Run("user-only row has mailbox_id null user_id set status user-only", func(t *testing.T) {
		body := getAdminJSON(t, router, token, "/api/v1/users")
		var users []map[string]any
		if err := json.Unmarshal(body, &users); err != nil {
			t.Fatalf("users must be JSON array: %v: %s", err, body)
		}
		var found bool
		for _, u := range users {
			if u["email"] == "legacy@test.local" {
				found = true
				if u["mailbox_id"] != nil {
					t.Fatalf("user-only row must have null mailbox_id, got %v", u["mailbox_id"])
				}
				uid, ok := u["user_id"].(float64)
				if !ok || uid != 999 {
					t.Fatalf("user-only row must have user_id=999, got %v (type %T)", u["user_id"], u["user_id"])
				}
				if u["status"] != "user-only" {
					t.Fatalf("user-only row must have status='user-only', got %v", u["status"])
				}
			}
		}
		if !found {
			t.Fatalf("user-only row not found in list: %s", body)
		}
	})

	// Password reset using users.id (not a mailbox_id) returns 404
	t.Run("password reset with users.id not in coremail_mailboxes returns 404", func(t *testing.T) {
		req := httptest.NewRequest("PATCH", "/api/v1/mailboxes/999/password", strings.NewReader(`{"password":"NewPass123!"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Cookie", "csrf_token="+csrfToken)
		req.Header.Set("X-CSRF-Token", csrfToken)
		resp, err := router.App().Test(req)
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		if resp.StatusCode != 404 {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("expected 404, got %d: %s", resp.StatusCode, body)
		}
	})

	// PASSWORD RESET

	// PASSWORD RESET
	t.Run("password reset success", func(t *testing.T) {
		req := httptest.NewRequest("PATCH", "/api/v1/mailboxes/2/password", strings.NewReader(`{"password":"NewPass123!"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Cookie", "csrf_token="+csrfToken)
		req.Header.Set("X-CSRF-Token", csrfToken)
		resp, err := router.App().Test(req)
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		if resp.StatusCode != 200 {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
		}
		body, _ := io.ReadAll(resp.Body)
		if !strings.Contains(string(body), `"email":"user1@test.local"`) {
			t.Fatalf("response must include email: %s", body)
		}
		if strings.Contains(string(body), "password_hash") || strings.Contains(string(body), "$argon2") {
			t.Fatalf("response must not expose password hash material: %s", body)
		}
	})

	t.Run("password reset short password rejected", func(t *testing.T) {
		req := httptest.NewRequest("PATCH", "/api/v1/mailboxes/2/password", strings.NewReader(`{"password":"short7"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Cookie", "csrf_token="+csrfToken)
		req.Header.Set("X-CSRF-Token", csrfToken)
		resp, err := router.App().Test(req)
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		if resp.StatusCode != 400 {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("expected 400, got %d: %s", resp.StatusCode, body)
		}
	})

	t.Run("password reset missing mailbox rejected", func(t *testing.T) {
		req := httptest.NewRequest("PATCH", "/api/v1/mailboxes/999/password", strings.NewReader(`{"password":"ValidPass123!"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Cookie", "csrf_token="+csrfToken)
		req.Header.Set("X-CSRF-Token", csrfToken)
		resp, err := router.App().Test(req)
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		if resp.StatusCode != 404 {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("expected 404, got %d: %s", resp.StatusCode, body)
		}
	})

	// STATUS
	t.Run("status disable non-admin success", func(t *testing.T) {
		req := httptest.NewRequest("PATCH", "/api/v1/mailboxes/2/status", strings.NewReader(`{"status":"suspended"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Cookie", "csrf_token="+csrfToken)
		req.Header.Set("X-CSRF-Token", csrfToken)
		resp, err := router.App().Test(req)
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		if resp.StatusCode != 200 {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
		}
		body, _ := io.ReadAll(resp.Body)
		if !strings.Contains(string(body), `"status":"suspended"`) {
			t.Fatalf("response must include new status: %s", body)
		}
	})

	t.Run("status enable non-admin success", func(t *testing.T) {
		req := httptest.NewRequest("PATCH", "/api/v1/mailboxes/2/status", strings.NewReader(`{"status":"active"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Cookie", "csrf_token="+csrfToken)
		req.Header.Set("X-CSRF-Token", csrfToken)
		resp, err := router.App().Test(req)
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		if resp.StatusCode != 200 {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
		}
		body, _ := io.ReadAll(resp.Body)
		if !strings.Contains(string(body), `"status":"active"`) {
			t.Fatalf("response must include new status: %s", body)
		}
	})

	t.Run("disable admin mailbox rejected", func(t *testing.T) {
		req := httptest.NewRequest("PATCH", "/api/v1/mailboxes/1/status", strings.NewReader(`{"status":"suspended"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Cookie", "csrf_token="+csrfToken)
		req.Header.Set("X-CSRF-Token", csrfToken)
		resp, err := router.App().Test(req)
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		if resp.StatusCode != 403 {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("expected 403, got %d: %s", resp.StatusCode, body)
		}
	})

	// DELETE
	t.Run("delete non-admin mailbox success", func(t *testing.T) {
		req := httptest.NewRequest("DELETE", "/api/v1/mailboxes/2", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Cookie", "csrf_token="+csrfToken)
		req.Header.Set("X-CSRF-Token", csrfToken)
		resp, err := router.App().Test(req)
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		if resp.StatusCode != 200 {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
		}
		body, _ := io.ReadAll(resp.Body)
		if !strings.Contains(string(body), `"email":"user1@test.local"`) {
			t.Fatalf("response must include email: %s", body)
		}
	})

	t.Run("delete admin mailbox rejected", func(t *testing.T) {
		req := httptest.NewRequest("DELETE", "/api/v1/mailboxes/1", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Cookie", "csrf_token="+csrfToken)
		req.Header.Set("X-CSRF-Token", csrfToken)
		resp, err := router.App().Test(req)
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		if resp.StatusCode != 403 {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("expected 403, got %d: %s", resp.StatusCode, body)
		}
	})

	t.Run("delete missing mailbox rejected", func(t *testing.T) {
		req := httptest.NewRequest("DELETE", "/api/v1/mailboxes/999", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Cookie", "csrf_token="+csrfToken)
		req.Header.Set("X-CSRF-Token", csrfToken)
		resp, err := router.App().Test(req)
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		if resp.StatusCode != 404 {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("expected 404, got %d: %s", resp.StatusCode, body)
		}
	})

	// CSRF REJECTION
	t.Run("password reset missing CSRF rejected", func(t *testing.T) {
		req := httptest.NewRequest("PATCH", "/api/v1/mailboxes/2/password", strings.NewReader(`{"password":"NewPass123!"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := router.App().Test(req)
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		if resp.StatusCode != 403 {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("expected 403, got %d: %s", resp.StatusCode, body)
		}
	})

	t.Run("status update missing CSRF rejected", func(t *testing.T) {
		req := httptest.NewRequest("PATCH", "/api/v1/mailboxes/2/status", strings.NewReader(`{"status":"suspended"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := router.App().Test(req)
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		if resp.StatusCode != 403 {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("expected 403, got %d: %s", resp.StatusCode, body)
		}
	})

	t.Run("delete missing CSRF rejected", func(t *testing.T) {
		req := httptest.NewRequest("DELETE", "/api/v1/mailboxes/2", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := router.App().Test(req)
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		if resp.StatusCode != 403 {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("expected 403, got %d: %s", resp.StatusCode, body)
		}
	})

	t.Run("password reset invalid CSRF rejected", func(t *testing.T) {
		req := httptest.NewRequest("PATCH", "/api/v1/mailboxes/2/password", strings.NewReader(`{"password":"NewPass123!"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Cookie", "csrf_token=invalidtoken")
		req.Header.Set("X-CSRF-Token", "differenttoken")
		resp, err := router.App().Test(req)
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		if resp.StatusCode != 403 {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("expected 403, got %d: %s", resp.StatusCode, body)
		}
	})
}

func TestCreateMailboxEndpoint(t *testing.T) {
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

	now := time.Now().UTC().Format("2006-01-02 15:04:05")
	hashedPw, _ := authenticator.HashPassword("TestPassword123!")
	_, err = sqlDB.Exec(
		`INSERT INTO users (created_at, updated_at, email, password_hash, role, tenant_id, active, email_verified)
		 VALUES (?, ?, 'admin@test.local', ?, 'admin', 1, 1, 1)`,
		now, now, hashedPw,
	)
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	_, err = sqlDB.Exec(
		`INSERT INTO coremail_domains (name, tenant_id, reseller_id, status, plan, description, max_mailboxes, max_aliases, max_quota_mb, dkim_enabled, dkim_selector, dmarc_enabled, mtasts_enabled, catchall_address, abuse_contact, labels, mailbox_count, created_at, updated_at)
		 VALUES ('test.local', 1, 0, 'active', 'enterprise', '', 100, 50, 1024, 0, '', 0, 0, '', '', '', 0, ?, ?)`,
		now, now,
	)
	if err != nil {
		t.Fatalf("insert domain: %v", err)
	}

	token := loginForTest(t, router, "admin@test.local", "TestPassword123!")

	csrfToken := getCSRFTokenForTest(t, router, token)

	t.Run("missing CSRF cookie returns 403", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/api/v1/mailboxes", strings.NewReader(`{"email":"user1@test.local","password":"SecurePass123!"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := router.App().Test(req)
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		if resp.StatusCode != 403 {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("expected 403, got %d: %s", resp.StatusCode, body)
		}
		body, _ := io.ReadAll(resp.Body)
		if !strings.Contains(string(body), "CSRF") && !strings.Contains(string(body), "csrf") {
			t.Fatalf("expected CSRF error message, got %s", body)
		}
	})

	t.Run("invalid CSRF token returns 403", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/api/v1/mailboxes", strings.NewReader(`{"email":"user1@test.local","password":"SecurePass123!"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Cookie", "csrf_token=invalidtoken")
		req.Header.Set("X-CSRF-Token", "differenttoken")
		resp, err := router.App().Test(req)
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		if resp.StatusCode != 403 {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("expected 403, got %d: %s", resp.StatusCode, body)
		}
		body, _ := io.ReadAll(resp.Body)
		if !strings.Contains(string(body), "CSRF") && !strings.Contains(string(body), "csrf") {
			t.Fatalf("expected CSRF error message, got %s", body)
		}
	})

	tests := []struct {
		name       string
		payload    string
		wantStatus int
		wantErr    string
	}{
		{
			name:       "success",
			payload:    `{"email":"user1@test.local","password":"SecurePass123!"}`,
			wantStatus: 201,
		},
		{
			name:       "duplicate email",
			payload:    `{"email":"user1@test.local","password":"AnotherPass456!"}`,
			wantStatus: 409,
			wantErr:    "already exists",
		},
		{
			name:       "unknown domain",
			payload:    `{"email":"user@nonexistent.local","password":"SecurePass123!"}`,
			wantStatus: 404,
			wantErr:    "domain not found",
		},
		{
			name:       "short password",
			payload:    `{"email":"user2@test.local","password":"short7"}`,
			wantStatus: 400,
			wantErr:    "at least 8 characters",
		},
		{
			name:       "missing email",
			payload:    `{"password":"SecurePass123!"}`,
			wantStatus: 400,
			wantErr:    "required",
		},
		{
			name:       "missing password",
			payload:    `{"email":"user3@test.local"}`,
			wantStatus: 400,
			wantErr:    "required",
		},
		{
			name:       "invalid email format",
			payload:    `{"email":"notanemail","password":"SecurePass123!"}`,
			wantStatus: 400,
			wantErr:    "invalid email",
		},
		{
			name:       "invalid email whitespace",
			payload:    `{"email":"bad user@test.local","password":"SecurePass123!"}`,
			wantStatus: 400,
			wantErr:    "invalid email",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/api/v1/mailboxes", strings.NewReader(tc.payload))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+token)
			req.Header.Set("Cookie", "csrf_token="+csrfToken)
			req.Header.Set("X-CSRF-Token", csrfToken)
			resp, err := router.App().Test(req)
			if err != nil {
				t.Fatalf("request: %v", err)
			}
			if resp.StatusCode != tc.wantStatus {
				body, _ := io.ReadAll(resp.Body)
				t.Fatalf("expected %d, got %d: %s", tc.wantStatus, resp.StatusCode, body)
			}
			if tc.wantErr != "" {
				body, _ := io.ReadAll(resp.Body)
				if !strings.Contains(string(body), tc.wantErr) {
					t.Fatalf("expected error containing %q, got %s", tc.wantErr, body)
				}
			}
		})
	}

	t.Run("response does not expose password_hash", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/api/v1/mailboxes", strings.NewReader(`{"email":"safe@test.local","password":"SafePass123!"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Cookie", "csrf_token="+csrfToken)
		req.Header.Set("X-CSRF-Token", csrfToken)
		resp, err := router.App().Test(req)
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		if resp.StatusCode != 201 {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("expected 201, got %d: %s", resp.StatusCode, body)
		}
		body, _ := io.ReadAll(resp.Body)
		if strings.Contains(string(body), "password_hash") || strings.Contains(string(body), "argon2id") || strings.Contains(string(body), "$argon2") {
			t.Fatalf("response must not expose password_hash: %s", body)
		}
		if !strings.Contains(string(body), `"email":"safe@test.local"`) {
			t.Fatalf("response must include email: %s", body)
		}
		if !strings.Contains(string(body), `"status":"active"`) {
			t.Fatalf("response must include status: %s", body)
		}
	})
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

func getCSRFTokenForTest(t *testing.T, router *Router, token string) string {
	t.Helper()
	req := httptest.NewRequest("GET", "/api/v1/csrf-token", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("csrf token request: %v", err)
	}
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("csrf token expected 200, got %d: %s", resp.StatusCode, body)
	}
	var data struct {
		CSRFToken string `json:"csrf_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		t.Fatalf("decode csrf token: %v", err)
	}
	if data.CSRFToken == "" {
		t.Fatal("csrf token endpoint returned empty token")
	}
	return data.CSRFToken
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
