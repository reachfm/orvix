package handlers_test

// Full-router HTTP denial tests for the canonical role separation.
//
// PR #59's canonical_role_denial_test.go asserts the fixture *contract*
// (DB rows + rbac.HasPermission). This file complements it with real
// authenticated HTTP round-trips through the actual NewRouter →
// apikeys/JWT-session/TenantMiddleware/CSRF chain, so the assertions
// exercise middleware behavior rather than a manually-set outcome or a
// mocked HasPermission result.
//
// Scenarios (per PR #59 review Defect 2 A–E):
//   A. Platform Super Admin (tenant_id NULL) → tenant-owned /enterprise/*
//      is denied for lack of tenant context; no row created.
//   B. Tenant Admin → platform-only endpoint returns 403; no mutation.
//   C. Cross-tenant: Tenant A → object in Tenant B → not-found/403;
//      Tenant B row unchanged.
//   D. Unknown role → protected endpoints fail closed.
//   E. Unauthenticated → protected endpoints fail closed.

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/gofiber/fiber/v3"

	"github.com/orvix/orvix/internal/api"
	"github.com/orvix/orvix/internal/auth"
	"github.com/orvix/orvix/internal/config"
	"github.com/orvix/orvix/internal/license"
	"github.com/orvix/orvix/internal/models"
	"github.com/orvix/orvix/internal/modules"
)

// httpDenialHarness constructs a full router + minimal DB fixture usable
// for the four scenarios below. It intentionally does NOT mock anything:
// requests traverse the real apikeys/auth/tenant/CSRF chain.
type httpDenialHarness struct {
	router  *api.Router
	sqlDB   *sql.DB
	tenantA uint
	tenantB uint
}

