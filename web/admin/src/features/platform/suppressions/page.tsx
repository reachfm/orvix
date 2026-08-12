import { useState } from "react";
import * as Dialog from "@radix-ui/react-dialog";
import { X, Loader2, AlertCircle, Plus, History } from "lucide-react";
import TenantScopeBanner from "../tenant-context/components/TenantScopeBanner";
import { useTenantScope } from "../tenant-context/queries";
import { usePlatformSuppression, usePlatformSuppressionHistory, usePlatformSuppressions } from "./queries";
import { useAddSuppressionMutation, useDeleteSuppressionMutation, useReactivateSuppressionMutation, useReleaseSuppressionMutation } from "./mutations";
import PaginationControls from "../components/PaginationControls";
import StatusBadge from "../components/StatusBadge";
import ConfirmDialog from "../../../components/ConfirmDialog";
import {
  SUPPRESSION_REASONS,
  SUPPRESSION_STATES,
  suppressionReleaseConfirmation,
  type AddSuppressionRequest,
  type Suppression,
  type SuppressionReason,
} from "./contract";
import { formatTimestamp, suppressionImpact, suppressionReasonLabel, suppressionStateLabel, suppressionStateTone } from "./formatters";
import { safeErrorInfo } from "../errors";

const PAGE_SIZE = 50;

