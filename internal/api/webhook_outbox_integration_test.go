package api

import (
	"context"
	"encoding/json"
	"testing"

	domainadmin "github.com/orvix/orvix/internal/admin/domain"
	"github.com/orvix/orvix/internal/webhooks"
)

func TestDomainCreateCommitsWebhookOutboxAtomically(t *testing.T) {
	router, _, _ := newPublicAPITestRouter(t)
	svc := router.h.DomainAdminService()
	created, err := svc.CreateDomain(context.Background(), domainadmin.CreateDomainRequest{Name: "outbox.example"}, 1)
	if err != nil {
		t.Fatalf("create audited domain: %v", err)
	}

	sqlDB, _ := router.db.DB()
	var topic, aggregateID, payload string
	if err := sqlDB.QueryRow(`SELECT topic, aggregate_id, payload FROM platform_outbox_events WHERE topic = ?`, webhooks.OutboxTopic).Scan(&topic, &aggregateID, &payload); err != nil {
		t.Fatalf("read webhook outbox event: %v", err)
	}
	var event webhooks.Event
	if err := json.Unmarshal([]byte(payload), &event); err != nil {
		t.Fatalf("decode webhook event: %v", err)
	}
	if topic != webhooks.OutboxTopic || aggregateID != event.ID || event.Type != "domain.created" || event.TenantID != 1 || event.SchemaVersion != 1 || event.OccurredAt.IsZero() {
		t.Fatalf("unexpected outbox event: topic=%q aggregate=%q event=%+v", topic, aggregateID, event)
	}
	var body struct {
		DomainID uint   `json:"domain_id"`
		Name     string `json:"name"`
	}
	if err := json.Unmarshal(event.Payload, &body); err != nil || body.DomainID != created.ID || body.Name != created.Name {
		t.Fatalf("unexpected immutable payload: body=%+v err=%v", body, err)
	}
	var auditCount int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM orvix_audit WHERE action='domain.create' AND target_id=?`, created.ID).Scan(&auditCount); err != nil || auditCount != 1 {
		t.Fatalf("audit count=%d err=%v", auditCount, err)
	}
}

func TestDomainCreateRollsBackWhenWebhookOutboxWriteFails(t *testing.T) {
	router, _, _ := newPublicAPITestRouter(t)
	sqlDB, _ := router.db.DB()
	if _, err := sqlDB.Exec(`DROP TABLE platform_outbox_events`); err != nil {
		t.Fatal(err)
	}
	if _, err := router.h.DomainAdminService().CreateDomain(context.Background(), domainadmin.CreateDomainRequest{Name: "rollback-outbox.example"}, 1); err == nil {
		t.Fatal("expected domain creation to fail when webhook outbox is unavailable")
	}
	var domains, audits int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM coremail_domains WHERE name='rollback-outbox.example'`).Scan(&domains); err != nil {
		t.Fatal(err)
	}
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM orvix_audit WHERE action='domain.create' AND target LIKE '%rollback-outbox%'`).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if domains != 0 || audits != 0 {
		t.Fatalf("partial mutation survived rollback: domains=%d audits=%d", domains, audits)
	}
}
