// Security test suite for the Admin Console self-update v2 endpoints
// (admin_updates.go / router.go's `updatesAdmin` group). Exercises the
// HTTP layer end-to-end against a fake selfupdate.IPCClient — no real
// Unix socket or orvix-updater daemon is used, per the design in
// internal/selfupdate/ipc.go's IPCClient interface.
package handlers_test

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/api"
	"github.com/orvix/orvix/internal/auth"
	"github.com/orvix/orvix/internal/config"
	"github.com/orvix/orvix/internal/license"
	"github.com/orvix/orvix/internal/models"
	"github.com/orvix/orvix/internal/modules"
	"github.com/orvix/orvix/internal/selfupdate"
	"go.uber.org/zap"
)

// fakeSelfUpdateClient is a minimal in-memory stand-in for
// selfupdate.IPCClient. unreachable simulates the daemon being down
// (selfupdate.ErrUpdaterUnreachable); otherwise it echoes back a
// canned OK response and records every call it received so tests can
// assert what was actually passed through (idempotency key, etc.).
type fakeSelfUpdateClient struct {
	unreachable bool
	resp        selfupdate.Response
	calls       []selfupdate.Request
}

func (f *fakeSelfUpdateClient) Call(req selfupdate.Request) (*selfupdate.Response, error) {
	f.calls = append(f.calls, req)
	if f.unreachable {
		return nil, selfupdate.ErrUpdaterUnreachable
	}
	r := f.resp
	if r.OK == false && r.Error == "" {
		r.OK = true
	}
	return &r, nil
}

func buildAdminUpdatesHarness(t *testing.T) (*api.Router, *sql.DB, *fakeSelfUpdateClient, string, string) {
	t.Helper()
	logger := zap.NewNop()
	cfg := config.Defaults()
	cfg.Server.AdminUIDir = "../../release/admin"
	cfg.Server.WebmailUIDir = "../../release/webmail"
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = t.TempDir() + "/orvix.db?_loc=auto&_busy_timeout=5000&_txlock=immediate"
	// Disable the default socket-based wiring; the test injects a fake
	// client via router.SetSelfUpdateClient below.
	cfg.SelfUpdate.SocketPath = ""

	db, err := config.NewDatabase(&cfg.Database, logger)
	if err != nil {
		t.Fatalf("database: %v", err)
	}
	if err := models.MigrateAllRaw(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	authenticator, err := auth.NewAuthenticator(&cfg.Auth, db, logger)
	if err != nil {
		t.Fatalf("authenticator: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("sql db: %v", err)
	}
	now := time.Now().UTC().Format("2006-01-02 15:04:05")
	hashedPw, err := authenticator.HashPassword("TestPassword123!")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	// Platform-super-admin user: the only role allowed onto
	// /api/v1/admin/updates/*.
	if _, err := sqlDB.Exec(
		`INSERT INTO users (created_at, updated_at, email, password_hash, role, tenant_id, active, email_verified)
		 VALUES (?, ?, 'platform@test.local', ?, 'platform_super_admin', 1, 1, 1)`,
		now, now, hashedPw); err != nil {
		t.Fatalf("insert platform admin: %v", err)
	}
	// Tenant admin user: must be REJECTED by these endpoints (role
	// gate is RequireAnyRole(RoleSuperAdmin, RolePlatformSuperAdmin) —
	// plain "admin" is deliberately excluded).
	if _, err := sqlDB.Exec(
		`INSERT INTO users (created_at, updated_at, email, password_hash, role, tenant_id, active, email_verified)
		 VALUES (?, ?, 'tenant@test.local', ?, 'admin', 1, 1, 1)`,
		now, now, hashedPw); err != nil {
		t.Fatalf("insert tenant admin: %v", err)
	}
	// Second platform-super-admin user: exists solely so tests can
	// prove the documented session-agnostic CSRF finding (see
	// internal/auth/csrf_regression_test.go's
	// cross_user_token_still_accepted_because_middleware_is_session_agnostic)
	// holds true at the full HTTP-handler layer too, not just inside
	// CSRFManager.Middleware() in isolation.
	if _, err := sqlDB.Exec(
		`INSERT INTO users (created_at, updated_at, email, password_hash, role, tenant_id, active, email_verified)
		 VALUES (?, ?, 'platform2@test.local', ?, 'platform_super_admin', 1, 1, 1)`,
		now, now, hashedPw); err != nil {
		t.Fatalf("insert second platform admin: %v", err)
	}

	router := api.NewRouter(cfg, authenticator, logger, db, modules.NewRegistry(logger), license.NewFeatureFlags(logger), nil)
	fake := &fakeSelfUpdateClient{resp: selfupdate.Response{OK: true, Job: &selfupdate.Job{ID: "job_1"}}}
	router.SetSelfUpdateClient(fake)

	t.Cleanup(func() {
		router.App().Shutdown()
		sqlDB.Close()
	})

	token := adminUpdatesLogin(t, router, "platform@test.local", "TestPassword123!")
	csrf := adminUpdatesCSRF(t, router, token)
	return router, sqlDB, fake, token, csrf
}

func adminUpdatesLogin(t *testing.T, router *api.Router, email, password string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"email": email, "password": password})
	req := httptest.NewRequest("POST", "/api/v1/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := router.App().Test(req)
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("login status %d: %s", resp.StatusCode, b)
	}
	var out struct {
		AccessToken string `json:"access_token"`
	}
	b, _ := io.ReadAll(resp.Body)
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("login decode: %v: %s", err, b)
	}
	return out.AccessToken
}

