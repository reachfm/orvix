package handlers_test

// Acceptance tests for POST /api/v1/platform/users/:id/deactivate
// (Phase 8 production-acceptance remediation, item 1: canonical, audited,
// non-self-service deactivation of a platform-scoped account).

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
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

const (
	pulActorEmail  = "pul-actor@platform.local"
	pulActorPass   = "PlatformActor!2026"
	pulTargetEmail = "pul-target@platform.local"
	pulTargetPass  = "PlatformTarget!2026"
	pulTenantEmail = "pul-tenant-admin@tenant1.local"
	pulTenantPass  = "PulTenant!2026"
)

type platformUserLifecycleEnv struct {
	router     *api.Router
	db         *sql.DB
	actorTok   string
	actorCSRF  string
	tenantTok  string
	tenantCSRF string
	actorID    uint
	targetID   uint
}

func buildPlatformUserLifecycleEnv(t *testing.T) *platformUserLifecycleEnv {
	t.Helper()
	logger := zap.NewNop()
	cfg := config.Defaults()
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = t.TempDir() + "/pul.db?_loc=auto&_busy_timeout=5000&_txlock=immediate"
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
	now := time.Now().UTC()
	actorHash, _ := authenticator.HashPassword(pulActorPass)
	targetHash, _ := authenticator.HashPassword(pulTargetPass)
	if _, err := sqlDB.Exec("INSERT INTO users (created_at, updated_at, email, password_hash, role, tenant_id, active, email_verified) VALUES (?, ?, ?, ?, 'platform_super_admin', NULL, 1, 1)", now, now, pulActorEmail, actorHash); err != nil {
		t.Fatalf("seed actor: %v", err)
	}
	if _, err := sqlDB.Exec("INSERT INTO users (created_at, updated_at, email, password_hash, role, tenant_id, active, email_verified) VALUES (?, ?, ?, ?, 'platform_super_admin', NULL, 1, 1)", now, now, pulTargetEmail, targetHash); err != nil {
		t.Fatalf("seed target: %v", err)
	}
	if _, err := sqlDB.Exec("INSERT INTO tenants (id, created_at, updated_at, name, slug, domain, plan, active) VALUES (1, ?, ?, 'tenant-a', 'tenant-a', 't1.example', 'enterprise', 1)", now, now); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	seedTenantAdminWithPassword(t, sqlDB, pulTenantEmail, 1, pulTenantPass)

	var actorID, targetID uint
	if err := sqlDB.QueryRow("SELECT id FROM users WHERE email = ?", pulActorEmail).Scan(&actorID); err != nil {
		t.Fatalf("actor id: %v", err)
	}
	if err := sqlDB.QueryRow("SELECT id FROM users WHERE email = ?", pulTargetEmail).Scan(&targetID); err != nil {
		t.Fatalf("target id: %v", err)
	}

	router := api.NewRouter(cfg, authenticator, logger, db, modules.NewRegistry(logger), license.NewFeatureFlags(logger), nil)
	t.Cleanup(func() {
		_ = router.App().Shutdown()
		_ = sqlDB.Close()
	})

	actorTok := importRouteLogin(t, router, pulActorEmail, pulActorPass)
	tenantTok := importRouteLogin(t, router, pulTenantEmail, pulTenantPass)
	return &platformUserLifecycleEnv{
		router:     router,
		db:         sqlDB,
		actorTok:   actorTok,
		actorCSRF:  importRouteCSRF(t, router, actorTok),
		tenantTok:  tenantTok,
		tenantCSRF: importRouteCSRF(t, router, tenantTok),
		actorID:    actorID,
		targetID:   targetID,
	}
}

type deactivateOpts struct {
	token          string
	csrf           string
	idempotencyKey string
	rawBody        []byte
}

func (e *platformUserLifecycleEnv) deactivateRaw(t *testing.T, id uint, opts deactivateOpts) (*http.Response, map[string]interface{}) {
	t.Helper()
	path := "/api/v1/platform/users/" + itoa(int64(id)) + "/deactivate"
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(opts.rawBody))
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

