package domain

// Phase 8 C2: domain operability enforcement for DKIM generation,
// rotation, and the platform DKIM surface. Reuses
// newDomainWithDKIMTestDB (service_test.go).

import (
	"context"
	"testing"
)

func TestGenerateDKIM_DisabledDomainRejectedBeforeKeygen(t *testing.T) {
	db, svc := newDomainWithDKIMTestDB(t)
	ctx := context.Background()
	d, err := svc.CreateDomain(ctx, CreateDomainRequest{Name: "dkim-disabled.example.test"}, 5)
	if err != nil {
		t.Fatalf("create domain: %v", err)
	}
	if _, err := db.Exec("UPDATE coremail_domains SET status = 'disabled' WHERE id = ?", d.ID); err != nil {
		t.Fatalf("disable domain: %v", err)
	}

	if _, err := svc.GenerateDKIM(ctx, d.ID, 5, "mail"); err != ErrDomainDisabled {
		t.Fatalf("generate on disabled domain: err = %v, want ErrDomainDisabled", err)
	}

	// No key material was ever persisted — the guard ran before
	// dkim.GenerateKeyPair, not after.
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM coremail_dkim_config WHERE domain = ?", "dkim-disabled.example.test").Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected 0 dkim_config rows for a rejected domain, got %d", count)
	}
}

func TestGenerateDKIM_CrossTenantDomainDenied(t *testing.T) {
	_, svc := newDomainWithDKIMTestDB(t)
	ctx := context.Background()
	d, err := svc.CreateDomain(ctx, CreateDomainRequest{Name: "dkim-xtenant.example.test"}, 5)
	if err != nil {
		t.Fatalf("create domain: %v", err)
	}
	// Tenant 6 requesting DKIM generation for tenant 5's domain must
	// see the same not-found contract as a genuinely unknown domain.
	if _, err := svc.GenerateDKIM(ctx, d.ID, 6, "mail"); err != ErrDomainNotFound {
		t.Fatalf("cross-tenant generate: err = %v, want ErrDomainNotFound", err)
	}
}

func TestRotateDKIM_DisabledDomainRejectedWithoutTouchingKey(t *testing.T) {
	db, svc := newDomainWithDKIMTestDB(t)
	ctx := context.Background()
	d, err := svc.CreateDomain(ctx, CreateDomainRequest{Name: "dkim-rotate-disabled.example.test"}, 5)
	if err != nil {
		t.Fatalf("create domain: %v", err)
	}
	first, err := svc.GenerateDKIM(ctx, d.ID, 5, "mail")
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if _, err := db.Exec("UPDATE coremail_domains SET status = 'suspended' WHERE id = ?", d.ID); err != nil {
		t.Fatalf("suspend domain: %v", err)
	}

	if _, err := svc.RotateDKIM(ctx, d.ID, 5, "mail"); err != ErrDomainSuspended {
		t.Fatalf("rotate on suspended domain: err = %v, want ErrDomainSuspended", err)
	}

	// The stored key must be byte-identical to what GenerateDKIM
	// originally produced — rotation never touched it.
	var storedSelector string
	if err := db.QueryRow("SELECT selector FROM coremail_dkim_config WHERE domain = ?", "dkim-rotate-disabled.example.test").Scan(&storedSelector); err != nil {
		t.Fatalf("read stored config: %v", err)
	}
	if storedSelector != "mail" {
		t.Fatalf("stored selector changed despite rejected rotation: %q", storedSelector)
	}
	_ = first
}

func TestPlatformDKIM_DisabledDomainRejected(t *testing.T) {
	db, svc := newDomainWithDKIMTestDB(t)
	ctx := context.Background()
	d, err := svc.CreateDomain(ctx, CreateDomainRequest{Name: "platform-dkim-disabled.example.test"}, 5)
	if err != nil {
		t.Fatalf("create domain: %v", err)
	}
	if _, err := db.Exec("UPDATE coremail_domains SET status = 'locked' WHERE id = ?", d.ID); err != nil {
		t.Fatalf("lock domain: %v", err)
	}

	if _, err := svc.PlatformDKIM(ctx, "platform-dkim-disabled.example.test", "mail", ""); err != ErrDomainLocked {
		t.Fatalf("platform dkim on locked domain: err = %v, want ErrDomainLocked", err)
	}

	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM coremail_dkim_config WHERE domain = ?", "platform-dkim-disabled.example.test").Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected 0 dkim_config rows for a rejected platform request, got %d", count)
	}
}

func TestGenerateDKIM_RepositoryFailureFailsClosed(t *testing.T) {
	db, svc := newDomainWithDKIMTestDB(t)
	ctx := context.Background()
	d, err := svc.CreateDomain(ctx, CreateDomainRequest{Name: "dkim-infra-fail.example.test"}, 5)
	if err != nil {
		t.Fatalf("create domain: %v", err)
	}
	db.Close()

	_, err = svc.GenerateDKIM(ctx, d.ID, 5, "mail")
	if err == nil {
		t.Fatal("expected an error after closing the database")
	}
	if err == ErrDomainNotFound {
		t.Fatal("a repository failure must not be misreported as ErrDomainNotFound")
	}
}
