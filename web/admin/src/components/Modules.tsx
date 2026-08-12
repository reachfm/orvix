import { useQuery } from "@tanstack/react-query";
import { api } from "../api";

interface ModuleInfo {
  id: string;
  version: string;
  status: string;
}

export default function Modules() {
  const { data, isLoading, error } = useQuery<ModuleInfo[]>({ queryKey: ["modules"], queryFn: api.listModules });

  if (isLoading) return <p className="text-[var(--text-secondary)]">Loading...</p>;
  if (error) return <p className="text-[var(--danger)]">Failed to load modules: {(error as Error).message}</p>;

  const modules = data || [];

  return (
    <div>
      <h2 className="text-2xl font-semibold mb-6 text-[var(--text-primary)]">Modules</h2>

      {modules.length === 0 ? (
        <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-xl p-8 text-center text-[var(--text-secondary)]">
          No modules registered.
        </div>
      ) : (
        <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-xl overflow-hidden">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-[var(--border)]">
                <th className="text-left p-4 text-[var(--text-secondary)] font-medium">Module</th>
                <th className="text-left p-4 text-[var(--text-secondary)] font-medium">Version</th>
                <th className="text-left p-4 text-[var(--text-secondary)] font-medium">Status</th>
              </tr>
            </thead>
            <tbody>
              {modules.map((m) => (
                <tr key={m.id} className="border-b border-[var(--border)] hover:bg-[var(--bg-elevated)]">
                  <td className="p-4 text-[var(--text-primary)]">{m.id}</td>
                  <td className="p-4 text-[var(--text-secondary)]">v{m.version}</td>
                  <td className="p-4">
                    <span className="px-2 py-1 text-xs rounded-full bg-[var(--success)]/10 text-[var(--success)]">{m.status}</span>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}
