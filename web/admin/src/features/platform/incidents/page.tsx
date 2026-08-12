import { useState } from "react";
import { useIncidents, useIncidentDetail, useIncidentTimeline, useCreateIncident, useUpdateIncident } from "./queries";
import { SEVERITIES, INCIDENT_STATUSES, type Incident, type Severity, type IncidentStatus } from "./contract";

function SeverityBadge({ severity }: { severity: string }) {
  const tone =
    severity === "critical"
      ? "text-[var(--danger)] bg-[var(--danger)]/10"
      : severity === "major"
        ? "text-[var(--warning)] bg-[var(--warning)]/10"
        : "text-[var(--text-secondary)] bg-[var(--bg-subtle)]";
  return <span className={`px-2 py-0.5 rounded text-xs ${tone}`}>{severity}</span>;
}

function IncidentDetail({ incident, onClose }: { incident: Incident; onClose: () => void }) {
  const { data: timeline } = useIncidentTimeline(incident.id);
  const update = useUpdateIncident(incident.id);
  const [status, setStatus] = useState<IncidentStatus>(incident.status);
  const [message, setMessage] = useState("");
  const [feedback, setFeedback] = useState<{ kind: "ok" | "error"; message: string } | null>(null);

  const submitUpdate = () => {
    if (message.trim() === "") { setFeedback({ kind: "error", message: "A timeline message is required." }); return; }
    setFeedback(null);
    update.mutate({ status, message: message.trim() }, {
      onSuccess: () => { setMessage(""); setFeedback({ kind: "ok", message: "Incident updated." }); },
      onError: (e) => setFeedback({ kind: "error", message: e instanceof Error ? e.message : "Update failed." }),
    });
  };

  return (
    <div className="border border-[var(--border)] rounded-lg p-4 bg-[var(--bg-surface)] space-y-4">
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-2">
          <h3 className="text-sm font-semibold text-[var(--text-primary)]">#{incident.id} — {incident.title}</h3>
          <SeverityBadge severity={incident.severity} />
        </div>
        <button onClick={onClose} className="text-sm text-[var(--text-secondary)] hover:underline">Close</button>
      </div>
      <p className="text-sm text-[var(--text-secondary)]">{incident.description || "No description."}</p>
      <div className="grid grid-cols-2 md:grid-cols-4 gap-2 text-sm">
        <div><span className="text-[var(--text-secondary)]">Status</span><br /><span className="text-[var(--text-primary)]">{incident.status}</span></div>
        <div><span className="text-[var(--text-secondary)]">Services</span><br /><span className="text-[var(--text-primary)]">{(incident.services ?? []).join(", ") || "—"}</span></div>
        <div><span className="text-[var(--text-secondary)]">Regions</span><br /><span className="text-[var(--text-primary)]">{(incident.regions ?? []).join(", ") || "—"}</span></div>
        <div><span className="text-[var(--text-secondary)]">Created</span><br /><span className="text-[var(--text-primary)]">{new Date(incident.created_at).toLocaleString()}</span></div>
      </div>

      <div className="flex flex-wrap gap-2 items-end">
        <label className="block text-sm text-[var(--text-secondary)]">
          Status
          <select value={status} onChange={(e) => setStatus(e.target.value as IncidentStatus)} className="mt-1 px-3 py-2 bg-[var(--bg-base)] border border-[var(--border)] rounded text-sm">
            {INCIDENT_STATUSES.map((s) => <option key={s} value={s}>{s}</option>)}
          </select>
        </label>
        <input value={message} onChange={(e) => setMessage(e.target.value)} placeholder="Timeline message (required)" className="flex-1 min-w-48 px-3 py-2 bg-[var(--bg-base)] border border-[var(--border)] rounded text-sm" />
        <button onClick={submitUpdate} disabled={update.isPending} className="px-4 py-2 bg-[var(--accent)] text-white rounded text-sm disabled:opacity-50">Update</button>
      </div>
      {feedback && <p className={`text-sm ${feedback.kind === "ok" ? "text-[var(--success)]" : "text-[var(--danger)]"}`}>{feedback.message}</p>}

      <div>
        <h4 className="text-sm font-semibold text-[var(--text-primary)] mb-2">Timeline</h4>
        {!timeline || timeline.length === 0 ? (
          <p className="text-sm text-[var(--text-muted)]">No timeline events.</p>
        ) : (
          <ul className="space-y-2">
            {timeline.map((ev) => (
              <li key={ev.id} className="text-sm border-l-2 border-[var(--border)] pl-3">
                <span className="text-[var(--text-primary)]">{ev.message}</span>
                <span className="block text-xs text-[var(--text-secondary)]">{ev.operator} · {new Date(ev.created_at).toLocaleString()}{ev.status ? ` · ${ev.status}` : ""}</span>
              </li>
            ))}
          </ul>
        )}
      </div>
    </div>
  );
}

