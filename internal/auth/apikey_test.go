package auth

import (
	"crypto/sha256"
	"fmt"
	"testing"

	"go.uber.org/zap"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func newTestLogger(t *testing.T) *zap.Logger {
	t.Helper()
	cfg := zap.NewDevelopmentConfig()
	cfg.OutputPaths = []string{"/dev/null"}
	l, _ := cfg.Build()
	return l
}

func testAPIKeyManager(t *testing.T) *APIKeyManager {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	db.Exec(`CREATE TABLE api_keys (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		created_at DATETIME NOT NULL,
		updated_at DATETIME NOT NULL,
		deleted_at DATETIME,
		name TEXT NOT NULL,
		user_id INTEGER NOT NULL,
		tenant_id INTEGER NOT NULL DEFAULT 0,
		role TEXT NOT NULL DEFAULT 'user',
		key_hash TEXT NOT NULL UNIQUE,
		key_prefix TEXT NOT NULL DEFAULT '',
		scopes TEXT NOT NULL DEFAULT '',
		enabled INTEGER NOT NULL DEFAULT 1,
		last_used DATETIME,
		expires_at DATETIME
	)`)
	return NewAPIKeyManager(db, newTestLogger(t))
}

func TestAPICreateAndAuthenticate(t *testing.T) {
	mgr := testAPIKeyManager(t)
	fullKey, record, err := mgr.Generate("test-key", 1, 1, "user", []string{"domains.read"}, 30)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if fullKey == "" {
		t.Fatal("expected non-empty secret")
	}
	if record.Name != "test-key" {
		t.Errorf("expected 'test-key', got '%s'", record.Name)
	}
	validated, err := mgr.Validate(fullKey)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if validated.ID != record.ID {
		t.Error("wrong ID")
	}
}

func TestAPIRotateOldKeyFails(t *testing.T) {
	mgr := testAPIKeyManager(t)
	fullKey, record, err := mgr.Generate("rotate-test", 1, 1, "user", []string{"mailboxes.read"}, 30)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if _, err := mgr.Validate(fullKey); err != nil {
		t.Fatalf("old key should work before rotation: %v", err)
	}
	newKey, newRecord, err := mgr.RotateByID(record.ID, 1, 1, "user", []string{"mailboxes.read"}, 30)
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if newKey == "" {
		t.Fatal("expected new key")
	}
	if newRecord.ID == record.ID {
		t.Error("new key should have different ID")
	}
	if _, err := mgr.Validate(fullKey); err == nil {
		t.Fatal("old key should fail after rotation")
	}
	if _, err := mgr.Validate(newKey); err != nil {
		t.Fatalf("new key should work: %v", err)
	}
}

func TestAPIRotateRollbackOnInsertFailure(t *testing.T) {
	mgr := testAPIKeyManager(t)
	fullKey, oldRecord, err := mgr.Generate("rollback-test", 1, 1, "user", []string{"domains.read"}, 30)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	// Create a blocker key with a known hash to force a duplicate on insert.
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte("orv_blocker")))
	mgr.db.Exec("INSERT INTO api_keys (name, user_id, tenant_id, role, key_hash, key_prefix) VALUES ('blocker', 1, 1, 'user', ?, 'orv_block')", hash)

	_, _, err = mgr.RotateByID(oldRecord.ID, 1, 1, "user", []string{"domains.read"}, 30)
	if err == nil {
		t.Fatal("rotation should fail when replacement insert fails")
	}
	if _, err := mgr.Validate(fullKey); err != nil {
		t.Fatalf("old key should remain valid after failed rotate: %v", err)
	}
}

func TestAPIRotateCrossUserDenied(t *testing.T) {
	mgr := testAPIKeyManager(t)
	_, record, err := mgr.Generate("user-1-key", 1, 1, "user", nil, 30)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	_, _, err = mgr.RotateByID(record.ID, 2, 1, "user", nil, 30)
	if err == nil {
		t.Fatal("cross-user rotation should be denied")
	}
}

func TestAPIRotateCrossTenantDenied(t *testing.T) {
	mgr := testAPIKeyManager(t)
	_, record, err := mgr.Generate("tenant-1-key", 1, 1, "user", nil, 30)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	_, _, err = mgr.RotateByID(record.ID, 1, 2, "user", nil, 30)
	if err == nil {
		t.Fatal("cross-tenant rotation should be denied")
	}
}

func TestAPIRotateUnrelatedSameNameKey(t *testing.T) {
	mgr := testAPIKeyManager(t)
	key1, rec1, err := mgr.Generate("shared-name", 1, 1, "user", nil, 30)
	if err != nil {
		t.Fatalf("gen user1: %v", err)
	}
	key2, _, err := mgr.Generate("shared-name", 2, 2, "user", nil, 30)
	if err != nil {
		t.Fatalf("gen user2: %v", err)
	}
	_, _, err = mgr.RotateByID(rec1.ID, 1, 1, "user", nil, 30)
	if err != nil {
		t.Fatalf("rotate user1 key: %v", err)
	}
	if _, err := mgr.Validate(key1); err == nil {
		t.Error("rotated key1 should fail")
	}
	if _, err := mgr.Validate(key2); err != nil {
		t.Errorf("unrelated key2 should still work: %v", err)
	}
}
