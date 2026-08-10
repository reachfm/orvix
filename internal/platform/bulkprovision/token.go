package bulkprovision

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
)

// generateSetupToken returns a random one-time setup token (raw, given
// to the operator to deliver out of band) and its SHA-256 hash (the
// only form ever persisted). Mirrors the pattern already established
// in internal/admin/organization/invitation.go.
func generateSetupToken() (raw string, hash string, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", err
	}
	raw = base64.RawURLEncoding.EncodeToString(b)
	return raw, hashSetupToken(raw), nil
}

func hashSetupToken(raw string) string {
	h := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(h[:])
}

// generateDiscardedPassword returns a random password used only to
// satisfy CreateMailbox's password requirement — it is immediately
// discarded by the caller and never logged, stored, or returned. The
// mailbox's real credential path is the setup token, not this value.
func generateDiscardedPassword() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
