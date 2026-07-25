package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/binary"
	"fmt"
	"hash"
	"math"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/config"
	"github.com/orvix/orvix/internal/models"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// ── harness ─────────────────────────────────────────────────────

type reauthHarness struct {
	db     *gorm.DB
	auth   *Authenticator
	reauth *ReauthManager
}

func newReauthHarness(t *testing.T, mfaSecret string) *reauthHarness {
	t.Helper()
	logger := zap.NewNop()
	cfg := config.DatabaseConfig{
		Driver: "sqlite",
		DSN:    t.TempDir() + "/orvix_reauth.db?_loc=auto&_busy_timeout=5000&_txlock=immediate",
	}
	db, err := config.NewDatabase(&cfg, logger)
	if err != nil {
		t.Fatalf("sqlite db: %v", err)
	}
	if err := models.MigrateAllRaw(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	authCfg := &config.AuthConfig{
		JWTKeyPath:   t.TempDir() + "/jwt_key.pem",
		JWTAccessTTL: 15 * time.Minute,
	}
	authInst, err := NewAuthenticator(authCfg, db, logger)
	if err != nil {
		t.Fatalf("new auth: %v", err)
	}

	rm := NewReauthManager(db, logger, authInst)

	hash, err := HashPassword("correct-password")
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	mfaEnabled := mfaSecret != ""
	err = db.Exec(
		"INSERT INTO users (created_at, updated_at, email, password_hash, role, mfa_secret, mfa_enabled) VALUES (?, ?, ?, ?, ?, ?, ?)",
		time.Now(), time.Now(), "admin@test.com", hash, "platform_super_admin", mfaSecret, mfaEnabled,
	).Error
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			sqlDB.Close()
		}
	})

	return &reauthHarness{db: db, auth: authInst, reauth: rm}
}

func (h *reauthHarness) createGrant(userID uint, sessionID string, tenantID uint, scope ReauthScope) (*RecentAuthGrant, error) {
	return h.reauth.CreateGrant(userID, sessionID, tenantID, scope)
}

func (h *reauthHarness) validateGrant(userID uint, sessionID string, tenantID uint, scope ReauthScope) error {
	return h.reauth.ValidateGrant(userID, sessionID, tenantID, scope)
}

// ── TOTP helpers ──────────────────────────────────────────────────

func generateTestSecret() string {
	b := make([]byte, 20)
	rand.Read(b)
	// Standard base32 (RFC 4648) without padding, matching base32Decode in reauth.go.
	const table = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567"
	var dst []byte
	buf := uint64(0)
	bits := 0
	for _, v := range b {
		buf = (buf << 8) | uint64(v)
		bits += 8
		for bits >= 5 {
			bits -= 5
			dst = append(dst, table[(buf>>bits)&31])
		}
	}
	if bits > 0 {
		dst = append(dst, table[(buf<<(5-bits))&31])
	}
	return string(dst)
}

func generateTOTPCode(secret string, t time.Time) string {
	secretBytes, _ := base32Decode(secret)
	period := uint64(30)
	counter := uint64(t.Unix()) / period
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, counter)
	macFn := func() hash.Hash { return sha1.New() }
	mac := hmac.New(macFn, secretBytes)
	mac.Write(buf)
	hs := mac.Sum(nil)
	offset := hs[len(hs)-1] & 0xf
	binCode := (int32(hs[offset]&0x7f) << 24) |
		(int32(hs[offset+1]) << 16) |
		(int32(hs[offset+2]) << 8) |
		int32(hs[offset+3])
	otp := binCode % int32(math.Pow10(6))
	return fmt.Sprintf("%06d", int(otp))
}

// ── Middleware test helper ────────────────────────────────────────

func testReauthMiddleware(rm *ReauthManager, scope ReauthScope, userID uint, sessionID string, tenantID uint) int {
	app := fiber.New()
	app.Use(func(c fiber.Ctx) error {
		c.Locals("user_id", userID)
		c.Locals("session_id", sessionID)
		c.Locals("tenant_id", tenantID)
		return c.Next()
	})
	app.Post("/protected", rm.RequireReauth(scope), func(c fiber.Ctx) error {
		return c.SendStatus(200)
	})
	req := httptest.NewRequest("POST", "/protected", nil)
	resp, err := app.Test(req, fiber.TestConfig{Timeout: time.Second})
	if err != nil {
		return -1
	}
	return resp.StatusCode
}

