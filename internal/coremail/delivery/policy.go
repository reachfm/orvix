package delivery

import (
	"context"
	"fmt"
	"time"
)

// DeliveryPolicy defines rate and volume limits for outbound delivery.
// All zero values mean "unlimited".
type DeliveryPolicy struct {
	// Per-domain outbound limits (resets hourly).
	MaxOutboundPerDomain   int   `json:"max_outbound_per_domain"`
	MaxRecipientsPerDomain int64 `json:"max_recipients_per_domain"`

	// Per-mailbox outbound limits.
	MaxOutboundPerMailbox   int   `json:"max_outbound_per_mailbox"`
	MaxRecipientsPerMessage int   `json:"max_recipients_per_message"`
	MaxMessageSizeBytes     int64 `json:"max_message_size_bytes"`

	// Per-tenant outbound limits.
	MaxOutboundPerTenant   int   `json:"max_outbound_per_tenant"`
	MaxRecipientsPerTenant int64 `json:"max_recipients_per_tenant"`

	// Anti-loop.
	MaxReceivedHeaders int `json:"max_received_headers"`
	MaxDeferralCount   int `json:"max_deferral_count"`

	// Window for rate limit reset.
	Window time.Duration `json:"window"`
}

// DefaultDeliveryPolicy returns safe defaults for enterprise outbound delivery.
func DefaultDeliveryPolicy() DeliveryPolicy {
	return DeliveryPolicy{
		MaxOutboundPerDomain:    1000,
		MaxRecipientsPerDomain:  5000,
		MaxOutboundPerMailbox:   200,
		MaxRecipientsPerMessage: 100,
		MaxMessageSizeBytes:     25 * 1024 * 1024, // 25 MB
		MaxOutboundPerTenant:    10000,
		MaxRecipientsPerTenant:  50000,
		MaxReceivedHeaders:      50,
		MaxDeferralCount:        10,
		Window:                  1 * time.Hour,
	}
}

// PolicyResult holds the outcome of a policy check.
type PolicyResult struct {
	Allowed bool
	Reason  string
	Code    int // SMTP error code if denied
}

// PolicyEnforcer checks delivery policies before sending.
type PolicyEnforcer struct {
	Policy DeliveryPolicy
}

// NewPolicyEnforcer creates a policy enforcer.
func NewPolicyEnforcer(p DeliveryPolicy) *PolicyEnforcer {
	return &PolicyEnforcer{Policy: p}
}

// CheckSendPolicy validates that a sender is allowed to send.
func (pe *PolicyEnforcer) CheckSender(ctx context.Context, fromAddr string, domainID, mailboxID, tenantID *uint, currentOutbound int) *PolicyResult {
	// Check mailbox suspended/disabled.
	if mailboxID != nil {
		// In production this would query the mailbox status.
		// For now, the queue worker checks MailboxID presence.
	}

	if pe.Policy.MaxOutboundPerMailbox > 0 && currentOutbound >= pe.Policy.MaxOutboundPerMailbox {
		return &PolicyResult{
			Allowed: false,
			Reason:  fmt.Sprintf("mailbox outbound limit of %d/hr reached", pe.Policy.MaxOutboundPerMailbox),
			Code:    550,
		}
	}
	_ = domainID
	_ = tenantID
	return &PolicyResult{Allowed: true}
}

// CheckDomainPolicy validates domain-level limits.
func (pe *PolicyEnforcer) CheckDomain(ctx context.Context, domain string, currentOutbound, currentRecipients int) *PolicyResult {
	if pe.Policy.MaxOutboundPerDomain > 0 && currentOutbound >= pe.Policy.MaxOutboundPerDomain {
		return &PolicyResult{
			Allowed: false,
			Reason:  fmt.Sprintf("domain outbound limit of %d/hr reached", pe.Policy.MaxOutboundPerDomain),
			Code:    550,
		}
	}
	if pe.Policy.MaxRecipientsPerDomain > 0 && int64(currentRecipients) >= pe.Policy.MaxRecipientsPerDomain {
		return &PolicyResult{
			Allowed: false,
			Reason:  fmt.Sprintf("domain recipient limit of %d/hr reached", pe.Policy.MaxRecipientsPerDomain),
			Code:    550,
		}
	}
	return &PolicyResult{Allowed: true}
}

// CheckMessageSize validates message size.
func (pe *PolicyEnforcer) CheckMessageSize(sizeBytes int64) *PolicyResult {
	if pe.Policy.MaxMessageSizeBytes > 0 && sizeBytes > pe.Policy.MaxMessageSizeBytes {
		return &PolicyResult{
			Allowed: false,
			Reason:  fmt.Sprintf("message size %d exceeds maximum %d", sizeBytes, pe.Policy.MaxMessageSizeBytes),
			Code:    552,
		}
	}
	return &PolicyResult{Allowed: true}
}

// CheckRecipients validates recipient count per message.
func (pe *PolicyEnforcer) CheckRecipients(count int) *PolicyResult {
	if pe.Policy.MaxRecipientsPerMessage > 0 && count > pe.Policy.MaxRecipientsPerMessage {
		return &PolicyResult{
			Allowed: false,
			Reason:  fmt.Sprintf("recipient count %d exceeds maximum %d per message", count, pe.Policy.MaxRecipientsPerMessage),
			Code:    550,
		}
	}
	return &PolicyResult{Allowed: true}
}
