package handlers_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/auth"
)

// TestPlatformScheduleOrganizationDeletion_RequiresPlatformAdmin is the
// Phase G auth-enforcement regression: a plain tenant owner (role=user,
// not a platform admin) must be rejected by
// POST /api/v1/platform/organizations/:id/deletion, same as every other
// /platform/organizations/* route (platformMW gating, not weakened here).
func TestPlatformScheduleOrganizationDeletion_RequiresPlatformAdmin(t *testing.T) {
	env := newSignupTxEnvSQLite(t)
	now := time.Now().UTC()

	res, err := env.sqlDB.Exec(`INSERT INTO tenants (name, slug, domain, plan, active, created_at, updated_at) VALUES ('victim', 'victim', 'victim.example', 'free', 1, ?, ?)`, now, now)
	if err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	tenantID, _ := res.LastInsertId()

	pw, err := auth.HashPassword("OwnerPass!2026")
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	if _, err := env.sqlDB.Exec(`INSERT INTO users (created_at, updated_at, email, password_hash, role, tenant_id, active, email_verified) VALUES (?, ?, 'owner@victim.example', ?, 'user', ?, 1, 1)`, now, now, pw, tenantID); err != nil {
		t.Fatalf("seed owner user: %v", err)
	}

	loginPayload, _ := json.Marshal(map[string]string{"username": "owner@victim.example", "password": "OwnerPass!2026"})
	loginReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(loginPayload))
	loginReq.Header.Set("Content-Type", "application/json")
	loginResp, err := env.router.App().Test(loginReq, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	defer loginResp.Body.Close()
	var loginOut struct {
		AccessToken string `json:"access_token"`
	}
	json.NewDecoder(loginResp.Body).Decode(&loginOut)
	if loginOut.AccessToken == "" {
		t.Fatalf("login: empty access token, status=%d", loginResp.StatusCode)
	}

	body, _ := json.Marshal(map[string]string{"confirm_domain": "victim.example", "reason": "attempted privilege escalation"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/platform/organizations/"+strconv.FormatInt(tenantID, 10)+"/deletion", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+loginOut.AccessToken)
	resp, err := env.router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("schedule deletion request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		t.Fatalf("non-platform-admin was able to schedule org deletion, status=%d", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401/403 for non-platform-admin caller, got %d", resp.StatusCode)
	}

	var pending int64
	env.sqlDB.QueryRow(`SELECT COUNT(*) FROM org_deletions WHERE organization_id = ?`, tenantID).Scan(&pending)
	if pending != 0 {
		t.Fatalf("deletion was scheduled despite auth rejection: %d org_deletions rows", pending)
	}
}
