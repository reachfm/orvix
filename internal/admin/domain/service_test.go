package domain

import (
	"context"
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

func newDomainTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	_, err = db.Exec(`CREATE TABLE coremail_domains (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		tenant_id INTEGER NOT NULL,
		name TEXT NOT NULL,
		status TEXT NOT NULL,
		plan TEXT,
		description TEXT,
		max_mailboxes INTEGER,
		max_aliases INTEGER,
		max_quota_mb INTEGER,
		dkim_enabled INTEGER DEFAULT 0,
		dkim_selector TEXT,
		dmarc_enabled INTEGER DEFAULT 0,
		created_at DATETIME,
		updated_at DATETIME,
		deleted_at DATETIME
	);
	CREATE TABLE coremail_mailboxes (id INTEGER PRIMARY KEY, domain_id INTEGER, deleted_at DATETIME);
	CREATE TABLE coremail_aliases (id INTEGER PRIMARY KEY, domain_id INTEGER, deleted_at DATETIME);
	CREATE TABLE coremail_admin_groups (id INTEGER PRIMARY KEY, tenant_id INTEGER, name TEXT, deleted_at DATETIME);
	CREATE TABLE coremail_admin_group_members (group_id INTEGER, user_id INTEGER);`)
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func TestDomainServiceTenantScopedLifecycle(t *testing.T) {
	svc := NewService(NewDomainAdminRepo(newDomainTestDB(t)), nil, nil)
	ctx := context.Background()

	created, err := svc.CreateDomain(ctx, CreateDomainRequest{Name: "example.test"}, 5)
	if err != nil {
		t.Fatalf("create domain: %v", err)
	}
	if created.TenantID != 5 || created.Status != "active" {
		t.Fatalf("unexpected created domain: %#v", created)
	}
	if _, err := svc.GetDomain(ctx, created.ID, 6); err != ErrDomainNotFound {
		t.Fatalf("cross-tenant domain read must fail closed, got %v", err)
	}
	if err := svc.SetDomainStatus(ctx, created.ID, 5, "suspended", "billing"); err != nil {
		t.Fatalf("set status: %v", err)
	}
	got, err := svc.GetDomain(ctx, created.ID, 5)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "suspended" {
		t.Fatalf("status not persisted: %#v", got)
	}
}
