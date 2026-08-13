import { useState } from "react";
import * as Dialog from "@radix-ui/react-dialog";
import { X, Loader2, AlertCircle, Users } from "lucide-react";
import TenantScopeBanner from "../tenant-context/components/TenantScopeBanner";
import { useTenantScope } from "../tenant-context/queries";
import { usePlatformGroupMembers, usePlatformGroups } from "./queries";
import PaginationControls from "../components/PaginationControls";
import { safeErrorInfo } from "../errors";

const PAGE_SIZE = 25;

function GroupMembersDrawer({ tenantId, groupId, onClose }: { tenantId: number; groupId: number; onClose: () => void }) {
  const { data: members, isLoading, isError, error } = usePlatformGroupMembers(tenantId, groupId);
  return (
    <Dialog.Root open onOpenChange={(o) => !o && onClose()}>
      <Dialog.Portal>
        <Dialog.Overlay className="fixed inset-0 bg-black/60 z-40" />
        <Dialog.Content className="fixed right-0 top-0 h-full w-full max-w-md bg-[var(--bg-surface)] border-l border-[var(--border)] z-50 overflow-y-auto p-6">
          <div className="flex items-start justify-between mb-4">
            <Dialog.Title className="text-lg font-semibold text-[var(--text-primary)]">Group members</Dialog.Title>
            <Dialog.Close className="text-[var(--text-secondary)] hover:text-[var(--text-primary)]">
              <X size={20} />
            </Dialog.Close>
          </div>
          {isLoading ? (
            <div className="flex items-center justify-center h-40">
              <Loader2 size={24} className="text-[var(--accent)] animate-spin" />
            </div>
          ) : isError ? (
            <div className="border border-[var(--danger)]/30 rounded-xl p-6" role="alert">
              <p className="text-[var(--danger)] text-sm font-medium">{safeErrorInfo(error).title}</p>
              <p className="text-xs text-[var(--text-secondary)] mt-0.5">{safeErrorInfo(error).detail}</p>
            </div>
          ) : members && members.members.length === 0 ? (
            <p className="text-sm text-[var(--text-secondary)]">This group has no members.</p>
          ) : (
            <ul className="divide-y divide-[var(--bg-subtle)]" aria-label="Group member addresses">
              {(members?.members ?? []).map((m) => (
                <li key={m} className="py-2 text-sm text-[var(--text-primary)]">{m}</li>
              ))}
            </ul>
          )}
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}

export default function GroupsPage() {
  const { data: scope } = useTenantScope();
  const tenantId = scope?.tenantId ?? null;
  const [query, setQuery] = useState("");
  const [page, setPage] = useState(0);
  const [membersGroupId, setMembersGroupId] = useState<number | null>(null);

  const listQ = usePlatformGroups(tenantId, { q: query || undefined, limit: PAGE_SIZE, offset: page * PAGE_SIZE });
  const groups = listQ.data?.groups ?? [];
  const total = listQ.data?.total ?? 0;

  return (
    <div className="space-y-6">
      <div>
        <h2 className="text-xl font-semibold text-[var(--text-primary)]">Platform Groups</h2>
        <p className="text-sm text-[var(--text-secondary)]">
          Platform-wide group inventory per tenant with membership views. Group management mutations are not exposed on
          the platform route family — this page is inventory and membership read-only.
        </p>
      </div>

      <TenantScopeBanner />

      {tenantId === null ? (
        <div className="border border-[var(--border)] rounded-lg p-6 bg-[var(--bg-surface)]">
          <p className="text-sm font-medium text-[var(--text-primary)]">Select a tenant</p>
          <p className="text-sm text-[var(--text-secondary)] mt-1">
            Platform group routes require an explicit target tenant id in the path.
          </p>
        </div>
      ) : (
        <>
          <input
            value={query}
            onChange={(e) => { setQuery(e.target.value); setPage(0); }}
            placeholder="Search group name or address…"
            aria-label="Search groups"
            className="flex-1 max-w-sm px-3 py-2 bg-[var(--bg-elevated)] border border-[var(--border)] rounded text-sm text-[var(--text-primary)]"
          />

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
          ) : groups.length === 0 ? (
            <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-xl p-8 text-center text-[var(--text-secondary)] text-sm">
              {query ? "No groups match this search." : `No groups for tenant ${tenantId}.`}
            </div>
          ) : (
            <>
              <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-lg overflow-hidden">
                <div className="overflow-x-auto">
                  <table className="w-full text-sm" aria-label="Platform groups">
                    <thead>
                      <tr className="border-b border-[var(--border)] text-left text-[var(--text-secondary)]">
                        <th className="p-3">Name</th>
                        <th className="p-3">Description</th>
                        <th className="p-3">Members</th>
                        <th className="p-3">Updated</th>
                        <th className="p-3 w-16"><span className="sr-only">Actions</span></th>
                      </tr>
                    </thead>
                    <tbody>
                      {groups.map((g) => (
                        <tr key={g.id} className="border-b border-[var(--bg-subtle)]">
                          <td className="p-3 text-[var(--text-primary)] font-medium">{g.name}</td>
                          <td className="p-3 text-[var(--text-secondary)]">{g.description || "—"}</td>
                          <td className="p-3 text-[var(--text-secondary)]">{g.member_count}</td>
                          <td className="p-3 text-[var(--text-secondary)]">{new Date(g.updated_at).toLocaleString()}</td>
                          <td className="p-3">
                            <button
                              type="button"
                              aria-label={`View members of ${g.name}`}
                              onClick={() => setMembersGroupId(g.id)}
                              className="inline-flex items-center gap-1.5 px-2.5 py-1.5 text-xs rounded border border-[var(--border)] text-[var(--text-secondary)] hover:text-[var(--text-primary)]"
                            >
                              <Users size={14} /> Members
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

      {tenantId !== null && membersGroupId !== null && (
        <GroupMembersDrawer tenantId={tenantId} groupId={membersGroupId} onClose={() => setMembersGroupId(null)} />
      )}
    </div>
  );
}
