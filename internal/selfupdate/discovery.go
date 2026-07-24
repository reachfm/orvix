// This file implements Phase E of the Admin Console self-update feature:
// release discovery against the official GitHub release channel for
// reachfm/orvix, and nothing else. It never accepts a caller-supplied URL
// or repository — the GitHub API host and the GitHub release-asset hosts
// are hardcoded allowlist entries, and every URL this package is about to
// fetch (including the single redirect hop it permits) is validated against
// that allowlist and against SSRF-sensitive IP ranges before the request is
// made.
//
// Discovery only resolves a candidate release and independently verifies it
// via the existing VerifyBundle (verify.go, Phase 0) — it does not install,
// migrate, restart, or touch the filesystem beyond reading the bytes it
// downloaded into memory.
package selfupdate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// githubOwner/githubRepo are the ONLY repository this package will ever
// query. They are not configurable at runtime by any external input.
const (
	githubOwner = "reachfm"
	githubRepo  = "orvix"

	defaultGitHubAPIHost = "api.github.com"
)

// defaultAssetHosts is the allowlist of hosts GitHub itself redirects
// release-asset downloads to. GitHub's release asset download flow
// typically 302s from api.github.com/.../assets/<id> (with an
// Accept: application/octet-stream request) to a signed URL on one of
// these object-storage hosts.
var defaultAssetHosts = []string{
	"objects.githubusercontent.com",
	"github-releases.githubusercontent.com",
}

// Size and timeout limits. Kept generous enough for a real release bundle
// but bounded so a compromised or malicious host on the allowlist (or a
// MITM of it) cannot exhaust memory or hang the updater daemon.
const (
	maxMetadataBytes  = 8 << 20   // 8 MiB: GitHub API JSON responses (release list, commit lookup)
	maxSidecarBytes   = 64 << 10  // 64 KiB: checksum/signature/manifest sidecars
	maxArtifactBytes  = 500 << 20 // 500 MiB: the actual release artifact
	requestTimeout    = 20 * time.Second
	maxRedirectHops   = 1
	userAgentDiscover = "orvix-updater-discovery"
)

// Channel selects which GitHub releases are eligible.
type Channel string

const (
	ChannelStable     Channel = "stable"
	ChannelPrerelease Channel = "prerelease"
)

// Errors returned by discovery. Callers (the updater daemon's job pipeline)
// match on these with errors.Is to decide how to report a blocker without
// parsing message strings.
var (
	ErrNoReleaseFound      = errors.New("selfupdate: no matching release found")
	ErrUnofficialAssets    = errors.New("selfupdate: release is missing one or more required signed assets")
	ErrHostNotAllowed      = errors.New("selfupdate: refusing to fetch from a non-allowlisted host")
	ErrRedirectNotAllowed  = errors.New("selfupdate: refusing to follow redirect to a non-allowlisted host")
	ErrDownloadTooLarge    = errors.New("selfupdate: download exceeded the maximum allowed size")
	ErrWrongArchitecture   = errors.New("selfupdate: release asset is not built for linux/amd64")
	ErrCommitMismatch      = errors.New("selfupdate: release commit does not match manifest commit")
	ErrSameVersion         = errors.New("selfupdate: available version is the same as the installed version")
	ErrPrivateOrLoopbackIP = errors.New("selfupdate: refusing to fetch from a private, loopback, or link-local address")
)

// wantArchLabel is the only architecture the updater currently installs.
// It must appear in the release asset's filename (see
// release/scripts/build-release-bundle.sh's naming convention:
// orvix-enterprise-mail-<version>-<os>-<arch>.tar.gz).
const wantArchLabel = "linux-amd64"

// requiredAssetSuffixes enumerates the sidecar assets every official
// release must publish alongside the main artifact. Missing any of these
// means the release is not signed/official and must be rejected outright
// (ErrUnofficialAssets), never silently treated as "no signature needed".
var requiredAssetSuffixes = struct {
	checksum, signature, manifest string
}{
	checksum:  ".sha256",
	signature: ".sig",
	manifest:  "manifest.json",
}

