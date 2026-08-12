package auth

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v3"
)

func TestPublicMiddlewareRequiresTenantAPIKeyAndExportsIdentity(t *testing.T) {
	m := newRotationManager(t)
	key, rec, err := m.Generate("public", 7, 11, string(RoleTenantAdmin), []string{"public:domains:read"}, 1)
	if err != nil {
		t.Fatal(err)
	}
	app := fiber.New()
	app.Get("/public", m.PublicMiddleware(), func(c fiber.Ctx) error {
		scopes, _ := c.Locals("api_key_scopes").(map[string]struct{})
		_, hasScope := scopes["public:domains:read"]
		return c.JSON(fiber.Map{
			"tenant_id": c.Locals("api_key_tenant_id"),
			"key_id":    c.Locals("api_key_id"),
			"has_scope": hasScope,
		})
	})

	for _, authz := range []string{"", "Bearer not-an-orvix-key"} {
		req := httptest.NewRequest("GET", "/public", nil)
		if authz != "" {
			req.Header.Set("Authorization", authz)
		}
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != fiber.StatusUnauthorized {
			t.Fatalf("auth %q status=%d, want 401", authz, resp.StatusCode)
		}
	}

	req := httptest.NewRequest("GET", "/public", nil)
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	var body struct {
		TenantID uint `json:"tenant_id"`
		KeyID    uint `json:"key_id"`
		HasScope bool `json:"has_scope"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.TenantID != 11 || body.KeyID != rec.ID || !body.HasScope {
		t.Fatalf("unexpected identity: %+v", body)
	}
}

func TestPublicMiddlewareRejectsRevokedAndPlatformKeys(t *testing.T) {
	m := newRotationManager(t)
	revoked, rec, err := m.Generate("revoked", 7, 11, string(RoleTenantAdmin), []string{"public:domains:read"}, 1)
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := m.db.DB()
	if err != nil {
		t.Fatal(err)
	}
	res, err := sqlDB.Exec("UPDATE api_keys SET active = 0 WHERE key_prefix = ?", rec.KeyPrefix)
	if err != nil {
		t.Fatal(err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		t.Fatalf("revocation affected %d rows", n)
	}
	platform, _, err := m.Generate("platform", 1, 11, string(RolePlatformSuperAdmin), []string{"public:domains:read"}, 1)
	if err != nil {
		t.Fatal(err)
	}
	app := fiber.New()
	app.Get("/public", m.PublicMiddleware(), func(c fiber.Ctx) error { return c.SendStatus(204) })
	for label, key := range map[string]string{"revoked": revoked, "platform": platform} {
		req := httptest.NewRequest("GET", "/public", nil)
		req.Header.Set("Authorization", "Bearer "+key)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != fiber.StatusUnauthorized {
			t.Fatalf("%s status=%d, want 401", label, resp.StatusCode)
		}
	}
}

func TestPublicMiddlewareRetainsAllowedIPEnforcement(t *testing.T) {
	m := newRotationManager(t)
	key, rec, err := m.Generate("restricted", 7, 11, string(RoleTenantAdmin), []string{"public:domains:read"}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.SetAllowedIPs(rec.ID, 7, "203.0.113.0/24"); err != nil {
		t.Fatal(err)
	}
	app := fiber.New()
	app.Get("/public", m.PublicMiddleware(), func(c fiber.Ctx) error { return c.SendStatus(204) })
	req := httptest.NewRequest("GET", "/public", nil)
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != fiber.StatusUnauthorized {
		t.Fatalf("status=%d, want 401", resp.StatusCode)
	}
}
