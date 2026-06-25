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
	"time"

	"github.com/orvix/orvix/internal/coremail/storage"
)

type PushSubscription struct {
	ID         uint       `json:"id"`
	MailboxID  uint       `json:"mailbox_id"`
	Endpoint   string     `json:"endpoint"`
	P256DHKey  string     `json:"p256dh_key"`
	AuthKey    string     `json:"auth_key"`
	UserAgent  string     `json:"user_agent"`
	DisabledAt *time.Time `json:"disabled_at,omitempty"`
	LastSeenAt *time.Time `json:"last_seen_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
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

func GenerateVAPIDKeys() (publicKey, privateKey string, err error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", "", fmt.Errorf("generate vapid key: %w", err)
	}
	pub := elliptic.Marshal(key.Curve, key.X, key.Y)
	priv := make([]byte, 32)
	key.D.Bytes()
	if n := len(key.D.Bytes()); n <= 32 {
		copy(priv[32-n:], key.D.Bytes())
	} else {
		copy(priv, key.D.Bytes()[:32])
	}
	return base64.RawURLEncoding.EncodeToString(pub),
		base64.RawURLEncoding.EncodeToString(priv), nil
}

func (pn *PushNotifier) NotifyMailboxMessage(ctx context.Context, mailboxID uint, messageID string, fromAddr string, subject string) {
	if pn.VAPID.PrivateKey == "" || pn.VAPID.PublicKey == "" {
		return
	}
	subs, err := pn.Repo.ListByMailbox(ctx, mailboxID, &PushSubscriptionFilter{Disabled: boolPtr(false)})
	if err != nil || len(subs) == 0 {
		return
	}
	payload := map[string]interface{}{
		"message_id":  messageID,
		"mailbox_id":  mailboxID,
		"from":        fromAddr,
		"subject":     subject,
		"title":       "New mail received",
		"body":        truncateSubject(subject),
		"folder":      "INBOX",
		"timestamp":   time.Now().UTC().Format(time.RFC3339),
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

func isGoneError(err error) bool {
	if err == nil {
		return false
	}
	return containsAny(err.Error(), "410", "404 Not Found", "unsubscribed", "expired")
}

func containsAny(s string, substrs ...string) bool {
	for _, sub := range substrs {
		if len(s) >= len(sub) {
			for i := 0; i <= len(s)-len(sub); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
		}
	}
	return false
}