// ── Tests ──────────────────────────────────────────────────────────

func TestReauth_MissingGrant_Rejected(t *testing.T) {
	h := newReauthHarness(t, "")
	err := h.validateGrant(1, "test-session-1", 1, ScopeTenantManagement)
	if err != ErrReauthGrantMissing {
		t.Fatalf("expected ErrReauthGrantMissing, got: %v", err)
	}
}

func TestReauth_ExpiredGrant_Rejected(t *testing.T) {
	h := newReauthHarness(t, "")
	now := time.Now()
	h.db.Exec(
		"INSERT INTO recent_auth_grants (created_at, user_id, session_id, tenant_id, scope, expires_at) VALUES (?, ?, ?, ?, ?, ?)",
		now.Add(-time.Hour), uint(1), "test-session-1", uint(1), "tenant_management", now.Add(-time.Minute),
	)
	err := h.validateGrant(1, "test-session-1", 1, ScopeTenantManagement)
	if err != ErrReauthGrantMissing {
		t.Fatalf("expected ErrReauthGrantMissing (expired), got: %v", err)
	}
}

func TestReauth_WrongUser_Rejected(t *testing.T) {
	h := newReauthHarness(t, "")
	now := time.Now()
	h.db.Exec(
		"INSERT INTO recent_auth_grants (created_at, user_id, session_id, tenant_id, scope, expires_at) VALUES (?, ?, ?, ?, ?, ?)",
		now, uint(99), "test-session-1", uint(1), "tenant_management", now.Add(time.Hour),
	)
	err := h.validateGrant(1, "test-session-1", 1, ScopeTenantManagement)
	if err != ErrReauthGrantMissing {
		t.Fatalf("expected ErrReauthGrantMissing for wrong user, got: %v", err)
	}
}

func TestReauth_WrongSession_Rejected(t *testing.T) {
	h := newReauthHarness(t, "")
	now := time.Now()
	h.db.Exec(
		"INSERT INTO recent_auth_grants (created_at, user_id, session_id, tenant_id, scope, expires_at) VALUES (?, ?, ?, ?, ?, ?)",
		now, uint(1), "other-session", uint(1), "tenant_management", now.Add(time.Hour),
	)
	err := h.validateGrant(1, "session-1", 1, ScopeTenantManagement)
	if err != ErrReauthGrantMissing {
		t.Fatalf("expected ErrReauthGrantMissing for wrong session, got: %v", err)
	}
}

func TestReauth_WrongScope_Rejected(t *testing.T) {
	h := newReauthHarness(t, "")
	now := time.Now()
	h.db.Exec(
		"INSERT INTO recent_auth_grants (created_at, user_id, session_id, tenant_id, scope, expires_at) VALUES (?, ?, ?, ?, ?, ?)",
		now, uint(1), "test-session-1", uint(1), "domain_management", now.Add(time.Hour),
	)
	err := h.validateGrant(1, "test-session-1", 1, ScopeTenantManagement)
	if err != ErrReauthGrantWrongScope {
		t.Fatalf("expected ErrReauthGrantWrongScope, got: %v", err)
	}
}

func TestReauth_PasswordSuccess(t *testing.T) {
	h := newReauthHarness(t, "")
	err := h.reauth.VerifyPassword(1, "correct-password")
	if err != nil {
		t.Fatalf("expected password verification to succeed, got: %v", err)
	}
}

func TestReauth_WrongPassword(t *testing.T) {
	h := newReauthHarness(t, "")
	err := h.reauth.VerifyPassword(1, "wrong-password")
	if err != ErrReauthPasswordWrong {
		t.Fatalf("expected ErrReauthPasswordWrong, got: %v", err)
	}
}

func TestReauth_PasswordSuccess_CreatesGrant(t *testing.T) {
	h := newReauthHarness(t, "")

	err := h.reauth.VerifyPassword(1, "correct-password")
	if err != nil {
		t.Fatalf("password verification: %v", err)
	}

	grant, err := h.createGrant(1, "session-1", 1, ScopeTenantManagement)
	if err != nil {
		t.Fatalf("create grant: %v", err)
	}
	if grant == nil || grant.ID == 0 {
		t.Fatalf("expected non-zero grant ID")
	}
	if grant.Scope != string(ScopeTenantManagement) {
		t.Fatalf("expected scope tenant_management, got %s", grant.Scope)
	}

	err = h.validateGrant(1, "session-1", 1, ScopeTenantManagement)
	if err != nil {
		t.Fatalf("expected valid grant after creation, got: %v", err)
	}
}

