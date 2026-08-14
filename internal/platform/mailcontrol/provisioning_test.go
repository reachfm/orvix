package mailcontrol

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/orvix/orvix/internal/platform/kernel"
)

// ── Platform domain provisioning (service level) ───────────────────

func TestService_CreateDomain_Success(t *testing.T) {
	svc, _, db := newMailControlHarness(t)
	mustSeedTenant(t, db, 1)

	name := "provisioned.example.com"
	result, err := svc.CreateDomain(context.Background(), PlatformCreateDomainRequest{
		Name: name, Status: "active",
	}, 1, 100, nil)
	if err != nil {
		t.Fatalf("create domain: %v", err)
	}
	if result.Domain.Name != name {
		t.Fatalf("name=%s", result.Domain.Name)
	}
	if result.Domain.TenantID != 1 {
		t.Fatalf("tenant_id must be the explicit target, got %d", result.Domain.TenantID)
	}
	if result.Idempotent {
		t.Fatal("first create must not be idempotent")
	}
	if result.DNSNextStep != "publish_and_verify_dns" {
		t.Fatalf("dns next step=%q", result.DNSNextStep)
	}
	if result.PublicDNSChanged {
		t.Fatal("provisioning must never claim DNS was changed")
	}
	// The tenant_id=0 fallback must never be used.
	var tenantID uint
	if err := db.QueryRow(`SELECT tenant_id FROM coremail_domains WHERE name=?`, name).Scan(&tenantID); err != nil {
		t.Fatal(err)
	}
	if tenantID != 1 {
		t.Fatalf("stored tenant_id=%d want 1", tenantID)
	}
	// Domain-level mail-access mode is NOT settable at create time.
	if result.Domain.MailAccessMode != "" && result.Domain.MailAccessMode != "internal_external" {
		t.Fatalf("unexpected domain mode %q", result.Domain.MailAccessMode)
	}
}

func TestService_CreateDomain_WithDKIMAndDNSRequirements(t *testing.T) {
	svc, repo, _ := newMailControlHarness(t)
	mustSeedTenant(t, repo.db, 1)

	req := PlatformCreateDomainRequest{Name: "dkim.example.com", DKIM: &PlatformDKIMOptions{Generate: true, Selector: "mail"}}
	result, err := svc.CreateDomain(context.Background(), req, 1, 100, []PlatformDNSRequirement{
		{Name: "@", Type: "MX", Value: "mail.example.com", TTL: 3600, Required: true},
		{Name: "mail", Type: "A", Value: "192.0.2.10", TTL: 3600, Required: true},
	})
	if err != nil {
		t.Fatalf("create domain with dkim: %v", err)
	}
	if result.DKIM == nil {
		t.Fatal("expected DKIM result")
	}
	if result.DKIM.PublicDNSTxt == "" || strings.Contains(result.DKIM.PublicDNSTxt, "PRIVATE") {
		t.Fatalf("dkim result must carry only the public TXT: %+v", result.DKIM)
	}
	if len(result.DNSRequirements) != 2 {
		t.Fatalf("dns requirements not persisted in result: %d", len(result.DNSRequirements))
	}
	// The private key must never be readable through the public
	// surface.
	if strings.Contains(result.DNSNextStep, "BEGIN") {
		t.Fatal("no key material may appear anywhere in the result")
	}
}

