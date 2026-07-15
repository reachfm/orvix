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
    suspended: "text-red-400 bg-red-400/10", cancelled: "text-gray-400 bg-gray-400/10",
    expired: "text-gray-400 bg-gray-400/10",
  };

  return (
    <div className="space-y-6">
      <h2 className="text-xl font-semibold text-white">Billing & Subscription</h2>

      {subLoading ? <div className="text-gray-400">Loading subscription...</div> : sub ? (
        <div className="bg-[#1A1D24] border border-[#262A33] rounded-lg p-6">
          <div className="flex items-center gap-3 mb-4">
            <CreditCard className="w-5 h-5 text-[#4F7CFF]" />
            <h3 className="text-lg font-medium text-white">Current Subscription</h3>
            <span className={`px-2 py-1 rounded text-xs font-medium ${statusColors[sub.status] || "text-gray-400"}`}>
              {sub.status.replace(/_/g, " ")}
            </span>
          </div>
          <div className="grid grid-cols-2 gap-4 text-sm">
            <div><span className="text-gray-400">Plan: </span><span className="text-white">{sub.plan_id}</span></div>
            <div><span className="text-gray-400">Storage: </span><span className="text-white">{sub.storage_mb} MB</span></div>
            <div><span className="text-gray-400">Send Limit: </span><span className="text-white">{sub.send_limit_day} / day</span></div>
            <div><span className="text-gray-400">Period End: </span><span className="text-white">{sub.current_period_end}</span></div>
          </div>
        </div>
      ) : (
        <div className="bg-[#1A1D24] border border-[#262A33] rounded-lg p-6 text-center">
          <AlertTriangle className="w-8 h-8 text-yellow-400 mx-auto mb-2" />
          <p className="text-gray-400">No active subscription</p>
          <p className="text-sm text-gray-500 mt-1">Billing provider not configured</p>
        </div>
      )}

      {usage && (
        <div className="bg-[#1A1D24] border border-[#262A33] rounded-lg p-6">
          <h3 className="text-lg font-medium text-white mb-4">Usage</h3>
          <div className="grid grid-cols-2 gap-4 text-sm">
            <div><span className="text-gray-400">Emails Sent: </span><span className="text-white">{usage.emails_sent}</span></div>
            <div><span className="text-gray-400">Emails Received: </span><span className="text-white">{usage.emails_received}</span></div>
            <div><span className="text-gray-400">Mailboxes: </span><span className="text-white">{usage.mailboxes_used}</span></div>
            <div><span className="text-gray-400">Domains: </span><span className="text-white">{usage.domains_used}</span></div>
          </div>
        </div>
      )}

      {plans && (
        <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-4 gap-4">
          {plans.map((p: any) => (
            <div key={p.id} className={`bg-[#1A1D24] border rounded-lg p-6 ${selectedPlan === p.id ? "border-[#4F7CFF]" : "border-[#262A33]"}`}>
              <h4 className="text-white font-medium">{p.name}</h4>
              <p className="text-2xl font-bold text-white mt-2">
                ${(p.price_monthly / 100).toFixed(0)}<span className="text-sm text-gray-400">/mo</span>
              </p>
              <div className="text-xs text-gray-400 mt-3 space-y-1">
                <p>{p.max_mailboxes} mailboxes</p>
                <p>{p.max_domains} domains</p>
                <p>{p.storage_mb} MB storage</p>
                <p>{p.send_limit_day} sends/day</p>
              </div>
              {sub?.plan_id !== p.id && (
                <button onClick={() => createSub.mutate(p.id)} className="mt-4 w-full bg-[#4F7CFF] text-white rounded py-2 text-sm hover:bg-[#3D6AE8] transition">
                  {createSub.isPending ? "Saving..." : "Select"}
                </button>
              )}
              {sub?.plan_id === p.id && <div className="mt-4 w-full text-center text-[#4F7CFF] text-sm flex items-center justify-center gap-1"><Check className="w-4 h-4" /> Current</div>}
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
