package handlers_test

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
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
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

// The signup-OTP test env reuses newSignupTxEnvSQLite (customer_signup_transaction_test.go),
// which already stands up a real router + SQLite DB with the exact same JWT
// secret config.Defaults() uses. Because the OTP hash is HMAC-SHA256 keyed
// with cfg.Auth.JWTSecret (see hashOTP in customer_signup_otp.go) and the
// code space is only 10^6, tests recover the plaintext code by brute-forcing
// the digest locally with the known secret — this avoids needing to
// intercept the outbound mail sender (which isn't exposed for override
// through the public api.Router surface) while still exercising the real
// production hashing path end-to-end.

func otpTestSecret() string {
	return "test-secret-64-bytes-signup-otp-fixture-XXXXXXXXXXXXXXXXXXXXXXXX"
}

// fakeMailSender is a deterministic, in-memory handlers.MailSender for
// tests. The router's default sender (initTransactionalMailSender) dials
// real SMTP based on cfg.CoreMail.SMTPHost/Port (0.0.0.0:25 by default),
// which — now that sendOTPEmail's synchronous failures are surfaced to the
// caller instead of being swallowed — would make every OTP test dependent
// on real network conditions. Router.SetMailSender lets tests swap in this
// fake instead.
type fakeMailSender struct {
	mu   sync.Mutex
	fail bool
	sent []fakeSentMail
}

type fakeSentMail struct {
	To, Subject, Body string
}

func newFakeMailSender(fail bool) *fakeMailSender {
	return &fakeMailSender{fail: fail}
}

func (f *fakeMailSender) Send(to, subject, body string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail {
		return fmt.Errorf("fake smtp: simulated delivery failure")
	}
	f.sent = append(f.sent, fakeSentMail{To: to, Subject: subject, Body: body})
	return nil
}

func (f *fakeMailSender) setFail(v bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fail = v
}

func (f *fakeMailSender) sentCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.sent)
}

// newSignupTxEnvSQLiteWithSecret is newSignupTxEnvSQLite (customer_signup_transaction_test.go)
// with a fixed JWTSecret, so tests can independently recover the OTP
// plaintext by brute-forcing the known-keyed HMAC (see bruteForceOTP above).
// It wires a default SUCCEEDING fakeMailSender so existing tests exercise
// real send-success behavior without touching the network; tests that need
// to exercise delivery failure should override via
// env.router.SetMailSender(newFakeMailSender(true)).
func newSignupTxEnvSQLiteWithSecret(t *testing.T, secret string) *signupTxEnv {
	t.Helper()
	cfg := config.Defaults()
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = t.TempDir() + "/signup-otp.db?_loc=auto&_busy_timeout=5000&_txlock=immediate"
	cfg.Auth.JWTSecret = secret
	env := newSignupTxEnv(t, cfg)
	env.router.SetMailSender(newFakeMailSender(false))
	return env
}

func bruteForceOTP(t *testing.T, secret, wantHash string) string {
	t.Helper()
	for i := 0; i < 1000000; i++ {
		code := fmt.Sprintf("%06d", i)
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write([]byte(code))
		if hex.EncodeToString(mac.Sum(nil)) == wantHash {
			return code
		}
	}
	t.Fatalf("could not recover OTP for hash %s", wantHash)
	return ""
}

func otpHashFor(t *testing.T, sqlDB *sql.DB, email string) string {
	t.Helper()
	var h string
	if err := sqlDB.QueryRow(`SELECT otp_hash FROM pending_registrations WHERE normalized_email = ?`, email).Scan(&h); err != nil {
		t.Fatalf("read otp_hash: %v", err)
	}
	return h
}

