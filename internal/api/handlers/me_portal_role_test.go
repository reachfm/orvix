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

// TestMePortal_SelfSignupOwnerGetsOrganizationPortal is the regression test
// for Bug 1: a self-signup tenant owner (role="user", valid tenant_id) must
// land in the organization portal, not fall through to portal="".
func TestMePortal_SelfSignupOwnerGetsOrganizationPortal(t *testing.T) {
	h := newMePortalHarness(t)
	now := time.Now().UTC()
	res, _ := h.sqlDB.Exec(`INSERT INTO tenants (name, slug, domain, plan, active, created_at, updated_at) VALUES ('owner-co', 'owner-co', 'owner-co.example', 'free', 1, ?, ?)`, now, now)
	tid, _ := res.LastInsertId()

	pw, _ := auth.HashPassword("OwnerPass!2026")
	h.sqlDB.Exec(`INSERT INTO users (created_at, updated_at, email, password_hash, role, tenant_id, active, email_verified) VALUES (?, ?, 'owner@test.local', ?, 'user', ?, 1, 1)`, now, now, pw, tid)

	tok := h.login(t, "owner@test.local", "OwnerPass!2026")
	status, out := h.me(t, tok)
	if status != http.StatusOK {
		t.Fatalf("me status=%d body=%v", status, out)
	}
	if out["portal"] != "organization" {
		t.Fatalf("self-signup owner (role=user) portal=%v, want organization; body=%v", out["portal"], out)
	}
	org, ok := out["organization"].(map[string]interface{})
	if !ok || org["id"] == nil {
		t.Fatalf("expected organization block in /me response, got %v", out)
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

// TestCountAdmins_CountsSelfSignupOwner is the regression test for Bug 2:
// a role="user" tenant owner must be counted as an admin by the Platform
// dashboard's organization admin count.
func TestCountAdmins_CountsSelfSignupOwner(t *testing.T) {
	h := newMePortalHarness(t)
	now := time.Now().UTC()
	res, _ := h.sqlDB.Exec(`INSERT INTO tenants (name, slug, domain, plan, active, created_at, updated_at) VALUES ('cnt-co', 'cnt-co', 'cnt-co.example', 'free', 1, ?, ?)`, now, now)
	tid, _ := res.LastInsertId()
	pw, _ := auth.HashPassword("CountPass!2026")
	h.sqlDB.Exec(`INSERT INTO users (created_at, updated_at, email, password_hash, role, tenant_id, active, email_verified) VALUES (?, ?, 'cnt-owner@test.local', ?, 'user', ?, 1, 1)`, now, now, pw, tid)

	repo := organization.NewOrganizationRepo(h.sqlDB)
	count, err := repo.CountAdmins(context.Background(), uint(tid))
	if err != nil {
		t.Fatalf("CountAdmins: %v", err)
	}
	if count != 1 {
		t.Fatalf("CountAdmins for role=user tenant owner = %d, want 1", count)
	}
}
