import { useEffect, useRef, useCallback } from "react";
import { X } from "lucide-react";

interface DialogProps {
  open: boolean;
  onClose: () => void;
  title: string;
  description?: string;
  children?: React.ReactNode;
  footer?: React.ReactNode;
}

export default function Dialog({ open, onClose, title, description, children, footer }: DialogProps) {
  const panelRef = useRef<HTMLDivElement>(null);
  const titleId = "dialog-title";

  const handleKeyDown = useCallback((e: KeyboardEvent) => {
    if (e.key === "Escape") { onClose(); return; }
    if (e.key === "Tab" && panelRef.current) {
      const focusable = panelRef.current.querySelectorAll<HTMLElement>(
        'button, [href], input, select, textarea, [tabindex]:not([tabindex="-1"])'
      );
      if (focusable.length === 0) return;
      const first = focusable[0];
      const last = focusable[focusable.length - 1];
      if (e.shiftKey) {
        if (document.activeElement === first) { e.preventDefault(); last.focus(); }
      } else {
        if (document.activeElement === last) { e.preventDefault(); first.focus(); }
      }
    }
  }, [onClose]);

  useEffect(() => {
    if (!open) return;
    document.addEventListener("keydown", handleKeyDown);
    const timer = setTimeout(() => panelRef.current?.querySelector<HTMLElement>("button, input, select, textarea")?.focus(), 50);
    return () => { document.removeEventListener("keydown", handleKeyDown); clearTimeout(timer); };
  }, [open, handleKeyDown]);

  if (!open) return null;

  return (
    <div className="orvix-dialog-backdrop" onClick={(e) => { if (e.target === e.currentTarget) onClose(); }}>
      <div ref={panelRef} className="orvix-dialog-panel w-full max-w-lg p-6" role="dialog" aria-modal="true" aria-labelledby={titleId}>
        <div className="flex items-start justify-between gap-4 mb-4">
          <div>
            <h2 id={titleId} className="text-lg font-semibold text-[var(--text-primary)]">{title}</h2>
            {description && <p className="mt-1 text-sm text-[var(--text-secondary)]">{description}</p>}
          </div>
          <button onClick={onClose} className="orvix-btn orvix-btn-ghost orvix-btn-sm p-1.5 shrink-0" aria-label="Close dialog">
            <X size={18} />
          </button>
        </div>
        {children && <div className="mb-6">{children}</div>}
        {footer && <div className="flex justify-end gap-3 pt-4 border-t border-[var(--border)]">{footer}</div>}
      </div>
    </div>
  );
}
