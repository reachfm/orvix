package handlers_test

// Acceptance tests for POST /api/v1/platform/domains/:tenant_id/:id/deactivate
// (Phase 8 production-acceptance remediation, item 3: canonical, audited
// platform domain deactivation/soft-delete lifecycle).

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
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

	"github.com/orvix/orvix/internal/admin/domain"
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
	if out["status"] != "disabled" {
		t.Fatalf("expected status=deactivated, got %v", out["status"])
	}

	var status string
	var deletedAt sql.NullTime
	var deactivatedAt sql.NullTime
	if err := env.db.QueryRow("SELECT status, deleted_at, deactivated_at FROM coremail_domains WHERE id = ?", domID).Scan(&status, &deletedAt, &deactivatedAt); err != nil {
		t.Fatalf("read domain: %v", err)
	}
	if status != "disabled" {
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

// TestDeactivatePlatformDomain_ThenGuardRejectsIt is the integration
// proof that item 3's lifecycle endpoint and the C1 canonical
// operability guard (internal/admin/domain.CheckOperabilityTx) are
// genuinely consistent: a domain this endpoint deactivates must be
// the SAME domain the guard used by every other subsystem (mailbox
// creation today; aliases/DKIM/SMTP/webmail-send/queue once C2/C3
// wire it in) refuses. This would have caught the earlier bug where
// this endpoint wrote an invented "deactivated" status value the
// guard's DomainStatus enum didn't recognize.
func TestDeactivatePlatformDomain_ThenGuardRejectsIt(t *testing.T) {
	env := buildPlatformDomainLifecycleEnv(t)
	domID := env.seedDomain(t, 1, "guard-check.example")

	resp, _ := env.deactivate(t, 1, domID, domainDeactivateOpts{token: env.psaTok, csrf: env.psaCSRF, idempotencyKey: "k-guard-check"},
		map[string]any{"confirm": domainConfirm(domID), "reason": "prove guard consistency", "expected_version": 1})
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	repo := domain.NewDomainAdminRepo(env.db)
	out := repo.CheckOperabilityTx(context.Background(), "guard-check.example", 1, false)
	if out.Operational() {
		t.Fatal("the canonical operability guard reports a just-deactivated domain as operational — " +
			"item 3's lifecycle endpoint and the guard have drifted apart")
	}
	if !errors.Is(out.Err, domain.ErrDomainDisabled) {
		t.Fatalf("expected ErrDomainDisabled from the guard, got %v", out.Err)
	}
}

// ── Real domain version projection (API-contract closure, concern 1) ──

func (e *platformDomainLifecycleEnv) getDomain(t *testing.T, tenantID, domainID uint, opts domainDeactivateOpts) (*http.Response, map[string]interface{}) {
	t.Helper()
	path := "/api/v1/platform/domains/" + itoa(int64(tenantID)) + "/" + itoa(int64(domainID))
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if opts.token != "" {
		req.Header.Set("Authorization", "Bearer "+opts.token)
	}
	resp, err := e.router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("get domain: %v", err)
	}
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]interface{}
	_ = json.Unmarshal(raw, &out)
	return resp, out
}

func (e *platformDomainLifecycleEnv) listDomains(t *testing.T, tenantID uint, opts domainDeactivateOpts) map[string]interface{} {
	t.Helper()
	path := "/api/v1/platform/domains/" + itoa(int64(tenantID))
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if opts.token != "" {
		req.Header.Set("Authorization", "Bearer "+opts.token)
	}
	resp, err := e.router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("list domains: %v", err)
	}
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]interface{}
	_ = json.Unmarshal(raw, &out)
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("list domains: expected 200, got %d: %v", resp.StatusCode, out)
	}
	return out
}

func TestGetPlatformDomain_ReturnsRealVersion(t *testing.T) {
	env := buildPlatformDomainLifecycleEnv(t)
	domID := env.seedDomain(t, 1, "version-detail.example")

	resp, out := env.getDomain(t, 1, domID, domainDeactivateOpts{token: env.psaTok})
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("expected 200, got %d: %v", resp.StatusCode, out)
	}
	v, ok := out["version"].(float64)
	if !ok || v != 1 {
		t.Fatalf("expected version=1 from the real column, got %v", out["version"])
	}
}

func TestListPlatformDomains_ReturnsRealVersion(t *testing.T) {
	env := buildPlatformDomainLifecycleEnv(t)
	env.seedDomain(t, 1, "version-list.example")

	out := env.listDomains(t, 1, domainDeactivateOpts{token: env.psaTok})
	domains, ok := out["domains"].([]interface{})
	if !ok || len(domains) == 0 {
		t.Fatalf("expected at least one domain, got %v", out)
	}
	row := domains[0].(map[string]interface{})
	v, ok := row["version"].(float64)
	if !ok || v != 1 {
		t.Fatalf("expected version=1 in list projection, got %v", row["version"])
	}
}

