import { useState } from "react";
import {
  useMonitoringAlertsQuery, useMonitoringCapacityQuery, useMonitoringSnapshotQuery,
  useMonitoringProvidersQuery, useAlertDeliveriesQuery,
} from "../queries";
import { useResolveAlertMutation } from "../mutations";
import { Loading, ErrorBox, Empty } from "./StateViews";
import type { Capacity, ComponentHealth } from "../contract";

type Sub = "alerts" | "capacity" | "snapshot" | "providers" | "deliveries";

function HealthPill({ h }: { h: ComponentHealth }) {
  const color = h.status === "ok" ? "text-[var(--success)]" : h.status === "warning" ? "text-[var(--warning)]" : "text-[var(--danger)]";
  return <span className={color}>{h.status}{h.message ? ` — ${h.message}` : ""}</span>;
}

function CapacityGrid({ c }: { c: Capacity }) {
  const rows: [string, number][] = [
    ["Domains", c.domainCount], ["Mailboxes", c.mailboxCount], ["Messages", c.messageCount],
    ["Attachments", c.attachmentCount], ["Queue", c.queueCount], ["Queue dead-letter", c.queueDeadLetter],
    ["Storage bytes", c.storageBytes], ["Database size", c.databaseSize], ["Backups", c.backupCount], ["Backup bytes", c.backupBytes],
  ];
  return (
    <div className="grid grid-cols-2 md:grid-cols-5 gap-3">
      {rows.map(([label, v]) => (
        <div key={label} className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-xl p-3">
          <p className="text-xs text-[var(--text-secondary)] mb-1">{label}</p>
          <p className="text-lg font-bold text-[var(--text-primary)]">{v}</p>
        </div>
      ))}
    </div>
  );
}