// Discoverer resolves and verifies official Orvix releases from GitHub.
// The zero value is not usable — construct with NewDiscoverer (production)
// or newTestDiscoverer (tests only, in discovery_test.go).
type Discoverer struct {
	apiHost    string
	assetHosts map[string]bool
	httpClient *http.Client

	// trustedPublicKeyPEM is the Ed25519 public key VerifyBundle checks
	// signatures against. Supplied by the caller (loaded from
	// release/trust/orvix-release-signing.pub.pem by whoever constructs
	// the Discoverer) rather than hardcoded here, so tests can supply a
	// throwaway keypair without touching the real trust root.
	trustedPublicKeyPEM []byte

	// allowPrivateIPs disables the public-IP-only DNS/IP check. It is
	// NEVER set true in production — NewDiscoverer never sets it — and
	// exists solely so discovery_test.go can point the Discoverer at an
	// httptest.Server, which is only reachable on a loopback address.
	// The host-allowlist check still applies unconditionally either way.
	allowPrivateIPs bool

	// scheme is "https" in production (the only value NewDiscoverer ever
	// sets) and is only ever "http" in tests, where allowPrivateIPs is
	// also true, so an httptest.Server (which is plain HTTP) can be used
	// as a GitHub stand-in.
	scheme string
}

// NewDiscoverer returns a production Discoverer that only ever talks to the
// real GitHub API host and the real GitHub release-asset hosts. There is no
// exported way to override those hosts — the only injection point
// (newTestDiscoverer) lives in discovery_test.go and is unreachable from
// non-test code.
func NewDiscoverer(trustedPublicKeyPEM []byte) *Discoverer {
	return newDiscoverer(defaultGitHubAPIHost, defaultAssetHosts, trustedPublicKeyPEM, false)
}

func newDiscoverer(apiHost string, assetHosts []string, trustedPublicKeyPEM []byte, allowPrivateIPs bool) *Discoverer {
	return newDiscovererWithTimeout(apiHost, assetHosts, trustedPublicKeyPEM, allowPrivateIPs, requestTimeout)
}

// newDiscovererWithTimeout is the same as newDiscoverer but lets tests use
// a short timeout instead of the production requestTimeout, so a
// request-timeout test doesn't have to actually wait 20+ seconds.
func newDiscovererWithTimeout(apiHost string, assetHosts []string, trustedPublicKeyPEM []byte, allowPrivateIPs bool, timeout time.Duration) *Discoverer {
	hosts := make(map[string]bool, len(assetHosts))
	for _, h := range assetHosts {
		hosts[strings.ToLower(h)] = true
	}
	scheme := "https"
	if allowPrivateIPs {
		scheme = "http"
	}
	d := &Discoverer{
		apiHost:             strings.ToLower(apiHost),
		assetHosts:          hosts,
		trustedPublicKeyPEM: trustedPublicKeyPEM,
		allowPrivateIPs:     allowPrivateIPs,
		scheme:              scheme,
	}
	d.httpClient = &http.Client{
		Timeout: timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) > maxRedirectHops {
				return fmt.Errorf("selfupdate: too many redirects (max %d)", maxRedirectHops)
			}
			if err := d.validateURL(req.URL); err != nil {
				return fmt.Errorf("%w: %s", ErrRedirectNotAllowed, req.URL.Host)
			}
			return nil
		},
	}
	return d
}

// isAllowedHost reports whether host (already lowercased, no port) is
// either the configured GitHub API host or one of the configured asset
// hosts.
func (d *Discoverer) isAllowedHost(host string) bool {
	host = strings.ToLower(host)
	if host == d.apiHost {
		return true
	}
	return d.assetHosts[host]
}

