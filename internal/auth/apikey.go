package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/dbdialect"
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

// dialect resolves the dialect-aware SQL helpers for the manager's DB. The
// whole manager uses raw database/sql (not GORM) because the repository's
// custom modernc SQLite dialector does not give GORM writes and raw reads a
// shared view, and its transaction support is unusable ("invalid transaction").
// Raw SQL behaves identically on SQLite and PostgreSQL.
func (m *APIKeyManager) dialect() (*sql.DB, *dbdialect.Info, error) {
	sqlDB, err := m.db.DB()
	if err != nil {
		return nil, nil, fmt.Errorf("api key: db: %w", err)
	}
	d, err := dbdialect.Detect(sqlDB)
	if err != nil {
		d = dbdialect.FromDriver("sqlite")
	}
	return sqlDB, d, nil
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
	scopesStr := strings.Join(scopes, ",")
	now := time.Now().UTC()
	var expiresAt *time.Time
	if ttlDays > 0 {
		t := now.AddDate(0, 0, ttlDays)
		expiresAt = &t
	}

	sqlDB, d, err := m.dialect()
	if err != nil {
		return "", nil, err
	}
	cols := "created_at, updated_at, name, user_id, tenant_id, role, key_hash, key_prefix, scopes, active, expires_at"
	vals := d.Placeholder(1) + ", " + d.Placeholder(2) + ", " + d.Placeholder(3) + ", " +
		d.Placeholder(4) + ", " + d.Placeholder(5) + ", " + d.Placeholder(6) + ", " +
		d.Placeholder(7) + ", " + d.Placeholder(8) + ", " + d.Placeholder(9) + ", " +
		d.TrueLiteral() + ", " + d.Placeholder(10)
	args := []any{now, now, name, userID, tenantID, role, hash, prefix, scopesStr, expiresAt}

	var id uint
	if d.IsPostgres() {
		if err := sqlDB.QueryRow("INSERT INTO api_keys ("+cols+") VALUES ("+vals+") RETURNING id", args...).Scan(&id); err != nil {
			return "", nil, fmt.Errorf("failed to store API key: %w", err)
		}
	} else {
		res, err := sqlDB.Exec("INSERT INTO api_keys ("+cols+") VALUES ("+vals+")", args...)
		if err != nil {
			return "", nil, fmt.Errorf("failed to store API key: %w", err)
		}
		lastID, err := res.LastInsertId()
		if err != nil {
			return "", nil, fmt.Errorf("failed to read API key id: %w", err)
		}
		id = uint(lastID)
	}

	record := &APIKeyRecord{
		ID: id, Name: name, KeyPrefix: prefix, KeyHash: hash, UserID: userID,
		TenantID: tenantID, Role: role, Scopes: scopesStr, Enabled: true,
		ExpiresAt: expiresAt, CreatedAt: now,
	}
	m.logger.Info("API key generated", zap.String("name", name), zap.String("prefix", prefix), zap.Uint("tenant", tenantID))
	return fullKey, record, nil
}

// Validate checks if an API key is valid and returns the record.
func (m *APIKeyManager) Validate(key string) (*APIKeyRecord, error) {
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(key)))

	sqlDB, d, err := m.dialect()
	if err != nil {
		return nil, err
	}
	sel := "SELECT id, name, user_id, tenant_id, role, key_prefix, scopes, expires_at FROM api_keys WHERE key_hash = " +
		d.Placeholder(1) + " AND active = " + d.TrueLiteral() + " AND deleted_at IS NULL"
	var r APIKeyRecord
	if err := sqlDB.QueryRow(sel, hash).Scan(&r.ID, &r.Name, &r.UserID, &r.TenantID, &r.Role, &r.KeyPrefix, &r.Scopes, &r.ExpiresAt); err != nil {
		return nil, fmt.Errorf("invalid API key")
	}
	if r.ExpiresAt != nil && time.Now().After(*r.ExpiresAt) {
		return nil, fmt.Errorf("API key expired")
	}
	r.KeyHash = hash
	r.Enabled = true

	// Best-effort last-used bookkeeping; never fail auth on it.
	_, _ = sqlDB.Exec("UPDATE api_keys SET last_used_at = "+d.Placeholder(1)+" WHERE id = "+d.Placeholder(2), time.Now().UTC(), r.ID)
	return &r, nil
}

