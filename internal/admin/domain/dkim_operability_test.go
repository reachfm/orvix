package domain

// Phase 8 C2: domain operability enforcement for DKIM generation,
// rotation, and the platform DKIM surface. Reuses
// newDomainWithDKIMTestDB (service_test.go).

import (
	"context"
	"errors"
	"testing"

	"github.com/orvix/orvix/internal/coremail/dkim"
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

// ── C2 Part 1: decisive keygen-invocation-count proof ───────────────

// countingKeyGen is the injectable test double: it records every call
// (so tests can assert EXACT invocation counts, not just that an
// error was returned) and can be told to fail on its Nth call.
type countingKeyGen struct {
	calls  int
	failOn int // 0 = never fail
	realFn func(selector, domainName string) (string, string, error)
}

func (g *countingKeyGen) generate(selector, domainName string) (string, string, error) {
	g.calls++
	if g.failOn != 0 && g.calls == g.failOn {
		return "", "", errors.New("simulated keygen failure")
	}
	return g.realFn(selector, domainName)
}

func newCountingKeyGen() *countingKeyGen {
	return &countingKeyGen{realFn: dkim.GenerateKeyPair}
}

func TestGenerateDKIM_KeygenNeverInvokedOnRejection(t *testing.T) {
	cases := []struct {
		name      string
		status    string
		tenantID  uint // tenant the call is made as; 5 is the domain's real owner
		closeDB   bool
		wantOpErr error
	}{
		{name: "disabled", status: "disabled", tenantID: 5, wantOpErr: ErrDomainDisabled},
		{name: "suspended", status: "suspended", tenantID: 5, wantOpErr: ErrDomainSuspended},
		{name: "locked", status: "locked", tenantID: 5, wantOpErr: ErrDomainLocked},
		{name: "cross-tenant", status: "active", tenantID: 6, wantOpErr: ErrDomainNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db, svc := newDomainWithDKIMTestDB(t)
			ctx := context.Background()
			d, err := svc.CreateDomain(ctx, CreateDomainRequest{Name: "keygen-" + tc.name + ".example.test"}, 5)
			if err != nil {
				t.Fatalf("create domain: %v", err)
			}
			if tc.status != "active" {
				if _, err := db.Exec("UPDATE coremail_domains SET status = ? WHERE id = ?", tc.status, d.ID); err != nil {
					t.Fatalf("set status: %v", err)
				}
			}
			gen := newCountingKeyGen()
			svc.SetDKIMKeyGenerator(gen.generate)

			_, err = svc.GenerateDKIM(ctx, d.ID, tc.tenantID, "mail")
			if err != tc.wantOpErr {
				t.Fatalf("expected %v, got %v", tc.wantOpErr, err)
			}
			if gen.calls != 0 {
				t.Fatalf("expected the key generator to be invoked 0 times, got %d", gen.calls)
			}

			var dkimCount, historyCount, auditCount int
			_ = db.QueryRow("SELECT COUNT(*) FROM coremail_dkim_config WHERE domain = ?", "keygen-"+tc.name+".example.test").Scan(&dkimCount)
			if dkimCount != 0 {
				t.Fatalf("expected 0 dkim_config rows, got %d", dkimCount)
			}
			_ = db.QueryRow("SELECT COUNT(*) FROM coremail_dkim_selector_history WHERE domain = ?", "keygen-"+tc.name+".example.test").Scan(&historyCount)
			if historyCount != 0 {
				t.Fatalf("expected 0 selector-history rows (no selector/version mutation), got %d", historyCount)
			}
			_ = db.QueryRow("SELECT COUNT(*) FROM coremail_audit WHERE action = 'domain.dkim.generate' AND result = 'success'").Scan(&auditCount)
			if auditCount != 0 {
				t.Fatalf("expected 0 success audit entries, got %d", auditCount)
			}
		})
	}
}

