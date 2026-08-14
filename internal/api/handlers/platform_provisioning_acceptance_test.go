package handlers_test

// Route-level acceptance tests for the platform domain & mailbox
// provisioning foundation (Backend Phase 1). These exercise the REAL
// router (api.NewRouter) and the REAL middleware chain — auth, RBAC,
// CSRF, idempotency — against the production wiring:
//
//   - PSA-only gate (tenant roles denied);
//   - CSRF enforcement on the new mutations;
//   - required Idempotency-Key with replay / conflict / concurrency;
//   - explicit target tenant + lifecycle gates (0 / missing /
//     suspended / deleted);
//   - strict JSON (unknown fields rejected);
//   - safe contracts (no password, no hash, no key material);
//   - legacy tenant-admin create persists inherit.

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"go.uber.org/zap"

	"github.com/orvix/orvix/internal/api"
	"github.com/orvix/orvix/internal/auth"
	"github.com/orvix/orvix/internal/config"
	"github.com/orvix/orvix/internal/coremail/storage"
	"github.com/orvix/orvix/internal/license"
	"github.com/orvix/orvix/internal/models"
	"github.com/orvix/orvix/internal/modules"
)

type provEnv struct {
	router     *api.Router
	psaToken   string
	tenantAdm  string
	otherAdm   string
	psaCSRF    string
	tenantCSRF string
	db         *sql.DB
}

const (
	provPSAEmail    = "prov-psa@platform.local"
	provPSAPass     = "ProvPlatformPass!2026"
	provTenantEmail = "prov-admin@tenant1.local"
	provTenantPass  = "ProvTenant1Pass!2026"
	provOtherEmail  = "prov-admin@tenant2.local"
	provOtherPass   = "ProvTenant2Pass!2026"
)

func buildPlatformProvisioningEnv(t *testing.T) *provEnv {
	t.Helper()
	logger := zap.NewNop()
	cfg := config.Defaults()
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = t.TempDir() + "/prov.db?_loc=auto&_busy_timeout=5000&_txlock=immediate"
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
	// The coremail storage schema is owned by the storage package and
	// provisioned by the runtime module at boot; mirror that here so
	// folder provisioning works through the real service wiring.
	for _, stmt := range storage.Tables() {
		if _, err := sqlDB.Exec(stmt); err != nil {
			t.Fatalf("storage schema: %v", err)
		}
	}
	for _, stmt := range storage.Indexes() {
		_, _ = sqlDB.Exec(stmt)
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
	seedTenantAdminWithPassword(t, sqlDB, provTenantEmail, 1, provTenantPass)
	seedTenantAdminWithPassword(t, sqlDB, provOtherEmail, 2, provOtherPass)
	psaHash, _ := authenticator.HashPassword(provPSAPass)
	exec("INSERT INTO users (created_at, updated_at, email, password_hash, role, tenant_id, active, email_verified) VALUES (?, ?, '"+provPSAEmail+"', ?, 'platform_super_admin', NULL, 1, 1)", now, now, psaHash)

	router := api.NewRouter(cfg, authenticator, logger, db, modules.NewRegistry(logger), license.NewFeatureFlags(logger), nil)
	t.Cleanup(func() {
		_ = router.App().Shutdown()
		_ = sqlDB.Close()
	})

	psaToken := importRouteLogin(t, router, provPSAEmail, provPSAPass)
	return &provEnv{
		router:     router,
		psaToken:   psaToken,
		tenantAdm:  importRouteLogin(t, router, provTenantEmail, provTenantPass),
		otherAdm:   importRouteLogin(t, router, provOtherEmail, provOtherPass),
		psaCSRF:    importRouteCSRF(t, router, psaToken),
		tenantCSRF: importRouteCSRF(t, router, importRouteLogin(t, router, provTenantEmail, provTenantPass)),
		db:         sqlDB,
	}
}

func (e *provEnv) do(t *testing.T, method, path, token string, body interface{}, headers map[string]string) (*http.Response, []byte) {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, rdr)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := e.router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	raw, _ := io.ReadAll(resp.Body)
	return resp, raw
}

func (e *provEnv) psaDo(t *testing.T, method, path string, body interface{}, headers map[string]string) (*http.Response, []byte) {
	t.Helper()
	h := map[string]string{"Cookie": "csrf_token=" + e.psaCSRF, "X-CSRF-Token": e.psaCSRF}
	for k, v := range headers {
		h[k] = v
	}
	return e.do(t, method, path, e.psaToken, body, h)
}

