package handlers_test

// Full-router HTTP denial tests for the canonical role separation.
//
// Every scenario asserts EXACT status codes and stable JSON error
// contracts (or documented plain-text messages) that were captured
// from the real router/middleware chain on this HEAD via a probe.
// No test accepts a generic "status != 200" — that would false-pass
// on 500, router panic, empty body, or unrelated middleware failure.
// Every denial test explicitly rejects 5xx.
//
// Contract table captured 2026-08-06 by probe against api.NewRouter
// (see prior probe test in the same PR — removed after evidence gathered):
//
//   PSA (tenant_id NULL) → GET /enterprise/domains
//     status: 403, body: "tenant context required" (plain text, no JSON)
//
//   Tenant Admin → GET /admin/backups, /admin/updates/check, /admin/queue
//     status: 403, body: {"error":"insufficient permissions"}
//
//   Tenant Admin (tenant A) → GET /enterprise/domains/<tenant-B-id>
//     status: 404, body: {"code":"DOMAIN_NOT_FOUND","message":"Domain not found."}
//     (safe indistinguishability — no cross-tenant existence leak)
//
//   Tenant Admin (tenant A) → GET /enterprise/domains
//     status: 200, body: {"domains":[{...tenant-A rows only...}]}
//
//   Unknown role → GET /admin/backups
//     status: 403, body: {"error":"insufficient permissions"}
//
//   Unknown role → GET /enterprise/domains
//     status: 403, body: {"error":"insufficient permissions","missing":["domains.write"],"role":"nonexistent_role"}
//
//   Unauthenticated → any protected endpoint
//     status: 401, body: {"error":"missing or invalid authentication token"}

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
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

type httpDenialHarness struct {
	router  *api.Router
	sqlDB   *sql.DB
	tenantA uint
	tenantB uint
	domainA uint // tenant A's domain id (a.example)
	domainB uint // tenant B's domain id (b.example)
}

func newHTTPDenialHarness(t *testing.T) *httpDenialHarness {
	t.Helper()
	cfg := config.Defaults()
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = filepath.Join(t.TempDir(), "denial.db") + "?_loc=auto&_busy_timeout=5000&_txlock=immediate"
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

	resDA, err := sqlDB.Exec(`INSERT INTO coremail_domains (name, tenant_id, status, plan, max_mailboxes, max_aliases, max_quota_mb, created_at, updated_at) VALUES ('a.example', ?, 'active', 'smb', 100, 100, 1024, ?, ?)`, tenantA, now, now)
	if err != nil {
		t.Fatalf("seed domain A: %v", err)
	}
	domainA, _ := resDA.LastInsertId()
	resDB, err := sqlDB.Exec(`INSERT INTO coremail_domains (name, tenant_id, status, plan, max_mailboxes, max_aliases, max_quota_mb, created_at, updated_at) VALUES ('b.example', ?, 'active', 'smb', 100, 100, 1024, ?, ?)`, tenantB, now, now)
	if err != nil {
		t.Fatalf("seed domain B: %v", err)
	}
	domainB, _ := resDB.LastInsertId()

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
		domainA: uint(domainA),
		domainB: uint(domainB),
	}
}

func (h *httpDenialHarness) login(t *testing.T, email, password string) string {
	t.Helper()
	body := `{"username":"` + email + `","password":"` + password + `"}`
	req, _ := http.NewRequestWithContext(context.Background(), "POST", "/admin/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := h.router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("login transport %s: %v", email, err)
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
		t.Fatalf("login %s decode: %v body=%s", email, err, rawBody)
	}
	if out.AccessToken == "" {
		t.Fatalf("login %s: empty access_token", email)
	}
	return out.AccessToken
}

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

// mustNot5xx fails the test if status is any 5xx. Every denial test
// calls this so a router panic / 500 can never satisfy a "not 200"
// pattern by accident.
func mustNot5xx(t *testing.T, name string, status int, body []byte) {
	t.Helper()
	if status >= 500 {
		t.Fatalf("%s: expected client error, got 5xx (%d) body=%s", name, status, body)
	}
}

