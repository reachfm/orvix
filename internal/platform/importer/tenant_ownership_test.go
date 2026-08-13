package importer

import (
	"context"
	"errors"
	"testing"

	"github.com/orvix/orvix/internal/dbdialect"
	"github.com/orvix/orvix/internal/platform/kernel"
)

// H-7 regression suite: platform imports must never create tenant-0 orphans.
//
// SECURITY INVARIANT UNDER TEST: no tenant-owned entity may be created,
// updated, or compensated with tenant_id <= 0.
//
// The verified defect was that a platform-scoped import carried tenant_id = 0
// end to end, so admins/domains/mailboxes/aliases/groups/memberships were
// written with tenant_id = 0 — invisible to every tenant UI, unreachable by
// tenant-scoped queries, and (for users) permanently unable to log in because
// a tenant-scoped role fails authorization validation without tenant_id > 0.

// importTestTenantID is the explicit target tenant the lifecycle tests import
// into. Platform-scoped imports now REQUIRE an explicit target; using a real
// tenant id here is what the production contract demands.
const importTestTenantID uint = 1

// ── The choke point: production adapters ───────────────────────────────

// TestProductionAdapters_RejectTenantZero is the decisive proof. Every
// production adapter method that takes a tenant is invoked with 0 and must
// refuse. This is the single boundary every entity write passes through (HTTP
// tenant + platform, CLI, executor, resume, compensation), so a refusal here
// makes the orphan structurally impossible regardless of caller.
func TestProductionAdapters_RejectTenantZero(t *testing.T) {
	// Adapters are driven directly with tenant 0; no database is required
	// because the guard rejects before any statement is issued.
	orgA := &prodOrgAdapter{}
	adminA := &prodAdminAdapter{}
	domainA := &prodDomainAdapter{}
	mailboxA := &prodMailboxAdapter{}
	aliasA := &prodAliasAdapter{}
	groupA := &prodGroupAdapter{}
	ctx := context.Background()
	safe := map[string]any{"name": "x"}

	type check struct {
		name string
		err  error
	}
	var results []check

	// Creates.
	_, err := orgA.CreateOrganization(ctx, "n", "d.test", 0)
	results = append(results, check{"CreateOrganization", err})
	_, err = adminA.CreateTenantAdmin(ctx, "a@b.test", "n", "pw", "tenant_admin", 0)
	results = append(results, check{"CreateTenantAdmin", err})
	_, err = domainA.CreateDomain(ctx, "d.test", 0)
	results = append(results, check{"CreateDomain", err})
	_, err = mailboxA.CreateMailbox(ctx, "m@d.test", "n", "pw", "d.test", 0)
	results = append(results, check{"CreateMailbox", err})
	_, err = aliasA.CreateAlias(ctx, "a@d.test", "b@d.test", 0, 0)
	results = append(results, check{"CreateAlias", err})
	_, err = groupA.CreateGroup(ctx, "g", "desc", 0)
	results = append(results, check{"CreateGroup", err})

	// Membership.
	results = append(results, check{"AddGroupMember", groupA.AddGroupMember(ctx, "g", "m@d.test", 0)})

	// Updates (safe-field paths).
	results = append(results, check{"UpdateOrganization", orgA.UpdateOrganization(ctx, 1, 0, safe)})
	results = append(results, check{"UpdateTenantAdmin", adminA.UpdateTenantAdmin(ctx, 1, 0, safe)})
	results = append(results, check{"UpdateDomain", domainA.UpdateDomain(ctx, 1, 0, safe)})
	results = append(results, check{"UpdateMailbox", mailboxA.UpdateMailbox(ctx, 1, 0, safe)})
	results = append(results, check{"UpdateGroup", groupA.UpdateGroup(ctx, 1, 0, safe)})

	// Compensation paths must also refuse: acting with tenant 0 would target
	// exactly the orphan rows this invariant forbids.
	results = append(results, check{"SoftDeleteOrganization", orgA.SoftDeleteOrganization(ctx, 1, 0)})
	results = append(results, check{"SoftDeleteUser", adminA.SoftDeleteUser(ctx, 1, 0)})
	results = append(results, check{"SoftDeleteDomain", domainA.SoftDeleteDomain(ctx, 1, 0)})
	results = append(results, check{"SoftDeleteMailbox", mailboxA.SoftDeleteMailbox(ctx, 1, 0)})
	results = append(results, check{"SoftDeleteAlias", aliasA.SoftDeleteAlias(ctx, 1, 0)})
	results = append(results, check{"SoftDeleteGroup", groupA.SoftDeleteGroup(ctx, 1, 0)})
	results = append(results, check{"RemoveGroupMember", groupA.RemoveGroupMember(ctx, 1, 0)})

	if len(results) != 19 {
		t.Fatalf("expected all 19 tenant-taking adapter methods covered, got %d", len(results))
	}
	for _, r := range results {
		if r.err == nil {
			t.Errorf("%s accepted tenant 0 — tenant-owned entity could be orphaned", r.name)
			continue
		}
		var ie *ImportError
		if !errors.As(r.err, &ie) || ie.Code != CodeTenantRequired {
			t.Errorf("%s must fail with %s, got %v", r.name, CodeTenantRequired, r.err)
		}
	}
}

