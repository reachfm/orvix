import { useState } from "react";
import * as Dialog from "@radix-ui/react-dialog";
import { X, Loader2, AlertCircle, KeyRound, Trash2 } from "lucide-react";
import StatusBadge from "../../components/StatusBadge";
import ConfirmDialog from "../../../../components/ConfirmDialog";
import { usePlatformMailbox } from "../queries";
import {
  useDeleteMailboxMutation,
  useResetMailboxPasswordMutation,
  useSetMailboxAccessModeMutation,
  useSetMailboxQuotaMutation,
  useSetMailboxStatusMutation,
} from "../mutations";
import { allowedMailboxTransitions, formatBytes, mailAccessModeLabel, mailboxStatusLabel, mailboxStatusTone } from "../formatters";
import { mailboxPurgeConfirmation, MAILBOX_ACCESS_MODE_OPTIONS, type MailAccessMode } from "../contract";
import PasswordResetDialog from "./PasswordResetDialog";
import { safeErrorInfo, errorCodeOf } from "../../errors";

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div>
      <dt className="text-xs font-medium text-[var(--text-secondary)]">{label}</dt>
      <dd className="text-sm text-[var(--text-primary)] mt-0.5">{children}</dd>
    </div>
  );
}

export default function MailboxDetailDrawer({
  tenantId,
  id,
  onClose,
}: {
  tenantId: number;
  id: number;
  onClose: () => void;
}) {
  const { data: mailbox, isLoading, isError, error } = usePlatformMailbox(tenantId, id);
  const statusMut = useSetMailboxStatusMutation(tenantId);
  const quotaMut = useSetMailboxQuotaMutation(tenantId);
  const deleteMut = useDeleteMailboxMutation(tenantId);
  const accessModeMut = useSetMailboxAccessModeMutation(tenantId);

  const [statusDraft, setStatusDraft] = useState("");
  const [quotaDraft, setQuotaDraft] = useState("");
  const [accessModeDraft, setAccessModeDraft] = useState<MailAccessMode | "">("");
  const [accessModeConflict, setAccessModeConflict] = useState(false);
  const [showPasswordReset, setShowPasswordReset] = useState(false);
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [mutationError, setMutationError] = useState<unknown>(null);

  const submitting = statusMut.isPending || quotaMut.isPending || deleteMut.isPending || accessModeMut.isPending;
  const transitions = mailbox ? allowedMailboxTransitions(mailbox.status) : [];

  return (
    <Dialog.Root open onOpenChange={(o) => !o && onClose()}>
      <Dialog.Portal>
        <Dialog.Overlay className="fixed inset-0 bg-black/60 z-40" />
        <Dialog.Content className="fixed right-0 top-0 h-full w-full max-w-xl bg-[var(--bg-surface)] border-l border-[var(--border)] z-50 overflow-y-auto p-6">
          <div className="flex items-start justify-between mb-4">
            <Dialog.Title className="text-lg font-semibold text-[var(--text-primary)]">
              {mailbox ? mailbox.email : "Mailbox detail"}
            </Dialog.Title>
            <Dialog.Close className="text-[var(--text-secondary)] hover:text-[var(--text-primary)]">
              <X size={20} />
            </Dialog.Close>
          </div>

          {isLoading ? (
            <div className="flex items-center justify-center h-40">
              <Loader2 size={24} className="text-[var(--accent)] animate-spin" />
            </div>
          ) : isError || !mailbox ? (
            <div className="border border-[var(--danger)]/30 rounded-xl p-6 flex items-center gap-3" role="alert">
              <AlertCircle size={20} className="text-[var(--danger)]" />
              <div>
                <p className="text-[var(--danger)] text-sm font-medium">Failed to load mailbox</p>
                <p className="text-xs text-[var(--text-secondary)] mt-0.5">{safeErrorInfo(error).detail}</p>
              </div>
            </div>
          ) : (
            <div className="space-y-6">
              <dl className="grid grid-cols-2 gap-4">
                <Field label="Tenant">#{mailbox.tenant_id}</Field>
                <Field label="Status">
                  <StatusBadge tone={mailboxStatusTone(mailbox.status)} label={`Status ${mailboxStatusLabel(mailbox.status)}`}>
                    {mailboxStatusLabel(mailbox.status)}
                  </StatusBadge>
                </Field>
                <Field label="Domain">{mailbox.domain || `#${mailbox.domain_id}`}</Field>
                <Field label="Name">{mailbox.name || "—"}</Field>
                <Field label="Admin">{mailbox.is_admin ? "yes" : "no"}</Field>
                <Field label="Quota">{mailbox.quota_mb > 0 ? `${mailbox.quota_mb} MB` : "unlimited"}</Field>
                <Field label="Used">{formatBytes(mailbox.used_bytes)}</Field>
                <Field label="Created">{new Date(mailbox.created_at).toLocaleString()}</Field>
                <Field label="Configured mail access">{mailAccessModeLabel(mailbox.mail_access_mode)}</Field>
                <Field label="Effective mail access">{mailAccessModeLabel(mailbox.effective_mail_access_mode)}</Field>
              </dl>

              {mailbox.status !== "deleted" && (
                <>
                  {/* Lifecycle status — valid transitions only */}
                  <section aria-label="Mailbox status" className="border border-[var(--border)] rounded-lg p-4">
                    <h3 className="text-sm font-semibold text-[var(--text-primary)] mb-2">Lifecycle status</h3>
                    {transitions.length === 0 ? (
                      <p className="text-xs text-[var(--text-secondary)]">No status transition is allowed for this state.</p>
                    ) : (
                      <div className="flex flex-wrap items-center gap-2">
                        <select
                          aria-label="New mailbox status"
                          value={statusDraft}
                          onChange={(e) => setStatusDraft(e.target.value)}
                          className="px-3 py-1.5 bg-[var(--bg-elevated)] border border-[var(--border)] rounded text-sm text-[var(--text-primary)]"
                        >
                          <option value="">— Choose status —</option>
                          {transitions.map((s) => (
                            <option key={s} value={s}>{mailboxStatusLabel(s)}</option>
                          ))}
                        </select>
                        <button
                          type="button"
                          disabled={!statusDraft || submitting}
                          onClick={() =>
                            statusMut.mutate({ id: mailbox.id, status: statusDraft }, {
                              onSuccess: () => setStatusDraft(""),
                              onError: (e) => setMutationError(e),
                            })
                          }
                          className="px-3 py-1.5 text-sm rounded bg-[var(--accent)] text-white disabled:opacity-40"
                        >
                          {statusMut.isPending ? "Saving…" : "Apply status"}
                        </button>
                      </div>
                    )}
                  </section>

                  {/* Quota update */}
                  <section aria-label="Mailbox quota" className="border border-[var(--border)] rounded-lg p-4">
                    <h3 className="text-sm font-semibold text-[var(--text-primary)] mb-2">Quota</h3>
                    <div className="flex flex-wrap items-center gap-2">
                      <input
                        type="number"
                        min={1}
                        value={quotaDraft}
                        onChange={(e) => setQuotaDraft(e.target.value)}
                        placeholder={mailbox.quota_mb > 0 ? `${mailbox.quota_mb} MB` : "MB (unlimited currently)"}
                        aria-label="New quota in MB"
                        className="px-3 py-1.5 bg-[var(--bg-elevated)] border border-[var(--border)] rounded text-sm text-[var(--text-primary)]"
                      />
                      <button
                        type="button"
                        disabled={!quotaDraft || Number(quotaDraft) <= 0 || submitting}
                        onClick={() =>
                          quotaMut.mutate({ id: mailbox.id, quotaMb: Math.round(Number(quotaDraft)) }, {
                            onSuccess: () => setQuotaDraft(""),
                            onError: (e) => setMutationError(e),
                          })
                        }
                        className="px-3 py-1.5 text-sm rounded bg-[var(--accent)] text-white disabled:opacity-40"
                      >
                        {quotaMut.isPending ? "Saving…" : "Set quota"}
                      </button>
                    </div>
                  </section>

                  {/* Mail-access policy — guarded by real optimistic concurrency */}
                  <section aria-label="Mail access policy" className="border border-[var(--border)] rounded-lg p-4">
                    <h3 className="text-sm font-semibold text-[var(--text-primary)] mb-2">Mail access policy</h3>
                    <p className="text-xs text-[var(--text-secondary)] mb-2">
                      Configured: {mailAccessModeLabel(mailbox.mail_access_mode)} · Effective: {mailAccessModeLabel(mailbox.effective_mail_access_mode)}
                    </p>
                    {accessModeConflict && (
                      <p className="text-xs text-[var(--warning)] mb-2" role="alert">
                        This mailbox changed elsewhere since it was last read. The record has been refreshed — review the
                        current policy before trying again.
                      </p>
                    )}
                    <div className="flex flex-wrap items-center gap-2">
                      <select
                        aria-label="New mail access mode"
                        value={accessModeDraft}
                        onChange={(e) => setAccessModeDraft(e.target.value as MailAccessMode)}
                        className="px-3 py-1.5 bg-[var(--bg-elevated)] border border-[var(--border)] rounded text-sm text-[var(--text-primary)]"
                      >
                        <option value="">— Choose mode —</option>
                        {MAILBOX_ACCESS_MODE_OPTIONS.map((o) => (
                          <option key={o.value} value={o.value}>{o.label}</option>
                        ))}
                      </select>
                      <button
                        type="button"
                        disabled={!accessModeDraft || submitting}
                        onClick={() =>
                          accessModeMut.mutate(
                            { id: mailbox.id, mailAccessMode: accessModeDraft as MailAccessMode, expectedVersion: mailbox.version, idempotencyKey: crypto.randomUUID() },
                            {
                              onSuccess: () => { setAccessModeDraft(""); setAccessModeConflict(false); },
                              onError: (e) => {
                                setMutationError(e);
                                if (errorCodeOf(e) === "PRECONDITION_FAILED") setAccessModeConflict(true);
                              },
                            },
                          )
                        }
                        className="px-3 py-1.5 text-sm rounded bg-[var(--accent)] text-white disabled:opacity-40"
                      >
                        {accessModeMut.isPending ? "Saving…" : "Apply mode"}
                      </button>
                    </div>
                  </section>

                  {/* Password reset — one-time credential */}
                  <div className="flex flex-wrap gap-2">
                    <button
                      type="button"
                      onClick={() => setShowPasswordReset(true)}
                      className="inline-flex items-center gap-2 px-3 py-2 text-sm rounded border border-[var(--border)] text-[var(--text-secondary)] hover:text-[var(--text-primary)]"
                    >
                      <KeyRound size={16} /> Reset password
                    </button>
                    <button
                      type="button"
                      onClick={() => setConfirmDelete(true)}
                      className="inline-flex items-center gap-2 px-3 py-2 text-sm rounded border border-[var(--danger)]/40 text-[var(--danger)] hover:bg-[var(--danger)]/5"
                    >
                      <Trash2 size={16} /> Soft-delete mailbox
                    </button>
                  </div>
                </>
              )}

              {mutationError !== null && (
                <div className="border border-[var(--danger)]/30 rounded-lg p-3 text-sm" role="alert">
                  <p className="text-[var(--danger)] font-medium">{safeErrorInfo(mutationError).title}</p>
                  <p className="text-xs text-[var(--text-secondary)] mt-0.5">{safeErrorInfo(mutationError).detail}</p>
                </div>
              )}
            </div>
          )}
        </Dialog.Content>
      </Dialog.Portal>

      {showPasswordReset && mailbox && (
        <PasswordResetDialog
          tenantId={tenantId}
          mailboxId={mailbox.id}
          email={mailbox.email}
          onClose={() => setShowPasswordReset(false)}
        />
      )}

      {confirmDelete && mailbox && (
        <ConfirmDialog
          open
          onOpenChange={(o) => !o && setConfirmDelete(false)}
          title="Soft-delete mailbox"
          description={`Delete ${mailbox.email}? The mailbox is soft-deleted and its data is no longer deliverable. Type the confirmation phrase to proceed.`}
          requireTypedName={mailboxPurgeConfirmation(mailbox.id)}
          confirmLabel="Delete mailbox"
          danger
          pending={deleteMut.isPending}
          onConfirm={() =>
            deleteMut.mutate(
              { id: mailbox.id, confirmation: mailboxPurgeConfirmation(mailbox.id) },
              { onSuccess: () => { setConfirmDelete(false); onClose(); }, onError: (e) => setMutationError(e) },
            )
          }
        />
      )}
    </Dialog.Root>
  );
}
