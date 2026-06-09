package licensing

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"time"
)

// GenerateMachineID creates a unique machine fingerprint.
func GenerateMachineID() string {
	components := []string{}

	// Hostname.
	if host, err := os.Hostname(); err == nil {
		components = append(components, host)
	}

	// Machine ID file (Linux).
	if data, err := os.ReadFile("/etc/machine-id"); err == nil {
		components = append(components, strings.TrimSpace(string(data)))
	}
	if data, err := os.ReadFile("/var/lib/dbus/machine-id"); err == nil {
		components = append(components, strings.TrimSpace(string(data)))
	}

	input := strings.Join(components, "|")
	if input == "" {
		// Fallback: generate random ID.
		b := make([]byte, 32)
		rand.Read(b)
		return hex.EncodeToString(b)
	}

	h := sha256.Sum256([]byte(input))
	return hex.EncodeToString(h[:])
}

// ValidateLicense performs full license validation.
func ValidateLicense(lic *License) *ValidationResult {
	result := &ValidationResult{Valid: true}

	// 1. Edition check.
	if !lic.Edition.Valid() {
		result.Valid = false
		result.Errors = append(result.Errors, fmt.Sprintf("invalid edition: %s", lic.Edition))
	}

	// 2. Expiry check.
	if !lic.ExpiresAt.IsZero() && time.Now().After(lic.ExpiresAt) {
		result.Valid = false
		result.Errors = append(result.Errors, "license has expired")
	}

	// 3. Signature check.
	if !VerifyLicenseSignature(lic) {
		result.Valid = false
		result.Errors = append(result.Errors, "invalid signature")
	}

	// 4. Machine binding check.
	if lic.MachineBinding != "" {
		machineID := GenerateMachineID()
		if lic.MachineBinding != machineID {
			result.Errors = append(result.Errors, "machine binding mismatch")
			result.Valid = false
		}
	} else {
		result.Warnings = append(result.Warnings, "no machine binding")
	}

	return result
}

// HasFeature checks if a feature is enabled in the license.
func HasFeature(lic *License, feature string) bool {
	if lic == nil {
		return false
	}
	if lic.Edition == EditionCommunity {
		return false
	}
	for _, f := range lic.Features {
		if f == feature {
			return true
		}
	}
	return false
}

// CheckLimit checks if a resource limit is exceeded.
func CheckLimit(lic *License, current, limit int64) bool {
	if lic == nil {
		return false
	}
	if limit <= 0 {
		return true // unlimited
	}
	return current <= limit
}