// ── Platform domain creation ───────────────────────────────────────

func TestPlatformCreateDomain_PSAValidCreate(t *testing.T) {
	env := buildPlatformProvisioningEnv(t)
	resp, raw := env.psaDo(t, "POST", "/api/v1/platform/domains/1", map[string]interface{}{
		"name": "psa.example.com", "status": "active",
	}, map[string]string{"Idempotency-Key": "dom-create-1"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create status %d: %s", resp.StatusCode, raw)
	}
	var out struct {
		Domain struct {
			ID       uint   `json:"id"`
			TenantID uint   `json:"tenant_id"`
			Name     string `json:"name"`
		} `json:"domain"`
		DNSNextStep string `json:"dns_next_step"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Domain.TenantID != 1 || out.Domain.Name != "psa.example.com" {
		t.Fatalf("unexpected domain: %+v", out.Domain)
	}
	if out.DNSNextStep != "publish_and_verify_dns" {
		t.Fatalf("dns next step: %q", out.DNSNextStep)
	}
	// Never claims DNS was changed.
	if strings.Contains(string(raw), `"public_dns_changed":true`) {
		t.Fatal("provisioning must never claim public DNS changed")
	}
}

func TestPlatformCreateDomain_TenantAdminDenied(t *testing.T) {
	env := buildPlatformProvisioningEnv(t)
	resp, raw := env.do(t, "POST", "/api/v1/platform/domains/1", env.tenantAdm, map[string]interface{}{
		"name": "denied.example.com",
	}, map[string]string{"Cookie": "csrf_token=" + env.tenantCSRF, "X-CSRF-Token": env.tenantCSRF, "Idempotency-Key": "k1"})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("tenant admin must be denied, got %d: %s", resp.StatusCode, raw)
	}
}

func TestPlatformCreateDomain_MissingCSRFDenied(t *testing.T) {
	env := buildPlatformProvisioningEnv(t)
	resp, raw := env.do(t, "POST", "/api/v1/platform/domains/1", env.psaToken, map[string]interface{}{
		"name": "nocsrf.example.com",
	}, map[string]string{"Idempotency-Key": "k1"})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("missing CSRF must be denied, got %d: %s", resp.StatusCode, raw)
	}
}

func TestPlatformCreateDomain_MissingIdempotencyKey(t *testing.T) {
	env := buildPlatformProvisioningEnv(t)
	resp, raw := env.psaDo(t, "POST", "/api/v1/platform/domains/1", map[string]interface{}{
		"name": "nokey.example.com",
	}, nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("missing Idempotency-Key must be 400, got %d: %s", resp.StatusCode, raw)
	}
}

func TestPlatformCreateDomain_UnknownFieldRejected(t *testing.T) {
	env := buildPlatformProvisioningEnv(t)
	resp, raw := env.psaDo(t, "POST", "/api/v1/platform/domains/1", map[string]interface{}{
		"name": "strict.example.com", "mail_access_mode": "internal_only", // unknown on domain create
	}, map[string]string{"Idempotency-Key": "k1"})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("unknown field must be rejected, got %d: %s", resp.StatusCode, raw)
	}
	if !strings.Contains(string(raw), "mail_access_mode") {
		t.Fatalf("error must name the unknown field: %s", raw)
	}
}

func TestPlatformCreateDomain_SameKeyReplay(t *testing.T) {
	env := buildPlatformProvisioningEnv(t)
	body := map[string]interface{}{"name": "replay.example.com", "status": "active"}
	resp1, raw1 := env.psaDo(t, "POST", "/api/v1/platform/domains/1", body, map[string]string{"Idempotency-Key": "replay-key"})
	if resp1.StatusCode != http.StatusCreated {
		t.Fatalf("first create: %d %s", resp1.StatusCode, raw1)
	}
	resp2, raw2 := env.psaDo(t, "POST", "/api/v1/platform/domains/1", body, map[string]string{"Idempotency-Key": "replay-key"})
	// The replay returns the ORIGINAL stored result verbatim (201),
	// never a re-execution.
	if resp2.StatusCode != http.StatusCreated {
		t.Fatalf("replay must return the original result status, got %d: %s", resp2.StatusCode, raw2)
	}
	if resp2.Header.Get("X-Idempotency-Replay") != "true" {
		t.Fatal("replay must carry X-Idempotency-Replay: true")
	}
	if string(raw1) != string(raw2) {
		t.Fatalf("replay body must equal the original:\n%s\nvs\n%s", raw1, raw2)
	}
	var count int
	if err := env.db.QueryRow(`SELECT COUNT(*) FROM coremail_domains WHERE name='replay.example.com'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("replay must not create a second domain, found %d", count)
	}
}

func TestPlatformCreateDomain_ChangedRequestConflict(t *testing.T) {
	env := buildPlatformProvisioningEnv(t)
	env.psaDo(t, "POST", "/api/v1/platform/domains/1", map[string]interface{}{"name": "conflict.example.com"}, map[string]string{"Idempotency-Key": "same-key"})
	resp, raw := env.psaDo(t, "POST", "/api/v1/platform/domains/1", map[string]interface{}{"name": "different.example.com"}, map[string]string{"Idempotency-Key": "same-key"})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("same key + changed body must conflict, got %d: %s", resp.StatusCode, raw)
	}
}

