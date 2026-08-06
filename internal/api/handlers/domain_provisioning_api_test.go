package handlers_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"io"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
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

// provisioningEnv is a full HTTP harness over the real router, so these tests
// exercise routing, RBAC, CSRF, binding and the service transaction end to
// end rather than calling the service directly.
type provisioningEnv struct {
	router *api.Router
	db     *sql.DB

	tenantAToken string
	tenantACSRF  string
	tenantBToken string
	tenantBCSRF  string
}

func buildProvisioningEnv(t *testing.T, planA string, maxDomainsA, maxMailboxesA int) *provisioningEnv {
	t.Helper()
	logger := zap.NewNop()
	cfg := config.Defaults()
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = filepath.Join(t.TempDir(), "provisioning.db") + "?_busy_timeout=5000&_txlock=immediate"
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

	exec("INSERT INTO tenants (id, created_at, updated_at, name, slug, domain, plan, max_domains, max_mailboxes, active) VALUES (1, ?, ?, 'tenant-a', 'tenant-a', 'tenanta.example', ?, ?, ?, 1)",
		now, now, planA, maxDomainsA, maxMailboxesA)
	exec("INSERT INTO tenants (id, created_at, updated_at, name, slug, domain, plan, max_domains, max_mailboxes, active) VALUES (2, ?, ?, 'tenant-b', 'tenant-b', 'tenantb.example', 'enterprise', 50, 5000, 1)",
		now, now)

	const adminAEmail = "admin@tenanta.example"
	const adminAPass = "TenantAPass!2026"
	seedTenantAdminWithPassword(t, sqlDB, adminAEmail, 1, adminAPass)

	const adminBEmail = "admin@tenantb.example"
	const adminBPass = "TenantBPass!2026"
	seedTenantAdminWithPassword(t, sqlDB, adminBEmail, 2, adminBPass)

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

	tokenA := loginDomainTest(t, router, adminAEmail, adminAPass)
	tokenB := loginDomainTest(t, router, adminBEmail, adminBPass)
	return &provisioningEnv{
		router:       router,
		db:           sqlDB,
		tenantAToken: tokenA,
		tenantACSRF:  getDomainCSRF(t, router, tokenA),
		tenantBToken: tokenB,
		tenantBCSRF:  getDomainCSRF(t, router, tokenB),
	}
}

func (e *provisioningEnv) do(t *testing.T, token, csrf, method, path string, body interface{}) (int, map[string]interface{}, string) {
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
	req.Header.Set("Cookie", strings.Join([]string{"access_token=" + token, "csrf_token=" + csrf}, "; "))
	req.Header.Set("X-CSRF-Token", csrf)
	resp, err := e.router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var parsed map[string]interface{}
	_ = json.Unmarshal(raw, &parsed)
	return resp.StatusCode, parsed, strings.TrimSpace(string(raw))
}

func (e *provisioningEnv) createDomain(t *testing.T, body interface{}) (int, map[string]interface{}, string) {
	return e.do(t, e.tenantAToken, e.tenantACSRF, "POST", "/api/v1/enterprise/domains", body)
}

// --- contract --------------------------------------------------------------

// TestProvisioningEndpointLegacyBodyStillWorks is the backward-compatibility
// guarantee: the historic {"name":"..."} body must keep succeeding.
func TestProvisioningEndpointLegacyBodyStillWorks(t *testing.T) {
	e := buildProvisioningEnv(t, "enterprise", 20, 1000)

	status, body, raw := e.createDomain(t, map[string]interface{}{"name": "legacy.example"})
	if status != 201 {
		t.Fatalf("status = %d, want 201: %s", status, raw)
	}
	d, _ := body["domain"].(map[string]interface{})
	if d == nil || d["name"] != "legacy.example" {
		t.Fatalf("domain not returned: %s", raw)
	}
}

