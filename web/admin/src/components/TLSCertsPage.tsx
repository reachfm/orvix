import { useState } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { Shield, RefreshCw, Trash2, UploadCloud, AlertTriangle, CheckCircle2, Clock } from "lucide-react";
import { api } from "../api";

function SkeletonCard() { return <div className="rounded-xl border border-[var(--border)] bg-[var(--bg-surface)] p-5 animate-pulse"><div className="h-4 bg-[var(--border)] rounded w-1/3 mb-4" /><div className="h-3 bg-[var(--border)] rounded w-2/3 mb-2" /><div className="h-3 bg-[var(--border)] rounded w-1/2" /></div>; }

export default function TLSCertsPage() {
  const queryClient = useQueryClient();
  const [showUpload, setShowUpload] = useState(false);
  const [uploadForm, setUploadForm] = useState({ domain: "", cert_pem: "", key_pem: "" });

  const { data: certs, isLoading } = useQuery({ queryKey: ["tls-certs"], queryFn: () => api.listTLSCerts?.() ?? Promise.resolve([]) });
  const certList: any[] = Array.isArray(certs) ? certs : [];

  const renewMutation = useMutation({ mutationFn: (domain: string) => api.renewTLSCert(domain) ?? Promise.resolve(), onSuccess: () => queryClient.invalidateQueries({ queryKey: ["tls-certs"] }) });
  const deleteMutation = useMutation({ mutationFn: (domain: string) => api.deleteTLSCert(domain) ?? Promise.resolve(), onSuccess: () => queryClient.invalidateQueries({ queryKey: ["tls-certs"] }) });
  const uploadMutation = useMutation({ mutationFn: (d: typeof uploadForm) => api.uploadTLSCert(d.domain, { cert_pem: d.cert_pem, key_pem: d.key_pem }) ?? Promise.resolve(), onSuccess: () => { queryClient.invalidateQueries({ queryKey: ["tls-certs"] }); setShowUpload(false); } });

  const daysRemaining = (until: string) => { const d = new Date(until).getTime() - Date.now(); return Math.max(0, Math.ceil(d / (1000 * 60 * 60 * 24))); };
  const countColor = (days: number) => days < 30 ? "text-[var(--status-danger)]" : days < 60 ? "text-[var(--accent-yellow)]" : "text-[var(--status-success)]";
  const barColor = (days: number) => days < 30 ? "var(--status-danger)" : days < 60 ? "var(--accent-yellow)" : "var(--status-success)";

  const totalCerts = certList.length;
  const expiringSoon = certList.filter((c: any) => daysRemaining(c.valid_until || c.not_after) < 30).length;
  const autoRenew = certList.filter((c: any) => c.auto_renew !== false).length;
  const expired = certList.filter((c: any) => daysRemaining(c.valid_until || c.not_after) === 0).length;

  return (
    <div className="space-y-6">
      <div>
        <p className="text-xs font-semibold uppercase tracking-[0.14em] text-[var(--accent)] mb-1">SSL / TLS Security</p>
        <h1 className="text-2xl font-bold text-[var(--text-primary)]">TLS Certificates</h1>
        <p className="mt-1 text-sm text-[var(--text-secondary)]">Certificate inventory, renewal, and upload</p>
      </div>

      <div className="grid grid-cols-2 lg:grid-cols-4 gap-3">
        {[{ label: "Total Certs", value: totalCerts, color: "var(--accent)" },{ label: "Expiring Soon", value: expiringSoon, color: "var(--accent-yellow)" },{ label: "Auto-Renew", value: autoRenew, color: "var(--status-success)" },{ label: "Expired", value: expired, color: "var(--status-danger)" }].map((s) => <div key={s.label} className="rounded-xl border border-[var(--border)] bg-[var(--bg-surface)] p-4"><p className="text-xs text-[var(--text-muted)]">{s.label}</p><p className="text-2xl font-bold text-[var(--text-primary)] mt-1">{s.value}</p></div>)}
      </div>

      {expiringSoon > 0 && <div className="flex items-center gap-2 rounded-lg border border-[var(--accent-yellow)]/30 bg-[var(--accent-yellow)]/5 p-3 text-sm text-[var(--accent-yellow)]"><AlertTriangle size={16} /> {expiringSoon} certificate{expiringSoon > 1 ? "s" : ""} expiring within 30 days</div>}

      <button onClick={() => setShowUpload(true)} className="inline-flex items-center gap-2 bg-[var(--accent)] text-[var(--bg-base)] rounded-lg px-4 py-2 text-sm"><UploadCloud size={14} /> Upload Certificate</button>

      <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
        {isLoading ? Array.from({ length: 4 }).map((_, i) => <SkeletonCard key={i} />) : certList.map((c: any) => {
          const days = daysRemaining(c.valid_until || c.not_after);
          return (
            <div key={c.id} className="rounded-xl border border-[var(--border)] bg-[var(--bg-surface)] p-5">
              <div className="flex justify-between mb-3">
                <h3 className="text-sm font-semibold text-[var(--text-primary)]">{c.domain || c.name || c.common_name}</h3>
                <div className="flex gap-1">
                  <button onClick={() => renewMutation.mutate(c.domain || c.name || c.common_name)} className="p-1 text-[var(--text-muted)] hover:text-[var(--accent)]" title="Renew"><RefreshCw size={14} /></button>
                  <button onClick={() => deleteMutation.mutate(c.domain || c.name || c.common_name)} className="p-1 text-[var(--text-muted)] hover:text-[var(--status-danger)]" title="Delete"><Trash2 size={14} /></button>
                </div>
              </div>
              <p className="text-xs text-[var(--text-muted)]">Issuer: {c.issuer || c.issuer_name || "—"}</p>
              <p className="text-xs text-[var(--text-muted)]">Valid: {c.valid_from || c.not_before} → {c.valid_until || c.not_after}</p>
              <div className="mt-3">
                <div className="flex justify-between text-xs mb-1"><span className={`font-medium ${countColor(days)}`}>{days} days remaining</span></div>
                <div className="h-2 rounded-full bg-[var(--bg-base)] overflow-hidden"><div className="h-full rounded-full transition-all" style={{ width: `${Math.min(100, Math.max(0, days / 90 * 100))}%`, background: barColor(days) }} /></div>
              </div>
              <div className="flex items-center justify-between mt-3"><span className={`text-xs ${c.status === "valid" ? "text-[var(--status-success)]" : "text-[var(--status-danger)]"}`}>{c.status}</span><span className={`text-xs ${c.auto_renew !== false ? "text-[var(--status-success)]" : "text-[var(--text-muted)]"}`}>{c.auto_renew !== false ? "Auto-renew on" : "Manual"}</span></div>
            </div>
          );
        })}
      </div>

      {showUpload && <div className="fixed inset-0 z-50 bg-black/50 flex items-center justify-center" onClick={() => setShowUpload(false)}><div className="rounded-xl border border-[var(--border)] bg-[var(--bg-surface)] p-6 w-full max-w-lg" onClick={(e) => e.stopPropagation()}><h3 className="text-lg font-semibold mb-4">Upload Certificate</h3><div className="space-y-3"><input placeholder="Domain" value={uploadForm.domain} onChange={(e) => setUploadForm({ ...uploadForm, domain: e.target.value })} className="w-full bg-[var(--bg-base)] border border-[var(--border)] rounded-lg px-3 py-2 text-sm text-[var(--text-primary)]" /><textarea placeholder="PEM Certificate" value={uploadForm.cert_pem} onChange={(e) => setUploadForm({ ...uploadForm, cert_pem: e.target.value })} rows={4} className="w-full bg-[var(--bg-base)] border border-[var(--border)] rounded-lg px-3 py-2 text-sm text-[var(--text-primary)] font-mono text-xs" /><textarea placeholder="Private Key (PEM)" value={uploadForm.key_pem} onChange={(e) => setUploadForm({ ...uploadForm, key_pem: e.target.value })} rows={4} className="w-full bg-[var(--bg-base)] border border-[var(--border)] rounded-lg px-3 py-2 text-sm text-[var(--text-primary)] font-mono text-xs" /></div><div className="flex gap-2 mt-4 justify-end"><button onClick={() => setShowUpload(false)} className="border border-[var(--border)] text-[var(--text-secondary)] rounded-lg px-4 py-2 text-sm">Cancel</button><button onClick={() => uploadMutation.mutate(uploadForm)} disabled={!uploadForm.domain} className="bg-[var(--accent)] text-[var(--bg-base)] rounded-lg px-4 py-2 text-sm">Upload</button></div></div></div>}
    </div>
  );
}
