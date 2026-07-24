package selfupdate

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// versionRe is the only shape a version string may ever take on the wire
// between the browser, the API, and the updater. Anything else is rejected
// before it reaches any exec.Command, file path, or URL construction.
var versionRe = regexp.MustCompile(`^\d+\.\d+\.\d+(-[a-zA-Z0-9.-]+)?$`)

// ValidateVersionString rejects anything that is not a strict semver-ish
// version (optionally with an rc/prerelease suffix). This is the first gate
// against command/path injection via a version string — every other
// version-consuming function in this package calls it first.
func ValidateVersionString(v string) error {
	if v == "" {
		return errors.New("version must not be empty")
	}
	if len(v) > 64 {
		return errors.New("version exceeds maximum length")
	}
	if !versionRe.MatchString(v) {
		return fmt.Errorf("version %q does not match the required format X.Y.Z[-prerelease]", v)
	}
	return nil
}

// Manifest mirrors the manifest.json shape written by
// release/scripts/build-release-bundle.sh's write_release_manifest.
type Manifest struct {
	Schema         int    `json:"schema"`
	Product        string `json:"product"`
	Version        string `json:"version"`
	Channel        string `json:"channel"`
	Commit         string `json:"commit"`
	BuildTime      string `json:"build_time"`
	Target         string `json:"target"`
	Artifact       string `json:"artifact"`
	ArtifactSHA256 string `json:"artifact_sha256"`
	SBOM           string `json:"sbom"`
	SBOMSHA256     string `json:"sbom_sha256"`
}

// VerifiedBundle is the result of a bundle that passed every check in
// VerifyBundle. Only a VerifiedBundle may be handed to the code that
// actually invokes release/upgrade.sh.
type VerifiedBundle struct {
	SHA256  string
	Version string
	Commit  string
	Channel string
	Target  string
}

// ErrDowngrade is returned by VerifyBundle when the verified target version
// is not strictly greater than the currently installed version. Callers
// that want to allow moving to an older version MUST NOT call VerifyBundle
// for that purpose — downgrade is only reachable via a rollback to a
// previously verified local snapshot, never via Install.
var ErrDowngrade = errors.New("selfupdate: target version is not newer than the installed version")

// VerifyBundle performs every mandatory check on a downloaded release
// artifact before it is safe to hand to the upgrade path:
//
//  1. requested version and artifact bytes' SHA256 match the checksum
//     sidecar exactly (byte-for-byte, not just non-empty)
//  2. the Ed25519 signature over the raw artifact bytes verifies against
//     the supplied trusted public key (release/trust/orvix-release-signing.pub.pem)
//  3. the manifest.json's artifact_sha256 field matches the same checksum
//  4. the version is consistent across: the release tag, the asset
//     filename, and the manifest — a mismatch anywhere aborts
//  5. the resolved version is strictly greater than currentVersion (reject
//     downgrade-via-install)
//
// Any failure returns a non-nil error and the artifact must be discarded
// without ever being extracted or executed.
func VerifyBundle(
	tag string,
	assetFilename string,
	artifact []byte,
	checksumSidecar []byte,
	signature []byte,
	manifestJSON []byte,
	trustedPublicKeyPEM []byte,
	currentVersion string,
) (*VerifiedBundle, error) {
	// 1. checksum
	sum := sha256.Sum256(artifact)
	actualHex := hex.EncodeToString(sum[:])
	wantHex, err := parseChecksumSidecar(checksumSidecar)
	if err != nil {
		return nil, fmt.Errorf("checksum sidecar: %w", err)
	}
	if !strings.EqualFold(actualHex, wantHex) {
		return nil, fmt.Errorf("checksum mismatch: artifact sha256 %s does not match sidecar %s", actualHex, wantHex)
	}

	// 2. Ed25519 signature over the raw artifact bytes (PureEdDSA — matches
	// `openssl pkeyutl -verify -rawin`, the same scheme
	// release/scripts/verify-release-signature.sh already uses).
	pub, err := parseEd25519PublicKeyPEM(trustedPublicKeyPEM)
	if err != nil {
		return nil, fmt.Errorf("trusted public key: %w", err)
	}
	if !ed25519.Verify(pub, artifact, signature) {
		return nil, errors.New("Ed25519 signature verification failed against the trusted public key")
	}

	// 3 + 4. manifest and cross-source version consistency
	var m Manifest
	if err := json.Unmarshal(manifestJSON, &m); err != nil {
		return nil, fmt.Errorf("malformed manifest.json: %w", err)
	}
	if !strings.EqualFold(m.ArtifactSHA256, actualHex) {
		return nil, fmt.Errorf("manifest artifact_sha256 %s does not match verified checksum %s", m.ArtifactSHA256, actualHex)
	}
	if err := ValidateVersionString(m.Version); err != nil {
		return nil, fmt.Errorf("manifest version: %w", err)
	}
	tagVersion := strings.TrimPrefix(tag, "v")
	if !strings.EqualFold(tagVersion, m.Version) {
		return nil, fmt.Errorf("version mismatch: release tag %q implies version %q but manifest reports %q", tag, tagVersion, m.Version)
	}
	wantAssetPrefix := fmt.Sprintf("orvix-enterprise-mail-%s-", m.Version)
	if !strings.HasPrefix(assetFilename, wantAssetPrefix) {
		return nil, fmt.Errorf("version mismatch: asset filename %q does not start with expected %q", assetFilename, wantAssetPrefix)
	}
	if m.Artifact != "" && m.Artifact != assetFilename {
		return nil, fmt.Errorf("manifest artifact field %q does not match downloaded asset name %q", m.Artifact, assetFilename)
	}

	// 5. downgrade rejection
	if currentVersion != "" {
		if err := ValidateVersionString(currentVersion); err != nil {
			return nil, fmt.Errorf("current version: %w", err)
		}
		cmp, err := compareVersions(m.Version, currentVersion)
		if err != nil {
			return nil, err
		}
		if cmp <= 0 {
			return nil, ErrDowngrade
		}
	}

	return &VerifiedBundle{
		SHA256:  actualHex,
		Version: m.Version,
		Commit:  m.Commit,
		Channel: m.Channel,
		Target:  m.Target,
	}, nil
}

