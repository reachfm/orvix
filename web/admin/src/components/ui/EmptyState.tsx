import type { LucideIcon } from "lucide-react";
import { Inbox } from "lucide-react";

interface EmptyStateProps {
  icon?: LucideIcon;
  title: string;
  description?: string;
  action?: { label: string; onClick: () => void };
}

export default function EmptyState({ icon: Icon = Inbox, title, description, action }: EmptyStateProps) {
  return (
    <div className="orvix-empty-state">
      <Icon size={48} className="mx-auto mb-4 opacity-40" />
      <p className="text-base font-medium text-[var(--text-secondary)]">{title}</p>
      {description && <p className="mt-1 text-sm">{description}</p>}
      {action && (
        <button onClick={action.onClick} className="orvix-btn orvix-btn-secondary orvix-btn-sm mt-4">
          {action.label}
        </button>
      )}
    </div>
  );
}
