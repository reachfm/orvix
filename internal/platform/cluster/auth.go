package cluster

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
)

// GenerateEnrollmentSecret returns a random raw secret (given to the
// node operator once, out of band) and its SHA-256 hash (the only
// form ever persisted) — mirrors the setup-token pattern already
// established in internal/platform/bulkprovision/token.go.
func GenerateEnrollmentSecret() (raw string, hash string, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", err
	}
	raw = base64.RawURLEncoding.EncodeToString(b)
	return raw, HashSecret(raw), nil
}

func HashSecret(raw string) string {
	h := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(h[:])
}

// VerifySecret does a constant-time comparison against the stored
// hash, never the raw secret — timing-safe against hash-guessing.
func VerifySecret(raw, storedHash string) bool {
	computed := HashSecret(raw)
	return subtle.ConstantTimeCompare([]byte(computed), []byte(storedHash)) == 1
}
