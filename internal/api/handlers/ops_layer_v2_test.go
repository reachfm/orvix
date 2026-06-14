package handlers_test

// Tests for the Enterprise Operations Layer v2 endpoints. These cover:
//
//   Part A — Mailbox search/filter query params (q, status, admin)
//   Part B — Domain search/filter query params (q, status)
//   Part C — Bulk mailbox status (success, admin mailbox protected, CSRF gating)
//   Part D — Bulk domain status (success, CSRF gating)
//   Part E — AdminSummary.recent_activity safe-fields cap
//   Part F — AdminSummary.top_domains ordering and cap
//   Part G — CSV export safe fields and headers
//
// The tests build a real router against a tempdir SQLite database (mirroring
// the pattern in internal/api/router_test.go) and assert on the JSON and
// CSV bodies returned by the public API surface. We use the external test
// package so we can import the `api` package (which itself imports
// `handlers`) without creating an import cycle.

import (
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/api"
	"github.com/orvix/orvix/internal/auth"
	"github.com/orvix/orvix/internal/config"
	"github.com/orvix/orvix/internal/license"
	"github.com/orvix/orvix/internal/models"
	"github.com/orvix/orvix/internal/modules"
	"go.uber.org/zap"
)

// buildOpsV2TestHarness builds a router on a fresh tempdir SQLite DB with the
// CoreMail tables migrated and a single admin user, admin mailbox, and one
// non-admin mailbox, plus two mail domains. It returns the router, the SQL
// handle, the access token, and the CSRF token. The caller must defer
// router.App().Shutdown() and sqlDB.Close() to release the SQLite file.
func buildOpsV2TestHarness(t *testing.T) (*api.Router, *sql.DB, string, string) {
	t.Helper()
	logger := zap.NewNop()
	cfg := config.Defaults()
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = filepath.Join(t.TempDir(), "orvix.db") + "?_loc=auto&_busy_timeout=5000&_txlock=immediate"
	db, err := config.NewDatabase(&cfg.Database, logger)
	if err != nil {
		t.Fatalf("database: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("sql db: %v", err)
	}
	if err := models.MigrateAllRaw(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	authenticator, err := auth.NewAuthenticator(&cfg.Auth, db, logger)
	if err != nil {
		t.Fatalf("authenticator: %v", err)
	}
	now := time.Now().UTC().Format("2006-01-02 15:04:05")
	hashedPw, err := authenticator.HashPassword("TestPassword123!")
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	if _, err := sqlDB.Exec(
		`INSERT INTO users (created_at, updated_at, email, password_hash, role, tenant_id, active, email_verified)
		 VALUES (?, ?, 'admin@test.local', ?, 'admin', 1, 1, 1)`,
		now, now, hashedPw,
	); err != nil {
		t.Fatalf("insert admin user: %v", err)
	}
	router := api.NewRouter(cfg, authenticator, logger, db, modules.NewRegistry(logger), license.NewFeatureFlags(logger), nil)
	token := loginOpsV2(t, router)
	csrf := csrfOpsV2(t, router, token)
	return router, sqlDB, token, csrf
}

// loginOpsV2 logs the admin in via the /admin/login route and returns the access token.
func loginOpsV2(t *testing.T, router *api.Router) string {
	t.Helper()
	payload := `{"username":"admin@test.local","password":"TestPassword123!"}`
	req := httptest.NewRequest("POST", "/admin/login", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	resp, err := router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("login request: %v", err)
	}
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("login expected 200, got %d: %s", resp.StatusCode, body)
	}
	var parsed struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		t.Fatalf("decode login: %v", err)
	}
	if parsed.AccessToken == "" {
		t.Fatal("login did not return access token")
	}
	return parsed.AccessToken
}