// validateURL is the single SSRF gate every outbound request (initial and
// redirect) must pass. It rejects:
//   - non-https schemes
//   - hosts outside the allowlist
//   - hostnames that resolve to a private, loopback, link-local, or
//     otherwise non-global-unicast IP address
func (d *Discoverer) validateURL(u *url.URL) error {
	if u.Scheme != d.scheme {
		return fmt.Errorf("%w: scheme %q is not %s", ErrHostNotAllowed, u.Scheme, d.scheme)
	}
	if u.Host == "" {
		return fmt.Errorf("%w: empty host", ErrHostNotAllowed)
	}
	// The allowlist is matched against the full authority (host[:port]) so
	// that test doubles (httptest.Server, which always includes a port)
	// can be allowlisted precisely; production GitHub hosts never include
	// a port so this is equivalent to a bare-hostname match there.
	if !d.isAllowedHost(u.Host) {
		return fmt.Errorf("%w: %s", ErrHostNotAllowed, u.Host)
	}
	if d.allowPrivateIPs {
		return nil
	}
	return checkHostResolvesToPublicIP(u.Hostname())
}

// checkHostResolvesToPublicIP resolves host and rejects it if any resolved
// address is private, loopback, link-local, unspecified, or otherwise not
// a global unicast address. This defends against DNS rebinding / a
// GitHub-adjacent hostname (even one on the allowlist, in the unlikely
// event of DNS compromise) resolving into RFC1918/loopback/link-local
// space.
func checkHostResolvesToPublicIP(host string) error {
	// If host is already a literal IP, skip DNS resolution.
	if ip := net.ParseIP(host); ip != nil {
		return checkIPPublic(ip)
	}
	ips, err := net.LookupIP(host)
	if err != nil {
		return fmt.Errorf("selfupdate: resolving host %q: %w", host, err)
	}
	if len(ips) == 0 {
		return fmt.Errorf("selfupdate: host %q resolved to no addresses", host)
	}
	for _, ip := range ips {
		if err := checkIPPublic(ip); err != nil {
			return err
		}
	}
	return nil
}

func checkIPPublic(ip net.IP) error {
	if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsUnspecified() || ip.IsPrivate() || ip.IsMulticast() {
		return fmt.Errorf("%w: %s", ErrPrivateOrLoopbackIP, ip.String())
	}
	// Belt-and-braces for ranges Go's IsPrivate doesn't cover on some
	// versions (e.g. IPv4 shared address space 100.64.0.0/10, and
	// benchmarking/documentation ranges): reject anything that isn't a
	// global unicast address.
	if !ip.IsGlobalUnicast() {
		return fmt.Errorf("%w: %s", ErrPrivateOrLoopbackIP, ip.String())
	}
	return nil
}

// limitedGet performs a GET against u (already validated by the caller via
// validateURL) and returns up to maxBytes of the response body. It always
// closes the response body.
func (d *Discoverer) limitedGet(ctx context.Context, u *url.URL, accept string, maxBytes int64) ([]byte, *http.Response, error) {
	if err := d.validateURL(u); err != nil {
		return nil, nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("User-Agent", userAgentDiscover)
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	resp, err := d.httpClient.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()

	// The final URL after any redirect must also be allowlisted —
	// CheckRedirect already enforces this per-hop, but re-check the
	// terminal response's host defensively in case a future Go stdlib
	// change alters CheckRedirect's invocation guarantees.
	if resp.Request != nil && resp.Request.URL != nil {
		if err := d.validateURL(resp.Request.URL); err != nil {
			return nil, resp, err
		}
	}

	lr := io.LimitReader(resp.Body, maxBytes+1)
	body, err := io.ReadAll(lr)
	if err != nil {
		return nil, resp, err
	}
	if int64(len(body)) > maxBytes {
		return nil, resp, ErrDownloadTooLarge
	}
	return body, resp, nil
}

// ---- GitHub API response shapes (only the fields we use) ----

type ghAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
}

