package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"time"

	"github.com/gofiber/fiber/v3"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

var (
	csrfTokenLength = 32
)

// CSRFManager handles double-submit cookie CSRF protection.
type CSRFManager struct {
	db     *gorm.DB
	logger *zap.Logger
	secure bool
}

// NewCSRFManager creates a new CSRF manager and migrates its table.
func NewCSRFManager(db *gorm.DB, logger *zap.Logger, secure bool) *CSRFManager {
	// RC2 FIX: Skip AutoMigrate - tables are created with raw SQL
	return &CSRFManager{
		db:     db,
		logger: logger,
		secure: secure,
	}
}

// CSRFRecord stores CSRF token hashes for validation.
type CSRFRecord struct {
	ID        uint      `gorm:"primaryKey"`
	TokenHash string    `gorm:"uniqueIndex;not null"`
	UserID    uint      `gorm:"index;not null"`
	ExpiresAt time.Time `gorm:"not null"`
	CreatedAt time.Time
}

// GenerateToken creates a new CSRF token, stores its hash, and sets the cookie.
func (cm *CSRFManager) GenerateToken(c fiber.Ctx, userID uint) (string, error) {
	b := make([]byte, csrfTokenLength)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("failed to generate CSRF token: %w", err)
	}

	token := base64.RawURLEncoding.EncodeToString(b)
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(token)))

	record := CSRFRecord{
		TokenHash: hash,
		UserID:    userID,
		ExpiresAt: time.Now().Add(24 * time.Hour),
	}

	if err := cm.db.Create(&record).Error; err != nil {
		return "", fmt.Errorf("failed to store CSRF token: %w", err)
	}

	c.Cookie(&fiber.Cookie{
		Name:     "csrf_token",
		Value:    token,
		Expires:  time.Now().Add(24 * time.Hour),
		HTTPOnly: false,
		Secure:   cm.secure,
		SameSite: "Strict",
		Path:     "/",
	})

	return token, nil
}

// Middleware validates CSRF tokens on state-changing requests.
func (cm *CSRFManager) Middleware() fiber.Handler {
	return func(c fiber.Ctx) error {
		method := c.Method()

		if method == "GET" || method == "HEAD" || method == "OPTIONS" {
			return c.Next()
		}

		cookieToken := c.Cookies("csrf_token")
		if cookieToken == "" {
			cm.logger.Warn("CSRF cookie missing")
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
				"error": "CSRF token missing in cookie",
			})
		}

		headerToken := c.Get("X-CSRF-Token")
		if headerToken == "" {
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
				"error": "CSRF token missing in header",
			})
		}

		if cookieToken != headerToken {
			cm.logger.Warn("CSRF token mismatch")
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
				"error": "CSRF token mismatch",
			})
		}

		cookieHash := fmt.Sprintf("%x", sha256.Sum256([]byte(cookieToken)))
		var record CSRFRecord
		if err := cm.db.Where("token_hash = ? AND expires_at > ?", cookieHash, time.Now()).
			First(&record).Error; err != nil {
			cm.logger.Warn("CSRF token not found or expired")
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
				"error": "CSRF token invalid or expired",
			})
		}

		return c.Next()
	}
}

// InvalidateUserTokens removes all CSRF tokens for a user.
func (cm *CSRFManager) InvalidateUserTokens(userID uint) error {
	return cm.db.Where("user_id = ?", userID).Delete(&CSRFRecord{}).Error
}

// InvalidateToken removes a specific CSRF token.
func (cm *CSRFManager) InvalidateToken(token string) error {
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(token)))
	return cm.db.Where("token_hash = ?", hash).Delete(&CSRFRecord{}).Error
}
