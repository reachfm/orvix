package billing

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/orvix/orvix/internal/dbdialect"
)

func TestMigrateWebhookEventsRepairsLegacyRecordEventConflictTarget(t *testing.T) {
	db := newTestDB(t)
	dialect, err := dbdialect.Detect(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("DROP TABLE webhook_events"); err != nil {
		t.Fatal(err)
	}

	legacyDDL := fmt.Sprintf(`CREATE TABLE webhook_events (
		id TEXT PRIMARY KEY,
		provider TEXT DEFAULT '',
		event_type TEXT DEFAULT '',
		provider_sub_id TEXT DEFAULT '',
		raw_payload %s,
		signature TEXT DEFAULT '',
		received_at %s NOT NULL,
		processed_at %s,
		processing_error TEXT DEFAULT '',
		idempotency_key TEXT NOT NULL UNIQUE,
		created_at %s DEFAULT CURRENT_TIMESTAMP
	)`, dialect.BlobType(), dialect.TimestampType(), dialect.TimestampType(), dialect.TimestampType())
	if _, err := db.Exec(legacyDDL); err != nil {
		t.Fatal(err)
	}

	insertLegacy := `INSERT INTO webhook_events
		(id, provider, event_type, received_at, idempotency_key, created_at)
		VALUES (` + dialect.Placeholders(6) + `)`
	now := time.Now().UTC()
	if _, err := db.Exec(insertLegacy, "evt_legacy", "", "invoice.created", now, "idem_legacy", now); err != nil {
		t.Fatal(err)
	}

	if err := MigrateWebhookEvents(db); err != nil {
		t.Fatalf("migrate legacy webhook schema: %v", err)
	}
	if err := MigrateWebhookEvents(db); err != nil {
		t.Fatalf("repeat migration must be idempotent: %v", err)
	}

	var provider string
	if err := db.QueryRow("SELECT provider FROM webhook_events WHERE id = "+dialect.Placeholder(1), "evt_legacy").Scan(&provider); err != nil {
		t.Fatal(err)
	}
	if provider != "legacy" {
		t.Fatalf("legacy row provider: got %q want legacy", provider)
	}

	service := NewWebhookService(db)
	record := func(provider, id, key string) error {
		return service.RecordEvent(context.Background(), &WebhookEventRecord{
			ID: id, Provider: provider, EventType: "invoice.created",
			ReceivedAt: now, IdempotencyKey: key,
		})
	}
	if err := record("stripe", "evt_legacy", "idem_legacy"); err != nil {
		t.Fatalf("provider-scoped legacy ID/key must be reusable: %v", err)
	}
	if err := record("stripe", "evt_legacy", "idem_legacy"); !errors.Is(err, ErrWebhookAlreadyProcessed) {
		t.Fatalf("same provider duplicate: got %v want ErrWebhookAlreadyProcessed", err)
	}
	if err := record("adyen", "evt_legacy", "idem_legacy"); err != nil {
		t.Fatalf("different provider must not conflict: %v", err)
	}

	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM webhook_events WHERE id = "+dialect.Placeholder(1), "evt_legacy").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 3 {
		t.Fatalf("preserved plus provider-scoped rows: got %d want 3", count)
	}
}
