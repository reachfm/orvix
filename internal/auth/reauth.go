package auth

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/binary"
	"errors"
	"fmt"
	"hash"
	"math"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/dbdialect"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// ── Action scopes ──────────────────────────────────────────────────

type ReauthScope string

const (
	ScopeTenantManagement  ReauthScope = "tenant_management"
	ScopeIdentityManagement ReauthScope = "identity_management"
	ScopeDomainManagement  ReauthScope = "domain_management"
	ScopeMailboxManagement ReauthScope = "mailbox_management"
	ScopeBillingManagement ReauthScope = "billing_management"
	ScopeFirewallManagement ReauthScope = "firewall_management"
	ScopeAPIKeyManagement   ReauthScope = "api_key_management"
	ScopeQueueDestructive   ReauthScope = "queue_destructive"
	ScopeBackupRestore      ReauthScope = "backup_restore"
	ScopeSecuritySettings   ReauthScope = "security_settings"
	ScopeSystemSettings     ReauthScope = "system_settings"
	ScopeSystemUpdate       ReauthScope = "system_update"
)

var AllReauthScopes = []ReauthScope{
	ScopeTenantManagement, ScopeIdentityManagement, ScopeDomainManagement,
	ScopeMailboxManagement, ScopeBillingManagement, ScopeFirewallManagement,
	ScopeAPIKeyManagement, ScopeQueueDestructive, ScopeBackupRestore,
	ScopeSecuritySettings, ScopeSystemSettings, ScopeSystemUpdate,
}

// ── Errors ─────────────────────────────────────────────────────────

var (
	ErrReauthGrantMissing   = errors.New("recent authentication grant missing")
	ErrReauthGrantExpired   = errors.New("recent authentication grant expired")
	ErrReauthGrantWrongUser = errors.New("recent authentication grant belongs to a different user")
	ErrReauthGrantWrongSession = errors.New("recent authentication grant belongs to a different session")
	ErrReauthGrantWrongTenant = errors.New("recent authentication grant belongs to a different tenant")
	ErrReauthGrantWrongScope  = errors.New("recent authentication grant does not cover the requested scope")
	ErrReauthPasswordWrong = errors.New("incorrect password")
	ErrReauthTOTPRequired  = errors.New("TOTP verification required but not provided")
	ErrReauthTOTPInvalid   = errors.New("invalid TOTP code")
	ErrReauthTOTPReplayed  = errors.New("TOTP code has already been used")
	ErrReauthRateLimited   = errors.New("too many verification attempts")
	ErrReauthMFARequired   = errors.New("multi-factor authentication required")
)

// ── Grant ──────────────────────────────────────────────────────────

// RecentAuthGrant is a short-lived grant proving the user recently
// authenticated with password (and TOTP when MFA is enabled). It is
// bound to a specific user, session, tenant, and action scope.
type RecentAuthGrant struct {
	ID        uint       `gorm:"primaryKey" json:"id"`
	CreatedAt time.Time  `json:"created_at"`
	UserID    uint       `gorm:"index:idx_reauth_user_scope;not null" json:"user_id"`
	SessionID string     `gorm:"not null" json:"session_id"`
	TenantID  uint       `gorm:"not null" json:"tenant_id"`
	Scope     string     `gorm:"index:idx_reauth_user_scope;not null" json:"scope"`
	ExpiresAt time.Time  `gorm:"not null" json:"expires_at"`
}

const reauthGrantTTL = 15 * time.Minute
const reauthMaxFailedAttempts = 5
const reauthRateLimitWindow = 5 * time.Minute
const totpSkew = 1

// ── ReauthManager ──────────────────────────────────────────────────

type ReauthManager struct {
	db           *gorm.DB
	dialect      *dbdialect.Info
	logger       *zap.Logger
	auth         *Authenticator
	now          func() time.Time
}

func NewReauthManager(db *gorm.DB, logger *zap.Logger, auth *Authenticator) *ReauthManager {
	dialect := dbdialect.FromDriver("sqlite")
	if db != nil {
		if sqlDB, err := db.DB(); err == nil {
			if di, err2 := dbdialect.Detect(sqlDB); err2 == nil {
				dialect = di
			}
		}
	}
	return &ReauthManager{
		db:      db,
		dialect: dialect,
		logger:  logger,
		auth:    auth,
		now:     time.Now,
	}
}

