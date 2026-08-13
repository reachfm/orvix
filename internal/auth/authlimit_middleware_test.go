package auth

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"go.uber.org/zap"
)

// Middleware-level verification of the combo dimension (same (ip, account)
// pair) using a custom policy, through a real Fiber app. The router-level
// acceptance tests cover ip and account end-to-end with production policy;
// combo is the tightest budget and needs a custom policy to be observable in
// isolation (with production policy, the account budget trips at the same
// count and would mask it).
func TestAuthLimitMiddlewareComboDimension(t *testing.T) {
	policy := AuthLimitPolicy{IPMax: 100, AccountMax: 100, ComboMax: 5, Window: 15 * time.Minute}
	limiter := NewAuthLimiter(nil, policy, zap.NewNop())
	app := fiber.New()
	app.Post("/probe", AuthLimitMiddleware(limiter, CredentialAccountFromBody, zap.NewNop()), func(c fiber.Ctx) error {
		return c.SendStatus(fiber.StatusOK)
	})

	req := func() *http.Response {
		httpreq := httptest.NewRequest("POST", "/probe", strings.NewReader(`{"email":"victim@example.com","password":"secret"}`))
		httpreq.Header.Set("Content-Type", "application/json")
		resp, err := app.Test(httpreq)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}

	for i := 1; i <= 5; i++ {
		if r := req(); r.StatusCode != fiber.StatusOK {
			t.Fatalf("attempt %d: want 200, got %d", i, r.StatusCode)
		}
	}
	if r := req(); r.StatusCode != fiber.StatusTooManyRequests {
		t.Fatalf("attempt 6: want 429 (combo budget), got %d", r.StatusCode)
	}
}

// TestAuthLimitMiddlewareResponseShape guards the single opaque 429 shape
// shared by every dimension: identical body, Retry-After present, no
// dimension leak.
func TestAuthLimitMiddlewareResponseShape(t *testing.T) {
	policy := AuthLimitPolicy{IPMax: 1, AccountMax: 1, ComboMax: 1, Window: 15 * time.Minute}
	limiter := NewAuthLimiter(nil, policy, zap.NewNop())
	app := fiber.New()
	app.Post("/probe", AuthLimitMiddleware(limiter, CredentialAccountFromBody, zap.NewNop()), func(c fiber.Ctx) error {
		return c.SendStatus(fiber.StatusOK)
	})

	body := `{"email":"victim@example.com","password":"secret"}`
	send := func() *http.Response {
		httpreq := httptest.NewRequest("POST", "/probe", strings.NewReader(body))
		httpreq.Header.Set("Content-Type", "application/json")
		resp, err := app.Test(httpreq)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	if r := send(); r.StatusCode != fiber.StatusOK {
		t.Fatalf("first: want 200, got %d", r.StatusCode)
	}
	refused := send()
	if refused.StatusCode != fiber.StatusTooManyRequests {
		t.Fatalf("second: want 429, got %d", refused.StatusCode)
	}
	if got := refused.Header.Get("Retry-After"); got != "900" {
		t.Fatalf("Retry-After = %q, want 900", got)
	}
	if got := refused.Header.Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
	gotBodyBytes, err := io.ReadAll(refused.Body)
	if err != nil {
		t.Fatal(err)
	}
	gotBody := string(gotBodyBytes)
	for _, leak := range []string{"account", "combo", "ip", "dimension"} {
		if strings.Contains(gotBody, leak) {
			t.Fatalf("429 body leaks %q: %s", leak, gotBody)
		}
	}
}

// TestAuthLimitMiddlewareMalformedBodyStillCountsIp: a body the extractor
// cannot parse yields no account dimension, but the IP budget still applies —
// garbage input cannot mint unlimited budgets.
func TestAuthLimitMiddlewareMalformedBodyStillCountsIp(t *testing.T) {
	policy := AuthLimitPolicy{IPMax: 2, AccountMax: 100, ComboMax: 100, Window: 15 * time.Minute}
	limiter := NewAuthLimiter(nil, policy, zap.NewNop())
	app := fiber.New()
	app.Post("/probe", AuthLimitMiddleware(limiter, CredentialAccountFromBody, zap.NewNop()), func(c fiber.Ctx) error {
		return c.SendStatus(fiber.StatusOK)
	})

	send := func(payload string) *http.Response {
		httpreq := httptest.NewRequest("POST", "/probe", strings.NewReader(payload))
		httpreq.Header.Set("Content-Type", "application/json")
		resp, err := app.Test(httpreq)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	// Two malformed bodies from the same IP consume the IP budget; the third
	// must be refused regardless of how the body changes.
	if r := send(`{not json`); r.StatusCode != fiber.StatusOK {
		t.Fatalf("malformed 1: want 200, got %d", r.StatusCode)
	}
	if r := send(`[1,2,3]`); r.StatusCode != fiber.StatusOK {
		t.Fatalf("malformed 2: want 200, got %d", r.StatusCode)
	}
	if r := send(`{"email":"x@example.com","password":"p"}`); r.StatusCode != fiber.StatusTooManyRequests {
		t.Fatalf("third: want 429 (ip budget), got %d", r.StatusCode)
	}
}
