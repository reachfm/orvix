package webhooks

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

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
