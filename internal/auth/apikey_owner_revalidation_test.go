package auth

import (
	"database/sql"
	"sync"
	"testing"
	"time"
)

// H-2 regression suite: API keys must never retain stale authority.
//
// Before this fix, apikey.validate() checked only the key row (active,
// expires_at, allowed_ips) and then handed the STORED role to the request. A
// key minted while its owner held platform_super_admin kept full platform
// access after the owner was demoted, suspended, or soft-deleted. See
// ORVIX_FINAL_SECURITY_AUDIT_REPORT H-2.
//
// These tests exercise the real manager against a real migrated database.

// ensureKeyOwner provisions (idempotently) the tenant and user that a test
// wants to mint an API key for, with an explicit id so the test can keep using
// fixed ids. H-2 requires a real, currently-authorized owner at mint time, so
// suites that previously minted keys for phantom user ids must provision them.
func ensureKeyOwner(t *testing.T, m *APIKeyManager, uid, tid uint, role Role) {
	t.Helper()
	sqlDB, d, err := m.dialect()
	if err != nil {
		t.Fatalf("dialect: %v", err)
	}
	now := time.Now().UTC()

	if tid > 0 {
		var n int
		_ = sqlDB.QueryRow("SELECT COUNT(*) FROM tenants WHERE id = "+d.Placeholder(1), tid).Scan(&n)
		if n == 0 {
			slug := "t" + itoaUint(tid)
			if _, err := sqlDB.Exec(
				"INSERT INTO tenants (id, created_at, updated_at, name, slug, domain, plan, active) VALUES ("+d.Placeholders(8)+")",
				tid, now, now, slug, slug, slug+".test", "enterprise", true); err != nil {
				t.Fatalf("insert tenant %d: %v", tid, err)
			}
		}
	}

	var n int
	_ = sqlDB.QueryRow("SELECT COUNT(*) FROM users WHERE id = "+d.Placeholder(1), uid).Scan(&n)
	var tenantArg any
	if tid == 0 {
		tenantArg = nil
	} else {
		tenantArg = tid
	}
	if n == 0 {
		if _, err := sqlDB.Exec(
			"INSERT INTO users (id, created_at, updated_at, email, password_hash, role, tenant_id, active, email_verified, token_version) VALUES ("+
				d.Placeholders(10)+")",
			uid, now, now, "owner"+itoaUint(uid)+"@example.test", "x", string(role), tenantArg, true, true, 0); err != nil {
			t.Fatalf("insert user %d: %v", uid, err)
		}
		return
	}
	if _, err := sqlDB.Exec(
		"UPDATE users SET role = "+d.Placeholder(1)+", tenant_id = "+d.Placeholder(2)+", active = "+d.Placeholder(3)+
			", deleted_at = NULL WHERE id = "+d.Placeholder(4),
		string(role), tenantArg, true, uid); err != nil {
		t.Fatalf("update user %d: %v", uid, err)
	}
}

func itoaUint(v uint) string {
	if v == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}

// seedUser inserts a users row and returns its id.
func seedUser(t *testing.T, m *APIKeyManager, role string, tenantID uint, active bool, tokenVersion int64) uint {
	t.Helper()
	sqlDB, d, err := m.dialect()
	if err != nil {
		t.Fatalf("dialect: %v", err)
	}
	now := time.Now().UTC()
	var tenant any
	if tenantID == 0 {
		tenant = nil
	} else {
		tenant = tenantID
		seedTenant(t, m, tenantID, true)
	}
	res, err := sqlDB.Exec(
		"INSERT INTO users (created_at, updated_at, email, password_hash, role, tenant_id, active, email_verified, token_version) VALUES ("+
			d.Placeholders(9)+")",
		now, now, uniqueEmail(t), "x", role, tenant, active, true, tokenVersion)
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("user id: %v", err)
	}
	return uint(id)
}

var emailSeq struct {
	sync.Mutex
	n int
}

func uniqueEmail(t *testing.T) string {
	t.Helper()
	emailSeq.Lock()
	defer emailSeq.Unlock()
	emailSeq.n++
	return t.Name() + "-" + time.Now().Format("150405.000000000") + "-" + string(rune('a'+emailSeq.n%26)) + "@example.test"
}

