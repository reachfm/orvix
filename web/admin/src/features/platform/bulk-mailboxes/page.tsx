import { useState } from "react";
import { Loader2, AlertCircle, CheckCircle2, XCircle } from "lucide-react";
import TenantScopeBanner from "../tenant-context/components/TenantScopeBanner";
import { useTenantScope } from "../tenant-context/queries";
import { usePlatformMailboxes } from "../mailboxes/queries";
import { useBulkMailboxStatusMutation } from "./mutations";
import ConfirmDialog from "../../../components/ConfirmDialog";
import PaginationControls from "../components/PaginationControls";
import { mailboxStatusLabel } from "../mailboxes/formatters";
import { BULK_MAILBOX_MAX, impactSummary, type BulkMailboxAction, type BulkMailboxResult } from "./contract";
import { safeErrorInfo } from "../errors";

const PAGE_SIZE = 50;

const ACTION_META: Record<BulkMailboxAction, { label: string; status: string; irreversible: boolean }> = {
  suspend: { label: "Suspend", status: "suspended", irreversible: false },
  reactivate: { label: "Reactivate", status: "active", irreversible: false },
  delete: { label: "Soft-delete", status: "deleted", irreversible: true },
};

export default function BulkMailboxesPage() {
  const { data: scope } = useTenantScope();
  const tenantId = scope?.tenantId ?? null;
  const [page, setPage] = useState(0);
  const [selected, setSelected] = useState<ReadonlySet<number>>(new Set());
  const [action, setAction] = useState<BulkMailboxAction | "">("");
  const [confirmOpen, setConfirmOpen] = useState(false);
  const [result, setResult] = useState<BulkMailboxResult | null>(null);
  const [mutationError, setMutationError] = useState<unknown>(null);

  const listQ = usePlatformMailboxes(tenantId, { limit: PAGE_SIZE, offset: page * PAGE_SIZE });
  const mailboxes = listQ.data?.mailboxes ?? [];
  const total = listQ.data?.total ?? 0;
  const bulkMut = useBulkMailboxStatusMutation(tenantId);

  const toggle = (id: number) => {
    setSelected((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  };

  const overMax = selected.size > BULK_MAILBOX_MAX;
  const preview = action
    ? impactSummary({
        ids: [...selected],
        action,
        tenantId: tenantId ?? 0,
        affectedStatus: ACTION_META[action].status,
        irreversible: ACTION_META[action].irreversible,
      })
    : "";

  return (
    <div className="space-y-6">
      <div>
        <h2 className="text-xl font-semibold text-[var(--text-primary)]">Bulk Mailbox Operations</h2>
        <p className="text-sm text-[var(--text-secondary)]">
          Apply one status transition to many mailboxes of an explicit tenant through the production bulk endpoint
          (POST /platform/mailboxes/:tenant_id/bulk/status). Selection is capped at {BULK_MAILBOX_MAX} per call; large
          provisioning continues to use Imports.
        </p>
      </div>

      <TenantScopeBanner />

      {tenantId === null ? (
        <div className="border border-[var(--border)] rounded-lg p-6 bg-[var(--bg-surface)]">
          <p className="text-sm font-medium text-[var(--text-primary)]">Select a tenant</p>
          <p className="text-sm text-[var(--text-secondary)] mt-1">
            Bulk mailbox routes require an explicit target tenant id in the path.
          </p>
        </div>
      ) : (
        <>
          <div className="flex flex-wrap items-center gap-3 bg-[var(--bg-surface)] border border-[var(--border)] rounded-lg p-4">
            <label className="text-sm text-[var(--text-secondary)]">
              Action
              <select
                value={action}
                onChange={(e) => setAction(e.target.value as BulkMailboxAction)}
                aria-label="Bulk action"
                className="ml-2 px-3 py-1.5 bg-[var(--bg-elevated)] border border-[var(--border)] rounded text-sm text-[var(--text-primary)]"
              >
                <option value="">— Choose action —</option>
                {Object.entries(ACTION_META).map(([key, meta]) => (
                  <option key={key} value={key}>{meta.label}</option>
                ))}
              </select>
            </label>
            <span className="text-sm text-[var(--text-secondary)]">
              {selected.size} selected{overMax ? ` (max ${BULK_MAILBOX_MAX})` : ""}
            </span>
            <button
              type="button"
              disabled={selected.size === 0 || !action || overMax || bulkMut.isPending}
              onClick={() => setConfirmOpen(true)}
              className="px-3 py-1.5 text-sm rounded bg-[var(--accent)] text-white disabled:opacity-40"
            >
              {bulkMut.isPending ? "Applying…" : `Apply ${ACTION_META[action as BulkMailboxAction]?.label ?? "action"}`}
            </button>
            {selected.size > 0 && (
              <button
                type="button"
                onClick={() => setSelected(new Set())}
                className="text-sm text-[var(--text-secondary)] hover:text-[var(--text-primary)]"
              >
                Clear selection
              </button>
            )}
          </div>

          {preview && !confirmOpen && (
            <p className="text-sm text-[var(--text-secondary)] border border-[var(--border)] rounded-lg p-3 bg-[var(--bg-surface)]">
              {preview}
            </p>
          )}

          {listQ.isLoading ? (
            <div className="flex items-center justify-center h-48">
              <Loader2 size={24} className="text-[var(--accent)] animate-spin" />
            </div>
          ) : listQ.error ? (
            <div className="bg-[var(--bg-surface)] border border-[var(--danger)]/30 rounded-xl p-6 flex items-start gap-3" role="alert">
              <AlertCircle size={20} className="text-[var(--danger)] shrink-0" />
              <div>
                <p className="text-[var(--danger)] text-sm font-medium">{safeErrorInfo(listQ.error).title}</p>
                <p className="text-xs text-[var(--text-secondary)] mt-0.5">{safeErrorInfo(listQ.error).detail}</p>
              </div>
            </div>
          ) : mailboxes.length === 0 ? (
            <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-xl p-8 text-center text-[var(--text-secondary)] text-sm">
              No mailboxes for tenant {tenantId}.
            </div>
          ) : (
            <>
              <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-lg overflow-hidden">
                <div className="overflow-x-auto">
                  <table className="w-full text-sm" aria-label="Bulk mailbox selection">
                    <thead>
                      <tr className="border-b border-[var(--border)] text-left text-[var(--text-secondary)]">
                        <th className="p-3 w-10">
                          <span className="sr-only">Select</span>
                        </th>
                        <th className="p-3">Email</th>
                        <th className="p-3">Status</th>
                        <th className="p-3">Quota</th>
                      </tr>
                    </thead>
                    <tbody>
                      {mailboxes.map((m) => (
                        <tr key={m.id} className="border-b border-[var(--bg-subtle)]">
                          <td className="p-3">
                            <input
                              type="checkbox"
                              aria-label={`Select mailbox ${m.id}`}
                              checked={selected.has(m.id)}
                              onChange={() => toggle(m.id)}
                              disabled={selected.size >= BULK_MAILBOX_MAX && !selected.has(m.id)}
                            />
                          </td>
                          <td className="p-3 text-[var(--text-primary)]">{m.email}</td>
                          <td className="p-3 text-[var(--text-secondary)]">{mailboxStatusLabel(m.status)}</td>
                          <td className="p-3 text-[var(--text-secondary)]">{m.quota_mb > 0 ? `${m.quota_mb} MB` : "unlimited"}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              </div>
              <PaginationControls page={page} pageSize={PAGE_SIZE} total={total} onChange={(p) => { setPage(p); setSelected(new Set()); }} />
            </>
          )}

          {mutationError && (
            <div className="border border-[var(--danger)]/30 rounded-lg p-3 text-sm" role="alert">
              <p className="text-[var(--danger)] font-medium">{safeErrorInfo(mutationError).title}</p>
              <p className="text-xs text-[var(--text-secondary)] mt-0.5">{safeErrorInfo(mutationError).detail}</p>
            </div>
          )}

          {result && (
            <div className="border border-[var(--border)] rounded-lg p-4 bg-[var(--bg-surface)]" role="status">
              <div className="flex items-center gap-2 text-sm font-medium text-[var(--text-primary)]">
                {result.failed && result.failed.length > 0 ? (
                  <XCircle size={18} className="text-[var(--warning)]" />
                ) : (
                  <CheckCircle2 size={18} className="text-[var(--success)]" />
                )}
                Bulk result: {result.succeeded}/{result.total} succeeded
              </div>
              <p className="text-xs text-[var(--text-secondary)] mt-1">
                The backend reports per-row outcomes; partial results are possible when individual mailboxes cannot be
                transitioned.
              </p>
              {result.failed && result.failed.length > 0 && (
                <ul className="mt-2 text-xs text-[var(--text-secondary)] list-disc pl-4">
                  {result.failed.map((f) => (
                    <li key={f.id}>Mailbox #{f.id}: {f.error}</li>
                  ))}
                </ul>
              )}
              <button
                type="button"
                onClick={() => setResult(null)}
                className="mt-2 text-xs text-[var(--text-secondary)] hover:text-[var(--text-primary)]"
              >
                Dismiss
              </button>
            </div>
          )}

          <ConfirmDialog
            open={confirmOpen}
            onOpenChange={(o) => !o && setConfirmOpen(false)}
            title={`${ACTION_META[action as BulkMailboxAction]?.label ?? "Apply"} ${selected.size} mailbox(es)`}
            description={preview}
            requireTypedName={ACTION_META[action as BulkMailboxAction]?.irreversible ? "DELETE" : undefined}
            confirmLabel="Apply to selection"
            danger={ACTION_META[action as BulkMailboxAction]?.irreversible}
            pending={bulkMut.isPending}
            onConfirm={() => {
              if (!action) return;
              bulkMut.mutate(
                { ids: [...selected], action },
                {
                  onSuccess: (res) => { setResult(res); setConfirmOpen(false); setSelected(new Set()); },
                  onError: (e) => { setMutationError(e); setConfirmOpen(false); },
                },
              );
            }}
          />
        </>
      )}
    </div>
  );
}
