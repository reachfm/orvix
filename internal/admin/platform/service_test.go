package platform

import (
	"context"
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

func TestPlatformServiceOrganizationSummaryCounts(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	_, err = db.Exec(`CREATE TABLE tenants (
		id INTEGER PRIMARY KEY,
		name TEXT,
		slug TEXT,
		domain TEXT,
		plan TEXT,
		active INTEGER,
		created_at DATETIME,
		deleted_at DATETIME
	);
	CREATE TABLE coremail_mailboxes (id INTEGER PRIMARY KEY, tenant_id INTEGER, deleted_at DATETIME);
	CREATE TABLE coremail_domains (id INTEGER PRIMARY KEY, tenant_id INTEGER, deleted_at DATETIME);
	CREATE TABLE users (id INTEGER PRIMARY KEY, tenant_id INTEGER, email TEXT, role TEXT, deleted_at DATETIME);
	INSERT INTO tenants (id, name, slug, domain, plan, active, created_at) VALUES (7, 'Tenant A', 'tenant-a', 'tenant-a.test', 'enterprise', 1, CURRENT_TIMESTAMP);
	INSERT INTO coremail_mailboxes (id, tenant_id) VALUES (1, 7), (2, 7);
	INSERT INTO coremail_domains (id, tenant_id) VALUES (1, 7);
	INSERT INTO users (id, tenant_id, email, role) VALUES (1, 7, 'admin@tenant-a.test', 'admin');`)
	if err != nil {
		t.Fatal(err)
	}

	svc := NewPlatformService(db, nil, nil)
	summaries, total, err := svc.ListOrganizationSummaries(context.Background(), "Tenant", 10, 0)
	if err != nil {
		t.Fatalf("list summaries: %v", err)
	}
	if total != 1 || len(summaries) != 1 {
		t.Fatalf("unexpected summary count total=%d len=%d", total, len(summaries))
	}
	if summaries[0].MailboxCount != 2 || summaries[0].DomainCount != 1 {
		t.Fatalf("wrong resource counts: %#v", summaries[0])
	}
	detail, err := svc.GetOrganizationDetail(context.Background(), 7)
	if err != nil {
		t.Fatalf("detail: %v", err)
	}
	if admins, ok := detail["administrators"].([]map[string]interface{}); !ok || len(admins) != 1 {
		t.Fatalf("expected one administrator in detail, got %#v", detail["administrators"])
	}
}