func (e *platformUserLifecycleEnv) deactivate(t *testing.T, token, csrf string, id uint, idemKey string, body map[string]string) (*http.Response, map[string]interface{}) {
	t.Helper()
	b, _ := json.Marshal(body)
	return e.deactivateRaw(t, id, deactivateOpts{token: token, csrf: csrf, idempotencyKey: idemKey, rawBody: b})
}

func confirmPhrase(id uint) string { return "DEACTIVATE-USER-" + itoa(int64(id)) }

func TestDeactivatePlatformUser_SelfTargetRejected(t *testing.T) {
	env := buildPlatformUserLifecycleEnv(t)
	resp, out := env.deactivate(t, env.actorTok, env.actorCSRF, env.actorID, "key-self", map[string]string{
		"confirm": confirmPhrase(env.actorID),
		"reason":  "test",
	})
	if resp.StatusCode != fiber.StatusConflict {
		t.Fatalf("expected 409, got %d: %v", resp.StatusCode, out)
	}
}

func TestDeactivatePlatformUser_TenantAdminDenied(t *testing.T) {
	env := buildPlatformUserLifecycleEnv(t)
	resp, out := env.deactivate(t, env.tenantTok, env.tenantCSRF, env.targetID, "key-tenant", map[string]string{
		"confirm": confirmPhrase(env.targetID),
		"reason":  "test",
	})
	if resp.StatusCode != fiber.StatusForbidden {
		t.Fatalf("expected 403 for tenant admin, got %d: %v", resp.StatusCode, out)
	}
	var active int
	_ = env.db.QueryRow("SELECT active FROM users WHERE id = ?", env.targetID).Scan(&active)
	if active != 1 {
		t.Fatalf("target must remain active after a denied request, active=%d", active)
	}
}

func TestDeactivatePlatformUser_UnauthenticatedDenied(t *testing.T) {
	env := buildPlatformUserLifecycleEnv(t)
	resp, _ := env.deactivate(t, "", "", env.targetID, "key-unauth", map[string]string{
		"confirm": confirmPhrase(env.targetID),
		"reason":  "test",
	})
	if resp.StatusCode != fiber.StatusUnauthorized && resp.StatusCode != fiber.StatusForbidden {
		t.Fatalf("expected 401/403 unauthenticated, got %d", resp.StatusCode)
	}
}

func TestDeactivatePlatformUser_WrongConfirmationRejected(t *testing.T) {
	env := buildPlatformUserLifecycleEnv(t)
	resp, _ := env.deactivate(t, env.actorTok, env.actorCSRF, env.targetID, "key-wrongconfirm", map[string]string{
		"confirm": "DEACTIVATE-USER-999999999",
		"reason":  "test",
	})
	if resp.StatusCode != fiber.StatusPreconditionFailed {
		t.Fatalf("expected 412, got %d", resp.StatusCode)
	}
	var active int
	_ = env.db.QueryRow("SELECT active FROM users WHERE id = ?", env.targetID).Scan(&active)
	if active != 1 {
		t.Fatalf("target should remain active after rejected request, active=%d", active)
	}
}