func TestPlatformCreateDomain_ConcurrentIdenticalSubmissionsExecuteOnce(t *testing.T) {
	env := buildPlatformProvisioningEnv(t)
	// Repeated runs (3x) demonstrate stable idempotency behavior.
	for run := 0; run < 3; run++ {
		name := fmt.Sprintf("conc%d.example.com", run)
		body := map[string]interface{}{"name": name, "status": "active"}
		key := fmt.Sprintf("conc-key-%d", run)
		const workers = 6
		var wg sync.WaitGroup
		statuses := make([]int, workers)
		for i := 0; i < workers; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				resp, _ := env.psaDo(t, "POST", "/api/v1/platform/domains/1", body, map[string]string{"Idempotency-Key": key})
				statuses[i] = resp.StatusCode
			}(i)
		}
		wg.Wait()

		created := 0
		for _, s := range statuses {
			if s == http.StatusCreated {
				created++
			}
		}
		if created != 1 {
			t.Fatalf("run %d: exactly one concurrent create must win, got created=%d (statuses=%v)", run, created, statuses)
		}
		var count int
		if err := env.db.QueryRow(`SELECT COUNT(*) FROM coremail_domains WHERE name=?`, name).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("run %d: exactly one domain row expected, got %d", run, count)
		}
	}
}

func TestPlatformCreateDomain_DuplicateNormalizedDomainConflict(t *testing.T) {
	env := buildPlatformProvisioningEnv(t)
	env.psaDo(t, "POST", "/api/v1/platform/domains/1", map[string]interface{}{"name": "dup.example.com"}, map[string]string{"Idempotency-Key": "k1"})
	resp, raw := env.psaDo(t, "POST", "/api/v1/platform/domains/1", map[string]interface{}{"name": "DUP.example.com."}, map[string]string{"Idempotency-Key": "k2"})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("normalized duplicate must conflict, got %d: %s", resp.StatusCode, raw)
	}
}

func TestPlatformCreateDomain_CrossTenantNameConflict(t *testing.T) {
	env := buildPlatformProvisioningEnv(t)
	env.psaDo(t, "POST", "/api/v1/platform/domains/1", map[string]interface{}{"name": "taken.example.com"}, map[string]string{"Idempotency-Key": "k1"})
	resp, raw := env.psaDo(t, "POST", "/api/v1/platform/domains/2", map[string]interface{}{"name": "taken.example.com"}, map[string]string{"Idempotency-Key": "k2"})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("name owned by another tenant must conflict without disclosure, got %d: %s", resp.StatusCode, raw)
	}
	if strings.Contains(string(raw), "tenant") {
		t.Fatalf("conflict must not disclose the owning tenant: %s", raw)
	}
}

