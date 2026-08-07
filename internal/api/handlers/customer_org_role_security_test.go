package handlers_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"go.uber.org/zap"

	"github.com/orvix/orvix/internal/api"
	"github.com/orvix/orvix/internal/auth"
	"github.com/orvix/orvix/internal/config"
	"github.com/orvix/orvix/internal/license"
	"github.com/orvix/orvix/internal/models"
	"github.com/orvix/orvix/internal/modules"
)

type memberRoleHarness struct {
	router   *api.Router
	sqlDB    *sql.DB
	tenantA  uint
	targetID uint
}

func newMemberRoleHarness(t *testing.T) *memberRoleHarness {
	t.Helper()
	cfg := config.Defaults()
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = filepath.Join(t.TempDir(), "mrole.db") + "?_loc=auto&_busy_timeout=5000&_journal_mode=WAL"
	cfg.Auth.JWTSecret = "test-secret-64-bytes-member-role-http-security-fixture-XXXX"
	cfg.Auth.JWTKeyPath = filepath.Join(t.TempDir(), "jwt.pem")

	logger := zap.NewNop()
	gdb, err := config.NewDatabase(&cfg.Database, logger)
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	if err := models.MigrateAllRaw(gdb); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	sqlDB, _ := gdb.DB()
	t.Cleanup(func() { sqlDB.Close() })

	authn, err := auth.NewAuthenticator(&cfg.Auth, gdb, logger)
	if err != nil {
		t.Fatalf("authn: %v", err)
	}

	adminUI := filepath.Join(t.TempDir(), "admin")
	webmailUI := filepath.Join(t.TempDir(), "webmail")
	os.MkdirAll(adminUI, 0755)
	os.MkdirAll(webmailUI, 0755)
	os.WriteFile(filepath.Join(adminUI, "index.html"), []byte("<html></html>"), 0644)
	os.WriteFile(filepath.Join(webmailUI, "index.html"), []byte("<html></html>"), 0644)
	os.WriteFile(filepath.Join(webmailUI, "webmail.js"), []byte(""), 0644)
	os.WriteFile(filepath.Join(webmailUI, "webmail.css"), []byte(""), 0644)
	cfg.Server.AdminUIDir = adminUI
	cfg.Server.WebmailUIDir = webmailUI

	router := api.NewRouter(cfg, authn, logger, gdb, modules.NewRegistry(logger), license.NewFeatureFlags(logger), nil)
	t.Cleanup(func() { router.App().Shutdown() })

	now := time.Now().UTC()
	// Tenant.
	res, _ := sqlDB.Exec(`INSERT INTO tenants (name, slug, domain, plan, active, created_at, updated_at) VALUES ('a', 'a', 'a.example', 'enterprise', 1, ?, ?)`, now, now)
	tid, _ := res.LastInsertId()

	// Caller: tenant_admin.
	ch, _ := auth.HashPassword("CallerPass!2026")
	sqlDB.Exec(`INSERT INTO users (created_at, updated_at, email, password_hash, role, tenant_id, active, email_verified) VALUES (?, ?, 'caller@test.local', ?, 'tenant_admin', ?, 1, 1)`, now, now, ch, tid)

	// Target: tenant_support, token_version=200.
	th, _ := auth.HashPassword("TargetPass!2026")
	res2, _ := sqlDB.Exec(`INSERT INTO users (created_at, updated_at, email, password_hash, role, tenant_id, active, email_verified, token_version) VALUES (?, ?, 'target@test.local', ?, 'tenant_support', ?, 1, 1, 200)`, now, now, th, tid)
	tgtID, _ := res2.LastInsertId()

	return &memberRoleHarness{
		router:   router,
		sqlDB:    sqlDB,
		tenantA:  uint(tid),
		targetID: uint(tgtID),
	}
}