func seedTenant(t *testing.T, m *APIKeyManager, tenantID uint, active bool) {
	t.Helper()
	sqlDB, d, err := m.dialect()
	if err != nil {
		t.Fatalf("dialect: %v", err)
	}
	var exists int
	_ = sqlDB.QueryRow("SELECT COUNT(*) FROM tenants WHERE id = "+d.Placeholder(1), tenantID).Scan(&exists)
	if exists > 0 {
		setTenantActive(t, m, tenantID, active)
		return
	}
	now := time.Now().UTC()
	slug := t.Name() + "-" + time.Now().Format("150405.000000000")
	if _, err := sqlDB.Exec(
		"INSERT INTO tenants (id, created_at, updated_at, name, slug, domain, plan, active) VALUES ("+d.Placeholders(8)+")",
		tenantID, now, now, slug, slug, slug+".test", "enterprise", active); err != nil {
		t.Fatalf("insert tenant: %v", err)
	}
}

func setTenantActive(t *testing.T, m *APIKeyManager, tenantID uint, active bool) {
	t.Helper()
	sqlDB, d, err := m.dialect()
	if err != nil {
		t.Fatalf("dialect: %v", err)
	}
	if _, err := sqlDB.Exec("UPDATE tenants SET active = "+d.Placeholder(1)+" WHERE id = "+d.Placeholder(2), active, tenantID); err != nil {
		t.Fatalf("update tenant: %v", err)
	}
}

func execUser(t *testing.T, m *APIKeyManager, query string, args ...any) {
	t.Helper()
	sqlDB, _, err := m.dialect()
	if err != nil {
		t.Fatalf("dialect: %v", err)
	}
	if _, err := sqlDB.Exec(query, args...); err != nil {
		t.Fatalf("exec: %v", err)
	}
}

// --- positive control -------------------------------------------------------

func TestAPIKey_ValidActiveOwnerSucceeds(t *testing.T) {
	m := newAPIKeyTestManager(t)
	uid := seedUser(t, m, string(RoleTenantAdmin), 1, true, 0)

	key, _, err := m.Generate("k", uid, 1, string(RoleTenantAdmin), nil, 0)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	rec, err := m.Validate(key)
	if err != nil {
		t.Fatalf("expected a live key for an active owner to validate, got %v", err)
	}
	if rec.Role != string(RoleTenantAdmin) {
		t.Fatalf("expected role tenant_admin, got %s", rec.Role)
	}
}

// --- owner status -----------------------------------------------------------

func TestAPIKey_OwnerDeactivatedAfterMint_Denied(t *testing.T) {
	m := newAPIKeyTestManager(t)
	uid := seedUser(t, m, string(RoleTenantAdmin), 1, true, 0)
	key, _, err := m.Generate("k", uid, 1, string(RoleTenantAdmin), nil, 0)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	sqlDB, d, _ := m.dialect()
	execUser(t, m, "UPDATE users SET active = "+d.Placeholder(1)+" WHERE id = "+d.Placeholder(2), false, uid)
	_ = sqlDB

	if _, err := m.Validate(key); err == nil {
		t.Fatal("a key whose owner was deactivated must be denied")
	}
}

func TestAPIKey_OwnerSoftDeletedAfterMint_Denied(t *testing.T) {
	m := newAPIKeyTestManager(t)
	uid := seedUser(t, m, string(RoleTenantAdmin), 1, true, 0)
	key, _, _ := m.Generate("k", uid, 1, string(RoleTenantAdmin), nil, 0)

	_, d, _ := m.dialect()
	execUser(t, m, "UPDATE users SET deleted_at = "+d.Placeholder(1)+" WHERE id = "+d.Placeholder(2), time.Now().UTC(), uid)

	if _, err := m.Validate(key); err == nil {
		t.Fatal("a key whose owner was soft-deleted must be denied")
	}
}

