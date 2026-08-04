package domain

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/orvix/orvix/internal/audit"
	"github.com/orvix/orvix/internal/coremail/dkim"
	_ "modernc.org/sqlite"
)

// newProvisioningTestDB builds the domain-package schema plus the tenants
// table the plan checks read. Every statement is plain SQL that is valid on
// BOTH SQLite and PostgreSQL apart from the AUTOINCREMENT/DATETIME spellings,
// which mirror the shipped SQLite migration exactly.
func newProvisioningTestDB(t *testing.T, plan string, maxDomains, maxMailboxes int) *sql.DB {
	t.Helper()
	db := newDomainTestDB(t)
	if _, err := db.Exec(`CREATE TABLE tenants (
		id INTEGER PRIMARY KEY,
		plan TEXT NOT NULL DEFAULT 'smb',
		max_domains INTEGER NOT NULL DEFAULT 0,
		max_mailboxes INTEGER NOT NULL DEFAULT 0,
		deleted_at DATETIME
	)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO tenants (id, plan, max_domains, max_mailboxes) VALUES (?, ?, ?, ?)`,
		5, plan, maxDomains, maxMailboxes); err != nil {
		t.Fatal(err)
	}
	return db
}

func newProvisioningService(db *sql.DB) *Service {
	return NewService(NewDomainAdminRepo(db), dkim.NewSQLRepo(db), audit.NewExtendedStore(db), nil)
}

func intPtr(v int) *int     { return &v }
func i64Ptr(v int64) *int64 { return &v }

// --- happy path ------------------------------------------------------------

func TestProvisionDomainNormalizesAndCreates(t *testing.T) {
	db := newProvisioningTestDB(t, "business", 10, 500)
	svc := newProvisioningService(db)

	res, err := svc.ProvisionDomain(context.Background(), CreateDomainRequest{
		Name:        "  EXAMPLE.Test.  ",
		Description: "Primary corporate domain",
		Status:      "active",
	}, 5)
	if err != nil {
		t.Fatalf("provision: %v", err)
	}
	if res.Domain.Name != "example.test" {
		t.Errorf("name = %q, want normalized example.test", res.Domain.Name)
	}
	if res.Domain.Status != "active" {
		t.Errorf("status = %q, want active", res.Domain.Status)
	}
	if res.Domain.Description != "Primary corporate domain" {
		t.Errorf("description not persisted: %q", res.Domain.Description)
	}
	if res.DNS == nil || res.DNS.PublicDNSChanged {
		t.Error("response must state that no public DNS was changed")
	}
	if res.DKIM != nil {
		t.Error("DKIM must not be generated when not requested")
	}
}

func TestProvisionDomainIDNAIsStoredAsPunycode(t *testing.T) {
	db := newProvisioningTestDB(t, "business", 10, 500)
	svc := newProvisioningService(db)

	res, err := svc.ProvisionDomain(context.Background(), CreateDomainRequest{Name: "münchen.de"}, 5)
	if err != nil {
		t.Fatalf("provision idna: %v", err)
	}
	if res.Domain.Name != "xn--mnchen-3ya.de" {
		t.Fatalf("stored name = %q, want punycode xn--mnchen-3ya.de", res.Domain.Name)
	}
	// The unicode spelling must now collide with the stored A-label, which is
	// the whole point of normalizing before the uniqueness check.
	if _, err := svc.ProvisionDomain(context.Background(), CreateDomainRequest{Name: "MÜNCHEN.DE"}, 5); err != ErrDomainAlreadyExists {
		t.Fatalf("unicode re-submit err = %v, want ErrDomainAlreadyExists", err)
	}
}

func TestProvisionDomainInvalidNameMatrix(t *testing.T) {
	db := newProvisioningTestDB(t, "business", 10, 500)
	svc := newProvisioningService(db)

	for _, name := range []string{
		"", "   ", "example", "https://example.com", "http://example.com/path",
		"example.com/path", "user@example.com", "exa mple.com", "*.example.com",
		"example.com:8080", "example.com#frag", "example.com?q=1",
		"-bad.com", "bad-.com", ".example.com", "example..com",
		strings.Repeat("a", 64) + ".com", "com", "co.uk",
	} {
		if _, err := svc.ProvisionDomain(context.Background(), CreateDomainRequest{Name: name}, 5); err != ErrInvalidDomainName {
			t.Errorf("ProvisionDomain(%q) err = %v, want ErrInvalidDomainName", name, err)
		}
	}
}

