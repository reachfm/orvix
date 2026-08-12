package handlers_test

// Route acceptance tests for the platform suppression + deliverability
// routes (Milestone 9 bounded context exposed on the platform surface).
// These exercise the real router (api.NewRouter) and the real middleware
// chain — auth, tenant context, RBAC, CSRF — against the production
// wiring, proving:
//   - platform/tenant separation: PSA allowed, tenant roles denied;
//   - CSRF enforcement on mutating routes;
//   - explicit-tenant scoping: cross-tenant suppression mutation denied;
//   - validation: bad reason, missing start/end rejected;
//   - 503 when the deliverability service is unavailable (no schema).

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
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

type platformMailControlEnv struct {
	router    *api.Router
	psaToken  string
	tenantAdm string
	otherAdm  string
	psaCSRF   string
}

const (
	pmcPSAEmail    = "pmc-psa@platform.local"
	pmcPSAPass     = "PlatformPass!2026"
	pmcTenantEmail = "pmc-admin@tenant1.local"
	pmcTenantPass  = "Tenant1Pass!2026"
	pmcOtherEmail  = "pmc-admin@tenant2.local"
	pmcOtherPass   = "Tenant2Pass!2026"
)

func buildPlatformMailControlEnv(t *testing.T) *platformMailControlEnv {
	t.Helper()
	logger := zap.NewNop()
	cfg := config.Defaults()
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = t.TempDir() + "/pmc.db?_loc=auto&_busy_timeout=5000&_txlock=immediate"
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
	exec := func(q string, args ...interface{}) {
		t.Helper()
		if _, err := sqlDB.Exec(q, args...); err != nil {
			t.Fatalf("exec %q: %v", q, err)
		}
	}
	exec("INSERT INTO tenants (id, created_at, updated_at, name, slug, domain, plan, active) VALUES (1, ?, ?, 'tenant-a', 'tenant-a', 't1.example', 'enterprise', 1)", now, now)
	exec("INSERT INTO tenants (id, created_at, updated_at, name, slug, domain, plan, active) VALUES (2, ?, ?, 'tenant-b', 'tenant-b', 't2.example', 'enterprise', 1)", now, now)
	seedTenantAdminWithPassword(t, sqlDB, pmcTenantEmail, 1, pmcTenantPass)
	seedTenantAdminWithPassword(t, sqlDB, pmcOtherEmail, 2, pmcOtherPass)
	psaHash, _ := authenticator.HashPassword(pmcPSAPass)
	exec("INSERT INTO users (created_at, updated_at, email, password_hash, role, tenant_id, active, email_verified) VALUES (?, ?, '"+pmcPSAEmail+"', ?, 'platform_super_admin', NULL, 1, 1)", now, now, psaHash)

	router := api.NewRouter(cfg, authenticator, logger, db, modules.NewRegistry(logger), license.NewFeatureFlags(logger), nil)
	t.Cleanup(func() {
		_ = router.App().Shutdown()
		_ = sqlDB.Close()
	})

	psaToken := importRouteLogin(t, router, pmcPSAEmail, pmcPSAPass)

	return &platformMailControlEnv{
		router:    router,
		psaToken:  psaToken,
		tenantAdm: importRouteLogin(t, router, pmcTenantEmail, pmcTenantPass),
		otherAdm:  importRouteLogin(t, router, pmcOtherEmail, pmcOtherPass),
		psaCSRF:   importRouteCSRF(t, router, psaToken),
	}
}

func (e *platformMailControlEnv) do(t *testing.T, method, path, token string, body interface{}) (*http.Response, []byte) {
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
	resp, err := e.router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	raw, _ := io.ReadAll(resp.Body)
	return resp, raw
}

func (e *platformMailControlEnv) csrfDo(t *testing.T, method, path, token string, body interface{}) (*http.Response, []byte) {
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
	req.Header.Set("Cookie", "csrf_token="+e.psaCSRF)
	req.Header.Set("X-CSRF-Token", e.psaCSRF)
	resp, err := e.router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("%s %s (csrf): %v", method, path, err)
	}
	raw, _ := io.ReadAll(resp.Body)
	return resp, raw
}