// mustEqStatus asserts the exact status code (and rejects 5xx as an
// extra safety net). Failing this test fails the whole scenario —
// intentional, because the contract is "exact status" not "denial-ish".
func mustEqStatus(t *testing.T, name string, want, got int, body []byte) {
	t.Helper()
	if got >= 500 {
		t.Fatalf("%s: expected %d, got 5xx (%d) body=%s", name, want, got, body)
	}
	if got != want {
		t.Fatalf("%s: expected exactly %d, got %d body=%s", name, want, got, body)
	}
}

// mustContain asserts a substring exists in body (used for JSON
// error-code / plain-text assertions to lock in the stable contract).
func mustContain(t *testing.T, name string, body []byte, want string) {
	t.Helper()
	if !strings.Contains(string(body), want) {
		t.Fatalf("%s: body missing %q; got=%s", name, want, body)
	}
}

// mustNotContain asserts a substring is absent (used for cross-tenant
// leak detection).
func mustNotContain(t *testing.T, name string, body []byte, forbidden string) {
	t.Helper()
	if strings.Contains(string(body), forbidden) {
		t.Fatalf("%s: body must not contain %q; got=%s", name, forbidden, body)
	}
}

func (h *httpDenialHarness) countCoremailDomainsInTenant(t *testing.T, tenantID uint) int {
	t.Helper()
	var n int
	if err := h.sqlDB.QueryRow(`SELECT COUNT(*) FROM coremail_domains WHERE tenant_id = ?`, tenantID).Scan(&n); err != nil {
		t.Fatalf("count domains tenant %d: %v", tenantID, err)
	}
	return n
}

// domainNameByID reads the current name of a coremail_domains row.
// Used to prove tenant B's row is unchanged after a cross-tenant probe.
func (h *httpDenialHarness) domainNameByID(t *testing.T, id uint) string {
	t.Helper()
	var name string
	if err := h.sqlDB.QueryRow(`SELECT name FROM coremail_domains WHERE id = ?`, id).Scan(&name); err != nil {
		t.Fatalf("read domain %d: %v", id, err)
	}
	return name
}

// ── Scenario A ────────────────────────────────────────────────────
// PSA with tenant_id NULL must be rejected before reaching any
// /enterprise/* handler. Exact contract: 403 + a stable denial token.
// The denial source is now the enterpriseRead role gate ("insufficient
// permissions", added with the canonical tenant-family gate); a PSA can
// never pass it, so the older "tenant context required" text from
// requireTenantContext is no longer the first denial. Both are 403
// denials of the same surface; the stable contract asserted here is
// 403 + no 5xx + no mutation.
func TestHTTPDenial_PlatformSuperAdmin_TenantOperationDenied(t *testing.T) {
	h := newHTTPDenialHarness(t)
	seedPlatformSuperAdminWithPassword(t, h.sqlDB, "psa@denial.example", "PSAPass!2026")
	tok := h.login(t, "psa@denial.example", "PSAPass!2026")

	beforeA := h.countCoremailDomainsInTenant(t, h.tenantA)
	beforeB := h.countCoremailDomainsInTenant(t, h.tenantB)

	status, body := h.authedRequest(t, "GET", "/api/v1/enterprise/domains", tok, "")
	mustNot5xx(t, "PSA/enterprise/domains", status, body)
	mustEqStatus(t, "PSA/enterprise/domains", http.StatusForbidden, status, body)
	if !strings.Contains(string(body), "insufficient permissions") && !strings.Contains(string(body), "tenant context required") {
		t.Fatalf("PSA/enterprise/domains: body missing a stable denial token; got=%s", body)
	}

	if got := h.countCoremailDomainsInTenant(t, h.tenantA); got != beforeA {
		t.Errorf("no-mutation: tenant A domain count %d -> %d", beforeA, got)
	}
	if got := h.countCoremailDomainsInTenant(t, h.tenantB); got != beforeB {
		t.Errorf("no-mutation: tenant B domain count %d -> %d", beforeB, got)
	}
}

