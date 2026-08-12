import { useState } from "react";
import * as Dialog from "@radix-ui/react-dialog";
import { X, Loader2, AlertCircle, Plus, Copy, Check, KeyRound, Play, Trash2, Power, PowerOff, Pencil } from "lucide-react";
import type { ReactNode } from "react";
import StatusBadge from "../components/StatusBadge";
import PaginationControls from "../components/PaginationControls";
import ConfirmDialog from "../../../components/ConfirmDialog";
import { usePlatformRelays } from "./queries";
import {
  useCreatePlatformRelayMutation,
  useDeletePlatformRelayMutation,
  useDisablePlatformRelayMutation,
  useEnablePlatformRelayMutation,
  newIdempotencyKey,
  useRotatePlatformRelayCredentialsMutation,
  useTestPlatformRelayMutation,
  useUpdatePlatformRelayMutation,
} from "./mutations";
import {
  lastTestResultLabel,
  relayDeleteConfirmation,
  relayDisableConfirmation,
  relayRotateConfirmation,
  type ConnSecurity,
  type CreatePlatformRelayRequest,
  type PlatformRelay,
  type RelayScope,
  type TLSValidation,
  type UpdatePlatformRelayRequest,
} from "./contract";
import { safeErrorInfo } from "../errors";

import RelayDetailDrawer from "./components/RelayDetailDrawer";

const PAGE_SIZE = 50;

function Field({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div>
      <dt className="text-xs font-medium text-[var(--text-secondary)]">{label}</dt>
      <dd className="text-sm text-[var(--text-primary)] mt-0.5">{children}</dd>
    </div>
  );
}

function SecretValue({ value, onCopied }: { value: string; onCopied: () => void }) {
  const [copied, setCopied] = useState(false);
  return (
    <span className="inline-flex items-center gap-1.5">
      <code className="px-2 py-1 bg-[var(--bg-elevated)] border border-[var(--border)] rounded text-xs break-all">{value}</code>
      <button
        type="button"
        aria-label="Copy one-time secret"
        onClick={() => { void navigator.clipboard?.writeText(value).catch(() => {}); setCopied(true); onCopied(); }}
        className="p-1 border border-[var(--border)] rounded text-[var(--text-secondary)] hover:text-[var(--text-primary)]"
      >
        {copied ? <Check size={14} /> : <Copy size={14} />}
      </button>
    </span>
  );
}