func otpDoJSON(t *testing.T, env *signupTxEnv, method, path string, payload map[string]string) (int, map[string]interface{}) {
	t.Helper()
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := env.router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	var out map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func TestSignupOTP_HappyPath_ActivatesExactlyOneTenantAndOwner(t *testing.T) {
	env := newSignupTxEnvSQLiteWithSecret(t, otpTestSecret())
	email := "otp-happy@example.com"

	status, _ := otpDoJSON(t, env, "POST", "/api/v1/auth/signup/start", map[string]string{
		"email": email, "password": "StrongPass123", "name": "Otp Happy Co",
	})
	if status != http.StatusOK {
		t.Fatalf("signup/start status=%d", status)
	}

	hash := otpHashFor(t, env.sqlDB, email)
	code := bruteForceOTP(t, otpTestSecret(), hash)

	status, out := otpDoJSON(t, env, "POST", "/api/v1/auth/signup/verify", map[string]string{"email": email, "code": code})
	if status != http.StatusCreated {
		t.Fatalf("signup/verify status=%d body=%v", status, out)
	}
	if out["access_token"] == "" || out["access_token"] == nil {
		t.Fatalf("expected access_token in verify response, got %v", out)
	}

	assertSignupCounts(t, env.sqlDB, email, "example.com", 1, 1, 1)

	var consumedAt sql.NullTime
	if err := env.sqlDB.QueryRow(`SELECT consumed_at FROM pending_registrations WHERE normalized_email = ?`, email).Scan(&consumedAt); err != nil {
		t.Fatalf("read consumed_at: %v", err)
	}
	if !consumedAt.Valid {
		t.Fatalf("pending registration not marked consumed after successful verify")
	}
}

func TestSignupOTP_IncorrectCodeIncrementsAttemptsAndDoesNotActivate(t *testing.T) {
	env := newSignupTxEnvSQLiteWithSecret(t, otpTestSecret())
	email := "otp-wrong@example.com"
	otpDoJSON(t, env, "POST", "/api/v1/auth/signup/start", map[string]string{
		"email": email, "password": "StrongPass123", "name": "X",
	})

	status, _ := otpDoJSON(t, env, "POST", "/api/v1/auth/signup/verify", map[string]string{"email": email, "code": "000001"})
	// Extremely unlikely 000001 is the real code; if it is, the test data is
	// degenerate — treat as a hard failure rather than a false pass.
	realHash := otpHashFor(t, env.sqlDB, email)
	mac := hmac.New(sha256.New, []byte(otpTestSecret()))
	mac.Write([]byte("000001"))
	if hex.EncodeToString(mac.Sum(nil)) == realHash {
		t.Skip("degenerate test data: 000001 happened to be the real code")
	}
	if status != http.StatusBadRequest {
		t.Fatalf("verify with wrong code status=%d, want 400", status)
	}
	var attempts int
	if err := env.sqlDB.QueryRow(`SELECT otp_attempts FROM pending_registrations WHERE normalized_email = ?`, email).Scan(&attempts); err != nil {
		t.Fatalf("read otp_attempts: %v", err)
	}
	if attempts != 1 {
		t.Fatalf("otp_attempts=%d, want 1", attempts)
	}
	assertSignupCounts(t, env.sqlDB, email, "example.com", 0, 0, 0)
}

func TestSignupOTP_MaxAttemptsBlocksFurtherVerification(t *testing.T) {
	env := newSignupTxEnvSQLiteWithSecret(t, otpTestSecret())
	email := "otp-maxattempts@example.com"
	otpDoJSON(t, env, "POST", "/api/v1/auth/signup/start", map[string]string{
		"email": email, "password": "StrongPass123", "name": "X",
	})
	realHash := otpHashFor(t, env.sqlDB, email)
	realCode := bruteForceOTP(t, otpTestSecret(), realHash)

	// Drive otp_attempts to the max directly (rather than issuing 5 wrong
	// HTTP verify calls) so this test exercises the SignupVerify handler's
	// own max-attempts business rule in isolation from the unrelated
	// per-account credential-throttle middleware (authThrottle), which has
	// its own, separately-tested 5-per-15-minutes budget on this same
	// endpoint and would otherwise return 429 before the handler's logic
	// ever runs.
	if _, err := env.sqlDB.Exec(`UPDATE pending_registrations SET otp_attempts = 5 WHERE normalized_email = ?`, email); err != nil {
		t.Fatalf("force otp_attempts: %v", err)
	}

	status, _ := otpDoJSON(t, env, "POST", "/api/v1/auth/signup/verify", map[string]string{"email": email, "code": realCode})
	if status != http.StatusBadRequest {
		t.Fatalf("verify after max attempts status=%d, want 400", status)
	}
	assertSignupCounts(t, env.sqlDB, email, "example.com", 0, 0, 0)
}

func TestSignupOTP_ExpiredCodeRejected(t *testing.T) {
	env := newSignupTxEnvSQLiteWithSecret(t, otpTestSecret())
	email := "otp-expired@example.com"
	otpDoJSON(t, env, "POST", "/api/v1/auth/signup/start", map[string]string{
		"email": email, "password": "StrongPass123", "name": "X",
	})
	realHash := otpHashFor(t, env.sqlDB, email)
	realCode := bruteForceOTP(t, otpTestSecret(), realHash)

	// Force expiry.
	if _, err := env.sqlDB.Exec(`UPDATE pending_registrations SET otp_expires_at = ? WHERE normalized_email = ?`, time.Now().UTC().Add(-time.Minute), email); err != nil {
		t.Fatalf("force expiry: %v", err)
	}

	status, _ := otpDoJSON(t, env, "POST", "/api/v1/auth/signup/verify", map[string]string{"email": email, "code": realCode})
	if status != http.StatusBadRequest {
		t.Fatalf("verify with expired code status=%d, want 400", status)
	}
	assertSignupCounts(t, env.sqlDB, email, "example.com", 0, 0, 0)
}

func TestSignupOTP_ResendCooldownEnforced(t *testing.T) {
	env := newSignupTxEnvSQLiteWithSecret(t, otpTestSecret())
	email := "otp-cooldown@example.com"
	otpDoJSON(t, env, "POST", "/api/v1/auth/signup/start", map[string]string{
		"email": email, "password": "StrongPass123", "name": "X",
	})
	status, _ := otpDoJSON(t, env, "POST", "/api/v1/auth/signup/resend", map[string]string{"email": email})
	if status != http.StatusTooManyRequests {
		t.Fatalf("immediate resend status=%d, want 429", status)
	}
}

func TestSignupOTP_ResendInvalidatesOldCode(t *testing.T) {
	env := newSignupTxEnvSQLiteWithSecret(t, otpTestSecret())
	email := "otp-resend-invalidate@example.com"
	otpDoJSON(t, env, "POST", "/api/v1/auth/signup/start", map[string]string{
		"email": email, "password": "StrongPass123", "name": "X",
	})
	oldHash := otpHashFor(t, env.sqlDB, email)
	oldCode := bruteForceOTP(t, otpTestSecret(), oldHash)

	// Bypass the cooldown directly in the DB (simulating time passing)
	// rather than sleeping in the test.
	if _, err := env.sqlDB.Exec(`UPDATE pending_registrations SET otp_created_at = ? WHERE normalized_email = ?`, time.Now().UTC().Add(-2*time.Minute), email); err != nil {
		t.Fatalf("backdate otp_created_at: %v", err)
	}
	status, _ := otpDoJSON(t, env, "POST", "/api/v1/auth/signup/resend", map[string]string{"email": email})
	if status != http.StatusOK {
		t.Fatalf("resend after cooldown status=%d", status)
	}
	newHash := otpHashFor(t, env.sqlDB, email)
	if newHash == oldHash {
		t.Fatalf("resend did not rotate the OTP hash")
	}

	status, _ = otpDoJSON(t, env, "POST", "/api/v1/auth/signup/verify", map[string]string{"email": email, "code": oldCode})
	if status != http.StatusBadRequest {
		t.Fatalf("old code still accepted after resend, status=%d, want 400", status)
	}
}

func TestSignupOTP_SingleUse_ConcurrentVerifyIdempotent(t *testing.T) {
	env := newSignupTxEnvSQLiteWithSecret(t, otpTestSecret())
	email := "otp-concurrent@example.com"
	otpDoJSON(t, env, "POST", "/api/v1/auth/signup/start", map[string]string{
		"email": email, "password": "StrongPass123", "name": "X",
	})
	hash := otpHashFor(t, env.sqlDB, email)
	code := bruteForceOTP(t, otpTestSecret(), hash)

	var wg sync.WaitGroup
	statuses := make([]int, 5)
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			s, _ := otpDoJSON(t, env, "POST", "/api/v1/auth/signup/verify", map[string]string{"email": email, "code": code})
			statuses[idx] = s
		}(i)
	}
	wg.Wait()

	successCount := 0
	for _, s := range statuses {
		if s == http.StatusCreated {
			successCount++
		}
	}
	if successCount != 1 {
		t.Fatalf("concurrent verify with same code succeeded %d times, want exactly 1; statuses=%v", successCount, statuses)
	}
	assertSignupCounts(t, env.sqlDB, email, "example.com", 1, 1, 1)
}

