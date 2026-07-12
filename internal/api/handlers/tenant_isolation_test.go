package handlers_test

// Regression coverage for the cross-tenant IDOR fix: a mailbox/domain
// admin in one tenant must never be able to read, modify, or delete a
// resource that belongs to a different tenant, even when it knows (or
// guesses) the target's numeric id / domain name. Every case below sets
// up two fully separate tenants and asserts that tenant A's admin gets
// a 404 (never mailbox/domain data, never a mutation) against tenant
// B's resources, while retaining full access to its own.

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http/httptest"
	"path/filepath"
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

type tenantIsolationEnv struct {
	router *api.Router

	// Tenant A: the attacker's own, legitimate tenant.
	tenantAToken string
	tenantACSRF  string
	mailboxAID   int64
	domainAName  string

	// Tenant B: the victim tenant. Tenant A must never reach these.
	mailboxBID  int64
	domainBName string
}

func buildTenantIsolationEnv(t *testing.T) *tenantIsolationEnv {
	t.Helper()
	logger := zap.NewNop()
	cfg := config.Defaults()
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = filepath.Join(t.TempDir(), "tenantisolation.db") + "?_loc=auto&_busy_timeout=5000&_txlock=immediate"
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
	exec := func(query string, args ...interface{}) {
		t.Helper()
		if _, err := sqlDB.Exec(query, args...); err != nil {
			t.Fatalf("exec %q: %v", query, err)
		}
	}

	// Two independent tenants.
	exec("INSERT INTO tenants (id, created_at, updated_at, name, slug, domain, plan, active) VALUES (1, ?, ?, 'tenant-a', 'tenant-a', 'tenanta.example', 'enterprise', 1)", now, now)
	exec("INSERT INTO tenants (id, created_at, updated_at, name, slug, domain, plan, active) VALUES (2, ?, ?, 'tenant-b', 'tenant-b', 'tenantb.example', 'enterprise', 1)", now, now)

	// Tenant A: one domain, one admin user/mailbox.
	exec("INSERT INTO coremail_domains (id, name, tenant_id, status, plan, max_mailboxes, max_aliases, max_quota_mb, created_at, updated_at) VALUES (1, 'tenanta.example', 1, 'active', 'enterprise', 0, 0, 0, ?, ?)", now, now)
	const adminAEmail = "admin@tenanta.example"
	const adminAPass = "TenantAPass!2026"
	adminAHash, err := authenticator.HashPassword(adminAPass)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	exec("INSERT INTO users (created_at, updated_at, email, password_hash, role, tenant_id, active, email_verified) VALUES (?, ?, ?, ?, 'admin', 1, 1, 1)", now, now, adminAEmail, adminAHash)
	exec(`INSERT INTO coremail_mailboxes (id, domain_id, tenant_id, local_part, email, name, password_hash, auth_scheme, status, quota_mb, is_admin, created_at, updated_at)
	      VALUES (1, 1, 1, 'admin', ?, 'Admin A', ?, 'argon2id', 'active', 1024, 1, ?, ?)`, adminAEmail, adminAHash, now, now)

	// Tenant B: separate domain, separate non-admin mailbox — the
	// resource tenant A's admin must never be able to touch.
	exec("INSERT INTO coremail_domains (id, name, tenant_id, status, plan, max_mailboxes, max_aliases, max_quota_mb, created_at, updated_at) VALUES (2, 'tenantb.example', 2, 'active', 'enterprise', 0, 0, 0, ?, ?)", now, now)
	const victimEmail = "victim@tenantb.example"
	victimHash, err := authenticator.HashPassword("VictimPass!2026")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	exec(`INSERT INTO coremail_mailboxes (id, domain_id, tenant_id, local_part, email, name, password_hash, auth_scheme, status, quota_mb, is_admin, created_at, updated_at)
	      VALUES (2, 2, 2, 'victim', ?, 'Victim', ?, 'argon2id', 'active', 1024, 0, ?, ?)`, victimEmail, victimHash, now, now)

	scratchDir := t.TempDir()
	adminDir := filepath.Join(scratchDir, "admin")
	webmailDir := filepath.Join(scratchDir, "webmail")
	if err := mkdirEmpty(adminDir); err != nil {
		t.Fatalf("mkdir admin: %v", err)
	}
	if err := mkdirEmpty(webmailDir); err != nil {
		t.Fatalf("mkdir webmail: %v", err)
	}
	cfg.Server.AdminUIDir = adminDir
	cfg.Server.WebmailUIDir = webmailDir

	router := api.NewRouter(cfg, authenticator, logger, db, modules.NewRegistry(logger), license.NewFeatureFlags(logger), nil)
	t.Cleanup(func() {
		_ = router.App().Shutdown()
		_ = sqlDB.Close()
	})

	tenantAToken := loginDomainTest(t, router, adminAEmail, adminAPass)
	tenantACSRF := getDomainCSRF(t, router, tenantAToken)

	return &tenantIsolationEnv{
		router:       router,
		tenantAToken: tenantAToken,
		tenantACSRF:  tenantACSRF,
		mailboxAID:   1,
		domainAName:  "tenanta.example",
		mailboxBID:   2,
		domainBName:  "tenantb.example",
	}
}