// csrfOpsV2 fetches a CSRF token via the GET /api/v1/csrf-token endpoint.
func csrfOpsV2(t *testing.T, router *api.Router, token string) string {
	t.Helper()
	req := httptest.NewRequest("GET", "/api/v1/csrf-token", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("csrf request: %v", err)
	}
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("csrf expected 200, got %d: %s", resp.StatusCode, body)
	}
	var data struct {
		CSRFToken string `json:"csrf_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		t.Fatalf("decode csrf: %v", err)
	}
	if data.CSRFToken == "" {
		t.Fatal("csrf token endpoint returned empty token")
	}
	return data.CSRFToken
}

// insertCoreMailDomain inserts a coremail_domains row at a known id.
func insertCoreMailDomain(t *testing.T, sqlDB *sql.DB, id int, name, plan, status string) {
	t.Helper()
	now := time.Now().UTC().Format("2006-01-02 15:04:05")
	if _, err := sqlDB.Exec(
		`INSERT INTO coremail_domains (id, name, tenant_id, reseller_id, status, plan, description, max_mailboxes, max_aliases, max_quota_mb, dkim_enabled, dkim_selector, dmarc_enabled, mtasts_enabled, catchall_address, abuse_contact, labels, mailbox_count, created_at, updated_at)
		 VALUES (?, ?, 1, 0, ?, ?, '', 100, 50, 1024, 0, '', 0, 0, '', '', '', 0, ?, ?)`,
		id, name, status, plan, now, now,
	); err != nil {
		t.Fatalf("insert coremail domain %q: %v", name, err)
	}
}

// insertCoreMailbox inserts a coremail_mailboxes row at a known id.
func insertCoreMailbox(t *testing.T, sqlDB *sql.DB, id int, domainID int, localPart, email, status string, isAdmin int) {
	t.Helper()
	now := time.Now().UTC().Format("2006-01-02 15:04:05")
	testHash := "$argon2id$v=19$m=1024,t=1,p=1$c2FsdA$aGFzaA"
	if _, err := sqlDB.Exec(
		`INSERT INTO coremail_mailboxes (id, domain_id, tenant_id, local_part, email, name, password_hash, auth_scheme, status, quota_mb, is_admin, created_at, updated_at)
		 VALUES (?, ?, 1, ?, ?, ?, ?, 'argon2id', ?, 1024, ?, ?, ?)`,
		id, domainID, localPart, email, localPart, testHash, status, isAdmin, now, now,
	); err != nil {
		t.Fatalf("insert coremail mailbox %q: %v", email, err)
	}
}

// doOpsV2Request issues an arbitrary request through the router with the given
// auth headers and returns the response.
func doOpsV2Request(t *testing.T, router *api.Router, method, path, body, token, csrf string, setCSRF bool) (*http.Response, []byte) {
	t.Helper()
	var rdr io.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, rdr)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if setCSRF && csrf != "" {
		req.Header.Set("Cookie", "csrf_token="+csrf)
		req.Header.Set("X-CSRF-Token", csrf)
	}
	resp, err := router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	data, _ := io.ReadAll(resp.Body)
	return resp, data
}

// =========================== Part A — Mailbox filters ===========================

func TestOpsV2_MailboxFilters(t *testing.T) {
	router, sqlDB, token, _ := buildOpsV2TestHarness(t)
	defer router.App().Shutdown()
	defer sqlDB.Close()

	insertCoreMailDomain(t, sqlDB, 1, "test.local", "enterprise", "active")
	insertCoreMailbox(t, sqlDB, 1, 1, "admin", "admin@test.local", "active", 1)
	insertCoreMailbox(t, sqlDB, 2, 1, "alice", "alice@test.local", "active", 0)
	insertCoreMailbox(t, sqlDB, 3, 1, "bob", "bob@test.local", "suspended", 0)
	insertCoreMailbox(t, sqlDB, 4, 1, "carol", "carol@another.example", "active", 0)
	// Soft-deleted row that must never appear in results.
	insertCoreMailbox(t, sqlDB, 5, 1, "ghost", "ghost@test.local", "active", 0)
	if _, err := sqlDB.Exec("UPDATE coremail_mailboxes SET deleted_at = ? WHERE id = 5", time.Now().UTC()); err != nil {
		t.Fatalf("soft delete: %v", err)
	}

	type tc struct {
		name    string
		path    string
		wantLen int
		wantEm  string // expected email present in result (empty = no check)
	}
	cases := []tc{
		{"q=admin", "/api/v1/mailboxes?q=admin", 1, "admin@test.local"},
		{"q=ALICE case-insensitive", "/api/v1/mailboxes?q=ALICE", 1, "alice@test.local"},
		{"q=@another.example domain-scoped", "/api/v1/mailboxes?q=@another.example", 1, "carol@another.example"},
		{"status=active", "/api/v1/mailboxes?status=active", 3, "alice@test.local"},
		{"status=suspended", "/api/v1/mailboxes?status=suspended", 1, "bob@test.local"},
		{"admin=true", "/api/v1/mailboxes?admin=true", 1, "admin@test.local"},
		{"admin=false", "/api/v1/mailboxes?admin=false", 3, "alice@test.local"},
		{"q+status combo", "/api/v1/mailboxes?q=test&status=active", 2, "alice@test.local"},
		{"no filter baseline", "/api/v1/mailboxes", 4, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp, body := doOpsV2Request(t, router, "GET", c.path, "", token, "", false)
			if resp.StatusCode != 200 {
				t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
			}
			var rows []map[string]any
			if err := json.Unmarshal(body, &rows); err != nil {
				t.Fatalf("mailboxes must be JSON array: %v: %s", err, body)
			}
			if len(rows) != c.wantLen {
				t.Fatalf("expected %d rows, got %d: %s", c.wantLen, len(rows), body)
			}
			if c.wantEm != "" {
				found := false
				for _, r := range rows {
					if r["email"] == c.wantEm {
						found = true
						break
					}
				}
				if !found {
					t.Fatalf("expected %q in results, got %s", c.wantEm, body)
				}
			}
			// Forbidden: password material must never appear in any filtered response.
			low := strings.ToLower(string(body))
			for _, forbidden := range []string{"password_hash", "$argon2", "argon2id"} {
				if strings.Contains(low, strings.ToLower(forbidden)) {
					t.Fatalf("mailbox response must not contain %s: %s", forbidden, body)
				}
			}
		})
	}

	// The /api/v1/users endpoint shares the same handler — it must also be filtered.
	t.Run("users endpoint shares filter", func(t *testing.T) {
		resp, body := doOpsV2Request(t, router, "GET", "/api/v1/users?status=suspended", "", token, "", false)
		if resp.StatusCode != 200 {
			t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
		}
		var rows []map[string]any
		if err := json.Unmarshal(body, &rows); err != nil {
			t.Fatalf("users must be JSON array: %v: %s", err, body)
		}
		if len(rows) != 1 || rows[0]["email"] != "bob@test.local" {
			t.Fatalf("expected only bob (suspended), got %s", body)
		}
	})
}

// =========================== Part B — Domain filters ===========================

func TestOpsV2_DomainFilters(t *testing.T) {
	router, sqlDB, token, _ := buildOpsV2TestHarness(t)
	defer router.App().Shutdown()
	defer sqlDB.Close()

	insertCoreMailDomain(t, sqlDB, 1, "test.local", "enterprise", "active")
	insertCoreMailDomain(t, sqlDB, 2, "example.com", "smb", "active")
	insertCoreMailDomain(t, sqlDB, 3, "another.org", "smb", "suspended")
	// Soft-deleted domain must be hidden by the filter and the base list.
	insertCoreMailDomain(t, sqlDB, 4, "deleted.example", "smb", "active")
	if _, err := sqlDB.Exec("UPDATE coremail_domains SET deleted_at = ? WHERE id = 4", time.Now().UTC()); err != nil {
		t.Fatalf("soft delete: %v", err)
	}

	type tc struct {
		name   string
		path   string
		want   int
		wantDm string
	}
	cases := []tc{
		{"q=test", "/api/v1/domains?q=test", 1, "test.local"},
		{"q=EXAMPLE case-insensitive", "/api/v1/domains?q=EXAMPLE", 1, "example.com"},
		{"status=active", "/api/v1/domains?status=active", 2, "test.local"},
		{"status=suspended", "/api/v1/domains?status=suspended", 1, "another.org"},
		{"q+status combo", "/api/v1/domains?q=example&status=active", 1, "example.com"},
		{"no filter baseline", "/api/v1/domains", 3, "test.local"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp, body := doOpsV2Request(t, router, "GET", c.path, "", token, "", false)
			if resp.StatusCode != 200 {
				t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
			}
			var rows []map[string]any
			if err := json.Unmarshal(body, &rows); err != nil {
				t.Fatalf("domains must be JSON array: %v: %s", err, body)
			}
			if len(rows) != c.want {
				t.Fatalf("expected %d rows, got %d: %s", c.want, len(rows), body)
			}
			if c.wantDm != "" {
				found := false
				for _, r := range rows {
					if r["domain"] == c.wantDm {
						found = true
						break
					}
				}
				if !found {
					t.Fatalf("expected %q in results, got %s", c.wantDm, body)
				}
			}
			for _, forbidden := range []string{"password", "hash", "secret", "token"} {
				if strings.Contains(strings.ToLower(string(body)), forbidden) {
					t.Fatalf("domain response must not contain %s: %s", forbidden, body)
				}
			}
		})
	}
}

// =========================== Part C — Bulk mailbox status ===========================

func TestOpsV2_BulkMailboxStatus(t *testing.T) {
	router, sqlDB, token, csrf := buildOpsV2TestHarness(t)
	defer router.App().Shutdown()
	defer sqlDB.Close()

	insertCoreMailDomain(t, sqlDB, 1, "test.local", "enterprise", "active")
	// id 1: admin mailbox (must be skipped, never changed).
	insertCoreMailbox(t, sqlDB, 1, 1, "admin", "admin@test.local", "active", 1)
	// ids 2, 3: non-admin mailboxes in active state — eligible for bulk suspend.
	insertCoreMailbox(t, sqlDB, 2, 1, "alice", "alice@test.local", "active", 0)
	insertCoreMailbox(t, sqlDB, 3, 1, "bob", "bob@test.local", "active", 0)
	// id 4: soft-deleted — must be skipped.
	insertCoreMailbox(t, sqlDB, 4, 1, "ghost", "ghost@test.local", "active", 0)
	if _, err := sqlDB.Exec("UPDATE coremail_mailboxes SET deleted_at = ? WHERE id = 4", time.Now().UTC()); err != nil {
		t.Fatalf("soft delete: %v", err)
	}
	// id 5: already suspended.
	insertCoreMailbox(t, sqlDB, 5, 1, "carol", "carol@test.local", "suspended", 0)

	t.Run("success suspends eligible mailboxes", func(t *testing.T) {
		resp, body := doOpsV2Request(t, router, "POST", "/api/v1/mailboxes/bulk/status",
			`{"mailbox_ids":[1,2,3,4,5],"status":"suspended"}`, token, csrf, true)
		if resp.StatusCode != 200 {
			t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
		}
		var data struct {
			Updated int `json:"updated"`
			Skipped int `json:"skipped"`
		}
		if err := json.Unmarshal(body, &data); err != nil {
			t.Fatalf("decode: %v: %s", err, body)
		}
		// RACE-SAFE: updated count = number of rows where the safety
		// predicate (id=? AND deleted_at IS NULL AND is_admin=0) matched
		// and the UPDATE ran. In SQLite, the no-op case (carol already
		// 'suspended') still reports RowsAffected=1 because the WHERE
		// matched. So the 3 eligible rows (alice, bob, carol) are counted
		// as updated; the admin (1) and the soft-deleted (4) are skipped.
		if data.Updated != 3 {
			t.Fatalf("expected updated=3 (alice, bob, carol), got %d (body: %s)", data.Updated, body)
		}
		if data.Skipped != 2 {
			t.Fatalf("expected skipped=2 (admin + soft-deleted), got %d (body: %s)", data.Skipped, body)
		}
		// Verify the admin mailbox is unchanged — the race-safe predicate
		// is_admin=0 must be enforced at the write site.
		var adminStatus string
		if err := sqlDB.QueryRow("SELECT status FROM coremail_mailboxes WHERE id = 1").Scan(&adminStatus); err != nil {
			t.Fatalf("read admin status: %v", err)
		}
		if adminStatus != "active" {
			t.Fatalf("admin mailbox must remain active, got %s", adminStatus)
		}
		// Verify the soft-deleted mailbox is unchanged.
		var ghostStatus string
		var ghostDeletedAt sql.NullString
		if err := sqlDB.QueryRow("SELECT status, deleted_at FROM coremail_mailboxes WHERE id = 4").Scan(&ghostStatus, &ghostDeletedAt); err != nil {
			t.Fatalf("read ghost: %v", err)
		}
		if ghostStatus != "active" {
			t.Fatalf("soft-deleted mailbox status must remain unchanged, got %s", ghostStatus)
		}
		if !ghostDeletedAt.Valid {
			t.Fatalf("soft-deleted mailbox must remain soft-deleted (deleted_at was cleared?)")
		}
		// Verify alice and bob are now suspended.
		for _, id := range []int{2, 3} {
			var s string
			if err := sqlDB.QueryRow("SELECT status FROM coremail_mailboxes WHERE id = ?", id).Scan(&s); err != nil {
				t.Fatalf("read id %d: %v", id, s)
			}
			if s != "suspended" {
				t.Fatalf("id %d should be suspended, got %s", id, s)
			}
		}
		// Verify carol is still suspended (was already).
		var carol string
		if err := sqlDB.QueryRow("SELECT status FROM coremail_mailboxes WHERE id = 5").Scan(&carol); err != nil {
			t.Fatalf("read carol: %v", err)
		}
		if carol != "suspended" {
			t.Fatalf("carol should still be suspended, got %s", carol)
		}
		// Audit log entry must be recorded.
		var n int
		if err := sqlDB.QueryRow("SELECT COUNT(*) FROM coremail_audit WHERE action = 'mailbox.bulk_status'").Scan(&n); err != nil {
			t.Fatalf("audit count: %v", err)
		}
		if n != 1 {
			t.Fatalf("expected exactly 1 bulk mailbox audit entry, got %d", n)
		}
	})

	t.Run("race-safe predicate: admin mailbox never updated even when targeted alongside non-admin", func(t *testing.T) {
		// Reset state: re-activate alice and bob so the bulk enable below
		// is the only mutation we expect to count.
		if _, err := sqlDB.Exec("UPDATE coremail_mailboxes SET status = 'active' WHERE id IN (2, 3) AND is_admin = 0"); err != nil {
			t.Fatalf("reset: %v", err)
		}
		// Bulk enable: ids 1 (admin), 2 (alice active), 3 (bob active).
		// Spec: updated count must reflect actual RowsAffected only.
		// Race-safe predicate is_admin=0 means admin returns 0 rows
		// affected → skipped. alice and bob return 1 each → updated=2.
		resp, body := doOpsV2Request(t, router, "POST", "/api/v1/mailboxes/bulk/status",
			`{"mailbox_ids":[1,2,3],"status":"active"}`, token, csrf, true)
		if resp.StatusCode != 200 {
			t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
		}
		var data struct {
			Updated int `json:"updated"`
			Skipped int `json:"skipped"`
		}
		_ = json.Unmarshal(body, &data)
		if data.Updated != 2 {
			t.Fatalf("expected updated=2 (alice+bob), got %d (%s)", data.Updated, body)
		}
		if data.Skipped != 1 {
			t.Fatalf("expected skipped=1 (admin), got %d (%s)", data.Skipped, body)
		}
		// Admin mailbox must still be active.
		var adminStatus string
		sqlDB.QueryRow("SELECT status FROM coremail_mailboxes WHERE id = 1").Scan(&adminStatus)
		if adminStatus != "active" {
			t.Fatalf("admin must remain active, got %s", adminStatus)
		}
	})

	t.Run("re-enable suspends none (everything already suspended)", func(t *testing.T) {
		// Spec guarantee: the race-safe UPDATE never applies a status
		// change to a soft-deleted or admin row. For no-op rewrites
		// (where the row already matches the requested status) the
		// race-safe UPDATE still matches the row and (in SQLite's
		// go-sqlite3 driver) reports RowsAffected=1. The spec says
		// the response updated count must reflect actual RowsAffected
		// only. So we assert the on-disk invariant: the rows must
		// remain 'suspended' and not have been flipped back to a
		// different state.
		resp, body := doOpsV2Request(t, router, "POST", "/api/v1/mailboxes/bulk/status",
			`{"mailbox_ids":[2,3,5],"status":"suspended"}`, token, csrf, true)
		if resp.StatusCode != 200 {
			t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
		}
		for _, id := range []int{2, 3, 5} {
			var s string
			if err := sqlDB.QueryRow("SELECT status FROM coremail_mailboxes WHERE id = ?", id).Scan(&s); err != nil {
				t.Fatalf("read id %d: %v", id, err)
			}
			if s != "suspended" {
				t.Fatalf("id %d must remain suspended, got %s", id, s)
			}
		}
		_ = body
	})

	t.Run("admin mailbox protected when targeted alone", func(t *testing.T) {
		resp, body := doOpsV2Request(t, router, "POST", "/api/v1/mailboxes/bulk/status",
			`{"mailbox_ids":[1],"status":"suspended"}`, token, csrf, true)
		if resp.StatusCode != 200 {
			t.Fatalf("expected 200 (skip not error), got %d: %s", resp.StatusCode, body)
		}
		var data struct {
			Updated int `json:"updated"`
			Skipped int `json:"skipped"`
		}
		_ = json.Unmarshal(body, &data)
		if data.Updated != 0 || data.Skipped != 1 {
			t.Fatalf("admin mailbox must be skipped, got updated=%d,skipped=%d (%s)", data.Updated, data.Skipped, body)
		}
		var adminStatus string
		sqlDB.QueryRow("SELECT status FROM coremail_mailboxes WHERE id = 1").Scan(&adminStatus)
		if adminStatus != "active" {
			t.Fatalf("admin mailbox must remain active, got %s", adminStatus)
		}
	})

	t.Run("invalid status rejected", func(t *testing.T) {
		resp, body := doOpsV2Request(t, router, "POST", "/api/v1/mailboxes/bulk/status",
			`{"mailbox_ids":[2],"status":"paused"}`, token, csrf, true)
		if resp.StatusCode != 400 {
			t.Fatalf("expected 400, got %d: %s", resp.StatusCode, body)
		}
	})

	t.Run("empty mailbox_ids rejected", func(t *testing.T) {
		resp, body := doOpsV2Request(t, router, "POST", "/api/v1/mailboxes/bulk/status",
			`{"mailbox_ids":[],"status":"suspended"}`, token, csrf, true)
		if resp.StatusCode != 400 {
			t.Fatalf("expected 400, got %d: %s", resp.StatusCode, body)
		}
	})

	t.Run("missing CSRF (write via unprotected admin group)", func(t *testing.T) {
		// The bulk endpoint lives under the CSRF-protected `men` group.
		// We invoke it without the X-CSRF-Token header and expect the
		// middleware to reject the call. We confirm by mounting a parallel
		// test path on the same router (it would still 403 because the
		// real route requires CSRF).
		//
		// The endpoint path itself is fixed under `men`; we cannot call
		// the no-csrf variant of the same path. Instead we confirm the
		// middleware rejection by sending a request without the CSRF
		// token header.
		// Snapshot alice's status before the rejected call.
		var before string
		sqlDB.QueryRow("SELECT status FROM coremail_mailboxes WHERE id = 2").Scan(&before)
		resp, body := doOpsV2Request(t, router, "POST", "/api/v1/mailboxes/bulk/status",
			`{"mailbox_ids":[2],"status":"active"}`, token, csrf, false)
		if resp.StatusCode != 403 && resp.StatusCode != 401 {
			t.Fatalf("expected 401/403 for missing CSRF, got %d: %s", resp.StatusCode, body)
		}
		// State must be unchanged — the request was rejected.
		var after string
		sqlDB.QueryRow("SELECT status FROM coremail_mailboxes WHERE id = 2").Scan(&after)
		if after != before {
			t.Fatalf("alice status must remain %s (request was rejected), got %s", before, after)
		}
	})

	t.Run("invalid CSRF token rejected (mismatched cookie/header)", func(t *testing.T) {
		// CSRF middleware: cookie and header must match exactly. We send two
		// different values to force the mismatch branch.
		var before string
		sqlDB.QueryRow("SELECT status FROM coremail_mailboxes WHERE id = 2").Scan(&before)
		req := httptest.NewRequest("POST", "/api/v1/mailboxes/bulk/status",
			strings.NewReader(`{"mailbox_ids":[2],"status":"active"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Cookie", "csrf_token=invalid-cookie-token")
		req.Header.Set("X-CSRF-Token", "different-header-token")
		resp, err := router.App().Test(req, fiber.TestConfig{Timeout: 0})
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		if resp.StatusCode != 403 && resp.StatusCode != 401 {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("expected 401/403, got %d: %s", resp.StatusCode, body)
		}
		// State must be unchanged.
		var after string
		sqlDB.QueryRow("SELECT status FROM coremail_mailboxes WHERE id = 2").Scan(&after)
		if after != before {
			t.Fatalf("alice status must remain %s, got %s", before, after)
		}
	})
}