func TestSignupOTP_DuplicateActiveUserRejectedGenerically(t *testing.T) {
	env := newSignupTxEnvSQLiteWithSecret(t, otpTestSecret())
	assertSignupCompletesAndCommitsAtomically(t, env) // creates signup-ok@example.com via immediate path

	status, out := otpDoJSON(t, env, "POST", "/api/v1/auth/signup/start", map[string]string{
		"email": "signup-ok@example.com", "password": "AnotherPass123", "name": "Dup",
	})
	if status != http.StatusConflict {
		t.Fatalf("signup/start for existing active user status=%d, want 409", status)
	}
	if out["error"] != "unable to complete signup" {
		t.Fatalf("expected generic error, got %v", out["error"])
	}
}

func TestSignupOTP_ExistingPlatformIdentityRejectedGenerically(t *testing.T) {
	env := newSignupTxEnvSQLiteWithSecret(t, otpTestSecret())
	now := time.Now().UTC()
	if _, err := env.sqlDB.Exec(
		`INSERT INTO users (created_at, updated_at, email, password_hash, role, tenant_id, active, email_verified) VALUES (?, ?, 'platform2@example.com', 'x', 'platform_super_admin', NULL, 1, 1)`,
		now, now,
	); err != nil {
		t.Fatalf("seed platform identity: %v", err)
	}

	status, out := otpDoJSON(t, env, "POST", "/api/v1/auth/signup/start", map[string]string{
		"email": "platform2@example.com", "password": "AttackerPass123", "name": "Attacker",
	})
	if status != http.StatusConflict {
		t.Fatalf("signup/start for platform identity email status=%d, want 409", status)
	}
	if out["error"] != "unable to complete signup" {
		t.Fatalf("expected generic error, got %v", out["error"])
	}
	var pendingCount int64
	env.sqlDB.QueryRow(`SELECT COUNT(*) FROM pending_registrations WHERE normalized_email = 'platform2@example.com'`).Scan(&pendingCount)
	if pendingCount != 0 {
		t.Fatalf("pending_registration created for a platform identity email")
	}
}

