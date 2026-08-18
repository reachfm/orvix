import { useState } from "react";
import * as Dialog from "@radix-ui/react-dialog";
import { X, Loader2, AlertCircle, Users, Plus, Trash2, UserPlus, UserMinus } from "lucide-react";
import TenantScopeBanner from "../tenant-context/components/TenantScopeBanner";
import { useTenantScope } from "../tenant-context/queries";
import { usePlatformGroupMembers, usePlatformGroups } from "./queries";
import {
  useAddPlatformGroupMemberMutation,
  useCreatePlatformGroupMutation,
  useDeletePlatformGroupMutation,
  useRemovePlatformGroupMemberMutation,
} from "./mutations";
import PaginationControls from "../components/PaginationControls";
import ConfirmDialog from "../../../components/ConfirmDialog";
import { safeErrorInfo } from "../errors";
import { confirmGroupDelete } from "./contract";

const PAGE_SIZE = 25;

function GroupMembersDrawer({ tenantId, groupId, onClose }: { tenantId: number; groupId: number; onClose: () => void }) {
  const { data: members, isLoading, isError, error, refetch } = usePlatformGroupMembers(tenantId, groupId);
  const addMut = useAddPlatformGroupMemberMutation(tenantId, groupId);
  const removeMut = useRemovePlatformGroupMemberMutation(tenantId, groupId);
  const [email, setEmail] = useState("");
  const [errorMsg, setErrorMsg] = useState<unknown>(null);

  const submitAdd = () => {
    setErrorMsg(null);
    addMut.mutate(email.trim(), {
      onSuccess: () => setEmail(""),
      onError: (e) => setErrorMsg(e),
    });
  };

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

          <div className="mb-4">
            <div className="flex gap-2">
              <input
                type="email"
                value={email}
                onChange={(e) => setEmail(e.target.value)}
                placeholder="member@example.com"
                aria-label="Member email to add"
                className="flex-1 px-3 py-2 bg-[var(--bg-elevated)] border border-[var(--border)] rounded text-sm text-[var(--text-primary)]"
              />
              <button
                type="button"
                disabled={!email.trim() || addMut.isPending}
                onClick={submitAdd}
                className="inline-flex items-center gap-1.5 px-3 py-2 text-sm rounded bg-[var(--accent)] text-white disabled:opacity-40"
              >
                {addMut.isPending ? <Loader2 size={14} className="animate-spin" /> : <UserPlus size={14} />}
                Add
              </button>
            </div>
            {(addMut.error || errorMsg !== null) && (
              <p className="text-xs text-[var(--danger)] mt-1.5" role="alert">{safeErrorInfo(addMut.error ?? errorMsg).detail}</p>
            )}
            {addMut.isSuccess && <p className="text-xs text-[var(--success)] mt-1.5" role="status">Member added.</p>}
          </div>

          {isLoading ? (
            <div className="flex items-center justify-center h-40">
              <Loader2 size={24} className="text-[var(--accent)] animate-spin" />
            </div>
          ) : isError ? (
            <div className="border border-[var(--danger)]/30 rounded-xl p-6" role="alert">
              <p className="text-[var(--danger)] text-sm font-medium">{safeErrorInfo(error).title}</p>
              <p className="text-xs text-[var(--text-secondary)] mt-0.5">{safeErrorInfo(error).detail}</p>
              <button
                type="button"
                onClick={() => refetch()}
                className="mt-3 px-3 py-1.5 text-xs rounded border border-[var(--border)] text-[var(--text-primary)] hover:bg-[var(--bg-elevated)]"
              >
                Retry
              </button>
            </div>
          ) : members && members.members.length === 0 ? (
            <p className="text-sm text-[var(--text-secondary)]">This group has no members.</p>
          ) : (
            <ul className="divide-y divide-[var(--bg-subtle)]" aria-label="Group member addresses">
              {(members?.members ?? []).map((m) => (
                <li key={m} className="py-2 flex items-center justify-between gap-2">
                  <span className="text-sm text-[var(--text-primary)] break-all">{m}</span>
                  <button
                    type="button"
                    aria-label={`Remove member ${m}`}
                    disabled={removeMut.isPending}
                    onClick={() =>
                      removeMut.mutate(m, {
                        onError: (e) => setErrorMsg(e),
                      })
                    }
                    className="p-1.5 text-[var(--text-secondary)] hover:text-[var(--danger)] disabled:opacity-40 shrink-0"
                  >
                    <UserMinus size={15} />
                  </button>
                </li>
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
  const [creating, setCreating] = useState(false);
  const [deleting, setDeleting] = useState<number | null>(null);

  const listQ = usePlatformGroups(tenantId, { q: query || undefined, limit: PAGE_SIZE, offset: page * PAGE_SIZE });
  const groups = listQ.data?.groups ?? [];
  const total = listQ.data?.total ?? 0;
  const createMut = useCreatePlatformGroupMutation(tenantId ?? 0);
  const deleteMut = useDeletePlatformGroupMutation(tenantId ?? 0);

  return (
    <div className="space-y-6">
      <div className="flex items-start justify-between gap-4">
        <div>
          <h2 className="text-xl font-semibold text-[var(--text-primary)]">Platform Groups</h2>
          <p className="text-sm text-[var(--text-secondary)]">
            Platform-wide group inventory per tenant with full membership management: create, delete (typed
            confirmation), add and remove members — all audited and tenant-scoped server-side.
          </p>
        </div>
        {tenantId !== null && (
          <button
            type="button"
            onClick={() => setCreating(true)}
            className="inline-flex items-center gap-1.5 px-3 py-1.5 text-sm rounded bg-[var(--accent)] text-white hover:bg-[var(--accent-hover)] shrink-0"
          >
            <Plus size={14} /> Create group
          </button>
        )}
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
                        <th className="p-3 w-32"><span className="sr-only">Actions</span></th>
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
                            <div className="flex items-center gap-1.5">
                              <button
                                type="button"
                                aria-label={`View members of ${g.name}`}
                                onClick={() => setMembersGroupId(g.id)}
                                className="inline-flex items-center gap-1.5 px-2.5 py-1.5 text-xs rounded border border-[var(--border)] text-[var(--text-secondary)] hover:text-[var(--text-primary)]"
                              >
                                <Users size={14} /> Members
                              </button>
                              <button
                                type="button"
                                aria-label={`Delete group ${g.name}`}
                                title="Delete group (typed confirmation required)"
                                onClick={() => setDeleting(g.id)}
                                className="p-1.5 text-[var(--text-secondary)] hover:text-[var(--danger)]"
                              >
                                <Trash2 size={14} />
                              </button>
                            </div>
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

      {creating && tenantId !== null && (
        <CreateGroupDialog
          tenantId={tenantId}
          onClose={() => setCreating(false)}
          onCreate={(body) => {
            createMut.mutate(body, { onSuccess: () => setCreating(false) });
          }}
          pending={createMut.isPending}
          error={createMut.error}
        />
      )}

      <ConfirmDialog
        open={deleting !== null}
        onOpenChange={(o) => !o && setDeleting(null)}
        title="Delete group"
        description={`Group "${groups.find((g) => g.id === deleting)?.name ?? deleting}" will be soft-deleted for tenant ${tenantId}. This is irreversible from the console. Type the confirmation phrase to proceed.`}
        requireTypedName={deleting !== null ? confirmGroupDelete(deleting) : undefined}
        confirmLabel="Delete group"
        danger
        pending={deleteMut.isPending}
        onConfirm={() => {
          if (deleting === null) return;
          deleteMut.mutate(deleting, {
            onSuccess: () => setDeleting(null),
          });
        }}
      />
    </div>
  );
}

function CreateGroupDialog({
  tenantId,
  onClose,
  onCreate,
  pending,
  error,
}: {
  tenantId: number;
  onClose: () => void;
  onCreate: (body: { name: string; description?: string }) => void;
  pending: boolean;
  error: unknown;
}) {
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  return (
    <Dialog.Root open onOpenChange={(o) => { if (!o) onClose(); }}>
      <Dialog.Portal>
        <Dialog.Overlay className="fixed inset-0 bg-black/60 z-40" />
        <Dialog.Content className="fixed left-1/2 top-1/2 -translate-x-1/2 -translate-y-1/2 z-50 w-full max-w-md bg-[var(--bg-surface)] border border-[var(--border)] rounded-xl p-6">
          <div className="flex items-start justify-between mb-4">
            <Dialog.Title className="text-base font-semibold text-[var(--text-primary)]">Create group</Dialog.Title>
            <Dialog.Close aria-label="Close" className="text-[var(--text-secondary)] hover:text-[var(--text-primary)]">
              <X size={18} />
            </Dialog.Close>
          </div>
          {error !== null && (
            <div className="border border-[var(--danger)]/30 rounded-lg p-3 text-sm mb-4" role="alert">
              <p className="text-[var(--danger)] font-medium">{safeErrorInfo(error).title}</p>
              <p className="text-xs text-[var(--text-secondary)] mt-0.5">{safeErrorInfo(error).detail}</p>
            </div>
          )}
          <label className="block text-sm">
            <span className="text-[var(--text-secondary)]">Group name * (tenant {tenantId})</span>
            <input
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="sales-team"
              required
              className="mt-1 w-full px-3 py-2 bg-[var(--bg-elevated)] border border-[var(--border)] rounded text-sm text-[var(--text-primary)]"
            />
          </label>
          <label className="block text-sm mt-3">
            <span className="text-[var(--text-secondary)]">Description</span>
            <input
              value={description}
              onChange={(e) => setDescription(e.target.value)}
              className="mt-1 w-full px-3 py-2 bg-[var(--bg-elevated)] border border-[var(--border)] rounded text-sm text-[var(--text-primary)]"
            />
          </label>
          <div className="flex justify-end gap-2 mt-4">
            <Dialog.Close className="px-3 py-2 text-sm text-[var(--text-secondary)] hover:text-[var(--text-primary)]">
              Cancel
            </Dialog.Close>
            <button
              type="button"
              disabled={!name.trim() || pending}
              onClick={() => onCreate({ name: name.trim(), description: description.trim() || undefined })}
              className="inline-flex items-center gap-2 px-3 py-2 text-sm rounded bg-[var(--accent)] text-white disabled:opacity-40"
            >
              {pending && <Loader2 size={14} className="animate-spin" />}
              Create group
            </button>
          </div>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
