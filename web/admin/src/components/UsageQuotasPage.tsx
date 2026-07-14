import { useQuery } from "@tanstack/react-query";
import { BarChart, Globe, Mail, Send, HardDrive, AlertTriangle } from "lucide-react";
import { api } from "../api";

export default function UsageQuotasPage() {
  const { data: usage } = useQuery({ queryKey: ["usage"], queryFn: api.getUsage });
  const { data: sub } = useQuery({ queryKey: ["subscription"], queryFn: api.getSubscription });
  const { data: plans } = useQuery({ queryKey: ["plans"], queryFn: api.getPlans });

  const plan = plans?.find((p: any) => p.id === sub?.plan_id);

  const usageItems = [
    { label: "Domains", used: usage?.domains_used || 0, limit: plan?.max_domains || 1, icon: Globe },
    { label: "Mailboxes", used: usage?.mailboxes_used || 0, limit: plan?.max_mailboxes || 5, icon: Mail },
    { label: "Emails Sent", used: usage?.emails_sent || 0, limit: plan?.send_limit_day || 500, icon: Send, suffix: "/day" },
    { label: "Storage", used: Math.round((usage?.storage_used_mb || 0) / 1024 * 10) / 10, limit: Math.round((plan?.storage_mb || 1024) / 1024 * 10) / 10, icon: HardDrive, unit: "GB" },
    { label: "API Calls", used: usage?.api_calls || 0, limit: 10000, icon: BarChart },
  ];

  return (
    <div className="space-y-6">
      <h2 className="text-xl font-semibold text-white">Usage & Quotas</h2>

      {plan && (
        <div className="bg-[#1A1D24] border border-[#262A33] rounded-lg p-4 text-sm">
          <span className="text-gray-400">Plan: </span>
          <span className="text-white font-medium">{plan.name}</span>
          {sub?.status && (
            <span className={`ml-2 text-xs px-2 py-0.5 rounded ${
              sub.status === "active" || sub.status === "trialing" ? "bg-green-400/10 text-green-400" : "bg-yellow-400/10 text-yellow-400"
            }`}>{sub.status}</span>
          )}
        </div>
      )}

      <div className="grid gap-4">
        {usageItems.map((item) => {
          const Icon = item.icon;
          const pct = item.limit > 0 ? Math.min(100, Math.round((item.used / item.limit) * 100)) : 0;
          const isNearLimit = pct >= 80;
          const isOverLimit = pct >= 100;

          return (
            <div key={item.label} className="bg-[#1A1D24] border border-[#262A33] rounded-lg p-4">
              <div className="flex items-center justify-between mb-2">
                <div className="flex items-center gap-2">
                  <Icon className={`w-4 h-4 ${isOverLimit ? "text-red-400" : isNearLimit ? "text-yellow-400" : "text-[#4F7CFF]"}`} />
                  <span className="text-white text-sm">{item.label}</span>
                </div>
                <div className="flex items-center gap-2">
                  {isOverLimit && <AlertTriangle className="w-4 h-4 text-red-400" />}
                  <span className="text-sm text-gray-300">
                    {item.used}{item.unit || ""} / {item.limit}{item.unit || ""}{item.suffix || ""}
                  </span>
                </div>
              </div>
              <div className="h-2 bg-[#0C0E12] rounded-full overflow-hidden">
                <div className={`h-full rounded-full transition-all ${
                  isOverLimit ? "bg-red-500" : isNearLimit ? "bg-yellow-500" : "bg-[#4F7CFF]"
                }`} style={{ width: `${Math.min(100, pct)}%` }} />
              </div>
            </div>
          );
        })}
      </div>
    </div>
  );
}
