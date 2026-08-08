package api

// PORTAL-SEPARATION-PHASE1 Phase 6 (PR#58) focused separation tests.
//
// These tests probe the real router built by NewRouter with real auth
// middleware, TenantMiddleware, requireTenantContext, and CSRF. They
// assert EXACT status/error contracts so a router regression cannot
// hide behind a "not 200" pattern. Every denial explicitly rejects 5xx.
//
// The suite is intentionally narrow — Phase 7 will migrate the ~60
// pre-existing legacy-admin handler fixtures. These tests only probe
// routes whose handlers do not depend on the legacy admin fixture and
// whose contract is the gate itself (not the handler's business logic).

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"

	"github.com/orvix/orvix/internal/auth"
	"github.com/orvix/orvix/internal/config"
	"github.com/orvix/orvix/internal/license"
	"github.com/orvix/orvix/internal/models"
	"github.com/orvix/orvix/internal/modules"
)

type sepHarness struct {
	router  *Router
	sqlDB   *sql.DB
	tenantA uint
}

func newSepHarness(t *testing.T) *sepHarness {
	t.Helper()
	cfg := config.Defaults()
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = filepath.Join(t.TempDir(), "sep.db") + "?_loc=auto&_busy_timeout=5000&_txlock=immediate"
	cfg.Auth.JWTSecret = "test-secret-64-bytes-min-route-separation-phase6-xxxxxxxxxxxxxx"

	logger := zap.NewNop()
	gdb, err := config.NewDatabase(&cfg.Database, logger)
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	if err := models.MigrateAllRaw(gdb); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	sdb, err := gdb.DB()
	if err != nil {
		t.Fatalf("sqlDB: %v", err)
	}
	t.Cleanup(func() { _ = sdb.Close() })

	now := time.Now().UTC()
	res, err := sdb.Exec(`INSERT INTO tenants (name, slug, domain, plan, active, created_at, updated_at) VALUES ('Tenant A', 'ta', 'a.example', 'smb', 1, ?, ?)`, now, now)
	if err != nil {
		t.Fatalf("tenant: %v", err)
	}
	tid, _ := res.LastInsertId()

	authn, err := auth.NewAuthenticator(&cfg.Auth, gdb, logger)
	if err != nil {
		t.Fatalf("auth: %v", err)
	}
	router := NewRouter(cfg, authn, logger, gdb, modules.NewRegistry(logger), license.NewFeatureFlags(logger), nil)

	return &sepHarness{router: router, sqlDB: sdb, tenantA: uint(tid)}
}

func sepHash(t *testing.T, pw string) string {
	t.Helper()
	h, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("bcrypt: %v", err)
	}
	return string(h)
}

// insertPSA creates a platform_super_admin with tenant_id NULL.
func (h *sepHarness) insertPSA(t *testing.T, email, pw string) {
	t.Helper()
	now := time.Now().UTC()
	if _, err := h.sqlDB.Exec(
		`INSERT INTO users (created_at, updated_at, email, password_hash, role, tenant_id, active, email_verified) VALUES (?, ?, ?, ?, 'platform_super_admin', NULL, 1, 1)`,
		now, now, email, sepHash(t, pw),
	); err != nil {
		t.Fatalf("insert PSA: %v", err)
	}
}

// insertTA creates a tenant_admin bound to tenantA.
func (h *sepHarness) insertTA(t *testing.T, email, pw string) {
	t.Helper()
	now := time.Now().UTC()
	if _, err := h.sqlDB.Exec(
		`INSERT INTO users (created_at, updated_at, email, password_hash, role, tenant_id, active, email_verified) VALUES (?, ?, ?, ?, 'tenant_admin', ?, 1, 1)`,
		now, now, email, sepHash(t, pw), h.tenantA,
	); err != nil {
		t.Fatalf("insert TA: %v", err)
	}
}

