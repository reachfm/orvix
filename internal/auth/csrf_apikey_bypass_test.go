package auth

// Regression coverage for the CSRF-coverage fix: CSRFManager.Middleware
// must skip the CSRF cookie/header check for requests authenticated via
// API key (auth_method == "apikey"), since API keys are Bearer tokens
// that browsers never attach automatically cross-site — CSRF protection
// is meaningless there, and requiring a cookie the API client was never
// issued would break every API-key integration the moment CSRF is
// enforced on the admin group by default. Session-authenticated (cookie)
// requests must still be rejected without a valid CSRF token.

import (
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v3"
)

func buildCSRFTestApp(t *testing.T, simulateAPIKey bool) *fiber.App {
	t.Helper()
	app := fiber.New()
	cm := NewCSRFManager(nil, testLogger(t), true)
	app.Use(func(c fiber.Ctx) error {
		if simulateAPIKey {
			c.Locals("auth_method", "apikey")
		}
		return c.Next()
	})
	app.Use(cm.Middleware())
	app.Post("/test", func(c fiber.Ctx) error {
		return c.SendStatus(200)
	})
	return app
}

func TestCSRFMiddleware_APIKeyRequestBypassesCSRF(t *testing.T) {
	app := buildCSRFTestApp(t, true)
	req := httptest.NewRequest("POST", "/test", nil)
	// Deliberately no csrf_token cookie or X-CSRF-Token header.
	resp, err := app.Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("api-key request should bypass CSRF, expected 200, got %d", resp.StatusCode)
	}
}

func TestCSRFMiddleware_SessionRequestStillRequiresCSRF(t *testing.T) {
	app := buildCSRFTestApp(t, false)
	req := httptest.NewRequest("POST", "/test", nil)
	resp, err := app.Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	if resp.StatusCode != 403 {
		t.Fatalf("session request without a CSRF token should be rejected, expected 403, got %d", resp.StatusCode)
	}
}
