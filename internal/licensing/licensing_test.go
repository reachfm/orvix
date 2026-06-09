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

func generateTestKey(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil { t.Fatalf("generate key: %v", err) }
	return pub, priv
}

func signLicense(t *testing.T, lic *License, priv ed25519.PrivateKey) string {
	t.Helper()
	sig := lic.Signature
	lic.Signature = ""
	data, _ := json.Marshal(lic)
	lic.Signature = sig
	signature := ed25519.Sign(priv, data)
	return base64.StdEncoding.EncodeToString(signature)
}

func makeLicense(t *testing.T, edition Edition, daysValid int, machineID string) *License {
	t.Helper()
	lic := &License{
		LicenseID:    "ORV-TEST-001",
		Edition:      edition,
		IssuedAt:     time.Now().Add(-1 * time.Hour),
		ExpiresAt:    time.Now().Add(time.Duration(daysValid) * 24 * time.Hour),
		Features:     []string{"jmap", "admin", "backup"},
		MachineBinding: machineID,
	}
	return lic
}

func TestParseLicense(t *testing.T) {
	pub, priv := generateTestKey(t)
	SetPublicKey(pub)

	lic := makeLicense(t, EditionEnterprise, 365, "")
	lic.Signature = signLicense(t, lic, priv)

	data, err := SerializeLicense(lic)
	if err != nil { t.Fatalf("serialize: %v", err) }

	parsed, err := ParseLicense(data)
	if err != nil { t.Fatalf("parse: %v", err) }
	if parsed.Edition != EditionEnterprise {
		t.Fatalf("expected enterprise, got %s", parsed.Edition)
	}
}

func TestValidLicense(t *testing.T) {
	pub, priv := generateTestKey(t)
	SetPublicKey(pub)

	lic := makeLicense(t, EditionEnterprise, 365, "")
	lic.Signature = signLicense(t, lic, priv)

	result := ValidateLicense(lic)
	if !result.Valid {
		t.Fatalf("expected valid, errors: %v", result.Errors)
	}
}

func TestInvalidSignature(t *testing.T) {
	pub, _ := generateTestKey(t)
	SetPublicKey(pub)

	lic := makeLicense(t, EditionEnterprise, 365, "")
	lic.Signature = "AAAA" // invalid base64

	result := ValidateLicense(lic)
	if result.Valid {
		t.Fatal("expected invalid signature")
	}
}

func TestExpiredLicense(t *testing.T) {
	pub, priv := generateTestKey(t)
	SetPublicKey(pub)

	lic := makeLicense(t, EditionEnterprise, -30, "") // expired 30 days ago
	lic.Signature = signLicense(t, lic, priv)

	result := ValidateLicense(lic)
	if result.Valid {
		t.Fatal("expected expired license")
	}
}

func TestMachineBinding(t *testing.T) {
	pub, priv := generateTestKey(t)
	SetPublicKey(pub)

	machineID := GenerateMachineID()
	lic := makeLicense(t, EditionEnterprise, 365, machineID)
	lic.Signature = signLicense(t, lic, priv)

	result := ValidateLicense(lic)
	if !result.Valid {
		t.Fatalf("expected valid with matching machine, errors: %v", result.Errors)
	}
}

func TestMachineMismatch(t *testing.T) {
	pub, priv := generateTestKey(t)
	SetPublicKey(pub)

	lic := makeLicense(t, EditionEnterprise, 365, "different-machine-id")
	lic.Signature = signLicense(t, lic, priv)

	result := ValidateLicense(lic)
	if result.Valid {
		t.Fatal("expected invalid due to machine mismatch")
	}
}

func TestEditionCheck(t *testing.T) {
	editions := []Edition{EditionCommunity, EditionProfessional, EditionEnterprise, EditionDatacenter, EditionMSP}
	for _, e := range editions {
		if !e.Valid() {
			t.Errorf("expected valid edition: %s", e)
		}
	}
	if Edition("invalid").Valid() {
		t.Fatal("expected invalid edition")
	}
}

func TestHasFeature(t *testing.T) {
	pub, priv := generateTestKey(t)
	SetPublicKey(pub)

	lic := makeLicense(t, EditionEnterprise, 365, "")
	lic.Signature = signLicense(t, lic, priv)

	if !HasFeature(lic, "jmap") {
		t.Fatal("expected jmap feature")
	}
	if HasFeature(lic, "nonexistent") {
		t.Fatal("expected no nonexistent feature")
	}
	if HasFeature(nil, "anything") {
		t.Fatal("expected false for nil license")
	}
}

func TestServiceLoadLicense(t *testing.T) {
	pub, _ := generateTestKey(t)
	SetPublicKey(pub)

	svc := NewService("/nonexistent/license.json")
	status := svc.LoadLicense(context.Background())
	if status.Valid {
		t.Fatal("expected invalid for nonexistent file")
	}
}

func TestServiceSetLicenseFromJSON(t *testing.T) {
	pub, priv := generateTestKey(t)
	SetPublicKey(pub)

	lic := makeLicense(t, EditionEnterprise, 365, GenerateMachineID())
	lic.Signature = signLicense(t, lic, priv)

	data, _ := SerializeLicense(lic)

	svc := NewService("/nonexistent")
	if err := svc.SetLicenseFromJSON(context.Background(), data); err != nil {
		t.Fatalf("set license: %v", err)
	}

	if !svc.IsValid() {
		t.Fatal("expected valid license after loading")
	}
	if svc.CurrentEdition() != EditionEnterprise {
		t.Fatalf("expected enterprise, got %s", svc.CurrentEdition())
	}
}

