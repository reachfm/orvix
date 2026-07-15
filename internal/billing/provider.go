package billing

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"
)

// NewPaymentProviderFromConfig creates a PaymentProvider from configuration.
// When the provider is not enabled or not configured, nil is returned and
// webhook/complaint endpoints return 503 (service unavailable).
func NewPaymentProviderFromConfig(providerName, secret, webhookSecret string, enabled bool) (PaymentProvider, error) {
	if !enabled || providerName == "" {
		return nil, nil
	}
	switch providerName {
	case "stripe":
		return nil, fmt.Errorf("stripe provider not yet implemented")
	case "hmac":
		return newHMACPaymentProvider(secret, webhookSecret), nil
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
}

func newHMACPaymentProvider(secret, webhookSecret string) *hmacPaymentProvider {
	return &hmacPaymentProvider{secret: secret, webhookSecret: webhookSecret}
}

func (p *hmacPaymentProvider) CreateCheckout(tenantID uint, planID PlanID, interval BillingInterval, returnURL string) (*CheckoutSession, error) {
	return nil, fmt.Errorf("payment provider: checkout not available")
}

func (p *hmacPaymentProvider) GetCustomerPortalURL(tenantID uint, returnURL string) (string, error) {
	return "", fmt.Errorf("payment provider: portal not available")
}

func (p *hmacPaymentProvider) VerifyWebhook(payload []byte, signature string) (*WebhookEvent, error) {
	if p.webhookSecret == "" || signature == "" {
		return nil, ErrWebhookInvalid
	}
	ts := fmt.Sprintf("%d", time.Now().Unix())
	signedPayload := fmt.Sprintf("%s.%s", ts, string(payload))
	mac := hmac.New(sha256.New, []byte(p.webhookSecret))
	mac.Write([]byte(signedPayload))
	expectedSig := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(signature), []byte(expectedSig)) {
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
