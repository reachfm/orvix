package updates

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// Verifier holds the trusted public key(s) used to verify update
// manifests. Verification is pure stdlib crypto over in-memory bytes —
// no shelling out, no external tools.
type Verifier struct {
	trustedKeys []ed25519.PublicKey
}

// NewVerifier constructs a Verifier trusting the given ed25519 public
// keys (allows key rotation: an artifact signed by any trusted key
// verifies).
func NewVerifier(trustedKeys ...ed25519.PublicKey) *Verifier {
	return &Verifier{trustedKeys: trustedKeys}
}

// VerifyManifest checks that signature is a valid ed25519 signature,
// by one of the trusted keys, over the exact manifestJSON bytes. An
// empty signature is rejected as ErrUnsigned (not merely an invalid
// signature) so the caller can distinguish and report the two cases
// differently.
func (v *Verifier) VerifyManifest(manifestJSON, signature []byte) error {
	if len(signature) == 0 {
		return ErrUnsigned
	}
	if len(v.trustedKeys) == 0 {
		return ErrInvalidSignature
	}
	for _, key := range v.trustedKeys {
		if ed25519.Verify(key, manifestJSON, signature) {
			return nil
		}
	}
	return ErrInvalidSignature
}

// VerifyArtifact recomputes the artifact's sha256 and compares it,
// constant-time-irrelevant (a hash comparison, not a secret
// comparison) against the value in the already-signature-verified
// manifest.
func VerifyArtifactHash(artifact []byte, manifest Manifest) error {
	sum := sha256.Sum256(artifact)
	got := hex.EncodeToString(sum[:])
	if got != manifest.SHA256 {
		return ErrHashMismatch
	}
	return nil
}

// ParseManifest decodes the manifest JSON. Kept as a named function
// (rather than inlined json.Unmarshal at each call site) so every
// caller gets identical error handling.
func ParseManifest(manifestJSON []byte) (Manifest, error) {
	var m Manifest
	if err := json.Unmarshal(manifestJSON, &m); err != nil {
		return Manifest{}, err
	}
	return m, nil
}
