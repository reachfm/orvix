package mailcontrol

import (
	"context"
	"strings"
	"testing"

	"github.com/orvix/orvix/internal/platform/kernel"
)

func TestRequireTenant(t *testing.T) {
	svc, _, _ := newMailControlHarness(t)
	if _, err := svc.ListDomains(context.Background(), PlatformDomainFilter{TenantID: 0}); err == nil {
		t.Fatal("expected tenant-required error when tenant_id is 0")
	}
	if _, err := svc.ListMailboxes(context.Background(), PlatformMailboxFilter{TenantID: 0}); err == nil {
		t.Fatal("expected tenant-required error when tenant_id is 0")
	}
	if _, err := svc.ListAliases(context.Background(), PlatformAliasFilter{TenantID: 0}); err == nil {
		t.Fatal("expected tenant-required error when tenant_id is 0")
	}
	if _, err := svc.ListGroups(context.Background(), PlatformGroupFilter{TenantID: 0}); err == nil {
		t.Fatal("expected tenant-required error when tenant_id is 0")
	}
}

func TestCrossTenantDomainDenied(t *testing.T) {
	svc, _, db := newMailControlHarness(t)
	mustSeedTenant(t, db, 1)
	mustSeedTenant(t, db, 2)

	// Seed a domain row for tenant 1.
	if _, err := db.Exec(`INSERT INTO coremail_domains (tenant_id, name, status, plan, created_at, updated_at)
		VALUES (1, 'one.example', 'active', 'business', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}

	// Reading it as tenant 2 must NOT find it (ownership is enforced by
	// the admin service's tenant-scoped SQL).
	_, err := svc.GetDomain(context.Background(), 1, 2)
	if err == nil {
		t.Fatal("expected cross-tenant domain lookup to fail")
	}
	kerr := kernel.AsAPIError(err)
	if kerr.Code != kernel.ErrCodeNotFound {
		t.Fatalf("expected NOT_FOUND for cross-tenant domain, got %s", kerr.Code)
	}
}

func TestSetDomainStatusTenantScoped(t *testing.T) {
	svc, _, db := newMailControlHarness(t)
	mustSeedTenant(t, db, 1)
	mustSeedTenant(t, db, 2)
	if _, err := db.Exec(`INSERT INTO coremail_domains (tenant_id, name, status, plan, created_at, updated_at)
		VALUES (1, 'one.example', 'active', 'business', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}

	// Tenant 2 must not be able to transition tenant 1's domain.
	if err := svc.SetDomainStatus(context.Background(), 1, 2, "suspended", "test", 99); err == nil {
		t.Fatal("expected cross-tenant status transition to fail")
	}
	// Tenant 1 can.
	if err := svc.SetDomainStatus(context.Background(), 1, 1, "suspended", "test", 99); err != nil {
		t.Fatalf("expected same-tenant status transition to succeed: %v", err)
	}
}

func TestMailAccessModeValidation(t *testing.T) {
	svc, _, db := newMailControlHarness(t)
	mustSeedTenant(t, db, 1)
	if _, err := db.Exec(`INSERT INTO coremail_domains (tenant_id, name, status, plan, created_at, updated_at)
		VALUES (1, 'mode.example', 'active', 'business', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetMailAccessMode(context.Background(), 1, 1, "bogus_mode", 99); err == nil {
		t.Fatal("expected validation error for bogus mail-access mode")
	}
	if err := svc.SetMailAccessMode(context.Background(), 1, 1, "internal_only", 99); err != nil {
		t.Fatalf("expected internal_only to apply: %v", err)
	}
	d, err := svc.GetDomain(context.Background(), 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if d.MailAccessMode != "internal_only" {
		t.Fatalf("expected internal_only, got %q", d.MailAccessMode)
	}
}

func TestAliasLoopRejected(t *testing.T) {
	svc, _, db := newMailControlHarness(t)
	mustSeedTenant(t, db, 1)
	if _, err := db.Exec(`INSERT INTO coremail_domains (tenant_id, name, status, plan, created_at, updated_at)
		VALUES (1, 'loop.example', 'active', 'business', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateAlias(context.Background(), 1, 1, "x@loop.example", "x@loop.example", 99); err == nil {
		t.Fatal("expected alias loop to be rejected")
	}
}

func TestAliasCrossTenantDenied(t *testing.T) {
	svc, _, db := newMailControlHarness(t)
	mustSeedTenant(t, db, 1)
	mustSeedTenant(t, db, 2)
	if _, err := db.Exec(`INSERT INTO coremail_domains (tenant_id, name, status, plan, created_at, updated_at)
		VALUES (1, 'a.example', 'active', 'business', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	// Creating an alias on tenant 2 using tenant 1's domain must fail.
	if _, err := svc.CreateAlias(context.Background(), 2, 1, "a@a.example", "b@a.example", 99); err == nil {
		t.Fatal("expected cross-tenant alias creation to fail (domain not owned by tenant 2)")
	}
}

func TestBulkMailboxStatusBounded(t *testing.T) {
	svc, _, db := newMailControlHarness(t)
	mustSeedTenant(t, db, 1)
	if _, err := db.Exec(`INSERT INTO coremail_domains (tenant_id, name, status, plan, created_at, updated_at)
		VALUES (1, 'b.example', 'active', 'business', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 3; i++ {
		if _, err := db.Exec(`INSERT INTO coremail_mailboxes (domain_id, tenant_id, local_part, email, name, password_hash, status, quota_mb, created_at, updated_at)
			VALUES (1, 1, 'u', ?, ?, 'hash', 'active', 1024, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`, "u"+itoa(i)+"@b.example", "U"+itoa(i)); err != nil {
			t.Fatal(err)
		}
	}

	// >500 ids is rejected.
	ids := make([]uint, 501)
	for i := range ids {
		ids[i] = uint(i + 1)
	}
	if _, err := svc.BulkMailboxStatus(context.Background(), BulkMailboxRequest{TenantID: 1, IDs: ids, Action: BulkMailboxSuspend}, 99); err == nil {
		t.Fatal("expected >500 ids to be rejected")
	}

	// Invalid action is rejected.
	if _, err := svc.BulkMailboxStatus(context.Background(), BulkMailboxRequest{TenantID: 1, IDs: []uint{1}, Action: BulkMailboxAction("nuke")}, 99); err == nil {
		t.Fatal("expected invalid bulk action to be rejected")
	}

	// Suspending mailboxes across tenant boundary must not affect other
	// tenants. Only tenant 1 mailboxes exist, so all succeed.
	res, err := svc.BulkMailboxStatus(context.Background(), BulkMailboxRequest{TenantID: 1, IDs: []uint{1, 2, 3}, Action: BulkMailboxSuspend, Reason: "test"}, 99)
	if err != nil {
		t.Fatal(err)
	}
	if res.Succeeded != 3 {
		t.Fatalf("expected 3 suspended, got %d", res.Succeeded)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var sb strings.Builder
	for n > 0 {
		sb.WriteByte(byte('0' + n%10))
		n /= 10
	}
	runes := []rune(sb.String())
	for i, j := 0, len(runes)-1; i < j; i, j = i+1, j-1 {
		runes[i], runes[j] = runes[j], runes[i]
	}
	return string(runes)
}