// --- isolation and authorization ------------------------------------------

func TestProvisionDomainRejectsMissingTenant(t *testing.T) {
	db := newProvisioningTestDB(t, "business", 10, 500)
	svc := newProvisioningService(db)
	if _, err := svc.ProvisionDomain(context.Background(), CreateDomainRequest{Name: "notenant.test"}, 0); err != ErrDomainForbidden {
		t.Fatalf("err = %v, want ErrDomainForbidden for a missing tenant", err)
	}
}

func TestProvisionDomainCrossTenantNameIsAPlainConflict(t *testing.T) {
	db := newProvisioningTestDB(t, "business", 10, 500)
	if _, err := db.Exec(`INSERT INTO tenants (id, plan, max_domains, max_mailboxes) VALUES (?, 'business', 10, 500)`, 6); err != nil {
		t.Fatal(err)
	}
	svc := newProvisioningService(db)

	if _, err := svc.ProvisionDomain(context.Background(), CreateDomainRequest{Name: "shared.test"}, 5); err != nil {
		t.Fatalf("tenant 5 provision: %v", err)
	}
	// Tenant 6 must get a bare conflict that reveals nothing about ownership,
	// and must NOT get the idempotent replay of tenant 5's domain even when
	// it supplies an idempotency key.
	res, err := svc.ProvisionDomain(context.Background(), CreateDomainRequest{
		Name:           "shared.test",
		IdempotencyKey: "abc",
	}, 6)
	if err != ErrDomainAlreadyExists {
		t.Fatalf("cross-tenant err = %v, want ErrDomainAlreadyExists", err)
	}
	if res != nil {
		t.Fatal("cross-tenant conflict must not return another tenant's domain")
	}
}

func TestProvisionDomainFailsClosedOnUnknownPlan(t *testing.T) {
	db := newDomainTestDB(t) // no tenants table row at all
	if _, err := db.Exec(`CREATE TABLE tenants (id INTEGER PRIMARY KEY, plan TEXT, max_domains INTEGER, max_mailboxes INTEGER, deleted_at DATETIME)`); err != nil {
		t.Fatal(err)
	}
	svc := newProvisioningService(db)

	if _, err := svc.ProvisionDomain(context.Background(), CreateDomainRequest{Name: "noplan.test"}, 5); err != ErrPlanUnavailable {
		t.Fatalf("err = %v, want ErrPlanUnavailable (fail closed)", err)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM coremail_domains WHERE name='noplan.test'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("a domain was created despite unknown plan data")
	}
}

// --- limits ----------------------------------------------------------------

func TestProvisionDomainOmittedLimitsInheritNotUnlimited(t *testing.T) {
	db := newProvisioningTestDB(t, "business", 10, 250)
	svc := newProvisioningService(db)

	res, err := svc.ProvisionDomain(context.Background(), CreateDomainRequest{Name: "inherit.test"}, 5)
	if err != nil {
		t.Fatalf("provision: %v", err)
	}
	if res.Domain.MaxMailboxes != LimitInherit {
		t.Errorf("stored max_mailboxes = %d, want LimitInherit(0)", res.Domain.MaxMailboxes)
	}
	eff := res.EffectiveLimits
	if !eff.MaxMailboxesInherited {
		t.Error("effective limits must report the mailbox cap as inherited")
	}
	if eff.MaxMailboxesUnlimited {
		t.Error("an inherited cap under a FINITE plan must not be reported as unlimited")
	}
	if eff.MaxMailboxes != 250 {
		t.Errorf("effective max_mailboxes = %d, want the plan ceiling 250", eff.MaxMailboxes)
	}
}

func TestProvisionDomainInheritedLimitUnderUnlimitedPlan(t *testing.T) {
	db := newProvisioningTestDB(t, "enterprise", 0, 0) // 0 = unlimited plan
	svc := newProvisioningService(db)

	res, err := svc.ProvisionDomain(context.Background(), CreateDomainRequest{Name: "unlimited.test"}, 5)
	if err != nil {
		t.Fatalf("provision: %v", err)
	}
	if !res.EffectiveLimits.MaxMailboxesUnlimited {
		t.Error("an inherited cap under an UNLIMITED plan must be reported unlimited")
	}
	if res.EffectiveLimits.MaxMailboxes != LimitUnlimited {
		t.Errorf("unlimited must be explicit (-1), got %d", res.EffectiveLimits.MaxMailboxes)
	}
	if res.Plan == nil || !res.Plan.MaxMailboxesUnlimited || res.Plan.RemainingMailboxes != nil {
		t.Error("unlimited plan must report remaining_mailboxes as null, never 0")
	}
}

