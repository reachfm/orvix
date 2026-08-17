package handlers_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/auth"
)

// TestSignupRejectsExistingPlatformIdentityEmail is the Phase C regression:
// a signup attempt using an email that already belongs to a platform
// super admin / superadmin identity must create no tenant, user, or
// subscription row, must return a generic failure (not a role-revealing
// message), and must leave the platform identity provably unmutated.
func TestSignupRejectsExistingPlatformIdentityEmail(t *testing.T) {
	env := newSignupTxEnvSQLite(t)

	now := time.Now().UTC()
	platformEmail := "platform-admin@example.com"
	pwHash, err := auth.HashPassword("PlatformAdminPass!2026")
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	res, err := env.sqlDB.Exec(
		`INSERT INTO users (created_at, updated_at, email, password_hash, role, tenant_id, active, email_verified) VALUES (?, ?, ?, ?, 'platform_super_admin', NULL, 1, 1)`,
		now, now, platformEmail, pwHash,
	)
	if err != nil {
		t.Fatalf("seed platform identity: %v", err)
	}
	platformUserID, _ := res.LastInsertId()

	var beforeHash, beforeRole string
	if err := env.sqlDB.QueryRow(`SELECT password_hash, role FROM users WHERE id = ?`, platformUserID).Scan(&beforeHash, &beforeRole); err != nil {
		t.Fatalf("read platform identity before: %v", err)
	}

	payload, _ := json.Marshal(map[string]string{
		"email":    platformEmail,
		"password": "AttackerPass123",
		"name":     "Attacker Co",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/signup", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	resp, err := env.router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("signup request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("signup with platform-identity email status=%d, want 409", resp.StatusCode)
	}
	var body map[string]string
	json.NewDecoder(resp.Body).Decode(&body)
	if body["error"] == "" {
		t.Fatalf("expected an error message, got %v", body)
	}
	forbidden := []string{"admin", "platform", "superadmin", "super_admin"}
	lowerErr := strings.ToLower(body["error"])
	for _, word := range forbidden {
		if strings.Contains(lowerErr, word) {
			t.Fatalf("signup error message leaks account type (%q): %q", word, body["error"])
		}
	}

	// No new tenant/user/subscription rows.
	assertSignupCounts(t, env.sqlDB, platformEmail, "example.com", 0, 1 /* only the seeded platform user */, 0)

	// Platform identity is provably unmutated.
	var afterHash, afterRole string
	var afterTenant *int64
	if err := env.sqlDB.QueryRow(`SELECT password_hash, role, tenant_id FROM users WHERE id = ?`, platformUserID).Scan(&afterHash, &afterRole, &afterTenant); err != nil {
		t.Fatalf("read platform identity after: %v", err)
	}
	if afterHash != beforeHash || afterRole != beforeRole {
		t.Fatalf("platform identity mutated: before=(%s,%s) after=(%s,%s)", beforeHash, beforeRole, afterHash, afterRole)
	}
	if afterTenant != nil {
		t.Fatalf("platform identity gained a tenant_id: %v", *afterTenant)
	}
}
