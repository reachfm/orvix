package api

// Route-level proof for the dedicated MFA/account rate limiter: it returns 429
// with a real integer-seconds Retry-After header once the per-user budget is
// exceeded, isolates users, and resets after its window — using the exact
// factory the router wires onto /account/mfa/*.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
)

func newLimiterApp(max int, window time.Duration, retrySecs int) *fiber.App {
	lim := newUserRateLimiter("mfa_req", max, window, retrySecs, "too many MFA attempts, please try again later")
	app := fiber.New()
	// Simulate the authenticated caller: user id comes from a test header.
	app.Use(func(c fiber.Ctx) error {
		if u := c.Get("X-Test-User"); u != "" {
			c.Locals("user_id", uint(u[0]-'0'))
		}
		return c.Next()
	})
	app.Post("/account/mfa/verify", lim, func(c fiber.Ctx) error {
		return c.JSON(fiber.Map{"ok": true})
	})
	return app
}

func hit(t *testing.T, app *fiber.App, user string) (int, string, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/account/mfa/verify", nil)
	req.Header.Set("X-Test-User", user)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	return resp.StatusCode, resp.Header.Get("Retry-After"), body
}

func TestMFARateLimiterThresholdAndHeader(t *testing.T) {
	app := newLimiterApp(3, time.Minute, 900)

	// First 3 requests for user 1 pass.
	for i := 0; i < 3; i++ {
		if code, _, _ := hit(t, app, "1"); code != http.StatusOK {
			t.Fatalf("request %d: got %d, want 200", i+1, code)
		}
	}
	// 4th is throttled with 429 + a real integer-seconds Retry-After header.
	code, retry, body := hit(t, app, "1")
	if code != http.StatusTooManyRequests {
		t.Fatalf("over-limit: got %d, want 429", code)
	}
	if retry != "900" {
		t.Fatalf("Retry-After header: got %q, want \"900\"", retry)
	}
	if v, ok := body["retry_after"].(float64); !ok || v != 900 {
		t.Fatalf("stable JSON error: retry_after=%v", body["retry_after"])
	}
	if body["error"] == nil {
		t.Fatalf("stable JSON error body missing: %v", body)
	}
}

func TestMFARateLimiterUserIsolation(t *testing.T) {
	app := newLimiterApp(3, time.Minute, 900)
	for i := 0; i < 3; i++ {
		hit(t, app, "1")
	}
	// User 1 is now throttled...
	if code, _, _ := hit(t, app, "1"); code != http.StatusTooManyRequests {
		t.Fatalf("user 1 should be throttled: got %d", code)
	}
	// ...but user 2 has its own independent budget.
	if code, _, _ := hit(t, app, "2"); code != http.StatusOK {
		t.Fatalf("user 2 must not be affected by user 1's limit: got %d", code)
	}
}

func TestMFARateLimiterResetsAfterWindow(t *testing.T) {
	app := newLimiterApp(2, 1*time.Second, 900)
	for i := 0; i < 2; i++ {
		hit(t, app, "1")
	}
	if code, _, _ := hit(t, app, "1"); code != http.StatusTooManyRequests {
		t.Fatalf("user 1 should be throttled before reset: got %d", code)
	}
	// After the window elapses the budget resets.
	time.Sleep(1300 * time.Millisecond)
	if code, _, _ := hit(t, app, "1"); code != http.StatusOK {
		t.Fatalf("limiter must reset after its window: got %d", code)
	}
}
