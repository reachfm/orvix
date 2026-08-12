// Shared semantic status badge for the platform console. Tones are
// expressed through color variables PLUS a text label and an optional
// icon — never color-only — so status remains readable without color
// vision and in both themes.
import type { ReactNode } from "react";

export type StatusTone = "success" | "warning" | "danger" | "neutral" | "info";

const TONE_CLASSES: Record<StatusTone, string> = {
  success: "text-[var(--success)] bg-[var(--success)]/10",
  warning: "text-[var(--warning)] bg-[var(--warning)]/10",
  danger: "text-[var(--danger)] bg-[var(--danger)]/10",
  neutral: "text-[var(--text-secondary)] bg-[var(--bg-subtle)]",
  info: "text-[var(--accent)] bg-[var(--accent)]/10",
};

export default function StatusBadge({
  tone = "neutral",
  children,
  icon,
  label,
}: {
  tone?: StatusTone;
  children: ReactNode;
  icon?: ReactNode;
  label?: string;
}) {
  return (
    <span
      role={label ? "status" : undefined}
      aria-label={label}
      className={`inline-flex items-center gap-1 px-2 py-0.5 rounded text-xs font-medium ${TONE_CLASSES[tone]}`}
    >
      {icon ?? <span aria-hidden="true">●</span>}
      {children}
    </span>
  );
}
