package handlers

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"fmt"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/config"
	"github.com/orvix/orvix/internal/dbdialect"
	"go.uber.org/zap"
)

func (h *Handler) AccountMFAStatus(c fiber.Ctx) error {
	userID := c.Locals("user_id").(uint)
	sqlDB, err := h.db.DB()
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "database error"})
	}
	var mfaEnabled bool
	var mfaLabel string
	dial := dbdialect.FromDriver(h.cfg.Database.Driver)
	err = sqlDB.QueryRow("SELECT COALESCE(mfa_enabled, "+dial.FalseLiteral()+"), COALESCE(mfa_label, '') FROM users WHERE id = "+dial.Placeholder(1), userID).Scan(&mfaEnabled, &mfaLabel)
	if err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "user not found"})
	}
	return c.JSON(fiber.Map{"enabled": mfaEnabled, "label": mfaLabel})
}

func (h *Handler) AccountMFASetup(c fiber.Ctx) error {
	userID := c.Locals("user_id").(uint)
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
	var passwordHash, email string
	dial := dbdialect.FromDriver(h.cfg.Database.Driver)
	err = sqlDB.QueryRow("SELECT email, password_hash FROM users WHERE id = "+dial.Placeholder(1), userID).Scan(&email, &passwordHash)
	if err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "user not found"})
	}
	if !h.auth.VerifyPassword(req.CurrentPassword, passwordHash) {
		return c.Status(401).JSON(fiber.Map{"error": "current password is incorrect"})
	}

	secretBytes := make([]byte, mfaSecretSize)
	if _, err := rand.Read(secretBytes); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "failed to generate secret"})
	}
	secret := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(secretBytes)
	hashedSecret := hashSecret(secretBytes)
	_, err = sqlDB.Exec("UPDATE users SET pending_mfa_secret = "+dial.Placeholder(1)+", mfa_label = "+dial.Placeholder(2)+" WHERE id = "+dial.Placeholder(3),
		base64Encode(hashedSecret), email, userID)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "failed to store pending setup"})
	}
	encryptedSecret, err := config.Encrypt(secretBytes)
	if err != nil {
		h.logger.Error("failed to encrypt pending mfa secret", zap.Error(err))
		return c.Status(500).JSON(fiber.Map{"error": "failed to store pending setup"})
	}
	_, err = sqlDB.Exec("UPDATE users SET pending_mfa_secret_raw = "+dial.Placeholder(1)+" WHERE id = "+dial.Placeholder(2), encryptedSecret, userID)
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
	return c.JSON(fiber.Map{"secret": secret, "otpauth_url": otpauthURL, "label": email})
}

func (h *Handler) AccountMFAVerify(c fiber.Ctx) error {
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
	dial := dbdialect.FromDriver(h.cfg.Database.Driver)
	err = sqlDB.QueryRow("SELECT pending_mfa_secret, COALESCE(pending_mfa_secret_raw, ''), email FROM users WHERE id = "+dial.Placeholder(1), userID).Scan(&pendingSecret, &pendingSecretRaw, &email)
	if err != nil || pendingSecret == "" {
		return c.Status(400).JSON(fiber.Map{"error": "no pending MFA setup; call /mfa/setup first"})
	}
	if pendingSecretRaw == "" {
		return c.Status(400).JSON(fiber.Map{"error": "no pending MFA setup; call /mfa/setup first"})
	}
	rawSecretBytes, err := config.Decrypt(pendingSecretRaw)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "internal error"})
	}
	if !verifyTOTP(rawSecretBytes, req.Code) {
		h.logger.Warn("mfa setup verify: invalid TOTP code", zap.Uint("user_id", userID))
		return c.Status(400).JSON(fiber.Map{"error": "invalid TOTP code"})
	}
	_, err = sqlDB.Exec("UPDATE users SET mfa_enabled = "+dial.TrueLiteral()+", mfa_secret = pending_mfa_secret, mfa_secret_raw = pending_mfa_secret_raw, pending_mfa_secret = '', pending_mfa_secret_raw = '' WHERE id = "+dial.Placeholder(1), userID)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "failed to enable MFA"})
	}
	rawCodes := generateRecoveryCodes()
	for _, code := range rawCodes {
		codeHash := fmt.Sprintf("%x", sha256.Sum256([]byte(code)))
		sqlDB.Exec("INSERT INTO mfa_recovery_codes (user_id, code_hash) VALUES ("+dial.Placeholder(1)+", "+dial.Placeholder(2)+")", userID, codeHash)
	}
	h.writeAuditLog(c, "mfa.enabled", email)
	return c.JSON(fiber.Map{"status": "mfa_enabled", "recovery_codes": rawCodes})
}