type ghRelease struct {
	TagName     string    `json:"tag_name"`
	Name        string    `json:"name"`
	Body        string    `json:"body"`
	Draft       bool      `json:"draft"`
	Prerelease  bool      `json:"prerelease"`
	PublishedAt time.Time `json:"published_at"`
	Assets      []ghAsset `json:"assets"`
}

type ghCommitRef struct {
	SHA string `json:"sha"`
}

type ghTagRef struct {
	Object ghCommitRef `json:"object"`
}

// apiURL builds a validated https URL against the configured API host for
// path (which must start with "/"). It never accepts caller-controlled
// host/scheme input.
func (d *Discoverer) apiURL(path string, query url.Values) *url.URL {
	u := &url.URL{
		Scheme: d.scheme,
		Host:   d.apiHost,
		Path:   path,
	}
	if query != nil {
		u.RawQuery = query.Encode()
	}
	return u
}

// FindLatestRelease queries the GitHub API for the latest release on the
// given channel, restricted to reachfm/orvix. installedVersion (already
// validated by the caller, e.g. via ValidateVersionString) is threaded
// through so the returned ReleaseInfo can report blockers like
// same-version/downgrade without a second round trip.
func (d *Discoverer) FindLatestRelease(ctx context.Context, channel Channel, installedVersion string) (*ghRelease, error) {
	switch channel {
	case ChannelStable:
		u := d.apiURL(fmt.Sprintf("/repos/%s/%s/releases/latest", githubOwner, githubRepo), nil)
		body, resp, err := d.limitedGet(ctx, u, "application/vnd.github+json", maxMetadataBytes)
		if err != nil {
			return nil, fmt.Errorf("fetching latest stable release: %w", err)
		}
		if resp.StatusCode == http.StatusNotFound {
			return nil, ErrNoReleaseFound
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("selfupdate: GitHub API returned status %d for latest release", resp.StatusCode)
		}
		var rel ghRelease
		if err := json.Unmarshal(body, &rel); err != nil {
			return nil, fmt.Errorf("selfupdate: malformed release JSON: %w", err)
		}
		if rel.Draft {
			return nil, ErrNoReleaseFound
		}
		return &rel, nil

	case ChannelPrerelease:
		// GitHub has no "/releases/latest including prereleases" endpoint —
		// /releases/latest explicitly excludes prereleases and drafts. List
		// releases (already newest-first) and take the first non-draft.
		u := d.apiURL(fmt.Sprintf("/repos/%s/%s/releases", githubOwner, githubRepo), url.Values{"per_page": {"10"}})
		body, resp, err := d.limitedGet(ctx, u, "application/vnd.github+json", maxMetadataBytes)
		if err != nil {
			return nil, fmt.Errorf("fetching release list: %w", err)
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("selfupdate: GitHub API returned status %d for release list", resp.StatusCode)
		}
		var rels []ghRelease
		if err := json.Unmarshal(body, &rels); err != nil {
			return nil, fmt.Errorf("selfupdate: malformed release list JSON: %w", err)
		}
		// Defensive: GitHub already returns newest-first by created_at, but
		// don't trust ordering from a network response for anything
		// security-relevant (we don't rely on it beyond picking "latest"
		// for display, since VerifyBundle independently enforces
		// downgrade-rejection using the parsed version, not list order).
		sort.SliceStable(rels, func(i, j int) bool { return rels[i].PublishedAt.After(rels[j].PublishedAt) })
		for _, r := range rels {
			if r.Draft {
				continue
			}
			r := r
			return &r, nil
		}
		return nil, ErrNoReleaseFound

	default:
		return nil, fmt.Errorf("selfupdate: unknown channel %q", channel)
	}
}