func TestAPIKey_OwnerRowRemoved_Denied(t *testing.T) {
	m := newAPIKeyTestManager(t)
	uid := seedUser(t, m, string(RoleTenantAdmin), 1, true, 0)
	key, _, _ := m.Generate("k", uid, 1, string(RoleTenantAdmin), nil, 0)

	_, d, _ := m.dialect()
	execUser(t, m, "DELETE FROM users WHERE id = "+d.Placeholder(1), uid)

	if _, err := m.Validate(key); err == nil {
		t.Fatal("a key whose owner no longer exists must be denied")
	}
}

// --- the headline case: demoted platform super admin ------------------------

func TestAPIKey_DemotedPlatformSuperAdmin_LosesPlatformAccess(t *testing.T) {
	m := newAPIKeyTestManager(t)
	// A platform super admin has no tenant binding.
	uid := seedUser(t, m, string(RolePlatformSuperAdmin), 0, true, 0)
	key, _, err := m.Generate("psa", uid, 0, string(RolePlatformSuperAdmin), nil, 0)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if _, err := m.Validate(key); err != nil {
		t.Fatalf("sanity: PSA key should work while the owner is PSA: %v", err)
	}

	// Demote to a tenant-scoped role (which also requires a tenant).
	seedTenant(t, m, 7, true)
	_, d, _ := m.dialect()
	execUser(t, m, "UPDATE users SET role = "+d.Placeholder(1)+", tenant_id = "+d.Placeholder(2)+" WHERE id = "+d.Placeholder(3),
		string(RoleTenantReadOnly), 7, uid)

	rec, err := m.Validate(key)
	if err == nil {
		t.Fatalf("a demoted operator's old PSA key must be denied, got role=%s", rec.Role)
	}
}

func TestAPIKey_PromotionDoesNotBroadenOldKey(t *testing.T) {
	m := newAPIKeyTestManager(t)
	uid := seedUser(t, m, string(RoleTenantReadOnly), 1, true, 0)
	key, _, err := m.Generate("narrow", uid, 1, string(RoleTenantReadOnly), []string{"read"}, 0)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	// Promote the owner.
	_, d, _ := m.dialect()
	execUser(t, m, "UPDATE users SET role = "+d.Placeholder(1)+" WHERE id = "+d.Placeholder(2), string(RoleTenantAdmin), uid)

	rec, err := m.Validate(key)
	if err == nil {
		t.Fatalf("an old narrow key must not silently widen after promotion (got role=%s)", rec.Role)
	}
}

// --- token version (revocation events) --------------------------------------

func TestAPIKey_OwnerTokenVersionBumped_Denied(t *testing.T) {
	m := newAPIKeyTestManager(t)
	uid := seedUser(t, m, string(RoleTenantAdmin), 1, true, 0)
	key, _, err := m.Generate("k", uid, 1, string(RoleTenantAdmin), nil, 0)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if _, err := m.Validate(key); err != nil {
		t.Fatalf("sanity: key should validate before revocation: %v", err)
	}

	// Password reset / logout-all / global revocation bumps token_version.
	_, d, _ := m.dialect()
	execUser(t, m, "UPDATE users SET token_version = token_version + 1 WHERE id = "+d.Placeholder(1), uid)

	if _, err := m.Validate(key); err == nil {
		t.Fatal("a key minted before a global revocation must be denied")
	}
}

// TestAPIKey_LegacyKeyFailsSafeAfterRevocation covers the backward-compat
// policy: a key row predating owner_token_version defaults to 0, which still
// matches a never-revoked owner, but must stop working the moment that owner
// has any revocation event.
func TestAPIKey_LegacyKeyFailsSafeAfterRevocation(t *testing.T) {
	m := newAPIKeyTestManager(t)
	uid := seedUser(t, m, string(RoleTenantAdmin), 1, true, 0)
	key, rec, err := m.Generate("legacy", uid, 1, string(RoleTenantAdmin), nil, 0)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	// Simulate a pre-migration row.
	_, d, _ := m.dialect()
	execUser(t, m, "UPDATE api_keys SET owner_token_version = 0 WHERE id = "+d.Placeholder(1), rec.ID)

	if _, err := m.Validate(key); err != nil {
		t.Fatalf("a legacy key for a never-revoked owner should still work: %v", err)
	}

	execUser(t, m, "UPDATE users SET token_version = 5 WHERE id = "+d.Placeholder(1), uid)
	if _, err := m.Validate(key); err == nil {
		t.Fatal("a legacy key must not survive a revocation event")
	}
}

