// Exact request/response contracts for the platform-owned billing
// endpoints (internal/api/handlers/platform_billing_admin.go and
// internal/platform/billing/domain.go), matching the Go structs
// field-for-field.

export type AdjustmentType = "credit" | "debit";

export interface Adjustment {
  id: number;
  tenant_id: number;
  type: AdjustmentType;
  amount_cents: number;
  currency: string;
  reason: string;
  actor_id: number;
  idempotency_key?: string;
  created_at: string;
}

export interface Balance {
  tenant_id: number;
  currency: string;
  balance_cents: number;
  version: number;
  updated_at: string;
}

export interface ReconciliationReport {
  tenant_id: number;
  currency: string;
  stored_balance_cents: number;
  recomputed_balance_cents: number;
  total_credits_cents: number;
  total_debits_cents: number;
  discrepancy_cents: number;
  discrepant: boolean;
  generated_at: string;
}

export interface ListAdjustmentsResponse {
  adjustments: Adjustment[];
}

export interface CreateAdjustmentRequest {
  type: AdjustmentType;
  amount_cents: number;
  currency: string;
  reason: string;
}

export const ADJUSTMENT_TYPES: ReadonlyArray<AdjustmentType> = ["credit", "debit"];

// ── GET /platform/billing/tenants/:tenant_id/overview ──────────────
// (GetPlatformBillingOverview, platform_billing_admin.go). EVERY field
// is read from the real billing/usage/invoice/ledger stores: missing
// subscriptions/usage are null, invoices is never null ([] when none),
// and payment_provider reports the HONEST configuration state
// (configured:false + note "payment provider not configured" when no
// provider is wired). The UI must never fabricate cards, MRR, or paid
// invoices from this shape.

export interface BillingSubscription {
  id: number;
  tenant_id: number;
  plan_id: string;
  status: string;
  billing_interval: string;
  trial_ends_at?: string | null;
  current_period_start: string;
  current_period_end: string;
  cancelled_at?: string | null;
  past_due_since?: string | null;
  grace_period_ends_at?: string | null;
  suspended_at?: string | null;
  storage_mb: number;
  send_limit_day: number;
  provider?: string;
  provider_sub_id?: string;
  created_at: string;
  updated_at: string;
}

export interface BillingPlan {
  id: string;
  name: string;
  description?: string;
  price_monthly: number;
  price_yearly: number;
  max_domains: number;
  max_mailboxes: number;
  storage_mb: number;
  send_limit_day: number;
}

export interface BillingUsage {
  id: number;
  tenant_id: number;
  period_start: string;
  period_end: string;
  emails_sent: number;
  emails_received: number;
  mailboxes_used: number;
  domains_used: number;
}

export interface BillingInvoice {
  id: number;
  tenant_id: number;
  provider: string;
  provider_invoice_id: string;
  invoice_number: string;
  currency: string;
  subtotal: number;
  tax: number;
  total: number;
  amount_paid: number;
  amount_due: number;
  status: string;
  period_start?: string | null;
  period_end?: string | null;
  issued_at?: string | null;
  due_at?: string | null;
  paid_at?: string | null;
}

export interface PaymentProviderState {
  provider: string;
  enabled: boolean;
  configured: boolean;
  note: string;
}

export interface PlatformBillingOverview {
  tenant_id: number;
  subscription: BillingSubscription | null;
  plan: BillingPlan | null;
  usage: BillingUsage | null;
  invoices: BillingInvoice[];
  balance: (Balance & { updated_at: string }) | null;
  adjustments: Adjustment[];
  reconciliation: ReconciliationReport | null;
  payment_provider: PaymentProviderState;
}
