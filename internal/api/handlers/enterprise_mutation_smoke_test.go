package handlers_test

// Full-router regression coverage for the enterprise-blocker fixes:
//
//  1. requireTenantActive (internal/api/router.go) now queries the
//     canonical "tenants" table instead of a nonexistent "organizations"
//     table. Before this fix, every /api/v1/enterprise/* mutation
//     either 403'd unconditionally or panicked with a nil-pointer
//     dereference (confirmed empirically during the customer_mail.go
//     IDOR fix). These tests prove: an active tenant's mutations
//     succeed, an inactive tenant's mutations are rejected (not
//     panicked), and no organizations-table dependency remains.
//  2. coremail_groups / coremail_group_members now have real
//     migrations (internal/models/models.go for SQLite,
//     internal/models/postgres_migrations.go for PostgreSQL). These
//     tests prove the customer Groups feature works end-to-end
//     through the real HTTP router, not just at the handler level.
//  3. GET /admin/mailboxes now calls ListMailboxes instead of
//     ListUsers. This test proves the response is mailbox-shaped.

import (
	"bytes"
	"database/sql"
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

// enterpriseSmokeEnv mirrors tenantIsolationEnv (tenant_isolation_test.go)
// but also exposes the raw *sql.DB so these tests can directly flip a
// tenant's active flag to exercise requireTenantActive.
type enterpriseSmokeEnv struct {
	router *api.Router
	sqlDB  *sql.DB

	tenantAToken string
	tenantACSRF  string
}

func buildEnterpriseSmokeEnv(t *testing.T) *enterpriseSmokeEnv {
	t.Helper()
	logger := zap.NewNop()
	cfg := config.Defaults()
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = filepath.Join(t.TempDir(), "enterprisesmoke.db") + "?_loc=auto&_busy_timeout=5000&_txlock=immediate"
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

	exec("INSERT INTO tenants (id, created_at, updated_at, name, slug, domain, plan, active) VALUES (1, ?, ?, 'tenant-a', 'tenant-a', 'tenanta.example', 'enterprise', 1)", now, now)
	exec("INSERT INTO tenants (id, created_at, updated_at, name, slug, domain, plan, active) VALUES (2, ?, ?, 'tenant-b', 'tenant-b', 'tenantb.example', 'enterprise', 1)", now, now)
	exec("INSERT INTO coremail_domains (id, name, tenant_id, status, plan, max_mailboxes, max_aliases, max_quota_mb, created_at, updated_at) VALUES (1, 'tenanta.example', 1, 'active', 'enterprise', 0, 0, 0, ?, ?)", now, now)
	exec("INSERT INTO coremail_domains (id, name, tenant_id, status, plan, max_mailboxes, max_aliases, max_quota_mb, created_at, updated_at) VALUES (2, 'tenantb.example', 2, 'active', 'enterprise', 0, 0, 0, ?, ?)", now, now)

	const adminAEmail = "admin@tenanta.example"
	const adminAPass = "TenantAPass!2026"
	adminAHash, err := authenticator.HashPassword(adminAPass)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	exec("INSERT INTO users (created_at, updated_at, email, password_hash, role, tenant_id, active, email_verified) VALUES (?, ?, ?, ?, 'admin', 1, 1, 1)", now, now, adminAEmail, adminAHash)
	exec(`INSERT INTO coremail_mailboxes (id, domain_id, tenant_id, local_part, email, name, password_hash, auth_scheme, status, quota_mb, is_admin, created_at, updated_at)
	      VALUES (1, 1, 1, 'admin', ?, 'Admin A', ?, 'argon2id', 'active', 1024, 1, ?, ?)`, adminAEmail, adminAHash, now, now)

	victimHash, err := authenticator.HashPassword("VictimPass!2026")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	exec(`INSERT INTO coremail_mailboxes (id, domain_id, tenant_id, local_part, email, name, password_hash, auth_scheme, status, quota_mb, is_admin, created_at, updated_at)
	      VALUES (2, 2, 2, 'victim', ?, 'Victim', ?, 'argon2id', 'active', 1024, 0, ?, ?)`, "victim@tenantb.example", victimHash, now, now)

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

	token := loginDomainTest(t, router, adminAEmail, adminAPass)
	csrf := getDomainCSRF(t, router, token)

	return &enterpriseSmokeEnv{
		router:       router,
		sqlDB:        sqlDB,
		tenantAToken: token,
		tenantACSRF:  csrf,
	}
}

// --- requireTenantActive: active tenant succeeds through the real router ---

func TestRequireTenantActive_ActiveTenantSucceeds(t *testing.T) {
	e := buildEnterpriseSmokeEnv(t)
	buf, _ := json.Marshal(map[string]interface{}{
		"domain_id": 1, "from_addr": "smoke@tenanta.example", "to_addr": "dest@tenanta.example",
	})
	req := httptest.NewRequest("POST", "/api/v1/enterprise/aliases", bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Cookie", strings.Join([]string{
		"access_token=" + e.tenantAToken,
		"csrf_token=" + e.tenantACSRF,
	}, "; "))
	req.Header.Set("X-CSRF-Token", e.tenantACSRF)
	resp, err := e.router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 201 {
		t.Fatalf("create alias for active tenant through full router: expected 201, got %d: %s", resp.StatusCode, string(raw))
	}
}