/** One-time generated credential dialog — shown once, never persisted, cleared on close. */
function GeneratedSecretDialog({ secret, onClose }: { secret: string; onClose: () => void }) {
  return (
    <Dialog.Root open onOpenChange={(o) => { if (!o) onClose(); }}>
      <Dialog.Portal>
        <Dialog.Overlay className="fixed inset-0 bg-black/60 z-40" />
        <Dialog.Content className="fixed left-1/2 top-1/2 -translate-x-1/2 -translate-y-1/2 z-50 w-full max-w-md bg-[var(--bg-surface)] border border-[var(--border)] rounded-xl p-6">
          <Dialog.Title className="text-base font-semibold text-[var(--text-primary)] mb-3">Generated credential</Dialog.Title>
          <div className="border border-[var(--warning)]/40 rounded-lg p-3 text-sm bg-[var(--warning)]/5 mb-4" role="alert">
            <p className="text-[var(--warning)] font-medium">Shown once — copy it now</p>
            <p className="text-xs text-[var(--text-secondary)] mt-1">
              The previous credential is unrecoverable. This value will not be shown again and is never stored or logged.
            </p>
          </div>
          <div className="mb-4">
            <SecretValue value={secret} onCopied={() => {}} />
          </div>
          <div className="flex justify-end">
            <button
              type="button"
              onClick={onClose}
              className="px-3 py-2 text-sm rounded bg-[var(--accent)] text-white"
            >
              I have saved it — dismiss
            </button>
          </div>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}

function RelayFormDialog({
  relay,
  onClose,
}: {
  relay?: PlatformRelay;
  onClose: () => void;
}) {
  const createMut = useCreatePlatformRelayMutation();
  const updateMut = useUpdatePlatformRelayMutation();
  const [name, setName] = useState(relay?.name ?? "");
  const [host, setHost] = useState(relay?.host ?? "");
  const [port, setPort] = useState(relay?.port ? String(relay.port) : "587");
  const [scope, setScope] = useState<RelayScope>(relay?.scope ?? "global");
  const [username, setUsername] = useState(relay?.username ?? "");
  const [password, setPassword] = useState("");
  const [connSecurity, setConnSecurity] = useState<ConnSecurity>(relay?.conn_security ?? "starttls");
  const [tlsValidation, setTlsValidation] = useState<TLSValidation>(relay?.tls_validation ?? "strict");
  const [priority, setPriority] = useState(relay?.priority ? String(relay.priority) : "10");
  const [active, setActive] = useState(relay?.active ?? true);
  const [error, setError] = useState<unknown>(null);

  const submitting = createMut.isPending || updateMut.isPending;

  const submit = () => {
    if (relay) {
      const body: UpdatePlatformRelayRequest = { version: relay.version, name, host, port: Number(port), scope, username, conn_security: connSecurity, tls_validation: tlsValidation, priority: Number(priority), active };
      if (password) body.password = password;
      updateMut.mutate(
        { id: relay.id, body, idempotencyKey: newIdempotencyKey() },
        { onSuccess: () => onClose(), onError: (e) => setError(e) },
      );
      return;
    }
    const body: CreatePlatformRelayRequest = { scope, name, host, port: Number(port), username, conn_security: connSecurity, tls_validation: tlsValidation, priority: Number(priority), active };
    if (password) body.password = password;
    createMut.mutate(
      { body, idempotencyKey: newIdempotencyKey() },
      { onSuccess: () => onClose(), onError: (e) => setError(e) },
    );
  };

  return (
    <Dialog.Root open onOpenChange={(o) => !o && onClose()}>
      <Dialog.Portal>
        <Dialog.Overlay className="fixed inset-0 bg-black/60 z-40" />
        <Dialog.Content className="fixed left-1/2 top-1/2 -translate-x-1/2 -translate-y-1/2 z-50 w-full max-w-lg bg-[var(--bg-surface)] border border-[var(--border)] rounded-xl p-6 max-h-[90vh] overflow-y-auto">
          <Dialog.Title className="text-base font-semibold text-[var(--text-primary)] mb-4">
            {relay ? `Edit relay: ${relay.name}` : "Create relay"}
          </Dialog.Title>
          <div className="space-y-3">
            <label className="block text-sm text-[var(--text-secondary)]">
              Name
              <input value={name} onChange={(e) => setName(e.target.value)} className="mt-1 w-full px-3 py-2 bg-[var(--bg-elevated)] border border-[var(--border)] rounded text-sm text-[var(--text-primary)]" />
            </label>
            <label className="block text-sm text-[var(--text-secondary)]">
              Host
              <input value={host} onChange={(e) => setHost(e.target.value)} placeholder="smtp.provider.example" className="mt-1 w-full px-3 py-2 bg-[var(--bg-elevated)] border border-[var(--border)] rounded text-sm text-[var(--text-primary)]" />
            </label>
            <div className="grid grid-cols-2 gap-3">
              <label className="block text-sm text-[var(--text-secondary)]">
                Port
                <input type="number" min={1} max={65535} value={port} onChange={(e) => setPort(e.target.value)} className="mt-1 w-full px-3 py-2 bg-[var(--bg-elevated)] border border-[var(--border)] rounded text-sm text-[var(--text-primary)]" />
              </label>
              <label className="block text-sm text-[var(--text-secondary)]">
                Scope
                <select value={scope} onChange={(e) => setScope(e.target.value as RelayScope)} className="mt-1 w-full px-3 py-2 bg-[var(--bg-elevated)] border border-[var(--border)] rounded text-sm text-[var(--text-primary)]">
                  <option value="global">global</option>
                  <option value="tenant">tenant</option>
                  <option value="domain">domain</option>
                </select>
              </label>
            </div>
            <div className="grid grid-cols-2 gap-3">
              <label className="block text-sm text-[var(--text-secondary)]">
                Connection security
                <select value={connSecurity} onChange={(e) => setConnSecurity(e.target.value as ConnSecurity)} className="mt-1 w-full px-3 py-2 bg-[var(--bg-elevated)] border border-[var(--border)] rounded text-sm text-[var(--text-primary)]">
                  <option value="none">none</option>
                  <option value="starttls">STARTTLS</option>
                  <option value="implicit_tls">Implicit TLS</option>
                </select>
              </label>
              <label className="block text-sm text-[var(--text-secondary)]">
                TLS validation
                <select value={tlsValidation} onChange={(e) => setTlsValidation(e.target.value as TLSValidation)} className="mt-1 w-full px-3 py-2 bg-[var(--bg-elevated)] border border-[var(--border)] rounded text-sm text-[var(--text-primary)]">
                  <option value="strict">strict</option>
                  <option value="opportunistic">opportunistic</option>
                </select>
              </label>
            </div>
            <div className="grid grid-cols-2 gap-3">
              <label className="block text-sm text-[var(--text-secondary)]">
                Priority
                <input type="number" value={priority} onChange={(e) => setPriority(e.target.value)} className="mt-1 w-full px-3 py-2 bg-[var(--bg-elevated)] border border-[var(--border)] rounded text-sm text-[var(--text-primary)]" />
              </label>
              <label className="block text-sm text-[var(--text-secondary)]">
                Enabled
                <select value={active ? "1" : "0"} onChange={(e) => setActive(e.target.value === "1")} className="mt-1 w-full px-3 py-2 bg-[var(--bg-elevated)] border border-[var(--border)] rounded text-sm text-[var(--text-primary)]">
                  <option value="1">yes</option>
                  <option value="0">no</option>
                </select>
              </label>
            </div>
            <label className="block text-sm text-[var(--text-secondary)]">
              Username
              <input value={username} onChange={(e) => setUsername(e.target.value)} autoComplete="off" className="mt-1 w-full px-3 py-2 bg-[var(--bg-elevated)] border border-[var(--border)] rounded text-sm text-[var(--text-primary)]" />
            </label>
            <label className="block text-sm text-[var(--text-secondary)]">
              {relay ? (relay.has_credential ? "New password (leave blank to keep current credential)" : "Password") : "Password"}
              <input
                type="password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                autoComplete="new-password"
                className="mt-1 w-full px-3 py-2 bg-[var(--bg-elevated)] border border-[var(--border)] rounded text-sm text-[var(--text-primary)]"
              />
            </label>
            {relay && (
              <p className="text-xs text-[var(--text-muted)]">
                Existing credentials are never shown. A password entered here is transmitted once over TLS and cleared from
                the form afterwards.
              </p>
            )}
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
                disabled={!name.trim() || !host.trim() || !port || submitting}
                onClick={submit}
                className="px-3 py-2 text-sm rounded bg-[var(--accent)] text-white disabled:opacity-40"
              >
                {submitting ? "Saving…" : relay ? "Save changes" : "Create relay"}
              </button>
            </div>
          </div>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}

export default function RelaysPage() {
  const [page, setPage] = useState(0);
  const [search, setSearch] = useState("");
  const [selectedId, setSelectedId] = useState<number | null>(null);
  const [createOpen, setCreateOpen] = useState(false);
  const [editRelay, setEditRelay] = useState<PlatformRelay | null>(null);
  const [confirmAction, setConfirmAction] = useState<{ id: number; kind: "disable" | "rotate" | "delete" } | null>(null);
  const [generatedSecret, setGeneratedSecret] = useState<string | null>(null);
  const [mutationError, setMutationError] = useState<unknown>(null);

  const listQ = usePlatformRelays({ q: search || undefined, limit: PAGE_SIZE, offset: page * PAGE_SIZE });
  const enableMut = useEnablePlatformRelayMutation();
  const disableMut = useDisablePlatformRelayMutation();
  const rotateMut = useRotatePlatformRelayCredentialsMutation();
  const testMut = useTestPlatformRelayMutation();
  const deleteMut = useDeletePlatformRelayMutation();
  const [testPendingId, setTestPendingId] = useState<number | null>(null);

  const relays = listQ.data?.relays ?? [];
  const total = listQ.data?.total ?? 0;

  const confirmationFor = (kind: "disable" | "rotate" | "delete", id: number) =>
    kind === "disable" ? relayDisableConfirmation(id) : kind === "rotate" ? relayRotateConfirmation(id) : relayDeleteConfirmation(id);

  const confirmLabelFor = (kind: "disable" | "rotate" | "delete") =>
    kind === "disable" ? "Disable relay" : kind === "rotate" ? "Rotate credentials" : "Delete relay";

  const onConfirm = () => {
    if (!confirmAction) return;
    const { id, kind } = confirmAction;
    const confirmation = confirmationFor(kind, id);
    if (kind === "disable") {
      const relay = relays.find((r) => r.id === id);
      if (!relay) return;
      disableMut.mutate({ id, version: relay.version, idempotencyKey: newIdempotencyKey(), confirmation }, { onSuccess: () => setConfirmAction(null), onError: (e) => setMutationError(e) });
    } else if (kind === "rotate") {
      const relay = relays.find((r) => r.id === id);
      if (!relay) return;
      rotateMut.mutate(
        { id, version: relay.version, idempotencyKey: newIdempotencyKey(), confirmation },
        {
          onSuccess: (res) => { setConfirmAction(null); if (res.show_once && res.generated_password) setGeneratedSecret(res.generated_password); },
          onError: (e) => setMutationError(e),
        },
      );
    } else {
      deleteMut.mutate({ id, confirmation }, { onSuccess: () => { setConfirmAction(null); setSelectedId(null); }, onError: (e) => setMutationError(e) });
    }
  };

  const runTest = (id: number) => {
    setTestPendingId(id);
    testMut.mutate(
      { id, idempotencyKey: newIdempotencyKey() },
      { onSettled: () => setTestPendingId(null), onError: (e) => setMutationError(e) },
    );
  };

  const enableRelay = (relay: PlatformRelay) => {
    enableMut.mutate(
      { id: relay.id, version: relay.version, idempotencyKey: newIdempotencyKey() },
      { onError: (e) => setMutationError(e) },
    );
  };

  return (
    <div className="space-y-6">
      <div>
        <h2 className="text-xl font-semibold text-[var(--text-primary)]">Platform Relays</h2>
        <p className="text-sm text-[var(--text-secondary)]">
          Production outbound relay endpoints. Credentials are encrypted at rest, never returned by reads, and rotated
          credentials are shown exactly once. Connectivity tests report safe results only.
        </p>
      </div>

      <div className="flex flex-wrap items-center gap-2">
        <input
          value={search}
          onChange={(e) => { setSearch(e.target.value); setPage(0); }}
          placeholder="Search relay name or host…"
          aria-label="Search relays"
          className="flex-1 max-w-sm px-3 py-2 bg-[var(--bg-elevated)] border border-[var(--border)] rounded text-sm text-[var(--text-primary)]"
        />
        <button
          type="button"
          onClick={() => setCreateOpen(true)}
          className="inline-flex items-center gap-2 px-3 py-2 text-sm rounded bg-[var(--accent)] text-white"
        >
          <Plus size={16} /> Create relay
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
      ) : relays.length === 0 ? (
        <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-xl p-8 text-center text-[var(--text-secondary)] text-sm">
          {search ? "No relays match this search." : "No relays configured."}
        </div>
      ) : (
        <>
          <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-lg overflow-hidden">
            <div className="overflow-x-auto">
              <table className="w-full text-sm" aria-label="Platform relays">
                <thead>
                  <tr className="border-b border-[var(--border)] text-left text-[var(--text-secondary)]">
                    <th className="p-3">Name</th>
                    <th className="p-3">Host:port</th>
                    <th className="p-3">Scope</th>
                    <th className="p-3">Auth</th>
                    <th className="p-3">State</th>
                    <th className="p-3">Last test</th>
                    <th className="p-3">Actions</th>
                  </tr>
                </thead>
                <tbody>
                  {relays.map((r) => (
                    <tr key={r.id} className="border-b border-[var(--bg-subtle)]">
                      <td className="p-3">
                        <button type="button" className="text-[var(--text-primary)] font-medium hover:underline" onClick={() => setSelectedId(r.id)}>
                          {r.name}
                        </button>
                      </td>
                      <td className="p-3 text-[var(--text-secondary)] font-mono text-xs">{r.host}:{r.port}</td>
                      <td className="p-3 text-[var(--text-secondary)]">{r.scope}</td>
                      <td className="p-3 text-[var(--text-secondary)]">{r.has_credential ? "configured" : "none"}</td>
                      <td className="p-3">
                        <StatusBadge tone={r.active ? "success" : "neutral"} label={r.active ? "Enabled" : "Disabled"}>
                          {r.active ? "enabled" : "disabled"}
                        </StatusBadge>
                      </td>
                      <td className="p-3 text-[var(--text-secondary)]">
                        {r.last_test_at ? (
                          <>
                            <span>{lastTestResultLabel(r.last_test_result)}</span>
                            <span className="block text-xs text-[var(--text-muted)]">{new Date(r.last_test_at).toLocaleString()}</span>
                          </>
                        ) : (
                          "never"
                        )}
                      </td>
                      <td className="p-3">
                        <div className="flex items-center gap-1">
                          {r.active ? (
                            <button type="button" aria-label={`Disable ${r.name}`} title="Disable" onClick={() => setConfirmAction({ id: r.id, kind: "disable" })} className="p-1.5 text-[var(--text-secondary)] hover:text-[var(--warning)]">
                              <PowerOff size={15} />
                            </button>
                          ) : (
                            <button type="button" aria-label={`Enable ${r.name}`} title="Enable" onClick={() => enableRelay(r)} className="p-1.5 text-[var(--text-secondary)] hover:text-[var(--success)]">
                              <Power size={15} />
                            </button>
                          )}
                          <button type="button" aria-label={`Test ${r.name}`} title="Test connectivity" onClick={() => runTest(r.id)} className="p-1.5 text-[var(--text-secondary)] hover:text-[var(--accent)]">
                            {testPendingId === r.id ? <Loader2 size={15} className="animate-spin" /> : <Play size={15} />}
                          </button>
                          <button type="button" aria-label={`Edit ${r.name}`} title="Edit" onClick={() => setSelectedId(r.id)} className="p-1.5 text-[var(--text-secondary)] hover:text-[var(--accent)]">
                            <Pencil size={15} />
                          </button>
                          <button type="button" aria-label={`Rotate credentials for ${r.name}`} title="Rotate credentials" onClick={() => setConfirmAction({ id: r.id, kind: "rotate" })} className="p-1.5 text-[var(--text-secondary)] hover:text-[var(--accent)]">
                            <KeyRound size={15} />
                          </button>
                          <button type="button" aria-label={`Delete ${r.name}`} title="Delete" onClick={() => setConfirmAction({ id: r.id, kind: "delete" })} className="p-1.5 text-[var(--text-secondary)] hover:text-[var(--danger)]">
                            <Trash2 size={15} />
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

      {mutationError !== null && (
        <div className="border border-[var(--danger)]/30 rounded-lg p-3 text-sm" role="alert">
          <p className="text-[var(--danger)] font-medium">{safeErrorInfo(mutationError).title}</p>
          <p className="text-xs text-[var(--text-secondary)] mt-0.5">{safeErrorInfo(mutationError).detail}</p>
        </div>
      )}

      {createOpen && <RelayFormDialog onClose={() => setCreateOpen(false)} />}

      {editRelay !== null && <RelayFormDialog relay={editRelay} onClose={() => setEditRelay(null)} />}

      {selectedId !== null && (
        <RelayDetailDrawer
          id={selectedId}
          onClose={() => setSelectedId(null)}
          onEditRequested={() => {
            const relay = relays.find((r) => r.id === selectedId);
            if (relay) setEditRelay(relay);
            else setSelectedId(null);
          }}
        />
      )}

      <ConfirmDialog
        open={confirmAction !== null}
        onOpenChange={(o) => !o && setConfirmAction(null)}
        title={confirmAction ? confirmLabelFor(confirmAction.kind) : ""}
        description={
          confirmAction?.kind === "rotate"
            ? "Rotating replaces the current credential immediately. The previous credential is unrecoverable. A generated replacement is shown once."
            : confirmAction?.kind === "delete"
              ? "Delete this relay endpoint? It will be removed from routing immediately."
              : "Disable this relay endpoint? It is removed from routing until re-enabled."
        }
        requireTypedName={confirmAction ? confirmationFor(confirmAction.kind, confirmAction.id) : undefined}
        confirmLabel={confirmAction ? confirmLabelFor(confirmAction.kind) : ""}
        danger
        pending={disableMut.isPending || rotateMut.isPending || deleteMut.isPending}
        onConfirm={onConfirm}
      />

      {generatedSecret !== null && <GeneratedSecretDialog secret={generatedSecret} onClose={() => setGeneratedSecret(null)} />}
    </div>
  );
}