// TestPlatformDomainVersion_RoundTripsThroughGuardedMutation is the
// decisive test for concern 1: the version GET returns is the exact
// value the guarded deactivate mutation accepts as expected_version —
// no separate query, no guessed value, no synthetic counter.
func TestPlatformDomainVersion_RoundTripsThroughGuardedMutation(t *testing.T) {
	env := buildPlatformDomainLifecycleEnv(t)
	domID := env.seedDomain(t, 1, "version-roundtrip.example")

	_, getOut := env.getDomain(t, 1, domID, domainDeactivateOpts{token: env.psaTok})
	version, ok := getOut["version"].(float64)
	if !ok {
		t.Fatalf("GET domain did not return a numeric version: %v", getOut)
	}

	resp, out := env.deactivate(t, 1, domID, domainDeactivateOpts{token: env.psaTok, csrf: env.psaCSRF, idempotencyKey: "k-version-roundtrip"},
		map[string]any{"confirm": domainConfirm(domID), "reason": "prove version round-trip", "expected_version": int(version)})
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("expected 200 using the GET-returned version as expected_version, got %d: %v", resp.StatusCode, out)
	}
	newVersion, ok := out["version"].(float64)
	if !ok || newVersion != version+1 {
		t.Fatalf("expected version to advance from %v to %v, got %v", version, version+1, out["version"])
	}
}

// TestPlatformDomainVersion_StaleValueRejected proves the version this
// concern adds is actually load-bearing for optimistic concurrency,
// not just a display field: a version read before a concurrent
// mutation elsewhere bumped it must be rejected, not silently applied
// (the already-deactivated-is-idempotent path is a separate, correct
// behavior covered by TestDeactivatePlatformDomain_AlreadyDeactivatedIsIdempotentNotAnError
// and deliberately not what this test exercises).
func TestPlatformDomainVersion_StaleValueRejected(t *testing.T) {
	env := buildPlatformDomainLifecycleEnv(t)
	domID := env.seedDomain(t, 1, "version-stale.example")

	_, getOut := env.getDomain(t, 1, domID, domainDeactivateOpts{token: env.psaTok})
	staleVersion := int(getOut["version"].(float64))

	// Simulate a concurrent mutation elsewhere bumping the real version
	// out from under the value already read above.
	if _, err := env.db.Exec("UPDATE coremail_domains SET version = version + 1 WHERE id = ?", domID); err != nil {
		t.Fatalf("simulate concurrent bump: %v", err)
	}

	resp, out := env.deactivate(t, 1, domID, domainDeactivateOpts{token: env.psaTok, csrf: env.psaCSRF, idempotencyKey: "k-version-stale"},
		map[string]any{"confirm": domainConfirm(domID), "reason": "stale retry", "expected_version": staleVersion})
	if resp.StatusCode != fiber.StatusConflict {
		t.Fatalf("expected 409 for a stale expected_version, got %d: %v", resp.StatusCode, out)
	}
	var status string
	_ = env.db.QueryRow("SELECT status FROM coremail_domains WHERE id = ?", domID).Scan(&status)
	if status != "active" {
		t.Fatalf("domain must be untouched after a version conflict, status=%s", status)
	}
}

// TestGetPlatformDomain_CrossTenantDoesNotDiscloseVersion proves the
// version field added by this concern does not create a new
// cross-tenant disclosure path — a foreign tenant's domain id must
// still resolve to the existing non-disclosing not-found response,
// never a partial body containing the real version.
func TestGetPlatformDomain_CrossTenantDoesNotDiscloseVersion(t *testing.T) {
	env := buildPlatformDomainLifecycleEnv(t)
	domID := env.seedDomain(t, 1, "version-crosstenant.example")

	resp, out := env.getDomain(t, 2, domID, domainDeactivateOpts{token: env.psaTok})
	if resp.StatusCode != fiber.StatusNotFound {
		t.Fatalf("expected 404 for a cross-tenant domain id, got %d: %v", resp.StatusCode, out)
	}
	if _, present := out["version"]; present {
		t.Fatalf("cross-tenant response must never include a version field, got %v", out)
	}
}

// ── Read-only DNS/DKIM snapshot + DKIM generate/rotate (API-contract closure, concerns 2/3) ──

func (e *platformDomainLifecycleEnv) getDomainDNS(t *testing.T, tenantID, domainID uint, opts domainDeactivateOpts) (*http.Response, map[string]interface{}) {
	t.Helper()
	path := "/api/v1/platform/domains/" + itoa(int64(tenantID)) + "/" + itoa(int64(domainID)) + "/dns"
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if opts.token != "" {
		req.Header.Set("Authorization", "Bearer "+opts.token)
	}
	resp, err := e.router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("get domain dns: %v", err)
	}
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]interface{}
	_ = json.Unmarshal(raw, &out)
	return resp, out
}

func (e *platformDomainLifecycleEnv) dkimMutate(t *testing.T, action string, tenantID, domainID uint, opts domainDeactivateOpts, body map[string]any) (*http.Response, map[string]interface{}) {
	t.Helper()
	b, _ := json.Marshal(body)
	path := "/api/v1/platform/domains/" + itoa(int64(tenantID)) + "/" + itoa(int64(domainID)) + "/dkim/" + action
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
		t.Fatalf("%s dkim: %v", action, err)
	}
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]interface{}
	_ = json.Unmarshal(raw, &out)
	return resp, out
}