// =========================== Part D — Bulk domain status ===========================

func TestOpsV2_BulkDomainStatus(t *testing.T) {
	router, sqlDB, token, csrf := buildOpsV2TestHarness(t)
	defer router.App().Shutdown()
	defer sqlDB.Close()

	insertCoreMailDomain(t, sqlDB, 1, "test.local", "enterprise", "active")
	insertCoreMailDomain(t, sqlDB, 2, "example.com", "smb", "active")
	insertCoreMailDomain(t, sqlDB, 3, "another.org", "smb", "active")
	// Soft-deleted domain.
	insertCoreMailDomain(t, sqlDB, 4, "deleted.example", "smb", "active")
	if _, err := sqlDB.Exec("UPDATE coremail_domains SET deleted_at = ? WHERE id = 4", time.Now().UTC()); err != nil {
		t.Fatalf("soft delete: %v", err)
	}

	t.Run("success suspends known active domains", func(t *testing.T) {
		resp, body := doOpsV2Request(t, router, "POST", "/api/v1/domains/bulk/status",
			`{"domains":["test.local","example.com","another.org","deleted.example","missing.example"],"status":"suspended"}`,
			token, csrf, true)
		if resp.StatusCode != 200 {
			t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
		}
		var data struct {
			Updated int `json:"updated"`
			Skipped int `json:"skipped"`
		}
		_ = json.Unmarshal(body, &data)
		// RACE-SAFE: 3 active domains match the predicate (test.local,
		// example.com, another.org) → updated=3. The soft-deleted
		// (deleted.example) and the missing one (missing.example) do
		// not match the predicate → RowsAffected=0 → skipped=2.
		if data.Updated != 3 {
			t.Fatalf("expected updated=3, got %d (body: %s)", data.Updated, body)
		}
		if data.Skipped != 2 {
			t.Fatalf("expected skipped=2 (deleted + missing), got %d (body: %s)", data.Skipped, body)
		}
		// Audit log entry must be recorded.
		var n int
		if err := sqlDB.QueryRow("SELECT COUNT(*) FROM coremail_audit WHERE action = 'domain.bulk_status'").Scan(&n); err != nil {
			t.Fatalf("audit count: %v", err)
		}
		if n != 1 {
			t.Fatalf("expected exactly 1 bulk domain audit entry, got %d", n)
		}
	})

	t.Run("soft-deleted domain is never updated", func(t *testing.T) {
		// Re-activate test.local so we can probe the soft-deleted
		// domain's behavior in isolation.
		if _, err := sqlDB.Exec("UPDATE coremail_domains SET status = 'active' WHERE name = 'test.local' AND deleted_at IS NULL"); err != nil {
			t.Fatalf("reset: %v", err)
		}
		// Target only the soft-deleted domain. The race-safe predicate
		// `deleted_at IS NULL` must keep the UPDATE from touching it.
		resp, body := doOpsV2Request(t, router, "POST", "/api/v1/domains/bulk/status",
			`{"domains":["deleted.example"],"status":"suspended"}`,
			token, csrf, true)
		if resp.StatusCode != 200 {
			t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
		}
		var data struct {
			Updated int `json:"updated"`
			Skipped int `json:"skipped"`
		}
		_ = json.Unmarshal(body, &data)
		if data.Updated != 0 {
			t.Fatalf("updated must be 0 for soft-deleted domain, got %d (%s)", data.Updated, body)
		}
		if data.Skipped != 1 {
			t.Fatalf("skipped must be 1 for soft-deleted domain, got %d (%s)", data.Skipped, body)
		}
		// Confirm the soft-deleted row is still soft-deleted and still 'active'.
		var status string
		var deletedAt sql.NullString
		if err := sqlDB.QueryRow("SELECT status, deleted_at FROM coremail_domains WHERE name = 'deleted.example'").Scan(&status, &deletedAt); err != nil {
			t.Fatalf("read soft-deleted: %v", err)
		}
		if !deletedAt.Valid {
			t.Fatalf("soft-deleted domain must remain soft-deleted (deleted_at was cleared?)")
		}
		if status != "active" {
			t.Fatalf("soft-deleted domain status must remain 'active', got %s", status)
		}
	})

	t.Run("missing domain does not count as updated", func(t *testing.T) {
		// Bulk enable: only the missing domain is targeted. The race-safe
		// predicate (name=? AND deleted_at IS NULL) must produce 0 rows
		// affected → skipped=1, updated=0.
		resp, body := doOpsV2Request(t, router, "POST", "/api/v1/domains/bulk/status",
			`{"domains":["never-existed.example"],"status":"active"}`,
			token, csrf, true)
		if resp.StatusCode != 200 {
			t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
		}
		var data struct {
			Updated int `json:"updated"`
			Skipped int `json:"skipped"`
		}
		_ = json.Unmarshal(body, &data)
		if data.Updated != 0 {
			t.Fatalf("missing domain must not be counted as updated, got updated=%d (%s)", data.Updated, body)
		}
		if data.Skipped != 1 {
			t.Fatalf("missing domain must be skipped, got skipped=%d (%s)", data.Skipped, body)
		}
	})

	t.Run("invalid status rejected", func(t *testing.T) {
		resp, body := doOpsV2Request(t, router, "POST", "/api/v1/domains/bulk/status",
			`{"domains":["test.local"],"status":"paused"}`, token, csrf, true)
		if resp.StatusCode != 400 {
			t.Fatalf("expected 400, got %d: %s", resp.StatusCode, body)
		}
	})

	t.Run("empty domains rejected", func(t *testing.T) {
		resp, body := doOpsV2Request(t, router, "POST", "/api/v1/domains/bulk/status",
			`{"domains":[],"status":"suspended"}`, token, csrf, true)
		if resp.StatusCode != 400 {
			t.Fatalf("expected 400, got %d: %s", resp.StatusCode, body)
		}
	})

	t.Run("missing CSRF rejected", func(t *testing.T) {
		var before string
		sqlDB.QueryRow("SELECT status FROM coremail_domains WHERE name = 'example.com'").Scan(&before)
		resp, body := doOpsV2Request(t, router, "POST", "/api/v1/domains/bulk/status",
			`{"domains":["example.com"],"status":"active"}`, token, csrf, false)
		if resp.StatusCode != 403 && resp.StatusCode != 401 {
			t.Fatalf("expected 401/403, got %d: %s", resp.StatusCode, body)
		}
		// State must be unchanged.
		var after string
		sqlDB.QueryRow("SELECT status FROM coremail_domains WHERE name = 'example.com'").Scan(&after)
		if after != before {
			t.Fatalf("example.com must remain %s, got %s", before, after)
		}
	})

	t.Run("invalid CSRF token rejected (mismatched cookie/header)", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/api/v1/domains/bulk/status",
			strings.NewReader(`{"domains":["example.com"],"status":"active"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Cookie", "csrf_token=invalid-cookie-token")
		req.Header.Set("X-CSRF-Token", "different-header-token")
		resp, err := router.App().Test(req, fiber.TestConfig{Timeout: 0})
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		if resp.StatusCode != 403 && resp.StatusCode != 401 {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("expected 401/403, got %d: %s", resp.StatusCode, body)
		}
	})
}

// =========================== Part E+F — AdminSummary extensions ===========================

func TestOpsV2_AdminSummaryExtended(t *testing.T) {
	router, sqlDB, token, _ := buildOpsV2TestHarness(t)
	defer router.App().Shutdown()
	defer sqlDB.Close()

	insertCoreMailDomain(t, sqlDB, 1, "test.local", "enterprise", "active")
	insertCoreMailDomain(t, sqlDB, 2, "example.com", "smb", "active")
	insertCoreMailDomain(t, sqlDB, 3, "another.org", "smb", "suspended")
	insertCoreMailDomain(t, sqlDB, 4, "fourth.example", "smb", "active")
	insertCoreMailDomain(t, sqlDB, 5, "fifth.example", "smb", "active")
	insertCoreMailDomain(t, sqlDB, 6, "sixth.example", "smb", "active")
	// 6th domain has 0 mailboxes — should still appear in top_domains because
	// the LEFT JOIN yields 0.
	insertCoreMailDomain(t, sqlDB, 7, "seventh.example", "smb", "active") // 7th domain
	insertCoreMailbox(t, sqlDB, 1, 1, "admin", "admin@test.local", "active", 1)
	insertCoreMailbox(t, sqlDB, 2, 1, "a1", "a1@test.local", "active", 0)
	insertCoreMailbox(t, sqlDB, 3, 1, "a2", "a2@test.local", "active", 0)
	insertCoreMailbox(t, sqlDB, 4, 1, "a3", "a3@test.local", "active", 0)
	insertCoreMailbox(t, sqlDB, 5, 1, "a4", "a4@test.local", "active", 0)
	insertCoreMailbox(t, sqlDB, 6, 2, "b1", "b1@example.com", "active", 0)
	insertCoreMailbox(t, sqlDB, 7, 2, "b2", "b2@example.com", "active", 0)

	// Insert 12 audit entries so the cap of 10 is exercised.
	now := time.Now().UTC().Format("2006-01-02 15:04:05")
	for i := 0; i < 12; i++ {
		ts := time.Now().UTC().Add(-time.Duration(i) * time.Minute).Format("2006-01-02 15:04:05")
		if _, err := sqlDB.Exec(
			`INSERT INTO coremail_audit (actor, role, action, target, result, ip, user_agent, timestamp)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			"user:1", "admin", "mailbox.create", "mailbox:a@x", "success", "10.0.0.1", "ua", ts,
		); err != nil {
			t.Fatalf("insert audit %d: %v", i, err)
		}
	}

	resp, body := doOpsV2Request(t, router, "GET", "/api/v1/admin/summary", "", token, "", false)
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}
	var data map[string]any
	if err := json.Unmarshal(body, &data); err != nil {
		t.Fatalf("summary must be JSON object: %v: %s", err, body)
	}

	t.Run("recent_activity has at most 10 entries with safe fields only", func(t *testing.T) {
		ra, ok := data["recent_activity"].([]any)
		if !ok {
			t.Fatalf("summary must include recent_activity array: %s", body)
		}
		if len(ra) > 10 {
			t.Fatalf("recent_activity must be <= 10 entries, got %d", len(ra))
		}
		if len(ra) == 0 {
			t.Fatalf("expected at least 1 recent_activity entry, got 0")
		}
		forbidden := []string{"ip", "userAgent", "user_agent", "password", "hash", "token", "jwt", "bearer", "secret", "body", "headers"}
		for i, e := range ra {
			entry, ok := e.(map[string]any)
			if !ok {
				t.Fatalf("entry %d must be an object: %v", i, e)
			}
			for _, key := range []string{"action", "actor", "target", "result", "timestamp"} {
				if _, present := entry[key]; !present {
					t.Fatalf("recent_activity[%d] missing %q: %s", i, key, body)
				}
			}
			// Forbidden keys (camelCase or snake_case forms).
			for _, f := range forbidden {
				if _, present := entry[f]; present {
					t.Fatalf("recent_activity[%d] must not contain %q: %s", i, f, body)
				}
			}
		}
	})

	t.Run("top_domains has at most 5 entries sorted desc by mailbox_count", func(t *testing.T) {
		td, ok := data["top_domains"].([]any)
		if !ok {
			t.Fatalf("summary must include top_domains array: %s", body)
		}
		if len(td) > 5 {
			t.Fatalf("top_domains must be <= 5 entries, got %d", len(td))
		}
		if len(td) == 0 {
			t.Fatalf("expected at least 1 top_domains entry, got 0")
		}
		prev := int64(1<<31 - 1)
		for i, e := range td {
			entry, ok := e.(map[string]any)
			if !ok {
				t.Fatalf("top_domains[%d] must be an object: %v", i, e)
			}
			for _, key := range []string{"domain", "mailbox_count"} {
				if _, present := entry[key]; !present {
					t.Fatalf("top_domains[%d] missing %q: %s", i, key, body)
				}
			}
			mc, ok := entry["mailbox_count"].(float64)
			if !ok {
				t.Fatalf("top_domains[%d].mailbox_count must be number, got %T", i, entry["mailbox_count"])
			}
			if int64(mc) > prev {
				t.Fatalf("top_domains not sorted desc at index %d: %v > %d", i, mc, prev)
			}
			prev = int64(mc)
		}
		// First entry should be one of the two high-count domains.
		first := td[0].(map[string]any)["domain"]
		if first != "test.local" && first != "example.com" {
			t.Fatalf("top_domains[0] expected test.local or example.com (5 + 2 mailboxes), got %v", first)
		}
	})

	t.Run("summary does not leak secret material", func(t *testing.T) {
		low := strings.ToLower(string(body))
		for _, f := range []string{"password", "hash", "argon2", "secret", "bearer", "useragent"} {
			if strings.Contains(low, f) {
				t.Fatalf("summary must not contain %s: %s", f, body)
			}
		}
	})
	_ = now
}

