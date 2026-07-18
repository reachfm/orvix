package handlers_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/api"
	"github.com/orvix/orvix/internal/auth"
	"github.com/orvix/orvix/internal/config"
	"github.com/orvix/orvix/internal/license"
	"github.com/orvix/orvix/internal/models"
	"github.com/orvix/orvix/internal/modules"
	"go.uber.org/zap"
)

type matrixEnv struct {
	router          *api.Router
	tenantAdmin     string
	tenantAdminCSRF string
	platformAdmin   string
	platformCSRF    string
	mailboxAID      int64
	mailboxBID      int64
	domainAName     string
	domainBName     string
}

func buildMatrixEnv(t *testing.T) *matrixEnv {
	t.Helper()
	logger := zap.NewNop()
	cfg := config.Defaults()
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = t.TempDir() + "/matrix.db?_loc=auto&_busy_timeout=5000&_txlock=immediate"
	db, err := config.NewDatabase(&cfg.Database, logger)
	if err != nil {
		t.Fatalf("database: %v", err)
	}
	if err := models.MigrateAllRaw(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("sql db: %v", err)
	}
	authenticator, err := auth.NewAuthenticator(&cfg.Auth, db, logger)
	if err != nil {
		t.Fatalf("authenticator: %v", err)
	}
	now := time.Now().UTC()
	exec := func(q string, args ...interface{}) {
		if _, err := sqlDB.Exec(q, args...); err != nil {
			t.Fatalf("exec: %v", err)
		}
	}

	exec("INSERT INTO tenants (id, created_at, updated_at, name, slug, domain, plan, active) VALUES (1, ?, ?, 'tenant-a', 'tenant-a', 'tenanta.example', 'enterprise', 1)", now, now)
	exec("INSERT INTO tenants (id, created_at, updated_at, name, slug, domain, plan, active) VALUES (2, ?, ?, 'tenant-b', 'tenant-b', 'tenantb.example', 'enterprise', 1)", now, now)
	exec("INSERT INTO coremail_domains (id, name, tenant_id, status, plan, max_mailboxes, max_aliases, max_quota_mb, created_at, updated_at) VALUES (1, 'tenanta.example', 1, 'active', 'enterprise', 0, 0, 0, ?, ?)", now, now)
	exec("INSERT INTO coremail_domains (id, name, tenant_id, status, plan, max_mailboxes, max_aliases, max_quota_mb, created_at, updated_at) VALUES (2, 'tenantb.example', 2, 'active', 'enterprise', 0, 0, 0, ?, ?)", now, now)

	taHash, _ := authenticator.HashPassword("TenantAdminPass!")
	exec("INSERT INTO users (created_at, updated_at, email, password_hash, role, tenant_id, active, email_verified) VALUES (?, ?, 'ta@tenanta.example', ?, 'admin', 1, 1, 1)", now, now, taHash)
	exec("INSERT INTO coremail_mailboxes (id, domain_id, tenant_id, local_part, email, name, password_hash, auth_scheme, status, quota_mb, is_admin, created_at, updated_at) VALUES (1, 1, 1, 'ta', 'ta@tenanta.example', 'TA', ?, 'argon2id', 'active', 1024, 1, ?, ?)", taHash, now, now)

	psaHash, _ := authenticator.HashPassword("PlatformSuperPass!")
	exec("INSERT INTO users (created_at, updated_at, email, password_hash, role, tenant_id, active, email_verified) VALUES (?, ?, 'psa@platform.local', ?, 'platform_super_admin', NULL, 1, 1)", now, now, psaHash)

	vHash, _ := authenticator.HashPassword("VictimPass!")
	exec("INSERT INTO coremail_mailboxes (id, domain_id, tenant_id, local_part, email, name, password_hash, auth_scheme, status, quota_mb, is_admin, created_at, updated_at) VALUES (2, 2, 2, 'victim', 'victim@tenantb.example', 'Victim', ?, 'argon2id', 'active', 1024, 0, ?, ?)", vHash, now, now)

	scratchDir := t.TempDir()
	adminDir := scratchDir + "/admin"
	webmailDir := scratchDir + "/webmail"
	mkdirP(adminDir)
	mkdirP(webmailDir)
	cfg.Server.AdminUIDir = adminDir
	cfg.Server.WebmailUIDir = webmailDir

	router := api.NewRouter(cfg, authenticator, logger, db, modules.NewRegistry(logger), license.NewFeatureFlags(logger), nil)
	t.Cleanup(func() {
		router.App().Shutdown()
		sqlDB.Close()
	})

	taToken := matrixLogin(t, router, "ta@tenanta.example", "TenantAdminPass!")
	taCSRF := matrixCSRF(t, router, taToken)
	psaToken := matrixLogin(t, router, "psa@platform.local", "PlatformSuperPass!")
	psaCSRF := matrixCSRF(t, router, psaToken)

	return &matrixEnv{
		router:          router,
		tenantAdmin:     taToken,
		tenantAdminCSRF: taCSRF,
		platformAdmin:   psaToken,
		platformCSRF:    psaCSRF,
		mailboxAID:      1,
		mailboxBID:      2,
		domainAName:     "tenanta.example",
		domainBName:     "tenantb.example",
	}
}

