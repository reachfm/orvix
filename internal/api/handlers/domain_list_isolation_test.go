package handlers_test

// Router-level regression coverage for the tenant-isolation fix in
// ListDomains (internal/api/handlers/handlers.go), backing GET
// /api/v1/domains.
//
// Before the fix, the handler ran an unscoped
// `SELECT id, name, plan, status FROM coremail_domains WHERE deleted_at IS
// NULL [q/status filters]` query with no tenant_id clause, so a
// tenant-scoped RoleAdmin received every tenant's domains. The sibling
// paths (ExportDomainsCSV, ListMailboxes, GetDomain, GetMailbox) already
// scope by tenant via isSuperRole / scopedTenantID / callerOwnsTenant;
// ListDomains did not.
//
// These tests exercise the REAL middleware chain (rate-limit -> apikey/JWT
// auth -> TenantMiddleware -> RequireAnyRole -> CSRF) via /api/v1/auth/login.

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http/httptest"
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

type domainListEnv struct {
	router        *api.Router
	tenant1Adm    string // RoleAdmin, tenant 1
	tenant2Adm    string // RoleAdmin, tenant 2
	superAdm      string // platform_super_admin
	plainUser     string // RoleUser — must be rejected by the admin group entirely
	noTenantAdmin string // RoleAdmin whose user row has tenant_id = 0 (unresolved tenant)
}

func buildDomainListEnv(t *testing.T) *domainListEnv {
	t.Helper()
	logger := zap.NewNop()
	cfg := config.Defaults()
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = t.TempDir() + "/domainlist.db?_loc=auto&_busy_timeout=5000&_txlock=immediate"
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
	exec := func(q string, args ...interface{}) {
		t.Helper()
		if _, err := sqlDB.Exec(q, args...); err != nil {
			t.Fatalf("exec %q: %v", q, err)
		}
	}

	exec("INSERT INTO tenants (id, created_at, updated_at, name, slug, domain, plan, active) VALUES (1, ?, ?, 'tenant-a', 'tenant-a', 't1.example', 'enterprise', 1)", now, now)
	exec("INSERT INTO tenants (id, created_at, updated_at, name, slug, domain, plan, active) VALUES (2, ?, ?, 'tenant-b', 'tenant-b', 't2.example', 'enterprise', 1)", now, now)

	// Tenant 1: two domains (one active, one suspended) to exercise q/status
	// filters within the caller's own tenant.
	exec("INSERT INTO coremail_domains (id, name, tenant_id, status, plan, max_mailboxes, max_aliases, max_quota_mb, created_at, updated_at) VALUES (1, 'alpha.t1.example', 1, 'active', 'enterprise', 0, 0, 0, ?, ?)", now, now)
	exec("INSERT INTO coremail_domains (id, name, tenant_id, status, plan, max_mailboxes, max_aliases, max_quota_mb, created_at, updated_at) VALUES (2, 'beta.t1.example', 1, 'suspended', 'smb', 0, 0, 0, ?, ?)", now, now)
	// Soft-deleted tenant-1 domain — must never appear.
	exec("INSERT INTO coremail_domains (id, name, tenant_id, status, plan, max_mailboxes, max_aliases, max_quota_mb, created_at, updated_at, deleted_at) VALUES (3, 'ghost.t1.example', 1, 'active', 'enterprise', 0, 0, 0, ?, ?, ?)", now, now, now)

	// Tenant 2: the victim domain tenant 1 must never see.
	exec("INSERT INTO coremail_domains (id, name, tenant_id, status, plan, max_mailboxes, max_aliases, max_quota_mb, created_at, updated_at) VALUES (4, 'victim.t2.example', 2, 'active', 'enterprise', 0, 0, 0, ?, ?)", now, now)

	a1Hash, _ := authenticator.HashPassword("Tenant1Pass!2026")
	exec("INSERT INTO users (created_at, updated_at, email, password_hash, role, tenant_id, active, email_verified) VALUES (?, ?, 'admin1@t1.example', ?, 'admin', 1, 1, 1)", now, now, a1Hash)
	a2Hash, _ := authenticator.HashPassword("Tenant2Pass!2026")
	exec("INSERT INTO users (created_at, updated_at, email, password_hash, role, tenant_id, active, email_verified) VALUES (?, ?, 'admin2@t2.example', ?, 'admin', 2, 1, 1)", now, now, a2Hash)
	psaHash, _ := authenticator.HashPassword("PlatformPass!2026")
	exec("INSERT INTO users (created_at, updated_at, email, password_hash, role, tenant_id, active, email_verified) VALUES (?, ?, 'psa@platform.local', ?, 'platform_super_admin', NULL, 1, 1)", now, now, psaHash)
	userHash, _ := authenticator.HashPassword("PlainUserPass!2026")
	exec("INSERT INTO users (created_at, updated_at, email, password_hash, role, tenant_id, active, email_verified) VALUES (?, ?, 'plain@t1.example', ?, 'user', 1, 1, 1)", now, now, userHash)
	// RoleAdmin with an unresolved tenant (tenant_id = 0 -> TenantMiddleware
	// never sets the "tenant_id" local -> scopedTenantID falls back to -1).
	noTenantHash, _ := authenticator.HashPassword("NoTenantPass!2026")
	exec("INSERT INTO users (created_at, updated_at, email, password_hash, role, tenant_id, active, email_verified) VALUES (?, ?, 'notenant@nowhere.example', ?, 'admin', 0, 1, 1)", now, now, noTenantHash)

	scratchDir := t.TempDir()
	adminDir := scratchDir + "/admin"
	webmailDir := scratchDir + "/webmail"
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

	return &domainListEnv{
		router:        router,
		tenant1Adm:    domainListLogin(t, router, "admin1@t1.example", "Tenant1Pass!2026"),
		tenant2Adm:    domainListLogin(t, router, "admin2@t2.example", "Tenant2Pass!2026"),
		superAdm:      domainListLogin(t, router, "psa@platform.local", "PlatformPass!2026"),
		plainUser:     domainListLogin(t, router, "plain@t1.example", "PlainUserPass!2026"),
		noTenantAdmin: domainListLogin(t, router, "notenant@nowhere.example", "NoTenantPass!2026"),
	}
}