func TestProvisionDomainFiniteLimitsWithinPlan(t *testing.T) {
	db := newProvisioningTestDB(t, "business", 10, 500)
	svc := newProvisioningService(db)

	res, err := svc.ProvisionDomain(context.Background(), CreateDomainRequest{
		Name: "finite.test",
		Limits: &DomainLimits{
			MaxMailboxes:          intPtr(50),
			MaxAliases:            intPtr(200),
			DefaultMailboxQuotaMB: i64Ptr(3072),
			MaxMailboxQuotaMB:     i64Ptr(10240),
		},
	}, 5)
	if err != nil {
		t.Fatalf("provision: %v", err)
	}
	if res.Domain.MaxMailboxes != 50 || res.Domain.MaxAliases != 200 || res.Domain.MaxQuotaMB != 10240 {
		t.Errorf("limits not persisted: %#v", res.Domain)
	}
	if res.Domain.DefaultMailboxQuotaMB != 3072 {
		t.Errorf("default quota = %d, want 3072", res.Domain.DefaultMailboxQuotaMB)
	}
	if res.EffectiveLimits.MaxMailboxesInherited || res.EffectiveLimits.MaxMailboxes != 50 {
		t.Errorf("effective limits wrong: %#v", res.EffectiveLimits)
	}
}

func TestProvisionDomainLimitAboveOrgAllowanceIsRejected(t *testing.T) {
	db := newProvisioningTestDB(t, "starter", 10, 100)
	svc := newProvisioningService(db)

	_, err := svc.ProvisionDomain(context.Background(), CreateDomainRequest{
		Name:   "toobig.test",
		Limits: &DomainLimits{MaxMailboxes: intPtr(500)},
	}, 5)
	if err == nil || !strings.Contains(err.Error(), ErrLimitExceedsPlan.Error()) {
		t.Fatalf("err = %v, want ErrLimitExceedsPlan", err)
	}
}

func TestProvisionDomainExhaustsRemainingAllocatableCapacity(t *testing.T) {
	db := newProvisioningTestDB(t, "starter", 10, 100)
	svc := newProvisioningService(db)
	ctx := context.Background()

	// First domain pins 80 of the plan's 100 mailboxes.
	if _, err := svc.ProvisionDomain(ctx, CreateDomainRequest{
		Name:   "first.test",
		Limits: &DomainLimits{MaxMailboxes: intPtr(80)},
	}, 5); err != nil {
		t.Fatalf("first provision: %v", err)
	}
	// 30 more would over-allocate the plan even though 30 < the 100 ceiling.
	_, err := svc.ProvisionDomain(ctx, CreateDomainRequest{
		Name:   "second.test",
		Limits: &DomainLimits{MaxMailboxes: intPtr(30)},
	}, 5)
	if err == nil || !strings.Contains(err.Error(), "remaining allocatable capacity") {
		t.Fatalf("err = %v, want a remaining-capacity rejection", err)
	}
	// 20 exactly fits.
	if _, err := svc.ProvisionDomain(ctx, CreateDomainRequest{
		Name:   "third.test",
		Limits: &DomainLimits{MaxMailboxes: intPtr(20)},
	}, 5); err != nil {
		t.Fatalf("exact-fit provision: %v", err)
	}
}

func TestProvisionDomainUnlimitedRequiresUnlimitedPlan(t *testing.T) {
	finite := newProvisioningTestDB(t, "starter", 10, 100)
	svc := newProvisioningService(finite)
	_, err := svc.ProvisionDomain(context.Background(), CreateDomainRequest{
		Name:   "wantunlimited.test",
		Limits: &DomainLimits{MaxMailboxes: intPtr(LimitUnlimited)},
	}, 5)
	if err == nil || !strings.Contains(err.Error(), ErrLimitExceedsPlan.Error()) {
		t.Fatalf("finite plan err = %v, want ErrLimitExceedsPlan", err)
	}
}

func TestProvisionDomainDefaultQuotaAboveMaximumIsRejected(t *testing.T) {
	db := newProvisioningTestDB(t, "business", 10, 500)
	svc := newProvisioningService(db)

	_, err := svc.ProvisionDomain(context.Background(), CreateDomainRequest{
		Name: "badquota.test",
		Limits: &DomainLimits{
			DefaultMailboxQuotaMB: i64Ptr(20480),
			MaxMailboxQuotaMB:     i64Ptr(10240),
		},
	}, 5)
	if err == nil || !strings.Contains(err.Error(), ErrLimitContradiction.Error()) {
		t.Fatalf("err = %v, want ErrLimitContradiction", err)
	}
}

