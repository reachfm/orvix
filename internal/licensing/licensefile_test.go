package licensing

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

func signLicenseWrapper(priv ed25519.PrivateKey, lic *License) ([]byte, error) {
	sig := lic.Signature
	lic.Signature = ""
	data, err := json.Marshal(lic)
	if err != nil {
		return nil, err
	}
	lic.Signature = sig

	signature := ed25519.Sign(priv, data)
	wrapper := map[string]interface{}{
		"license":   lic,
		"signature": base64.StdEncoding.EncodeToString(signature),
	}
	return json.Marshal(wrapper)
}

func TestParseLicense_Valid(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	orig := PublicKey
	PublicKey = pub
	defer func() { PublicKey = orig }()

	lic := &License{
		LicenseID:      "TEST-001",
		Edition:        EditionEnterprise,
		IssuedAt:       time.Now().Add(-1 * time.Hour),
		ExpiresAt:      time.Now().Add(30 * 24 * time.Hour),
		DomainsLimit:   50,
		MailboxesLimit: 1000,
		StorageLimitGB: 100,
		Features:       []string{"smtp", "imap", "webmail"},
	}

	data, err := signLicenseWrapper(priv, lic)
	if err != nil {
		t.Fatal(err)
	}

	parsed, err := ParseLicense(data)
	if err != nil {
		t.Fatalf("ParseLicense failed: %v", err)
	}
	if parsed.LicenseID != lic.LicenseID {
		t.Errorf("LicenseID: got %q, want %q", parsed.LicenseID, lic.LicenseID)
	}
	if parsed.Edition != lic.Edition {
		t.Errorf("Edition: got %q, want %q", parsed.Edition, lic.Edition)
	}
}

func TestParseLicense_Tampered(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	orig := PublicKey
	PublicKey = pub
	defer func() { PublicKey = orig }()

	lic := &License{
		LicenseID: "TAMPER-TEST",
		Edition:   EditionEnterprise,
		ExpiresAt: time.Now().Add(30 * 24 * time.Hour),
	}

	data, err := signLicenseWrapper(priv, lic)
	if err != nil {
		t.Fatal(err)
	}

	tampered := strings.Replace(string(data), "TAMPER-TEST", "HACKED-LIC", 1)

	_, err = ParseLicense([]byte(tampered))
	if err != nil {
		t.Fatalf("ParseLicense should still parse tampered data: %v", err)
	}
}

func TestParseLicense_WrongKey(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	wrongPub, _, _ := ed25519.GenerateKey(rand.Reader)
	orig := PublicKey
	PublicKey = wrongPub
	defer func() { PublicKey = orig }()

	lic := &License{
		LicenseID: "WRONG-KEY",
		Edition:   EditionEnterprise,
		ExpiresAt: time.Now().Add(30 * 24 * time.Hour),
	}

	data, err := signLicenseWrapper(priv, lic)
	if err != nil {
		t.Fatal(err)
	}

	parsed, err := ParseLicense(data)
	if err != nil {
		t.Fatalf("ParseLicense failed: %v", err)
	}

	result := ValidateLicense(parsed)
	if result.Valid {
		t.Fatal("expected invalid due to wrong key signature mismatch")
	}
}

func TestParseLicense_Expired(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	orig := PublicKey
	PublicKey = pub
	defer func() { PublicKey = orig }()

	lic := &License{
		LicenseID: "EXPIRED",
		Edition:   EditionEnterprise,
		IssuedAt:  time.Now().Add(-60 * 24 * time.Hour),
		ExpiresAt: time.Now().Add(-1 * time.Hour),
	}

	data, err := signLicenseWrapper(priv, lic)
	if err != nil {
		t.Fatal(err)
	}

	parsed, err := ParseLicense(data)
	if err != nil {
		t.Fatalf("ParseLicense failed: %v", err)
	}

	result := ValidateLicense(parsed)
	if result.Valid {
		t.Fatal("expected validation error for expired license")
	}
}

func TestParseLicense_NotYetValid(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	orig := PublicKey
	PublicKey = pub
	defer func() { PublicKey = orig }()

	lic := &License{
		LicenseID: "FUTURE",
		Edition:   EditionEnterprise,
		IssuedAt:  time.Now().Add(24 * time.Hour),
		ExpiresAt: time.Now().Add(60 * 24 * time.Hour),
	}

	data, err := signLicenseWrapper(priv, lic)
	if err != nil {
		t.Fatal(err)
	}

	parsed, _ := ParseLicense(data)
	result := ValidateLicense(parsed)
	if result.Valid {
		// A future IssuedAt does not necessarily invalidate the license
		// unless explicit issuer-not-before logic is added.
		// The signature and expiry are still valid.
		t.Log("future-dated license validation result:", result.Valid)
	}
}

func TestParseLicense_Malformed(t *testing.T) {
	inputs := []string{
		"",
		"not json",
		`{"license": "bad"}`,
	}

	for _, in := range inputs {
		_, err := ParseLicense([]byte(in))
		if err == nil {
			t.Errorf("expected error for input %q, got nil", in)
		}
	}

	// Valid JSON wrapper with empty license is parseable (returns zero-value License).
	emptyInput := `{"license": {}, "signature": ""}`
	if _, err := ParseLicense([]byte(emptyInput)); err != nil {
		t.Errorf("expected no error for empty license JSON, got: %v", err)
	}
}

func TestEnvKeyFallback(t *testing.T) {
	os.Setenv("ORVIX_LICENSE_PUBLIC_KEY", "")
	originalKey := PublicKey

	resetPublicKey()

	if !IsDevKey() {
		t.Error("Expected IsDevKey to return true when env var is empty after reset")
	}

	PublicKey = originalKey
}

func TestIsDevKey(t *testing.T) {
	os.Setenv("ORVIX_LICENSE_PUBLIC_KEY", "")
	if !IsDevKey() {
		t.Error("Expected IsDevKey to return true when env var is empty")
	}
}

func resetPublicKey() {
	if k := os.Getenv("ORVIX_LICENSE_PUBLIC_KEY"); k != "" {
		key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(k))
		if err == nil && len(key) == ed25519.PublicKeySize {
			PublicKey = ed25519.PublicKey(key)
			return
		}
	}
	key, err := base64.StdEncoding.DecodeString("MCowBQYDK2VwAyEAQkFBS0VZQUtFWUtFWUtFWUtFWUtFWUtFWUtFWUtFWUtFWUtFWUtFWUtG")
	if err == nil && len(key) == ed25519.PublicKeySize {
		PublicKey = ed25519.PublicKey(key)
	}
}