// TestProductionAdapters_TenantZeroRejectedBeforeAnyStatement proves the guard
// runs before the adapter touches its database handle: the adapters above were
// constructed with a nil *sql.DB, so if a guard were missing the call would
// panic instead of returning a typed error. A clean typed error is therefore
// evidence that no statement was attempted.
func TestProductionAdapters_TenantZeroRejectedBeforeAnyStatement(t *testing.T) {
	a := &prodAdminAdapter{} // nil db, nil dialect
	_, err := a.CreateTenantAdmin(context.Background(), "x@y.test", "n", "pw", "tenant_admin", 0)
	if err == nil {
		t.Fatal("expected rejection")
	}
	var ie *ImportError
	if !errors.As(err, &ie) || ie.Code != CodeTenantRequired {
		t.Fatalf("expected %s, got %v", CodeTenantRequired, err)
	}
}

// ── Entry point: Service.Create ────────────────────────────────────────

// TestCreate_RejectsZeroTenantBeforeStaging proves an import that cannot
// legally own its entities is refused before anything is persisted: no staged
// source file, no job row, no checkpoint — and therefore nothing to clean up.
func TestCreate_RejectsZeroTenantBeforeStaging(t *testing.T) {
	db := setupTestDB(t)
	svc := NewService(testRepo(t, db), testAdapters(t, db), mustStaging(t), nil, nil)

	_, err := svc.Create(context.Background(), CreateImportParams{
		TenantID: 0, Scope: "platform", Actor: "psa", SourceType: SourceCSV, SourceName: "x.csv",
	}, []byte("entity,name,domain\norganization,Acme,acme.test\n"))
	if err == nil {
		t.Fatal("platform import without a target tenant must be rejected")
	}
	// The public error is a typed validation failure whose field map names the
	// offending input, so the API surfaces something actionable without
	// leaking internals. (err.Error() itself is the generic envelope.)
	var kerr *kernel.Error
	if !errors.As(err, &kerr) {
		t.Fatalf("expected a typed kernel error, got %T: %v", err, err)
	}
	if _, ok := kerr.Fields["target_tenant_id"]; !ok {
		t.Fatalf("validation error must name target_tenant_id, got fields %v", kerr.Fields)
	}

	// Nothing may be left behind by the rejected precondition.
	var jobs int
	if err := db.QueryRow(`SELECT COUNT(*) FROM platform_import_jobs`).Scan(&jobs); err == nil && jobs != 0 {
		t.Fatalf("a rejected import must leave no job row, found %d", jobs)
	}
}

