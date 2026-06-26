package push

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/orvix/orvix/internal/coremail/storage"
)

// PushSubscription is the row model stored in push_subscriptions.
//
// IMPORTANT: P256DHKey and AuthKey are the per-subscriber encryption
// secrets. They MUST NEVER cross the HTTP boundary. The JSON tags below
// set `json:"-"` so even an accidental marshalling (e.g. via the JSON
// debug endpoint, admin tooling, or a future handler that forgets to
// use a view type) cannot leak them. Handlers that need to expose the
// subscription to the API surface must use SanitizedView (see below).
//
// Endpoint and UserAgent are NOT crypto secrets. Endpoint reveals the
// push service (FCM/Mozilla/Apple); UserAgent is non-sensitive. They
// are safe to surface but should be paired with a short fingerprint
// for UI purposes (see PushSubscriptionView.EndpointFingerprint).
type PushSubscription struct {
	ID         uint       `json:"id"`
	MailboxID  uint       `json:"mailbox_id"`
	Endpoint   string     `json:"-"`
	P256DHKey  string     `json:"-"`
	AuthKey    string     `json:"-"`
	UserAgent  string     `json:"-"`
	DisabledAt *time.Time `json:"disabled_at,omitempty"`
	LastSeenAt *time.Time `json:"last_seen_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

// PushSubscriptionView is the safe HTTP response shape. It omits
// every crypto secret and replaces the raw endpoint with a short
// fingerprint (host + last 8 chars of the path) so the UI can show
// "subscribed on FCM/Mozilla/Apple" without leaking the full URL.
// UserAgent is also redacted — exact browser / OS versions are an
// information disclosure and the UI does not need them.
type PushSubscriptionView struct {
	ID                  uint       `json:"id"`
	MailboxID           uint       `json:"mailbox_id"`
	EndpointFingerprint string     `json:"endpoint_fingerprint"`
	EndpointKind        string     `json:"endpoint_kind"`
	DisabledAt          *time.Time `json:"disabled_at,omitempty"`
	LastSeenAt          *time.Time `json:"last_seen_at,omitempty"`
	CreatedAt           time.Time  `json:"created_at"`
	UpdatedAt           time.Time  `json:"updated_at"`
}

// SanitizedView converts a PushSubscription row into its safe HTTP
// counterpart. The full endpoint, the per-subscriber encryption
// secrets, and the UserAgent never leave the server.
func (s *PushSubscription) SanitizedView() PushSubscriptionView {
	return PushSubscriptionView{
		ID:                  s.ID,
		MailboxID:           s.MailboxID,
		EndpointFingerprint: endpointFingerprint(s.Endpoint),
		EndpointKind:        endpointKind(s.Endpoint),
		DisabledAt:          s.DisabledAt,
		LastSeenAt:          s.LastSeenAt,
		CreatedAt:           s.CreatedAt,
		UpdatedAt:           s.UpdatedAt,
	}
}

// endpointKind classifies the push service from the endpoint URL.
// FCM (Google), Mozilla (autopush), Apple (Safari). Anything unknown
// returns "other" so the UI never crashes on a brand new vendor.
func endpointKind(endpoint string) string {
	lower := strings.ToLower(endpoint)
	switch {
	case strings.Contains(lower, "fcm.googleapis.com"), strings.Contains(lower, "fcm-notifications.googleapis.com"):
		return "fcm"
	case strings.Contains(lower, "updates.push.services.mozilla.com"), strings.Contains(lower, "push.mozilla.com"):
		return "mozilla"
	case strings.Contains(lower, "push.apple.com"), strings.Contains(lower, "web.push.apple.com"):
		return "apple"
	case strings.Contains(lower, "wns.windows.com"):
		return "wns"
	default:
		return "other"
	}
}

// endpointFingerprint returns a stable, non-reversible summary of the
// endpoint suitable for display in the UI: "<host>…<last8>" where
// <last8> is the trailing 8 characters of the endpoint path. The full
// endpoint URL is never returned to clients.
func endpointFingerprint(endpoint string) string {
	if endpoint == "" {
		return ""
	}
	scheme := ""
	rest := endpoint
	if idx := strings.Index(rest, "://"); idx > 0 {
		scheme = rest[:idx+3]
		rest = rest[idx+3:]
	}
	host := rest
	path := ""
	if slash := strings.IndexByte(rest, '/'); slash > 0 {
		host = rest[:slash]
		path = rest[slash+1:]
	}
	tail := path
	if len(tail) > 8 {
		tail = tail[len(tail)-8:]
	}
	if tail == "" {
		return scheme + host
	}
	return scheme + host + "…" + tail
}

type PushSubscriptionFilter struct {
	MailboxID *uint
	Disabled  *bool
	Limit     int
}

type VAPIDConfig struct {
	PublicKey  string `json:"public_key"`
	PrivateKey string `json:"-"`
	Subject    string `json:"subject"`
}

type PushNotifier struct {
	Store      *storage.MailStore
	Repo       SubscriptionRepository
	VAPID      VAPIDConfig
	Sender     *WebPushSender
}

func NewPushNotifier(ms *storage.MailStore, repo SubscriptionRepository, vapid VAPIDConfig) *PushNotifier {
	return &PushNotifier{
		Store:  ms,
		Repo:   repo,
		VAPID:  vapid,
		Sender: NewWebPushSender(vapid),
	}
}

// GenerateVAPIDKeys returns a fresh ECDSA P-256 VAPID keypair
// encoded as RFC 7515 / RFC 8292 raw-point + raw-scalar. The
// public key is the elliptic.Marshal of (X, Y) on P-256; the
// private key is the 32-byte big-endian scalar. Both are URL-safe
// base64 (no padding) so they can be pasted straight into YAML /
// env without escaping.
func GenerateVAPIDKeys() (publicKey, privateKey string, err error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", "", fmt.Errorf("generate vapid key: %w", err)
	}
	pub := elliptic.Marshal(key.Curve, key.X, key.Y)
	priv := make([]byte, 32)
	dBytes := key.D.Bytes()
	if n := len(dBytes); n <= 32 {
		copy(priv[32-n:], dBytes)
	} else {
		copy(priv, dBytes[:32])
	}
	return base64.RawURLEncoding.EncodeToString(pub),
		base64.RawURLEncoding.EncodeToString(priv), nil
}

// NotifyMailboxMessage dispatches a push notification to every
// active subscription of the given mailbox.
//
// Self-send filter: when recipientEmail is non-empty and matches
// fromAddr (case-insensitive), the notification is suppressed. The
// caller passes recipientEmail = the mailbox owner's primary
// address. This is the canonical fix for "I sent mail to myself from
// my own webmail session and got a notification on the device that
// sent it." Sender-equality is enforced here, NOT in the worker,
// because the worker does not always know the recipient's address
// at notify-time — only the mailbox ID — so we look it up here.
//
// subject is the message subject (≤100 chars after truncation). It
// is encrypted to each subscriber per RFC 8291 before the request
// is sent, so it is only visible to the recipient's browser.
//
// Failed sends with a 410 Gone status (or any "endpoint
// permanently removed" signal) mark the subscription as disabled
// so subsequent messages do not retry the dead endpoint.
func (pn *PushNotifier) NotifyMailboxMessage(ctx context.Context, mailboxID uint, messageID string, fromAddr string, subject string, recipientEmail string) {
	if pn.VAPID.PrivateKey == "" || pn.VAPID.PublicKey == "" {
		return
	}
	if pn.Repo == nil {
		return
	}
	if recipientEmail != "" && fromAddr != "" && strings.EqualFold(strings.TrimSpace(recipientEmail), strings.TrimSpace(fromAddr)) {
		return
	}
	subs, err := pn.Repo.ListByMailbox(ctx, mailboxID, &PushSubscriptionFilter{Disabled: boolPtr(false)})
	if err != nil || len(subs) == 0 {
		return
	}
	payload := map[string]interface{}{
		"message_id": messageID,
		"mailbox_id": mailboxID,
		"from":       fromAddr,
		"subject":    subject,
		"title":      "New mail received",
		"body":       truncateSubject(subject),
		"folder":     "INBOX",
		"timestamp":  time.Now().UTC().Format(time.RFC3339),
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return
	}
	for _, sub := range subs {
		if sub.DisabledAt != nil {
			continue
		}
		err := pn.Sender.Send(ctx, &sub, payloadJSON)
		if err != nil {
			if isGoneError(err) {
				now := time.Now().UTC()
				sub.DisabledAt = &now
				_ = pn.Repo.Update(ctx, &sub)
			}
		} else {
			now := time.Now().UTC()
			sub.LastSeenAt = &now
			_ = pn.Repo.UpdateLastSeen(ctx, sub.ID, now)
		}
	}
}

func (pn *PushNotifier) IsEnabled() bool {
	return pn.VAPID.PublicKey != "" && pn.VAPID.PrivateKey != ""
}

func truncateSubject(subject string) string {
	if len(subject) > 100 {
		return subject[:97] + "..."
	}
	return subject
}

func boolPtr(b bool) *bool {
	return &b
}

func signVAPID(header, claims string, privateKey string) (string, error) {
	privBytes, err := base64.RawURLEncoding.DecodeString(privateKey)
	if err != nil {
		return "", fmt.Errorf("decode vapid private key: %w", err)
	}
	key := new(ecdsa.PrivateKey)
	key.Curve = elliptic.P256()
	key.D = new(big.Int).SetBytes(privBytes)
	key.PublicKey.X, key.PublicKey.Y = key.Curve.ScalarBaseMult(privBytes)

	token := header + "." + claims
	h := sha256.Sum256([]byte(token))
	r, s, err := ecdsa.Sign(rand.Reader, key, h[:])
	if err != nil {
		return "", fmt.Errorf("sign vapid: %w", err)
	}
	curveBits := key.Curve.Params().BitSize
	keyBytes := curveBits / 8
	if curveBits%8 > 0 {
		keyBytes++
	}
	sig := make([]byte, 2*keyBytes)
	r.FillBytes(sig[:keyBytes])
	s.FillBytes(sig[keyBytes:])
	return token + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

// isGoneError returns true when the push service has told us the
// subscription is permanently dead. We treat 410 Gone (the standard
// "remove this endpoint" reply), 404 Not Found (the endpoint was
// already cleaned up server-side), and a small set of well-known
// service strings as terminal. Other errors are transient and the
// subscription stays active.
func isGoneError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "410") {
		return true
	}
	if strings.Contains(msg, "404 not found") || strings.Contains(msg, "404 gone") {
		return true
	}
	if strings.Contains(msg, "unsubscribed") || strings.Contains(msg, "subscription") && strings.Contains(msg, "expired") {
		return true
	}
	return false
}