// --- requireTenantActive: inactive tenant is rejected, not panicked ---

func TestRequireTenantActive_InactiveTenantRejected(t *testing.T) {
	e := buildEnterpriseSmokeEnv(t)
	if _, err := e.sqlDB.Exec("UPDATE tenants SET active = 0 WHERE id = 1"); err != nil {
		t.Fatalf("suspend tenant: %v", err)
	}

	buf, _ := json.Marshal(map[string]interface{}{
		"domain_id": 1, "from_addr": "blocked@tenanta.example", "to_addr": "dest@tenanta.example",
	})
	req := httptest.NewRequest("POST", "/api/v1/enterprise/aliases", bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Cookie", strings.Join([]string{
		"access_token=" + e.tenantAToken,
		"csrf_token=" + e.tenantACSRF,
	}, "; "))
	req.Header.Set("X-CSRF-Token", e.tenantACSRF)
	resp, err := e.router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 403 {
		t.Fatalf("mutation for suspended tenant: expected 403, got %d: %s", resp.StatusCode, string(raw))
	}

	// GET must still work while suspended (read access is intentionally
	// allowed so the tenant can see their own status).
	getReq := httptest.NewRequest("GET", "/api/v1/enterprise/aliases", nil)
	getReq.Header.Set("Cookie", "access_token="+e.tenantAToken)
	getResp, err := e.router.App().Test(getReq, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("get request failed: %v", err)
	}
	if getResp.StatusCode != 200 {
		t.Fatalf("GET for suspended tenant: expected 200, got %d", getResp.StatusCode)
	}
}

// --- requireTenantActive: missing tenant context is rejected safely ---

