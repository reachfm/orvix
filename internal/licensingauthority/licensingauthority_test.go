package licensingauthority

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ── Cache Tests ──────────────────────────────────────────────

func TestCacheSaveLoad(t *testing.T) {
	dir := t.TempDir()
	cachePath := filepath.Join(dir, "cache.json")
	secret := "test-secret"

	cache := &EntitlementCache{
		LicenseID:                "test-license",
		Edition:                  "professional",
		Features:                 []string{"smtp", "imap"},
		Limits:                   EntitlementLimits{MaxDomains: 50, MaxMailboxes: 500, MaxStorageGB: 50},
		LastSuccessfulValidation: time.Now().Truncate(time.Second),
		NextValidation:           time.Now().Add(6 * time.Hour).Truncate(time.Second),
		GraceExpiresAt:           time.Now().Add(30 * 24 * time.Hour).Truncate(time.Second),
		AuthorityState:           AuthorityOnline,
	}

	if err := SaveCache(cache, cachePath, secret); err != nil {
		t.Fatalf("SaveCache: %v", err)
	}

	if _, err := os.Stat(cachePath); os.IsNotExist(err) {
		t.Fatal("cache file was not created")
	}

	loaded, err := LoadCache(cachePath, secret)
	if err != nil {
		t.Fatalf("LoadCache: %v", err)
	}

	if loaded.LicenseID != "test-license" {
		t.Fatalf("expected test-license, got %s", loaded.LicenseID)
	}
	if loaded.Edition != "professional" {
		t.Fatalf("expected professional, got %s", loaded.Edition)
	}
	if len(loaded.Features) != 2 {
		t.Fatalf("expected 2 features, got %d", len(loaded.Features))
	}
	if loaded.Limits.MaxDomains != 50 {
		t.Fatalf("expected 50 max domains, got %d", loaded.Limits.MaxDomains)
	}
	if loaded.AuthorityState != AuthorityOnline {
		t.Fatalf("expected online, got %s", loaded.AuthorityState)
	}
}

func TestCacheTamperDetection(t *testing.T) {
	dir := t.TempDir()
	cachePath := filepath.Join(dir, "cache.json")
	secret := "test-secret"

	cache := &EntitlementCache{
		LicenseID:                "test-license",
		Edition:                  "enterprise",
		Features:                 []string{"all"},
		Limits:                   EntitlementLimits{MaxDomains: 1000, MaxMailboxes: 10000, MaxStorageGB: 1000},
		LastSuccessfulValidation: time.Now(),
		NextValidation:           time.Now().Add(6 * time.Hour),
		GraceExpiresAt:           time.Now().Add(90 * 24 * time.Hour),
		AuthorityState:           AuthorityOnline,
	}
	SaveCache(cache, cachePath, secret)

	// Tamper with the cached file.
	data, _ := os.ReadFile(cachePath)
	tampered := strings.Replace(string(data), "enterprise", "datacenter", 1)
	os.WriteFile(cachePath, []byte(tampered), 0600)

	_, err := LoadCache(cachePath, secret)
	if err == nil {
		t.Fatal("expected tampering to be detected")
	}
	if !strings.Contains(err.Error(), "tampering detected") {
		t.Fatalf("expected tampering detected error, got: %v", err)
	}
}

func TestCacheWrongSecretDetected(t *testing.T) {
	dir := t.TempDir()
	cachePath := filepath.Join(dir, "cache.json")

	cache := &EntitlementCache{
		LicenseID:                "test-license",
		Edition:                  "professional",
		Features:                 []string{},
		Limits:                   EntitlementLimits{},
		LastSuccessfulValidation: time.Now(),
		NextValidation:           time.Now().Add(6 * time.Hour),
		GraceExpiresAt:           time.Now().Add(30 * 24 * time.Hour),
		AuthorityState:           AuthorityOnline,
	}
	SaveCache(cache, cachePath, "real-secret")

	_, err := LoadCache(cachePath, "wrong-secret")
	if err == nil {
		t.Fatal("expected load to fail with wrong secret")
	}
}

// ── Authority Client Tests ──────────────────────────────────