func TestReauth_MFARequired_WhenMFASecretExists(t *testing.T) {
	secret := generateTestSecret()
	h := newReauthHarness(t, secret)

	err := h.reauth.VerifyTOTP(1, "000000")
	if err == nil {
		t.Fatalf("expected TOTP to fail for invalid code")
	}
}

func TestReauth_ValidTOTP(t *testing.T) {
	secret := generateTestSecret()
	h := newReauthHarness(t, secret)

	code := generateTOTPCode(secret, time.Now())
	err := h.reauth.VerifyTOTP(1, code)
	if err != nil {
		t.Fatalf("expected valid TOTP to succeed, got: %v", err)
	}
}

func TestReauth_InvalidTOTP_Rejected(t *testing.T) {
	secret := generateTestSecret()
	h := newReauthHarness(t, secret)

	err := h.reauth.VerifyTOTP(1, "123456")
	if err == nil {
		t.Fatalf("expected invalid TOTP to fail")
	}
}

func TestReauth_Logout_InvalidatesGrants(t *testing.T) {
	h := newReauthHarness(t, "")
	now := time.Now()
	h.db.Exec(
		"INSERT INTO recent_auth_grants (created_at, user_id, session_id, tenant_id, scope, expires_at) VALUES (?, ?, ?, ?, ?, ?)",
		now, uint(1), "session-1", uint(1), "tenant_management", now.Add(time.Hour),
	)

	err := h.validateGrant(1, "session-1", 1, ScopeTenantManagement)
	if err != nil {
		t.Fatalf("expected valid grant before logout, got: %v", err)
	}

	if err := h.reauth.RevokeUserGrants(1); err != nil {
		t.Fatalf("revoke grants: %v", err)
	}

	err = h.validateGrant(1, "session-1", 1, ScopeTenantManagement)
	if err != ErrReauthGrantMissing {
		t.Fatalf("expected ErrReauthGrantMissing after logout, got: %v", err)
	}
}

func TestReauth_PasswordChange_InvalidatesGrants(t *testing.T) {
	h := newReauthHarness(t, "")
	now := time.Now()
	h.db.Exec(
		"INSERT INTO recent_auth_grants (created_at, user_id, session_id, tenant_id, scope, expires_at) VALUES (?, ?, ?, ?, ?, ?)",
		now, uint(1), "session-1", uint(1), "tenant_management", now.Add(time.Hour),
	)

	if err := h.reauth.RevokeUserGrants(1); err != nil {
		t.Fatalf("revoke grants: %v", err)
	}

	err := h.validateGrant(1, "session-1", 1, ScopeTenantManagement)
	if err != ErrReauthGrantMissing {
		t.Fatalf("expected ErrReauthGrantMissing after password change, got: %v", err)
	}
}

func TestReauth_SessionRevocation_InvalidatesGrants(t *testing.T) {
	h := newReauthHarness(t, "")
	now := time.Now()
	h.db.Exec(
		"INSERT INTO recent_auth_grants (created_at, user_id, session_id, tenant_id, scope, expires_at) VALUES (?, ?, ?, ?, ?, ?)",
		now, uint(1), "session-1", uint(1), "tenant_management", now.Add(time.Hour),
	)

	if err := h.reauth.RevokeSessionGrants(1, "session-1"); err != nil {
		t.Fatalf("revoke session grants: %v", err)
	}

	err := h.validateGrant(1, "session-1", 1, ScopeTenantManagement)
	if err != ErrReauthGrantMissing {
		t.Fatalf("expected ErrReauthGrantMissing after session revocation, got: %v", err)
	}
}

func TestReauth_RateLimiting_BlocksAfterFailures(t *testing.T) {
	h := newReauthHarness(t, "")

	for i := 0; i < reauthMaxFailedAttempts; i++ {
		err := h.reauth.VerifyPassword(1, "wrong-password")
		if err != ErrReauthPasswordWrong {
			t.Fatalf("attempt %d: expected wrong password, got: %v", i, err)
		}
	}

	err := h.reauth.VerifyPassword(1, "correct-password")
	if err != ErrReauthRateLimited {
		t.Fatalf("expected rate limited after %d failures, got: %v", reauthMaxFailedAttempts, err)
	}
}