// =========================== Part G — CSV exports ===========================

func TestOpsV2_CSVExports(t *testing.T) {
	router, sqlDB, token, _ := buildOpsV2TestHarness(t)
	defer router.App().Shutdown()
	defer sqlDB.Close()

	insertCoreMailDomain(t, sqlDB, 1, "test.local", "enterprise", "active")
	insertCoreMailDomain(t, sqlDB, 2, "example.com", "smb", "suspended")
	insertCoreMailbox(t, sqlDB, 1, 1, "admin", "admin@test.local", "active", 1)
	insertCoreMailbox(t, sqlDB, 2, 1, "alice", "alice@test.local", "suspended", 0)
	insertCoreMailbox(t, sqlDB, 3, 1, "carl", "carl@test.local", "active", 0)
	insertCoreMailbox(t, sqlDB, 4, 1, "quoted", "quoted@test.local", "active", 0)
	insertCoreMailbox(t, sqlDB, 5, 1, "ghost", "ghost@test.local", "active", 0)
	if _, err := sqlDB.Exec("UPDATE coremail_mailboxes SET deleted_at = ? WHERE id = 5", time.Now().UTC()); err != nil {
		t.Fatalf("soft delete: %v", err)
	}

	t.Run("mailbox export headers and safe fields", func(t *testing.T) {
		resp, body := doOpsV2Request(t, router, "GET", "/api/v1/mailboxes/export", "", token, "", false)
		if resp.StatusCode != 200 {
			t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
		}
		if got := resp.Header.Get("Content-Type"); got != "text/csv; charset=utf-8" {
			t.Fatalf("expected Content-Type 'text/csv; charset=utf-8', got %q", got)
			_ = body
		}
		cd := resp.Header.Get("Content-Disposition")
		if !strings.HasPrefix(cd, "attachment; filename=\"mailboxes-") || !strings.HasSuffix(cd, ".csv\"") {
			t.Fatalf("unexpected Content-Disposition: %q", cd)
		}
		text := string(body)
		lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
		if len(lines) < 2 {
			t.Fatalf("expected at least header + 1 data row, got %d: %s", len(lines), text)
		}
		if lines[0] != "email,status,is_admin" {
			t.Fatalf("unexpected header row: %q", lines[0])
		}
		// Data rows: 4 (soft-deleted excluded).
		if len(lines)-1 != 4 {
			t.Fatalf("expected 4 data rows (soft-deleted excluded), got %d: %s", len(lines)-1, text)
		}
		// Forbidden columns/material must not appear anywhere in the export.
		low := strings.ToLower(text)
		for _, f := range []string{"password", "hash", "argon2", "token", "jwt", "bearer", "secret", "body", "headers"} {
			if strings.Contains(low, f) {
				t.Fatalf("mailbox CSV must not contain %q: %s", f, text)
			}
		}
		// Comma in local_part is not exposed in the export (the column is
		// `email`, not `local_part`). Emails can't contain commas, so we
		// just confirm the rows are parseable as CSV with the right number
		// of columns.
		for _, line := range lines[1:] {
			if strings.Count(line, ",") < 2 {
				t.Fatalf("expected at least 2 commas per data row, got %q", line)
			}
		}
	})

	t.Run("domain export headers and safe fields", func(t *testing.T) {
		resp, body := doOpsV2Request(t, router, "GET", "/api/v1/domains/export", "", token, "", false)
		if resp.StatusCode != 200 {
			t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
		}
		if got := resp.Header.Get("Content-Type"); got != "text/csv; charset=utf-8" {
			t.Fatalf("expected Content-Type 'text/csv; charset=utf-8', got %q", got)
		}
		cd := resp.Header.Get("Content-Disposition")
		if !strings.HasPrefix(cd, "attachment; filename=\"domains-") || !strings.HasSuffix(cd, ".csv\"") {
			t.Fatalf("unexpected Content-Disposition: %q", cd)
		}
		text := string(body)
		lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
		if len(lines) < 3 {
			t.Fatalf("expected header + 2 data rows, got %d: %s", len(lines), text)
		}
		if lines[0] != "domain,status,plan,mailbox_count" {
			t.Fatalf("unexpected header row: %q", lines[0])
		}
		if len(lines)-1 != 2 {
			t.Fatalf("expected 2 data rows, got %d: %s", len(lines)-1, text)
		}
		low := strings.ToLower(text)
		for _, f := range []string{"password", "hash", "argon2", "token", "jwt", "bearer", "secret", "body", "headers"} {
			if strings.Contains(low, f) {
				t.Fatalf("domain CSV must not contain %q: %s", f, text)
			}
		}
	})

	t.Run("mailbox export is admin-only", func(t *testing.T) {
		// Unauthenticated request must be rejected.
		resp, _ := doOpsV2Request(t, router, "GET", "/api/v1/mailboxes/export", "", "", "", false)
		if resp.StatusCode != 401 {
			t.Fatalf("expected 401 for unauthenticated export, got %d", resp.StatusCode)
		}
	})
}