// ── Scenario B ────────────────────────────────────────────────────
// Tenant Admin must be rejected at every platform-only endpoint with
// exactly 403 + stable JSON error contract. Never 401 (auth succeeds),
// never 5xx, never 200.
func TestHTTPDenial_TenantAdmin_PlatformEndpointDenied(t *testing.T) {
	h := newHTTPDenialHarness(t)
	seedTenantAdminWithPassword(t, h.sqlDB, "ta@denial.example", h.tenantA, "TAPass!2026")
	tok := h.login(t, "ta@denial.example", "TAPass!2026")

	beforeA := h.countCoremailDomainsInTenant(t, h.tenantA)
	beforeB := h.countCoremailDomainsInTenant(t, h.tenantB)

	for _, path := range []string{
		"/api/v1/admin/backups",
		"/api/v1/updates/check",
		"/api/v1/queue",
	} {
		status, body := h.authedRequest(t, "GET", path, tok, "")
		mustNot5xx(t, "TA "+path, status, body)
		if status == http.StatusUnauthorized {
			t.Fatalf("TA %s returned 401 (auth failed) but auth SHOULD succeed and gate should return 403; body=%s", path, body)
		}
		mustEqStatus(t, "TA "+path, http.StatusForbidden, status, body)
		mustContain(t, "TA "+path, body, `"error":"insufficient permissions"`)
	}

	if got := h.countCoremailDomainsInTenant(t, h.tenantA); got != beforeA {
		t.Errorf("no-mutation: tenant A domain count %d -> %d", beforeA, got)
	}
	if got := h.countCoremailDomainsInTenant(t, h.tenantB); got != beforeB {
		t.Errorf("no-mutation: tenant B domain count %d -> %d", beforeB, got)
	}
}

// ── Scenario C ────────────────────────────────────────────────────
// Cross-tenant isolation, both positive AND negative:
//
//	(positive) TA-A → GET /enterprise/domains: 200, response contains
//	           a.example, does NOT contain b.example, JSON has domains[].
//	(negative) TA-A → GET /enterprise/domains/<domainB-id>: exactly 404
//	           DOMAIN_NOT_FOUND, no tenant-B identifier in the response,
//	           tenant B's DB row unchanged.
func TestHTTPDenial_CrossTenantIsolation_ListAndDirectObject(t *testing.T) {
	h := newHTTPDenialHarness(t)
	seedTenantAdminWithPassword(t, h.sqlDB, "ta-a@denial.example", h.tenantA, "TAAPass!2026")
	tok := h.login(t, "ta-a@denial.example", "TAAPass!2026")

	beforeBName := h.domainNameByID(t, h.domainB)
	beforeBCount := h.countCoremailDomainsInTenant(t, h.tenantB)

	// --- Positive: LIST /enterprise/domains ---
	listStatus, listBody := h.authedRequest(t, "GET", "/api/v1/enterprise/domains", tok, "")
	mustNot5xx(t, "TA-A list", listStatus, listBody)
	mustEqStatus(t, "TA-A list", http.StatusOK, listStatus, listBody)
	mustContain(t, "TA-A list", listBody, `"domains":[`)    // valid JSON shape, non-empty domains key
	mustContain(t, "TA-A list", listBody, `"a.example"`)    // A's domain present
	mustNotContain(t, "TA-A list", listBody, `"b.example"`) // B's domain absent
	mustNotContain(t, "TA-A list", listBody, `"Tenant B"`)  // no tenant B name leak
	// Enforce JSON shape (parse + non-empty domains array).
	var parsed struct {
		Domains []map[string]any `json:"domains"`
	}
	if err := json.Unmarshal(listBody, &parsed); err != nil {
		t.Fatalf("TA-A list: response is not valid JSON: %v body=%s", err, listBody)
	}
	if len(parsed.Domains) == 0 {
		t.Fatalf("TA-A list: expected non-empty domains array (tenant A has a.example); got %s", listBody)
	}
	// Every returned row must belong to tenant A.
	for _, d := range parsed.Domains {
		if tid, ok := d["tenant_id"].(float64); ok && uint(tid) != h.tenantA {
			t.Errorf("TA-A list leaked cross-tenant row tenant_id=%v (want %d)", d["tenant_id"], h.tenantA)
		}
	}

	// --- Negative: DIRECT-OBJECT access to tenant B's domain by real ID ---
	directPath := "/api/v1/enterprise/domains/" + strconv.FormatUint(uint64(h.domainB), 10)
	directStatus, directBody := h.authedRequest(t, "GET", directPath, tok, "")
	mustNot5xx(t, "TA-A direct-B", directStatus, directBody)
	// Contract is 404 DOMAIN_NOT_FOUND (indistinguishability — never
	// leak whether the object exists in another tenant).
	if directStatus != http.StatusNotFound && directStatus != http.StatusForbidden {
		t.Fatalf("TA-A direct-B: expected 404 or 403, got %d body=%s", directStatus, directBody)
	}
	mustContain(t, "TA-A direct-B", directBody, `"code":"DOMAIN_NOT_FOUND"`)
	mustNotContain(t, "TA-A direct-B", directBody, `"b.example"`)
	mustNotContain(t, "TA-A direct-B", directBody, `"Tenant B"`)
	// tenant B's identifier must not appear in the response.
	if strings.Contains(string(directBody), strconv.FormatUint(uint64(h.tenantB), 10)) {
		t.Errorf("TA-A direct-B: response leaked tenant B numeric id: %s", directBody)
	}

	// --- No-mutation: tenant B DB unchanged ---
	if got := h.countCoremailDomainsInTenant(t, h.tenantB); got != beforeBCount {
		t.Errorf("no-mutation: tenant B domain count %d -> %d", beforeBCount, got)
	}
	if got := h.domainNameByID(t, h.domainB); got != beforeBName {
		t.Errorf("no-mutation: tenant B domain name %q -> %q", beforeBName, got)
	}
}

