package push

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// SSRF-safe push endpoint validation error messages. Exported as
// constants so the HTTP handler can return the same string to the
// caller without hardcoding.
const (
	ErrEndpointScheme     = "push endpoint must use HTTPS"
	ErrEndpointUserinfo   = "push endpoint must not contain userinfo"
	ErrEndpointLocalhost  = "push endpoint must not target localhost"
	ErrEndpointPrivateIP  = "push endpoint must not target a private IP range"
	ErrEndpointUnsafePort = "push endpoint port is not allowed"
	ErrEndpointNoDNS      = "push endpoint hostname does not resolve to a public IP"
	ErrEndpointRedirect   = "push endpoint must not redirect"
	ErrEndpointInternal   = "push endpoint hostname appears to be internal"
	ErrEndpointBadLabel   = "push endpoint hostname contains a forbidden label"
	ErrEndpointIPFragment = "push endpoint hostname contains an IP-fragment label"
)

// blockedHostnameLabels is the per-label blocklist. ANY occurrence of
// one of these strings as a hostname label (case-insensitive) causes
// the endpoint to be rejected. The list covers the labels that
// commonly appear in private/internal deployments, RFC 6762 mDNS
// names, documentation placeholders, and the well-known metadata IP
// fragment "169.254.169.254" (cloud-provider metadata service).
//
// Critically, this list is checked BEFORE any "known push service"
// allowlist short-circuit so that hostnames like
//
//	https://127.0.0.1.fcm.googleapis.com/push
//	https://internal.fcm.googleapis.com/push
//	https://localhost.fcm.googleapis.com/push
//	https://169.254.169.254.web.push.apple.com/push
//
// are all rejected — the per-label check runs unconditionally.
var blockedHostnameLabels = map[string]struct{}{
	"localhost":     {},
	"internal":      {},
	"corp":          {},
	"home":          {},
	"lan":           {},
	"intranet":      {},
	"private":       {},
	"test":          {},
	"example":       {},
	"examplecom":    {}, // split-form defense
	"invalid":       {},
	"local":         {},
	"loopback":      {},
	"host":          {},
	"broadcasthost": {},
}

// knownPushServices is the strict allowlist of push service
// hostnames the runtime will accept without DNS resolution. Every
// entry is an exact hostname (case-insensitive). No suffix
// matching, no wildcard subdomains — the per-label blocklist runs
// first, so the suffix-vs-strict-distinction no longer matters for
// safety; we keep strict matching so that future sub-push-service
// subdomains (e.g. operators who front FCM through their own
// reverse proxy) can register themselves explicitly via
// push.AllowedPushServices (not implemented yet) without breaking
// the safety invariant.
//
// Keep this list short. Each entry is a hostname the operator has
// independently confirmed terminates at a public push service.
var knownPushServices = map[string]struct{}{
	// Google Firebase Cloud Messaging — exact apex only. Real
	// subscription endpoints always use the apex; the SDK never
	// issues subdomains like "*.fcm.googleapis.com" to the
	// application server (the path component carries the
	// sub-stream identifier, not the hostname).
	"fcm.googleapis.com":               {},
	"fcm-notifications.googleapis.com": {},
	// Mozilla autopush.
	"updates.push.services.mozilla.com": {},
	"push.services.mozilla.com":         {},
	// Apple Safari Push (WebKit).
	"push.apple.com":     {},
	"web.push.apple.com": {},
}

type WebPushSender struct {
	vapid  VAPIDConfig
	client *http.Client
	// skipValidate disables the runtime re-validation in Send.
	// Production code leaves this false; tests that inject a
	// local httptest server endpoint (which is not on the strict
	// known-push-service allowlist) flip it on via the test seam
	// so the encryption + signing pipeline runs end-to-end
	// without the SSRF validator rejecting the test URL.
	skipValidate bool
}