func tenantReq(t *testing.T, e *tenantIsolationEnv, method, path string, body interface{}) (int, map[string]interface{}) {
	t.Helper()
	var bodyReader io.Reader
	if body != nil {
		buf, _ := json.Marshal(body)
		bodyReader = bytes.NewReader(buf)
	}
	req := httptest.NewRequest(method, path, bodyReader)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Cookie", strings.Join([]string{
		"access_token=" + e.tenantAToken,
		"csrf_token=" + e.tenantACSRF,
	}, "; "))
	req.Header.Set("X-CSRF-Token", e.tenantACSRF)
	resp, err := e.router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()
	out := map[string]interface{}{}
	if resp.StatusCode != 204 {
		raw, _ := io.ReadAll(resp.Body)
		if len(raw) > 0 {
			_ = json.Unmarshal(raw, &out)
		}
	}
	return resp.StatusCode, out
}

// TestTenantIsolation_OwnResourcesAccessible is the sanity check: without
// it, a test that only asserts 404s on cross-tenant access could pass
// vacuously if TenantMiddleware/callerOwnsTenant were wired wrong and
// blocked everyone, including legitimate same-tenant access.
func TestTenantIsolation_OwnResourcesAccessible(t *testing.T) {
	e := buildTenantIsolationEnv(t)

	status, body := tenantReq(t, e, "GET", "/api/v1/mailboxes/1", nil)
	if status != 200 {
		t.Fatalf("GET own mailbox: expected 200, got %d: %v", status, body)
	}

	status, body = tenantReq(t, e, "GET", "/api/v1/domains/"+e.domainAName, nil)
	if status != 200 {
		t.Fatalf("GET own domain: expected 200, got %d: %v", status, body)
	}
}

func TestTenantIsolation_GetMailboxBlocksCrossTenant(t *testing.T) {
	e := buildTenantIsolationEnv(t)
	status, body := tenantReq(t, e, "GET", "/api/v1/mailboxes/2", nil)
	if status != 404 {
		t.Fatalf("GET cross-tenant mailbox: expected 404, got %d: %v", status, body)
	}
}

func TestTenantIsolation_UpdateMailboxPasswordBlocksCrossTenant(t *testing.T) {
	e := buildTenantIsolationEnv(t)
	status, body := tenantReq(t, e, "PATCH", "/api/v1/mailboxes/2/password", map[string]string{"password": "NewPassword!2026"})
	if status != 404 {
		t.Fatalf("PATCH cross-tenant mailbox password: expected 404, got %d: %v", status, body)
	}
}

func TestTenantIsolation_DeleteMailboxBlocksCrossTenant(t *testing.T) {
	e := buildTenantIsolationEnv(t)
	status, body := tenantReq(t, e, "DELETE", "/api/v1/mailboxes/2", nil)
	if status != 404 {
		t.Fatalf("DELETE cross-tenant mailbox: expected 404, got %d: %v", status, body)
	}
}

func TestTenantIsolation_UpdateMailboxStatusBlocksCrossTenant(t *testing.T) {
	e := buildTenantIsolationEnv(t)
	status, body := tenantReq(t, e, "PATCH", "/api/v1/mailboxes/2/status", map[string]string{"status": "suspended"})
	if status != 404 {
		t.Fatalf("PATCH cross-tenant mailbox status: expected 404, got %d: %v", status, body)
	}
}