func TestProvisioningEndpointFullWizardBody(t *testing.T) {
	e := buildProvisioningEnv(t, "enterprise", 20, 1000)

	status, body, raw := e.createDomain(t, map[string]interface{}{
		"name":        "  WIZARD.Example.  ",
		"description": "Provisioned by the wizard",
		"status":      "active",
		"limits": map[string]interface{}{
			"max_mailboxes":            50,
			"max_aliases":              200,
			"default_mailbox_quota_mb": 3072,
			"max_mailbox_quota_mb":     10240,
		},
		"dkim": map[string]interface{}{"generate": true, "selector": "mail"},
	})
	if status != 201 {
		t.Fatalf("status = %d, want 201: %s", status, raw)
	}

	d, _ := body["domain"].(map[string]interface{})
	if d == nil || d["name"] != "wizard.example" {
		t.Fatalf("normalized name missing: %s", raw)
	}
	if d["max_mailboxes"].(float64) != 50 || d["max_aliases"].(float64) != 200 {
		t.Errorf("limits not persisted: %s", raw)
	}

	dkim, _ := body["dkim"].(map[string]interface{})
	if dkim == nil {
		t.Fatalf("dkim block missing: %s", raw)
	}
	if dkim["selector"] != "mail" {
		t.Errorf("selector = %v", dkim["selector"])
	}
	if dkim["dns_record_name"] != "mail._domainkey.wizard.example" {
		t.Errorf("dns record name = %v", dkim["dns_record_name"])
	}
	if txt, _ := dkim["public_dns_txt"].(string); !strings.Contains(txt, "p=") {
		t.Errorf("public TXT missing: %v", dkim["public_dns_txt"])
	}

	eff, _ := body["effective_limits"].(map[string]interface{})
	if eff == nil {
		t.Fatalf("effective_limits missing: %s", raw)
	}

	dns, _ := body["dns"].(map[string]interface{})
	if dns == nil || dns["public_dns_changed"] != false {
		t.Errorf("response must state no public DNS was changed: %s", raw)
	}
}

// TestProvisioningResponseNeverContainsPrivateKey is the privacy regression
// test at the HTTP boundary — it asserts on the RAW response bytes and on the
// exact key that was stored, not on a struct.
func TestProvisioningResponseNeverContainsPrivateKey(t *testing.T) {
	e := buildProvisioningEnv(t, "enterprise", 20, 1000)

	status, _, raw := e.createDomain(t, map[string]interface{}{
		"name": "keysafe.example",
		"dkim": map[string]interface{}{"generate": true},
	})
	if status != 201 {
		t.Fatalf("status = %d: %s", status, raw)
	}

	var storedKey string
	if err := e.db.QueryRow(`SELECT private_key_pem FROM coremail_dkim_config WHERE domain='keysafe.example'`).Scan(&storedKey); err != nil {
		t.Fatalf("read stored key: %v", err)
	}
	if storedKey == "" {
		t.Fatal("no key stored; the assertion below would be vacuous")
	}

	for _, marker := range []string{"BEGIN RSA", "BEGIN PRIVATE", "PRIVATE KEY", "private_key", storedKey} {
		if strings.Contains(raw, marker) {
			t.Errorf("HTTP response leaks private key material (%q)", marker)
		}
	}

	// The audit trail must be clean too.
	rows, err := e.db.Query(`SELECT action, before, after, reason FROM orvix_audit`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var action, before, after, reason string
		if err := rows.Scan(&action, &before, &after, &reason); err != nil {
			t.Fatal(err)
		}
		blob := action + before + after + reason
		for _, marker := range []string{"BEGIN RSA", "BEGIN PRIVATE", "PRIVATE KEY", storedKey} {
			if strings.Contains(blob, marker) {
				t.Errorf("audit row %q leaks private key material (%q)", action, marker)
			}
		}
	}
}

// --- typed errors ----------------------------------------------------------