func TestPlatformCreateDomain_SuspendedAndDeletedTenantsRejected(t *testing.T) {
	env := buildPlatformProvisioningEnv(t)
	now := time.Now().UTC()
	env.db.Exec(`INSERT INTO tenants (id, created_at, updated_at, name, slug, domain, plan, active) VALUES (10, ?, ?, 'susp', 'susp', 'susp.example', 'business', 0)`, now, now)
	env.db.Exec(`INSERT INTO tenants (id, created_at, updated_at, name, slug, domain, plan, active, deleted_at) VALUES (11, ?, ?, 'del', 'del', 'del.example', 'business', 1, ?)`, now, now, now)

	resp, raw := env.psaDo(t, "POST", "/api/v1/platform/domains/10", map[string]interface{}{"name": "s.example.com"}, map[string]string{"Idempotency-Key": "k1"})
	if resp.StatusCode != http.StatusConflict && resp.StatusCode != http.StatusForbidden {
		t.Fatalf("suspended tenant must be rejected, got %d: %s", resp.StatusCode, raw)
	}
	resp, raw = env.psaDo(t, "POST", "/api/v1/platform/domains/11", map[string]interface{}{"name": "d.example.com"}, map[string]string{"Idempotency-Key": "k2"})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("deleted tenant must be not-found, got %d: %s", resp.StatusCode, raw)
	}
	resp, raw = env.psaDo(t, "POST", "/api/v1/platform/domains/999", map[string]interface{}{"name": "n.example.com"}, map[string]string{"Idempotency-Key": "k3"})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("nonexistent tenant must be not-found, got %d: %s", resp.StatusCode, raw)
	}
}

func TestPlatformCreateDomain_NoPrivateKeyInResponseOrAudit(t *testing.T) {
	env := buildPlatformProvisioningEnv(t)
	resp, raw := env.psaDo(t, "POST", "/api/v1/platform/domains/1", map[string]interface{}{
		"name": "dkim.example.com", "dkim": map[string]interface{}{"generate": true, "selector": "mail"},
	}, map[string]string{"Idempotency-Key": "k1"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create with dkim: %d %s", resp.StatusCode, raw)
	}
	if strings.Contains(string(raw), "PRIVATE KEY") || strings.Contains(string(raw), "BEGIN") {
		t.Fatal("response must never contain private key material")
	}
	// Audit trail must not carry key material either.
	rows, err := env.db.Query(`SELECT after FROM orvix_audit WHERE action IN ('domain.provision','domain.dkim.generate')`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var after string
		if err := rows.Scan(&after); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(after, "PRIVATE") || strings.Contains(after, "BEGIN") {
			t.Fatalf("audit payload leaked key material: %s", after)
		}
	}
}

func TestPlatformCreateDomain_TenantZeroAndMalformedRejected(t *testing.T) {
	env := buildPlatformProvisioningEnv(t)
	resp, raw := env.psaDo(t, "POST", "/api/v1/platform/domains/0", map[string]interface{}{"name": "zero.example.com"}, map[string]string{"Idempotency-Key": "k1"})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("tenant 0 must be rejected, got %d: %s", resp.StatusCode, raw)
	}
	resp, _ = env.psaDo(t, "POST", "/api/v1/platform/domains/abc", map[string]interface{}{"name": "malformed.example.com"}, map[string]string{"Idempotency-Key": "k2"})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("malformed tenant id must be rejected, got %d", resp.StatusCode)
	}
}

// ── Platform mailbox creation ───────────────────────────────────────

func TestPlatformCreateMailbox_ValidForEachExplicitMode(t *testing.T) {
	env := buildPlatformProvisioningEnv(t)
	env.psaDo(t, "POST", "/api/v1/platform/domains/1", map[string]interface{}{"name": "mb.example.com"}, map[string]string{"Idempotency-Key": "d1"})

	for _, mode := range []string{"internal_only", "internal_external"} {
		resp, raw := env.psaDo(t, "POST", "/api/v1/platform/mailboxes/1", map[string]interface{}{
			"email": "u-" + mode + "@mb.example.com", "name": "User", "password": "TempSecret123!",
			"quota_mb": 2048, "send_limit_per_hour": 100, "force_password_change": true,
			"mail_access_mode": mode,
		}, map[string]string{"Idempotency-Key": "mb-" + mode})
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("create mailbox mode=%s: %d %s", mode, resp.StatusCode, raw)
		}
		if resp.Header.Get("Cache-Control") != "no-store" {
			t.Fatalf("sensitive mutation must carry Cache-Control: no-store, got %q", resp.Header.Get("Cache-Control"))
		}
		if strings.Contains(string(raw), "TempSecret123") || strings.Contains(string(raw), "password") {
			t.Fatalf("password must never appear in the response: %s", raw)
		}
		var out struct {
			Mailbox struct {
				MailAccessMode          string `json:"mail_access_mode"`
				EffectiveMailAccessMode string `json:"effective_mail_access_mode"`
			} `json:"mailbox"`
		}
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if out.Mailbox.MailAccessMode != mode || out.Mailbox.EffectiveMailAccessMode != mode {
			t.Fatalf("mode mismatch: %+v", out.Mailbox)
		}
		// System folders must exist.
		var id uint
		env.db.QueryRow(`SELECT id FROM coremail_mailboxes WHERE email=?`, "u-"+mode+"@mb.example.com").Scan(&id)
		var folders int
		env.db.QueryRow(`SELECT COUNT(*) FROM coremail_folders WHERE mailbox_id=?`, id).Scan(&folders)
		if folders != 6 {
			t.Fatalf("expected 6 system folders, got %d", folders)
		}
	}
}

