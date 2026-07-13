package licensing

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// PublicKey is the compiled-in Ed25519 public key for signature verification.
// Set via environment variable ORVIX_LICENSE_PUBLIC_KEY (base64) in production.
// Falls back to development test key when the env var is unset.
var PublicKey ed25519.PublicKey

func init() {
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

// IsDevKey returns true when using the development test key.
func IsDevKey() bool {
	prodKey := os.Getenv("ORVIX_LICENSE_PUBLIC_KEY")
	return prodKey == ""
}

// ParseLicense parses, validates signature, and returns a License from signed JSON.
func ParseLicense(data []byte) (*License, error) {
	var wrapper struct {
		License   License `json:"license"`
		Signature string  `json:"signature"`
	}
	if err := json.Unmarshal(data, &wrapper); err != nil {
		return nil, fmt.Errorf("parse license JSON: %w", err)
	}

	lic := &wrapper.License
	lic.Signature = wrapper.Signature

	return lic, nil
}

// SerializeLicense produces the signed JSON for a license.
func SerializeLicense(lic *License) ([]byte, error) {
	sig := lic.Signature
	lic.Signature = ""

	wrapper := struct {
		License   *License `json:"license"`
		Signature string   `json:"signature"`
	}{
		License:   lic,
		Signature: sig,
	}

	lic.Signature = sig
	return json.MarshalIndent(wrapper, "", "  ")
}

// VerifyLicenseSignature checks the Ed25519 signature on a license.
func VerifyLicenseSignature(lic *License) bool {
	if PublicKey == nil {
		return false
	}

	sig, err := base64.StdEncoding.DecodeString(lic.Signature)
	if err != nil || len(sig) != ed25519.SignatureSize {
		return false
	}

	// Serialize license without signature for verification.
	savedSig := lic.Signature
	lic.Signature = ""
	data, err := json.Marshal(lic)
	lic.Signature = savedSig
	if err != nil {
		return false
	}

	return ed25519.Verify(PublicKey, data, sig)
}

// SetPublicKey sets the Ed25519 public key for signature verification.
func SetPublicKey(key ed25519.PublicKey) {
	PublicKey = key
}

// SetPublicKeyPEM sets the public key from a base64-encoded Ed25519 key.
func SetPublicKeyB64(b64 string) error {
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(b64))
	if err != nil {
		return fmt.Errorf("decode public key: %w", err)
	}
	if len(key) != ed25519.PublicKeySize {
		return fmt.Errorf("invalid public key length: %d", len(key))
	}
	PublicKey = ed25519.PublicKey(key)
	return nil
}
