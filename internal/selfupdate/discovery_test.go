package selfupdate

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// ---- test fixture helpers ----

func genTestKeyPair(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey, []byte) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})
	return pub, priv, pemBytes
}

// releaseFixture bundles the bytes for one signed candidate release plus
// the metadata needed to serve it from a fake GitHub server.
type releaseFixture struct {
	version      string
	tag          string
	artifactName string
	artifact     []byte
	checksum     []byte
	signature    []byte
	manifest     []byte
	commit       string
}

func buildFixture(t *testing.T, priv ed25519.PrivateKey, version, commit string, arch string, corruptChecksum, corruptSig, badManifestJSON bool) releaseFixture {
	t.Helper()
	tag := "v" + version
	artifactName := fmt.Sprintf("orvix-enterprise-mail-%s-%s.tar.gz", version, arch)
	artifact := []byte("fake-artifact-bytes-for-" + version + "-" + arch)

	sum := sha256.Sum256(artifact)
	hexSum := hex.EncodeToString(sum[:])
	if corruptChecksum {
		hexSum = strings.Repeat("0", 64)
	}
	checksum := []byte(fmt.Sprintf("%s  %s\n", hexSum, artifactName))

	sig := ed25519.Sign(priv, artifact)
	if corruptSig {
		sig = append([]byte{}, sig...)
		sig[0] ^= 0xFF
	}

	var manifestBytes []byte
	if badManifestJSON {
		manifestBytes = []byte("{not valid json")
	} else {
		m := Manifest{
			Schema:         1,
			Product:        "orvix",
			Version:        version,
			Channel:        "stable",
			Commit:         commit,
			BuildTime:      "2026-01-01T00:00:00Z",
			Target:         arch,
			Artifact:       artifactName,
			ArtifactSHA256: hex.EncodeToString(sum[:]),
		}
		b, err := json.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		manifestBytes = b
	}

	return releaseFixture{
		version:      version,
		tag:          tag,
		artifactName: artifactName,
		artifact:     artifact,
		checksum:     checksum,
		signature:    sig,
		manifest:     manifestBytes,
		commit:       commit,
	}
}

// fakeGitHub is an httptest-backed stand-in for api.github.com plus the
// asset host, serving exactly one release (the "latest" one) built from a
// releaseFixture. assetHost, if non-empty, overrides where asset download
// URLs point (used by the redirect-to-disallowed-host test); otherwise
// assets are served from the same server as the API.
type fakeGitHub struct {
	srv           *httptest.Server
	fx            *releaseFixture // nil => 404 "no release"
	omitAssets    map[string]bool // asset "kind" (checksum/signature/manifest) to omit
	redirectAsset string          // if set, /assets/artifact redirects here instead of serving directly
	oversizeKind  string          // "checksum"|"signature"|"manifest"|"" — serve an oversized body for this sidecar
}

func newFakeGitHub(t *testing.T, fx *releaseFixture) *fakeGitHub {
	t.Helper()
	fg := &fakeGitHub{fx: fx, omitAssets: map[string]bool{}}
	fg.srv = httptest.NewServer(http.HandlerFunc(fg.handle))
	t.Cleanup(fg.srv.Close)
	return fg
}

func (fg *fakeGitHub) baseURL() string { return fg.srv.URL }
func (fg *fakeGitHub) host() string {
	u, _ := url.Parse(fg.srv.URL)
	return u.Host
}

func (fg *fakeGitHub) assetURL(kind string) string {
	return fg.baseURL() + "/assets/" + kind
}