func (h *Handler) AccountMFADisable(c fiber.Ctx) error {
	userID := c.Locals("user_id").(uint)
	var req struct {
		CurrentPassword string `json:"current_password"`
		Code            string `json:"code"`
		RecoveryCode    string `json:"recovery_code"`
	}
	if err := c.Bind().JSON(&req); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "current_password and code or recovery_code required"})
	}
	sqlDB, err := h.db.DB()
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "database error"})
	}
	var passwordHash string
	dial := dbdialect.FromDriver(h.cfg.Database.Driver)
	err = sqlDB.QueryRow("SELECT password_hash FROM users WHERE id = "+dial.Placeholder(1), userID).Scan(&passwordHash)
	if err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "user not found"})
	}
	if !h.auth.VerifyPassword(req.CurrentPassword, passwordHash) {
		return c.Status(401).JSON(fiber.Map{"error": "current password is incorrect"})
	}

	if req.RecoveryCode != "" {
		codeHash := fmt.Sprintf("%x", sha256.Sum256([]byte(req.RecoveryCode)))
		var recoveryID uint
		err = sqlDB.QueryRow("SELECT id FROM mfa_recovery_codes WHERE user_id = "+dial.Placeholder(1)+" AND code_hash = "+dial.Placeholder(2)+" AND used_at IS NULL", userID, codeHash).Scan(&recoveryID)
		if err != nil {
			return c.Status(401).JSON(fiber.Map{"error": "invalid recovery code"})
		}
		res, err := sqlDB.Exec("UPDATE mfa_recovery_codes SET used_at = "+dial.Placeholder(1)+" WHERE id = "+dial.Placeholder(2)+" AND used_at IS NULL", time.Now().UTC(), recoveryID)
		if err != nil || mustRowsAffected(res) != 1 {
			return c.Status(401).JSON(fiber.Map{"error": "invalid recovery code"})
		}
	} else if req.Code != "" {
		var mfaSecretRaw string
		err = sqlDB.QueryRow("SELECT COALESCE(mfa_secret_raw, '') FROM users WHERE id = "+dial.Placeholder(1), userID).Scan(&mfaSecretRaw)
		if err != nil || mfaSecretRaw == "" {
			return c.Status(400).JSON(fiber.Map{"error": "MFA is not enabled"})
		}
		rawSecretBytes, err := config.Decrypt(mfaSecretRaw)
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"error": "internal error"})
		}
		if !verifyTOTP(rawSecretBytes, req.Code) {
			return c.Status(401).JSON(fiber.Map{"error": "invalid MFA code"})
		}
	} else {
		return c.Status(400).JSON(fiber.Map{"error": "code or recovery_code required"})
	}

	_, err = sqlDB.Exec("UPDATE users SET mfa_enabled = "+dial.FalseLiteral()+", mfa_secret = '', mfa_secret_raw = '', pending_mfa_secret = '', pending_mfa_secret_raw = '' WHERE id = "+dial.Placeholder(1), userID)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "failed to disable MFA"})
	}
	sqlDB.Exec("DELETE FROM mfa_recovery_codes WHERE user_id = "+dial.Placeholder(1), userID)
	h.writeAuditLog(c, "mfa.disabled", fmt.Sprintf("user:%d", userID))
	return c.JSON(fiber.Map{"status": "mfa_disabled"})
}

func (h *Handler) AccountMFARegenerateRecoveryCodes(c fiber.Ctx) error {
	userID := c.Locals("user_id").(uint)
	var req struct {
		CurrentPassword string `json:"current_password"`
		Code            string `json:"code"`
	}
	if err := c.Bind().JSON(&req); err != nil || req.CurrentPassword == "" {
		return c.Status(400).JSON(fiber.Map{"error": "current_password required"})
	}
	sqlDB, err := h.db.DB()
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "database error"})
	}
	var passwordHash string
	dial := dbdialect.FromDriver(h.cfg.Database.Driver)
	err = sqlDB.QueryRow("SELECT password_hash FROM users WHERE id = "+dial.Placeholder(1), userID).Scan(&passwordHash)
	if err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "user not found"})
	}
	if !h.auth.VerifyPassword(req.CurrentPassword, passwordHash) {
		return c.Status(401).JSON(fiber.Map{"error": "current password is incorrect"})
	}
	if req.Code != "" {
		var mfaSecretRaw string
		err = sqlDB.QueryRow("SELECT COALESCE(mfa_secret_raw, '') FROM users WHERE id = "+dial.Placeholder(1), userID).Scan(&mfaSecretRaw)
		if err != nil || mfaSecretRaw == "" {
			return c.Status(400).JSON(fiber.Map{"error": "MFA is not enabled"})
		}
		rawSecretBytes, err := config.Decrypt(mfaSecretRaw)
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"error": "internal error"})
		}
		if !verifyTOTP(rawSecretBytes, req.Code) {
			return c.Status(401).JSON(fiber.Map{"error": "invalid MFA code"})
		}
	}

	sqlDB.Exec("DELETE FROM mfa_recovery_codes WHERE user_id = "+dial.Placeholder(1), userID)
	rawCodes := generateRecoveryCodes()
	for _, code := range rawCodes {
		codeHash := fmt.Sprintf("%x", sha256.Sum256([]byte(code)))
		sqlDB.Exec("INSERT INTO mfa_recovery_codes (user_id, code_hash) VALUES ("+dial.Placeholder(1)+", "+dial.Placeholder(2)+")", userID, codeHash)
	}
	h.writeAuditLog(c, "mfa.recovery_codes.regenerated", fmt.Sprintf("user:%d", userID))
	return c.JSON(fiber.Map{"recovery_codes": rawCodes})
}

func mustRowsAffected(res interface{}) int64 {
	if r, ok := res.(interface{ RowsAffected() (int64, error) }); ok {
		n, _ := r.RowsAffected()
		return n
	}
	return 0
}