func TestProvisionDomainRejectsNegativeAndOverflowingLimits(t *testing.T) {
	db := newProvisioningTestDB(t, "business", 10, 500)
	svc := newProvisioningService(db)

	cases := map[string]*DomainLimits{
		"negative mailboxes":     {MaxMailboxes: intPtr(-7)},
		"zero mailboxes":         {MaxMailboxes: intPtr(0)},
		"negative aliases":       {MaxAliases: intPtr(-2)},
		"zero default quota":     {DefaultMailboxQuotaMB: i64Ptr(0)},
		"negative default quota": {DefaultMailboxQuotaMB: i64Ptr(-1)},
		"overflowing max quota":  {MaxMailboxQuotaMB: i64Ptr(1 << 62)},
		"overflowing default":    {DefaultMailboxQuotaMB: i64Ptr(1 << 62)},
	}
	for name, limits := range cases {
		_, err := svc.ProvisionDomain(context.Background(), CreateDomainRequest{
			Name:   strings.ReplaceAll(name, " ", "-") + ".test",
			Limits: limits,
		}, 5)
		if err == nil || !strings.Contains(err.Error(), ErrInvalidLimit.Error()) {
			t.Errorf("%s: err = %v, want ErrInvalidLimit", name, err)
		}
	}
}

func TestMBToBytesIsOverflowSafe(t *testing.T) {
	if got := mbToBytes(-1); got != 0 {
		t.Errorf("mbToBytes(-1) = %d, want 0 (sentinel, not a negative byte count)", got)
	}
	if got := mbToBytes(0); got != 0 {
		t.Errorf("mbToBytes(0) = %d, want 0", got)
	}
	if got := mbToBytes(100); got != 100*1024*1024 {
		t.Errorf("mbToBytes(100) = %d", got)
	}
	// Saturates instead of wrapping to a negative number.
	if got := mbToBytes(1 << 62); got <= 0 {
		t.Errorf("mbToBytes(1<<62) = %d, want a saturated positive value", got)
	}
}

// --- plan ceiling ----------------------------------------------------------

func TestProvisionDomainRespectsPlanDomainCeiling(t *testing.T) {
	db := newProvisioningTestDB(t, "starter", 2, 500)
	svc := newProvisioningService(db)
	ctx := context.Background()

	for i := 0; i < 2; i++ {
		if _, err := svc.ProvisionDomain(ctx, CreateDomainRequest{Name: fmt.Sprintf("d%d.test", i)}, 5); err != nil {
			t.Fatalf("provision %d: %v", i, err)
		}
	}
	if _, err := svc.ProvisionDomain(ctx, CreateDomainRequest{Name: "overflow.test"}, 5); err != ErrDomainLimitReached {
		t.Fatalf("err = %v, want ErrDomainLimitReached", err)
	}
}

// --- DKIM atomicity and privacy -------------------------------------------

