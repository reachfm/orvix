package webhooks

import (
	"context"
	"database/sql"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/orvix/orvix/internal/config"
	_ "modernc.org/sqlite"
)

func newWHTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "wh.db")+"?_txlock=immediate")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	return db
}

func TestWHSignVerify(t *testing.T) {
	secret := []byte("test-secret")
	body := []byte(`{"event":"test"}`)
	ts := time.Now().Unix()
	sig := Sign(secret, body, ts)
	if !Verify(secret, body, ts, sig, 5*time.Minute) {
		t.Fatal("valid signature should verify")
	}
	if Verify(secret, []byte(`{"event":"tampered"}`), ts, sig, 5*time.Minute) {
		t.Fatal("modified body should fail")
	}
	if Verify([]byte("wrong"), body, ts, sig, 5*time.Minute) {
		t.Fatal("wrong secret should fail")
	}
	oldTs := time.Now().Add(-10 * time.Minute).Unix()
	if Verify(secret, body, oldTs, Sign(secret, body, oldTs), 5*time.Minute) {
		t.Fatal("expired timestamp should fail")
	}
}

func TestWHDeliveryAndTenantIsolation(t *testing.T) {
	db := newWHTestDB(t)
	repo := NewRepository(db)
	ctx := context.Background()
	if err := repo.EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}
	svc := NewService(repo, nil)
	_ = repo.InsertSubscription(ctx, &Subscription{TenantID: 1, Scope: ScopeTenant, URL: "https://example.com/hook", Events: []string{"domain.created"}, Active: true})
	_ = repo.InsertSubscription(ctx, &Subscription{TenantID: 0, Scope: ScopePlatform, URL: "https://platform.com/hook", Events: []string{"domain.created"}, Active: true})
	eventID, err := svc.Dispatch(ctx, "domain.created", string(ScopeTenant), 1, []byte(`{}`))
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if eventID == "" {
		t.Fatal("expected event ID")
	}
	pending, _ := repo.PendingDeliveries(ctx, 10)
	if len(pending) != 1 {
		t.Fatalf("expected 1 delivery, got %d", len(pending))
	}
	sub, _ := repo.GetSubscription(ctx, pending[0].SubscriptionID)
	if sub.TenantID != 1 {
		t.Fatalf("tenant isolation failed: got tenant %d", sub.TenantID)
	}
}

func TestWHSubscriptionConcurrency(t *testing.T) {
	db := newWHTestDB(t)
	repo := NewRepository(db)
	ctx := context.Background()
	_ = repo.EnsureSchema(ctx)
	sub := &Subscription{TenantID: 1, Scope: ScopeTenant, URL: "https://example.com", Events: []string{"domain.created"}, Active: true}
	if err := repo.InsertSubscription(ctx, sub); err != nil {
		t.Fatal(err)
	}
	c1, _ := repo.GetSubscription(ctx, sub.ID)
	c2, _ := repo.GetSubscription(ctx, sub.ID)
	c1.Active = false
	if err := repo.UpdateSubscription(ctx, c1); err != nil {
		t.Fatalf("first update: %v", err)
	}
	c2.Active = false
	if err := repo.UpdateSubscription(ctx, c2); err == nil {
		t.Fatal("expected stale version error")
	}
}

func TestWHSecretEncryptedAndRotatable(t *testing.T) {
	oldKey := os.Getenv("ORVIX_ENCRYPTION_KEY")
	defer os.Setenv("ORVIX_ENCRYPTION_KEY", oldKey)
	os.Setenv("ORVIX_ENCRYPTION_KEY", "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff")
	db := newWHTestDB(t)
	repo := NewRepository(db)
	ctx := context.Background()
	if err := repo.EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}
	svc := NewService(repo, nil)
	sub, returned, err := svc.CreateSubscriptionWithSecret(ctx, 1, ScopeTenant, "https://example.com/hook", []string{"domain.created"}, []byte("original-secret"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if returned != hex.EncodeToString([]byte("original-secret")) {
		t.Fatal("creation did not return the one-time secret")
	}
	stored, err := repo.GetSubscription(ctx, sub.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.SecretEncrypted == "original-secret" || stored.SecretEncrypted == returned {
		t.Fatal("secret was stored in plaintext")
	}
	plain, err := config.Decrypt(stored.SecretEncrypted)
	if err != nil || string(plain) != "original-secret" {
		t.Fatalf("stored secret does not decrypt to original: %v", err)
	}
	rotated, newSecret, err := svc.RotateSecret(ctx, sub.ID)
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if newSecret == returned || rotated.SecretEncrypted == stored.SecretEncrypted {
		t.Fatal("rotation did not replace secret")
	}
}

func TestWHRetryAndReactivate(t *testing.T) {
	db := newWHTestDB(t)
	repo := NewRepository(db)
	ctx := context.Background()
	if err := repo.EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}
	svc := NewService(repo, nil)
	sub := &Subscription{TenantID: 1, Scope: ScopeTenant, URL: "https://example.com", Events: []string{"domain.created"}, Active: true, Suspended: true}
	if err := repo.InsertSubscription(ctx, sub); err != nil {
		t.Fatal(err)
	}
	d := &Delivery{EventID: "evt_test", SubscriptionID: sub.ID, Status: "suspended"}
	if err := repo.InsertDelivery(ctx, d); err != nil {
		t.Fatal(err)
	}
	if err := svc.RetryDelivery(ctx, d.ID); err != nil {
		t.Fatalf("retry: %v", err)
	}
	got, _ := repo.GetDelivery(ctx, d.ID)
	if got.Status != "suspended" {
		t.Fatalf("manual replay mutated original history: got %s", got.Status)
	}
	history, err := repo.DeliveryHistory(ctx, sub.ID, 10)
	if err != nil || len(history) != 2 || history[0].Status != "pending" || history[0].ReplayOf == nil || *history[0].ReplayOf != d.ID {
		t.Fatalf("expected a new pending replay referencing original: history=%+v err=%v", history, err)
	}
	if _, err := svc.Reactivate(ctx, sub.ID); err != nil {
		t.Fatalf("reactivate: %v", err)
	}
	gotSub, _ := repo.GetSubscription(ctx, sub.ID)
	if !gotSub.Active || gotSub.Suspended {
		t.Fatal("subscription was not reactivated")
	}
}
