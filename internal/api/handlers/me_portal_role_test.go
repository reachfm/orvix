package handlers_test

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

	"github.com/orvix/orvix/internal/admin/organization"
	"github.com/orvix/orvix/internal/api"
	"github.com/orvix/orvix/internal/auth"
	"github.com/orvix/orvix/internal/config"
	"github.com/orvix/orvix/internal/license"
	"github.com/orvix/orvix/internal/models"
	"github.com/orvix/orvix/internal/modules"
)

// mePortalHarness stands up a real router + SQLite DB so /me and CountAdmins
// can be exercised end-to-end, mirroring the pattern in
// customer_org_role_security_test.go.
type mePortalHarness struct {
	router *api.Router
	sqlDB  *sql.DB
	authn  *auth.Authenticator
}

func newMePortalHarness(t *testing.T) *mePortalHarness {
	t.Helper()
	cfg := config.Defaults()
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = filepath.Join(t.TempDir(), "meportal.db") + "?_loc=auto&_busy_timeout=5000&_journal_mode=WAL"
	cfg.Auth.JWTSecret = "test-secret-64-bytes-me-portal-role-security-fixture-XXXXXX"
	cfg.Auth.JWTKeyPath = filepath.Join(t.TempDir(), "jwt.pem")

	logger := zap.NewNop()
	gdb, err := config.NewDatabase(&cfg.Database, logger)
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	if err := models.MigrateAllRaw(gdb); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	sqlDB, _ := gdb.DB()
	t.Cleanup(func() { sqlDB.Close() })

	authn, err := auth.NewAuthenticator(&cfg.Auth, gdb, logger)
	if err != nil {
		t.Fatalf("authn: %v", err)
	}

	adminUI := filepath.Join(t.TempDir(), "admin")
	webmailUI := filepath.Join(t.TempDir(), "webmail")
	os.MkdirAll(adminUI, 0755)
	os.MkdirAll(webmailUI, 0755)
	os.WriteFile(filepath.Join(adminUI, "index.html"), []byte("<html></html>"), 0644)
	os.WriteFile(filepath.Join(webmailUI, "index.html"), []byte("<html></html>"), 0644)
	os.WriteFile(filepath.Join(webmailUI, "webmail.js"), []byte(""), 0644)
	os.WriteFile(filepath.Join(webmailUI, "webmail.css"), []byte(""), 0644)
	cfg.Server.AdminUIDir = adminUI
	cfg.Server.WebmailUIDir = webmailUI

	router := api.NewRouter(cfg, authn, logger, gdb, modules.NewRegistry(logger), license.NewFeatureFlags(logger), nil)
	t.Cleanup(func() { router.App().Shutdown() })

	return &mePortalHarness{router: router, sqlDB: sqlDB, authn: authn}
}

func (h *mePortalHarness) login(t *testing.T, email, password string) string {
	t.Helper()
	body := `{"username":"` + email + `","password":"` + password + `"}`
	req, _ := http.NewRequestWithContext(context.Background(), "POST", "/admin/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := h.router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("login %s: %v", email, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login %s: status %d body=%s", email, resp.StatusCode, raw)
	}
	var out struct {
		AccessToken string `json:"access_token"`
	}
	json.Unmarshal(raw, &out)
	if out.AccessToken == "" {
		t.Fatalf("login %s: empty token", email)
	}
	return out.AccessToken
}