func matrixLogin(t *testing.T, r *api.Router, email, pass string) string {
	t.Helper()
	b, _ := json.Marshal(map[string]string{"email": email, "password": pass})
	req := httptest.NewRequest("POST", "/api/v1/auth/login", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	resp, err := r.App().Test(req)
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	raw, _ := io.ReadAll(resp.Body)
	var out struct {
		AccessToken string `json:"access_token"`
	}
	json.Unmarshal(raw, &out)
	if out.AccessToken == "" {
		t.Fatalf("no access_token for %s: %s", email, string(raw))
	}
	return out.AccessToken
}

func matrixCSRF(t *testing.T, r *api.Router, token string) string {
	t.Helper()
	req := httptest.NewRequest("GET", "/api/v1/csrf-token", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := r.App().Test(req)
	if err != nil {
		t.Fatalf("csrf: %v", err)
	}
	for _, c := range resp.Cookies() {
		if c.Name == "csrf_token" {
			return c.Value
		}
	}
	t.Fatal("no csrf_token cookie in response")
	return ""
}

func matrixReq(t *testing.T, e *matrixEnv, token, csrf, method, path string, body interface{}) (int, map[string]interface{}) {
	t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, r)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if csrf != "" {
		req.Header.Set("Cookie", "access_token="+token+"; csrf_token="+csrf)
		req.Header.Set("X-CSRF-Token", csrf)
	}
	resp, err := e.router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	out := map[string]interface{}{}
	if resp.StatusCode != 204 {
		raw, _ := io.ReadAll(resp.Body)
		if len(raw) > 0 {
			json.Unmarshal(raw, &out)
		}
	}
	return resp.StatusCode, out
}

func mkdirP(path string) {
	_ = mkdirEmpty(path)
}

func TestMatrix_PlatformSuperAdminCanAccessPlatformRoutes(t *testing.T) {
	e := buildMatrixEnv(t)
	status, _ := matrixReq(t, e, e.platformAdmin, e.platformCSRF, "GET", "/api/v1/console/internal/overview", nil)
	if status != 200 {
		t.Fatalf("platform super admin should access internal ops: got %d", status)
	}
}

func TestMatrix_TenantAdminCannotAccessPlatformRoutes(t *testing.T) {
	e := buildMatrixEnv(t)
	status, _ := matrixReq(t, e, e.tenantAdmin, e.tenantAdminCSRF, "GET", "/api/v1/console/internal/overview", nil)
	if status != 403 {
		t.Fatalf("tenant admin must not access internal ops: expected 403, got %d", status)
	}
}

func TestMatrix_TenantAdminCanReadOwnResource(t *testing.T) {
	e := buildMatrixEnv(t)
	status, body := matrixReq(t, e, e.tenantAdmin, "", "GET", "/api/v1/mailboxes/1", nil)
	if status != 200 {
		t.Fatalf("own resource: expected 200, got %d: %v", status, body)
	}
}

func TestMatrix_TenantAdminCannotReadCrossTenantByID(t *testing.T) {
	e := buildMatrixEnv(t)
	status, _ := matrixReq(t, e, e.tenantAdmin, "", "GET", "/api/v1/mailboxes/2", nil)
	if status != 404 {
		t.Fatalf("cross-tenant by ID: expected 404, got %d", status)
	}
}

func TestMatrix_TenantAdminCannotSearchCrossTenant(t *testing.T) {
	e := buildMatrixEnv(t)
	status, body := matrixReq(t, e, e.tenantAdmin, "", "GET", "/api/v1/mailboxes?q=victim", nil)
	if status == 200 {
		if items, _ := body["mailboxes"]; items != nil {
			for _, m := range items.([]interface{}) {
				mb := m.(map[string]interface{})
				if mb["email"] == "victim@tenantb.example" {
					t.Fatal("cross-tenant mailbox appeared in search results")
				}
			}
		}
	}
}

func TestMatrix_TenantAdminCannotUpdateCrossTenant(t *testing.T) {
	e := buildMatrixEnv(t)
	status, _ := matrixReq(t, e, e.tenantAdmin, e.tenantAdminCSRF, "PATCH", "/api/v1/mailboxes/2/status", map[string]string{"status": "suspended"})
	if status != 404 {
		t.Fatalf("cross-tenant update: expected 404, got %d", status)
	}
}

func TestMatrix_TenantAdminCannotDeleteCrossTenant(t *testing.T) {
	e := buildMatrixEnv(t)
	status, _ := matrixReq(t, e, e.tenantAdmin, e.tenantAdminCSRF, "DELETE", "/api/v1/mailboxes/2", nil)
	if status != 404 {
		t.Fatalf("cross-tenant delete: expected 404, got %d", status)
	}
}

func TestMatrix_BodyTenantIDCannotOverride(t *testing.T) {
	e := buildMatrixEnv(t)
	status, body := matrixReq(t, e, e.tenantAdmin, e.tenantAdminCSRF, "POST", "/api/v1/mailboxes", map[string]interface{}{
		"email":     "attempt@tenanta.example",
		"password":  "ValidPass!2026",
		"domain":    e.domainAName,
		"tenant_id": 2,
		"quota_mb":  50,
	})
	// The handler must NOT create a mailbox in tenant 2.
	// If the request succeeds, the mailbox must be in tenant 1.
	if status == 200 || status == 201 {
		mb, _ := body["tenant_id"].(float64)
		if mb == 2 {
			t.Fatalf("body tenant_id override succeeded: mailbox created in tenant 2")
		}
	}
	// If it failed (e.g. 400/403/404), that's also acceptable — the point
	// is tenant 1's admin cannot create resources in tenant 2.
}

func TestMatrix_PathTenantIDCannotOverride(t *testing.T) {
	e := buildMatrixEnv(t)
	status, body := matrixReq(t, e, e.tenantAdmin, "", "GET", "/api/v1/admin/organizations/2", nil)
	if status == 200 {
		if name, _ := body["name"]; name == "tenant-b" {
			t.Fatalf("tenant admin accessed other tenant organization data via path")
		}
	}
}

func TestMatrix_UnknownRoleIsDenied(t *testing.T) {
	e := buildMatrixEnv(t)
	status, _ := matrixReq(t, e, "invalid.token.here", "", "GET", "/api/v1/mailboxes/1", nil)
	if status < 400 {
		t.Fatalf("invalid token: expected 4xx, got %d", status)
	}
}

func TestMatrix_UnauthenticatedIsDenied(t *testing.T) {
	e := buildMatrixEnv(t)
	req := httptest.NewRequest("GET", "/api/v1/mailboxes/1", nil)
	resp, err := e.router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if resp.StatusCode < 400 {
		t.Fatalf("unauthenticated request: expected 4xx, got %d", resp.StatusCode)
	}
}

func TestMatrix_PlatformSuperAdminCanAccessCrossTenant(t *testing.T) {
	e := buildMatrixEnv(t)
	status, body := matrixReq(t, e, e.platformAdmin, "", "GET", "/api/v1/mailboxes/2", nil)
	if status != 200 {
		t.Fatalf("platform admin accessing cross-tenant mailbox: expected 200, got %d: %v", status, body)
	}
}
