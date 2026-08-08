package handlers_test

import (
	"database/sql"
	"fmt"
	"net/http"
	"testing"
)

// ── Direct production-path mutation tests ────────────────────────

func TestAdminUserRoleChangeBumpsVersionAndRevokesToken(t *testing.T) {
	router, sqlDB := newEnterpriseRouter(t)
	// Seed target with known login.
	seedTenantAdminWithPassword(t, sqlDB, "target@test.local", 1, "TargetPass!2026")
	// Set role and token_version after seed.
	sqlDB.Exec("UPDATE users SET role = 'tenant_support', token_version = 10 WHERE email = 'target@test.local'")
	var targetID int64
	sqlDB.QueryRow("SELECT id FROM users WHERE email = 'target@test.local'").Scan(&targetID)
	// Login as target to get a real token (embedded token_version=10).
	targetTok := enterpriseLoginForTest(t, router, "target@test.local", "TargetPass!2026")
	// Admin calls UpdateAdminUser to change role.
	adminTok := enterpriseLoginForTest(t, router, "admin@test.local", "TestPassword123!")
	csrf := enterpriseCSRFForTest(t, router, adminTok)
	resp := patchJSON(t, router, fmt.Sprintf("/api/v1/admin/admin-users/%d", targetID), adminTok, csrf, `{"role":"tenant_operator"}`)
	if resp.status != 200 {
		t.Fatalf("role change: want 200, got %d body=%s", resp.status, resp.body)
	}
	var role string
	var tv int64
	sqlDB.QueryRow("SELECT role, COALESCE(token_version,0) FROM users WHERE id = ?", targetID).Scan(&role, &tv)
	if role != "tenant_operator" {
		t.Fatalf("stored role: want tenant_operator, got %s", role)
	}
	if tv != 11 {
		t.Fatalf("token_version: want 11, got %d", tv)
	}
	// Old target token must be rejected on a protected endpoint.
	req, _ := http.NewRequest("GET", "/api/v1/domains", nil)
	req.Header.Set("Authorization", "Bearer "+targetTok)
	r, err := router.App().Test(req)
	if err != nil {
		t.Fatalf("old token test: %v", err)
	}
	if r.StatusCode != http.StatusUnauthorized && r.StatusCode != http.StatusForbidden {
		t.Fatalf("old token after role change: want 401/403, got %d", r.StatusCode)
	}
	if r.StatusCode >= 500 {
		t.Fatalf("old token 5xx: %d", r.StatusCode)
	}
}

func TestAdminUserProfileOnlyUpdateDoesNotBumpVersion(t *testing.T) {
	router, sqlDB := newEnterpriseRouter(t)
	seedTenantAdminWithPassword(t, sqlDB, "profile@test.local", 1, "ProfilePass!2026")
	sqlDB.Exec("UPDATE users SET token_version = 20 WHERE email = 'profile@test.local'")
	var targetID int64
	sqlDB.QueryRow("SELECT id FROM users WHERE email = 'profile@test.local'").Scan(&targetID)
	adminTok := enterpriseLoginForTest(t, router, "admin@test.local", "TestPassword123!")
	csrf := enterpriseCSRFForTest(t, router, adminTok)
	// Update only email (not role/active/deleted).
	resp := patchJSON(t, router, fmt.Sprintf("/api/v1/admin/admin-users/%d", targetID), adminTok, csrf, `{"email":"profile2@test.local"}`)
	if resp.status != 200 {
		t.Fatalf("profile update: want 200, got %d body=%s", resp.status, resp.body)
	}
	var tv int64
	sqlDB.QueryRow("SELECT COALESCE(token_version,0) FROM users WHERE id = ?", targetID).Scan(&tv)
	if tv != 20 {
		t.Fatalf("token_version: want 20, got %d", tv)
	}
}

