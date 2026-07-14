package organization

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	_ "modernc.org/sqlite"
)

func benchmarkOrgSetup(b *testing.B) (*sql.DB, *Service) {
	b.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { db.Close() })
	_, err = db.Exec(`CREATE TABLE tenants (
		id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT, slug TEXT UNIQUE, domain TEXT,
		plan TEXT, max_domains INTEGER, max_mailboxes INTEGER, logo_url TEXT,
		primary_color TEXT, active INTEGER, created_at DATETIME, updated_at DATETIME, deleted_at DATETIME
	)`)
	if err != nil {
		b.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE users (
		id INTEGER PRIMARY KEY AUTOINCREMENT, tenant_id INTEGER, email TEXT, role TEXT,
		password_hash TEXT, active INTEGER, created_at DATETIME, updated_at DATETIME, deleted_at DATETIME
	)`)
	if err != nil {
		b.Fatal(err)
	}
	svc := NewService(NewOrganizationRepo(db), nil, nil)
	return db, svc
}

func BenchmarkCreateOrganization(b *testing.B) {
	_, svc := benchmarkOrgSetup(b)
	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		slug := fmt.Sprintf("tenant-%d", i)
		svc.CreateOrganization(ctx, CreateOrganizationRequest{Slug: slug, Domain: slug + ".test"}, 1)
	}
}

func BenchmarkGetOrganization(b *testing.B) {
	_, svc := benchmarkOrgSetup(b)
	ctx := context.Background()
	org, _ := svc.CreateOrganization(ctx, CreateOrganizationRequest{Slug: "bench", Domain: "bench.test"}, 1)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		svc.GetOrganization(ctx, org.ID)
	}
}

func BenchmarkListOrganizations(b *testing.B) {
	_, svc := benchmarkOrgSetup(b)
	ctx := context.Background()
	for i := 0; i < 100; i++ {
		slug := fmt.Sprintf("tenant-%d", i)
		svc.CreateOrganization(ctx, CreateOrganizationRequest{Slug: slug, Domain: slug + ".test"}, 1)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		svc.ListOrganizations(ctx, OrganizationFilter{Limit: 20, Offset: 0})
	}
}

func BenchmarkSuspendOrganization(b *testing.B) {
	_, svc := benchmarkOrgSetup(b)
	ctx := context.Background()
	org, _ := svc.CreateOrganization(ctx, CreateOrganizationRequest{Slug: "bench", Domain: "bench.test"}, 1)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		svc.SuspendOrganization(ctx, org.ID, 1, SuspensionManual, "benchmark")
		svc.ReactivateOrganization(ctx, org.ID)
	}
}
