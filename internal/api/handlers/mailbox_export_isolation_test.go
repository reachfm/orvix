package handlers_test

// Router-level regression coverage for the tenant-isolation fix in
// ExportMailboxesCSV and ExportDomainsCSV (internal/api/handlers/handlers.go).
//
// Before the fix, both CSV export handlers ran an unscoped
// `... WHERE deleted_at IS NULL` query with no tenant_id filter, so a
// tenant-scoped RoleAdmin calling GET /api/v1/mailboxes/export (or
// /domains/export) received every tenant's rows. The sibling read paths
// (ListMailboxes, GetMailbox, GetDomain) already scoped by tenant via
// isSuperRole / scopedTenantID / callerOwnsTenant; the exports did not.
//
// These tests exercise the REAL middleware chain (rate-limit -> apikey/JWT
// auth -> TenantMiddleware -> RequireAnyRole -> CSRF) by logging in through
// /api/v1/auth/login as three distinct principals:
//   - admin1: RoleAdmin scoped to tenant 1
//   - admin2: RoleAdmin scoped to tenant 2
//   - psa:    platform_super_admin (no tenant) — a super role
// and asserting the CSV body contents match the required visibility model.

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"io"
	"net/http/httptest"
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

type exportEnv struct {
	router      *api.Router
	tenant1Adm  string // RoleAdmin, tenant 1
	tenant2Adm  string // RoleAdmin, tenant 2
	superAdm    string // platform_super_admin
	weirdStatus string // crafted status value seeded into a tenant-1 mailbox
}

func buildExportEnv(t *testing.T) *exportEnv {
	t.Helper()
	logger := zap.NewNop()
	cfg := config.Defaults()
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = t.TempDir() + "/export.db?_loc=auto&_busy_timeout=5000&_txlock=immediate"
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

	exec("INSERT INTO coremail_domains (id, name, tenant_id, status, plan, max_mailboxes, max_aliases, max_quota_mb, created_at, updated_at) VALUES (1, 't1.example', 1, 'active', 'enterprise', 0, 0, 0, ?, ?)", now, now)
	exec("INSERT INTO coremail_domains (id, name, tenant_id, status, plan, max_mailboxes, max_aliases, max_quota_mb, created_at, updated_at) VALUES (2, 't2.example', 2, 'suspended', 'smb', 0, 0, 0, ?, ?)", now, now)

	a1Hash, _ := authenticator.HashPassword("Tenant1Pass!2026")
	exec("INSERT INTO users (created_at, updated_at, email, password_hash, role, tenant_id, active, email_verified) VALUES (?, ?, 'admin1@t1.example', ?, 'admin', 1, 1, 1)", now, now, a1Hash)
	a2Hash, _ := authenticator.HashPassword("Tenant2Pass!2026")
	exec("INSERT INTO users (created_at, updated_at, email, password_hash, role, tenant_id, active, email_verified) VALUES (?, ?, 'admin2@t2.example', ?, 'admin', 2, 1, 1)", now, now, a2Hash)
	psaHash, _ := authenticator.HashPassword("PlatformPass!2026")
	exec("INSERT INTO users (created_at, updated_at, email, password_hash, role, tenant_id, active, email_verified) VALUES (?, ?, 'psa@platform.local', ?, 'platform_super_admin', NULL, 1, 1)", now, now, psaHash)

	mb := func(id, domainID, tenantID int, localPart, email, status string, isAdmin int) {
		exec(`INSERT INTO coremail_mailboxes (id, domain_id, tenant_id, local_part, email, name, password_hash, auth_scheme, status, quota_mb, is_admin, created_at, updated_at)
		      VALUES (?, ?, ?, ?, ?, ?, '$argon2id$v=19$m=1024,t=1,p=1$c2FsdA$aGFzaA', 'argon2id', ?, 1024, ?, ?, ?)`,
			id, domainID, tenantID, localPart, email, localPart, status, isAdmin, now, now)
	}

	// Crafted status value exercising CSV escaping: embedded comma, double
	// quote, and newline. csvField must quote the field and double the quote.
	weird := "hold,\"weird\"\nline2"

	// Tenant 1 mailboxes.
	mb(1, 1, 1, "admin1", "admin1@t1.example", "active", 1)
	mb(2, 1, 1, "alice", "alice@t1.example", "active", 0)
	mb(3, 1, 1, "weird", "weird@t1.example", weird, 0)
	// Soft-deleted tenant-1 mailbox — must never appear.
	mb(4, 1, 1, "ghost", "ghost@t1.example", "active", 0)
	exec("UPDATE coremail_mailboxes SET deleted_at = ? WHERE id = 4", now)

	// Tenant 2 mailboxes — the victim rows tenant 1 must never see.
	mb(5, 2, 2, "victim", "victim@t2.example", "active", 0)
	mb(6, 2, 2, "bob", "bob@t2.example", "suspended", 0)

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

	return &exportEnv{
		router:      router,
		tenant1Adm:  exportLogin(t, router, "admin1@t1.example", "Tenant1Pass!2026"),
		tenant2Adm:  exportLogin(t, router, "admin2@t2.example", "Tenant2Pass!2026"),
		superAdm:    exportLogin(t, router, "psa@platform.local", "PlatformPass!2026"),
		weirdStatus: weird,
	}
}