func TestGetPlatformDomainDNS_NoDKIMReturnsNotConfiguredWithoutGenerating(t *testing.T) {
	env := buildPlatformDomainLifecycleEnv(t)
	domID := env.seedDomain(t, 1, "dns-no-dkim.example")

	resp, out := env.getDomainDNS(t, 1, domID, domainDeactivateOpts{token: env.psaTok})
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("expected 200, got %d: %v", resp.StatusCode, out)
	}
	if configured, _ := out["dkim_configured"].(bool); configured {
		t.Fatalf("expected dkim_configured=false, got %v", out)
	}
	if _, present := out["dkim_public_dns_txt"]; present {
		t.Fatalf("no DKIM configured must not include a TXT value, got %v", out)
	}
	var count int
	if err := env.db.QueryRow("SELECT COUNT(*) FROM coremail_dkim_config WHERE domain = ?", "dns-no-dkim.example").Scan(&count); err != nil {
		t.Fatalf("count dkim rows: %v", err)
	}
	if count != 0 {
		t.Fatalf("GET must never generate a DKIM key, found %d rows", count)
	}
}

func TestGetPlatformDomainDNS_CrossTenantDenied(t *testing.T) {
	env := buildPlatformDomainLifecycleEnv(t)
	domID := env.seedDomain(t, 1, "dns-crosstenant.example")

	resp, out := env.getDomainDNS(t, 2, domID, domainDeactivateOpts{token: env.psaTok})
	if resp.StatusCode != fiber.StatusNotFound {
		t.Fatalf("expected 404 for a cross-tenant domain id, got %d: %v", resp.StatusCode, out)
	}
}

func TestGetPlatformDomainDNS_TenantAdminDenied(t *testing.T) {
	env := buildPlatformDomainLifecycleEnv(t)
	domID := env.seedDomain(t, 1, "dns-tenant-denied.example")

	resp, _ := env.getDomainDNS(t, 1, domID, domainDeactivateOpts{token: env.tenantTok})
	if resp.StatusCode != fiber.StatusForbidden && resp.StatusCode != fiber.StatusNotFound {
		t.Fatalf("expected tenant-admin denial (403/404), got %d", resp.StatusCode)
	}
}

func TestGeneratePlatformDomainDKIM_SucceedsOnceAndReturnsPublicMaterialOnly(t *testing.T) {
	env := buildPlatformDomainLifecycleEnv(t)
	domID := env.seedDomain(t, 1, "dkim-generate.example")

	resp, out := env.dkimMutate(t, "generate", 1, domID, domainDeactivateOpts{token: env.psaTok, csrf: env.psaCSRF, idempotencyKey: "k-dkim-gen"},
		map[string]any{"selector": "mail", "expected_version": 1})
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("expected 200, got %d: %v", resp.StatusCode, out)
	}
	if out["selector"] != "mail" {
		t.Fatalf("selector=%v", out["selector"])
	}
	if _, ok := out["public_dns_txt"].(string); !ok {
		t.Fatalf("expected a public_dns_txt string, got %v", out)
	}
	v, ok := out["version"].(float64)
	if !ok || v != 2 {
		t.Fatalf("expected version to advance to 2, got %v", out["version"])
	}
	// The response body — serialized JSON, not a Go struct inspection —
	// must never contain the private key or any PEM material.
	raw, _ := json.Marshal(out)
	if strings.Contains(string(raw), "PRIVATE KEY") || strings.Contains(string(raw), "private_key") {
		t.Fatalf("response leaked private key material: %s", raw)
	}

	dnsResp, dnsOut := env.getDomainDNS(t, 1, domID, domainDeactivateOpts{token: env.psaTok})
	if dnsResp.StatusCode != fiber.StatusOK {
		t.Fatalf("expected 200 from GET dns after generate, got %d: %v", dnsResp.StatusCode, dnsOut)
	}
	if configured, _ := dnsOut["dkim_configured"].(bool); !configured {
		t.Fatalf("expected dkim_configured=true after generate, got %v", dnsOut)
	}
}

func TestGeneratePlatformDomainDKIM_DuplicateReturnsTypedConflict(t *testing.T) {
	env := buildPlatformDomainLifecycleEnv(t)
	domID := env.seedDomain(t, 1, "dkim-duplicate.example")

	resp1, out1 := env.dkimMutate(t, "generate", 1, domID, domainDeactivateOpts{token: env.psaTok, csrf: env.psaCSRF, idempotencyKey: "k-dup-1"},
		map[string]any{"selector": "mail", "expected_version": 1})
	if resp1.StatusCode != fiber.StatusOK {
		t.Fatalf("expected first generate to succeed, got %d: %v", resp1.StatusCode, out1)
	}

	resp2, out2 := env.dkimMutate(t, "generate", 1, domID, domainDeactivateOpts{token: env.psaTok, csrf: env.psaCSRF, idempotencyKey: "k-dup-2"},
		map[string]any{"selector": "mail", "expected_version": 2})
	if resp2.StatusCode != fiber.StatusConflict {
		t.Fatalf("expected 409 for a duplicate generate, got %d: %v", resp2.StatusCode, out2)
	}
}

func TestRotatePlatformDomainDKIM_WithoutConfirmationRejected(t *testing.T) {
	env := buildPlatformDomainLifecycleEnv(t)
	domID := env.seedDomain(t, 1, "dkim-rotate-noconfirm.example")

	genResp, _ := env.dkimMutate(t, "generate", 1, domID, domainDeactivateOpts{token: env.psaTok, csrf: env.psaCSRF, idempotencyKey: "k-rotate-gen"},
		map[string]any{"selector": "mail", "expected_version": 1})
	if genResp.StatusCode != fiber.StatusOK {
		t.Fatalf("setup generate failed: %d", genResp.StatusCode)
	}

	resp, out := env.dkimMutate(t, "rotate", 1, domID, domainDeactivateOpts{token: env.psaTok, csrf: env.psaCSRF, idempotencyKey: "k-rotate-noconfirm"},
		map[string]any{"selector": "mail", "expected_version": 2})
	if resp.StatusCode != fiber.StatusPreconditionFailed {
		t.Fatalf("expected 412 without confirm_rotation, got %d: %v", resp.StatusCode, out)
	}
}

