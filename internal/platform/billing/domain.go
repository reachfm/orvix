// Package billing implements the remaining Feature 18 gap on top of
// the already-extensive internal/billing package (plans, entitlements,
// quotas, subscriptions with the full trial/active/grace/past_due/
// suspended/cancelled lifecycle, usage counters, plan versions,
// operator overrides, a provider-neutral PaymentProvider port with a
// working HMAC implementation, and an idempotent invoice service
// already driven by webhook events with provider-event-ID dedup).
// None of that is duplicated here. This package adds what a survey
// found genuinely missing: audited manual credits/adjustments with
// optimistic concurrency and a running balance, and a reconciliation
// report tying usage/invoices/adjustments together.
package billing

import "time"

// AdjustmentType distinguishes an operator-granted credit (reduces
// what the tenant owes) from a debit adjustment (e.g. correcting an
// undercharge) — both go through the same audited, reasoned path.
type AdjustmentType string

const (
	AdjustmentCredit AdjustmentType = "credit"
	AdjustmentDebit  AdjustmentType = "debit"
)

// Adjustment is one manual credit/debit — immutable once created
// (correcting a mistake means creating a new, opposite adjustment,
// never editing history).
type Adjustment struct {
	ID          uint           `json:"id"`
	TenantID    uint           `json:"tenant_id"`
	Type        AdjustmentType `json:"type"`
	AmountCents int64          `json:"amount_cents"` // always positive; Type determines direction
	Currency    string         `json:"currency"`
	Reason      string         `json:"reason"`
	ActorID     uint           `json:"actor_id"`
	// IdempotencyKey prevents a retried request (e.g. a double-click
	// or a webhook redelivery of an operator action) from applying the
	// same adjustment twice.
	IdempotencyKey string    `json:"idempotency_key,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
}

// Balance is a tenant's running credit balance in integer minor
// units — never a float. Version is the optimistic-concurrency guard
// exposed to callers that need to reason about "did my adjustment
// apply to the balance I saw", even though ApplyAdjustment itself
// uses a single atomic UPDATE (not read-then-write) and so never
// actually loses an update regardless of Version.
type Balance struct {
	TenantID     uint      `json:"tenant_id"`
	Currency     string    `json:"currency"`
	BalanceCents int64     `json:"balance_cents"`
	Version      int       `json:"version"`
	UpdatedAt    time.Time `json:"updated_at"`
}
