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

func TestWebmailServiceWired(t *testing.T) {
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

	// GET /api/v1/webmail/accounts without auth should return 401 (not 503).
	// 503 would mean the webmail service is nil — proving it IS wired.
	req := httptest.NewRequest("GET", "/api/v1/webmail/accounts", nil)
	resp, err := router.App().Test(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if resp.StatusCode == 503 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("webmail service not wired: got 503: %s", body)
	}
	// Must be 401 (unauthorized) because service IS available but auth is missing.
	if resp.StatusCode != 401 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 401 (unauthorized, service available), got %d: %s", resp.StatusCode, body)
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
	if _, ok := queue[0]["id"]; !ok {
		t.Fatalf("queue entry must include id field: %s", queueBody)
	}
	if _, ok := queue[0]["attempts"]; !ok {
		t.Fatalf("queue entry must include attempts field: %s", queueBody)
	}
	if _, ok := queue[0]["message_id"]; !ok {
		t.Fatalf("queue entry must include message_id field: %s", queueBody)
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
	if !strings.Contains(string(jsBody), "q-action") {
		t.Fatalf("admin app.js must have queue action controls (q-action)")
	}
	if !strings.Contains(string(jsBody), "mv-action") {
		t.Fatalf("admin app.js must have mailbox view control (mv-action)")
	}
	if !strings.Contains(string(jsBody), "dv-action") {
		t.Fatalf("admin app.js must have domain view control (dv-action)")
	}
	if !strings.Contains(string(jsBody), "wm-view") {
		t.Fatalf("admin app.js must have webmail view control (wm-view)")
	}
	if !strings.Contains(string(jsBody), `event.target.closest("button.wm-view")`) {
		t.Fatalf("admin app.js must delegate clicks for webmail view buttons")
	}
	if !strings.Contains(string(jsBody), "loadWebmailDetail(Number(mailboxId), email)") {
		t.Fatalf("admin app.js must call loadWebmailDetail with mailbox id and email")
	}
	if !strings.Contains(string(jsBody), `showDetail("webmail-detail")`) {
		t.Fatalf("admin app.js must show the webmail detail view")
	}
	if !strings.Contains(string(jsBody), `detailName === "webmail-detail" ? "webmail"`) {
		t.Fatalf("admin app.js must return from webmail detail to the webmail page")
	}
	if !strings.Contains(string(jsBody), "update check not configured") {
		t.Fatalf("admin app.js must render clean update-feed-not-configured state")
	}
	if !strings.Contains(string(jsBody), "release_notes") {
		t.Fatalf("admin app.js must render release notes from update feed response")
	}
	if !strings.Contains(string(jsBody), "latest_version") || !strings.Contains(string(jsBody), "latest_sha") {
		t.Fatalf("admin app.js must render latest version and SHA from update feed response")
	}
	resp, err = router.App().Test(httptest.NewRequest("GET", "/admin", nil))
	if err != nil {
		t.Fatalf("GET /admin request: %v", err)
	}
	indexBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("GET /admin body: %v", err)
	}
	if !strings.Contains(string(indexBody), `data-detail-view="webmail-detail"`) {
		t.Fatalf("admin index.html must include webmail-detail section")
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

// TestWebmailAssetCORSHeadersReflectAdminOrigin is the
// regression test for the production "webmail frontend is
// broken" symptom. The webmail index.html ships a
// `<script type="module" crossorigin>` tag, which means the
// browser sends every /webmail/assets/* fetch with CORS
// mode active and requires Access-Control-Allow-Origin to
// match the page's own origin. If the admin server's
// allowed_origins does NOT include the admin host that
// serves the webmail, the browser silently drops the
// module, the React app never mounts, and the user sees
// an empty page (the bug). This test configures the admin
// host as a "production-like" value and asserts that the
// CORS response header echoes it back. A change to
// install.sh's write_config that drops the admin host
// from allowed_origins will fail this test.
func TestWebmailAssetCORSHeadersReflectAdminOrigin(t *testing.T) {
	logger := zap.NewNop()
	cfg := config.Defaults()
	cfg.Server.AdminUIDir = filepath.Join("..", "..", "release", "admin")
	cfg.Server.WebmailUIDir = filepath.Join("..", "..", "release", "webmail")
	cfg.Server.AllowedOrigins = []string{
		"https://admin.example.com",
		"http://admin.example.com",
		"https://mail.example.com",
	}
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

	// The browser always sends an Origin header for module
	// script fetches, even on first load. The admin server
	// must respond with Access-Control-Allow-Origin matching
	// the page's own origin. Without it, the module is blocked.
	req := httptest.NewRequest("GET", "/webmail/assets/index-CmhA8wNq.js", nil)
	req.Header.Set("Origin", "https://admin.example.com")
	resp, err := router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("asset: %v", err)
	}
	acao := resp.Header.Get("Access-Control-Allow-Origin")
	if acao != "https://admin.example.com" {
		t.Fatalf("Access-Control-Allow-Origin: want %q, got %q (this is the webmail-CORS regression)", "https://admin.example.com", acao)
	}
}

// TestWebmailAssetCORSRejectsForeignOrigin guards against
// the inverse regression: the admin server must NOT echo
// back a CORS allow-origin for a host that was never
// configured. A wildcard or accidental "*" header would
// break the credentialed cookie contract and allow
// cross-origin admin API access from any site.
func TestWebmailAssetCORSRejectsForeignOrigin(t *testing.T) {
	logger := zap.NewNop()
	cfg := config.Defaults()
	cfg.Server.AdminUIDir = filepath.Join("..", "..", "release", "admin")
	cfg.Server.WebmailUIDir = filepath.Join("..", "..", "release", "webmail")
	cfg.Server.AllowedOrigins = []string{"https://admin.example.com"}
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

	req := httptest.NewRequest("GET", "/webmail/assets/index-CmhA8wNq.js", nil)
	req.Header.Set("Origin", "https://evil.example.com")
	resp, err := router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("asset: %v", err)
	}
	acao := resp.Header.Get("Access-Control-Allow-Origin")
	if acao == "*" || acao == "https://evil.example.com" {
		t.Fatalf("Access-Control-Allow-Origin leaked foreign origin: %q", acao)
	}
}

