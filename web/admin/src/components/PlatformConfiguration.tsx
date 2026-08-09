import { useState } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { Settings, Loader2, AlertCircle, Flag } from "lucide-react";
import { api } from "../api";

type Tab = "settings" | "feature-flags";

function Loading() { return <div className="flex items-center justify-center h-32"><Loader2 size={20} className="text-[var(--accent)] animate-spin" /></div>; }
function ErrorBox({ error }: { error: unknown }) {
  return <div className="bg-[var(--bg-surface)] border border-[var(--danger)]/30 rounded-xl p-4 flex items-center gap-3"><AlertCircle size={18} className="text-[var(--danger)]" /><span className="text-[var(--danger)] text-sm">{(error as Error)?.message || "Failed to load"}</span></div>;
}
function Empty({ text }: { text: string }) { return <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-xl p-6 text-center text-[var(--text-secondary)] text-sm">{text}</div>; }

// Settings are shown read-only with a raw key/value editor for the fields
// the backend returns, PATCHed as a single object — no secret fields are
// ever rendered (password/hash/secret/key/token substrings are masked).
function maskIfSecret(key: string, value: unknown): string {
  if (/password|secret|token|private_key|hash|dsn/i.test(key)) return "••••••••";
  return typeof value === "object" ? JSON.stringify(value) : String(value);
}

function SettingsTab() {
  const qc = useQueryClient();
  const q = useQuery<Record<string, unknown>>({ queryKey: ["admin-settings"], queryFn: api.getAdminSettings });
  const [draft, setDraft] = useState<Record<string, string>>({});
  const patchMut = useMutation({
    mutationFn: () => api.patchAdminSettings(draft),
    onSuccess: () => { qc.invalidateQueries({ queryKey: ["admin-settings"] }); setDraft({}); },
  });
  const entries = Object.entries(q.data ?? {}).filter(([k]) => !/password|secret|token|private_key|hash|dsn/i.test(k));

  return q.isLoading ? <Loading /> : q.error ? <ErrorBox error={q.error} /> : (
    <div>
      <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-xl p-4 mb-4">
        {entries.length === 0 ? <p className="text-[var(--text-secondary)] text-sm">No editable settings reported</p> : (
          <dl className="space-y-3">
            {entries.map(([k, v]) => (
              <div key={k} className="flex items-center justify-between gap-4">
                <dt className="text-sm text-[var(--text-secondary)] font-mono">{k}</dt>
                <dd>
                  <input
                    defaultValue={typeof v === "boolean" ? String(v) : maskIfSecret(k, v)}
                    onChange={(e) => setDraft((d) => ({ ...d, [k]: e.target.value }))}
                    className="px-2 py-1 bg-[var(--bg-elevated)] border border-[var(--border)] rounded text-xs text-[var(--text-primary)] w-64 text-right"
                  />
                </dd>
              </div>
            ))}
          </dl>
        )}
      </div>
      <button
        disabled={Object.keys(draft).length === 0 || patchMut.isPending}
        onClick={() => patchMut.mutate()}
        className="px-3 py-1.5 text-xs bg-[var(--accent)] text-white rounded disabled:opacity-40"
      >
        {patchMut.isPending ? "Saving…" : "Save changes"}
      </button>
      {patchMut.isError && <p className="text-[var(--danger)] text-xs mt-2">{(patchMut.error as Error).message}</p>}
      {patchMut.isSuccess && <p className="text-[var(--success)] text-xs mt-2">Saved.</p>}
    </div>
  );
}

function FeatureFlagsTab() {
  const qc = useQueryClient();
  const q = useQuery<any[]>({ queryKey: ["feature-flags"], queryFn: api.listFeatureFlags });
  const toggleMut = useMutation({
    mutationFn: ({ id, enabled }: { id: string; enabled: boolean }) => api.updateFeatureFlag(id, { enabled }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["feature-flags"] }),
  });
  const rows = q.data ?? [];

  return q.isLoading ? <Loading /> : q.error ? <ErrorBox error={q.error} /> : rows.length === 0 ? <Empty text="No feature flags configured" /> : (
    <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-xl overflow-hidden">
      <table className="w-full text-sm">
        <thead><tr className="border-b border-[var(--border)]"><th className="text-left p-3 text-[var(--text-secondary)]">Flag</th><th className="text-left p-3 text-[var(--text-secondary)]">Tier</th><th className="text-right p-3 text-[var(--text-secondary)]">Enabled</th></tr></thead>
        <tbody>{rows.map((f: any) => (
          <tr key={f.id} className="border-b border-[var(--border)]">
            <td className="p-3 text-[var(--text-primary)]">{f.name}</td>
            <td className="p-3 text-[var(--text-secondary)]">{f.tier_required}</td>
            <td className="p-3 text-right">
              <button
                disabled={toggleMut.isPending}
                onClick={() => toggleMut.mutate({ id: f.id, enabled: !f.enabled })}
                className={`px-2 py-1 text-xs rounded ${f.enabled ? "bg-[var(--success)]/10 text-[var(--success)]" : "bg-[var(--border)] text-[var(--text-secondary)]"}`}
              >
                {f.enabled ? "On" : "Off"}
              </button>
            </td>
          </tr>
        ))}</tbody>
      </table>
    </div>
  );
}

export default function PlatformConfiguration() {
  const [tab, setTab] = useState<Tab>("settings");
  return (
    <div>
      <h2 className="text-2xl font-semibold mb-4 text-[var(--text-primary)] flex items-center gap-2"><Settings size={22} className="text-[var(--accent)]" /> Configuration</h2>
      <div className="flex gap-1 mb-6 border-b border-[var(--border)]">
        <button onClick={() => setTab("settings")} className={`px-3 py-2 text-sm border-b-2 ${tab === "settings" ? "border-[var(--accent)] text-[var(--text-primary)]" : "border-transparent text-[var(--text-secondary)]"}`}>Settings</button>
        <button onClick={() => setTab("feature-flags")} className={`flex items-center gap-1 px-3 py-2 text-sm border-b-2 ${tab === "feature-flags" ? "border-[var(--accent)] text-[var(--text-primary)]" : "border-transparent text-[var(--text-secondary)]"}`}><Flag size={12} /> Feature Flags</button>
      </div>
      {tab === "settings" ? <SettingsTab /> : <FeatureFlagsTab />}
    </div>
  );
}
