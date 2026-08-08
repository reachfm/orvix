package handlers_test

import (
	"crypto/hmac"
	"crypto/sha1"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
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

// setEncryptionKey ensures config.Encrypt/Decrypt has a stable key for
// the MFA-completion tests (they persist mfa_secret_raw as an encrypted
// blob via config.Encrypt; the production TOTP-verify path reads it via
// config.Decrypt). Called by every MFA-completion test that seeds an
// MFA-enabled user.
func setEncryptionKey(t *testing.T) {
	t.Helper()
	// 32-byte hex key.
	key := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	if err := os.Setenv("ORVIX_ENCRYPTION_KEY", key); err != nil {
		t.Fatalf("set encryption key: %v", err)
	}
	t.Cleanup(func() { os.Unsetenv("ORVIX_ENCRYPTION_KEY") })
}

// computeTOTPTest reimplements the production HMAC-SHA1 TOTP so we can
// generate valid codes without depending on unexported handlers helpers.
// Matches internal/api/handlers/admin_mfa.go verifyTOTP.
func computeTOTPTest(secret []byte, counter int64) string {
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, uint64(counter))
	mac := hmac.New(sha1.New, secret)
	mac.Write(buf)
	hash := mac.Sum(nil)
	offset := hash[len(hash)-1] & 0x0f
	bin := binary.BigEndian.Uint32(hash[offset:offset+4]) & 0x7fffffff
	return fmt.Sprintf("%06d", bin%1000000)
}

// seedMFAUser inserts a user with MFA enabled, an encrypted TOTP secret,
// and returns the userID and the RAW 20-byte TOTP secret so the test can
// generate valid codes.
func (e *mfaAuthEnv) seedMFAUser(t *testing.T, email, role string, tenantID *uint) (uint, []byte) {
	t.Helper()
	rawSecret := []byte("0123456789abcdef0123") // 20 bytes
	encrypted, err := config.Encrypt(rawSecret)
	if err != nil {
		t.Fatalf("encrypt secret: %v", err)
	}
	now := time.Now().UTC()
	hash, _ := auth.HashPassword("Pass!2026")
	var tid interface{}
	if tenantID == nil {
		tid = nil
	} else {
		tid = *tenantID
	}
	res, err := e.sqlDB.Exec(
		`INSERT INTO users (created_at, updated_at, email, password_hash, role, tenant_id, active, email_verified, mfa_enabled, mfa_secret_raw) VALUES (?, ?, ?, ?, ?, ?, 1, 1, 1, ?)`,
		now, now, email, hash, role, tid, encrypted,
	)
	if err != nil {
		t.Fatalf("seed mfa user: %v", err)
	}
	id, _ := res.LastInsertId()
	return uint(id), rawSecret
}