func adminUpdatesCSRF(t *testing.T, router *api.Router, token string) string {
	t.Helper()
	req := httptest.NewRequest("GET", "/api/v1/csrf-token", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := router.App().Test(req)
	if err != nil {
		t.Fatalf("csrf: %v", err)
	}
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("csrf status %d: %s", resp.StatusCode, b)
	}
	var out struct {
		CSRFToken string `json:"csrf_token"`
	}
	b, _ := io.ReadAll(resp.Body)
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("csrf decode: %v: %s", err, b)
	}
	return out.CSRFToken
}

type auReqOpts struct {
	token   string
	csrf    bool
	csrfVal string // CSRF cookie/header value to send
	body    string
}

func adminUpdatesRequest(t *testing.T, router *api.Router, method, path string, opts auReqOpts) (*http.Response, []byte) {
	t.Helper()
	var reader io.Reader
	if opts.body != "" {
		reader = bytes.NewReader([]byte(opts.body))
	}
	req := httptest.NewRequest(method, path, reader)
	if opts.body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if opts.token != "" {
		req.Header.Set("Authorization", "Bearer "+opts.token)
	}
	if opts.csrf {
		req.Header.Set("Cookie", "csrf_token="+opts.csrfVal)
		req.Header.Set("X-CSRF-Token", opts.csrfVal)
	}
	resp, err := router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("request %s %s: %v", method, path, err)
	}
	b, _ := io.ReadAll(resp.Body)
	return resp, b
}

// ── Auth / role gates ────────────────────────────────────────────

func TestAdminUpdates_Unauthenticated(t *testing.T) {
	router, _, _, _, _ := buildAdminUpdatesHarness(t)
	resp, _ := adminUpdatesRequest(t, router, "GET", "/api/v1/admin/updates/status", auReqOpts{})
	if resp.StatusCode != 401 && resp.StatusCode != 403 {
		t.Fatalf("expected 401/403 unauthenticated, got %d", resp.StatusCode)
	}
}

func TestAdminUpdates_WrongRole_TenantAdminRejected(t *testing.T) {
	router, _, _, _, _ := buildAdminUpdatesHarness(t)
	tenantToken := adminUpdatesLogin(t, router, "tenant@test.local", "TestPassword123!")
	resp, _ := adminUpdatesRequest(t, router, "GET", "/api/v1/admin/updates/status", auReqOpts{token: tenantToken})
	if resp.StatusCode != 403 {
		t.Fatalf("expected 403 for tenant admin, got %d", resp.StatusCode)
	}
}

// ── CSRF ─────────────────────────────────────────────────────────

func TestAdminUpdates_MissingCSRFRejected(t *testing.T) {
	router, _, _, token, _ := buildAdminUpdatesHarness(t)
	resp, _ := adminUpdatesRequest(t, router, "POST", "/api/v1/admin/updates/check", auReqOpts{
		token: token, body: `{}`,
	})
	if resp.StatusCode == 200 {
		t.Fatalf("expected non-200 without CSRF, got %d", resp.StatusCode)
	}
}

// TestAdminUpdates_ForgedMatchingCSRFRejected is the core CSRF-fix
// regression scenario for this endpoint family: a cookie/header pair
// that agree with EACH OTHER but were never issued by CSRFManager.
// This is exactly the bug root-caused in internal/config/sqlite_dialect.go
// (a no-op GORM dialector meant the DB lookup that should reject a
// non-issued token never ran). It runs through the real router — real
// csrf.Middleware(), real CSRFManager, real sqlite GORM DB — with no
// mock bypass of CSRF, so it would have caught the original bug.
func TestAdminUpdates_ForgedMatchingCSRFRejected(t *testing.T) {
	router, _, fake, token, _ := buildAdminUpdatesHarness(t)
	forged := "forged-token-never-issued-by-the-server"
	resp, _ := adminUpdatesRequest(t, router, "POST", "/api/v1/admin/updates/check", auReqOpts{
		token: token, csrf: true, csrfVal: forged, body: `{}`,
	})
	if resp.StatusCode != 403 {
		t.Fatalf("expected 403 for forged matching cookie/header, got %d", resp.StatusCode)
	}
	if len(fake.calls) != 0 {
		t.Fatalf("IPC client must not be called for a forged CSRF token, got %d calls", len(fake.calls))
	}
}

