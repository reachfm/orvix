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
)

type WebPushSender struct {
	vapid  VAPIDConfig
	client *http.Client
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
// POST to. It enforces:
//   - scheme MUST be https
//   - no userinfo (embedded credentials)
//   - host MUST NOT be localhost or a loopback IP
//   - host MUST NOT be a private / RFC1918 / RFC6598 / link-local /
//     unique-local / metadata IP range
//   - hostname MUST resolve to at least one public IPv4 address
//     (or be a known push service domain)
//   - port MUST NOT be a well-known non-HTTPS unsafe port
//     (< 1024 except 443), unless the port is explicitly allowed
//     by the allowlist.
//   - known push service domains (FCM, Mozilla, Apple) bypass
//     DNS resolution for reliability (they already use public IPs).
//
// The check is conservative: if there is any doubt about the safety
// of the endpoint, the endpoint is rejected.
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

	// Reject bare IPs that are private.
	if ip := net.ParseIP(host); ip != nil {
		if isPrivateIP(ip) {
			return fmt.Errorf(ErrEndpointPrivateIP)
		}
		return nil
	}

	// Reject localhost hostnames.
	lowerHost := strings.ToLower(host)
	if lowerHost == "localhost" || strings.HasPrefix(lowerHost, "localhost.") {
		return fmt.Errorf(ErrEndpointLocalhost)
	}

	// Reject non-public TLDs / internal suffixes.
	if isInternalHostname(lowerHost) {
		return fmt.Errorf("push endpoint hostname %q appears to be internal", host)
	}

	// Reject known-bad ports (< 1024 and not 443, not common
	// push-service ports). Known push services use 443 or 8443;
	// we also allow 8443 for Mozilla autopush.
	unsafePort := false
	switch port {
	case "":
		// default HTTPS port — safe
	case "443", "8443":
		// allowed
	default:
		p := port
		if n := len(p); n > 0 && p[0] >= '1' && p[0] <= '9' {
			unsafePort = true
		}
	}
	if unsafePort {
		return fmt.Errorf(ErrEndpointUnsafePort)
	}

	// Known push services bypass DNS resolution.
	if isKnownPushService(lowerHost) {
		return nil
	}

	// DNS resolution: verify the host resolves to at least one
	// public IPv4 address.
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
		if !isPrivateIP(ip) {
			hasPublic = true
			break
		}
	}
	if !hasPublic {
		return fmt.Errorf(ErrEndpointPrivateIP)
	}
	return nil
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

// isInternalHostname returns true if the hostname looks like it
// belongs to a private/internal network.
func isInternalHostname(host string) bool {
	parts := strings.Split(host, ".")
	if len(parts) < 2 {
		return true // bare hostname with no dot
	}
	suffix := parts[len(parts)-1]
	switch suffix {
	case "local", "internal", "corp", "home", "lan", "intranet",
		"private", "test", "example", "invalid", "localhost":
		return true
	}
	// .arpa is used for reverse DNS — never a valid push endpoint.
	if suffix == "arpa" {
		return true
	}
	return false
}

// isKnownPushService returns true for well-known browser push
// service hostnames. These are always public and bypass DNS
// resolution for reliability.
func isKnownPushService(host string) bool {
	switch {
	case strings.HasSuffix(host, ".fcm.googleapis.com"),
		strings.HasSuffix(host, ".fcm-notifications.googleapis.com"),
		host == "fcm.googleapis.com",
		host == "fcm-notifications.googleapis.com":
		return true
	case strings.HasSuffix(host, ".updates.push.services.mozilla.com"),
		strings.HasSuffix(host, ".push.mozilla.com"),
		host == "updates.push.services.mozilla.com",
		host == "push.mozilla.com":
		return true
	case strings.HasSuffix(host, ".push.apple.com"),
		strings.HasSuffix(host, ".web.push.apple.com"),
		host == "push.apple.com",
		host == "web.push.apple.com":
		return true
	default:
		return false
	}
}

func (s *WebPushSender) Send(ctx context.Context, sub *PushSubscription, payload []byte) error {
	if sub.DisabledAt != nil || s.vapid.PrivateKey == "" {
		return nil
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