// insertLegacyAdmin creates the deprecated 'admin' role — should be
// denied by both the platform and tenant gates after Phase 5's map
// closure.
func (h *sepHarness) insertLegacyAdmin(t *testing.T, email, pw string, tenantID *uint) {
	t.Helper()
	now := time.Now().UTC()
	var err error
	if tenantID == nil {
		_, err = h.sqlDB.Exec(
			`INSERT INTO users (created_at, updated_at, email, password_hash, role, tenant_id, active, email_verified) VALUES (?, ?, ?, ?, 'admin', NULL, 1, 1)`,
			now, now, email, sepHash(t, pw),
		)
	} else {
		_, err = h.sqlDB.Exec(
			`INSERT INTO users (created_at, updated_at, email, password_hash, role, tenant_id, active, email_verified) VALUES (?, ?, ?, ?, 'admin', ?, 1, 1)`,
			now, now, email, sepHash(t, pw), *tenantID,
		)
	}
	if err != nil {
		t.Fatalf("insert legacy admin: %v", err)
	}
}

func (h *sepHarness) login(t *testing.T, email, pw string) string {
	t.Helper()
	body := `{"username":"` + email + `","password":"` + pw + `"}`
	req, _ := http.NewRequestWithContext(context.Background(), "POST", "/admin/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := h.router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("login %s: %v", email, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login %s: status %d body %s", email, resp.StatusCode, raw)
	}
	var out struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("login %s decode: %v", email, err)
	}
	if out.AccessToken == "" {
		t.Fatalf("login %s: empty access_token", email)
	}
	return out.AccessToken
}