func (h *memberRoleHarness) login(t *testing.T, email, password string) string {
	t.Helper()
	body := fmt.Sprintf(`{"username":"%s","password":"%s"}`, email, password)
	req, _ := http.NewRequestWithContext(context.Background(), "POST", "/admin/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := h.router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("login %s: %v", email, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login %s: status %d body=%s", email, resp.StatusCode, raw)
	}
	var out struct {
		AccessToken string `json:"access_token"`
	}
	json.Unmarshal(raw, &out)
	if out.AccessToken == "" {
		t.Fatalf("login %s: empty token", email)
	}
	return out.AccessToken
}

func (h *memberRoleHarness) csrfToken(t *testing.T, bearer string) string {
	t.Helper()
	req, _ := http.NewRequestWithContext(context.Background(), "GET", "/api/v1/csrf-token", nil)
	req.Header.Set("Authorization", "Bearer "+bearer)
	resp, err := h.router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("csrf: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("csrf: status %d body=%s", resp.StatusCode, raw)
	}
	var out struct {
		CSRFToken string `json:"csrf_token"`
	}
	json.Unmarshal(raw, &out)
	if out.CSRFToken == "" {
		t.Fatal("csrf: empty token")
	}
	return out.CSRFToken
}

func (h *memberRoleHarness) resetTarget(t *testing.T) {
	t.Helper()
	h.sqlDB.Exec("UPDATE users SET role = 'tenant_support', token_version = 200, active = 1, deleted_at = NULL WHERE id = ?", h.targetID)
}

func (h *memberRoleHarness) targetState(t *testing.T) (role string, tv int64, active bool, deletedAt sql.NullTime) {
	t.Helper()
	h.sqlDB.QueryRow("SELECT role, COALESCE(token_version,0), active, deleted_at FROM users WHERE id = ?", h.targetID).Scan(&role, &tv, &active, &deletedAt)
	return
}

// countSuccessfulMemberRoleAudit returns the number of audit rows
// recording a SUCCESSFUL member.role_update for the target member.
// Production Handler.UpdateMemberRole (customer_org.go:127) writes to
// coremail_audit via writeAuditLog (handlers.go:3527) with
// action="member.role_update", result="success" and
// target="user:<memberID> role:<submittedRole>". When the Service
// allowlist rejects the role, the handler returns 400 BEFORE
// writeAuditLog, so no success row must appear for a forbidden role.
func (h *memberRoleHarness) countSuccessfulMemberRoleAudit(t *testing.T) int {
	t.Helper()
	var n int
	if err := h.sqlDB.QueryRow(
		"SELECT COUNT(*) FROM coremail_audit WHERE action = ? AND result = ? AND target LIKE ?",
		"member.role_update", "success",
		fmt.Sprintf("user:%d%%", h.targetID),
	).Scan(&n); err != nil {
		t.Fatalf("audit count query: %v", err)
	}
	return n
}