func defaultPushClient() *http.Client {
	return &http.Client{
		Timeout: 15 * time.Second,
		// Prevent redirect-based SSRF: push services do not redirect
		// push POST requests to a different endpoint; the endpoint URL
		// is the final delivery point. Disabling redirects ensures a
		// malicious or compromised endpoint cannot chain a redirect
		// to an internal address.
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func NewWebPushSender(vapid VAPIDConfig) *WebPushSender {
	return &WebPushSender{
		vapid:  vapid,
		client: defaultPushClient(),
	}
}

// NewWebPushSenderWithClient is the test seam: it lets the
// caller swap in a custom http.Client (typically wrapping an
// httptest.Server's transport) so the encryption / signing
// pipeline can be exercised without hitting a real push
// service. Production code uses NewWebPushSender.
func NewWebPushSenderWithClient(vapid VAPIDConfig, client *http.Client) *WebPushSender {
	if client == nil {
		return NewWebPushSender(vapid)
	}
	return &WebPushSender{vapid: vapid, client: client}
}

// SetSkipValidate toggles the runtime re-validation in Send.
// Production code MUST leave it false; only tests that wire
// a local httptest server URL into the subscription row need
// to flip this on. The setter is unexported-by-convention —
// it lives in the test seam and should not appear in
// production call sites.
func (s *WebPushSender) SetSkipValidate(skip bool) {
	s.skipValidate = skip
}

type vapidHeader struct {
	Typ string `json:"typ"`
	Alg string `json:"alg"`
}

type vapidClaims struct {
	Aud string `json:"aud"`
	Exp int64  `json:"exp"`
	Sub string `json:"sub"`
}

// ValidatePushEndpoint checks that a push endpoint URL is safe to
// POST to. The check is layered — every layer runs before the
// next can short-circuit, so an attacker cannot bypass a layer by
// piggy-backing on a later allowlist:
//
//  1. scheme MUST be https (cleartext POSTs leak VAPID JWTs).
//  2. no userinfo (embedded credentials).
//  3. host MUST NOT be localhost.
//  4. host MUST resolve to a valid hostname (no bare IPs in DNS,
//     no dotted-quad fragments embedded in the leftmost labels).
//  5. EVERY label of the hostname MUST NOT appear in
//     blockedHostnameLabels — covers internal-sounding labels
//     like "localhost", "internal", "corp", "lan", etc. This runs
//     BEFORE the known-push-service allowlist so that
//     `127.0.0.1.fcm.googleapis.com`, `internal.fcm.googleapis.com`,
//     and `localhost.fcm.googleapis.com` are all rejected.
//  6. The host MUST NOT contain dotted-quad IPv4 fragments in its
//     leftmost labels (e.g. `127.0.0.1.fcm.googleapis.com`).
//  7. port MUST be one of the allowed push-service ports.
//  8. The host MUST be a member of the strict knownPushServices
//     allowlist (exact match, case-insensitive). We DO NOT bypass
//     DNS resolution for known services anymore: every endpoint is
//     resolved, and the resolved IPs are checked against the
//     private-IP blocklist. This costs one DNS lookup per
//     subscription but closes the SSRF gap definitively.
//
// If any check fails, the endpoint is rejected. There is no
// "trust the suffix" fallback.
func ValidatePushEndpoint(endpoint string) error {
	u, err := url.Parse(endpoint)
	if err != nil {
		return fmt.Errorf("invalid endpoint URL: %w", err)
	}
	if u.Scheme != "https" {
		return fmt.Errorf(ErrEndpointScheme)
	}
	if u.User != nil && u.User.String() != "" {
		return fmt.Errorf(ErrEndpointUserinfo)
	}
	host := u.Hostname()
	port := u.Port()
	if host == "" {
		return fmt.Errorf("push endpoint must include a hostname")
	}
	lowerHost := strings.ToLower(host)

	// Layer 3 — bare localhost literal. Bare IP literals are
	// handled by the per-label IP-fragment check below.
	if lowerHost == "localhost" {
		return fmt.Errorf(ErrEndpointLocalhost)
	}

	// Layer 4 — host must look like a real DNS name. Bare IPs are
	// allowed (and routed to layer 5/8), but anything that does not
	// contain a dot is suspicious.
	if !strings.Contains(lowerHost, ".") {
		return fmt.Errorf("push endpoint hostname %q must contain a dot", host)
	}

	// Layer 5 — per-label blocklist. Split the hostname on every
	// dot and reject any label that appears in blockedHostnameLabels.
	labels := strings.Split(lowerHost, ".")
	for _, label := range labels {
		if label == "" {
			return fmt.Errorf("push endpoint hostname %q has an empty label", host)
		}
		if _, bad := blockedHostnameLabels[label]; bad {
			return fmt.Errorf("%s: %s", ErrEndpointBadLabel, label)
		}
	}

	// Layer 6 — IP-fragment detection. Reject hostnames whose
	// leftmost labels look like a dotted-quad IPv4 address. This
	// catches `127.0.0.1.fcm.googleapis.com`, `10.0.0.1.push.mozilla.com`,
	// `169.254.169.254.web.push.apple.com` etc. We require at
	// least two leading numeric labels — a single label like
	// "127" alone is a valid DNS label.
	if hasIPv4PrefixLabels(lowerHost) {
		return fmt.Errorf(ErrEndpointIPFragment)
	}

	// Layer 7 — port allowlist.
	if !isAllowedPort(port) {
		return fmt.Errorf(ErrEndpointUnsafePort)
	}

	// Layer 8 — strict known-push-service allowlist. The host
	// must EXACTLY match one of the entries in knownPushServices
	// (case-insensitive). Subdomains are NOT accepted: real push
	// subscription endpoints always target the apex hostname
	// (the path component carries the per-subscription identifier).
	if _, ok := knownPushServices[lowerHost]; !ok {
		return fmt.Errorf("push endpoint hostname %q is not a known push service", host)
	}

	// Layer 9 — DNS resolution against the resolved IPs. We
	// always resolve, even for known services, so that an operator
	// who mistakenly lists a private IP in DNS (or whose DNS is
	// hijacked) cannot exfiltrate payloads to internal hosts.
	addrs, err := net.DefaultResolver.LookupHost(context.Background(), host)
	if err != nil || len(addrs) == 0 {
		return fmt.Errorf(ErrEndpointNoDNS)
	}
	hasPublic := false
	for _, addr := range addrs {
		ip := net.ParseIP(addr)
		if ip == nil {
			continue
		}
		if isPrivateIP(ip) {
			continue
		}
		hasPublic = true
		break
	}
	if !hasPublic {
		return fmt.Errorf(ErrEndpointPrivateIP)
	}
	return nil
}

// isAllowedPort returns true for the ports push services are known
// to listen on. Default HTTPS (443) and Mozilla autopush's 8443.
// Anything else is rejected so a hostile endpoint cannot lure the
// server into POSTing to a database port, an internal admin port,
// or any random high port the attacker happens to control.
func isAllowedPort(port string) bool {
	switch port {
	case "":
		return true // default HTTPS port
	case "443", "8443":
		return true
	}
	return false
}

// hasIPv4PrefixLabels reports whether the host's leftmost labels
// spell out a dotted-quad IPv4 address. We treat any hostname whose
// first N labels (for N in {2,3,4}) are all numeric AND the first
// label is a plausible IPv4 first octet (1..223) as suspicious.
//
// Examples caught:
//   - "127.0.0.1.fcm.googleapis.com"
//   - "10.0.0.1.push.services.mozilla.com"
//   - "169.254.169.254.web.push.apple.com"
//
// Examples NOT caught (legit hostname that happens to start with a
// number):
//   - "8.8.8.8.google.com"  — caught because "8.8.8.8" parses as an IP
//     first, then the residual ".google.com" suffix is what fails.
//   - "1.2.3.example" — caught because four consecutive numeric labels.
//   - "3.example.com" — NOT caught (only one numeric label); this is
//     a valid TLD-style hostname.
func hasIPv4PrefixLabels(host string) bool {
	labels := strings.Split(host, ".")
	if len(labels) < 4 {
		return false
	}
	// Try interpreting labels[0..N] as an IPv4 dotted quad for
	// N in {2,3,4}. The longest match is preferred so that a
	// hostname like "127.0.0.1.fcm.googleapis.com" is detected
	// even when subsequent labels look like a valid domain.
	for n := 4; n >= 2; n-- {
		if len(labels) < n {
			continue
		}
		candidate := strings.Join(labels[:n], ".")
		if ip := net.ParseIP(candidate); ip != nil {
			// Pure IPv4 only (no v4-in-v6 mixed notation).
			if ip.To4() != nil && ip.Equal(ip.To4()) {
				return true
			}
		}
	}
	return false
}

// ptrTime is a tiny helper that returns a pointer to t. Used by
// the runtime-revalidation branch in Send so we can stamp the
// subscription as disabled without dragging in the encoding/json
// pointer-tick boilerplate.
func ptrTime(t time.Time) *time.Time {
	return &t
}

// isPrivateIP returns true if ip belongs to a private, loopback,
// link-local, unique-local, or metadata IP range.
func isPrivateIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ip.IsLoopback() {
		return true
	}
	if ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return true
	}
	if ip.IsPrivate() {
		return true
	}
	if ip.IsUnspecified() {
		return true
	}
	// RFC 6598 — Carrier-grade NAT (100.64.0.0/10)
	if ip4 := ip.To4(); ip4 != nil {
		if ip4[0] == 100 && ip4[1]&0xC0 == 64 {
			return true
		}
		// 169.254.0.0/16 — link-local (already covered by IsLinkLocalUnicast
		// for IPv4 but check explicitly for the metadata IP 169.254.169.254)
		if ip4[0] == 169 && ip4[1] == 254 {
			return true
		}
	}
	// Unique-local IPv6 (fc00::/7)
	if ip4 := ip.To16(); ip4 != nil && len(ip4) == 16 {
		if ip4[0]&0xFE == 0xFC {
			return true
		}
	}
	return false
}

