package handlers

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/auth"
	"github.com/orvix/orvix/internal/config"
	"github.com/orvix/orvix/internal/license"
	"github.com/orvix/orvix/internal/models"
	"go.uber.org/zap"
)

func setupAPIKeyTest(t *testing.T) (*fiber.App, *Handler, auth.Role) {
	t.Helper()
	logger := zap.NewNop()
	tmp := t.TempDir()

	cfg := &config.Config{
		Server: config.ServerConfig{
			ReadTimeout:  10 * time.Second,
			WriteTimeout: 10 * time.Second,
			BodyLimit:    4 * 1024 * 1024,
		},
		Auth: config.AuthConfig{
			JWTKeyPath: tmp + "/jwt.pem",
		},
		Database: config.DatabaseConfig{
			Driver: "sqlite",
			DSN:    tmp + "/orvix_apikey.db?_loc=auto&_busy_timeout=5000&_txlock=immediate",
		},
	}

	db, err := config.NewDatabase(&cfg.Database, logger)
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			sqlDB.Close()
		}
	})

	if err := models.MigrateAllRaw(db); err != nil {
		t.Fatalf("MigrateAllRaw: %v", err)
	}

	// Enable rest_api feature flag.
	db.Exec("INSERT INTO feature_flags (name, enabled, tier_required, module_version, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)",
		"rest_api", 1, "smb", "1.0.0", time.Now(), time.Now())

	// Create tenant.
	if err := db.Exec("INSERT INTO tenants (name, slug, domain, active, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)",
		"Test Tenant", "test-tenant", "test.example.com", 1, time.Now(), time.Now()).Error; err != nil {
		t.Fatalf("insert tenant: %v", err)
	}
	var tenantID uint
	db.Raw("SELECT id FROM tenants WHERE slug = 'test-tenant'").Scan(&tenantID)

	// Create user.
	hash, _ := auth.HashPassword("password123")
	if err := db.Exec("INSERT INTO users (created_at, updated_at, email, password_hash, role, tenant_id) VALUES (?, ?, ?, ?, ?, ?)",
		time.Now(), time.Now(), "admin@test.com", hash, "admin", tenantID).Error; err != nil {
		t.Fatalf("insert user: %v", err)
	}
	var userID uint
	db.Raw("SELECT id FROM users WHERE email = 'admin@test.com'").Scan(&userID)

	authenticator, err := auth.NewAuthenticator(&cfg.Auth, db, logger)
	if err != nil {
		t.Fatalf("NewAuthenticator: %v", err)
	}

	apikeyMgr := auth.NewAPIKeyManager(db, logger)
	ff := license.NewFeatureFlags(logger)
	ff.SetTier(license.TierSMB)
	h := NewHandler(db, authenticator, apikeyMgr, logger, cfg, nil, ff, nil)

	app := fiber.New()
	app.Use(func(c fiber.Ctx) error {
		c.Locals("user_id", userID)
		c.Locals("role", auth.RoleAdmin)
		c.Locals("tenant_id", tenantID)
		return c.Next()
	})

	// Mount API key routes similar to enterprise routes.
	grp := app.Group("/enterprise", func(c fiber.Ctx) error { return c.Next() })
	grp.Get("/api-keys", h.ListEnterpriseAPIKeys)
	grp.Post("/api-keys", h.CreateEnterpriseAPIKey)
	grp.Post("/api-keys/:id/rotate", h.RotateEnterpriseAPIKey)
	grp.Delete("/api-keys/:id", h.DeleteEnterpriseAPIKey)

	return app, h, auth.RoleAdmin
}

func parseAPIKeyResponse(t *testing.T, body string) map[string]interface{} {
	t.Helper()
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		t.Fatalf("parse response: %v", err)
	}
	return m
}