func createAdminQueueFixture(t *testing.T, sqlDB *sql.DB, now string) {
	t.Helper()
	_, err := sqlDB.Exec(`CREATE TABLE IF NOT EXISTS coremail_queue (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		tenant_id INTEGER NOT NULL DEFAULT 0,
		domain_id INTEGER NOT NULL DEFAULT 0,
		mailbox_id INTEGER,
		message_id TEXT NOT NULL DEFAULT '',
		from_address TEXT NOT NULL DEFAULT '',
		to_address TEXT NOT NULL DEFAULT '',
		recipient_domain TEXT NOT NULL DEFAULT '',
		direction TEXT NOT NULL DEFAULT 'outbound',
		status TEXT NOT NULL DEFAULT 'pending',
		priority INTEGER NOT NULL DEFAULT 0,
		attempt_count INTEGER NOT NULL DEFAULT 0,
		max_attempts INTEGER NOT NULL DEFAULT 16,
		next_attempt_at DATETIME,
		last_attempt_at DATETIME,
		last_error TEXT NOT NULL DEFAULT '',
		delivery_mode TEXT NOT NULL DEFAULT 'remote_smtp',
		remote_host TEXT NOT NULL DEFAULT '',
		remote_ip TEXT NOT NULL DEFAULT '',
		tls_used INTEGER NOT NULL DEFAULT 0,
		lease_owner TEXT NOT NULL DEFAULT '',
		lease_expires_at DATETIME,
		created_at DATETIME NOT NULL,
		updated_at DATETIME NOT NULL,
		completed_at DATETIME,
		dead_letter_at DATETIME,
		deleted_at DATETIME
	)`)
	if err != nil {
		t.Fatalf("create queue fixture: %v", err)
	}
	_, err = sqlDB.Exec(
		`INSERT INTO coremail_queue (message_id, from_address, to_address, status, attempt_count, next_attempt_at, created_at, updated_at, deleted_at)
		 VALUES ('msg-001', 'sender@test.local', 'admin@test.local', 'pending', 1, ?, ?, ?, NULL)`,
		now, now, now,
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

func TestQueueReturnsSafeFields(t *testing.T) {
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
	now := time.Now().UTC().Format("2006-01-02 15:04:05")
	hashedPwTest, _ := authenticator.HashPassword("TestPassword123!")
	_, err = sqlDB.Exec(
		`INSERT INTO users (created_at, updated_at, email, password_hash, role, tenant_id, active, email_verified)
		 VALUES (?, ?, 'admin@test.local', ?, 'admin', 1, 1, 1)`,
		now, now, hashedPwTest,
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
	_, err = sqlDB.Exec(
		`INSERT INTO coremail_mailboxes (domain_id, tenant_id, local_part, email, name, password_hash, auth_scheme, status, quota_mb, is_admin, created_at, updated_at)
		 VALUES (1, 1, 'admin', 'admin@test.local', 'Admin', ?, 'argon2id', 'active', 1024, 1, ?, ?)`,
		"$argon2id$v=19$m=1024,t=1,p=1$c2FsdA$aGFzaA", now, now,
	)
	if err != nil {
		t.Fatalf("insert mailbox: %v", err)
	}
	createAdminQueueFixture(t, sqlDB, now)
	router := NewRouter(cfg, authenticator, logger, db, modules.NewRegistry(logger), license.NewFeatureFlags(logger), nil)
	defer router.App().Shutdown()
	token := loginForTest(t, router, "admin@test.local", "TestPassword123!")

	t.Run("queue returns JSON array always", func(t *testing.T) {
		body := getAdminJSON(t, router, token, "/api/v1/queue")
		var queue []map[string]any
		if err := json.Unmarshal(body, &queue); err != nil {
			t.Fatalf("queue must be JSON array: %v: %s", err, body)
		}
	})

	t.Run("queue returns safe fields", func(t *testing.T) {
		body := getAdminJSON(t, router, token, "/api/v1/queue")
		var queue []map[string]any
		if err := json.Unmarshal(body, &queue); err != nil {
			t.Fatalf("queue must be JSON array: %v: %s", err, body)
		}
		if len(queue) == 0 {
			t.Fatalf("expected at least one queue entry")
		}
		entry := queue[0]
		for _, key := range []string{"id", "from", "to", "status", "attempts", "message_id", "next_attempt_at", "created_at", "updated_at"} {
			if _, ok := entry[key]; !ok {
				t.Fatalf("queue entry must include %s: %v", key, entry)
			}
		}
		for _, forbidden := range []string{"body", "headers", "password", "secret", "auth", "token", "hash"} {
			if strings.Contains(string(body), forbidden) {
				t.Fatalf("queue response must not expose %s: %s", forbidden, body)
			}
		}
	})

	t.Run("queue empty returns empty array not null", func(t *testing.T) {
		_, err := sqlDB.Exec("DELETE FROM coremail_queue")
		if err != nil {
			t.Fatalf("clear queue: %v", err)
		}
		body := getAdminJSON(t, router, token, "/api/v1/queue")
		if string(body) == "null" {
			t.Fatalf("queue must return [] not null")
		}
		var queue []map[string]any
		if err := json.Unmarshal(body, &queue); err != nil {
			t.Fatalf("queue must be JSON array: %v: %s", err, body)
		}
		if len(queue) != 0 {
			t.Fatalf("expected empty array, got %d entries", len(queue))
		}
	})
}

func TestAuditLogsReturnsSafeFields(t *testing.T) {
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
	now := time.Now().UTC().Format("2006-01-02 15:04:05")
	hashedPwAudit, _ := authenticator.HashPassword("TestPassword123!")
	_, err = sqlDB.Exec(
		`INSERT INTO users (created_at, updated_at, email, password_hash, role, tenant_id, active, email_verified)
		 VALUES (?, ?, 'admin@test.local', ?, 'admin', 1, 1, 1)`,
		now, now, hashedPwAudit,
	)
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	router := NewRouter(cfg, authenticator, logger, db, modules.NewRegistry(logger), license.NewFeatureFlags(logger), nil)
	defer router.App().Shutdown()
	token := loginForTest(t, router, "admin@test.local", "TestPassword123!")

	t.Run("audit logs returns JSON array always", func(t *testing.T) {
		body := getAdminJSON(t, router, token, "/api/v1/audit/logs")
		var logs []map[string]any
		if err := json.Unmarshal(body, &logs); err != nil {
			t.Fatalf("audit logs must be JSON array: %v: %s", err, body)
		}
	})

	t.Run("audit logs empty returns empty array not null", func(t *testing.T) {
		_, err := sqlDB.Exec("DELETE FROM coremail_audit")
		if err != nil {
			t.Fatalf("clear audit: %v", err)
		}
		body := getAdminJSON(t, router, token, "/api/v1/audit/logs")
		if string(body) == "null" {
			t.Fatalf("audit logs must return [] not null")
		}
		var logs []map[string]any
		if err := json.Unmarshal(body, &logs); err != nil {
			t.Fatalf("audit logs must be JSON array: %v: %s", err, body)
		}
	})

	t.Run("audit logs does not expose secrets", func(t *testing.T) {
		_, err := sqlDB.Exec(
			`INSERT INTO coremail_audit (actor, role, action, target, result, ip, user_agent, timestamp)
			 VALUES ('user:1', 'admin', 'domain.create', 'domain:example.com', 'success', '127.0.0.1', 'test-agent', ?)`,
			time.Now().UTC().Format("2006-01-02 15:04:05"),
		)
		if err != nil {
			t.Fatalf("insert audit: %v", err)
		}
		body := getAdminJSON(t, router, token, "/api/v1/audit/logs")
		for _, forbidden := range []string{"password", "secret", "hash", "token", "bearer"} {
			if strings.Contains(strings.ToLower(string(body)), forbidden) {
				t.Fatalf("audit logs must not expose %s: %s", forbidden, body)
			}
		}
		var logs []map[string]any
		if err := json.Unmarshal(body, &logs); err != nil {
			t.Fatalf("audit logs must be JSON array: %v: %s", err, body)
		}
		if len(logs) == 0 {
			t.Fatalf("expected at least one audit log entry")
		}
		entry := logs[0]
		for _, key := range []string{"id", "action", "actor", "target", "result", "timestamp"} {
			if _, ok := entry[key]; !ok {
				t.Fatalf("audit log entry must include %s: %v", key, entry)
			}
		}
		if _, ok := entry["ip"]; ok {
			t.Fatalf("audit log entry must not expose ip field: %v", entry)
		}
		if _, ok := entry["userAgent"]; ok {
			t.Fatalf("audit log entry must not expose userAgent field: %v", entry)
		}
	})
}

func TestAdminSummaryEndpoint(t *testing.T) {
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
		t.Fatalf("insert domain: %v", err)
	}
	_, err = sqlDB.Exec(
		`INSERT INTO coremail_mailboxes (domain_id, tenant_id, local_part, email, name, password_hash, auth_scheme, status, quota_mb, is_admin, created_at, updated_at)
		 VALUES (1, 1, 'admin', 'admin@test.local', 'Admin', ?, 'argon2id', 'active', 1024, 1, ?, ?)`,
		"$argon2id$v=19$m=1024,t=1,p=1$c2FsdA$aGFzaA", now, now,
	)
	if err != nil {
		t.Fatalf("insert mailbox: %v", err)
	}
	createAdminQueueFixture(t, sqlDB, now)
	router := NewRouter(cfg, authenticator, logger, db, modules.NewRegistry(logger), license.NewFeatureFlags(logger), nil)
	defer router.App().Shutdown()
	token := loginForTest(t, router, "admin@test.local", "TestPassword123!")

	t.Run("summary returns object with required sections", func(t *testing.T) {
		body := getAdminJSON(t, router, token, "/api/v1/admin/summary")
		var data map[string]any
		if err := json.Unmarshal(body, &data); err != nil {
			t.Fatalf("summary must be JSON object: %v: %s", err, body)
		}
		for _, section := range []string{"domains", "mailboxes", "queue", "audit", "runtime"} {
			if _, ok := data[section]; !ok {
				t.Fatalf("summary must include %s section: %s", section, body)
			}
		}
	})

	t.Run("summary domain counts correct for fixture", func(t *testing.T) {
		body := getAdminJSON(t, router, token, "/api/v1/admin/summary")
		var data map[string]any
		json.Unmarshal(body, &data)
		domains := data["domains"].(map[string]any)
		if domains["total"].(float64) < 1 {
			t.Fatalf("expected at least 1 domain, got %v: %s", domains["total"], body)
		}
	})

	t.Run("summary mailbox counts correct for fixture", func(t *testing.T) {
		body := getAdminJSON(t, router, token, "/api/v1/admin/summary")
		var data map[string]any
		json.Unmarshal(body, &data)
		mailboxes := data["mailboxes"].(map[string]any)
		if mailboxes["total"].(float64) < 1 {
			t.Fatalf("expected at least 1 mailbox, got %v: %s", mailboxes["total"], body)
		}
		if mailboxes["admin"].(float64) < 1 {
			t.Fatalf("expected at least 1 admin mailbox, got %v: %s", mailboxes["admin"], body)
		}
	})

	t.Run("summary queue counts exist", func(t *testing.T) {
		body := getAdminJSON(t, router, token, "/api/v1/admin/summary")
		var data map[string]any
		json.Unmarshal(body, &data)
		queue := data["queue"].(map[string]any)
		if _, ok := queue["total"]; !ok {
			t.Fatalf("queue section must include total: %s", body)
		}
		if _, ok := queue["pending"]; !ok {
			t.Fatalf("queue section must include pending: %s", body)
		}
	})

	t.Run("summary runtime section present", func(t *testing.T) {
		body := getAdminJSON(t, router, token, "/api/v1/admin/summary")
		var data map[string]any
		json.Unmarshal(body, &data)
		runtime := data["runtime"].(map[string]any)
		if _, ok := runtime["status"]; !ok {
			t.Fatalf("runtime section must include status: %s", body)
		}
		if _, ok := runtime["version"]; !ok {
			t.Fatalf("runtime section must include version: %s", body)
		}
	})

	t.Run("summary contains no secrets", func(t *testing.T) {
		body := getAdminJSON(t, router, token, "/api/v1/admin/summary")
		for _, forbidden := range []string{"password", "hash", "token", "jwt", "bearer", "secret", "body", "headers"} {
			if strings.Contains(strings.ToLower(string(body)), forbidden) {
				t.Fatalf("summary must not contain %s: %s", forbidden, body)
			}
		}
	})
}

func TestQueueActions(t *testing.T) {
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
	createAdminQueueFixture(t, sqlDB, now)
	router := NewRouter(cfg, authenticator, logger, db, modules.NewRegistry(logger), license.NewFeatureFlags(logger), nil)
	defer router.App().Shutdown()
	token := loginForTest(t, router, "admin@test.local", "TestPassword123!")
	csrfToken := getCSRFTokenForTest(t, router, token)

	t.Run("retry success", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/api/v1/queue/1/retry", nil)
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
		if !strings.Contains(string(body), `"status":"pending"`) {
			t.Fatalf("retry must return pending status: %s", body)
		}
	})

	t.Run("retry missing queue id returns 404", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/api/v1/queue/99999/retry", nil)
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

	t.Run("retry invalid id returns 400", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/api/v1/queue/abc/retry", nil)
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

	t.Run("retry missing CSRF returns 403", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/api/v1/queue/1/retry", nil)
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

	t.Run("retry invalid CSRF returns 403", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/api/v1/queue/1/retry", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Cookie", "csrf_token=invalid")
		req.Header.Set("X-CSRF-Token", "bad")
		resp, err := router.App().Test(req)
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		if resp.StatusCode != 403 {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("expected 403, got %d: %s", resp.StatusCode, body)
		}
	})

	t.Run("delete success", func(t *testing.T) {
		req := httptest.NewRequest("DELETE", "/api/v1/queue/1", nil)
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
		if !strings.Contains(string(body), `"deleted":true`) {
			t.Fatalf("delete must return deleted:true: %s", body)
		}
	})

	t.Run("double-delete returns 404", func(t *testing.T) {
		req := httptest.NewRequest("DELETE", "/api/v1/queue/1", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Cookie", "csrf_token="+csrfToken)
		req.Header.Set("X-CSRF-Token", csrfToken)
		resp, err := router.App().Test(req)
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		if resp.StatusCode != 404 {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("expected 404 on double-delete, got %d: %s", resp.StatusCode, body)
		}
	})

	t.Run("retry soft-deleted queue row returns 404", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/api/v1/queue/1/retry", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Cookie", "csrf_token="+csrfToken)
		req.Header.Set("X-CSRF-Token", csrfToken)
		resp, err := router.App().Test(req)
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		if resp.StatusCode != 404 {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("expected 404 on retry of soft-deleted queue row, got %d: %s", resp.StatusCode, body)
		}
	})

	t.Run("delete missing queue id returns 404", func(t *testing.T) {
		req := httptest.NewRequest("DELETE", "/api/v1/queue/99999", nil)
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

	t.Run("delete invalid id returns 400", func(t *testing.T) {
		req := httptest.NewRequest("DELETE", "/api/v1/queue/abc", nil)
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

	t.Run("delete missing CSRF returns 403", func(t *testing.T) {
		req := httptest.NewRequest("DELETE", "/api/v1/queue/1", nil)
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

	t.Run("delete response safe no secrets", func(t *testing.T) {
		createAdminQueueFixture(t, sqlDB, now)
		req := httptest.NewRequest("DELETE", "/api/v1/queue/2", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Cookie", "csrf_token="+csrfToken)
		req.Header.Set("X-CSRF-Token", csrfToken)
		resp, err := router.App().Test(req)
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		body, _ := io.ReadAll(resp.Body)
		for _, forbidden := range []string{"body", "headers", "password", "secret", "auth", "token", "hash"} {
			if strings.Contains(strings.ToLower(string(body)), forbidden) {
				t.Fatalf("delete response must not expose %s: %s", forbidden, body)
			}
		}
	})

	t.Run("queue list after retry and delete remains safe", func(t *testing.T) {
		body := getAdminJSON(t, router, token, "/api/v1/queue")
		for _, forbidden := range []string{"body", "headers", "password", "secret", "auth", "token", "hash"} {
			if strings.Contains(strings.ToLower(string(body)), forbidden) {
				t.Fatalf("queue list must not expose %s: %s", forbidden, body)
			}
		}
		var queue []map[string]any
		if err := json.Unmarshal(body, &queue); err != nil {
			t.Fatalf("queue must be JSON array: %v: %s", err, body)
		}
		for _, entry := range queue {
			id := int(entry["id"].(float64))
			if id == 1 || id == 2 {
				t.Fatalf("queue list must exclude soft-deleted row %d: %s", id, body)
			}
		}
	})

	t.Run("dashboard queue counts exclude soft-deleted rows", func(t *testing.T) {
		body := getAdminJSON(t, router, token, "/api/v1/admin/summary")
		var data map[string]any
		json.Unmarshal(body, &data)
		queue := data["queue"].(map[string]any)
		total := queue["total"].(float64)
		if total != 0 {
			t.Fatalf("queue total should be 0 after all rows deleted, got %v: %s", total, body)
		}
	})
}

func TestRuntimeUpdateHelperScript(t *testing.T) {
	scriptPath := filepath.Join("..", "..", "release", "scripts", "apply-runtime-update.sh")
	if _, err := os.Stat(scriptPath); os.IsNotExist(err) {
		t.Fatalf("apply-runtime-update.sh not found at %s", scriptPath)
	}
	content, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatalf("read script: %v", err)
	}
	script := string(content)
	if !strings.Contains(script, "/usr/local/go/bin/go") {
		t.Fatalf("apply-runtime-update.sh must include /usr/local/go/bin/go fallback")
	}
	if strings.Contains(script, "git pull") {
		t.Fatalf("apply-runtime-update.sh must not contain git pull")
	}
	if strings.Contains(script, "caddy") || strings.Contains(script, "Caddy") {
		t.Fatalf("apply-runtime-update.sh must not reference Caddy")
	}
	if strings.Contains(script, "iptables") || strings.Contains(script, "ufw") || strings.Contains(script, "firewall") {
		t.Fatalf("apply-runtime-update.sh must not touch firewall")
	}
	if !strings.Contains(script, "exit 1") {
		t.Fatalf("apply-runtime-update.sh must exit non-zero on health failure")
	}
}

func TestMailboxDetailsEndpoint(t *testing.T) {
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
		t.Fatalf("insert domain: %v", err)
	}
	_, err = sqlDB.Exec(
		`INSERT INTO coremail_mailboxes (domain_id, tenant_id, local_part, email, name, password_hash, auth_scheme, status, quota_mb, is_admin, created_at, updated_at)
		 VALUES (1, 1, 'admin', 'admin@test.local', 'Admin', ?, 'argon2id', 'active', 1024, 1, ?, ?)`,
		"$argon2id$v=19$m=1024,t=1,p=1$c2FsdA$aGFzaA", now, now,
	)
	if err != nil {
		t.Fatalf("insert mailbox: %v", err)
	}
	router := NewRouter(cfg, authenticator, logger, db, modules.NewRegistry(logger), license.NewFeatureFlags(logger), nil)
	defer router.App().Shutdown()
	token := loginForTest(t, router, "admin@test.local", "TestPassword123!")

	t.Run("mailbox detail success", func(t *testing.T) {
		body := getAdminJSON(t, router, token, "/api/v1/mailboxes/1")
		var data map[string]any
		if err := json.Unmarshal(body, &data); err != nil {
			t.Fatalf("must be JSON: %v: %s", err, body)
		}
		if data["mailbox_id"] == nil {
			t.Fatalf("response must include mailbox_id: %s", body)
		}
		if data["email"] != "admin@test.local" {
			t.Fatalf("expected email admin@test.local, got %v: %s", data["email"], body)
		}
		if _, ok := data["stats"]; !ok {
			t.Fatalf("response must include stats: %s", body)
		}
		stats := data["stats"].(map[string]any)
		if _, ok := stats["messages"]; !ok {
			t.Fatalf("stats must include messages: %s", body)
		}
		if _, ok := stats["queue_items"]; !ok {
			t.Fatalf("stats must include queue_items: %s", body)
		}
	})

	t.Run("mailbox detail missing returns 404", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/v1/mailboxes/99999", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := router.App().Test(req)
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		if resp.StatusCode != 404 {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("expected 404, got %d: %s", resp.StatusCode, body)
		}
	})

	t.Run("mailbox detail no password exposure", func(t *testing.T) {
		body := getAdminJSON(t, router, token, "/api/v1/mailboxes/1")
		for _, forbidden := range []string{"password", "argon2", "hash", "secret", "token", "jwt", "bearer"} {
			if strings.Contains(strings.ToLower(string(body)), forbidden) {
				t.Fatalf("mailbox detail must not contain %s: %s", forbidden, body)
			}
		}
	})
}

func TestDomainDetailsEndpoint(t *testing.T) {
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
		t.Fatalf("insert domain: %v", err)
	}
	_, err = sqlDB.Exec(
		`INSERT INTO coremail_mailboxes (domain_id, tenant_id, local_part, email, name, password_hash, auth_scheme, status, quota_mb, is_admin, created_at, updated_at)
		 VALUES (1, 1, 'admin', 'admin@test.local', 'Admin', ?, 'argon2id', 'active', 1024, 1, ?, ?)`,
		"$argon2id$v=19$m=1024,t=1,p=1$c2FsdA$aGFzaA", now, now,
	)
	if err != nil {
		t.Fatalf("insert mailbox: %v", err)
	}
	router := NewRouter(cfg, authenticator, logger, db, modules.NewRegistry(logger), license.NewFeatureFlags(logger), nil)
	defer router.App().Shutdown()
	token := loginForTest(t, router, "admin@test.local", "TestPassword123!")

	t.Run("domain detail success", func(t *testing.T) {
		body := getAdminJSON(t, router, token, "/api/v1/domains/test.local")
		var data map[string]any
		if err := json.Unmarshal(body, &data); err != nil {
			t.Fatalf("must be JSON: %v: %s", err, body)
		}
		if data["domain"] != "test.local" {
			t.Fatalf("expected domain test.local, got %v: %s", data["domain"], body)
		}
		if _, ok := data["mailbox_count"]; !ok {
			t.Fatalf("response must include mailbox_count: %s", body)
		}
		if _, ok := data["mailboxes"]; !ok {
			t.Fatalf("response must include mailboxes list: %s", body)
		}
		mc := data["mailbox_count"].(float64)
		if mc < 1 {
			t.Fatalf("expected mailbox_count >= 1, got %v: %s", mc, body)
		}
		mbList := data["mailboxes"].([]any)
		if len(mbList) < 1 {
			t.Fatalf("expected at least 1 mailbox in mailboxes list: %s", body)
		}
	})

	t.Run("domain detail missing returns 404", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/v1/domains/nonexistent.com", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := router.App().Test(req)
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		if resp.StatusCode != 404 {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("expected 404, got %d: %s", resp.StatusCode, body)
		}
	})

	t.Run("deleted domain returns 404", func(t *testing.T) {
		sqlDB.Exec("UPDATE coremail_domains SET deleted_at = ? WHERE name = 'test.local'", time.Now().UTC().Format("2006-01-02 15:04:05"))
		req := httptest.NewRequest("GET", "/api/v1/domains/test.local", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := router.App().Test(req)
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		if resp.StatusCode != 404 {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("expected 404 for deleted domain, got %d: %s", resp.StatusCode, body)
		}
	})
}

func TestEntityAuditEndpoints(t *testing.T) {
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
		t.Fatalf("insert domain: %v", err)
	}
	_, err = sqlDB.Exec(
		`INSERT INTO coremail_mailboxes (domain_id, tenant_id, local_part, email, name, password_hash, auth_scheme, status, quota_mb, is_admin, created_at, updated_at)
		 VALUES (1, 1, 'admin', 'admin@test.local', 'Admin', ?, 'argon2id', 'active', 1024, 1, ?, ?)`,
		"$argon2id$v=19$m=1024,t=1,p=1$c2FsdA$aGFzaA", now, now,
	)
	if err != nil {
		t.Fatalf("insert mailbox: %v", err)
	}
	router := NewRouter(cfg, authenticator, logger, db, modules.NewRegistry(logger), license.NewFeatureFlags(logger), nil)
	defer router.App().Shutdown()
	token := loginForTest(t, router, "admin@test.local", "TestPassword123!")

	t.Run("mailbox audit returns array", func(t *testing.T) {
		body := getAdminJSON(t, router, token, "/api/v1/mailboxes/1/audit")
		var entries []map[string]any
		if err := json.Unmarshal(body, &entries); err != nil {
			t.Fatalf("must be JSON array: %v: %s", err, body)
		}
	})

	t.Run("domain audit returns array", func(t *testing.T) {
		body := getAdminJSON(t, router, token, "/api/v1/domains/test.local/audit")
		var entries []map[string]any
		if err := json.Unmarshal(body, &entries); err != nil {
			t.Fatalf("must be JSON array: %v: %s", err, body)
		}
	})

	t.Run("audit returns empty array when no entries", func(t *testing.T) {
		_, _ = sqlDB.Exec("DELETE FROM coremail_audit")
		body := getAdminJSON(t, router, token, "/api/v1/mailboxes/1/audit")
		if string(body) == "null" {
			t.Fatalf("must return [] not null: %s", body)
		}
		var entries []map[string]any
		json.Unmarshal(body, &entries)
		if len(entries) != 0 {
			t.Fatalf("expected empty array, got %d entries: %s", len(entries), body)
		}
	})

	t.Run("audit contains no secrets", func(t *testing.T) {
		for _, path := range []string{"/api/v1/mailboxes/1/audit", "/api/v1/domains/test.local/audit"} {
			body := getAdminJSON(t, router, token, path)
			for _, forbidden := range []string{"password", "hash", "token", "jwt", "bearer", "secret", "ip", "userAgent"} {
				if strings.Contains(strings.ToLower(string(body)), forbidden) {
					t.Fatalf("%s must not expose %s: %s", path, forbidden, body)
				}
			}
		}
	})
}
