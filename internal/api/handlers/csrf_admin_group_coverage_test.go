package handlers_test

// Regression coverage for the CSRF-coverage fix: several state-changing
// admin routes (compliance policy writes, collaboration mailbox writes,
// legal holds, etc.) were previously mounted directly on the `admin`
// route group with no CSRF middleware at all, forgeable from a
// third-party page since the admin session cookie is sent automatically.
// The fix moves CSRF enforcement onto the whole `admin` group
// (deny-by-default) instead of only the `men` sub-group routes authors
// remembered to nest inside it. These tests exercise the real router +
// real CSRF middleware — no bypass — against a route that was
// previously unprotected (POST /api/v1/compliance/policies).

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v3"
)

func TestCSRF_CompliancePoliciesRejectsMissingToken(t *testing.T) {
	e := buildAdminDomainAdvancedEnv(t)

	req := httptest.NewRequest("POST", "/api/v1/compliance/policies", bytes.NewReader([]byte("{}")))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Cookie", "access_token="+e.adminToken)
	// Deliberately no csrf_token cookie / X-CSRF-Token header.
	resp, err := e.router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 403 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 403 (CSRF required) without a token, got %d: %s", resp.StatusCode, string(body))
	}
}

func TestCSRF_CompliancePoliciesAcceptsValidToken(t *testing.T) {
	e := buildAdminDomainAdvancedEnv(t)

	status, body := domainReq(t, e, "POST", "/api/v1/compliance/policies", e.adminToken, e.csrfToken, map[string]interface{}{})
	if status != 201 {
		t.Fatalf("expected 201 with a valid CSRF token, got %d: %v", status, body)
	}
}

func TestCSRF_CollaborationMailboxesRejectsMissingToken(t *testing.T) {
	e := buildAdminDomainAdvancedEnv(t)

	payload, _ := json.Marshal(map[string]interface{}{})
	req := httptest.NewRequest("POST", "/api/v1/collaboration/mailboxes", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Cookie", "access_token="+e.adminToken)
	resp, err := e.router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 403 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 403 (CSRF required) without a token, got %d: %s", resp.StatusCode, string(b))
	}
}

// TestCSRF_AdminGroupGETStillWorksWithoutToken pins down that the fix
// does not accidentally start requiring a CSRF token on read-only admin
// GET routes (csrf.Middleware already no-ops on GET, but this proves it
// end to end through the real route tree after moving the middleware up
// to wrap the whole admin group).
func TestCSRF_AdminGroupGETStillWorksWithoutToken(t *testing.T) {
	e := buildAdminDomainAdvancedEnv(t)

	req := httptest.NewRequest("GET", "/api/v1/domains", nil)
	req.Header.Set("Cookie", "access_token="+e.adminToken)
	resp, err := e.router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200 for a GET route without any CSRF token, got %d: %s", resp.StatusCode, string(b))
	}
}
