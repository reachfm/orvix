package billing

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const DefaultWebhookTolerance = 5 * time.Minute

// NewPaymentProviderFromConfig creates a PaymentProvider from configuration.
// When the provider is not enabled or not configured, nil is returned and
// webhook/complaint endpoints return 503 (service unavailable).
func NewPaymentProviderFromConfig(providerName, secret, webhookSecret string, enabled bool, tolerance time.Duration) (PaymentProvider, error) {
	if !enabled || providerName == "" {
		return nil, nil
	}
	switch providerName {
	case "stripe":
		return nil, fmt.Errorf("stripe provider not yet implemented")
	case "hmac":
		return newHMACPaymentProvider(secret, webhookSecret, tolerance), nil
	default:
		return nil, fmt.Errorf("unsupported payment provider: %s", providerName)
	}
}

// hmacPaymentProvider is a production-ready payment provider that
// authenticates webhooks with HMAC-SHA256 signatures. It is used when
// a payment provider is configured but no gateway-specific adapter
// (Stripe, Paddle, etc.) is linked. The provider does not implement
// checkout or portal flows — those remain unavailable until a real
// gateway adapter is wired.
type hmacPaymentProvider struct {
	secret        string
	webhookSecret string
	tolerance     time.Duration
	now           func() time.Time
}

func newHMACPaymentProvider(secret, webhookSecret string, tolerance time.Duration) *hmacPaymentProvider {
	if tolerance <= 0 {
		tolerance = DefaultWebhookTolerance
	}
	return &hmacPaymentProvider{
		secret:        secret,
		webhookSecret: webhookSecret,
		tolerance:     tolerance,
		now:           time.Now,
	}
}

func (p *hmacPaymentProvider) CreateCheckout(tenantID uint, planID PlanID, interval BillingInterval, returnURL string) (*CheckoutSession, error) {
	return nil, fmt.Errorf("payment provider: checkout not available")
}

func (p *hmacPaymentProvider) GetCustomerPortalURL(tenantID uint, returnURL string) (string, error) {
	return "", fmt.Errorf("payment provider: portal not available")
}

func (p *hmacPaymentProvider) VerifyWebhook(payload []byte, timestamp, signature string) (*WebhookEvent, error) {
	timestamp = strings.TrimSpace(timestamp)
	signature = strings.TrimSpace(signature)
	if p.webhookSecret == "" || timestamp == "" || signature == "" {
		return nil, ErrWebhookInvalid
	}
	ts, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil || ts <= 0 || strconv.FormatInt(ts, 10) != timestamp {
		return nil, ErrWebhookTimestampMalformed
	}
	now := p.now
	if now == nil {
		now = time.Now
	}
	age := now().Unix() - ts
	if age < 0 {
		age = -age
	}
	if time.Duration(age)*time.Second > p.tolerance {
		return nil, ErrWebhookTimestampExpired
	}
	signedPayload := fmt.Sprintf("%s.%s", timestamp, string(payload))
	mac := hmac.New(sha256.New, []byte(p.webhookSecret))
	mac.Write([]byte(signedPayload))
	gotSig, err := hex.DecodeString(signature)
	if err != nil || len(gotSig) != sha256.Size {
		return nil, ErrWebhookInvalid
	}
	expectedSig := mac.Sum(nil)
	if !hmac.Equal(gotSig, expectedSig) {
		return nil, ErrWebhookInvalid
	}
	var raw struct {
		Type string `json:"type"`
		Data struct {
			Object struct {
				ID            string `json:"id"`
				Subscription  string `json:"subscription"`
				Status        string `json:"status"`
				PaymentStatus string `json:"payment_status"`
			} `json:"object"`
		} `json:"data"`
	}
	if err := json.Unmarshal(payload, &raw); err != nil {
		return nil, err
	}
	return &WebhookEvent{
		Type:               raw.Type,
		ProviderSubID:      raw.Data.Object.Subscription,
		SubscriptionStatus: raw.Data.Object.Status,
		PaymentStatus:      raw.Data.Object.PaymentStatus,
		ProviderEventID:    raw.Data.Object.ID,
	}, nil
}

func (p *hmacPaymentProvider) CancelSubscription(providerSubID string) error {
	return fmt.Errorf("payment provider: cancel not available")
}

func (p *hmacPaymentProvider) SynchronizeSubscription(providerSubID string) (*SyncResult, error) {
	return nil, fmt.Errorf("payment provider: sync not available")
}
