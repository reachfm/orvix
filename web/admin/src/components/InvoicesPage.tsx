import { useQuery } from "@tanstack/react-query";
import { CreditCard, Download, FileText, HardDrive, Mail } from "lucide-react";
import { api } from "../api";

export default function InvoicesPage() {
  const { data: sub, isLoading: subLoading } = useQuery({ queryKey: ["subscription"], queryFn: api.getSubscription });
  const { data: usage, isLoading: usageLoading } = useQuery({ queryKey: ["usage"], queryFn: api.getUsage });

  if (subLoading || usageLoading) return <p className="text-[#8B92A8]">Loading...</p>;

  const statusColors: Record<string, string> = {
    active: "bg-[#34D399]/10 text-[#34D399]",
    trialing: "bg-[#4F7CFF]/10 text-[#4F7CFF]",
    past_due: "bg-[#FBBF24]/10 text-[#FBBF24]",
    cancelled: "bg-[#F87171]/10 text-[#F87171]",
    suspended: "bg-[#F87171]/10 text-[#F87171]",
  };

  const invoiceEntries = sub ? [
    {
      id: 1,
      description: `${sub.plan_id || "Subscription"} - Monthly charge`,
      amount: sub.amount ? `$${(sub.amount / 100).toFixed(2)}` : "-",
      date: sub.current_period_start || "-",
      status: sub.status || "unknown",
    },
  ] : [];

  return (
    <div className="space-y-6">
      <h2 className="text-xl font-semibold text-white">Billing & Invoices</h2>

      {sub ? (
        <div className="bg-[#1A1D24] border border-[#262A33] rounded-lg p-6">
          <div className="flex items-center gap-3 mb-4">
            <CreditCard className="w-5 h-5 text-[#4F7CFF]" />
            <h3 className="text-lg font-medium text-white">Current Plan</h3>
            {sub.status && (
              <span className={`px-2 py-1 rounded text-xs font-medium ${statusColors[sub.status] || "text-gray-400 bg-gray-400/10"}`}>
                {sub.status}
              </span>
            )}
          </div>
          <div className="grid grid-cols-2 gap-4 text-sm">
            <div><span className="text-gray-400">Plan: </span><span className="text-white">{sub.plan_id || "-"}</span></div>
            <div><span className="text-gray-400">Status: </span><span className="text-white">{(sub.status || "").replace(/_/g, " ")}</span></div>
            <div><span className="text-gray-400">Period Start: </span><span className="text-white">{sub.current_period_start || "-"}</span></div>
            <div><span className="text-gray-400">Period End: </span><span className="text-white">{sub.current_period_end || "-"}</span></div>
            {sub.storage_mb && <div><span className="text-gray-400">Storage: </span><span className="text-white">{sub.storage_mb} MB</span></div>}
            {sub.send_limit_day && <div><span className="text-gray-400">Send Limit: </span><span className="text-white">{sub.send_limit_day}/day</span></div>}
          </div>
        </div>
      ) : (
        <div className="bg-[#1A1D24] border border-[#262A33] rounded-lg p-6 text-center">
          <CreditCard className="w-8 h-8 text-[#555D73] mx-auto mb-2" />
          <p className="text-gray-400">No active subscription</p>
          <p className="text-sm text-gray-500 mt-1">Subscribe to a plan to view billing details</p>
        </div>
      )}

      {usage && (
        <div className="bg-[#1A1D24] border border-[#262A33] rounded-lg p-6">
          <h3 className="text-lg font-medium text-white mb-4">Usage Summary</h3>
          <div className="grid grid-cols-2 md:grid-cols-4 gap-4">
            <div className="bg-[#0C0E12] rounded-lg p-4">
              <Mail className="w-4 h-4 text-[#4F7CFF] mb-2" />
              <p className="text-2xl font-bold text-white">{usage.mailboxes_used || 0}</p>
              <p className="text-xs text-gray-400">Mailboxes</p>
            </div>
            <div className="bg-[#0C0E12] rounded-lg p-4">
              <CreditCard className="w-4 h-4 text-[#34D399] mb-2" />
              <p className="text-2xl font-bold text-white">{usage.domains_used || 0}</p>
              <p className="text-xs text-gray-400">Domains</p>
            </div>
            <div className="bg-[#0C0E12] rounded-lg p-4">
              <HardDrive className="w-4 h-4 text-[#FBBF24] mb-2" />
              <p className="text-2xl font-bold text-white">{usage.emails_sent || 0}</p>
              <p className="text-xs text-gray-400">Emails Sent</p>
            </div>
            <div className="bg-[#0C0E12] rounded-lg p-4">
              <HardDrive className="w-4 h-4 text-[#F87171] mb-2" />
              <p className="text-2xl font-bold text-white">{usage.emails_received || 0}</p>
              <p className="text-xs text-gray-400">Received</p>
            </div>
          </div>
        </div>
      )}

      <div className="bg-[#1A1D24] border border-[#262A33] rounded-lg p-6">
        <div className="flex items-center gap-3 mb-4">
          <FileText className="w-5 h-5 text-[#4F7CFF]" />
          <h3 className="text-lg font-medium text-white">Invoices</h3>
        </div>

        {invoiceEntries.length > 0 ? (
          <div className="overflow-hidden rounded-lg border border-[#2A2F3E]">
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b border-[#2A2F3E] bg-[#0C0E12]">
                  <th className="text-left p-3 text-[#8B92A8] font-medium">Description</th>
                  <th className="text-left p-3 text-[#8B92A8] font-medium">Date</th>
                  <th className="text-left p-3 text-[#8B92A8] font-medium">Amount</th>
                  <th className="text-left p-3 text-[#8B92A8] font-medium">Status</th>
                  <th className="text-right p-3 text-[#8B92A8] font-medium">Download</th>
                </tr>
              </thead>
              <tbody>
                {invoiceEntries.map((inv) => (
                  <tr key={inv.id} className="border-b border-[#262A33] hover:bg-[#13161C]">
                    <td className="p-3 text-[#E8EAF0]">{inv.description}</td>
                    <td className="p-3 text-[#8B92A8]">{inv.date}</td>
                    <td className="p-3 text-white font-medium">{inv.amount}</td>
                    <td className="p-3">
                      <span className={`px-2 py-1 text-xs rounded-full ${statusColors[inv.status] || "text-gray-400 bg-gray-400/10"}`}>
                        {inv.status}
                      </span>
                    </td>
                    <td className="p-3 text-right">
                      <button
                        disabled
                        title="Invoice generation coming soon"
                        className="inline-flex items-center gap-1 text-xs text-[#555D73] cursor-not-allowed"
                      >
                        <Download size={14} /> PDF
                      </button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        ) : (
          <p className="text-[#8B92A8] text-sm text-center py-4">No invoices yet.</p>
        )}
      </div>
    </div>
  );
}
