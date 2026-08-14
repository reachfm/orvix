package bulkprovision

import "crypto/rand"

import "encoding/base64"

// generateDiscardedPassword returns a random password used only to
// satisfy CreateMailbox's password requirement — it is immediately
// discarded by the caller and never logged, stored, or returned. The
// mailbox's real credential path is the platform's existing
// forgot-password/activation flow (ForcePasswordChange is set on
// every bulk-created mailbox), not this value.
func generateDiscardedPassword() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