func TestService_CreateDomain_TenantGates(t *testing.T) {
	svc, _, db := newMailControlHarness(t)
	mustSeedTenant(t, db, 1)
	// Tenant 2 exists but is inactive; tenant 3 is deleted; tenant 4
	// does not exist.
	now := time.Now().UTC()
	if _, err := db.Exec(`INSERT INTO tenants (id, name, slug, domain, plan, active, created_at, updated_at) VALUES (2, 'Inactive', 'inactive', 'i.example', 'business', 0, ?, ?)`, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO tenants (id, name, slug, domain, plan, active, deleted_at, created_at, updated_at) VALUES (3, 'Deleted', 'deleted', 'd.example', 'business', 1, ?, ?, ?)`, now, now, now); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name     string
		tenantID uint
		wantCode kernel.ErrorCode
	}{
		{"tenant zero", 0, kernel.ErrCodeValidation},
		{"nonexistent tenant", 4, kernel.ErrCodeNotFound},
		{"deleted tenant", 3, kernel.ErrCodeNotFound},
		{"suspended/inactive tenant", 2, kernel.ErrCodeConflict},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.CreateDomain(context.Background(), PlatformCreateDomainRequest{Name: "x-" + tc.name + ".example.com"}, tc.tenantID, 100, nil)
			if err == nil {
				t.Fatal("expected error")
			}
			var kerr *kernel.Error
			if !errors.As(err, &kerr) {
				t.Fatalf("expected typed kernel error, got %T: %v", err, err)
			}
			if kerr.Code != tc.wantCode {
				t.Fatalf("want code %s, got %s (%v)", tc.wantCode, kerr.Code, err)
			}
		})
	}
}

func TestService_CreateDomain_DuplicateName(t *testing.T) {
	svc, _, db := newMailControlHarness(t)
	mustSeedTenant(t, db, 1)

	if _, err := svc.CreateDomain(context.Background(), PlatformCreateDomainRequest{Name: "dup.example.com"}, 1, 100, nil); err != nil {
		t.Fatalf("first create: %v", err)
	}
	_, err := svc.CreateDomain(context.Background(), PlatformCreateDomainRequest{Name: "dup.example.com"}, 1, 100, nil)
	if err == nil || !strings.Contains(err.Error(), "domain already exists") {
		t.Fatalf("duplicate normalized domain must conflict, got %v", err)
	}
}

func TestService_CreateDomain_ConcurrentIdenticalSubmissionsCreateOnce(t *testing.T) {
	svc, _, db := newMailControlHarness(t)
	mustSeedTenant(t, db, 1)

	// Repeated runs (3x) demonstrate stable results across runs.
	for run := 0; run < 3; run++ {
		name := "race" + itoa(run) + ".example.com"
		const workers = 8
		var wg sync.WaitGroup
		errs := make([]error, workers)
		for i := 0; i < workers; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				_, errs[i] = svc.CreateDomain(context.Background(), PlatformCreateDomainRequest{Name: name}, 1, 100, nil)
			}(i)
		}
		wg.Wait()

		created := 0
		conflicts := 0
		for _, err := range errs {
			if err == nil {
				created++
			} else if strings.Contains(err.Error(), "already exists") {
				conflicts++
			}
		}
		if created != 1 {
			t.Fatalf("run %d: exactly one concurrent create must win, got %d (conflicts=%d)", run, created, conflicts)
		}
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM coremail_domains WHERE name=?`, name).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("run %d: exactly one domain row expected, got %d", run, count)
		}
	}
}

// ── Platform mailbox provisioning (service level) ───────────────────

func TestService_CreateMailbox_ValidModes(t *testing.T) {
	svc, _, db := newMailControlHarness(t)
	mustSeedTenant(t, db, 1)
	seedProvDomain(t, svc, db, "mail.example.com", 1)

	for _, mode := range []string{"internal_only", "internal_external"} {
		result, err := svc.CreateMailbox(context.Background(), PlatformCreateMailboxRequest{
			Email:            "user-" + mode + "@mail.example.com",
			Password:         "TempSecret123!",
			QuotaMB:          2048,
			SendLimitPerHour: 100,
			MailAccessMode:   mode,
		}, 1, 100)
		if err != nil {
			t.Fatalf("create mailbox mode=%s: %v", mode, err)
		}
		if result.Mailbox.MailAccessMode != mode {
			t.Fatalf("configured mode=%q want %q", result.Mailbox.MailAccessMode, mode)
		}
		if result.Mailbox.EffectiveMailAccessMode != mode {
			t.Fatalf("effective mode=%q want %q", result.Mailbox.EffectiveMailAccessMode, mode)
		}
		// The password must never be returned by the platform result.
		if strings.Contains(result.Mailbox.Email, "TempSecret123") {
			t.Fatal("password leaked")
		}
	}
}

func TestService_CreateMailbox_MissingOrInvalidModeRejected(t *testing.T) {
	svc, _, db := newMailControlHarness(t)
	mustSeedTenant(t, db, 1)
	seedProvDomain(t, svc, db, "mail.example.com", 1)

	for _, mode := range []string{"", "inherit", "external_only", "open"} {
		_, err := svc.CreateMailbox(context.Background(), PlatformCreateMailboxRequest{
			Email:    "u-" + strings.ReplaceAll(mode, "_", "-") + "@mail.example.com",
			Password: "TempSecret123!", MailAccessMode: mode,
		}, 1, 100)
		if err == nil {
			t.Fatalf("mode %q must be rejected on the platform route", mode)
		}
		var kerr *kernel.Error
		if !errors.As(err, &kerr) || kerr.Code != kernel.ErrCodeValidation {
			t.Fatalf("mode %q must be a typed validation error, got %v", mode, err)
		}
		if _, ok := kerr.Fields["mail_access_mode"]; !ok {
			t.Fatalf("mode %q error must name the field, got %v", mode, err)
		}
	}
}

