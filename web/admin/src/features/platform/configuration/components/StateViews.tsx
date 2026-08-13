import { Loader2, AlertCircle } from "lucide-react";

export function Loading() {
  return <div className="flex items-center justify-center h-32" role="status"><Loader2 size={20} className="text-[var(--accent)] animate-spin" /></div>;
}
export function ErrorBox({ error }: { error: unknown }) {
  return (
    <div className="bg-[var(--bg-surface)] border border-[var(--danger)]/30 rounded-xl p-4 flex items-center gap-3" role="alert">
      <AlertCircle size={18} className="text-[var(--danger)]" />
      <span className="text-[var(--danger)] text-sm">{(error as Error)?.message || "Failed to load"}</span>
    </div>
  );
}
export function Empty({ text }: { text: string }) {
  return <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-xl p-6 text-center text-[var(--text-secondary)] text-sm">{text}</div>;
}