func TestRotatePlatformDomainDKIM_ConfirmedSucceedsAndAdvancesVersion(t *testing.T) {
	env := buildPlatformDomainLifecycleEnv(t)
	domID := env.seedDomain(t, 1, "dkim-rotate-confirmed.example")

	genResp, genOut := env.dkimMutate(t, "generate", 1, domID, domainDeactivateOpts{token: env.psaTok, csrf: env.psaCSRF, idempotencyKey: "k-rotate-confirmed-gen"},
		map[string]any{"selector": "mail", "expected_version": 1})
	if genResp.StatusCode != fiber.StatusOK {
		t.Fatalf("setup generate failed: %d: %v", genResp.StatusCode, genOut)
	}
	firstTXT := genOut["public_dns_txt"]

	resp, out := env.dkimMutate(t, "rotate", 1, domID, domainDeactivateOpts{token: env.psaTok, csrf: env.psaCSRF, idempotencyKey: "k-rotate-confirmed"},
		map[string]any{"selector": "mail", "confirm_rotation": "rotate-dkim-key", "expected_version": 2})
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("expected 200 for confirmed rotation, got %d: %v", resp.StatusCode, out)
	}
	v, ok := out["version"].(float64)
	if !ok || v != 3 {
		t.Fatalf("expected version to advance to 3, got %v", out["version"])
	}
	if out["public_dns_txt"] == firstTXT {
		t.Fatalf("rotation must produce a different public key, got the same TXT value")
	}
}

func TestPlatformDKIM_StaleExpectedVersionRejected(t *testing.T) {
	env := buildPlatformDomainLifecycleEnv(t)
	domID := env.seedDomain(t, 1, "dkim-stale-version.example")

	if _, err := env.db.Exec("UPDATE coremail_domains SET version = version + 1 WHERE id = ?", domID); err != nil {
		t.Fatalf("simulate concurrent bump: %v", err)
	}

	resp, out := env.dkimMutate(t, "generate", 1, domID, domainDeactivateOpts{token: env.psaTok, csrf: env.psaCSRF, idempotencyKey: "k-stale-dkim"},
		map[string]any{"selector": "mail", "expected_version": 1})
	if resp.StatusCode != fiber.StatusConflict {
		t.Fatalf("expected 409 for a stale expected_version, got %d: %v", resp.StatusCode, out)
	}
	var count int
	if err := env.db.QueryRow("SELECT COUNT(*) FROM coremail_dkim_config WHERE domain = ?", "dkim-stale-version.example").Scan(&count); err != nil {
		t.Fatalf("count dkim rows: %v", err)
	}
	if count != 0 {
		t.Fatalf("a stale version conflict must not create a DKIM row, found %d", count)
	}
}

func TestGeneratePlatformDomainDKIM_CrossTenantDenied(t *testing.T) {
	env := buildPlatformDomainLifecycleEnv(t)
	domID := env.seedDomain(t, 1, "dkim-crosstenant.example")

	resp, out := env.dkimMutate(t, "generate", 2, domID, domainDeactivateOpts{token: env.psaTok, csrf: env.psaCSRF, idempotencyKey: "k-crosstenant-dkim"},
		map[string]any{"selector": "mail", "expected_version": 1})
	if resp.StatusCode != fiber.StatusNotFound {
		t.Fatalf("expected 404 for a cross-tenant generate, got %d: %v", resp.StatusCode, out)
	}
	var count int
	if err := env.db.QueryRow("SELECT COUNT(*) FROM coremail_dkim_config WHERE domain = ?", "dkim-crosstenant.example").Scan(&count); err != nil {
		t.Fatalf("count dkim rows: %v", err)
	}
	if count != 0 {
		t.Fatalf("a cross-tenant generate must never create a DKIM row, found %d", count)
	}
}

func TestGeneratePlatformDomainDKIM_TenantAdminDenied(t *testing.T) {
	env := buildPlatformDomainLifecycleEnv(t)
	domID := env.seedDomain(t, 1, "dkim-tenant-denied.example")

	resp, _ := env.dkimMutate(t, "generate", 1, domID, domainDeactivateOpts{token: env.tenantTok, csrf: env.tenantCSRF, idempotencyKey: "k-tenant-denied-dkim"},
		map[string]any{"selector": "mail", "expected_version": 1})
	if resp.StatusCode != fiber.StatusForbidden && resp.StatusCode != fiber.StatusNotFound {
		t.Fatalf("expected tenant-admin denial (403/404), got %d", resp.StatusCode)
	}
}

