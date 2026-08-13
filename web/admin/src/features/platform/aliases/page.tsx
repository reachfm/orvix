import { useState } from "react";
import * as Dialog from "@radix-ui/react-dialog";
import { X, Loader2, AlertCircle, Plus, Trash2 } from "lucide-react";
import TenantScopeBanner from "../tenant-context/components/TenantScopeBanner";
import { useTenantScope } from "../tenant-context/queries";
import { usePlatformAliases } from "./queries";
import { useCreatePlatformAliasMutation, useDeletePlatformAliasMutation } from "./mutations";
import { usePlatformDomains } from "../domains/queries";
import ConfirmDialog from "../../../components/ConfirmDialog";
import PaginationControls from "../components/PaginationControls";
import StatusBadge from "../components/StatusBadge";
import { safeErrorInfo } from "../errors";

const PAGE_SIZE = 25;

function AliasCreateDialog({ tenantId, onClose }: { tenantId: number; onClose: () => void }) {
  const createMut = useCreatePlatformAliasMutation(tenantId);
  const { data: domains } = usePlatformDomains(tenantId, { limit: 200, offset: 0 });
  const [domainId, setDomainId] = useState("");
  const [fromAddr, setFromAddr] = useState("");
  const [toAddr, setToAddr] = useState("");
  const [error, setError] = useState<unknown>(null);

  const domainOptions = (domains?.domains ?? []).filter((d) => d.status === "active");

  return (
    <Dialog.Root open onOpenChange={(o) => !o && onClose()}>
      <Dialog.Portal>
        <Dialog.Overlay className="fixed inset-0 bg-black/60 z-40" />
        <Dialog.Content className="fixed left-1/2 top-1/2 -translate-x-1/2 -translate-y-1/2 z-50 w-full max-w-md bg-[var(--bg-surface)] border border-[var(--border)] rounded-xl p-6">
          <Dialog.Title className="text-base font-semibold text-[var(--text-primary)] mb-4">Create alias</Dialog.Title>
          <div className="space-y-3">
            <label className="block text-sm text-[var(--text-secondary)]">
              Domain
              <select
                value={domainId}
                onChange={(e) => setDomainId(e.target.value)}
                className="mt-1 w-full px-3 py-2 bg-[var(--bg-elevated)] border border-[var(--border)] rounded text-sm text-[var(--text-primary)]"
              >
                <option value="">— Choose domain —</option>
                {domainOptions.map((d) => (
                  <option key={d.id} value={String(d.id)}>{d.name}</option>
                ))}
              </select>
            </label>
            <label className="block text-sm text-[var(--text-secondary)]">
              Source address (alias)
              <input
                value={fromAddr}
                onChange={(e) => setFromAddr(e.target.value)}
                placeholder="sales@example.com"
                className="mt-1 w-full px-3 py-2 bg-[var(--bg-elevated)] border border-[var(--border)] rounded text-sm text-[var(--text-primary)]"
              />
            </label>
            <label className="block text-sm text-[var(--text-secondary)]">
              Destination address
              <input
                value={toAddr}
                onChange={(e) => setToAddr(e.target.value)}
                placeholder="team@example.com"
                className="mt-1 w-full px-3 py-2 bg-[var(--bg-elevated)] border border-[var(--border)] rounded text-sm text-[var(--text-primary)]"
              />
            </label>
            <p className="text-xs text-[var(--text-muted)]">
              The backend enforces tenant scoping: a destination outside this tenant is rejected.
            </p>
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
                disabled={!domainId || !fromAddr.trim() || !toAddr.trim() || createMut.isPending}
                onClick={() =>
                  createMut.mutate(
                    { domain_id: Number(domainId), from_addr: fromAddr.trim(), to_addr: toAddr.trim() },
                    { onSuccess: () => onClose(), onError: (e) => setError(e) },
                  )
                }
                className="px-3 py-2 text-sm rounded bg-[var(--accent)] text-white disabled:opacity-40"
              >
                {createMut.isPending ? "Creating…" : "Create alias"}
              </button>
            </div>
          </div>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}

export default function AliasesPage() {
  const { data: scope } = useTenantScope();
  const tenantId = scope?.tenantId ?? null;
  const [query, setQuery] = useState("");
  const [page, setPage] = useState(0);
  const [createOpen, setCreateOpen] = useState(false);
  const [deleteTarget, setDeleteTarget] = useState<{ id: number; from: string } | null>(null);

  const listQ = usePlatformAliases(tenantId, { q: query || undefined, limit: PAGE_SIZE, offset: page * PAGE_SIZE });
  const deleteMut = useDeletePlatformAliasMutation(tenantId);

  const aliases = listQ.data?.aliases ?? [];
  const total = listQ.data?.total ?? 0;

  return (
    <div className="space-y-6">
      <div>
        <h2 className="text-xl font-semibold text-[var(--text-primary)]">Platform Aliases</h2>
        <p className="text-sm text-[var(--text-secondary)]">
          Platform-wide alias inventory per tenant. Aliases forward from a source address to a destination within the
          same tenant.
        </p>
      </div>

      <TenantScopeBanner />

      {tenantId === null ? (
        <div className="border border-[var(--border)] rounded-lg p-6 bg-[var(--bg-surface)]">
          <p className="text-sm font-medium text-[var(--text-primary)]">Select a tenant</p>
          <p className="text-sm text-[var(--text-secondary)] mt-1">
            Platform alias routes require an explicit target tenant id in the path.
          </p>
        </div>
      ) : (
        <>
          <div className="flex flex-wrap items-center gap-2">
            <input
              value={query}
              onChange={(e) => { setQuery(e.target.value); setPage(0); }}
              placeholder="Search source or destination…"
              aria-label="Search aliases"
              className="flex-1 max-w-sm px-3 py-2 bg-[var(--bg-elevated)] border border-[var(--border)] rounded text-sm text-[var(--text-primary)]"
            />
            <button
              type="button"
              onClick={() => setCreateOpen(true)}
              className="inline-flex items-center gap-2 px-3 py-2 text-sm rounded bg-[var(--accent)] text-white"
            >
              <Plus size={16} /> Create alias
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
          ) : aliases.length === 0 ? (
            <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-xl p-8 text-center text-[var(--text-secondary)] text-sm">
              {query ? "No aliases match this search." : `No aliases for tenant ${tenantId}.`}
            </div>
          ) : (
            <>
              <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-lg overflow-hidden">
                <div className="overflow-x-auto">
                  <table className="w-full text-sm" aria-label="Platform aliases">
                    <thead>
                      <tr className="border-b border-[var(--border)] text-left text-[var(--text-secondary)]">
                        <th className="p-3">Source</th>
                        <th className="p-3">Destination</th>
                        <th className="p-3">State</th>
                        <th className="p-3">Updated</th>
                        <th className="p-3 w-16"><span className="sr-only">Actions</span></th>
                      </tr>
                    </thead>
                    <tbody>
                      {aliases.map((a) => (
                        <tr key={a.id} className="border-b border-[var(--bg-subtle)]">
                          <td className="p-3 text-[var(--text-primary)] font-medium">{a.from_addr}</td>
                          <td className="p-3 text-[var(--text-secondary)]">{a.to_addr}</td>
                          <td className="p-3">
                            <StatusBadge tone={a.active ? "success" : "neutral"} label={a.active ? "Enabled" : "Disabled"}>
                              {a.active ? "enabled" : "disabled"}
                            </StatusBadge>
                          </td>
                          <td className="p-3 text-[var(--text-secondary)]">{new Date(a.updated_at).toLocaleString()}</td>
                          <td className="p-3">
                            <button
                              type="button"
                              aria-label={`Delete alias ${a.from_addr}`}
                              onClick={() => setDeleteTarget({ id: a.id, from: a.from_addr })}
                              className="p-1.5 text-[var(--text-secondary)] hover:text-[var(--danger)]"
                            >
                              <Trash2 size={16} />
                            </button>
                          </td>
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

      {tenantId !== null && createOpen && <AliasCreateDialog tenantId={tenantId} onClose={() => setCreateOpen(false)} />}

      <ConfirmDialog
        open={deleteTarget !== null}
        onOpenChange={(o) => !o && setDeleteTarget(null)}
        title="Delete alias"
        description={`Delete alias ${deleteTarget?.from ?? ""}? Forwarding stops immediately.`}
        requireTypedName={deleteTarget?.from}
        confirmLabel="Delete alias"
        danger
        pending={deleteMut.isPending}
        onConfirm={() => {
          if (!deleteTarget) return;
          deleteMut.mutate(deleteTarget.id, { onSuccess: () => setDeleteTarget(null) });
        }}
      />
    </div>
  );
}
