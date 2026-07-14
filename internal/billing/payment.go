package billing

import "errors"

var (
	ErrPaymentProvider = errors.New("payment provider error")
	ErrWebhookInvalid  = errors.New("invalid webhook signature")
)

type PaymentProvider interface {
	CreateCheckout(tenantID uint, planID PlanID, interval BillingInterval, returnURL string) (*CheckoutSession, error)
	GetCustomerPortalURL(tenantID uint, returnURL string) (string, error)
	VerifyWebhook(payload []byte, signature string) (*WebhookEvent, error)
	CancelSubscription(providerSubID string) error
	SynchronizeSubscription(providerSubID string) (*SyncResult, error)
}

type CheckoutSession struct {
	URL       string `json:"url"`
	SessionID string `json:"session_id"`
}

type WebhookEvent struct {
	Type            string `json:"type"`
	ProviderSubID   string `json:"provider_sub_id"`
	CustomerID      string `json:"customer_id"`
	SubscriptionStatus string `json:"subscription_status"`
	InvoiceID       string `json:"invoice_id"`
	AmountPaid      int64  `json:"amount_paid"`
	Currency        string `json:"currency"`
	PaymentStatus   string `json:"payment_status"`
}

type SyncResult struct {
	ProviderSubID string             `json:"provider_sub_id"`
	Status        SubscriptionStatus `json:"status"`
	CurrentPeriodEnd string          `json:"current_period_end"`
	CancelAtPeriodEnd bool           `json:"cancel_at_period_end"`
}

type NoopPaymentProvider struct{}

func NewNoopPaymentProvider() *NoopPaymentProvider {
	return &NoopPaymentProvider{}
}

func (p *NoopPaymentProvider) CreateCheckout(tenantID uint, planID PlanID, interval BillingInterval, returnURL string) (*CheckoutSession, error) {
	return &CheckoutSession{
		URL:       returnURL + "?checkout=simulated&plan=" + string(planID),
		SessionID: "cs_simulated_" + string(planID),
	}, nil
}

func (p *NoopPaymentProvider) GetCustomerPortalURL(tenantID uint, returnURL string) (string, error) {
	return returnURL + "?portal=simulated", nil
}

func (p *NoopPaymentProvider) VerifyWebhook(payload []byte, signature string) (*WebhookEvent, error) {
	return &WebhookEvent{
		Type:            "checkout.session.completed",
		ProviderSubID:   "sub_simulated",
		SubscriptionStatus: "active",
		PaymentStatus:   "paid",
	}, nil
}

func (p *NoopPaymentProvider) CancelSubscription(providerSubID string) error {
	return nil
}

func (p *NoopPaymentProvider) SynchronizeSubscription(providerSubID string) (*SyncResult, error) {
	return &SyncResult{
		ProviderSubID: providerSubID,
		Status:        SubActive,
		CancelAtPeriodEnd: false,
	}, nil
}
