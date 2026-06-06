package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/gofiber/fiber/v3"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// APIKeyRecord represents a stored API key.
type APIKeyRecord struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	Name      string    `gorm:"not null" json:"name"`
	KeyPrefix string    `gorm:"uniqueIndex;not null;size:8" json:"key_prefix"`
	KeyHash   string    `gorm:"uniqueIndex;not null" json:"-"`
	UserID    uint      `gorm:"index;not null" json:"user_id"`
	Role      string    `gorm:"not null;default:'user'" json:"role"`
	Enabled   bool      `gorm:"not null;default:true" json:"enabled"`
	LastUsed  time.Time `json:"last_used"`
	ExpiresAt time.Time `json:"expires_at"`
	CreatedAt time.Time `json:"created_at"`
}

// APIKeyManager handles API key lifecycle.
type APIKeyManager struct {
	db     *gorm.DB
	logger *zap.Logger
}

// NewAPIKeyManager creates a new API key manager.
func NewAPIKeyManager(db *gorm.DB, logger *zap.Logger) *APIKeyManager {
	_ = db.AutoMigrate(&APIKeyRecord{})
	return &APIKeyManager{
		db:     db,
		logger: logger,
	}
}

// Generate creates a new API key and returns the full key (shown once).
func (m *APIKeyManager) Generate(name string, userID uint, role string, ttl time.Duration) (string, *APIKeyRecord, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", nil, fmt.Errorf("failed to generate API key: %w", err)
	}

	fullKey := "orv_" + hex.EncodeToString(b)
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(fullKey)))
	prefix := fullKey[:11]

	record := &APIKeyRecord{
		Name:      name,
		KeyPrefix: prefix,
		KeyHash:   hash,
		UserID:    userID,
		Role:      role,
		Enabled:   true,
		ExpiresAt: time.Now().Add(ttl),
	}

	if err := m.db.Create(record).Error; err != nil {
		return "", nil, fmt.Errorf("failed to store API key: %w", err)
	}

	m.logger.Info("API key generated", zap.String("name", name), zap.String("prefix", prefix))
	return fullKey, record, nil
}

// Validate checks if an API key is valid and returns the record.
func (m *APIKeyManager) Validate(key string) (*APIKeyRecord, error) {
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(key)))

	var record APIKeyRecord
	if err := m.db.Where("key_hash = ? AND enabled = ?", hash, true).First(&record).Error; err != nil {
		return nil, fmt.Errorf("invalid API key")
	}

	if !record.ExpiresAt.IsZero() && time.Now().After(record.ExpiresAt) {
		return nil, fmt.Errorf("API key expired")
	}

	m.db.Model(&record).Update("last_used", time.Now())
	return &record, nil
}

// Rotate invalidates the old key and generates a new one.
func (m *APIKeyManager) Rotate(name string, userID uint, role string, ttl time.Duration) (string, *APIKeyRecord, error) {
	m.db.Where("name = ? AND user_id = ?", name, userID).Delete(&APIKeyRecord{})
	return m.Generate(name, userID, role, ttl)
}

// Revoke disables an API key by ID.
func (m *APIKeyManager) Revoke(id uint) error {
	result := m.db.Model(&APIKeyRecord{}).Where("id = ?", id).Update("enabled", false)
	if result.RowsAffected == 0 {
		return fmt.Errorf("API key not found")
	}
	return nil
}

// List returns all API keys for a user.
func (m *APIKeyManager) List(userID uint) ([]APIKeyRecord, error) {
	var keys []APIKeyRecord
	if err := m.db.Where("user_id = ?", userID).Find(&keys).Error; err != nil {
		return nil, err
	}
	return keys, nil
}

// Middleware validates API key from Authorization header (Bearer scheme).
func (m *APIKeyManager) Middleware() fiber.Handler {
	return func(c fiber.Ctx) error {
		authHeader := c.Get("Authorization")
		if len(authHeader) < 8 || authHeader[:7] != "Bearer " {
			return c.Next()
		}
		token := authHeader[7:]

		if len(token) < 10 || token[:4] != "orv_" {
			return c.Next()
		}

		record, err := m.Validate(token)
		if err != nil {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "invalid API key"})
		}

		c.Locals("user_id", record.UserID)
		c.Locals("role", Role(record.Role))
		c.Locals("auth_method", "apikey")

		return c.Next()
	}
}