// RotateByID atomically rotates an API key by ID using a raw database/sql
// transaction (GORM's transaction helper silently fails — "invalid
// transaction" — under the repository's custom modernc SQLite dialector, so it
// cannot be used here). Within one transaction it: loads the old key scoped by
// id+user+tenant (locking the row FOR UPDATE on PostgreSQL), verifies it is
// active, inserts the replacement, disables ONLY that old key by id, and
// commits. Any error rolls the whole thing back, leaving the old key valid.
// Works identically on SQLite and PostgreSQL via dialect-aware SQL.
func (m *APIKeyManager) RotateByID(oldID, userID, tenantID uint, role string, scopes []string, ttlDays int) (string, *APIKeyRecord, error) {
	sqlDB, err := m.db.DB()
	if err != nil {
		return "", nil, fmt.Errorf("api key rotate: db: %w", err)
	}
	d, err := dbdialect.Detect(sqlDB)
	if err != nil {
		d = dbdialect.FromDriver("sqlite")
	}

	tx, err := sqlDB.Begin()
	if err != nil {
		return "", nil, fmt.Errorf("api key rotate: begin: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	// 1. Load the exact old key, locking the row on PostgreSQL so two
	// concurrent rotations of the same key cannot each commit a replacement.
	sel := "SELECT name, active FROM api_keys WHERE id = " + d.Placeholder(1) +
		" AND user_id = " + d.Placeholder(2) + " AND tenant_id = " + d.Placeholder(3) +
		" AND deleted_at IS NULL"
	if d.IsPostgres() {
		sel += " FOR UPDATE"
	}
	var oldName string
	var active bool
	if err := tx.QueryRow(sel, oldID, userID, tenantID).Scan(&oldName, &active); err != nil {
		if err == sql.ErrNoRows {
			return "", nil, fmt.Errorf("API key not found")
		}
		return "", nil, fmt.Errorf("api key rotate: load: %w", err)
	}
	if !active {
		return "", nil, fmt.Errorf("API key is already disabled")
	}

	// 2. Generate the replacement secret / hash / prefix.
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", nil, fmt.Errorf("api key rotate: generate: %w", err)
	}
	fullKey := "orv_" + hex.EncodeToString(b)
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(fullKey)))
	prefix := fullKey[:11]
	scopesStr := strings.Join(scopes, ",")
	now := time.Now().UTC()
	var expiresAt *time.Time
	if ttlDays > 0 {
		t := now.AddDate(0, 0, ttlDays)
		expiresAt = &t
	}

	// 3. Insert the replacement (active written as a dialect literal so the
	// boolean/integer column type is correct on both engines).
	cols := "created_at, updated_at, name, user_id, tenant_id, role, key_hash, key_prefix, scopes, active, expires_at"
	vals := d.Placeholder(1) + ", " + d.Placeholder(2) + ", " + d.Placeholder(3) + ", " +
		d.Placeholder(4) + ", " + d.Placeholder(5) + ", " + d.Placeholder(6) + ", " +
		d.Placeholder(7) + ", " + d.Placeholder(8) + ", " + d.Placeholder(9) + ", " +
		d.TrueLiteral() + ", " + d.Placeholder(10)
	insArgs := []any{now, now, oldName, userID, tenantID, role, hash, prefix, scopesStr, expiresAt}

	var newID uint
	if d.IsPostgres() {
		ins := "INSERT INTO api_keys (" + cols + ") VALUES (" + vals + ") RETURNING id"
		if err := tx.QueryRow(ins, insArgs...).Scan(&newID); err != nil {
			return "", nil, fmt.Errorf("api key rotate: insert replacement: %w", err)
		}
	} else {
		ins := "INSERT INTO api_keys (" + cols + ") VALUES (" + vals + ")"
		res, err := tx.Exec(ins, insArgs...)
		if err != nil {
			return "", nil, fmt.Errorf("api key rotate: insert replacement: %w", err)
		}
		lastID, err := res.LastInsertId()
		if err != nil {
			return "", nil, fmt.Errorf("api key rotate: replacement id: %w", err)
		}
		newID = uint(lastID)
	}

	// 4. Disable ONLY the selected old key by id.
	upd := "UPDATE api_keys SET active = " + d.FalseLiteral() + ", updated_at = " +
		d.Placeholder(1) + " WHERE id = " + d.Placeholder(2)
	res, err := tx.Exec(upd, now, oldID)
	if err != nil {
		return "", nil, fmt.Errorf("api key rotate: disable old: %w", err)
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return "", nil, fmt.Errorf("api key rotate: old key vanished before disable")
	}

	// 5. Commit atomically.
	if err := tx.Commit(); err != nil {
		return "", nil, fmt.Errorf("api key rotate: commit: %w", err)
	}
	committed = true

	newRecord := &APIKeyRecord{
		ID: newID, Name: oldName, KeyPrefix: prefix, KeyHash: hash, UserID: userID,
		TenantID: tenantID, Role: role, Scopes: scopesStr, Enabled: true,
		ExpiresAt: expiresAt, CreatedAt: now,
	}
	m.logger.Info("API key rotated", zap.String("name", newRecord.Name), zap.String("prefix", newRecord.KeyPrefix), zap.Uint("old_id", oldID), zap.Uint("new_id", newID))
	return fullKey, newRecord, nil
}

// RevokeScoped disables an API key by ID, scoped to the owning user.
func (m *APIKeyManager) RevokeScoped(id, userID uint) error {
	sqlDB, d, err := m.dialect()
	if err != nil {
		return err
	}
	res, err := sqlDB.Exec("UPDATE api_keys SET active = "+d.FalseLiteral()+", updated_at = "+d.Placeholder(1)+
		" WHERE id = "+d.Placeholder(2)+" AND user_id = "+d.Placeholder(3)+" AND deleted_at IS NULL",
		time.Now().UTC(), id, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("API key not found")
	}
	return nil
}

// List returns all API keys for a user (never the full secret).
func (m *APIKeyManager) List(userID uint) ([]APIKeyRecord, error) {
	sqlDB, d, err := m.dialect()
	if err != nil {
		return nil, err
	}
	rows, err := sqlDB.Query("SELECT id, name, key_prefix, scopes, active, last_used_at, expires_at, created_at FROM api_keys WHERE user_id = "+
		d.Placeholder(1)+" AND deleted_at IS NULL ORDER BY id", userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var keys []APIKeyRecord
	for rows.Next() {
		var r APIKeyRecord
		var active bool
		if err := rows.Scan(&r.ID, &r.Name, &r.KeyPrefix, &r.Scopes, &active, &r.LastUsed, &r.ExpiresAt, &r.CreatedAt); err != nil {
			return nil, err
		}
		r.Enabled = active
		r.UserID = userID
		keys = append(keys, r)
	}
	return keys, rows.Err()
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
