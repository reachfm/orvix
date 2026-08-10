package auth

import (
	"context"
	"crypto/sha256"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/orvix/orvix/internal/config"
	"github.com/orvix/orvix/internal/models"
	"go.uber.org/zap"
)

func sha256Hex(token string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(token)))
}

func newSessionMetadataTestAuth(t *testing.T) *Authenticator {
	t.Helper()
	logger := zap.NewNop()
	cfg := config.Defaults()
	dir := t.TempDir()
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = filepath.Join(dir, "sessmeta.db") + "?_loc=auto&_busy_timeout=5000&_txlock=immediate"
	db, err := config.NewDatabase(&cfg.Database, logger)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := models.MigrateAllRaw(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			sqlDB.Close()
		}
	})
	a, err := NewAuthenticator(&cfg.Auth, db, logger)
	if err != nil {
		t.Fatalf("new authenticator: %v", err)
	}
	return a
}

// TestGenerateOpaqueSession_PersistsRealIPAndUserAgent is the direct
// regression proof for "Session metadata is returned as Unknown despite
// available request data": before this fix, ip was hardcoded to "" and
// user_agent was never written at all, regardless of what was passed.
func TestGenerateOpaqueSession_PersistsRealIPAndUserAgent(t *testing.T) {
	a := newSessionMetadataTestAuth(t)
	token, err := a.GenerateOpaqueSession(7, "tenant_admin", "u@test.local", "203.0.113.42", "Mozilla/5.0 TestAgent")
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	tokenHash := sha256Hex(token)

	sqlDB, err := a.db.DB()
	if err != nil {
		t.Fatal(err)
	}
	var ip, ua string
	if err := sqlDB.QueryRow("SELECT ip, COALESCE(user_agent, '') FROM sessions WHERE token_hash = ?", tokenHash).Scan(&ip, &ua); err != nil {
		t.Fatalf("query session row: %v", err)
	}
	if ip != "203.0.113.42" {
		t.Fatalf("expected the real IP to be persisted, got %q", ip)
	}
	if ua != "Mozilla/5.0 TestAgent" {
		t.Fatalf("expected the real User-Agent to be persisted, got %q", ua)
	}
}

func TestGenerateRefreshToken_PersistsRealIPAndUserAgent(t *testing.T) {
	a := newSessionMetadataTestAuth(t)
	token, _, err := a.GenerateRefreshToken(7, "jti-1", "198.51.100.7", "curl/8.0")
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	tokenHash := sha256Hex(token)

	sqlDB, err := a.db.DB()
	if err != nil {
		t.Fatal(err)
	}
	var ip, ua string
	if err := sqlDB.QueryRow("SELECT ip, COALESCE(user_agent, '') FROM sessions WHERE token_hash = ?", tokenHash).Scan(&ip, &ua); err != nil {
		t.Fatalf("query session row: %v", err)
	}
	if ip != "198.51.100.7" {
		t.Fatalf("expected the real IP to be persisted, got %q", ip)
	}
	if ua != "curl/8.0" {
		t.Fatalf("expected the real User-Agent to be persisted, got %q", ua)
	}
}

// TestRefreshToken_PreservesDeviceMetadataAcrossRotation proves the
// rotated session keeps the metadata of the session it replaced, rather
// than reverting to empty on every refresh.
func TestRefreshToken_PreservesDeviceMetadataAcrossRotation(t *testing.T) {
	a := newSessionMetadataTestAuth(t)
	sqlDB, err := a.db.DB()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sqlDB.Exec("INSERT INTO users (id, email, password_hash, role, active, tenant_id, token_version, created_at, updated_at) VALUES (1, 'u@test.local', 'x', 'tenant_admin', 1, 1, 0, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)"); err != nil {
		t.Fatalf("seed user: %v", err)
	}

	refresh1, _, err := a.GenerateRefreshToken(1, "jti-orig", "203.0.113.9", "OriginalDevice/1.0")
	if err != nil {
		t.Fatalf("initial generate: %v", err)
	}

	_, refresh2, _, err := a.RefreshToken(context.Background(), refresh1)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	tokenHash := sha256Hex(refresh2)

	var ip, ua string
	if err := sqlDB.QueryRow("SELECT ip, COALESCE(user_agent, '') FROM sessions WHERE token_hash = ?", tokenHash).Scan(&ip, &ua); err != nil {
		t.Fatalf("query rotated session row: %v", err)
	}
	if ip != "203.0.113.9" || ua != "OriginalDevice/1.0" {
		t.Fatalf("expected device metadata to survive rotation, got ip=%q ua=%q", ip, ua)
	}
}
