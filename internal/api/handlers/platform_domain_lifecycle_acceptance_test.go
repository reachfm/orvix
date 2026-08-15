package handlers_test

// Acceptance tests for POST /api/v1/platform/domains/:tenant_id/:id/deactivate
// (Phase 8 production-acceptance remediation, item 3: canonical, audited
// platform domain deactivation/soft-delete lifecycle).

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"go.uber.org/zap"

	"github.com/orvix/orvix/internal/api"
	"github.com/orvix/orvix/internal/auth"
	"github.com/orvix/orvix/internal/config"
	"github.com/orvix/orvix/internal/coremail/queue"
	"github.com/orvix/orvix/internal/license"
	"github.com/orvix/orvix/internal/models"
	"github.com/orvix/orvix/internal/modules"
)

const (
	pdlPSAEmail    = "pdl-psa@platform.local"
	pdlPSAPass     = "PlatformPSA!2026"
	pdlTenantEmail = "pdl-tenant-admin@tenant1.local"
	pdlTenantPass  = "PdlTenant!2026"
)

type platformDomainLifecycleEnv struct {
	router     *api.Router
	db         *sql.DB
	psaTok     string
	psaCSRF    string
	tenantTok  string
	tenantCSRF string
}

func buildPlatformDomainLifecycleEnv(t *testing.T) *platformDomainLifecycleEnv {
	t.Helper()
	logger := zap.NewNop()
	cfg := config.Defaults()
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = t.TempDir() + "/pdl.db?_loc=auto&_busy_timeout=5000&_txlock=immediate"
	db, err := config.NewDatabase(&cfg.Database, logger)
	if err != nil {
		t.Fatalf("database: %v", err)
	}
	if err := models.MigrateAllRaw(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	authenticator, err := auth.NewAuthenticator(&cfg.Auth, db, logger)
	if err != nil {
		t.Fatalf("authenticator: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("sql db: %v", err)
	}
	// coremail_queue isn't part of MigrateAllRaw (it's provisioned by
	// the queue engine's own startup path in the real server); this
	// test exercises the domain-deactivate queue-dependency check, so
	// it needs the real schema, not a weakened production check.
	for _, ddl := range queue.Tables() {
		if _, err := sqlDB.Exec(ddl); err != nil {
			t.Fatalf("create queue schema: %v", err)
		}
	}
	now := time.Now().UTC()
	psaHash, _ := authenticator.HashPassword(pdlPSAPass)
	if _, err := sqlDB.Exec("INSERT INTO users (created_at, updated_at, email, password_hash, role, tenant_id, active, email_verified) VALUES (?, ?, ?, ?, 'platform_super_admin', NULL, 1, 1)", now, now, pdlPSAEmail, psaHash); err != nil {
		t.Fatalf("seed psa: %v", err)
	}
	if _, err := sqlDB.Exec("INSERT INTO tenants (id, created_at, updated_at, name, slug, domain, plan, active) VALUES (1, ?, ?, 'tenant-a', 'tenant-a', 't1.example', 'enterprise', 1)", now, now); err != nil {
		t.Fatalf("seed tenant 1: %v", err)
	}
	if _, err := sqlDB.Exec("INSERT INTO tenants (id, created_at, updated_at, name, slug, domain, plan, active) VALUES (2, ?, ?, 'tenant-b', 'tenant-b', 't2.example', 'enterprise', 1)", now, now); err != nil {
		t.Fatalf("seed tenant 2: %v", err)
	}
	seedTenantAdminWithPassword(t, sqlDB, pdlTenantEmail, 1, pdlTenantPass)

	router := api.NewRouter(cfg, authenticator, logger, db, modules.NewRegistry(logger), license.NewFeatureFlags(logger), nil)
	t.Cleanup(func() {
		_ = router.App().Shutdown()
		_ = sqlDB.Close()
	})

	psaTok := importRouteLogin(t, router, pdlPSAEmail, pdlPSAPass)
	tenantTok := importRouteLogin(t, router, pdlTenantEmail, pdlTenantPass)
	return &platformDomainLifecycleEnv{
		router:     router,
		db:         sqlDB,
		psaTok:     psaTok,
		psaCSRF:    importRouteCSRF(t, router, psaTok),
		tenantTok:  tenantTok,
		tenantCSRF: importRouteCSRF(t, router, tenantTok),
	}
}

// seedDomain inserts a domain row directly (bypassing the create
// route, which isn't under test here) and returns its id.
func (e *platformDomainLifecycleEnv) seedDomain(t *testing.T, tenantID uint, name string) uint {
	t.Helper()
	now := time.Now().UTC()
	res, err := e.db.Exec(
		"INSERT INTO coremail_domains (name, tenant_id, status, created_at, updated_at, version) VALUES (?, ?, 'active', ?, ?, 1)",
		name, tenantID, now, now)
	if err != nil {
		t.Fatalf("seed domain: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("seed domain id: %v", err)
	}
	return uint(id)
}

type domainDeactivateOpts struct {
	token, csrf, idempotencyKey string
}

func (e *platformDomainLifecycleEnv) deactivate(t *testing.T, tenantID, domainID uint, opts domainDeactivateOpts, body map[string]any) (*http.Response, map[string]interface{}) {
	t.Helper()
	b, _ := json.Marshal(body)
	path := "/api/v1/platform/domains/" + itoa(int64(tenantID)) + "/" + itoa(int64(domainID)) + "/deactivate"
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	if opts.token != "" {
		req.Header.Set("Authorization", "Bearer "+opts.token)
	}
	if opts.csrf != "" {
		req.Header.Set("Cookie", "csrf_token="+opts.csrf)
		req.Header.Set("X-CSRF-Token", opts.csrf)
	}
	if opts.idempotencyKey != "" {
		req.Header.Set("Idempotency-Key", opts.idempotencyKey)
	}
	resp, err := e.router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("deactivate: %v", err)
	}
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]interface{}
	_ = json.Unmarshal(raw, &out)
	return resp, out
}

func domainConfirm(id uint) string { return "DEACTIVATE-DOMAIN-" + itoa(int64(id)) }

func TestDeactivatePlatformDomain_TenantAdminDenied(t *testing.T) {
	env := buildPlatformDomainLifecycleEnv(t)
	domID := env.seedDomain(t, 1, "d1.example")
	resp, _ := env.deactivate(t, 1, domID, domainDeactivateOpts{token: env.tenantTok, csrf: env.tenantCSRF, idempotencyKey: "k1"},
		map[string]any{"confirm": domainConfirm(domID), "reason": "test", "expected_version": 1})
	if resp.StatusCode != fiber.StatusForbidden {
		t.Fatalf("expected 403 for tenant admin, got %d", resp.StatusCode)
	}
}

func TestDeactivatePlatformDomain_UnauthenticatedDenied(t *testing.T) {
	env := buildPlatformDomainLifecycleEnv(t)
	domID := env.seedDomain(t, 1, "d1.example")
	resp, _ := env.deactivate(t, 1, domID, domainDeactivateOpts{idempotencyKey: "k1"},
		map[string]any{"confirm": domainConfirm(domID), "reason": "test", "expected_version": 1})
	if resp.StatusCode != fiber.StatusUnauthorized && resp.StatusCode != fiber.StatusForbidden {
		t.Fatalf("expected 401/403, got %d", resp.StatusCode)
	}
}

func TestDeactivatePlatformDomain_CrossTenantPathIsNotFound(t *testing.T) {
	env := buildPlatformDomainLifecycleEnv(t)
	domID := env.seedDomain(t, 1, "d1.example")
	// Request targets tenant 2 in the path but the domain belongs to
	// tenant 1 — the SQL predicate must make this a 404, not leak
	// existence or allow cross-tenant mutation.
	resp, _ := env.deactivate(t, 2, domID, domainDeactivateOpts{token: env.psaTok, csrf: env.psaCSRF, idempotencyKey: "k1"},
		map[string]any{"confirm": domainConfirm(domID), "reason": "test", "expected_version": 1})
	if resp.StatusCode != fiber.StatusNotFound {
		t.Fatalf("expected 404 for cross-tenant path, got %d", resp.StatusCode)
	}
	var status string
	_ = env.db.QueryRow("SELECT status FROM coremail_domains WHERE id = ?", domID).Scan(&status)
	if status != "active" {
		t.Fatalf("domain must remain untouched, status=%s", status)
	}
}

func TestDeactivatePlatformDomain_WrongConfirmationRejected(t *testing.T) {
	env := buildPlatformDomainLifecycleEnv(t)
	domID := env.seedDomain(t, 1, "d1.example")
	resp, _ := env.deactivate(t, 1, domID, domainDeactivateOpts{token: env.psaTok, csrf: env.psaCSRF, idempotencyKey: "k1"},
		map[string]any{"confirm": "DEACTIVATE-DOMAIN-999999999", "reason": "test", "expected_version": 1})
	if resp.StatusCode != fiber.StatusPreconditionFailed {
		t.Fatalf("expected 412, got %d", resp.StatusCode)
	}
}

func TestDeactivatePlatformDomain_MissingExpectedVersionRejected(t *testing.T) {
	env := buildPlatformDomainLifecycleEnv(t)
	domID := env.seedDomain(t, 1, "d1.example")
	resp, _ := env.deactivate(t, 1, domID, domainDeactivateOpts{token: env.psaTok, csrf: env.psaCSRF, idempotencyKey: "k1"},
		map[string]any{"confirm": domainConfirm(domID), "reason": "test"})
	if resp.StatusCode != fiber.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestDeactivatePlatformDomain_StaleVersionConflicts(t *testing.T) {
	env := buildPlatformDomainLifecycleEnv(t)
	domID := env.seedDomain(t, 1, "d1.example")
	resp, out := env.deactivate(t, 1, domID, domainDeactivateOpts{token: env.psaTok, csrf: env.psaCSRF, idempotencyKey: "k1"},
		map[string]any{"confirm": domainConfirm(domID), "reason": "test", "expected_version": 99})
	if resp.StatusCode != fiber.StatusConflict {
		t.Fatalf("expected 409 for stale version, got %d: %v", resp.StatusCode, out)
	}
	var status string
	_ = env.db.QueryRow("SELECT status FROM coremail_domains WHERE id = ?", domID).Scan(&status)
	if status != "active" {
		t.Fatalf("domain must be untouched after a version conflict, status=%s", status)
	}
}

func TestDeactivatePlatformDomain_RefusesWithActiveMailboxes(t *testing.T) {
	env := buildPlatformDomainLifecycleEnv(t)
	domID := env.seedDomain(t, 1, "d1.example")
	now := time.Now().UTC()
	if _, err := env.db.Exec("INSERT INTO coremail_mailboxes (domain_id, tenant_id, local_part, email, password_hash, created_at, updated_at) VALUES (?, 1, 'user1', 'user1@d1.example', 'h', ?, ?)",
		domID, now, now); err != nil {
		t.Fatalf("seed mailbox: %v", err)
	}
	resp, out := env.deactivate(t, 1, domID, domainDeactivateOpts{token: env.psaTok, csrf: env.psaCSRF, idempotencyKey: "k1"},
		map[string]any{"confirm": domainConfirm(domID), "reason": "test", "expected_version": 1})
	if resp.StatusCode != fiber.StatusConflict {
		t.Fatalf("expected 409 (active mailboxes), got %d: %v", resp.StatusCode, out)
	}
	var status string
	_ = env.db.QueryRow("SELECT status FROM coremail_domains WHERE id = ?", domID).Scan(&status)
	if status != "active" {
		t.Fatalf("domain must remain untouched (rollback proof), status=%s", status)
	}
}

func TestDeactivatePlatformDomain_RefusesWithQueuedMail(t *testing.T) {
	env := buildPlatformDomainLifecycleEnv(t)
	domID := env.seedDomain(t, 1, "d1.example")
	now := time.Now().UTC()
	if _, err := env.db.Exec("INSERT INTO coremail_queue (tenant_id, domain_id, message_id, to_address, status, created_at, updated_at) VALUES (1, ?, 'msg-1', 'x@y.example', 'pending', ?, ?)",
		domID, now, now); err != nil {
		t.Fatalf("seed queue entry: %v", err)
	}
	resp, out := env.deactivate(t, 1, domID, domainDeactivateOpts{token: env.psaTok, csrf: env.psaCSRF, idempotencyKey: "k1"},
		map[string]any{"confirm": domainConfirm(domID), "reason": "test", "expected_version": 1})
	if resp.StatusCode != fiber.StatusConflict {
		t.Fatalf("expected 409 (queued mail), got %d: %v", resp.StatusCode, out)
	}
}

func TestDeactivatePlatformDomain_SameKeyReplayReturnsStoredResponse(t *testing.T) {
	env := buildPlatformDomainLifecycleEnv(t)
	domID := env.seedDomain(t, 1, "d1.example")
	body := map[string]any{"confirm": domainConfirm(domID), "reason": "cleanup", "expected_version": 1}

	resp1, out1 := env.deactivate(t, 1, domID, domainDeactivateOpts{token: env.psaTok, csrf: env.psaCSRF, idempotencyKey: "key-replay"}, body)
	if resp1.StatusCode != fiber.StatusOK {
		t.Fatalf("first call: expected 200, got %d: %v", resp1.StatusCode, out1)
	}
	resp2, out2 := env.deactivate(t, 1, domID, domainDeactivateOpts{token: env.psaTok, csrf: env.psaCSRF, idempotencyKey: "key-replay"}, body)
	if resp2.StatusCode != fiber.StatusOK {
		t.Fatalf("replay: expected 200, got %d: %v", resp2.StatusCode, out2)
	}
	if out1["request_id"] != out2["request_id"] {
		t.Fatalf("replay must return the exact stored response")
	}
}

func TestDeactivatePlatformDomain_AlreadyDeactivatedIsIdempotentNotAnError(t *testing.T) {
	env := buildPlatformDomainLifecycleEnv(t)
	domID := env.seedDomain(t, 1, "d1.example")

	resp1, _ := env.deactivate(t, 1, domID, domainDeactivateOpts{token: env.psaTok, csrf: env.psaCSRF, idempotencyKey: "first"},
		map[string]any{"confirm": domainConfirm(domID), "reason": "first pass", "expected_version": 1})
	if resp1.StatusCode != fiber.StatusOK {
		t.Fatalf("first deactivation: expected 200, got %d", resp1.StatusCode)
	}

	// A distinct request (different Idempotency-Key, e.g. a genuine
	// retry) against an already-deactivated domain must still succeed.
	resp2, out2 := env.deactivate(t, 1, domID, domainDeactivateOpts{token: env.psaTok, csrf: env.psaCSRF, idempotencyKey: "second"},
		map[string]any{"confirm": domainConfirm(domID), "reason": "retry after lost response", "expected_version": 1})
	if resp2.StatusCode != fiber.StatusOK {
		t.Fatalf("second deactivation of an already-deactivated domain: expected 200, got %d: %v", resp2.StatusCode, out2)
	}
}

func TestDeactivatePlatformDomain_SuccessPreservesDKIMAndAudits(t *testing.T) {
	env := buildPlatformDomainLifecycleEnv(t)
	domID := env.seedDomain(t, 1, "d1.example")
	now := time.Now().UTC()
	if _, err := env.db.Exec("INSERT INTO coremail_dkim_config (domain, selector, private_key_pem, created_at, updated_at) VALUES ('d1.example', 'sel1', 'priv-pem', ?, ?)",
		now, now); err != nil {
		t.Fatalf("seed dkim config: %v", err)
	}

	resp, out := env.deactivate(t, 1, domID, domainDeactivateOpts{token: env.psaTok, csrf: env.psaCSRF, idempotencyKey: "k-success"},
		map[string]any{"confirm": domainConfirm(domID), "reason": "disposable test domain cleanup", "expected_version": 1})
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("expected 200, got %d: %v", resp.StatusCode, out)
	}
	if out["status"] != "deactivated" {
		t.Fatalf("expected status=deactivated, got %v", out["status"])
	}

	var status string
	var deletedAt sql.NullTime
	var deactivatedAt sql.NullTime
	if err := env.db.QueryRow("SELECT status, deleted_at, deactivated_at FROM coremail_domains WHERE id = ?", domID).Scan(&status, &deletedAt, &deactivatedAt); err != nil {
		t.Fatalf("read domain: %v", err)
	}
	if status != "deactivated" {
		t.Fatalf("expected status=deactivated, got %s", status)
	}
	if deletedAt.Valid {
		t.Fatalf("deactivation must never set deleted_at")
	}
	if !deactivatedAt.Valid {
		t.Fatalf("expected deactivated_at to be set")
	}

	var dkimCount int
	_ = env.db.QueryRow("SELECT COUNT(*) FROM coremail_dkim_config WHERE domain = 'd1.example'").Scan(&dkimCount)
	if dkimCount != 1 {
		t.Fatalf("deactivation must preserve DKIM config, got count=%d", dkimCount)
	}

	var auditCount int
	_ = env.db.QueryRow("SELECT COUNT(*) FROM coremail_audit WHERE action = 'platform_domain.deactivate'").Scan(&auditCount)
	if auditCount != 1 {
		t.Fatalf("expected exactly 1 audit entry, got %d", auditCount)
	}
}
