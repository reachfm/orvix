import { useState } from "react";
import * as Dialog from "@radix-ui/react-dialog";
import { X, Copy, Check, AlertTriangle, Loader2 } from "lucide-react";
import { useResetMailboxPasswordMutation } from "../mutations";
import { safeErrorInfo } from "../../errors";

/**
 * One-time credential dialog. The backend returns the generated
 * password EXACTLY ONCE; this dialog shows it with a copy control and
 * an explicit irreversible dismissal. The password lives only in
 * component state for the dialog's lifetime — never in query cache,
 * local/session storage, logs, or error messages — and is cleared when
 * the dialog closes.
 */
export default function PasswordResetDialog({
  tenantId,
  mailboxId,
  email,
  onClose,
}: {
  tenantId: number;
  mailboxId: number;
  email: string;
  onClose: () => void;
}) {
  const resetMut = useResetMailboxPasswordMutation(tenantId);
  const [generated, setGenerated] = useState<string | null>(null);
  const [copied, setCopied] = useState(false);
  const [error, setError] = useState<unknown>(null);

  const close = () => {
    setGenerated(null);
    setCopied(false);
    onClose();
  };

  return (
    <Dialog.Root open onOpenChange={(o) => { if (!o) close(); }}>
      <Dialog.Portal>
        <Dialog.Overlay className="fixed inset-0 bg-black/60 z-40" />
        <Dialog.Content className="fixed left-1/2 top-1/2 -translate-x-1/2 -translate-y-1/2 z-50 w-full max-w-md bg-[var(--bg-surface)] border border-[var(--border)] rounded-xl p-6">
          <div className="flex items-start justify-between mb-3">
            <Dialog.Title className="text-base font-semibold text-[var(--text-primary)]">Reset password</Dialog.Title>
            <Dialog.Close className="text-[var(--text-secondary)] hover:text-[var(--text-primary)]">
              <X size={18} />
            </Dialog.Close>
          </div>

          {generated ? (
            <div className="space-y-4">
              <div className="border border-[var(--warning)]/40 rounded-lg p-3 text-sm bg-[var(--warning)]/5" role="alert">
                <div className="flex items-center gap-2 text-[var(--warning)] font-medium">
                  <AlertTriangle size={16} /> This password is shown once
                </div>
                <p className="text-xs text-[var(--text-secondary)] mt-1">
                  Copy it now. It will not be shown again and cannot be retrieved later.
                </p>
              </div>
              <div className="flex items-center gap-2">
                <code className="flex-1 px-3 py-2 bg-[var(--bg-elevated)] border border-[var(--border)] rounded text-sm break-all" data-testid="generated-password">
                  {generated}
                </code>
                <button
                  type="button"
                  aria-label="Copy generated password"
                  onClick={() => {
                    void navigator.clipboard?.writeText(generated).catch(() => {});
                    setCopied(true);
                  }}
                  className="p-2 border border-[var(--border)] rounded text-[var(--text-secondary)] hover:text-[var(--text-primary)]"
                >
                  {copied ? <Check size={16} /> : <Copy size={16} />}
                </button>
              </div>
              <div className="flex justify-end gap-2">
                <button
                  type="button"
                  onClick={close}
                  className="px-3 py-2 text-sm rounded bg-[var(--accent)] text-white"
                >
                  I have saved it — dismiss
                </button>
              </div>
            </div>
          ) : (
            <div className="space-y-4">
              <Dialog.Description className="text-sm text-[var(--text-secondary)]">
                Generate a new random password for <span className="text-[var(--text-primary)]">{email}</span>? The
                previous password stops working immediately. The new credential is returned once.
              </Dialog.Description>
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
                  disabled={resetMut.isPending}
                  onClick={() =>
                    resetMut.mutate(mailboxId, {
                      onSuccess: (res) => {
                        if (res.show_once && res.generated_password) setGenerated(res.generated_password);
                        else setError(new Error("The backend did not return a one-time credential."));
                      },
                      onError: (e) => setError(e),
                    })
                  }
                  className="inline-flex items-center gap-2 px-3 py-2 text-sm rounded bg-[var(--accent)] text-white disabled:opacity-40"
                >
                  {resetMut.isPending && <Loader2 size={14} className="animate-spin" />}
                  Generate password
                </button>
              </div>
            </div>
          )}
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