func domainListLogin(t *testing.T, r *api.Router, email, pass string) string {
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

type domainListRow struct {
	ID           uint   `json:"id"`
	Domain       string `json:"domain"`
	Plan         string `json:"plan"`
	Status       string `json:"status"`
	MailboxCount int    `json:"mailbox_count"`
}

func listDomains(t *testing.T, e *domainListEnv, token, path string) (int, []domainListRow) {
	t.Helper()
	req := httptest.NewRequest("GET", path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := e.router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("request %s: %v", path, err)
	}
	raw, _ := io.ReadAll(resp.Body)
	var rows []domainListRow
	_ = json.Unmarshal(raw, &rows)
	return resp.StatusCode, rows
}

func domainNames(rows []domainListRow) []string {
	names := make([]string, 0, len(rows))
	for _, r := range rows {
		names = append(names, r.Domain)
	}
	return names
}

func containsDomain(rows []domainListRow, name string) bool {
	for _, r := range rows {
		if r.Domain == name {
			return true
		}
	}
	return false
}

// 1. Platform super admin sees domains from multiple tenants.
func TestListDomains_SuperAdminSeesMultipleTenants(t *testing.T) {
	e := buildDomainListEnv(t)
	status, rows := listDomains(t, e, e.superAdm, "/api/v1/domains")
	if status != 200 {
		t.Fatalf("super admin: expected 200, got %d", status)
	}
	for _, want := range []string{"alpha.t1.example", "beta.t1.example", "victim.t2.example"} {
		if !containsDomain(rows, want) {
			t.Fatalf("super admin list missing %q; got %v", want, domainNames(rows))
		}
	}
}

// 2. Tenant admin sees only its own tenant's domains.
func TestListDomains_TenantAdminSeesOnlyOwnTenant(t *testing.T) {
	e := buildDomainListEnv(t)
	status, rows := listDomains(t, e, e.tenant1Adm, "/api/v1/domains")
	if status != 200 {
		t.Fatalf("tenant1: expected 200, got %d", status)
	}
	for _, want := range []string{"alpha.t1.example", "beta.t1.example"} {
		if !containsDomain(rows, want) {
			t.Fatalf("tenant1 list missing own domain %q; got %v", want, domainNames(rows))
		}
	}
	if containsDomain(rows, "victim.t2.example") {
		t.Fatalf("CROSS-TENANT LEAK: tenant1 list contains victim.t2.example; got %v", domainNames(rows))
	}
}

// 3. Tenant admin cannot see another tenant's domains (explicit negative).
func TestListDomains_TenantAdminCannotSeeOtherTenant(t *testing.T) {
	e := buildDomainListEnv(t)
	_, rows := listDomains(t, e, e.tenant1Adm, "/api/v1/domains")
	if containsDomain(rows, "victim.t2.example") {
		t.Fatalf("tenant1 must never see tenant2's domain; got %v", domainNames(rows))
	}
}

// 4. Isolation works in both tenant directions.
func TestListDomains_IsolationBidirectional(t *testing.T) {
	e := buildDomainListEnv(t)
	status, rows := listDomains(t, e, e.tenant2Adm, "/api/v1/domains")
	if status != 200 {
		t.Fatalf("tenant2: expected 200, got %d", status)
	}
	if !containsDomain(rows, "victim.t2.example") {
		t.Fatalf("tenant2 list missing own domain; got %v", domainNames(rows))
	}
	for _, bad := range []string{"alpha.t1.example", "beta.t1.example"} {
		if containsDomain(rows, bad) {
			t.Fatalf("CROSS-TENANT LEAK: tenant2 list contains %q; got %v", bad, domainNames(rows))
		}
	}
}

// 5/6. Forged tenant_id / organization_id / customer_id query params cannot
// escape tenant scope.
func TestListDomains_ForgedTenantIdentityParamsIgnored(t *testing.T) {
	e := buildDomainListEnv(t)
	for _, path := range []string{
		"/api/v1/domains?tenant_id=2",
		"/api/v1/domains?organization_id=2",
		"/api/v1/domains?customer_id=2",
	} {
		status, rows := listDomains(t, e, e.tenant1Adm, path)
		if status != 200 {
			t.Fatalf("%s: expected 200, got %d", path, status)
		}
		if containsDomain(rows, "victim.t2.example") {
			t.Fatalf("FORGED PARAM ESCAPED TENANT: %s exposed victim.t2.example; got %v", path, domainNames(rows))
		}
		if !containsDomain(rows, "alpha.t1.example") {
			t.Fatalf("%s dropped own tenant rows; got %v", path, domainNames(rows))
		}
	}
}

// 7. q filter still works inside the caller's tenant.
func TestListDomains_QFilterWorksWithinTenant(t *testing.T) {
	e := buildDomainListEnv(t)
	status, rows := listDomains(t, e, e.tenant1Adm, "/api/v1/domains?q=alpha")
	if status != 200 {
		t.Fatalf("q filter: expected 200, got %d", status)
	}
	if len(rows) != 1 || rows[0].Domain != "alpha.t1.example" {
		t.Fatalf("q=alpha: expected exactly [alpha.t1.example], got %v", domainNames(rows))
	}
}

// 8. status filter still works inside the caller's tenant.
func TestListDomains_StatusFilterWorksWithinTenant(t *testing.T) {
	e := buildDomainListEnv(t)
	status, rows := listDomains(t, e, e.tenant1Adm, "/api/v1/domains?status=suspended")
	if status != 200 {
		t.Fatalf("status filter: expected 200, got %d", status)
	}
	if len(rows) != 1 || rows[0].Domain != "beta.t1.example" {
		t.Fatalf("status=suspended: expected exactly [beta.t1.example], got %v", domainNames(rows))
	}
}

// 9. Deleted domains remain excluded (own tenant and cross-tenant).
func TestListDomains_DeletedDomainsExcluded(t *testing.T) {
	e := buildDomainListEnv(t)
	_, rows := listDomains(t, e, e.tenant1Adm, "/api/v1/domains")
	if containsDomain(rows, "ghost.t1.example") {
		t.Fatalf("soft-deleted domain leaked: %v", domainNames(rows))
	}
	_, superRows := listDomains(t, e, e.superAdm, "/api/v1/domains")
	if containsDomain(superRows, "ghost.t1.example") {
		t.Fatalf("soft-deleted domain leaked even to super admin: %v", domainNames(superRows))
	}
}

// 10. Unauthenticated request is rejected.
func TestListDomains_RequiresAuth(t *testing.T) {
	e := buildDomainListEnv(t)
	status, _ := listDomains(t, e, "", "/api/v1/domains")
	if status != 401 {
		t.Fatalf("unauthenticated: expected 401, got %d", status)
	}
}

// 11. Unauthorized role is rejected (RoleUser is not in RequireAnyRole).
func TestListDomains_UnauthorizedRoleRejected(t *testing.T) {
	e := buildDomainListEnv(t)
	status, _ := listDomains(t, e, e.plainUser, "/api/v1/domains")
	if status != 403 {
		t.Fatalf("plain user role: expected 403, got %d", status)
	}
}

// 12. Empty/unresolved tenant context fails safely for a non-super caller:
// no panic, no 500, and — critically — no cross-tenant data returned. The
// handler degrades to an empty list rather than leaking or erroring.
func TestListDomains_UnresolvedTenantFailsSafely(t *testing.T) {
	e := buildDomainListEnv(t)
	status, rows := listDomains(t, e, e.noTenantAdmin, "/api/v1/domains")
	if status != 200 {
		t.Fatalf("unresolved tenant: expected 200 (empty list), got %d", status)
	}
	if len(rows) != 0 {
		t.Fatalf("unresolved tenant admin must see zero domains, got %v", domainNames(rows))
	}
}

// 13. Response contains no cross-tenant IDs, names, plans, or statuses.
func TestListDomains_NoCrossTenantFieldLeakage(t *testing.T) {
	e := buildDomainListEnv(t)
	_, rows := listDomains(t, e, e.tenant1Adm, "/api/v1/domains")
	for _, r := range rows {
		if r.Domain == "victim.t2.example" || r.ID == 4 {
			t.Fatalf("tenant2's domain row leaked into tenant1's response: %+v", r)
		}
	}
}
