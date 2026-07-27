import { createContext, useContext, useState, useCallback, useRef } from "react";
import { AlertTriangle, CheckCircle2, Info, XCircle, X } from "lucide-react";
import type { ToastConfig, ToastVariant } from "../../types/ui";

interface ToastContextValue {
  toast: (cfg: { message: string; variant?: ToastVariant; duration?: number }) => void;
}

const ToastCtx = createContext<ToastContextValue>({ toast: () => {} });
export const useToast = () => useContext(ToastCtx);

const icons: Record<ToastVariant, React.ElementType> = {
  success: CheckCircle2, warning: AlertTriangle, danger: XCircle, info: Info,
};
const colors: Record<ToastVariant, string> = {
  success: "var(--status-success)", warning: "var(--status-warning)",
  danger: "var(--status-danger)", info: "var(--status-info)",
};

export function ToastProvider({ children }: { children: React.ReactNode }) {
  const [toasts, setToasts] = useState<ToastConfig[]>([]);
  const counter = useRef(0);

  const addToast = useCallback((cfg: { message: string; variant?: ToastVariant; duration?: number }) => {
    const id = `toast-${++counter.current}`;
    const variant = cfg.variant || "info";
    const duration = cfg.duration ?? 4000;
    const t: ToastConfig = { id, message: cfg.message, variant, duration };
    setToasts((prev) => [...prev.slice(-4), t]);
    if (duration > 0) {
      setTimeout(() => setToasts((prev) => prev.filter((x) => x.id !== id)), duration);
    }
  }, []);

  const remove = (id: string) => setToasts((prev) => prev.filter((x) => x.id !== id));

  return (
    <ToastCtx.Provider value={{ toast: addToast }}>
      {children}
      <div className="orvix-toast-container" role="region" aria-label="Notifications">
        {toasts.map((t) => {
          const Icon = icons[t.variant];
          return (
            <div key={t.id} className="orvix-toast" role="alert" aria-live={t.variant === "danger" ? "assertive" : "polite"}>
              <Icon size={18} style={{ color: colors[t.variant] }} />
              <span className="text-sm text-[var(--text-primary)] flex-1">{t.message}</span>
              <button onClick={() => remove(t.id)} className="p-0.5 text-[var(--text-muted)] hover:text-[var(--text-primary)]">
                <X size={14} />
              </button>
            </div>
          );
        })}
      </div>
    </ToastCtx.Provider>
  );
}