// TestAdminUpdates_InvalidCSRFRejected is kept as a lighter-weight
// alias of the forged-matching-token scenario above (same shape,
// different token value) for historical continuity with earlier
// Phase test names.
func TestAdminUpdates_InvalidCSRFRejected(t *testing.T) {
	router, _, _, token, _ := buildAdminUpdatesHarness(t)
	resp, _ := adminUpdatesRequest(t, router, "POST", "/api/v1/admin/updates/check", auReqOpts{
		token: token, csrf: true, csrfVal: "not-a-real-token", body: `{}`,
	})
	if resp.StatusCode == 200 {
		t.Fatalf("expected non-200 with invalid CSRF, got %d", resp.StatusCode)
	}
}

// TestAdminUpdates_MissingCSRFCookieOnlyRejected exercises the cookie-
// absent-but-header-present branch specifically (distinct from
// TestAdminUpdates_MissingCSRFRejected, which omits both).
func TestAdminUpdates_MissingCSRFCookieOnlyRejected(t *testing.T) {
	router, _, fake, token, csrf := buildAdminUpdatesHarness(t)
	req := httptest.NewRequest("POST", "/api/v1/admin/updates/check", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-CSRF-Token", csrf) // header only, no Cookie header at all
	resp, err := router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if resp.StatusCode != 403 {
		t.Fatalf("expected 403 with missing CSRF cookie, got %d", resp.StatusCode)
	}
	if len(fake.calls) != 0 {
		t.Fatalf("IPC client must not be called when CSRF cookie is missing")
	}
}

// TestAdminUpdates_MissingCSRFHeaderOnlyRejected exercises the header-
// absent-but-cookie-present branch specifically.
func TestAdminUpdates_MissingCSRFHeaderOnlyRejected(t *testing.T) {
	router, _, fake, token, csrf := buildAdminUpdatesHarness(t)
	req := httptest.NewRequest("POST", "/api/v1/admin/updates/check", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Cookie", "csrf_token="+csrf) // cookie only, no X-CSRF-Token header
	resp, err := router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if resp.StatusCode != 403 {
		t.Fatalf("expected 403 with missing CSRF header, got %d", resp.StatusCode)
	}
	if len(fake.calls) != 0 {
		t.Fatalf("IPC client must not be called when CSRF header is missing")
	}
}

// TestAdminUpdates_MismatchedCSRFCookieHeaderRejected sends two
// DIFFERENT, both-real, both-issued CSRF tokens as cookie vs header.
// This is distinct from the forged-matching-pair scenario: here both
// values individually correspond to legitimately issued tokens, but
// because they don't match EACH OTHER the double-submit check must
// still reject the request before either is even looked up in the DB.
func TestAdminUpdates_MismatchedCSRFCookieHeaderRejected(t *testing.T) {
	router, sqlDB, fake, token, csrf1 := buildAdminUpdatesHarness(t)
	// csrf2: a second, independently issued, genuinely valid CSRF token
	// for the same platform admin (a fresh call to the /csrf-token
	// endpoint issues a brand-new record).
	csrf2 := adminUpdatesCSRF(t, router, token)
	if csrf1 == csrf2 {
		t.Fatal("test setup bug: expected two distinct issued CSRF tokens")
	}
	_ = sqlDB
	req := httptest.NewRequest("POST", "/api/v1/admin/updates/check", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Cookie", "csrf_token="+csrf1)
	req.Header.Set("X-CSRF-Token", csrf2)
	resp2, err := router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if resp2.StatusCode != 403 {
		t.Fatalf("expected 403 for mismatched (but individually valid) cookie/header tokens, got %d", resp2.StatusCode)
	}
	if len(fake.calls) != 0 {
		t.Fatalf("IPC client must not be called for mismatched CSRF cookie/header")
	}
}

// insertExpiredCSRFRecord writes a csrf_records row directly (bypassing
// CSRFManager.GenerateToken, which always sets a 24h-future expiry) so
// the test can exercise Middleware's expiry check deterministically.
// Schema per internal/models/models.go's csrf_records migration:
// id, created_at, token_hash (unique), user_id, expires_at.
func insertExpiredCSRFRecord(t *testing.T, sqlDB *sql.DB, token string, userID uint) {
	t.Helper()
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(token)))
	now := time.Now().UTC().Format("2006-01-02 15:04:05")
	past := time.Now().UTC().Add(-time.Hour).Format("2006-01-02 15:04:05")
	if _, err := sqlDB.Exec(
		`INSERT INTO csrf_records (created_at, token_hash, user_id, expires_at) VALUES (?, ?, ?, ?)`,
		now, hash, userID, past); err != nil {
		t.Fatalf("insert expired csrf record: %v", err)
	}
}

func platformAdminUserID(t *testing.T, sqlDB *sql.DB, email string) uint {
	t.Helper()
	var id uint
	if err := sqlDB.QueryRow(`SELECT id FROM users WHERE email = ?`, email).Scan(&id); err != nil {
		t.Fatalf("lookup user id for %s: %v", email, err)
	}
	return id
}

// TestAdminUpdates_ExpiredCSRFTokenRejected reuses the same mechanism
// as internal/auth/csrf_regression_test.go's "expired_token_rejected"
// subtest (insert a CSRFRecord whose ExpiresAt is already in the
// past), but drives it through the full HTTP admin_updates handler
// stack rather than a bare CSRF-only harness.
func TestAdminUpdates_ExpiredCSRFTokenRejected(t *testing.T) {
	router, sqlDB, fake, token, _ := buildAdminUpdatesHarness(t)
	userID := platformAdminUserID(t, sqlDB, "platform@test.local")
	expiredToken := "expired-admin-updates-csrf-token"
	insertExpiredCSRFRecord(t, sqlDB, expiredToken, userID)

	resp, _ := adminUpdatesRequest(t, router, "POST", "/api/v1/admin/updates/check", auReqOpts{
		token: token, csrf: true, csrfVal: expiredToken, body: `{}`,
	})
	if resp.StatusCode != 403 {
		t.Fatalf("expected 403 for expired CSRF token, got %d", resp.StatusCode)
	}
	if len(fake.calls) != 0 {
		t.Fatalf("IPC client must not be called for an expired CSRF token")
	}
}

// TestAdminUpdates_CrossUserCSRFTokenAcceptedBySessionAgnosticDesign
// confirms the documented finding (internal/auth/csrf_regression_test.go:
// "cross_user_token_still_accepted_because_middleware_is_session_agnostic")
// holds at the full admin_updates HTTP layer: CSRFManager.Middleware()
// checks token_hash + expiry only, never binding to the authenticated
// caller's user ID. A platform admin authenticated as user A, presenting
// a CSRF cookie/header pair that was legitimately issued to a DIFFERENT
// platform admin (user B), is accepted by CSRF middleware — the request
// still requires user A's own valid Bearer token to pass the earlier
// auth/role gates, so this is not a privilege-escalation gap, just the
// documented by-design behavior. This test asserts the actual behavior,
// not an invented stricter one.
func TestAdminUpdates_CrossUserCSRFTokenAcceptedBySessionAgnosticDesign(t *testing.T) {
	router, _, fake, tokenA, _ := buildAdminUpdatesHarness(t)
	tokenB := adminUpdatesLogin(t, router, "platform2@test.local", "TestPassword123!")
	csrfIssuedToB := adminUpdatesCSRF(t, router, tokenB)

	// Authenticate the HTTP request as admin A, but present admin B's
	// legitimately issued CSRF token.
	resp, body := adminUpdatesRequest(t, router, "POST", "/api/v1/admin/updates/check", auReqOpts{
		token: tokenA, csrf: true, csrfVal: csrfIssuedToB, body: `{}`,
	})
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200: CSRF middleware is session-agnostic by design (does not bind to UserID), got %d: %s", resp.StatusCode, body)
	}
	if len(fake.calls) != 1 {
		t.Fatalf("expected the request to reach the IPC layer, got %d calls", len(fake.calls))
	}
}

