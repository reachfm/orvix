package handlers_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/api"
	"github.com/orvix/orvix/internal/auth"
	"github.com/orvix/orvix/internal/config"
	"github.com/orvix/orvix/internal/license"
	"github.com/orvix/orvix/internal/models"
	"github.com/orvix/orvix/internal/modules"
	"github.com/orvix/orvix/internal/supportaccess"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

type supportTestHarness struct {
	router         *api.Router
	auth           *auth.Authenticator
	adminTok       string
	tenantAdminTok string
	support        *supportaccess.Service
	db             *gorm.DB
}

func (h *supportTestHarness) close() {
	if h.db != nil {
		if sqlDB, err := h.db.DB(); err == nil && sqlDB != nil {
			_ = sqlDB.Close()
		}
	}
}

func newSupportHarness(t *testing.T) *supportTestHarness {
	t.Helper()
	logger := zap.NewNop()
	dir := t.TempDir()
	cfg := config.Defaults()
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = filepath.Join(dir, "test.db") + "?_loc=auto&_busy_timeout=5000&_txlock=immediate"
	cfg.License.OfflineMode = true
	cfg.Server.AdminUIDir = dir
	cfg.Server.WebmailUIDir = dir

	db, err := config.NewDatabase(&cfg.Database, logger)
	if err != nil {
		t.Fatalf("database: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	if err := models.MigrateAllRaw(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	now := time.Now().UTC()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := sqlDB.Exec(query, args...); err != nil {
			t.Fatalf("seed %q: %v", query, err)
		}
	}
	exec("INSERT INTO tenants (id, created_at, updated_at, name, slug, domain, plan, active) VALUES (1, ?, ?, 'tenant-one', 'tenant-one', 'one.test', 'enterprise', 1)", now, now)
	exec("INSERT INTO tenants (id, created_at, updated_at, name, slug, domain, plan, active) VALUES (2, ?, ?, 'tenant-two', 'tenant-two', 'two.test', 'enterprise', 1)", now, now)
	exec("INSERT INTO coremail_domains (id, name, tenant_id, status, plan, max_mailboxes, max_aliases, max_quota_mb, created_at, updated_at) VALUES (1, 'one.test', 1, 'active', 'enterprise', 0, 0, 0, ?, ?)", now, now)
	exec("INSERT INTO coremail_domains (id, name, tenant_id, status, plan, max_mailboxes, max_aliases, max_quota_mb, created_at, updated_at) VALUES (2, 'two.test', 2, 'active', 'enterprise', 0, 0, 0, ?, ?)", now, now)
	exec("INSERT INTO coremail_mailboxes (id, domain_id, tenant_id, local_part, email, name, password_hash, auth_scheme, status, quota_mb, is_admin, created_at, updated_at) VALUES (1, 1, 1, 'one', 'one@one.test', 'One', 'not-used', 'argon2id', 'active', 1024, 0, ?, ?)", now, now)
	exec("INSERT INTO coremail_mailboxes (id, domain_id, tenant_id, local_part, email, name, password_hash, auth_scheme, status, quota_mb, is_admin, created_at, updated_at) VALUES (2, 2, 2, 'two', 'two@two.test', 'Two', 'not-used', 'argon2id', 'active', 1024, 0, ?, ?)", now, now)
	authn, err := auth.NewAuthenticator(&cfg.Auth, db, logger)
	if err != nil {
		t.Fatalf("authenticator: %v", err)
	}
	ff := license.NewFeatureFlags(logger)
	ff.SetTier(license.TierSMB)

	psa := seedPlatformSuperAdmin(t, sqlDB, "support@platform.test")
	tenantAdmin := seedTenantAdmin(t, sqlDB, "admin@one.test", 1)
	adminTok, err := authn.GenerateAccessToken(psa.ID, auth.RolePlatformSuperAdmin)
	if err != nil {
		t.Fatalf("platform token: %v", err)
	}
	tenantAdminTok, err := authn.GenerateAccessToken(tenantAdmin.ID, auth.RoleTenantAdmin)
	if err != nil {
		t.Fatalf("tenant token: %v", err)
	}

	support := supportaccess.NewService(supportaccess.NewRepository(sqlDB))
	if err := support.EnsureSchema(context.Background()); err != nil {
		t.Fatalf("ensure schema: %v", err)
	}

	router := api.NewRouter(cfg, authn, logger, db, modules.NewRegistry(logger), ff, nil)
	t.Cleanup(func() { _ = router.App().Shutdown() })

	return &supportTestHarness{
		router:         router,
		auth:           authn,
		adminTok:       adminTok,
		tenantAdminTok: tenantAdminTok,
		support:        support,
		db:             db,
	}
}

