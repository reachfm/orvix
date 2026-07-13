package organization

import (
	"context"
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

func newOrganizationTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	_, err = db.Exec(`CREATE TABLE tenants (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT,
		slug TEXT,
		domain TEXT,
		plan TEXT,
		max_domains INTEGER,
		max_mailboxes INTEGER,
		logo_url TEXT,
		primary_color TEXT,
		active INTEGER,
		created_at DATETIME,
		updated_at DATETIME,
		deleted_at DATETIME
	);
	CREATE TABLE users (id INTEGER PRIMARY KEY, tenant_id INTEGER, role TEXT, deleted_at DATETIME);`)
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func TestOrganizationServiceLifecycleAndDuplicateSlug(t *testing.T) {
	svc := NewService(NewOrganizationRepo(newOrganizationTestDB(t)), nil, nil)
	ctx := context.Background()

	created, err := svc.CreateOrganization(ctx, CreateOrganizationRequest{Slug: "tenant-a", Domain: "tenant-a.test"}, 1)
	if err != nil {
		t.Fatalf("create organization: %v", err)
	}
	if created.Name != "tenant-a" || !created.Active {
		t.Fatalf("defaults not applied: %#v", created)
	}
	if _, err := svc.CreateOrganization(ctx, CreateOrganizationRequest{Slug: "tenant-a"}, 1); err != ErrOrganizationExists {
		t.Fatalf("duplicate slug should fail with ErrOrganizationExists, got %v", err)
	}
	if err := svc.SetOrganizationActive(ctx, created.ID, false, "test"); err != nil {
		t.Fatalf("disable organization: %v", err)
	}
	got, err := svc.GetOrganization(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Active {
		t.Fatalf("organization remained active after disable")
	}
}
