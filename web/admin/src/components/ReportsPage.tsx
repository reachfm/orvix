import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { BarChart3, TrendingUp, Mail, Building2, Download, FileText, Activity } from "lucide-react";
import { LineChart, Line, BarChart, Bar, XAxis, YAxis, CartesianGrid, Tooltip, ResponsiveContainer } from "recharts";
import { api } from "../api";

function Tabs({ tabs, active, onChange }: { tabs: { id: string; label: string }[]; active: string; onChange: (id: string) => void }) {
  return <div className="flex gap-1 border-b border-[var(--border)] mb-5">{tabs.map((t) => <button key={t.id} onClick={() => onChange(t.id)} className={`px-4 py-2.5 text-sm font-medium border-b-2 transition-colors ${active === t.id ? "border-[var(--accent)] text-[var(--accent)]" : "border-transparent text-[var(--text-muted)] hover:text-[var(--text-secondary)]"}`}>{t.label}</button>)}</div>;
}

export default function ReportsPage() {
  const [tab, setTab] = useState("traffic");
  const [range, setRange] = useState("30d");

  const { data: traffic, isLoading: tl } = useQuery({ queryKey: ["traffic", range], queryFn: () => api.getEmailTrafficStats?.(range === "7d" ? 7 : range === "90d" ? 90 : 30) ?? Promise.resolve([]), enabled: tab === "traffic" });
  const { data: delivery } = useQuery({ queryKey: ["delivery", range], queryFn: () => api.getDeliveryReport(range) ?? Promise.resolve([]), enabled: tab === "delivery" });
  const { data: tenants, isLoading: tnl } = useQuery({ queryKey: ["tenant-report"], queryFn: () => api.getTenantReport?.() ?? Promise.resolve([]), enabled: tab === "tenants" });

  const trafficData: any[] = Array.isArray(traffic) ? traffic : [];
  const deliveryData: any[] = Array.isArray(delivery) ? delivery : [];
  const tenantData: any[] = Array.isArray(tenants) ? tenants : [];

  const totalSent = trafficData.reduce((s: number, d: any) => s + (d.sent || 0), 0);
  const totalReceived = trafficData.reduce((s: number, d: any) => s + (d.received || 0), 0);
  const totalBounce = trafficData.reduce((s: number, d: any) => s + (d.bounce || 0), 0);
  const totalSpam = trafficData.reduce((s: number, d: any) => s + (d.spam || 0), 0);

  const handleExport = (type: string, format: string) => api.exportReport?.(type, format) ?? Promise.resolve();

  return (
    <div className="space-y-6">
      <div>
        <p className="text-xs font-semibold uppercase tracking-[0.14em] text-[var(--accent)] mb-1">Analytics & Insights</p>
        <h1 className="text-2xl font-bold text-[var(--text-primary)]">Reports</h1>
        <p className="mt-1 text-sm text-[var(--text-secondary)]">Email traffic, delivery metrics, tenant analytics</p>
      </div>

      <Tabs tabs={[{ id: "traffic", label: "Email Traffic" }, { id: "delivery", label: "Delivery" }, { id: "tenants", label: "Tenants" }, { id: "export", label: "Export" }]} active={tab} onChange={setTab} />

      {tab === "traffic" && (
        <div className="space-y-4">
          <div className="flex gap-2">{["7d","30d","90d"].map((r) => <button key={r} onClick={() => setRange(r)} className={`text-xs px-3 py-1 rounded-full border ${range === r ? "border-[var(--accent)] bg-[var(--accent)]/10 text-[var(--accent)]" : "border-[var(--border)] text-[var(--text-muted)]"}`}>{r}</button>)}</div>
          <div className="grid grid-cols-2 lg:grid-cols-4 gap-3">{[{ label: "Sent", value: totalSent.toLocaleString(), icon: TrendingUp, color: "var(--accent)" },{ label: "Received", value: totalReceived.toLocaleString(), icon: Mail, color: "var(--accent-blue)" },{ label: "Bounced", value: totalBounce.toLocaleString(), icon: Activity, color: "var(--accent-yellow)" },{ label: "Spam", value: totalSpam.toLocaleString(), icon: BarChart3, color: "var(--status-danger)" }].map((s) => <div key={s.label} className="rounded-xl border border-[var(--border)] bg-[var(--bg-surface)] p-4"><p className="text-xs text-[var(--text-muted)]">{s.label}</p><p className="text-2xl font-bold text-[var(--text-primary)] mt-1">{s.value}</p></div>)}</div>
          <div className="rounded-xl border border-[var(--border)] bg-[var(--bg-surface)] p-5 h-[300px]">
            <ResponsiveContainer><LineChart data={trafficData}><CartesianGrid stroke="var(--border)" strokeDasharray="3 3" /><XAxis dataKey="date" stroke="var(--text-muted)" fontSize={12} /><YAxis stroke="var(--text-muted)" fontSize={12} /><Tooltip contentStyle={{ background: "var(--bg-surface)", border: "1px solid var(--border)", borderRadius: 8 }} /><Line type="monotone" dataKey="sent" stroke="var(--accent)" strokeWidth={2} dot={false} /><Line type="monotone" dataKey="received" stroke="var(--accent-blue)" strokeWidth={2} dot={false} /><Line type="monotone" dataKey="bounce" stroke="var(--accent-yellow)" strokeWidth={2} dot={false} /><Line type="monotone" dataKey="spam" stroke="var(--status-danger)" strokeWidth={2} dot={false} /></LineChart></ResponsiveContainer>
          </div>
        </div>
      )}

      {tab === "delivery" && (
        <div className="space-y-4">
          <div className="rounded-xl border border-[var(--border)] bg-[var(--bg-surface)] p-5 h-[300px]">
            <ResponsiveContainer><BarChart data={deliveryData}><CartesianGrid stroke="var(--border)" strokeDasharray="3 3" /><XAxis dataKey="domain" stroke="var(--text-muted)" fontSize={12} /><YAxis stroke="var(--text-muted)" fontSize={12} /><Tooltip contentStyle={{ background: "var(--bg-surface)", border: "1px solid var(--border)", borderRadius: 8 }} /><Bar dataKey="delivered" fill="var(--accent)" radius={[4,4,0,0]} /></BarChart></ResponsiveContainer>
          </div>
          <div className="rounded-xl border border-[var(--border)] bg-[var(--bg-surface)] overflow-hidden"><table className="w-full text-sm"><thead><tr className="border-b border-[var(--border)]">{["Domain","Sent","Delivered","Bounced","Rate"].map((h) => <th key={h} className="text-left px-4 py-3 text-[var(--text-muted)] text-xs uppercase">{h}</th>)}</tr></thead><tbody>{deliveryData.map((d: any) => <tr key={d.domain} className="border-b border-[var(--border)] hover:bg-[var(--bg-base)]"><td className="px-4 py-3 text-[var(--text-primary)]">{d.domain}</td><td className="px-4 py-3">{d.sent}</td><td className="px-4 py-3 text-[var(--status-success)]">{d.delivered}</td><td className="px-4 py-3 text-[var(--status-danger)]">{d.bounced || 0}</td><td className="px-4 py-3">{d.rate || d.delivery_rate}%</td></tr>)}</tbody></table></div>
        </div>
      )}

      {tab === "tenants" && (
        <div className="rounded-xl border border-[var(--border)] bg-[var(--bg-surface)] overflow-hidden"><table className="w-full text-sm"><thead><tr className="border-b border-[var(--border)]">{["Tenant","Domains","Mailboxes","Storage","Plan","Health"].map((h) => <th key={h} className="text-left px-4 py-3 text-[var(--text-muted)] text-xs uppercase">{h}</th>)}</tr></thead><tbody>{tenantData.map((t: any) => <tr key={t.id || t.slug} className="border-b border-[var(--border)] hover:bg-[var(--bg-base)]"><td className="px-4 py-3 text-[var(--text-primary)]">{t.name || t.slug}</td><td className="px-4 py-3">{t.domains || 0}</td><td className="px-4 py-3">{t.mailboxes || 0}</td><td className="px-4 py-3">{t.storage || "—"}</td><td className="px-4 py-3"><span className="text-xs px-2 py-0.5 rounded-full bg-[var(--accent)]/10 text-[var(--accent)]">{t.plan || "Free"}</span></td><td className="px-4 py-3"><span className={`w-2 h-2 rounded-full inline-block mr-1 ${(t.health || 100) > 80 ? "bg-[var(--status-success)]" : "bg-[var(--accent-yellow)]"}`} />{t.health || "—"}</td></tr>)}</tbody></table></div>
      )}

      {tab === "export" && (
        <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
          {["traffic","delivery","tenants"].map((t) => <div key={t} className="rounded-xl border border-[var(--border)] bg-[var(--bg-surface)] p-5 text-center">
            <FileText size={32} className="text-[var(--text-muted)] mx-auto mb-3" />
            <h3 className="text-sm font-medium text-[var(--text-primary)] capitalize">{t} Report</h3>
            <div className="flex gap-2 justify-center mt-4">
              <button onClick={() => handleExport(t, "csv")} className="bg-[var(--accent)] text-[var(--bg-base)] rounded-lg px-4 py-2 text-sm"><Download size={14} className="inline mr-1" />CSV</button>
              <button onClick={() => handleExport(t, "pdf")} className="border border-[var(--border)] text-[var(--text-secondary)] rounded-lg px-4 py-2 text-sm"><Download size={14} className="inline mr-1" />PDF</button>
            </div>
          </div>)}
        </div>
      )}
    </div>
  );
}