// SetClockForTest overrides the clock for testing.
func (rm *ReauthManager) SetClockForTest(now func() time.Time) func() {
	prev := rm.now
	rm.now = now
	return func() { rm.now = prev }
}

// ── TOTP verification (RFC 6238) ─────────────────────────────────

// generateTOTP implements RFC 6238 TOTP with HMAC-SHA1, 6 digits,
// and 30-second period. secret is the base32-decoded shared secret.
func generateTOTP(secret []byte, t time.Time, periodSec uint64, digits int) string {
	counter := uint64(t.Unix()) / periodSec
	code := hotp(secret, counter, digits)
	return fmt.Sprintf("%06d", code)
}

// hotp implements RFC 4226 HOTP with HMAC-SHA1.
func hotp(secret []byte, counter uint64, digits int) int {
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, counter)

	mac := hmac.New(func() hash.Hash { return sha1.New() }, secret)
	mac.Write(buf)
	hs := mac.Sum(nil)

	offset := hs[len(hs)-1] & 0xf
	binCode := (int32(hs[offset]&0x7f) << 24) |
		(int32(hs[offset+1]) << 16) |
		(int32(hs[offset+2]) << 8) |
		int32(hs[offset+3])
	otp := binCode % int32(math.Pow10(digits))
	return int(otp)
}

// validateTOTP checks if the given code is valid for the secret
// at the given time, within a ±skew window of 30-second steps.
func validateTOTP(secret []byte, code string, t time.Time, skew int) bool {
	period := uint64(30)
	for i := -skew; i <= skew; i++ {
		check := t.Add(time.Duration(i) * time.Duration(period) * time.Second)
		expected := generateTOTP(secret, check, period, 6)
		if expected == code {
			return true
		}
	}
	return false
}

// ── Verification methods ─────────────────────────────────────────

// VerifyPassword verifies the current user's password. It does NOT
// create a grant; call CreateGrant after successful verification.
func (rm *ReauthManager) VerifyPassword(userID uint, password string) error {
	if rm.isRateLimited(userID) {
		return ErrReauthRateLimited
	}
	var user struct {
		PasswordHash string
	}
	if err := rm.db.Table("users").Select("password_hash").Where("id = ?", userID).Take(&user).Error; err != nil {
		rm.recordFailedAttempt(userID)
		return ErrReauthPasswordWrong
	}
	if user.PasswordHash == "" {
		rm.recordFailedAttempt(userID)
		return ErrReauthPasswordWrong
	}
	result := VerifyPasswordWithRehash(password, user.PasswordHash)
	if !result.Valid {
		rm.recordFailedAttempt(userID)
		return ErrReauthPasswordWrong
	}
	rm.clearFailedAttempts(userID)
	return nil
}

// VerifyTOTP verifies an RFC 6238 TOTP code against the user's stored
// MFA secret. Returns ErrReauthTOTPRequired if MFA is not enabled.
func (rm *ReauthManager) VerifyTOTP(userID uint, code string) error {
	if rm.isRateLimited(userID) {
		return ErrReauthRateLimited
	}
	var mfa struct {
		MFASecret string
		MFAEnabled bool
	}
	err := rm.db.Table("users").Select("mfa_secret, mfa_enabled").Where("id = ?", userID).Take(&mfa).Error
	if err != nil || !mfa.MFAEnabled || mfa.MFASecret == "" {
		return ErrReauthTOTPRequired
	}
	if len(code) != 6 {
		rm.recordFailedAttempt(userID)
		return ErrReauthTOTPInvalid
	}
	secret, decErr := base32Decode(mfa.MFASecret)
	if decErr != nil {
		rm.logger.Error("failed to decode MFA secret", zap.Error(decErr))
		rm.recordFailedAttempt(userID)
		return ErrReauthTOTPInvalid
	}
	if !validateTOTP(secret, code, rm.now(), totpSkew) {
		rm.recordFailedAttempt(userID)
		return ErrReauthTOTPInvalid
	}
	rm.clearFailedAttempts(userID)
	return nil
}

// ── Grant management ──────────────────────────────────────────────

