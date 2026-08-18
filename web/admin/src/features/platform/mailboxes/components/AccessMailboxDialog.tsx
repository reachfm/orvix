import { useState } from "react";
import * as Dialog from "@radix-ui/react-dialog";
import { X, Loader2, KeyRound } from "lucide-react";
import { useStartMailboxSupportViewMutation } from "../mutations";
import { accessMailboxConfirmation, SUPPORT_VIEW_DEFAULT_DURATION_MINUTES, SUPPORT_VIEW_MAX_DURATION_MINUTES, type StartMailboxSupportViewResponse } from "../contract";
import { safeErrorInfo } from "../../errors";

/**
 * Starts an audited, read-only, time-boxed mailbox support-view
 * session. This is deliberately NOT impersonation: the operator stays
 * authenticated as themselves throughout, the mailbox password is
 * never read, requested, or displayed, and no customer session/JWT is
 * ever minted by this flow.
 */
export default function AccessMailboxDialog({
  tenantId,
  mailboxId,
  email,
  onClose,
  onStarted,
}: {
  tenantId: number;
  mailboxId: number;
  email: string;
  onClose: () => void;
  onStarted: (session: StartMailboxSupportViewResponse) => void;
}) {
  const startMut = useStartMailboxSupportViewMutation(tenantId);
  const [ticketRef, setTicketRef] = useState("");
  const [reason, setReason] = useState("");
  const [duration, setDuration] = useState(SUPPORT_VIEW_DEFAULT_DURATION_MINUTES);
  const [confirmText, setConfirmText] = useState("");
  const [error, setError] = useState<unknown>(null);

  const confirmPhrase = accessMailboxConfirmation(mailboxId);
  const canSubmit = ticketRef.trim() !== "" && reason.trim() !== "" && confirmText === confirmPhrase && !startMut.isPending;

  return (
    <Dialog.Root open onOpenChange={(o) => !o && onClose()}>
      <Dialog.Portal>
        <Dialog.Overlay className="fixed inset-0 bg-black/60 z-40" />
        <Dialog.Content className="fixed left-1/2 top-1/2 -translate-x-1/2 -translate-y-1/2 z-50 w-full max-w-md bg-[var(--bg-surface)] border border-[var(--border)] rounded-xl p-6">
          <div className="flex items-start justify-between mb-3">
            <Dialog.Title className="text-base font-semibold text-[var(--text-primary)] flex items-center gap-2">
              <KeyRound size={18} className="text-[var(--accent)]" /> Access customer mailbox
            </Dialog.Title>
            <Dialog.Close className="text-[var(--text-secondary)] hover:text-[var(--text-primary)]">
              <X size={18} />
            </Dialog.Close>
          </div>
          <Dialog.Description className="text-sm text-[var(--text-secondary)] mb-4">
            Starts an audited, read-only support session for <span className="text-[var(--text-primary)]">{email}</span>.
            You remain signed in as yourself — this never reveals or resets the customer's password and never signs you
            in as the customer.
          </Dialog.Description>

          <div className="space-y-3">
            <label className="block text-xs text-[var(--text-secondary)]" htmlFor="msv-ticket-ref">
              Ticket / reference
              <input
                id="msv-ticket-ref"
                value={ticketRef}
                onChange={(e) => setTicketRef(e.target.value)}
                placeholder="e.g. SUP-1234"
                className="mt-1 w-full px-3 py-2 bg-[var(--bg-elevated)] border border-[var(--border)] rounded text-sm text-[var(--text-primary)]"
              />
            </label>
            <label className="block text-xs text-[var(--text-secondary)]" htmlFor="msv-reason">
              Reason
              <textarea
                id="msv-reason"
                value={reason}
                onChange={(e) => setReason(e.target.value)}
                rows={2}
                placeholder="Why do you need to view this mailbox?"
                className="mt-1 w-full px-3 py-2 bg-[var(--bg-elevated)] border border-[var(--border)] rounded text-sm text-[var(--text-primary)]"
              />
            </label>
            <label className="block text-xs text-[var(--text-secondary)]" htmlFor="msv-duration">
              Duration (minutes, max {SUPPORT_VIEW_MAX_DURATION_MINUTES})
              <input
                id="msv-duration"
                type="number"
                min={1}
                max={SUPPORT_VIEW_MAX_DURATION_MINUTES}
                value={duration}
                onChange={(e) => setDuration(Math.min(SUPPORT_VIEW_MAX_DURATION_MINUTES, Math.max(1, Number(e.target.value) || 1)))}
                className="mt-1 w-full px-3 py-2 bg-[var(--bg-elevated)] border border-[var(--border)] rounded text-sm text-[var(--text-primary)]"
              />
            </label>
            <label className="block text-xs text-[var(--text-secondary)]" htmlFor="msv-confirm">
              Type <code className="text-[var(--text-primary)]">{confirmPhrase}</code> to confirm
              <input
                id="msv-confirm"
                value={confirmText}
                onChange={(e) => setConfirmText(e.target.value)}
                className="mt-1 w-full px-3 py-2 bg-[var(--bg-elevated)] border border-[var(--border)] rounded text-sm text-[var(--text-primary)]"
              />
            </label>
          </div>

          {error !== null && (
            <div className="border border-[var(--danger)]/30 rounded-lg p-3 text-sm mt-3" role="alert">
              <p className="text-[var(--danger)] font-medium">{safeErrorInfo(error).title}</p>
              <p className="text-xs text-[var(--text-secondary)] mt-0.5">{safeErrorInfo(error).detail}</p>
            </div>
          )}

          <div className="flex justify-end gap-2 mt-4">
            <Dialog.Close className="px-3 py-2 text-sm text-[var(--text-secondary)] hover:text-[var(--text-primary)]">Cancel</Dialog.Close>
            <button
              type="button"
              disabled={!canSubmit}
              onClick={() =>
                startMut.mutate(
                  { id: mailboxId, body: { ticket_ref: ticketRef.trim(), reason: reason.trim(), duration_minutes: duration, confirm: confirmPhrase } },
                  { onSuccess: (session) => onStarted(session), onError: (e) => setError(e) },
                )
              }
              className="inline-flex items-center gap-2 px-3 py-2 text-sm rounded bg-[var(--accent)] text-white disabled:opacity-40"
            >
              {startMut.isPending && <Loader2 size={14} className="animate-spin" />}
              Start read-only session
            </button>
          </div>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
