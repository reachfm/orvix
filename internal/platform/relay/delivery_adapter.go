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

// maxFallbackChain bounds how many providers one delivery attempt may try.
// An unbounded chain would let a large misconfigured pool turn a single
// message into dozens of outbound dials.
const maxFallbackChain = 4

// SelectRoute resolves the routing decision for one outbound message.
//
// FAIL-CLOSED (A): every failure below returns a typed error. NONE of them
// returns a Direct route. The previous implementation answered
// `&RelayRoute{Direct: true}` whenever the provider lookup errored or the
// provider row was missing, so a database blip or a deleted provider silently
// downgraded a mandatory relay route to unauthenticated direct-to-MX delivery
// — bypassing compliance routing, egress-IP policy, and the relay's own SSRF
// and TLS protections. Direct delivery is now returned only when routing
// policy explicitly and successfully selects it.
func (a *DeliveryAdapter) SelectRoute(ctx context.Context, req delivery.RelayRouteRequest) (*delivery.RelayRouteDecision, error) {
	route, err := a.svc.SelectRoute(ctx, RouteRequest{
		TenantID: req.TenantID,
		DomainID: req.DomainID,
		// The FULL envelope sender, not the bare domain. The previous code
		// passed senderDomain into SenderAddress, so sender-pattern matching
		// could never see a local part.
		SenderAddress:        req.SenderAddress,
		SenderDomain:         req.SenderDomain,
		RecipientDomain:      req.RecipientDomain,
		SenderMailAccessMode: req.SenderMailAccessMode,
		Seed:                 req.Seed,
	})
	if err != nil {
		return nil, err
	}
	if route == nil {
		return nil, ErrNoRouteAvailable
	}
	if route.Direct {
		// The ONLY path that yields direct delivery: policy chose it.
		return &delivery.RelayRouteDecision{Route: &delivery.RelayRoute{Direct: true}}, nil
	}

	primary, err := a.resolveProviderRoute(ctx, *route, req.TenantID)
	if err != nil {
		return nil, err
	}

	decision := &delivery.RelayRouteDecision{Route: primary}

	// (C) Faithfully propagate the ordered fallback chain. Duplicates are
	// dropped so one provider is never dialled twice in a single attempt, and
	// a fallback that cannot be resolved safely is SKIPPED rather than
	// failing the whole delivery — the primary route is still valid. A
	// fallback is never allowed to become a direct route.
	seen := map[uint]bool{primary.ProviderID: true}
	for _, fb := range route.Fallbacks {
		if len(decision.Fallbacks) >= maxFallbackChain {
			break
		}
		if fb.Direct || fb.ProviderID == 0 || seen[fb.ProviderID] {
			continue
		}
		resolved, ferr := a.resolveProviderRoute(ctx, fb, req.TenantID)
		if ferr != nil {
			// An unsafe/undecryptable/missing fallback is excluded from the
			// chain; it must never silently widen into a weaker path.
			continue
		}
		seen[fb.ProviderID] = true
		decision.Fallbacks = append(decision.Fallbacks, *resolved)
	}
	return decision, nil
}

// resolveProviderRoute loads, validates and (only then) decrypts the
// credential for one selected route. Every failure is a typed error; none is
// a direct-delivery downgrade.
func (a *DeliveryAdapter) resolveProviderRoute(ctx context.Context, route SelectedRoute, tenantID uint) (*delivery.RelayRoute, error) {
	provider, err := a.svc.repo.GetProvider(ctx, route.ProviderID)
	if err != nil {
		// Infrastructure failure → retryable. NOT direct delivery.
		return nil, ErrProviderUnavailable
	}
	if provider == nil {
		// The route names a provider that does not exist: a configuration
		// integrity problem, not permission to deliver unrelayed.
		return nil, ErrProviderNotFound
	}

	// Cross-scope guard: a provider belonging to another tenant must never be
	// dialled on this tenant's behalf, even if a stale rule or legacy row
	// references it. Tenant 0 denotes a platform-shared provider.
	if provider.TenantID != 0 && tenantID != 0 && provider.TenantID != tenantID {
		return nil, ErrCrossTenantProvider
	}

	// POOL-OWNERSHIP RE-CHECK (F2): a provider may serve only the pool it
	// belongs to — provider.TenantID must equal pool.TenantID. This closes
	// the cross-tenant provider-injection shape at the delivery boundary
	// even for rows created before the API-side fix: a legacy tenant-0
	// provider injected into a tenant-owned pool would otherwise be treated
	// as platform-shared and carry that tenant's mail through an
	// attacker-controlled SMTP endpoint. A mismatch is a configuration
	// integrity failure — permanent, never a direct-delivery downgrade.
	if provider.PoolID != 0 {
		pool, perr := a.svc.repo.GetPool(ctx, provider.PoolID)
		if perr != nil {
			return nil, ErrProviderUnavailable
		}
		if pool == nil {
			return nil, ErrProviderNotFound
		}
		if pool.TenantID != provider.TenantID {
			return nil, ErrCrossTenantProvider
		}
	}

	// Validate the destination BEFORE decrypting the credential, so an unsafe
	// host never causes the secret to exist in memory. (Hostnames resolving
	// to internal addresses are additionally refused by the validating dialer
	// at connect time, before any byte is transmitted.)
	if verr := ValidateRelayTarget(provider.Host, provider.Port); verr != nil {
		return nil, ErrUnsafeTarget
	}

	// (D) Credential-safety policy is evaluated BEFORE decryption: a provider
	// configured to authenticate over plaintext or unverified TLS is refused
	// outright, so its credential is never decrypted, let alone transmitted.
	if perr := ValidateCredentialTransport(*provider); perr != nil {
		return nil, perr
	}

	password := ""
	if provider.HasSecret() {
		pw, derr := a.svc.decryptCredential(*provider)
		if derr != nil {
			// Never dial with an empty password: that could authenticate
			// anonymously somewhere it should not. The error is generic and
			// never carries ciphertext, key path, or any part of the secret.
			return nil, ErrCredentialUnavailable
		}
		password = pw
	}

	return &delivery.RelayRoute{
		ProviderID: route.ProviderID, ProviderName: route.ProviderName,
		Host: route.Host, Port: route.Port, ConnSecurity: string(route.ConnSecurity),
		Username: provider.Username, Password: password,
	}, nil
}

// RecordAttemptResult surfaces bookkeeping failures to the caller instead of
// discarding them (I). The worker deliberately does not retry on this error:
// the SMTP transaction has already completed.
func (a *DeliveryAdapter) RecordAttemptResult(ctx context.Context, providerID uint, success bool) error {
	return a.svc.RecordAttemptResult(ctx, providerID, success)
}

func (a *DeliveryAdapter) Deliver(ctx context.Context, route *delivery.RelayRoute, from string, to []string, data []byte) delivery.RelayDeliverResult {
	p := Provider{
		Host: route.Host, Port: route.Port, Username: route.Username,
		ConnSecurity: ConnSecurity(route.ConnSecurity),
		// Strict is the only validation mode reachable here: SelectRoute
		// refuses to resolve a credentialed provider that is not configured
		// for strict TLS (ValidateCredentialTransport), and a provider with no
		// credential has nothing to protect. Hardcoding strict therefore
		// tightens rather than fakes the posture — an opportunistic provider
		// never reaches this function with a password.
		TLSValidation: TLSValidationStrict,
	}
	res := Deliver(ctx, p, route.Password, from, to, data)
	return delivery.RelayDeliverResult{Success: res.Success, TempFail: res.TempFail, Ambiguous: res.Ambiguous, StatusMsg: res.StatusMsg}
}