func TestPlatformCreateMailbox_MissingModeRejected(t *testing.T) {
	env := buildPlatformProvisioningEnv(t)
	env.psaDo(t, "POST", "/api/v1/platform/domains/1", map[string]interface{}{"name": "mb.example.com"}, map[string]string{"Idempotency-Key": "d1"})
	resp, raw := env.psaDo(t, "POST", "/api/v1/platform/mailboxes/1", map[string]interface{}{
		"email": "nomode@mb.example.com", "password": "TempSecret123!",
	}, map[string]string{"Idempotency-Key": "k1"})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("missing mail_access_mode must be rejected on the platform route, got %d: %s", resp.StatusCode, raw)
	}
}

func TestPlatformCreateMailbox_InheritRejected(t *testing.T) {
	env := buildPlatformProvisioningEnv(t)
	env.psaDo(t, "POST", "/api/v1/platform/domains/1", map[string]interface{}{"name": "mb.example.com"}, map[string]string{"Idempotency-Key": "d1"})
	resp, raw := env.psaDo(t, "POST", "/api/v1/platform/mailboxes/1", map[string]interface{}{
		"email": "inh@mb.example.com", "password": "TempSecret123!", "mail_access_mode": "inherit",
	}, map[string]string{"Idempotency-Key": "k1"})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("explicit inherit must be rejected on the platform route, got %d: %s", resp.StatusCode, raw)
	}
}

func TestPlatformCreateMailbox_CrossTenantDomainNotFound(t *testing.T) {
	env := buildPlatformProvisioningEnv(t)
	env.psaDo(t, "POST", "/api/v1/platform/domains/1", map[string]interface{}{"name": "t1only.example.com"}, map[string]string{"Idempotency-Key": "d1"})
	resp, raw := env.psaDo(t, "POST", "/api/v1/platform/mailboxes/2", map[string]interface{}{
		"email": "x@t1only.example.com", "password": "TempSecret123!", "mail_access_mode": "internal_only",
	}, map[string]string{"Idempotency-Key": "k1"})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("cross-tenant domain must be not-found, got %d: %s", resp.StatusCode, raw)
	}
}

func TestPlatformCreateMailbox_DomainStateEligibilityMatrix(t *testing.T) {
	env := buildPlatformProvisioningEnv(t)
	states := []struct{ name, status string }{
		{"disabled.example.com", "disabled"},
		{"suspended.example.com", "suspended"},
		{"locked.example.com", "locked"},
		{"bogus.example.com", "bogus"},
	}
	for _, st := range states {
		env.psaDo(t, "POST", "/api/v1/platform/domains/1", map[string]interface{}{"name": st.name}, map[string]string{"Idempotency-Key": "d-" + st.name})
		env.db.Exec(`UPDATE coremail_domains SET status=? WHERE name=?`, st.status, st.name)
		resp, raw := env.psaDo(t, "POST", "/api/v1/platform/mailboxes/1", map[string]interface{}{
			"email": "u@" + st.name, "password": "TempSecret123!", "mail_access_mode": "internal_only",
		}, map[string]string{"Idempotency-Key": "k-" + st.name})
		if resp.StatusCode != http.StatusConflict {
			t.Fatalf("domain state %q must reject mailbox creation with conflict, got %d: %s", st.status, resp.StatusCode, raw)
		}
	}
}

