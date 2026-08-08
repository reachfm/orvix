package handlers_test

import (
	"bytes"
	"encoding/json"
	"strconv"
	"testing"
	"time"

	"github.com/orvix/orvix/internal/auth"
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
	var created struct {
		ID int64 `json:"id"`
	}
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
	var created struct {
		ID int64 `json:"id"`
	}
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
	var created struct {
		ID int64 `json:"id"`
	}
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
	_ = token

	// Seed a second canonical tenant_admin user directly (the admin-users
	// create handler only accepts legacy role strings, which canonical
	// token issuance now rejects at login).
	otherHash, _ := auth.HashPassword("TestPassword123!")
	now := time.Now().UTC()
	res, err := sqlDB.Exec(
		`INSERT INTO users (created_at, updated_at, email, password_hash, role, tenant_id, active, email_verified) VALUES (?, ?, 'other@test.local', ?, 'tenant_admin', 1, 1, 1)`,
		now, now, otherHash,
	)
	if err != nil {
		t.Fatalf("seed other admin: %v", err)
	}
	var other struct {
		ID int64 `json:"id"`
	}
	other.ID, _ = res.LastInsertId()

	// Demote the original admin so only one tenant_admin remains
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
// the auth layer fails closed before any handler runs. A broken users
// table prevents ValidateAccessToken from checking token_version, so
// the token is rejected as invalid (401) at the auth layer.
func TestAdminUsersDBErrorsNotLeaked(t *testing.T) {
	router, sqlDB := newEnterpriseRouter(t)
	token := enterpriseLoginForTest(t, router, "admin@test.local", "TestPassword123!")
	csrf := enterpriseCSRFForTest(t, router, token)

	if _, err := sqlDB.Exec("DROP TABLE users"); err != nil {
		t.Fatalf("drop table: %v", err)
	}

	// Auth middleware fails closed → 401 on token validation failure.
	resp := getJSON(t, router, "/api/v1/admin/admin-users", token)
	if resp.status != 401 {
		t.Fatalf("list with broken DB: want 401, got %d body=%s", resp.status, resp.body)
	}
	resp2 := postJSON(t, router, "/api/v1/admin/admin-users", token, csrf,
		`{"email":"x@test.local","password":"TestPass123!","role":"admin"}`)
	if resp2.status != 401 {
		t.Fatalf("create with broken DB: want 401, got %d body=%s", resp2.status, resp2.body)
	}
	resp3 := patchJSON(t, router, "/api/v1/admin/admin-users/2/status", token, csrf,
		`{"active":false}`)
	if resp3.status != 401 {
		t.Fatalf("status with broken DB: want 401, got %d body=%s", resp3.status, resp3.body)
	}
}