// parseChecksumSidecar accepts the sha256sum(1) format written by
// build-release-bundle.sh: "<hex>  <path>\n". Only the first token on the
// first non-empty line is used; the path is intentionally ignored (we
// already know which artifact we downloaded).
func parseChecksumSidecar(b []byte) (string, error) {
	fields := strings.Fields(string(b))
	if len(fields) == 0 {
		return "", errors.New("empty checksum sidecar")
	}
	hexSum := strings.ToLower(fields[0])
	if len(hexSum) != 64 {
		return "", fmt.Errorf("malformed sha256 hex (want 64 chars, got %d)", len(hexSum))
	}
	if _, err := hex.DecodeString(hexSum); err != nil {
		return "", fmt.Errorf("malformed sha256 hex: %w", err)
	}
	return hexSum, nil
}

// parseEd25519PublicKeyPEM decodes a PEM-encoded SubjectPublicKeyInfo
// wrapping a raw 32-byte Ed25519 public key, matching what `openssl genpkey
// -algorithm Ed25519` / `openssl pkey -pubout` produce.
func parseEd25519PublicKeyPEM(pemBytes []byte) (ed25519.PublicKey, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, errors.New("no PEM block found")
	}
	// SubjectPublicKeyInfo for Ed25519 is a fixed 12-byte ASN.1/DER prefix
	// (algorithm identifier for id-Ed25519) followed by the raw 32-byte key.
	// Avoid pulling in x509.ParsePKIXPublicKey's broader surface for a
	// single fixed key type; instead take the last 32 bytes, which is
	// exactly how Go's own x509 parser extracts an Ed25519 key from this
	// DER structure.
	der := block.Bytes
	if len(der) < ed25519.PublicKeySize {
		return nil, fmt.Errorf("public key DER too short (%d bytes)", len(der))
	}
	key := der[len(der)-ed25519.PublicKeySize:]
	return ed25519.PublicKey(key), nil
}

// compareVersions compares two "X.Y.Z[-prerelease]" strings already
// validated by ValidateVersionString. It returns >0 if a>b, <0 if a<b, 0 if
// equal. A version with a prerelease suffix is considered older than the
// same X.Y.Z without one (rc < final), and prerelease suffixes compare
// lexically when the numeric core is equal.
func compareVersions(a, b string) (int, error) {
	coreA, preA := splitVersion(a)
	coreB, preB := splitVersion(b)
	na, err := parseVersionCore(coreA)
	if err != nil {
		return 0, err
	}
	nb, err := parseVersionCore(coreB)
	if err != nil {
		return 0, err
	}
	for i := 0; i < 3; i++ {
		if na[i] != nb[i] {
			if na[i] > nb[i] {
				return 1, nil
			}
			return -1, nil
		}
	}
	switch {
	case preA == "" && preB == "":
		return 0, nil
	case preA == "" && preB != "":
		return 1, nil
	case preA != "" && preB == "":
		return -1, nil
	default:
		return strings.Compare(preA, preB), nil
	}
}

func splitVersion(v string) (core, prerelease string) {
	if i := strings.IndexByte(v, '-'); i >= 0 {
		return v[:i], v[i+1:]
	}
	return v, ""
}

func parseVersionCore(core string) ([3]int, error) {
	var out [3]int
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return out, fmt.Errorf("invalid version core %q", core)
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return out, fmt.Errorf("invalid version core %q: %w", core, err)
		}
		out[i] = n
	}
	return out, nil
}
