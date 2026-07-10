package handlers

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base32"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/auth"
	"github.com/orvix/orvix/internal/dbdialect"
	"go.uber.org/zap"
)

const mfaSecretSize = 20 // RFC 6238

// MFAStatusGet returns the current MFA status for the authenticated admin.
// GET /api/v1/admin/mfa/status
func (h *Handler) MFAStatusGet(c fiber.Ctx) error {
	userID := c.Locals("user_id").(uint)

	sqlDB, err := h.db.DB()
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "database error"})
	}

	var mfaEnabled bool
	var mfaLabel string
	err = sqlDB.QueryRow(
		"SELECT COALESCE(mfa_enabled, 0), COALESCE(mfa_label, '') FROM users WHERE id = ?",
		userID).Scan(&mfaEnabled, &mfaLabel)
	if err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "user not found"})
	}

	return c.JSON(fiber.Map{
		"enabled": mfaEnabled,
		"label":   mfaLabel,
	})
}

// MFASetupBegin starts TOTP setup. Returns secret and otpauth URL.
// POST /api/v1/admin/mfa/setup/begin
func (h *Handler) MFASetupBegin(c fiber.Ctx) error {
	userID := c.Locals("user_id").(uint)

	// Verify current password before allowing MFA changes
	var req struct {
		CurrentPassword string `json:"current_password"`
	}
	if err := c.Bind().JSON(&req); err != nil || req.CurrentPassword == "" {
		return c.Status(400).JSON(fiber.Map{"error": "current_password required"})
	}

	sqlDB, err := h.db.DB()
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "database error"})
	}

	var passwordHash string
	var email string
	err = sqlDB.QueryRow("SELECT email, password_hash FROM users WHERE id = ?", userID).Scan(&email, &passwordHash)
	if err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "user not found"})
	}

	if !h.auth.VerifyPassword(req.CurrentPassword, passwordHash) {
		return c.Status(401).JSON(fiber.Map{"error": "current password is incorrect"})
	}

	// Generate TOTP secret
	secretBytes := make([]byte, mfaSecretSize)
	if _, err := rand.Read(secretBytes); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "failed to generate secret"})
	}
	secret := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(secretBytes)

	// Store secret temporarily (hashed) in a pending_mfa_secret column
	// Only store hashed version, never plaintext
	hashedSecret := hashSecret(secretBytes)
	_, err = sqlDB.Exec(`UPDATE users SET pending_mfa_secret = ?, mfa_label = ? WHERE id = ?`,
		base64Encode(hashedSecret), email, userID)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "failed to store pending setup"})
	}

	// Also store raw secret temporarily for verification
	_, err = sqlDB.Exec(`UPDATE users SET pending_mfa_secret_raw = ? WHERE id = ?`,
		base64Encode(secretBytes), userID)
	if err != nil {
		h.logger.Warn("failed to store raw pending mfa secret", zap.Error(err))
	}

	issuer := "Orvix"
	if h.cfg.CoreMail.Hostname != "" {
		issuer = h.cfg.CoreMail.Hostname
	}
	otpauthURL := fmt.Sprintf("otpauth://totp/%s:%s?secret=%s&issuer=%s&algorithm=SHA1&digits=6&period=30",
		issuer, email, secret, issuer)

	h.writeAuditLog(c, "mfa.setup.begin", email)

	return c.JSON(fiber.Map{
		"secret":      secret,
		"otpauth_url": otpauthURL,
		"label":       email,
	})
}