func TestStatusWithUsage(t *testing.T) {
	pub, priv := generateTestKey(t)
	SetPublicKey(pub)

	lic := makeLicense(t, EditionEnterprise, 365, GenerateMachineID())
	lic.Signature = signLicense(t, lic, priv)

	svc := NewService("/nonexistent")
	svc.SetLicenseFromJSON(context.Background(), mustSerialize(t, lic))

	domainCount := func(ctx context.Context) (int64, error) { return 3, nil }
	mailboxCount := func(ctx context.Context) (int64, error) { return 25, nil }

	result := svc.StatusWithUsage(context.Background(), domainCount, mailboxCount)
	if result["edition"] != "enterprise" {
		t.Fatalf("expected enterprise, got %s", result["edition"])
	}
	usage, ok := result["usage"].(map[string]interface{})
	if !ok { t.Fatal("expected usage map") }
	if usage["domains"] != int64(3) { t.Fatalf("expected 3 domains, got %v", usage["domains"]) }
	if usage["mailboxes"] != int64(25) { t.Fatalf("expected 25 mailboxes, got %v", usage["mailboxes"]) }
}

func TestStatusWithUsageNoLicense(t *testing.T) {
	svc := NewService("/nonexistent")
	result := svc.StatusWithUsage(context.Background(), nil, nil)
	if result["edition"] != "community" {
		t.Fatalf("expected community, got %s", result["edition"])
	}
}

func TestGraceStateValid(t *testing.T) {
	pub, priv := generateTestKey(t)
	SetPublicKey(pub)

	lic := makeLicense(t, EditionEnterprise, 365, GenerateMachineID())
	lic.Signature = signLicense(t, lic, priv)

	svc := NewService("/nonexistent")
	svc.SetLicenseFromJSON(context.Background(), mustSerialize(t, lic))

	result := svc.StatusWithUsage(context.Background(), nil, nil)
	if result["graceState"] != "valid" {
		t.Fatalf("expected valid, got %s", result["graceState"])
	}
}

func TestGraceStateExpired(t *testing.T) {
	pub, priv := generateTestKey(t)
	SetPublicKey(pub)

	lic := makeLicense(t, EditionEnterprise, -10, GenerateMachineID()) // expired 10 days ago
	lic.Signature = signLicense(t, lic, priv)

	svc := NewService("/nonexistent")
	svc.SetLicenseFromJSON(context.Background(), mustSerialize(t, lic))

	result := svc.StatusWithUsage(context.Background(), nil, nil)
	if result["graceState"] != "expired" {
		t.Fatalf("expected expired, got %s", result["graceState"])
	}
}

func TestLimitsFromResponse(t *testing.T) {
	pub, priv := generateTestKey(t)
	SetPublicKey(pub)

	lic := makeLicense(t, EditionEnterprise, 365, GenerateMachineID())
	lic.DomainsLimit = 100
	lic.MailboxesLimit = 1000
	lic.StorageLimitGB = 500
	lic.Signature = signLicense(t, lic, priv)

	svc := NewService("/nonexistent")
	svc.SetLicenseFromJSON(context.Background(), mustSerialize(t, lic))

	result := svc.StatusWithUsage(context.Background(), nil, nil)
	limits, ok := result["limits"].(map[string]interface{})
	if !ok { t.Fatal("expected limits map") }
	if limits["domains"] != int64(100) { t.Fatalf("expected 100, got %v", limits["domains"]) }
	if limits["mailboxes"] != int64(1000) { t.Fatalf("expected 1000, got %v", limits["mailboxes"]) }
	if limits["storageGB"] != int64(500) { t.Fatalf("expected 500, got %v", limits["storageGB"]) }
}

func TestDefaultLimits(t *testing.T) {
	svc := NewService("/nonexistent")
	result := svc.StatusWithUsage(context.Background(), nil, nil)
	limits, ok := result["limits"].(map[string]interface{})
	if !ok { t.Fatal("expected limits map") }
	if limits["domains"] != int64(1) { t.Fatalf("expected 1, got %v", limits["domains"]) }
	if limits["mailboxes"] != int64(5) { t.Fatalf("expected 5, got %v", limits["mailboxes"]) }
}

func TestConcurrentValidation(t *testing.T) {
	pub, priv := generateTestKey(t)
	SetPublicKey(pub)

	lic := makeLicense(t, EditionEnterprise, 365, "")
	lic.Signature = signLicense(t, lic, priv)

	svc := NewService("/nonexistent")
	svc.SetLicenseFromJSON(context.Background(), mustSerialize(t, lic))

	t.Run("concurrent", func(t *testing.T) {
		t.Parallel()
		for i := 0; i < 10; i++ {
			status := svc.Status(context.Background())
			if status.Edition != EditionEnterprise {
				t.Errorf("expected enterprise, got %s", status.Edition)
			}
		}
	})
}

func mustSerialize(t *testing.T, lic *License) []byte {
	t.Helper()
	data, err := SerializeLicense(lic)
	if err != nil { t.Fatalf("serialize: %v", err) }
	return data
}