func TestNoopClientReturnsSafeDefaults(t *testing.T) {
	client := &NoopAuthorityClient{}

	vr, err := client.Validate(context.Background(), &ValidationRequest{LicenseID: "test"})
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if !vr.Valid {
		t.Fatal("expected valid response")
	}
	if vr.LicenseState != LicenseValid {
		t.Fatalf("expected LicenseValid, got %s", vr.LicenseState)
	}

	ar, err := client.Activate(context.Background(), &ActivationRequest{LicenseID: "test"})
	if err != nil {
		t.Fatalf("Activate: %v", err)
	}
	if !ar.Activated {
		t.Fatal("expected activated")
	}

	hr, err := client.Heartbeat(context.Background(), &HeartbeatRequest{LicenseID: "test"})
	if err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}
	if !hr.Acknowledged {
		t.Fatal("expected acknowledged")
	}

	er, err := client.Entitlements(context.Background(), &EntitlementRequest{LicenseID: "test"})
	if err != nil {
		t.Fatalf("Entitlements: %v", err)
	}
	if er.Edition != "community" {
		t.Fatalf("expected community edition, got %s", er.Edition)
	}
}

func TestFakeClientValidationSuccess(t *testing.T) {
	client := NewFakeAuthorityClient()

	vr, err := client.Validate(context.Background(), &ValidationRequest{
		LicenseID: "fake-123",
		Edition:   "professional",
		MachineID: "machine-1",
	})
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if !vr.Valid {
		t.Fatal("expected valid")
	}
}

func TestFakeClientValidationFailure(t *testing.T) {
	client := NewFakeAuthorityClient()
	client.SetValidateFn(func(req *ValidationRequest) (*ValidationResponse, error) {
		return &ValidationResponse{Valid: false, LicenseState: LicenseExpired, Reason: "license expired"}, nil
	})

	vr, err := client.Validate(context.Background(), &ValidationRequest{
		LicenseID: "expired-license",
	})
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if vr.Valid {
		t.Fatal("expected invalid")
	}
	if vr.LicenseState != LicenseExpired {
		t.Fatalf("expected LicenseExpired, got %s", vr.LicenseState)
	}
}

func TestFakeClientError(t *testing.T) {
	client := NewFakeAuthorityClient()
	client.SetValidateFn(func(req *ValidationRequest) (*ValidationResponse, error) {
		return nil, fmt.Errorf("network error")
	})

	_, err := client.Validate(context.Background(), &ValidationRequest{LicenseID: "test"})
	if err == nil {
		t.Fatal("expected error")
	}
}

// ── Service Tests ──────────────────────────────────────────

func TestServiceStatusIncludesAuthorityFields(t *testing.T) {
	svc := NewAuthorityService(&NoopAuthorityClient{}, "")

	status := svc.Status()
	if status == nil {
		t.Fatal("expected non-nil status")
	}

	if status.AuthorityState != AuthorityUnknown {
		t.Fatalf("expected AuthorityUnknown, got %s", status.AuthorityState)
	}
	if status.CacheValid != true {
		t.Fatal("expected cache valid")
	}
	if status.LicenseID != "community-default" {
		t.Fatalf("expected community-default, got %s", status.LicenseID)
	}
}

func TestServiceValidateSuccess(t *testing.T) {
	svc := NewAuthorityService(&NoopAuthorityClient{}, "")

	status := svc.ValidateWithAuthority(context.Background(), "test-license", "professional", "machine-1")
	if status == nil {
		t.Fatal("expected non-nil status")
	}
	if status.AuthorityState != AuthorityOnline {
		t.Fatalf("expected online, got %s", status.AuthorityState)
	}
	if status.LicenseState != LicenseValid {
		t.Fatalf("expected LicenseValid, got %s", status.LicenseState)
	}
	if !status.OfflineAllowed {
		t.Fatal("expected offline allowed")
	}
}

func TestServiceValidateFailure(t *testing.T) {
	fake := NewFakeAuthorityClient()
	fake.SetValidateFn(func(req *ValidationRequest) (*ValidationResponse, error) {
		return nil, fmt.Errorf("authority unreachable")
	})

	svc := NewAuthorityService(fake, "")

	status := svc.ValidateWithAuthority(context.Background(), "test-license", "professional", "machine-1")
	if status == nil {
		t.Fatal("expected non-nil status")
	}
	if status.AuthorityState != AuthorityOffline {
		t.Fatalf("expected offline, got %s", status.AuthorityState)
	}
	if !status.OfflineAllowed {
		t.Fatal("expected offline allowed initially")
	}
}

