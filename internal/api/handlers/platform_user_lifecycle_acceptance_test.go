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
)

type platformUserLifecycleEnv struct {
	router    *api.Router
	db        *sql.DB
	actorTok  string
	actorCSRF string
	actorID   uint
	targetID  uint
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
	return &platformUserLifecycleEnv{
		router:    router,
		db:        sqlDB,
		actorTok:  actorTok,
		actorCSRF: importRouteCSRF(t, router, actorTok),
		actorID:   actorID,
		targetID:  targetID,
	}
}

func (e *platformUserLifecycleEnv) deactivate(t *testing.T, token, csrf string, id uint, body interface{}) (*http.Response, map[string]interface{}) {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	}
	path := "/api/v1/platform/users/" + itoa(int64(id)) + "/deactivate"
	req := httptest.NewRequest(http.MethodPost, path, rdr)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if csrf != "" {
		req.Header.Set("Cookie", "csrf_token="+csrf)
		req.Header.Set("X-CSRF-Token", csrf)
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

func TestDeactivatePlatformUser_SelfTargetRejected(t *testing.T) {
	env := buildPlatformUserLifecycleEnv(t)
	resp, out := env.deactivate(t, env.actorTok, env.actorCSRF, env.actorID, map[string]string{
		"confirm": "DEACTIVATE-USER-" + itoa(int64(env.actorID)),
		"reason":  "test",
	})
	if resp.StatusCode != fiber.StatusConflict {
		t.Fatalf("expected 409, got %d: %v", resp.StatusCode, out)
	}
}

func TestDeactivatePlatformUser_WrongConfirmationRejected(t *testing.T) {
	env := buildPlatformUserLifecycleEnv(t)
	resp, _ := env.deactivate(t, env.actorTok, env.actorCSRF, env.targetID, map[string]string{
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
	resp, _ := env.deactivate(t, env.actorTok, env.actorCSRF, env.targetID, map[string]string{
		"confirm": "DEACTIVATE-USER-" + itoa(int64(env.targetID)),
		"reason":  "",
	})
	if resp.StatusCode != fiber.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestDeactivatePlatformUser_MissingCSRFRejected(t *testing.T) {
	env := buildPlatformUserLifecycleEnv(t)
	resp, _ := env.deactivate(t, env.actorTok, "", env.targetID, map[string]string{
		"confirm": "DEACTIVATE-USER-" + itoa(int64(env.targetID)),
		"reason":  "test",
	})
	if resp.StatusCode != fiber.StatusForbidden {
		t.Fatalf("expected 403 (CSRF), got %d", resp.StatusCode)
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

	resp, out := env.deactivate(t, env.actorTok, env.actorCSRF, env.targetID, map[string]string{
		"confirm": "DEACTIVATE-USER-" + itoa(int64(env.targetID)),
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
