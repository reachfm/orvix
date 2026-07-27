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
  if (!dateStr) return "â€”";
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
        <Loader2 size={24} className="divide-[var(--border)] animate-spin" />
      </div>
    );
  }

  if (error) {
    return (
      <div className="bg-[var(--bg-shell)] border border-[#F87171]/30 rounded-xl p-6 flex items-center gap-3">
        <AlertCircle size={20} className="text-[var(--status-success)]" />
        <span className="text-[var(--status-success)] text-sm">Failed to load backups: {error}</span>
      </div>
    );
  }

  return (
    <div>
      <h2 className="text-2xl font-semibold mb-6 text-[var(--text-secondary)] flex items-center gap-2">
        <HardDrive size={24} className="divide-[var(--border)]" />
        Backups
      </h2>

      {backups.length === 0 ? (
        <div className="bg-[var(--bg-shell)] border border-[var(--border)] rounded-xl p-8 text-center">
          <HardDrive size={32} className="text-[var(--text-primary)] mx-auto mb-3" />
          <p className="text-[var(--text-muted)] text-sm">No backups found</p>
        </div>
      ) : (
        <div className="bg-[var(--bg-shell)] border border-[var(--border)] rounded-xl overflow-hidden">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-[var(--border)]">
                <th className="text-left p-4 text-[var(--text-muted)] font-medium">Name</th>
                <th className="text-left p-4 text-[var(--text-muted)] font-medium">Status</th>
                <th className="text-left p-4 text-[var(--text-muted)] font-medium">Size</th>
                <th className="text-left p-4 text-[var(--text-muted)] font-medium">Created</th>
                <th className="text-left p-4 text-[var(--text-muted)] font-medium">Completed</th>
              </tr>
            </thead>
            <tbody>
              {backups.map((b) => (
                <tr key={b.id} className="border-b border-[var(--border)] hover:bg-[var(--bg-surface)]">
                  <td className="p-4 text-[var(--text-secondary)] font-medium max-w-[200px] truncate">
                    {b.name}
                  </td>
                  <td className="p-4">
                    {b.status === "completed" || b.status === "verified" ? (
                      <span className="inline-flex items-center gap-1 px-2 py-1 text-xs rounded-full bg-[#34D399]/10 text-[var(--text-muted)]">
                        <CheckCircle2 size={12} />
                        {b.status}
                      </span>
                    ) : b.status === "failed" ? (
                      <span className="inline-flex items-center gap-1 px-2 py-1 text-xs rounded-full bg-[#F87171]/10 text-[var(--status-success)]">
                        <XCircle size={12} />
                        {b.status}
                      </span>
                    ) : (
                      <span className="inline-flex items-center gap-1 px-2 py-1 text-xs rounded-full bg-[#FBBF24]/10 bg-[var(--accent-blue)]/10">
                        <Clock size={12} />
                        {b.status}
                      </span>
                    )}
                  </td>
                  <td className="p-4 text-[var(--text-secondary)]">{formatBytes(b.sizeBytes)}</td>
                  <td className="p-4 text-[var(--text-muted)]">{formatDate(b.createdAt)}</td>
                  <td className="p-4 text-[var(--text-muted)]">{formatDate(b.completedAt)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}
