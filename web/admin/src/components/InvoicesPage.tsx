import { useQuery } from "@tanstack/react-query";
import { CreditCard, Download, ExternalLink, FileText, HardDrive, Mail } from "lucide-react";
import { api } from "../api";

export default function InvoicesPage() {
  const { data: sub, isLoading: subLoading } = useQuery({ queryKey: ["subscription"], queryFn: api.getSubscription });
  const { data: usage, isLoading: usageLoading } = useQuery({ queryKey: ["usage"], queryFn: api.getUsage });
  const { data: invoices, isLoading: invLoading } = useQuery({ queryKey: ["invoices"], queryFn: api.listInvoices });

  if (subLoading || usageLoading || invLoading) return <p className="text-[var(--text-secondary)]">Loading...</p>;

  const statusColors: Record<string, string> = {
    active: "bg-[var(--success)]/10 text-[var(--success)]",
    trialing: "bg-[var(--accent)]/10 text-[var(--accent)]",
    past_due: "bg-[var(--warning)]/10 text-[var(--warning)]",
    cancelled: "bg-[var(--danger)]/10 text-[var(--danger)]",
    suspended: "bg-[var(--danger)]/10 text-[var(--danger)]",
    paid: "bg-[var(--success)]/10 text-[var(--success)]",
    open: "bg-[var(--accent)]/10 text-[var(--accent)]",
    void: "bg-[var(--text-muted)]/10 text-[var(--text-muted)]",
    uncollectible: "bg-[var(--danger)]/10 text-[var(--danger)]",
  };

  const formatAmount = (amount: number, currency: string) => {
    const major = (amount / 100).toFixed(2);
    return new Intl.NumberFormat("en-US", { style: "currency", currency: (currency || "USD").toUpperCase() })
      .format(parseFloat(major));
  };

  const invList = Array.isArray(invoices) ? invoices : [];

  return (
    <div className="space-y-6">
      <h2 className="text-xl font-semibold text-[var(--text-primary)]">Billing & Invoices</h2>

      {sub ? (
        <div className="bg-[var(--bg-elevated)] border border-[var(--bg-subtle)] rounded-lg p-6">
          <div className="flex items-center gap-3 mb-4">
            <CreditCard className="w-5 h-5 text-[var(--accent)]" />
            <h3 className="text-lg font-medium text-[var(--text-primary)]">Current Plan</h3>
            {sub.status && (
              <span className={`px-2 py-1 rounded text-xs font-medium ${statusColors[sub.status] || "text-[var(--text-secondary)] bg-[var(--text-muted)]/10"}`}>
                {sub.status}
              </span>
            )}
          </div>
          <div className="grid grid-cols-2 gap-4 text-sm">
            <div><span className="text-[var(--text-secondary)]">Plan: </span><span className="text-[var(--text-primary)]">{sub.plan_id || "-"}</span></div>
            <div><span className="text-[var(--text-secondary)]">Status: </span><span className="text-[var(--text-primary)]">{(sub.status || "").replace(/_/g, " ")}</span></div>
            <div><span className="text-[var(--text-secondary)]">Period Start: </span><span className="text-[var(--text-primary)]">{sub.current_period_start || "-"}</span></div>
            <div><span className="text-[var(--text-secondary)]">Period End: </span><span className="text-[var(--text-primary)]">{sub.current_period_end || "-"}</span></div>
          </div>
        </div>
      ) : (
        <div className="bg-[var(--bg-elevated)] border border-[var(--bg-subtle)] rounded-lg p-6 text-center">
          <CreditCard className="w-8 h-8 text-[var(--text-muted)] mx-auto mb-2" />
          <p className="text-[var(--text-secondary)]">No active subscription</p>
          <p className="text-sm text-[var(--text-muted)] mt-1">Subscribe to a plan to view billing details</p>
        </div>
      )}

      {usage && (
        <div className="bg-[var(--bg-elevated)] border border-[var(--bg-subtle)] rounded-lg p-6">
          <h3 className="text-lg font-medium text-[var(--text-primary)] mb-4">Usage Summary</h3>
          <div className="grid grid-cols-2 md:grid-cols-4 gap-4">
            <div className="bg-[var(--bg-base)] rounded-lg p-4">
              <Mail className="w-4 h-4 text-[var(--accent)] mb-2" />
              <p className="text-2xl font-bold text-[var(--text-primary)]">{usage.mailboxes_used || 0}</p>
              <p className="text-xs text-[var(--text-secondary)]">Mailboxes</p>
            </div>
            <div className="bg-[var(--bg-base)] rounded-lg p-4">
              <CreditCard className="w-4 h-4 text-[var(--success)] mb-2" />
              <p className="text-2xl font-bold text-[var(--text-primary)]">{usage.domains_used || 0}</p>
              <p className="text-xs text-[var(--text-secondary)]">Domains</p>
            </div>
            <div className="bg-[var(--bg-base)] rounded-lg p-4">
              <HardDrive className="w-4 h-4 text-[var(--warning)] mb-2" />
              <p className="text-2xl font-bold text-[var(--text-primary)]">{usage.emails_sent || 0}</p>
              <p className="text-xs text-[var(--text-secondary)]">Emails Sent</p>
            </div>
            <div className="bg-[var(--bg-base)] rounded-lg p-4">
              <HardDrive className="w-4 h-4 text-[var(--danger)] mb-2" />
              <p className="text-2xl font-bold text-[var(--text-primary)]">{usage.emails_received || 0}</p>
              <p className="text-xs text-[var(--text-secondary)]">Received</p>
            </div>
          </div>
        </div>
      )}

      <div className="bg-[var(--bg-elevated)] border border-[var(--bg-subtle)] rounded-lg p-6">
        <div className="flex items-center gap-3 mb-4">
          <FileText className="w-5 h-5 text-[var(--accent)]" />
          <h3 className="text-lg font-medium text-[var(--text-primary)]">Invoices</h3>
        </div>

        {invList.length > 0 ? (
          <div className="overflow-hidden rounded-lg border border-[var(--border)]">
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b border-[var(--border)] bg-[var(--bg-base)]">
                  <th className="text-left p-3 text-[var(--text-secondary)] font-medium">Invoice</th>
                  <th className="text-left p-3 text-[var(--text-secondary)] font-medium">Period</th>
                  <th className="text-left p-3 text-[var(--text-secondary)] font-medium">Amount</th>
                  <th className="text-left p-3 text-[var(--text-secondary)] font-medium">Status</th>
                  <th className="text-right p-3 text-[var(--text-secondary)] font-medium">Actions</th>
                </tr>
              </thead>
              <tbody>
                {invList.map((inv: any) => (
                  <tr key={inv.id} className="border-b border-[var(--bg-subtle)] hover:bg-[var(--bg-surface)]">
                    <td className="p-3 text-[var(--text-primary)]">{inv.invoice_number || `#${inv.id}`}</td>
                    <td className="p-3 text-[var(--text-secondary)]">
                      {inv.period_start ? new Date(inv.period_start).toLocaleDateString() : "-"}
                      {" – "}
                      {inv.period_end ? new Date(inv.period_end).toLocaleDateString() : "-"}
                    </td>
                    <td className="p-3 text-[var(--text-primary)] font-medium">{formatAmount(inv.total, inv.currency)}</td>
                    <td className="p-3">
                      <span className={`px-2 py-1 text-xs rounded-full ${statusColors[inv.status] || "text-[var(--text-secondary)] bg-[var(--text-muted)]/10"}`}>
                        {inv.status}
                      </span>
                    </td>
                    <td className="p-3 text-right">
                      <div className="flex items-center justify-end gap-2">
                        {inv.hosted_invoice_url && (
                          <a href={inv.hosted_invoice_url} target="_blank" rel="noopener noreferrer"
                            className="inline-flex items-center gap-1 text-xs text-[var(--accent)] hover:underline">
                            <ExternalLink size={14} /> View
                          </a>
                        )}
                        {inv.pdf_url ? (
                          <a href={inv.pdf_url} target="_blank" rel="noopener noreferrer"
                            className="inline-flex items-center gap-1 text-xs text-[var(--accent)] hover:underline">
                            <Download size={14} /> PDF
                          </a>
                        ) : (
                          <span className="text-xs text-[var(--text-muted)]">–</span>
                        )}
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        ) : (
          <p className="text-[var(--text-secondary)] text-sm text-center py-4">No invoices yet.</p>
        )}
      </div>
    </div>
  );
}