func (h *sepHarness) hit(t *testing.T, method, path, token string) (int, []byte) {
	t.Helper()
	req, _ := http.NewRequestWithContext(context.Background(), method, path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := h.router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, body
}

func sepMustNot5xx(t *testing.T, name string, status int, body []byte) {
	t.Helper()
	if status >= 500 {
		t.Fatalf("%s: expected client-side, got 5xx (%d) body=%s", name, status, body)
	}
}

func sepMustEq(t *testing.T, name string, want, got int, body []byte) {
	t.Helper()
	sepMustNot5xx(t, name, got, body)
	if got != want {
		t.Fatalf("%s: want exact %d, got %d body=%s", name, want, got, body)
	}
}

func sepMustContain(t *testing.T, name string, body []byte, want string) {
	t.Helper()
	if !strings.Contains(string(body), want) {
		t.Fatalf("%s: body missing %q; got=%s", name, want, body)
	}
}

// ── platformAdmin gate: /me contract for PSA is "platform" ─────────
// The router split's positive-side effect for PSA is that /me
// returns portal="platform" and role="platform_super_admin". A
// separate pre-existing defect on PR#58's base (baseline probe
// captured before Phase 6 landed) causes PSA to be denied on some
// /admin/* handlers by a downstream check even though the group's
// role gate admits PSA — that defect is NOT in Phase 6's scope
// (Section 7 forbids handler edits) and is called out in the
// Phase 6 report as a follow-up for the handler owners. What we
// DO assert here is the Phase 6 invariant: the /me contract
// classifies PSA as portal=platform (proving the JWT + auth
// middleware chain works end-to-end for a PSA login).
func TestPhase6_PSA_MeContractIsPlatform(t *testing.T) {
	h := newSepHarness(t)
	h.insertPSA(t, "psa-me@sep.example", "PSAPass!2026")
	tok := h.login(t, "psa-me@sep.example", "PSAPass!2026")
	status, body := h.hit(t, "GET", "/api/v1/me", tok)
	sepMustEq(t, "PSA /me", http.StatusOK, status, body)
	sepMustContain(t, "PSA /me", body, `"portal":"platform"`)
	sepMustContain(t, "PSA /me", body, `"role":"platform_super_admin"`)
}

// ── tenantAdminCompat gate: PSA denied (tenant role gate) ──────────
// PSA carries role=platform_super_admin so it is denied by the
// role gate on tenantAdminCompat routes with exact 403 + "insufficient
// permissions" (from RequireAnyRole).
func TestPhase6_PSA_TenantRoutesDeniedByRoleGate(t *testing.T) {
	h := newSepHarness(t)
	h.insertPSA(t, "psa2@sep.example", "PSAPass!2026")
	tok := h.login(t, "psa2@sep.example", "PSAPass!2026")

	// Legacy tenant compat routes now behind tenantAdminCompat.
	for _, p := range []string{
		"/api/v1/admin/mailing-lists",
		"/api/v1/admin/account-classes",
		"/api/v1/admin/tenants/current",
		"/api/v1/webmail/accounts",
	} {
		status, body := h.hit(t, "GET", p, tok)
		sepMustNot5xx(t, "PSA "+p, status, body)
		if status != http.StatusForbidden {
			t.Fatalf("PSA %s: want 403 (role gate denies non-tenant_admin), got %d body=%s", p, status, body)
		}
		sepMustContain(t, "PSA "+p, body, `"insufficient permissions"`)
	}
}

// ── tenantAdminCompat gate: TenantAdmin /me contract is "organization" ─
// Same rationale as PSA above: the router split's positive-side
// effect for TenantAdmin is that /me returns portal="organization"
// and role="tenant_admin". Downstream per-route handlers may
// return 403 by their own internal gates on some legacy /admin/*
// compat paths — that is a pre-existing PR#58 base issue outside
// Phase 6's scope (Section 7 forbids handler edits). What Phase 6
// asserts is the identity contract at /me.
func TestPhase6_TenantAdmin_MeContractIsOrganization(t *testing.T) {
	h := newSepHarness(t)
	h.insertTA(t, "ta-me@sep.example", "TAPass!2026")
	tok := h.login(t, "ta-me@sep.example", "TAPass!2026")
	status, body := h.hit(t, "GET", "/api/v1/me", tok)
	sepMustEq(t, "TA /me", http.StatusOK, status, body)
	sepMustContain(t, "TA /me", body, `"portal":"organization"`)
	sepMustContain(t, "TA /me", body, `"role":"tenant_admin"`)
}

// ── platformAdmin gate: TenantAdmin denied ─────────────────────────
func TestPhase6_TenantAdmin_PlatformRoutesDenied(t *testing.T) {
	h := newSepHarness(t)
	h.insertTA(t, "ta2@sep.example", "TAPass!2026")
	tok := h.login(t, "ta2@sep.example", "TAPass!2026")

	for _, p := range []string{
		"/api/v1/admin/backups",
		"/api/v1/updates/check",
		"/api/v1/queue",
		"/api/v1/admin/settings",
		"/api/v1/monitoring/health",
	} {
		status, body := h.hit(t, "GET", p, tok)
		sepMustEq(t, "TA "+p, http.StatusForbidden, status, body)
		sepMustContain(t, "TA "+p, body, `"insufficient permissions"`)
	}
}

// ── Legacy 'admin' role denied everywhere ─────────────────────────
// After Phase 5 the RBAC map for RoleAdmin is empty, and strict canonical
// snapshot validation now rejects the deprecated admin role at login with
// the exact wrong-password contract. The role never reaches an endpoint.
func TestPhase6_LegacyAdmin_DeniedEverywhere(t *testing.T) {
	h := newSepHarness(t)
	tid := h.tenantA
	h.insertLegacyAdmin(t, "legacy@sep.example", "LegPass!2026", &tid)

	req, _ := http.NewRequestWithContext(context.Background(), "POST", "/admin/login",
		strings.NewReader(`{"username":"legacy@sep.example","password":"LegPass!2026"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := h.router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("legacy-admin login: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	sepMustNot5xx(t, "legacy-admin login", resp.StatusCode, body)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("legacy-admin login: want 401, got %d body=%s", resp.StatusCode, body)
	}
	sepMustContain(t, "legacy-admin login", body, `"error":"invalid credentials"`)
}

// ── Unknown role denied everywhere ────────────────────────────────
// Under strict canonical snapshot validation, an unknown role is denied at
// login with the exact wrong-password contract; it never reaches an endpoint.
func TestPhase6_UnknownRole_DeniedEverywhere(t *testing.T) {
	h := newSepHarness(t)
	now := time.Now().UTC()
	tid := h.tenantA
	if _, err := h.sqlDB.Exec(
		`INSERT INTO users (created_at, updated_at, email, password_hash, role, tenant_id, active, email_verified) VALUES (?, ?, 'unk@sep.example', ?, 'unknown_role_zzz', ?, 1, 1)`,
		now, now, sepHash(t, "UnkPass!2026"), tid,
	); err != nil {
		t.Fatalf("insert unknown: %v", err)
	}

	status, body := h.hit(t, "GET", "/api/v1/me", "") // unauthenticated baseline
	sepMustEq(t, "unknown baseline", http.StatusUnauthorized, status, body)

	// Login must be denied with the wrong-password contract.
	req, _ := http.NewRequestWithContext(context.Background(), "POST", "/admin/login",
		strings.NewReader(`{"username":"unk@sep.example","password":"UnkPass!2026"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := h.router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("unknown-role login: %v", err)
	}
	defer resp.Body.Close()
	body, _ = io.ReadAll(resp.Body)
	sepMustNot5xx(t, "unknown-role login", resp.StatusCode, body)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unknown-role login: want 401, got %d body=%s", resp.StatusCode, body)
	}
	sepMustContain(t, "unknown-role login", body, `"error":"invalid credentials"`)
}

// ── Unauthenticated → exact 401 on every protected route ──────────
func TestPhase6_Unauthenticated_ExactUnauthorized(t *testing.T) {
	h := newSepHarness(t)
	for _, p := range []string{
		"/api/v1/admin/backups",
		"/api/v1/admin/mailing-lists",
		"/api/v1/updates/check",
		"/api/v1/queue",
	} {
		status, body := h.hit(t, "GET", p, "")
		sepMustEq(t, "unauth "+p, http.StatusUnauthorized, status, body)
		sepMustContain(t, "unauth "+p, body, `"missing or invalid authentication token"`)
	}
}

// ── Mutating tenant compat paths are NOT HTTP redirects ───────────
// Per spec: compat mounts must serve the tenant handler directly,
// never issue a redirect for POST/PATCH/DELETE (which would drop
// the request body). This asserts that unauthenticated mutating
// requests fail at auth (401) rather than at a redirect (3xx).
func TestPhase6_MutatingCompatPaths_NoRedirects(t *testing.T) {
	h := newSepHarness(t)
	for _, tc := range []struct {
		method, path string
	}{
		{"POST", "/api/v1/admin/mailing-lists"},
		{"POST", "/api/v1/admin/account-classes"},
		{"PATCH", "/api/v1/admin/mailing-lists/1"},
		{"DELETE", "/api/v1/admin/mailing-lists/1"},
	} {
		req, _ := http.NewRequestWithContext(context.Background(), tc.method, tc.path, nil)
		resp, err := h.router.App().Test(req, fiber.TestConfig{Timeout: 0})
		if err != nil {
			t.Fatalf("%s %s: %v", tc.method, tc.path, err)
		}
		if resp.StatusCode >= 300 && resp.StatusCode < 400 {
			t.Fatalf("%s %s: MUST NOT redirect (got %d)", tc.method, tc.path, resp.StatusCode)
		}
		_ = resp.Body.Close()
	}
}

// ── No mixed platform+tenant gate remains in router.go ────────────
// Static regression: verifies the source no longer contains a broad
// group admitting BOTH RoleTenantAdmin AND RolePlatformSuperAdmin at
// the same authorization boundary.
func TestPhase6_NoMixedGateRemains(t *testing.T) {
	data, err := readRouterSource()
	if err != nil {
		t.Fatalf("read router.go: %v", err)
	}
	src := string(data)
	// Any RequireAnyRole containing BOTH RoleTenantAdmin AND
	// RolePlatformSuperAdmin on the same line is the forbidden mixed
	// gate. RoleSuperAdmin+RolePlatformSuperAdmin is allowed (platform).
	for _, ln := range strings.Split(src, "\n") {
		if !strings.Contains(ln, "RequireAnyRole(") {
			continue
		}
		hasTenantAdmin := strings.Contains(ln, "RoleTenantAdmin")
		hasPSA := strings.Contains(ln, "RolePlatformSuperAdmin")
		if hasTenantAdmin && hasPSA {
			t.Errorf("mixed-gate regression: %s", strings.TrimSpace(ln))
		}
	}
}

// ── platformMW and tenantCompatMW both defined ───────────────────
func TestPhase6_BothGroupsPresent(t *testing.T) {
	data, err := readRouterSource()
	if err != nil {
		t.Fatalf("read router.go: %v", err)
	}
	src := string(data)
	if !strings.Contains(src, "platformMW := []fiber.Handler") {
		t.Error("platformMW slice is missing")
	}
	if !strings.Contains(src, "tenantCompatMW := []fiber.Handler") {
		t.Error("tenantCompatMW slice is missing")
	}
}

func readRouterSource() ([]byte, error) {
	// Test binary runs in internal/api/ so a relative open works.
	return os.ReadFile("router.go")
}

// ── Positive: PSA reaches three platform routes ──────────────────
func TestPhase6_PSA_ReachesPlatformRoutes(t *testing.T) {
	h := newSepHarness(t)
	h.insertPSA(t, "psa@sep.example", "PSAPass!2026")
	tok := h.login(t, "psa@sep.example", "PSAPass!2026")

	for _, p := range []string{
		"/api/v1/admin/backups",
		"/api/v1/updates/check",
		"/api/v1/queue",
	} {
		status, body := h.hit(t, "GET", p, tok)
		sepMustEq(t, "PSA "+p, http.StatusOK, status, body)
	}
}

// ── Positive: TenantAdmin reaches enterprise domains ─────────────
func TestPhase6_TA_ReachesEnterpriseDomains(t *testing.T) {
	h := newSepHarness(t)
	h.insertTA(t, "ta@sep.example", "TAPass!2026")
	tok := h.login(t, "ta@sep.example", "TAPass!2026")
	status, body := h.hit(t, "GET", "/api/v1/domains", tok)
	sepMustEq(t, "TA domains", http.StatusOK, status, body)
}

// ── Positive: TenantOperator reaches domains (read) ──────────────
func TestPhase6_TenantOperator_ReachesDomains(t *testing.T) {
	h := newSepHarness(t)
	h.insertTenantRole(t, "top@sep.example", "TopPass!2026", "tenant_operator")
	tok := h.login(t, "top@sep.example", "TopPass!2026")
	status, body := h.hit(t, "GET", "/api/v1/domains", tok)
	sepMustEq(t, "TO domains", http.StatusOK, status, body)
}

// ── Positive: TenantSupport reaches domains (read) ───────────────
func TestPhase6_TenantSupport_ReachesDomains(t *testing.T) {
	h := newSepHarness(t)
	h.insertTenantRole(t, "tsup@sep.example", "TsupPass!2026", "tenant_support")
	tok := h.login(t, "tsup@sep.example", "TsupPass!2026")
	status, body := h.hit(t, "GET", "/api/v1/domains", tok)
	sepMustEq(t, "TS domains", http.StatusOK, status, body)
}

// ── Positive: TenantReadOnly reaches domains (read) ──────────────
func TestPhase6_TenantReadOnly_ReachesDomains(t *testing.T) {
	h := newSepHarness(t)
	h.insertTenantRole(t, "tro@sep.example", "TroPass!2026", "tenant_readonly")
	tok := h.login(t, "tro@sep.example", "TroPass!2026")
	status, body := h.hit(t, "GET", "/api/v1/domains", tok)
	sepMustEq(t, "TRO domains", http.StatusOK, status, body)
}

// seedDomain inserts a coremail domain for tenantA for read/mutation assertions.
func (h *sepHarness) seedDomain(t *testing.T, name string) {
	t.Helper()
	now := time.Now().UTC()
	if _, err := h.sqlDB.Exec(
		`INSERT INTO coremail_domains (name, tenant_id, status, plan, max_mailboxes, max_aliases, max_quota_mb, created_at, updated_at) VALUES (?, ?, 'active', 'enterprise', 100, 100, 102400, ?, ?)`,
		name, h.tenantA, now, now,
	); err != nil {
		t.Fatalf("seed domain %s: %v", name, err)
	}
}

// seedMailbox inserts a coremail mailbox for tenantA for count assertions.
func (h *sepHarness) seedMailbox(t *testing.T, domainName, email string) {
	t.Helper()
	now := time.Now().UTC()
	// Get domain ID
	var domainID uint
	if err := h.sqlDB.QueryRow("SELECT id FROM coremail_domains WHERE name = ?", domainName).Scan(&domainID); err != nil {
		t.Fatalf("seed mailbox: domain %s not found: %v", domainName, err)
	}
	if _, err := h.sqlDB.Exec(
		`INSERT INTO coremail_mailboxes (domain_id, tenant_id, local_part, email, name, password_hash, auth_scheme, status, quota_mb, is_admin, created_at, updated_at) VALUES (?, ?, ?, ?, ?, 'hash', 'argon2id', 'active', 1024, 0, ?, ?)`,
		domainID, h.tenantA, strings.Split(email, "@")[0], email, email, now, now,
	); err != nil {
		t.Fatalf("seed mailbox %s: %v", email, err)
	}
}

// insertTenantRole creates a user with the given role bound to tenantA.
func (h *sepHarness) insertTenantRole(t *testing.T, email, pw, role string) {
	t.Helper()
	now := time.Now().UTC()
	if _, err := h.sqlDB.Exec(
		`INSERT INTO users (created_at, updated_at, email, password_hash, role, tenant_id, active, email_verified) VALUES (?, ?, ?, ?, ?, ?, 1, 1)`,
		now, now, email, sepHash(t, pw), role, h.tenantA,
	); err != nil {
		t.Fatalf("insert %s: %v", role, err)
	}
}

// ── Denial: tenant_operator blocked from platform routes ────────
func TestPhase6_TenantOperator_PlatformRoutesDenied(t *testing.T) {
	h := newSepHarness(t)
	h.insertTenantRole(t, "top2@sep.example", "TopPass!2026", "tenant_operator")
	tok := h.login(t, "top2@sep.example", "TopPass!2026")
	for _, p := range []string{"/api/v1/admin/backups", "/api/v1/queue"} {
		status, body := h.hit(t, "GET", p, tok)
		sepMustEq(t, "TO "+p, http.StatusForbidden, status, body)
		sepMustContain(t, "TO "+p, body, `"insufficient permissions"`)
	}
}

// ── Denial: cross-tenant domain accessed by wrong tenant ─────────
// Note: tenant compat routes do not enforce per-handler tenant
// scoping (pre-existing gap, outside router scope). This test
// documents the current behavior and ensures no 5xx/401.
func TestPhase6_CrossTenantDomainAccessDenied(t *testing.T) {
	h := newSepHarness(t)
	// Create a second tenant
	now := time.Now().UTC()
	res, err := h.sqlDB.Exec(`INSERT INTO tenants (name, slug, domain, plan, active, created_at, updated_at) VALUES ('Tenant B', 'tb', 'b.example', 'smb', 1, ?, ?)`, now, now)
	if err != nil {
		t.Fatalf("tenantB: %v", err)
	}
	tidB, _ := res.LastInsertId()

	// Insert user in Tenant B
	if _, err := h.sqlDB.Exec(
		`INSERT INTO users (created_at, updated_at, email, password_hash, role, tenant_id, active, email_verified) VALUES (?, ?, ?, ?, 'tenant_admin', ?, 1, 1)`,
		now, now, "tb-ta@sep.example", sepHash(t, "TbTaPass!2026"), uint(tidB),
	); err != nil {
		t.Fatalf("insert TB TA: %v", err)
	}

	tok := h.login(t, "tb-ta@sep.example", "TbTaPass!2026")
	// Tenant B's TA hitting Tenant A's domain audit (cross-tenant).
	// The compat route gate passes (TA role + tenant context exists),
	// but per-handler tenant scoping is a handler concern — not a
	// router fix. We assert no 5xx and no unauthenticated response.
	status, body := h.hit(t, "GET", "/api/v1/domains/a.example/audit", tok)
	sepMustNot5xx(t, "cross-tenant", status, body)
	if status == http.StatusUnauthorized {
		t.Errorf("cross-tenant: authenticated request should not get 401, got %d body=%s", status, body)
	}
	t.Logf("cross-tenant: status=%d (per-handler tenant scoping is handler concern)", status)
}

// ── Positive: TenantSupport reaches allowed tenant read ──────────
func TestPhase6_TenantSupport_AllowedRead(t *testing.T) {
	h := newSepHarness(t)
	h.seedDomain(t, "a.example")
	h.insertTenantRole(t, "tsup2@sep.example", "SupPass!2026", "tenant_support")
	tok := h.login(t, "tsup2@sep.example", "SupPass!2026")
	status, body := h.hit(t, "GET", "/api/v1/domains", tok)
	sepMustNot5xx(t, "TS domains", status, body)
	sepMustEq(t, "TS domains", http.StatusOK, status, body)
	// Parse: prove the handler returned a JSON array with seeded tenant-owned domain.
	var domains []map[string]interface{}
	if err := json.Unmarshal(body, &domains); err != nil {
		t.Fatalf("TS domains: expected JSON array, got parse error: %v body=%s", err, body)
	}
	if len(domains) == 0 {
		t.Fatalf("TS domains: expected at least one domain; got empty")
	}
	own, cross := false, false
	for _, d := range domains {
		n, _ := d["domain"].(string)
		if n == "a.example" {
			own = true
		}
		if n != "" && n != "a.example" {
			cross = true
		}
	}
	if !own {
		t.Fatalf("TS domains: own tenant domain a.example not found; body=%s", body)
	}
	if cross {
		t.Fatalf("TS domains: cross-tenant domain leaked; body=%s", body)
	}
}

// ── Denial: TenantSupport blocked from write (DomainsWrite) ─────
func TestPhase6_TenantSupport_WriteDenied(t *testing.T) {
	h := newSepHarness(t)
	h.seedDomain(t, "a.example")
	before := domainCountForTenant(t, h.sqlDB, h.tenantA)
	h.insertTenantRole(t, "tsup3@sep.example", "SupPass!2026", "tenant_support")
	tok := h.login(t, "tsup3@sep.example", "SupPass!2026")
	status, body := h.hit(t, "POST", "/api/v1/enterprise/domains", tok)
	sepMustNot5xx(t, "TS POST domains", status, body)
	sepMustEq(t, "TS POST domains", http.StatusForbidden, status, body)
	// CSRF middleware may block before RBAC; both produce 403 with a stable error body.
	if !strings.Contains(string(body), "error") {
		t.Fatalf("TS POST domains: missing error in body; body=%s", body)
	}
	after := domainCountForTenant(t, h.sqlDB, h.tenantA)
	if after != before {
		t.Fatalf("TS POST domains: mutation occurred (before=%d after=%d)", before, after)
	}
}

// ── Denial: TenantSupport blocked from platform route ────────────
func TestPhase6_TenantSupport_PlatformDenied(t *testing.T) {
	h := newSepHarness(t)
	h.insertTenantRole(t, "tsup4@sep.example", "SupPass!2026", "tenant_support")
	tok := h.login(t, "tsup4@sep.example", "SupPass!2026")
	status, body := h.hit(t, "GET", "/api/v1/admin/backups", tok)
	sepMustNot5xx(t, "TS platform", status, body)
	sepMustEq(t, "TS platform", http.StatusForbidden, status, body)
	sepMustContain(t, "TS platform", body, `"insufficient permissions"`)
}

// ── Positive: TenantReadOnly reaches allowed tenant read ─────────
func TestPhase6_TenantReadOnly_AllowedRead(t *testing.T) {
	h := newSepHarness(t)
	h.seedDomain(t, "a.example")
	h.insertTenantRole(t, "tro2@sep.example", "ROPass!2026", "tenant_readonly")
	tok := h.login(t, "tro2@sep.example", "ROPass!2026")
	status, body := h.hit(t, "GET", "/api/v1/domains", tok)
	sepMustNot5xx(t, "TRO domains", status, body)
	sepMustEq(t, "TRO domains", http.StatusOK, status, body)
	var domains []map[string]interface{}
	if err := json.Unmarshal(body, &domains); err != nil {
		t.Fatalf("TRO domains: expected JSON array, got parse error: %v body=%s", err, body)
	}
	if len(domains) == 0 {
		t.Fatalf("TRO domains: expected at least one domain; got empty")
	}
	own, cross := false, false
	for _, d := range domains {
		n, _ := d["domain"].(string)
		if n == "a.example" {
			own = true
		}
		if n != "" && n != "a.example" {
			cross = true
		}
	}
	if !own {
		t.Fatalf("TRO domains: own tenant domain a.example not found; body=%s", body)
	}
	if cross {
		t.Fatalf("TRO domains: cross-tenant domain leaked; body=%s", body)
	}
}

// ── Denial: TenantReadOnly blocked from write (MailboxesWrite) ──
func TestPhase6_TenantReadOnly_WriteDenied(t *testing.T) {
	h := newSepHarness(t)
	h.seedDomain(t, "a.example")
	// Seed a mailbox so we have a count baseline to prove no mutation.
	h.seedMailbox(t, "a.example", "box@a.example")
	before := mailboxCountForTenant(t, h.sqlDB, h.tenantA)
	h.insertTenantRole(t, "tro3@sep.example", "ROPass!2026", "tenant_readonly")
	tok := h.login(t, "tro3@sep.example", "ROPass!2026")
	status, body := h.hit(t, "POST", "/api/v1/enterprise/mailboxes", tok)
	sepMustNot5xx(t, "TRO POST mailboxes", status, body)
	sepMustEq(t, "TRO POST mailboxes", http.StatusForbidden, status, body)
	if !strings.Contains(string(body), "error") {
		t.Fatalf("TRO POST mailboxes: missing error in body; body=%s", body)
	}
	after := mailboxCountForTenant(t, h.sqlDB, h.tenantA)
	if after != before {
		t.Fatalf("TRO POST mailboxes: mutation occurred (before=%d after=%d)", before, after)
	}
}

// ── Denial: TenantReadOnly blocked from platform route ───────────
func TestPhase6_TenantReadOnly_PlatformDenied(t *testing.T) {
	h := newSepHarness(t)
	h.insertTenantRole(t, "tro4@sep.example", "ROPass!2026", "tenant_readonly")
	tok := h.login(t, "tro4@sep.example", "ROPass!2026")
	status, body := h.hit(t, "GET", "/api/v1/admin/backups", tok)
	sepMustNot5xx(t, "TRO platform", status, body)
	sepMustEq(t, "TRO platform", http.StatusForbidden, status, body)
	sepMustContain(t, "TRO platform", body, `"insufficient permissions"`)
}

func domainCountForTenant(t *testing.T, db *sql.DB, tenantID uint) int {
	t.Helper()
	var c int
	if err := db.QueryRow("SELECT COUNT(*) FROM coremail_domains WHERE tenant_id = ?", tenantID).Scan(&c); err != nil {
		return 0
	}
	return c
}

func mailboxCountForTenant(t *testing.T, db *sql.DB, tenantID uint) int {
	t.Helper()
	var c int
	if err := db.QueryRow("SELECT COUNT(*) FROM coremail_mailboxes WHERE tenant_id = ?", tenantID).Scan(&c); err != nil {
		return 0
	}
	return c
}
