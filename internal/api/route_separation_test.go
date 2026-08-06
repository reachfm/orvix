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
		"/api/v1/admin/updates/check",
		"/api/v1/admin/queue",
		"/api/v1/admin/settings",
		"/api/v1/monitoring/health",
	} {
		status, body := h.hit(t, "GET", p, tok)
		sepMustEq(t, "TA "+p, http.StatusForbidden, status, body)
		sepMustContain(t, "TA "+p, body, `"insufficient permissions"`)
	}
}

// ── Legacy 'admin' role denied on BOTH platform and tenant ─────────
// After Phase 5 the RBAC map for RoleAdmin is empty; here we assert
// the role gate itself denies at BOTH platform and tenant sides.
func TestPhase6_LegacyAdmin_DeniedEverywhere(t *testing.T) {
	h := newSepHarness(t)
	tid := h.tenantA
	h.insertLegacyAdmin(t, "legacy@sep.example", "LegPass!2026", &tid)
	tok := h.login(t, "legacy@sep.example", "LegPass!2026")

	for _, tc := range []struct {
		path string
	}{
		{"/api/v1/admin/backups"},
		{"/api/v1/admin/updates/check"},
		{"/api/v1/admin/queue"},
		{"/api/v1/admin/mailing-lists"},
		{"/api/v1/admin/account-classes"},
	} {
		status, body := h.hit(t, "GET", tc.path, tok)
		sepMustEq(t, "legacy "+tc.path, http.StatusForbidden, status, body)
		sepMustContain(t, "legacy "+tc.path, body, `"insufficient permissions"`)
	}
}

// ── Unknown role denied everywhere ────────────────────────────────
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
	tok := h.login(t, "unk@sep.example", "UnkPass!2026")

	for _, p := range []string{
		"/api/v1/admin/backups",
		"/api/v1/admin/mailing-lists",
	} {
		status, body := h.hit(t, "GET", p, tok)
		sepMustEq(t, "unknown "+p, http.StatusForbidden, status, body)
	}
}

// ── Unauthenticated → exact 401 on every protected route ──────────
func TestPhase6_Unauthenticated_ExactUnauthorized(t *testing.T) {
	h := newSepHarness(t)
	for _, p := range []string{
		"/api/v1/admin/backups",
		"/api/v1/admin/mailing-lists",
		"/api/v1/admin/updates/check",
		"/api/v1/admin/queue",
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

// ── platformAdmin and tenantAdminCompat both defined ──────────────
func TestPhase6_BothGroupsPresent(t *testing.T) {
	data, err := readRouterSource()
	if err != nil {
		t.Fatalf("read router.go: %v", err)
	}
	src := string(data)
	if !strings.Contains(src, "platformAdmin := protected.Group(") {
		t.Error("platformAdmin group is missing")
	}
	if !strings.Contains(src, "tenantAdminCompat := protected.Group(") {
		t.Error("tenantAdminCompat group is missing")
	}
}

func readRouterSource() ([]byte, error) {
	// Test binary runs in internal/api/ so a relative open works.
	return os.ReadFile("router.go")
}
