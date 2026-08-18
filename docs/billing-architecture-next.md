# Billing architecture — next steps

Status: design notes only, nothing in this document is implemented as a
result of it. It records what the repo already supports as of this branch
(feature/domain-mailbox-provisioning-frontend, Phase I of the
domain-mailbox-provisioning mission) and what decisions remain open before
a real payment gateway can be wired in.

## What the repo already supports

The schema and abstractions below are real and already in production use
(self-signup free-tier subscriptions, the Platform Billing ledger/adjustment
UI). This is not a green-field design — it's documenting the seams a real
provider integration would plug into.

### Schema (SQLite; dialect-portable via `internal/dbdialect`, Postgres DDL exists in parallel — see `internal/models/postgres_migrations.go`)

- **`plans`** (`internal/billing/setup.go`) — id, name, price_monthly,
  price_yearly, max_domains, max_mailboxes, storage_mb, send_limit_day,
  features (JSON string). Currently a flat table, not versioned in the DB —
  see "Plan Versioning" below for the code-level version overlay that
  exists instead.
- **`subscriptions`** — one row per tenant (`tenant_id UNIQUE`), status,
  billing_interval, trial/period/grace/suspension timestamps, and
  `provider` / `provider_sub_id` columns already present for linking to an
  external gateway's subscription object.
- **`invoices`** (`internal/models/models.go`) — tenant_id,
  subscription_id, provider, provider_invoice_id (unique per provider),
  currency, subtotal/tax/total/amount_paid/amount_due, status, period
  start/end, hosted_invoice_url, pdf_url, provider_event_id/
  provider_event_created_at/provider_updated_at (for reconciling against
  out-of-order webhook delivery).
- **`usage_records`** — per-tenant, per-period (`UNIQUE(tenant_id,
  period_start)`) counters: mailboxes/domains/storage used, emails sent/
  received, api_calls. Feeds metered/usage-based billing if that's ever
  needed; currently used for quota enforcement (`internal/billing/quota.go`,
  `usage_counter.go`), not billing itself.
- **`webhook_events`** — already a real event ledger: `UNIQUE(provider,
  id)` and `UNIQUE(provider, idempotency_key)` give replay protection at
  the database level, plus `received_at`/`processed_at`/
  `processing_error` for observability. This is the piece a "provider
  abstraction, idempotent signed webhooks, event ledger, replay
  protection" design would otherwise have to invent — it exists.
- **No `payment_methods` table yet.** There is no stored-card /
  stored-payment-instrument schema in this repo. `PaymentProvider`
  (below) exposes `GetCustomerPortalURL`, implying the intended pattern is
  "redirect to the provider's hosted portal for payment-method management"
  rather than storing card data in Orvix's own database — this repo has
  never touched PCI-scoped data and the open design decision is whether to
  keep it that way (recommended) or introduce a `payment_methods` table
  that only stores a provider-issued token/reference (never a PAN).

### Provider abstraction (`internal/billing/payment.go`, `provider.go`)

```go
type PaymentProvider interface {
    CreateCheckout(tenantID uint, planID PlanID, interval BillingInterval, returnURL string) (*CheckoutSession, error)
    GetCustomerPortalURL(tenantID uint, returnURL string) (string, error)
    VerifyWebhook(payload []byte, timestamp, signature string) (*WebhookEvent, error)
    CancelSubscription(providerSubID string) error
    SynchronizeSubscription(providerSubID string) (*SyncResult, error)
}
```

Two implementations exist today:

- **`hmac` provider** (`newHMACPaymentProvider`) — a real, working
  implementation that authenticates inbound webhooks with HMAC-SHA256 and
  a timestamp tolerance window (`DefaultWebhookTolerance = 5 * time.Minute`,
  see `ErrWebhookTimestampExpired`/`ErrWebhookTimestampMalformed`). Used
  today for whatever "generic" or internal-test provider is configured; it
  does not implement `CreateCheckout`/`GetCustomerPortalURL` as real hosted
  flows (no gateway to redirect to).