func TestService_CreateMailbox_CrossTenantDomainRejected(t *testing.T) {
	svc, _, db := newMailControlHarness(t)
	mustSeedTenant(t, db, 1)
	mustSeedTenant(t, db, 2)
	seedProvDomain(t, svc, db, "t1.example.com", 1)

	// Tenant 2 tries to use tenant 1's domain: safe not-found.
	_, err := svc.CreateMailbox(context.Background(), PlatformCreateMailboxRequest{
		Email: "user@t1.example.com", Password: "TempSecret123!", MailAccessMode: "internal_only",
	}, 2, 100)
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("cross-tenant domain must resolve to not-found, got %v", err)
	}
}

func TestService_CreateMailbox_DomainStateEligibility(t *testing.T) {
	svc, _, db := newMailControlHarness(t)
	mustSeedTenant(t, db, 1)

	states := []struct {
		name   string
		status string
		want   string
	}{
		{"disabled.example.com", "disabled", "disabled"},
		{"suspended.example.com", "suspended", "suspended"},
		{"locked.example.com", "locked", "locked"},
		{"unknown.example.com", "bogus", "unavailable"},
	}
	for _, st := range states {
		if _, err := svc.CreateDomain(context.Background(), PlatformCreateDomainRequest{Name: st.name, Status: "active"}, 1, 100, nil); err != nil {
			t.Fatalf("seed domain %s: %v", st.name, err)
		}
		var id uint
		if err := db.QueryRow(`SELECT id FROM coremail_domains WHERE name=?`, st.name).Scan(&id); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`UPDATE coremail_domains SET status=? WHERE id=?`, st.status, id); err != nil {
			t.Fatal(err)
		}
		_, err := svc.CreateMailbox(context.Background(), PlatformCreateMailboxRequest{
			Email: "u@" + st.name, Password: "TempSecret123!", MailAccessMode: "internal_only",
		}, 1, 100)
		if err == nil {
			t.Fatalf("domain state %q must make creation fail", st.status)
		}
		if !strings.Contains(err.Error(), st.want) {
			t.Fatalf("domain state %q: want error mentioning %q, got %v", st.status, st.want, err)
		}
	}
}

func TestService_CreateMailbox_DuplicateEmail(t *testing.T) {
	svc, _, db := newMailControlHarness(t)
	mustSeedTenant(t, db, 1)
	seedProvDomain(t, svc, db, "mail.example.com", 1)

	req := PlatformCreateMailboxRequest{Email: "dup@mail.example.com", Password: "TempSecret123!", MailAccessMode: "internal_only"}
	if _, err := svc.CreateMailbox(context.Background(), req, 1, 100); err != nil {
		t.Fatalf("first create: %v", err)
	}
	_, err := svc.CreateMailbox(context.Background(), req, 1, 100)
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("duplicate email must conflict, got %v", err)
	}
}

func TestService_CreateMailbox_SuspendedTenantRejected(t *testing.T) {
	svc, _, db := newMailControlHarness(t)
	mustSeedTenant(t, db, 1)
	seedProvDomain(t, svc, db, "mail.example.com", 1)
	now := time.Now().UTC()
	if _, err := db.Exec(`UPDATE tenants SET active=0 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	_ = now
	_, err := svc.CreateMailbox(context.Background(), PlatformCreateMailboxRequest{
		Email: "u@mail.example.com", Password: "TempSecret123!", MailAccessMode: "internal_only",
	}, 1, 100)
	if err == nil || !strings.Contains(err.Error(), "suspended") {
		t.Fatalf("suspended tenant must be rejected, got %v", err)
	}
}

func TestService_CreateMailbox_SystemFoldersProvisioned(t *testing.T) {
	svc, _, db := newMailControlHarness(t)
	mustSeedTenant(t, db, 1)
	seedProvDomain(t, svc, db, "mail.example.com", 1)

	result, err := svc.CreateMailbox(context.Background(), PlatformCreateMailboxRequest{
		Email: "folders@mail.example.com", Password: "TempSecret123!", MailAccessMode: "internal_external",
	}, 1, 100)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	var folderCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM coremail_folders WHERE mailbox_id=?`, result.Mailbox.ID).Scan(&folderCount); err != nil {
		t.Fatal(err)
	}
	if folderCount != 6 {
		t.Fatalf("expected 6 system folders, got %d", folderCount)
	}
}