export default function MonitoringPanel() {
  const [sub, setSub] = useState<Sub>("alerts");
  const alertsQ = useMonitoringAlertsQuery(sub === "alerts");
  const capacityQ = useMonitoringCapacityQuery(sub === "capacity");
  const snapshotQ = useMonitoringSnapshotQuery(sub === "snapshot");
  const providersQ = useMonitoringProvidersQuery(sub === "providers");
  const deliveriesQ = useAlertDeliveriesQuery(sub === "deliveries");
  const resolveMut = useResolveAlertMutation();

  const alerts = alertsQ.data?.alerts ?? [];

  return (
    <div>
      <div className="flex gap-1 mb-4">
        {(["alerts", "capacity", "snapshot", "providers", "deliveries"] as const).map((s) => (
          <button key={s} onClick={() => setSub(s)} className={`px-3 py-1.5 text-xs rounded capitalize ${sub === s ? "bg-[var(--bg-subtle)] text-[var(--text-primary)]" : "text-[var(--text-secondary)]"}`}>{s}</button>
        ))}
        <a href="/api/v1/metrics" target="_blank" rel="noreferrer" className="ml-auto px-3 py-1.5 text-xs text-[var(--accent)] hover:underline">Open raw metrics</a>
      </div>

      {sub === "alerts" && (alertsQ.isLoading ? <Loading /> : alertsQ.error ? <ErrorBox error={alertsQ.error} /> : alerts.length === 0 ? <Empty text="No active alerts" /> : (
        <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-xl overflow-hidden">
          <table className="w-full text-sm"><thead><tr className="border-b border-[var(--border)]"><th className="text-left p-3 text-[var(--text-secondary)]">Alert</th><th className="text-left p-3 text-[var(--text-secondary)]">Severity</th><th className="text-right p-3 text-[var(--text-secondary)]">Action</th></tr></thead>
          <tbody>{alerts.map((a) => (
            <tr key={a.id} className="border-b border-[var(--border)]"><td className="p-3 text-[var(--text-primary)]">{a.title}</td><td className="p-3 text-[var(--text-secondary)]">{a.severity}</td>
              <td className="p-3 text-right"><button disabled={resolveMut.isPending} onClick={() => resolveMut.mutate(a.id)} className="text-xs text-[var(--accent)] hover:underline disabled:opacity-50">Resolve</button></td></tr>
          ))}</tbody></table>
        </div>
      ))}

      {sub === "capacity" && (capacityQ.isLoading ? <Loading /> : capacityQ.error ? <ErrorBox error={capacityQ.error} /> : capacityQ.data ? <CapacityGrid c={capacityQ.data} /> : null)}

      {sub === "snapshot" && (snapshotQ.isLoading ? <Loading /> : snapshotQ.error ? <ErrorBox error={snapshotQ.error} /> : snapshotQ.data ? (
        <div className="space-y-3">
          <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-xl p-4 text-sm space-y-1">
            <p className="text-[var(--text-secondary)]">Service: <HealthPill h={{ status: snapshotQ.data.serviceStatus, message: "" }} /></p>
            <p className="text-[var(--text-secondary)]">Database: <HealthPill h={snapshotQ.data.dbHealth} /></p>
            <p className="text-[var(--text-secondary)]">Queue: <HealthPill h={snapshotQ.data.queueHealth} /></p>
            <p className="text-[var(--text-secondary)]">Backup: <HealthPill h={snapshotQ.data.backupHealth} /></p>
            <p className="text-[var(--text-secondary)]">API: <HealthPill h={snapshotQ.data.apiHealth} /></p>
            <p className="text-[var(--text-secondary)]">DNS readiness: <HealthPill h={snapshotQ.data.dnsReadiness} /></p>
            <p className="text-[var(--text-secondary)]">Certificate expiry: <span className="text-[var(--text-primary)]">{snapshotQ.data.certExpiry.status}</span> ({snapshotQ.data.certExpiry.expiringWithin7} within 7d, {snapshotQ.data.certExpiry.expiringWithin30} within 30d)</p>
            <p className="text-[var(--text-secondary)]">Open alerts: <span className="text-[var(--text-primary)]">{snapshotQ.data.openAlerts}</span></p>
          </div>
          <CapacityGrid c={snapshotQ.data.capacity} />
        </div>
      ) : null)}

      {sub === "providers" && (providersQ.isLoading ? <Loading /> : providersQ.error ? <ErrorBox error={providersQ.error} /> : (!providersQ.data?.providers.length) ? <Empty text="No alert-delivery providers configured" /> : (
        <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-xl overflow-hidden">
          <table className="w-full text-sm"><thead><tr className="border-b border-[var(--border)]"><th className="text-left p-3 text-[var(--text-secondary)]">Provider</th><th className="text-left p-3 text-[var(--text-secondary)]">Enabled</th><th className="text-left p-3 text-[var(--text-secondary)]">Secret configured</th></tr></thead>
          <tbody>{providersQ.data.providers.map((p) => (
            <tr key={p.name} className="border-b border-[var(--border)]"><td className="p-3 text-[var(--text-primary)]">{p.name}</td><td className="p-3 text-[var(--text-secondary)]">{p.enabled ? "yes" : "no"}</td><td className="p-3 text-[var(--text-secondary)]">{p.hasSecret ? "yes" : "no"}</td></tr>
          ))}</tbody></table>
        </div>
      ))}

      {sub === "deliveries" && (deliveriesQ.isLoading ? <Loading /> : deliveriesQ.error ? <ErrorBox error={deliveriesQ.error} /> : (!deliveriesQ.data?.deliveries.length) ? <Empty text={deliveriesQ.data?.honest_note || "No alert deliveries"} /> : (
        <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-xl overflow-hidden"><table className="w-full text-sm"><tbody>{deliveriesQ.data.deliveries.map((d) => (
          <tr key={d.id} className="border-b border-[var(--border)]"><td className="p-3 text-[var(--text-primary)]">{d.provider}</td><td className="p-3 text-[var(--text-secondary)]">{d.status}</td><td className="p-3 text-[var(--text-muted)]">{d.alertTitle}</td></tr>
        ))}</tbody></table></div>
      ))}
    </div>
  );
}
