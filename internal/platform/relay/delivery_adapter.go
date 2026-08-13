package relay

import (
	"context"

	"github.com/orvix/orvix/internal/coremail/delivery"
)

// DeliveryAdapter implements internal/coremail/delivery.RelaySelector,
// wiring the relay control plane into the real outbound SMTP path.
// This is the ONLY place a decrypted relay credential exists outside
// this package's own dial.go — it is read from Service.decryptCredential
// immediately before the dial and is never returned, logged, or
// stored by this adapter.
type DeliveryAdapter struct {
	svc *Service
}

func NewDeliveryAdapter(svc *Service) *DeliveryAdapter {
	return &DeliveryAdapter{svc: svc}
}

var _ delivery.RelaySelector = (*DeliveryAdapter)(nil)

func (a *DeliveryAdapter) SelectRoute(ctx context.Context, tenantID uint, senderDomain, senderMailAccessMode, recipientDomain string, seed int64) (*delivery.RelayRoute, error) {
	route, err := a.svc.SelectRoute(ctx, RouteRequest{
		TenantID: tenantID, SenderAddress: senderDomain, RecipientDomain: recipientDomain,
		SenderMailAccessMode: senderMailAccessMode, Seed: seed,
	})
	if err != nil {
		return nil, err
	}
	if route.Direct {
		return &delivery.RelayRoute{Direct: true}, nil
	}
	provider, err := a.svc.repo.GetProvider(ctx, route.ProviderID)
	if err != nil || provider == nil {
		return &delivery.RelayRoute{Direct: true}, nil // fail safe to direct rather than error the whole delivery
	}

	// H-4: validate the destination BEFORE decrypting the credential. A host
	// with unsafe syntax or an internal IP literal is rejected here, so its
	// credential is never decrypted into memory. (Hostnames that resolve to
	// an internal address are additionally caught by the validating dialer at
	// connect time, before any byte or credential is transmitted.) Refusing
	// the route as an error — rather than silently falling back to direct
	// MX delivery — keeps a misconfigured/hostile relay from quietly
	// exfiltrating mail out a different path; the delivery worker treats the
	// error as a retriable failure.
	if verr := ValidateRelayTarget(provider.Host, provider.Port); verr != nil {
		return nil, ErrUnsafeTarget
	}

	password, derr := a.svc.decryptCredential(*provider)
	if derr != nil {
		// A credential that cannot be decrypted must fail the route, not
		// dial with an empty password (which could authenticate anonymously
		// somewhere it should not). The error is generic — it never carries
		// the ciphertext, key path, or provider secret.
		return nil, ErrCredentialUnavailable
	}
	return &delivery.RelayRoute{
		ProviderID: route.ProviderID, ProviderName: route.ProviderName,
		Host: route.Host, Port: route.Port, ConnSecurity: string(route.ConnSecurity),
		Username: provider.Username, Password: password,
	}, nil
}

func (a *DeliveryAdapter) RecordAttemptResult(ctx context.Context, providerID uint, success bool) {
	_ = a.svc.RecordAttemptResult(ctx, providerID, success)
}

func (a *DeliveryAdapter) Deliver(ctx context.Context, route *delivery.RelayRoute, from string, to []string, data []byte) delivery.RelayDeliverResult {
	p := Provider{
		Host: route.Host, Port: route.Port, Username: route.Username,
		ConnSecurity: ConnSecurity(route.ConnSecurity), TLSValidation: TLSValidationStrict,
	}
	res := Deliver(ctx, p, route.Password, from, to, data)
	return delivery.RelayDeliverResult{Success: res.Success, TempFail: res.TempFail, StatusMsg: res.StatusMsg}
}
