package relay

// secretCodec is the narrow port around internal/config's AES-GCM
// encrypt-at-rest primitives — the project's secret-reference
// mechanism. Defined as a port (not a direct import of internal/config
// throughout this package) so tests can substitute a fast fake
// instead of touching the real on-disk/env-derived key material.
type secretCodec interface {
	Encrypt(plaintext string) (string, error)
	Decrypt(ciphertext string) (string, error)
}

// RedactedProvider is the ONLY shape a provider takes when serialized
// to an API response, log line, or audit record — SecretRef and any
// derived plaintext never appear, only a boolean.
type RedactedProvider struct {
	Provider
	HasCredential bool `json:"has_credential"`
}

// Redact strips the encrypted secret reference from a Provider before
// it leaves the service layer, replacing it with a boolean. Even
// though SecretRef is already `json:"-"`, Redact is the explicit,
// auditable choke point every read path is required to call — the
// json tag alone is a convention that a future field rename could
// silently break; this function is what actually gets tested.
func Redact(p Provider) RedactedProvider {
	has := p.SecretRef != ""
	p.SecretRef = ""
	return RedactedProvider{Provider: p, HasCredential: has}
}

func RedactAll(ps []Provider) []RedactedProvider {
	out := make([]RedactedProvider, 0, len(ps))
	for _, p := range ps {
		out = append(out, Redact(p))
	}
	return out
}