// resolveTagCommit looks up the commit SHA a tag points at via the GitHub
// API (GET /repos/{owner}/{repo}/git/ref/tags/{tag}), so the target commit
// comes from GitHub itself rather than being guessed or trusted from the
// manifest alone.
func (d *Discoverer) resolveTagCommit(ctx context.Context, tag string) (string, error) {
	u := d.apiURL(fmt.Sprintf("/repos/%s/%s/git/ref/tags/%s", githubOwner, githubRepo, url.PathEscape(tag)), nil)
	body, resp, err := d.limitedGet(ctx, u, "application/vnd.github+json", maxMetadataBytes)
	if err != nil {
		return "", fmt.Errorf("resolving tag commit: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("selfupdate: GitHub API returned status %d resolving tag %q", resp.StatusCode, tag)
	}
	var ref ghTagRef
	if err := json.Unmarshal(body, &ref); err != nil {
		return "", fmt.Errorf("selfupdate: malformed tag ref JSON: %w", err)
	}
	sha := ref.Object.SHA
	// Annotated tags point at a tag object, not the commit directly; GitHub
	// resolves this the same way for the `git/ref` endpoint returning the
	// tag object's SHA in that case. Either way this is GitHub's own
	// answer for "what does this tag point to" — good enough for the
	// commit cross-check against the manifest, which is a defense-in-depth
	// consistency check, not the primary trust anchor (VerifyBundle's
	// signature check is).
	if sha == "" {
		return "", fmt.Errorf("selfupdate: tag %q resolved to an empty commit SHA", tag)
	}
	return sha, nil
}

// findAsset returns the release asset whose name matches pred, or nil.
func findAsset(rel *ghRelease, pred func(name string) bool) *ghAsset {
	for i := range rel.Assets {
		if pred(rel.Assets[i].Name) {
			return &rel.Assets[i]
		}
	}
	return nil
}

// selectArtifactAsset picks the main release artifact: the asset that is
// NOT a checksum/signature/manifest sidecar and whose name indicates the
// wanted architecture.
func selectArtifactAsset(rel *ghRelease) *ghAsset {
	for i := range rel.Assets {
		name := rel.Assets[i].Name
		if strings.HasSuffix(name, requiredAssetSuffixes.checksum) ||
			strings.HasSuffix(name, requiredAssetSuffixes.signature) ||
			strings.HasSuffix(name, requiredAssetSuffixes.manifest) {
			continue
		}
		if strings.Contains(name, wantArchLabel) {
			return &rel.Assets[i]
		}
	}
	return nil
}

// DiscoverResult bundles the resolved+verified release metadata together
// with the raw verified artifact bytes, so a caller (Phase G's install
// pipeline) never has to re-download or re-verify.
type DiscoverResult struct {
	Info     ReleaseInfoFull
	Verified *VerifiedBundle
	Artifact []byte
}

// ReleaseInfoFull extends the Phase D ReleaseInfo with the discovery-time
// fields the requirements call for: installed/available version
// comparison, compatibility flags, and blockers. ReleaseInfo itself (in
// types.go) is left untouched so existing callers/tests of that struct are
// unaffected; this struct embeds it.
type ReleaseInfoFull struct {
	ReleaseInfo

	InstalledVersion string `json:"installed_version"`
	AvailableVersion string `json:"available_version"`
	TargetCommit     string `json:"target_commit"`
	Architecture     string `json:"architecture"`

	NeedsMigration bool `json:"needs_migration"`
	NeedsRestart   bool `json:"needs_restart"`
	Compatible     bool `json:"compatible"`

	Blockers []string `json:"blockers,omitempty"`
}

// blocker string constants — stable identifiers a UI/API layer can key
// off of, rather than parsing prose.
const (
	BlockerDowngrade         = "downgrade"
	BlockerSameVersion       = "same_version"
	BlockerUnofficialAssets  = "unofficial_assets"
	BlockerWrongArchitecture = "wrong_architecture"
	BlockerChecksumMismatch  = "checksum_mismatch"
	BlockerSignatureMismatch = "signature_mismatch"
	BlockerManifestMalformed = "manifest_malformed"
	BlockerCommitMismatch    = "commit_mismatch"
	BlockerVersionInconsist  = "version_inconsistent"
)

// DiscoverRelease finds, downloads, and independently verifies the latest
// release on channel, restricted to reachfm/orvix. installedVersion must
// already be a validated version string (ValidateVersionString) — the
// caller is expected to have it from local state, not from any untrusted
// source. trustedPublicKeyPEM was supplied at construction time via
// NewDiscoverer.
//
// A non-nil error is returned only for operational/transport failures
// (network error, malformed API response, host not allowlisted, etc).
// Verification failures that are meaningful "this release cannot be
// installed" outcomes (unofficial assets, bad checksum/signature, wrong
// architecture, downgrade, same version, commit mismatch) are instead
// surfaced via DiscoverResult.Info.Blockers with Compatible=false, and
// DiscoverResult.Verified/Artifact left nil/empty — the caller can render
// a helpful message without treating every rejection as an exceptional
// error.
func (d *Discoverer) DiscoverRelease(ctx context.Context, channel Channel, installedVersion string) (*DiscoverResult, error) {
	if err := ValidateVersionString(installedVersion); err != nil {
		return nil, fmt.Errorf("installed version: %w", err)
	}

	rel, err := d.FindLatestRelease(ctx, channel, installedVersion)
	if err != nil {
		return nil, err
	}

	info := ReleaseInfoFull{
		ReleaseInfo: ReleaseInfo{
			Tag:         rel.TagName,
			Version:     strings.TrimPrefix(rel.TagName, "v"),
			Channel:     string(channel),
			PublishedAt: rel.PublishedAt,
			Prerelease:  rel.Prerelease,
		},
		InstalledVersion: installedVersion,
		Architecture:     wantArchLabel,
	}
	result := &DiscoverResult{Info: info}

	// Version sanity before spending a single byte of bandwidth on assets.
	if err := ValidateVersionString(info.Version); err != nil {
		result.Info.Blockers = append(result.Info.Blockers, BlockerVersionInconsist)
		return result, nil
	}
	if cmp, err := compareVersions(info.Version, installedVersion); err != nil {
		result.Info.Blockers = append(result.Info.Blockers, BlockerVersionInconsist)
		return result, nil
	} else if cmp == 0 {
		result.Info.Blockers = append(result.Info.Blockers, BlockerSameVersion)
		return result, nil
	} else if cmp < 0 {
		result.Info.Blockers = append(result.Info.Blockers, BlockerDowngrade)
		return result, nil
	}
	result.Info.AvailableVersion = info.Version

	artifactAsset := selectArtifactAsset(rel)
	checksumAsset := findAsset(rel, func(n string) bool { return strings.HasSuffix(n, requiredAssetSuffixes.checksum) })
	sigAsset := findAsset(rel, func(n string) bool { return strings.HasSuffix(n, requiredAssetSuffixes.signature) })
	manifestAsset := findAsset(rel, func(n string) bool { return strings.HasSuffix(n, requiredAssetSuffixes.manifest) })

	if artifactAsset == nil {
		result.Info.Blockers = append(result.Info.Blockers, BlockerWrongArchitecture)
		return result, nil
	}
	result.Info.AssetName = artifactAsset.Name
	if checksumAsset == nil || sigAsset == nil || manifestAsset == nil {
		result.Info.Blockers = append(result.Info.Blockers, BlockerUnofficialAssets)
		return result, nil
	}
	result.Info.ChecksumSidecar = checksumAsset.Name
	result.Info.SignatureSidecar = sigAsset.Name
	result.Info.ManifestAsset = manifestAsset.Name

	if !strings.Contains(artifactAsset.Name, wantArchLabel) {
		result.Info.Blockers = append(result.Info.Blockers, BlockerWrongArchitecture)
		return result, nil
	}

	// Download all four assets, each independently size-capped.
	artifactBytes, err := d.downloadAsset(ctx, artifactAsset.BrowserDownloadURL, maxArtifactBytes)
	if err != nil {
		return nil, fmt.Errorf("downloading artifact: %w", err)
	}
	checksumBytes, err := d.downloadAsset(ctx, checksumAsset.BrowserDownloadURL, maxSidecarBytes)
	if err != nil {
		return nil, fmt.Errorf("downloading checksum sidecar: %w", err)
	}
	sigBytes, err := d.downloadAsset(ctx, sigAsset.BrowserDownloadURL, maxSidecarBytes)
	if err != nil {
		return nil, fmt.Errorf("downloading signature sidecar: %w", err)
	}
	manifestBytes, err := d.downloadAsset(ctx, manifestAsset.BrowserDownloadURL, maxSidecarBytes)
	if err != nil {
		return nil, fmt.Errorf("downloading manifest: %w", err)
	}

	verified, verr := VerifyBundle(rel.TagName, artifactAsset.Name, artifactBytes, checksumBytes, sigBytes, manifestBytes, d.trustedPublicKeyPEM, installedVersion)
	if verr != nil {
		result.Info.Blockers = append(result.Info.Blockers, classifyVerifyError(verr))
		return result, nil
	}

	// Manifest target/commit cross-check against GitHub's own record of
	// what the tag points to, if the manifest carries a commit (it does —
	// Manifest.Commit).
	if verified.Commit != "" {
		targetCommit, cerr := d.resolveTagCommit(ctx, rel.TagName)
		if cerr != nil {
			return nil, fmt.Errorf("resolving release target commit: %w", cerr)
		}
		result.Info.TargetCommit = targetCommit
		if !strings.EqualFold(targetCommit, verified.Commit) {
			result.Info.Blockers = append(result.Info.Blockers, BlockerCommitMismatch)
			return result, nil
		}
	}

	result.Info.NeedsMigration = releaseNeedsMigration(installedVersion, verified.Version)
	result.Info.NeedsRestart = true // every install replaces the running binary
	result.Info.Compatible = true
	result.Verified = verified
	result.Artifact = artifactBytes
	return result, nil
}

// classifyVerifyError maps a VerifyBundle error to a stable blocker code.
func classifyVerifyError(err error) string {
	if errors.Is(err, ErrDowngrade) {
		return BlockerDowngrade
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "checksum"):
		return BlockerChecksumMismatch
	case strings.Contains(msg, "signature"):
		return BlockerSignatureMismatch
	case strings.Contains(msg, "malformed manifest"):
		return BlockerManifestMalformed
	case strings.Contains(msg, "version mismatch"):
		return BlockerVersionInconsist
	default:
		return BlockerUnofficialAssets
	}
}

