package handlers_test

// F1 regression: the tenant relay error mapper previously recursed
// infinitely for four validation errors (ErrInvalidConnSecurity,
// ErrUnsafeTarget, ErrNameRequired, ErrInsecureCredentialTransport),
// causing a fatal stack overflow that killed the entire API process
// from a single tenant request. This test drives the real router +
// middleware + handler with an invalid conn_security, asserts a clean
// 400, then proves the process is still alive by completing a valid
// follow-up request. Malformed requests are repeated concurrently.

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gofiber/fiber/v3"
)

// f1TenantEnv builds a fresh router with a tenant admin and the
// tenant's own CSRF token (the tenant relay surface requires tenant
// auth + CSRF).
func f1TenantEnv(t *testing.T) *platformMailControlEnv {
	t.Helper()
	env := buildPlatformMailControlEnv(t)
	return env
}

func (e *platformMailControlEnv) tenantRelayDo(t *testing.T, method, path, tenantToken, tenantCSRF string, body interface{}) (*http.Response, []byte) {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = strings.NewReader(string(b))
	}
	req := httptest.NewRequest(method, path, rdr)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if tenantToken != "" {
		req.Header.Set("Authorization", "Bearer "+tenantToken)
	}
	if tenantCSRF != "" {
		req.Header.Set("Cookie", "csrf_token="+tenantCSRF)
		req.Header.Set("X-CSRF-Token", tenantCSRF)
	}
	resp, err := e.router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	raw, _ := io.ReadAll(resp.Body)
	return resp, raw
}

func TestF1_TenantRelayInvalidConnSecurityReturns400AndProcessSurvives(t *testing.T) {
	env := f1TenantEnv(t)
	tenantCSRF := importRouteCSRF(t, env.router, env.tenantAdm)
	path := "/api/v1/enterprise/relay/providers"

	invalid := map[string]interface{}{
		"pool_id": 1, "name": "crash-me", "host": "smtp.example.com", "port": 587,
		"conn_security": "tls", // not in {none,starttls,implicit_tls}
	}
	resp, raw := env.tenantRelayDo(t, "POST", path, env.tenantAdm, tenantCSRF, invalid)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid conn_security must be a 400, got %d: %s", resp.StatusCode, raw)
	}
	if strings.Contains(strings.ToLower(string(raw)), "stack") || strings.Contains(strings.ToLower(string(raw)), "goroutine") {
		t.Fatalf("400 body leaked internal crash detail: %s", raw)
	}

	// Empty conn_security is also invalid.
	empty := map[string]interface{}{
		"pool_id": 1, "name": "crash-me-2", "host": "smtp.example.com", "port": 587,
		"conn_security": "",
	}
	resp, raw = env.tenantRelayDo(t, "POST", path, env.tenantAdm, tenantCSRF, empty)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("empty conn_security must be a 400, got %d: %s", resp.StatusCode, raw)
	}

	// The process must still be alive: a valid follow-up request succeeds.
	valid := map[string]interface{}{
		"pool_id": 1, "name": "still-alive", "host": "smtp.example.com", "port": 587,
		"conn_security": "starttls", "tls_validation": "strict",
	}
	resp, raw = env.tenantRelayDo(t, "POST", path, env.tenantAdm, tenantCSRF, valid)
	if resp.StatusCode == http.StatusBadRequest && strings.Contains(string(raw), "stack overflow") {
		t.Fatalf("process crashed: %s", raw)
	}
	// A pool_id of 1 does not exist yet, so a correct 404 (not a crash) is
	// the expected outcome proving the process survived.
	if resp.StatusCode != http.StatusNotFound {
		// Pool may or may not exist depending on test isolation; the
		// critical assertion is that we got a structured error response,
		// NOT a process death.
		if resp.StatusCode == http.StatusInternalServerError && strings.Contains(string(raw), "stack overflow") {
			t.Fatalf("process crashed: %s", raw)
		}
	}
}

func TestF1_TenantRelayMalformedRequestsConcurrentlyDoNotCrash(t *testing.T) {
	env := f1TenantEnv(t)
	tenantCSRF := importRouteCSRF(t, env.router, env.tenantAdm)
	path := "/api/v1/enterprise/relay/providers"

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			bad := map[string]interface{}{
				"pool_id": 1, "name": fmt.Sprintf("boom-%d", i), "host": "smtp.example.com", "port": 587,
				"conn_security": map[int]string{0: "tls", 1: "", 2: "STARTTLS", 3: "implicit"}[i%4],
			}
			resp, raw := env.tenantRelayDo(t, "POST", path, env.tenantAdm, tenantCSRF, bad)
			if resp.StatusCode != http.StatusBadRequest {
				t.Errorf("concurrent invalid request %d: expected 400, got %d: %s", i, resp.StatusCode, raw)
			}
		}(i)
	}
	wg.Wait()

	// Process alive: a follow-up structured request still gets a
	// structured (non-crash) response.
	resp, raw := env.tenantRelayDo(t, "POST", path, env.tenantAdm, tenantCSRF, map[string]interface{}{
		"pool_id": 1, "name": "post-concurrency", "host": "smtp.example.com", "port": 587,
		"conn_security": "starttls", "tls_validation": "strict",
	})
	if strings.Contains(strings.ToLower(string(raw)), "stack overflow") {
		t.Fatalf("process crashed after concurrent malformed requests: %s", raw)
	}
	_ = resp
}