func TestSignupOTP_VerifiedCustomerGetsOrganizationPortal(t *testing.T) {
	env := newSignupTxEnvSQLiteWithSecret(t, otpTestSecret())
	email := "otp-portal@example.com"
	otpDoJSON(t, env, "POST", "/api/v1/auth/signup/start", map[string]string{
		"email": email, "password": "StrongPass123", "name": "Portal Co",
	})
	hash := otpHashFor(t, env.sqlDB, email)
	code := bruteForceOTP(t, otpTestSecret(), hash)
	status, out := otpDoJSON(t, env, "POST", "/api/v1/auth/signup/verify", map[string]string{"email": email, "code": code})
	if status != http.StatusCreated {
		t.Fatalf("verify status=%d body=%v", status, out)
	}
	token, _ := out["access_token"].(string)
	if token == "" {
		t.Fatalf("no access token returned")
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := env.router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("me: %v", err)
	}
	defer resp.Body.Close()
	var meOut map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&meOut)
	if meOut["portal"] != "organization" {
		t.Fatalf("verified customer portal=%v, want organization; body=%v", meOut["portal"], meOut)
	}
}

// TestSignupOTP_NotPersistedPlaintext proves the pending_registrations row
// never contains the plaintext code: the stored otp_hash column must not
// equal the code itself, must not literally contain the code as a
// substring, and must be the expected HMAC digest length in hex (32 bytes
// -> 64 hex chars) rather than a 6-digit string.
func TestSignupOTP_NotPersistedPlaintext(t *testing.T) {
	env := newSignupTxEnvSQLiteWithSecret(t, otpTestSecret())
	email := "otp-noplaintext@example.com"
	otpDoJSON(t, env, "POST", "/api/v1/auth/signup/start", map[string]string{
		"email": email, "password": "StrongPass123", "name": "X",
	})
	hash := otpHashFor(t, env.sqlDB, email)
	code := bruteForceOTP(t, otpTestSecret(), hash)

	if hash == code {
		t.Fatalf("otp_hash equals plaintext code")
	}
	if len(hash) != 64 {
		t.Fatalf("otp_hash length=%d, want 64 (hex-encoded SHA-256 HMAC digest) — looks like plaintext or a different encoding", len(hash))
	}
	if bytesContains(hash, code) {
		t.Fatalf("otp_hash contains the plaintext code as a substring: %s", hash)
	}
}