func TestDeactivatePlatformUser_MissingReasonRejected(t *testing.T) {
	env := buildPlatformUserLifecycleEnv(t)
	resp, _ := env.deactivate(t, env.actorTok, env.actorCSRF, env.targetID, "key-noreason", map[string]string{
		"confirm": confirmPhrase(env.targetID),
		"reason":  "",
	})
	if resp.StatusCode != fiber.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestDeactivatePlatformUser_MissingCSRFRejected(t *testing.T) {
	env := buildPlatformUserLifecycleEnv(t)
	resp, _ := env.deactivate(t, env.actorTok, "", env.targetID, "key-nocsrf", map[string]string{
		"confirm": confirmPhrase(env.targetID),
		"reason":  "test",
	})
	if resp.StatusCode != fiber.StatusForbidden {
		t.Fatalf("expected 403 (CSRF), got %d", resp.StatusCode)
	}
}

func TestDeactivatePlatformUser_MissingIdempotencyKeyRejected(t *testing.T) {
	env := buildPlatformUserLifecycleEnv(t)
	resp, _ := env.deactivate(t, env.actorTok, env.actorCSRF, env.targetID, "", map[string]string{
		"confirm": confirmPhrase(env.targetID),
		"reason":  "test",
	})
	if resp.StatusCode != fiber.StatusBadRequest {
		t.Fatalf("expected 400 (missing Idempotency-Key), got %d", resp.StatusCode)
	}
}

func TestDeactivatePlatformUser_UnknownTargetIs404(t *testing.T) {
	env := buildPlatformUserLifecycleEnv(t)
	resp, _ := env.deactivate(t, env.actorTok, env.actorCSRF, 999999, "key-missing", map[string]string{
		"confirm": confirmPhrase(999999),
		"reason":  "test",
	})
	if resp.StatusCode != fiber.StatusNotFound {
		t.Fatalf("expected 404 for unknown target, got %d", resp.StatusCode)
	}
}

func TestDeactivatePlatformUser_TenantScopedTargetRejected(t *testing.T) {
	env := buildPlatformUserLifecycleEnv(t)
	var tenantAdminID uint
	if err := env.db.QueryRow("SELECT id FROM users WHERE email = ?", pulTenantEmail).Scan(&tenantAdminID); err != nil {
		t.Fatalf("tenant admin id: %v", err)
	}
	resp, out := env.deactivate(t, env.actorTok, env.actorCSRF, tenantAdminID, "key-crossscope", map[string]string{
		"confirm": confirmPhrase(tenantAdminID),
		"reason":  "test",
	})
	if resp.StatusCode != fiber.StatusBadRequest {
		t.Fatalf("expected 400 (not a platform identity), got %d: %v", resp.StatusCode, out)
	}
	var active int
	_ = env.db.QueryRow("SELECT active FROM users WHERE id = ?", tenantAdminID).Scan(&active)
	if active != 1 {
		t.Fatalf("tenant-scoped user must not be touched by the platform endpoint, active=%d", active)
	}
}

func TestDeactivatePlatformUser_SameKeyReplayReturnsStoredResponse(t *testing.T) {
	env := buildPlatformUserLifecycleEnv(t)
	body := map[string]string{"confirm": confirmPhrase(env.targetID), "reason": "cleanup"}

	resp1, out1 := env.deactivate(t, env.actorTok, env.actorCSRF, env.targetID, "key-replay", body)
	if resp1.StatusCode != fiber.StatusOK {
		t.Fatalf("first call: expected 200, got %d: %v", resp1.StatusCode, out1)
	}
	resp2, out2 := env.deactivate(t, env.actorTok, env.actorCSRF, env.targetID, "key-replay", body)
	if resp2.StatusCode != fiber.StatusOK {
		t.Fatalf("replay: expected 200, got %d: %v", resp2.StatusCode, out2)
	}
	if out1["request_id"] != out2["request_id"] {
		t.Fatalf("replay must return the exact stored response, request_id differs: %v vs %v", out1["request_id"], out2["request_id"])
	}

	var auditCount int
	_ = env.db.QueryRow("SELECT COUNT(*) FROM coremail_audit WHERE action = 'platform_user.deactivate'").Scan(&auditCount)
	if auditCount != 1 {
		t.Fatalf("replay must not re-run the transaction or write a second audit entry, got %d", auditCount)
	}
}

func TestDeactivatePlatformUser_ChangedBodySameKeyConflicts(t *testing.T) {
	env := buildPlatformUserLifecycleEnv(t)
	resp1, _ := env.deactivate(t, env.actorTok, env.actorCSRF, env.targetID, "key-changedbody", map[string]string{
		"confirm": confirmPhrase(env.targetID),
		"reason":  "first reason",
	})
	if resp1.StatusCode != fiber.StatusOK {
		t.Fatalf("first call: expected 200, got %d", resp1.StatusCode)
	}
	resp2, _ := env.deactivate(t, env.actorTok, env.actorCSRF, env.targetID, "key-changedbody", map[string]string{
		"confirm": confirmPhrase(env.targetID),
		"reason":  "different reason entirely",
	})
	if resp2.StatusCode != fiber.StatusConflict {
		t.Fatalf("expected 409 for a changed body under the same idempotency key, got %d", resp2.StatusCode)
	}
}

func TestDeactivatePlatformUser_ConcurrentSameKeyExecutesOnce(t *testing.T) {
	env := buildPlatformUserLifecycleEnv(t)
	body := map[string]string{"confirm": confirmPhrase(env.targetID), "reason": "concurrent test"}

	const n = 8
	var wg sync.WaitGroup
	statuses := make([]int, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			resp, _ := env.deactivate(t, env.actorTok, env.actorCSRF, env.targetID, "key-concurrent", body)
			statuses[i] = resp.StatusCode
		}(i)
	}
	wg.Wait()

	ok := 0
	for _, s := range statuses {
		if s == fiber.StatusOK {
			ok++
		}
	}
	if ok == 0 {
		t.Fatalf("expected at least one 200 among concurrent identical requests, statuses=%v", statuses)
	}

	var auditCount int
	_ = env.db.QueryRow("SELECT COUNT(*) FROM coremail_audit WHERE action = 'platform_user.deactivate'").Scan(&auditCount)
	if auditCount != 1 {
		t.Fatalf("concurrent identical requests must execute exactly once, got %d audit entries", auditCount)
	}
	var sessCount int
	_ = env.db.QueryRow("SELECT active FROM users WHERE id = ?", env.targetID).Scan(&sessCount)
}