// TestGeneratePlatformDomainDKIM_ConcurrentRequestsProduceExactlyOneConfig
// races two generate requests carrying the SAME expected_version (both
// read version=1 before either mutated it) against the same domain.
// The version-guarded UPDATE inside PlatformDKIMForTenant's locked
// transaction must allow exactly one to win; the other must observe
// either a stale-version conflict or an already-configured conflict
// (never a second DKIM row).
func TestGeneratePlatformDomainDKIM_ConcurrentRequestsProduceExactlyOneConfig(t *testing.T) {
	env := buildPlatformDomainLifecycleEnv(t)
	domID := env.seedDomain(t, 1, "dkim-concurrent.example")

	var wg sync.WaitGroup
	statuses := make([]int, 2)
	start := make(chan struct{})
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			resp, _ := env.dkimMutate(t, "generate", 1, domID,
				domainDeactivateOpts{token: env.psaTok, csrf: env.psaCSRF, idempotencyKey: fmt.Sprintf("k-concurrent-%d", i)},
				map[string]any{"selector": "mail", "expected_version": 1})
			statuses[i] = resp.StatusCode
		}(i)
	}
	close(start)
	wg.Wait()

	successes := 0
	for _, s := range statuses {
		if s == fiber.StatusOK {
			successes++
		} else if s != fiber.StatusConflict {
			t.Fatalf("unexpected status %d (want 200 or 409)", s)
		}
	}
	if successes != 1 {
		t.Fatalf("expected exactly one winner, got %d successes: %v", successes, statuses)
	}

	var count int
	if err := env.db.QueryRow("SELECT COUNT(*) FROM coremail_dkim_config WHERE domain = ?", "dkim-concurrent.example").Scan(&count); err != nil {
		t.Fatalf("count dkim rows: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected exactly one DKIM config row, got %d", count)
	}
}

// ── Platform domain DELETE (canonical deleted_at tombstone) ────────────────
//
// POST /api/v1/platform/domains/:tenant_id/:id/delete
//
// Distinct authority and semantics from deactivate above: PERMANENT,
// requires deactivate-first, purges the active DKIM config (history
// preserved), never cascades to mailboxes/aliases/queued mail.

func (e *platformDomainLifecycleEnv) deleteDomain(t *testing.T, tenantID, domainID uint, opts domainDeactivateOpts, body map[string]any) (*http.Response, map[string]interface{}) {
	t.Helper()
	b, _ := json.Marshal(body)
	path := "/api/v1/platform/domains/" + itoa(int64(tenantID)) + "/" + itoa(int64(domainID)) + "/delete"
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
		t.Fatalf("delete: %v", err)
	}
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]interface{}
	_ = json.Unmarshal(raw, &out)
	return resp, out
}

func deleteConfirm(id uint) string { return "DELETE-DOMAIN-" + itoa(int64(id)) }

// deactivateForDelete deactivates a freshly-seeded domain (version 1 ->
// 2) so delete-path tests can exercise the deactivate-then-delete gate
// without repeating the deactivate call inline. Returns the domain id
// and its version after deactivation (2).
func (e *platformDomainLifecycleEnv) deactivateForDelete(t *testing.T, tenantID uint, name string) (domID uint, version int) {
	t.Helper()
	domID = e.seedDomain(t, tenantID, name)
	resp, out := e.deactivate(t, tenantID, domID, domainDeactivateOpts{token: e.psaTok, csrf: e.psaCSRF, idempotencyKey: "deactivate-for-delete-" + name},
		map[string]any{"confirm": domainConfirm(domID), "reason": "pre-delete deactivation", "expected_version": 1})
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("pre-delete deactivation failed: %d: %v", resp.StatusCode, out)
	}
	return domID, 2
}

func TestDeletePlatformDomain_TenantAdminDenied(t *testing.T) {
	env := buildPlatformDomainLifecycleEnv(t)
	domID, ver := env.deactivateForDelete(t, 1, "del-tenant-denied.example")
	resp, _ := env.deleteDomain(t, 1, domID, domainDeactivateOpts{token: env.tenantTok, csrf: env.tenantCSRF, idempotencyKey: "k1"},
		map[string]any{"confirm": deleteConfirm(domID), "reason": "test", "expected_version": ver})
	if resp.StatusCode != fiber.StatusForbidden {
		t.Fatalf("expected 403 for tenant admin, got %d", resp.StatusCode)
	}
}

func TestDeletePlatformDomain_UnauthenticatedDenied(t *testing.T) {
	env := buildPlatformDomainLifecycleEnv(t)
	domID, ver := env.deactivateForDelete(t, 1, "del-unauth.example")
	resp, _ := env.deleteDomain(t, 1, domID, domainDeactivateOpts{idempotencyKey: "k1"},
		map[string]any{"confirm": deleteConfirm(domID), "reason": "test", "expected_version": ver})
	if resp.StatusCode != fiber.StatusUnauthorized && resp.StatusCode != fiber.StatusForbidden {
		t.Fatalf("expected 401/403, got %d", resp.StatusCode)
	}
}

// TestDeletePlatformDomain_CrossTenantPathIsNotFound is the C — "cross-
// tenant -> 404/403 without leak" requirement: the path names tenant 2
// but the domain belongs to tenant 1.
func TestDeletePlatformDomain_CrossTenantPathIsNotFound(t *testing.T) {
	env := buildPlatformDomainLifecycleEnv(t)
	domID, ver := env.deactivateForDelete(t, 1, "del-crosstenant.example")
	resp, _ := env.deleteDomain(t, 2, domID, domainDeactivateOpts{token: env.psaTok, csrf: env.psaCSRF, idempotencyKey: "k1"},
		map[string]any{"confirm": deleteConfirm(domID), "reason": "test", "expected_version": ver})
	if resp.StatusCode != fiber.StatusNotFound {
		t.Fatalf("expected 404 for cross-tenant path, got %d", resp.StatusCode)
	}
	var deletedAt sql.NullTime
	_ = env.db.QueryRow("SELECT deleted_at FROM coremail_domains WHERE id = ?", domID).Scan(&deletedAt)
	if deletedAt.Valid {
		t.Fatalf("domain must remain untouched by a cross-tenant path")
	}
}