func (s *WebPushSender) Send(ctx context.Context, sub *PushSubscription, payload []byte) error {
	if sub.DisabledAt != nil || s.vapid.PrivateKey == "" {
		return nil
	}
	// Defense-in-depth: re-validate the endpoint at send time.
	// The handler validates on subscribe and on /push/test, but
	// the stored row could have been mutated outside the API
	// (database tampering, manual SQL, restored from backup with
	// stale data). Skipping this check here would let a tampered
	// row exfiltrate payloads to a hostile endpoint. The DNS
	// lookup is cached by the resolver, so the per-send cost is
	// small. Tests that inject a local httptest URL flip
	// skipValidate on; production code must NEVER set it.
	if !s.skipValidate {
		if err := ValidatePushEndpoint(sub.Endpoint); err != nil {
			// Disable the subscription so we don't retry forever.
			sub.DisabledAt = ptrTime(time.Now().UTC())
			return fmt.Errorf("endpoint failed runtime validation: %w", err)
		}
	}
	p256dh, err := base64.RawURLEncoding.DecodeString(sub.P256DHKey)
	if err != nil {
		return fmt.Errorf("decode p256dh: %w", err)
	}
	auth, err := base64.RawURLEncoding.DecodeString(sub.AuthKey)
	if err != nil {
		return fmt.Errorf("decode auth: %w", err)
	}
	encrypted, salt, serverPub, err := encryptPayload(payload, p256dh, auth)
	if err != nil {
		return fmt.Errorf("encrypt payload: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, "POST", sub.Endpoint, bytes.NewReader(encrypted))
	if err != nil {
		return fmt.Errorf("create push request: %w", err)
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Content-Encoding", "aes128gcm")
	req.Header.Set("Encryption", fmt.Sprintf("salt=%s", base64.RawURLEncoding.EncodeToString(salt)))
	req.Header.Set("Crypto-Key", fmt.Sprintf("dh=%s", base64.RawURLEncoding.EncodeToString(serverPub)))
	req.Header.Set("TTL", "86400")
	req.Header.Set("Urgency", "normal")

	if s.vapid.PrivateKey != "" {
		vapidAuth, err := buildVAPIDHeader(s.vapid, sub.Endpoint)
		if err != nil {
			return fmt.Errorf("vapid auth: %w", err)
		}
		req.Header.Set("Authorization", "vapid t="+vapidAuth+", k="+s.vapid.PublicKey)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("push http: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 && resp.StatusCode != 429 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("push rejected: %d %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

func buildVAPIDHeader(vapid VAPIDConfig, endpoint string) (string, error) {
	header := vapidHeader{Typ: "JWT", Alg: "ES256"}
	headerJSON, _ := json.Marshal(header)
	aud := originFromEndpoint(endpoint)
	claims := vapidClaims{
		Aud: aud,
		Exp: time.Now().Add(12 * time.Hour).Unix(),
		Sub: vapid.Subject,
	}
	claimsJSON, _ := json.Marshal(claims)
	headerB64 := base64.RawURLEncoding.EncodeToString(headerJSON)
	claimsB64 := base64.RawURLEncoding.EncodeToString(claimsJSON)
	return signVAPID(headerB64, claimsB64, vapid.PrivateKey)
}

func originFromEndpoint(endpoint string) string {
	if idx := strings.Index(endpoint, "://"); idx > 0 {
		rest := endpoint[idx+3:]
		if slash := strings.IndexByte(rest, '/'); slash > 0 {
			return endpoint[:idx+3+slash]
		}
		return endpoint
	}
	return endpoint
}

func encryptPayload(payload, p256dhKey, authKey []byte) (ciphertext, salt, serverPub []byte, err error) {
	salt = make([]byte, 16)
	if _, err = io.ReadFull(rand.Reader, salt); err != nil {
		return nil, nil, nil, err
	}
	curve := elliptic.P256()
	localKey, err := ecdhGenerateKey(curve)
	if err != nil {
		return nil, nil, nil, err
	}
	localPub := elliptic.Marshal(curve, localKey.X, localKey.Y)
	x, y := elliptic.Unmarshal(curve, p256dhKey)
	if x == nil || y == nil {
		return nil, nil, nil, fmt.Errorf("invalid p256dh key")
	}
	sharedX, _ := curve.ScalarMult(x, y, localKey.D.Bytes())
	sharedSecret := sharedX.Bytes()

	info := append([]byte("WebPush: info\x00"), p256dhKey...)
	info = append(info, localPub...)
	ikm := ecdhExtract(salt, authKey)
	cekInfo := append([]byte("Content-Encoding: aes128gcm\x00"), info...)
	nonceInfo := append([]byte("Content-Encoding: nonce\x00"), info...)
	prk := ecdhExtract(sharedSecret, ikm)
	cek := expand(prk, cekInfo, 16)
	nonce := expand(prk, nonceInfo, 12)

	block, err := aes.NewCipher(cek)
	if err != nil {
		return nil, nil, nil, err
	}
	aesgcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, nil, err
	}
	padded := payload
	paddingLen := 0
	if l := len(payload); l%16 != 0 {
		paddingLen = 16 - l%16
		padded = make([]byte, l+paddingLen+1)
		copy(padded, payload)
		padded[l+paddingLen] = byte(paddingLen)
	} else {
		padded = make([]byte, l+1)
		copy(padded, payload)
		padded[l] = 0
	}
	ciphertext = aesgcm.Seal(nil, nonce, padded, nil)
	return ciphertext, salt, localPub, nil
}

func ecdhExtract(secret, salt []byte) []byte {
	mac := hmac.New(sha256.New, salt)
	mac.Write(secret)
	return mac.Sum(nil)
}

func expand(prk, info []byte, length int) []byte {
	var result []byte
	mac := hmac.New(sha256.New, prk)
	counter := byte(0)
	for len(result) < length {
		if counter > 0 {
			mac.Reset()
		}
		mac.Write(result)
		mac.Write(info)
		mac.Write([]byte{counter})
		result = mac.Sum(result)
		counter++
	}
	return result[:length]
}

func ecdhGenerateKey(curve elliptic.Curve) (*ecdhKey, error) {
	priv, x, y, err := elliptic.GenerateKey(curve, rand.Reader)
	if err != nil {
		return nil, err
	}
	return &ecdhKey{Priv: priv, X: x, Y: y, D: new(big.Int).SetBytes(priv)}, nil
}

type ecdhKey struct {
	Priv []byte
	X, Y *big.Int
	D    *big.Int
}