func TestDeactivatePlatformUser_AlreadyInactiveIsIdempotentNotAnError(t *testing.T) {
	env := buildPlatformUserLifecycleEnv(t)
	body := map[string]string{"confirm": confirmPhrase(env.targetID), "reason": "first pass"}

	resp1, _ := env.deactivate(t, env.actorTok, env.actorCSRF, env.targetID, "key-first-pass", body)
	if resp1.StatusCode != fiber.StatusOK {
		t.Fatalf("first deactivation: expected 200, got %d", resp1.StatusCode)
	}

	// A distinct request (different Idempotency-Key — e.g. a genuine
	// retry after the client never saw the first response) against an
	// already-inactive target must still succeed, not error.
	resp2, out2 := env.deactivate(t, env.actorTok, env.actorCSRF, env.targetID, "key-second-pass", map[string]string{
		"confirm": confirmPhrase(env.targetID),
		"reason":  "retry after lost response",
	})
	if resp2.StatusCode != fiber.StatusOK {
		t.Fatalf("second deactivation of an already-inactive target: expected 200, got %d: %v", resp2.StatusCode, out2)
	}
	if out2["active"] != false {
		t.Fatalf("expected active=false, got %v", out2["active"])
	}
}

func TestDeactivatePlatformUser_NoSecretsInResponseOrAudit(t *testing.T) {
	env := buildPlatformUserLifecycleEnv(t)
	resp, _ := env.deactivate(t, env.actorTok, env.actorCSRF, env.targetID, "key-secrecy", map[string]string{
		"confirm": confirmPhrase(env.targetID),
		"reason":  "secrecy check",
	})
	raw, _ := io.ReadAll(resp.Body)
	body := string(raw)
	for _, forbidden := range []string{pulTargetPass, "password_hash", "token_hash", "key_hash"} {
		if bytes.Contains([]byte(body), []byte(forbidden)) {
			t.Fatalf("response leaked %q", forbidden)
		}
	}
	var target, auditBlob string
	_ = env.db.QueryRow("SELECT target FROM coremail_audit WHERE action = 'platform_user.deactivate'").Scan(&target)
	auditBlob = target
	for _, forbidden := range []string{pulTargetPass} {
		if bytes.Contains([]byte(auditBlob), []byte(forbidden)) {
			t.Fatalf("audit record leaked %q", forbidden)
		}
	}
}