func TestDeletePlatformDomain_WrongConfirmationRejected(t *testing.T) {
	env := buildPlatformDomainLifecycleEnv(t)
	domID, ver := env.deactivateForDelete(t, 1, "del-wrongconfirm.example")
	resp, _ := env.deleteDomain(t, 1, domID, domainDeactivateOpts{token: env.psaTok, csrf: env.psaCSRF, idempotencyKey: "k1"},
		map[string]any{"confirm": "DELETE-DOMAIN-999999999", "reason": "test", "expected_version": ver})
	if resp.StatusCode != fiber.StatusPreconditionFailed {
		t.Fatalf("expected 412, got %d", resp.StatusCode)
	}
}

func TestDeletePlatformDomain_ReasonRequired(t *testing.T) {
	env := buildPlatformDomainLifecycleEnv(t)
	domID, ver := env.deactivateForDelete(t, 1, "del-noreason.example")
	resp, _ := env.deleteDomain(t, 1, domID, domainDeactivateOpts{token: env.psaTok, csrf: env.psaCSRF, idempotencyKey: "k1"},
		map[string]any{"confirm": deleteConfirm(domID), "expected_version": ver})
	if resp.StatusCode != fiber.StatusBadRequest {
		t.Fatalf("expected 400 (reason required), got %d", resp.StatusCode)
	}
}

func TestDeletePlatformDomain_StaleVersionConflicts(t *testing.T) {
	env := buildPlatformDomainLifecycleEnv(t)
	domID, _ := env.deactivateForDelete(t, 1, "del-staleversion.example")
	resp, out := env.deleteDomain(t, 1, domID, domainDeactivateOpts{token: env.psaTok, csrf: env.psaCSRF, idempotencyKey: "k1"},
		map[string]any{"confirm": deleteConfirm(domID), "reason": "test", "expected_version": 999})
	if resp.StatusCode != fiber.StatusConflict {
		t.Fatalf("expected 409 for stale version, got %d: %v", resp.StatusCode, out)
	}
	var deletedAt sql.NullTime
	_ = env.db.QueryRow("SELECT deleted_at FROM coremail_domains WHERE id = ?", domID).Scan(&deletedAt)
	if deletedAt.Valid {
		t.Fatalf("domain must be untouched after a version conflict")
	}
}

// TestDeletePlatformDomain_RequiresDeactivationFirst proves the
// deactivate-then-delete policy gate: a live, never-deactivated domain
// cannot be deleted directly, even with a correct confirmation/version.
func TestDeletePlatformDomain_RequiresDeactivationFirst(t *testing.T) {
	env := buildPlatformDomainLifecycleEnv(t)
	domID := env.seedDomain(t, 1, "del-notdeactivated.example")
	resp, out := env.deleteDomain(t, 1, domID, domainDeactivateOpts{token: env.psaTok, csrf: env.psaCSRF, idempotencyKey: "k1"},
		map[string]any{"confirm": deleteConfirm(domID), "reason": "test", "expected_version": 1})
	if resp.StatusCode != fiber.StatusConflict {
		t.Fatalf("expected 409 (not deactivated), got %d: %v", resp.StatusCode, out)
	}
	if out["code"] != "DOMAIN_NOT_DEACTIVATED" {
		t.Fatalf("expected code=DOMAIN_NOT_DEACTIVATED, got %v", out["code"])
	}
	var deletedAt sql.NullTime
	_ = env.db.QueryRow("SELECT deleted_at FROM coremail_domains WHERE id = ?", domID).Scan(&deletedAt)
	if deletedAt.Valid {
		t.Fatalf("domain must remain untouched")
	}
}

func TestDeletePlatformDomain_RefusesWithActiveMailboxes(t *testing.T) {
	env := buildPlatformDomainLifecycleEnv(t)
	domID, ver := env.deactivateForDelete(t, 1, "del-mailboxblocker.example")
	now := time.Now().UTC()
	if _, err := env.db.Exec("INSERT INTO coremail_mailboxes (domain_id, tenant_id, local_part, email, password_hash, created_at, updated_at) VALUES (?, 1, 'user1', 'user1@del-mailboxblocker.example', 'h', ?, ?)",
		domID, now, now); err != nil {
		t.Fatalf("seed mailbox: %v", err)
	}
	resp, out := env.deleteDomain(t, 1, domID, domainDeactivateOpts{token: env.psaTok, csrf: env.psaCSRF, idempotencyKey: "k1"},
		map[string]any{"confirm": deleteConfirm(domID), "reason": "test", "expected_version": ver})
	if resp.StatusCode != fiber.StatusConflict {
		t.Fatalf("expected 409 (active mailboxes), got %d: %v", resp.StatusCode, out)
	}
	if out["code"] != "DOMAIN_DELETE_BLOCKED" {
		t.Fatalf("expected code=DOMAIN_DELETE_BLOCKED, got %v", out["code"])
	}
	blockers, _ := out["blockers"].(map[string]interface{})
	if blockers == nil || blockers["mailboxes"].(float64) != 1 {
		t.Fatalf("expected blockers.mailboxes=1, got %v", out["blockers"])
	}
	var deletedAt sql.NullTime
	_ = env.db.QueryRow("SELECT deleted_at FROM coremail_domains WHERE id = ?", domID).Scan(&deletedAt)
	if deletedAt.Valid {
		t.Fatalf("domain must not be deleted while a live mailbox exists")
	}
}