func TestProvisionDomainGeneratesDKIMAtomically(t *testing.T) {
	db := newProvisioningTestDB(t, "business", 10, 500)
	svc := newProvisioningService(db)

	res, err := svc.ProvisionDomain(context.Background(), CreateDomainRequest{
		Name: "dkim.test",
		DKIM: &DKIMOptions{Generate: true, Selector: "Mail"},
	}, 5)
	if err != nil {
		t.Fatalf("provision with dkim: %v", err)
	}
	if res.DKIM == nil {
		t.Fatal("DKIM result missing")
	}
	if res.DKIM.Selector != "mail" {
		t.Errorf("selector = %q, want normalized lowercase mail", res.DKIM.Selector)
	}
	if res.DKIM.DNSRecordName != "mail._domainkey.dkim.test" {
		t.Errorf("dns record name = %q", res.DKIM.DNSRecordName)
	}
	if !strings.Contains(res.DKIM.PublicDNSTxt, "p=") {
		t.Errorf("public TXT does not look like a DKIM record: %q", res.DKIM.PublicDNSTxt)
	}
	// The domain row must record the DKIM state in the same commit.
	var enabled int
	var selector string
	if err := db.QueryRow(`SELECT dkim_enabled, dkim_selector FROM coremail_domains WHERE id=?`, res.Domain.ID).Scan(&enabled, &selector); err != nil {
		t.Fatal(err)
	}
	if enabled != 1 || selector != "mail" {
		t.Errorf("domain dkim state not committed: enabled=%d selector=%q", enabled, selector)
	}
	// And the config row exists.
	var cfgCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM coremail_dkim_config WHERE domain='dkim.test'`).Scan(&cfgCount); err != nil {
		t.Fatal(err)
	}
	if cfgCount != 1 {
		t.Errorf("dkim config rows = %d, want 1", cfgCount)
	}
}

func TestProvisionDomainRejectsInvalidDKIMSelector(t *testing.T) {
	db := newProvisioningTestDB(t, "business", 10, 500)
	svc := newProvisioningService(db)

	for _, sel := range []string{"bad selector", "sel.with.dots", "-lead", "trail-", strings.Repeat("s", 64), "sel/slash"} {
		if _, err := svc.ProvisionDomain(context.Background(), CreateDomainRequest{
			Name: "sel.test",
			DKIM: &DKIMOptions{Generate: true, Selector: sel},
		}, 5); err != ErrInvalidDKIMSelector {
			t.Errorf("selector %q err = %v, want ErrInvalidDKIMSelector", sel, err)
		}
	}
}

// TestProvisionDomainRollsBackWhenDKIMFails proves the whole unit is atomic:
// a pre-existing DKIM config makes the in-transaction generate fail, and the
// domain insert that preceded it must be rolled back with it.
func TestProvisionDomainRollsBackWhenDKIMFails(t *testing.T) {
	db := newProvisioningTestDB(t, "business", 10, 500)
	svc := newProvisioningService(db)

	if _, err := db.Exec(`INSERT INTO coremail_dkim_config (domain, selector, private_key_pem, enabled, created_at, updated_at)
		VALUES ('rollback.test', 'mail', 'PLACEHOLDER', 1, '2026-01-01', '2026-01-01')`); err != nil {
		t.Fatal(err)
	}

	if _, err := svc.ProvisionDomain(context.Background(), CreateDomainRequest{
		Name: "rollback.test",
		DKIM: &DKIMOptions{Generate: true},
	}, 5); err != ErrDKIMAlreadyConfigured {
		t.Fatalf("err = %v, want ErrDKIMAlreadyConfigured", err)
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM coremail_domains WHERE name='rollback.test'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("domain survived a failed DKIM step: the transaction did not roll back")
	}
}

// TestProvisionDomainRollsBackWhenAuditFails proves an audit write failure
// also rolls the domain and its DKIM config back — provisioning is never
// recorded silently.
func TestProvisionDomainRollsBackWhenAuditFails(t *testing.T) {
	db := newProvisioningTestDB(t, "business", 10, 500)
	// Drop the audit table so RecordTx fails inside the transaction.
	if _, err := db.Exec(`DROP TABLE orvix_audit`); err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	svc := newProvisioningService(db)

	if _, err := svc.ProvisionDomain(context.Background(), CreateDomainRequest{
		Name: "auditfail.test",
		DKIM: &DKIMOptions{Generate: true},
	}, 5); err == nil {
		t.Fatal("provisioning must fail when the audit record cannot be written")
	}

	var domains, configs int
	if err := db.QueryRow(`SELECT COUNT(*) FROM coremail_domains WHERE name='auditfail.test'`).Scan(&domains); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM coremail_dkim_config WHERE domain='auditfail.test'`).Scan(&configs); err != nil {
		t.Fatal(err)
	}
	if domains != 0 || configs != 0 {
		t.Fatalf("audit failure left partial state: domains=%d dkim_configs=%d", domains, configs)
	}
}