func TestReauth_AuditEvent_IsPersisted(t *testing.T) {
	h := newReauthHarness(t, "")

	err := h.reauth.RecordAuditEvent(ReauthAuditEvent{
		UserID:    1,
		Action:    "password_verify",
		Scope:     "tenant_management",
		SessionID: "test-session-1",
		Success:   true,
		Timestamp: time.Now(),
	})
	if err != nil {
		t.Fatalf("record audit event: %v", err)
	}

	var count int
	h.db.Raw("SELECT COUNT(*) FROM coremail_audit WHERE action = ?", "reauth.password_verify").Scan(&count)
	if count != 1 {
		t.Fatalf("expected 1 audit entry, got %d", count)
	}
}

func TestReauth_ConcurrentGrantCreation(t *testing.T) {
	h := newReauthHarness(t, "")

	var wg sync.WaitGroup
	errCh := make(chan error, 20)

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := h.reauth.CreateGrant(1, fmt.Sprintf("session-%d", i), 1, ScopeTenantManagement)
			if err != nil {
				errCh <- err
			}
		}(i)
	}
	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Errorf("concurrent grant creation error: %v", err)
	}

	var count int
	h.db.Raw("SELECT COUNT(*) FROM recent_auth_grants WHERE user_id = ?", uint(1)).Scan(&count)
	if count != 10 {
		t.Fatalf("expected 10 grants, got %d", count)
	}
}