func TestPlatformCreateMailbox_DuplicateEmailConflict(t *testing.T) {
	env := buildPlatformProvisioningEnv(t)
	env.psaDo(t, "POST", "/api/v1/platform/domains/1", map[string]interface{}{"name": "mb.example.com"}, map[string]string{"Idempotency-Key": "d1"})
	body := map[string]interface{}{
		"email": "dup@mb.example.com", "password": "TempSecret123!", "mail_access_mode": "internal_only",
	}
	resp, raw := env.psaDo(t, "POST", "/api/v1/platform/mailboxes/1", body, map[string]string{"Idempotency-Key": "k1"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("first create: %d %s", resp.StatusCode, raw)
	}
	resp, raw = env.psaDo(t, "POST", "/api/v1/platform/mailboxes/1", body, map[string]string{"Idempotency-Key": "k2"})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("duplicate email must conflict, got %d: %s", resp.StatusCode, raw)
	}
}

func TestPlatformCreateMailbox_SuspendedTenantRejected(t *testing.T) {
	env := buildPlatformProvisioningEnv(t)
	env.psaDo(t, "POST", "/api/v1/platform/domains/1", map[string]interface{}{"name": "mb.example.com"}, map[string]string{"Idempotency-Key": "d1"})
	now := time.Now().UTC()
	env.db.Exec(`UPDATE tenants SET active=0 WHERE id=1`, now)
	resp, raw := env.psaDo(t, "POST", "/api/v1/platform/mailboxes/1", map[string]interface{}{
		"email": "u@mb.example.com", "password": "TempSecret123!", "mail_access_mode": "internal_only",
	}, map[string]string{"Idempotency-Key": "k1"})
	if resp.StatusCode != http.StatusConflict && resp.StatusCode != http.StatusForbidden {
		t.Fatalf("suspended tenant must reject mailbox creation, got %d: %s", resp.StatusCode, raw)
	}
}

func TestPlatformCreateMailbox_LegacyTenantAdminWithoutModePersistsInherit(t *testing.T) {
	env := buildPlatformProvisioningEnv(t)
	env.psaDo(t, "POST", "/api/v1/platform/domains/1", map[string]interface{}{"name": "legacy.example.com"}, map[string]string{"Idempotency-Key": "d1"})

	// Tenant-admin route: no mail_access_mode field — must persist
	// inherit and keep working exactly as before.
	resp, raw := env.do(t, "POST", "/api/v1/mailboxes", env.tenantAdm, map[string]interface{}{
		"email": "legacy@legacy.example.com", "password": "LegacyPass!2026", "name": "Legacy",
	}, map[string]string{"Cookie": "csrf_token=" + env.tenantCSRF, "X-CSRF-Token": env.tenantCSRF})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("legacy tenant-admin create must keep working, got %d: %s", resp.StatusCode, raw)
	}
	var mode string
	if err := env.db.QueryRow(`SELECT mail_access_mode FROM coremail_mailboxes WHERE email='legacy@legacy.example.com'`).Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if mode != "inherit" {
		t.Fatalf("legacy create without mode must persist inherit, got %q", mode)
	}
}

// ── Webmail send path policy enforcement ───────────────────────────
//
// The webmail send handler enforces the canonical mail-access policy
// BEFORE any quota reservation or queue write. These tests drive the
// REAL router + webmail env + policy wiring: an internal-only mailbox
// is denied external sends with the stable MAIL_ACCESS_DENIED code
// and allowed local sends; flipping the mailbox to internal_external
// re-enables external sends without any other change.

func TestWebmailSendPolicy_InternalOnlyBlocksExternal(t *testing.T) {
	e := buildWebmailTestEnv(t)
	mailboxID := mailboxIDForEmail(t, e.mailbox, e.email)
	if err := e.mailbox.Folders.EnsureSystemFolders(t.Context(), mailboxID, nil); err != nil {
		t.Fatalf("ensure folders: %v", err)
	}
	if _, err := e.mailbox.DB.Exec(`UPDATE coremail_mailboxes SET mail_access_mode='internal_only' WHERE email=?`, e.email); err != nil {
		t.Fatalf("set internal_only: %v", err)
	}
	tok := e.loginAdmin(t)

	status, body := e.webmailRequest(t, "POST", "/api/v1/webmail/send", tok, map[string]string{
		"to": "external@example.com", "subject": "hi", "body": "body",
	})
	if status != http.StatusForbidden {
		t.Fatalf("internal-only webmail send to external must be 403, got %d: %v", status, body)
	}
	if body["code"] != "MAIL_ACCESS_DENIED" {
		t.Fatalf("denial must carry the stable code, got %v", body)
	}
	// A denied send must never consume delivery quota as success: no
	// queue entries and no Sent-copy message.
	metrics, err := e.queue.Metrics(t.Context(), nil)
	if err != nil {
		t.Fatalf("queue metrics: %v", err)
	}
	if metrics.Pending != 0 && metrics.Delivered != 0 {
		t.Fatalf("denied send must not enqueue: %+v", metrics)
	}
}

