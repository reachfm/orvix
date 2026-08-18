package handlers_test

// Focused diagnostic test for the FINAL-ENTERPRISE-COMPLETION
// live acceptance blocker:
//
//	PSA current password works for /auth/login
//	but is rejected by /auth/change-password as
//	"current password is incorrect".
//
// Root cause (proved from source):
//   - /auth/login used `sqlDB.QueryRow("SELECT password_hash FROM
//     users WHERE email = ?")` (raw SQL).
//   - /auth/change-password used
//     `h.db.Table("users").First(&user, userID)` (GORM, anonymous
//     struct), which silently returned no rows in some SQLite /
//     Postgres combinations, leaving `user.PasswordHash` empty.
//     The subsequent `h.auth.VerifyPassword(plain, "")` always
//     returned false.
//
// Fix applied in this pass:
//   - /auth/change-password now uses the same raw-SQL pattern as
//     /auth/login, plus a raw-SQL `UPDATE ... RowsAffected`-checked
//     write.
//   - Both handlers share ONE canonical password verifier
//     (auth.VerifyPasswordWithRehash, exposed via h.auth.VerifyPassword)
//     that supports both Argon2id (canonical) and bcrypt (legacy /
//     reset-script) hashes.
//
// This test pins the contract end-to-end: a password accepted by
// login MUST be accepted as current_password by change-password; the
// change must persist; the old password must stop working; the new
// password must work; /me still returns the SAME role.

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"

	"github.com/orvix/orvix/internal/auth"
)

// loginAs issues POST /auth/login and returns (status, accessToken).
func loginAsAdmin(t *testing.T, router *fiber.App, email, password string) (int, string) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"email": email, "password": password})
	req := httptest.NewRequest("POST", "/api/v1/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "10.99.1.1:50001"
	req.Header.Set("X-Forwarded-For", "10.99.1.1")
	resp, err := router.Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)

	tok := ""
	for _, c := range resp.Cookies() {
		if c.Name == "access_token" {
			tok = c.Value
			break
		}
	}
	if tok == "" {
		var body struct {
			AccessToken string `json:"access_token"`
		}
		_ = json.Unmarshal(respBody, &body)
		tok = body.AccessToken
	}
	return resp.StatusCode, tok
}

// csrfFor fetches /api/v1/csrf-token with the access_token cookie.
func csrfFor(t *testing.T, router *fiber.App, accessToken string) string {
	t.Helper()
	req := httptest.NewRequest("GET", "/api/v1/csrf-token", nil)
	req.Header.Set("Cookie", "access_token="+accessToken)
	req.Header.Set("Accept", "application/json")
	resp, err := router.Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("csrf-token: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("csrf-token: status=%d body=%s", resp.StatusCode, string(b))
	}
	var body struct {
		CSRFToken string `json:"csrf_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("csrf-token decode: %v", err)
	}
	if body.CSRFToken == "" {
		t.Fatal("csrf-token: empty")
	}
	return body.CSRFToken
}

// changePasswordAs issues POST /auth/change-password with the supplied
// access + csrf credentials. Returns (status, body).
func changePasswordAs(t *testing.T, router *fiber.App, accessToken, csrfToken, current, next string) (int, []byte) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{
		"current_password": current,
		"new_password":     next,
	})
	req := httptest.NewRequest("POST", "/api/v1/auth/change-password", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Cookie", "access_token="+accessToken+"; csrf_token="+csrfToken)
	req.Header.Set("X-CSRF-Token", csrfToken)
	resp, err := router.Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("change-password: %v", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, respBody
}

// TestChangePasswordConsistency_LoginAcceptedAsCurrentPassword
// proves the FINAL-ENTERPRISE-COMPLETION live acceptance blocker is
// fixed: a password accepted by /auth/login MUST be accepted as
// current_password by /auth/change-password. If this test fails, the
// bug has regressed.
func TestChangePasswordConsistency_LoginAcceptedAsCurrentPassword(t *testing.T) {
	env := newLoginAuthHarness(t)

	email := "psa-test@example.test"
	originalPass := "OriginalPass!2026"
	newPass := "UpdatedPass!2026"

	// Seed the user with a real Argon2id hash (the canonical Login
	// verifier supports this natively).
	hash, err := auth.HashPassword(originalPass)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	now := time.Now().UTC()
	if _, err := env.sqlDB.Exec(
		`INSERT INTO users (created_at, updated_at, email, password_hash, role, tenant_id, active, email_verified, deleted_at)
		 VALUES (?, ?, ?, ?, 'platform_super_admin', NULL, 1, 1, NULL)`,
		now, now, email, hash,
	); err != nil {
		t.Fatalf("seed user: %v", err)
	}

	// 1. login works with the original password.
	status, accessToken := loginAsAdmin(t, env.router.App(), email, originalPass)
	if status != http.StatusOK {
		t.Fatalf("login (original): want 200, got %d", status)
	}
	if accessToken == "" {
		t.Fatalf("login (original): empty access_token")
	}

	csrf := csrfFor(t, env.router.App(), accessToken)

	// 2. change-password accepts the SAME original as current_password.
	// This is the live acceptance blocker; before the fix the handler
	// returned 401 "current password is incorrect".
	cpStatus, cpBody := changePasswordAs(t, env.router.App(), accessToken, csrf, originalPass, newPass)
	if cpStatus != http.StatusOK {
		t.Fatalf("change-password (original): want 200, got %d body=%s", cpStatus, string(cpBody))
	}
	if !strings.Contains(strings.ToLower(string(cpBody)), "password changed") {
		t.Fatalf("change-password (original): unexpected body: %s", string(cpBody))
	}

	// 3. login with the OLD password now FAILS (password was actually
	// rotated; not a no-op).
	statusOld, _ := loginAsAdmin(t, env.router.App(), email, originalPass)
	if statusOld != http.StatusUnauthorized {
		t.Fatalf("login (old password): want 401, got %d", statusOld)
	}

	// 4. login with the NEW password succeeds.
	statusNew, _ := loginAsAdmin(t, env.router.App(), email, newPass)
	if statusNew != http.StatusOK {
		t.Fatalf("login (new password): want 200, got %d", statusNew)
	}

	// 5. /me (after re-login) still reports the SAME canonical role —
	// the change-password path did not silently flip any identity /
	// tenant binding.
	meStatus, meBody := meAs(t, env.router.App(), email, newPass)
	if meStatus != http.StatusOK {
		t.Fatalf("me (after change): want 200, got %d body=%s", meStatus, string(meBody))
	}
	var meDoc struct {
		Role   string `json:"role"`
		Portal string `json:"portal"`
	}
	_ = json.Unmarshal(meBody, &meDoc)
	if meDoc.Role != "platform_super_admin" {
		t.Fatalf("me (after change): role=%q, want platform_super_admin", meDoc.Role)
	}
	if meDoc.Portal != "platform" {
		t.Fatalf("me (after change): portal=%q, want platform (tenantless PSA)", meDoc.Portal)
	}
}

// meAs logs in and then fetches /me with the issued access token.
func meAs(t *testing.T, router *fiber.App, email, password string) (int, []byte) {
	t.Helper()
	_, tok := loginAsAdmin(t, router, email, password)
	if tok == "" {
		t.Fatal("meAs: empty token")
	}
	req := httptest.NewRequest("GET", "/api/v1/me", nil)
	req.Header.Set("Cookie", "access_token="+tok)
	resp, err := router.Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("me: %v", err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}