// --- tenant binding ---------------------------------------------------------

func TestAPIKey_OwnerTenantDeactivated_Denied(t *testing.T) {
	m := newAPIKeyTestManager(t)
	uid := seedUser(t, m, string(RoleTenantAdmin), 3, true, 0)
	key, _, err := m.Generate("k", uid, 3, string(RoleTenantAdmin), nil, 0)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	setTenantActive(t, m, 3, false)

	if _, err := m.Validate(key); err == nil {
		t.Fatal("a tenant key must be denied once its tenant is deactivated")
	}
}

func TestAPIKey_OwnerTenantSoftDeleted_Denied(t *testing.T) {
	m := newAPIKeyTestManager(t)
	uid := seedUser(t, m, string(RoleTenantAdmin), 4, true, 0)
	key, _, _ := m.Generate("k", uid, 4, string(RoleTenantAdmin), nil, 0)

	_, d, _ := m.dialect()
	execUser(t, m, "UPDATE tenants SET deleted_at = "+d.Placeholder(1)+" WHERE id = "+d.Placeholder(2), time.Now().UTC(), 4)

	if _, err := m.Validate(key); err == nil {
		t.Fatal("a tenant key must be denied once its tenant is soft-deleted")
	}
}

// TestAPIKey_CrossTenantBindingDenied proves a key bound to a different tenant
// than its owner cannot authenticate, even if every other field is valid.
func TestAPIKey_CrossTenantBindingDenied(t *testing.T) {
	m := newAPIKeyTestManager(t)
	uid := seedUser(t, m, string(RoleTenantAdmin), 5, true, 0)
	seedTenant(t, m, 6, true)
	key, rec, err := m.Generate("k", uid, 5, string(RoleTenantAdmin), nil, 0)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	// Re-point the key at a tenant the owner does not belong to.
	_, d, _ := m.dialect()
	execUser(t, m, "UPDATE api_keys SET tenant_id = "+d.Placeholder(1)+" WHERE id = "+d.Placeholder(2), 6, rec.ID)

	if _, err := m.Validate(key); err == nil {
		t.Fatal("a key bound to a foreign tenant must be denied")
	}
}

// TestAPIKey_PlatformKeyWithTenantBindingDenied pins that platform-super-admin
// keys are never tenant credentials.
func TestAPIKey_PlatformKeyWithTenantBindingDenied(t *testing.T) {
	m := newAPIKeyTestManager(t)
	uid := seedUser(t, m, string(RolePlatformSuperAdmin), 0, true, 0)
	key, rec, err := m.Generate("psa", uid, 0, string(RolePlatformSuperAdmin), nil, 0)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	seedTenant(t, m, 9, true)
	_, d, _ := m.dialect()
	execUser(t, m, "UPDATE api_keys SET tenant_id = "+d.Placeholder(1)+" WHERE id = "+d.Placeholder(2), 9, rec.ID)

	if _, err := m.Validate(key); err == nil {
		t.Fatal("a platform-super-admin key carrying a tenant binding must be denied")
	}
}

// TestAPIKey_MintingForUnauthorizedOwnerRejected proves a key cannot be
// created for a user who is not currently authorised at all.
func TestAPIKey_MintingForUnauthorizedOwnerRejected(t *testing.T) {
	m := newAPIKeyTestManager(t)
	uid := seedUser(t, m, string(RoleTenantAdmin), 1, false /* inactive */, 0)
	if _, _, err := m.Generate("k", uid, 1, string(RoleTenantAdmin), nil, 0); err == nil {
		t.Fatal("minting a key for an inactive user must fail")
	}
	if _, _, err := m.Generate("k", 999999, 1, string(RoleTenantAdmin), nil, 0); err == nil {
		t.Fatal("minting a key for a non-existent user must fail")
	}
}