// =========================== Sanity: secret-leak static check ===========================

// TestOpsV2_HandlerSourceHasNoForbiddenColumns is a static check that the new
// endpoints never read or expose sensitive columns. This guards against future
// refactors that might add a column to a SELECT list without thinking.
func TestOpsV2_HandlerSourceHasNoForbiddenColumns(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(".", "handlers.go"))
	if err != nil {
		t.Fatalf("read handlers.go: %v", err)
	}
	src := string(raw)
	// Slice relevant to the new endpoints.
	sections := []string{
		"ExportMailboxesCSV",
		"ExportDomainsCSV",
		"BulkMailboxStatus",
		"BulkDomainStatus",
		"ListUsers",
		"ListDomains",
		"AdminSummary",
	}
	// Find the bodies of those funcs by scanning between each `func (h *Handler) X(`
	// and the next top-level `func ` declaration.
	for _, name := range sections {
		start := strings.Index(src, "func (h *Handler) "+name+"(")
		if start < 0 {
			t.Fatalf("could not find func %s in handlers.go", name)
		}
		rest := src[start:]
		// Find the next `func (` declaration.
		end := strings.Index(rest[1:], "func (")
		if end < 0 {
			end = len(rest) - 1
		}
		body := rest[:end+1]
		low := strings.ToLower(body)
		// CSV endpoints should not write password_hash or similar.
		if name == "ExportMailboxesCSV" || name == "ExportDomainsCSV" {
			for _, f := range []string{"password_hash", "password", "argon2", "token", "jwt", "bearer", "secret"} {
				if strings.Contains(low, f) {
					t.Fatalf("%s must not reference %q", name, f)
				}
			}
		}
		// recent_activity must not include IP, userAgent, secrets.
		if name == "AdminSummary" {
			if strings.Contains(low, "e.ip") || strings.Contains(low, "e.useragent") || strings.Contains(low, "e.user_agent") {
				t.Fatalf("AdminSummary recent_activity must not include IP or user agent")
			}
		}
	}
}
