import { useState, useEffect } from "react";
import { HardDrive, CheckCircle2, XCircle, Clock, Loader2, AlertCircle } from "lucide-react";

interface Backup {
  id: string;
  name: string;
  status: string;
  sizeBytes: number;
  sha256: string;
  createdAt: string;
  completedAt: string;
}

function formatBytes(bytes: number): string {
  if (bytes === 0) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  const i = Math.floor(Math.log(bytes) / Math.log(1024));
  return `${(bytes / Math.pow(1024, i)).toFixed(1)} ${units[i]}`;
}

function formatDate(dateStr: string): string {
  if (!dateStr) return "—";
  return new Date(dateStr).toLocaleString();
}

export default function BackupStatus() {
  const [backups, setBackups] = useState<Backup[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    fetch("/api/v1/admin/backups")
      .then((res) => {
        if (!res.ok) throw new Error(`HTTP ${res.status}`);
        return res.json();
      })
      .then((json) => {
        setBackups(Array.isArray(json) ? json : json.backups ?? []);
        setLoading(false);
      })
      .catch((err) => {
        setError(err.message);
        setLoading(false);
      });
  }, []);

  if (loading) {
    return (
      <div className="flex items-center justify-center h-64">
        <Loader2 size={24} className="text-[var(--accent)] animate-spin" />
      </div>
    );
  }

  if (error) {
    return (
      <div className="bg-[var(--bg-surface)] border border-[var(--danger)]/30 rounded-xl p-6 flex items-center gap-3">
        <AlertCircle size={20} className="text-[var(--danger)]" />
        <span className="text-[var(--danger)] text-sm">Failed to load backups: {error}</span>
      </div>
    );
  }

  return (
    <div>
      <h2 className="text-2xl font-semibold mb-6 text-[var(--text-primary)] flex items-center gap-2">
        <HardDrive size={24} className="text-[var(--accent)]" />
        Backups
      </h2>

      {backups.length === 0 ? (
        <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-xl p-8 text-center">
          <HardDrive size={32} className="text-[var(--text-muted)] mx-auto mb-3" />
          <p className="text-[var(--text-secondary)] text-sm">No backups found</p>
        </div>
      ) : (
        <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-xl overflow-hidden">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-[var(--border)]">
                <th className="text-left p-4 text-[var(--text-secondary)] font-medium">Name</th>
                <th className="text-left p-4 text-[var(--text-secondary)] font-medium">Status</th>
                <th className="text-left p-4 text-[var(--text-secondary)] font-medium">Size</th>
                <th className="text-left p-4 text-[var(--text-secondary)] font-medium">Created</th>
                <th className="text-left p-4 text-[var(--text-secondary)] font-medium">Completed</th>
              </tr>
            </thead>
            <tbody>
              {backups.map((b) => (
                <tr key={b.id} className="border-b border-[var(--border)] hover:bg-[var(--bg-elevated)]">
                  <td className="p-4 text-[var(--text-primary)] font-medium max-w-[200px] truncate">
                    {b.name}
                  </td>
                  <td className="p-4">
                    {b.status === "completed" || b.status === "verified" ? (
                      <span className="inline-flex items-center gap-1 px-2 py-1 text-xs rounded-full bg-[var(--success)]/10 text-[var(--success)]">
                        <CheckCircle2 size={12} />
                        {b.status}
                      </span>
                    ) : b.status === "failed" ? (
                      <span className="inline-flex items-center gap-1 px-2 py-1 text-xs rounded-full bg-[var(--danger)]/10 text-[var(--danger)]">
                        <XCircle size={12} />
                        {b.status}
                      </span>
                    ) : (
                      <span className="inline-flex items-center gap-1 px-2 py-1 text-xs rounded-full bg-[var(--warning)]/10 text-[var(--warning)]">
                        <Clock size={12} />
                        {b.status}
                      </span>
                    )}
                  </td>
                  <td className="p-4 text-[var(--text-primary)]">{formatBytes(b.sizeBytes)}</td>
                  <td className="p-4 text-[var(--text-secondary)]">{formatDate(b.createdAt)}</td>
                  <td className="p-4 text-[var(--text-secondary)]">{formatDate(b.completedAt)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}
