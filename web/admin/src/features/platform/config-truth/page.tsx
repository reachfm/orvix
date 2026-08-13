import { useState } from "react";
import { useSettings, useMutateSetting, useCapabilities } from "./queries";
import type { Setting } from "./contract";

function SecretValue() {
  return <span className="text-[var(--text-muted)]">•••••••• (redacted)</span>;
}

function SettingRow({ setting }: { setting: Setting }) {
  const mutate = useMutateSetting();
  const [value, setValue] = useState("");
  const [feedback, setFeedback] = useState<{ kind: "ok" | "error"; message: string } | null>(null);

  const save = () => {
    setFeedback(null);
    mutate.mutate(
      { key: setting.key, data: { value, version: setting.version } },
      {
        onSuccess: () => { setValue(""); setFeedback({ kind: "ok", message: "Setting updated (pending restart if required)." }); },
        onError: (e) => setFeedback({ kind: "error", message: e instanceof Error ? e.message : "Update failed." }),
      },
    );
  };

  const renderValue = (v: unknown) => (setting.secret ? <SecretValue /> : <span className="text-[var(--text-primary)] font-mono text-xs">{String(v ?? "")}</span>);

  return (
    <tr className="border-b border-[var(--bg-subtle)]">
      <td className="p-3 text-[var(--text-primary)] font-mono text-xs">{setting.key}</td>
      <td className="p-3 text-[var(--text-secondary)]">{setting.section}</td>
      <td className="p-3">{renderValue(setting.effective_value)}</td>
      <td className="p-3 text-[var(--text-secondary)]">{setting.source}</td>
      <td className="p-3 text-[var(--text-secondary)]">{setting.immutable ? "immutable" : "mutable"}</td>
      <td className="p-3 text-[var(--text-secondary)]">{setting.restart_required ? "restart required" : "live"}</td>
      <td className="p-3">
        {!setting.immutable && !setting.secret ? (
          <div className="flex items-center gap-2">
            <input value={value} onChange={(e) => setValue(e.target.value)} placeholder="New value" className="px-2 py-1 text-xs bg-[var(--bg-base)] border border-[var(--border)] rounded" />
            <button onClick={save} disabled={mutate.isPending} className="px-2 py-1 rounded text-xs bg-[var(--accent)] text-white disabled:opacity-50">Save</button>
          </div>
        ) : setting.secret ? (
          <span className="text-xs text-[var(--text-muted)]">Secret — not editable here</span>
        ) : null}
        {feedback && <span className={`block mt-1 text-xs ${feedback.kind === "ok" ? "text-[var(--success)]" : "text-[var(--danger)]"}`}>{feedback.message}</span>}
      </td>
    </tr>
  );
}

export function CapabilitiesPanel() {
  const { data, isLoading } = useCapabilities();
  return (
    <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-lg overflow-hidden">
      <h3 className="text-sm font-semibold text-[var(--text-primary)] p-4 border-b border-[var(--border)]">Runtime capabilities</h3>
      {isLoading ? (
        <p className="p-4 text-sm text-[var(--text-muted)]">Loading…</p>
      ) : !data || data.capabilities.length === 0 ? (
        <p className="p-4 text-sm text-[var(--text-muted)]">No capability registry entries.</p>
      ) : (
        <table className="w-full text-sm">
          <thead>
            <tr className="border-b border-[var(--border)] text-left text-[var(--text-secondary)]">
              <th className="p-3">Capability</th><th className="p-3">Availability</th><th className="p-3">Reason</th><th className="p-3">Version</th>
            </tr>
          </thead>
          <tbody>
            {data.capabilities.map((c) => (
              <tr key={c.id} className="border-b border-[var(--bg-subtle)]">
                <td className="p-3 text-[var(--text-primary)]">{c.name}</td>
                <td className="p-3">
                  <span className={`px-2 py-0.5 rounded text-xs ${
                    c.availability === "enabled" ? "text-[var(--success)] bg-[var(--success)]/10"
                      : c.availability === "degraded" ? "text-[var(--warning)] bg-[var(--warning)]/10"
                        : c.availability === "disabled" ? "text-[var(--text-secondary)] bg-[var(--bg-subtle)]"
                          : "text-[var(--danger)] bg-[var(--danger)]/10"
                  }`}>{c.availability}</span>
                </td>
                <td className="p-3 text-[var(--text-secondary)]">{c.reason || "—"}</td>
                <td className="p-3 text-[var(--text-secondary)]">{c.version || "—"}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </div>
  );
}

export default function ConfigTruthPage() {
  const { data, isLoading } = useSettings();

  return (
    <div className="space-y-6">
      <div>
        <h2 className="text-xl font-semibold text-[var(--text-primary)]">Configuration Truth</h2>
        <p className="text-sm text-[var(--text-secondary)]">Authoritative settings with source, mutability, and restart state. Secrets are redacted.</p>
      </div>

      {isLoading ? (
        <p className="text-sm text-[var(--text-muted)]">Loading…</p>
      ) : !data || data.settings.length === 0 ? (
        <p className="text-sm text-[var(--text-muted)]">No configuration settings registered.</p>
      ) : (
        <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-lg overflow-hidden">
          <div className="max-h-[36rem] overflow-auto">
            <table className="w-full text-sm">
              <thead className="sticky top-0 bg-[var(--bg-surface)]">
                <tr className="border-b border-[var(--border)] text-left text-[var(--text-secondary)]">
                  <th className="p-3">Key</th><th className="p-3">Section</th><th className="p-3">Effective</th><th className="p-3">Source</th><th className="p-3">Mutability</th><th className="p-3">Restart</th><th className="p-3">Actions</th>
                </tr>
              </thead>
              <tbody>
                {data.settings.map((s) => <SettingRow key={s.key} setting={s} />)}
              </tbody>
            </table>
          </div>
        </div>
      )}

      <CapabilitiesPanel />
    </div>
  );
}