func TestRequireTenantActive_MissingTenantRejectedSafely(t *testing.T) {
	e := buildEnterpriseSmokeEnv(t)
	buf, _ := json.Marshal(map[string]interface{}{
		"domain_id": 1, "from_addr": "noauth@tenanta.example", "to_addr": "dest@tenanta.example",
	})
	req := httptest.NewRequest("POST", "/api/v1/enterprise/aliases", bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	// No access_token cookie at all.
	resp, err := e.router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	if resp.StatusCode == 500 {
		t.Fatalf("missing tenant context must not panic/500, got %d", resp.StatusCode)
	}
	if resp.StatusCode != 401 && resp.StatusCode != 403 {
		t.Fatalf("missing tenant context: expected 401 or 403, got %d", resp.StatusCode)
	}
}

// --- Groups feature: full round-trip through the real router ---

func TestEnterpriseGroups_FullRouterRoundTrip(t *testing.T) {
	e := buildEnterpriseSmokeEnv(t)

	create := func(name string) (int, map[string]interface{}) {
		buf, _ := json.Marshal(map[string]interface{}{"name": name, "description": "smoke test group"})
		req := httptest.NewRequest("POST", "/api/v1/enterprise/groups", bytes.NewReader(buf))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Cookie", strings.Join([]string{
			"access_token=" + e.tenantAToken,
			"csrf_token=" + e.tenantACSRF,
		}, "; "))
		req.Header.Set("X-CSRF-Token", e.tenantACSRF)
		resp, err := e.router.App().Test(req, fiber.TestConfig{Timeout: 0})
		if err != nil {
			t.Fatalf("create group request failed: %v", err)
		}
		out := map[string]interface{}{}
		raw, _ := io.ReadAll(resp.Body)
		_ = json.Unmarshal(raw, &out)
		return resp.StatusCode, out
	}

	status, body := create("smoke-test-group")
	if status != 201 {
		t.Fatalf("create group: expected 201, got %d: %v", status, body)
	}

	listReq := httptest.NewRequest("GET", "/api/v1/enterprise/groups", nil)
	listReq.Header.Set("Cookie", "access_token="+e.tenantAToken)
	listResp, err := e.router.App().Test(listReq, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("list groups request failed: %v", err)
	}
	if listResp.StatusCode != 200 {
		t.Fatalf("list groups: expected 200, got %d", listResp.StatusCode)
	}
	var groups []map[string]interface{}
	raw, _ := io.ReadAll(listResp.Body)
	if err := json.Unmarshal(raw, &groups); err != nil {
		t.Fatalf("unmarshal groups list: %v (raw=%s)", err, raw)
	}
	found := false
	for _, g := range groups {
		if g["name"] == "smoke-test-group" {
			found = true
		}
	}
	if !found {
		t.Fatalf("created group not present in list: %v", groups)
	}

	// Duplicate name for the same tenant must be rejected safely (not a
	// panic/500), enforced by the UNIQUE(tenant_id, name) constraint.
	status, body = create("smoke-test-group")
	if status == 500 {
		t.Fatalf("duplicate group name must not panic/500, got %d: %v", status, body)
	}
	if status == 201 {
		t.Fatalf("duplicate group name must be rejected, got 201: %v", body)
	}
}

// --- Admin mailboxes route: proves the ListUsers->ListMailboxes fix ---

func TestAdminMailboxesRoute_ReturnsMailboxesNotUsers(t *testing.T) {
	e := buildEnterpriseSmokeEnv(t)
	// The "admin" router group (internal/api/router.go) is mounted with
	// an empty URL prefix directly under /api/v1 — "admin" is only the
	// Go variable name, not a URL segment. The live route is therefore
	// /api/v1/mailboxes, not /api/v1/admin/mailboxes.
	req := httptest.NewRequest("GET", "/api/v1/mailboxes", nil)
	req.Header.Set("Cookie", "access_token="+e.tenantAToken)
	resp, err := e.router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		t.Fatalf("GET /admin/mailboxes: expected 200, got %d: %s", resp.StatusCode, string(raw))
	}
	var mailboxes []map[string]interface{}
	if err := json.Unmarshal(raw, &mailboxes); err != nil {
		t.Fatalf("unmarshal mailboxes response: %v (raw=%s)", err, raw)
	}
	if len(mailboxes) == 0 {
		t.Fatalf("expected at least one mailbox, got none")
	}
	m := mailboxes[0]
	// Mailbox-shaped: has "id" and "domain". The old, buggy ListUsers
	// response used a different field set (nullable mailbox_id, user_id,
	// "user-only" status marker) — presence of "mailbox_id" is the
	// clearest single discriminator that the route is still broken.
	if _, ok := m["id"]; !ok {
		t.Fatalf("mailbox response missing 'id' field (still user-shaped?): %v", m)
	}
	if _, ok := m["domain"]; !ok {
		t.Fatalf("mailbox response missing 'domain' field (still user-shaped?): %v", m)
	}
	if _, ok := m["mailbox_id"]; ok {
		t.Fatalf("mailbox response has 'mailbox_id' field — this is the ListUsers shape, route still broken: %v", m)
	}

	// Tenant scoping: tenant A must not see tenant B's mailbox.
	for _, mb := range mailboxes {
		if email, _ := mb["email"].(string); strings.Contains(email, "tenantb.example") {
			t.Fatalf("admin mailboxes list leaked cross-tenant mailbox: %v", mb)
		}
	}
}

// --- Sanity: migration actually creates the required tables ---

func TestGroupsSchema_MigrationCreatesRequiredTables(t *testing.T) {
	e := buildEnterpriseSmokeEnv(t)
	for _, table := range []string{"coremail_groups", "coremail_group_members"} {
		var name string
		if err := e.sqlDB.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&name); err != nil {
			t.Fatalf("table %s not created by migration: %v", table, err)
		}
	}
}