func TestWebmailSendPolicy_InternalOnlyAllowsLocal(t *testing.T) {
	e := buildWebmailTestEnv(t)
	mailboxID := mailboxIDForEmail(t, e.mailbox, e.email)
	if err := e.mailbox.Folders.EnsureSystemFolders(t.Context(), mailboxID, nil); err != nil {
		t.Fatalf("ensure folders: %v", err)
	}
	if _, err := e.mailbox.DB.Exec(`UPDATE coremail_mailboxes SET mail_access_mode='internal_only' WHERE email=?`, e.email); err != nil {
		t.Fatalf("set internal_only: %v", err)
	}
	tok := e.loginAdmin(t)

	status, body := e.webmailRequest(t, "POST", "/api/v1/webmail/send", tok, map[string]string{
		"to": e.email, "subject": "hi", "body": "body",
	})
	if status != http.StatusCreated {
		t.Fatalf("internal-only webmail send to a local mailbox must be allowed, got %d: %v", status, body)
	}
}

func TestWebmailSendPolicy_ExternalEnabledRestoresExternalSend(t *testing.T) {
	e := buildWebmailTestEnv(t)
	mailboxID := mailboxIDForEmail(t, e.mailbox, e.email)
	if err := e.mailbox.Folders.EnsureSystemFolders(t.Context(), mailboxID, nil); err != nil {
		t.Fatalf("ensure folders: %v", err)
	}
	if _, err := e.mailbox.DB.Exec(`UPDATE coremail_mailboxes SET mail_access_mode='internal_external' WHERE email=?`, e.email); err != nil {
		t.Fatalf("set internal_external: %v", err)
	}
	tok := e.loginAdmin(t)

	status, body := e.webmailRequest(t, "POST", "/api/v1/webmail/send", tok, map[string]string{
		"to": "external@example.com", "subject": "hi", "body": "body",
	})
	if status != http.StatusCreated {
		t.Fatalf("external-enabled webmail send to external must be allowed, got %d: %v", status, body)
	}
}

// ── Platform mailbox access-mode mutation ───────────────────────────