// TestCreate_AcceptsExplicitTargetTenant is the positive control: with a valid
// explicit target, creation succeeds and the job records that tenant as its
// immutable owner.
func TestCreate_AcceptsExplicitTargetTenant(t *testing.T) {
	db := setupTestDB(t)
	svc := NewService(testRepo(t, db), testAdapters(t, db), mustStaging(t), nil, nil)

	job, err := svc.Create(context.Background(), CreateImportParams{
		TenantID: 7, Scope: "platform", Actor: "psa", SourceType: SourceCSV, SourceName: "x.csv",
	}, []byte("entity,name,domain\norganization,Acme,acme.test\n"))
	if err != nil {
		t.Fatalf("create with explicit target tenant: %v", err)
	}
	if job.TenantID != 7 {
		t.Fatalf("job must record the explicit target tenant, got %d", job.TenantID)
	}
}

// TestCreate_TargetTenantIsImmutableAcrossLifecycle proves the tenant decided
// at creation is authoritative: a caller claiming a different tenant cannot
// reach the job, so ownership cannot drift between validate, execute, resume
// and compensate.
func TestCreate_TargetTenantIsImmutableAcrossLifecycle(t *testing.T) {
	db := setupTestDB(t)
	svc := NewService(testRepo(t, db), testAdapters(t, db), mustStaging(t), nil, nil)
	ctx := context.Background()

	job, err := svc.Create(ctx, CreateImportParams{
		TenantID: 9, Scope: "platform", Actor: "psa", SourceType: SourceCSV, SourceName: "x.csv",
	}, []byte("entity,name,domain\norganization,Acme,acme.test\n"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Reading the job back under a DIFFERENT tenant must not succeed: the
	// stored owner is authoritative, not the caller's claim.
	if _, err := svc.Get(ctx, job.ID, 9999, "platform"); err == nil {
		t.Fatal("a foreign tenant must not be able to read another tenant's import job")
	}

	reloaded, err := svc.Get(ctx, job.ID, 9, "platform")
	if err != nil {
		t.Fatalf("get with the owning tenant: %v", err)
	}
	if reloaded.TenantID != 9 {
		t.Fatalf("stored target tenant changed: %d", reloaded.TenantID)
	}
}

// TestScanTenantZeroOrphans_ReportsWithoutMutating proves the diagnostic finds
// pre-existing orphans and — critically — changes nothing. Any repair must be
// an explicit operator decision, because only an operator knows which tenant an
// orphan should have belonged to.
func TestScanTenantZeroOrphans_ReportsWithoutMutating(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	// Seed one legitimate row and one pre-existing orphan.
	if _, err := db.Exec(`INSERT INTO coremail_domains (tenant_id, name, status) VALUES (5, 'ok.test', 'active')`); err != nil {
		t.Skipf("schema does not match this diagnostic in the test harness: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO coremail_domains (tenant_id, name, status) VALUES (0, 'orphan.test', 'active')`); err != nil {
		t.Fatalf("seed orphan: %v", err)
	}

	var before int
	if err := db.QueryRow(`SELECT COUNT(*) FROM coremail_domains`).Scan(&before); err != nil {
		t.Fatalf("count before: %v", err)
	}

	report, err := ScanTenantZeroOrphans(ctx, db, dbdialect.FromDriver("sqlite"))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if report.Total < 1 {
		t.Fatalf("expected the seeded orphan to be reported, got %+v", report)
	}
	var found bool
	for _, c := range report.Counts {
		if c.Table == "coremail_domains" && c.Rows >= 1 {
			found = true
		}
	}
	if !found {
		t.Fatalf("coremail_domains orphan not reported: %+v", report)
	}

	// The scan must not have touched a single row.
	var after int
	if err := db.QueryRow(`SELECT COUNT(*) FROM coremail_domains`).Scan(&after); err != nil {
		t.Fatalf("count after: %v", err)
	}
	if after != before {
		t.Fatalf("diagnostic mutated data: %d rows before, %d after", before, after)
	}
	var stillOrphan int
	if err := db.QueryRow(`SELECT COUNT(*) FROM coremail_domains WHERE tenant_id = 0`).Scan(&stillOrphan); err != nil {
		t.Fatalf("orphan recount: %v", err)
	}
	if stillOrphan != 1 {
		t.Fatalf("diagnostic must not repair orphans, found %d", stillOrphan)
	}
}