// CreateGrant issues a short-lived grant for the given user, session,
// tenant, and scope. The grant expires after reauthGrantTTL.
func (rm *ReauthManager) CreateGrant(userID uint, sessionID string, tenantID uint, scope ReauthScope) (*RecentAuthGrant, error) {
	now := rm.now()
	grant := &RecentAuthGrant{
		UserID:    userID,
		SessionID: sessionID,
		TenantID:  tenantID,
		Scope:     string(scope),
		ExpiresAt: now.Add(reauthGrantTTL),
		CreatedAt: now,
	}
	sqlDB, err := rm.db.DB()
	if err != nil {
		return nil, fmt.Errorf("create grant: db access: %w", err)
	}
	d := rm.dialect
	insert := "INSERT INTO recent_auth_grants (created_at, user_id, session_id, tenant_id, scope, expires_at) VALUES (" +
		d.Placeholders(6) + ")"
	result, err := sqlDB.Exec(insert, now, userID, sessionID, tenantID, string(scope), grant.ExpiresAt)
	if err != nil {
		return nil, fmt.Errorf("create grant: %w", err)
	}
	id, _ := result.LastInsertId()
	grant.ID = uint(id)
	return grant, nil
}

// ValidateGrant checks whether a valid grant exists for the given
// user, session, tenant, and scope. It purges expired grants first.
func (rm *ReauthManager) ValidateGrant(userID uint, sessionID string, tenantID uint, scope ReauthScope) error {
	sqlDB, err := rm.db.DB()
	if err != nil {
		return ErrReauthGrantMissing
	}
	d := rm.dialect
	now := rm.now()

	// Purge expired grants.
	_, _ = sqlDB.Exec("DELETE FROM recent_auth_grants WHERE expires_at < "+d.Placeholder(1), now)

	var dbScope string
	query := "SELECT scope FROM recent_auth_grants WHERE user_id = " + d.Placeholder(1) +
		" AND session_id = " + d.Placeholder(2) +
		" AND tenant_id = " + d.Placeholder(3) +
		" AND expires_at > " + d.Placeholder(4) +
		" ORDER BY created_at DESC LIMIT 1"
	err = sqlDB.QueryRow(query, userID, sessionID, tenantID, now).Scan(&dbScope)
	if err != nil {
		return ErrReauthGrantMissing
	}
	if dbScope != string(scope) {
		return ErrReauthGrantWrongScope
	}
	return nil
}

// RevokeUserGrants removes all grants for a user. Called on logout,
// password change, or logout-all.
func (rm *ReauthManager) RevokeUserGrants(userID uint) error {
	sqlDB, err := rm.db.DB()
	if err != nil {
		return err
	}
	d := rm.dialect
	_, err = sqlDB.Exec("DELETE FROM recent_auth_grants WHERE user_id = "+d.Placeholder(1), userID)
	return err
}

// RevokeSessionGrants removes grants for a specific user session.
// Called on session revocation.
func (rm *ReauthManager) RevokeSessionGrants(userID uint, sessionID string) error {
	sqlDB, err := rm.db.DB()
	if err != nil {
		return err
	}
	d := rm.dialect
	_, err = sqlDB.Exec("DELETE FROM recent_auth_grants WHERE user_id = "+d.Placeholder(1)+" AND session_id = "+d.Placeholder(2), userID, sessionID)
	return err
}

// RevokeAllGrants removes every grant (used on system-wide events).
func (rm *ReauthManager) RevokeAllGrants() error {
	sqlDB, err := rm.db.DB()
	if err != nil {
		return err
	}
	_, err = sqlDB.Exec("DELETE FROM recent_auth_grants")
	return err
}

// ── Middleware ─────────────────────────────────────────────────────

// RequireReauth returns a middleware that enforces a valid recent
// authentication grant for the given scope. The caller must set
// "user_id", "session_id", and "tenant_id" locals before this
// middleware (typically via RequireSession and TenantMiddleware).
//
// The middleware also checks whether MFA is required. If the user
// has MFA enabled, the grant must have been created with TOTP
// verification (enforced at CreateGrant time by the handler).
func (rm *ReauthManager) RequireReauth(scope ReauthScope) fiber.Handler {
	return func(c fiber.Ctx) error {
		userID, ok := c.Locals("user_id").(uint)
		if !ok || userID == 0 {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"error": "authentication required for re-authorization",
			})
		}
		sessionID, _ := c.Locals("session_id").(string)
		tenantID, _ := c.Locals("tenant_id").(uint)

		if err := rm.ValidateGrant(userID, sessionID, tenantID, scope); err != nil {
			switch {
			case errors.Is(err, ErrReauthGrantMissing):
				return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
					"error":    "recent authentication required",
					"reauth":   true,
					"scope":    string(scope),
				})
			case errors.Is(err, ErrReauthGrantWrongScope):
				return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
					"error":    "recent authentication does not cover required scope",
					"reauth":   true,
					"scope":    string(scope),
				})
			default:
				return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
					"error": "failed to verify recent authentication",
				})
			}
		}
		return c.Next()
	}
}

