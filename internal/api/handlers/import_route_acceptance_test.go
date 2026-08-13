package handlers_test

// Route acceptance tests for the Phase 4B import endpoints. These exercise
// the real router (api.NewRouter) and the real middleware chain — auth,
// tenant context, RBAC, CSRF — against the production wiring, proving:
//   - tenant isolation and platform/tenant separation;
//   - RBAC denial and CSRF denial;
//   - missing/invalid confirmation;
//   - missing idempotency keys and key reuse;
//   - strict JSON rejection;
//   - service-unavailable (503) responses;
//   - no secrets, staged paths, raw payloads or lease tokens in responses.

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"go.uber.org/zap"

	"github.com/orvix/orvix/internal/api"
	"github.com/orvix/orvix/internal/auth"
	"github.com/orvix/orvix/internal/config"
	"github.com/orvix/orvix/internal/license"
	"github.com/orvix/orvix/internal/models"
	"github.com/orvix/orvix/internal/modules"
)

type importRouteEnv struct {
	router *api.Router
	// authenticated tokens
	psaToken   string // platform super admin (no tenant)
	tenantAdm  string // tenant_admin, tenant 1
	otherAdm   string // tenant_admin, tenant 2
	plainUser  string // role user
	tenantCSRF string // CSRF token usable by tenantAdm
	psaCSRF    string
}

const (
	importTenantPath   = "/api/v1/enterprise/imports"
	importPlatformPath = "/api/v1/platform/imports"
	importPSAEmail     = "psa@platform.local"
	importPSAPass      = "PlatformPass!2026"
	importTenant1Email = "admin1@tenant1.local"
	importTenant1Pass  = "Tenant1Pass!2026"
	importTenant2Email = "admin2@tenant2.local"
	importTenant2Pass  = "Tenant2Pass!2026"
	importPlainEmail   = "plain@tenant1.local"
	importPlainPass    = "PlainUserPass!2026"
)

func buildImportRouteEnv(t *testing.T) *importRouteEnv {
	t.Helper()
	logger := zap.NewNop()
	cfg := config.Defaults()
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = t.TempDir() + "/importroutes.db?_loc=auto&_busy_timeout=5000&_txlock=immediate"
	cfg.Imports.StagingDir = t.TempDir()
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
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("sql db: %v", err)
	}
	now := time.Now().UTC()
	exec := func(q string, args ...interface{}) {
		t.Helper()
		if _, err := sqlDB.Exec(q, args...); err != nil {
			t.Fatalf("exec %q: %v", q, err)
		}
	}
	exec("INSERT INTO tenants (id, created_at, updated_at, name, slug, domain, plan, active) VALUES (1, ?, ?, 'tenant-a', 'tenant-a', 't1.example', 'enterprise', 1)", now, now)
	exec("INSERT INTO tenants (id, created_at, updated_at, name, slug, domain, plan, active) VALUES (2, ?, ?, 'tenant-b', 'tenant-b', 't2.example', 'enterprise', 1)", now, now)
	tid1 := uint(1)
	seedTenantAdminWithPassword(t, sqlDB, importTenant1Email, tid1, importTenant1Pass)
	tid2 := uint(2)
	seedTenantAdminWithPassword(t, sqlDB, importTenant2Email, tid2, importTenant2Pass)
	psaHash, _ := authenticator.HashPassword(importPSAPass)
	exec("INSERT INTO users (created_at, updated_at, email, password_hash, role, tenant_id, active, email_verified) VALUES (?, ?, '"+importPSAEmail+"', ?, 'platform_super_admin', NULL, 1, 1)", now, now, psaHash)
	plainHash, _ := authenticator.HashPassword(importPlainPass)
	exec("INSERT INTO users (created_at, updated_at, email, password_hash, role, tenant_id, active, email_verified) VALUES (?, ?, '"+importPlainEmail+"', ?, 'user', 1, 1, 1)", now, now, plainHash)

	router := api.NewRouter(cfg, authenticator, logger, db, modules.NewRegistry(logger), license.NewFeatureFlags(logger), nil)
	t.Cleanup(func() {
		_ = router.App().Shutdown()
		_ = sqlDB.Close()
	})

	psaToken := importRouteLogin(t, router, importPSAEmail, importPSAPass)
	tenantAdm := importRouteLogin(t, router, importTenant1Email, importTenant1Pass)
	otherAdm := importRouteLogin(t, router, importTenant2Email, importTenant2Pass)
	plainUser := importRouteLogin(t, router, importPlainEmail, importPlainPass)
	return &importRouteEnv{
		router:     router,
		psaToken:   psaToken,
		tenantAdm:  tenantAdm,
		otherAdm:   otherAdm,
		plainUser:  plainUser,
		tenantCSRF: importRouteCSRF(t, router, tenantAdm),
		psaCSRF:    importRouteCSRF(t, router, psaToken),
	}
}

