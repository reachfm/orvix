package handlers_test

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
)

func TestAdminUsersCreateListGet(t *testing.T) {
	router, _ := newEnterpriseRouter(t)
	token := enterpriseLoginForTest(t, router, "admin@test.local", "TestPassword123!")
	csrf := enterpriseCSRFForTest(t, router, token)

	resp := postJSON(t, router, "/api/v1/admin/admin-users", token, csrf,
		`{"email":"staff@test.local","password":"TestPassword123!","role":"admin"}`)
	if resp.status != 201 {
		t.Fatalf("create: want 201, got %d %s", resp.status, resp.body)
	}
	var created struct{ ID int64 `json:"id"` }
	if err := json.Unmarshal(resp.bodyBytes, &created); err != nil || created.ID == 0 {
		t.Fatalf("parse created: %v body=%s", err, resp.body)
	}

	resp2 := getJSON(t, router, "/api/v1/admin/admin-users", token)
	if resp2.status != 200 {
		t.Fatalf("list: want 200, got %d %s", resp2.status, resp2.body)
	}
	if !bytes.Contains(resp2.bodyBytes, []byte("staff@test.local")) {
		t.Fatalf("list missing staff: %s", resp2.body)
	}

	resp3 := getJSON(t, router, "/api/v1/admin/admin-users/"+strconv.FormatInt(created.ID, 10), token)
	if resp3.status != 200 {
		t.Fatalf("get: want 200, got %d %s", resp3.status, resp3.body)
	}
	if !bytes.Contains(resp3.bodyBytes, []byte("staff@test.local")) {
		t.Fatalf("get missing email: %s", resp3.body)
	}

	resp4 := postJSON(t, router, "/api/v1/admin/admin-users", token, csrf,
		`{"email":"staff@test.local","password":"OtherPass123!","role":"admin"}`)
	if resp4.status != 409 {
		t.Fatalf("duplicate: want 409, got %d %s", resp4.status, resp4.body)
	}
}

func TestAdminUsersUpdateStatus(t *testing.T) {
	router, _ := newEnterpriseRouter(t)
	token := enterpriseLoginForTest(t, router, "admin@test.local", "TestPassword123!")
	csrf := enterpriseCSRFForTest(t, router, token)

	resp := postJSON(t, router, "/api/v1/admin/admin-users", token, csrf,
		`{"email":"staff2@test.local","password":"TestPassword123!","role":"admin"}`)
	if resp.status != 201 {
		t.Fatalf("create: %d %s", resp.status, resp.body)
	}
	var created struct{ ID int64 `json:"id"` }
	json.Unmarshal(resp.bodyBytes, &created)

	resp2 := patchJSON(t, router, "/api/v1/admin/admin-users/"+strconv.FormatInt(created.ID, 10)+"/status", token, csrf,
		`{"active":false}`)
	if resp2.status != 200 {
		t.Fatalf("disable: want 200, got %d %s", resp2.status, resp2.body)
	}

	resp3 := patchJSON(t, router, "/api/v1/admin/admin-users/"+strconv.FormatInt(created.ID, 10)+"/status", token, csrf,
		`{"active":true}`)
	if resp3.status != 200 {
		t.Fatalf("enable: want 200, got %d %s", resp3.status, resp3.body)
	}
}

func TestAdminUsersResetPassword(t *testing.T) {
	router, _ := newEnterpriseRouter(t)
	token := enterpriseLoginForTest(t, router, "admin@test.local", "TestPassword123!")
	csrf := enterpriseCSRFForTest(t, router, token)

	resp := postJSON(t, router, "/api/v1/admin/admin-users", token, csrf,
		`{"email":"staff3@test.local","password":"OldPass123!","role":"admin"}`)
	if resp.status != 201 {
		t.Fatalf("create: %d %s", resp.status, resp.body)
	}
	var created struct{ ID int64 `json:"id"` }
	json.Unmarshal(resp.bodyBytes, &created)

	resp2 := patchJSON(t, router, "/api/v1/admin/admin-users/"+strconv.FormatInt(created.ID, 10)+"/password", token, csrf,
		`{"password":"NewPass456!"}`)
	if resp2.status != 200 {
		t.Fatalf("reset pw: want 200, got %d %s", resp2.status, resp2.body)
	}

	resp3 := patchJSON(t, router, "/api/v1/admin/admin-users/"+strconv.FormatInt(created.ID, 10)+"/password", token, csrf,
		`{"password":"short"}`)
	if resp3.status != 400 {
		t.Fatalf("short pw: want 400, got %d %s", resp3.status, resp3.body)
	}
}