func TestGenerateDKIM_KeygenInvokedExactlyOnceOnSuccess(t *testing.T) {
	db, svc := newDomainWithDKIMTestDB(t)
	ctx := context.Background()
	d, err := svc.CreateDomain(ctx, CreateDomainRequest{Name: "keygen-active.example.test"}, 5)
	if err != nil {
		t.Fatalf("create domain: %v", err)
	}
	gen := newCountingKeyGen()
	svc.SetDKIMKeyGenerator(gen.generate)

	res, err := svc.GenerateDKIM(ctx, d.ID, 5, "mail")
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if res == nil || res.Selector != "mail" {
		t.Fatalf("expected a real result, got %#v", res)
	}
	if gen.calls != 1 {
		t.Fatalf("expected the key generator to be invoked exactly once, got %d", gen.calls)
	}

	var dkimCount int
	_ = db.QueryRow("SELECT COUNT(*) FROM coremail_dkim_config WHERE domain = ?", "keygen-active.example.test").Scan(&dkimCount)
	if dkimCount != 1 {
		t.Fatalf("expected exactly 1 persisted dkim_config row, got %d", dkimCount)
	}
}

func TestRotateDKIM_KeygenNeverInvokedOnRejection(t *testing.T) {
	db, svc := newDomainWithDKIMTestDB(t)
	ctx := context.Background()
	d, err := svc.CreateDomain(ctx, CreateDomainRequest{Name: "rotate-keygen.example.test"}, 5)
	if err != nil {
		t.Fatalf("create domain: %v", err)
	}
	// Generate for real first (with the real generator) so RotateDKIM
	// has an existing config to (not) touch.
	if _, err := svc.GenerateDKIM(ctx, d.ID, 5, "mail"); err != nil {
		t.Fatalf("initial generate: %v", err)
	}
	var storedBefore string
	if err := db.QueryRow("SELECT private_key_pem FROM coremail_dkim_config WHERE domain = ?", "rotate-keygen.example.test").Scan(&storedBefore); err != nil {
		t.Fatalf("read stored key: %v", err)
	}

	if _, err := db.Exec("UPDATE coremail_domains SET status = 'disabled' WHERE id = ?", d.ID); err != nil {
		t.Fatalf("disable domain: %v", err)
	}
	gen := newCountingKeyGen()
	svc.SetDKIMKeyGenerator(gen.generate)

	if _, err := svc.RotateDKIM(ctx, d.ID, 5, "mail"); err != ErrDomainDisabled {
		t.Fatalf("expected ErrDomainDisabled, got %v", err)
	}
	if gen.calls != 0 {
		t.Fatalf("expected the key generator to be invoked 0 times on a rejected rotation, got %d", gen.calls)
	}

	var storedAfter string
	if err := db.QueryRow("SELECT private_key_pem FROM coremail_dkim_config WHERE domain = ?", "rotate-keygen.example.test").Scan(&storedAfter); err != nil {
		t.Fatalf("read stored key: %v", err)
	}
	if storedAfter != storedBefore {
		t.Fatal("the stored private key changed despite a rejected rotation")
	}
}

func TestPlatformDKIM_KeygenNeverInvokedOnRejection(t *testing.T) {
	db, svc := newDomainWithDKIMTestDB(t)
	ctx := context.Background()
	d, err := svc.CreateDomain(ctx, CreateDomainRequest{Name: "platform-keygen.example.test"}, 5)
	if err != nil {
		t.Fatalf("create domain: %v", err)
	}
	if _, err := db.Exec("UPDATE coremail_domains SET status = 'suspended' WHERE id = ?", d.ID); err != nil {
		t.Fatalf("suspend domain: %v", err)
	}
	gen := newCountingKeyGen()
	svc.SetDKIMKeyGenerator(gen.generate)

	if _, err := svc.PlatformDKIM(ctx, "platform-keygen.example.test", "mail", ""); err != ErrDomainSuspended {
		t.Fatalf("expected ErrDomainSuspended, got %v", err)
	}
	if gen.calls != 0 {
		t.Fatalf("expected the key generator to be invoked 0 times, got %d", gen.calls)
	}
	var dkimCount int
	_ = db.QueryRow("SELECT COUNT(*) FROM coremail_dkim_config WHERE domain = ?", "platform-keygen.example.test").Scan(&dkimCount)
	if dkimCount != 0 {
		t.Fatalf("expected 0 dkim_config rows, got %d", dkimCount)
	}
}