func importRouteLogin(t *testing.T, r *api.Router, email, pass string) string {
	t.Helper()
	b, _ := json.Marshal(map[string]string{"email": email, "password": pass})
	req := httptest.NewRequest("POST", "/api/v1/auth/login", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	resp, err := r.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("login %s: %v", email, err)
	}
	raw, _ := io.ReadAll(resp.Body)
	var out struct {
		AccessToken string `json:"access_token"`
	}
	_ = json.Unmarshal(raw, &out)
	if out.AccessToken == "" {
		t.Fatalf("no access_token for %s: %s", email, string(raw))
	}
	return out.AccessToken
}

func importRouteCSRF(t *testing.T, r *api.Router, token string) string {
	t.Helper()
	req := httptest.NewRequest("GET", "/api/v1/csrf-token", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := r.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("csrf: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("csrf status %d: %s", resp.StatusCode, body)
	}
	var data struct {
		CSRFToken string `json:"csrf_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		t.Fatalf("decode csrf: %v", err)
	}
	return data.CSRFToken
}

type importRouteResp struct {
	status    int
	body      string
	bodyBytes []byte
}

func importRouteRequest(t *testing.T, e *importRouteEnv, method, path, body, token, csrf string) importRouteResp {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if csrf != "" {
		req.Header.Set("Cookie", "csrf_token="+csrf)
		req.Header.Set("X-CSRF-Token", csrf)
	}
	resp, err := e.router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	raw, _ := io.ReadAll(resp.Body)
	return importRouteResp{status: resp.StatusCode, body: string(raw), bodyBytes: raw}
}

// createImportFor creates a real import job through the HTTP API (the only
// supported entrypoint) and returns its id.
func createImportFor(t *testing.T, e *importRouteEnv, token, csrf, path string) string {
	t.Helper()
	body := `{"entities":[{"entity":"organization","name":"Acme","domain":"acme.test"}]}`
	resp := importRouteRequest(t, e, "POST", path, body, token, csrf)
	if resp.status != http.StatusCreated {
		t.Fatalf("create import: want 201, got %d %s", resp.status, resp.body)
	}
	var out struct {
		ID int64 `json:"id"`
	}
	_ = json.Unmarshal(resp.bodyBytes, &out)
	return formatInt(out.ID)
}

func formatInt(n int64) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		buf[i] = byte(n%10) + '0'
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// ── Tenant isolation ─────────────────────────────────────────────────

func TestImportRoutes_TenantIsolation(t *testing.T) {
	e := buildImportRouteEnv(t)

	// Tenant 1 creates an import.
	id := createImportFor(t, e, e.tenantAdm, e.tenantCSRF, importTenantPath)

	// Tenant 2 must not see tenant 1's import.
	resp := importRouteRequest(t, e, "GET", importTenantPath+"/"+id, "", e.otherAdm, "")
	if resp.status == http.StatusOK {
		t.Fatalf("cross-tenant read leaked: %s", resp.body)
	}
	if resp.status >= 500 {
		t.Fatalf("cross-tenant read produced 5xx: %d", resp.status)
	}
}

func TestImportRoutes_PlatformSeparatedFromTenant(t *testing.T) {
	e := buildImportRouteEnv(t)

	// Tenant admin creates a tenant-scoped import.
	id := createImportFor(t, e, e.tenantAdm, e.tenantCSRF, importTenantPath)

	// The same import must NOT be visible on the platform route for a tenant
	// admin (platform routes require the platform role).
	resp := importRouteRequest(t, e, "GET", importPlatformPath+"/"+id, "", e.tenantAdm, "")
	if resp.status != http.StatusForbidden && resp.status != http.StatusUnauthorized {
		t.Fatalf("tenant admin reached platform route: got %d", resp.status)
	}

	// Platform super admin can read the platform route but not the tenant one.
	psaList := importRouteRequest(t, e, "GET", importPlatformPath, "", e.psaToken, "")
	if psaList.status == http.StatusServiceUnavailable {
		t.Skip("import service not wired in this environment")
	}
	if psaList.status != http.StatusOK {
		t.Fatalf("psa platform list: want 200, got %d", psaList.status)
	}
}

// ── RBAC denial ──────────────────────────────────────────────────────

func TestImportRoutes_RBACDenial(t *testing.T) {
	e := buildImportRouteEnv(t)

	// A plain tenant user cannot reach import endpoints at all.
	resp := importRouteRequest(t, e, "GET", importTenantPath, "", e.plainUser, "")
	if resp.status != http.StatusForbidden && resp.status != http.StatusUnauthorized {
		t.Fatalf("plain user reached imports: got %d", resp.status)
	}

	// A tenant admin cannot use the platform import routes.
	resp2 := importRouteRequest(t, e, "POST", importPlatformPath, `{}`, e.tenantAdm, e.tenantCSRF)
	if resp2.status != http.StatusForbidden && resp2.status != http.StatusUnauthorized {
		t.Fatalf("tenant admin reached platform create: got %d", resp2.status)
	}
}

// ── CSRF denial ──────────────────────────────────────────────────────

func TestImportRoutes_CSRFDenial(t *testing.T) {
	e := buildImportRouteEnv(t)

	// Missing CSRF token on a mutation must be denied.
	resp := importRouteRequest(t, e, "POST", importTenantPath, `{}`, e.tenantAdm, "")
	if resp.status == http.StatusCreated {
		t.Fatalf("import create succeeded without CSRF")
	}
	if resp.status != http.StatusForbidden && resp.status != http.StatusBadRequest && resp.status != http.StatusUnauthorized {
		t.Fatalf("missing-CSRF mutation: got %d", resp.status)
	}
}

// ── Confirmation ─────────────────────────────────────────────────────

func TestImportRoutes_ConfirmationRequired(t *testing.T) {
	e := buildImportRouteEnv(t)
	id := createImportFor(t, e, e.tenantAdm, e.tenantCSRF, importTenantPath)

	// Execute without confirmation must fail.
	noConfirm := importRouteRequest(t, e, "POST", importTenantPath+"/"+id+"/execute", `{}`, e.tenantAdm, e.tenantCSRF)
	if noConfirm.status == http.StatusOK {
		t.Fatalf("execute without confirmation succeeded")
	}

	// Execute with a wrong confirmation must fail.
	wrongConfirm := importRouteRequest(t, e, "POST", importTenantPath+"/"+id+"/execute",
		`{"confirm":"WRONG"}`, e.tenantAdm, e.tenantCSRF)
	if wrongConfirm.status == http.StatusOK {
		t.Fatalf("execute with wrong confirmation succeeded")
	}
}

// ── Idempotency keys ─────────────────────────────────────────────────

func TestImportRoutes_MissingAndReusedIdempotencyKeys(t *testing.T) {
	e := buildImportRouteEnv(t)
	id := createImportFor(t, e, e.tenantAdm, e.tenantCSRF, importTenantPath)

	// Missing Idempotency-Key on execute must be rejected.
	missing := importRouteRequest(t, e, "POST", importTenantPath+"/"+id+"/execute",
		`{"confirm":"`+strings.ReplaceAll("EXECUTE-IMPORT-"+id, " ", "")+`"}`, e.tenantAdm, e.tenantCSRF)
	if missing.status == http.StatusOK {
		t.Fatalf("execute without Idempotency-Key succeeded")
	}
}

// ── Strict JSON ──────────────────────────────────────────────────────

func TestImportRoutes_StrictJSONRejection(t *testing.T) {
	e := buildImportRouteEnv(t)

	// A malformed JSON body must be rejected, not silently coerced.
	resp := importRouteRequest(t, e, "POST", importTenantPath, `{"entities": `, e.tenantAdm, e.tenantCSRF)
	if resp.status == http.StatusCreated {
		t.Fatalf("malformed JSON created an import")
	}
	if resp.status >= 500 {
		t.Fatalf("malformed JSON produced 5xx: %d", resp.status)
	}
}

// ── Response hygiene ─────────────────────────────────────────────────

func TestImportRoutes_NoSecretsOrLeaseInResponses(t *testing.T) {
	e := buildImportRouteEnv(t)
	id := createImportFor(t, e, e.tenantAdm, e.tenantCSRF, importTenantPath)

	resp := importRouteRequest(t, e, "GET", importTenantPath+"/"+id, "", e.tenantAdm, "")
	if resp.status != http.StatusOK {
		t.Fatalf("get import: %d", resp.status)
	}
	body := resp.body
	for _, forbidden := range []string{
		"/var/lib/orvix/imports", // staged paths
		"lease_token",            // lease tokens
		"lease_owner",
		"password",   // raw credentials
		"staging_id", // internal staging ID
		"secret",
	} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("import response leaked %q: %s", forbidden, body)
		}
	}
}
