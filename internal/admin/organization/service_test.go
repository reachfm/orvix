package organization

import (
	"context"
	"database/sql"
	"testing"

	"github.com/orvix/orvix/internal/audit"
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

func TestOrganizationMutationRollsBackWhenAuditWriteFails(t *testing.T) {
	db := newOrganizationTestDB(t)
	db.SetMaxOpenConns(1)
	svc := NewService(NewOrganizationRepo(db), audit.NewExtendedStore(db), nil)
	if _, err := svc.CreateOrganization(context.Background(), CreateOrganizationRequest{Slug: "audit-failure", Domain: "audit-failure.test"}, 1); err == nil {
		t.Fatal("audit failure must fail the organization mutation")
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM tenants WHERE slug = ?`, "audit-failure").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("organization mutation committed without its audit record")
	}
}