func TestOfflineOperationAllowed(t *testing.T) {
	fake := NewFakeAuthorityClient()
	svc := NewAuthorityService(fake, "")

	// Initially should allow offline.
	if !svc.CanOperateOffline() {
		t.Fatal("expected offline operation allowed initially")
	}

	// Validate with failing authority to trigger offline state.
	svc.ValidateWithAuthority(context.Background(), "test", "professional", "m1")

	// Should still allow offline (grace not expired).
	if !svc.CanOperateOffline() {
		t.Fatal("expected offline allowed during grace")
	}
}

func TestOfflineGraceExpired(t *testing.T) {
	fake := NewFakeAuthorityClient()
	svc := NewAuthorityService(fake, "")

	// Force a cache with an expired grace.
	svc.mu.Lock()
	svc.cache.GraceExpiresAt = time.Now().Add(-1 * time.Hour) // expired 1 hour ago
	svc.cache.AuthorityState = AuthorityOffline
	svc.cache.NextValidation = time.Now().Add(-1 * time.Hour) // past due
	svc.offlineSince = time.Now().Add(-48 * time.Hour)
	svc.mu.Unlock()

	if svc.CanOperateOffline() {
		t.Fatal("expected offline NOT allowed after grace expired")
	}

	status := svc.Status()
	if status.LicenseState != LicenseExpired {
		t.Fatalf("expected LicenseExpired, got %s", status.LicenseState)
	}
}

func TestServiceLoadCacheFromDisk(t *testing.T) {
	dir := t.TempDir()
	cachePath := filepath.Join(dir, "cache.json")

	// Create a cache to disk.
	svc1 := NewAuthorityService(&NoopAuthorityClient{}, cachePath)
	svc1.ValidateWithAuthority(context.Background(), "loaded-license", "enterprise", "m1")

	// Create new service that loads from disk.
	svc2 := NewAuthorityService(&NoopAuthorityClient{}, cachePath)

	if err := svc2.LoadCache(); err != nil {
		t.Fatalf("LoadCache: %v", err)
	}

	status := svc2.Status()
	if status.LicenseID != "loaded-license" {
		t.Fatalf("expected loaded-license, got %s", status.LicenseID)
	}
}

func TestServiceNoNetworkStartup(t *testing.T) {
	// NoopAuthorityClient never calls network — this verifies startup doesn't block.
	svc := NewAuthorityService(&NoopAuthorityClient{}, "")
	if svc == nil {
		t.Fatal("expected non-nil service on no-network startup")
	}

	status := svc.Status()
	if status == nil {
		t.Fatal("expected non-nil status on no-network startup")
	}
}

func TestAdminStatusIncludesAuthorityFields(t *testing.T) {
	svc := NewAuthorityService(&NoopAuthorityClient{}, "")
	status := svc.Status()

	// These fields must be present for admin visibility.
	fields := map[string]interface{}{
		"licenseId":      status.LicenseID,
		"edition":        status.Edition,
		"licenseState":   string(status.LicenseState),
		"authorityState": string(status.AuthorityState),
		"cacheValid":     status.CacheValid,
		"offlineAllowed": status.OfflineAllowed,
	}

	for field, val := range fields {
		if val == "" && field != "lastValidation" && field != "nextValidation" && field != "graceExpiresAt" {
			t.Fatalf("expected non-empty %s, got %v", field, val)
		}
	}

	// Verify JSON serialization for API response.
	data, err := json.Marshal(status)
	if err != nil {
		t.Fatalf("marshal status: %v", err)
	}

	var decoded map[string]interface{}
	json.Unmarshal(data, &decoded)

	for field := range fields {
		if _, ok := decoded[field]; !ok {
			t.Fatalf("expected field %s in JSON response", field)
		}
	}
}