func TestAdminUsersLastSuperadminProtection(t *testing.T) {
	router, sqlDB := newEnterpriseRouter(t)
	token := enterpriseLoginForTest(t, router, "admin@test.local", "TestPassword123!")
	csrf := enterpriseCSRFForTest(t, router, token)

	// Create a second admin
	resp := postJSON(t, router, "/api/v1/admin/admin-users", token, csrf,
		`{"email":"other@test.local","password":"TestPassword123!","role":"superadmin"}`)
	if resp.status != 201 {
		t.Fatalf("create other admin: %d %s", resp.status, resp.body)
	}
	var other struct{ ID int64 `json:"id"` }
	json.Unmarshal(resp.bodyBytes, &other)

	// Demote the original admin so only one superadmin remains
	if _, err := sqlDB.Exec("UPDATE users SET role = 'user' WHERE email = 'admin@test.local'"); err != nil {
		t.Fatalf("demote: %v", err)
	}

	// Other admin tries to disable self (should be rejected even without superadmin check)
	token2 := enterpriseLoginForTest(t, router, "other@test.local", "TestPassword123!")
	csrf2 := enterpriseCSRFForTest(t, router, token2)
	resp2 := patchJSON(t, router, "/api/v1/admin/admin-users/"+strconv.FormatInt(other.ID, 10)+"/status", token2, csrf2,
		`{"active":false}`)
	if resp2.status != 409 && resp2.status != 403 {
		t.Fatalf("self-disable: want 409/403, got %d %s", resp2.status, resp2.body)
	}
}

func TestAdminUsersSelfDisableProtection(t *testing.T) {
	router, _ := newEnterpriseRouter(t)
	token := enterpriseLoginForTest(t, router, "admin@test.local", "TestPassword123!")
	csrf := enterpriseCSRFForTest(t, router, token)

	// The seed admin cannot disable self
	resp := patchJSON(t, router, "/api/v1/admin/admin-users/1/status", token, csrf,
		`{"active":false}`)
	if resp.status != 409 {
		t.Fatalf("self-disable: want 409, got %d %s", resp.status, resp.body)
	}
}

func TestAdminUsersSelfDeleteProtection(t *testing.T) {
	router, _ := newEnterpriseRouter(t)
	token := enterpriseLoginForTest(t, router, "admin@test.local", "TestPassword123!")
	csrf := enterpriseCSRFForTest(t, router, token)

	// The seed admin cannot delete self
	resp := delJSON(t, router, "/api/v1/admin/admin-users/1", token, csrf)
	if resp.status != 409 {
		t.Fatalf("self-delete: want 409, got %d %s", resp.status, resp.body)
	}
}

func TestAdminUsersUnauthorized(t *testing.T) {
	router, _ := newEnterpriseRouter(t)
	resp := getJSON(t, router, "/api/v1/admin/admin-users", "")
	if resp.status != 401 {
		t.Fatalf("unauthorized: want 401, got %d %s", resp.status, resp.body)
	}
}

// TestAdminUsersDBErrorsNotLeaked verifies that when DB errors occur,
// the API response does not leak raw SQL/driver internals.
func TestAdminUsersDBErrorsNotLeaked(t *testing.T) {
	router, sqlDB := newEnterpriseRouter(t)
	// Login BEFORE breaking the DB so we have a valid token
	token := enterpriseLoginForTest(t, router, "admin@test.local", "TestPassword123!")
	csrf := enterpriseCSRFForTest(t, router, token)

	// Now drop the users table so queries fail with errors
	if _, err := sqlDB.Exec("DROP TABLE users"); err != nil {
		t.Fatalf("drop table: %v", err)
	}

	// List — should get 500 with generic error
	resp := getJSON(t, router, "/api/v1/admin/admin-users", token)
	if resp.status != 500 {
		t.Fatalf("list with broken DB: want 500, got %d body=%s", resp.status, resp.body)
	}
	for _, banned := range []string{"SQL", "sqlite", "no such table", "syntax", "database", "driver"} {
		if strings.Contains(strings.ToLower(resp.body), strings.ToLower(banned)) {
			t.Errorf("list error leaked %q; body=%s", banned, resp.body)
		}
	}

	// Create — should get 500 with generic error
	resp2 := postJSON(t, router, "/api/v1/admin/admin-users", token, csrf,
		`{"email":"x@test.local","password":"TestPass123!","role":"admin"}`)
	if resp2.status != 500 {
		t.Fatalf("create with broken DB: want 500, got %d body=%s", resp2.status, resp2.body)
	}
	for _, banned := range []string{"SQL", "sqlite", "no such table", "syntax", "database", "driver"} {
		if strings.Contains(strings.ToLower(resp2.body), strings.ToLower(banned)) {
			t.Errorf("create error leaked %q; body=%s", banned, resp2.body)
		}
	}

	// Update status — should get 500 with generic error
	resp3 := patchJSON(t, router, "/api/v1/admin/admin-users/2/status", token, csrf,
		`{"active":false}`)
	if resp3.status != 500 {
		t.Fatalf("status with broken DB: want 500, got %d body=%s", resp3.status, resp3.body)
	}
	for _, banned := range []string{"SQL", "sqlite", "no such table", "syntax", "database", "driver"} {
		if strings.Contains(strings.ToLower(resp3.body), strings.ToLower(banned)) {
			t.Errorf("status error leaked %q; body=%s", banned, resp3.body)
		}
	}
}