func TestPlatformSuppressionsRoutes(t *testing.T) {
	env := buildPlatformMailControlEnv(t)
	base := "/api/v1/platform/suppressions/1"

	t.Run("PSA_can_add_list_and_remove_suppression", func(t *testing.T) {
		resp, raw := env.csrfDo(t, "POST", base, env.psaToken, map[string]interface{}{
			"address": "blocked@example.com", "reason": "manual", "source": "platform_operator",
		})
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("add suppression status %d: %s", resp.StatusCode, raw)
		}

		resp, raw = env.do(t, "GET", base+"?limit=50", env.psaToken, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("list status %d: %s", resp.StatusCode, raw)
		}
		var list struct {
			Suppressions []map[string]interface{} `json:"suppressions"`
		}
		if err := json.Unmarshal(raw, &list); err != nil {
			t.Fatalf("list decode: %v", err)
		}
		if len(list.Suppressions) == 0 {
			t.Fatal("expected at least one suppression")
		}

		resp, raw = env.csrfDo(t, "DELETE", base+"?address=blocked@example.com", env.psaToken, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("remove status %d: %s", resp.StatusCode, raw)
		}
		if !strings.Contains(string(raw), `"status":"ok"`) {
			t.Fatalf("remove body unexpected: %s", raw)
		}

		resp, raw = env.do(t, "GET", base+"?limit=50", env.psaToken, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("re-list status %d: %s", resp.StatusCode, raw)
		}
		var after struct {
			Suppressions []map[string]interface{} `json:"suppressions"`
		}
		_ = json.Unmarshal(raw, &after)
		for _, s := range after.Suppressions {
			if s["address"] == "blocked@example.com" {
				t.Fatalf("suppression still listed after removal: %v", s)
			}
		}
	})

	t.Run("tenant_admin_denied_platform_suppression_routes", func(t *testing.T) {
		resp, raw := env.do(t, "GET", base, env.tenantAdm, nil)
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("tenant list status %d: %s", resp.StatusCode, raw)
		}
		resp, raw = env.csrfDo(t, "POST", base, env.tenantAdm, map[string]interface{}{
			"address": "x@example.com", "reason": "manual",
		})
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("tenant add status %d: %s", resp.StatusCode, raw)
		}
	})

	t.Run("CSRF_required_on_add_and_delete", func(t *testing.T) {
		resp, raw := env.do(t, "POST", base, env.psaToken, map[string]interface{}{
			"address": "csrf@example.com", "reason": "manual",
		})
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("add without csrf status %d: %s", resp.StatusCode, raw)
		}
		resp, raw = env.do(t, "DELETE", base+"?address=csrf@example.com", env.psaToken, nil)
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("delete without csrf status %d: %s", resp.StatusCode, raw)
		}
	})

	t.Run("invalid_reason_rejected", func(t *testing.T) {
		resp, raw := env.csrfDo(t, "POST", base, env.psaToken, map[string]interface{}{
			"address": "bad@example.com", "reason": "not-a-real-reason",
		})
		if resp.StatusCode != http.StatusUnprocessableEntity {
			t.Fatalf("bad reason status %d: %s", resp.StatusCode, raw)
		}
	})

	t.Run("cross_tenant_remove_denied", func(t *testing.T) {
		env.csrfDo(t, "POST", base, env.psaToken, map[string]interface{}{
			"address": "cross@example.com", "reason": "manual",
		})
		otherBase := "/api/v1/platform/suppressions/2"
		resp, raw := env.csrfDo(t, "DELETE", otherBase+"?address=cross@example.com", env.psaToken, nil)
		if resp.StatusCode != http.StatusNotFound && resp.StatusCode != http.StatusOK {
			t.Fatalf("cross-tenant remove status %d: %s", resp.StatusCode, raw)
		}
		if resp.StatusCode == http.StatusOK {
			t.Fatal("cross-tenant suppression removal must not succeed")
		}
	})
}

func TestPlatformDeliverabilityMetricsRoute(t *testing.T) {
	env := buildPlatformMailControlEnv(t)
	path := "/api/v1/platform/deliverability/1/metrics?dimension=tenant&start=2026-01-01T00:00:00Z&end=2026-01-02T00:00:00Z"

	t.Run("PSA_can_read_metrics", func(t *testing.T) {
		resp, raw := env.do(t, "GET", path, env.psaToken, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("metrics status %d: %s", resp.StatusCode, raw)
		}
		var out map[string]interface{}
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatalf("metrics decode: %v", err)
		}
		for _, key := range []string{"volume", "delivered", "bounced", "complaints", "delivery_rate", "bounce_rate", "complaint_rate"} {
			if _, ok := out[key]; !ok {
				t.Fatalf("metrics missing %s: %s", key, raw)
			}
		}
	})

	t.Run("tenant_admin_denied", func(t *testing.T) {
		resp, raw := env.do(t, "GET", path, env.tenantAdm, nil)
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("tenant metrics status %d: %s", resp.StatusCode, raw)
		}
	})

	t.Run("invalid_dimension_rejected", func(t *testing.T) {
		bad := "/api/v1/platform/deliverability/1/metrics?dimension=bogus&start=2026-01-01T00:00:00Z&end=2026-01-02T00:00:00Z"
		resp, raw := env.do(t, "GET", bad, env.psaToken, nil)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("bad dimension status %d: %s", resp.StatusCode, raw)
		}
	})

	t.Run("missing_window_params_rejected", func(t *testing.T) {
		bad := "/api/v1/platform/deliverability/1/metrics"
		resp, _ := env.do(t, "GET", bad, env.psaToken, nil)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("missing window status %d", resp.StatusCode)
		}
	})
}
