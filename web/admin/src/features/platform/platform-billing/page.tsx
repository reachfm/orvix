import { useState } from "react";
import { AlertTriangle, CheckCircle2, XCircle } from "lucide-react";
import { useBalance, useAdjustments, useReconciliation, useCreateAdjustment, useBillingOverview } from "./queries";
import { formatMinorUnits, formatSignedMinorUnits } from "./formatters";
import { ADJUSTMENT_TYPES, type AdjustmentType } from "./contract";
import { useTenantScope } from "../tenant-context/queries";
import TenantScopeBanner from "../tenant-context/components/TenantScopeBanner";

/**
 * B-3: Platform Billing requires an EXPLICIT tenant selection.
 *
 * This page previously received a hardcoded `tenantId={1}` from the router, so
 * a Platform Super Admin was shown — and could mutate — the first customer's
 * billing data regardless of which customer they intended. The tenant now
 * comes from the canonical platform tenant scope (the same selector the
 * mail-control pages use, sourced from the real organization inventory), and
 * nothing is requested until a tenant is chosen.
 *
 * The selection is a request TARGET, never authentication: the backend still
 * enforces platform RBAC and explicit tenant scoping on every route, so
 * editing the selection in DevTools cannot bypass authorization.
 */
function money(cents: number, currency?: string): string {
  return formatMinorUnits(cents, currency);
}

function Row({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex justify-between gap-4">
      <dt className="text-[var(--text-secondary)]">{label}</dt>
      <dd className="text-[var(--text-primary)] text-right">{value}</dd>
    </div>
  );
}