func TestDeletePlatformDomain_RefusesWithActiveAliases(t *testing.T) {
	env := buildPlatformDomainLifecycleEnv(t)
	domID, ver := env.deactivateForDelete(t, 1, "del-aliasblocker.example")
	now := time.Now().UTC()
	if _, err := env.db.Exec("INSERT INTO coremail_aliases (domain_id, tenant_id, from_addr, to_addr, active, created_at, updated_at) VALUES (?, 1, 'alias1@del-aliasblocker.example', 'target@example.com', 1, ?, ?)",
		domID, now, now); err != nil {
		t.Fatalf("seed alias: %v", err)
	}
	resp, out := env.deleteDomain(t, 1, domID, domainDeactivateOpts{token: env.psaTok, csrf: env.psaCSRF, idempotencyKey: "k1"},
		map[string]any{"confirm": deleteConfirm(domID), "reason": "test", "expected_version": ver})
	if resp.StatusCode != fiber.StatusConflict {
		t.Fatalf("expected 409 (active aliases), got %d: %v", resp.StatusCode, out)
	}
	blockers, _ := out["blockers"].(map[string]interface{})
	if blockers == nil || blockers["aliases"].(float64) != 1 {
		t.Fatalf("expected blockers.aliases=1, got %v", out["blockers"])
	}
}

func TestDeletePlatformDomain_RefusesWithQueuedMail(t *testing.T) {
	env := buildPlatformDomainLifecycleEnv(t)
	domID, ver := env.deactivateForDelete(t, 1, "del-queueblocker.example")
	now := time.Now().UTC()
	if _, err := env.db.Exec("INSERT INTO coremail_queue (tenant_id, domain_id, message_id, to_address, status, created_at, updated_at) VALUES (1, ?, 'msg-1', 'x@y.example', 'pending', ?, ?)",
		domID, now, now); err != nil {
		t.Fatalf("seed queue entry: %v", err)
	}
	resp, out := env.deleteDomain(t, 1, domID, domainDeactivateOpts{token: env.psaTok, csrf: env.psaCSRF, idempotencyKey: "k1"},
		map[string]any{"confirm": deleteConfirm(domID), "reason": "test", "expected_version": ver})
	if resp.StatusCode != fiber.StatusConflict {
		t.Fatalf("expected 409 (queued mail), got %d: %v", resp.StatusCode, out)
	}
	blockers, _ := out["blockers"].(map[string]interface{})
	if blockers == nil || blockers["queued_messages"].(float64) != 1 {
		t.Fatalf("expected blockers.queued_messages=1, got %v", out["blockers"])
	}
}

// TestDeletePlatformDomain_SuccessSetsDeletedAtPurgesDKIMPreservesHistory
// is the full happy-path proof covering I, J, K, L, M from the mission
// spec in one deterministic scenario.
func TestDeletePlatformDomain_SuccessSetsDeletedAtPurgesDKIMPreservesHistory(t *testing.T) {
	env := buildPlatformDomainLifecycleEnv(t)
	domID, ver := env.deactivateForDelete(t, 1, "del-success.example")
	now := time.Now().UTC()
	if _, err := env.db.Exec("INSERT INTO coremail_dkim_config (domain, selector, private_key_pem, created_at, updated_at) VALUES ('del-success.example', 'sel1', 'priv-pem', ?, ?)",
		now, now); err != nil {
		t.Fatalf("seed dkim config: %v", err)
	}
	if _, err := env.db.Exec("CREATE TABLE IF NOT EXISTS coremail_dkim_selector_history (id INTEGER PRIMARY KEY AUTOINCREMENT, domain TEXT, tenant_id INTEGER, selector TEXT, action TEXT, created_at DATETIME)"); err != nil {
		t.Fatalf("ensure dkim history table: %v", err)
	}
	if _, err := env.db.Exec("INSERT INTO coremail_dkim_selector_history (domain, tenant_id, selector, action, created_at) VALUES ('del-success.example', 1, 'sel1', 'generate', ?)", now); err != nil {
		t.Fatalf("seed dkim history: %v", err)
	}

	resp, out := env.deleteDomain(t, 1, domID, domainDeactivateOpts{token: env.psaTok, csrf: env.psaCSRF, idempotencyKey: "k-success"},
		map[string]any{"confirm": deleteConfirm(domID), "reason": "permanent removal after offboarding", "expected_version": ver})
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("expected 200, got %d: %v", resp.StatusCode, out)
	}
	if out["deleted"] != true {
		t.Fatalf("expected deleted=true, got %v", out["deleted"])
	}

	// I: deleted_at set.
	var deletedAt sql.NullTime
	if err := env.db.QueryRow("SELECT deleted_at FROM coremail_domains WHERE id = ?", domID).Scan(&deletedAt); err != nil {
		t.Fatalf("read domain: %v", err)
	}
	if !deletedAt.Valid {
		t.Fatalf("expected deleted_at to be set")
	}

	// J: domain disappears from the normal Platform list/detail path
	// (GetDomain/ListDomains filter deleted_at IS NULL — proven at the
	// repository layer used by every platform domain read route).
	var activeCount int
	_ = env.db.QueryRow("SELECT COUNT(*) FROM coremail_domains WHERE id = ? AND deleted_at IS NULL", domID).Scan(&activeCount)
	if activeCount != 0 {
		t.Fatalf("domain must no longer be a live row for active-inventory queries")
	}

	// K: active DKIM config purged.
	var dkimCount int
	_ = env.db.QueryRow("SELECT COUNT(*) FROM coremail_dkim_config WHERE domain = 'del-success.example'").Scan(&dkimCount)
	if dkimCount != 0 {
		t.Fatalf("expected active DKIM config purged, got count=%d", dkimCount)
	}

	// L: DKIM/audit history preserved.
	var histCount int
	_ = env.db.QueryRow("SELECT COUNT(*) FROM coremail_dkim_selector_history WHERE domain = 'del-success.example'").Scan(&histCount)
	if histCount != 1 {
		t.Fatalf("expected DKIM selector history preserved, got count=%d", histCount)
	}
	var auditCount int
	_ = env.db.QueryRow("SELECT COUNT(*) FROM coremail_audit WHERE action = 'platform_domain.delete'").Scan(&auditCount)
	if auditCount != 1 {
		t.Fatalf("expected exactly 1 platform_domain.delete audit entry, got %d", auditCount)
	}
	var deactivateAuditCount int
	_ = env.db.QueryRow("SELECT COUNT(*) FROM coremail_audit WHERE action = 'platform_domain.deactivate'").Scan(&deactivateAuditCount)
	if deactivateAuditCount != 1 {
		t.Fatalf("expected the earlier deactivate audit entry to survive, got %d", deactivateAuditCount)
	}
}

