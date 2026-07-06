package handlers_test

import (
	"strings"
	"testing"
)

// TestLoginProtectionStatusPersistenceOK verifies that when
// LoadFromDB succeeds (normal test setup), the status endpoint
// returns persistence = "db" and persistence_ok = true.
func TestLoginProtectionStatusPersistenceOK(t *testing.T) {
	router, _ := newEnterpriseRouter(t)
	token := enterpriseLoginForTest(t, router, "admin@test.local", "TestPassword123!")

	resp := getJSON(t, router, "/api/v1/admin/login-protection/status", token)
	if resp.status != 200 {
		t.Fatalf("status: want 200, got %d %s", resp.status, resp.body)
	}
	body := resp.body
	if !strings.Contains(body, `"persistence":"db"`) {
		t.Errorf("expected persistence=db, body=%s", body)
	}
	if !strings.Contains(body, `"persistence_ok":true`) {
		t.Errorf("expected persistence_ok=true, body=%s", body)
	}
	if strings.Contains(body, "persistence_error") {
		t.Errorf("persistence_error should not be present when ok, body=%s", body)
	}
}

// TestLoginProtectionStatusPersistenceDegradedFromLoadFailure
// verifies the REAL production degraded path. A malformed
// coremail_lockouts table is created BEFORE api.NewRouter runs.
// When NewRouter's trust wiring code calls LoadFromDB, the
// SELECT key, expires_at FROM coremail_lockouts query fails
// because the table has the wrong columns. The production error
// branch at router.go:273-275 calls SetTrustPersistence(false,
// sanitizedMessage). This test does NOT manually re-wire trust,
// does NOT call SetTrustPersistence directly, and uses zero
// exported/global mutable test seams.
func TestLoginProtectionStatusPersistenceDegradedFromLoadFailure(t *testing.T) {
	router, _ := newEnterpriseRouterWithMalformedLockouts(t)
	token := enterpriseLoginForTest(t, router, "admin@test.local", "TestPassword123!")

	resp := getJSON(t, router, "/api/v1/admin/login-protection/status", token)
	if resp.status != 200 {
		t.Fatalf("status: want 200, got %d %s", resp.status, resp.body)
	}
	body := resp.body

	if !strings.Contains(body, `"persistence":"in_memory"`) {
		t.Errorf("expected persistence=in_memory, body=%s", body)
	}
	if !strings.Contains(body, `"persistence_ok":false`) {
		t.Errorf("expected persistence_ok=false, body=%s", body)
	}
	if !strings.Contains(body, "persistence_error") {
		t.Errorf("expected persistence_error field, body=%s", body)
	}

	for _, banned := range []string{"SELECT", "sqlite", "no such column", "syntax error", "driver", "stack trace", "DSN", "password", "secret"} {
		if strings.Contains(strings.ToLower(body), strings.ToLower(banned)) {
			t.Errorf("persistence_error must not contain %q; body=%s", banned, body)
		}
	}
}

// TestLoginProtectionStatusPersistenceErrorSanitized verifies the
// persistence_error field does not leak raw DB internals.
func TestLoginProtectionStatusPersistenceErrorSanitized(t *testing.T) {
	router, _ := newEnterpriseRouter(t)
	router.SetTrustPersistence(false, "Trust engine persistence load failed. Lockouts are tracked in-memory and reset on restart.")
	token := enterpriseLoginForTest(t, router, "admin@test.local", "TestPassword123!")

	resp := getJSON(t, router, "/api/v1/admin/login-protection/status", token)
	if resp.status != 200 {
		t.Fatalf("status: want 200, got %d %s", resp.status, resp.body)
	}
	body := resp.body
	for _, banned := range []string{"SQL", "sqlite", "no such table", "syntax", "driver", "stack trace"} {
		if strings.Contains(strings.ToLower(body), strings.ToLower(banned)) {
			t.Errorf("persistence_error must not contain %q; body=%s", banned, body)
		}
	}
}

func TestLoginProtectionStatusUnauthorized(t *testing.T) {
	router, _ := newEnterpriseRouter(t)
	resp := getJSON(t, router, "/api/v1/admin/login-protection/status", "")
	if resp.status != 401 {
		t.Fatalf("unauthorized status: want 401, got %d %s", resp.status, resp.body)
	}
}

func TestLoginProtectionLockoutsList(t *testing.T) {
	router, _ := newEnterpriseRouter(t)
	token := enterpriseLoginForTest(t, router, "admin@test.local", "TestPassword123!")

	resp := getJSON(t, router, "/api/v1/admin/login-protection/lockouts", token)
	if resp.status != 200 {
		t.Fatalf("list lockouts: want 200, got %d %s", resp.status, resp.body)
	}
}

func TestLoginProtectionLockoutsUnauthorized(t *testing.T) {
	router, _ := newEnterpriseRouter(t)
	resp := getJSON(t, router, "/api/v1/admin/login-protection/lockouts", "")
	if resp.status != 401 {
		t.Fatalf("unauthorized: want 401, got %d %s", resp.status, resp.body)
	}
}

func TestLoginProtectionClearLockoutUnauthorized(t *testing.T) {
	router, _ := newEnterpriseRouter(t)
	resp := postJSON(t, router, "/api/v1/admin/login-protection/lockouts/test-key/clear", "", "", "")
	if resp.status != 401 {
		t.Fatalf("unauthorized clear: want 401, got %d %s", resp.status, resp.body)
	}
}