func (fg *fakeGitHub) handle(w http.ResponseWriter, r *http.Request) {
	switch {
	case strings.HasSuffix(r.URL.Path, "/releases/latest"):
		fg.serveRelease(w, false)
	case strings.HasSuffix(r.URL.Path, "/releases"):
		fg.serveReleaseList(w)
	case strings.Contains(r.URL.Path, "/git/ref/tags/"):
		fg.serveTagRef(w)
	case strings.HasSuffix(r.URL.Path, "/assets/artifact"):
		fg.serveAsset(w, r, "artifact")
	case strings.HasSuffix(r.URL.Path, "/assets/checksum"):
		fg.serveAsset(w, r, "checksum")
	case strings.HasSuffix(r.URL.Path, "/assets/signature"):
		fg.serveAsset(w, r, "signature")
	case strings.HasSuffix(r.URL.Path, "/assets/manifest"):
		fg.serveAsset(w, r, "manifest")
	default:
		http.NotFound(w, r)
	}
}

func (fg *fakeGitHub) serveRelease(w http.ResponseWriter, _ bool) {
	if fg.fx == nil {
		http.NotFound(w, nil)
		return
	}
	rel := fg.buildGHRelease()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(rel)
}

func (fg *fakeGitHub) serveReleaseList(w http.ResponseWriter) {
	if fg.fx == nil {
		json.NewEncoder(w).Encode([]ghRelease{})
		return
	}
	rel := fg.buildGHRelease()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode([]ghRelease{rel})
}

func (fg *fakeGitHub) buildGHRelease() ghRelease {
	fx := fg.fx
	var assets []ghAsset
	assets = append(assets, ghAsset{Name: fx.artifactName, BrowserDownloadURL: fg.assetURL("artifact")})
	if !fg.omitAssets["checksum"] {
		assets = append(assets, ghAsset{Name: fx.artifactName + ".sha256", BrowserDownloadURL: fg.assetURL("checksum")})
	}
	if !fg.omitAssets["signature"] {
		assets = append(assets, ghAsset{Name: fx.artifactName + ".sig", BrowserDownloadURL: fg.assetURL("signature")})
	}
	if !fg.omitAssets["manifest"] {
		assets = append(assets, ghAsset{Name: "manifest.json", BrowserDownloadURL: fg.assetURL("manifest")})
	}
	return ghRelease{
		TagName:     fx.tag,
		Name:        fx.tag,
		PublishedAt: time.Now().UTC(),
		Assets:      assets,
	}
}

func (fg *fakeGitHub) serveTagRef(w http.ResponseWriter) {
	if fg.fx == nil {
		http.NotFound(w, nil)
		return
	}
	json.NewEncoder(w).Encode(ghTagRef{Object: ghCommitRef{SHA: fg.fx.commit}})
}

func (fg *fakeGitHub) serveAsset(w http.ResponseWriter, r *http.Request, kind string) {
	if kind == "artifact" && fg.redirectAsset != "" {
		http.Redirect(w, r, fg.redirectAsset, http.StatusFound)
		return
	}
	if fg.oversizeKind == kind {
		big := make([]byte, maxSidecarBytes*2)
		w.Write(big)
		return
	}
	switch kind {
	case "artifact":
		w.Write(fg.fx.artifact)
	case "checksum":
		w.Write(fg.fx.checksum)
	case "signature":
		w.Write(fg.fx.signature)
	case "manifest":
		w.Write(fg.fx.manifest)
	}
}

func testDiscoverer(fg *fakeGitHub, pubPEM []byte) *Discoverer {
	host := fg.host()
	return newDiscoverer(host, []string{host}, pubPEM, true)
}

// ---- tests ----

func TestDiscoverRelease_SuccessfulSignedRelease(t *testing.T) {
	_, priv, pubPEM := genTestKeyPair(t)
	fx := buildFixture(t, priv, "2.0.0", "deadbeefcafef00d0123456789abcdef01234567", "linux-amd64", false, false, false)
	fg := newFakeGitHub(t, &fx)
	d := testDiscoverer(fg, pubPEM)

	res, err := d.DiscoverRelease(context.Background(), ChannelStable, "1.9.0")
	if err != nil {
		t.Fatalf("DiscoverRelease: %v", err)
	}
	if len(res.Info.Blockers) != 0 {
		t.Fatalf("expected no blockers, got %v", res.Info.Blockers)
	}
	if !res.Info.Compatible {
		t.Fatal("expected Compatible=true")
	}
	if res.Verified == nil {
		t.Fatal("expected a verified bundle")
	}
	if res.Verified.Version != "2.0.0" {
		t.Fatalf("expected version 2.0.0, got %s", res.Verified.Version)
	}
	if res.Info.TargetCommit != fx.commit {
		t.Fatalf("expected target commit %s, got %s", fx.commit, res.Info.TargetCommit)
	}
	if string(res.Artifact) != string(fx.artifact) {
		t.Fatal("returned artifact bytes do not match fixture")
	}
}