// TestProvisionDomainNeverExposesPrivateKey is the privacy regression test:
// the provisioning result and the audit trail must contain the PUBLIC record
// only. It asserts on the real generated key, not a fixture.
func TestProvisionDomainNeverExposesPrivateKey(t *testing.T) {
	db := newProvisioningTestDB(t, "business", 10, 500)
	svc := newProvisioningService(db)

	res, err := svc.ProvisionDomain(context.Background(), CreateDomainRequest{
		Name: "private.test",
		DKIM: &DKIMOptions{Generate: true},
	}, 5)
	if err != nil {
		t.Fatalf("provision: %v", err)
	}

	// Read the key that WAS stored, so the assertions below compare against
	// real material rather than a guess.
	var storedKey string
	if err := db.QueryRow(`SELECT private_key_pem FROM coremail_dkim_config WHERE domain='private.test'`).Scan(&storedKey); err != nil {
		t.Fatal(err)
	}
	if storedKey == "" {
		t.Fatal("no private key was stored; the test would be vacuous")
	}

	rendered := fmt.Sprintf("%#v", res)
	for _, marker := range []string{"BEGIN RSA", "BEGIN PRIVATE", "PRIVATE KEY", storedKey} {
		if strings.Contains(rendered, marker) {
			t.Errorf("provisioning result leaks private key material (%q)", marker)
		}
	}

	// The audit trail must be equally clean.
	rows, err := db.Query(`SELECT action, before, after, reason FROM orvix_audit`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	seenProvision, seenDKIM := false, false
	for rows.Next() {
		var action, before, after, reason string
		if err := rows.Scan(&action, &before, &after, &reason); err != nil {
			t.Fatal(err)
		}
		switch action {
		case "domain.provision":
			seenProvision = true
		case "domain.dkim.generate":
			seenDKIM = true
		}
		blob := action + before + after + reason
		for _, marker := range []string{"BEGIN RSA", "BEGIN PRIVATE", "PRIVATE KEY", storedKey} {
			if strings.Contains(blob, marker) {
				t.Errorf("audit entry %q leaks private key material (%q)", action, marker)
			}
		}
	}
	if !seenProvision || !seenDKIM {
		t.Errorf("canonical audit events missing: provision=%t dkim=%t", seenProvision, seenDKIM)
	}
}

// --- duplicates and idempotency -------------------------------------------

func TestProvisionDomainDuplicateIsTypedConflict(t *testing.T) {
	db := newProvisioningTestDB(t, "business", 10, 500)
	svc := newProvisioningService(db)
	ctx := context.Background()

	if _, err := svc.ProvisionDomain(ctx, CreateDomainRequest{Name: "dup.test"}, 5); err != nil {
		t.Fatalf("first: %v", err)
	}
	if _, err := svc.ProvisionDomain(ctx, CreateDomainRequest{Name: "dup.test"}, 5); err != ErrDomainAlreadyExists {
		t.Fatalf("second err = %v, want ErrDomainAlreadyExists", err)
	}
}

func TestProvisionDomainIsIdempotentWithKey(t *testing.T) {
	db := newProvisioningTestDB(t, "business", 10, 500)
	svc := newProvisioningService(db)
	ctx := context.Background()

	first, err := svc.ProvisionDomain(ctx, CreateDomainRequest{Name: "idem.test", IdempotencyKey: "k-1"}, 5)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	if first.Idempotent {
		t.Error("first submission must not be marked idempotent")
	}
	second, err := svc.ProvisionDomain(ctx, CreateDomainRequest{Name: "idem.test", IdempotencyKey: "k-1"}, 5)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if !second.Idempotent {
		t.Error("replay must be marked idempotent")
	}
	if second.Domain.ID != first.Domain.ID {
		t.Errorf("replay returned a different domain: %d vs %d", second.Domain.ID, first.Domain.ID)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM coremail_domains WHERE name='idem.test'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("double submit created %d domains, want 1", count)
	}
}

// TestProvisionDomainConcurrentDoubleSubmit is the deterministic double-click
// test: exactly one of two simultaneous identical submissions creates the
// domain and the other receives the typed conflict — never two rows, never a
// raw SQL error.
func TestProvisionDomainConcurrentDoubleSubmit(t *testing.T) {
	db := newProvisioningTestDB(t, "business", 50, 5000)
	db.SetMaxOpenConns(1)
	svc := newProvisioningService(db)

	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = svc.ProvisionDomain(context.Background(), CreateDomainRequest{Name: "race.test"}, 5)
		}(i)
	}
	wg.Wait()

	success, conflict := 0, 0
	for _, err := range errs {
		switch {
		case err == nil:
			success++
		case err == ErrDomainAlreadyExists:
			conflict++
		default:
			t.Errorf("unexpected error: %v", err)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatalf("success=%d conflict=%d, want exactly 1/1", success, conflict)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM coremail_domains WHERE name='race.test'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("concurrent submit created %d rows, want 1", count)
	}
}

// --- plan summary ----------------------------------------------------------