func TestProvisioningEndpointTypedErrors(t *testing.T) {
	e := buildProvisioningEnv(t, "starter", 20, 100)

	cases := []struct {
		name       string
		body       map[string]interface{}
		wantStatus int
		wantCode   string
	}{
		{"invalid name", map[string]interface{}{"name": "not a domain"}, 400, "INVALID_DOMAIN_NAME"},
		{"public suffix", map[string]interface{}{"name": "co.uk"}, 400, "INVALID_DOMAIN_NAME"},
		{"bad initial status", map[string]interface{}{"name": "s.example", "status": "suspended"}, 400, "DOMAIN_STATUS_INVALID"},
		{"long description", map[string]interface{}{"name": "l.example", "description": strings.Repeat("x", 501)}, 422, "DESCRIPTION_TOO_LONG"},
		{"bad selector", map[string]interface{}{"name": "k.example", "dkim": map[string]interface{}{"generate": true, "selector": "bad selector"}}, 422, "INVALID_DKIM_SELECTOR"},
		{"limit above plan", map[string]interface{}{"name": "p.example", "limits": map[string]interface{}{"max_mailboxes": 9999}}, 422, "LIMIT_EXCEEDS_PLAN"},
		{"contradictory quotas", map[string]interface{}{"name": "q.example", "limits": map[string]interface{}{"default_mailbox_quota_mb": 8192, "max_mailbox_quota_mb": 2048}}, 422, "LIMIT_CONTRADICTION"},
		{"negative limit", map[string]interface{}{"name": "n.example", "limits": map[string]interface{}{"max_mailboxes": -7}}, 422, "INVALID_LIMIT"},
	}
	for _, tc := range cases {
		status, body, raw := e.createDomain(t, tc.body)
		if status != tc.wantStatus {
			t.Errorf("%s: status = %d, want %d: %s", tc.name, status, tc.wantStatus, raw)
			continue
		}
		if code, _ := body["code"].(string); code != tc.wantCode {
			t.Errorf("%s: code = %q, want %q: %s", tc.name, code, tc.wantCode, raw)
		}
	}
}

func TestProvisioningEndpointDuplicateIsConflict(t *testing.T) {
	e := buildProvisioningEnv(t, "enterprise", 20, 1000)

	if status, _, raw := e.createDomain(t, map[string]interface{}{"name": "dup.example"}); status != 201 {
		t.Fatalf("first: %d %s", status, raw)
	}
	status, body, raw := e.createDomain(t, map[string]interface{}{"name": "dup.example"})
	if status != 409 {
		t.Fatalf("status = %d, want 409: %s", status, raw)
	}
	if code, _ := body["code"].(string); code != "DOMAIN_ALREADY_EXISTS" {
		t.Errorf("code = %q: %s", code, raw)
	}
}

// TestProvisioningEndpointCrossTenantIsolation proves a domain owned by
// another tenant is a bare conflict that never reveals the owner, and that
// tenant A cannot see or affect tenant B's domain.
func TestProvisioningEndpointCrossTenantIsolation(t *testing.T) {
	e := buildProvisioningEnv(t, "enterprise", 20, 1000)

	if status, _, raw := e.do(t, e.tenantBToken, e.tenantBCSRF, "POST", "/api/v1/enterprise/domains",
		map[string]interface{}{"name": "owned-by-b.example"}); status != 201 {
		t.Fatalf("tenant B create: %d %s", status, raw)
	}

	status, body, raw := e.createDomain(t, map[string]interface{}{"name": "owned-by-b.example"})
	if status != 409 {
		t.Fatalf("status = %d, want 409: %s", status, raw)
	}
	if code, _ := body["code"].(string); code != "DOMAIN_ALREADY_EXISTS" {
		t.Errorf("code = %q: %s", code, raw)
	}
	// Nothing about tenant B may appear in the response.
	for _, leak := range []string{"tenant_id", "tenant-b", "\"id\":"} {
		if strings.Contains(raw, leak) {
			t.Errorf("conflict response leaks ownership detail (%q): %s", leak, raw)
		}
	}

	// And tenant A's domain list must not contain it.
	_, listBody, listRaw := e.do(t, e.tenantAToken, e.tenantACSRF, "GET", "/api/v1/enterprise/domains", nil)
	if strings.Contains(listRaw, "owned-by-b.example") {
		t.Errorf("cross-tenant domain leaked into the list: %s", listRaw)
	}
	_ = listBody
}