func TestDiscoverRelease_UnsignedReleaseRejected(t *testing.T) {
	_, priv, pubPEM := genTestKeyPair(t)
	fx := buildFixture(t, priv, "2.0.0", "deadbeef", "linux-amd64", false, false, false)
	fg := newFakeGitHub(t, &fx)
	fg.omitAssets["signature"] = true
	d := testDiscoverer(fg, pubPEM)

	res, err := d.DiscoverRelease(context.Background(), ChannelStable, "1.9.0")
	if err != nil {
		t.Fatalf("DiscoverRelease: %v", err)
	}
	if !containsBlocker(res.Info.Blockers, BlockerUnofficialAssets) {
		t.Fatalf("expected unofficial_assets blocker, got %v", res.Info.Blockers)
	}
	if res.Verified != nil {
		t.Fatal("expected no verified bundle for an unsigned release")
	}
}

func TestDiscoverRelease_BadChecksumRejected(t *testing.T) {
	_, priv, pubPEM := genTestKeyPair(t)
	fx := buildFixture(t, priv, "2.0.0", "deadbeef", "linux-amd64", true, false, false)
	fg := newFakeGitHub(t, &fx)
	d := testDiscoverer(fg, pubPEM)

	res, err := d.DiscoverRelease(context.Background(), ChannelStable, "1.9.0")
	if err != nil {
		t.Fatalf("DiscoverRelease: %v", err)
	}
	if !containsBlocker(res.Info.Blockers, BlockerChecksumMismatch) {
		t.Fatalf("expected checksum_mismatch blocker, got %v", res.Info.Blockers)
	}
}

func TestDiscoverRelease_BadSignatureRejected(t *testing.T) {
	_, priv, pubPEM := genTestKeyPair(t)
	fx := buildFixture(t, priv, "2.0.0", "deadbeef", "linux-amd64", false, true, false)
	fg := newFakeGitHub(t, &fx)
	d := testDiscoverer(fg, pubPEM)

	res, err := d.DiscoverRelease(context.Background(), ChannelStable, "1.9.0")
	if err != nil {
		t.Fatalf("DiscoverRelease: %v", err)
	}
	if !containsBlocker(res.Info.Blockers, BlockerSignatureMismatch) {
		t.Fatalf("expected signature_mismatch blocker, got %v", res.Info.Blockers)
	}
}

func TestDiscoverRelease_WrongArchitectureRejected(t *testing.T) {
	_, priv, pubPEM := genTestKeyPair(t)
	fx := buildFixture(t, priv, "2.0.0", "deadbeef", "darwin-arm64", false, false, false)
	fg := newFakeGitHub(t, &fx)
	d := testDiscoverer(fg, pubPEM)

	res, err := d.DiscoverRelease(context.Background(), ChannelStable, "1.9.0")
	if err != nil {
		t.Fatalf("DiscoverRelease: %v", err)
	}
	if !containsBlocker(res.Info.Blockers, BlockerWrongArchitecture) {
		t.Fatalf("expected wrong_architecture blocker, got %v", res.Info.Blockers)
	}
}

func TestDiscoverRelease_DowngradeRejected(t *testing.T) {
	_, priv, pubPEM := genTestKeyPair(t)
	fx := buildFixture(t, priv, "1.0.0", "deadbeef", "linux-amd64", false, false, false)
	fg := newFakeGitHub(t, &fx)
	d := testDiscoverer(fg, pubPEM)

	res, err := d.DiscoverRelease(context.Background(), ChannelStable, "2.0.0")
	if err != nil {
		t.Fatalf("DiscoverRelease: %v", err)
	}
	if !containsBlocker(res.Info.Blockers, BlockerDowngrade) {
		t.Fatalf("expected downgrade blocker, got %v", res.Info.Blockers)
	}
}

