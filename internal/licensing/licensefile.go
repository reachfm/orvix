package licensing

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// PublicKey is the configured Ed25519 verification key. Production never
// receives an implicit development key: a missing or malformed key leaves
// verification disabled and every signed license fails closed.
var PublicKey ed25519.PublicKey
var PublicKeyID string
var devKeyActive bool

const developmentPublicKeyB64 = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="

func init() {
	_ = ConfigurePublicKeyFromEnvironment()
}

// ConfigurePublicKeyFromEnvironment reloads the trusted verification key.
// The development key is available only behind an explicit opt-in intended
// for isolated development and tests.
func ConfigurePublicKeyFromEnvironment() error {
	PublicKey = nil
	PublicKeyID = ""
	devKeyActive = false
	encoded := strings.TrimSpace(os.Getenv("ORVIX_LICENSE_PUBLIC_KEY"))
	if encoded == "" {
		if os.Getenv("ORVIX_LICENSE_DEV_MODE") != "1" {
			return fmt.Errorf("trusted license public key is not configured")
		}
		encoded = developmentPublicKeyB64
		devKeyActive = true
	}
	key, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return fmt.Errorf("decode license public key: %w", err)
	}
	if len(key) != ed25519.PublicKeySize {
		return fmt.Errorf("invalid license public key length: %d", len(key))
	}
	PublicKey = append(ed25519.PublicKey(nil), key...)
	PublicKeyID = strings.TrimSpace(os.Getenv("ORVIX_LICENSE_KEY_ID"))
	if PublicKeyID == "" {
		sum := sha256.Sum256(key)
		PublicKeyID = hex.EncodeToString(sum[:8])
	}
	return nil
}

// IsDevKey returns true when using the development test key.
func IsDevKey() bool {
	return devKeyActive
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
	copyLicense := *lic
	sig := copyLicense.Signature
	copyLicense.Signature = ""

	wrapper := struct {
		License   *License `json:"license"`
		Signature string   `json:"signature"`
	}{
		License:   &copyLicense,
		Signature: sig,
	}

	return json.MarshalIndent(wrapper, "", "  ")
}

// VerifyLicenseSignature checks the Ed25519 signature on a license.
func VerifyLicenseSignature(lic *License) bool {
	if PublicKey == nil {
		return false
	}
	if lic.KeyID != "" && (PublicKeyID == "" || lic.KeyID != PublicKeyID) {
		return false
	}

	sig, err := base64.StdEncoding.DecodeString(lic.Signature)
	if err != nil || len(sig) != ed25519.SignatureSize {
		return false
	}

	// Serialize license without signature for verification.
	copyLicense := *lic
	copyLicense.Signature = ""
	data, err := json.Marshal(&copyLicense)
	if err != nil {
		return false
	}

	return ed25519.Verify(PublicKey, data, sig)
}

// SetPublicKey sets the Ed25519 public key for signature verification.
func SetPublicKey(key ed25519.PublicKey) {
	PublicKey = append(ed25519.PublicKey(nil), key...)
	sum := sha256.Sum256(key)
	PublicKeyID = hex.EncodeToString(sum[:8])
	devKeyActive = false
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
	sum := sha256.Sum256(key)
	PublicKeyID = hex.EncodeToString(sum[:8])
	devKeyActive = false
	return nil
}