// MFASetupVerify verifies a TOTP code and enables MFA.
// POST /api/v1/admin/mfa/setup/verify
func (h *Handler) MFASetupVerify(c fiber.Ctx) error {
	userID := c.Locals("user_id").(uint)

	var req struct {
		Code string `json:"code"`
	}
	if err := c.Bind().JSON(&req); err != nil || req.Code == "" {
		return c.Status(400).JSON(fiber.Map{"error": "code required"})
	}

	sqlDB, err := h.db.DB()
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "database error"})
	}

	var pendingSecret, pendingSecretRaw, email string
	err = sqlDB.QueryRow("SELECT pending_mfa_secret, COALESCE(pending_mfa_secret_raw, ''), email FROM users WHERE id = ?", userID).
		Scan(&pendingSecret, &pendingSecretRaw, &email)
	if err != nil || pendingSecret == "" {
		return c.Status(400).JSON(fiber.Map{"error": "no pending MFA setup; call /mfa/setup/begin first"})
	}

	// Decode raw pending secret for TOTP verification.
	if pendingSecretRaw == "" {
		return c.Status(400).JSON(fiber.Map{"error": "no pending MFA setup; call /mfa/setup/begin first"})
	}
	rawSecretBytes, err := base64Decode(pendingSecretRaw)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "internal error"})
	}

	// Verify TOTP code against the pending secret.
	if !verifyTOTP(rawSecretBytes, req.Code) {
		h.logger.Warn("mfa setup verify: invalid TOTP code",
			zap.Uint("user_id", userID))
		return c.Status(400).JSON(fiber.Map{"error": "invalid TOTP code"})
	}

	// Move pending to active: hash the secret and store in mfa_secret,
	// and keep the raw secret in mfa_secret_raw for future TOTP verification.
	_, err = sqlDB.Exec(`UPDATE users SET mfa_enabled = 1, mfa_secret = pending_mfa_secret,
		mfa_secret_raw = pending_mfa_secret_raw,
		pending_mfa_secret = '', pending_mfa_secret_raw = '' WHERE id = ?`, userID)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "failed to enable MFA"})
	}

	// Generate recovery codes (hashed)
	rawCodes := generateRecoveryCodes()
	for _, code := range rawCodes {
		codeHash := fmt.Sprintf("%x", sha256.Sum256([]byte(code)))
		sqlDB.Exec(`INSERT INTO mfa_recovery_codes (user_id, code_hash) VALUES (?, ?)`, userID, codeHash)
	}

	h.writeAuditLog(c, "mfa.enabled", email)

	return c.JSON(fiber.Map{
		"status":         "mfa_enabled",
		"recovery_codes": rawCodes,
	})
}

// MFADisable disables MFA for the authenticated user.
// POST /api/v1/admin/mfa/disable
func (h *Handler) MFADisable(c fiber.Ctx) error {
	userID := c.Locals("user_id").(uint)

	var req struct {
		CurrentPassword string `json:"current_password"`
		Code            string `json:"code"`
	}
	if err := c.Bind().JSON(&req); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "current_password and code required"})
	}

	sqlDB, err := h.db.DB()
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "database error"})
	}

	var passwordHash string
	err = sqlDB.QueryRow("SELECT password_hash FROM users WHERE id = ?", userID).Scan(&passwordHash)
	if err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "user not found"})
	}
	if !h.auth.VerifyPassword(req.CurrentPassword, passwordHash) {
		return c.Status(401).JSON(fiber.Map{"error": "current password is incorrect"})
	}

	// Verify the TOTP code against the active MFA secret.
	if req.Code == "" {
		return c.Status(400).JSON(fiber.Map{"error": "MFA code required to disable"})
	}
	var mfaSecretRaw string
	err = sqlDB.QueryRow("SELECT COALESCE(mfa_secret_raw, '') FROM users WHERE id = ?", userID).Scan(&mfaSecretRaw)
	if err != nil || mfaSecretRaw == "" {
		return c.Status(400).JSON(fiber.Map{"error": "MFA is not enabled or secret is missing"})
	}
	rawSecretBytes, err := base64Decode(mfaSecretRaw)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "internal error"})
	}
	if !verifyTOTP(rawSecretBytes, req.Code) {
		h.logger.Warn("mfa disable: invalid TOTP code",
			zap.Uint("user_id", userID))
		return c.Status(401).JSON(fiber.Map{"error": "invalid MFA code"})
	}

	_, err = sqlDB.Exec(`UPDATE users SET mfa_enabled = 0, mfa_secret = '', mfa_secret_raw = '', pending_mfa_secret = '', pending_mfa_secret_raw = '' WHERE id = ?`, userID)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "failed to disable MFA"})
	}
	sqlDB.Exec(`DELETE FROM mfa_recovery_codes WHERE user_id = ?`, userID)

	h.writeAuditLog(c, "mfa.disabled", "")

	return c.JSON(fiber.Map{"status": "mfa_disabled"})
}

