import { useState } from "react";
import * as Dialog from "@radix-ui/react-dialog";
import { AlertTriangle, X } from "lucide-react";

// ConfirmDialog is the shared confirmation control for every mutating
// platform-console action. Non-destructive mutations use `requireTypedName:
// undefined` (a plain Confirm/Cancel). Destructive mutations (restore,
// delete, retention run, update run/apply, MFA disable, cert delete, ...)
// must pass `requireTypedName` set to the exact resource name/identifier —
// the Confirm button stays disabled until the operator types it verbatim.
export default function ConfirmDialog({
  open,
  onOpenChange,
  title,
  description,
  requireTypedName,
  confirmLabel = "Confirm",
  danger,
  pending,
  onConfirm,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  title: string;
  description: string;
  requireTypedName?: string;
  confirmLabel?: string;
  danger?: boolean;
  pending?: boolean;
  onConfirm: () => void;
}) {
  const [typed, setTyped] = useState("");
  const canConfirm = requireTypedName ? typed === requireTypedName : true;

  return (
    <Dialog.Root open={open} onOpenChange={(o) => { onOpenChange(o); if (!o) setTyped(""); }}>
      <Dialog.Portal>
        <Dialog.Overlay className="fixed inset-0 bg-black/60 z-40" />
        <Dialog.Content className="fixed left-1/2 top-1/2 -translate-x-1/2 -translate-y-1/2 z-50 w-full max-w-md bg-[#13161C] border border-[#2A2F3E] rounded-xl p-6">
          <div className="flex items-start justify-between mb-3">
            <div className="flex items-center gap-2">
              {danger && <AlertTriangle size={18} className="text-[#F87171]" />}
              <Dialog.Title className="text-base font-semibold text-[#E8EAF0]">{title}</Dialog.Title>
            </div>
            <Dialog.Close className="text-[#8B92A8] hover:text-[#E8EAF0]"><X size={18} /></Dialog.Close>
          </div>
          <Dialog.Description className="text-sm text-[#8B92A8] mb-4">{description}</Dialog.Description>
          {requireTypedName && (
            <div className="mb-4">
              <label className="block text-xs text-[#8B92A8] mb-1">
                Type <span className="text-[#E8EAF0] font-mono">{requireTypedName}</span> to confirm
              </label>
              <input
                autoFocus
                value={typed}
                onChange={(e) => setTyped(e.target.value)}
                className="w-full px-3 py-2 bg-[#1A1E26] border border-[#2A2F3E] rounded text-sm text-[#E8EAF0]"
              />
            </div>
          )}
          <div className="flex justify-end gap-2">
            <Dialog.Close className="px-3 py-2 text-sm text-[#8B92A8] hover:text-[#E8EAF0]">Cancel</Dialog.Close>
            <button
              disabled={!canConfirm || pending}
              onClick={onConfirm}
              className={`px-3 py-2 text-sm rounded disabled:opacity-40 ${danger ? "bg-[#F87171] text-black" : "bg-[#4F7CFF] text-white"}`}
            >
              {pending ? "Working…" : confirmLabel}
            </button>
          </div>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