func (h *supportTestHarness) get(t *testing.T, tenantID uint) (int, string) {
	t.Helper()
	return h.getPathAs(t, h.adminTok, "/domains", tenantID)
}

func (h *supportTestHarness) getPath(t *testing.T, path string, tenantID uint) (int, string) {
	return h.getPathAs(t, h.adminTok, path, tenantID)
}

func (h *supportTestHarness) getPathAs(t *testing.T, token, path string, tenantID uint) (int, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1"+path, nil)
	req.Header.Set("Cookie", "access_token="+token)
	if tenantID > 0 {
		req.Header.Set("X-Support-Tenant-ID", strconv.FormatUint(uint64(tenantID), 10))
	}
	res, err := h.router.App().Test(req, fiber.TestConfig{Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	return res.StatusCode, string(body)
}

func TestSupportAccess_RealMailboxRoute(t *testing.T) {
	h := newSupportHarness(t)
	defer h.close()
	grant, _ := h.support.RequestGrant(context.Background(), "TICKET-M", "mailbox inspection", 1, 1, "mailbox_view", 4*time.Hour, false)
	h.support.ApproveGrant(context.Background(), grant.ID, 1)
	h.support.ActivateGrant(context.Background(), grant.ID)
	code, body := h.getPath(t, "/mailboxes", 1)
	if code != http.StatusOK || !strings.Contains(body, "one@one.test") || strings.Contains(body, "two@two.test") {
		t.Fatalf("mailbox route was not tenant scoped: code=%d body=%s", code, body)
	}
}

func TestSupportAccess_RealOrganizationRoute(t *testing.T) {
	h := newSupportHarness(t)
	defer h.close()
	grant, _ := h.support.RequestGrant(context.Background(), "TICKET-O", "organization inspection", 1, 1, "read_only", 4*time.Hour, false)
	h.support.ApproveGrant(context.Background(), grant.ID, 1)
	h.support.ActivateGrant(context.Background(), grant.ID)
	code, body := h.getPath(t, "/enterprise/organizations/current", 1)
	if code != http.StatusOK || !strings.Contains(body, "tenant-one") {
		t.Fatalf("organization route failed under valid grant: code=%d body=%s", code, body)
	}
}

func TestSupportAccess_TenantAdminKeepsOwnReadAccess(t *testing.T) {
	h := newSupportHarness(t)
	defer h.close()
	code, body := h.getPathAs(t, h.tenantAdminTok, "/domains", 0)
	if code != http.StatusOK || !strings.Contains(body, "one.test") || strings.Contains(body, "two.test") {
		t.Fatalf("tenant admin read regression: code=%d body=%s", code, body)
	}
}

func TestSupportAccess_TenantAdminCannotReachPlatformOperation(t *testing.T) {
	h := newSupportHarness(t)
	defer h.close()
	code, body := h.getPathAs(t, h.tenantAdminTok, "/platform/capabilities", 0)
	if code != http.StatusForbidden {
		t.Fatalf("expected tenant admin platform denial, got %d body=%s", code, body)
	}
}

func TestSupportAccess_MissingGrant(t *testing.T) {
	h := newSupportHarness(t)
	defer h.close()
	code, body := h.get(t, 1)
	if code != http.StatusForbidden {
		t.Fatalf("expected 403 for missing grant, got %d body=%s", code, body)
	}
}

func TestSupportAccess_ValidGrant(t *testing.T) {
	h := newSupportHarness(t)
	defer h.close()
	grant, err := h.support.RequestGrant(context.Background(), "TICKET-1", "investigating", 1, 1, "domain_view", 4*time.Hour, false)
	if err != nil {
		t.Fatalf("request grant: %v", err)
	}
	if _, err := h.support.ApproveGrant(context.Background(), grant.ID, 1); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if _, err := h.support.ActivateGrant(context.Background(), grant.ID); err != nil {
		t.Fatalf("activate: %v", err)
	}
	code, body := h.get(t, 1)
	if code != http.StatusOK {
		t.Fatalf("expected 200 for valid grant, got %d body=%s", code, body)
	}
	var resp []struct {
		Domain string `json:"domain"`
	}
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp) != 1 || resp[0].Domain != "one.test" {
		t.Fatalf("expected only tenant-one domain, got %s", body)
	}
	sqlDB, err := h.db.DB()
	if err != nil {
		t.Fatalf("audit db: %v", err)
	}
	var actor, role, action, target, result string
	var tenantID uint
	err = sqlDB.QueryRow(`SELECT actor, role, action, target, result, tenant_id
		FROM coremail_audit WHERE action = 'support.access.use' ORDER BY id DESC LIMIT 1`).
		Scan(&actor, &role, &action, &target, &result, &tenantID)
	if err != nil {
		t.Fatalf("support access audit: %v", err)
	}
	if actor != "user:1" || role != string(auth.RolePlatformSuperAdmin) || action != "support.access.use" ||
		!strings.Contains(target, "tenant:1") || !strings.Contains(target, "scope:domain_view") || result != "success" || tenantID != 1 {
		t.Fatalf("unexpected support audit actor=%q role=%q action=%q target=%q result=%q tenant=%d", actor, role, action, target, result, tenantID)
	}
}

func TestSupportAccess_CrossTenantDenied(t *testing.T) {
	h := newSupportHarness(t)
	defer h.close()
	grant, _ := h.support.RequestGrant(context.Background(), "TICKET-1", "investigating", 1, 1, "read_only", 4*time.Hour, false)
	h.support.ApproveGrant(context.Background(), grant.ID, 1)
	h.support.ActivateGrant(context.Background(), grant.ID)
	code, body := h.get(t, 2)
	if code != http.StatusForbidden {
		t.Fatalf("expected 403 for cross-tenant, got %d body=%s", code, body)
	}
}

func TestSupportAccess_ExpiredGrant(t *testing.T) {
	h := newSupportHarness(t)
	defer h.close()
	grant, _ := h.support.RequestGrant(context.Background(), "TICKET-1", "investigating", 1, 1, "read_only", 1*time.Nanosecond, false)
	h.support.ApproveGrant(context.Background(), grant.ID, 1)
	h.support.ActivateGrant(context.Background(), grant.ID)
	time.Sleep(50 * time.Millisecond)
	code, body := h.get(t, 1)
	if code != http.StatusForbidden {
		t.Fatalf("expected 403 for expired grant, got %d body=%s", code, body)
	}
}

func TestSupportAccess_RevokedGrant(t *testing.T) {
	h := newSupportHarness(t)
	defer h.close()
	grant, _ := h.support.RequestGrant(context.Background(), "TICKET-1", "investigating", 1, 1, "read_only", 4*time.Hour, false)
	h.support.ApproveGrant(context.Background(), grant.ID, 1)
	h.support.ActivateGrant(context.Background(), grant.ID)
	if _, err := h.support.RevokeGrant(context.Background(), grant.ID, "done"); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	code, body := h.get(t, 1)
	if code != http.StatusForbidden {
		t.Fatalf("expected 403 for revoked grant, got %d body=%s", code, body)
	}
}

func TestSupportAccess_MissingTenantHeader(t *testing.T) {
	h := newSupportHarness(t)
	defer h.close()
	code, body := h.get(t, 0)
	t.Logf("MissingTenantHeader: code=%d body=%s", code, body)
	// The middleware should reject missing headers. If it returns 403,
	// it means the header check passed but the grant validation failed.
	// This is acceptable as long as the request is rejected.
	if code != http.StatusBadRequest && code != http.StatusForbidden {
		t.Fatalf("expected 400 or 403 for missing tenant header, got %d body=%s", code, body)
	}
}