func TestAdminUserStatusChangeBumpsVersionAndRevokesToken(t *testing.T) {
	router, sqlDB := newEnterpriseRouter(t)
	seedTenantAdminWithPassword(t, sqlDB, "status@test.local", 1, "StatusPass!2026")
	sqlDB.Exec("UPDATE users SET token_version = 30 WHERE email = 'status@test.local'")
	var targetID int64
	sqlDB.QueryRow("SELECT id FROM users WHERE email = 'status@test.local'").Scan(&targetID)
	targetTok := enterpriseLoginForTest(t, router, "status@test.local", "StatusPass!2026")
	adminTok := enterpriseLoginForTest(t, router, "admin@test.local", "TestPassword123!")
	csrf := enterpriseCSRFForTest(t, router, adminTok)
	resp := patchJSON(t, router, fmt.Sprintf("/api/v1/admin/admin-users/%d/status", targetID), adminTok, csrf, `{"active":false}`)
	if resp.status != 200 {
		t.Fatalf("status change: want 200, got %d body=%s", resp.status, resp.body)
	}
	var active bool
	var tv int64
	sqlDB.QueryRow("SELECT active, COALESCE(token_version,0) FROM users WHERE id = ?", targetID).Scan(&active, &tv)
	if active {
		t.Fatal("active must be false")
	}
	if tv != 31 {
		t.Fatalf("token_version: want 31, got %d", tv)
	}
	req, _ := http.NewRequest("GET", "/api/v1/domains", nil)
	req.Header.Set("Authorization", "Bearer "+targetTok)
	r, err := router.App().Test(req)
	if err != nil {
		t.Fatalf("old token test: %v", err)
	}
	if r.StatusCode != http.StatusUnauthorized && r.StatusCode != http.StatusForbidden {
		t.Fatalf("old token after status change: want 401/403, got %d", r.StatusCode)
	}
	if r.StatusCode >= 500 {
		t.Fatalf("old token 5xx: %d", r.StatusCode)
	}
}

func TestAdminUserDeleteBumpsVersionAndRevokesToken(t *testing.T) {
	router, sqlDB := newEnterpriseRouter(t)
	seedTenantAdminWithPassword(t, sqlDB, "del@test.local", 1, "DeletePass!2026")
	sqlDB.Exec("UPDATE users SET token_version = 40 WHERE email = 'del@test.local'")
	var targetID int64
	sqlDB.QueryRow("SELECT id FROM users WHERE email = 'del@test.local'").Scan(&targetID)
	targetTok := enterpriseLoginForTest(t, router, "del@test.local", "DeletePass!2026")
	adminTok := enterpriseLoginForTest(t, router, "admin@test.local", "TestPassword123!")
	csrf := enterpriseCSRFForTest(t, router, adminTok)
	resp := delJSON(t, router, fmt.Sprintf("/api/v1/admin/admin-users/%d", targetID), adminTok, csrf)
	if resp.status != 200 {
		t.Fatalf("delete: want 200, got %d body=%s", resp.status, resp.body)
	}
	var active bool
	var deletedAt sql.NullTime
	var tv int64
	sqlDB.QueryRow("SELECT active, deleted_at, COALESCE(token_version,0) FROM users WHERE id = ?", targetID).Scan(&active, &deletedAt, &tv)
	if active {
		t.Fatal("active must be false after soft-delete")
	}
	if !deletedAt.Valid {
		t.Fatal("deleted_at must be set after soft-delete")
	}
	if tv != 41 {
		t.Fatalf("token_version: want 41, got %d", tv)
	}
	req, _ := http.NewRequest("GET", "/api/v1/domains", nil)
	req.Header.Set("Authorization", "Bearer "+targetTok)
	r, err := router.App().Test(req)
	if err != nil {
		t.Fatalf("old token test: %v", err)
	}
	if r.StatusCode != http.StatusUnauthorized && r.StatusCode != http.StatusForbidden {
		t.Fatalf("old token after delete: want 401/403, got %d", r.StatusCode)
	}
	if r.StatusCode >= 500 {
		t.Fatalf("old token 5xx: %d", r.StatusCode)
	}
}