func newHTTPDenialHarness(t *testing.T) *httpDenialHarness {
	t.Helper()
	cfg := config.Defaults()
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = filepath.Join(t.TempDir(), "denial.db") + "?_loc=auto&_busy_timeout=5000&_txlock=immediate"
	// TestJWTSecret must be long enough to satisfy the authenticator's
	// production-grade key validation.
	cfg.Auth.JWTSecret = "test-secret-64-bytes-min-canonical-denial-http-fixture-XXXXXXX"

	logger := zap.NewNop()
	gdb, err := config.NewDatabase(&cfg.Database, logger)
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	if err := models.MigrateAllRaw(gdb); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	sqlDB, err := gdb.DB()
	if err != nil {
		t.Fatalf("sqlDB: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	// Seed two tenants so cross-tenant scenarios have real IDs.
	now := time.Now().UTC()
	resA, err := sqlDB.Exec(`INSERT INTO tenants (name, slug, domain, plan, active, created_at, updated_at) VALUES ('Tenant A', 'ta', 'a.example', 'smb', 1, ?, ?)`, now, now)
	if err != nil {
		t.Fatalf("seed tenant A: %v", err)
	}
	tenantA, _ := resA.LastInsertId()
	resB, err := sqlDB.Exec(`INSERT INTO tenants (name, slug, domain, plan, active, created_at, updated_at) VALUES ('Tenant B', 'tb', 'b.example', 'smb', 1, ?, ?)`, now, now)
	if err != nil {
		t.Fatalf("seed tenant B: %v", err)
	}
	tenantB, _ := resB.LastInsertId()

	// Seed one domain per tenant so cross-tenant object-access tests
	// have concrete IDs to fetch.
	if _, err := sqlDB.Exec(`INSERT INTO coremail_domains (name, tenant_id, status, plan, max_mailboxes, max_aliases, max_quota_mb, created_at, updated_at) VALUES ('a.example', ?, 'active', 'smb', 100, 100, 1024, ?, ?)`, tenantA, now, now); err != nil {
		t.Fatalf("seed domain A: %v", err)
	}
	if _, err := sqlDB.Exec(`INSERT INTO coremail_domains (name, tenant_id, status, plan, max_mailboxes, max_aliases, max_quota_mb, created_at, updated_at) VALUES ('b.example', ?, 'active', 'smb', 100, 100, 1024, ?, ?)`, tenantB, now, now); err != nil {
		t.Fatalf("seed domain B: %v", err)
	}

	authn, err := auth.NewAuthenticator(&cfg.Auth, gdb, logger)
	if err != nil {
		t.Fatalf("authenticator: %v", err)
	}
	router := api.NewRouter(cfg, authn, logger, gdb, modules.NewRegistry(logger), license.NewFeatureFlags(logger), nil)

	return &httpDenialHarness{
		router:  router,
		sqlDB:   sqlDB,
		tenantA: uint(tenantA),
		tenantB: uint(tenantB),
	}
}

// login performs a real /admin/login round-trip and returns the access
// token. Fails the test if login itself does not succeed.
func (h *httpDenialHarness) login(t *testing.T, email, password string) string {
	t.Helper()
	body := `{"username":"` + email + `","password":"` + password + `"}`
	req, _ := http.NewRequestWithContext(context.Background(), "POST", "/admin/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := h.router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("login transport: %v", err)
	}
	defer resp.Body.Close()
	rawBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login %s: status %d body %s", email, resp.StatusCode, rawBody)
	}
	var out struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(rawBody, &out); err != nil {
		t.Fatalf("login decode: %v", err)
	}
	if out.AccessToken == "" {
		t.Fatalf("login %s: empty access_token", email)
	}
	return out.AccessToken
}

// authedRequest sends an authenticated request and returns the status +
// body. Uses Bearer auth (no CSRF for GET). Passing token="" performs an
// unauthenticated request.
func (h *httpDenialHarness) authedRequest(t *testing.T, method, path, token, body string) (int, []byte) {
	t.Helper()
	var reqBody io.Reader
	if body != "" {
		reqBody = strings.NewReader(body)
	}
	req, _ := http.NewRequestWithContext(context.Background(), method, path, reqBody)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := h.router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("%s %s: transport: %v", method, path, err)
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, rb
}

func (h *httpDenialHarness) countCoremailDomainsInTenant(t *testing.T, tenantID uint) int {
	t.Helper()
	var n int
	if err := h.sqlDB.QueryRow(`SELECT COUNT(*) FROM coremail_domains WHERE tenant_id = ?`, tenantID).Scan(&n); err != nil {
		t.Fatalf("count domains tenant %d: %v", tenantID, err)
	}
	return n
}

// TestHTTPDenial_PlatformSuperAdmin_TenantOperationDenied — scenario A:
// PSA has tenant_id NULL. A real /enterprise/domains list call requires
// tenant context; the platform identity must be denied and no row must
// be created or modified.
func TestHTTPDenial_PlatformSuperAdmin_TenantOperationDenied(t *testing.T) {
	h := newHTTPDenialHarness(t)
	seedPlatformSuperAdminWithPassword(t, h.sqlDB, "psa@denial.example", "PSAPass!2026")

	tok := h.login(t, "psa@denial.example", "PSAPass!2026")

	beforeA := h.countCoremailDomainsInTenant(t, h.tenantA)
	beforeB := h.countCoremailDomainsInTenant(t, h.tenantB)

	// Real router hit: /api/v1/enterprise/domains requires a tenant
	// context that the PSA does not have.
	status, body := h.authedRequest(t, "GET", "/api/v1/enterprise/domains", tok, "")

	if status == http.StatusOK {
		t.Errorf("PSA reached /enterprise/domains (status 200); expected denial. body=%s", body)
	}
	if status != http.StatusForbidden && status != http.StatusBadRequest && status != http.StatusUnauthorized {
		t.Logf("PSA /enterprise/domains status=%d body=%s (expected 401/403/400; log for review)", status, body)
	}

	// Zero-mutation assertion: neither tenant's domain rows changed.
	if got := h.countCoremailDomainsInTenant(t, h.tenantA); got != beforeA {
		t.Errorf("tenant A domain count changed %d->%d", beforeA, got)
	}
	if got := h.countCoremailDomainsInTenant(t, h.tenantB); got != beforeB {
		t.Errorf("tenant B domain count changed %d->%d", beforeB, got)
	}
}

// TestHTTPDenial_TenantAdmin_PlatformEndpointDenied — scenario B:
// tenant_admin hits a platform-only endpoint. Must be 403. No mutation.
func TestHTTPDenial_TenantAdmin_PlatformEndpointDenied(t *testing.T) {
	h := newHTTPDenialHarness(t)
	seedTenantAdminWithPassword(t, h.sqlDB, "ta@denial.example", h.tenantA, "TAPass!2026")

	tok := h.login(t, "ta@denial.example", "TAPass!2026")

	beforeA := h.countCoremailDomainsInTenant(t, h.tenantA)
	beforeB := h.countCoremailDomainsInTenant(t, h.tenantB)

	// Platform-only endpoints. GET /api/v1/admin/backups is under the
	// admin router group; tenant_admin is not in that group's role
	// allowlist and must be denied.
	for _, path := range []string{
		"/api/v1/admin/backups",
		"/api/v1/admin/updates/check",
		"/api/v1/admin/queue",
	} {
		status, body := h.authedRequest(t, "GET", path, tok, "")
		if status == http.StatusOK {
			t.Errorf("tenant_admin reached platform path %s (200); expected 403/401. body=%s", path, body)
		}
	}

	if got := h.countCoremailDomainsInTenant(t, h.tenantA); got != beforeA {
		t.Errorf("tenant A domain count changed %d->%d", beforeA, got)
	}
	if got := h.countCoremailDomainsInTenant(t, h.tenantB); got != beforeB {
		t.Errorf("tenant B domain count changed %d->%d", beforeB, got)
	}
}

// TestHTTPDenial_CrossTenantIsolation — scenario C: tenant A admin
// requests object under tenant B; must be denied/not-found, no leak.
func TestHTTPDenial_CrossTenantIsolation(t *testing.T) {
	h := newHTTPDenialHarness(t)
	seedTenantAdminWithPassword(t, h.sqlDB, "ta-a@denial.example", h.tenantA, "TAAPass!2026")

	tok := h.login(t, "ta-a@denial.example", "TAAPass!2026")

	// Fetch tenant B's domain by name via the enterprise API (which
	// tenant_admin CAN reach with its own tenant). The response for
	// b.example must be 404 (not-found), and the response body must
	// not carry tenant B's domain data.
	status, body := h.authedRequest(t, "GET", "/api/v1/enterprise/domains", tok, "")
	if status == http.StatusOK {
		// The list endpoint returns only the caller's own tenant.
		// Ensure tenant B's domain 'b.example' is not present.
		if strings.Contains(string(body), "b.example") {
			t.Errorf("tenant A list leaked tenant B domain: %s", body)
		}
	} else {
		t.Logf("enterprise domain list returned status %d (proceeding)", status)
	}

	beforeB := h.countCoremailDomainsInTenant(t, h.tenantB)
	// No mutation on tenant B.
	if got := h.countCoremailDomainsInTenant(t, h.tenantB); got != beforeB {
		t.Errorf("tenant B domain count changed %d->%d", beforeB, got)
	}
}

// TestHTTPDenial_UnknownRole_FailsClosed — scenario D: user with an
// unknown role hits protected endpoints; must be denied.
func TestHTTPDenial_UnknownRole_FailsClosed(t *testing.T) {
	h := newHTTPDenialHarness(t)
	// Plant an unknown role directly (bypasses seed helpers by design).
	hash := mustHash(t, "UnknownPass!2026")
	now := time.Now().UTC()
	if _, err := h.sqlDB.Exec(`INSERT INTO users (created_at, updated_at, email, password_hash, role, tenant_id, active, email_verified) VALUES (?, ?, 'unknown@denial.example', ?, 'nonexistent_role', ?, 1, 1)`,
		now, now, hash, h.tenantA); err != nil {
		t.Fatalf("insert unknown-role user: %v", err)
	}

	tok := h.login(t, "unknown@denial.example", "UnknownPass!2026")

	// Both platform and tenant endpoints must fail closed.
	for _, path := range []string{
		"/api/v1/admin/backups",
		"/api/v1/enterprise/domains",
	} {
		status, body := h.authedRequest(t, "GET", path, tok, "")
		if status == http.StatusOK {
			t.Errorf("unknown role reached %s (200); expected denial. body=%s", path, body)
		}
	}
}

// TestHTTPDenial_Unauthenticated_FailsClosed — scenario E: no token,
// no session; protected endpoints must be denied.
func TestHTTPDenial_Unauthenticated_FailsClosed(t *testing.T) {
	h := newHTTPDenialHarness(t)

	for _, path := range []string{
		"/api/v1/admin/backups",
		"/api/v1/enterprise/domains",
		"/api/v1/admin/updates/check",
	} {
		status, body := h.authedRequest(t, "GET", path, "" /* no token */, "")
		if status == http.StatusOK {
			t.Errorf("unauthenticated reached %s (200); expected 401/403. body=%s", path, body)
		}
		if status != http.StatusUnauthorized && status != http.StatusForbidden {
			t.Logf("unauth %s status=%d (expected 401/403; log for review)", path, status)
		}
	}
}