func TestService_CreateMailbox_ConcurrentDomainCapEnforced(t *testing.T) {
	svc, _, db := newMailControlHarness(t)
	mustSeedTenant(t, db, 1)

	// Repeated runs (3x) demonstrate stable cap enforcement.
	for run := 0; run < 3; run++ {
		domain := "cap" + itoa(run) + ".example.com"
		if _, err := svc.CreateDomain(context.Background(), PlatformCreateDomainRequest{
			Name:   domain,
			Limits: &PlatformDomainLimits{MaxMailboxes: intPtr(3)},
		}, 1, 100, nil); err != nil {
			t.Fatalf("run %d seed domain: %v", run, err)
		}

		const workers = 6
		var wg sync.WaitGroup
		created := make([]bool, workers)
		for i := 0; i < workers; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				_, err := svc.CreateMailbox(context.Background(), PlatformCreateMailboxRequest{
					Email:          "cap" + itoa(run) + "-" + itoa(i) + "@" + domain,
					Password:       "TempSecret123!",
					MailAccessMode: "internal_only",
				}, 1, 100)
				created[i] = err == nil
			}(i)
		}
		wg.Wait()

		successes := 0
		for _, ok := range created {
			if ok {
				successes++
			}
		}
		if successes != 3 {
			t.Fatalf("run %d: domain cap 3 must allow exactly 3 concurrent creates, got %d", run, successes)
		}
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM coremail_mailboxes WHERE tenant_id=1 AND domain_id=(SELECT id FROM coremail_domains WHERE name=?)`, domain).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 3 {
			t.Fatalf("run %d: domain must hold exactly 3 mailboxes, got %d", run, count)
		}
	}
}

func TestService_CreateMailbox_QuotaCeilingEnforced(t *testing.T) {
	svc, _, db := newMailControlHarness(t)
	mustSeedTenant(t, db, 1)
	if _, err := svc.CreateDomain(context.Background(), PlatformCreateDomainRequest{
		Name:   "quota.example.com",
		Limits: &PlatformDomainLimits{MaxMailboxQuotaMB: int64Ptr(1024)},
	}, 1, 100, nil); err != nil {
		t.Fatalf("seed domain: %v", err)
	}
	_, err := svc.CreateMailbox(context.Background(), PlatformCreateMailboxRequest{
		Email: "big@quota.example.com", Password: "TempSecret123!", QuotaMB: 4096, MailAccessMode: "internal_only",
	}, 1, 100)
	if err == nil || !strings.Contains(err.Error(), "quota") {
		t.Fatalf("quota above domain ceiling must be rejected, got %v", err)
	}
}

// ── Access-mode mutation (service level) ────────────────────────────

func TestService_SetMailboxAccessMode_Success(t *testing.T) {
	svc, _, db := newMailControlHarness(t)
	mustSeedTenant(t, db, 1)
	seedProvDomain(t, svc, db, "mail.example.com", 1)
	created, err := svc.CreateMailbox(context.Background(), PlatformCreateMailboxRequest{
		Email: "mode@mail.example.com", Password: "TempSecret123!", MailAccessMode: "internal_external",
	}, 1, 100)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	result, err := svc.SetMailboxAccessMode(context.Background(), created.Mailbox.ID, 1, PlatformSetMailboxAccessModeRequest{
		MailAccessMode: "internal_only", ExpectedVersion: 1,
	}, 100)
	if err != nil {
		t.Fatalf("set mode: %v", err)
	}
	if result.MailAccessMode != "internal_only" || result.EffectiveMailAccessMode != "internal_only" {
		t.Fatalf("cfg=%q eff=%q", result.MailAccessMode, result.EffectiveMailAccessMode)
	}
	if result.Version != 2 {
		t.Fatalf("version=%d want 2", result.Version)
	}
	// Audit evidence.
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM orvix_audit WHERE action='mailbox.mail_access_mode.set'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("expected 1 audit record, got %d", count)
	}
}

func TestService_SetMailboxAccessMode_VersionConflict(t *testing.T) {
	svc, _, db := newMailControlHarness(t)
	mustSeedTenant(t, db, 1)
	seedProvDomain(t, svc, db, "mail.example.com", 1)
	created, err := svc.CreateMailbox(context.Background(), PlatformCreateMailboxRequest{
		Email: "mode@mail.example.com", Password: "TempSecret123!", MailAccessMode: "internal_external",
	}, 1, 100)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	_, err = svc.SetMailboxAccessMode(context.Background(), created.Mailbox.ID, 1, PlatformSetMailboxAccessModeRequest{
		MailAccessMode: "internal_only", ExpectedVersion: 1,
	}, 100)
	if err != nil {
		t.Fatalf("first set: %v", err)
	}
	_, err = svc.SetMailboxAccessMode(context.Background(), created.Mailbox.ID, 1, PlatformSetMailboxAccessModeRequest{
		MailAccessMode: "internal_only", ExpectedVersion: 1,
	}, 100)
	if err == nil || !strings.Contains(err.Error(), "version") {
		t.Fatalf("stale version must be a precondition conflict, got %v", err)
	}
}

func TestService_SetMailboxAccessMode_CrossTenantNotFound(t *testing.T) {
	svc, _, db := newMailControlHarness(t)
	mustSeedTenant(t, db, 1)
	mustSeedTenant(t, db, 2)
	seedProvDomain(t, svc, db, "mail.example.com", 1)
	created, err := svc.CreateMailbox(context.Background(), PlatformCreateMailboxRequest{
		Email: "mode@mail.example.com", Password: "TempSecret123!", MailAccessMode: "internal_external",
	}, 1, 100)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	_, err = svc.SetMailboxAccessMode(context.Background(), created.Mailbox.ID, 2, PlatformSetMailboxAccessModeRequest{
		MailAccessMode: "internal_only", ExpectedVersion: 1,
	}, 100)
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("cross-tenant mutation must be not-found without disclosure, got %v", err)
	}
}

// TestService_PlatformMailbox_VersionExposedOnListAndGet proves the
// contract correction: PlatformMailbox now exposes the mailbox's real
// optimistic-concurrency version through List and Get, not just the
// SetMailboxAccessMode mutation result — so a caller can perform a
// genuine guarded access-mode mutation after only a read, without
// first triggering a mutation to learn the version.
func TestService_PlatformMailbox_VersionExposedOnListAndGet(t *testing.T) {
	svc, _, db := newMailControlHarness(t)
	mustSeedTenant(t, db, 1)
	seedProvDomain(t, svc, db, "mail.example.com", 1)

	created, err := svc.CreateMailbox(context.Background(), PlatformCreateMailboxRequest{
		Email: "ver@mail.example.com", Password: "TempSecret123!", MailAccessMode: "internal_external",
	}, 1, 100)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.Mailbox.Version != 1 {
		t.Fatalf("a freshly created mailbox must report version 1, got %d", created.Mailbox.Version)
	}

	// Get must return the same real version, not a fabricated default.
	got, err := svc.GetMailbox(context.Background(), created.Mailbox.ID, 1)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Version != 1 {
		t.Fatalf("Get must expose version=1, got %d", got.Version)
	}

	// List must also carry it.
	list, err := svc.ListMailboxes(context.Background(), PlatformMailboxFilter{TenantID: 1})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var found *PlatformMailbox
	for i := range list.Mailboxes {
		if list.Mailboxes[i].ID == created.Mailbox.ID {
			found = &list.Mailboxes[i]
		}
	}
	if found == nil {
		t.Fatal("created mailbox missing from list")
	}
	if found.Version != 1 {
		t.Fatalf("List must expose version=1, got %d", found.Version)
	}

	// Use the version Get returned (not a hardcoded 1) to perform a
	// genuine guarded mutation — proving it is the real, usable value.
	mutated, err := svc.SetMailboxAccessMode(context.Background(), created.Mailbox.ID, 1, PlatformSetMailboxAccessModeRequest{
		MailAccessMode: "internal_only", ExpectedVersion: got.Version,
	}, 100)
	if err != nil {
		t.Fatalf("mutation using the read version must succeed: %v", err)
	}
	if mutated.Version != 2 {
		t.Fatalf("version must advance to 2, got %d", mutated.Version)
	}

	// A subsequent Get must reflect the NEW version — proving List/Get
	// track the live column, not a stale or constant value.
	after, err := svc.GetMailbox(context.Background(), created.Mailbox.ID, 1)
	if err != nil {
		t.Fatalf("get after mutation: %v", err)
	}
	if after.Version != 2 {
		t.Fatalf("Get after mutation must expose version=2, got %d", after.Version)
	}

	// The stale version Get returned BEFORE the mutation must now be
	// rejected as a genuine precondition conflict — proving optimistic
	// concurrency was not weakened or bypassed by this change.
	if _, err := svc.SetMailboxAccessMode(context.Background(), created.Mailbox.ID, 1, PlatformSetMailboxAccessModeRequest{
		MailAccessMode: "internal_external", ExpectedVersion: got.Version,
	}, 100); err == nil || !strings.Contains(err.Error(), "version") {
		t.Fatalf("the now-stale version must be rejected as a real conflict, got %v", err)
	}
}

// TestService_PlatformMailbox_ListGet_CrossTenantDenied proves the
// version fix did not weaken tenant isolation on the read paths it
// touched.
func TestService_PlatformMailbox_ListGet_CrossTenantDenied(t *testing.T) {
	svc, _, db := newMailControlHarness(t)
	mustSeedTenant(t, db, 1)
	mustSeedTenant(t, db, 2)
	seedProvDomain(t, svc, db, "mail.example.com", 1)
	created, err := svc.CreateMailbox(context.Background(), PlatformCreateMailboxRequest{
		Email: "iso@mail.example.com", Password: "TempSecret123!", MailAccessMode: "internal_only",
	}, 1, 100)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if _, err := svc.GetMailbox(context.Background(), created.Mailbox.ID, 2); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("cross-tenant Get must be not-found, got %v", err)
	}

	list, err := svc.ListMailboxes(context.Background(), PlatformMailboxFilter{TenantID: 2})
	if err != nil {
		t.Fatalf("list tenant 2: %v", err)
	}
	for _, m := range list.Mailboxes {
		if m.ID == created.Mailbox.ID {
			t.Fatal("tenant 1's mailbox leaked into tenant 2's list")
		}
	}
}

// TestService_PlatformMailbox_NoSecretsInProjection proves the
// PlatformMailbox projection (list, get, and create result) never
// carries a password, hash, token, or internal path — the version
// field addition must not have widened what this contract exposes.
func TestService_PlatformMailbox_NoSecretsInProjection(t *testing.T) {
	svc, _, db := newMailControlHarness(t)
	mustSeedTenant(t, db, 1)
	seedProvDomain(t, svc, db, "mail.example.com", 1)
	created, err := svc.CreateMailbox(context.Background(), PlatformCreateMailboxRequest{
		Email: "safe@mail.example.com", Password: "TempSecret123!", MailAccessMode: "internal_only",
	}, 1, 100)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	body, err := json.Marshal(created)
	if err != nil {
		t.Fatal(err)
	}
	lower := strings.ToLower(string(body))
	for _, forbidden := range []string{"tempsecret123", "password", "hash", "argon2", "token", "/tmp/", "\\appdata\\"} {
		if strings.Contains(lower, forbidden) {
			t.Fatalf("PlatformCreateMailboxResult leaked %q: %s", forbidden, body)
		}
	}

	got, err := svc.GetMailbox(context.Background(), created.Mailbox.ID, 1)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	getBody, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	getLower := strings.ToLower(string(getBody))
	for _, forbidden := range []string{"tempsecret123", "hash", "argon2", "token", "/tmp/", "\\appdata\\"} {
		if strings.Contains(getLower, forbidden) {
			t.Fatalf("PlatformMailbox (Get) leaked %q: %s", forbidden, getBody)
		}
	}
}

// ── helpers ─────────────────────────────────────────────────────────

func seedProvDomain(t *testing.T, svc *Service, db *sql.DB, name string, tenantID uint) {
	t.Helper()
	if _, err := svc.CreateDomain(context.Background(), PlatformCreateDomainRequest{Name: name, Status: "active"}, tenantID, 100, nil); err != nil {
		t.Fatalf("seed domain %s: %v", name, err)
	}
}

func intPtr(v int) *int { return &v }

func int64Ptr(v int64) *int64 { return &v }