// TestDeletePlatformDomain_UnauthorizedRolesDenied — N: unauthorized
// roles denied. tenant admin already covered above; this proves an
// authenticated PSA token WITHOUT the delete permission specifically
// (simulated via the tenant-admin token, which never carries
// PermPlatformDomainsDelete) is denied, matching the deactivate
// equivalent.
func TestDeletePlatformDomain_UnauthorizedRolesDenied(t *testing.T) {
	env := buildPlatformDomainLifecycleEnv(t)
	domID, ver := env.deactivateForDelete(t, 1, "del-unauthorized.example")
	resp, _ := env.deleteDomain(t, 1, domID, domainDeactivateOpts{token: env.tenantTok, csrf: env.tenantCSRF, idempotencyKey: "k1"},
		map[string]any{"confirm": deleteConfirm(domID), "reason": "test", "expected_version": ver})
	if resp.StatusCode != fiber.StatusForbidden {
		t.Fatalf("expected 403, got %d", resp.StatusCode)
	}
}

// TestDeletePlatformDomain_SameKeyReplayReturnsStoredResponse — O:
// idempotency behavior.
func TestDeletePlatformDomain_SameKeyReplayReturnsStoredResponse(t *testing.T) {
	env := buildPlatformDomainLifecycleEnv(t)
	domID, ver := env.deactivateForDelete(t, 1, "del-replay.example")
	body := map[string]any{"confirm": deleteConfirm(domID), "reason": "cleanup", "expected_version": ver}

	resp1, out1 := env.deleteDomain(t, 1, domID, domainDeactivateOpts{token: env.psaTok, csrf: env.psaCSRF, idempotencyKey: "key-replay"}, body)
	if resp1.StatusCode != fiber.StatusOK {
		t.Fatalf("first call: expected 200, got %d: %v", resp1.StatusCode, out1)
	}
	resp2, out2 := env.deleteDomain(t, 1, domID, domainDeactivateOpts{token: env.psaTok, csrf: env.psaCSRF, idempotencyKey: "key-replay"}, body)
	if resp2.StatusCode != fiber.StatusOK {
		t.Fatalf("replay: expected 200, got %d: %v", resp2.StatusCode, out2)
	}
	if out1["request_id"] != out2["request_id"] {
		t.Fatalf("replay must return the exact stored response")
	}
}

// TestDeactivatePlatformDomain_RemainsUnchangedAfterDeleteFeatureAdded
// — P: deactivate remains unchanged. A plain regression proof that
// adding delete did not alter deactivate's own contract/behavior.
func TestDeactivatePlatformDomain_RemainsUnchangedAfterDeleteFeatureAdded(t *testing.T) {
	env := buildPlatformDomainLifecycleEnv(t)
	domID := env.seedDomain(t, 1, "del-deactivate-unchanged.example")
	resp, out := env.deactivate(t, 1, domID, domainDeactivateOpts{token: env.psaTok, csrf: env.psaCSRF, idempotencyKey: "k1"},
		map[string]any{"confirm": domainConfirm(domID), "reason": "still works", "expected_version": 1})
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("expected 200, got %d: %v", resp.StatusCode, out)
	}
	var deletedAt sql.NullTime
	if err := env.db.QueryRow("SELECT deleted_at FROM coremail_domains WHERE id = ?", domID).Scan(&deletedAt); err != nil {
		t.Fatalf("read domain: %v", err)
	}
	if deletedAt.Valid {
		t.Fatalf("deactivate must never set deleted_at, even after delete exists as a sibling route")
	}
}