function AddSuppressionDialog({ tenantId, onClose }: { tenantId: number; onClose: () => void }) {
  const addMut = useAddSuppressionMutation(tenantId);
  const [address, setAddress] = useState("");
  const [reason, setReason] = useState<SuppressionReason>("manual");
  const [notes, setNotes] = useState("");
  const [expiry, setExpiry] = useState("");
  const [error, setError] = useState<unknown>(null);

  const submit = () => {
    const body: AddSuppressionRequest = { address: address.trim().toLowerCase(), reason, notes: notes || undefined };
    if (expiry) body.expires_at = new Date(expiry).toISOString();
    addMut.mutate(body, { onSuccess: () => onClose(), onError: (e) => setError(e) });
  };

  return (
    <Dialog.Root open onOpenChange={(o) => !o && onClose()}>
      <Dialog.Portal>
        <Dialog.Overlay className="fixed inset-0 bg-black/60 z-40" />
        <Dialog.Content className="fixed left-1/2 top-1/2 -translate-x-1/2 -translate-y-1/2 z-50 w-full max-w-md bg-[var(--bg-surface)] border border-[var(--border)] rounded-xl p-6">
          <Dialog.Title className="text-base font-semibold text-[var(--text-primary)] mb-4">Add suppression</Dialog.Title>
          <div className="space-y-3">
            <div className="border border-[var(--warning)]/40 rounded-lg p-3 text-xs bg-[var(--warning)]/5" role="alert">
              An active suppression blocks outbound delivery to this address. The address is normalized to lowercase.
            </div>
            <label className="block text-sm text-[var(--text-secondary)]">
              Address
              <input
                value={address}
                onChange={(e) => setAddress(e.target.value)}
                placeholder="recipient@example.com"
                className="mt-1 w-full px-3 py-2 bg-[var(--bg-elevated)] border border-[var(--border)] rounded text-sm text-[var(--text-primary)]"
              />
            </label>
            <label className="block text-sm text-[var(--text-secondary)]">
              Reason
              <select
                value={reason}
                onChange={(e) => setReason(e.target.value as SuppressionReason)}
                className="mt-1 w-full px-3 py-2 bg-[var(--bg-elevated)] border border-[var(--border)] rounded text-sm text-[var(--text-primary)]"
              >
                {SUPPRESSION_REASONS.map((r) => (
                  <option key={r} value={r}>{suppressionReasonLabel(r)}</option>
                ))}
              </select>
            </label>
            <label className="block text-sm text-[var(--text-secondary)]">
              Expires (optional)
              <input
                type="datetime-local"
                value={expiry}
                onChange={(e) => setExpiry(e.target.value)}
                className="mt-1 w-full px-3 py-2 bg-[var(--bg-elevated)] border border-[var(--border)] rounded text-sm text-[var(--text-primary)]"
              />
            </label>
            <label className="block text-sm text-[var(--text-secondary)]">
              Notes
              <textarea
                value={notes}
                onChange={(e) => setNotes(e.target.value)}
                rows={2}
                className="mt-1 w-full px-3 py-2 bg-[var(--bg-elevated)] border border-[var(--border)] rounded text-sm text-[var(--text-primary)]"
              />
            </label>
            {error !== null && (
              <div className="border border-[var(--danger)]/30 rounded-lg p-3 text-sm" role="alert">
                <p className="text-[var(--danger)] font-medium">{safeErrorInfo(error).title}</p>
                <p className="text-xs text-[var(--text-secondary)] mt-0.5">{safeErrorInfo(error).detail}</p>
              </div>
            )}
            <div className="flex justify-end gap-2">
              <Dialog.Close className="px-3 py-2 text-sm text-[var(--text-secondary)] hover:text-[var(--text-primary)]">Cancel</Dialog.Close>
              <button
                type="button"
                disabled={!address.trim() || addMut.isPending}
                onClick={submit}
                className="px-3 py-2 text-sm rounded bg-[var(--accent)] text-white disabled:opacity-40"
              >
                {addMut.isPending ? "Adding…" : "Add suppression"}
              </button>
            </div>
          </div>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}

function SuppressionDetailDrawer({ tenantId, id, onClose }: { tenantId: number; id: number; onClose: () => void }) {
  const { data: record, isLoading } = usePlatformSuppression(tenantId, id);
  const { data: history, isLoading: historyLoading } = usePlatformSuppressionHistory(tenantId, id);
  const releaseMut = useReleaseSuppressionMutation(tenantId);
  const reactivateMut = useReactivateSuppressionMutation(tenantId);
  const deleteMut = useDeleteSuppressionMutation(tenantId);
  const [confirmRelease, setConfirmRelease] = useState(false);
  const [error, setError] = useState<unknown>(null);

  return (
    <Dialog.Root open onOpenChange={(o) => !o && onClose()}>
      <Dialog.Portal>
        <Dialog.Overlay className="fixed inset-0 bg-black/60 z-40" />
        <Dialog.Content className="fixed right-0 top-0 h-full w-full max-w-xl bg-[var(--bg-surface)] border-l border-[var(--border)] z-50 overflow-y-auto p-6">
          <div className="flex items-start justify-between mb-4">
            <Dialog.Title className="text-lg font-semibold text-[var(--text-primary)]">
              {record ? record.address : "Suppression detail"}
            </Dialog.Title>
            <Dialog.Close className="text-[var(--text-secondary)] hover:text-[var(--text-primary)]">
              <X size={20} />
            </Dialog.Close>
          </div>

          {isLoading ? (
            <div className="flex items-center justify-center h-40">
              <Loader2 size={24} className="text-[var(--accent)] animate-spin" />
            </div>
          ) : !record ? (
            <div className="border border-[var(--danger)]/30 rounded-xl p-6" role="alert">
              <p className="text-[var(--danger)] text-sm font-medium">Failed to load suppression</p>
            </div>
          ) : (
            <div className="space-y-6">
              <dl className="grid grid-cols-2 gap-4">
                <div>
                  <dt className="text-xs font-medium text-[var(--text-secondary)]">Address</dt>
                  <dd className="text-sm text-[var(--text-primary)] mt-0.5">{record.address}</dd>
                </div>
                <div>
                  <dt className="text-xs font-medium text-[var(--text-secondary)]">State</dt>
                  <dd className="mt-0.5">
                    <StatusBadge tone={suppressionStateTone(record.state)} label={`State ${suppressionStateLabel(record.state)}`}>
                      {suppressionStateLabel(record.state)}
                    </StatusBadge>
                  </dd>
                </div>
                <div>
                  <dt className="text-xs font-medium text-[var(--text-secondary)]">Reason</dt>
                  <dd className="text-sm text-[var(--text-primary)] mt-0.5">{suppressionReasonLabel(record.reason)}</dd>
                </div>
                <div>
                  <dt className="text-xs font-medium text-[var(--text-secondary)]">Source</dt>
                  <dd className="text-sm text-[var(--text-primary)] mt-0.5">{record.source}</dd>
                </div>
                <div>
                  <dt className="text-xs font-medium text-[var(--text-secondary)]">Created</dt>
                  <dd className="text-sm text-[var(--text-primary)] mt-0.5">{formatTimestamp(record.created_at)}</dd>
                </div>
                <div>
                  <dt className="text-xs font-medium text-[var(--text-secondary)]">Expires</dt>
                  <dd className="text-sm text-[var(--text-primary)] mt-0.5">{formatTimestamp(record.expires_at)}</dd>
                </div>
                <div>
                  <dt className="text-xs font-medium text-[var(--text-secondary)]">Released</dt>
                  <dd className="text-sm text-[var(--text-primary)] mt-0.5">{formatTimestamp(record.released_at)}</dd>
                </div>
                <div>
                  <dt className="text-xs font-medium text-[var(--text-secondary)]">Version</dt>
                  <dd className="text-sm text-[var(--text-primary)] mt-0.5">#{record.version}</dd>
                </div>
              </dl>

              <p className="text-sm text-[var(--text-secondary)] border border-[var(--border)] rounded-lg p-3">
                {suppressionImpact(record.state)}
              </p>

              <div className="flex flex-wrap gap-2">
                {record.state === "active" && (
                  <>
                    <button
                      type="button"
                      onClick={() => setConfirmRelease(true)}
                      className="px-3 py-2 text-sm rounded border border-[var(--border)] text-[var(--text-secondary)] hover:text-[var(--text-primary)]"
                    >
                      Release suppression
                    </button>
                    <button
                      type="button"
                      onClick={() =>
                        releaseMut.mutate({ id: record.id, reason: "operator release" }, { onError: (e) => setError(e) })
                      }
                      className="px-3 py-2 text-sm rounded border border-[var(--border)] text-[var(--text-secondary)] hover:text-[var(--text-primary)]"
                    >
                      {releaseMut.isPending ? "Releasing…" : "Release (no reason)"}
                    </button>
                  </>
                )}
                {(record.state === "released" || record.state === "expired") && (
                  <button
                    type="button"
                    onClick={() =>
                      reactivateMut.mutate({ id: record.id, body: { reason: record.reason } }, { onError: (e) => setError(e) })
                    }
                    className="px-3 py-2 text-sm rounded bg-[var(--accent)] text-white disabled:opacity-40"
                  >
                    {reactivateMut.isPending ? "Reactivating…" : "Reactivate"}
                  </button>
                )}
              </div>

              {error !== null && (
                <div className="border border-[var(--danger)]/30 rounded-lg p-3 text-sm" role="alert">
                  <p className="text-[var(--danger)] font-medium">{safeErrorInfo(error).title}</p>
                  <p className="text-xs text-[var(--text-secondary)] mt-0.5">{safeErrorInfo(error).detail}</p>
                </div>
              )}

              <section aria-label="Suppression history">
                <h3 className="flex items-center gap-2 text-sm font-semibold text-[var(--text-primary)] mb-2">
                  <History size={16} /> Lifecycle history
                </h3>
                {historyLoading ? (
                  <p className="text-xs text-[var(--text-muted)]">Loading history…</p>
                ) : !history || history.events.length === 0 ? (
                  <p className="text-xs text-[var(--text-muted)]">No lifecycle events recorded.</p>
                ) : (
                  <ul className="divide-y divide-[var(--bg-subtle)] border border-[var(--border)] rounded-lg">
                    {history.events.map((ev) => (
                      <li key={ev.id} className="px-3 py-2 text-sm">
                        <span className="text-[var(--text-primary)]">{ev.event}</span>
                        <span className="text-xs text-[var(--text-muted)] ml-2">{formatTimestamp(ev.at)}</span>
                        {ev.reason && <span className="block text-xs text-[var(--text-secondary)]">{ev.reason}</span>}
                      </li>
                    ))}
                  </ul>
                )}
              </section>
            </div>
          )}

          <ConfirmDialog
            open={confirmRelease}
            onOpenChange={(o) => !o && setConfirmRelease(false)}
            title="Release suppression"
            description={`Release suppression for ${record?.address ?? ""}? Delivery resumes immediately; the lifecycle history is retained.`}
            requireTypedName={record ? suppressionReleaseConfirmation(record.id) : undefined}
            confirmLabel="Release suppression"
            danger
            pending={deleteMut.isPending}
            onConfirm={() => {
              if (!record) return;
              deleteMut.mutate(
                { id: record.id, confirmation: suppressionReleaseConfirmation(record.id) },
                { onSuccess: () => { setConfirmRelease(false); onClose(); }, onError: (e) => setError(e) },
              );
            }}
          />
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}

export default function SuppressionsPage() {
  const { data: scope } = useTenantScope();
  const tenantId = scope?.tenantId ?? null;
  const [q, setQ] = useState("");
  const [reason, setReason] = useState("");
  const [state, setState] = useState("");
  const [page, setPage] = useState(0);
  const [selectedId, setSelectedId] = useState<number | null>(null);
  const [createOpen, setCreateOpen] = useState(false);

  const listQ = usePlatformSuppressions(tenantId, {
    q: q || undefined,
    reason: (reason || undefined) as SuppressionReason | undefined,
    state: (state || undefined) as Suppression["state"] | undefined,
    limit: PAGE_SIZE,
    offset: page * PAGE_SIZE,
  });

  const suppressions = listQ.data?.suppressions ?? [];
  const total = listQ.data?.total ?? 0;

  return (
    <div className="space-y-6">
      <div>
        <h2 className="text-xl font-semibold text-[var(--text-primary)]">Suppression Management</h2>
        <p className="text-sm text-[var(--text-secondary)]">
          Platform-wide suppression lifecycle per tenant. An active suppression blocks outbound delivery; release and
          expiry restore it while retaining history.
        </p>
      </div>

      <TenantScopeBanner />

      {tenantId === null ? (
        <div className="border border-[var(--border)] rounded-lg p-6 bg-[var(--bg-surface)]">
          <p className="text-sm font-medium text-[var(--text-primary)]">Select a tenant</p>
          <p className="text-sm text-[var(--text-secondary)] mt-1">
            Platform suppression routes require an explicit target tenant id in the path.
          </p>
        </div>
      ) : (
        <>
          <div className="flex flex-wrap items-center gap-2">
            <input
              value={q}
              onChange={(e) => { setQ(e.target.value); setPage(0); }}
              placeholder="Search address…"
              aria-label="Search suppressions"
              className="flex-1 max-w-sm px-3 py-2 bg-[var(--bg-elevated)] border border-[var(--border)] rounded text-sm text-[var(--text-primary)]"
            />
            <label className="text-sm text-[var(--text-secondary)]">
              Reason
              <select value={reason} onChange={(e) => { setReason(e.target.value); setPage(0); }} className="ml-2 px-3 py-2 bg-[var(--bg-elevated)] border border-[var(--border)] rounded text-sm text-[var(--text-primary)]">
                <option value="">All</option>
                {SUPPRESSION_REASONS.map((r) => (
                  <option key={r} value={r}>{suppressionReasonLabel(r)}</option>
                ))}
              </select>
            </label>
            <label className="text-sm text-[var(--text-secondary)]">
              State
              <select value={state} onChange={(e) => { setState(e.target.value); setPage(0); }} className="ml-2 px-3 py-2 bg-[var(--bg-elevated)] border border-[var(--border)] rounded text-sm text-[var(--text-primary)]">
                <option value="">All</option>
                {SUPPRESSION_STATES.map((s) => (
                  <option key={s} value={s}>{suppressionStateLabel(s)}</option>
                ))}
              </select>
            </label>
            <button
              type="button"
              onClick={() => setCreateOpen(true)}
              className="inline-flex items-center gap-2 px-3 py-2 text-sm rounded bg-[var(--accent)] text-white"
            >
              <Plus size={16} /> Add suppression
            </button>
          </div>

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
          ) : suppressions.length === 0 ? (
            <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-xl p-8 text-center text-[var(--text-secondary)] text-sm">
              {q || reason || state ? "No suppressions match the current filters." : `No suppressions for tenant ${tenantId}.`}
            </div>
          ) : (
            <>
              <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-lg overflow-hidden">
                <div className="overflow-x-auto">
                  <table className="w-full text-sm" aria-label="Platform suppressions">
                    <thead>
                      <tr className="border-b border-[var(--border)] text-left text-[var(--text-secondary)]">
                        <th className="p-3">Address</th>
                        <th className="p-3">Reason</th>
                        <th className="p-3">Source</th>
                        <th className="p-3">State</th>
                        <th className="p-3">Expires</th>
                        <th className="p-3">Created</th>
                      </tr>
                    </thead>
                    <tbody>
                      {suppressions.map((s) => (
                        <tr key={s.id} className="border-b border-[var(--bg-subtle)] hover:bg-[var(--bg-elevated)] cursor-pointer" onClick={() => setSelectedId(s.id)}>
                          <td className="p-3 text-[var(--text-primary)] font-medium">{s.address}</td>
                          <td className="p-3 text-[var(--text-secondary)]">{suppressionReasonLabel(s.reason)}</td>
                          <td className="p-3 text-[var(--text-secondary)]">{s.source}</td>
                          <td className="p-3">
                            <StatusBadge tone={suppressionStateTone(s.state)} label={`State ${suppressionStateLabel(s.state)}`}>
                              {suppressionStateLabel(s.state)}
                            </StatusBadge>
                          </td>
                          <td className="p-3 text-[var(--text-secondary)]">{formatTimestamp(s.expires_at)}</td>
                          <td className="p-3 text-[var(--text-secondary)]">{formatTimestamp(s.created_at)}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              </div>
              <PaginationControls page={page} pageSize={PAGE_SIZE} total={total} onChange={setPage} />
            </>
          )}
        </>
      )}

      {tenantId !== null && createOpen && <AddSuppressionDialog tenantId={tenantId} onClose={() => setCreateOpen(false)} />}

      {tenantId !== null && selectedId !== null && (
        <SuppressionDetailDrawer tenantId={tenantId} id={selectedId} onClose={() => setSelectedId(null)} />
      )}
    </div>
  );
}
