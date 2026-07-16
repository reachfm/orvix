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

// APIKeyRecord represents a stored API key with tenant binding and scopes.
type APIKeyRecord struct {
	ID        uint       `gorm:"primaryKey" json:"id"`
	Name      string     `gorm:"not null" json:"name"`
	KeyPrefix string     `gorm:"uniqueIndex;not null;size:8" json:"key_prefix"`
	KeyHash   string     `gorm:"uniqueIndex;not null" json:"-"`
	UserID    uint       `gorm:"index;not null" json:"user_id"`
	TenantID  uint       `gorm:"not null;default:0" json:"tenant_id"`
	Role      string     `gorm:"not null;default:'user'" json:"role"`
	Scopes    string     `gorm:"type:text" json:"scopes,omitempty"`
	Enabled   bool       `gorm:"column:active;not null;default:true" json:"enabled"`
	LastUsed  *time.Time `gorm:"column:last_used_at" json:"last_used,omitempty"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
}

// APIKeyRequest is used for creating or rotating API keys.
type APIKeyRequest struct {
	Name    string   `json:"name"`
	Scopes  []string `json:"scopes,omitempty"`
	TTLDays int      `json:"ttl_days,omitempty"`
}

// APIKeyManager handles API key lifecycle.
type APIKeyManager struct {
	db     *gorm.DB
	logger *zap.Logger
}

// NewAPIKeyManager creates a new API key manager.
func NewAPIKeyManager(db *gorm.DB, logger *zap.Logger) *APIKeyManager {
	// RC2 FIX: Skip AutoMigrate - tables are created with raw SQL
	return &APIKeyManager{
		db:     db,
		logger: logger,
	}
}

// Generate creates a new API key and returns the full key (shown once).
func (m *APIKeyManager) Generate(name string, userID, tenantID uint, role string, scopes []string, ttlDays int) (string, *APIKeyRecord, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", nil, fmt.Errorf("failed to generate API key: %w", err)
	}

	fullKey := "orv_" + hex.EncodeToString(b)
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(fullKey)))
	prefix := fullKey[:11]

	scopesStr := ""
	if len(scopes) > 0 {
		for i, s := range scopes {
			if i > 0 {
				scopesStr += ","
			}
			scopesStr += s
		}
	}

	var expiresAt *time.Time
	if ttlDays > 0 {
		t := time.Now().AddDate(0, 0, ttlDays)
		expiresAt = &t
	}

	record := &APIKeyRecord{
		Name:      name,
		KeyPrefix: prefix,
		KeyHash:   hash,
		UserID:    userID,
		TenantID:  tenantID,
		Role:      role,
		Scopes:    scopesStr,
		Enabled:   true,
		ExpiresAt: expiresAt,
	}

	if err := m.db.Create(record).Error; err != nil {
		return "", nil, fmt.Errorf("failed to store API key: %w", err)
	}

	m.logger.Info("API key generated", zap.String("name", name), zap.String("prefix", prefix), zap.Uint("tenant", tenantID))
	return fullKey, record, nil
}

// Validate checks if an API key is valid and returns the record.
func (m *APIKeyManager) Validate(key string) (*APIKeyRecord, error) {
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(key)))

	var record APIKeyRecord
	if err := m.db.Where("key_hash = ? AND active = ?", hash, true).First(&record).Error; err != nil {
		return nil, fmt.Errorf("invalid API key")
	}

	if record.ExpiresAt != nil && time.Now().After(*record.ExpiresAt) {
		return nil, fmt.Errorf("API key expired")
	}

	now := time.Now()
	m.db.Model(&record).Update("last_used", now)
	return &record, nil
}

// Rotate invalidates the old key and generates a new one. Deprecated:
// use RotateByID which is ID-scoped and transactional.
func (m *APIKeyManager) Rotate(name string, userID, tenantID uint, role string, scopes []string, ttlDays int) (string, *APIKeyRecord, error) {
	m.db.Where("name = ? AND user_id = ?", name, userID).Delete(&APIKeyRecord{})
	return m.Generate(name, userID, tenantID, role, scopes, ttlDays)
}

// RotateByID atomically rotates an API key by ID. Within a single
// GORM transaction: loads the old key (scoped by id+user+tenant),
// validates it is enabled, generates a new key, inserts the replacement,
// disables the old key, and commits. Rollback on any error.
func (m *APIKeyManager) RotateByID(oldID, userID, tenantID uint, role string, scopes []string, ttlDays int) (string, *APIKeyRecord, error) {
	var fullKey string
	var newRecord *APIKeyRecord

	err := m.db.Transaction(func(tx *gorm.DB) error {
		// Load old key scoped by ID + user.
		var old APIKeyRecord
		if err := tx.Where("id = ? AND user_id = ? AND tenant_id = ?", oldID, userID, tenantID).First(&old).Error; err != nil {
			return fmt.Errorf("API key not found: %w", err)
		}
		if !old.Enabled {
			return fmt.Errorf("API key is already disabled")
		}

		// Generate new key.
		b := make([]byte, 32)
		if _, err := rand.Read(b); err != nil {
			return fmt.Errorf("failed to generate API key: %w", err)
		}
		fullKey = "orv_" + hex.EncodeToString(b)
		hash := fmt.Sprintf("%x", sha256.Sum256([]byte(fullKey)))
		prefix := fullKey[:11]

		scopesStr := ""
		if len(scopes) > 0 {
			for i, s := range scopes {
				if i > 0 {
					scopesStr += ","
				}
				scopesStr += s
			}
		}

		var expiresAt *time.Time
		if ttlDays > 0 {
			t := time.Now().AddDate(0, 0, ttlDays)
			expiresAt = &t
		}

		newRecord = &APIKeyRecord{
			Name:      old.Name,
			KeyPrefix: prefix,
			KeyHash:   hash,
			UserID:    userID,
			TenantID:  tenantID,
			Role:      role,
			Scopes:    scopesStr,
			Enabled:   true,
			ExpiresAt: expiresAt,
		}

		if err := tx.Create(newRecord).Error; err != nil {
			return fmt.Errorf("failed to insert replacement key: %w", err)
		}

		// Disable old key.
		if err := tx.Model(&APIKeyRecord{}).Where("id = ?", oldID).Update("active", false).Error; err != nil {
			return fmt.Errorf("failed to revoke old key: %w", err)
		}

		return nil
	})

	if err != nil {
		return "", nil, err
	}
	m.logger.Info("API key rotated", zap.String("name", newRecord.Name), zap.String("prefix", newRecord.KeyPrefix), zap.Uint("id", oldID))
	return fullKey, newRecord, nil
}

// Revoke disables an API key by ID (legacy — use RevokeScoped to enforce ownership).
func (m *APIKeyManager) Revoke(id uint) error {
	result := m.db.Model(&APIKeyRecord{}).Where("id = ?", id).Update("enabled", false)
	if result.RowsAffected == 0 {
		return fmt.Errorf("API key not found")
	}
	return nil
}

// RevokeScoped disables an API key by ID, scoped to the owning user.
func (m *APIKeyManager) RevokeScoped(id, userID uint) error {
	result := m.db.Model(&APIKeyRecord{}).Where("id = ? AND user_id = ?", id, userID).Update("active", false)
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
