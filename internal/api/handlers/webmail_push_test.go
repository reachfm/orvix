package handlers

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/orvix/orvix/internal/coremail/push"
	_ "modernc.org/sqlite"

	"path/filepath"
)

func TestPushSubscriptionCreateAndList(t *testing.T) {
	db := setupPushDB(t)
	defer db.Close()
	repo := push.NewSubscriptionSQLRepo(db)
	ctx := context.Background()

	sub := &push.PushSubscription{
		MailboxID: 1, Endpoint: "https://fcm.googleapis.com/fcm/send/test123",
		P256DHKey: "test-key", AuthKey: "test-auth",
	}
	if err := repo.Create(ctx, sub); err != nil {
		t.Fatalf("create: %v", err)
	}
	if sub.ID == 0 {
		t.Fatal("expected non-zero ID")
	}

	disabled := false
	subs, err := repo.ListByMailbox(ctx, 1, &push.PushSubscriptionFilter{Disabled: &disabled})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(subs) != 1 {
		t.Fatalf("expected 1 sub, got %d", len(subs))
	}

	found, err := repo.GetByEndpoint(ctx, sub.Endpoint)
	if err != nil || found == nil {
		t.Fatalf("get by endpoint: %v", err)
	}
	if found.MailboxID != 1 {
		t.Errorf("mailbox mismatch: %d", found.MailboxID)
	}
}

func TestPushSubscriptionDisable(t *testing.T) {
	db := setupPushDB(t)
	defer db.Close()
	repo := push.NewSubscriptionSQLRepo(db)
	ctx := context.Background()

	repo.Create(ctx, &push.PushSubscription{
		MailboxID: 1, Endpoint: "https://push.example.com/test", P256DHKey: "k", AuthKey: "a",
	})

	if err := repo.Disable(ctx, 1); err != nil {
		t.Fatalf("disable: %v", err)
	}
	sub, err := repo.GetByEndpoint(ctx, "https://push.example.com/test")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if sub.DisabledAt == nil {
		t.Fatal("expected disabled_at to be set")
	}

	disabled := false
	active, _ := repo.ListByMailbox(ctx, 1, &push.PushSubscriptionFilter{Disabled: &disabled})
	if len(active) != 0 {
		t.Fatalf("expected 0 active subs, got %d", len(active))
	}
}

func TestPushSubscriptionCleanupExpired(t *testing.T) {
	db := setupPushDB(t)
	defer db.Close()
	repo := push.NewSubscriptionSQLRepo(db)
	ctx := context.Background()

	repo.Create(ctx, &push.PushSubscription{
		MailboxID: 1, Endpoint: "ep1", P256DHKey: "k1", AuthKey: "a1",
	})
	repo.Create(ctx, &push.PushSubscription{
		MailboxID: 1, Endpoint: "ep2", P256DHKey: "k2", AuthKey: "a2",
	})

	repo.Disable(ctx, 1)
	now := time.Now().UTC()
	db.Exec("UPDATE push_subscriptions SET disabled_at = ?, updated_at = ? WHERE id = 2", now.Add(-48*time.Hour), now)

	n, err := repo.CleanupExpired(ctx, 24*time.Hour)
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 expired sub, got %d", n)
	}
}

func TestPushVAPIDKeyGeneration(t *testing.T) {
	pub1, priv1, err := push.GenerateVAPIDKeys()
	if err != nil {
		t.Fatalf("generate 1: %v", err)
	}
	pub2, priv2, err := push.GenerateVAPIDKeys()
	if err != nil {
		t.Fatalf("generate 2: %v", err)
	}
	if pub1 == "" || priv1 == "" || pub2 == "" || priv2 == "" {
		t.Fatal("keys must not be empty")
	}
	if pub1 == pub2 {
		t.Error("successive generations must produce different keys")
	}
}

func TestPushNotifierDisabledWithoutKeys(t *testing.T) {
	pn := push.NewPushNotifier(nil, nil, push.VAPIDConfig{})
	if pn.IsEnabled() {
		t.Fatal("notifier must be disabled without VAPID keys")
	}
}

func setupPushDB(t *testing.T) *sql.DB {
	t.Helper()
	tmpDir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(tmpDir, "test.db")+"?_journal_mode=WAL")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	for _, stmt := range []string{
		`CREATE TABLE IF NOT EXISTS push_subscriptions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			mailbox_id INTEGER NOT NULL,
			endpoint TEXT NOT NULL,
			p256dh_key TEXT NOT NULL,
			auth_key TEXT NOT NULL,
			user_agent TEXT NOT NULL DEFAULT '',
			disabled_at DATETIME,
			last_seen_at DATETIME,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_push_sub_endpoint ON push_subscriptions(endpoint)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("create table: %v", err)
		}
	}
	return db
}