func TestDefaultEntitlement(t *testing.T) {
	def := DefaultEntitlement()
	if def.Edition != "community" {
		t.Fatalf("expected community, got %s", def.Edition)
	}
	if def.LicenseID != "community-default" {
		t.Fatalf("expected community-default, got %s", def.LicenseID)
	}
	if def.Limits.MaxDomains != 1 {
		t.Fatalf("expected 1 max domain, got %d", def.Limits.MaxDomains)
	}
	if def.Limits.MaxMailboxes != 5 {
		t.Fatalf("expected 5 max mailboxes, got %d", def.Limits.MaxMailboxes)
	}
}

func TestSignCacheConsistency(t *testing.T) {
	secret := "consistent-secret"

	cache1 := &EntitlementCache{
		LicenseID:                "test",
		Edition:                  "professional",
		Features:                 []string{"smtp", "imap"},
		Limits:                   EntitlementLimits{MaxDomains: 50, MaxMailboxes: 500},
		LastSuccessfulValidation: time.Date(2026, 6, 9, 10, 0, 0, 0, time.UTC),
		NextValidation:           time.Date(2026, 6, 9, 16, 0, 0, 0, time.UTC),
		GraceExpiresAt:           time.Date(2026, 7, 9, 10, 0, 0, 0, time.UTC),
		AuthorityState:           AuthorityOnline,
	}

	cache2 := &EntitlementCache{
		LicenseID:                "test",
		Edition:                  "professional",
		Features:                 []string{"smtp", "imap"},
		Limits:                   EntitlementLimits{MaxDomains: 50, MaxMailboxes: 500},
		LastSuccessfulValidation: time.Date(2026, 6, 9, 10, 0, 0, 0, time.UTC),
		NextValidation:           time.Date(2026, 6, 9, 16, 0, 0, 0, time.UTC),
		GraceExpiresAt:           time.Date(2026, 7, 9, 10, 0, 0, 0, time.UTC),
		AuthorityState:           AuthorityOnline,
	}

	sig1 := SignCache(cache1, secret)
	sig2 := SignCache(cache2, secret)

	if sig1 != sig2 {
		t.Fatal("signatures should be consistent for identical cache data")
	}
}

func TestEntitlementLimitsJSON(t *testing.T) {
	limits := EntitlementLimits{
		MaxDomains:   100,
		MaxMailboxes: 1000,
		MaxStorageGB: 100,
		MaxNodes:     5,
		MaxTenants:   10,
		MaxChildren:  50,
	}

	data, err := json.Marshal(limits)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded EntitlementLimits
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded.MaxDomains != 100 || decoded.MaxMailboxes != 1000 || decoded.MaxNodes != 5 ||
		decoded.MaxTenants != 10 || decoded.MaxChildren != 50 {
		t.Fatal("JSON round-trip failed for EntitlementLimits")
	}
}

func TestFakeClientConcurrencySafe(t *testing.T) {
	client := NewFakeAuthorityClient()

	done := make(chan bool)
	for i := 0; i < 10; i++ {
		go func() {
			_, err := client.Validate(context.Background(), &ValidationRequest{LicenseID: "concurrent"})
			if err != nil {
				t.Errorf("concurrent validate: %v", err)
			}
			done <- true
		}()
	}

	for i := 0; i < 10; i++ {
		<-done
	}
}

func TestAuthorityStatusJSONIncludesAllFields(t *testing.T) {
	status := &AuthorityStatus{
		LicenseID:      "status-license",
		Edition:        "datacenter",
		LicenseState:   LicenseValid,
		AuthorityState: AuthorityOnline,
		LastValidation: time.Now(),
		NextValidation: time.Now().Add(6 * time.Hour),
		GraceExpiresAt: time.Now().Add(90 * 24 * time.Hour),
		CacheValid:     true,
		OfflineAllowed: true,
		OfflineSeconds: 0,
	}

	data, err := json.Marshal(status)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded map[string]interface{}
	json.Unmarshal(data, &decoded)

	requiredFields := []string{
		"licenseId", "edition", "licenseState", "authorityState",
		"lastValidation", "nextValidation", "graceExpiresAt",
		"cacheValid", "offlineAllowed", "offlineSeconds",
	}

	for _, field := range requiredFields {
		if _, ok := decoded[field]; !ok {
			t.Fatalf("missing required field: %s", field)
		}
	}
}