func TestGetPlanSummaryReportsRealUsageAndExplicitUnlimited(t *testing.T) {
	db := newProvisioningTestDB(t, "business", 10, 500)
	svc := newProvisioningService(db)
	ctx := context.Background()

	res, err := svc.ProvisionDomain(ctx, CreateDomainRequest{
		Name:   "usage.test",
		Limits: &DomainLimits{MaxMailboxes: intPtr(40)},
	}, 5)
	if err != nil {
		t.Fatalf("provision: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO coremail_mailboxes (domain_id, tenant_id, used_bytes, msg_count) VALUES (?, 5, 4096, 2), (?, 5, 8192, 5)`,
		res.Domain.ID, res.Domain.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO coremail_aliases (domain_id, tenant_id) VALUES (?, 5)`, res.Domain.ID); err != nil {
		t.Fatal(err)
	}

	s, err := svc.GetPlanSummary(ctx, 5)
	if err != nil {
		t.Fatalf("plan summary: %v", err)
	}
	if s.Plan != "business" {
		t.Errorf("plan = %q", s.Plan)
	}
	if s.DomainsUsed != 1 || s.MailboxesUsed != 2 || s.AliasesUsed != 1 {
		t.Errorf("usage wrong: %#v", s)
	}
	if s.RemainingDomains == nil || *s.RemainingDomains != 9 {
		t.Errorf("remaining domains = %v, want 9", s.RemainingDomains)
	}
	if s.RemainingMailboxes == nil || *s.RemainingMailboxes != 498 {
		t.Errorf("remaining mailboxes = %v, want 498", s.RemainingMailboxes)
	}
	if s.MailboxesAllocated != 40 {
		t.Errorf("allocated = %d, want 40", s.MailboxesAllocated)
	}
	if s.StorageUsedBytes != 12288 {
		t.Errorf("storage used = %d, want 12288", s.StorageUsedBytes)
	}
	// No organization alias ceiling exists, so it must be reported as
	// explicitly unlimited with a null remaining — never a fake 0.
	if !s.MaxAliasesUnlimited || s.RemainingAliases != nil {
		t.Error("alias capacity must be explicitly unlimited with null remaining")
	}
}

func TestGetPlanSummaryFailsClosedWhenPlanMissing(t *testing.T) {
	db := newDomainTestDB(t)
	if _, err := db.Exec(`CREATE TABLE tenants (id INTEGER PRIMARY KEY, plan TEXT, max_domains INTEGER, max_mailboxes INTEGER, deleted_at DATETIME)`); err != nil {
		t.Fatal(err)
	}
	svc := newProvisioningService(db)
	if _, err := svc.GetPlanSummary(context.Background(), 5); err != ErrPlanUnavailable {
		t.Fatalf("err = %v, want ErrPlanUnavailable", err)
	}
}

// --- enforcement helpers ---------------------------------------------------

func TestResolveMailboxCap(t *testing.T) {
	cases := []struct {
		name          string
		domainMax     int
		orgMax        int
		wantLimit     int
		wantUnlimited bool
	}{
		{"explicit unlimited domain", LimitUnlimited, 100, 0, true},
		{"finite domain cap wins", 25, 100, 25, false},
		{"inherit finite plan", LimitInherit, 100, 100, false},
		{"inherit unlimited plan", LimitInherit, 0, 0, true},
		{"finite cap under unlimited plan", 25, 0, 25, false},
	}
	for _, tc := range cases {
		limit, unlimited := ResolveMailboxCap(tc.domainMax, tc.orgMax)
		if limit != tc.wantLimit || unlimited != tc.wantUnlimited {
			t.Errorf("%s: got (%d,%t), want (%d,%t)", tc.name, limit, unlimited, tc.wantLimit, tc.wantUnlimited)
		}
	}
}

func TestResolveAliasCap(t *testing.T) {
	if limit, unlimited := ResolveAliasCap(30); limit != 30 || unlimited {
		t.Errorf("finite alias cap: got (%d,%t)", limit, unlimited)
	}
	if _, unlimited := ResolveAliasCap(LimitInherit); !unlimited {
		t.Error("inheriting alias cap must be unlimited: no org alias ceiling exists")
	}
	if _, unlimited := ResolveAliasCap(LimitUnlimited); !unlimited {
		t.Error("explicit unlimited alias cap must be unlimited")
	}
}

func TestResolveQuotaBounds(t *testing.T) {
	// Inheriting: no ceiling, default falls back to the historic 1024.
	if maxMB, unlimited, def := ResolveQuotaBounds(LimitInherit, LimitInherit); !unlimited || maxMB != 0 || def != DefaultMailboxQuotaMB {
		t.Errorf("inherit: got (%d,%t,%d)", maxMB, unlimited, def)
	}
	// Finite ceiling with a domain default below it.
	if maxMB, unlimited, def := ResolveQuotaBounds(10240, 3072); unlimited || maxMB != 10240 || def != 3072 {
		t.Errorf("finite: got (%d,%t,%d)", maxMB, unlimited, def)
	}
	// A default above the ceiling is clamped, never stamped over it.
	if _, _, def := ResolveQuotaBounds(2048, 8192); def != 2048 {
		t.Errorf("default must clamp to the ceiling, got %d", def)
	}
}

func TestValidateDKIMSelector(t *testing.T) {
	if got, err := ValidateDKIMSelector(""); err != nil || got != "mail" {
		t.Errorf("empty selector = (%q,%v), want (mail,nil)", got, err)
	}
	if got, err := ValidateDKIMSelector("  ORVIX  "); err != nil || got != "orvix" {
		t.Errorf("normalization failed: (%q,%v)", got, err)
	}
	for _, bad := range []string{"a b", "a.b", "-x", "x-", "x/y", strings.Repeat("z", 64)} {
		if _, err := ValidateDKIMSelector(bad); err != ErrInvalidDKIMSelector {
			t.Errorf("ValidateDKIMSelector(%q) err = %v, want ErrInvalidDKIMSelector", bad, err)
		}
	}
}

// TestProvisionDomainRejectsUnsupportedInitialStatus locks the initial-status
// contract: only active and disabled may be chosen at provisioning time.
func TestProvisionDomainRejectsUnsupportedInitialStatus(t *testing.T) {
	db := newProvisioningTestDB(t, "business", 10, 500)
	svc := newProvisioningService(db)

	for _, status := range []string{"suspended", "locked", "pending", "verified", "deleted", "bogus"} {
		if _, err := svc.ProvisionDomain(context.Background(), CreateDomainRequest{
			Name:   "status.test",
			Status: status,
		}, 5); err != ErrInvalidDomainStatus {
			t.Errorf("status %q err = %v, want ErrInvalidDomainStatus", status, err)
		}
	}
	if _, err := svc.ProvisionDomain(context.Background(), CreateDomainRequest{
		Name:   "disabled-start.test",
		Status: "Disabled",
	}, 5); err != nil {
		t.Errorf("disabled must be an accepted initial status: %v", err)
	}
}

func TestProvisionDomainRejectsOverlongDescription(t *testing.T) {
	db := newProvisioningTestDB(t, "business", 10, 500)
	svc := newProvisioningService(db)
	if _, err := svc.ProvisionDomain(context.Background(), CreateDomainRequest{
		Name:        "longdesc.test",
		Description: strings.Repeat("x", MaxDomainDescriptionLen+1),
	}, 5); err != ErrDescriptionTooLong {
		t.Fatalf("err = %v, want ErrDescriptionTooLong", err)
	}
}

// TestProvisionDomainAcceptsLegacyFlatBody proves backward compatibility: the
// pre-wizard request shape still works and still produces the same limits.
func TestProvisionDomainAcceptsLegacyFlatBody(t *testing.T) {
	db := newProvisioningTestDB(t, "business", 10, 500)
	svc := newProvisioningService(db)

	res, err := svc.ProvisionDomain(context.Background(), CreateDomainRequest{
		Name:         "legacy.test",
		MaxMailboxes: 40,
		MaxAliases:   10,
		MaxQuotaMB:   2048,
	}, 5)
	if err != nil {
		t.Fatalf("legacy body: %v", err)
	}
	if res.Domain.MaxMailboxes != 40 || res.Domain.MaxAliases != 10 || res.Domain.MaxQuotaMB != 2048 {
		t.Errorf("legacy flat limits not applied: %#v", res.Domain)
	}
}

func TestProvisionDomainBareLegacyBodyInherits(t *testing.T) {
	db := newProvisioningTestDB(t, "business", 10, 500)
	svc := newProvisioningService(db)

	res, err := svc.ProvisionDomain(context.Background(), CreateDomainRequest{Name: "bare.test"}, 5)
	if err != nil {
		t.Fatalf("bare body: %v", err)
	}
	if res.Domain.MaxMailboxes != LimitInherit || res.Domain.MaxAliases != LimitInherit {
		t.Errorf("a bare body must inherit, not pin defaults: %#v", res.Domain)
	}
}

var _ = sql.ErrNoRows
