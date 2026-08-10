package relay

import "github.com/orvix/orvix/internal/config"

// configSecretCodec adapts internal/config's package-level
// EncryptString/DecryptString to the secretCodec port. This is the
// production implementation; tests use a fake to avoid depending on
// the process's encryption-key file.
type configSecretCodec struct{}

func (configSecretCodec) Encrypt(plaintext string) (string, error) {
	return config.EncryptString(plaintext)
}

func (configSecretCodec) Decrypt(ciphertext string) (string, error) {
	return config.DecryptString(ciphertext)
}