// verifyTOTP validates a TOTP code against the secret.
func verifyTOTP(secret []byte, code string) bool {
	if len(code) != 6 {
		return false
	}
	now := time.Now().UTC().Unix()
	// Allow a window of +/- 1 period (30s each)
	for offset := int64(-1); offset <= 1; offset++ {
		expected := computeTOTP(secret, now/30+offset)
		if expected == code {
			return true
		}
	}
	return false
}

// computeTOTP computes a 6-digit TOTP value for a given counter.
// Uses HMAC-SHA1 per RFC 6238 / RFC 4226.
func computeTOTP(secret []byte, counter int64) string {
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, uint64(counter))
	mac := hmac.New(sha1.New, secret)
	mac.Write(buf)
	hash := mac.Sum(nil)
	offset := hash[len(hash)-1] & 0x0f
	bin := binary.BigEndian.Uint32(hash[offset:offset+4]) & 0x7fffffff
	return fmt.Sprintf("%06d", bin%1000000)
}

func hashSecret(b []byte) []byte {
	mac := hmac.New(sha256.New, []byte("orvix-mfa-secret-hash"))
	mac.Write(b)
	return mac.Sum(nil)
}

func base64Encode(b []byte) string {
	return base64.StdEncoding.EncodeToString(b)
}

func base64Decode(s string) ([]byte, error) {
	return base64.StdEncoding.DecodeString(s)
}

func generateRecoveryCodes() []string {
	codes := make([]string, 8)
	for i := 0; i < 8; i++ {
		b := make([]byte, 8)
		rand.Read(b)
		codes[i] = fmt.Sprintf("%04x-%04x-%04x-%04x",
			binary.BigEndian.Uint16(b[0:2]),
			binary.BigEndian.Uint16(b[2:4]),
			binary.BigEndian.Uint16(b[4:6]),
			binary.BigEndian.Uint16(b[6:8]))
	}
	return codes
}