func (h *mePortalHarness) me(t *testing.T, bearer string) (int, map[string]interface{}) {
	t.Helper()
	req, _ := http.NewRequestWithContext(context.Background(), "GET", "/api/v1/me", nil)
	req.Header.Set("Authorization", "Bearer "+bearer)
	resp, err := h.router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("me: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]interface{}
	json.Unmarshal(raw, &out)
	return resp.StatusCode, out
}

// TestMePortal_SelfSignupOwnerGetsOrganizationPortal is the regression
// test for Bug 1 (fixed canonically): a self-signup tenant owner is
// persisted as tenant_admin by the server (customer_auth.go /
// customer_signup_otp.go) and must land in the organization portal.
func TestMePortal_SelfSignupOwnerGetsOrganizationPortal(t *testing.T) {
	h := newMePortalHarness(t)
	now := time.Now().UTC()
	res, _ := h.sqlDB.Exec(`INSERT INTO tenants (name, slug, domain, plan, active, created_at, updated_at) VALUES ('owner-co', 'owner-co', 'owner-co.example', 'free', 1, ?, ?)`, now, now)
	tid, _ := res.LastInsertId()

	pw, _ := auth.HashPassword("OwnerPass!2026")
	h.sqlDB.Exec(`INSERT INTO users (created_at, updated_at, email, password_hash, role, tenant_id, active, email_verified) VALUES (?, ?, 'owner@test.local', ?, 'tenant_admin', ?, 1, 1)`, now, now, pw, tid)

	tok := h.login(t, "owner@test.local", "OwnerPass!2026")
	status, out := h.me(t, tok)
	if status != http.StatusOK {
		t.Fatalf("me status=%d body=%v", status, out)
	}
	if out["portal"] != "organization" {
		t.Fatalf("self-signup owner (tenant_admin) portal=%v, want organization; body=%v", out["portal"], out)
	}
	org, ok := out["organization"].(map[string]interface{})
	if !ok || org["id"] == nil {
		t.Fatalf("expected organization block in /me response, got %v", out)
	}
}

// TestMePortal_RoleUserFailsClosed is the canonical RoleUser regression:
// a RoleUser row (per-mailbox webmail end-user) with a valid tenant_id
// must NOT be classified into the full Organization Admin shell — the
// server fails closed with portal="" and the client shows the
// access-unavailable state instead of granting the admin console.
func TestMePortal_RoleUserFailsClosed(t *testing.T) {
	h := newMePortalHarness(t)
	now := time.Now().UTC()
	res, _ := h.sqlDB.Exec(`INSERT INTO tenants (name, slug, domain, plan, active, created_at, updated_at) VALUES ('webmail-co', 'webmail-co', 'webmail-co.example', 'free', 1, ?, ?)`, now, now)
	tid, _ := res.LastInsertId()

	pw, _ := auth.HashPassword("WebmailPass!2026")
	h.sqlDB.Exec(`INSERT INTO users (created_at, updated_at, email, password_hash, role, tenant_id, active, email_verified) VALUES (?, ?, 'webmail@test.local', ?, 'user', ?, 1, 1)`, now, now, pw, tid)

	tok := h.login(t, "webmail@test.local", "WebmailPass!2026")
	status, out := h.me(t, tok)
	if status != http.StatusOK {
		t.Fatalf("me status=%d body=%v", status, out)
	}
	if out["portal"] != "" {
		t.Fatalf("RoleUser portal=%v, want empty string (fail closed — webmail end-user, not an Organization administrator); body=%v", out["portal"], out)
	}
	if _, hasOrg := out["organization"]; hasOrg {
		t.Fatalf("RoleUser /me must not carry an organization block; body=%v", out)
	}
}

// TestMePortal_PlatformSuperAdminStillGetsPlatformPortal guards against
// regressing the platform branch while fixing Bug 1.
func TestMePortal_PlatformSuperAdminStillGetsPlatformPortal(t *testing.T) {
	h := newMePortalHarness(t)
	now := time.Now().UTC()
	pw, _ := auth.HashPassword("PlatPass!2026")
	h.sqlDB.Exec(`INSERT INTO users (created_at, updated_at, email, password_hash, role, tenant_id, active, email_verified) VALUES (?, ?, 'plat@test.local', ?, 'platform_super_admin', NULL, 1, 1)`, now, now, pw)

	tok := h.login(t, "plat@test.local", "PlatPass!2026")
	status, out := h.me(t, tok)
	if status != http.StatusOK {
		t.Fatalf("me status=%d body=%v", status, out)
	}
	if out["portal"] != "platform" {
		t.Fatalf("platform_super_admin portal=%v, want platform; body=%v", out["portal"], out)
	}
}

// TestMePortal_UnrecognizedRoleFailsClosed is the required fail-closed
// regression: an identity with no recognized role/tenant combination must
// still resolve to portal="" rather than being guessed client-side.
//
// Note: the normal /admin/login path already rejects unrecognized roles
// before token issuance (handlers.go Login -> ValidateUserForTokenIssuance),
// so this scenario cannot be reached by logging in normally — that's a
// separate, correct fail-closed gate. To exercise the Me() classifier's own
// fail-closed default directly (defense in depth, and to guard against a
// future loosening of ValidateUserForTokenIssuance), this test mints an
// access token directly via the authenticator, bypassing login.
func TestMePortal_UnrecognizedRoleFailsClosed(t *testing.T) {
	h := newMePortalHarness(t)
	now := time.Now().UTC()
	pw, _ := auth.HashPassword("WeirdPass!2026")
	// Legacy/unnormalized role with no tenant — must not resolve to any portal.
	res, err := h.sqlDB.Exec(`INSERT INTO users (created_at, updated_at, email, password_hash, role, tenant_id, active, email_verified) VALUES (?, ?, 'weird@test.local', ?, 'legacy_unknown', NULL, 1, 1)`, now, now, pw)
	if err != nil {
		t.Fatalf("insert unrecognized-role user: %v", err)
	}
	uid, _ := res.LastInsertId()

	tok, err := h.authn.GenerateAccessToken(uint(uid), auth.Role("legacy_unknown"))
	if err != nil {
		t.Fatalf("mint token: %v", err)
	}
	status, out := h.me(t, tok)
	if status != http.StatusOK {
		t.Fatalf("me status=%d body=%v", status, out)
	}
	if out["portal"] != "" {
		t.Fatalf("unrecognized role portal=%v, want empty string (fail closed); body=%v", out["portal"], out)
	}
}

// TestCountAdmins_CountsSignupOwnerAsTenantAdmin is the canonical
// regression for the Platform Organizations drawer's "Admin users" field:
// a signup-created owner persisted as tenant_admin must be counted, while
// a plain RoleUser (webmail end-user) must NOT be counted as an admin.
func TestCountAdmins_CountsSignupOwnerAsTenantAdmin(t *testing.T) {
	h := newMePortalHarness(t)
	now := time.Now().UTC()
	res, _ := h.sqlDB.Exec(`INSERT INTO tenants (name, slug, domain, plan, active, created_at, updated_at) VALUES ('cnt-co', 'cnt-co', 'cnt-co.example', 'free', 1, ?, ?)`, now, now)
	tid, _ := res.LastInsertId()
	pw, _ := auth.HashPassword("CountPass!2026")
	// Signup-created owner — canonical tenant_admin.
	h.sqlDB.Exec(`INSERT INTO users (created_at, updated_at, email, password_hash, role, tenant_id, active, email_verified) VALUES (?, ?, 'cnt-owner@test.local', ?, 'tenant_admin', ?, 1, 1)`, now, now, pw, tid)
	// Webmail end-user in the same tenant — must never be counted.
	h.sqlDB.Exec(`INSERT INTO users (created_at, updated_at, email, password_hash, role, tenant_id, active, email_verified) VALUES (?, ?, 'cnt-webmail@test.local', ?, 'user', ?, 1, 1)`, now, now, pw, tid)

	repo := organization.NewOrganizationRepo(h.sqlDB)
	count, err := repo.CountAdmins(context.Background(), uint(tid))
	if err != nil {
		t.Fatalf("CountAdmins: %v", err)
	}
	if count != 1 {
		t.Fatalf("CountAdmins = %d, want 1 (only the tenant_admin owner; webmail RoleUser must not count)", count)
	}
}

// TestCountAdmins_RoleUserOnlyTenantCountsZero proves the failure mode the
// legacy role='user' owner rows produced: a tenant whose ONLY user rows are
// RoleUser has ZERO countable admins (this is exactly what the operator's
// `orvix admin repair-signup-owner` repair fixes for legacy signup rows).
func TestCountAdmins_RoleUserOnlyTenantCountsZero(t *testing.T) {
	h := newMePortalHarness(t)
	now := time.Now().UTC()
	res, _ := h.sqlDB.Exec(`INSERT INTO tenants (name, slug, domain, plan, active, created_at, updated_at) VALUES ('legacy-co', 'legacy-co', 'legacy-co.example', 'free', 1, ?, ?)`, now, now)
	tid, _ := res.LastInsertId()
	pw, _ := auth.HashPassword("LegacyPass!2026")
	h.sqlDB.Exec(`INSERT INTO users (created_at, updated_at, email, password_hash, role, tenant_id, active, email_verified) VALUES (?, ?, 'legacy-owner@test.local', ?, 'user', ?, 1, 1)`, now, now, pw, tid)

	repo := organization.NewOrganizationRepo(h.sqlDB)
	count, err := repo.CountAdmins(context.Background(), uint(tid))
	if err != nil {
		t.Fatalf("CountAdmins: %v", err)
	}
	if count != 0 {
		t.Fatalf("CountAdmins for a RoleUser-only tenant = %d, want 0 (webmail end-users are not administrators)", count)
	}
}

// TestCountAdmins_LegacyAdminRowsStillCounted guards the migration window:
// pre-normalization 'admin'/'superadmin' rows remain countable until the
// startup normalizer rewrites them to canonical roles.
func TestCountAdmins_LegacyAdminRowsStillCounted(t *testing.T) {
	h := newMePortalHarness(t)
	now := time.Now().UTC()
	res, _ := h.sqlDB.Exec(`INSERT INTO tenants (name, slug, domain, plan, active, created_at, updated_at) VALUES ('legacy-admin-co', 'legacy-admin-co', 'legacy-admin-co.example', 'free', 1, ?, ?)`, now, now)
	tid, _ := res.LastInsertId()
	pw, _ := auth.HashPassword("LegacyAdminPass!2026")
	h.sqlDB.Exec(`INSERT INTO users (created_at, updated_at, email, password_hash, role, tenant_id, active, email_verified) VALUES (?, ?, 'legacy-admin@test.local', ?, 'admin', ?, 1, 1)`, now, now, pw, tid)

	repo := organization.NewOrganizationRepo(h.sqlDB)
	count, err := repo.CountAdmins(context.Background(), uint(tid))
	if err != nil {
		t.Fatalf("CountAdmins: %v", err)
	}
	if count != 1 {
		t.Fatalf("CountAdmins for legacy 'admin' row = %d, want 1 (migration-window rows remain countable)", count)
	}
}
