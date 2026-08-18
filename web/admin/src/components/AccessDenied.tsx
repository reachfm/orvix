import { ShieldX } from "lucide-react";
import { ApiError } from "../api";

/**
 * Renders a polished access-denied state for genuine authorization
 * denials (403 with the backend's stable "insufficient permissions"
 * contract). Any OTHER error is rendered as a normal error message —
 * a friendly denial screen must never mask a real backend defect.
 */
export function isAccessDenied(err: unknown): boolean {
  if (!err) return false;
  if (err instanceof ApiError) {
    if (err.status === 403) return true;
  }
  const msg = String((err as any)?.message || "").toLowerCase();
  return msg.includes("insufficient permissions") || msg.includes("forbidden");
}

export default function AccessDenied({ message = "You don't have permission to view this page." }: { message?: string }) {
  return (
    <div className="flex flex-col items-center justify-center py-16 text-center">
      <ShieldX className="w-10 h-10 text-[var(--text-muted)] mb-3" />
      <h3 className="text-base font-semibold text-[var(--text-primary)] mb-1">Access Denied</h3>
      <p className="text-sm text-[var(--text-secondary)] max-w-sm">{message}</p>
    </div>
  );
}

/**
 * Renders either the polished AccessDenied state (for genuine 403
 * authorization denials) or the raw backend error message.
 */
export function ErrorOrAccessDenied({ err }: { err: unknown }) {
  if (isAccessDenied(err)) {
    return <AccessDenied />;
  }
  return <p className="text-[var(--danger)] text-sm">{(err as Error)?.message || "An error occurred"}</p>;
}