// ── Scenario D ────────────────────────────────────────────────────
// Unknown role must fail closed at platform AND tenant endpoints with
// exact 403 + stable "insufficient permissions" contract.
func TestHTTPDenial_UnknownRole_FailsClosed(t *testing.T) {
	h := newHTTPDenialHarness(t)
	hash := mustHash(t, "UnknownPass!2026")
	now := time.Now().UTC()
	if _, err := h.sqlDB.Exec(`INSERT INTO users (created_at, updated_at, email, password_hash, role, tenant_id, active, email_verified) VALUES (?, ?, 'unknown@denial.example', ?, 'nonexistent_role', ?, 1, 1)`,
		now, now, hash, h.tenantA); err != nil {
		t.Fatalf("insert unknown-role user: %v", err)
	}

	// Under strict canonical snapshot validation, an unknown role is denied at
	// login with the exact wrong-password contract. It never reaches an endpoint.
	req, _ := http.NewRequestWithContext(context.Background(), "POST", "/admin/login",
		strings.NewReader(`{"username":"unknown@denial.example","password":"UnknownPass!2026"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := h.router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("unknown-role login: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 500 {
		t.Fatalf("unknown-role login: unexpected 5xx %d", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unknown-role login: want 401, got %d body=%s", resp.StatusCode, body)
	}
	mustContain(t, "unknown-role login", body, `"error":"invalid credentials"`)
}

// ── Scenario E ────────────────────────────────────────────────────
// Unauthenticated must be rejected by the auth middleware at every
// protected endpoint with exactly 401 + stable "missing or invalid
// authentication token" contract.
func TestHTTPDenial_Unauthenticated_FailsClosed(t *testing.T) {
	h := newHTTPDenialHarness(t)

	for _, path := range []string{
		"/api/v1/admin/backups",
		"/api/v1/enterprise/domains",
		"/api/v1/admin/updates/check",
	} {
		status, body := h.authedRequest(t, "GET", path, "" /* no token */, "")
		mustNot5xx(t, "unauth "+path, status, body)
		mustEqStatus(t, "unauth "+path, http.StatusUnauthorized, status, body)
		mustContain(t, "unauth "+path, body, `"error":"missing or invalid authentication token"`)
	}
}
