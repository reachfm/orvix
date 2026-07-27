import { useEffect, useRef, useCallback } from "react";
import { X } from "lucide-react";

interface DrawerProps {
  open: boolean;
  onClose: () => void;
  title: string;
  children: React.ReactNode;
}

export default function Drawer({ open, onClose, title, children }: DrawerProps) {
  const panelRef = useRef<HTMLDivElement>(null);

  const handleKeyDown = useCallback((e: KeyboardEvent) => {
    if (e.key === "Escape") onClose();
  }, [onClose]);

  useEffect(() => {
    if (!open) return;
    document.addEventListener("keydown", handleKeyDown);
    return () => document.removeEventListener("keydown", handleKeyDown);
  }, [open, handleKeyDown]);

  if (!open) return null;

  return (
    <>
      <div className="orvix-drawer-backdrop" onClick={onClose} />
      <div ref={panelRef} className="orvix-drawer-panel right-0" role="dialog" aria-modal="true">
        <div className="flex items-center justify-between p-4 border-b border-[var(--border)]">
          <h2 className="text-base font-semibold text-[var(--text-primary)]">{title}</h2>
          <button onClick={onClose} className="orvix-btn orvix-btn-ghost orvix-btn-sm p-1" aria-label="Close drawer">
            <X size={18} />
          </button>
        </div>
        <div className="p-4">{children}</div>
      </div>
    </>
  );
}