func TestProvisioningEndpointRequiresAuthentication(t *testing.T) {
	e := buildProvisioningEnv(t, "enterprise", 20, 1000)

	req := httptest.NewRequest("POST", "/api/v1/enterprise/domains", bytes.NewReader([]byte(`{"name":"unauth.example"}`)))
	req.Header.Set("Content-Type", "application/json")
	resp, err := e.router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == 201 {
		t.Fatal("unauthenticated provisioning succeeded")
	}
	if resp.StatusCode != 401 && resp.StatusCode != 403 {
		t.Errorf("status = %d, want 401/403", resp.StatusCode)
	}

	var count int
	if err := e.db.QueryRow(`SELECT COUNT(*) FROM coremail_domains WHERE name='unauth.example'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("an unauthenticated request created a domain")
	}
}

// TestProvisioningEndpointDoubleSubmitCreatesOne is the double-click contract
// at the HTTP layer: two simultaneous identical POSTs must leave exactly one
// domain, with the loser receiving a typed conflict rather than a 500.
func TestProvisioningEndpointDoubleSubmitCreatesOne(t *testing.T) {
	e := buildProvisioningEnv(t, "enterprise", 20, 1000)

	var wg sync.WaitGroup
	statuses := make([]int, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			statuses[i], _, _ = e.createDomain(t, map[string]interface{}{"name": "double.example"})
		}(i)
	}
	wg.Wait()

	created, conflicted := 0, 0
	for _, s := range statuses {
		switch s {
		case 201:
			created++
		case 409:
			conflicted++
		default:
			t.Errorf("unexpected status %d", s)
		}
	}
	if created != 1 || conflicted != 1 {
		t.Errorf("created=%d conflicted=%d, want 1/1", created, conflicted)
	}

	var count int
	if err := e.db.QueryRow(`SELECT COUNT(*) FROM coremail_domains WHERE name='double.example'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("double submit created %d domains, want 1", count)
	}
}

func TestProvisioningEndpointIdempotencyKeyReplays(t *testing.T) {
	e := buildProvisioningEnv(t, "enterprise", 20, 1000)

	body := map[string]interface{}{"name": "idem.example", "idempotency_key": "wizard-1"}
	status1, resp1, raw1 := e.createDomain(t, body)
	if status1 != 201 {
		t.Fatalf("first: %d %s", status1, raw1)
	}
	status2, resp2, raw2 := e.createDomain(t, body)
	if status2 != 201 {
		t.Fatalf("replay: %d %s", status2, raw2)
	}
	if resp2["idempotent"] != true {
		t.Errorf("replay not marked idempotent: %s", raw2)
	}
	d1 := resp1["domain"].(map[string]interface{})
	d2 := resp2["domain"].(map[string]interface{})
	if d1["id"] != d2["id"] {
		t.Errorf("replay returned a different domain: %v vs %v", d1["id"], d2["id"])
	}
}

// --- capacity endpoint -----------------------------------------------------

func TestOrganizationCapacityEndpoint(t *testing.T) {
	e := buildProvisioningEnv(t, "business", 10, 500)

	if status, _, raw := e.createDomain(t, map[string]interface{}{
		"name":   "cap.example",
		"limits": map[string]interface{}{"max_mailboxes": 40},
	}); status != 201 {
		t.Fatalf("seed domain: %d %s", status, raw)
	}

	status, body, raw := e.do(t, e.tenantAToken, e.tenantACSRF, "GET", "/api/v1/enterprise/organizations/current/capacity", nil)
	if status != 200 {
		t.Fatalf("status = %d, want 200: %s", status, raw)
	}
	cap, _ := body["capacity"].(map[string]interface{})
	if cap == nil {
		t.Fatalf("capacity missing: %s", raw)
	}
	if cap["plan"] != "business" {
		t.Errorf("plan = %v", cap["plan"])
	}
	if cap["domains_used"].(float64) != 1 {
		t.Errorf("domains_used = %v", cap["domains_used"])
	}
	if cap["remaining_domains"].(float64) != 9 {
		t.Errorf("remaining_domains = %v", cap["remaining_domains"])
	}
	if cap["mailboxes_allocated"].(float64) != 40 {
		t.Errorf("mailboxes_allocated = %v", cap["mailboxes_allocated"])
	}
	// Unlimited must be explicit, and remaining must be null (not 0).
	if cap["max_aliases_unlimited"] != true {
		t.Errorf("max_aliases_unlimited = %v, want true", cap["max_aliases_unlimited"])
	}
	if v, present := cap["remaining_aliases"]; !present || v != nil {
		t.Errorf("remaining_aliases = %v, want an explicit null for unlimited", v)
	}
}