// TestAPIKey_MintingWithMismatchedRoleRejected proves the caller cannot stamp
// a role onto a key that the owner does not currently hold.
func TestAPIKey_MintingWithMismatchedRoleRejected(t *testing.T) {
	m := newAPIKeyTestManager(t)
	uid := seedUser(t, m, string(RoleTenantReadOnly), 1, true, 0)
	if _, _, err := m.Generate("k", uid, 1, string(RolePlatformSuperAdmin), nil, 0); err == nil {
		t.Fatal("minting a platform key for a tenant-readonly owner must fail")
	}
}

// --- key state --------------------------------------------------------------

func TestAPIKey_RevokedKeyDenied(t *testing.T) {
	m := newAPIKeyTestManager(t)
	uid := seedUser(t, m, string(RoleTenantAdmin), 1, true, 0)
	key, rec, _ := m.Generate("k", uid, 1, string(RoleTenantAdmin), nil, 0)
	if err := m.RevokeScoped(rec.ID, uid); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if _, err := m.Validate(key); err == nil {
		t.Fatal("a revoked key must be denied")
	}
}

func TestAPIKey_ExpiredKeyDenied(t *testing.T) {
	m := newAPIKeyTestManager(t)
	uid := seedUser(t, m, string(RoleTenantAdmin), 1, true, 0)
	key, rec, _ := m.Generate("k", uid, 1, string(RoleTenantAdmin), nil, 0)

	_, d, _ := m.dialect()
	execUser(t, m, "UPDATE api_keys SET expires_at = "+d.Placeholder(1)+" WHERE id = "+d.Placeholder(2),
		time.Now().UTC().Add(-time.Hour), rec.ID)

	if _, err := m.Validate(key); err == nil {
		t.Fatal("an expired key must be denied")
	}
}

func TestAPIKey_IPRestrictedKeyDeniedFromWrongIP(t *testing.T) {
	m := newAPIKeyTestManager(t)
	uid := seedUser(t, m, string(RoleTenantAdmin), 1, true, 0)
	key, rec, _ := m.Generate("k", uid, 1, string(RoleTenantAdmin), nil, 0)
	if err := m.SetAllowedIPs(rec.ID, uid, "203.0.113.0/24"); err != nil {
		t.Fatalf("set allowed ips: %v", err)
	}
	if _, err := m.ValidateForIP(key, "198.51.100.9"); err == nil {
		t.Fatal("an IP-restricted key used from a disallowed address must be denied")
	}
	if _, err := m.ValidateForIP(key, "203.0.113.5"); err != nil {
		t.Fatalf("the key should work from an allowed address: %v", err)
	}
}

// --- concurrency ------------------------------------------------------------

// TestAPIKey_NoStaleAuthorizationWindowUnderConcurrency demotes the owner
// while many validations run, and asserts that no validation performed after
// the demotion committed still reports the old role. Every result must be
// either a clean success with the pre-demotion role (raced ahead of the
// update) or a denial — never a success carrying stale platform authority
// after the change is visible.
func TestAPIKey_NoStaleAuthorizationWindowUnderConcurrency(t *testing.T) {
	m := newAPIKeyTestManager(t)
	uid := seedUser(t, m, string(RolePlatformSuperAdmin), 0, true, 0)
	key, _, err := m.Generate("psa", uid, 0, string(RolePlatformSuperAdmin), nil, 0)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	_, d, _ := m.dialect()
	seedTenant(t, m, 11, true)

	var wg sync.WaitGroup
	var mu sync.Mutex
	staleAfterChange := 0
	demoted := make(chan struct{})

	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-demoted // only validate AFTER the demotion has committed
			if rec, err := m.Validate(key); err == nil && rec.Role == string(RolePlatformSuperAdmin) {
				mu.Lock()
				staleAfterChange++
				mu.Unlock()
			}
		}()
	}

	execUser(t, m, "UPDATE users SET role = "+d.Placeholder(1)+", tenant_id = "+d.Placeholder(2)+" WHERE id = "+d.Placeholder(3),
		string(RoleTenantReadOnly), 11, uid)
	close(demoted)
	wg.Wait()

	if staleAfterChange != 0 {
		t.Fatalf("%d validations still returned platform_super_admin after the owner was demoted", staleAfterChange)
	}
}

var _ = sql.ErrNoRows