// ── Re-auth on install/rollback ─────────────────────────────────

func TestAdminUpdates_InstallMissingReauthRejected(t *testing.T) {
	router, _, fake, token, csrf := buildAdminUpdatesHarness(t)
	resp, body := adminUpdatesRequest(t, router, "POST", "/api/v1/admin/updates/install", auReqOpts{
		token: token, csrf: true, csrfVal: csrf,
		body: `{"idempotency_key":"key-1","requested_version":"1.2.3"}`,
	})
	if resp.StatusCode != 401 {
		t.Fatalf("expected 401 missing password re-auth, got %d: %s", resp.StatusCode, body)
	}
	if len(fake.calls) != 0 {
		t.Fatalf("IPC client must not be called when re-auth is missing, got %d calls", len(fake.calls))
	}
}

func TestAdminUpdates_InstallStaleReauthRejected(t *testing.T) {
	router, _, fake, token, csrf := buildAdminUpdatesHarness(t)
	resp, body := adminUpdatesRequest(t, router, "POST", "/api/v1/admin/updates/install", auReqOpts{
		token: token, csrf: true, csrfVal: csrf,
		body: `{"password":"WrongPassword!","idempotency_key":"key-1","requested_version":"1.2.3"}`,
	})
	if resp.StatusCode != 401 {
		t.Fatalf("expected 401 wrong password, got %d: %s", resp.StatusCode, body)
	}
	if len(fake.calls) != 0 {
		t.Fatalf("IPC client must not be called on failed re-auth, got %d calls", len(fake.calls))
	}
}

func TestAdminUpdates_RollbackMissingReauthRejected(t *testing.T) {
	router, _, fake, token, csrf := buildAdminUpdatesHarness(t)
	resp, _ := adminUpdatesRequest(t, router, "POST", "/api/v1/admin/updates/rollback", auReqOpts{
		token: token, csrf: true, csrfVal: csrf,
		body: `{"idempotency_key":"key-1","target":"snap_1"}`,
	})
	if resp.StatusCode != 401 {
		t.Fatalf("expected 401 missing re-auth on rollback, got %d", resp.StatusCode)
	}
	if len(fake.calls) != 0 {
		t.Fatalf("IPC client must not be called when rollback re-auth is missing")
	}
}