func TestReauth_AllScopes_AreDefined(t *testing.T) {
	expected := []ReauthScope{
		ScopeTenantManagement, ScopeIdentityManagement, ScopeDomainManagement,
		ScopeMailboxManagement, ScopeBillingManagement, ScopeFirewallManagement,
		ScopeAPIKeyManagement, ScopeQueueDestructive, ScopeBackupRestore,
		ScopeSecuritySettings, ScopeSystemSettings, ScopeSystemUpdate,
	}
	if len(AllReauthScopes) != len(expected) {
		t.Fatalf("expected %d scopes, got %d", len(expected), len(AllReauthScopes))
	}
	for _, s := range expected {
		found := false
		for _, a := range AllReauthScopes {
			if a == s {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("scope %s not in AllReauthScopes", s)
		}
	}
}

func TestReauth_PurgeExpiredGrants(t *testing.T) {
	h := newReauthHarness(t, "")
	now := time.Now()

	h.db.Exec(
		"INSERT INTO recent_auth_grants (created_at, user_id, session_id, tenant_id, scope, expires_at) VALUES (?, ?, ?, ?, ?, ?)",
		now.Add(-time.Hour), uint(1), "test-session-1", uint(1), "tenant_management", now.Add(-time.Minute),
	)
	h.db.Exec(
		"INSERT INTO recent_auth_grants (created_at, user_id, session_id, tenant_id, scope, expires_at) VALUES (?, ?, ?, ?, ?, ?)",
		now, uint(1), "test-session-1", uint(1), "tenant_management", now.Add(time.Hour),
	)

	err := h.reauth.ValidateGrant(1, "test-session-1", 1, ScopeTenantManagement)
	if err != nil {
		t.Fatalf("expected valid grant to be found, got: %v", err)
	}

	var count int
	h.db.Raw("SELECT COUNT(*) FROM recent_auth_grants WHERE user_id = ?", uint(1)).Scan(&count)
	if count != 1 {
		t.Fatalf("expected 1 remaining grant after purge, got %d", count)
	}
}

func TestReauth_RevokeAllGrants(t *testing.T) {
	h := newReauthHarness(t, "")
	now := time.Now()

	for i := 0; i < 3; i++ {
		h.db.Exec(
			"INSERT INTO recent_auth_grants (created_at, user_id, session_id, tenant_id, scope, expires_at) VALUES (?, ?, ?, ?, ?, ?)",
			now, uint(1), fmt.Sprintf("session-%d", i), uint(1), "tenant_management", now.Add(time.Hour),
		)
	}

	if err := h.reauth.RevokeAllGrants(); err != nil {
		t.Fatalf("revoke all: %v", err)
	}

	var count int
	h.db.Raw("SELECT COUNT(*) FROM recent_auth_grants").Scan(&count)
	if count != 0 {
		t.Fatalf("expected 0 grants after RevokeAll, got %d", count)
	}
}

func TestReauth_SessionIDBound(t *testing.T) {
	h := newReauthHarness(t, "")
	now := time.Now()

	h.db.Exec(
		"INSERT INTO recent_auth_grants (created_at, user_id, session_id, tenant_id, scope, expires_at) VALUES (?, ?, ?, ?, ?, ?)",
		now, uint(1), "session-1", uint(1), "tenant_management", now.Add(time.Hour),
	)

	err := h.reauth.ValidateGrant(1, "session-1", 1, ScopeTenantManagement)
	if err != nil {
		t.Fatalf("expected grant for session-1 to be valid, got: %v", err)
	}

	err = h.reauth.ValidateGrant(1, "session-2", 1, ScopeTenantManagement)
	if err != ErrReauthGrantMissing {
		t.Fatalf("expected ErrReauthGrantMissing for different session, got: %v", err)
	}
}

func TestReauth_GrantHasExpiry(t *testing.T) {
	h := newReauthHarness(t, "")
	now := time.Now()

	grant, err := h.createGrant(1, "session-1", 1, ScopeTenantManagement)
	if err != nil {
		t.Fatalf("create grant: %v", err)
	}

	if grant.ExpiresAt.Before(now.Add(reauthGrantTTL - time.Minute)) {
		t.Fatalf("grant expiry %v is too soon (expected ~%v)", grant.ExpiresAt, reauthGrantTTL)
	}
	if grant.ExpiresAt.After(now.Add(reauthGrantTTL + time.Minute)) {
		t.Fatalf("grant expiry %v is too far (expected ~%v)", grant.ExpiresAt, reauthGrantTTL)
	}
}

func TestReauth_TOTPRequiredWhenMFAEnabled(t *testing.T) {
	secret := generateTestSecret()
	h := newReauthHarness(t, secret)

	err := h.reauth.VerifyTOTP(1, "123456")
	if err == nil {
		t.Fatalf("expected error for invalid TOTP")
	}
}

func TestReauth_NoSecretLeakage(t *testing.T) {
	h := newReauthHarness(t, "")

	err := h.reauth.VerifyPassword(1, "wrong-password")
	if err != ErrReauthPasswordWrong {
		t.Fatalf("expected ErrReauthPasswordWrong, got: %v", err)
	}
}

func TestReauth_Middleware_SessionRequired(t *testing.T) {
	h := newReauthHarness(t, "")
	code := testReauthMiddleware(h.reauth, ScopeTenantManagement, 1, "session-1", 1)
	if code != fiber.StatusForbidden {
		t.Fatalf("expected 403 for missing grant via middleware, got %d", code)
	}
}

func TestReauth_Middleware_WithValidGrant(t *testing.T) {
	h := newReauthHarness(t, "")
	now := time.Now()
	h.db.Exec(
		"INSERT INTO recent_auth_grants (created_at, user_id, session_id, tenant_id, scope, expires_at) VALUES (?, ?, ?, ?, ?, ?)",
		now, uint(1), "session-1", uint(1), "tenant_management", now.Add(time.Hour),
	)
	code := testReauthMiddleware(h.reauth, ScopeTenantManagement, 1, "session-1", 1)
	if code != fiber.StatusOK {
		t.Fatalf("expected 200 for valid grant via middleware, got %d", code)
	}
}

func TestReauth_CreateAndValidateTOTPGrant(t *testing.T) {
	secret := generateTestSecret()
	h := newReauthHarness(t, secret)

	code := generateTOTPCode(secret, time.Now())
	err := h.reauth.VerifyTOTP(1, code)
	if err != nil {
		t.Fatalf("TOTP verification failed: %v", err)
	}

	grant, err := h.reauth.CreateGrant(1, "session-1", 1, ScopeTenantManagement)
	if err != nil {
		t.Fatalf("create grant after TOTP: %v", err)
	}
	if grant.ID == 0 {
		t.Fatalf("expected non-zero grant ID")
	}

	err = h.reauth.ValidateGrant(1, "session-1", 1, ScopeTenantManagement)
	if err != nil {
		t.Fatalf("validate grant after TOTP creation: %v", err)
	}
}
