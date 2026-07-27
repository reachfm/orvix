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

  const createSub = useMutation({
    mutationFn: (planId: string) => api.createSubscription({ plan_id: planId, billing_interval: "monthly" }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["subscription"] }),
  });

  const statusColors: Record<string, string> = {
    active: "text-green-400 bg-green-400/10", trialing: "text-blue-400 bg-blue-400/10",
    past_due: "text-yellow-400 bg-yellow-400/10", grace_period: "text-orange-400 bg-orange-400/10",
    suspended: "text-red-400 bg-red-400/10", cancelled: "text-[var(--text-secondary)] bg-gray-400/10",
    expired: "text-[var(--text-secondary)] bg-gray-400/10",
  };

  return (
    <div className="space-y-6">
      <h2 className="text-xl font-semibold text-[var(--text-primary)]">Billing & Subscription</h2>

      {subLoading ? <div className="text-[var(--text-secondary)]">Loading subscription...</div> : sub ? (
        <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-lg p-6">
          <div className="flex items-center gap-3 mb-4">
            <CreditCard className="w-5 h-5 text-[var(--accent-blue)]" />
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
        <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-lg p-6 text-center">
          <AlertTriangle className="w-8 h-8 text-yellow-400 mx-auto mb-2" />
          <p className="text-[var(--text-secondary)]">No active subscription</p>
          <p className="text-sm text-[var(--text-muted)] mt-1">Billing provider not configured</p>
        </div>
      )}

      {usage && (
        <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-lg p-6">
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
            <div key={p.id} className={`bg-[var(--bg-surface)] border rounded-lg p-6 ${selectedPlan === p.id ? "border-[#4F7CFF]" : "border-[var(--border)]"}`}>
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
                <button onClick={() => createSub.mutate(p.id)} className="mt-4 w-full bg-[var(--accent-blue)] text-[var(--text-primary)] rounded py-2 text-sm hover:bg-[var(--accent-hover)] transition">
                  {createSub.isPending ? "Saving..." : "Select"}
                </button>
              )}
              {sub?.plan_id === p.id && <div className="mt-4 w-full text-center text-[var(--accent-blue)] text-sm flex items-center justify-center gap-1"><Check className="w-4 h-4" /> Current</div>}
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
