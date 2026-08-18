import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { CreditCard, Check, AlertTriangle, RefreshCw, X } from "lucide-react";
import { api } from "../api";

export default function BillingPage() {
  const queryClient = useQueryClient();
  const [selectedPlan, setSelectedPlan] = useState("");

  const { data: plans, isLoading: plansLoading } = useQuery({ queryKey: ["plans"], queryFn: api.getPlans });
  const { data: sub, isLoading: subLoading } = useQuery({ queryKey: ["subscription"], queryFn: api.getSubscription });
  const { data: usage } = useQuery({ queryKey: ["usage"], queryFn: api.getUsage });
  // GET /enterprise/billing/state — the HONEST provider-configuration
  // read. "Payment provider not configured" is rendered from
  // payment_provider.configured=false, never from a hardcoded string.
  const { data: billingState, error: billingStateError } = useQuery({
    queryKey: ["billing-state"],
    queryFn: api.getBillingState,
  });

  const createSub = useMutation({
    mutationFn: (planId: string) => api.createSubscription({ plan_id: planId, billing_interval: "monthly" }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["subscription"] });
      queryClient.invalidateQueries({ queryKey: ["billing-state"] });
    },
  });

  const provider = billingState?.payment_provider;
  const providerConfigured = provider?.configured === true;

  const statusColors: Record<string, string> = {
    active: "text-[var(--success)] bg-[var(--success)]/10", trialing: "text-[var(--info)] bg-[var(--info)]/10",
    past_due: "text-[var(--warning)] bg-[var(--warning)]/10", grace_period: "text-[var(--warning)] bg-[var(--warning)]/10",
    suspended: "text-[var(--danger)] bg-[var(--danger)]/10", cancelled: "text-[var(--text-secondary)] bg-[var(--text-muted)]/10",
    expired: "text-[var(--text-secondary)] bg-[var(--text-muted)]/10",
  };

  return (
    <div className="space-y-6">
      <h2 className="text-xl font-semibold text-[var(--text-primary)]">Billing & Subscription</h2>

      {subLoading ? <div className="text-[var(--text-secondary)]">Loading subscription...</div> : sub ? (
        <div className="bg-[var(--bg-elevated)] border border-[var(--bg-subtle)] rounded-lg p-6">
          <div className="flex items-center gap-3 mb-4">
            <CreditCard className="w-5 h-5 text-[var(--accent)]" />
            <h3 className="text-lg font-medium text-[var(--text-primary)]">Current Subscription</h3>
            <span className={`px-2 py-1 rounded text-xs font-medium ${statusColors[sub.status] || "text-[var(--text-secondary)]"}`}>
              {sub.status.replace(/_/g, " ")}
            </span>
          </div>
          <div className="grid grid-cols-2 gap-4 text-sm">
            <div><span className="text-[var(--text-secondary)]">Plan: </span><span className="text-[var(--text-primary)]">{sub.plan_id}</span></div>
            <div><span className="text-[var(--text-secondary)]">Storage: </span><span className="text-[var(--text-primary)]">{sub.storage_mb} MB</span></div>
            <div><span className="text-[var(--text-secondary)]">Send Limit: </span><span className="text-[var(--text-primary)]">{sub.send_limit_day} / day</span></div>
            <div><span className="text-[var(--text-secondary)]">Period End: </span><span className="text-[var(--text-primary)]">{sub.current_period_end}</span></div>
          </div>
        </div>
      ) : (
        <div className="bg-[var(--bg-elevated)] border border-[var(--bg-subtle)] rounded-lg p-6 text-center">
          <AlertTriangle className="w-8 h-8 text-[var(--warning)] mx-auto mb-2" />
          <p className="text-[var(--text-secondary)]">No active subscription</p>
        </div>
      )}

      {billingStateError && !providerConfigured && (
        <div className="bg-[var(--bg-elevated)] border border-[var(--warning)]/30 rounded-lg p-4 text-sm" role="status">
          <p className="text-[var(--text-primary)] font-medium flex items-center gap-1.5">
            <AlertTriangle size={14} className="text-[var(--warning)]" /> Payment provider state unavailable
          </p>
          <p className="text-[var(--text-secondary)] text-xs mt-1">
            The backend could not report payment configuration right now: {(billingStateError as Error).message}
          </p>
        </div>
      )}

      {provider && (
        <div className={`bg-[var(--bg-elevated)] border rounded-lg p-4 text-sm ${providerConfigured ? "border-[var(--success)]/30" : "border-[var(--warning)]/30"}`}>
          {providerConfigured ? (
            <p className="text-[var(--text-primary)] flex items-center gap-1.5">
              <Check size={14} className="text-[var(--success)]" /> Payment provider: {provider.provider}
              {provider.enabled ? "" : " (configured but disabled)"}
            </p>
          ) : (
            <p className="text-[var(--text-primary)] flex items-center gap-1.5">
              <AlertTriangle size={14} className="text-[var(--warning)]" /> Payment provider not configured
            </p>
          )}
          <p className="text-[var(--text-muted)] text-xs mt-1">
            {provider.note}
            {providerConfigured ? "" : " — cards, MRR, and paid invoices are never fabricated; they appear only from real provider events."}
          </p>
        </div>
      )}

      {usage && (
        <div className="bg-[var(--bg-elevated)] border border-[var(--bg-subtle)] rounded-lg p-6">
          <h3 className="text-lg font-medium text-[var(--text-primary)] mb-4">Usage</h3>
          <div className="grid grid-cols-2 gap-4 text-sm">
            <div><span className="text-[var(--text-secondary)]">Emails Sent: </span><span className="text-[var(--text-primary)]">{usage.emails_sent}</span></div>
            <div><span className="text-[var(--text-secondary)]">Emails Received: </span><span className="text-[var(--text-primary)]">{usage.emails_received}</span></div>
            <div><span className="text-[var(--text-secondary)]">Mailboxes: </span><span className="text-[var(--text-primary)]">{usage.mailboxes_used}</span></div>
            <div><span className="text-[var(--text-secondary)]">Domains: </span><span className="text-[var(--text-primary)]">{usage.domains_used}</span></div>
          </div>
        </div>
      )}

      {plans && (
        <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-4 gap-4">
          {plans.map((p: any) => (
            <div key={p.id} className={`bg-[var(--bg-elevated)] border rounded-lg p-6 ${selectedPlan === p.id ? "border-[var(--accent)]" : "border-[var(--bg-subtle)]"}`}>
              <h4 className="text-[var(--text-primary)] font-medium">{p.name}</h4>
              <p className="text-2xl font-bold text-[var(--text-primary)] mt-2">
                ${(p.price_monthly / 100).toFixed(0)}<span className="text-sm text-[var(--text-secondary)]">/mo</span>
              </p>
              <div className="text-xs text-[var(--text-secondary)] mt-3 space-y-1">
                <p>{p.max_mailboxes} mailboxes</p>
                <p>{p.max_domains} domains</p>
                <p>{p.storage_mb} MB storage</p>
                <p>{p.send_limit_day} sends/day</p>
              </div>
              {sub?.plan_id !== p.id && (
                <button onClick={() => createSub.mutate(p.id)} className="mt-4 w-full bg-[var(--accent)] text-white rounded py-2 text-sm hover:bg-[var(--accent-hover)] transition">
                  {createSub.isPending ? "Saving..." : "Select"}
                </button>
              )}
              {sub?.plan_id === p.id && <div className="mt-4 w-full text-center text-[var(--accent)] text-sm flex items-center justify-center gap-1"><Check className="w-4 h-4" /> Current</div>}
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