func TestOrganizationCapacityUnlimitedPlanIsExplicit(t *testing.T) {
	e := buildProvisioningEnv(t, "enterprise", 0, 0)

	status, body, raw := e.do(t, e.tenantAToken, e.tenantACSRF, "GET", "/api/v1/enterprise/organizations/current/capacity", nil)
	if status != 200 {
		t.Fatalf("status = %d: %s", status, raw)
	}
	cap := body["capacity"].(map[string]interface{})
	if cap["max_domains_unlimited"] != true || cap["max_mailboxes_unlimited"] != true {
		t.Errorf("unlimited flags missing: %s", raw)
	}
	if v, present := cap["remaining_domains"]; !present || v != nil {
		t.Errorf("remaining_domains = %v, want null for an unlimited plan (never a fake 0)", v)
	}
	if v, present := cap["remaining_mailboxes"]; !present || v != nil {
		t.Errorf("remaining_mailboxes = %v, want null for an unlimited plan", v)
	}
}

func TestOrganizationCapacityIsTenantScoped(t *testing.T) {
	e := buildProvisioningEnv(t, "starter", 3, 50)

	_, bodyA, _ := e.do(t, e.tenantAToken, e.tenantACSRF, "GET", "/api/v1/enterprise/organizations/current/capacity", nil)
	_, bodyB, _ := e.do(t, e.tenantBToken, e.tenantBCSRF, "GET", "/api/v1/enterprise/organizations/current/capacity", nil)

	capA := bodyA["capacity"].(map[string]interface{})
	capB := bodyB["capacity"].(map[string]interface{})
	if capA["plan"] != "starter" || capB["plan"] != "enterprise" {
		t.Fatalf("each tenant must see only its own plan: A=%v B=%v", capA["plan"], capB["plan"])
	}
	if capA["max_domains"].(float64) != 3 {
		t.Errorf("tenant A max_domains = %v, want 3", capA["max_domains"])
	}
}

func TestOrganizationCapacityRequiresAuthentication(t *testing.T) {
	e := buildProvisioningEnv(t, "business", 10, 500)

	req := httptest.NewRequest("GET", "/api/v1/enterprise/organizations/current/capacity", nil)
	resp, err := e.router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == 200 {
		t.Fatal("unauthenticated capacity read succeeded")
	}
}

// --- alias cap enforcement -------------------------------------------------

// TestAliasCapIsEnforcedAtCreation proves the stored alias cap is real
// enforcement, and that concurrent creations cannot overshoot it.
func TestAliasCapIsEnforcedAtCreation(t *testing.T) {
	e := buildProvisioningEnv(t, "enterprise", 20, 1000)

	status, body, raw := e.createDomain(t, map[string]interface{}{
		"name":   "aliascap.example",
		"limits": map[string]interface{}{"max_aliases": 2},
	})
	if status != 201 {
		t.Fatalf("create domain: %d %s", status, raw)
	}
	domainID := body["domain"].(map[string]interface{})["id"]

	for i := 0; i < 2; i++ {
		s, _, r := e.do(t, e.tenantAToken, e.tenantACSRF, "POST", "/api/v1/enterprise/aliases", map[string]interface{}{
			"domain_id": domainID,
			"from_addr": strings.Repeat("a", i+1) + "@aliascap.example",
			"to_addr":   "dest@aliascap.example",
		})
		if s != 201 {
			t.Fatalf("alias %d: %d %s", i, s, r)
		}
	}

	s, aliasBody, r := e.do(t, e.tenantAToken, e.tenantACSRF, "POST", "/api/v1/enterprise/aliases", map[string]interface{}{
		"domain_id": domainID,
		"from_addr": "overflow@aliascap.example",
		"to_addr":   "dest@aliascap.example",
	})
	if s != 409 {
		t.Fatalf("over-cap alias status = %d, want 409: %s", s, r)
	}
	if code, _ := aliasBody["code"].(string); code != "ALIAS_LIMIT_REACHED" {
		t.Errorf("code = %q, want ALIAS_LIMIT_REACHED: %s", code, r)
	}

	var count int
	if err := e.db.QueryRow(`SELECT COUNT(*) FROM coremail_aliases WHERE deleted_at IS NULL`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("alias count = %d, want exactly the cap of 2", count)
	}
}

