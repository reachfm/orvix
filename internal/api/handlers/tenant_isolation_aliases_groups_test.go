package handlers_test

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

type aliasGroupIsolationEnv struct {
	router       *api.Router
	tenantAToken string
	tenantACSRF  string
}

func buildAliasGroupIsolationEnv(t *testing.T) *aliasGroupIsolationEnv {
	t.Helper()
	logger := zap.NewNop()
	cfg := config.Defaults()
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = filepath.Join(t.TempDir(), "aliasgroupisolation.db") + "?_busy_timeout=5000&_txlock=immediate"
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
	const adminAEmail = "admin@tenanta.example"
	const adminAPass = "TenantAPass!2026"
	adminAHash, err := authenticator.HashPassword(adminAPass)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	exec("INSERT INTO users (created_at, updated_at, email, password_hash, role, tenant_id, active, email_verified) VALUES (?, ?, ?, ?, 'admin', 1, 1, 1)", now, now, adminAEmail, adminAHash)
	exec("INSERT INTO coremail_mailboxes (id, domain_id, tenant_id, local_part, email, name, password_hash, auth_scheme, status, quota_mb, is_admin, created_at, updated_at) VALUES (1, 1, 1, 'admin', ?, 'Admin A', ?, 'argon2id', 'active', 1024, 1, ?, ?)", adminAEmail, adminAHash, now, now)

	exec("INSERT INTO coremail_domains (id, name, tenant_id, status, plan, max_mailboxes, max_aliases, max_quota_mb, created_at, updated_at) VALUES (2, 'tenantb.example', 2, 'active', 'enterprise', 0, 0, 0, ?, ?)", now, now)
	const victimEmail = "victim@tenantb.example"
	victimHash, err := authenticator.HashPassword("VictimPass!2026")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	exec("INSERT INTO coremail_mailboxes (id, domain_id, tenant_id, local_part, email, name, password_hash, auth_scheme, status, quota_mb, is_admin, created_at, updated_at) VALUES (2, 2, 2, 'victim', ?, 'Victim', ?, 'argon2id', 'active', 1024, 0, ?, ?)", victimEmail, victimHash, now, now)

	exec("INSERT INTO coremail_aliases (id, domain_id, tenant_id, from_addr, to_addr, active, created_at, updated_at) VALUES (1, 2, 2, 'info@tenantb.example', 'victim@tenantb.example', 1, ?, ?)", now, now)
	exec("INSERT INTO coremail_groups (id, tenant_id, name, description, created_at, updated_at) VALUES (1, 2, 'team-b', 'Tenant B team', ?, ?)", now, now)
	exec("INSERT INTO coremail_group_members (id, group_id, email, added_at) VALUES (1, 1, 'victim@tenantb.example', ?)", now)
	exec("INSERT INTO coremail_groups (id, tenant_id, name, description, created_at, updated_at) VALUES (2, 1, 'team-a', 'Tenant A team', ?, ?)", now, now)

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

	return &aliasGroupIsolationEnv{
		router:       router,
		tenantAToken: tenantAToken,
		tenantACSRF:  tenantACSRF,
	}
}

func aliasGroupReq(t *testing.T, e *aliasGroupIsolationEnv, method, path string, body interface{}) (int, string) {
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
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, strings.TrimSpace(string(raw))
}

func TestAliasGroupIsolation_DeleteCrossTenantAlias(t *testing.T) {
	e := buildAliasGroupIsolationEnv(t)
	status, body := aliasGroupReq(t, e, "DELETE", "/api/v1/enterprise/aliases/1", nil)
	t.Logf("DELETE cross-tenant alias: status=%d body=%s", status, body)
	if status != 404 {
		t.Fatalf("expected 404, got %d: %s", status, body)
	}
}

func TestAliasGroupIsolation_DeleteCrossTenantGroup(t *testing.T) {
	e := buildAliasGroupIsolationEnv(t)
	status, body := aliasGroupReq(t, e, "DELETE", "/api/v1/enterprise/groups/1", nil)
	t.Logf("DELETE cross-tenant group: status=%d body=%s", status, body)
	if status != 404 {
		t.Fatalf("expected 404, got %d: %s", status, body)
	}
}

func TestAliasGroupIsolation_AddMemberToCrossTenantGroup(t *testing.T) {
	e := buildAliasGroupIsolationEnv(t)
	status, body := aliasGroupReq(t, e, "POST", "/api/v1/enterprise/groups/1/members", map[string]string{"email": "admin@tenanta.example"})
	t.Logf("POST add member: status=%d body=%s", status, body)
	if status != 404 {
		t.Fatalf("expected 404, got %d: %s", status, body)
	}
}

func TestAliasGroupIsolation_RemoveMemberFromCrossTenantGroup(t *testing.T) {
	e := buildAliasGroupIsolationEnv(t)
	status, body := aliasGroupReq(t, e, "DELETE", "/api/v1/enterprise/groups/1/members/1", nil)
	t.Logf("DELETE remove member: status=%d body=%s", status, body)
	if status != 404 {
		t.Fatalf("expected 404, got %d: %s", status, body)
	}
}

func TestAliasGroupIsolation_OwnGroupCRUDSucceeds(t *testing.T) {
	e := buildAliasGroupIsolationEnv(t)

	status, body := aliasGroupReq(t, e, "POST", "/api/v1/enterprise/groups", map[string]string{"name": "team-a-new", "description": "New team"})
	t.Logf("POST own group: status=%d body=%s", status, body)
	if status != 201 {
		t.Fatalf("expected 201, got %d: %s", status, body)
	}

	req := httptest.NewRequest("GET", "/api/v1/enterprise/groups", nil)
	req.Header.Set("Cookie", "access_token="+e.tenantAToken)
	resp, err := e.router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("list groups: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(raw), "team-a") || !strings.Contains(string(raw), "team-a-new") {
		t.Fatalf("own groups not visible in list: %s", string(raw))
	}
	if strings.Contains(string(raw), "team-b") {
		t.Fatalf("cross-tenant group leaked in list: %s", string(raw))
	}

	status, body = aliasGroupReq(t, e, "DELETE", "/api/v1/enterprise/groups/2", nil)
	t.Logf("DELETE own group: status=%d body=%s", status, body)
	if status != 200 {
		t.Fatalf("expected 200, got %d: %s", status, body)
	}
}

func TestAliasGroupIsolation_DeleteOwnAlias(t *testing.T) {
	e := buildAliasGroupIsolationEnv(t)

	aliasGroupReq(t, e, "POST", "/api/v1/enterprise/aliases", map[string]interface{}{
		"domain_id": 1,
		"from_addr": "admin@tenanta.example",
		"to_addr":   "forward@example.com",
	})

	status, body := aliasGroupReq(t, e, "DELETE", "/api/v1/enterprise/aliases/2", nil)
	t.Logf("DELETE own alias: status=%d body=%s", status, body)
	if status != 200 {
		t.Fatalf("expected 200, got %d: %s", status, body)
	}
}
