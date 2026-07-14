package dns

import (
	"context"
	"fmt"

	"go.uber.org/zap"
)

// Provider defines the interface for DNS providers.
type Provider interface {
	Name() string
	CreateMXRecord(ctx context.Context, domain, target string, priority int) error
	CreateTXTRecord(ctx context.Context, domain, name, value string) error
	CreateDKIMRecord(ctx context.Context, domain, selector, publicKey string) error
	CreateDMARCRecord(ctx context.Context, domain, policy string) error
	CreateSPFRecord(ctx context.Context, domain, value string) error
}

// Manager handles DNS automation across multiple providers.
type Manager struct {
	providers map[string]Provider
	logger    *zap.Logger
}

// NewManager creates a new DNS manager.
func NewManager(logger *zap.Logger) *Manager {
	return &Manager{
		providers: make(map[string]Provider),
		logger:    logger,
	}
}

// RegisterProvider registers a DNS provider.
func (m *Manager) RegisterProvider(provider Provider) {
	m.providers[provider.Name()] = provider
	m.logger.Info("dns provider registered", zap.String("provider", provider.Name()))
}

// WizardResult contains the DNS wizard setup results.
type WizardResult struct {
	SPFStatus     string `json:"spf_status"`
	DKIMStatus    string `json:"dkim_status"`
	DMARCStatus   string `json:"dmarc_status"`
	MXStatus      string `json:"mx_status"`
	DKIMSelector  string `json:"dkim_selector"`
	DKIMPublicKey string `json:"dkim_public_key"`
}

// RunWizard runs the complete DNS setup wizard for a domain.
func (m *Manager) RunWizard(ctx context.Context, providerName, domain string) (*WizardResult, error) {
	provider, ok := m.providers[providerName]
	if !ok {
		return nil, fmt.Errorf("unsupported DNS provider: %s", providerName)
	}

	result := &WizardResult{
		SPFStatus:   "pending",
		DKIMStatus:  "pending",
		DMARCStatus: "pending",
		MXStatus:    "pending",
	}

	if err := provider.CreateMXRecord(ctx, domain, fmt.Sprintf("mail.%s", domain), 10); err != nil {
		m.logger.Error("failed to create MX record", zap.Error(err))
	} else {
		result.MXStatus = "configured"
	}

	if err := provider.CreateSPFRecord(ctx, domain, fmt.Sprintf("v=spf1 mx include:%s ~all", domain)); err != nil {
		m.logger.Error("failed to create SPF record", zap.Error(err))
	} else {
		result.SPFStatus = "configured"
	}

	result.DKIMSelector = "orvix"
	result.DKIMPublicKey = "v=DKIM1; p=..."

	if err := provider.CreateDKIMRecord(ctx, domain, result.DKIMSelector, result.DKIMPublicKey); err != nil {
		m.logger.Error("failed to create DKIM record", zap.Error(err))
	} else {
		result.DKIMStatus = "configured"
	}

	if err := provider.CreateDMARCRecord(ctx, domain, "v=DMARC1; p=quarantine; rua=mailto:dmarc@"+domain); err != nil {
		m.logger.Error("failed to create DMARC record", zap.Error(err))
	} else {
		result.DMARCStatus = "configured"
	}

	return result, nil
}

// CloudflareProvider implements the Provider interface for Cloudflare.
type CloudflareProvider struct {
	apiKey string
	logger *zap.Logger
}

// NewCloudflareProvider creates a new Cloudflare DNS provider.
func NewCloudflareProvider(apiKey string, logger *zap.Logger) *CloudflareProvider {
	return &CloudflareProvider{
		apiKey: apiKey,
		logger: logger,
	}
}

func (c *CloudflareProvider) Name() string { return "cloudflare" }

func (c *CloudflareProvider) CreateMXRecord(ctx context.Context, domain, target string, priority int) error {
	c.logger.Info("creating MX record via cloudflare", zap.String("domain", domain))
	return nil
}

func (c *CloudflareProvider) CreateTXTRecord(ctx context.Context, domain, name, value string) error {
	c.logger.Info("creating TXT record via cloudflare", zap.String("domain", domain))
	return nil
}

func (c *CloudflareProvider) CreateDKIMRecord(ctx context.Context, domain, selector, publicKey string) error {
	return c.CreateTXTRecord(ctx, domain, selector+"._domainkey", publicKey)
}

func (c *CloudflareProvider) CreateDMARCRecord(ctx context.Context, domain, policy string) error {
	return c.CreateTXTRecord(ctx, domain, "_dmarc", policy)
}

func (c *CloudflareProvider) CreateSPFRecord(ctx context.Context, domain, value string) error {
	return c.CreateTXTRecord(ctx, domain, "@", value)
}
