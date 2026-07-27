import { AlertTriangle } from "lucide-react";
import Button from "./Button";

interface ErrorBannerProps {
  message: string;
  onRetry?: () => void;
  variant?: "inline" | "full";
}

export default function ErrorBanner({ message, onRetry, variant = "inline" }: ErrorBannerProps) {
  const content = (
    <div className="flex items-center gap-2 text-sm text-[var(--status-danger)]">
      <AlertTriangle size={16} className="shrink-0" />
      <span>{message}</span>
      {onRetry && <Button variant="ghost" size="sm" onClick={onRetry}>Retry</Button>}
    </div>
  );
  if (variant === "full") {
    return <div className="orvix-surface-card p-6">{content}</div>;
  }
  return <div className="rounded-lg border border-[var(--status-danger)]/30 bg-[var(--status-danger)]/10 p-3">{content}</div>;
}