export default function IncidentsPage() {
  const { data, isLoading } = useIncidents();
  const create = useCreateIncident();
  const [selectedId, setSelectedId] = useState<number | null>(null);
  const [title, setTitle] = useState("");
  const [description, setDescription] = useState("");
  const [severity, setSeverity] = useState<Severity>("minor");
  const [services, setServices] = useState("");
  const [feedback, setFeedback] = useState<{ kind: "ok" | "error"; message: string } | null>(null);
  const { data: selected } = useIncidentDetail(selectedId ?? 0);

  const submit = () => {
    if (title.trim() === "") { setFeedback({ kind: "error", message: "A title is required." }); return; }
    setFeedback(null);
    create.mutate(
      {
        title: title.trim(),
        description: description.trim() || undefined,
        severity,
        services: services.split(",").map((s) => s.trim()).filter(Boolean),
      },
      {
        onSuccess: () => { setTitle(""); setDescription(""); setServices(""); setFeedback({ kind: "ok", message: "Incident created." }); },
        onError: (e) => setFeedback({ kind: "error", message: e instanceof Error ? e.message : "Create failed." }),
      },
    );
  };

  return (
    <div className="space-y-6">
      <div>
        <h2 className="text-xl font-semibold text-[var(--text-primary)]">Incidents</h2>
        <p className="text-sm text-[var(--text-secondary)]">Incident management with public status projection separation.</p>
      </div>

      <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-lg p-4 space-y-3">
        <h3 className="text-sm font-semibold text-[var(--text-primary)]">Create incident</h3>
        <div className="grid grid-cols-1 md:grid-cols-2 gap-3">
          <input value={title} onChange={(e) => setTitle(e.target.value)} placeholder="Title (required)" className="px-3 py-2 bg-[var(--bg-base)] border border-[var(--border)] rounded text-sm" />
          <select value={severity} onChange={(e) => setSeverity(e.target.value as Severity)} className="px-3 py-2 bg-[var(--bg-base)] border border-[var(--border)] rounded text-sm">
            {SEVERITIES.map((s) => <option key={s} value={s}>{s}</option>)}
          </select>
          <input value={description} onChange={(e) => setDescription(e.target.value)} placeholder="Description" className="px-3 py-2 bg-[var(--bg-base)] border border-[var(--border)] rounded text-sm" />
          <input value={services} onChange={(e) => setServices(e.target.value)} placeholder="Services (comma-separated)" className="px-3 py-2 bg-[var(--bg-base)] border border-[var(--border)] rounded text-sm" />
        </div>
        <button onClick={submit} disabled={create.isPending} className="px-4 py-2 bg-[var(--accent)] text-white rounded text-sm disabled:opacity-50">
          {create.isPending ? "Creating…" : "Create Incident"}
        </button>
        {feedback && <p className={`text-sm ${feedback.kind === "ok" ? "text-[var(--success)]" : "text-[var(--danger)]"}`}>{feedback.message}</p>}
      </div>

      {isLoading ? (
        <p className="text-sm text-[var(--text-muted)]">Loading…</p>
      ) : !data || data.incidents.length === 0 ? (
        <p className="text-sm text-[var(--text-muted)]">No incidents found.</p>
      ) : (
        <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-lg overflow-hidden">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-[var(--border)] text-left text-[var(--text-secondary)]">
                <th className="p-3">ID</th><th className="p-3">Title</th><th className="p-3">Severity</th><th className="p-3">Status</th><th className="p-3">Services</th><th className="p-3">Created</th>
              </tr>
            </thead>
            <tbody>
              {data.incidents.map((inc) => (
                <tr key={inc.id} className="border-b border-[var(--bg-subtle)] cursor-pointer hover:bg-[var(--bg-elevated)]" onClick={() => setSelectedId(inc.id)}>
                  <td className="p-3 text-[var(--text-primary)]">{inc.id}</td>
                  <td className="p-3 text-[var(--text-primary)]">{inc.title}</td>
                  <td className="p-3"><SeverityBadge severity={inc.severity} /></td>
                  <td className="p-3 text-[var(--text-secondary)]">{inc.status}</td>
                  <td className="p-3 text-[var(--text-secondary)]">{(inc.services ?? []).join(", ") || "—"}</td>
                  <td className="p-3 text-[var(--text-secondary)]">{new Date(inc.created_at).toLocaleString()}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {selected && <IncidentDetail incident={selected} onClose={() => setSelectedId(null)} />}
    </div>
  );
}