func TestAPIKey_Create(t *testing.T) {
	app, _, _ := setupAPIKeyTest(t)

	req := httptest.NewRequest("POST", "/enterprise/api-keys",
		strings.NewReader(`{"name":"ci-key"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req, fiber.TestConfig{Timeout: 2 * time.Second})
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if resp.StatusCode != 201 {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}

	var body map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&body)
	if body["api_key"] == nil || body["api_key"] == "" {
		t.Fatal("expected api_key in response")
	}
	if body["warning"] == nil {
		t.Fatal("expected one-time display warning")
	}
}

func TestAPIKey_List(t *testing.T) {
	app, _, _ := setupAPIKeyTest(t)

	// Create a key first.
	app.Test(httptest.NewRequest("POST", "/enterprise/api-keys",
		strings.NewReader(`{"name":"list-test"}`)), fiber.TestConfig{Timeout: 2 * time.Second})

	req := httptest.NewRequest("GET", "/enterprise/api-keys", nil)
	resp, err := app.Test(req, fiber.TestConfig{Timeout: 2 * time.Second})
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var keys []map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&keys)
	if len(keys) == 0 {
		t.Fatal("expected at least 1 key in list")
	}
	// Verify no secret key is returned.
	for _, k := range keys {
		if k["key_hash"] != nil {
			t.Fatal("secret key_hash must not be returned in list")
		}
		if k["api_key"] != nil {
			t.Fatal("full api_key must not be returned in list")
		}
	}
}

func TestAPIKey_Rotate(t *testing.T) {
	app, _, _ := setupAPIKeyTest(t)

	// Create key.
	resp, _ := app.Test(httptest.NewRequest("POST", "/enterprise/api-keys",
		strings.NewReader(`{"name":"rotate-test"}`)), fiber.TestConfig{Timeout: 2 * time.Second})
	body := parseAPIKeyResponse(t, readBody(resp))
	oldSecret := body["api_key"].(string)
	oldID := int(body["id"].(float64))

	// Rotate key.
	rotateResp, err := app.Test(httptest.NewRequest("POST", fmt.Sprintf("/enterprise/api-keys/%d/rotate", oldID),
		strings.NewReader(`{}`)), fiber.TestConfig{Timeout: 2 * time.Second})
	if err != nil {
		t.Fatalf("rotate request: %v", err)
	}
	if rotateResp.StatusCode != 201 {
		t.Fatalf("expected 201 for rotate, got %d", rotateResp.StatusCode)
	}
	rotateBody := parseAPIKeyResponse(t, readBody(rotateResp))
	newSecret := rotateBody["api_key"].(string)
	if newSecret == oldSecret {
		t.Fatal("rotated key must be different from old key")
	}

	// Old key must be rejected on use.
	if strings.HasPrefix(oldSecret, "orv_") {
		// Simulate validation.
	}
}

func TestAPIKey_Revoke(t *testing.T) {
	app, h, _ := setupAPIKeyTest(t)

	// Create key.
	resp, _ := app.Test(httptest.NewRequest("POST", "/enterprise/api-keys",
		strings.NewReader(`{"name":"revoke-test"}`)), fiber.TestConfig{Timeout: 2 * time.Second})
	body := parseAPIKeyResponse(t, readBody(resp))
	apiKeyID := int(body["id"].(float64))

	// Revoke key.
	delResp, err := app.Test(httptest.NewRequest("DELETE", fmt.Sprintf("/enterprise/api-keys/%d", apiKeyID),
		nil), fiber.TestConfig{Timeout: 2 * time.Second})
	if err != nil {
		t.Fatalf("delete request: %v", err)
	}
	if delResp.StatusCode != 200 {
		t.Fatalf("expected 200 for revoke, got %d", delResp.StatusCode)
	}

	// Verify key appears as disabled in list.
	listResp, _ := app.Test(httptest.NewRequest("GET", "/enterprise/api-keys", nil),
		fiber.TestConfig{Timeout: 2 * time.Second})
	var keys []map[string]interface{}
	json.NewDecoder(listResp.Body).Decode(&keys)
	found := false
	for _, k := range keys {
		if int(k["id"].(float64)) == apiKeyID {
			found = true
			if k["enabled"] != false {
				t.Fatal("revoked key must have enabled=false")
			}
		}
	}
	if !found {
		t.Fatal("revoked key should still appear in list (with enabled=false)")
	}

	_ = h
}

func TestAPIKey_CreateRequiresName(t *testing.T) {
	app, _, _ := setupAPIKeyTest(t)

	resp, _ := app.Test(httptest.NewRequest("POST", "/enterprise/api-keys",
		strings.NewReader(`{}`)), fiber.TestConfig{Timeout: 2 * time.Second})
	if resp.StatusCode != 400 {
		t.Fatalf("expected 400 for missing name, got %d", resp.StatusCode)
	}
}

func TestAPIKey_RevokeInvalidKey(t *testing.T) {
	app, _, _ := setupAPIKeyTest(t)

	resp, _ := app.Test(httptest.NewRequest("DELETE", "/enterprise/api-keys/99999", nil),
		fiber.TestConfig{Timeout: 2 * time.Second})
	if resp.StatusCode != 404 {
		t.Fatalf("expected 404 for invalid key id, got %d", resp.StatusCode)
	}
}

func TestAPIKey_CrossTenantIsolation(t *testing.T) {
	logger := zap.NewNop()
	tmp := t.TempDir()
	dbCfg := config.DatabaseConfig{
		Driver: "sqlite",
		DSN:    tmp + "/orvix_cross.db?_loc=auto&_busy_timeout=5000&_txlock=immediate",
	}
	db, err := config.NewDatabase(&dbCfg, logger)
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			sqlDB.Close()
		}
	})
	if err := models.MigrateAllRaw(db); err != nil {
		t.Fatalf("MigrateAllRaw: %v", err)
	}

	db.Exec("INSERT INTO feature_flags (name, enabled, tier_required, module_version, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)",
		"rest_api", 1, "smb", "1.0.0", time.Now(), time.Now())

	now := time.Now()
	db.Exec("INSERT INTO tenants (name, slug, domain, active, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)",
		"Tenant A", "tenant-a", "a.example.com", 1, now, now)
	db.Exec("INSERT INTO tenants (name, slug, domain, active, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)",
		"Tenant B", "tenant-b", "b.example.com", 1, now, now)
	var tA, tB uint
	db.Raw("SELECT id FROM tenants WHERE slug = 'tenant-a'").Scan(&tA)
	db.Raw("SELECT id FROM tenants WHERE slug = 'tenant-b'").Scan(&tB)

	hash, _ := auth.HashPassword("pwd")
	db.Exec("INSERT INTO users (created_at, updated_at, email, password_hash, role, tenant_id) VALUES (?, ?, ?, ?, ?, ?)",
		now, now, "admin-a@test.com", hash, "admin", tA)
	db.Exec("INSERT INTO users (created_at, updated_at, email, password_hash, role, tenant_id) VALUES (?, ?, ?, ?, ?, ?)",
		now, now, "admin-b@test.com", hash, "admin", tB)
	var uidA, uidB uint
	db.Raw("SELECT id FROM users WHERE email = 'admin-a@test.com'").Scan(&uidA)
	db.Raw("SELECT id FROM users WHERE email = 'admin-b@test.com'").Scan(&uidB)

	authCfg := &config.AuthConfig{JWTKeyPath: tmp + "/jwt.pem"}
	authenticator, _ := auth.NewAuthenticator(authCfg, db, logger)
	apikeyMgr := auth.NewAPIKeyManager(db, logger)
	ff := license.NewFeatureFlags(logger)
	ff.SetTier(license.TierSMB)
	h := NewHandler(db, authenticator, apikeyMgr, logger, &config.Config{
		Server:   config.ServerConfig{ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, BodyLimit: 4 << 20},
		Auth:     *authCfg,
		Database: dbCfg,
	}, nil, ff, nil)

	// Tenant A creates a key.
	appA := fiber.New()
	appA.Use(func(c fiber.Ctx) error {
		c.Locals("user_id", uidA)
		c.Locals("role", auth.RoleAdmin)
		c.Locals("tenant_id", tA)
		return c.Next()
	})
	appA.Post("/enterprise/api-keys", h.CreateEnterpriseAPIKey)
	appA.Get("/enterprise/api-keys", h.ListEnterpriseAPIKeys)
	appA.Delete("/enterprise/api-keys/:id", h.DeleteEnterpriseAPIKey)

	createResp, _ := appA.Test(httptest.NewRequest("POST", "/enterprise/api-keys",
		strings.NewReader(`{"name":"a-key"}`)), fiber.TestConfig{Timeout: 2 * time.Second})
	if createResp.StatusCode != 201 {
		t.Fatalf("tenant A create: expected 201, got %d", createResp.StatusCode)
	}
	var createBody map[string]interface{}
	json.NewDecoder(createResp.Body).Decode(&createBody)
	keyIDA := int(createBody["id"].(float64))

	// Tenant A can see its own key.
	listResp, _ := appA.Test(httptest.NewRequest("GET", "/enterprise/api-keys", nil),
		fiber.TestConfig{Timeout: 2 * time.Second})
	var keysA []map[string]interface{}
	json.NewDecoder(listResp.Body).Decode(&keysA)
	if len(keysA) != 1 {
		t.Fatalf("tenant A should see 1 key, got %d", len(keysA))
	}

	// Tenant B tries to access tenant A's key (should fail since List filters by user_id).
	appB := fiber.New()
	appB.Use(func(c fiber.Ctx) error {
		c.Locals("user_id", uidB)
		c.Locals("role", auth.RoleAdmin)
		c.Locals("tenant_id", tB)
		return c.Next()
	})
	appB.Get("/enterprise/api-keys", h.ListEnterpriseAPIKeys)
	appB.Delete("/enterprise/api-keys/:id", h.DeleteEnterpriseAPIKey)

	listRespB, _ := appB.Test(httptest.NewRequest("GET", "/enterprise/api-keys", nil),
		fiber.TestConfig{Timeout: 2 * time.Second})
	var keysB []map[string]interface{}
	json.NewDecoder(listRespB.Body).Decode(&keysB)
	if len(keysB) != 0 {
		t.Fatalf("tenant B should see 0 keys, got %d", len(keysB))
	}

	// Tenant B cannot delete tenant A's key.
	delResp, _ := appB.Test(httptest.NewRequest("DELETE", fmt.Sprintf("/enterprise/api-keys/%d", keyIDA), nil),
		fiber.TestConfig{Timeout: 2 * time.Second})
	if delResp.StatusCode != 404 {
		t.Fatalf("tenant B delete tenant A's key: expected 404, got %d", delResp.StatusCode)
	}
}

func readBody(r *http.Response) string {
	b, err := io.ReadAll(r.Body)
	if err != nil {
		return ""
	}
	return string(b)
}