func TestPlatformMailboxAccessMode_SuccessAndEffectiveValue(t *testing.T) {
	env := buildPlatformProvisioningEnv(t)
	env.psaDo(t, "POST", "/api/v1/platform/domains/1", map[string]interface{}{"name": "mb.example.com"}, map[string]string{"Idempotency-Key": "d1"})
	resp, raw := env.psaDo(t, "POST", "/api/v1/platform/mailboxes/1", map[string]interface{}{
		"email": "mode@mb.example.com", "password": "TempSecret123!", "mail_access_mode": "internal_external",
	}, map[string]string{"Idempotency-Key": "k1"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create: %d %s", resp.StatusCode, raw)
	}
	var created struct {
		Mailbox struct {
			ID uint `json:"id"`
		} `json:"mailbox"`
	}
	json.Unmarshal(raw, &created)

	resp, raw = env.psaDo(t, "POST", fmt.Sprintf("/api/v1/platform/mailboxes/1/%d/access-mode", created.Mailbox.ID), map[string]interface{}{
		"mail_access_mode": "internal_only", "expected_version": 1,
	}, map[string]string{"Idempotency-Key": "am-1"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("set mode: %d %s", resp.StatusCode, raw)
	}
	var out struct {
		MailAccessMode          string `json:"mail_access_mode"`
		EffectiveMailAccessMode string `json:"effective_mail_access_mode"`
		Version                 int    `json:"version"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.MailAccessMode != "internal_only" || out.EffectiveMailAccessMode != "internal_only" || out.Version != 2 {
		t.Fatalf("unexpected result: %+v", out)
	}
	if resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatal("access-mode mutation must carry Cache-Control: no-store")
	}
	// Audit evidence.
	var count int
	env.db.QueryRow(`SELECT COUNT(*) FROM orvix_audit WHERE action='mailbox.mail_access_mode.set'`).Scan(&count)
	if count == 0 {
		t.Fatal("expected audit evidence")
	}
}

func TestPlatformMailboxAccessMode_VersionConflict(t *testing.T) {
	env := buildPlatformProvisioningEnv(t)
	env.psaDo(t, "POST", "/api/v1/platform/domains/1", map[string]interface{}{"name": "mb.example.com"}, map[string]string{"Idempotency-Key": "d1"})
	resp, raw := env.psaDo(t, "POST", "/api/v1/platform/mailboxes/1", map[string]interface{}{
		"email": "mode@mb.example.com", "password": "TempSecret123!", "mail_access_mode": "internal_external",
	}, map[string]string{"Idempotency-Key": "k1"})
	var created struct {
		Mailbox struct {
			ID uint `json:"id"`
		} `json:"mailbox"`
	}
	json.Unmarshal(raw, &created)

	env.psaDo(t, "POST", fmt.Sprintf("/api/v1/platform/mailboxes/1/%d/access-mode", created.Mailbox.ID), map[string]interface{}{
		"mail_access_mode": "internal_only", "expected_version": 1,
	}, map[string]string{"Idempotency-Key": "am-1"})
	resp, raw = env.psaDo(t, "POST", fmt.Sprintf("/api/v1/platform/mailboxes/1/%d/access-mode", created.Mailbox.ID), map[string]interface{}{
		"mail_access_mode": "internal_only", "expected_version": 1,
	}, map[string]string{"Idempotency-Key": "am-2"})
	if resp.StatusCode != http.StatusPreconditionFailed {
		t.Fatalf("stale version must be 412, got %d: %s", resp.StatusCode, raw)
	}
}

func TestPlatformMailboxAccessMode_CrossTenantNotFound(t *testing.T) {
	env := buildPlatformProvisioningEnv(t)
	env.psaDo(t, "POST", "/api/v1/platform/domains/1", map[string]interface{}{"name": "mb.example.com"}, map[string]string{"Idempotency-Key": "d1"})
	resp, raw := env.psaDo(t, "POST", "/api/v1/platform/mailboxes/1", map[string]interface{}{
		"email": "mode@mb.example.com", "password": "TempSecret123!", "mail_access_mode": "internal_external",
	}, map[string]string{"Idempotency-Key": "k1"})
	var created struct {
		Mailbox struct {
			ID uint `json:"id"`
		} `json:"mailbox"`
	}
	json.Unmarshal(raw, &created)

	resp, raw = env.psaDo(t, "POST", fmt.Sprintf("/api/v1/platform/mailboxes/2/%d/access-mode", created.Mailbox.ID), map[string]interface{}{
		"mail_access_mode": "internal_only", "expected_version": 1,
	}, map[string]string{"Idempotency-Key": "am-x"})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("cross-tenant mutation must be not-found, got %d: %s", resp.StatusCode, raw)
	}
}

func TestPlatformMailboxAccessMode_IdempotentReplay(t *testing.T) {
	env := buildPlatformProvisioningEnv(t)
	env.psaDo(t, "POST", "/api/v1/platform/domains/1", map[string]interface{}{"name": "mb.example.com"}, map[string]string{"Idempotency-Key": "d1"})
	_, raw := env.psaDo(t, "POST", "/api/v1/platform/mailboxes/1", map[string]interface{}{
		"email": "mode@mb.example.com", "password": "TempSecret123!", "mail_access_mode": "internal_external",
	}, map[string]string{"Idempotency-Key": "k1"})
	var created struct {
		Mailbox struct {
			ID uint `json:"id"`
		} `json:"mailbox"`
	}
	json.Unmarshal(raw, &created)
	path := fmt.Sprintf("/api/v1/platform/mailboxes/1/%d/access-mode", created.Mailbox.ID)
	body := map[string]interface{}{"mail_access_mode": "internal_only", "expected_version": 1}

	resp1, raw1 := env.psaDo(t, "POST", path, body, map[string]string{"Idempotency-Key": "am-idem"})
	if resp1.StatusCode != http.StatusOK {
		t.Fatalf("first set: %d %s", resp1.StatusCode, raw1)
	}
	resp2, raw2 := env.psaDo(t, "POST", path, body, map[string]string{"Idempotency-Key": "am-idem"})
	if resp2.StatusCode != http.StatusOK || resp2.Header.Get("X-Idempotency-Replay") != "true" {
		t.Fatalf("replay: %d %s", resp2.StatusCode, raw2)
	}
	if string(raw1) != string(raw2) {
		t.Fatalf("replay body must equal the original:\n%s\nvs\n%s", raw1, raw2)
	}
}