func exportLogin(t *testing.T, r *api.Router, email, pass string) string {
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

// csvExport issues an authenticated GET and returns (status, raw CSV body).
func csvExport(t *testing.T, e *exportEnv, token, path string) (int, string) {
	t.Helper()
	req := httptest.NewRequest("GET", path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := e.router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("export request %s: %v", path, err)
	}
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(raw)
}

// parseCSVEmails returns the set of values in the `email` column (mailbox CSV)
// or `domain` column (domain CSV) using a real CSV parser so escaping is
// exercised, not naive line splitting.
func parseCSVColumn(t *testing.T, body, col string) []string {
	t.Helper()
	r := csv.NewReader(strings.NewReader(body))
	records, err := r.ReadAll()
	if err != nil {
		t.Fatalf("CSV parse error (escaping bug?): %v\nbody=%q", err, body)
	}
	if len(records) == 0 {
		t.Fatalf("empty CSV")
	}
	idx := -1
	for i, h := range records[0] {
		if h == col {
			idx = i
		}
	}
	if idx < 0 {
		t.Fatalf("column %q not found in header %v", col, records[0])
	}
	var out []string
	for _, row := range records[1:] {
		if idx < len(row) {
			out = append(out, row[idx])
		}
	}
	return out
}

func contains(vals []string, want string) bool {
	for _, v := range vals {
		if v == want {
			return true
		}
	}
	return false
}

// ---- Mailbox export ----

func TestMailboxExport_SuperAdminSeesAllTenants(t *testing.T) {
	e := buildExportEnv(t)
	status, body := csvExport(t, e, e.superAdm, "/api/v1/mailboxes/export")
	if status != 200 {
		t.Fatalf("super admin export: expected 200, got %d: %s", status, body)
	}
	emails := parseCSVColumn(t, body, "email")
	for _, want := range []string{"admin1@t1.example", "alice@t1.example", "victim@t2.example", "bob@t2.example"} {
		if !contains(emails, want) {
			t.Fatalf("super admin export missing %q; got %v", want, emails)
		}
	}
	if contains(emails, "ghost@t1.example") {
		t.Fatalf("soft-deleted mailbox leaked into export: %v", emails)
	}
}

func TestMailboxExport_TenantAdminSeesOnlyOwnTenant(t *testing.T) {
	e := buildExportEnv(t)
	status, body := csvExport(t, e, e.tenant1Adm, "/api/v1/mailboxes/export")
	if status != 200 {
		t.Fatalf("tenant1 export: expected 200, got %d: %s", status, body)
	}
	emails := parseCSVColumn(t, body, "email")
	// Must contain tenant-1 mailboxes.
	for _, want := range []string{"admin1@t1.example", "alice@t1.example", "weird@t1.example"} {
		if !contains(emails, want) {
			t.Fatalf("tenant1 export missing own mailbox %q; got %v", want, emails)
		}
	}
	// Must NOT contain any tenant-2 mailbox.
	for _, bad := range []string{"victim@t2.example", "bob@t2.example"} {
		if contains(emails, bad) {
			t.Fatalf("CROSS-TENANT LEAK: tenant1 export contains %q; got %v", bad, emails)
		}
	}
	// Soft-deleted own mailbox excluded.
	if contains(emails, "ghost@t1.example") {
		t.Fatalf("soft-deleted mailbox leaked: %v", emails)
	}
}

func TestMailboxExport_TenantAdminCannotSeeOtherTenant(t *testing.T) {
	e := buildExportEnv(t)
	// Tenant-2 admin must see only tenant-2, never tenant-1 — the mirror
	// direction, proving isolation is bidirectional and not incidental.
	status, body := csvExport(t, e, e.tenant2Adm, "/api/v1/mailboxes/export")
	if status != 200 {
		t.Fatalf("tenant2 export: expected 200, got %d: %s", status, body)
	}
	emails := parseCSVColumn(t, body, "email")
	for _, want := range []string{"victim@t2.example", "bob@t2.example"} {
		if !contains(emails, want) {
			t.Fatalf("tenant2 export missing own mailbox %q; got %v", want, emails)
		}
	}
	for _, bad := range []string{"admin1@t1.example", "alice@t1.example", "weird@t1.example"} {
		if contains(emails, bad) {
			t.Fatalf("CROSS-TENANT LEAK: tenant2 export contains %q; got %v", bad, emails)
		}
	}
}

func TestMailboxExport_ForgedTenantIDQueryParamIgnored(t *testing.T) {
	e := buildExportEnv(t)
	// A tenant-1 admin appends ?tenant_id=2 (and other forged identity
	// params) trying to escape their tenant. The handler derives tenant
	// only from authenticated context, so these must be ignored.
	for _, path := range []string{
		"/api/v1/mailboxes/export?tenant_id=2",
		"/api/v1/mailboxes/export?organization_id=2",
		"/api/v1/mailboxes/export?customer_id=2",
	} {
		status, body := csvExport(t, e, e.tenant1Adm, path)
		if status != 200 {
			t.Fatalf("%s: expected 200, got %d: %s", path, status, body)
		}
		emails := parseCSVColumn(t, body, "email")
		for _, bad := range []string{"victim@t2.example", "bob@t2.example"} {
			if contains(emails, bad) {
				t.Fatalf("FORGED PARAM ESCAPED TENANT: %s exposed %q; got %v", path, bad, emails)
			}
		}
		if !contains(emails, "alice@t1.example") {
			t.Fatalf("%s dropped own tenant rows; got %v", path, emails)
		}
	}
}

func TestMailboxExport_CSVHeaderAndEscaping(t *testing.T) {
	e := buildExportEnv(t)
	status, body := csvExport(t, e, e.tenant1Adm, "/api/v1/mailboxes/export")
	if status != 200 {
		t.Fatalf("export: expected 200, got %d", status)
	}
	// Header row exact.
	if !strings.HasPrefix(body, "email,status,is_admin\n") {
		t.Fatalf("unexpected header row: %q", strings.SplitN(body, "\n", 2)[0])
	}
	// Full document parses cleanly with a real CSV reader (proves comma /
	// quote / newline in the crafted status field are escaped correctly).
	r := csv.NewReader(strings.NewReader(body))
	records, err := r.ReadAll()
	if err != nil {
		t.Fatalf("CSV escaping broken — parser rejected body: %v", err)
	}
	// Find the weird@t1.example row and assert its status round-trips to the
	// exact crafted value including the comma, quote, and newline.
	var gotStatus string
	found := false
	for _, row := range records[1:] {
		if len(row) >= 2 && row[0] == "weird@t1.example" {
			gotStatus = row[1]
			found = true
		}
	}
	if !found {
		t.Fatalf("weird@t1.example row not found in export")
	}
	if gotStatus != e.weirdStatus {
		t.Fatalf("CSV escaping corrupted status: got %q, want %q", gotStatus, e.weirdStatus)
	}
}

func TestMailboxExport_NoSensitiveMaterial(t *testing.T) {
	e := buildExportEnv(t)
	_, body := csvExport(t, e, e.superAdm, "/api/v1/mailboxes/export")
	low := strings.ToLower(body)
	for _, forbidden := range []string{"password", "hash", "argon2", "token", "jwt", "bearer", "secret", "quota_mb", "1024"} {
		if strings.Contains(low, forbidden) {
			t.Fatalf("mailbox CSV must not contain %q: %s", forbidden, body)
		}
	}
}

func TestMailboxExport_RequiresAuth(t *testing.T) {
	e := buildExportEnv(t)
	status, _ := csvExport(t, e, "", "/api/v1/mailboxes/export")
	if status != 401 {
		t.Fatalf("unauthenticated export: expected 401, got %d", status)
	}
}

// ---- Domain export (Phase 5 — adjacent handler, same fix) ----

func TestDomainExport_TenantAdminSeesOnlyOwnTenant(t *testing.T) {
	e := buildExportEnv(t)
	status, body := csvExport(t, e, e.tenant1Adm, "/api/v1/domains/export")
	if status != 200 {
		t.Fatalf("tenant1 domain export: expected 200, got %d: %s", status, body)
	}
	domains := parseCSVColumn(t, body, "domain")
	if !contains(domains, "t1.example") {
		t.Fatalf("tenant1 domain export missing own domain; got %v", domains)
	}
	if contains(domains, "t2.example") {
		t.Fatalf("CROSS-TENANT LEAK: tenant1 domain export contains t2.example; got %v", domains)
	}
}

func TestDomainExport_SuperAdminSeesAllTenants(t *testing.T) {
	e := buildExportEnv(t)
	status, body := csvExport(t, e, e.superAdm, "/api/v1/domains/export")
	if status != 200 {
		t.Fatalf("super admin domain export: expected 200, got %d: %s", status, body)
	}
	domains := parseCSVColumn(t, body, "domain")
	for _, want := range []string{"t1.example", "t2.example"} {
		if !contains(domains, want) {
			t.Fatalf("super admin domain export missing %q; got %v", want, domains)
		}
	}
}

func TestDomainExport_ForgedTenantIDQueryParamIgnored(t *testing.T) {
	e := buildExportEnv(t)
	status, body := csvExport(t, e, e.tenant1Adm, "/api/v1/domains/export?tenant_id=2")
	if status != 200 {
		t.Fatalf("domain export with forged param: expected 200, got %d", status)
	}
	domains := parseCSVColumn(t, body, "domain")
	if contains(domains, "t2.example") {
		t.Fatalf("FORGED PARAM ESCAPED TENANT on domain export: got %v", domains)
	}
	if !contains(domains, "t1.example") {
		t.Fatalf("domain export dropped own tenant; got %v", domains)
	}
}