func TestDiscoverRelease_SameVersionRejected(t *testing.T) {
	_, priv, pubPEM := genTestKeyPair(t)
	fx := buildFixture(t, priv, "2.0.0", "deadbeef", "linux-amd64", false, false, false)
	fg := newFakeGitHub(t, &fx)
	d := testDiscoverer(fg, pubPEM)

	res, err := d.DiscoverRelease(context.Background(), ChannelStable, "2.0.0")
	if err != nil {
		t.Fatalf("DiscoverRelease: %v", err)
	}
	if !containsBlocker(res.Info.Blockers, BlockerSameVersion) {
		t.Fatalf("expected same_version blocker, got %v", res.Info.Blockers)
	}
}

func TestDiscoverRelease_MalformedManifestRejected(t *testing.T) {
	_, priv, pubPEM := genTestKeyPair(t)
	fx := buildFixture(t, priv, "2.0.0", "deadbeef", "linux-amd64", false, false, true)
	fg := newFakeGitHub(t, &fx)
	d := testDiscoverer(fg, pubPEM)

	res, err := d.DiscoverRelease(context.Background(), ChannelStable, "1.9.0")
	if err != nil {
		t.Fatalf("DiscoverRelease: %v", err)
	}
	if !containsBlocker(res.Info.Blockers, BlockerManifestMalformed) {
		t.Fatalf("expected manifest_malformed blocker, got %v", res.Info.Blockers)
	}
}

func TestDiscoverRelease_OversizedDownloadRejected(t *testing.T) {
	_, priv, pubPEM := genTestKeyPair(t)
	fx := buildFixture(t, priv, "2.0.0", "deadbeef", "linux-amd64", false, false, false)
	fg := newFakeGitHub(t, &fx)
	fg.oversizeKind = "manifest"
	d := testDiscoverer(fg, pubPEM)

	_, err := d.DiscoverRelease(context.Background(), ChannelStable, "1.9.0")
	if err == nil {
		t.Fatal("expected an error for oversized manifest download")
	}
	if !errors.Is(err, ErrDownloadTooLarge) {
		t.Fatalf("expected ErrDownloadTooLarge, got %v", err)
	}
}

func TestDiscoverRelease_RedirectToDisallowedHostRejected(t *testing.T) {
	_, priv, pubPEM := genTestKeyPair(t)
	fx := buildFixture(t, priv, "2.0.0", "deadbeef", "linux-amd64", false, false, false)
	fg := newFakeGitHub(t, &fx)

	evil := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("should never be read"))
	}))
	t.Cleanup(evil.Close)
	fg.redirectAsset = evil.URL + "/payload"

	d := testDiscoverer(fg, pubPEM)
	_, err := d.DiscoverRelease(context.Background(), ChannelStable, "1.9.0")
	if err == nil {
		t.Fatal("expected an error when the artifact redirects to a disallowed host")
	}
	if !errors.Is(err, ErrRedirectNotAllowed) && !strings.Contains(err.Error(), "redirect") {
		t.Fatalf("expected a redirect-not-allowed error, got %v", err)
	}
}

func TestDiscoverRelease_UnknownHostRejectedOutright(t *testing.T) {
	_, _, pubPEM := genTestKeyPair(t)
	// A Discoverer configured with an allowlist that does NOT include the
	// fake server's host must refuse to talk to it at all.
	d := newDiscoverer("definitely-not-allowlisted.example.invalid", nil, pubPEM, true)
	_, err := d.DiscoverRelease(context.Background(), ChannelStable, "1.0.0")
	if err == nil {
		t.Fatal("expected an error resolving against a host with no reachable allowlisted server")
	}
}

