package licensing

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"
)

func testEnforcer(t *testing.T, edition Edition, domainLimit, mailboxLimit int64) *EnforcementService {
	t.Helper()
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	SetPublicKey(pub)

	lic := &License{
		LicenseID:      "ORV-TEST-ENFORCE",
		Edition:        edition,
		IssuedAt:       time.Now().Add(-1 * time.Hour),
		ExpiresAt:      time.Now().Add(365 * 24 * time.Hour),
		DomainsLimit:   domainLimit,
		MailboxesLimit: mailboxLimit,
		Features:       []string{"jmap", "admin"},
		MachineBinding: GenerateMachineID(),
	}
	lic.Signature = signLicenseKey(t, lic, priv)

	svc := NewService("/nonexistent")
	svc.SetLicenseFromJSON(context.Background(), mustSerializeKey(t, lic))

	domainCount := func(ctx context.Context) (int64, error) { return int64(domainLimit), nil }
	mailboxCount := func(ctx context.Context) (int64, error) { return int64(mailboxLimit), nil }

	return NewEnforcementService(svc, domainCount, mailboxCount)
}

func signLicenseKey(t *testing.T, lic *License, priv ed25519.PrivateKey) string {
	t.Helper()
	sig := lic.Signature
	lic.Signature = ""
	data, _ := json.Marshal(lic)
	lic.Signature = sig
	return base64Encode(ed25519.Sign(priv, data))
}

func base64Encode(data []byte) string {
	return base64.StdEncoding.EncodeToString(data)
}

func mustSerializeKey(t *testing.T, lic *License) []byte {
	t.Helper()
	sig := lic.Signature
	lic.Signature = ""
	wrapper := struct {
		License   *License `json:"license"`
		Signature string   `json:"signature"`
	}{License: lic, Signature: sig}
	lic.Signature = sig
	data, _ := json.Marshal(wrapper)
	return data
}

func TestCanCreateDomainBelowLimit(t *testing.T) {
	enf := testEnforcer(t, EditionEnterprise, 10, 100)
	// Domain count equals limit, so at limit.
	ok, msg := enf.CanCreateDomain(context.Background())
	if ok {
		t.Fatalf("expected blocked at limit, msg: %s", msg)
	}
}

func TestCanCreateDomainUnlimited(t *testing.T) {
	enf := testEnforcer(t, EditionEnterprise, 0, 0)
	ok, msg := enf.CanCreateDomain(context.Background())
	if !ok {
		t.Fatalf("expected allowed for unlimited, msg: %s", msg)
	}
}

func TestCanCreateMailboxBelowLimit(t *testing.T) {
	enf := testEnforcer(t, EditionEnterprise, 0, 100)
	ok, msg := enf.CanCreateMailbox(context.Background())
	if ok {
		t.Fatalf("expected blocked at limit, msg: %s", msg)
	}
}

func TestCanCreateMailboxUnlimited(t *testing.T) {
	enf := testEnforcer(t, EditionEnterprise, 0, 0)
	ok, msg := enf.CanCreateMailbox(context.Background())
	if !ok {
		t.Fatalf("expected allowed for unlimited, msg: %s", msg)
	}
}

func TestCommunityDomainLimit(t *testing.T) {
	svc := NewService("/nonexistent")
	domainCount := func(ctx context.Context) (int64, error) { return 1, nil }
	mailboxCount := func(ctx context.Context) (int64, error) { return 0, nil }
	enf := NewEnforcementService(svc, domainCount, mailboxCount)

	ok, msg := enf.CanCreateDomain(context.Background())
	if ok {
		t.Fatal("expected blocked at community domain limit (1)")
	}
	_ = msg
}

func TestCommunityMailboxLimit(t *testing.T) {
	svc := NewService("/nonexistent")
	domainCount := func(ctx context.Context) (int64, error) { return 0, nil }
	mailboxCount := func(ctx context.Context) (int64, error) { return 5, nil }
	enf := NewEnforcementService(svc, domainCount, mailboxCount)

	ok, msg := enf.CanCreateMailbox(context.Background())
	if ok {
		t.Fatal("expected blocked at community mailbox limit (5)")
	}
	_ = msg
}

func TestCheckDomainLimit(t *testing.T) {
	enf := testEnforcer(t, EditionEnterprise, 10, 0)
	used, limit, err := enf.CheckDomainLimit(context.Background())
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if used != 10 {
		t.Fatalf("expected used 10, got %d", used)
	}
	if limit != 10 {
		t.Fatalf("expected limit 10, got %d", limit)
	}
}

func TestCheckMailboxLimit(t *testing.T) {
	enf := testEnforcer(t, EditionEnterprise, 0, 100)
	used, limit, err := enf.CheckMailboxLimit(context.Background())
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if used != 100 {
		t.Fatalf("expected used 100, got %d", used)
	}
	if limit != 100 {
		t.Fatalf("expected limit 100, got %d", limit)
	}
}

func TestNoLicenseServiceAllowsCreates(t *testing.T) {
	enf := NewEnforcementService(nil, nil, nil)
	ok, _ := enf.CanCreateDomain(context.Background())
	if !ok {
		t.Fatal("expected allowed with no license service")
	}
	ok, _ = enf.CanCreateMailbox(context.Background())
	if !ok {
		t.Fatal("expected allowed with no license service")
	}
}

func TestExpiredLicenseStillEnforces(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	SetPublicKey(pub)

	lic := &License{
		LicenseID:      "ORV-EXPIRED",
		Edition:        EditionEnterprise,
		IssuedAt:       time.Now().Add(-60 * 24 * time.Hour),
		ExpiresAt:      time.Now().Add(-1 * 24 * time.Hour), // expired yesterday
		DomainsLimit:   5,
		MailboxesLimit: 50,
		MachineBinding: GenerateMachineID(),
	}
	lic.Signature = signLicenseKey(t, lic, priv)

	svc := NewService("/nonexistent")
	svc.SetLicenseFromJSON(context.Background(), mustSerializeKey(t, lic))

	domainCount := func(ctx context.Context) (int64, error) { return 5, nil }
	mailboxCount := func(ctx context.Context) (int64, error) { return 50, nil }
	enf := NewEnforcementService(svc, domainCount, mailboxCount)

	// Even expired, limits should still be checked (not enforced harder — same limits).
	ok, _ := enf.CanCreateDomain(context.Background())
	if ok {
		t.Fatal("expected blocked at limit even with expired license")
	}
}