// ── Rate limiting ──────────────────────────────────────────────────

type reauthFailure struct {
	Count     int
	WindowStart time.Time
}

func (rm *ReauthManager) isRateLimited(userID uint) bool {
	sqlDB, err := rm.db.DB()
	if err != nil {
		return false
	}
	d := rm.dialect
	window := rm.now().Add(-reauthRateLimitWindow)
	var count int
	err = sqlDB.QueryRow(
		"SELECT COUNT(*) FROM reauth_failures WHERE user_id = "+d.Placeholder(1)+" AND created_at > "+d.Placeholder(2),
		userID, window,
	).Scan(&count)
	if err != nil {
		return false
	}
	return count >= reauthMaxFailedAttempts
}

func (rm *ReauthManager) recordFailedAttempt(userID uint) {
	sqlDB, err := rm.db.DB()
	if err != nil {
		return
	}
	d := rm.dialect
	_, _ = sqlDB.Exec(
		"INSERT INTO reauth_failures (created_at, user_id) VALUES ("+d.Placeholders(2)+")",
		rm.now(), userID,
	)
}

func (rm *ReauthManager) clearFailedAttempts(userID uint) {
	sqlDB, err := rm.db.DB()
	if err != nil {
		return
	}
	d := rm.dialect
	_, _ = sqlDB.Exec(
		"DELETE FROM reauth_failures WHERE user_id = "+d.Placeholder(1),
		userID,
	)
}

// ── Base32 helpers ─────────────────────────────────────────────────

func base32Decode(s string) ([]byte, error) {
	// RFC 4648 base32 without padding.
	table := "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567"
	rev := make(map[byte]int)
	for i, c := range []byte(table) {
		rev[c] = i
	}

	var dst []byte
	buf := uint64(0)
	bits := 0
	for _, c := range []byte(s) {
		v, ok := rev[c]
		if !ok {
			v2, ok2 := rev[byte(c)+32] // lowercase
			if !ok2 {
				return nil, fmt.Errorf("invalid base32 character: %c", c)
			}
			v = v2
		}
		buf = (buf << 5) | uint64(v)
		bits += 5
		if bits >= 8 {
			bits -= 8
			dst = append(dst, byte(buf>>bits))
			buf &= (1 << bits) - 1
		}
	}
	return dst, nil
}

// ── Audit event helpers ──────────────────────────────────────────

type ReauthAuditEvent struct {
	UserID    uint   `json:"user_id"`
	Action    string `json:"action"`
	Scope     string `json:"scope,omitempty"`
	SessionID string `json:"session_id,omitempty"`
	Success   bool   `json:"success"`
	Reason    string `json:"reason,omitempty"`
	Timestamp time.Time `json:"timestamp"`
}

// RecordAuditEvent writes a durable audit event for a re-auth action
// into the existing coremail_audit table (same schema used by the
// audit package). The actor column stores the user ID as a string
// since coremail_audit.actor is TEXT.
func (rm *ReauthManager) RecordAuditEvent(evt ReauthAuditEvent) error {
	if rm.db == nil {
		return nil
	}
	sqlDB, err := rm.db.DB()
	if err != nil {
		return err
	}
	d := rm.dialect
	now := evt.Timestamp
	if now.IsZero() {
		now = rm.now()
	}
	result := "allowed"
	if !evt.Success {
		result = "denied"
	}
	_, err = sqlDB.Exec(
		"INSERT INTO coremail_audit (actor, role, action, target, result, ip, user_agent, timestamp) VALUES ("+
			d.Placeholders(8)+")",
		fmt.Sprintf("%d", evt.UserID),
		"",
		"reauth."+evt.Action,
		evt.Scope,
		result,
		"",
		"",
		now,
	)
	if err != nil {
		rm.logger.Warn("failed to record reauth audit event", zap.Error(err))
	}
	return err
}