func TestUpdateMemberRoleHTTPRejectsForbiddenRoles(t *testing.T) {
	h := newMemberRoleHarness(t)
	callerTok := h.login(t, "caller@test.local", "CallerPass!2026")
	csrf := h.csrfToken(t, callerTok)

	forbiddenRoles := []string{
		"platform_super_admin",
		"superadmin",
		"super_admin",
		"super-admin",
		"admin",
		"operator",
		"readonly",
		"user",
		"billing",
		"nonexistent_role",
		"",
	}

	for _, role := range forbiddenRoles {
		t.Run(role, func(t *testing.T) {
			h.resetTarget(t)

			// BEFORE-state snapshot: target row and audit-success count.
			beforeRole, beforeTV, beforeActive, beforeDeleted := h.targetState(t)
			if beforeRole != "tenant_support" || beforeTV != 200 || !beforeActive || beforeDeleted.Valid {
				t.Fatalf("baseline before %q not sane: role=%s tv=%d active=%v deleted_valid=%v",
					role, beforeRole, beforeTV, beforeActive, beforeDeleted.Valid)
			}
			beforeAuditCount := h.countSuccessfulMemberRoleAudit(t)

			bodyJSON, _ := json.Marshal(map[string]string{"role": role})
			path := fmt.Sprintf("/api/v1/enterprise/members/%d/role", h.targetID)
			req, _ := http.NewRequestWithContext(context.Background(), "PATCH", path, strings.NewReader(string(bodyJSON)))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+callerTok)
			req.Header.Set("Cookie", "csrf_token="+csrf)
			req.Header.Set("X-CSRF-Token", csrf)
			resp, err := h.router.App().Test(req, fiber.TestConfig{Timeout: 0})
			if err != nil {
				t.Fatalf("PATCH %s: %v", path, err)
			}
			defer resp.Body.Close()
			body, _ := io.ReadAll(resp.Body)

			// Assert 400 exactly.
			if resp.StatusCode >= 500 {
				t.Fatalf("unexpected 5xx for role=%q: status=%d body=%s", role, resp.StatusCode, body)
			}
			if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
				t.Fatalf("unexpected auth error %d for role=%q: body=%s", resp.StatusCode, role, body)
			}
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("want 400 for role=%q, got %d body=%s", role, resp.StatusCode, body)
			}

			// Parse JSON error.
			var errResp struct {
				Error string `json:"error"`
			}
			if err := json.Unmarshal(body, &errResp); err != nil {
				t.Fatalf("response not JSON for role=%q: body=%s", role, body)
			}
			if errResp.Error != "invalid organization member role" {
				t.Fatalf("wrong error for role=%q: got %q", role, errResp.Error)
			}
			// Must not echo attacker-controlled role.
			if role != "" && strings.Contains(string(body), role) {
				t.Fatalf("response echoes submitted role %q: body=%s", role, body)
			}

			// AFTER-state: target row + audit-success count.
			afterRole, afterTV, afterActive, afterDeleted := h.targetState(t)
			if afterRole != "tenant_support" || afterTV != 200 || !afterActive {
				t.Fatalf("target mutated for %q: role=%s tv=%d active=%v",
					role, afterRole, afterTV, afterActive)
			}
			if afterDeleted.Valid {
				t.Fatalf("target deleted_at changed from NULL for %q: value=%v",
					role, afterDeleted.Time)
			}
			afterAuditCount := h.countSuccessfulMemberRoleAudit(t)
			if afterAuditCount != beforeAuditCount {
				t.Fatalf("audit count changed for rejected role %q: before=%d after=%d",
					role, beforeAuditCount, afterAuditCount)
			}
		})
	}

	// Extra: PSA escalation attempt — target cannot use platform endpoint.
	t.Run("PSA_escalation_platform_denied", func(t *testing.T) {
		h.resetTarget(t)
		bodyJSON, _ := json.Marshal(map[string]string{"role": "platform_super_admin"})
		path := fmt.Sprintf("/api/v1/enterprise/members/%d/role", h.targetID)
		req, _ := http.NewRequestWithContext(context.Background(), "PATCH", path, strings.NewReader(string(bodyJSON)))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+callerTok)
		req.Header.Set("Cookie", "csrf_token="+csrf)
		req.Header.Set("X-CSRF-Token", csrf)
		resp, _ := h.router.App().Test(req, fiber.TestConfig{Timeout: 0})
		resp.Body.Close()
		if resp.StatusCode != 400 {
			t.Fatalf("PSA escalation: want 400, got %d", resp.StatusCode)
		}

		// Login as target — still tenant_support.
		targetTok := h.login(t, "target@test.local", "TargetPass!2026")
		platReq, _ := http.NewRequestWithContext(context.Background(), "GET", "/api/v1/admin/backups", nil)
		platReq.Header.Set("Authorization", "Bearer "+targetTok)
		platResp, err := h.router.App().Test(platReq, fiber.TestConfig{Timeout: 0})
		if err != nil {
			t.Fatalf("platform GET: %v", err)
		}
		defer platResp.Body.Close()
		platBody, _ := io.ReadAll(platResp.Body)
		if platResp.StatusCode != http.StatusForbidden {
			t.Fatalf("target on platform: want 403, got %d body=%s", platResp.StatusCode, platBody)
		}
		if platResp.StatusCode >= 500 {
			t.Fatalf("platform 5xx: %d", platResp.StatusCode)
		}
		if !strings.Contains(string(platBody), "insufficient permissions") {
			t.Fatalf("platform denial missing error token: %s", platBody)
		}
	})
}