func bytesContains(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// TestSignupOTP_NeverLogsPlaintextCode drives signup/start and
// signup/verify (success and failure paths) through a router built with an
// observed zap core, then asserts the real plaintext OTP never appears in
// any captured log message or field across the whole flow.
func TestSignupOTP_NeverLogsPlaintextCode(t *testing.T) {
	core, observed := observer.New(zapcore.InfoLevel)
	logger := zap.New(core)

	cfg := config.Defaults()
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = t.TempDir() + "/signup-otp-logs.db?_loc=auto&_busy_timeout=5000&_txlock=immediate"
	cfg.Auth.JWTSecret = otpTestSecret()

	gdb, err := config.NewDatabase(&cfg.Database, logger)
	if err != nil {
		t.Fatalf("database: %v", err)
	}
	sqlDB, err := gdb.DB()
	if err != nil {
		t.Fatalf("sql db: %v", err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	if err := models.MigrateAllRaw(gdb); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	authenticator, err := auth.NewAuthenticator(&cfg.Auth, gdb, logger)
	if err != nil {
		t.Fatalf("authenticator: %v", err)
	}
	router := api.NewRouter(cfg, authenticator, logger, gdb, modules.NewRegistry(logger), license.NewFeatureFlags(logger), nil)
	t.Cleanup(func() { _ = router.App().Shutdown() })
	router.SetMailSender(newFakeMailSender(false))

	env := &signupTxEnv{router: router, sqlDB: sqlDB}
	email := "otp-nolog@example.com"
	otpDoJSON(t, env, "POST", "/api/v1/auth/signup/start", map[string]string{
		"email": email, "password": "StrongPass123", "name": "X",
	})
	hash := otpHashFor(t, env.sqlDB, email)
	code := bruteForceOTP(t, otpTestSecret(), hash)

	// One wrong attempt, then the real one, then resend + verify again.
	otpDoJSON(t, env, "POST", "/api/v1/auth/signup/verify", map[string]string{"email": email, "code": "111111"})
	otpDoJSON(t, env, "POST", "/api/v1/auth/signup/verify", map[string]string{"email": email, "code": code})

	for _, entry := range observed.All() {
		if strings.Contains(entry.Message, code) {
			t.Fatalf("OTP plaintext %q found in log message: %q", code, entry.Message)
		}
		for _, f := range entry.Context {
			if strings.Contains(fmt.Sprintf("%v", f.Interface), code) || strings.Contains(f.String, code) {
				t.Fatalf("OTP plaintext %q found in log field %q of message %q", code, f.Key, entry.Message)
			}
		}
	}
}

// TestSignupOTP_ActivationFailureRollsBackNoOrphan mirrors
// assertSignupSubscriptionFailureRollsBack for the immediate-signup path:
// if activation's subscription step fails, the whole transaction (tenant +
// user + subscription) must roll back, leaving no orphan tenant or user
// row, and the pending_registration must not be left consumed with no
// corresponding account.
func TestSignupOTP_ActivationFailureRollsBackNoOrphan(t *testing.T) {
	env := newSignupTxEnvSQLiteWithSecret(t, otpTestSecret())
	email := "otp-rollback@example.com"
	otpDoJSON(t, env, "POST", "/api/v1/auth/signup/start", map[string]string{
		"email": email, "password": "StrongPass123", "name": "X",
	})
	hash := otpHashFor(t, env.sqlDB, email)
	code := bruteForceOTP(t, otpTestSecret(), hash)

	if _, err := env.sqlDB.Exec(`DELETE FROM plans WHERE id = 'free'`); err != nil {
		t.Fatalf("delete free plan: %v", err)
	}

	status, _ := otpDoJSON(t, env, "POST", "/api/v1/auth/signup/verify", map[string]string{"email": email, "code": code})
	if status != http.StatusInternalServerError {
		t.Fatalf("verify with missing plan status=%d, want 500", status)
	}
	assertSignupCounts(t, env.sqlDB, email, "example.com", 0, 0, 0)
}

// --- Part 1 false-success-bug regression tests ---------------------------

// TestSignupOTP_NilMailSenderDoesNotClaimSuccess covers case (1) from the
// hotfix mission: mailSender == nil must not produce a "verification code
// sent" response.
func TestSignupOTP_NilMailSenderDoesNotClaimSuccess(t *testing.T) {
	env := newSignupTxEnvSQLiteWithSecret(t, otpTestSecret())
	env.router.SetMailSender(nil)
	email := "otp-nilsender@example.com"

	status, out := otpDoJSON(t, env, "POST", "/api/v1/auth/signup/start", map[string]string{
		"email": email, "password": "StrongPass123", "name": "X",
	})
	if status != http.StatusServiceUnavailable {
		t.Fatalf("signup/start with nil mail sender status=%d body=%v, want 503", status, out)
	}
	if msg, _ := out["error"].(string); strings.Contains(strings.ToLower(msg), "smtp") {
		t.Fatalf("raw SMTP/internal error leaked to HTTP response: %v", out)
	}
	var count int64
	env.sqlDB.QueryRow(`SELECT COUNT(*) FROM pending_registrations WHERE normalized_email = ?`, email).Scan(&count)
	if count != 0 {
		t.Fatalf("pending_registration persisted despite nil mail sender (would trap the customer): count=%d", count)
	}
}

// TestSignupOTP_SendFailureDoesNotClaimSuccess covers case (2): Send()
// returning an error must not produce a "sent" response, and must not leak
// the raw SMTP error text into the HTTP body.
func TestSignupOTP_SendFailureDoesNotClaimSuccess(t *testing.T) {
	env := newSignupTxEnvSQLiteWithSecret(t, otpTestSecret())
	env.router.SetMailSender(newFakeMailSender(true))
	email := "otp-sendfail@example.com"

	status, out := otpDoJSON(t, env, "POST", "/api/v1/auth/signup/start", map[string]string{
		"email": email, "password": "StrongPass123", "name": "X",
	})
	if status != http.StatusServiceUnavailable {
		t.Fatalf("signup/start with failing send status=%d body=%v, want 503", status, out)
	}
	if msg, _ := out["error"].(string); strings.Contains(msg, "fake smtp") || strings.Contains(strings.ToLower(msg), "simulated") {
		t.Fatalf("raw send-failure error text leaked to HTTP response: %v", out)
	}
}

// TestSignupOTP_SuccessfulSendStillReturnsSentResponse covers case (3): the
// ordinary success path is unchanged by the restructuring.
func TestSignupOTP_SuccessfulSendStillReturnsSentResponse(t *testing.T) {
	env := newSignupTxEnvSQLiteWithSecret(t, otpTestSecret())
	email := "otp-sendok@example.com"

	status, out := otpDoJSON(t, env, "POST", "/api/v1/auth/signup/start", map[string]string{
		"email": email, "password": "StrongPass123", "name": "X",
	})
	if status != http.StatusOK {
		t.Fatalf("signup/start status=%d body=%v", status, out)
	}
	if out["message"] != "verification code sent" {
		t.Fatalf("unexpected success body: %v", out)
	}
}

// TestSignupOTP_ResendSendFailureDoesNotClaimSuccess covers case (4): a
// synchronous resend failure must not falsely claim delivery.
func TestSignupOTP_ResendSendFailureDoesNotClaimSuccess(t *testing.T) {
	env := newSignupTxEnvSQLiteWithSecret(t, otpTestSecret())
	email := "otp-resend-sendfail@example.com"
	otpDoJSON(t, env, "POST", "/api/v1/auth/signup/start", map[string]string{
		"email": email, "password": "StrongPass123", "name": "X",
	})
	// Bypass the cooldown so the resend attempt actually reaches the send step.
	if _, err := env.sqlDB.Exec(`UPDATE pending_registrations SET otp_created_at = ? WHERE normalized_email = ?`, time.Now().UTC().Add(-2*time.Minute), email); err != nil {
		t.Fatalf("backdate otp_created_at: %v", err)
	}

	env.router.SetMailSender(newFakeMailSender(true))
	status, out := otpDoJSON(t, env, "POST", "/api/v1/auth/signup/resend", map[string]string{"email": email})
	if status != http.StatusServiceUnavailable {
		t.Fatalf("signup/resend with failing send status=%d body=%v, want 503", status, out)
	}
}

// TestSignupOTP_FailedSendDoesNotStartCooldown_CanRetryPromptly covers case
// (5): a failed first send must not trap the customer in the normal
// successful-send resend cooldown — since Part 1 discards the DB write
// entirely on send failure, an immediate retry after a failure must be
// allowed to proceed (and succeed) rather than being 429'd.
func TestSignupOTP_FailedSendDoesNotStartCooldown_CanRetryPromptly(t *testing.T) {
	env := newSignupTxEnvSQLiteWithSecret(t, otpTestSecret())
	fake := newFakeMailSender(true)
	env.router.SetMailSender(fake)
	email := "otp-retryprompt@example.com"

	status, out := otpDoJSON(t, env, "POST", "/api/v1/auth/signup/start", map[string]string{
		"email": email, "password": "StrongPass123", "name": "X",
	})
	if status != http.StatusServiceUnavailable {
		t.Fatalf("first signup/start (failing send) status=%d body=%v, want 503", status, out)
	}
	var count int64
	env.sqlDB.QueryRow(`SELECT COUNT(*) FROM pending_registrations WHERE normalized_email = ?`, email).Scan(&count)
	if count != 0 {
		t.Fatalf("pending_registration persisted despite failed send: count=%d", count)
	}

	// Immediately retry with a working sender — must succeed with NO
	// cooldown wait, since the failed attempt above never started one.
	fake.setFail(false)
	status, out = otpDoJSON(t, env, "POST", "/api/v1/auth/signup/start", map[string]string{
		"email": email, "password": "StrongPass123", "name": "X",
	})
	if status != http.StatusOK {
		t.Fatalf("prompt retry after failed send status=%d body=%v, want 200 (no cooldown should have started)", status, out)
	}
}

// TestSignupOTP_FailedSendDoesNotCreateAccount covers case (10) explicitly
// for the mail-delivery-failure path: no organization/user/subscription is
// created merely because mail delivery failed.
func TestSignupOTP_FailedSendDoesNotCreateAccount(t *testing.T) {
	env := newSignupTxEnvSQLiteWithSecret(t, otpTestSecret())
	env.router.SetMailSender(newFakeMailSender(true))
	email := "otp-nofalseaccount@example.com"

	otpDoJSON(t, env, "POST", "/api/v1/auth/signup/start", map[string]string{
		"email": email, "password": "StrongPass123", "name": "X",
	})
	assertSignupCounts(t, env.sqlDB, email, "example.com", 0, 0, 0)
}
