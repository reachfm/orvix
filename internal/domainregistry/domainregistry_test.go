package domainregistry

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	_ "modernc.org/sqlite"
)

func testService(t *testing.T) *Service {
	t.Helper()
	db, err := sql.Open("sqlite", fmt.Sprintf("%s/domains_test.db", t.TempDir()))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	repo := NewRepository(db)
	svc := NewService(repo)
	if err := svc.EnsureSchema(context.Background()); err != nil {
		t.Fatalf("ensure schema: %v", err)
	}
	return svc
}

func TestCreateDomain(t *testing.T) {
	svc := testService(t)
	ctx := context.Background()

	d, err := svc.CreateDomain(ctx, &CreateDomainRequest{Name: "example.com"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if d.ID == 0 {
		t.Fatal("expected non-zero ID")
	}
	if d.Name != "example.com" {
		t.Fatalf("expected example.com, got %s", d.Name)
	}
	if d.Status != DomainActive {
		t.Fatalf("expected active, got %s", d.Status)
	}
}

func TestCreateDuplicateRejected(t *testing.T) {
	svc := testService(t)
	ctx := context.Background()

	svc.CreateDomain(ctx, &CreateDomainRequest{Name: "example.com"})
	_, err := svc.CreateDomain(ctx, &CreateDomainRequest{Name: "example.com"})
	if err == nil {
		t.Fatal("expected error for duplicate domain")
	}
}

func TestCreateInvalidDomainRejected(t *testing.T) {
	svc := testService(t)
	ctx := context.Background()

	invalid := []string{"", "  ", "invalid", "domain with spaces.com", "domain..com", ".startdot.com", "enddot.", "a@b.com", "too-long-" + string(make([]byte, 300))}
	for _, name := range invalid {
		_, err := svc.CreateDomain(ctx, &CreateDomainRequest{Name: name})
		if err == nil {
			t.Errorf("expected error for invalid domain: %q", name)
		}
	}
}

func TestListDomains(t *testing.T) {
	svc := testService(t)
	ctx := context.Background()

	svc.CreateDomain(ctx, &CreateDomainRequest{Name: "a.com"})
	svc.CreateDomain(ctx, &CreateDomainRequest{Name: "b.com"})
	svc.CreateDomain(ctx, &CreateDomainRequest{Name: "c.com"})

	domains, err := svc.ListDomains(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(domains) != 3 {
		t.Fatalf("expected 3 domains, got %d", len(domains))
	}
}

func TestGetDomain(t *testing.T) {
	svc := testService(t)
	ctx := context.Background()

	created, _ := svc.CreateDomain(ctx, &CreateDomainRequest{Name: "gettest.com"})
	got, err := svc.GetDomain(ctx, created.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != "gettest.com" {
		t.Fatalf("expected gettest.com, got %s", got.Name)
	}
}

func TestGetByName(t *testing.T) {
	svc := testService(t)
	ctx := context.Background()

	svc.CreateDomain(ctx, &CreateDomainRequest{Name: "byname.com"})
	got, err := svc.GetByName(ctx, "byname.com")
	if err != nil {
		t.Fatalf("get by name: %v", err)
	}
	if got == nil {
		t.Fatal("expected non-nil domain")
	}
}

func TestUpdateDomain(t *testing.T) {
	svc := testService(t)
	ctx := context.Background()

	created, _ := svc.CreateDomain(ctx, &CreateDomainRequest{Name: "update.com"})
	suspended := DomainSuspended
	updated, err := svc.UpdateDomain(ctx, created.ID, &UpdateDomainRequest{Status: &suspended})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Status != DomainSuspended {
		t.Fatalf("expected suspended, got %s", updated.Status)
	}
}

func TestDeleteDomain(t *testing.T) {
	svc := testService(t)
	ctx := context.Background()

	created, _ := svc.CreateDomain(ctx, &CreateDomainRequest{Name: "delete.com"})
	if err := svc.DeleteDomain(ctx, created.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	got, _ := svc.GetDomain(ctx, created.ID)
	if got != nil {
		t.Fatal("expected nil after delete")
	}
}

func TestValidateDomain(t *testing.T) {
	valid := []string{"example.com", "sub.example.com", "my-domain.co.uk", "xn--fiqa61au8b7zsevnm8ak20mc4a87e.xn--fiqs8s"}
	for _, name := range valid {
		if err := ValidateDomain(name); err != nil {
			t.Errorf("expected valid: %q, got: %v", name, err)
		}
	}
	invalid := []string{"", "  ", "-start.com", "end-.com", "spaces in.com", "a..b.com", ".dot", "dot.", "a@b.com", "DOMAIN"}
	for _, name := range invalid {
		if err := ValidateDomain(name); err == nil {
			t.Errorf("expected invalid: %q", name)
		}
	}
}

func TestDomainExists(t *testing.T) {
	svc := testService(t)
	ctx := context.Background()

	svc.CreateDomain(ctx, &CreateDomainRequest{Name: "exists.com"})
	exists, err := svc.DomainExists(ctx, "exists.com")
	if err != nil {
		t.Fatalf("exists: %v", err)
	}
	if !exists {
		t.Fatal("expected domain to exist")
	}
	notExists, _ := svc.DomainExists(ctx, "notexists.com")
	if notExists {
		t.Fatal("expected domain to not exist")
	}
}
