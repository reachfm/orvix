package handlers_test

// Shared test-side CSRF material for the webmail suites.
//
// H-1 put the canonical CSRF middleware in front of every cookie-authenticated
// webmail mutation. The existing webmail test clients authenticate with a
// `Cookie: access_token=...` header — i.e. exactly the ambient-credential
// shape a browser uses — so they must now also carry the double-submit CSRF
// material, just like the real SPA does.
//
// These helpers mint a REAL token from the public bootstrap endpoint through
// the real router. Nothing here bypasses or weakens the middleware: the
// negative tests in webmail_csrf_test.go prove that a client which omits this
// material is rejected.

import (
	"encoding/json"
	"io"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v3"

	"github.com/orvix/orvix/internal/api"
)

// mintWebmailCSRF fetches a CSRF token from GET /api/v1/csrf-token, returning
// the token value (which the caller must send BOTH as the csrf_token cookie
// and as the X-CSRF-Token header — the double-submit contract).
func mintWebmailCSRF(t *testing.T, router *api.Router, accessToken string) string {
	t.Helper()
	req := httptest.NewRequest("GET", "/api/v1/csrf-token", nil)
	if accessToken != "" {
		req.Header.Set("Cookie", "access_token="+accessToken)
	}
	resp, err := router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("csrf bootstrap: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("csrf bootstrap: expected 200, got %d: %s", resp.StatusCode, string(body))
	}
	for _, c := range resp.Cookies() {
		if c.Name == "csrf_token" {
			return c.Value
		}
	}
	// Fall back to the JSON body for routers that only return the token.
	body, _ := io.ReadAll(resp.Body)
	var out struct {
		CSRFToken string `json:"csrf_token"`
	}
	_ = json.Unmarshal(body, &out)
	if out.CSRFToken == "" {
		t.Fatal("csrf bootstrap: no csrf_token cookie or body field")
	}
	return out.CSRFToken
}

// isWebmailMutation reports whether a method needs CSRF material.
func isWebmailMutation(method string) bool {
	switch method {
	case "GET", "HEAD", "OPTIONS":
		return false
	}
	return true
}

// webmailCookieAndCSRF builds the Cookie header value and the X-CSRF-Token
// header value a browser client would send for the given method. For safe
// methods it returns the bare access-token cookie and an empty CSRF header.
func webmailCookieAndCSRF(t *testing.T, router *api.Router, method, accessToken string) (string, string) {
	t.Helper()
	if accessToken == "" {
		return "", ""
	}
	if !isWebmailMutation(method) {
		return "access_token=" + accessToken, ""
	}
	token := mintWebmailCSRF(t, router, accessToken)
	return "access_token=" + accessToken + "; csrf_token=" + token, token
}