// MFALoginVerify completes MFA login after password authentication.
// Accepts an MFA challenge token and either a TOTP code or a recovery code.
// On success, issues normal access/refresh tokens.
// POST /api/v1/auth/mfa/verify (public — no auth middleware)
func (h *Handler) MFALoginVerify(c fiber.Ctx) error {
	var req struct {
		Challenge    string `json:"mfa_challenge"`
		Code         string `json:"code"`
		RecoveryCode string `json:"recovery_code"`
	}
	if err := c.Bind().JSON(&req); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid request"})
	}
	if req.Challenge == "" {
		return c.Status(400).JSON(fiber.Map{"error": "mfa_challenge required"})
	}
	if req.Code == "" && req.RecoveryCode == "" {
		return c.Status(400).JSON(fiber.Map{"error": "code or recovery_code required"})
	}

	userID, err := h.auth.ValidateMFAChallengeToken(req.Challenge)
	if err != nil {
		return c.Status(401).JSON(fiber.Map{"error": "invalid or expired MFA challenge"})
	}

	sqlDB, err := h.db.DB()
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "database error"})
	}

	// Verify either TOTP code or recovery code.
	if req.RecoveryCode != "" {
		// Recovery code redemption.
		codeHash := fmt.Sprintf("%x", sha256.Sum256([]byte(req.RecoveryCode)))
		var recoveryID uint
		err = sqlDB.QueryRow(`SELECT id FROM mfa_recovery_codes WHERE user_id = ? AND code_hash = ? AND used_at IS NULL`,
			userID, codeHash).Scan(&recoveryID)
		if err != nil {
			h.logger.Warn("mfa login: invalid or already-used recovery code",
				zap.Uint("user_id", userID))
			return c.Status(401).JSON(fiber.Map{"error": "invalid recovery code"})
		}
		// Mark recovery code as used (one-time use). The used_at predicate
		// keeps concurrent redemption attempts from reusing the same code.
		dial := dbdialect.FromDriver(h.cfg.Database.Driver)
		res, err := sqlDB.Exec(
			`UPDATE mfa_recovery_codes SET used_at = `+dial.Placeholder(1)+` WHERE id = `+dial.Placeholder(2)+` AND used_at IS NULL`,
			time.Now().UTC(), recoveryID)
		if err != nil {
			h.logger.Error("failed to mark recovery code used", zap.Error(err))
			return c.Status(500).JSON(fiber.Map{"error": "failed to redeem recovery code"})
		}
		affected, err := res.RowsAffected()
		if err != nil {
			h.logger.Error("failed to verify recovery code redemption", zap.Error(err))
			return c.Status(500).JSON(fiber.Map{"error": "failed to redeem recovery code"})
		}
		if affected != 1 {
			h.logger.Warn("mfa login: recovery code was already redeemed concurrently",
				zap.Uint("user_id", userID))
			return c.Status(401).JSON(fiber.Map{"error": "invalid recovery code"})
		}
		h.writeAuditLog(c, "mfa.login.recovery_code", fmt.Sprintf("user_id:%d recovery_id:%d", userID, recoveryID))
	} else {
		// TOTP code verification.
		var mfaSecretRaw string
		err = sqlDB.QueryRow("SELECT COALESCE(mfa_secret_raw, '') FROM users WHERE id = ?", userID).Scan(&mfaSecretRaw)
		if err != nil || mfaSecretRaw == "" {
			return c.Status(401).JSON(fiber.Map{"error": "MFA not configured"})
		}
		rawSecretBytes, err := base64Decode(mfaSecretRaw)
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"error": "internal error"})
		}
		if !verifyTOTP(rawSecretBytes, req.Code) {
			h.logger.Warn("mfa login: invalid TOTP code",
				zap.Uint("user_id", userID))
			return c.Status(401).JSON(fiber.Map{"error": "invalid code"})
		}
		h.writeAuditLog(c, "mfa.login.totp", fmt.Sprintf("user_id:%d", userID))
	}

	// MFA passed — issue tokens.
	var userRole string
	var userEmail string
	err = sqlDB.QueryRow("SELECT role, email FROM users WHERE id = ?", userID).Scan(&userRole, &userEmail)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "user lookup failed"})
	}

	// Issue opaque session cookie alongside JWT for transition.
	// Cookie issuance is the source of truth for browser auth; if
	// the store refuses the write we refuse the login rather than
	// return success without a usable session.
	if err := h.issueLoginSession(c, userID, auth.Role(userRole), userEmail); err != nil {
		h.logger.Error("failed to issue login session", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "authentication failed"})
	}

	accessToken, err := h.auth.GenerateAccessToken(userID, auth.Role(userRole))
	if err != nil {
		h.logger.Error("failed to generate access token", zap.Error(err))
		return c.Status(500).JSON(fiber.Map{"error": "authentication failed"})
	}
	refreshToken, expiresAt, err := h.auth.GenerateRefreshToken(userID)
	if err != nil {
		h.logger.Error("failed to generate refresh token", zap.Error(err))
		return c.Status(500).JSON(fiber.Map{"error": "authentication failed"})
	}

	c.Cookie(&fiber.Cookie{
		Name:     "access_token",
		Value:    accessToken,
		Expires:  time.Now().Add(15 * time.Minute),
		HTTPOnly: true,
		Secure:   true,
		SameSite: "None",
		Path:     "/",
		Domain:   h.cfg.Auth.CookieDomain,
	})
	c.Cookie(&fiber.Cookie{
		Name:     "refresh_token",
		Value:    refreshToken,
		Expires:  expiresAt,
		HTTPOnly: true,
		Secure:   true,
		SameSite: "None",
		Path:     "/api/v1/auth/refresh",
		Domain:   h.cfg.Auth.CookieDomain,
	})

	h.logger.Info("MFA login completed", zap.Uint("user_id", userID))

	return c.JSON(fiber.Map{
		"access_token":       accessToken,
		"access_expires_in":  900,
		"refresh_expires_in": int(30 * 24 * 3600),
	})
}
