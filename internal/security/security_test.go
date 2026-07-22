package security

import (
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/orvixemail/orvix/internal/models"
	"gorm.io/gorm"
)

func setupSecurityDB(t *testing.T) *gorm.DB {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}
	if err := db.AutoMigrate(&models.AuditLog{}, &models.APIKey{}); err != nil {
		t.Fatalf("failed to migrate: %v", err)
	}
	return db
}

func TestAuditServiceLog(t *testing.T) {
	db := setupSecurityDB(t)
	svc := NewAuditService(db)

	userID := uint(1)
	err := svc.Log(&userID, nil, "test.action", "test_resource", "123", "127.0.0.1", `{"key":"value"}`)
	if err != nil {
		t.Fatalf("AuditService.Log failed: %v", err)
	}

	logs, err := svc.Query(nil, 10, 0)
	if err != nil {
		t.Fatalf("AuditService.Query failed: %v", err)
	}
	if len(logs) != 1 {
		t.Errorf("expected 1 log entry, got %d", len(logs))
	}
	if logs[0].Action != "test.action" {
		t.Errorf("expected action=test.action, got %s", logs[0].Action)
	}
	if logs[0].Resource != "test_resource" {
		t.Errorf("expected resource=test_resource, got %s", logs[0].Resource)
	}
}

func TestAuditServiceQueryWithFilters(t *testing.T) {
	db := setupSecurityDB(t)
	svc := NewAuditService(db)

	userID := uint(1)
	svc.Log(&userID, nil, "user.create", "user", "1", "10.0.0.1", `{"email":"a@test.com"}`)
	svc.Log(&userID, nil, "domain.create", "domain", "2", "10.0.0.1", `{"name":"test.com"}`)

	logs, err := svc.Query(map[string]interface{}{"action": "user.create"}, 10, 0)
	if err != nil {
		t.Fatalf("Query with filter failed: %v", err)
	}
	if len(logs) != 1 {
		t.Errorf("expected 1 user.create log, got %d", len(logs))
	}
}

func TestAPIKeyGenerate(t *testing.T) {
	db := setupSecurityDB(t)
	svc := NewAPIKeyService(db)

	key, err := svc.Generate(1, "Test Key", []string{"read", "write"})
	if err != nil {
		t.Fatalf("APIKeyService.Generate failed: %v", err)
	}
	if key == "" {
		t.Error("generated key is empty")
	}
}

func TestAPIKeyValidate(t *testing.T) {
	db := setupSecurityDB(t)
	svc := NewAPIKeyService(db)

	key, err := svc.Generate(1, "Test Key", []string{"read"})
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	entry, err := svc.Validate(key)
	if err != nil {
		t.Fatalf("Validate failed: %v", err)
	}
	if entry.Name != "Test Key" {
		t.Errorf("expected name=Test Key, got %s", entry.Name)
	}
	if !entry.Active {
		t.Error("key should be active")
	}

	_, err = svc.Validate("invalid-key")
	if err == nil {
		t.Error("Validate should fail for invalid key")
	}
}

func TestAPIKeyRevoke(t *testing.T) {
	db := setupSecurityDB(t)
	svc := NewAPIKeyService(db)

	key, err := svc.Generate(1, "Revocable Key", []string{"read"})
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	entry, _ := svc.Validate(key)
	err = svc.Revoke(entry.ID)
	if err != nil {
		t.Fatalf("Revoke failed: %v", err)
	}

	_, err = svc.Validate(key)
	if err == nil {
		t.Error("Validate should fail for revoked key")
	}
}
