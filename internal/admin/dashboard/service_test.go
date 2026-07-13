package dashboard

import (
	"context"
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

func TestDashboardServiceTenantAndPlatformAggregation(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	_, err = db.Exec(`CREATE TABLE tenants (id INTEGER PRIMARY KEY, active INTEGER, deleted_at DATETIME);
	CREATE TABLE coremail_domains (id INTEGER PRIMARY KEY, tenant_id INTEGER, status TEXT, deleted_at DATETIME);
	CREATE TABLE coremail_mailboxes (id INTEGER PRIMARY KEY, tenant_id INTEGER, status TEXT, used_bytes INTEGER, deleted_at DATETIME);
	CREATE TABLE orvix_audit (id INTEGER PRIMARY KEY, tenant_id INTEGER, action TEXT, target TEXT, timestamp DATETIME);
	INSERT INTO tenants (id, active) VALUES (1, 1), (2, 0);
	INSERT INTO coremail_domains (id, tenant_id, status) VALUES (1, 1, 'active'), (2, 1, 'suspended'), (3, 2, 'active');
	INSERT INTO coremail_mailboxes (id, tenant_id, status, used_bytes) VALUES (1, 1, 'active', 100), (2, 1, 'suspended', 200), (3, 2, 'active', 300);
	INSERT INTO orvix_audit (id, tenant_id, action, target, timestamp) VALUES (1, 1, 'mailbox.create', 'mailbox:1', CURRENT_TIMESTAMP);`)
	if err != nil {
		t.Fatal(err)
	}

	svc := NewDashboardService(db)
	customer, err := svc.CustomerDashboard(context.Background(), 1)
	if err != nil {
		t.Fatalf("customer dashboard: %v", err)
	}
	if customer.TotalDomains != 2 || customer.HealthyDomains != 1 || customer.DomainsNeedingAttention != 1 {
		t.Fatalf("wrong tenant domain counts: %#v", customer)
	}
	if customer.TotalMailboxes != 2 || customer.ActiveMailboxes != 1 || customer.SuspendedMailboxes != 1 || customer.QuotaUsedBytes != 300 {
		t.Fatalf("wrong tenant mailbox counts: %#v", customer)
	}
	if len(customer.RecentActions) != 1 || customer.RecentActions[0].Action != "mailbox.create" {
		t.Fatalf("missing tenant audit action: %#v", customer.RecentActions)
	}

	platform, err := svc.PlatformDashboard(context.Background())
	if err != nil {
		t.Fatalf("platform dashboard: %v", err)
	}
	if platform.TotalOrganizations != 2 || platform.ActiveOrganizations != 1 || platform.TotalDomains != 3 || platform.TotalMailboxes != 3 || platform.QuotaUsedBytes != 600 {
		t.Fatalf("wrong platform counts: %#v", platform)
	}
}