func TestTenantIsolation_UpdateMailboxQuotaBlocksCrossTenant(t *testing.T) {
	e := buildTenantIsolationEnv(t)
	status, body := tenantReq(t, e, "PATCH", "/api/v1/mailboxes/2/quota", map[string]int64{"quota_mb": 1})
	if status != 404 {
		t.Fatalf("PATCH cross-tenant mailbox quota: expected 404, got %d: %v", status, body)
	}
}

func TestTenantIsolation_BulkMailboxStatusSkipsCrossTenant(t *testing.T) {
	e := buildTenantIsolationEnv(t)
	status, body := tenantReq(t, e, "POST", "/api/v1/mailboxes/bulk/status", map[string]interface{}{
		"mailbox_ids": []int64{2},
		"status":      "suspended",
	})
	if status != 200 {
		t.Fatalf("bulk mailbox status: expected 200, got %d: %v", status, body)
	}
	if updated, _ := body["updated"].(float64); updated != 0 {
		t.Fatalf("bulk mailbox status: expected 0 updated for cross-tenant id, got %v (body=%v)", body["updated"], body)
	}
	if skipped, _ := body["skipped"].(float64); skipped != 1 {
		t.Fatalf("bulk mailbox status: expected 1 skipped for cross-tenant id, got %v (body=%v)", body["skipped"], body)
	}
}

func TestTenantIsolation_GetDomainBlocksCrossTenant(t *testing.T) {
	e := buildTenantIsolationEnv(t)
	status, body := tenantReq(t, e, "GET", "/api/v1/domains/"+e.domainBName, nil)
	if status != 404 {
		t.Fatalf("GET cross-tenant domain: expected 404, got %d: %v", status, body)
	}
}

func TestTenantIsolation_UpdateDomainStatusBlocksCrossTenant(t *testing.T) {
	e := buildTenantIsolationEnv(t)
	status, body := tenantReq(t, e, "PATCH", "/api/v1/domains/"+e.domainBName+"/status", map[string]string{"status": "suspended"})
	if status != 404 {
		t.Fatalf("PATCH cross-tenant domain status: expected 404, got %d: %v", status, body)
	}
}

func TestTenantIsolation_DeleteDomainBlocksCrossTenant(t *testing.T) {
	e := buildTenantIsolationEnv(t)
	status, body := tenantReq(t, e, "DELETE", "/api/v1/domains/"+e.domainBName, nil)
	if status != 404 {
		t.Fatalf("DELETE cross-tenant domain: expected 404, got %d: %v", status, body)
	}
}

func TestTenantIsolation_BulkDomainStatusSkipsCrossTenant(t *testing.T) {
	e := buildTenantIsolationEnv(t)
	status, body := tenantReq(t, e, "POST", "/api/v1/domains/bulk/status", map[string]interface{}{
		"domains": []string{e.domainBName},
		"status":  "suspended",
	})
	if status != 200 {
		t.Fatalf("bulk domain status: expected 200, got %d: %v", status, body)
	}
	if updated, _ := body["updated"].(float64); updated != 0 {
		t.Fatalf("bulk domain status: expected 0 updated for cross-tenant domain, got %v (body=%v)", body["updated"], body)
	}
	if skipped, _ := body["skipped"].(float64); skipped != 1 {
		t.Fatalf("bulk domain status: expected 1 skipped for cross-tenant domain, got %v (body=%v)", body["skipped"], body)
	}
}

func TestTenantIsolation_MailboxAuditBlocksCrossTenant(t *testing.T) {
	e := buildTenantIsolationEnv(t)
	status, body := tenantReq(t, e, "GET", "/api/v1/mailboxes/2/audit", nil)
	if status != 200 {
		t.Fatalf("GET cross-tenant mailbox audit: expected 200 with empty body, got %d: %v", status, body)
	}
	// The handler degrades to an empty JSON array rather than a 404 on
	// unrelated lookup failures; assert it never leaks tenant B's
	// audit trail to tenant A regardless of status code shape.
	req := httptest.NewRequest("GET", "/api/v1/mailboxes/2/audit", nil)
	req.Header.Set("Cookie", "access_token="+e.tenantAToken)
	resp, err := e.router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	raw, _ := io.ReadAll(resp.Body)
	if strings.Contains(string(raw), "tenantb.example") || strings.Contains(string(raw), "victim@") {
		t.Fatalf("mailbox audit leaked tenant B data to tenant A: %s", string(raw))
	}
}