// releaseNeedsMigration is a conservative placeholder: any minor or major
// version bump is assumed to need a DB migration pass (the migration
// runner itself is a no-op if there is nothing pending); only a patch-only
// bump is assumed migration-free. This keeps Phase E honest about what it
// actually knows (it does not parse migration manifests) while still
// giving Phase F/G a useful default to preflight against.
func releaseNeedsMigration(installed, available string) bool {
	iCore, _ := splitVersion(installed)
	aCore, _ := splitVersion(available)
	iParts := strings.SplitN(iCore, ".", 3)
	aParts := strings.SplitN(aCore, ".", 3)
	if len(iParts) < 2 || len(aParts) < 2 {
		return true
	}
	return iParts[0] != aParts[0] || iParts[1] != aParts[1]
}

// downloadAsset validates and fetches a GitHub asset URL. GitHub asset
// download URLs are normally api.github.com URLs that redirect once to an
// objects.githubusercontent.com/github-releases.githubusercontent.com
// signed URL; both the initial host and the redirect target must be
// allowlisted, which limitedGet + the http.Client's CheckRedirect jointly
// enforce.
func (d *Discoverer) downloadAsset(ctx context.Context, rawURL string, maxBytes int64) ([]byte, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("selfupdate: malformed asset URL: %w", err)
	}
	body, resp, err := d.limitedGet(ctx, u, "application/octet-stream", maxBytes)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("selfupdate: asset download returned status %d", resp.StatusCode)
	}
	return body, nil
}