func TestDeactivatePlatformUser_SuccessRevokesEverythingAndAudits(t *testing.T) {
	env := buildPlatformUserLifecycleEnv(t)
	now := time.Now().UTC()

	if _, err := env.db.Exec("INSERT INTO sessions (created_at, updated_at, user_id, token_hash, role, email, ip, user_agent, jti, expires_at) VALUES (?, ?, ?, 'h', 'platform_super_admin', ?, '127.0.0.1', 'test', 'jti-1', ?)",
		now, now, env.targetID, pulTargetEmail, now.Add(time.Hour)); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	if _, err := env.db.Exec("INSERT INTO api_keys (created_at, updated_at, user_id, tenant_id, role, name, key_hash, key_prefix, active) VALUES (?, ?, ?, 0, 'platform_super_admin', 'test-key', 'h', 'pfx', 1)",
		now, now, env.targetID); err != nil {
		t.Fatalf("seed api key: %v", err)
	}
	if _, err := env.db.Exec("INSERT INTO mfa_recovery_codes (user_id, code_hash, created_at) VALUES (?, 'rc-hash', ?)", env.targetID, now); err != nil {
		t.Fatalf("seed recovery code: %v", err)
	}

	resp, out := env.deactivate(t, env.actorTok, env.actorCSRF, env.targetID, "key-full-success", map[string]string{
		"confirm": confirmPhrase(env.targetID),
		"reason":  "disposable test account cleanup",
	})
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("expected 200, got %d: %v", resp.StatusCode, out)
	}
	if out["active"] != false {
		t.Fatalf("expected active=false in response, got %v", out["active"])
	}
	if rid, _ := out["request_id"].(string); rid == "" {
		t.Fatalf("expected non-empty request_id")
	}

	var active int
	if err := env.db.QueryRow("SELECT active FROM users WHERE id = ?", env.targetID).Scan(&active); err != nil {
		t.Fatalf("read active: %v", err)
	}
	if active != 0 {
		t.Fatalf("expected active=0, got %d", active)
	}

	var sessCount int
	_ = env.db.QueryRow("SELECT COUNT(*) FROM sessions WHERE user_id = ?", env.targetID).Scan(&sessCount)
	if sessCount != 0 {
		t.Fatalf("expected 0 sessions, got %d", sessCount)
	}

	var keyActive int
	_ = env.db.QueryRow("SELECT active FROM api_keys WHERE user_id = ?", env.targetID).Scan(&keyActive)
	if keyActive != 0 {
		t.Fatalf("expected api key active=0, got %d", keyActive)
	}

	var unusedRecoveryCodes int
	_ = env.db.QueryRow("SELECT COUNT(*) FROM mfa_recovery_codes WHERE user_id = ? AND used_at IS NULL", env.targetID).Scan(&unusedRecoveryCodes)
	if unusedRecoveryCodes != 0 {
		t.Fatalf("expected 0 unused recovery codes, got %d", unusedRecoveryCodes)
	}

	// password login must now fail for the deactivated account.
	loginBody, _ := json.Marshal(map[string]string{
		"email":    pulTargetEmail,
		"password": pulTargetPass,
	})
	loginReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(loginBody))
	loginReq.Header.Set("Content-Type", "application/json")
	loginResp, err := env.router.App().Test(loginReq, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("login attempt: %v", err)
	}
	if loginResp.StatusCode == fiber.StatusOK {
		t.Fatalf("expected login to fail for deactivated user, got 200")
	}

	var auditCount int
	_ = env.db.QueryRow("SELECT COUNT(*) FROM coremail_audit WHERE action = 'platform_user.deactivate'").Scan(&auditCount)
	if auditCount != 1 {
		t.Fatalf("expected exactly 1 audit entry, got %d", auditCount)
	}
}