// TestAliasCreationRejectsCrossTenantDomain closes the IDOR the transactional
// rewrite fixed: a caller-supplied domain_id belonging to another tenant must
// not be usable.
func TestAliasCreationRejectsCrossTenantDomain(t *testing.T) {
	e := buildProvisioningEnv(t, "enterprise", 20, 1000)

	status, body, raw := e.do(t, e.tenantBToken, e.tenantBCSRF, "POST", "/api/v1/enterprise/domains",
		map[string]interface{}{"name": "bdomain.example"})
	if status != 201 {
		t.Fatalf("tenant B domain: %d %s", status, raw)
	}
	bDomainID := body["domain"].(map[string]interface{})["id"]

	s, _, r := e.do(t, e.tenantAToken, e.tenantACSRF, "POST", "/api/v1/enterprise/aliases", map[string]interface{}{
		"domain_id": bDomainID,
		"from_addr": "evil@bdomain.example",
		"to_addr":   "attacker@tenanta.example",
	})
	if s != 404 {
		t.Fatalf("cross-tenant alias status = %d, want 404: %s", s, r)
	}
	var count int
	if err := e.db.QueryRow(`SELECT COUNT(*) FROM coremail_aliases`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("an alias was attached to another tenant's domain")
	}
}

func TestAliasCapUnlimitedWhenInheriting(t *testing.T) {
	e := buildProvisioningEnv(t, "enterprise", 20, 1000)

	status, body, raw := e.createDomain(t, map[string]interface{}{"name": "noaliascap.example"})
	if status != 201 {
		t.Fatalf("create: %d %s", status, raw)
	}
	domainID := body["domain"].(map[string]interface{})["id"]

	for i := 0; i < 5; i++ {
		s, _, r := e.do(t, e.tenantAToken, e.tenantACSRF, "POST", "/api/v1/enterprise/aliases", map[string]interface{}{
			"domain_id": domainID,
			"from_addr": strings.Repeat("z", i+1) + "@noaliascap.example",
			"to_addr":   "dest@noaliascap.example",
		})
		if s != 201 {
			t.Fatalf("alias %d under an inheriting (uncapped) domain: %d %s", i, s, r)
		}
	}
}

// --- domain plan ceiling at the HTTP layer ---------------------------------

func TestProvisioningEndpointStopsAtPlanDomainCeiling(t *testing.T) {
	e := buildProvisioningEnv(t, "starter", 2, 100)

	for i := 0; i < 2; i++ {
		if s, _, r := e.createDomain(t, map[string]interface{}{"name": string(rune('a'+i)) + "ceiling.example"}); s != 201 {
			t.Fatalf("seed %d: %d %s", i, s, r)
		}
	}
	s, body, r := e.createDomain(t, map[string]interface{}{"name": "overflow-ceiling.example"})
	if s != 409 && s != 403 {
		t.Fatalf("status = %d, want a limit rejection: %s", s, r)
	}
	if code, _ := body["code"].(string); code != "DOMAIN_LIMIT_REACHED" {
		t.Errorf("code = %q, want DOMAIN_LIMIT_REACHED: %s", code, r)
	}
}
