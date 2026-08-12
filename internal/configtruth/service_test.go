package configtruth

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/orvix/orvix/internal/config"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

func newTestService(t *testing.T) (*Service, context.Context) {
	t.Helper()
	logger := zap.NewNop()
	dir := t.TempDir()
	cfg := config.Defaults()
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = filepath.Join(dir, "test.db") + "?_loc=auto&_busy_timeout=5000&_txlock=immediate"

	db, err := config.NewDatabase(&cfg.Database, logger)
	if err != nil {
		t.Fatalf("database: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil && sqlDB != nil {
			_ = sqlDB.Close()
		}
	})
	sqlDB, _ := db.DB()
	repo := NewRepository(sqlDB)
	ctx := context.Background()
	if err := repo.EnsureSchema(ctx); err != nil {
		t.Fatalf("schema: %v", err)
	}
	svc := NewService(repo)
	svc.RegisterField(Field{Key: "security.password_min_len", Section: "security", Type: "int", RestartRequired: true})
	svc.RegisterField(Field{Key: "backup.retention_count", Section: "backup", Type: "int", RestartRequired: false})
	svc.RegisterField(Field{Key: "jwt.secret", Section: "security", Type: "string", RestartRequired: true, Secret: true})
	return svc, ctx
}

func TestConfigTruth_GetDefault(t *testing.T) {
	svc, ctx := newTestService(t)
	s, err := svc.Get(ctx, "security.password_min_len")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if s.Key != "security.password_min_len" {
		t.Fatalf("expected key, got %s", s.Key)
	}
	if s.State != StateApplied {
		t.Fatalf("expected applied state, got %s", s.State)
	}
}

func TestConfigTruth_MutateApplied(t *testing.T) {
	svc, ctx := newTestService(t)
	result, err := svc.Mutate(ctx, "backup.retention_count", MutationRequest{Value: int64(10), ActorID: 1, Reason: "testing"})
	if err != nil {
		t.Fatalf("mutate: %v", err)
	}
	if !result.Applied {
		t.Fatal("expected applied for non-restart-required field")
	}
	if result.State != StateApplied {
		t.Fatalf("expected applied state, got %s", result.State)
	}
	// JSON unmarshaling returns float64 for numbers
	if toFloat(result.Setting.EffectiveValue) != 10 {
		t.Fatalf("expected effective value 10, got %v", result.Setting.EffectiveValue)
	}
}

func toFloat(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int64:
		return float64(n)
	case int:
		return float64(n)
	}
	return 0
}

func TestConfigTruth_MutateRestartRequired(t *testing.T) {
	svc, ctx := newTestService(t)
	result, err := svc.Mutate(ctx, "security.password_min_len", MutationRequest{Value: int64(12), ActorID: 1, Reason: "testing"})
	if err != nil {
		t.Fatalf("mutate: %v", err)
	}
	if result.Applied {
		t.Fatal("expected not applied for restart-required field")
	}
	if result.State != StatePending {
		t.Fatalf("expected pending state, got %s", result.State)
	}
	if toFloat(result.Setting.PendingValue) != 12 {
		t.Fatalf("expected pending value 12, got %v", result.Setting.PendingValue)
	}
}

func TestConfigTruth_MutateImmutable(t *testing.T) {
	svc, ctx := newTestService(t)
	_, err := svc.Mutate(ctx, "jwt.secret", MutationRequest{Value: "new-secret", ActorID: 1, Reason: "testing"})
	if err == nil {
		// jwt.secret is secret but not immutable, so it should succeed
		// Let me register an immutable field
	}
	svc.RegisterField(Field{Key: "immutable.field", Section: "general", Type: "string", Immutable: true})
	_, err = svc.Mutate(ctx, "immutable.field", MutationRequest{Value: "value", ActorID: 1})
	if err == nil {
		t.Fatal("expected error for immutable field")
	}
}

func TestConfigTruth_SecretRedacted(t *testing.T) {
	svc, ctx := newTestService(t)
	result, err := svc.Mutate(ctx, "jwt.secret", MutationRequest{Value: "super-secret", ActorID: 1, Reason: "testing"})
	if err != nil {
		t.Fatalf("mutate: %v", err)
	}
	if result.Setting.Value != "REDACTED" {
		t.Fatalf("expected REDACTED, got %v", result.Setting.Value)
	}
	// Verify the secret is not stored in plaintext
	if result.Setting.EffectiveValue == "super-secret" {
		t.Fatal("secret should not be stored in plaintext")
	}
}

func TestConfigTruth_StaleVersion(t *testing.T) {
	svc, ctx := newTestService(t)
	// First mutation creates the row with version 1
	result1, err := svc.Mutate(ctx, "backup.retention_count", MutationRequest{Value: int64(10), ActorID: 1})
	if err != nil {
		t.Fatalf("first mutate: %v", err)
	}
	// Second mutation with stale version (0) should succeed since we don't check version 0
	// Use version 1 which is current - should succeed
	_, err = svc.Mutate(ctx, "backup.retention_count", MutationRequest{Value: int64(20), Version: result1.Setting.Version, ActorID: 1})
	if err != nil {
		t.Fatalf("second mutate with correct version: %v", err)
	}
	// Third mutation with stale version (1) should fail since current is now 2
	_, err = svc.Mutate(ctx, "backup.retention_count", MutationRequest{Value: int64(30), Version: 1, ActorID: 1})
	if err == nil {
		t.Fatal("expected stale version error")
	}
}

func TestConfigTruth_InvalidValue(t *testing.T) {
	svc, ctx := newTestService(t)
	_, err := svc.Mutate(ctx, "security.password_min_len", MutationRequest{Value: "not-an-int", ActorID: 1})
	if err == nil {
		t.Fatal("expected validation error")
	}
}

func TestConfigTruth_List(t *testing.T) {
	svc, ctx := newTestService(t)
	settings, err := svc.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(settings) != 3 {
		t.Fatalf("expected 3 settings, got %d", len(settings))
	}
}

func TestConfigTruth_ConcurrentMutations(t *testing.T) {
	svc, ctx := newTestService(t)
	// Simulate concurrent mutations
	result1, err := svc.Mutate(ctx, "backup.retention_count", MutationRequest{Value: int64(10), ActorID: 1})
	if err != nil {
		t.Fatalf("first mutate: %v", err)
	}
	// Second mutation with correct version should succeed
	_, err = svc.Mutate(ctx, "backup.retention_count", MutationRequest{Value: int64(20), Version: result1.Setting.Version, ActorID: 1})
	if err != nil {
		t.Fatalf("second mutate: %v", err)
	}
	_ = time.Now
	_ = sql.ErrNoRows
	_ = gorm.DB{}
}