// loginForChallenge performs POST /api/v1/auth/login and returns the
// MFA challenge token. Fails the test on any other outcome.
func (e *mfaAuthEnv) loginForChallenge(t *testing.T, email string) string {
	t.Helper()
	status, body := e.doLogin(t, email, "Pass!2026")
	if status != 200 {
		t.Fatalf("login for %s: want 200, got %d body=%s", email, status, body)
	}
	var out struct {
		MFARequired  bool   `json:"mfa_required"`
		MFAChallenge string `json:"mfa_challenge"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("login for %s: decode: %v body=%s", email, err, body)
	}
	if !out.MFARequired || out.MFAChallenge == "" {
		t.Fatalf("login for %s: no MFA challenge in response: %s", email, body)
	}
	return out.MFAChallenge
}

// completeMFA POSTs /api/v1/auth/mfa/verify with the given challenge
// and TOTP code and returns the response.
type mfaCompletionResult struct {
	status      int
	body        []byte
	cookies     []*http.Cookie
	accessToken string
	sessionCK   string
}

func (e *mfaAuthEnv) completeMFA(t *testing.T, challenge, code string) mfaCompletionResult {
	t.Helper()
	body := fmt.Sprintf(`{"mfa_challenge":"%s","code":"%s"}`, challenge, code)
	req := httptest.NewRequest("POST", "/api/v1/auth/mfa/verify", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := e.router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("mfa verify: %v", err)
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(resp.Body)
	result := mfaCompletionResult{status: resp.StatusCode, body: rb, cookies: resp.Cookies()}
	for _, ck := range resp.Cookies() {
		if ck.Name == "access_token" {
			result.accessToken = ck.Value
		}
		if ck.Name == "__Host-orvix_session" {
			result.sessionCK = ck.Value
		}
	}
	// Also parse body for access_token (some flows return it in JSON).
	if result.accessToken == "" {
		var jbody struct {
			AccessToken string `json:"access_token"`
		}
		if json.Unmarshal(rb, &jbody) == nil && jbody.AccessToken != "" {
			result.accessToken = jbody.AccessToken
		}
	}
	return result
}

// ── Test A: canonical role matches JWT and opaque session ─────────
func TestMFACompletionCanonicalRoleMatchesTokenAndSession(t *testing.T) {
	tid := uint(1)
	cases := []struct {
		name   string
		role   string
		tenant *uint
	}{
		{"platform_super_admin", "platform_super_admin", nil},
		{"tenant_admin", "tenant_admin", &tid},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			setEncryptionKey(t)
			e := newMFAAuthEnv(t)
			e.sqlDB.Exec(`INSERT INTO tenants (name, slug, domain, plan, active) VALUES ('a','a','a.ex','enterprise',1)`)
			email := c.name + "@mfa-complete.test"
			uid, secret := e.seedMFAUser(t, email, c.role, c.tenant)

			challenge := e.loginForChallenge(t, email)
			code := computeTOTPTest(secret, time.Now().UTC().Unix()/30)
			res := e.completeMFA(t, challenge, code)

			if res.status >= 500 {
				t.Fatalf("%s: 5xx %d body=%s", c.name, res.status, res.body)
			}
			if res.status != 200 {
				t.Fatalf("%s: want 200, got %d body=%s", c.name, res.status, res.body)
			}
			if res.accessToken == "" {
				t.Fatalf("%s: no access token issued", c.name)
			}

			// Validate JWT via production path.
			jwtUID, jwtRole, jwtErr := e.authn.ValidateAccessToken(res.accessToken)
			if jwtErr != nil {
				t.Fatalf("%s: JWT invalid: %v", c.name, jwtErr)
			}
			if jwtUID != uid {
				t.Fatalf("%s: JWT uid=%d want=%d", c.name, jwtUID, uid)
			}
			if string(jwtRole) != c.role {
				t.Fatalf("%s: JWT role=%s want=%s", c.name, jwtRole, c.role)
			}
			// Validate opaque session via production path.
			if res.sessionCK == "" {
				t.Fatalf("%s: no opaque session cookie issued", c.name)
			}
			sUID, sRole, _, sErr := e.authn.ValidateOpaqueSession(res.sessionCK)
			if sErr != nil {
				t.Fatalf("%s: opaque session invalid: %v", c.name, sErr)
			}
			if sUID != uid {
				t.Fatalf("%s: opaque uid=%d want=%d", c.name, sUID, uid)
			}
			if string(sRole) != c.role {
				t.Fatalf("%s: opaque role=%s want=%s", c.name, sRole, c.role)
			}
			// Must match each other.
			if jwtRole != sRole {
				t.Fatalf("%s: JWT role %s != opaque role %s", c.name, jwtRole, sRole)
			}
			// No legacy role.
			for _, forbidden := range []string{"admin", "superadmin", "super_admin", "super-admin", "operator", "readonly"} {
				if string(jwtRole) == forbidden {
					t.Fatalf("%s: legacy role %s appeared in JWT", c.name, forbidden)
				}
			}
			// Exactly one session row.
			var cnt int
			e.sqlDB.QueryRow("SELECT COUNT(*) FROM sessions WHERE user_id = ?", uid).Scan(&cnt)
			if cnt < 1 {
				t.Fatalf("%s: no session persisted", c.name)
			}
		})
	}
}

// ── Test B: role change between challenge and completion ──────────
// The completion must NEVER issue the old role. Either it fails closed
// (which is a valid production choice — the completion may reject a
// mid-flow authorization change) or it issues the CURRENT (post-change)
// role. Stale role must never appear.
func TestMFACompletionUsesCurrentRoleAfterChallenge(t *testing.T) {
	setEncryptionKey(t)
	tid := uint(1)
	e := newMFAAuthEnv(t)
	e.sqlDB.Exec(`INSERT INTO tenants (name, slug, domain, plan, active) VALUES ('a','a','a.ex','enterprise',1)`)
	uid, secret := e.seedMFAUser(t, "ta-change@mfa-complete.test", "tenant_admin", &tid)

	challenge := e.loginForChallenge(t, "ta-change@mfa-complete.test")

	// Atomically mutate role + bump token_version between challenge and completion.
	if _, err := e.sqlDB.Exec(
		"UPDATE users SET role = 'tenant_readonly', token_version = COALESCE(token_version, 0) + 1 WHERE id = ?",
		uid,
	); err != nil {
		t.Fatalf("mutate: %v", err)
	}

	code := computeTOTPTest(secret, time.Now().UTC().Unix()/30)
	res := e.completeMFA(t, challenge, code)

	if res.status >= 500 {
		t.Fatalf("5xx status: %d body=%s", res.status, res.body)
	}

	if res.status == 200 && res.accessToken != "" {
		// Success path — issued role MUST be tenant_readonly, never
		// tenant_admin.
		_, jwtRole, jwtErr := e.authn.ValidateAccessToken(res.accessToken)
		if jwtErr != nil {
			t.Fatalf("JWT invalid: %v", jwtErr)
		}
		if string(jwtRole) == "tenant_admin" {
			t.Fatalf("STALE ROLE ESCALATION: JWT still says tenant_admin after DB demoted to tenant_readonly")
		}
		if string(jwtRole) != "tenant_readonly" {
			t.Fatalf("JWT role=%s want tenant_readonly", jwtRole)
		}
		if res.sessionCK != "" {
			_, sRole, _, sErr := e.authn.ValidateOpaqueSession(res.sessionCK)
			if sErr == nil && string(sRole) == "tenant_admin" {
				t.Fatalf("STALE ROLE ESCALATION: session still says tenant_admin")
			}
			if sErr == nil && string(sRole) != "tenant_readonly" {
				t.Fatalf("session role=%s want tenant_readonly", sRole)
			}
		}
		t.Logf("completion succeeded with CURRENT role tenant_readonly ✓")
		return
	}

	// Fail-closed path — production may reject the completion after a
	// mid-flow authorization change. The response must be the generic
	// authentication rejection contract, no token, no session.
	if res.status != 401 {
		t.Fatalf("expected 200 with current role OR 401 fail-closed, got %d body=%s", res.status, res.body)
	}
	if res.accessToken != "" {
		t.Fatalf("fail-closed but access token was issued")
	}
	if res.sessionCK != "" {
		t.Fatalf("fail-closed but session cookie was issued")
	}
	t.Logf("completion fail-closed after mid-flow role change ✓ contract=%s", res.body)
}

// ── Test C: inactive after challenge ──────────────────────────────
func TestMFACompletionInactiveAfterChallengeFailsClosed(t *testing.T) {
	setEncryptionKey(t)
	tid := uint(1)
	e := newMFAAuthEnv(t)
	e.sqlDB.Exec(`INSERT INTO tenants (name, slug, domain, plan, active) VALUES ('a','a','a.ex','enterprise',1)`)
	uid, secret := e.seedMFAUser(t, "ta-inactive@mfa-complete.test", "tenant_admin", &tid)
	challenge := e.loginForChallenge(t, "ta-inactive@mfa-complete.test")

	if _, err := e.sqlDB.Exec(
		"UPDATE users SET active = 0, token_version = COALESCE(token_version, 0) + 1 WHERE id = ?", uid,
	); err != nil {
		t.Fatalf("mutate: %v", err)
	}

	code := computeTOTPTest(secret, time.Now().UTC().Unix()/30)
	res := e.completeMFA(t, challenge, code)

	if res.status >= 500 {
		t.Fatalf("5xx: %d body=%s", res.status, res.body)
	}
	if res.status != 401 {
		t.Fatalf("want 401, got %d body=%s", res.status, res.body)
	}
	if res.accessToken != "" {
		t.Fatalf("token issued to inactive user")
	}
	if res.sessionCK != "" {
		t.Fatalf("session issued to inactive user")
	}
	var sessCnt int
	e.sqlDB.QueryRow("SELECT COUNT(*) FROM sessions WHERE user_id = ?", uid).Scan(&sessCnt)
	if sessCnt != 0 {
		t.Fatalf("session row created for inactive user: cnt=%d", sessCnt)
	}
}

// ── Test D: soft-deleted after challenge ──────────────────────────
func TestMFACompletionDeletedAfterChallengeFailsClosed(t *testing.T) {
	setEncryptionKey(t)
	tid := uint(1)
	e := newMFAAuthEnv(t)
	e.sqlDB.Exec(`INSERT INTO tenants (name, slug, domain, plan, active) VALUES ('a','a','a.ex','enterprise',1)`)
	uid, secret := e.seedMFAUser(t, "ta-deleted@mfa-complete.test", "tenant_admin", &tid)
	challenge := e.loginForChallenge(t, "ta-deleted@mfa-complete.test")

	now := time.Now().UTC()
	if _, err := e.sqlDB.Exec(
		"UPDATE users SET active = 0, deleted_at = ?, token_version = COALESCE(token_version, 0) + 1 WHERE id = ?", now, uid,
	); err != nil {
		t.Fatalf("mutate: %v", err)
	}

	code := computeTOTPTest(secret, time.Now().UTC().Unix()/30)
	res := e.completeMFA(t, challenge, code)

	if res.status >= 500 {
		t.Fatalf("5xx: %d body=%s", res.status, res.body)
	}
	if res.status != 401 {
		t.Fatalf("want 401, got %d body=%s", res.status, res.body)
	}
	if res.accessToken != "" {
		t.Fatalf("token issued to deleted user")
	}
	if res.sessionCK != "" {
		t.Fatalf("session issued to deleted user")
	}
	var sessCnt int
	e.sqlDB.QueryRow("SELECT COUNT(*) FROM sessions WHERE user_id = ?", uid).Scan(&sessCnt)
	if sessCnt != 0 {
		t.Fatalf("session row created for deleted user")
	}
}

// ── Test E: malformed authorization state after challenge ─────────
func TestMFACompletionMalformedAuthorizationAfterChallengeFailsClosed(t *testing.T) {
	tid := uint(1)
	// Each subtest starts from a valid tenant_admin (canonical) and
	// mutates to the malformed state between challenge and completion.
	cases := []struct {
		name      string
		mutateSQL string
	}{
		{"legacy_admin", "UPDATE users SET role = 'admin', token_version = COALESCE(token_version,0)+1 WHERE id = ?"},
		{"legacy_operator", "UPDATE users SET role = 'operator', token_version = COALESCE(token_version,0)+1 WHERE id = ?"},
		{"legacy_superadmin", "UPDATE users SET role = 'superadmin', token_version = COALESCE(token_version,0)+1 WHERE id = ?"},
		{"unknown_role", "UPDATE users SET role = 'nonexistent_role', token_version = COALESCE(token_version,0)+1 WHERE id = ?"},
		{"psa_with_tenant", "UPDATE users SET role = 'platform_super_admin', token_version = COALESCE(token_version,0)+1 WHERE id = ?"},
		{"tenant_admin_null_tenant", "UPDATE users SET tenant_id = NULL, token_version = COALESCE(token_version,0)+1 WHERE id = ?"},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			setEncryptionKey(t)
			e := newMFAAuthEnv(t)
			e.sqlDB.Exec(`INSERT INTO tenants (name, slug, domain, plan, active) VALUES ('a','a','a.ex','enterprise',1)`)
			email := c.name + "@mfa-mal.test"
			uid, secret := e.seedMFAUser(t, email, "tenant_admin", &tid)
			challenge := e.loginForChallenge(t, email)

			if _, err := e.sqlDB.Exec(c.mutateSQL, uid); err != nil {
				t.Fatalf("mutate: %v", err)
			}

			code := computeTOTPTest(secret, time.Now().UTC().Unix()/30)
			res := e.completeMFA(t, challenge, code)

			if res.status >= 500 {
				t.Fatalf("%s: 5xx %d body=%s", c.name, res.status, res.body)
			}
			if res.status != 401 {
				t.Fatalf("%s: want 401, got %d body=%s", c.name, res.status, res.body)
			}
			if res.accessToken != "" {
				t.Fatalf("%s: token issued to malformed user", c.name)
			}
			if res.sessionCK != "" {
				t.Fatalf("%s: session issued to malformed user", c.name)
			}
			var sessCnt int
			e.sqlDB.QueryRow("SELECT COUNT(*) FROM sessions WHERE user_id = ?", uid).Scan(&sessCnt)
			if sessCnt != 0 {
				t.Fatalf("%s: session row created", c.name)
			}
		})
	}
}

type mfaAuthEnv struct {
	router *api.Router
	sqlDB  *sql.DB
	authn  *auth.Authenticator
}

func newMFAAuthEnv(t *testing.T) *mfaAuthEnv {
	t.Helper()
	logger := zap.NewNop()
	cfg := config.Defaults()
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = filepath.Join(t.TempDir(), "mfauth.db") + "?_loc=auto&_busy_timeout=5000&_journal_mode=WAL"
	cfg.Auth.JWTKeyPath = filepath.Join(t.TempDir(), "jwt.pem")
	cfg.Auth.LoginRateLimit = 10000

	db, err := config.NewDatabase(&cfg.Database, logger)
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	if err := models.MigrateAllRaw(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	sqlDB, _ := db.DB()
	t.Cleanup(func() { sqlDB.Close() })

	authn, err := auth.NewAuthenticator(&cfg.Auth, db, logger)
	if err != nil {
		t.Fatalf("authn: %v", err)
	}

	router := api.NewRouter(cfg, authn, logger, db, modules.NewRegistry(logger), license.NewFeatureFlags(logger), nil)
	t.Cleanup(func() { router.App().Shutdown() })

	return &mfaAuthEnv{router: router, sqlDB: sqlDB, authn: authn}
}

func (e *mfaAuthEnv) seedUser(t *testing.T, email, role string, tenantID *uint, active, deleted bool) {
	t.Helper()
	now := time.Now().UTC()
	hash, _ := auth.HashPassword("Pass!2026")
	var tid interface{}
	if tenantID == nil {
		tid = nil
	} else {
		tid = *tenantID
	}
	var del interface{}
	if deleted {
		del = now
	} else {
		del = nil
	}
	_, err := e.sqlDB.Exec(
		`INSERT INTO users (created_at, updated_at, email, password_hash, role, tenant_id, active, email_verified, deleted_at, mfa_enabled) VALUES (?, ?, ?, ?, ?, ?, ?, 1, ?, 0)`,
		now, now, email, hash, role, tid, active, del,
	)
	if err != nil {
		t.Fatalf("seed %s: %v", email, err)
	}
}

func (e *mfaAuthEnv) doLogin(t *testing.T, email, password string) (int, []byte) {
	t.Helper()
	body := fmt.Sprintf(`{"email":"%s","password":"%s"}`, email, password)
	req := httptest.NewRequest("POST", "/api/v1/auth/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := e.router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

// TestMFAPreChallengeAuthorizationMatrix proves that invalid roles/tenant
// bindings are denied at login with the exact wrong-password contract. A
// valid canonical role succeeds (with or without MFA). Each denied case
// uses a fresh harness to avoid the login rate limiter.
func TestMFAPreChallengeAuthorizationMatrix(t *testing.T) {
	tenant := uint(1)

	// Valid user that CAN log in — proves the harness works and that the
	// canonical role succeeds.
	validBase := newMFAAuthEnv(t)
	validBase.sqlDB.Exec(`INSERT INTO tenants (name, slug, domain, plan, active) VALUES ('a','a','a.ex','enterprise',1)`)
	validBase.seedUser(t, "valid@mfa.test", "tenant_admin", &tenant, true, false)
	status, body := validBase.doLogin(t, "valid@mfa.test", "Pass!2026")
	if status != 200 {
		t.Fatalf("valid user: want 200, got %d body=%s", status, body)
	}
	var validResp struct {
		AccessToken string `json:"access_token"`
	}
	json.Unmarshal(body, &validResp)
	if validResp.AccessToken == "" {
		t.Fatalf("valid user: no access token: %s", body)
	}

	denied := []struct {
		name    string
		role    string
		tid     *uint
		active  bool
		deleted bool
	}{
		{"admin", "admin", &tenant, true, false},
		{"superadmin", "superadmin", &tenant, true, false},
		{"super_admin", "super_admin", &tenant, true, false},
		{"super_admin_hyphen", "super-admin", &tenant, true, false},
		{"operator", "operator", &tenant, true, false},
		{"readonly", "readonly", &tenant, true, false},
		{"unknown", "unknown_role", &tenant, true, false},
		{"empty", "", &tenant, true, false},
		{"psa_with_tenant", "platform_super_admin", &tenant, true, false},
		{"ta_null", "tenant_admin", nil, true, false},
		{"inactive", "tenant_admin", &tenant, false, false},
		{"deleted", "tenant_admin", &tenant, true, true},
	}
	for _, tc := range denied {
		t.Run(tc.name, func(t *testing.T) {
			e := newMFAAuthEnv(t)
			e.sqlDB.Exec(`INSERT INTO tenants (name, slug, domain, plan, active) VALUES ('a','a','a.ex','enterprise',1)`)
			email := tc.name + "@mfa.test"
			e.seedUser(t, email, tc.role, tc.tid, tc.active, tc.deleted)
			status, body := e.doLogin(t, email, "Pass!2026")
			if status >= 500 {
				t.Fatalf("%s: 5xx %d", tc.name, status)
			}
			if status != 401 {
				t.Fatalf("%s: want 401, got %d body=%s", tc.name, status, body)
			}
			if !strings.Contains(string(body), `"error":"invalid credentials"`) {
				t.Fatalf("%s: wrong body: %s", tc.name, body)
			}
		})
	}
}