// ── Happy path: valid re-auth + CSRF + role reaches the IPC layer ──

func TestAdminUpdates_InstallValidRequestReachesIPC(t *testing.T) {
	router, _, fake, token, csrf := buildAdminUpdatesHarness(t)
	resp, body := adminUpdatesRequest(t, router, "POST", "/api/v1/admin/updates/install", auReqOpts{
		token: token, csrf: true, csrfVal: csrf,
		body: `{"password":"TestPassword123!","idempotency_key":"key-1","requested_version":"1.2.3"}`,
	})
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}
	if len(fake.calls) != 1 {
		t.Fatalf("expected exactly 1 IPC call, got %d", len(fake.calls))
	}
	call := fake.calls[0]
	if call.Op != selfupdate.OpStartInstall {
		t.Errorf("expected OpStartInstall, got %q", call.Op)
	}
	if call.IdempotencyKey != "key-1" {
		t.Errorf("idempotency key not passed through: got %q", call.IdempotencyKey)
	}
	if call.RequestedVersion != "1.2.3" {
		t.Errorf("requested_version not passed through: got %q", call.RequestedVersion)
	}
}

func TestAdminUpdates_RollbackValidRequestReachesIPC(t *testing.T) {
	router, _, fake, token, csrf := buildAdminUpdatesHarness(t)
	resp, body := adminUpdatesRequest(t, router, "POST", "/api/v1/admin/updates/rollback", auReqOpts{
		token: token, csrf: true, csrfVal: csrf,
		body: `{"password":"TestPassword123!","idempotency_key":"key-2","target":"snap_1"}`,
	})
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}
	if len(fake.calls) != 1 {
		t.Fatalf("expected exactly 1 IPC call, got %d", len(fake.calls))
	}
	call := fake.calls[0]
	if call.Op != selfupdate.OpStartRollback {
		t.Errorf("expected OpStartRollback, got %q", call.Op)
	}
	if call.IdempotencyKey != "key-2" {
		t.Errorf("idempotency key not passed through: got %q", call.IdempotencyKey)
	}
	if call.JobID != "snap_1" {
		t.Errorf("target not passed through as JobID: got %q", call.JobID)
	}
}

// ── Idempotency key required ─────────────────────────────────────

func TestAdminUpdates_InstallMissingIdempotencyKeyRejected(t *testing.T) {
	router, _, fake, token, csrf := buildAdminUpdatesHarness(t)
	resp, _ := adminUpdatesRequest(t, router, "POST", "/api/v1/admin/updates/install", auReqOpts{
		token: token, csrf: true, csrfVal: csrf,
		body: `{"password":"TestPassword123!","requested_version":"1.2.3"}`,
	})
	if resp.StatusCode != 400 {
		t.Fatalf("expected 400 missing idempotency_key, got %d", resp.StatusCode)
	}
	if len(fake.calls) != 0 {
		t.Fatalf("IPC client must not be called without idempotency_key")
	}
}

func TestAdminUpdates_RollbackMissingIdempotencyKeyRejected(t *testing.T) {
	router, _, fake, token, csrf := buildAdminUpdatesHarness(t)
	resp, _ := adminUpdatesRequest(t, router, "POST", "/api/v1/admin/updates/rollback", auReqOpts{
		token: token, csrf: true, csrfVal: csrf,
		body: `{"password":"TestPassword123!","target":"snap_1"}`,
	})
	if resp.StatusCode != 400 {
		t.Fatalf("expected 400 missing idempotency_key, got %d", resp.StatusCode)
	}
	if len(fake.calls) != 0 {
		t.Fatalf("IPC client must not be called without idempotency_key")
	}
}

// ── Updater unreachable → clean 503 ─────────────────────────────

func TestAdminUpdates_UpdaterUnreachableReturns503(t *testing.T) {
	router, _, fake, token, _ := buildAdminUpdatesHarness(t)
	fake.unreachable = true
	resp, body := adminUpdatesRequest(t, router, "GET", "/api/v1/admin/updates/status", auReqOpts{token: token})
	if resp.StatusCode != 503 {
		t.Fatalf("expected 503 when updater unreachable, got %d: %s", resp.StatusCode, body)
	}
	for _, banned := range []string{"connection refused", "no such file", "dial unix", ".sock"} {
		if bytes.Contains(body, []byte(banned)) {
			t.Errorf("503 response leaks raw transport detail %q: %s", banned, body)
		}
	}
}