func TestCheckHostResolvesToPublicIP_RejectsPrivateAndLoopbackRanges(t *testing.T) {
	cases := []struct {
		name string
		ip   string
		want bool // true == should be rejected
	}{
		{"loopback v4", "127.0.0.1", true},
		{"loopback v6", "::1", true},
		{"rfc1918 10", "10.1.2.3", true},
		{"rfc1918 172.16", "172.16.0.5", true},
		{"rfc1918 192.168", "192.168.1.1", true},
		{"link-local v4", "169.254.1.1", true},
		{"unspecified", "0.0.0.0", true},
		{"public v4", "8.8.8.8", false},
		{"public v6", "2606:4700:4700::1111", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := checkHostResolvesToPublicIP(c.ip)
			if c.want && err == nil {
				t.Fatalf("expected %s to be rejected as private/loopback, but it was allowed", c.ip)
			}
			if !c.want && err != nil {
				t.Fatalf("expected %s to be allowed as public, but got error: %v", c.ip, err)
			}
		})
	}
}

func TestDiscoverRelease_SSRFAttemptRejected(t *testing.T) {
	_, _, pubPEM := genTestKeyPair(t)
	// Even if somehow allowlisted by hostname, a Discoverer built WITHOUT
	// the test-only allowPrivateIPs escape hatch must refuse to talk to a
	// host that resolves to a private/loopback address — exercised here by
	// constructing production-shaped discoverer (allowPrivateIPs=false)
	// against 127.0.0.1 directly, simulating an attacker-controlled asset
	// URL that resolves to loopback/internal space.
	d := newDiscoverer("127.0.0.1", []string{"127.0.0.1"}, pubPEM, false)
	_, err := d.DiscoverRelease(context.Background(), ChannelStable, "1.0.0")
	if err == nil {
		t.Fatal("expected SSRF attempt against loopback address to be rejected")
	}
	if !errors.Is(err, ErrPrivateOrLoopbackIP) {
		t.Fatalf("expected ErrPrivateOrLoopbackIP, got %v", err)
	}
}

func TestDiscoverRelease_RequestTimeout(t *testing.T) {
	_, priv, pubPEM := genTestKeyPair(t)
	fx := buildFixture(t, priv, "2.0.0", "deadbeef", "linux-amd64", false, false, false)
	_ = fx

	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(500 * time.Millisecond)
		w.Write([]byte("{}"))
	}))
	t.Cleanup(slow.Close)

	u, _ := url.Parse(slow.URL)
	d := newDiscovererWithTimeout(u.Host, []string{u.Host}, pubPEM, true, 50*time.Millisecond)

	_, err := d.DiscoverRelease(context.Background(), ChannelStable, "1.0.0")
	if err == nil {
		t.Fatal("expected a timeout error")
	}
	if !strings.Contains(err.Error(), "context deadline exceeded") && !strings.Contains(err.Error(), "Client.Timeout") {
		t.Fatalf("expected a timeout-flavored error, got: %v", err)
	}
}

func TestDiscoverRelease_PrereleaseChannel(t *testing.T) {
	_, priv, pubPEM := genTestKeyPair(t)
	fx := buildFixture(t, priv, "2.1.0-rc1", "deadbeef", "linux-amd64", false, false, false)
	fg := newFakeGitHub(t, &fx)
	d := testDiscoverer(fg, pubPEM)

	res, err := d.DiscoverRelease(context.Background(), ChannelPrerelease, "2.0.0")
	if err != nil {
		t.Fatalf("DiscoverRelease: %v", err)
	}
	if res.Verified == nil {
		t.Fatalf("expected a verified prerelease bundle, blockers=%v", res.Info.Blockers)
	}
	if res.Verified.Version != "2.1.0-rc1" {
		t.Fatalf("expected version 2.1.0-rc1, got %s", res.Verified.Version)
	}
}

func containsBlocker(blockers []string, want string) bool {
	for _, b := range blockers {
		if b == want {
			return true
		}
	}
	return false
}