- **`stripe` provider** — a named case exists in
  `NewPaymentProviderFromConfig` but returns
  `fmt.Errorf("stripe provider not yet implemented")`. **No Stripe (or any
  other named gateway) integration exists in this repo today.** Any
  design assuming Stripe specifically is not grounded in current code —
  see "Open decision: provider selection" below.

`internal/api/router.go` wires the payment webhook route to return `503`
when no provider is configured (`payment provider not configured — webhook
returns 503`, logged at startup) — the system already fails closed rather
than accepting unauthenticated webhook traffic when unconfigured.

### Plan versioning (`internal/billing/plan_version.go`)

`PlanLimits` + `PlanVersion` + `PlanVersionStore` exist as a code-level
overlay: this lets `max_domains`/`max_mailboxes`/etc. be versioned and
looked up by (plan, effective date) without needing a live migration of
the `plans` table for every limit change. This is a partial answer to
"Plan schema" — it does not yet cover price changes or proration, only
limits.

## What's still a gap

- No stored `payment_methods` schema (see above — likely intentional, but
  undecided).
- No real gateway adapter (Stripe or otherwise) — `CreateCheckout` and
  `GetCustomerPortalURL` have no working non-HMAC implementation, so
  self-service upgrade/downgrade and payment-method changes cannot
  currently redirect anywhere real.
- `plans` pricing itself isn't versioned in the DB (only `PlanLimits` is,
  via `PlanVersionStore`) — a price change today would retroactively
  affect every subscription reading that plan row unless the version
  overlay is extended to cover price too.
- Invoice generation only exists as a target schema
  (`invoice_service.go` exists — see that file for what's actually
  implemented today before assuming a gap; this doc does not re-verify its
  internals) fed by provider webhooks; there's no invoice generation path
  independent of a configured provider (i.e., no self-hosted "generate an
  invoice for a manual/offline payment" flow beyond the existing ledger
  adjustments feature used by Platform Billing today).

## Open decision: provider selection

The repo deliberately does not commit to Stripe, Paddle, or any other
named gateway — the `stripe` case in `provider.go` is a placeholder that
errors loudly rather than silently returning a broken provider. Whoever
picks up gateway integration next needs to decide:

1. **Which gateway** (Stripe is the natural default given the existing
   `WebhookEvent` field shapes closely mirror Stripe's webhook payload
   vocabulary — `amount_paid`, `amount_due`, `hosted_invoice_url`, etc. —
   but this is circumstantial, not a commitment already made in code).
2. **Whether to support multiple gateways** via the existing
   `PaymentProvider` interface (already gateway-agnostic) or hard-couple
   to one.
3. **Whether `payment_methods` gets a table at all**, or stays entirely
   provider-hosted (recommended default: stay provider-hosted, store only
   a provider customer/payment-method reference ID if anything).

None of this needs to be decided to keep using the existing `hmac`
provider path or the manual ledger-adjustment flow (Platform Billing's
current UI) — both work today without a named gateway.

## Recommended shape for the real integration (not implemented)

1. Add a real adapter behind the existing `PaymentProvider` interface —
   no interface changes needed for a first gateway; the interface already
   covers checkout, portal, webhook verification, cancellation, and sync.
2. Route inbound webhooks through the existing `webhook_events` ledger
   (already idempotent via the two `UNIQUE` constraints) before any
   subscription/invoice mutation — this is already the pattern
   `webhook.go`/`webhook_integration_test.go` exercise for the `hmac`
   provider; a new gateway adapter should feed the same table, not a
   parallel one.
3. Extend `PlanVersionStore` (or a sibling store) to version price, not
   just limits, if price changes need to not retroactively affect active
   subscriptions.
4. Decide the `payment_methods` question above before writing any schema
   for it — introducing it prematurely risks taking on PCI-adjacent scope
   this repo has avoided so far.