func TestAdminUpdates_NotConfiguredReturns503(t *testing.T) {
	// No SetSelfUpdateClient call at all: selfUpdateClient stays nil.
	logger := zap.NewNop()
	cfg := config.Defaults()
	cfg.Server.AdminUIDir = "../../release/admin"
	cfg.Server.WebmailUIDir = "../../release/webmail"
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = t.TempDir() + "/orvix.db?_loc=auto&_busy_timeout=5000&_txlock=immediate"
	cfg.SelfUpdate.SocketPath = ""
	db, err := config.NewDatabase(&cfg.Database, logger)
	if err != nil {
		t.Fatalf("database: %v", err)
	}
	if err := models.MigrateAllRaw(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	authenticator, err := auth.NewAuthenticator(&cfg.Auth, db, logger)
	if err != nil {
		t.Fatalf("authenticator: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("sql db: %v", err)
	}
	now := time.Now().UTC().Format("2006-01-02 15:04:05")
	hashedPw, _ := authenticator.HashPassword("TestPassword123!")
	if _, err := sqlDB.Exec(
		`INSERT INTO users (created_at, updated_at, email, password_hash, role, tenant_id, active, email_verified)
		 VALUES (?, ?, 'platform@test.local', ?, 'platform_super_admin', 1, 1, 1)`,
		now, now, hashedPw); err != nil {
		t.Fatalf("insert: %v", err)
	}
	router := api.NewRouter(cfg, authenticator, logger, db, modules.NewRegistry(logger), license.NewFeatureFlags(logger), nil)
	t.Cleanup(func() { router.App().Shutdown(); sqlDB.Close() })
	token := adminUpdatesLogin(t, router, "platform@test.local", "TestPassword123!")

	resp, _ := adminUpdatesRequest(t, router, "GET", "/api/v1/admin/updates/status", auReqOpts{token: token})
	if resp.StatusCode != 503 {
		t.Fatalf("expected 503 when self-update not configured, got %d", resp.StatusCode)
	}
}

// ── Body validation ──────────────────────────────────────────────

func TestAdminUpdates_OversizedBodyRejected(t *testing.T) {
	router, _, fake, token, csrf := buildAdminUpdatesHarness(t)
	huge := `{"channel":"` + string(make([]byte, 20*1024)) + `"}`
	resp, _ := adminUpdatesRequest(t, router, "POST", "/api/v1/admin/updates/check", auReqOpts{
		token: token, csrf: true, csrfVal: csrf, body: huge,
	})
	if resp.StatusCode != 413 {
		t.Fatalf("expected 413 for oversized body, got %d", resp.StatusCode)
	}
	if len(fake.calls) != 0 {
		t.Fatalf("IPC client must not be called for oversized body")
	}
}

func TestAdminUpdates_MalformedJSONRejected(t *testing.T) {
	router, _, fake, token, csrf := buildAdminUpdatesHarness(t)
	resp, _ := adminUpdatesRequest(t, router, "POST", "/api/v1/admin/updates/check", auReqOpts{
		token: token, csrf: true, csrfVal: csrf, body: `{"channel": not-json`,
	})
	if resp.StatusCode != 400 {
		t.Fatalf("expected 400 for malformed JSON, got %d", resp.StatusCode)
	}
	if len(fake.calls) != 0 {
		t.Fatalf("IPC client must not be called for malformed JSON")
	}
}

func TestAdminUpdates_UnknownFieldRejected(t *testing.T) {
	router, _, fake, token, csrf := buildAdminUpdatesHarness(t)
	resp, _ := adminUpdatesRequest(t, router, "POST", "/api/v1/admin/updates/check", auReqOpts{
		token: token, csrf: true, csrfVal: csrf, body: `{"channel":"stable","unexpected_field":"x"}`,
	})
	if resp.StatusCode != 400 {
		t.Fatalf("expected 400 for unknown field, got %d", resp.StatusCode)
	}
	if len(fake.calls) != 0 {
		t.Fatalf("IPC client must not be called for a body with unknown fields")
	}
}

// ── Read-only endpoints happy path ───────────────────────────────

func TestAdminUpdates_StatusHappyPath(t *testing.T) {
	router, _, fake, token, _ := buildAdminUpdatesHarness(t)
	resp, body := adminUpdatesRequest(t, router, "GET", "/api/v1/admin/updates/status", auReqOpts{token: token})
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}
	if len(fake.calls) != 1 || fake.calls[0].Op != selfupdate.OpStatus {
		t.Fatalf("expected exactly one OpStatus call, got %+v", fake.calls)
	}
}

func TestAdminUpdates_HistoryAndSnapshotsHappyPath(t *testing.T) {
	router, _, fake, token, _ := buildAdminUpdatesHarness(t)
	fake.resp = selfupdate.Response{OK: true, Jobs: []selfupdate.Job{{ID: "job_1"}}, Snapshots: []selfupdate.RollbackSnapshot{{ID: "snap_1"}}}

	resp, body := adminUpdatesRequest(t, router, "GET", "/api/v1/admin/updates/history", auReqOpts{token: token})
	if resp.StatusCode != 200 {
		t.Fatalf("history: expected 200, got %d: %s", resp.StatusCode, body)
	}
	resp, body = adminUpdatesRequest(t, router, "GET", "/api/v1/admin/updates/snapshots", auReqOpts{token: token})
	if resp.StatusCode != 200 {
		t.Fatalf("snapshots: expected 200, got %d: %s", resp.StatusCode, body)
	}
}

// ── Phase K: pathological Unicode fuzzing through the HTTP boundary ──
//
// internal/selfupdate/security_test.go already fuzzes Request.Validate
// directly. These tests fuzz the same pathological values through the
// actual HTTP handlers (PostAdminUpdatesInstall/Check/Preflight), which
// apply their own validation (selfupdate.ValidateVersionString /
// ValidateChannel) BEFORE ever constructing a selfupdate.Request — a
// distinct code path from protocol.Validate. The requirement is: every
// pathological value is rejected with 400 and the request never reaches
// the IPC client (fake.calls stays empty), and none of them panic the
// handler.
var auPathologicalStrings = []string{
	"\xed\xa0\x80",                       // unpaired UTF-16 surrogate as raw bytes
	"\xc0\xaf",                           // overlong UTF-8
	"1.0.0\x00malicious",                 // embedded NUL
	"1.0.0‮",                             // RTL override control char
	"1.0.0\U0001F4A9",                    // emoji / astral-plane rune
	"1.0.0\r\nSet-Cookie: evil=1",        // CRLF injection shape
	"../../../../etc/passwd",             // path traversal
	"/etc/passwd",                        // absolute path
	"http://169.254.169.254/latest/meta", // SSRF-shaped value
}

func TestAdminUpdates_UnicodeFuzzRequestedVersionRejected(t *testing.T) {
	router, _, fake, token, csrf := buildAdminUpdatesHarness(t)
	for _, v := range auPathologicalStrings {
		body, _ := json.Marshal(map[string]string{
			"password": "TestPassword123!", "idempotency_key": "k", "requested_version": v,
		})
		resp, respBody := adminUpdatesRequest(t, router, "POST", "/api/v1/admin/updates/install", auReqOpts{
			token: token, csrf: true, csrfVal: csrf, body: string(body),
		})
		if resp.StatusCode != 400 {
			t.Errorf("requested_version %q: expected 400, got %d: %s", v, resp.StatusCode, respBody)
		}
	}
	if len(fake.calls) != 0 {
		t.Fatalf("IPC client must never be called for a pathological requested_version, got %d calls", len(fake.calls))
	}
}

func TestAdminUpdates_UnicodeFuzzChannelRejected(t *testing.T) {
	router, _, fake, token, csrf := buildAdminUpdatesHarness(t)
	for _, v := range auPathologicalStrings {
		body, _ := json.Marshal(map[string]string{"channel": v})
		resp, respBody := adminUpdatesRequest(t, router, "POST", "/api/v1/admin/updates/check", auReqOpts{
			token: token, csrf: true, csrfVal: csrf, body: string(body),
		})
		if resp.StatusCode != 400 {
			t.Errorf("channel %q: expected 400, got %d: %s", v, resp.StatusCode, respBody)
		}
	}
	if len(fake.calls) != 0 {
		t.Fatalf("IPC client must never be called for a pathological channel, got %d calls", len(fake.calls))
	}
}

func TestAdminUpdates_UnicodeFuzzIdempotencyKeyHandledSafely(t *testing.T) {
	// idempotency_key has no charset restriction (see protocol.go), only
	// a length cap enforced downstream by selfupdate.Request.Validate —
	// the HTTP handler itself only checks non-empty. This test proves a
	// pathological key does not panic the handler and either succeeds
	// (reaching the fake IPC client, since the daemon itself is the
	// layer that would reject it) or is cleanly rejected — never a 5xx.
	router, _, _, token, csrf := buildAdminUpdatesHarness(t)
	for _, v := range auPathologicalStrings {
		body, _ := json.Marshal(map[string]string{
			"password": "TestPassword123!", "idempotency_key": v, "requested_version": "1.2.3",
		})
		resp, respBody := adminUpdatesRequest(t, router, "POST", "/api/v1/admin/updates/install", auReqOpts{
			token: token, csrf: true, csrfVal: csrf, body: string(body),
		})
		if resp.StatusCode >= 500 {
			t.Errorf("idempotency_key %q: handler returned 5xx: %d: %s", v, resp.StatusCode, respBody)
		}
	}
}

// ── Phase K special investigation: "stale re-auth" does not apply ────
//
// requireSelfUpdateReauth (admin_updates.go) has no expiry/time-window
// concept whatsoever: it reads the "password" field fresh from THIS
// request's body and calls h.auth.VerifyPassword against the live DB
// hash on every single call — there is no cacheable step-up token, no
// session flag, nothing that could go "stale". This test proves that
// directly: a successful, freshly-reauthenticated install call is
// immediately followed by a second privileged call that omits the
// password, and the second call is rejected exactly like a
// never-authenticated one — there is no grace window whatsoever, so
// "stale re-auth" (a token that was valid but has since expired) is not
// a category this implementation has. See requireSelfUpdateReauth's own
// doc comment (admin_updates.go, "NOTE ON PROVENANCE") for the same
// conclusion from the implementer's side.
func TestAdminUpdates_ReauthHasNoStaleWindow_FreshCheckEveryCall(t *testing.T) {
	router, _, fake, token, csrf := buildAdminUpdatesHarness(t)

	body1, _ := json.Marshal(map[string]string{
		"password": "TestPassword123!", "idempotency_key": "key-fresh-1", "requested_version": "1.2.3",
	})
	resp1, respBody1 := adminUpdatesRequest(t, router, "POST", "/api/v1/admin/updates/install", auReqOpts{
		token: token, csrf: true, csrfVal: csrf, body: string(body1),
	})
	if resp1.StatusCode != 200 {
		t.Fatalf("expected first (properly reauthenticated) call to succeed, got %d: %s", resp1.StatusCode, respBody1)
	}

	// Immediately reuse the same authenticated session/token, but omit
	// the password this time. If there were any cacheable step-up
	// window, this would still be within it (zero elapsed time).
	body2, _ := json.Marshal(map[string]string{
		"idempotency_key": "key-fresh-2", "requested_version": "1.2.4",
	})
	resp2, respBody2 := adminUpdatesRequest(t, router, "POST", "/api/v1/admin/updates/install", auReqOpts{
		token: token, csrf: true, csrfVal: csrf, body: string(body2),
	})
	if resp2.StatusCode != 401 {
		t.Fatalf("expected the very next call (no password) to be rejected with 401 despite zero elapsed time since a valid reauth, got %d: %s", resp2.StatusCode, respBody2)
	}
	if len(fake.calls) != 1 {
		t.Fatalf("expected exactly 1 IPC call (only the properly reauthenticated one), got %d", len(fake.calls))
	}
}

// ── Rate limiting ─────────────────────────────────────────────────

// TestAdminUpdates_WriteRateLimitEnforced confirms the real
// production rate limiter wired in internal/api/router.go
// (selfUpdateWriteLimiter := newUserRateLimiter("selfupdate_write",
// 10, time.Minute, ...), applied to POST /check, /preflight,
// /install, /rollback) actually triggers: the 11th write request from
// the same authenticated user within the 1-minute window must be
// rejected with 429, with a Retry-After header, before ever reaching
// the handler/IPC layer.
func TestAdminUpdates_WriteRateLimitEnforced(t *testing.T) {
	router, _, fake, token, csrf := buildAdminUpdatesHarness(t)

	var last *http.Response
	for i := 0; i < 10; i++ {
		resp, body := adminUpdatesRequest(t, router, "POST", "/api/v1/admin/updates/check", auReqOpts{
			token: token, csrf: true, csrfVal: csrf, body: `{}`,
		})
		if resp.StatusCode != 200 {
			t.Fatalf("request %d: expected 200 within the rate-limit budget, got %d: %s", i+1, resp.StatusCode, body)
		}
		last = resp
	}
	_ = last

	resp, body := adminUpdatesRequest(t, router, "POST", "/api/v1/admin/updates/check", auReqOpts{
		token: token, csrf: true, csrfVal: csrf, body: `{}`,
	})
	if resp.StatusCode != 429 {
		t.Fatalf("expected 429 once the write rate limit (10/min) is exceeded, got %d: %s", resp.StatusCode, body)
	}
	if resp.Header.Get("Retry-After") == "" {
		t.Error("expected a Retry-After header on the 429 response")
	}
	if len(fake.calls) != 10 {
		t.Fatalf("expected exactly 10 IPC calls (the 11th must be blocked before reaching the handler), got %d", len(fake.calls))
	}
}

// TestAdminUpdates_ReadRateLimitEnforced does the same for the read
// limiter (selfUpdateReadLimiter, 60/min) applied to GET /status etc.
// 60 real requests is expensive to run per-test-invocation but still
// well within normal `go test` budgets, so the full threshold is
// exercised rather than mocked/shortened.
func TestAdminUpdates_ReadRateLimitEnforced(t *testing.T) {
	router, _, fake, token, _ := buildAdminUpdatesHarness(t)

	for i := 0; i < 60; i++ {
		resp, body := adminUpdatesRequest(t, router, "GET", "/api/v1/admin/updates/status", auReqOpts{token: token})
		if resp.StatusCode != 200 {
			t.Fatalf("request %d: expected 200 within the read rate-limit budget, got %d: %s", i+1, resp.StatusCode, body)
		}
	}

	resp, body := adminUpdatesRequest(t, router, "GET", "/api/v1/admin/updates/status", auReqOpts{token: token})
	if resp.StatusCode != 429 {
		t.Fatalf("expected 429 once the read rate limit (60/min) is exceeded, got %d: %s", resp.StatusCode, body)
	}
	if len(fake.calls) != 60 {
		t.Fatalf("expected exactly 60 IPC calls (the 61st must be blocked before reaching the handler), got %d", len(fake.calls))
	}
}