export default function PlatformBillingPage() {
  const { data: scope } = useTenantScope();
  const tenantId = scope?.tenantId ?? null;
  const tenantName = scope?.tenantName;

  const balance = useBalance(tenantId);
  const adjustments = useAdjustments(tenantId);
  const reconciliation = useReconciliation(tenantId);
  const overview = useBillingOverview(tenantId);
  const createAdj = useCreateAdjustment(tenantId);

  const [type, setType] = useState<AdjustmentType>("credit");
  const [amountCents, setAmountCents] = useState("");
  const [currency, setCurrency] = useState("USD");
  const [reason, setReason] = useState("");
  const [result, setResult] = useState<{ kind: "ok" | "error"; message: string } | null>(null);

  const submit = () => {
    const cents = Number.parseInt(amountCents, 10);
    if (!Number.isFinite(cents) || cents <= 0) {
      setResult({ kind: "error", message: "Amount must be a positive integer of minor units." });
      return;
    }
    if (reason.trim() === "") {
      setResult({ kind: "error", message: "A reason is required for every adjustment." });
      return;
    }
    setResult(null);
    createAdj.mutate(
      { data: { type, amount_cents: cents, currency: currency.trim() || "USD", reason: reason.trim() }, idempotencyKey: crypto.randomUUID() },
      {
        onSuccess: () => {
          setAmountCents("");
          setReason("");
          setResult({ kind: "ok", message: "Adjustment applied and audited." });
        },
        onError: (e) => setResult({ kind: "error", message: e instanceof Error ? e.message : "Adjustment failed." }),
      },
    );
  };

  const bal = balance.data;
  const rec = reconciliation.data;

  // No tenant selected: render the selector only. Nothing is fetched and no
  // tenant-specific figure is on screen, so an operator can never read one
  // customer's ledger while believing they are looking at another's.
  if (tenantId === null) {
    return (
      <div className="space-y-6">
        <div>
          <h2 className="text-xl font-semibold text-[var(--text-primary)]">Platform Billing</h2>
          <p className="text-sm text-[var(--text-secondary)]">
            Select a tenant to view its ledger balance, adjustments, and reconciliation.
          </p>
        </div>
        <TenantScopeBanner />
        <p className="text-sm text-[var(--text-muted)]" role="status">
          No tenant selected.
        </p>
      </div>
    );
  }

  return (
    <div className="space-y-6">
      <div>
        <h2 className="text-xl font-semibold text-[var(--text-primary)]">Platform Billing</h2>
        <p className="text-sm text-[var(--text-secondary)]">
          {tenantName ? `${tenantName} (tenant ${tenantId})` : `Tenant ${tenantId}`} — ledger balance, adjustments, and reconciliation.
        </p>
      </div>

      <TenantScopeBanner />

      {overview.isLoading ? (
        <p className="text-sm text-[var(--text-muted)]" role="status">Loading billing overview…</p>
      ) : overview.error ? (
        <div className="border border-[var(--danger)]/30 rounded-lg p-4 text-sm flex items-center gap-2" role="alert">
          <AlertTriangle size={16} className="text-[var(--danger)] shrink-0" />
          <span className="text-[var(--danger)]">Billing overview unavailable: {(overview.error as Error).message}</span>
        </div>
      ) : overview.data ? (
        <div className="grid grid-cols-1 lg:grid-cols-2 gap-4">
          <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-lg p-4">
            <h3 className="text-sm font-semibold text-[var(--text-primary)] mb-3">Subscription</h3>
            {overview.data.subscription && overview.data.plan ? (
              <dl className="space-y-1.5 text-sm">
                <Row label="Plan" value={`${overview.data.plan.name} (${overview.data.subscription.plan_id})`} />
                <Row label="Status" value={overview.data.subscription.status.replace(/_/g, " ")} />
                <Row label="Interval" value={overview.data.subscription.billing_interval} />
                <Row label="Period" value={`${new Date(overview.data.subscription.current_period_start).toLocaleDateString()} – ${new Date(overview.data.subscription.current_period_end).toLocaleDateString()}`} />
                {overview.data.subscription.trial_ends_at && (
                  <Row label="Trial ends" value={new Date(overview.data.subscription.trial_ends_at).toLocaleDateString()} />
                )}
              </dl>
            ) : (
              <p className="text-sm text-[var(--text-muted)]">No subscription recorded for this tenant.</p>
            )}
          </div>

          <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-lg p-4">
            <h3 className="text-sm font-semibold text-[var(--text-primary)] mb-3">Usage (current period)</h3>
            {overview.data.usage ? (
              <dl className="space-y-1.5 text-sm">
                <Row label="Emails sent" value={String(overview.data.usage.emails_sent)} />
                <Row label="Emails received" value={String(overview.data.usage.emails_received)} />
                <Row label="Mailboxes" value={String(overview.data.usage.mailboxes_used)} />
                <Row label="Domains" value={String(overview.data.usage.domains_used)} />
              </dl>
            ) : (
              <p className="text-sm text-[var(--text-muted)]">No usage recorded for this period.</p>
            )}
          </div>

          <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-lg p-4">
            <h3 className="text-sm font-semibold text-[var(--text-primary)] mb-3">Invoices</h3>
            {overview.data.invoices.length === 0 ? (
              <p className="text-sm text-[var(--text-muted)]">No invoices recorded. Rows appear only from real provider events.</p>
            ) : (
              <table className="w-full text-sm">
                <thead>
                  <tr className="border-b border-[var(--border)] text-left text-[var(--text-secondary)]">
                    <th className="py-1.5 pr-2">Number</th>
                    <th className="py-1.5 pr-2">Status</th>
                    <th className="py-1.5 pr-2">Total</th>
                    <th className="py-1.5">Issued</th>
                  </tr>
                </thead>
                <tbody>
                  {overview.data.invoices.map((inv) => (
                    <tr key={inv.id} className="border-b border-[var(--bg-subtle)]">
                      <td className="py-1.5 pr-2 text-[var(--text-primary)]">{inv.invoice_number}</td>
                      <td className="py-1.5 pr-2 text-[var(--text-secondary)]">{inv.status}</td>
                      <td className="py-1.5 pr-2 text-[var(--text-primary)]">{formatMinorUnits(inv.total, inv.currency)}</td>
                      <td className="py-1.5 text-[var(--text-secondary)]">
                        {inv.issued_at ? new Date(inv.issued_at).toLocaleDateString() : "—"}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </div>

          <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-lg p-4">
            <h3 className="text-sm font-semibold text-[var(--text-primary)] mb-3">Payment provider</h3>
            {overview.data.payment_provider.configured ? (
              <div className="flex items-center gap-2 text-sm">
                <CheckCircle2 size={16} className="text-[var(--success)] shrink-0" />
                <span className="text-[var(--text-primary)]">
                  {overview.data.payment_provider.provider}
                  {overview.data.payment_provider.enabled ? "" : " (configured but disabled)"}
                </span>
              </div>
            ) : (
              <div className="flex items-start gap-2 text-sm">
                <XCircle size={16} className="text-[var(--warning)] shrink-0 mt-0.5" />
                <span className="text-[var(--text-secondary)]">
                  {overview.data.payment_provider.note || "Payment provider not configured"}
                </span>
              </div>
            )}
            <p className="text-xs text-[var(--text-muted)] mt-2">
              Only real provider state is shown — no cards, MRR, or payment-success claims are ever fabricated.
            </p>
          </div>
        </div>
      ) : null}

      <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
        <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-lg p-4">
          <p className="text-xs text-[var(--text-secondary)]">Balance</p>
          {balance.isLoading ? (
            <p className="text-sm text-[var(--text-muted)]">Loading…</p>
          ) : bal ? (
            <p className="text-2xl font-bold text-[var(--text-primary)]">{money(bal.balance_cents, bal.currency)}</p>
          ) : (
            <p className="text-sm text-[var(--text-muted)]">Unavailable</p>
          )}
        </div>
        <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-lg p-4">
          <p className="text-xs text-[var(--text-secondary)]">Credits</p>
          <p className="text-2xl font-bold text-[var(--text-primary)]">{rec ? money(rec.total_credits_cents, rec.currency) : "—"}</p>
        </div>
        <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-lg p-4">
          <p className="text-xs text-[var(--text-secondary)]">Debits</p>
          <p className="text-2xl font-bold text-[var(--text-primary)]">{rec ? money(rec.total_debits_cents, rec.currency) : "—"}</p>
        </div>
      </div>

      {rec && (
        <div className={`rounded-lg border p-4 ${rec.discrepant ? "border-[var(--danger)] bg-[var(--danger)]/5" : "border-[var(--border)] bg-[var(--bg-surface)]"}`}>
          <p className="text-sm font-medium text-[var(--text-primary)]">
            Reconciliation {rec.discrepant ? "DISCREPANT" : "in sync"}
          </p>
          <p className="text-sm text-[var(--text-secondary)]">
            Stored {money(rec.stored_balance_cents, rec.currency)} · Recomputed {money(rec.recomputed_balance_cents, rec.currency)} · Discrepancy {formatSignedMinorUnits(rec.discrepancy_cents, rec.currency)}
          </p>
        </div>
      )}

      <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-lg p-4">
        <h3 className="text-sm font-semibold text-[var(--text-primary)] mb-3">Create Adjustment</h3>
        <div className="grid grid-cols-1 md:grid-cols-2 gap-3">
          <label className="block text-sm text-[var(--text-secondary)]">
            Type
            <select value={type} onChange={(e) => setType(e.target.value as AdjustmentType)} className="mt-1 w-full px-3 py-2 bg-[var(--bg-base)] border border-[var(--border)] rounded text-sm text-[var(--text-primary)]">
              {ADJUSTMENT_TYPES.map((t) => (
                <option key={t} value={t}>{t}</option>
              ))}
            </select>
          </label>
          <label className="block text-sm text-[var(--text-secondary)]">
            Amount (minor units)
            <input
              type="number"
              min={1}
              value={amountCents}
              onChange={(e) => setAmountCents(e.target.value)}
              className="mt-1 w-full px-3 py-2 bg-[var(--bg-base)] border border-[var(--border)] rounded text-sm text-[var(--text-primary)]"
            />
          </label>
          <label className="block text-sm text-[var(--text-secondary)]">
            Currency
            <input
              value={currency}
              onChange={(e) => setCurrency(e.target.value)}
              className="mt-1 w-full px-3 py-2 bg-[var(--bg-base)] border border-[var(--border)] rounded text-sm text-[var(--text-primary)]"
            />
          </label>
          <label className="block text-sm text-[var(--text-secondary)]">
            Reason (required)
            <input
              value={reason}
              onChange={(e) => setReason(e.target.value)}
              className="mt-1 w-full px-3 py-2 bg-[var(--bg-base)] border border-[var(--border)] rounded text-sm text-[var(--text-primary)]"
            />
          </label>
        </div>
        <button
          onClick={submit}
          disabled={createAdj.isPending}
          className="mt-3 px-4 py-2 bg-[var(--accent)] text-white rounded text-sm disabled:opacity-50"
        >
          {createAdj.isPending ? "Applying…" : "Apply Adjustment"}
        </button>
        {result && (
          <p className={`mt-2 text-sm ${result.kind === "ok" ? "text-[var(--success)]" : "text-[var(--danger)]"}`}>{result.message}</p>
        )}
      </div>

      <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-lg">
        <h3 className="text-sm font-semibold text-[var(--text-primary)] p-4 border-b border-[var(--border)]">Adjustment History</h3>
        {adjustments.isLoading ? (
          <p className="p-4 text-sm text-[var(--text-muted)]">Loading…</p>
        ) : !adjustments.data || adjustments.data.adjustments.length === 0 ? (
          <p className="p-4 text-sm text-[var(--text-muted)]">No adjustments recorded for this tenant.</p>
        ) : (
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-[var(--border)] text-left text-[var(--text-secondary)]">
                <th className="p-3">Type</th>
                <th className="p-3">Amount</th>
                <th className="p-3">Reason</th>
                <th className="p-3">Actor</th>
                <th className="p-3">Created</th>
              </tr>
            </thead>
            <tbody>
              {adjustments.data.adjustments.map((a) => (
                <tr key={a.id} className="border-b border-[var(--bg-subtle)]">
                  <td className="p-3 text-[var(--text-primary)]">{a.type}</td>
                  <td className="p-3 text-[var(--text-primary)]">{formatSignedMinorUnits(a.type === "credit" ? a.amount_cents : -a.amount_cents, a.currency)}</td>
                  <td className="p-3 text-[var(--text-secondary)]">{a.reason}</td>
                  <td className="p-3 text-[var(--text-secondary)]">user:{a.actor_id}</td>
                  <td className="p-3 text-[var(--text-secondary)]">{new Date(a.created_at).toLocaleString()}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
    </div>
  );
}
